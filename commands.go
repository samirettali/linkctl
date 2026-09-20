package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// parseFlags accepts flags before and after positional arguments, so `bookmark get 42 --full`
// works as well as `bookmark get --full 42`. The positionals end up in fs.Args() in order.
// Everything after a `--` is positional verbatim, so a search term starting with a dash
// can be passed as `bookmark list -- -foo`.
func parseFlags(fs *flag.FlagSet, args []string) error {
	var positional []string
	for len(args) > 0 {
		if args[0] == "--" {
			positional = append(positional, args[1:]...)
			break
		}
		if args[0] == "-" || !strings.HasPrefix(args[0], "-") {
			positional = append(positional, args[0])
			args = args[1:]
			continue
		}
		// Parse a flag with its value as one unit: a string value may itself be --.
		n := 1
		name, _, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(args[0], "-"), "-"), "=")
		if fl := fs.Lookup(name); fl != nil && !hasValue {
			boolean, ok := fl.Value.(interface{ IsBoolFlag() bool })
			if (!ok || !boolean.IsBoolFlag()) && len(args) > 1 {
				n = 2
			}
		}
		if err := fs.Parse(args[:n]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return errHelp
			}
			return fmt.Errorf("%s: %w", fs.Name(), err)
		}
		args = args[n:]
	}
	return fs.Parse(append([]string{"--"}, positional...))
}

// errHelp is returned when a subcommand gets -h/--help; main prints the usage and exits 0.
var errHelp = errors.New("help requested")

func isHelp(arg string) bool { return arg == "-h" || arg == "--help" || arg == "help" }

// noArgs rejects stray positionals, so a mistyped flag does not pass silently.
func noArgs(fs *flag.FlagSet) error {
	if fs.NArg() > 0 {
		return fmt.Errorf("%s: unexpected argument %q", fs.Name(), fs.Arg(0))
	}
	return nil
}

func parseIDs(name string, args []string) ([]int64, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("%s: at least one ID is required", name)
	}
	ids := make([]int64, 0, len(args))
	for _, arg := range args {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("%s: invalid ID %q", name, arg)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func parseOneID(name string, args []string) (int64, error) {
	ids, err := parseIDs(name, args)
	if err != nil {
		return 0, err
	}
	if len(ids) != 1 {
		return 0, fmt.Errorf("%s: exactly one ID is required", name)
	}
	return ids[0], nil
}

// stringList collects a repeatable flag such as --tag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

// triState is a pair of flags, --name and --no-name, that leave a field untouched when
// neither is given. Both at once is an error.
type triState struct {
	name    string
	on, off bool
	fs      *flag.FlagSet
}

func (t *triState) bind(fs *flag.FlagSet, usage string) {
	t.fs = fs
	fs.BoolVar(&t.on, t.name, false, usage)
	fs.BoolVar(&t.off, "no-"+t.name, false, "un"+usage)
}

func (t *triState) value(command string) (*bool, error) {
	var onGiven, offGiven bool
	t.fs.Visit(func(fl *flag.Flag) {
		onGiven = onGiven || fl.Name == t.name
		offGiven = offGiven || fl.Name == "no-"+t.name
	})
	if onGiven && offGiven {
		return nil, fmt.Errorf("%s: --%s and --no-%s are exclusive", command, t.name, t.name)
	}
	if onGiven {
		v := t.on
		return &v, nil
	}
	if offGiven {
		v := !t.off
		return &v, nil
	}
	return nil, nil
}

// parseTime accepts a relative duration ("24h", "7d", "2w") or an RFC 3339 timestamp and
// returns an RFC 3339 timestamp, which is what linkding's *_since filters take.
func parseTime(value string, now time.Time) (string, error) {
	if value == "" {
		return "", nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if d, err := parseDuration(value); err == nil {
		return now.Add(-d).UTC().Format(time.RFC3339), nil
	}
	return "", fmt.Errorf("invalid time %q: use a duration like 24h, 7d, 2w or an RFC 3339 timestamp", value)
}

func parseDuration(value string) (time.Duration, error) {
	if strings.HasSuffix(value, "d") || strings.HasSuffix(value, "w") {
		n, err := strconv.ParseInt(value[:len(value)-1], 10, 64)
		unit := 24 * time.Hour
		if strings.HasSuffix(value, "w") {
			unit *= 7
		}
		if err != nil || n < 0 || n > int64((1<<63-1)/unit) {
			return 0, fmt.Errorf("invalid duration %q", value)
		}
		return time.Duration(n) * unit, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid duration %q", value)
	}
	return d, nil
}

// pageSize is what one request asks for when --limit 0 walks the whole collection.
const pageSize = 100

// listAll walks a paginated endpoint with limit/offset from the given offset until the server
// has no next page, merging the results under the server's count. linkding has no uncapped mode.
func listAll(client *linkdingClient, path string, query url.Values, offset int) (page, error) {
	merged := page{Results: []json.RawMessage{}}
	seenPages := map[[sha256.Size]byte]bool{}
	for {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("offset", strconv.Itoa(offset))
		data, err := client.request("GET", path, q, nil)
		if err != nil {
			return page{}, err
		}
		p, err := decodePage(data, path)
		if err != nil {
			return page{}, err
		}
		if len(p.Results) > 0 {
			// A server that ignores offset can otherwise repeat a page forever.
			hash := sha256.New()
			for _, raw := range p.Results {
				hash.Write(raw)
				hash.Write([]byte{0})
			}
			var fingerprint [sha256.Size]byte
			copy(fingerprint[:], hash.Sum(nil))
			if seenPages[fingerprint] {
				return page{}, fmt.Errorf("decoding %s: repeated page while advancing offset", path)
			}
			seenPages[fingerprint] = true
		}
		merged.Count = p.Count
		merged.Results = append(merged.Results, p.Results...)
		if p.Next == nil {
			return merged, nil
		}
		if len(p.Results) == 0 || offset >= p.Count || len(p.Results) >= p.Count-offset {
			return page{}, fmt.Errorf("decoding %s: next page without progress within count", path)
		}
		offset += len(p.Results)
	}
}

// listPage fetches one page, or everything when limit is 0.
func listPage(client *linkdingClient, path string, query url.Values, limit, offset int) (page, error) {
	if limit == 0 {
		return listAll(client, path, query, offset)
	}
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	q.Set("limit", strconv.Itoa(limit))
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	data, err := client.request("GET", path, q, nil)
	if err != nil {
		return page{}, err
	}
	return decodePage(data, path)
}

func runBookmark(args []string) error {
	if len(args) == 0 {
		return errors.New("bookmark: subcommand required (list, get, check, add, update, delete, archive, unarchive, asset, singlefile)")
	}
	if isHelp(args[0]) {
		return errHelp
	}
	switch args[0] {
	case "asset":
		return runAsset(args[1:])
	case "singlefile":
		return runSinglefile(args[1:])
	case "list":
		return runBookmarkList(args[1:])
	case "get":
		return runBookmarkGet(args[1:])
	case "check":
		return runBookmarkCheck(args[1:])
	case "add":
		return runBookmarkAdd(args[1:])
	case "update":
		return runBookmarkUpdate(args[1:])
	case "delete":
		return runBookmarkBatch("delete", args[1:])
	case "archive":
		return runBookmarkBatch("archive", args[1:])
	case "unarchive":
		return runBookmarkBatch("unarchive", args[1:])
	default:
		return fmt.Errorf("bookmark: unknown subcommand %q", args[0])
	}
}

type bookmarkListOptions struct {
	terms                                  []string
	tags                                   []string
	unread                                 bool
	untagged                               bool
	archived                               bool
	sharedCollection                       bool
	user, sort, filterShared, filterUnread string
	bundle                                 int64
	addedSince                             string
	modifiedSince                          string
	limit                                  int
	offset                                 int
}

// searchQuery composes linkding's search syntax: free terms, #tag per --tag, !unread, !untagged.
func (o bookmarkListOptions) searchQuery() string {
	parts := append([]string{}, o.terms...)
	for _, t := range o.tags {
		parts = append(parts, "#"+t)
	}
	if o.unread {
		parts = append(parts, "!unread")
	}
	if o.untagged {
		parts = append(parts, "!untagged")
	}
	return strings.Join(parts, " ")
}

func (o bookmarkListOptions) path() string {
	if o.sharedCollection {
		return "/bookmarks/shared/"
	}
	if o.archived {
		return "/bookmarks/archived/"
	}
	return "/bookmarks/"
}

func (o bookmarkListOptions) query(now time.Time) (url.Values, error) {
	if o.limit < 0 || o.offset < 0 {
		return nil, errors.New("bookmark list: --limit and --offset must be >= 0")
	}
	if o.archived && o.sharedCollection {
		return nil, errors.New("bookmark list: --archived and --shared-collection are exclusive")
	}
	if o.user != "" && !o.sharedCollection {
		return nil, errors.New("bookmark list: --user requires --shared-collection")
	}
	if o.bundle < 0 {
		return nil, errors.New("bookmark list: --bundle must be a positive ID")
	}
	if o.sort != "" && o.sort != "added_asc" && o.sort != "added_desc" && o.sort != "modified_asc" && o.sort != "modified_desc" && o.sort != "title_asc" && o.sort != "title_desc" {
		return nil, errors.New("bookmark list: invalid --sort")
	}
	if o.unread && o.filterUnread != "" {
		return nil, errors.New("bookmark list: --unread and --filter-unread are exclusive")
	}
	q := url.Values{}
	for _, f := range []struct{ name, value string }{{"shared", o.filterShared}, {"unread", o.filterUnread}} {
		if f.value != "" {
			if f.value != "off" && f.value != "yes" && f.value != "no" {
				return nil, fmt.Errorf("bookmark list: --filter-%s must be off, yes or no", f.name)
			}
			q.Set(f.name, f.value)
		}
	}
	if o.user != "" {
		q.Set("user", o.user)
	}
	if o.sort != "" {
		q.Set("sort", o.sort)
	}
	if o.bundle > 0 {
		q.Set("bundle", strconv.FormatInt(o.bundle, 10))
	}
	if search := o.searchQuery(); search != "" {
		q.Set("q", search)
	}
	for _, f := range []struct{ flag, param, value string }{
		{"--added-since", "added_since", o.addedSince},
		{"--modified-since", "modified_since", o.modifiedSince},
	} {
		ts, err := parseTime(f.value, now)
		if err != nil {
			return nil, fmt.Errorf("bookmark list %s: %w", f.flag, err)
		}
		if ts != "" {
			q.Set(f.param, ts)
		}
	}
	return q, nil
}

func runBookmarkList(args []string) error {
	fs := newFlagSet("bookmark list")
	var o bookmarkListOptions
	var tags stringList
	fs.Var(&tags, "tag", "only bookmarks with this tag; repeatable, tags are ANDed")
	fs.BoolVar(&o.unread, "unread", false, "only unread bookmarks")
	fs.BoolVar(&o.untagged, "untagged", false, "only bookmarks without tags")
	fs.BoolVar(&o.archived, "archived", false, "search the archive instead")
	fs.BoolVar(&o.sharedCollection, "shared-collection", false, "read the shared bookmark collection")
	fs.StringVar(&o.user, "user", "", "shared collection owner username")
	fs.StringVar(&o.sort, "sort", "", "added_asc, added_desc, modified_asc, modified_desc, title_asc, title_desc")
	fs.StringVar(&o.filterShared, "filter-shared", "", "off, yes or no")
	fs.StringVar(&o.filterUnread, "filter-unread", "", "off, yes or no")
	fs.Int64Var(&o.bundle, "bundle", 0, "filter by bundle ID")
	fs.StringVar(&o.addedSince, "added-since", "", "bookmarks added after this duration ago or RFC 3339 time")
	fs.StringVar(&o.modifiedSince, "modified-since", "", "bookmarks modified after this duration ago or RFC 3339 time")
	fs.IntVar(&o.limit, "limit", 50, "page size, 0 for everything")
	fs.IntVar(&o.offset, "offset", 0, "page offset")
	full := fs.Bool("full", false, "return linkding's own objects")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	var bundleGiven bool
	fs.Visit(func(fl *flag.Flag) {
		if fl.Name == "bundle" {
			bundleGiven = true
		}
	})
	if bundleGiven && o.bundle <= 0 {
		return errors.New("bookmark list: --bundle must be a positive ID")
	}
	o.terms = fs.Args()
	o.tags = tags
	query, err := o.query(time.Now())
	if err != nil {
		return err
	}
	client, err := newLinkdingClient()
	if err != nil {
		return err
	}
	p, err := listPage(client, o.path(), query, o.limit, o.offset)
	if err != nil {
		return err
	}
	trimmed, err := trimBookmarkPage(p)
	if err != nil {
		return err
	}
	if *full {
		return writeJSON(p)
	}
	return writeJSON(trimmed)
}

func runBookmarkGet(args []string) error {
	fs := newFlagSet("bookmark get")
	full := fs.Bool("full", false, "return linkding's own object")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	id, err := parseOneID("bookmark get", fs.Args())
	if err != nil {
		return err
	}
	client, err := newLinkdingClient()
	if err != nil {
		return err
	}
	data, err := client.request("GET", fmt.Sprintf("/bookmarks/%d/", id), nil, nil)
	if err != nil {
		return err
	}
	return writeBookmark(data, *full)
}

func writeBookmark(data json.RawMessage, full bool) error {
	b, err := decodeBookmark(data)
	if err != nil {
		return err
	}
	if full {
		return writeJSON(data)
	}
	return writeJSON(b)
}

func runBookmarkCheck(args []string) error {
	fs := newFlagSet("bookmark check")
	ignoreCache := fs.Bool("ignore-cache", false, "refresh cached website metadata")
	full := fs.Bool("full", false, "return linkding's own object")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("bookmark check: exactly one URL is required")
	}
	client, err := newLinkdingClient()
	if err != nil {
		return err
	}
	query := url.Values{"url": {fs.Arg(0)}}
	if *ignoreCache {
		query.Set("ignore_cache", "true")
	}
	data, err := client.request("GET", "/bookmarks/check/", query, nil)
	if err != nil {
		return err
	}
	result, err := decodeCheck(data)
	if err != nil {
		return err
	}
	if *full {
		return writeJSON(data)
	}
	return writeJSON(result)
}

// bookmarkFields are the writable fields shared by add and update; a nil pointer is "not given".
type bookmarkFields struct {
	url, title, description, notes *string
	tags                           []string
	unread, shared, archived       *bool
	dateAdded, dateModified        *string
}

func bindBookmarkFields(fs *flag.FlagSet, withURL bool) func(command string) (bookmarkFields, error) {
	var f bookmarkFields
	var u, title, description, notes, dateAdded, dateModified string
	clearTags := fs.Bool("clear-tags", false, "replace tags with an empty list (auto-tagging may reapply tags)")
	var tags stringList
	unread := &triState{name: "unread"}
	shared := &triState{name: "shared"}
	archived := &triState{name: "archived"}
	if withURL {
		fs.StringVar(&u, "url", "", "new URL")
	}
	fs.StringVar(&title, "title", "", "title")
	fs.StringVar(&description, "description", "", "description")
	fs.StringVar(&notes, "notes", "", "notes, Markdown")
	fs.Var(&tags, "tag", "tag; repeatable, replaces the whole list on update")
	unread.bind(fs, "mark as unread")
	shared.bind(fs, "mark as shared")
	archived.bind(fs, "mark as archived")
	fs.StringVar(&dateAdded, "date-added", "", "date added, RFC 3339")
	fs.StringVar(&dateModified, "date-modified", "", "date modified, RFC 3339")
	return func(command string) (bookmarkFields, error) {
		given := map[string]bool{}
		fs.Visit(func(fl *flag.Flag) { given[fl.Name] = true })
		if given["url"] {
			f.url = &u
		}
		if given["title"] {
			f.title = &title
		}
		if given["description"] {
			f.description = &description
		}
		if given["notes"] {
			f.notes = &notes
		}
		if given["tag"] {
			f.tags = tags
		}
		if *clearTags {
			if given["tag"] {
				return f, fmt.Errorf("%s: --tag and --clear-tags are exclusive", command)
			}
			f.tags = []string{}
		}
		for _, field := range []struct {
			name, value string
			target      **string
		}{{"date-added", dateAdded, &f.dateAdded}, {"date-modified", dateModified, &f.dateModified}} {
			if given[field.name] {
				t, err := time.Parse(time.RFC3339, field.value)
				if err != nil {
					return f, fmt.Errorf("%s: --%s requires RFC 3339", command, field.name)
				}
				v := t.UTC().Format(time.RFC3339Nano)
				*field.target = &v
			}
		}
		var err error
		if f.unread, err = unread.value(command); err != nil {
			return f, err
		}
		if f.shared, err = shared.value(command); err != nil {
			return f, err
		}
		if f.archived, err = archived.value(command); err != nil {
			return f, err
		}
		return f, nil
	}
}

func (f bookmarkFields) body() map[string]any {
	body := map[string]any{}
	if f.url != nil {
		body["url"] = *f.url
	}
	if f.title != nil {
		body["title"] = *f.title
	}
	if f.description != nil {
		body["description"] = *f.description
	}
	if f.notes != nil {
		body["notes"] = *f.notes
	}
	if f.tags != nil {
		body["tag_names"] = f.tags
	}
	if f.unread != nil {
		body["unread"] = *f.unread
	}
	if f.shared != nil {
		body["shared"] = *f.shared
	}
	if f.archived != nil {
		body["is_archived"] = *f.archived
	}
	if f.dateAdded != nil {
		body["date_added"] = *f.dateAdded
	}
	if f.dateModified != nil {
		body["date_modified"] = *f.dateModified
	}
	return body
}

// runBookmarkAdd refuses a URL that is already saved unless --replace is given: linkding's
// POST merges into the existing bookmark (title, description, notes, unread, shared are
// overwritten and the tag list replaced) and still answers 201, so nothing else would tell.
func runBookmarkAdd(args []string) error {
	fs := newFlagSet("bookmark add")
	fields := bindBookmarkFields(fs, false)
	noScrape := fs.Bool("no-scrape", false, "do not fetch title and description from the page")
	noSnapshot := fs.Bool("no-snapshot", false, "disable automatic HTML snapshot creation")
	replace := fs.Bool("replace", false, "overwrite the bookmark if the URL is already saved")
	full := fs.Bool("full", false, "return linkding's own object")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("bookmark add: exactly one URL is required")
	}
	f, err := fields("bookmark add")
	if err != nil {
		return err
	}
	body := f.body()
	body["url"] = fs.Arg(0)
	query := url.Values{}
	if *noScrape {
		query.Set("disable_scraping", "true")
	}
	if *noSnapshot {
		query.Set("disable_html_snapshot", "true")
	}
	client, err := newLinkdingClient()
	if err != nil {
		return err
	}
	if !*replace {
		if err := refuseExisting(client, fs.Arg(0)); err != nil {
			return err
		}
	}
	data, err := client.request("POST", "/bookmarks/", query, body)
	if err != nil {
		return err
	}
	return writeBookmark(data, *full)
}

func refuseExisting(client *linkdingClient, target string) error {
	data, err := client.request("GET", "/bookmarks/check/", url.Values{"url": {target}}, nil)
	if err != nil {
		return err
	}
	result, err := decodeCheck(data)
	if err != nil {
		return err
	}
	if result.Bookmark != nil {
		return fmt.Errorf("bookmark add: %s is already bookmark %d; use 'bookmark update %d' to change it, or --replace to overwrite its fields and tags",
			target, result.Bookmark.ID, result.Bookmark.ID)
	}
	return nil
}

func runBookmarkUpdate(args []string) error {
	fs := newFlagSet("bookmark update")
	fields := bindBookmarkFields(fs, true)
	full := fs.Bool("full", false, "return linkding's own object")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	id, err := parseOneID("bookmark update", fs.Args())
	if err != nil {
		return err
	}
	f, err := fields("bookmark update")
	if err != nil {
		return err
	}
	body := f.body()
	if len(body) == 0 {
		return errors.New("bookmark update: nothing to update, pass at least one field")
	}
	client, err := newLinkdingClient()
	if err != nil {
		return err
	}
	data, err := client.request("PATCH", fmt.Sprintf("/bookmarks/%d/", id), nil, body)
	if err != nil {
		return err
	}
	return writeBookmark(data, *full)
}

type failedBookmark struct {
	ID    int64  `json:"id"`
	Error string `json:"error"`
}

// batchResult answers delete/archive/unarchive as {"<done>": [ids], "failed": [{id, error}]},
// where <done> is "deleted", "archived" or "unarchived".
type batchResult struct {
	done   string
	Done   []int64
	Failed []failedBookmark
}

func (r batchResult) MarshalJSON() ([]byte, error) {
	done, err := json.Marshal(r.Done)
	if err != nil {
		return nil, err
	}
	failed, err := json.Marshal(r.Failed)
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf(`{%q:%s,"failed":%s}`, r.done, done, failed)), nil
}

// batchAction maps an action to its request. linkding answers 204 to all three.
func batchAction(action string, id int64) (method, path, done string) {
	base := fmt.Sprintf("/bookmarks/%d/", id)
	switch action {
	case "delete":
		return "DELETE", base, "deleted"
	case "archive":
		return "POST", base + "archive/", "archived"
	default:
		return "POST", base + "unarchive/", "unarchived"
	}
}

func runBookmarkBatch(action string, args []string) error {
	name := "bookmark " + action
	fs := newFlagSet(name)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ids, err := parseIDs(name, fs.Args())
	if err != nil {
		return err
	}
	client, err := newLinkdingClient()
	if err != nil {
		return err
	}
	result, err := batchBookmarks(client, action, ids)
	if err != nil {
		return err
	}
	return writeJSON(result)
}

// batchBookmarks collects per-bookmark failures instead of aborting, except for an authError:
// a rejected token fails every bookmark the same way and the caller needs the fix, not a list.
func batchBookmarks(client *linkdingClient, action string, ids []int64) (batchResult, error) {
	_, _, done := batchAction(action, 0)
	result := batchResult{done: done, Done: []int64{}, Failed: []failedBookmark{}}
	for _, id := range ids {
		method, path, _ := batchAction(action, id)
		if _, err := client.request(method, path, nil, nil); err != nil {
			if isAuthError(err) {
				return result, err
			}
			result.Failed = append(result.Failed, failedBookmark{ID: id, Error: err.Error()})
			continue
		}
		result.Done = append(result.Done, id)
	}
	return result, nil
}

func isAuthError(err error) bool {
	var authErr *authError
	return errors.As(err, &authErr)
}

func runTag(args []string) error { return runResource("tag", args) }
