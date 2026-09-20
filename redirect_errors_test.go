package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcceptedRedirectTransportErrorCLIIsRedacted(t *testing.T) {
	const secret = "dummy-private-redirect-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, secret) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		w.Header().Set("Location", "/redirect/"+secret+"?token="+secret)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"tag", "get", "1"},
		{"bookmark", "asset", "download", "1", "2", "--output", filepath.Join(t.TempDir(), "asset.bin")},
	} {
		status, stdout, stderr := runCLIProcess(t, server.URL, args...)
		if status != 1 || stdout != "" || !strings.Contains(stderr, "EOF") || strings.Contains(stderr, secret) || strings.Contains(stderr, server.URL) {
			t.Errorf("transport error leaked URL or lost reason: status=%d stdout=%q stderr=%q", status, stdout, stderr)
		}
		if args[0] == "bookmark" {
			entries, err := os.ReadDir(filepath.Dir(args[len(args)-1]))
			if err != nil || len(entries) != 0 {
				t.Errorf("failed redirected download left files: %v %v", entries, err)
			}
		}
	}
}

func TestAcceptedRedirectErrorKeepsUnderlyingIdentity(t *testing.T) {
	const secret = "dummy-private-redirect-secret"
	for _, action := range []string{"json", "download"} {
		t.Run(action, func(t *testing.T) {
			calls := 0
			client := &linkdingClient{baseURL: "http://local.invalid", token: "fake", http: &http.Client{CheckRedirect: checkRedirect, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return &http.Response{StatusCode: 307, Header: http.Header{"Location": {"/" + secret}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				}
				// Include a nested URL wrapper as well as the one Client.Do adds.
				return nil, &url.Error{Op: "Get", URL: r.URL.String(), Err: context.DeadlineExceeded}
			})}}
			var err error
			if action == "json" {
				_, err = client.request("GET", "/tags/1/", nil, nil)
			} else {
				_, err = client.download("/assets/1/download/", filepath.Join(t.TempDir(), "asset"))
			}
			var timeout interface{ Timeout() bool }
			if calls != 2 || !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &timeout) || !timeout.Timeout() || strings.Contains(err.Error(), secret) {
				t.Fatalf("error identity/redaction: calls=%d err=%v", calls, err)
			}
		})
	}
}
