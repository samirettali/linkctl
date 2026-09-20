package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Exercise the real main (including os.Exit and stderr) without building or installing a CLI.
func TestCLIProcess(t *testing.T) {
	if os.Getenv("LINKCTL_TEST_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"linkctl"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func runCLIProcess(t *testing.T, baseURL string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "LINKCTL_TEST_PROCESS=1", "LINKDING_URL="+baseURL, "LINKDING_TOKEN=dummy-token", "PATH="+t.TempDir())
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	status := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		status = exit.ExitCode()
	}
	return status, stdout.String(), stderr.String()
}

func TestCLIExitAndOutputContracts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/bookmarks/1/":
			w.WriteHeader(204)
		case "/api/bookmarks/2/":
			w.WriteHeader(404)
			io.WriteString(w, `{"detail":"missing"}`)
		case "/api/bookmarks/3/":
			w.WriteHeader(401)
			io.WriteString(w, `{"detail":"bad token"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		name                   string
		args                   []string
		status                 int
		stdoutJSON, stderrJSON bool
		contains               string
	}{
		{"version", []string{"version"}, 0, true, false, version},
		{"stray version", []string{"version", "extra"}, 1, false, true, "unexpected argument"},
		{"help", []string{"bookmark", "list", "--help"}, 0, false, false, "Usage:"},
		{"version help", []string{"version", "--help"}, 0, false, false, "Usage:"},
		{"bad flag", []string{"bookmark", "get", "1", "--unknown"}, 1, false, true, "flag provided but not defined"},
		{"terminator", []string{"bookmark", "get", "--", "--help"}, 1, false, true, "invalid ID"},
		{"invalid mutation ID", []string{"bookmark", "delete", "1", "bad"}, 1, false, true, "invalid ID"},
		{"partial batch", []string{"bookmark", "delete", "1", "2"}, 0, true, false, `"failed":[{"id":2`},
		{"auth batch", []string{"bookmark", "delete", "1", "3"}, 1, false, true, `"fix":`},
		{"401", []string{"bookmark", "get", "3"}, 1, false, true, `"fix":`},
		{"404", []string{"bookmark", "get", "2"}, 1, false, true, `"status":404`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, stdout, stderr := runCLIProcess(t, server.URL, tc.args...)
			if status != tc.status || (tc.stdoutJSON && !json.Valid([]byte(stdout))) || (tc.stderrJSON && !json.Valid([]byte(stderr))) || !strings.Contains(stdout+stderr, tc.contains) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if !tc.stdoutJSON && stdout != "" {
				t.Errorf("unexpected stdout: %q", stdout)
			}
			if tc.stdoutJSON && stderr != "" {
				t.Errorf("unexpected stderr: %q", stderr)
			}
		})
	}
}

func TestInvalidConfigurationIsJSONAndRedacted(t *testing.T) {
	status, stdout, stderr := runCLIProcess(t, "https://user:dummy-private-secret@example.com", "bookmark", "get", "1")
	if status != 1 || stdout != "" || !json.Valid([]byte(stderr)) || !strings.Contains(stderr, `"fix":`) || strings.Contains(stderr, "dummy-private-secret") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
}
