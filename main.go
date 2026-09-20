package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

const version = "0.1.1"

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, errHelp) {
			printUsage()
			return
		}
		stderr := json.NewEncoder(os.Stderr)
		stderr.SetEscapeHTML(false)
		var authErr *authError
		var apiErr *APIError
		if errors.As(err, &authErr) {
			payload := map[string]any{"error": authErr.Message, "fix": authErr.Fix}
			if authErr.Cause != nil {
				payload["details"] = authErr.Cause.Error()
			}
			_ = stderr.Encode(payload)
		} else if errors.As(err, &apiErr) {
			_ = stderr.Encode(map[string]any{
				"error":   apiErr.Message,
				"status":  apiErr.Status,
				"details": apiErr.Details,
			})
		} else {
			_ = stderr.Encode(map[string]string{"error": err.Error()})
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "bookmark":
		return runBookmark(args[1:])
	case "tag":
		return runTag(args[1:])
	case "bundle":
		return runResource("bundle", args[1:])
	case "user":
		if len(args) < 2 {
			return errors.New("user: subcommand required (profile)")
		}
		if isHelp(args[1]) {
			return errHelp
		}
		if args[1] != "profile" {
			return fmt.Errorf("user: unknown subcommand %q", args[1])
		}
		return runProfile(args[2:])
	case "version", "--version", "-v":
		fs := newFlagSet("version")
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		if err := noArgs(fs); err != nil {
			return err
		}
		return writeJSON(map[string]string{"version": version})
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `linkctl reads and writes bookmarks in a linkding instance through its API.

Usage:
  linkctl bookmark list [QUERY...] [--tag TAG]... [--unread] [--untagged] [--archived]
                        [--added-since DURATION|RFC3339] [--modified-since DURATION|RFC3339]
                        [--limit N] [--offset N] [--full]
  linkctl bookmark get ID [--full]
  linkctl bookmark check URL [--full]
  linkctl bookmark add URL [--title S] [--description S] [--notes S] [--tag TAG]...
                           [--unread|--no-unread] [--shared|--no-shared] [--no-scrape] [--replace] [--full]
  linkctl bookmark update ID [--url S] [--title S] [--description S] [--notes S] [--tag TAG]...
                             [--unread|--no-unread] [--shared|--no-shared] [--full]
  linkctl bookmark delete ID...
  linkctl bookmark archive ID...
  linkctl bookmark unarchive ID...
  linkctl tag list [--limit N] [--offset N] [--full]
  linkctl tag get ID [--full]
  linkctl tag create NAME [--full]
  linkctl tag delete ID...
  linkctl bundle list [--limit N] [--offset N] [--full]
  linkctl bundle get ID [--full]
  linkctl bundle create --name S [BUNDLE FIELDS] [--full]
  linkctl bundle update ID [BUNDLE FIELDS] [--full]
  linkctl bundle delete ID...
  linkctl bookmark asset list BOOKMARK_ID [--limit N] [--offset N] [--full]
  linkctl bookmark asset get BOOKMARK_ID ASSET_ID [--full]
  linkctl bookmark asset upload BOOKMARK_ID --input PATH [--full]
  linkctl bookmark asset download BOOKMARK_ID ASSET_ID --output PATH
  linkctl bookmark asset delete BOOKMARK_ID ASSET_ID...
  linkctl bookmark singlefile URL --input PATH
  linkctl user profile [--full]
  linkctl version

Additional bookmark options:
  list: --shared-collection [--user USERNAME], --bundle ID, --sort SORT,
        --filter-unread off|yes|no, --filter-shared off|yes|no
  check: --ignore-cache
  add/update: --clear-tags, --archived|--no-archived,
              --date-added RFC3339, --date-modified RFC3339
  add: --no-snapshot
Bundle fields:
  --name S, --search S, --any-tags S, --all-tags S, --excluded-tags S,
  --filter-unread off|yes|no, --filter-shared off|yes|no, --order N

Configuration:
  LINKDING_URL     base URL of the instance (https://links.example.com)
  LINKDING_TOKEN   REST API token from Settings > Integrations
  When either is unset, both are read from the rbw entry "linkding-api-key"
  (the password is the token, the URI the base URL), if the vault is unlocked.

stdout is JSON; errors are JSON on stderr.
`)
}
