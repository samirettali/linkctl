package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRedirectPolicy(t *testing.T) {
	original, _ := http.NewRequest("GET", "https://links.example.com/api/bookmarks/", nil)
	for _, tc := range []struct {
		name, target, method string
		hops                 int
		allowed              bool
	}{
		{"same origin", "https://links.example.com/other", "GET", 1, true},
		{"case insensitive host", "https://LINKS.example.com/other", "GET", 1, true},
		{"downgrade", "http://links.example.com/other", "GET", 1, false},
		{"other port", "https://links.example.com:8443/other", "GET", 1, false},
		{"subdomain", "https://sub.links.example.com/other", "GET", 1, false},
		{"other host", "https://elsewhere.example/other", "GET", 1, false},
		{"userinfo", "https://user:dummy-secret@links.example.com/other", "GET", 1, false},
		{"method rewrite", "https://links.example.com/other", "POST", 1, false},
		{"loop", "https://links.example.com/other", "GET", 10, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, tc.target, nil)
			via := make([]*http.Request, tc.hops)
			for i := range via {
				via[i] = original
			}
			if err := checkRedirect(req, via); (err == nil) != tc.allowed {
				t.Errorf("allowed=%v err=%v", tc.allowed, err)
			}
		})
	}
}

func TestSameOriginRedirects(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		for _, method := range []string{"GET", "POST", "PATCH", "DELETE"} {
			t.Run(fmt.Sprintf("%d/%s", code, method), func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/start" {
						http.Redirect(w, r, "/final", code)
						return
					}
					calls++
					if r.Method != method || r.Header.Get("Authorization") != "Token dummy-token" {
						t.Errorf("request changed: %s, token present=%v", r.Method, r.Header.Get("Authorization") != "")
					}
					if method != "GET" {
						body, _ := io.ReadAll(r.Body)
						if string(body) != `{"notes":"private"}` {
							t.Errorf("body lost: %q", body)
						}
					}
					io.WriteString(w, `{"id":1}`)
				}))
				defer server.Close()
				t.Setenv("LINKDING_URL", server.URL)
				t.Setenv("LINKDING_TOKEN", "dummy-token")
				client, err := newLinkdingClient()
				if err != nil {
					t.Fatal(err)
				}
				var body any
				if method != "GET" {
					body = map[string]string{"notes": "private"}
				}
				_, err = client.request(method, "/start", nil, body)
				allowed := method == "GET" || code == 307 || code == 308
				if (err == nil) != allowed || (calls == 1) != allowed {
					t.Fatalf("allowed=%v calls=%d err=%v", allowed, calls, err)
				}
			})
		}
	}
}

func TestRedirectErrorsDoNotExposeLocationSecrets(t *testing.T) {
	for _, location := range []string{
		"https://user:dummy-private-secret@example.com/",
		"https://example.com/?token=dummy-private-secret",
		"https://example.com/%xx?token=dummy-private-secret",
	} {
		t.Run(location, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Location", location); w.WriteHeader(302) }))
			defer server.Close()
			t.Setenv("LINKDING_URL", server.URL)
			t.Setenv("LINKDING_TOKEN", "dummy-token")
			client, err := newLinkdingClient()
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.request("GET", "/bookmarks/", nil, nil)
			if err == nil || strings.Contains(err.Error(), "dummy-private-secret") {
				t.Fatalf("unsafe redirect error: %v", err)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type diagnosticBody struct {
	read   int
	closed bool
}

func (b *diagnosticBody) Read(p []byte) (int, error) {
	// Stay bounded even against the buggy implementation that reads the whole body.
	if b.read >= 64*1024 {
		return 0, io.EOF
	}
	for i := range p {
		p[i] = 'x'
	}
	b.read += len(p)
	return len(p), nil
}
func (b *diagnosticBody) Close() error { b.closed = true; return nil }

func TestErrorBodiesAreBoundedAndClosed(t *testing.T) {
	for _, status := range []int{401, 502} {
		body := &diagnosticBody{}
		client := &linkdingClient{baseURL: "https://links.example.com", token: "dummy", http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: body, Header: make(http.Header)}, nil
		})}}
		_, err := client.request("GET", "/bookmarks/", nil, nil)
		var api *APIError
		if !errors.As(err, &api) || api.Status != status || body.read > 16*1024 || !body.closed {
			t.Errorf("status/body contract: read=%d closed=%v err=%v", body.read, body.closed, err)
		}
		if status == 401 && !isAuthError(err) {
			t.Errorf("auth remedy lost: %v", err)
		}
	}
}

func TestTruncatedResponsePreservesStatus(t *testing.T) {
	for _, status := range []int{200, 401, 500} {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(status)
			io.WriteString(w, "partial")
		})
		_, err := client.request("GET", "/bookmarks/", nil, nil)
		if status == 200 {
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("missing read failure: %v", err)
			}
			continue
		}
		var api *APIError
		if !errors.As(err, &api) || api.Status != status || (status == 401 && !isAuthError(err)) {
			t.Errorf("status/remedy lost: %v", err)
		}
	}
}

func TestRequestTimeout(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	client.http.Timeout = 20 * time.Millisecond
	_, err := client.request("GET", "/bookmarks/", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout lost: %v", err)
	}
}
