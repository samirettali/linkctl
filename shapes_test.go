package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const rawBookmarkJSON = `{"id":11309,"url":"https://x.com/p","title":"T","description":"D","notes":"",
"web_archive_snapshot_url":"https://web.archive.org/x","favicon_url":"https://l/f.png",
"preview_image_url":"https://l/p.webp","is_archived":false,"unread":true,"shared":false,
"tag_names":null,"date_added":"2026-09-16T20:32:17Z","date_modified":"2026-09-16T20:32:17Z",
"website_title":null,"website_description":null}`

func TestTrimBookmark(t *testing.T) {
	b, err := decodeBookmark(json.RawMessage(rawBookmarkJSON))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(b)
	for _, dropped := range []string{"favicon_url", "preview_image_url", "web_archive_snapshot_url", "website_title", "website_description", "notes"} {
		if strings.Contains(string(encoded), dropped) {
			t.Errorf("%s should be dropped from the trimmed bookmark: %s", dropped, encoded)
		}
	}
	if !strings.Contains(string(encoded), `"tag_names":[]`) {
		t.Errorf("null tags should be an empty list: %s", encoded)
	}
	if b.ID != 11309 || b.Title != "T" || b.Description != "D" || !b.Unread {
		t.Errorf("unexpected trim: %+v", b)
	}
}

func TestDecodeCheck(t *testing.T) {
	result, err := decodeCheck(json.RawMessage(`{"bookmark":null,"metadata":{"url":"https://e/","title":"E","description":null,"preview_image":null},"auto_tags":null}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if string(encoded) != `{"bookmark":null,"metadata":{"url":"https://e/","title":"E","description":null,"preview_image":null},"auto_tags":[]}` {
		t.Errorf("unexpected check output: %s", encoded)
	}
	result, err = decodeCheck(json.RawMessage(`{"bookmark":` + rawBookmarkJSON + `,"metadata":{},"auto_tags":["x"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Bookmark == nil || result.Bookmark.ID != 11309 || result.AutoTags[0] != "x" {
		t.Errorf("saved bookmark should be trimmed through: %+v", result)
	}
}

func TestTrimTagPage(t *testing.T) {
	p, err := decodePage(json.RawMessage(`{"count":1,"next":null,"previous":null,"results":[{"id":5,"name":"talk","date_added":"2025-03-05T19:38:05Z"}]}`), "tags")
	if err != nil {
		t.Fatal(err)
	}
	tags, err := trimTagPage(p)
	if err != nil {
		t.Fatal(err)
	}
	if tags.Count != 1 || tags.Results[0].Name != "talk" {
		t.Errorf("unexpected tags: %+v", tags)
	}
	if empty, _ := decodePage(json.RawMessage(`{"count":0,"next":null,"previous":null,"results":null}`), "tags"); empty.Results == nil {
		t.Error("null results should decode to an empty list")
	}
}
