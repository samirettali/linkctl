---
name: linkding
description: Read and write Samir's linkding bookmarks with linkctl — search, save, retag, archive or delete bookmarks; manage tags, bundles and bookmark assets; upload SingleFile snapshots; read shared bookmarks and the user profile. Use for saved links, reading/watch queues, functional collections and their files.
compatibility: Requires linkctl with LINKDING_URL and LINKDING_TOKEN set, or an unlocked rbw vault holding the linkding-api-key entry.
---

# Linkding

`linkctl` reads and writes the linkding instance. stdout is JSON; errors are JSON on stderr.
The full API contract target is linkding v1.47.0; older deployments may lack endpoints.

**Unarchived is the read/watch-later queue. Archived is the functional collection**:
tools, docs, product pages and sites used repeatedly, never “consumed queue items”.
A functional link in the queue is misfiled: archive it instead of deleting it.
Durable rereading material belongs in Obsidian `Clippings/`.

## Output shapes

Every list keeps linkding's own envelope, `{"count", "next", "previous", "results": [...]}`; `count` is the total for the whole filter, not the page. Only the objects inside are trimmed:

```
bookmark  {id, url, title, description?, notes?, tag_names, unread, is_archived, shared,
           date_added, date_modified}
tag       {id, name, date_added}
```

`--full` on `bookmark list|get|check|add|update` returns linkding's objects verbatim, adding the favicon, preview image and web archive URLs plus the deprecated, always-null `website_title`/`website_description`. Do not use it unless the task needs one of those.

## Authentication

**Never check configuration before running a command.** Anything that needs it fails with the fix in the error:

```json
{"error": "linkding is not configured", "fix": "export LINKDING_URL and LINKDING_TOKEN, or ...", "details": "rbw is locked: run 'rbw unlock'"}
```

Relay the `fix` and relevant `details` to the user; unlocking the vault cannot be done for them. Vault commands share a 10-second deadline. HTTP calls time out after 60 seconds and only follow redirects that preserve the scheme, host, port and method without URL credentials. A refused redirect is an error, not a successful save.

## Search

```sh
linkctl bookmark list account abstraction           # free terms match title, description, notes and URL
linkctl bookmark list --tag go --tag kafka --limit 20   # tags are ANDed
linkctl bookmark list --unread
linkctl bookmark list --untagged
linkctl bookmark list nix --archived                # the archive is a separate collection
linkctl bookmark list --added-since 7d --limit 0    # everything from the last week
linkctl bookmark list --modified-since 2026-09-01T00:00:00Z
linkctl bookmark list --shared-collection --user alice # separate shared collection
linkctl bookmark list --bundle 3 --sort title_asc
linkctl bookmark list --filter-unread no --filter-shared yes
```

`--added-since`/`--modified-since` take `24h`, `7d`, `2w` or an RFC 3339 timestamp. The default page is 50; `--limit 0` walks every page and returns everything in one answer with `next` null, `--offset` skips the first N results either way. A search term starting with a dash goes after `--`: `bookmark list -- -foo`. Everything after `--` is a search term, so put flags before it. With several hundred results, group by tag and summarize per group rather than listing each item.

`--shared-collection` is distinct from the `--shared` mutation flag and cannot be
combined with `--archived`. `--user` is only valid for shared collection reads.
The filter flags accept `off|yes|no`; `off` disables the filter. Sort values are
`added_asc|added_desc|modified_asc|modified_desc|title_asc|title_desc`.

## One bookmark

```sh
linkctl bookmark get 412
linkctl bookmark check https://example.com/post
linkctl bookmark check https://example.com/post --ignore-cache
```

`check` answers `{"bookmark", "metadata", "auto_tags"}`: `bookmark` is the saved one or null; when null, `metadata` holds the scraped title and description and `auto_tags` the tags linkding would apply on its own. linkding scrapes the page live to answer, so a slow or unreachable site makes `check` slow; it is not a hang.

## Save and edit

```sh
linkctl bookmark add https://example.com/post --tag go --tag concurrency
linkctl bookmark add https://example.com/post --title 'Title' --notes 'why it matters' --no-scrape
linkctl bookmark update 412 --tag go --tag rust --notes 'revisit'
linkctl bookmark update 412 --no-unread
linkctl bookmark archive 412 413
linkctl bookmark unarchive 412
```

`add` scrapes the page for title and description unless `--no-scrape` is given, and answers the trimmed bookmark. **A URL that is already saved is refused**, with the existing ID in the error: change it with `update`. `--replace` forces the save, and linkding then overwrites title, description, notes, unread and shared with what was sent and replaces the tag list, so only use it when that is the intent. `update` sends only the flags given, but **`--tag` replaces the whole tag list**: read the bookmark first and repeat the tags to keep. `--unread`/`--no-unread` and `--shared`/`--no-shared` set the flag either way; neither leaves it alone. Explicit values are honored: `--unread=false` sends false and `--no-unread=false` sends true (likewise for shared). Both flag names together are rejected regardless of values.

The duplicate check fails closed on malformed responses, but the check and save are not atomic: concurrent writers can still race it. Never assume a failed or timed-out mutation had no effect; inspect the bookmark before retrying.

`archive`, `unarchive` and `delete` take several IDs and answer `{"archived"|"unarchived"|"deleted": [ids], "failed": [{"id", "error"}]}`; they go on past a per-bookmark failure and still exit 0, so report `failed` whenever it is non-empty. A 401 instead stops immediately, exits 1 and reports the authentication remedy on stderr; earlier successes are not printed, so re-check state before retrying. Take IDs from a previous `list`, `get` or `check`, never from memory.

`linkctl bookmark delete ID...` removes bookmarks permanently. Get authorization
before permanent deletion; an explicit request to delete is authorization.

Add/update also accept `--archived|--no-archived` with the same explicit-boolean
rules, `--clear-tags` (exclusive with `--tag` when true), and `--date-added` /
`--date-modified` (RFC 3339). Auto-tagging may add tags back after clearing.
`add --no-snapshot` disables automatic HTML snapshot creation. Empty string fields
clear their value; omission preserves it on update.

## Tags

```sh
linkctl tag list --limit 0
linkctl tag get 12
linkctl tag create golang
linkctl tag delete 12
```

Check existing tags before inventing one and reuse existing spelling. Current
linkding supports deletion; older servers may return 404/405. Deleting a tag
removes its associations, **not the bookmarks**. All deletion verbs require
explicit authorization, not an inferred cleanup step.

## Bundles

```sh
linkctl bundle list --limit 0
linkctl bundle get 3
linkctl bundle create --name Work --any-tags 'go kafka' --filter-unread yes
linkctl bundle update 3 --search '' --excluded-tags old --filter-unread off
linkctl bundle delete 3
```

Fields: `--name`, `--search`, `--any-tags`, `--all-tags`, `--excluded-tags`,
`--filter-unread off|yes|no`, `--filter-shared off|yes|no`, `--order INTEGER`.
Tag fields are space-separated strings. PATCH sends only supplied fields; `''`
clears strings and `off` clears filters. Bundle deletion removes the saved filter,
not its matching bookmarks. `--order` sets the API field, not a UI drag operation.

## Assets and snapshots

```sh
linkctl bookmark asset list 412 --limit 0
linkctl bookmark asset get 412 9
linkctl bookmark asset upload 412 --input /path/to/file.pdf
linkctl bookmark asset download 412 9 --output /path/to/new-file.pdf
linkctl bookmark asset delete 412 9 10
linkctl bookmark singlefile https://example.com/post --input /path/to/snapshot.html
```

Uploads require regular files (no symlinks/devices). Downloads require an explicit
new path whose parent exists, never overwrite, and print a JSON receipt. Server
filenames never pick the local destination. Assets delete permanently.
SingleFile **adds a snapshot and creates a bookmark if absent**, potentially
scraping metadata server-side; it is not a generic file upload or a replacement
of bookmark notes/tags. Inspect state after a failed/timed-out mutation instead of
blindly retrying. Uploads are never replayed through redirects; use the canonical
instance URL. A 403 may mean asset uploads are disabled on the server.

Tags, bundles and assets support the same list envelope and paging conventions.
Their small objects, plus `user profile`, pass through unchanged (`--full` is
accepted on JSON read/create/update commands). Tag/bundle/asset deletions use the
same `deleted`/`failed` batch and auth-stop semantics as bookmark deletion.

## Profile and compatibility

`linkctl user profile` reads preferences and the server version when supplied.
There are no account, API-token, settings or other administration commands.
A 404 may mean missing/inaccessible data or an unsupported route; 405 may mean an
unsupported method. Check version and ownership before retrying. Old servers may
silently ignore new query filters, so verify compatibility before relying on them.

## Operating rules

- Only mutate when asked. A search does not archive or retag anything.
- Report bookmarks with title and URL, never bare IDs. Keep the IDs at hand for the follow-up action.
- On a 404, re-check the ID before retrying.
- Never print the token.
