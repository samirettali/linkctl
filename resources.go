package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
)

// These small resource objects pass through unchanged, including future fields.
func validateResource(data json.RawMessage, kind string) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return fmt.Errorf("decoding %s: expected an object", kind)
	}
	var shape any
	switch kind {
	case "tag":
		shape = &tag{}
	case "bundle":
		shape = &rawBundle{}
	case "asset":
		shape = &rawAsset{}
	case "profile":
		shape = &rawProfile{}
	}
	if shape != nil {
		if err := json.Unmarshal(data, shape); err != nil {
			return fmt.Errorf("decoding %s: %w", kind, err)
		}
	}
	if kind == "profile" {
		var theme *string
		if err := json.Unmarshal(obj["theme"], &theme); err != nil || theme == nil {
			return errors.New("decoding profile: expected theme")
		}
		return nil
	}
	var id int64
	if err := json.Unmarshal(obj["id"], &id); err != nil || id <= 0 {
		return fmt.Errorf("decoding %s: expected a positive id", kind)
	}
	return nil
}

func writeResource(data json.RawMessage, kind string) error {
	if err := validateResource(data, kind); err != nil {
		return err
	}
	return writeJSON(data)
}

func runProfile(args []string) error {
	fs := newFlagSet("user profile")
	fs.Bool("full", false, "return the complete profile (default)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := noArgs(fs); err != nil {
		return err
	}
	c, err := newLinkdingClient()
	if err != nil {
		return err
	}
	data, err := c.request("GET", "/user/profile/", nil, nil)
	if err != nil {
		return err
	}
	return writeResource(data, "profile")
}

// runResource serves tag and bundle operations without inventing tag updates,
// which the public API does not support.
func runResource(kind string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s: subcommand required", kind)
	}
	if isHelp(args[0]) {
		return errHelp
	}
	action := args[0]
	if action != "list" && action != "get" && action != "create" && action != "delete" && !(kind == "bundle" && action == "update") {
		return fmt.Errorf("%s: unknown subcommand %q", kind, action)
	}
	fs := newFlagSet(kind + " " + action)
	if action != "delete" {
		fs.Bool("full", false, "return complete objects (default)")
	}
	limit, offset := 200, 0
	if action == "list" {
		fs.IntVar(&limit, "limit", 200, "page size, 0 for everything")
		fs.IntVar(&offset, "offset", 0, "page offset")
	}
	body := map[string]any{}
	values := map[string]*string{}
	var order int
	if kind == "bundle" && (action == "create" || action == "update") {
		for _, name := range []string{"name", "search", "any-tags", "all-tags", "excluded-tags", "filter-unread", "filter-shared"} {
			values[name] = fs.String(name, "", "bundle field; tag fields are space-separated strings")
		}
		fs.IntVar(&order, "order", 0, "bundle order")
	}
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	path := "/" + kind + "s/"
	var ids []int64
	switch action {
	case "list":
		if err := noArgs(fs); err != nil {
			return err
		}
		if limit < 0 || offset < 0 {
			return fmt.Errorf("%s: --limit and --offset must be >= 0", fs.Name())
		}
	case "get", "update":
		id, err := parseOneID(fs.Name(), fs.Args())
		if err != nil {
			return err
		}
		path += strconv.FormatInt(id, 10) + "/"
	case "delete":
		var err error
		ids, err = parseIDs(fs.Name(), fs.Args())
		if err != nil {
			return err
		}
	case "create":
		if kind == "tag" {
			if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
				return errors.New("tag create: exactly one nonempty name is required")
			}
			body["name"] = fs.Arg(0)
		} else if err := noArgs(fs); err != nil {
			return err
		}
	}
	if kind == "bundle" && (action == "create" || action == "update") {
		fs.Visit(func(f *flag.Flag) {
			if value, ok := values[f.Name]; ok {
				body[strings.ReplaceAll(f.Name, "-", "_")] = *value
			}
			if f.Name == "order" {
				body["order"] = order
			}
		})
		if action == "create" {
			if name, ok := body["name"].(string); !ok || strings.TrimSpace(name) == "" {
				return errors.New("bundle create: --name is required")
			}
		}
		if name, ok := body["name"].(string); ok && strings.TrimSpace(name) == "" {
			return errors.New("bundle: name cannot be empty")
		}
		for _, field := range []string{"filter_unread", "filter_shared"} {
			if value, ok := body[field]; ok && value != "off" && value != "yes" && value != "no" {
				return fmt.Errorf("bundle: %s must be off, yes or no", field)
			}
		}
		if len(body) == 0 {
			return errors.New("bundle update: nothing to update, pass at least one field")
		}
	}
	c, err := newLinkdingClient()
	if err != nil {
		return err
	}
	if action == "list" {
		p, err := listPage(c, path, nil, limit, offset)
		if err != nil {
			return err
		}
		for _, raw := range p.Results {
			if err := validateResource(raw, kind); err != nil {
				return err
			}
		}
		return writeJSON(p)
	}
	if action == "delete" {
		result, err := deleteResources(c, path, ids)
		if err != nil {
			return err
		}
		return writeJSON(result)
	}
	method := "GET"
	var payload any
	if action == "create" {
		method = "POST"
		payload = body
	}
	if action == "update" {
		method = "PATCH"
		payload = body
	}
	data, err := c.request(method, path, nil, payload)
	if err != nil {
		return err
	}
	return writeResource(data, kind)
}

func deleteResources(c *linkdingClient, path string, ids []int64) (batchResult, error) {
	result := batchResult{done: "deleted", Done: []int64{}, Failed: []failedBookmark{}}
	for _, id := range ids {
		_, err := c.request("DELETE", path+strconv.FormatInt(id, 10)+"/", nil, nil)
		if err != nil {
			if isAuthError(err) {
				return result, err
			}
			result.Failed = append(result.Failed, failedBookmark{ID: id, Error: err.Error()})
		} else {
			result.Done = append(result.Done, id)
		}
	}
	return result, nil
}
