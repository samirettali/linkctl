# AGENTS.md

`linkctl` is an agent-friendly CLI for a linkding instance, written in Go. It follows the shape of
`fluxctl` and `spotctl`: JSON on stdout, JSON errors on stderr, trimmed objects by default and
`--full` for the raw ones, standard library only.

## Commands

- `go test ./...` — run tests.
- `go vet ./...` — run static checks.
- `gofmt -l .` — must print nothing.
- `go build -o linkctl . && install -m755 linkctl ~/.local/bin/` — how it is installed today.

## Releasing

Bump `version` in `main.go` in a `chore: release vX.Y.Z` commit, then push an annotated tag.
`.github/workflows/release.yml` publishes the GitHub release and dispatches the updater in
`samirettali/nur` scoped to this package; the rules in spotctl's AGENTS.md apply unchanged
(the release matters, not the tag; the tag must sit on a commit that has the workflow; the
dispatch uses `NUR_DISPATCH_TOKEN` declared in `infra`). No Go dependencies, so `vendorHash`
is null in the NUR package and adding one would break the automatic bump.

The skill is consumed by dotfiles (`coding-agent-skills.nix`) through a `flake = false`
input on this repository, the way `miniflux` is.

## Status

Scope is what the Python wrapper it replaced covered: bookmarks (list, get, check, add, update,
delete, archive, unarchive) and `tag list`. Bundles, assets, tag creation and the user profile
are deliberately left for later.

## Conventions

- **The envelope is linkding's; only the objects inside are trimmed.** Every list is
  `{"count", "next", "previous", "results"}`. A trimmed bookmark keeps the API's field names
  and drops `favicon_url`, `preview_image_url`, `web_archive_snapshot_url` and the deprecated
  `website_title`/`website_description`, which linkding folds into `title`/`description` itself.
  `notes` and `description` are omitted when empty; `tag_names` is always a list, never null.
  Tags are already small and pass through as they are.
- **`--limit 0` walks the collection client-side.** linkding has no uncapped mode, so pages of
  100 are fetched until `next` is null and merged under the server's `count`, with `next` and
  `previous` null in the answer. An explicit `--limit` is one request, `--offset` moves within it.
- Search is composed into linkding's own `q` syntax: free terms, `#tag` per `--tag`, `!unread`,
  `!untagged`. `--archived` switches the path to `/bookmarks/archived/`, a separate collection.
- `--added-since`/`--modified-since` accept `24h`, `7d`, `2w` or RFC 3339 and are sent as
  RFC 3339 in UTC (`added_since`/`modified_since`).
- **Configuration comes from `LINKDING_URL` and `LINKDING_TOKEN`, with the rbw entry
  `linkding-api-key` as the fallback** (password = token, first URI = base URL). The variable is
  called a token because linkding does and the header is `Authorization: Token …`. The fallback
  never prompts: a locked vault is an error whose `fix` says to unlock it.
- **Errors carry their own remedy.** Missing configuration and a 401 both render as
  `{"error", "fix", "details"}`, so no caller needs a status check first. `authError` wraps the
  `APIError`, so `errors.As` still reaches the status code.
- linkding's error bodies are Django REST framework's: `{"detail": "..."}` or per-field
  `{"url": ["..."]}`. Both are flattened into one `details` line, fields sorted by name.
  Non-JSON bodies (a proxy's 502 page) are kept truncated to 300 characters.
- `add` and `update` share one flag set. A field is sent only when its flag was given, so an
  explicit `--title ''` clears the title and an absent one leaves it alone. `--unread` and
  `--shared` are tri-state through `--no-unread`/`--no-shared`; both at once is an error.
  `--tag` replaces the whole list on `update`, because that is what `PATCH` with `tag_names`
  does; the skill tells agents to read first. `update` with no field is an error rather than
  an empty PATCH.
- `add` takes the URL as a positional and has no `--url`; `update` has `--url` to move a
  bookmark. `--no-scrape` sends `disable_scraping=true` in the query, where linkding reads it.
- **`delete`, `archive` and `unarchive` are one request per ID.** linkding answers 204 to all
  three. The answer is `{"<done>": [ids], "failed": [{id, error}]}` with `<done>` being
  `deleted`, `archived` or `unarchived`; one failure does not abort the rest, except an
  `authError`, which would fail every remaining ID the same way and is returned as is so the
  caller gets the `fix` on stderr and a non-zero exit. Archiving an archived bookmark is a 204,
  not an error.
- `check` returns `{"bookmark", "metadata", "auto_tags"}` with `bookmark` trimmed when present
  and `metadata` passed through verbatim.
- Flags may come before or after positional arguments (`parseFlags` re-parses after each
  positional), because agents write `bookmark get 42 --full` as often as the other way round.
- `-h`/`--help`/`help` on a group (`bookmark --help`) or a leaf (`bookmark list -h`) prints the
  usage to stderr and exits 0 (`errHelp`). Every command rejects stray positionals, so a
  mistyped flag cannot pass silently.
- `LINKDING_URL` must be `http(s)://host`; requests go to `<url>/api/...`.
- Tags cannot be deleted through the API, so a smoke test that creates one leaves it behind.
