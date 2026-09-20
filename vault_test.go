package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeRBW(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rbw"), []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestVaultFallback(t *testing.T) {
	const entry = `{"data":{"password":"dummy-token","uris":[{"uri":"https://links.example.com/prefix/"}]}}`
	fakeRBW(t, "case \"$*\" in\nunlocked) exit 0;;\n'get --raw linkding-api-key') printf '%s' '"+entry+"';;\n*) exit 2;;\nesac\n")
	for _, tc := range []struct{ url, token, wantURL, wantToken string }{
		{"", "", "https://links.example.com/prefix", "dummy-token"},
		{"https://override.example.com", "", "https://override.example.com", "dummy-token"},
		{"", "override-token", "https://links.example.com/prefix", "override-token"},
	} {
		t.Setenv("LINKDING_URL", tc.url)
		t.Setenv("LINKDING_TOKEN", tc.token)
		client, err := newLinkdingClient()
		if err != nil {
			t.Fatal(err)
		}
		if client.baseURL != tc.wantURL || client.token != tc.wantToken {
			t.Error("wrong configuration precedence")
		}
	}
}

func TestVaultNeverUnlocksOrLeaksOutput(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "unexpected-command")
	fakeRBW(t, "if [ \"$1\" = unlocked ]; then exit 1; fi\n: > '"+marker+"'\n")
	_, _, err := readVault()
	if err == nil || !strings.Contains(err.Error(), "rbw unlock") {
		t.Fatalf("expected locked vault remedy, got %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("locked vault invoked another command")
	}
	fakeRBW(t, "if [ \"$1\" = unlocked ]; then exit 0; fi\nprintf 'dummy-private-token' >&2\nexit 1\n")
	_, _, err = readVault()
	if err == nil || strings.Contains(err.Error(), "dummy-private-token") {
		t.Fatalf("unsafe failure: %v", err)
	}
}

func TestVaultMalformedAndMissingFields(t *testing.T) {
	for _, entry := range []string{`null`, `{}`, `{"data":{"password":"dummy"}}`, `{"data":{"uris":[{"uri":"https://links.example.com"}]}}`, `invalid`} {
		fakeRBW(t, "if [ \"$1\" = unlocked ]; then exit 0; fi\nprintf '%s' '"+entry+"'\n")
		t.Setenv("LINKDING_URL", "")
		t.Setenv("LINKDING_TOKEN", "")
		_, err := newLinkdingClient()
		var auth *authError
		if !errors.As(err, &auth) || auth.Fix == "" {
			t.Errorf("missing configuration did not supply fix for %s: %v", entry, err)
		}
	}
}

func TestVaultCommandTimeout(t *testing.T) {
	for _, script := range []string{
		"exec /bin/sleep 30\n",
		"if [ \"$1\" = unlocked ]; then exit 0; fi\nexec /bin/sleep 30\n",
	} {
		fakeRBW(t, script)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		started := time.Now()
		_, _, err := readVaultContext(ctx)
		cancel()
		if elapsed := time.Since(started); !errors.Is(err, context.DeadlineExceeded) || elapsed > 2*time.Second {
			t.Fatalf("vault did not fail promptly with timeout: elapsed=%s err=%v", elapsed, err)
		}
	}
}

func TestVaultLargeOutputRemainsIntact(t *testing.T) {
	entry, err := json.Marshal(map[string]any{"data": map[string]any{"password": strings.Repeat("x", (4<<20)+1)}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "entry")
	if err := os.WriteFile(path, entry, 0600); err != nil {
		t.Fatal(err)
	}
	fakeRBW(t, "if [ \"$1\" = unlocked ]; then exit 0; fi\nexec /bin/cat '"+path+"'\n")
	_, token, err := readVault()
	if err != nil || token != strings.Repeat("x", (4<<20)+1) {
		t.Errorf("large vault record was rejected or truncated: bytes=%d err=%v", len(token), err)
	}
}
