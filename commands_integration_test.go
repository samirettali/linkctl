package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadCommands(t *testing.T) {
	const saved = `{"id":42,"url":"https://example.com","title":"T","tag_names":[],"favicon_url":"icon","future_field":{"keep":true}}`
	const listing = `{"count":7,"next":"https://links.example.com/?offset=5","previous":null,"results":[` + saved + `]}`
	for _, tc := range []struct {
		name          string
		args          []string
		path, payload string
		full          bool
	}{
		{"list", []string{"bookmark", "list", "words", "--tag", "go", "--unread", "--limit", "2", "--offset", "3"}, "/api/bookmarks/", listing, false},
		{"list full", []string{"bookmark", "list", "--full", "--limit", "2", "--offset", "3"}, "/api/bookmarks/", listing, true},
		{"archive list", []string{"bookmark", "list", "--archived", "--limit", "2", "--offset", "3"}, "/api/bookmarks/archived/", listing, false},
		{"get", []string{"bookmark", "get", "42"}, "/api/bookmarks/42/", saved, false},
		{"get full", []string{"bookmark", "get", "42", "--full"}, "/api/bookmarks/42/", saved, true},
		{"check", []string{"bookmark", "check", "https://example.com?a=1&b=2"}, "/api/bookmarks/check/", `{"bookmark":` + saved + `,"metadata":{"nested":{"keep":true}},"auto_tags":["go"]}`, false},
		{"check full", []string{"bookmark", "check", "https://example.com?a=1&b=2", "--full"}, "/api/bookmarks/check/", `{"bookmark":` + saved + `,"metadata":{"nested":{"keep":true}},"auto_tags":["go"]}`, true},
		{"tags", []string{"tag", "list", "--limit", "2", "--offset", "3"}, "/api/tags/", `{"count":7,"next":null,"previous":null,"results":[{"id":1,"name":"go","date_added":"date"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != tc.path || r.Header.Get("Accept") != "application/json" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if strings.Contains(tc.name, "list") || tc.name == "tags" {
					if r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("offset") != "3" {
						t.Errorf("paging lost: %s", r.URL)
					}
				}
				if tc.name == "list" && r.URL.Query().Get("q") != "words #go !unread" {
					t.Errorf("filter lost: %s", r.URL)
				}
				if strings.HasPrefix(tc.name, "check") && r.URL.Query().Get("url") != "https://example.com?a=1&b=2" {
					t.Errorf("check URL corrupted: %s", r.URL)
				}
				io.WriteString(w, tc.payload)
			}))
			defer server.Close()
			t.Setenv("LINKDING_URL", server.URL)
			t.Setenv("LINKDING_TOKEN", "dummy")
			output, err := captureStdout(t, func() error { return run(tc.args) })
			if err != nil || calls != 1 || !json.Valid([]byte(output)) {
				t.Fatalf("calls=%d err=%v output=%q", calls, err, output)
			}
			if tc.name != "tags" && (strings.Contains(output, "future_field") != tc.full || strings.Contains(output, "favicon_url") != tc.full) {
				t.Errorf("full/trimmed fields wrong: %s", output)
			}
			if strings.HasPrefix(tc.name, "check") && !strings.Contains(output, `"metadata":{"nested":{"keep":true}}`) {
				t.Errorf("metadata not preserved: %s", output)
			}
		})
	}
}

func TestFullModeRejectsMalformedObjects(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		payload string
	}{
		{[]string{"bookmark", "get", "1", "--full"}, `null`},
		{[]string{"bookmark", "get", "1", "--full"}, `{}`},
		{[]string{"bookmark", "check", "https://example.com", "--full"}, `{}`},
		{[]string{"bookmark", "list", "--full"}, `{"count":1,"next":null,"previous":null,"results":[null]}`},
		{[]string{"bookmark", "add", "https://example.com", "--replace", "--full"}, `{}`},
		{[]string{"bookmark", "update", "1", "--title", "new", "--full"}, `null`},
	} {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, tc.payload) })
		t.Setenv("LINKDING_URL", client.baseURL)
		t.Setenv("LINKDING_TOKEN", "dummy")
		output, err := captureStdout(t, func() error { return run(tc.args) })
		if err == nil || output != "" {
			t.Errorf("%v accepted malformed %s: err=%v output=%s", tc.args, tc.payload, err, output)
		}
	}
}

func TestUpdateOnlyExplicitFieldsAndPreservesLiteralValues(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/api/bookmarks/42/" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("wrong request: %s %s", r.Method, r.URL)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["url"] != "https://example.com/new" || body["notes"] != "--" || body["title"] != "" || body["description"] != "new" || body["unread"] != false || body["shared"] != true || len(body) != 6 {
			t.Errorf("wrong mutation: %v", body)
		}
		io.WriteString(w, `{"id":42,"url":"https://example.com/new"}`)
	})
	t.Setenv("LINKDING_URL", client.baseURL)
	t.Setenv("LINKDING_TOKEN", "dummy")
	_, err := captureStdout(t, func() error {
		return runBookmarkUpdate([]string{"--url", "https://example.com/new", "42", "--notes", "--", "--title", "", "--description", "new", "--unread=false", "--no-shared=false"})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMutationsValidateAllArgumentsBeforeRequests(t *testing.T) {
	calls := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("invalid command made an HTTP request") })
	t.Setenv("LINKDING_URL", client.baseURL)
	t.Setenv("LINKDING_TOKEN", "dummy")
	for _, args := range [][]string{
		{"bookmark", "update", "1"}, {"bookmark", "update", "1", "--shared=false", "--no-shared"},
		{"bookmark", "add", "https://example.com", "--unread=false", "--no-unread"},
		{"bookmark", "delete", "1", "0"}, {"bookmark", "archive", "1", "2x"}, {"bookmark", "unarchive", "1", "9223372036854775808"},
		{"bookmark", "list", "--limit", "-1"}, {"bookmark", "list", "--modified-since", "106752d"},
		{"tag", "list", "--offset", "-1"}, {"tag", "list", "extra"},
	} {
		_, err := captureStdout(t, func() error { return run(args) })
		if err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	if calls != 0 {
		t.Errorf("made %d requests", calls)
	}
}

func TestArchiveAndUnarchiveRequests(t *testing.T) {
	for _, action := range []string{"archive", "unarchive"} {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || r.URL.Path != "/api/bookmarks/42/"+action+"/" {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			}
			body, _ := io.ReadAll(r.Body)
			if len(body) != 0 {
				t.Errorf("unexpected body: %s", body)
			}
			w.WriteHeader(204)
		})
		t.Setenv("LINKDING_URL", client.baseURL)
		t.Setenv("LINKDING_TOKEN", "dummy")
		output, err := captureStdout(t, func() error { return run([]string{"bookmark", action, "42"}) })
		if err != nil || !strings.Contains(output, `"`+action+`d":[42]`) {
			t.Errorf("%s failed: %s %v", action, output, err)
		}
	}
}
