package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseTime(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{"", "", false},
		{"24h", "2026-09-16T12:00:00Z", false},
		{"7d", "2026-09-10T12:00:00Z", false},
		{"2w", "2026-09-03T12:00:00Z", false},
		{"2026-09-13T10:00:00+02:00", "2026-09-13T08:00:00Z", false},
		{"yesterday", "", true},
		{"-1h", "", true},
		{"d", "", true},
		{"1.5d", "", true},
	}
	for _, tc := range cases {
		got, err := parseTime(tc.in, now)
		if (err != nil) != tc.err {
			t.Fatalf("parseTime(%q) error = %v, want error %v", tc.in, err, tc.err)
		}
		if got != tc.want {
			t.Fatalf("parseTime(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBookmarkListQuery(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	o := bookmarkListOptions{terms: []string{"account", "abstraction"}, tags: []string{"evm", "aa"},
		unread: true, untagged: true, addedSince: "1d", limit: 20}
	q, err := o.query(now)
	if err != nil {
		t.Fatal(err)
	}
	if got := q.Get("q"); got != "account abstraction #evm #aa !unread !untagged" {
		t.Errorf("q = %q", got)
	}
	if q.Get("added_since") != "2026-09-16T12:00:00Z" || q.Has("modified_since") {
		t.Errorf("time filters: %v", q)
	}
	if q.Has("limit") {
		t.Error("limit belongs to listPage, not to the filter query")
	}
	if o.path() != "/bookmarks/" || (bookmarkListOptions{archived: true}).path() != "/bookmarks/archived/" {
		t.Error("archived should switch the path")
	}
	if q, _ := (bookmarkListOptions{}).query(now); q.Has("q") {
		t.Errorf("empty filter should send no q: %v", q)
	}
	for _, bad := range []bookmarkListOptions{{limit: -1}, {offset: -1}, {addedSince: "soon"}, {modifiedSince: "-2d"}} {
		if _, err := bad.query(now); err == nil {
			t.Errorf("expected an error for %+v", bad)
		}
	}
}

func TestBookmarkFields(t *testing.T) {
	fs := newFlagSet("bookmark update")
	fields := bindBookmarkFields(fs, true)
	if err := parseFlags(fs, []string{"42", "--title", "", "--tag", "go", "--tag", "kafka", "--no-unread"}); err != nil {
		t.Fatal(err)
	}
	f, err := fields("bookmark update")
	if err != nil {
		t.Fatal(err)
	}
	body := f.body()
	if title, ok := body["title"]; !ok || title != "" {
		t.Errorf("an explicit empty --title must clear the field: %v", body)
	}
	if tags, ok := body["tag_names"].([]string); !ok || strings.Join(tags, ",") != "go,kafka" {
		t.Errorf("tag_names = %v", body["tag_names"])
	}
	if unread, ok := body["unread"]; !ok || unread != false {
		t.Errorf("--no-unread should send unread=false: %v", body)
	}
	for _, key := range []string{"url", "description", "notes", "shared"} {
		if _, ok := body[key]; ok {
			t.Errorf("%s was not given and must not be sent: %v", key, body)
		}
	}

	fs = newFlagSet("bookmark update")
	fields = bindBookmarkFields(fs, true)
	if err := parseFlags(fs, []string{"42"}); err != nil {
		t.Fatal(err)
	}
	if f, _ := fields("bookmark update"); len(f.body()) != 0 {
		t.Errorf("no flags should give an empty body: %v", f.body())
	}

	fs = newFlagSet("bookmark add")
	fields = bindBookmarkFields(fs, false)
	if err := parseFlags(fs, []string{"--shared", "--no-shared"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fields("bookmark add"); err == nil {
		t.Error("--shared with --no-shared must be rejected")
	}
	if err := parseFlags(newFlagSet("bookmark add"), []string{"--url", "x"}); err == nil {
		t.Error("add must not accept --url, the URL is positional")
	}
}

func TestParseFlagsDoubleDash(t *testing.T) {
	fs := newFlagSet("x")
	full := fs.Bool("full", false, "")
	if err := parseFlags(fs, []string{"--full", "a", "--", "-foo", "--bar", "--"}); err != nil {
		t.Fatal(err)
	}
	if !*full {
		t.Error("--full before -- was ignored")
	}
	if got := strings.Join(fs.Args(), ","); got != "a,-foo,--bar,--" {
		t.Errorf("positionals = %q, want a,-foo,--bar,--", got)
	}
	fs = newFlagSet("x")
	if err := parseFlags(fs, []string{"-foo"}); err == nil {
		t.Error("a dash term without -- must still be an unknown flag")
	}
}

func TestParseFlagsInterspersed(t *testing.T) {
	fs := newFlagSet("x")
	full := fs.Bool("full", false, "")
	if err := parseFlags(fs, []string{"42", "--full", "43"}); err != nil {
		t.Fatal(err)
	}
	if !*full {
		t.Error("--full after a positional was ignored")
	}
	if got := strings.Join(fs.Args(), ","); got != "42,43" {
		t.Errorf("positionals = %q, want 42,43", got)
	}
}

func TestHelpAndStrays(t *testing.T) {
	for _, args := range [][]string{{"bookmark", "--help"}, {"tag", "-h"}, {"bookmark", "help"}, {"bookmark", "list", "-h"}} {
		if err := run(args); !errors.Is(err, errHelp) {
			t.Errorf("%v should surface errHelp, got %v", args, err)
		}
	}
	for _, args := range [][]string{{"tag", "list", "extra"}, {"bookmark", "get"}, {"bookmark", "get", "1", "2"},
		{"bookmark", "get", "x"}, {"bookmark", "check"}, {"bookmark", "add"}, {"bookmark", "nope"}, {"nope"}} {
		if err := run(args); err == nil || errors.Is(err, errHelp) {
			t.Errorf("%v should fail before any request, got %v", args, err)
		}
	}
}

func TestBatchAction(t *testing.T) {
	cases := map[string][3]string{
		"delete":    {"DELETE", "/bookmarks/7/", "deleted"},
		"archive":   {"POST", "/bookmarks/7/archive/", "archived"},
		"unarchive": {"POST", "/bookmarks/7/unarchive/", "unarchived"},
	}
	for action, want := range cases {
		method, path, done := batchAction(action, 7)
		if method != want[0] || path != want[1] || done != want[2] {
			t.Errorf("%s = %s %s %s", action, method, path, done)
		}
	}
}
