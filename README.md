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
```

Lists keep linkding's `{"count", "next", "previous", "results"}` envelope with the objects inside trimmed; `--full` on `bookmark list`, `get`, `check`, `add` and `update` returns linkding's own objects. `--limit 0` fetches every page and returns everything in one answer. `add` refuses a URL that is already saved, naming the existing ID, unless `--replace` is given: linkding would otherwise merge into it and overwrite its fields. `update --tag` replaces the whole tag list. `delete`, `archive` and `unarchive` take several IDs and report per-ID failures without aborting.

Explicit boolean values are honored: `--unread=false` and `--no-unread` both send false; `--no-unread=false` sends true. The same applies to shared. Giving both positive and negative flag names is an error regardless of their values. A string flag can take `--` as its value; only a standalone `--` ends flag parsing.

Malformed responses and repeated pagination pages fail rather than returning a misleading partial result. The duplicate check before `add` fails closed on malformed data, but is not atomic with the save: another writer can still race it. Per-ID batch failures remain in stdout JSON with exit status 0; authentication failures stop the batch with the remedy on stderr and exit status 1.

The agent skill lives in `.agents/skills/linkding`.
