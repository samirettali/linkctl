# Public user API coverage

## Contract and compatibility

Target: **linkding v1.47.0**. Contracts were checked against its
[API routes](https://github.com/sissbruecker/linkding/blob/v1.47.0/bookmarks/api/routes.py),
[serializers](https://github.com/sissbruecker/linkding/blob/v1.47.0/bookmarks/api/serializers.py),
[search and bundle models](https://github.com/sissbruecker/linkding/blob/v1.47.0/bookmarks/models.py),
[asset service](https://github.com/sissbruecker/linkding/blob/v1.47.0/bookmarks/services/assets.py),
[API documentation](https://github.com/sissbruecker/linkding/blob/v1.47.0/docs/src/content/docs/api.md)
and upstream API tests. Routes and serializers also match upstream commit
`27b7303baf41bb28babc610ac8eaa486e1ddfab5` at implementation time.

This is a contract target, **not a claim that older deployments support every
operation**. In particular, current tags support deletion; older deployments may
not. There is no version probe, speculative mutation, silent fallback or retry.
`user profile` exposes the server's `version` when supplied. Older servers can
silently ignore unknown query parameters; verify the deployment version before
relying on newer filters or creation controls.

Errors preserve HTTP status and server details in JSON: 404 can mean an unsupported
route, missing resource, or inaccessible resource; 405 can mean an unsupported
method. Check the instance version and resource ownership rather than retrying a
mutation. 403 on uploads can mean the server disabled asset uploads. A 401 carries
the normal authentication remedy. Batch failures appear under `failed` (exit 0),
except 401, which aborts with the remedy on stderr (exit 1). Earlier successes are
not printed on an auth abort; inspect state before retrying.

## Endpoint matrix

All paths below have the `/api` prefix. Lists support `--limit`, `--offset`, and
`--limit 0` walking with the existing progress/malformed-page protections. Client
pagination computes offsets; it never follows server-provided pagination URLs.

| Public endpoint / method | CLI capability | Notes |
| --- | --- | --- |
| `GET /bookmarks/` | `bookmark list [QUERY...]` | Search, tags, unread/untagged, time filters, bundle, sort and shared/read filters |
| `GET /bookmarks/archived/` | `bookmark list --archived` | Separate collection, same filters |
| `GET /bookmarks/shared/` | `bookmark list --shared-collection [--user USERNAME]` | Shared collection across users, not the `shared` mutation flag; uses configured authentication |
| `GET /bookmarks/:id/` | `bookmark get ID` | Trimmed or `--full` |
| `GET /bookmarks/check/` | `bookmark check URL [--ignore-cache]` | Server-side metadata scrape, not client-side scraping |
| `POST /bookmarks/` | `bookmark add URL` | Duplicate guard unless `--replace`; all writable user fields |
| `PATCH /bookmarks/:id/` | `bookmark update ID` | Only explicitly supplied fields; no duplicate PUT command |
| `DELETE /bookmarks/:id/` | `bookmark delete ID...` | Explicit permanent deletion, per-ID results |
| `POST /bookmarks/:id/archive/` | `bookmark archive ID...` | Per-ID results |
| `POST /bookmarks/:id/unarchive/` | `bookmark unarchive ID...` | Per-ID results |
| `POST /bookmarks/singlefile/` | `bookmark singlefile URL --input PATH` | Multipart `url` and `file`; adds an HTML snapshot; creates a bookmark if URL is absent |
| `GET /bookmarks/:id/assets/` | `bookmark asset list BOOKMARK_ID` | Paginated |
| `GET /bookmarks/:id/assets/:asset/` | `bookmark asset get BOOKMARK_ID ASSET_ID` | Asset metadata |
| `POST /bookmarks/:id/assets/upload/` | `bookmark asset upload BOOKMARK_ID --input PATH` | Multipart `file` |
| `GET /bookmarks/:id/assets/:asset/download/` | `bookmark asset download BOOKMARK_ID ASSET_ID --output PATH` | Bytes to an explicit new file; JSON receipt on stdout |
| `DELETE /bookmarks/:id/assets/:asset/` | `bookmark asset delete BOOKMARK_ID ASSET_ID...` | Permanent asset deletion; per-ID results |
| `GET /tags/` | `tag list` | Complete objects |
| `GET /tags/:id/` | `tag get ID` | Complete object |
| `POST /tags/` | `tag create NAME` | Server returns the existing tag if its name already exists |
| `DELETE /tags/:id/` | `tag delete ID...` | Removes the tag and its bookmark associations, not bookmarks |
| `GET /bundles/` | `bundle list` | Ordered, paginated |
| `GET /bundles/:id/` | `bundle get ID` | Complete object |
| `POST /bundles/` | `bundle create --name NAME [FIELDS]` | Omitted order is assigned server-side |
| `PATCH /bundles/:id/` | `bundle update ID [FIELDS]` | Only explicitly supplied fields; no duplicate PUT command |
| `DELETE /bundles/:id/` | `bundle delete ID...` | Deletes the saved filter, not matching bookmarks; server renumbers remaining bundles |
| `GET /user/profile/` | `user profile` | Read-only user preferences, including server version when present |

Tag, bundle, asset and profile objects are already small and pass through with
unknown fields retained; `--full` is accepted on their JSON read/create/update
commands and produces the same object. Bookmark trimming is unchanged.

## Writable fields and filters

Bookmark add/update fields: `--title`, `--description`, `--notes`, repeated `--tag`,
`--clear-tags`, `--unread`/`--no-unread`, `--shared`/`--no-shared`,
`--archived`/`--no-archived`, `--date-added`, `--date-modified`. Update also accepts
`--url`; add takes a positional URL. Dates require RFC 3339 and are normalized to
UTC. Server update rules may refresh modification timestamps.

Empty string flags clear strings; omitted flags leave fields unchanged on PATCH.
`--clear-tags` sends an empty list and conflicts with `--tag` when true. Linkding
applies auto-tagging rules on update, so server rules may add tags back. Explicit
boolean values follow Go syntax: `--archived=false` sends false;
`--no-archived=false` sends true. Giving both flag names is always an error.

Add also accepts `--no-scrape` and `--no-snapshot`, sending the presence-based query
parameters `disable_scraping` and `disable_html_snapshot` only when true.
`--no-scrape` does not suppress the duplicate guard's check scrape; `--replace`
skips the guard, with its documented overwrite risk. Update has no scraping or
snapshot-control flags because the upstream update serializer does not use them.

Bookmark lists accept `--bundle ID`, `--sort` (`added_asc`, `added_desc`,
`modified_asc`, `modified_desc`, `title_asc`, `title_desc`), and
`--filter-shared off|yes|no` / `--filter-unread off|yes|no`. `off` disables that
filter, not the boolean property. `--unread` remains the convenient search keyword
and conflicts with an explicit `--filter-unread` when true. `--shared-collection`
and `--archived` are exclusive; `--user` belongs only to the shared collection.
Shared reads still use configured authentication; anonymous access is deliberately
not a second authentication mode.

Bundle fields: `--name`, `--search`, `--any-tags`, `--all-tags`, `--excluded-tags`,
`--filter-unread off|yes|no`, `--filter-shared off|yes|no`, `--order INTEGER`.
Tag fields are **space-separated strings**, not JSON arrays or repeated flags.
Empty strings clear search/tag fields; `off` clears a bundle filter. An empty
name is invalid. `--order` sets the API field directly; it does not emulate the
web UI's separate drag-and-drop reorder behavior.

## File transfers

Uploads are supported only on Unix platforms (including macOS and Linux); other
platforms fail closed with a clear unsupported-platform error before any upload
request. Other commands remain buildable. Uploads accept an existing regular file,
reject symlinks/devices/directories, spool multipart into a private temporary file,
then send one request. Opening uses nonblocking/no-follow flags and validates the
opened descriptor against the precheck, so a substituted FIFO cannot block the
open and a substituted final symlink is rejected atomically. MIME type
comes from the filename extension (otherwise `application/octet-stream`); only
the basename is sent as the filename. Spooling uses disk rather than unbounded
RAM or a pipe goroutine. Temporary files and HTTP bodies are closed/removed on
failure and success. Ensure temporary storage has room for the complete upload.
Uploads are not replayed through 307/308 redirects; configure the canonical URL.

Downloads stream into a private temporary sibling, then publish atomically with
a no-clobber hard link. An existing file, directory or symlink at the output path
is never overwritten, including one created during transfer. The parent directory
must exist and its filesystem must support hard links. A failed or truncated
transfer leaves no output or temporary file. Completed files have mode 0600.
Server filenames and `Content-Disposition` never select local paths. JSON stdout
contains `bookmark`, `id`, `output`, and `bytes`; binary data never goes to stdout.
No implicit directory creation, overwrite switch, stdin or stdout binary mode.

SingleFile uploads return the upstream `message` object. The server creates a
bookmark when the URL is missing, can scrape its metadata, adds a snapshot and
updates the latest-snapshot pointer. It does not replace bookmark notes/tags or
implicitly delete existing assets. A timeout does not prove a mutation failed;
inspect bookmark/assets before any manual retry.

## Intentional exclusions

- Administration: accounts/users, API-token management, permissions and server
  settings. `user profile` is read-only; there is no settings mutation command.
- Web-only/internal routes and UI automation: bulk UI actions, import/export,
  preferences editing, tag rename, scraping triggers and bundle drag reordering
  are not invented as public API endpoints. Existing per-ID batch commands and
  JSON collection reads cover their public API counterparts where available.
- Duplicate routes: PUT would duplicate PATCH functionality for these serializers;
  API root discovery/OPTIONS do not add user-facing capabilities.
- No automated cleanup, implicit deletion or automatic mutation retries.

For Samir's library, **unarchived is the read/watch-later queue; archived is the
functional collection** (tools, documentation, product pages). Archived never
means “consumed queue”. A functional link in the queue is misfiled: archive it,
not delete it. Durable rereading material belongs in Obsidian `Clippings/`.

## Verification

Tests use `httptest` and fake tokens only, including real-main CLI subprocesses.
Coverage includes each new command, methods/paths/payloads, auth and HTTP errors,
malformed objects/pages, paging, PATCH omission/clearing, explicit booleans,
batches, binary multipart/downloads, no-clobber publication, truncated transfers,
resource cleanup and redirect credential/method protections. No live deployment,
vault, installation or release is required for verification.
