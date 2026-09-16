package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
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
		case "0":
			w.Write([]byte(`{"count":3,"next":"http://x/api/bookmarks/?limit=100&offset=2","previous":null,"results":[{"id":1},{"id":2}]}`))
		case "2":
			w.Write([]byte(`{"count":3,"next":null,"previous":"http://x/api/bookmarks/?limit=100","results":[{"id":3}]}`))
		default:
			t.Errorf("unexpected offset %s", r.URL.Query().Get("offset"))
		}
	})
	p, err := listPage(client, "/bookmarks/", map[string][]string{"q": {"!unread"}}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Count != 3 || len(p.Results) != 3 || p.Next != nil || p.Previous != nil {
		t.Errorf("pages not merged: count=%d results=%d next=%v", p.Count, len(p.Results), p.Next)
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
