package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, f func() error) (string, error) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = file
	defer func() { os.Stdout = old; file.Close() }()
	runErr := f()
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), runErr
}

func TestAuditRejectsRedirects(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PATCH", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			calls := 0
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{"id":1}`)) }))
			defer target.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+"/stolen", http.StatusFound)
			}))
			defer origin.Close()
			t.Setenv("LINKDING_URL", origin.URL)
			t.Setenv("LINKDING_TOKEN", "dummy-token")
			client, err := newLinkdingClient()
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.request(method, "/bookmarks/", nil, map[string]string{"notes": "private"})
			if err == nil || calls != 0 {
				t.Fatalf("redirect followed: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestAuditInvalidConfigDoesNotExposeSecrets(t *testing.T) {
	t.Setenv("LINKDING_TOKEN", "dummy-token")
	for _, value := range []string{"https://user:private-password@example.com", "https://example.com?token=private-password", "https://example.com#private-password", "https://example.com/%xx-private-password"} {
		t.Setenv("LINKDING_URL", value)
		_, err := newLinkdingClient()
		if err == nil {
			t.Errorf("accepted unsafe URL %q", value)
			continue
		}
		var auth *authError
		details := err.Error()
		if errors.As(err, &auth) && auth.Cause != nil {
			details += auth.Cause.Error()
		}
		if strings.Contains(details, "private-password") {
			t.Errorf("configuration error exposes secret: %s", details)
		}
	}
}

func TestAuditMalformedCheckCannotPermitAdd(t *testing.T) {
	for _, payload := range []string{`{}`, `null`, `{"metadata":{}}`, `{"bookmark":{}}`, `{"bookmark":{"id":0}}`} {
		t.Run(payload, func(t *testing.T) {
			posts := 0
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					posts++
					w.Write([]byte(`{"id":1,"url":"https://example.com"}`))
					return
				}
				w.Write([]byte(payload))
			})
			t.Setenv("LINKDING_URL", client.baseURL)
			t.Setenv("LINKDING_TOKEN", "dummy-token")
			_, err := captureStdout(t, func() error { return runBookmarkAdd([]string{"https://example.com"}) })
			if err == nil || posts != 0 {
				t.Errorf("unsafe add: posts=%d err=%v", posts, err)
			}
		})
	}
}

func TestAuditFlagTerminatorAsValue(t *testing.T) {
	fs := newFlagSet("update")
	fields := bindBookmarkFields(fs, true)
	if err := parseFlags(fs, []string{"42", "--notes", "--", "--title", "title", "--", "-literal"}); err != nil {
		t.Fatal(err)
	}
	f, err := fields("update")
	if err != nil {
		t.Fatal(err)
	}
	if f.notes == nil || *f.notes != "--" || f.title == nil || *f.title != "title" || strings.Join(fs.Args(), ",") != "42,-literal" {
		t.Fatalf("fields=%+v args=%v", f, fs.Args())
	}
}

func TestAuditExplicitFalseFields(t *testing.T) {
	for _, name := range []string{"unread", "shared"} {
		for _, negative := range []bool{false, true} {
			fs := newFlagSet("update")
			fields := bindBookmarkFields(fs, true)
			flagName := name
			if negative {
				flagName = "no-" + name
			}
			if err := parseFlags(fs, []string{"42", "--" + flagName + "=false"}); err != nil {
				t.Fatal(err)
			}
			f, err := fields("update")
			if err != nil {
				t.Fatal(err)
			}
			if v, ok := f.body()[name]; !ok || v != negative {
				t.Errorf("--%s=false: body=%v", flagName, f.body())
			}
		}
		fs := newFlagSet("update")
		fields := bindBookmarkFields(fs, true)
		if err := parseFlags(fs, []string{"--" + name + "=false", "--no-" + name}); err != nil {
			t.Fatal(err)
		}
		if _, err := fields("update"); err == nil {
			t.Errorf("opposed %s flags accepted", name)
		}
	}
}

func TestAuditDurationOverflow(t *testing.T) {
	for _, value := range []string{"106752d", "15251w", "9223372036854775807d", "9223372036854775807w"} {
		if d, err := parseDuration(value); err == nil {
			t.Errorf("%s overflow accepted as %s", value, d)
		}
	}
}

func TestAuditMalformedShapes(t *testing.T) {
	for _, value := range []string{`null`, `{}`, `{"detail":"wrong endpoint"}`, `{"count":-1,"results":[]}`, `{"count":0}`} {
		if _, err := decodePage(json.RawMessage(value), "bookmarks"); err == nil {
			t.Errorf("page accepted %s", value)
		}
	}
	for _, value := range []string{`null`, `{}`, `{"id":0}`, `{"id":-1}`} {
		if _, err := decodeBookmark(json.RawMessage(value)); err == nil {
			t.Errorf("bookmark accepted %s", value)
		}
		if _, err := trimTagPage(page{Results: []json.RawMessage{json.RawMessage(value)}}); err == nil {
			t.Errorf("tag accepted %s", value)
		}
	}
}

func TestAuditLargeSuccessRemainsIntact(t *testing.T) {
	payload := `{"id":1,"notes":"` + strings.Repeat("x", (16<<20)+1) + `"}`
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, payload) })
	data, err := client.request("GET", "/bookmarks/1/", nil, nil)
	if err != nil || string(data) != payload {
		t.Fatalf("large successful response was rejected or truncated: bytes=%d err=%v", len(data), err)
	}
}

func TestAuditMalformedSuccess(t *testing.T) {
	for _, payload := range []string{"<html>Login</html>", `{} {}`} {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, payload) })
		if _, err := client.request("GET", "/bookmarks/", nil, nil); err == nil {
			t.Errorf("malformed response accepted: %s", payload)
		}
	}
}

func TestAuditBatchPreservesDocumentedContract(t *testing.T) {
	for _, status := range []int{401, 404} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.WriteHeader(204)
					return
				}
				w.WriteHeader(status)
				io.WriteString(w, `{"detail":"failed"}`)
			})
			t.Setenv("LINKDING_URL", client.baseURL)
			t.Setenv("LINKDING_TOKEN", "dummy-token")
			output, err := captureStdout(t, func() error { return runBookmarkBatch("delete", []string{"1", "2", "3"}) })
			if status == 401 {
				if !isAuthError(err) || calls != 2 || output != "" {
					t.Errorf("auth error contract changed: calls=%d output=%q err=%v", calls, output, err)
				}
				return
			}
			var result struct {
				Deleted []int64          `json:"deleted"`
				Failed  []failedBookmark `json:"failed"`
			}
			if e := json.Unmarshal([]byte(output), &result); e != nil || len(result.Deleted) != 1 || result.Deleted[0] != 1 || len(result.Failed) != 2 || result.Failed[0].ID != 2 {
				t.Errorf("partial results lost: %q, decode=%v", output, e)
			}
			if err != nil || calls != 3 {
				t.Errorf("per-ID errors must continue and remain on stdout: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestAuditPaginationNoProgress(t *testing.T) {
	for _, results := range []string{`[]`, `[{"id":1}]`} {
		calls := 0
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls > 3 {
				w.WriteHeader(500)
				return
			}
			fmt.Fprintf(w, `{"count":1,"next":"https://untrusted.example/steal","previous":null,"results":%s}`, results)
		})
		_, err := listPage(client, "/bookmarks/", nil, 0, 0)
		if err == nil || calls > 1 {
			t.Errorf("inconsistent pagination not rejected promptly: calls=%d err=%v", calls, err)
		}
	}
}

func TestPaginationDoesNotFollowNextAndRejectsRepeatedPage(t *testing.T) {
	foreignCalls := 0
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreignCalls++
		t.Error("untrusted next URL was fetched")
	}))
	defer foreign.Close()
	calls := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 3 {
			w.WriteHeader(500)
			return
		}
		fmt.Fprintf(w, `{"count":1000,"next":%q,"previous":null,"results":[{"id":1}]}`, foreign.URL+"/?offset="+r.URL.Query().Get("offset"))
	})
	_, err := listPage(client, "/bookmarks/", url.Values{"q": {"#go"}}, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "repeated page") || calls != 2 || foreignCalls != 0 {
		t.Errorf("no-progress guard: calls=%d foreign=%d err=%v", calls, foreignCalls, err)
	}
}

func TestAuditVersionRejectsStrays(t *testing.T) {
	for _, args := range [][]string{{"version", "extra"}, {"--version", "--bad"}} {
		_, err := captureStdout(t, func() error { return run(args) })
		if err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}
