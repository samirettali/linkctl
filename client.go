package main

import (
	"bytes"
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
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, notConfigured(fmt.Sprintf("linkding URL %q is invalid: expected http(s)://host", baseURL), err)
	}
	return &linkdingClient{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// readVault returns the base URL and token stored in the rbw entry. It never prompts:
// a locked vault is an error, since pinentry has no terminal to ask on from an agent.
func readVault() (string, string, error) {
	if _, err := exec.LookPath("rbw"); err != nil {
		return "", "", errors.New("rbw is not installed")
	}
	if err := exec.Command("rbw", "unlocked").Run(); err != nil {
		return "", "", errors.New("rbw is locked: run 'rbw unlock'")
	}
	raw, err := exec.Command("rbw", "get", "--raw", rbwEntry).Output()
	if err != nil {
		return "", "", fmt.Errorf("rbw entry %q not found", rbwEntry)
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
		return nil, fmt.Errorf("calling linkding: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading linkding response: %w", err)
	}
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
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
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

const maxErrorDetails = 300

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
