package main

import (
	"encoding/json"
	"fmt"
)

// The trimmed shapes keep linkding's own envelope ({"count", "next", "previous", "results"})
// and only slim the objects inside. Field names stay the API's, so a trimmed bookmark is a
// strict subset of the raw one: what goes is the favicon, preview image and web archive URLs
// and the deprecated website_title/website_description, which linkding now folds into title
// and description itself.

type rawBookmark struct {
	ID                    int64    `json:"id"`
	URL                   string   `json:"url"`
	Title                 string   `json:"title"`
	Description           string   `json:"description"`
	Notes                 string   `json:"notes"`
	WebArchiveSnapshotURL string   `json:"web_archive_snapshot_url"`
	FaviconURL            string   `json:"favicon_url"`
	PreviewImageURL       string   `json:"preview_image_url"`
	IsArchived            bool     `json:"is_archived"`
	Unread                bool     `json:"unread"`
	Shared                bool     `json:"shared"`
	TagNames              []string `json:"tag_names"`
	DateAdded             string   `json:"date_added"`
	DateModified          string   `json:"date_modified"`
	WebsiteTitle          *string  `json:"website_title"`
	WebsiteDescription    *string  `json:"website_description"`
}

type bookmark struct {
	ID           int64    `json:"id"`
	URL          string   `json:"url"`
	Title        string   `json:"title"`
	Description  string   `json:"description,omitempty"`
	Notes        string   `json:"notes,omitempty"`
	TagNames     []string `json:"tag_names"`
	Unread       bool     `json:"unread"`
	IsArchived   bool     `json:"is_archived"`
	Shared       bool     `json:"shared"`
	DateAdded    string   `json:"date_added"`
	DateModified string   `json:"date_modified"`
}

// tag is both the wire shape and the trimmed one: linkding's tag object is already small.
type tag struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	DateAdded string `json:"date_added"`
}

// page is linkding's list envelope. results is decoded lazily so the same type serves
// bookmarks and tags, and so --full can pass the raw objects through untouched.
type page struct {
	Count    int               `json:"count"`
	Next     *string           `json:"next"`
	Previous *string           `json:"previous"`
	Results  []json.RawMessage `json:"results"`
}

type bookmarkPage struct {
	Count    int        `json:"count"`
	Next     *string    `json:"next"`
	Previous *string    `json:"previous"`
	Results  []bookmark `json:"results"`
}

type tagPage struct {
	Count    int     `json:"count"`
	Next     *string `json:"next"`
	Previous *string `json:"previous"`
	Results  []tag   `json:"results"`
}

func trimBookmark(raw rawBookmark) bookmark {
	b := bookmark{
		ID:           raw.ID,
		URL:          raw.URL,
		Title:        raw.Title,
		Description:  raw.Description,
		Notes:        raw.Notes,
		TagNames:     raw.TagNames,
		Unread:       raw.Unread,
		IsArchived:   raw.IsArchived,
		Shared:       raw.Shared,
		DateAdded:    raw.DateAdded,
		DateModified: raw.DateModified,
	}
	if b.TagNames == nil {
		b.TagNames = []string{}
	}
	return b
}

func decodePage(data json.RawMessage, what string) (page, error) {
	var p page
	if err := json.Unmarshal(data, &p); err != nil {
		return page{}, fmt.Errorf("decoding %s: %w", what, err)
	}
	if p.Results == nil {
		p.Results = []json.RawMessage{}
	}
	return p, nil
}

func decodeBookmark(data json.RawMessage) (bookmark, error) {
	var raw rawBookmark
	if err := json.Unmarshal(data, &raw); err != nil {
		return bookmark{}, fmt.Errorf("decoding bookmark: %w", err)
	}
	return trimBookmark(raw), nil
}

func trimBookmarkPage(p page) (bookmarkPage, error) {
	out := bookmarkPage{Count: p.Count, Next: p.Next, Previous: p.Previous, Results: make([]bookmark, 0, len(p.Results))}
	for _, raw := range p.Results {
		b, err := decodeBookmark(raw)
		if err != nil {
			return bookmarkPage{}, err
		}
		out.Results = append(out.Results, b)
	}
	return out, nil
}

func trimTagPage(p page) (tagPage, error) {
	out := tagPage{Count: p.Count, Next: p.Next, Previous: p.Previous, Results: make([]tag, 0, len(p.Results))}
	for _, raw := range p.Results {
		var t tag
		if err := json.Unmarshal(raw, &t); err != nil {
			return tagPage{}, fmt.Errorf("decoding tag: %w", err)
		}
		out.Results = append(out.Results, t)
	}
	return out, nil
}

// checkResult is the answer of /bookmarks/check/: the bookmark when the URL is saved, the
// scraped metadata and the tags linkding would apply on its own when it is not.
type checkResult struct {
	Bookmark *bookmark       `json:"bookmark"`
	Metadata json.RawMessage `json:"metadata"`
	AutoTags []string        `json:"auto_tags"`
}

func decodeCheck(data json.RawMessage) (checkResult, error) {
	var raw struct {
		Bookmark json.RawMessage `json:"bookmark"`
		Metadata json.RawMessage `json:"metadata"`
		AutoTags []string        `json:"auto_tags"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return checkResult{}, fmt.Errorf("decoding check: %w", err)
	}
	result := checkResult{Metadata: raw.Metadata, AutoTags: raw.AutoTags}
	if result.AutoTags == nil {
		result.AutoTags = []string{}
	}
	if len(raw.Bookmark) > 0 && string(raw.Bookmark) != "null" {
		b, err := decodeBookmark(raw.Bookmark)
		if err != nil {
			return checkResult{}, err
		}
		result.Bookmark = &b
	}
	return result, nil
}
