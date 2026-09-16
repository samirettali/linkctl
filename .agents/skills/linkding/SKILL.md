---
name: linkding
description: Read and write Samir's linkding bookmarks with linkctl — search saved links, check whether a URL is already bookmarked, save one with tags and notes, retag, archive or delete it, list tags. Use when asked what he has bookmarked about a topic, to save or bookmark a link, to look through his reading list or unread links, or to tidy tags.
compatibility: Requires linkctl with LINKDING_URL and LINKDING_TOKEN set, or an unlocked rbw vault holding the linkding-api-key entry.
---

# Linkding

`linkctl` reads and writes the linkding instance. stdout is JSON; errors are JSON on stderr.

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

Relay the `fix` to the user; unlocking the vault cannot be done for them.

## Search

```sh
linkctl bookmark list account abstraction           # free terms match title, description, notes and URL
linkctl bookmark list --tag go --tag kafka --limit 20   # tags are ANDed
linkctl bookmark list --unread
linkctl bookmark list --untagged
linkctl bookmark list nix --archived                # the archive is a separate collection
linkctl bookmark list --added-since 7d --limit 0    # everything from the last week
linkctl bookmark list --modified-since 2026-09-01T00:00:00Z
```

`--added-since`/`--modified-since` take `24h`, `7d`, `2w` or an RFC 3339 timestamp. The default page is 50; `--limit 0` walks every page and returns everything in one answer with `next` null, `--offset` skips the first N results either way. A search term starting with a dash goes after `--`: `bookmark list -- -foo`. Everything after `--` is a search term, so put flags before it. With several hundred results, group by tag and summarize per group rather than listing each item.

## One bookmark

```sh
linkctl bookmark get 412
linkctl bookmark check https://example.com/post
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

`add` scrapes the page for title and description unless `--no-scrape` is given, and answers the trimmed bookmark. **A URL that is already saved is refused**, with the existing ID in the error: change it with `update`. `--replace` forces the save, and linkding then overwrites title, description, notes, unread and shared with what was sent and replaces the tag list, so only use it when that is the intent. `update` sends only the flags given, but **`--tag` replaces the whole tag list**: read the bookmark first and repeat the tags to keep. `--unread`/`--no-unread` and `--shared`/`--no-shared` set the flag either way; neither leaves it alone.

`archive`, `unarchive` and `delete` take several IDs and answer `{"archived"|"unarchived"|"deleted": [ids], "failed": [{"id", "error"}]}`; they go on past a per-bookmark failure, so report `failed` whenever it is non-empty. Take IDs from a previous `list`, `get` or `check`, never from memory.

`linkctl bookmark delete ID...` removes bookmarks permanently. Ask before running it.

## Tags

```sh
linkctl tag list --limit 0
```

Check the existing tags before inventing a new one, and reuse the spelling that is already there. A tag cannot be deleted through the API: it lingers once created, so do not create throwaway ones.

## Operating rules

- Only mutate when asked. A search does not archive or retag anything.
- Report bookmarks with title and URL, never bare IDs. Keep the IDs at hand for the follow-up action.
- On a 404, re-check the ID before retrying.
- Never print the token.
