package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const rbwEntry = "linkding-api-key"

// APIError is a non-2xx answer from linkding.
type APIError struct {
	Status  int
	Message string
	Details string
}

func (err *APIError) Error() string {
	if err.Details != "" {
		return fmt.Sprintf("%s (status %d): %s", err.Message, err.Status, err.Details)
	}
	return fmt.Sprintf("%s (status %d)", err.Message, err.Status)
}

// authError carries its own remedy so no caller has to probe configuration first.
type authError struct {
	Message string
	Fix     string
	Cause   error
}

func (err *authError) Error() string { return err.Message + ": " + err.Fix }
func (err *authError) Unwrap() error { return err.Cause }

func notConfigured(message string, cause error) error {
	return &authError{
		Message: message,
		Fix:     "export LINKDING_URL and LINKDING_TOKEN, or store the token as the password and the URL as the URI of the rbw entry " + rbwEntry,
		Cause:   cause,
	}
}

type linkdingClient struct {
	baseURL string
	token   string
	http    *http.Client
}

// newLinkdingClient reads LINKDING_URL and LINKDING_TOKEN, falling back to the rbw vault
// when either is missing.
func newLinkdingClient() (*linkdingClient, error) {
	baseURL, token := os.Getenv("LINKDING_URL"), os.Getenv("LINKDING_TOKEN")
	if baseURL == "" || token == "" {
		vaultURL, vaultToken, err := readVault()
		if err != nil {
			return nil, notConfigured("linkding is not configured", err)
		}
		if baseURL == "" {
			baseURL = vaultURL
		}
		if token == "" {
			token = vaultToken
		}
	}
	if baseURL == "" {
		return nil, notConfigured("linkding URL is missing", nil)
	}
	if token == "" {
		return nil, notConfigured("linkding token is missing", nil)
	}
	baseURL = strings.TrimRight(baseURL, "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(baseURL, "#") {
		// Neither the URL nor url.Parse's error is safe to print: either can contain credentials.
		return nil, notConfigured("linkding URL is invalid: expected http(s)://host with an optional path, without user info, query or fragment", nil)
	}
	return &linkdingClient{
		baseURL: baseURL,
		token:   token,
		http: &http.Client{
			Timeout:       60 * time.Second,
			CheckRedirect: checkRedirect,
			Transport:     redirectTransport{http.DefaultTransport},
		},
	}, nil
}

// redirectError contains no URL data: net/http's outer url.Error can include the
// raw Location (including passwords), so request reports only this safe reason.
type redirectError string

func (err redirectError) Error() string { return string(err) }

// net/http parses Location before invoking CheckRedirect and includes the raw
// header in parse errors. Reject malformed locations before that can happen.
type redirectTransport struct{ base http.RoundTripper }

func (t redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	switch resp.StatusCode {
	case 301, 302, 303, 307, 308:
		if location := resp.Header.Get("Location"); location != "" {
			if _, err := req.URL.Parse(location); err != nil {
				resp.Body.Close()
				return nil, redirectError("refusing linkding redirect with an invalid Location header")
			}
		}
	}
	return resp, nil
}

// checkRedirect keeps credentials on their original origin and prevents redirects
// from silently changing a mutation into a successful-looking GET.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return redirectError("stopped after 10 redirects")
	}
	original := via[0]
	if req.URL.Scheme != original.URL.Scheme || !strings.EqualFold(req.URL.Host, original.URL.Host) || req.URL.User != nil {
		return redirectError("refusing linkding redirect to a different origin or URL credentials")
	}
	if req.Method != original.Method {
		return redirectError("refusing linkding redirect that changes the request method")
	}
	return nil
}

// readVault returns the base URL and token stored in the rbw entry. It never prompts:
// a locked vault is an error, since pinentry has no terminal to ask on from an agent.
func readVault() (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return readVaultContext(ctx)
}

func vaultCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "rbw", args...)
	// Bound waiting for inherited output pipes after the command exits, too.
	cmd.WaitDelay = time.Second
	return cmd
}

func readVaultContext(ctx context.Context) (string, string, error) {
	if _, err := exec.LookPath("rbw"); err != nil {
		return "", "", errors.New("rbw is not installed")
	}
	if err := vaultCommand(ctx, "unlocked").Run(); err != nil {
		if ctx.Err() != nil {
			return "", "", fmt.Errorf("checking rbw vault: %w", ctx.Err())
		}
		return "", "", errors.New("rbw is locked: run 'rbw unlock'")
	}
	raw, err := vaultCommand(ctx, "get", "--raw", rbwEntry).Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", "", fmt.Errorf("reading rbw entry: %w", ctx.Err())
		}
		return "", "", fmt.Errorf("rbw entry %q could not be read", rbwEntry)
	}
	var entry struct {
		Data struct {
			Password string `json:"password"`
			URIs     []struct {
				URI string `json:"uri"`
			} `json:"uris"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return "", "", fmt.Errorf("decoding rbw entry %q: %w", rbwEntry, err)
	}
	baseURL := ""
	if len(entry.Data.URIs) > 0 {
		baseURL = entry.Data.URIs[0].URI
	}
	return baseURL, entry.Data.Password, nil
}

// request performs one API call. A 2xx with an empty body returns nil data.
func (client *linkdingClient) request(method, path string, query url.Values, body any) (json.RawMessage, error) {
	endpoint := client.baseURL + "/api" + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+client.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.http.Do(req)
	if err != nil {
		var redirectErr redirectError
		if errors.As(err, &redirectErr) {
			return nil, fmt.Errorf("calling linkding: %w", redirectErr)
		}
		return nil, fmt.Errorf("calling linkding: %w", err)
	}
	defer resp.Body.Close()
	var responseBody io.Reader = resp.Body
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Diagnostics only need a prefix. Successful payloads remain uncapped to
		// preserve large notes and --limit 0's whole-library contract.
		responseBody = io.LimitReader(resp.Body, maxErrorResponseBytes)
	}
	data, readErr := io.ReadAll(responseBody)
	// A truncated error body must not discard the HTTP status or auth remedy.
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, &authError{
			Message: "linkding rejected the token",
			Fix:     "generate a new token in linkding under Settings > Integrations > REST API and update LINKDING_TOKEN or the rbw entry " + rbwEntry,
			Cause:   decodeAPIError(resp.StatusCode, data),
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, decodeAPIError(resp.StatusCode, data)
	}
	if readErr != nil {
		return nil, fmt.Errorf("reading linkding response: %w", readErr)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	if !json.Valid(data) {
		return nil, errors.New("linkding returned invalid JSON")
	}
	return json.RawMessage(data), nil
}

// decodeAPIError flattens the two error bodies Django REST framework produces, {"detail": "..."}
// and per-field {"url": ["..."]}, into one line; anything else (a proxy's 502 page) is kept
// verbatim but truncated.
func decodeAPIError(status int, data []byte) error {
	apiErr := &APIError{Status: status, Message: "linkding request failed"}
	var body map[string]json.RawMessage
	if json.Unmarshal(data, &body) == nil && len(body) > 0 {
		if detail, ok := body["detail"]; ok {
			var message string
			if json.Unmarshal(detail, &message) == nil {
				apiErr.Details = message
				return apiErr
			}
		}
		fields := make([]string, 0, len(body))
		for name, raw := range body {
			var messages []string
			var message string
			switch {
			case json.Unmarshal(raw, &messages) == nil:
				fields = append(fields, name+": "+strings.Join(messages, "; "))
			case json.Unmarshal(raw, &message) == nil:
				fields = append(fields, name+": "+message)
			default:
				fields = append(fields, name+": "+string(raw))
			}
		}
		sort.Strings(fields)
		apiErr.Details = strings.Join(fields, ", ")
		return apiErr
	}
	if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
		if runes := []rune(trimmed); len(runes) > maxErrorDetails {
			trimmed = string(runes[:maxErrorDetails]) + "…"
		}
		apiErr.Details = trimmed
	}
	return apiErr
}

const (
	maxErrorDetails       = 300
	maxErrorResponseBytes = 16 << 10
)

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
