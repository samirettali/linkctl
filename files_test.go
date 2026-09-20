package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadCLI(t *testing.T) {
	payload := []byte("<html>\x00binary\xff</html>")
	input := writeTestFile(t, "snapshot.html", payload)
	for _, single := range []bool{false, true} {
		t.Run(fmt.Sprint(single), func(t *testing.T) {
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				path := "/api/bookmarks/7/assets/upload/"
				if single {
					path = "/api/bookmarks/singlefile/"
				}
				if r.Method != "POST" || r.URL.Path != path || r.Header.Get("Authorization") != "Token dummy-token" || r.ContentLength <= int64(len(payload)) {
					t.Errorf("bad upload %s %s length=%d", r.Method, r.URL, r.ContentLength)
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				defer r.MultipartForm.RemoveAll()
				f, h, err := r.FormFile("file")
				if err != nil {
					t.Error(err)
					return
				}
				defer f.Close()
				got, _ := io.ReadAll(f)
				if !bytes.Equal(got, payload) || h.Filename != "snapshot.html" || !strings.HasPrefix(h.Header.Get("Content-Type"), "text/html") {
					t.Errorf("file %q header %v data %q", h.Filename, h.Header, got)
				}
				if single {
					if r.FormValue("url") != "https://example.com?a=1&b=2" {
						t.Error("missing URL")
					}
					io.WriteString(w, `{"message":"Snapshot uploaded successfully."}`)
				} else {
					if len(r.MultipartForm.Value) != 0 {
						t.Error("extra fields")
					}
					io.WriteString(w, `{"id":4,"bookmark":7,"future":true}`)
				}
			})
			args := []string{"bookmark", "asset", "upload", "7", "--input", input, "--full"}
			if single {
				args = []string{"bookmark", "singlefile", "https://example.com?a=1&b=2", "--input", input}
			}
			status, out, stderr := runCLIProcess(t, c.baseURL, args...)
			if status != 0 || stderr != "" || calls != 1 || (!strings.Contains(out, "future") && !strings.Contains(out, "message")) {
				t.Fatalf("%d %s %s calls=%d", status, out, stderr, calls)
			}
		})
	}
}

func TestFileCommandsErrorsCLI(t *testing.T) {
	input := writeTestFile(t, "data.bin", []byte{0, 1, 255})
	for _, action := range []string{"upload", "download", "singlefile"} {
		t.Run(action, func(t *testing.T) {
			for _, code := range []int{401, 403, 404, 405, 500} {
				t.Run(fmt.Sprint(code), func(t *testing.T) {
					calls := 0
					c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
						calls++
						w.WriteHeader(code)
						io.WriteString(w, `{"detail":"not available"}`)
					})
					output := filepath.Join(t.TempDir(), "new.bin")
					args := []string{"bookmark", "asset", "upload", "7", "--input", input}
					if action == "download" {
						args = []string{"bookmark", "asset", "download", "7", "4", "--output", output}
					}
					if action == "singlefile" {
						args = []string{"bookmark", "singlefile", "https://example.com", "--input", input}
					}
					status, out, stderr := runCLIProcess(t, c.baseURL, args...)
					if status != 1 || out != "" || calls != 1 {
						t.Fatalf("%d %s %s calls=%d", status, out, stderr, calls)
					}
					expected := fmt.Sprintf(`"status":%d`, code)
					if code == 401 {
						expected = `"fix":`
					}
					if !strings.Contains(stderr, expected) {
						t.Error(stderr)
					}
					entries, _ := os.ReadDir(filepath.Dir(output))
					if len(entries) != 0 {
						t.Errorf("download leaked files: %v", entries)
					}
				})
			}
		})
	}
}

func TestUploadMalformedAndCleanup(t *testing.T) {
	input := writeTestFile(t, "data.bin", bytes.Repeat([]byte{0, 255}, 1<<20))
	for _, single := range []bool{false, true} {
		for _, body := range []string{"", `null`, `{}`, `[]`, `not json`, `{"id":-1}`} {
			spool := t.TempDir()
			t.Setenv("TMPDIR", spool)
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); io.WriteString(w, body) })
			t.Setenv("LINKDING_URL", c.baseURL)
			t.Setenv("LINKDING_TOKEN", "fake")
			args := []string{"bookmark", "asset", "upload", "7", "--input", input}
			if single {
				args = []string{"bookmark", "singlefile", "https://example.com", "--input", input}
			}
			out, err := captureStdout(t, func() error { return run(args) })
			if err == nil || out != "" {
				t.Fatalf("single=%t body=%s err=%v out=%s", single, body, err, out)
			}
			entries, _ := os.ReadDir(spool)
			if len(entries) != 0 {
				t.Errorf("spool leaked: %v", entries)
			}
		}
	}
}

func TestDownloadCLI(t *testing.T) {
	payload := []byte{0, 255, 1, 128, '\n'}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/bookmarks/7/assets/4/download/" || r.Method != "GET" || r.Header.Get("Authorization") != "Token dummy-token" {
			t.Errorf("bad download %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="../../ignored.bin"`)
		w.Write(payload)
	})
	output := filepath.Join(t.TempDir(), "chosen.bin")
	status, out, stderr := runCLIProcess(t, c.baseURL, "bookmark", "asset", "download", "7", "4", "--output", output)
	got, err := os.ReadFile(output)
	if status != 0 || stderr != "" || !strings.Contains(out, `"bytes":5`) || err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("%d %s %s file=%v %v", status, out, stderr, got, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(output))
	if len(entries) != 1 {
		t.Errorf("leaked files %v", entries)
	}
	info, _ := os.Stat(output)
	if info.Mode().Perm() != 0600 {
		t.Errorf("permissions %o", info.Mode().Perm())
	}
}

func TestFileSafetyAndCleanup(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid file made a network request") })
	input := writeTestFile(t, "source", []byte("keep"))
	link := filepath.Join(t.TempDir(), "symlink")
	if err := os.Symlink(input, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{input + "-missing", filepath.Dir(input), link, "/dev/null"} {
		if _, err := c.upload("/bookmarks/7/assets/upload/", path, nil); err == nil {
			t.Errorf("accepted upload %s", path)
		}
	}
	for _, output := range []string{input, link, filepath.Dir(input), filepath.Join(input, "child")} {
		if _, err := c.download("/bookmarks/7/assets/4/download/", output); err == nil {
			t.Errorf("accepted output %s", output)
		}
	}
	got, _ := os.ReadFile(input)
	if string(got) != "keep" {
		t.Fatal("overwritten input")
	}
}

func TestDownloadFailuresDoNotPublish(t *testing.T) {
	for _, scenario := range []string{"truncated", "status204", "race", "401"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			output := filepath.Join(dir, "out")
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "truncated":
					w.Header().Set("Content-Length", "100")
					io.WriteString(w, "short")
				case "status204":
					w.WriteHeader(204)
				case "race":
					if err := os.WriteFile(output, []byte("winner"), 0600); err != nil {
						t.Error(err)
					}
					io.WriteString(w, "loser")
				case "401":
					w.WriteHeader(401)
					io.WriteString(w, `{"detail":"bad token"}`)
				}
			})
			_, err := c.download("/bookmarks/7/assets/4/download/", output)
			if err == nil {
				t.Fatal("expected failure")
			}
			entries, _ := os.ReadDir(dir)
			if scenario == "race" {
				got, _ := os.ReadFile(output)
				if string(got) != "winner" || len(entries) != 1 {
					t.Fatalf("overwrote racer: %s %v", got, entries)
				}
			} else if len(entries) != 0 {
				t.Errorf("leaked partial file: %v", entries)
			}
		})
	}
}

func TestTransferRedirectSecurity(t *testing.T) {
	input := writeTestFile(t, "input.txt", []byte("payload"))
	for _, action := range []string{"upload", "download"} {
		for _, scenario := range []string{"cross-origin", "method-change", "malformed", "credentials", "loop"} {
			t.Run(action+"/"+scenario, func(t *testing.T) {
				leaked := false
				other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					leaked = true
					t.Error("redirect contacted unrelated server")
				}))
				defer other.Close()
				var base string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					loc := other.URL + "/target"
					code := 302
					switch scenario {
					case "method-change":
						loc = base + "/changed"
					case "malformed":
						loc = "http://dummy-private-secret:bad%zz@host/"
					case "credentials":
						loc = strings.Replace(base, "http://", "http://dummy-private-secret@", 1) + "/target"
					case "loop":
						loc = base + "/loop"
						code = 307
					}
					w.Header().Set("Location", loc)
					w.WriteHeader(code)
				}))
				defer server.Close()
				base = server.URL
				t.Setenv("LINKDING_URL", base)
				t.Setenv("LINKDING_TOKEN", "fake")
				c, err := newLinkdingClient()
				if err != nil {
					t.Fatal(err)
				}
				if action == "upload" {
					_, err = c.upload("/upload", input, nil)
				} else {
					_, err = c.download("/download", filepath.Join(t.TempDir(), "out"))
				}
				// GET redirects never change method, so this scenario is a bounded loop.
				if err == nil || leaked || strings.Contains(err.Error(), "dummy-private-secret") {
					t.Errorf("unsafe error: %v leaked=%t", err, leaked)
				}
			})
		}
	}
}
