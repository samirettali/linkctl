# linkctl

Agent-friendly CLI for [linkding](https://linkding.link). stdout is JSON, errors are JSON on stderr, and the default output is trimmed to what an agent needs to search bookmarks and act on them.

## Install

```sh
go build -o linkctl . && install -m755 linkctl ~/.local/bin/
```

## Configure

Create a REST API token in linkding under Settings > Integrations, then either export it:

```sh
export LINKDING_URL=https://links.example.com
export LINKDING_TOKEN=...
```

or store it in an [rbw](https://github.com/doy/rbw) entry named `linkding-api-key`, with the token as the password and the instance URL as the URI. linkctl reads the vault when the environment is missing and the vault is unlocked. Vault commands share a 10-second timeout and never prompt or echo failure output.

The instance URL may include a path prefix, but not user info, a query or a fragment. HTTP requests time out after 60 seconds; redirects must keep the original scheme, host, port and method and cannot contain credentials. Error bodies are limited to a 16 KiB diagnostic prefix; successful payloads are not size-limited.

## Usage

```sh
linkctl bookmark list [QUERY...] [--tag TAG]... [--unread] [--untagged] [--archived]
                      [--added-since 24h|7d|2w|RFC3339] [--modified-since ...] [--limit N] [--offset N]
linkctl bookmark get ID
linkctl bookmark check URL
linkctl bookmark add URL [--title S] [--description S] [--notes S] [--tag TAG]...
                         [--unread|--no-unread] [--shared|--no-shared] [--no-scrape] [--replace]
linkctl bookmark update ID [--url S] [--title S] [--description S] [--notes S] [--tag TAG]...
                           [--unread|--no-unread] [--shared|--no-shared]
linkctl bookmark delete ID...
linkctl bookmark archive ID...
linkctl bookmark unarchive ID...
linkctl tag list [--limit N] [--offset N]
linkctl tag get ID
linkctl tag create NAME
linkctl tag delete ID...
linkctl bundle list [--limit N] [--offset N]
linkctl bundle get ID
linkctl bundle create --name NAME [--search S] [--any-tags S] [--all-tags S] [--excluded-tags S]
                      [--filter-unread off|yes|no] [--filter-shared off|yes|no] [--order N]
linkctl bundle update ID [same fields as create]
linkctl bundle delete ID...
linkctl bookmark asset list BOOKMARK_ID [--limit N] [--offset N]
linkctl bookmark asset get BOOKMARK_ID ASSET_ID
linkctl bookmark asset upload BOOKMARK_ID --input PATH
linkctl bookmark asset download BOOKMARK_ID ASSET_ID --output PATH
linkctl bookmark asset delete BOOKMARK_ID ASSET_ID...
linkctl bookmark singlefile URL --input PATH
linkctl user profile
```

Lists keep linkding's `{"count", "next", "previous", "results"}` envelope with the objects inside trimmed; `--full` on `bookmark list`, `get`, `check`, `add` and `update` returns linkding's own objects. `--limit 0` fetches every page and returns everything in one answer. `add` refuses a URL that is already saved, naming the existing ID, unless `--replace` is given: linkding would otherwise merge into it and overwrite its fields. `update --tag` replaces the whole tag list. `delete`, `archive` and `unarchive` take several IDs and report per-ID failures without aborting.

Explicit boolean values are honored: `--unread=false` and `--no-unread` both send false; `--no-unread=false` sends true. The same applies to shared and archived. Giving both positive and negative flag names is an error regardless of their values. A string flag can take `--` as its value; only a standalone `--` ends flag parsing.

Malformed responses and repeated pagination pages fail rather than returning a misleading partial result. The duplicate check before `add` fails closed on malformed data, but is not atomic with the save: another writer can still race it. Per-ID batch failures remain in stdout JSON with exit status 0; authentication failures stop the batch with the remedy on stderr and exit status 1.

## Full user API

The contract target is **linkding v1.47.0**, not every older deployment. See the
[endpoint/capability matrix](docs/api-coverage.md) for exact fields, compatibility
errors, file-transfer behavior and intentional administration/web-only exclusions.
Current linkding supports tag deletion; a 404/405 on older servers may indicate an
unsupported endpoint, not just a missing object.

Additional bookmark options:

- `list --shared-collection [--user USERNAME]` reads the separate shared collection;
  `--filter-shared off|yes|no` filters the selected collection instead.
- `list --bundle ID --sort title_asc --filter-unread no` supports bundles, sorting
  and read/unread filters. See `linkctl --help` for all sort values.
- `check --ignore-cache` refreshes cached website metadata.
- Add/update support `--archived|--no-archived`, `--clear-tags`, `--date-added` and
  `--date-modified` (RFC 3339). `--clear-tags` sends an empty list, although server
  auto-tagging can add tags back. Add also supports `--no-snapshot`.

Tag, bundle, asset and profile JSON objects pass through completely, with `--full`
accepted on their JSON read/create/update commands. Bundle tag fields are
space-separated strings; empty strings clear fields on PATCH, omission preserves
them. Bundle `off` disables a filter; it does not mean false.

File commands require explicit paths. Uploads accept regular files only and are
supported on Unix (including macOS and Linux); non-Unix uploads fail closed with a
clear unsupported-platform error before any upload request. Nonblocking/no-follow
opening rejects substituted FIFOs or symlinks safely. Other commands remain
buildable on non-Unix platforms. Downloads never overwrite an existing file or
symlink, ignore server-suggested filenames,
and print a JSON receipt rather than bytes. SingleFile uploads **add a snapshot
and create a bookmark if the URL is absent**. No automatic mutation retries or
implicit deletion occur. Tag deletion removes associations, bundle deletion
removes the saved filter, and asset deletion removes the stored file; none of
these delete the associated bookmarks.

For Samir's library, unarchived is the reading/watching queue; archived is the
functional collection of tools, docs and reusable sites, **not consumed items**.

The canonical agent skill lives in `.agents/skills/linkding`.
