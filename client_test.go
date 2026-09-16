package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) *linkdingClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &linkdingClient{baseURL: server.URL, token: "secret", http: server.Client()}
}

func TestRequestSendsTokenAndDecodesPage(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token secret" {
			t.Errorf("missing token header: %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/api/bookmarks/" || r.URL.Query().Get("q") != "#go" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Write([]byte(`{"count":1,"next":null,"previous":null,"results":[{"id":1,"url":"https://x","title":"T","favicon_url":"f","tag_names":["go"]}]}`))
	})
	data, err := client.request("GET", "/bookmarks/", map[string][]string{"q": {"#go"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := decodePage(data, "bookmarks")
	if err != nil {
		t.Fatal(err)
	}
	trimmed, err := trimBookmarkPage(p)
	if err != nil {
		t.Fatal(err)
	}
	if trimmed.Count != 1 || trimmed.Results[0].Title != "T" || trimmed.Results[0].TagNames[0] != "go" {
		t.Errorf("unexpected decode: %+v", trimmed)
	}
}

func TestRequestErrors(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/unauthorized":
			w.WriteHeader(401)
			w.Write([]byte(`{"detail":"Invalid token."}`))
		case "/api/missing":
			w.WriteHeader(404)
			w.Write([]byte(`{"detail":"No Bookmark matches the given query."}`))
		case "/api/invalid":
			w.WriteHeader(400)
			w.Write([]byte(`{"url":["Enter a valid URL."],"tag_names":["Tags may not contain spaces."]}`))
		case "/api/collision":
			w.WriteHeader(400)
			w.Write([]byte(`{"url":"A bookmark with this URL already exists."}`))
		case "/api/empty":
			w.WriteHeader(204)
		}
	})
	_, err := client.request("GET", "/unauthorized", nil, nil)
	var authErr *authError
	if !errors.As(err, &authErr) || authErr.Fix == "" {
		t.Errorf("401 should be an authError with a fix, got %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || apiErr.Details != "Invalid token." {
		t.Errorf("authError should unwrap to the APIError, got %v", err)
	}
	_, err = client.request("GET", "/missing", nil, nil)
	if !errors.As(err, &apiErr) || apiErr.Status != 404 || apiErr.Details != "No Bookmark matches the given query." {
		t.Errorf("unexpected 404 handling: %v", err)
	}
	_, err = client.request("POST", "/invalid", nil, map[string]any{"url": "x"})
	if !errors.As(err, &apiErr) || apiErr.Status != 400 ||
		apiErr.Details != "tag_names: Tags may not contain spaces., url: Enter a valid URL." {
		t.Errorf("field errors should be flattened and sorted: %v", err)
	}
	_, err = client.request("PATCH", "/collision", nil, map[string]any{"url": "x"})
	if !errors.As(err, &apiErr) || apiErr.Details != "url: A bookmark with this URL already exists." {
		t.Errorf("string-valued field errors should be flattened too: %v", err)
	}
	data, err := client.request("DELETE", "/empty", nil, nil)
	if err != nil || data != nil {
		t.Errorf("204 should be a nil success, got %v %v", data, err)
	}
}

func TestNewClientRejectsBadURL(t *testing.T) {
	t.Setenv("LINKDING_TOKEN", "k")
	for _, bad := range []string{"localhost:9090", "http://", "ftp://x", "links.example.com"} {
		t.Setenv("LINKDING_URL", bad)
		_, err := newLinkdingClient()
		var authErr *authError
		if !errors.As(err, &authErr) {
			t.Errorf("%q should be rejected with a fix, got %v", bad, err)
		}
	}
	t.Setenv("LINKDING_URL", "https://links.example.com/")
	client, err := newLinkdingClient()
	if err != nil || client.baseURL != "https://links.example.com" {
		t.Errorf("valid URL rejected or not trimmed: %v %v", client, err)
	}
}

func TestDecodeAPIErrorNonJSON(t *testing.T) {
	var apiErr *APIError
	err := decodeAPIError(502, []byte("<html>Bad Gateway</html>"))
	if !errors.As(err, &apiErr) || apiErr.Status != 502 || apiErr.Details != "<html>Bad Gateway</html>" {
		t.Errorf("non-JSON body should land in details verbatim: %v", err)
	}
	if err := decodeAPIError(500, nil); !errors.As(err, &apiErr) || apiErr.Details != "" {
		t.Errorf("empty body should leave details empty: %v", err)
	}
	err = decodeAPIError(502, []byte(strings.Repeat("x", 1000)))
	if !errors.As(err, &apiErr) || len([]rune(apiErr.Details)) != maxErrorDetails+1 {
		t.Errorf("details should be truncated to %d chars plus an ellipsis, got %d", maxErrorDetails, len(apiErr.Details))
	}
}

func TestListAllMergesPages(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "100" || r.URL.Query().Get("q") != "!unread" {
			t.Errorf("page requests must keep the filter and ask for %d: %s", pageSize, r.URL)
		}
		switch r.URL.Query().Get("offset") {
		case "5":
			w.Write([]byte(`{"count":8,"next":"http://x/api/bookmarks/?limit=100&offset=7","previous":null,"results":[{"id":1},{"id":2}]}`))
		case "7":
			w.Write([]byte(`{"count":8,"next":null,"previous":"http://x/api/bookmarks/?limit=100","results":[{"id":3}]}`))
		default:
			t.Errorf("unexpected offset %s", r.URL.Query().Get("offset"))
		}
	})
	p, err := listPage(client, "/bookmarks/", map[string][]string{"q": {"!unread"}}, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if p.Count != 8 || len(p.Results) != 3 || p.Next != nil || p.Previous != nil {
		t.Errorf("pages not merged from the offset: count=%d results=%d next=%v", p.Count, len(p.Results), p.Next)
	}
}

func TestAddAndUpdateRequests(t *testing.T) {
	posted := false
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Method != "GET" {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding body: %v", err)
			}
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/bookmarks/check/":
			if r.URL.Query().Get("url") == "https://e/saved" {
				w.Write([]byte(`{"bookmark":{"id":7,"url":"https://e/saved"},"metadata":{},"auto_tags":[]}`))
				return
			}
			w.Write([]byte(`{"bookmark":null,"metadata":{},"auto_tags":[]}`))
			return
		case r.Method == "POST" && r.URL.Path == "/api/bookmarks/":
			if body["url"] == "https://e/saved" {
				posted = true
				break
			}
			if r.URL.Query().Get("disable_scraping") != "true" {
				t.Errorf("--no-scrape must send disable_scraping=true: %s", r.URL)
			}
			if body["url"] != "https://e/p" || body["unread"] != true || len(body) != 3 {
				t.Errorf("add body = %v", body)
			}
			if tags, _ := body["tag_names"].([]any); len(tags) != 1 || tags[0] != "go" {
				t.Errorf("add tags = %v", body["tag_names"])
			}
		case r.Method == "PATCH" && r.URL.Path == "/api/bookmarks/42/":
			if body["notes"] != "n" || len(body) != 1 {
				t.Errorf("update must send only the given fields: %v", body)
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		w.WriteHeader(201)
		w.Write([]byte(`{"id":42,"url":"https://e/p","tag_names":["go"]}`))
	})
	t.Setenv("LINKDING_URL", client.baseURL)
	t.Setenv("LINKDING_TOKEN", "secret")
	quiet, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	stdout := os.Stdout
	os.Stdout = quiet
	t.Cleanup(func() { os.Stdout = stdout; quiet.Close() })
	if err := run([]string{"bookmark", "add", "https://e/p", "--tag", "go", "--unread", "--no-scrape"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"bookmark", "update", "42", "--notes", "n"}); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"bookmark", "add", "https://e/saved"})
	if err == nil || !strings.Contains(err.Error(), "already bookmark 7") || posted {
		t.Errorf("add on a saved URL must refuse before posting, got %v (posted=%v)", err, posted)
	}
	if err := run([]string{"bookmark", "add", "https://e/saved", "--replace"}); err != nil || !posted {
		t.Errorf("--replace must post through, got %v (posted=%v)", err, posted)
	}
}

func TestBatchContinuesPastFailures(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/bookmarks/1/archive/":
			w.WriteHeader(204)
		case r.Method == "POST" && r.URL.Path == "/api/bookmarks/2/archive/":
			w.WriteHeader(404)
			w.Write([]byte(`{"detail":"No Bookmark matches the given query."}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	result, err := batchBookmarks(client, "archive", []int64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Done) != 1 || result.Done[0] != 1 || len(result.Failed) != 1 || result.Failed[0].ID != 2 {
		t.Errorf("expected 1 archived and 2 failed, got %+v", result)
	}
	encoded, _ := result.MarshalJSON()
	if !strings.HasPrefix(string(encoded), `{"archived":[1],"failed":[{"id":2,`) {
		t.Errorf("unexpected JSON: %s", encoded)
	}
}

func TestBatchFailsFastOnAuthError(t *testing.T) {
	calls := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "DELETE" {
			t.Errorf("delete must use DELETE, got %s", r.Method)
		}
		w.WriteHeader(401)
		w.Write([]byte(`{"detail":"Invalid token."}`))
	})
	_, err := batchBookmarks(client, "delete", []int64{1, 2, 3})
	var authErr *authError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected an authError, got %v", err)
	}
	if calls != 1 {
		t.Errorf("should stop at the first 401, made %d calls", calls)
	}
}
