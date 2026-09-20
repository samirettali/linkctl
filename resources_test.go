package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type resourceCase struct {
	name                  string
	args                  []string
	method, path, payload string
	body                  map[string]any
}

func resourceCases() []resourceCase {
	return []resourceCase{
		{"shared list", []string{"bookmark", "list", "--shared-collection", "--limit", "2", "--offset", "3"}, "GET", "/api/bookmarks/shared/", `{"count":8,"next":null,"previous":null,"results":[{"id":4,"url":"https://example.com"}]}`, nil},
		{"tag list", []string{"tag", "list", "--full", "--limit", "2", "--offset", "3"}, "GET", "/api/tags/", `{"count":8,"next":null,"previous":null,"results":[{"id":4,"name":"go","future":true}]}`, nil},
		{"tag get", []string{"tag", "get", "4", "--full"}, "GET", "/api/tags/4/", `{"id":4,"name":"go","future":true}`, nil},
		{"tag create", []string{"tag", "create", "two words"}, "POST", "/api/tags/", `{"id":4,"name":"two words"}`, map[string]any{"name": "two words"}},
		{"tag delete", []string{"tag", "delete", "4"}, "DELETE", "/api/tags/4/", "", nil},
		{"bundle list", []string{"bundle", "list", "--limit", "2", "--offset", "3"}, "GET", "/api/bundles/", `{"count":8,"next":null,"previous":null,"results":[{"id":4,"name":"work","future":true}]}`, nil},
		{"bundle get", []string{"bundle", "get", "4", "--full"}, "GET", "/api/bundles/4/", `{"id":4,"name":"work","future":true}`, nil},
		{"bundle create", []string{"bundle", "create", "--name", "work", "--search", "words", "--any-tags", "go rust", "--all-tags", "work", "--excluded-tags", "old", "--filter-unread", "yes", "--filter-shared", "no", "--order", "0"}, "POST", "/api/bundles/", `{"id":4}`, map[string]any{"name": "work", "search": "words", "any_tags": "go rust", "all_tags": "work", "excluded_tags": "old", "filter_unread": "yes", "filter_shared": "no", "order": float64(0)}},
		{"bundle update", []string{"bundle", "update", "4", "--search", "", "--any-tags", "", "--all-tags", "", "--excluded-tags", "", "--filter-unread", "off", "--filter-shared", "off", "--order", "-1"}, "PATCH", "/api/bundles/4/", `{"id":4}`, map[string]any{"search": "", "any_tags": "", "all_tags": "", "excluded_tags": "", "filter_unread": "off", "filter_shared": "off", "order": float64(-1)}},
		{"bundle delete", []string{"bundle", "delete", "4"}, "DELETE", "/api/bundles/4/", "", nil},
		{"asset list", []string{"bookmark", "asset", "list", "7", "--limit", "2", "--offset", "3", "--full"}, "GET", "/api/bookmarks/7/assets/", `{"count":8,"next":null,"previous":null,"results":[{"id":4,"bookmark":7,"future":true}]}`, nil},
		{"asset get", []string{"bookmark", "asset", "get", "7", "4", "--full"}, "GET", "/api/bookmarks/7/assets/4/", `{"id":4,"bookmark":7,"future":true}`, nil},
		{"asset delete", []string{"bookmark", "asset", "delete", "7", "4"}, "DELETE", "/api/bookmarks/7/assets/4/", "", nil},
		{"profile", []string{"user", "profile", "--full"}, "GET", "/api/user/profile/", `{"theme":"auto","version":"1.47.0","future":true}`, nil},
	}
}

func TestResourceCLIContracts(t *testing.T) {
	for _, tc := range resourceCases() {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != tc.path || r.Header.Get("Authorization") != "Token dummy-token" {
					t.Errorf("request %s %s auth=%q", r.Method, r.URL, r.Header.Get("Authorization"))
				}
				if strings.Contains(tc.name, "list") && (r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("offset") != "3") {
					t.Errorf("paging %s", r.URL)
				}
				if tc.body != nil {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if !reflect.DeepEqual(body, tc.body) {
						t.Errorf("body=%#v want=%#v", body, tc.body)
					}
				}
				if tc.payload == "" {
					w.WriteHeader(204)
				} else {
					io.WriteString(w, tc.payload)
				}
			}))
			defer server.Close()
			status, out, stderr := runCLIProcess(t, server.URL, tc.args...)
			if status != 0 || stderr != "" || !json.Valid([]byte(out)) || calls != 1 {
				t.Fatalf("status=%d out=%s stderr=%s calls=%d", status, out, stderr, calls)
			}
			if strings.Contains(tc.payload, "future") && !strings.Contains(out, "future") {
				t.Errorf("lost fields: %s", out)
			}
			if tc.method == "DELETE" && !strings.Contains(out, `"deleted":[4]`) {
				t.Errorf("bad delete %s", out)
			}
		})
	}
}

func TestResourceCLIErrors(t *testing.T) {
	for _, tc := range resourceCases() {
		t.Run(tc.name, func(t *testing.T) {
			for _, code := range []int{401, 403, 404, 405, 500} {
				t.Run(fmt.Sprint(code), func(t *testing.T) {
					calls := 0
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						w.WriteHeader(code)
						io.WriteString(w, `{"detail":"unavailable"}`)
					}))
					defer server.Close()
					status, out, stderr := runCLIProcess(t, server.URL, tc.args...)
					if calls != 1 {
						t.Fatalf("unexpected retry: %d", calls)
					}
					if tc.method == "DELETE" && code != 401 {
						if status != 0 || !strings.Contains(out, `"failed":[{"id":4`) || stderr != "" {
							t.Fatalf("%d %s %s", status, out, stderr)
						}
						return
					}
					if status != 1 || out != "" || !json.Valid([]byte(stderr)) {
						t.Fatalf("%d %s %s", status, out, stderr)
					}
					if code == 401 && !strings.Contains(stderr, `"fix":`) {
						t.Errorf("missing fix %s", stderr)
					}
					if code != 401 && !strings.Contains(stderr, fmt.Sprintf(`"status":%d`, code)) {
						t.Errorf("lost status %s", stderr)
					}
				})
			}
		})
	}
}

func TestResourcesMalformedAndPaging(t *testing.T) {
	for _, tc := range resourceCases() {
		if tc.method == "DELETE" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			for _, bad := range []string{"", `null`, `[]`, `{}`, `{"id":0}`, `{"id":"4"}`, `{"theme":null}`, `{"count":1,"next":null,"previous":null,"results":[null]}`} {
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, bad) })
				t.Setenv("LINKDING_URL", c.baseURL)
				t.Setenv("LINKDING_TOKEN", "fake")
				out, err := captureStdout(t, func() error { return run(tc.args) })
				if err == nil || out != "" {
					t.Errorf("accepted %q: %v %s", bad, err, out)
				}
			}
		})
	}
	for _, args := range [][]string{{"tag", "list"}, {"bundle", "list"}, {"bookmark", "asset", "list", "7"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Query().Get("limit") != "100" {
					t.Error("wrong page size")
				}
				switch r.URL.Query().Get("offset") {
				case "2":
					io.WriteString(w, `{"count":4,"next":"https://evil.invalid/steal","previous":null,"results":[{"id":3}]}`)
				case "3":
					io.WriteString(w, `{"count":4,"next":null,"previous":null,"results":[{"id":4}]}`)
				default:
					t.Errorf("wrong offset %s", r.URL)
				}
			})
			t.Setenv("LINKDING_URL", c.baseURL)
			t.Setenv("LINKDING_TOKEN", "fake")
			out, err := captureStdout(t, func() error { return run(append(args, "--limit", "0", "--offset", "2")) })
			if err != nil || calls != 2 || !strings.Contains(out, `"count":4`) || !strings.Contains(out, `"next":null`) {
				t.Fatalf("%v %d %s", err, calls, out)
			}
		})
	}
}

func TestResourceKnownFieldTypes(t *testing.T) {
	for _, tc := range []struct{ kind, data string }{
		{"tag", `{"id":1,"name":{}}`}, {"tag", `{"id":1,"date_added":3}`},
		{"bundle", `{"id":1,"any_tags":[]}`}, {"bundle", `{"id":1,"order":"first"}`},
		{"asset", `{"id":1,"bookmark":"7"}`}, {"asset", `{"id":1,"file_size":{}}`},
		{"profile", `{"theme":"auto","enable_sharing":"yes"}`}, {"profile", `{"theme":"auto","search_preferences":[]}`},
	} {
		if err := validateResource(json.RawMessage(tc.data), tc.kind); err == nil {
			t.Errorf("accepted malformed %s: %s", tc.kind, tc.data)
		}
	}
}

func TestNewCommandValidation(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid input made request") })
	t.Setenv("LINKDING_URL", c.baseURL)
	t.Setenv("LINKDING_TOKEN", "fake")
	for _, args := range [][]string{
		{"tag"}, {"tag", "update", "1"}, {"tag", "create"}, {"tag", "create", ""}, {"tag", "create", "a", "b"}, {"tag", "get", "0"}, {"tag", "delete", "1", "bad"}, {"tag", "list", "--limit", "-1"},
		{"bundle"}, {"bundle", "create"}, {"bundle", "create", "--name", ""}, {"bundle", "create", "extra", "--name", "a"}, {"bundle", "update", "1"}, {"bundle", "update", "1", "--name", " "}, {"bundle", "update", "1", "--filter-unread", "true"}, {"bundle", "update", "1", "--filter-shared", "false"}, {"bundle", "list", "--offset", "-1"},
		{"user"}, {"user", "set"}, {"user", "profile", "extra"},
		{"bookmark", "asset"}, {"bookmark", "asset", "wat"}, {"bookmark", "asset", "list"}, {"bookmark", "asset", "get", "1"}, {"bookmark", "asset", "delete", "1"}, {"bookmark", "asset", "delete", "1", "2", "bad"}, {"bookmark", "asset", "upload", "1"}, {"bookmark", "asset", "download", "1", "2"}, {"bookmark", "asset", "list", "1", "--limit", "-1"},
		{"bookmark", "singlefile", "https://example.com"}, {"bookmark", "singlefile", "--input", "a"},
		{"bookmark", "list", "--shared-collection", "--archived"}, {"bookmark", "list", "--user", "alice"}, {"bookmark", "list", "--bundle", "0"}, {"bookmark", "list", "--bundle", "-1"}, {"bookmark", "list", "--sort", "bad"}, {"bookmark", "list", "--filter-shared", "true"}, {"bookmark", "list", "--unread", "--filter-unread", "no"},
		{"bookmark", "update", "1", "--archived=false", "--no-archived"}, {"bookmark", "update", "1", "--clear-tags", "--tag", "go"}, {"bookmark", "update", "1", "--date-added", "yesterday"},
	} {
		if err := run(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestNewDeleteBatchAndAuth(t *testing.T) {
	for _, args := range [][]string{{"tag", "delete"}, {"bundle", "delete"}, {"bookmark", "asset", "delete", "7"}} {
		for _, auth := range []bool{false, true} {
			var ids []string
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				id := filepath.Base(r.URL.Path)
				ids = append(ids, id)
				if id == "2" {
					if auth {
						w.WriteHeader(401)
					} else {
						w.WriteHeader(404)
					}
					io.WriteString(w, `{"detail":"missing"}`)
				} else {
					w.WriteHeader(204)
				}
			})
			t.Setenv("LINKDING_URL", c.baseURL)
			t.Setenv("LINKDING_TOKEN", "fake")
			out, err := captureStdout(t, func() error { return run(append(args, "1", "2", "3")) })
			if auth {
				if !isAuthError(err) || out != "" || len(ids) != 2 {
					t.Errorf("auth batch: %v %v %s", ids, err, out)
				}
			} else if err != nil || len(ids) != 3 || !strings.Contains(out, `"deleted":[1,3]`) || !strings.Contains(out, `"failed":[{"id":2`) {
				t.Errorf("batch: %v %v %s", ids, err, out)
			}
		}
	}
}

func TestNewBookmarkOptionsCLI(t *testing.T) {
	for _, tc := range []struct {
		args         []string
		method, path string
		query        map[string]string
		body         map[string]any
		payload      string
	}{
		{[]string{"bookmark", "list", "--shared-collection", "--user", "alice", "--bundle", "3", "--sort", "title_asc", "--filter-shared", "yes", "--filter-unread", "no", "words"}, "GET", "/api/bookmarks/shared/", map[string]string{"user": "alice", "bundle": "3", "sort": "title_asc", "shared": "yes", "unread": "no", "q": "words"}, nil, `{"count":1,"next":null,"previous":null,"results":[{"id":1,"favicon_url":"hidden"}]}`},
		{[]string{"bookmark", "check", "https://example.com", "--ignore-cache"}, "GET", "/api/bookmarks/check/", map[string]string{"ignore_cache": "true"}, nil, `{"bookmark":null,"metadata":{},"auto_tags":[]}`},
		{[]string{"bookmark", "update", "1", "--archived=false", "--clear-tags", "--date-added", "2026-01-01T01:00:00+01:00", "--date-modified", "2026-01-02T00:00:00Z"}, "PATCH", "/api/bookmarks/1/", nil, map[string]any{"is_archived": false, "tag_names": []any{}, "date_added": "2026-01-01T00:00:00Z", "date_modified": "2026-01-02T00:00:00Z"}, `{"id":1}`},
		{[]string{"bookmark", "add", "https://example.com", "--replace", "--no-archived=false", "--no-snapshot", "--no-scrape=false"}, "POST", "/api/bookmarks/", map[string]string{"disable_html_snapshot": "true", "disable_scraping": ""}, map[string]any{"url": "https://example.com", "is_archived": true}, `{"id":1}`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("%s %s", r.Method, r.URL)
				}
				for k, v := range tc.query {
					if r.URL.Query().Get(k) != v {
						t.Errorf("query %s", r.URL)
					}
				}
				if tc.body != nil {
					var got map[string]any
					json.NewDecoder(r.Body).Decode(&got)
					if !reflect.DeepEqual(got, tc.body) {
						t.Errorf("body %#v want %#v", got, tc.body)
					}
				}
				io.WriteString(w, tc.payload)
			})
			status, out, stderr := runCLIProcess(t, c.baseURL, tc.args...)
			if status != 0 || stderr != "" || !json.Valid([]byte(out)) {
				t.Fatalf("%d %s %s", status, out, stderr)
			}
		})
	}
}

func TestNewHelpCLI(t *testing.T) {
	for _, args := range [][]string{{"tag", "create"}, {"bundle"}, {"bundle", "update"}, {"user"}, {"user", "profile"}, {"bookmark", "asset"}, {"bookmark", "asset", "download"}, {"bookmark", "singlefile"}} {
		status, out, stderr := runCLIProcess(t, "http://unused.invalid", append(args, "--help")...)
		if status != 0 || out != "" || !strings.Contains(stderr, "Usage:") {
			t.Errorf("%v: %d %s %s", args, status, out, stderr)
		}
	}
}

func writeTestFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
