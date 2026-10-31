# HTTP API and progress

See [library creation and opening](library-opening.md) for creating native empty
libraries with `library.create` and opening one or more explicit paths.

See [audio duplicates, stable references, and sync](fingerprints.md) for library registration, fingerprint scans, reference-based batch selection, and audio reuse during sync.

## Runtime checks and mutation protection

`GET /v1/system/check` returns runtime facts without selecting or opening a library. Use `?checks=engine_desktop` to select checks, or omit it for all supported checks. Repeated/comma-separated check IDs are accepted and deduplicated. Unknown IDs return `400`. The equivalent job operation is `system.check` with `{"checks":["engine_desktop"]}`; the CLI is `engine-cli system check --json` (`--checks` selects checks).

The response contains `checked_at` (UTC timestamp), `platform`, and a `checks` object keyed by check ID. `engine_desktop` contains `status` (`ok` or `error`), `running` (boolean when known, otherwise `null`), and `pids` (matching process IDs, always an array). Failed checks also contain `error: {code, message}`. A completed HTTP check returns `200` even when a particular check fails; inspect its status rather than treating HTTP success as “safe to edit.”

Desktop detection uses native process snapshots on macOS and Windows and matches the Engine DJ executable name, not command-line substrings. Linux and other platforms always return `status: "ok"`, `running: false`, and empty `pids` without inspecting processes or checking Engine DJ platform availability. The check is an observation, not a library lock or a guarantee that Engine DJ will not start later.

Every CLI/API library mutation obtains a fresh desktop check before backups or undo-history writes. Running or unknown desktop state blocks the mutation; there is no bypass flag. API jobs report `engine_desktop_running` or `engine_desktop_unknown` as structured error codes. Dry runs and read-only operations remain available. The synchronous check endpoint remains available while a mutation job is running.

## Batch entity details

Fetch a UI selection in one request:

```text
GET /v1/tracks?ids=12,34,56
GET /v1/playlists?ids=7,9
```

Repeated `ids` query parameters are also accepted. Equivalent operations are `track.inspect` and `playlist.inspect` with `{"ids":[12,34,56]}`. Equivalent CLI commands are `engine-cli track inspect 12 34 56 --json` and `engine-cli playlist inspect 7 9 --json`.

Each request accepts 1–500 positive numeric library-scoped IDs. Results are `{"items":[{"id":12,"data":{...}}]}` in request order, including repeated IDs. Missing entities return `{"id":34,"data":null,"error":{"code":"not_found","message":"..."}}` without discarding other results. Invalid IDs reject the request; database/query failures fail the whole request. Playlist data includes its track details; track fields are shared with the single-track metadata endpoint. Reads do not create backups or history. `playlist.inspect` now takes an `ids` array instead of `identifier` and returns the batch envelope even for one item.

Use the local API to control an Engine DJ library from another CLI or a web UI. Close Engine DJ before changing the library.

```sh
engine-cli --lib-dir "$HOME/Music/Engine Library" api serve
```

The server prints its URL and listens on `127.0.0.1:8787`. Use `--port 0` to select an available port. For a browser UI on another origin, allow that exact origin:

```sh
engine-cli api serve --allow-origin http://localhost:3000
```

Other browser origins are rejected. Successful preflight responses allow `GET`, `POST`, `PUT`, `PATCH`, and `OPTIONS`, with `Content-Type` and `Last-Event-ID` request headers. An allowed browser origin can update library names and visibility through `PATCH /v1/library-preferences`. The server has no authentication and is intended for local applications. Global `--no-backup` disables automatic safety snapshots; it does not disable undo history.

The server can start when the configured library directory is absent so clients can discover libraries first. Submit `POST /v1/operations/library.discover` with `{}` to check Music and attached local drives on macOS/Windows, or `{"paths":["/Volumes/My Drive"]}` to check only specified roots. Each root is checked directly and at `Engine Library/` and `Music/Engine Library/`; discovery is nonrecursive and recognizes current `Database2/m.db` files. Legacy root-level databases are ignored. Windows uses the redirected Music folder and fixed/removable drive letters. Automatic scans exclude network drives.

The job result contains `libraries` and `warnings` arrays. Each library has `name` (for example `Local library` or `Drive: PRIME-128GB`), `source` (`local`, `drive`, or `folder`), and absolute `path` and `database` fields. Use `name` as a picker label and `path` as secondary text; names are not unique identifiers. Warnings contain `path` and `message`. Discovery does not select a library, create history, or modify databases. Start the server with the chosen `--lib-dir` to operate on it.

## Library state and file lifecycle

`api serve --state-db <path>` selects the machine-local SQLite state database; the default is `$XDG_STATE_HOME/engine-cli/state.db`, falling back to `$HOME/.local/state/engine-cli/state.db`. Keep it outside managed library folders. It stores known paths, cosmetic names, hidden entries, and tombstones for deleted or moved paths. It never replaces Engine's collection databases.

`GET /v1/library-preferences` returns an array of `{path,name,hidden,retired,instance,engine_uuid,registration}`, with identity fields as described in [library creation and opening](library-opening.md); paths that are not currently readable report `registration: "unavailable"`. Clients resolve a remembered `instance` to its path here and reopen it with `POST /v1/libraries` after a server restart. `PATCH /v1/library-preferences` accepts an absolute `path` and optional `name` and `hidden`. The corresponding catalog operations are `library.preferences.list` and `library.preferences.update`, with effect `state`. Hiding changes only sidebar visibility: discovery still reports the library, its media remains intact, and it still participates in shared-media checks. Updating a cosmetic name does not rename any folder or Engine database.

`library.delete`, `library.move`, and `library.media.aggregate` accept an absolute `path`. First submit `dry_run:true`. Their result includes `action`, `path`, optional `destination`, `applicable`, `files`, `affected_libraries`, `unavailable_libraries`, `warnings`, and `plan_token`. Each file supplies its source, optional destination, action, content hash, missing status, and referencing known libraries. Preview does not create snapshots or history. Apply with `dry_run:false` and the reviewed `plan_token`; changes to files, collections, or options invalidate the token. Normal Engine Desktop protection, safety snapshots, and undo apply to execution.

- Delete additionally requires `confirm_path` equal to the complete canonical preview path. It removes `.db` files directly in the library root and `Database2/`, plus files under `Artwork/`. Referenced audio is retained unless `delete_media:true`; `keep_shared:true` retains media referenced by another known library. Recovery history, staging data, other files, and unreferenced audio remain in place. This is not recursive folder deletion.
- Move requires `destination`, an unused path with an existing parent. It moves the complete folder, including its internal media and portable history, preserving collection identity. External audio remains in place. Other libraries referencing media inside the moved folder can lose access; preview lists them. Undo relocates the folder back and refuses an occupied original location, including with force.
- Aggregate uses `mode:"copy"|"move"` and `shared_mode:"copy"|"move"` (both default to copy). It gathers referenced external audio into deterministic paths under `Media/`, then updates this collection's track paths without re-encoding. When moving with `shared_mode:"copy"`, shared files are copied while private files move. Other collections' track paths are never silently rewritten.

Shared-file checks cover previously opened or discovered libraries, including hidden ones. Unavailable known libraries are reported; protection conservatively keeps or copies candidate files, while unsafe deletion/moving is blocked. Libraries unknown to this service cannot be checked. Deletion and relocation retire the old registry entry; successful undo reconciles its visibility.

## Metadata cleanup suggestions

`track.suggest` is a read-only job operation shared with the CLI:

```sh
engine-cli track suggest 12 34 --harness codex --json
engine-cli track suggest --playlists 7,9 --harness claude --json
```

Submit `POST /v1/operations/track.suggest` with `{"tracks":["12","34"],"harness":"codex"}` or
`{"playlists":["7","9"],"harness":"claude"}`. Library-scoped endpoints use the same operation and
parameters. Each request accepts exactly one selection array with 1–500 positive
numeric IDs or opaque `eng:<instance>:<kind>:<entity>` references, encoded as strings.
Names, titles, filenames, and paths are not selectors.

Playlist selection uses direct members, without recursively including descendants.
Specify descendant playlist IDs explicitly when needed. Tracks are deduplicated in
first-seen order: selector order, then each playlist's membership order. Every
selected playlist member is processed, even when the total exceeds 500 tracks.
Missing selectors or database failures reject the whole selection before any model
request. Empty playlists return `{"items":[],"processed":0,"total":0}` without launching a harness.

Install `agent-ask-cli` 1.0.0+ with `engine-cli tool upgrade agent-ask-cli`,
or set `auto_install:true` in the request (`--auto-install` on the CLI).
Claude Code or Codex must already be installed and authenticated on the server computer.
Engine uses the existing harness login without separate provider API keys.

The cleanup request accepts the same native options as `agent-ask-cli`:

| HTTP field | CLI flag | Meaning |
| --- | --- | --- |
| `harness` | `--harness` | Required `claude` or `codex`. |
| `model` | `--model` | Optional native model; omitted uses harness defaults. |
| `effort` | `--effort` | Optional native reasoning effort. Unsupported combinations fail explicitly. |
| `thinking` | `--thinking` | `auto` (default), `on`, or `off`; support depends on harness/model. |
| `harness_config_mode` | `--harness-config-mode` | `inherit` (default) or `clean` to suppress personal/project customization while retaining authentication. |
| `harness_config_file` | `--harness-config-file` | Native Claude settings JSON or Codex configuration TOML file. |
| `harness_home_dir` | `--harness-home-dir` | Alternate native configuration/state/authentication home. |
| `timeout` | `--timeout` | Positive duration per batch, default `5m`. |
| `auto_install` | `--auto-install` | Install or update the managed wrapper when needed; defaults to false. |

These are per-request options, including on library-scoped endpoints. Configuration
paths refer to files on the server computer. Engine runs each fresh harness session
in a disposable OS temporary directory and passes metadata through stdin. Native
configuration, authentication, tool calls and internal retries are handled by
agent-ask-cli and the selected harness. Engine supplies the cleanup prompt and
output schema; callers select tracks and harness settings.

```json
{"tracks":["12","34"],"harness":"codex","effort":"low","harness_config_mode":"clean","timeout":"3m","auto_install":true}
```

Only the selected title, artist, album, filename, and library-scoped track ID are
sent to the model. Audio, artwork, full track paths, and opaque references are not
sent. The cleanup prompt asks for conservative corrections to duplicated artist
prefixes, clearly embedded artists, broken encoding, HTML entities, and whitespace,
while preserving intentional spelling, accents, featured artists, and mix labels.
Metadata strings are treated as data. Model judgments still require user review.

Each completed batch is published immediately in deterministic selection order.
CLI text output prints its items immediately; `--json` writes one newline-delimited
JSON record per batch. An example single-batch record is:

```json
{"items":[{"id":12,"current":{"title":"Artist - Song","artist":"Artist"},"suggestion":{"title":"Song","artist":"Artist","reason":"Removed duplicated artist prefix."}},{"id":34,"current":{"title":"Clean title","artist":null},"suggestion":null}],"processed":2,"total":2}
```

Registered libraries also return each track's stable `ref`. Original database nulls
are retained in `current`; `suggestion:null` means no change was proposed. Each
proposal includes both proposed fields and an explanation. Schema validation and
application checks reject missing, duplicate, or unexpected IDs, incomplete model
responses, unexplained changes, and clearing an existing nonempty title or artist.

Requests use agent-ask-cli structured output in sequential batches of at most
25 tracks and 64 KiB of serialized metadata. A single oversized record fails
selection without truncation. Each batch has a five-minute deadline by default,
overridden by `timeout`. Cancellation interrupts the wrapper so it can stop the
harness. A failed or cancelled batch stops further calls and retains every
previously completed batch. CLI output already delivered remains valid; the
command exits nonzero. HTTP jobs finish as `failed` or `cancelled` with the stable
error code and all completed suggestions still present in `result`.

For web clients, subscribe to `GET /v1/jobs/{id}/events`. Each validated batch
produces an SSE `event: progress` whose data has `type:"result"`,
`phase:"track.suggest"`, and `result:{items:[...],processed:25,total:60}`.
`items` contains only that batch; `processed` is cumulative and `total` covers
the complete deduplicated selection. Append these items as they arrive. Events
retain the shared `job_id`, increasing `sequence`, and monotonic `percent`.
Progress advances after each batch; 100 is reserved for successful completion.

`GET /v1/jobs/{id}` exposes the accumulated `result` after the first batch while the job is running,
as well as after success, failure, or cancellation. It contains every completed
item and the same counts. Failed or unfinished batches do not contribute items
or increment `processed`. An empty playlist still succeeds with zero counts.
There is no repeated cumulative result on the CLI or SSE stream at completion;
the terminal event reports completion or failure, and the job status retains
the accumulated report. Normal job retention rules apply.

Use `Last-Event-ID` to resume SSE without replaying batches. If the event cursor
expires, fetch job status to recover every completed suggestion, then reconnect
using its `progress.sequence`; completed results survive event-buffer expiry.
If a batch's `processed` count exceeds the number of items accumulated locally,
recover from job status instead of treating the replayed tail as complete.

Suggestions create no library changes, backups, or history and remain available while
Engine DJ is running. To apply a reviewed correction, use `track.update` with explicit
fields; normal mutation guards, snapshots, and undo apply to that separate operation.

## Submit, observe, cancel

| Request | Response |
| --- | --- |
| `GET /v1/operations` | Operation names, parameter types, required fields, and whether each operation changes the library |
| `POST /v1/operations/{operation}` | `202` with a job object and a `Location` header pointing to its status |
| `GET /v1/jobs/{id}` | Current state, latest progress, and result or error |
| `GET /v1/jobs/{id}/events` | Server-sent events, including retained events followed by live events |
| `POST /v1/jobs/{id}/cancel` | `202` with current status; requests cancellation |
| `GET /v1/tracks/{id}` | Track metadata with JSON values and nulls |
| `GET /v1/tracks/{id}/audio` | Original local audio; supports `HEAD` and HTTP byte ranges for seeking |
| `GET /v1/tracks/{id}/beatgrid` | Saved Engine beat-grid anchors, or `{"grid":null}` when analysis is absent |
| `GET /v1/tracks/{id}/performance` | Read-only saved hot cues and loops with native slots, names, colors, and positions in seconds |
| `GET /v1/sessions` | Played sessions from Engine's play history, newest first; see [Play history](#play-history) |
| `GET /v1/sessions/{id}` | One played session with its plays in recorded order |

Every operation, including reads, runs as a job. Submit a JSON object with `Content-Type: application/json`; use `{}` when there are no parameters. Unknown fields and missing required parameters are rejected before the job starts. Requests are limited to 1 MiB.

```sh
curl -sS http://127.0.0.1:8787/v1/operations

curl -sS -H 'Content-Type: application/json' \
  -d '{"identifier":"42","query":"Artist Track extended mix","clear_timing":true}' \
  http://127.0.0.1:8787/v1/operations/track.upgrade
```

Use the returned job `id` in the following commands:

```sh
job_id=REPLACE_WITH_RETURNED_ID
curl -N "http://127.0.0.1:8787/v1/jobs/$job_id/events"
curl -sS "http://127.0.0.1:8787/v1/jobs/$job_id"
curl -sS -X POST "http://127.0.0.1:8787/v1/jobs/$job_id/cancel"
```

Jobs move from `queued` to `running`, then `succeeded`, `failed`, or `cancelled`. Read jobs run concurrently with each other and with non-collection writes, such as staging fetches and tool installs.

All writes run one at a time across opened libraries, including `library.backup`, `migrate.export`, and requests with `auto_install:true`. Backup and export retain their catalog `read` effect because they leave the source collection unchanged and create no source-library undo history.

Collection writes, restore, sync, and filesystem writes whose collection involvement is not yet resolved exclude read jobs. Bound parameters determine scheduling: a fetch with `import:true` excludes reads, while fetching only into Staging permits them. A pending collection writer blocks new reads until it finishes; jobs waiting for access remain `queued` and can be cancelled.

Engine database connections wait up to five seconds for SQLite locks. Direct track, playlist, artwork, beat-grid, staging, and library-listing reads, library opening, and preference responses share the collection barrier with jobs, so they cannot overlap restoration or other collection writes. Audio requests release that protection after resolving and opening the file, before streaming to the client; playback does not hold up collection writes. Waiting reads observe request cancellation. Operation catalog, job status/events, and system checks remain available while collection access is blocked.

Backup restoration enumerates every database and artwork folder before changing live files. Missing optional folders are allowed when the snapshot contains a database; unreadable folders and snapshots without databases fail with `backup_unavailable`, leaving the live files unchanged during validation. Later copy failures still fail the operation and retain the normal undo guarantees.

A disconnected client does not cancel its job. Cancellation is cooperative and can leave completed changes; use library history to inspect and undo changes. Shutdown requests cancellation and waits for workers.

The job's `result` depends on the operation. Track fields use JSON values and `null` for missing metadata. Collection tracks include `hasArtwork`: true when inline artwork bytes or a nonempty regular cache file exists, false when absent (including dangling artwork IDs), and null when availability cannot be determined. Collection lists, single/batch inspection, and playlist tracks share this field. Collection tracks also include `playlistIds`, described under [Library, playlist, and track operations](#library-playlist-and-track-operations), which also lists mutation results; those report changed IDs and paths rather than track objects. Availability checks read artwork catalogue metadata in one batch and reuse filesystem observations for shared covers; they do not load image bytes. A mutation can return a `history_id` identifying its undo record. When a terminal event becomes visible, the final job result and state are also available.

## Metadata refresh outcomes

`library.refresh` refreshes embedded tags and file sizes and scans for unindexed
audio. Missing files are counted; unreadable or malformed embedded tags leave
their metadata unchanged while file-size updates can still proceed. Unexpected
file inspection, database write, and directory enumeration errors stop the job.
Stored absolute audio paths are refreshed at their original location, including
external drives; relative paths resolve from the library directory. Refresh
preserves stored paths and scans only the library directory for unindexed audio.

A failed or cancelled refresh can retain a partial `result` with `totalDbTracks`,
`updatedCount`, `missingCount`, `unindexedCount`, and any `unindexedPaths` found so
far. `updatedCount` counts saved updates only; the other counts can be incomplete.
The job remains failed or cancelled. Its `history_id`, when present, identifies
the saved changes for review and `library.undo`; partial results do not authorize
automatic retry. Initial failures before track enumeration can have no result.

The equivalent `engine-cli library refresh` command prints these partial counts
before returning an error. Agent output uses `status: incomplete` and
`error_code`; successful runs retain `status: complete`. Both modes include the
undo `operation_id` when changes were saved.

## Failure codes

Request and job errors use `{code,message}`. The code is the stable consumer contract; `message` is diagnostic text and must not be displayed directly in a user interface. Map known codes to user-friendly guidance and use a safe generic message for unknown codes. Do the same for partial failures inside results and upstream progress details.

Progress events, acquisition results (including per-track, tag-file, and dependency failures), staged items, import and migration failures, and newly recorded history entries retain string-valued `error` and add `error_code`. Saved migration state pairs `errorMsg` with `error_code`. Successful records omit the code. Persisted records written before codes were introduced may lack it; consumers must handle that as an unclassified failure without displaying the diagnostic text.

| Code | Meaning and recovery |
| --- | --- |
| `library_not_found` | The configured library database is absent. Add or connect the library on the server computer, then refresh. No database is created by this read. |
| `library_refresh_write_failed` | Refresh stopped because a track update could not be saved. Check library access, then review the partial result and history before retrying or undoing saved changes. |
| `library_refresh_stat_failed` | Refresh could not inspect an audio file. Check its drive and access permissions, then review the partial result before retrying. This is not counted as a missing file. |
| `library_refresh_scan_failed` | Refresh could not finish scanning the library folder. Restore access and review the partial result before retrying; earlier track updates remain saved. |
| `library_unavailable` | The library could not be opened. Check the selected library and drive. |
| `playlist_not_empty` | The folder contains ordinary child playlists or saved Smartlists. Inspect its contents before explicitly choosing recursive removal (`force:true` or `playlist rm --force`). Nothing is removed by this refusal. |
| `smartlist_repair_stale` | The collection or retained history no longer matches the reviewed repair token. Preview again and review the current named actions before applying. |
| `smartlist_repair_ambiguous` | Native saved-playlist identity or history is contradictory. Preserve the collection and history for investigation; names alone cannot select a repair target. |
| `smartlist_repair_pending` | An interrupted saved-playlist repair has retained publication intent. Close Desktop and explicitly run `library.smartlist-repair.recover`; preserve all evidence if recovery validation refuses. |
| `permission_denied` | File access was denied. Check permissions on the server computer. |
| `resource_not_found` | A required file is missing. Restore access or choose another file. |
| `backup_unavailable` | A backup folder could not be enumerated or the snapshot contains no databases. Check the backup drive and folder permissions before retrying. No live files were changed by the failed validation. |
| `playlist_import_unavailable` | The server-local playlist file cannot be opened. Check its path and permissions. |
| `playlist_import_format` | The source is not a supported local M3U/M3U8 playlist or exceeds its limits. HLS playlists and remote media URLs are unsupported. |
| `playlist_import_encoding` | The selected encoding cannot decode this playlist. M3U8 requires UTF-8; choose an explicit supported encoding for M3U. |
| `playlist_import_empty` | The file contains no media entries. Choose a populated playlist. |
| `playlist_import_target` | The new name or parent is invalid, or that parent already contains a playlist with this name. |
| `playlist_import_stale` | The reviewed plan no longer matches the source, selected options, or collection. Review again before applying. |
| `playlist_import_unresolved` | No tracks match, or unresolved entries remain without explicit skip permission. Resolve them or review with `skip_missing:true`. |
| `rekordbox_import_invalid` | Choose a server-local Rekordbox XML file, a new root name, and supported omission/reuse policies. |
| `rekordbox_import_media` | An import source is not a regular audio file. Choose accessible local media. |
| `rekordbox_import_target` | Choose an unused name for the new import root in a destination library with native identity. Existing roots are never replaced. |
| `rekordbox_import_stale` | The XML, audio observation, source mappings, destination or reviewed options changed. Review again. |
| `rekordbox_import_review_required` | Resolve blocked sources or explicitly review the required skip, reuse and source-only policies before applying. |
| `play_history_unavailable` | The library has no play history database yet. Engine DJ creates it when tracks are played; listing sessions reports `available:false` instead. |
| `play_history_unreadable` | The play history database exists but cannot be read. Check the library drive and permissions; the history is never repaired or changed. |
| `invalid_session` | The session ID is not a positive number. |
| `session_not_found` | The played session is no longer in the history. Refresh the session list. |
| `session_import_target` | The new name or parent is invalid, or that parent already contains a playlist with the explicitly chosen name. |
| `session_import_stale` | The reviewed plan no longer matches the session, selected options, or collection. Review again before applying. |
| `session_import_unresolved` | No played track is in the library, or plays of missing tracks remain without explicit skip permission. Review with `skip_missing:true`. |
| `playlist_export_target` | Choose an existing playlist and a new absolute destination in an existing server folder. |
| `playlist_export_exists` | The destination already exists, including a directory or symbolic link. Choose a new filename; nothing is overwritten. |
| `playlist_export_format` | The destination is not M3U/M3U8 or a media path cannot be represented unambiguously. |
| `playlist_export_encoding` | The selected encoding cannot represent the metadata or media paths. M3U8 requires UTF-8. |
| `playlist_export_order` | Saved membership links are malformed or an origin resolves ambiguously. Repair the playlist in Engine before exporting. |
| `playlist_membership_order_invalid` | Saved playlist membership links are malformed or a track origin resolves ambiguously. Repair the playlist in Engine before browsing or changing its order. |
| `playlist_reorder_target` | The selected playlist no longer exists. Refresh the playlists before choosing the target again. |
| `playlist_reorder_view` | The shown rows, selected rows, direction or drop gap no longer match the saved playlist order. Refresh and select the tracks again. |
| `playlist_reorder_stale` | The reviewed library, playlist, membership identities or move changed. Review the current order again before applying. |
| `playlist_reorder_unchanged` | The reviewed move does not change the order. Choose a different position for the selected tracks. |
| `playlist_export_unavailable` | Unavailable memberships remain without explicit reviewed skip permission. |
| `playlist_export_stale` | The reviewed options, destination, membership order, metadata, or media availability changed. Review again. |
| `playlist_export_write` | Creating, writing, syncing, or closing the new file failed. Inspect the destination before retrying; cleanup is best-effort. |
| `tool_unavailable` | A required managed tool is not installed or cannot run. Install it with `tool.upgrade`. |
| `tool_outdated` | An installed managed tool is older than the release providing Engine's machine interface, reports an unrecognized version, or lacks that interface. Update it with `tool.upgrade`. |
| `tool_install_failed` | Downloading or installing a managed tool release failed, through `tool.upgrade` or `auto_install`. Check the connection and retry. |
| `tool_failed` | A dependent music tool reported a failure, including partial result failures. Check the tool and retry. |
| `batch_failed` | Every file in a multi-file media request failed. Each entry's `error_code` explains its file. |
| `suggestion_selection_failed` | A selected entity is missing, unreadable, or has oversized metadata. Refresh the selection before retrying. No model result is applied. |
| `harness_unavailable` | The selected Claude Code or Codex executable is unavailable; install and authenticate it on the server computer. |
| `unsupported_option` | The harness/model does not support a requested option. Choose a supported setting. |
| `configuration_failed` | Native harness configuration could not be prepared. Check the supplied configuration paths. |
| `harness_failed` | The native harness call failed. Check its login, configuration, and connectivity. |
| `invalid_input`, `invalid_schema`, `input_invalid` | agent-ask-cli rejected the supplied data or schemas. These are generated by Engine; report persistent failures. |
| `output_invalid` | agent-ask-cli rejected the harness response, or Engine received an invalid wrapper result. Retry or choose another model. |
| `output_failed` | The wrapper or CLI could not write a batch result. Check the output destination; results already delivered remain valid. |
| `llm_response_invalid` | A batch failed identity or metadata checks. Completed earlier suggestions remain available; review them before retrying unfinished work. |
| `timed_out` | The operation exceeded its deadline. Check the connection and retry. |
| `cancelled` | Work was cancelled. Completed changes may remain. |
| `operation_failed` | Unclassified failure. Inspect current state before retrying. |
| `invalid_response` | Engine could not encode a response. Refresh or update Engine. |
| `no_beat` | Analysis found no steady beat, as in ambient or spoken audio. Set the grid with tap tempo instead. |
| `no_key` | Analysis found no musical key, as in silent or percussion-only audio. Set the key manually. |
| `audio_too_short` | The audio is shorter than analysis needs. |
| `audio_unavailable` | The track's audio file could not be read or decoded. Check the file and FFmpeg on the server computer. |
| `analysis_failed` | Analysis produced no usable result. Retry or set the value manually. |
| `beatgrid_missing` | The track has no saved beat grid to edit. Analyze its beat grid first. |
| `beatgrid_invalid` | A supplied or saved grid is inconsistent. Start again from the saved grid. |
| `beatgrid_edit_invalid` | The edit or its fields are invalid. Choose a supported edit and values. |
| `beatgrid_tempo_range` | The edit would leave a tempo outside 30–250 BPM. |
| `beatgrid_half_unaligned` | A tempo marker sits between the beats of the halved tempo. Move or remove it before halving. |
| `beatgrid_marker_exists` | A tempo marker already sits on that beat. Choose another beat. |
| `beatgrid_locked` | Engine DJ has locked this beat grid. Unlock it in Engine DJ before editing. |

Existing request validation, origin protection, missing-entity, media, and desktop-check codes keep their meanings. Job status and its terminal progress event report the same failure code. Error classification follows wrapped Go error causes, not diagnostic message strings.

## Library sync previews

Submit `library.sync` with explicit library paths and exactly one scope:

```json
{
  "source": "/Music/Engine Library",
  "destination": "/Volumes/DJ_USB/Engine Library",
  "scope": {"playlist_ids": [42]},
  "dry_run": true,
  "mirror": false,
  "conflict": "error",
  "media": "changed"
}
```

Use `{"root":true}` for whole-library scope, including unassigned tracks, smartlist definitions, and the prepare queue. Playlist IDs refer to the source. The playlist scope includes descendants; ancestors are copied only as needed for hierarchy. For existing destination playlists, optional `playlist_map` entries contain numeric `source_id` and `destination_id`. No names are used to match entities. Both libraries must already exist and use verified schema 3.0.2 with distinct UUIDs.

The preview result contains `applicable`, `plan_token`, `summary`, `changes`, `conflicts`, and `warnings`. Each change identifies its entity type, stable identity, action, and available source/destination numeric IDs; labels are display-only. The summary reports track/playlist additions, updates and removals, file-copy counts, bytes to copy/preserve, available bytes, and estimated required space including database snapshots. A successful preview job may have `applicable:false`: show its conflicts before allowing application. Every conflict, and every change with a reason, carries a stable `code` beside its human-readable `reason`; clients explain conflicts from the code because reasons can include file system details. Conflict codes: `sync_insufficient_space`, `sync_audio_unverified`, `sync_audio_ambiguous`, `sync_source_track_missing`, `sync_source_audio_missing`, `sync_diverged`, `sync_destination_changed`, `sync_destination_audio_required`, `sync_destination_audio_shared`, `sync_destination_outside_library`, `sync_destination_path_taken`, `sync_destination_reserved`, `sync_destination_escapes`, `sync_destination_symlink`, `sync_destination_unreadable`, `sync_playlist_track_unresolved`, and `sync_playlist_title_conflict`. Reused destination audio is reported as a `reuse` change with code `sync_audio_reused`.

Apply by submitting the same request with `dry_run:false` and the returned `plan_token`. An outdated token fails before backups or mutation. Apply recomputes the comparison even without a token. Dry runs never create backups, history, media, or sync-state files. Unchanged repeated applications return no operation ID when the baseline is current.

Default `conflict:"error"` refuses divergent edits. Explicit `source` or `destination` chooses the winning side of a conflict. Destination-only changes remain unless mirroring requires reconciliation. Default `media:"changed"` copies missing/changed audio; `none` requires matching existing audio and still permits artwork transfer. `mirror:true` removes entries absent from the selected source scope, preserving tracks referenced by kept playlists. Reverse the endpoints to sync back; USB uses the same behavior as any other library path.

The applied result's `operation_id` and job's `history_id` identify undo history in the destination. Submit `library.undo` with `{"library":"/Volumes/DJ_USB/Engine Library","id":"<operation_id>"}` to undo there, or `library.history` with the same `library` field to list its operations. The server's configured library remains unchanged. Interrupted operations retain recovery data; undo a pending operation with `force:true` after inspecting it. Do not run Engine DJ during sync application or recovery.

## Saved beat grids

The beat-grid response contains `grid.sampleRate`, `grid.bpm`, and `grid.anchors`. Each anchor has `sample` (sample position, which can be negative for preroll), `beat` (zero-based beat index), and `beatsUntilNext` (the outgoing segment's beat count, `0` on the final anchor). Interpolate the interval between adjacent anchors using `beatsUntilNext`, then divide sample positions by `sampleRate` for seconds. Beats whose index is a multiple of four start bars. The last anchor ends the preceding segment. `bpm` is the whole-grid tempo Engine reports: the beats spanned divided by the grid's duration.

The API reads the adjusted grid Engine plays and edits from `PerformanceData.beatData`; it does not manufacture a grid from a track's BPM. Missing analysis, and grids with fewer than two anchors, return `{"grid":null}`. Invalid compressed payloads or inconsistent anchors return `422` with code `invalid_beatgrid`; decoded payloads are limited to 8 MiB. Encoding and decoding use `djtools` v0.2.0's native `BeatData` codec, which writes the full header, both grids, and the trailing source and reference-sample fields.

## Track analysis

`track.analyze` analyzes 1–500 `identifiers` from their audio and writes the selected results: `bpm`, `key`, and `grid` booleans, at least one required. `missing_only:true` keeps a BPM (`bpmAnalyzed` above zero), a key (zero or above), or a usable saved grid a track already has. Tracks with `isBeatGridLocked` keep their grids. `bpm_range` folds tempos into `58-115`, `68-135`, `78-155`, `88-175` (default), or `98-195`.

Audio decodes once through FFmpeg as 22.05 kHz mono. Tempo candidates come from `tactus` and key candidates from `mixedkey`, each over 30-second windows. The beat period and phase are fitted to a spectral-flux onset envelope of the whole track; the bar phase is the one whose beats bring the most spectral change. Windows that agree on a different tempo for at least a minute become tempo-change anchors at the change point between the two fitted grids. Analyzed grids follow Engine's analyzer layout: one anchor a bar before the first downbeat, one per tempo change, and a final anchor at least one second past the end of the audio, written to both the default and adjusted grids so Engine's reset returns to the analysis.

The grid keeps a saved grid's sample rate, because saved cues and loops are positioned with it, and otherwise uses the sample rate Engine recorded in `trackData` or the file's own. BPM analysis stores the whole-grid tempo in `bpmAnalyzed` and its rounded value in `bpm`. Writing a grid stores the same tempo with it, as Engine's analyzer does, so a grid never disagrees with the track's BPM; when `missing_only` would have kept a BPM, the replaced value is reported as written. Key analysis stores Engine's key index in `Track.key` and, when Engine has written `trackData`, in its key field, because Engine reads the key from track data when it loads a track; unreadable track data is left for Engine to rebuild. Analysis never sets `isAnalyzed` or writes overview waveforms, which Engine builds when it loads or analyzes the track. Unselected fields are untouched.

Up to four tracks analyze concurrently. Each track's results are written in its own transaction as soon as they are ready, after rechecking that Engine DJ Desktop is closed; when Desktop starts, the job fails before its next write. One safety snapshot and one undo record cover every track written, including the tracks written before a failure or cancellation; a failed or cancelled job's result still lists the tracks it wrote. Per-track analysis failures are reported in the result rather than failing the job:

```json
{"tracks":[{"track_id":5,"title":"Night Drive","bpm":{"status":"written","bpm":124.0065},"key":{"status":"written","key":23},"grid":{"status":"written","bpm":124.0065}},{"track_id":6,"title":"Ambient Intro","bpm":{"status":"failed","error":{"code":"no_beat","message":"…"}},"grid":{"status":"locked"}}]}
```

Tracks appear in request order after aliases are deduplicated, with the display title read before analysis: the title, or the filename without its extension. Only selected analyses appear. `status` is `written`, `kept` (`missing_only` found a value), `locked`, or `failed` with an `error` code: `no_beat`, `no_key`, `audio_too_short`, `audio_unavailable`, or `analysis_failed`. Written grids report `tempo_markers` when the analysis found tempo changes.

`track.add` and `staging.import` accept `analyze:true` and an optional `bpm_range` to analyze BPM, key, and beat grid of the tracks they add, in the second half of the job's progress. File-tag BPMs are replaced so the stored tempo matches the grid. Analysis failures are reported per track as `analysis` on each added or imported entry and never undo the import; Desktop starting during analysis fails the job, keeping the imports, their undo record, and the result entries for every import.

## Beat-grid edits

`POST /v1/tracks/{id}/beatgrid/edits` computes one edit for review without writing the library. It shares the collection read barrier, remains available while Desktop is running, and is available under `/v1/libraries/{key}`. The body is an edit with an optional draft: `anchors` (an adjusted grid from an earlier response) chains edits, and omitting it edits the saved grid. Times are seconds on the track's timeline; `marker` indexes the grid's anchors from 0.

| `edit` | Fields | Effect |
| --- | --- | --- |
| `shift` | `seconds` (nonzero, at most 60 either way) | Moves every beat, including fractional offsets |
| `bar_shift` | `beats` (±1 to ±3) | Moves every downbeat later (positive) or earlier, keeping beat positions |
| `downbeat` | `seconds` | Makes the beat nearest the time start a bar |
| `scale` | `factor` (`2` or `0.5`) | Doubles or halves every segment's tempo from the same anchors; doubling keeps every downbeat and halving every other one. Halving fails with `beatgrid_half_unaligned` when a tempo marker sits between the halved beats |
| `tempo` | `bpm`, `marker?` | Sets the BPM of the segment starting at the marker (default 0); later markers move with the segment's end |
| `marker_add` | `seconds` | Adds a tempo marker on the nearest beat, keeping both tempos |
| `marker_move` | `marker`, `seconds` | Moves an inner marker between its neighbours; both segments keep their beat counts |
| `marker_remove` | `marker` | Joins the segments either side of an inner marker at their average tempo |
| `tap` | `taps` (4–64 increasing times) | Fits the tempo and beat phase to taps. A pause over two seconds restarts the sequence, intervals more than 30% from the median are dropped, and a skipped tap counts two beats. The beat nearest the first tap keeps its index, so bars stay in place |
| `reset` | none | Restores the default grid from the last analysis |

Every result keeps beat indexes consistent and extends or trims the final anchor to the saved sample-length limit. The response is `{"grid":{"sampleRate":44100,"bpm":124.5,"anchors":[…]}}`. Missing analysis returns `409 beatgrid_missing`, and a grid locked in Engine DJ returns `409 beatgrid_locked`; malformed edits return `400 beatgrid_edit_invalid`; tempos outside 30–250 BPM return `422 beatgrid_tempo_range`; a marker on an existing marker returns `422 beatgrid_marker_exists`; inconsistent drafts return `422 beatgrid_invalid`.

`track.grid.update` saves a reviewed draft: `identifier` and `anchors`. It refuses locked grids, validates the grid and requires it to reach the saved sample-length limit, replaces the adjusted grid while keeping the default grid, header, and metadata, and sets `bpm` and `bpmAnalyzed` to the edited grid's whole-grid tempo, as Engine does when editing. Saving is Desktop-protected, snapshotted, and undoable, and returns `{"track_id":5}`. It fails with the edit codes above. Saved cues and loops keep their sample positions.

The matching commands are `engine-cli track analyze <tr|id|path...> [--bpm] [--key] [--grid] [--missing-only] [--bpm-range <range>]`, which analyzes everything when no analysis is chosen, `engine-cli track add --analyze [--bpm-range <range>] <path...>`, and `engine-cli track grid update <tr|id|path>` with exactly one of `--shift`, `--bar-shift`, `--downbeat`, `--double`, `--half`, `--tempo [--marker n]`, `--marker-add`, `--marker-move --marker n`, `--marker-remove`, `--tap t1,t2,…`, or `--reset`, plus `--dry-run` to print the edited grid without saving it.

## Saved cues and loops

`GET /v1/tracks/{id}/performance` is also available under `/v1/libraries/{key}` and accepts the same track identifiers as track details. It shares the collection read barrier and remains available while Desktop is running. It reads `PerformanceData.beatData`, `quickCues`, and `loops` through Engine CLI; it does not write analysis or create an undo record.

The response has `track_id`, an opaque `revision`, and `cues` and `loops` arrays, ordered by native slot (1–8). Unset slots are omitted; an empty name does not mean an unset slot. Colors are `#RRGGBB`. Cue `position` and loop `start`/`end` are source seconds derived from the saved sample rate, preserving fractional precision independently of browser playback speed:

```json
{"track_id":5,"revision":"opaque-current-revision","cues":[{"slot":8,"name":"Outro","color":"#FF0080","position":1.25}],"loops":[{"slot":3,"name":"Break","color":"#0080FF","start":2.5,"end":6.75}]}
```

Missing analysis returns empty marker arrays with the track ID and current revision. Malformed data or a missing sample rate for existing markers returns `422 invalid_saved_performance`; a failed analysis read returns `503 saved_performance_unavailable`. Neither case returns partial markers or guessed timing. Decoding uses `djtools` v0.2.0, including its bounded Qt decompression and native mixed-endian slot codec. The native codec preserves opaque payload fields. Reading does not activate cues or loops or expose the temporary main cue.

The job operation `track.cue.inspect` accepts `identifier` and returns the same saved performance. The collection-writing operations are:

| Operation | Required parameters | Result |
| --- | --- | --- |
| `track.cue.set` | `identifier`, `slot` (1–8), `position` (source seconds), `color` (`#RRGGBB`), `revision`; optional `name` defaults to empty | `{track_id,slot,revision,changed}` |
| `track.cue.clear` | `identifier`, `slot` (1–8), `revision` | `{track_id,slot,revision,changed}` |

Set replaces only the selected slot's position, name, and color. Clear removes only that slot. Both retain unrelated hot cues, loops, beat-grid data, and opaque native payload fields. Names must fit the native 255-byte UTF-8 field. Positions must be finite, nonnegative, and within the native track extent. When beat data is absent, Set uses the rate and extent recorded in native track data or counts decoded first-audio sample frames at that stream's native sample rate. It never derives the extent from container or stream duration metadata, guesses a sample rate, or uses the rounded collection duration. Reading and clearing remain available with offline audio.

The revision binds track origin identity, native performance bytes and row/table presence, and track interpretation inputs. Where audio probing is needed, it also binds the inspected media identity, size, modification time, or explicit unavailable state. Reinspect after replacing or restoring that audio. An incompatible revision fails with `cue_revision_stale`; invalid edits fail with `cue_invalid`; unusable audio fails with `cue_media_unavailable`, and audio changing during preparation fails with `cue_media_changed`. Invalid origin identity fails with `cue_track_identity_invalid`. A missing performance table fails with `saved_performance_unavailable`.

Encoding and validation run before the fresh Desktop check, snapshot, and undo history. Apply rechecks the captured state inside its database transaction. Successful writes are Desktop-protected, snapshotted, and undoable; their result comes from the committed edit without a separate post-commit read. A rejected or unreadable job observation must not be treated as permission to repeat an accepted write.

The matching CLI commands are `engine-cli track cue inspect <tr|id|path>`, `track cue set <tr|id|path> --slot A --position 1.25 --name Intro --color '#FF0080' --revision <revision>`, and `track cue clear <tr|id|path> --slot A --revision <revision>`. Slots accept A–H case-insensitively. All three accept `--json`; human inspection includes exact source positions and the revision needed for an edit.

## Shared progress fields

The operation produces the same progress events for CLI and HTTP consumers. For CLI NDJSON on stderr, use:

```sh
engine-cli --progress-json track upgrade 42 --clear-timing
```

Command results remain on stdout. Human CLI output displays the same overall percentage.

| Field | Meaning |
| --- | --- |
| `timestamp` | UTC event time in RFC 3339 format |
| `job_id` | Stable identifier for this operation, in CLI and HTTP events |
| `sequence` | Increasing event number within the operation, starting at 1 |
| `type` | `phase_start`, `progress`, `candidate_found`, `candidate_selected`, `smartlist_repair`, `complete`, `error`, or `cancelled`; HTTP adds `queued`, and acquisition can forward other Fetch Track event types such as `metadata_found` and `tagged` |
| `phase` | Current operation or work phase |
| `subphase` | Optional nested phase; during acquisition, Fetch Track's phase |
| `step`, `total_steps` | Optional stage numbering; Fetch Track's values remain under `source` |
| `percent` | Required overall completion, 0–100; monotonic within an operation |
| `message` | Optional explanation |
| `source` | Optional upstream event envelope plus `name: "fetch-track"`; retains upstream identity, sequence, timestamp, phase details, percentage, and payloads |
| `candidate` | Optional candidate object forwarded from Fetch Track |
| `metadata` | Optional metadata object forwarded from Fetch Track |
| `result` | Optional result payload; Fetch Track's acquired artifact remains under `source.result` |
| `error` | Optional string for failure or cancellation; HTTP job/status and request errors separately use `{code,message}` |
| `error_code` | Stable code accompanying a string-valued failure; consumers map it to user-friendly guidance |

Percentages describe completed stages or items, not elapsed time. A phase may remain at the same percentage while it works. Only successful completion emits 100%; errors and cancellations retain the last percentage.

During upgrade, Fetch Track's overall 0–100% maps to Engine's acquisition interval of 15–70%. For example, source 50% becomes Engine 42.5%. `source.percent` retains 50. Verification, metadata preservation, installation, and database work follow acquisition. Fetch Track's terminal event remains an Engine progress event until Engine finishes its own work. Engine's job identity and sequence are independent of Fetch Track's, which remain under `source`.

## Consume SSE

Every SSE frame uses `event: progress`; read `data.type` to distinguish event types. Its `id` is the job's sequence number. Browser clients can use native `EventSource`:

```js
const events = new EventSource(`${baseURL}/v1/jobs/${jobID}/events`);
events.addEventListener("progress", async ({ data }) => {
  const event = JSON.parse(data);
  console.log(event.percent, event.phase);
  if (["complete", "error", "cancelled"].includes(event.type)) {
    events.close();
    const job = await fetch(`${baseURL}/v1/jobs/${jobID}`).then(r => r.json());
    console.log(job);
  }
});
```

Reconnect with the `Last-Event-ID` header to replay events after that sequence. Browsers send this header automatically on reconnect. A consumed terminal cursor returns `204`, stopping reconnection. An expired cursor returns `409`; retrieve status and reconnect without a cursor. Heartbeat comments arrive every 15 seconds while idle.

The server retains up to 128 jobs and 1,024 events per job in memory. It evicts completed jobs as needed and rejects submissions with `503` when all slots are active. Restarting clears job status and events. Durable library undo history is separate. An evicted or unknown job returns `404`.

## Library-local custom tags

Custom tags are separate from embedded audio-file tags. Existing
`track.tags.inspect` and `track.tags.update` retain their file-metadata meaning.
The following operations use the selected library endpoint and the ordinary job
submission/result envelope. Category and value IDs are opaque UUID strings.

| Operation | Parameters | Result |
| --- | --- | --- |
| `tag.category.list` | None | `{revision,unavailable_tracks,categories:[{id,name,values:[{id,name,track_count}]}]}` |
| `tag.category.create` | `name`, `revision` | `{id,revision,changed}` |
| `tag.category.update` | `id`, `name`, `revision` | `{id,revision,changed}` |
| `tag.value.create` | `category`, `name`, `revision` | `{id,revision,changed}` |
| `tag.value.update` | `id`, `name`, `revision` | `{id,revision,changed}` |
| `tag.category.delete`, `tag.value.delete` | `id`, `revision`, `dry_run`, optional `plan_token` | `{id,revision,plan_token,affected_tracks,removed_assignments,removed_values,applied}` |
| `track.custom-tags.list` | None | `{revision,unavailable_tracks,categories:[{id,name,values:[{id,name,track_count}]}],tracks:[{track_id,values:[value_id...]}]}` |
| `track.custom-tags.inspect` | `track` | `{track_id,revision,values:[value_id...]}` |
| `track.custom-tags.update` | `track`, `revision`, `values` | `{track_id,revision,values:[value_id...]}` |
| `track.custom-tags.batch-inspect` | `tracks` | `{revision,track_ids,categories:[{id,name,values:[{id,name,selected_tracks}]}]}` |
| `track.custom-tags.batch-update` | `tracks`, `revision`, `add`, `remove`, `dry_run`, optional `plan_token` | `{revision,track_ids,add,remove,plan_token,changed_tracks,added_assignments,removed_assignments,applied}` |
| `tag.reconcile` | `revision`, `dry_run`, optional `plan_token` | `{revision,plan_token,unavailable_tracks,removed_assignments,applied}` |

`track.custom-tags.list` reads one library-wide taxonomy and assignment snapshot under
the collection read barrier. Both parts share the same companion revision. The
`tracks` array resolves native origin identities to current library-local track
IDs, ordered by ascending track ID; each value ID array is sorted. Tracks without
assignments and origins absent from this library are omitted from `tracks`; the
latter remain counted in `unavailable_tracks`. The complete current collection is
returned without truncation, so response size follows its assigned-track and
value counts. The read neither creates companion storage nor prunes assignments,
and does not create a snapshot or undo record.

`track` uses the existing single-track selector syntax. `values` is the complete
replacement array of value UUID strings; an explicit empty array clears the
track's custom tags. Missing or null arrays are rejected. Duplicate IDs and IDs
not present in this library are rejected. Assigning several values from the same
category is supported. Response arrays are arrays even when empty.

Batch inspection and updates accept 1–500 track selectors in `tracks`. Repeated
selectors resolving to the same local track are deduplicated in first-seen order;
`track_ids` returns that exact resolved order. Inspection counts each value's
assignments among these selected current tracks in `selected_tracks`, including
zero counts. It never changes assignments.

For batch updates, `add` and `remove` are required arrays of value UUIDs. Explicit
empty arrays preserve existing assignments. Values absent from both arrays are
kept; the operation does not replace each track's entire assignment set. Duplicate
IDs, unknown IDs, and IDs present in both arrays are rejected. Preview with
`dry_run:true`, then apply the same selection, revision, actions, and returned
`plan_token` with `dry_run:false`. The token binds the deduplicated local IDs,
their native origins, the global revision, and exact Add/Remove actions. Changes
to those identities or assignments require a new review.

`changed_tracks` counts selected local tracks whose assignments change.
`added_assignments` and `removed_assignments` count distinct native-origin/value
pairs. Apply commits the whole batch atomically and creates one undo history
entry. A preview with zero changed tracks succeeds; applying that reviewed no-op
is rejected with `tag_no_changes` before Desktop checks, backup, or history.
It does not change the operation's collection-write effect or scheduling.

The opaque `revision` covers both taxonomy definitions and all track assignments.
Read it from the latest list or inspect result and submit it unchanged with every
write. A concurrent definition or assignment change rejects a stale write with
`tag_revision_stale`; refresh both taxonomy and the selected track before editing
again. An unchanged replacement succeeds without modifying the store.

Assignments use Engine's native origin UUID and origin track ID, not paths or
local row IDs. `track_count` counts distinct assigned origins still present in
the selected Engine library. `unavailable_tracks` counts distinct assigned
origins no longer present after external edits, separately from current track
counts. Neither derived count changes the stored revision. List, inspect, and
preview never remove assignments or create a companion database.

To remove unavailable-track assignments, review `tag.reconcile` with
`dry_run:true`, then submit the same revision and returned plan token with
`dry_run:false`. The token binds the exact missing origins as well as companion
content; external origin restoration invalidates it even if companion bytes did
not change. Successful apply retains the reviewed counts and token, returns the
resulting revision and `applied:true`, and is undoable. Existing unavailable
assignments survive unrelated edits. Normal collection mutations remove only
assignments whose origins that mutation removed; duplicate merge unions values
onto the surviving native origin within the same history boundary. Sync rejects
unavailable assignments with `tag_assignments_stale` until reconciliation has
been reviewed and applied. No origin is inferred from matching filenames.

Deletion requires review. Submit `dry_run:true` to obtain counts and a plan token.
Submit the same ID, revision, and token with `dry_run:false` to apply. The token is
bound to the deletion kind, identity, and complete revision. Applying a category
deletion removes its values and their assignments; applying a value deletion
removes that value's assignments. `affected_tracks` counts distinct assigned
origins currently present in Engine; `removed_assignments` counts all stored
track/value pairs, including unavailable origins. `removed_values` counts value
definitions. The token also binds the exact currently affected origins, so an
external removal or restoration requires a fresh review even when the companion
revision is unchanged. Preview returns the current revision and `applied:false`; successful
application returns the new revision and `applied:true`. Changed impact requires
a new preview rather than accepting a stale consent.

Names are trimmed using Unicode whitespace and normalized to NFC. They must
contain 1–100 Unicode code points, valid UTF-8, and no control characters.
Unicode case-folded names must be unique across categories in the library, or
across values within the same category. The normalized display spelling is
retained. Renaming a value does not move it to another category.

List, inspect, batch inspection, batch previews, deletion previews, and reconciliation previews are reads. Actual writes use the collection
write barrier, fresh Desktop protection, snapshot, and durable history contracts.
Validation errors include `tag_name_invalid`, `tag_name_exists`,
`tag_values_required`, `tag_value_duplicate`, `tag_not_found`,
`tag_track_identity_invalid`, `tag_revision_stale`, `tag_plan_stale`,
`tag_tracks_invalid`, `tag_actions_overlap`, and `tag_no_changes`.
`track_not_found` retains its existing meaning. Clients must display their safe
error-code catalog messages rather than raw diagnostic text.

Unreadable or unsupported companion storage reports `tag_store_unavailable`.
Sync previews report `sync_tag_name_conflict` for distinct taxonomy IDs with
colliding normalized names and `sync_tag_reference_conflict` for accepted
decisions that would leave retained values or assignments without definitions.
Those conflicts must be resolved in the source/destination taxonomy or plan;
an automatic name-based identity merge is not performed. Partial multi-file
publication reports `history_recovery_required`, retains the pending history ID,
and refuses further collection writes until explicit forced undo recovery.

## Library-local track colors

Track colors are independent Engine CLI companion state, stored in
`.engine-cli/colors.db`. They are neither embedded audio tags, custom-tag values,
nor native Engine DJ Desktop colors. Each native origin UUID/track-ID pair has
at most one color. No identity is inferred from a path, filename, or another
library's local row ID.

| Operation | Parameters | Result |
| --- | --- | --- |
| `track.color.list` | None | `{revision,unavailable_tracks,tracks:[{track_id,color}]}` |
| `track.color.inspect` | `track` | `{track_id,color,revision,changed}` |
| `track.color.update` | `track`, `revision`, `color` | `{track_id,color,revision,changed}` |
| `track.color.reconcile` | `revision`, `dry_run`, optional `plan_token` | `{revision,plan_token,unavailable_tracks,removed_assignments,applied}` |

These operations use the selected library endpoint and ordinary job envelope.
`track` accepts the existing single-track selector syntax. A color is exactly
`#RRGGBB`, accepted case-insensitively and returned uppercase. An explicit empty
string clears a color; missing, null, whitespace, and other formats are rejected.
An unassigned track returns `color:""`. The opaque revision covers all stored
assignments, including unavailable origins, and must match on every write.

List returns every assigned current track in ascending local-ID order under one
collection read barrier and revision. It omits unassigned tracks and separately
counts unavailable assigned origins. List and inspect do not create storage,
remove assignments, or create backup/history records. Update requires fresh
Desktop protection and creates one undoable collection change. An unchanged
update is rejected with `color_no_changes` before backup or history enrollment.

Colors for externally removed tracks remain recoverable until explicit consent.
Review reconciliation with `dry_run:true`, then apply the same revision and exact
returned plan token. The token binds the missing identities, not only their
count; restoring or replacing an origin requires a new review. Apply returns
the reviewed counts/token, resulting revision, and `applied:true`. Zero-removal
application is rejected with `color_no_changes`. Reads and previews never prune
or enroll storage. Normal collection removal prunes only origins removed by
that operation, and Undo restores their colors.

Duplicate inspection includes each candidate's color. An assigned survivor
keeps its color; an unassigned survivor inherits a single distinct color from
removed tracks. Distinct removed colors with no survivor color are refused
before safety enrollment with `color_merge_conflict` so a choice can be made
before destructive merging.

Version-three backup presence metadata records tags, source archives, and colors independently. A snapshot whose
presence metadata records no colors restores absence rather than retaining later
colors. Version-one and version-two presence metadata predate colors and likewise restore their
absence; older backups without any companion section likewise restore absence.
Undo preserves the state it replaces. Library move/delete and ordinary history
include enrolled companion files. Sync plans scope colors to selected tracks,
bind both libraries' stored color revisions, and use the existing source versus
destination conflict decisions. Audio reuse binds the accepted color to the
surviving destination origin. Unavailable assignments reject sync with
`color_assignments_stale` until reviewed reconciliation. Engine and companion
publications are not one SQLite transaction: a failed companion publication
after Engine changes retains pending history and reports
`history_recovery_required` for explicit forced Undo recovery.

Validation and recovery codes are `color_invalid`, `color_revision_stale`,
`color_track_identity_invalid`, `color_plan_stale`, `color_no_changes`,
`color_store_unavailable`, `color_merge_conflict`, and `color_assignments_stale`.
`track_not_found` retains its existing meaning. Render safe catalog guidance
rather than raw diagnostics.

The corresponding commands are `track color list`, `track color inspect <track>`,
`track color update <track> --revision <revision> --color <#RRGGBB>`, and
`track color reconcile --revision <revision> --dry-run`, followed by the same
revision and `--plan-token <token>` to apply. Use `--color=` for explicit clearing.

## Library health checks

`library.verify` audits the selected library through the collection read barrier. A completed report retains the existing health counters and `isHealthy` field. Playlist memberships resolve through both their origin database UUID and track ID; a local track row ID alone does not identify a saved membership.

An unreadable collection table, unreadable membership row, or invalid saved playlist chain fails the job with `error.code: "library_verify_incomplete"` and no result. An incomplete audit never returns a partial healthy or unhealthy report. Cancellation and timeout retain their existing classifications. Retry the check; if the failure persists, inspect the library in Engine DJ before making changes. Verification performs no repair and creates no snapshot or Undo entry.

## Operations

### Incoming local-file queue

The `intake.*` jobs use the selected library endpoint and server-local source paths. The durable queue lives in `<lib-dir>/.engine-cli/intake.db`. Opening an API library resumes preparation; closing the library or API cancels and joins its worker before closing storage. Preparation copies files into staging and preserves the originals. Source identity, size and modification time are checked around copying; a quiet interval is a readiness heuristic, not a promise that another application has finished writing. Preparation never imports into the collection.

| Operation | Parameters | Result |
| --- | --- | --- |
| `intake.list` | none | queue view |
| `intake.process` | none | queue view after preparation reaches no eligible work |
| `intake.enqueue` | required `paths: string[]` | items in request order |
| `intake.cancel`, `intake.retry` | required positive integer `id` | updated item |
| `intake.pause`, `intake.resume` | none | durable control state |
| `intake.import.review` | required positive integer `id`; optional `to_playlist` selector | staged import review |
| `intake.import` | required positive integer `id` and `plan_token`; same optional `to_playlist` | `{item_id,history_id,track_id}` |

List and import review are read operations. Enqueue, process, cancel, retry, pause and resume have `system` effects and create no collection snapshot or Undo entry. Import has a `collection` effect with fresh Desktop protection, automatic backup unless disabled, and ordinary collection History/Undo. Inspect the live operation catalog for parameter metadata.

The queue view contains `items`, `control:{paused,reason?}`, and optional safe `worker_error_code` and `recovery_error_code`. Items contain `id`, `source_path`, `source_size`, `source_modified` (Unix nanoseconds), `status`, `attempt` and `cancel_requested`; `stage_id`, `content_hash`, `error_code` and `history_id` appear when available. Queue IDs are distinct from collection track IDs and opaque staging IDs. Status is `queued`, `preparing`, `staged`, `failed`, `importing`, `imported` or `cancelled`. Listing waits for initial receipt reconciliation, not completion of preparation. Empty storage returns an empty item array. A queue reopen failure after a committed library replacement is exposed separately as `recovery_error_code`; it does not reverse that replacement.

Enqueue preserves FIFO insertion order and deduplicates the same observed source version, including versions with retained import receipts. Earlier items remain durable if a later path fails. Processing waits for reconciliation and eligible preparation; item failures remain on the items, so a completed process job is not proof that every item staged successfully. Cancellation preserves sources and does not undo a committed import. Retry refuses History-owned import versions; a prepared copy returns to staged, otherwise it returns to queued.

Pause stops preparation and explicit imports. Control reasons are `manual`, `restored` or `history_unavailable`; an active queue omits the reason. Library replacement stops and joins the local worker before replacing storage. Backup/restore includes the queue, but restored queues remain paused until explicit resume. Missing or incomplete History evidence retains a recovery condition rather than authorizing a duplicate import. A live History publisher instead causes a cancellable wait, not a worker failure. The operational queue and its import receipts are excluded from ordinary collection snapshots, so Undo cannot reset deduplication or make a committed version eligible again.

Use the reviewed import sequence:

1. Enqueue sources and inspect or process the queue until the chosen item is staged.
2. Submit `intake.import.review` with that item ID and the optional existing destination playlist selector. Review returns `items` (staging `id`, `title`, `status`, optional `destination` and `content_hash`), `to_playlist`, `ready`, `skipped` and `plan_token`.
3. Inspect the destination and submit `intake.import` with the exact token, queue item ID and identical playlist selection. Changed staged bytes or destination state require a fresh review.
4. Monitor the job and refresh the queue. Retain a nonempty result and `history_id` even when the job fails during cleanup: a failed job does not prove that the collection remained unchanged. Offer the retained Undo/recovery information instead of submitting the import again.

The durable import intent precedes installation, and the exact imported track receipt precedes the History After snapshot. Restart reconciliation resolves interrupted post-commit results without repeating collection import. Undo changes the effective item state to cancelled; Undo-of-Undo restores imported status. Both retain the receipt and source-version deduplication. Prepared staging evidence remains retained for recovery.

Retained-receipt reconciliation observes retained import receipts under the existing History owner, then the intake worker owner, matching import ownership order. It rereads the current ImportIntent tuple (item_id, history_id and persisted track_id receipt) and conditionally updates matching Item state while both are held; Resume retains that boundary until successful reconciliation clears suspension. An empty intent set does not create or wait on History merely to prepare sources. Missing History is checked again under the worker owner before pausing recovery. Closing the service cancels an initial History wait and joins it without publishing initial readiness or treating cancellation as a worker failure. These semantics do not combine the independent publications into one transaction.

Import review observes collection state through the shared Smartlist-safe read boundary. A live History publisher or pending Smartlist repair refuses review instead of issuing a plan token from unpublished collection state. After the publisher finishes or pending repair is resolved, request a fresh review. This read boundary does not recover or apply repair, create a snapshot or Undo entry, or change import application ownership.

Render stable codes through the client's safe message catalog. `intake_replacing` means a competing library replacement owner must finish; `intake_paused` requires inspecting control state and explicit resume. `intake_import_recovery_required` and `intake_recovery_required` require retained History/storage recovery. `intake_source_changed`, `intake_staged_changed` and `intake_state_changed` refuse stale observations. Worker and recovery codes describe confirmed failures; pending refreshes do not establish a new failure. The CLI equivalents and flags are documented in the [incoming-file command reference](../skills/engine-cli/references/intake.md).

### Staged music

Staged acquisitions live under `<lib-dir>/.engine-cli/staging/`, independently of collection rows. Each item has a stable opaque `id`, `kind` (`track` or `mix`), `title`, `status` (`pending`, `ready`, or `failed`), and UTC `created_at`. Optional fields include `artist`, `album`, `source_url`, `query`, `error`, `audio_path`, `artwork_path`, `parent_id`, and `index`. Paths are relative to staging storage so moving the library preserves acquisitions. A track's `parent_id` identifies its mix; `index` preserves mix order. Failed or missing acquisitions remain available for retry after restarting the server. A failed `track.fetch`, `mix.fetch`, or `staging.retry` whose changes stay inside its own staged items records no undo history, and its job has no `history_id`; discard the failed item with `staging.rm` instead. A failure that changed the collection or files outside those items keeps its undo record.

`GET /v1/staging` and the `staging.list` job return a flat array, including mix groups and their child tracks; empty storage returns `[]`. `staging.inspect` accepts `{"id":"..."}` and returns one item, with an additional `items` array for a mix's children. `staging.duplicates` accepts the same parameters and returns collection candidates using the existing case-insensitive, trimmed title-and-artist metadata matcher. These candidates are evidence for review, not proof of identical audio.

`GET /v1/staging/{id}/audio` and `/artwork` stream the staged item's media, with normal origin checks and HTTP byte ranges. Media paths cannot be supplied by clients; paths and symlinks are confined to staging storage. Missing items or media return `404`; unsupported media types return `415`.

`staging.import` accepts `{"ids":["..."],"to_playlist":"optional existing playlist ID"}`, plus `analyze` and `bpm_range` to analyze the imported tracks as described under track analysis. Individual track IDs select tracks; a mix ID selects all its child tracks. Repeated selections are deduplicated. The result contains `imported` (`{id,track_id}` entries pairing each staged ID with its collection track) and `failed` (`{id,error}` entries), always arrays. Failed/pending children remain staged. Each ready track is installed under `Music/staging/<id>/`, imported through the ordinary Engine track operation, and optionally assigned to the playlist before its staged item is removed. Failed imports retain their acquisition for retry. If collection import succeeds but staged cleanup fails, the same ID appears in both arrays with a `track imported but staged cleanup failed` error; the acquisition stays staged for retry, which reuses the existing collection track. Completed mix groups disappear only after all children have been imported. Partial success is represented by both arrays; inspect `failed` even when the job succeeds.

`staging.rm` accepts `{"ids":["..."]}` and returns `{"removed":["..."]}`. Selecting a mix removes its children too. Removal records the actual manifests and media in undo history and leaves collection rows untouched. Import requires the ordinary fresh desktop check and safety snapshot. Undoing import restores staged acquisitions and reverses installed files, collection records, and playlist changes. Stage-only removal does not require a desktop check or collection snapshot.

The CLI equivalents are `engine-cli staging list [--json]`, `staging inspect <id> [--json]`, `staging rm <ids...> [--json]`, and `staging import <ids...> [--to-playlist <id>] [--json]`.

The operation catalog includes `effect`: `read`, `staging`, `collection`, `filesystem`, or `system`. `mutates` identifies an operation whose base effect changes state; `effect` distinguishes its scope. Collection effects receive desktop protection, safety snapshots, and database snapshots in their undo history (`scope: "collection"` in `library.history` entries). Staging and filesystem effects never write Engine databases: their undo history (`scope: "files"`) preserves only the files they change and copies no database. Entries recorded before scopes existed list as `collection`. A fetch request that also imports is treated as a collection effect when executed. `parameter_effects:{"auto_install":"system"}` identifies the additional tool-install effect when that parameter is true, including read operations and dry runs. `staging.retry` reports `additional_effects:["system"]` because saved acquisition options may install missing tools. These additional effects do not replace collection or staging protection.

`mix.inspect` previews a tracklist without writing library files or undo history. `track.candidates` and `mix.candidates` persist selection tokens, so they have a staging effect. These three lookup operations accept query and acquisition options; import, playlist assignment, and candidate continuation parameters belong only to fetch operations.

`track.verify` accepts exactly one of `target` (URL or server-local path), `identifier` (collection track), or `staging_id`, plus `min_bandwidth_khz` and `auto_install`. Verification does not require a collection. The CLI is `track verify [url|path|identifier]`, with `--target` for an explicit URL/path, `--staging-id`, `--min-bandwidth-khz`, and `--auto-install`.

`track.tags.inspect`, `track.tags.update`, and `track.artwork.embed` accept exactly one selector: `identifier`, `staging_id`, `path`, or an ordered array `identifiers`, `staging_ids`, or `paths` (at most 200 entries). Single selectors return the ordinary single-file report. Arrays return `{status,succeeded,failed,results:[{identifier|staging_id|path,result,error?,error_code?}]}` in request order, including repeated selectors. A failed file adds its diagnostic `error` and stable `error_code` to its entry. `status` is `success` when every file succeeds, `partial` when some fail, `failed` when all fail, and `cancelled` after cancellation. A partial batch succeeds: the job state is `succeeded` and mutating batches keep their `history_id`, so the completed files can be undone. Only a batch in which every file fails fails the job, with `batch_failed`. Cancellation retains results for files already processed. The CLI prints the same result and still exits unsuccessfully when any file fails.

Tag updates support nullable string overrides `artist`, `title`, `album`, `date`, `genre`, `comment`, plus `artwork`, `source_url`, `refresh`, `dry_run`, `no_cache`, and `auto_install`. Explicit empty strings clear tags; omitted fields retain native lookup behavior. Preview results preserve comments, including explicitly cleared comments. Artwork embedding accepts only the selector and required `artwork` URL/path. Inspect accepts only the selector. The CLI commands are `track tags inspect [path|identifier...]`, `track tags update [path|identifier...]`, and `track artwork embed [path|identifier...] --artwork <URL|path>`, with `--path`, `--paths`, `--staging-id`, and `--staging-ids` for explicit selection and hyphenated update flags.

Raw file mutations resolve collection ownership, including symlink and hard-link aliases, before execution. Collection files use the normal desktop guard, snapshot, metadata update, and undo workflow. Staged files update their manifests and retain staging undo. Other files use durable file history and an atomic replacement only after verifying compressed audio packets are unchanged. Native outputs must remain inside a private workspace and preserve the file extension. A failed validation retains the original file; undo restores completed replacements.

Engine requires `fetch-track` 1.10.1, `fetch-mix` 2.1.1, and `tag-track` 1.1.1 or newer, the releases providing its machine interfaces. Before running one, Engine reads its `--version` and refuses a missing tool with `tool_unavailable` or an older or unrecognized version with `tool_outdated`; `dev` builds pass the version check. With `auto_install:true`, Engine first installs a missing tool or updates an outdated one into the managed tool directory, then runs it only when the installed release meets the requirement; a failed download reports `tool_install_failed`, and a latest release that is still too old reports `tool_outdated`. The same option asks the tool to install its own missing dependencies.

`tool.inspect` reports `version`, `minimum_version`, executable availability (`available`), whether the tool meets Engine's requirement (`satisfied`), and machine-interface support (`native_json`). A satisfied version is confirmed by safely probing `--json dependencies`; inspection never installs anything. Supported tools include their typed dependency reports, including unsatisfied dependencies. Unsatisfied tools return `tool_unavailable` or `tool_outdated` with an actionable message and are not probed.

`tool.upgrade` installs the tool's latest GitHub release into Engine's managed tool directory (`$XDG_DATA_HOME/engine-cli/bin`, falling back to `~/.local/share/engine-cli/bin`) through godeps, without running the installed tool's own updater. Engine puts this directory ahead of `PATH` at startup, so managed releases take precedence over other copies. The result has `status` `success`, or `current` when the satisfied version did not change, and the installed `version`. Download failures return `tool_install_failed`; a release that still does not meet the requirement returns `tool_outdated`. `tool.dependencies.*` manage the tool's own upstream dependencies through that tool.

`track.upgrade` accepts the same acquisition options as `track.fetch`: `sources`, `skip_verify`, `skip_metadata`, `no_cache`, `auto_install`, `min_bandwidth_khz`, nullable `confirm_fingerprint`, and `js_runtime`. Omitting fingerprint confirmation preserves the upstream default; explicitly setting `false` disables it. Engine independently verifies replacement quality even when upstream verification is skipped. Curated metadata, playlist membership, the original audio, snapshot protection, and undo remain part of the upgrade operation.

To upgrade from a reviewed source, first call `track.candidates`, then supply its `selection_token`, a listed `candidate_url`, and the exact original `query` and acquisition options to `track.upgrade`, together with the collection `identifier` and timing choice. Tokens bind the original request and permitted candidate URLs; an arbitrary URL without a matching token is rejected before acquisition. The equivalent CLI supports `track upgrade <id|path> --query <query> --selection-token <token> --candidate-url <url>` and the corresponding hyphenated acquisition flags. `--json` writes the same upgrade result as the API to stdout: `track_id`, the replacement `path`, `original_path`, `timing_cleared`, and `reanalysis_required`. Progress and the undo operation ID go to stderr in JSON mode.

### Collection editing and artwork

`track.update` accepts `identifier` plus any supplied `title`, `artist`, `album`, `genre`, `comment`, `label`, `composer`, `remixer`, `year`, `rating_stars`, and `key`. At least one editable field is required. Omitted fields remain unchanged. Optional text can be cleared with an empty string; titles must contain text. Year is an integer from 0 to 9999, with 0 clearing the year. Rating is an integer from 0 (unrated) to 5 stars. Key is Engine's musical-key index from 0 to 23, with -1 clearing the key: even indexes walk the circle of fifths from C major (0, Camelot 8B, Open Key 1d), and each odd index is the preceding major's relative minor (1 is A minor, 8A, 1m; 23 is D minor, 7A, 12m). Track responses report the same index as `key`, which is `null` without a key; analyzed tracks without a detected key can also report -1. Editing the key changes the collection's key and leaves the analysis saved in `PerformanceData` untouched. Track responses include `ratingStars`; the stored `rating` field uses Engine's 0–100 scale. These edits change collection metadata, leave audio files and saved timing untouched, and support ordinary snapshots and undo. JSON null does not clear a field.

The matching CLI command is `engine-cli track update <tr|id|path>` with `--title`, `--artist`, `--album`, `--genre`, `--comment`, `--label`, `--composer`, `--remixer`, `--year`, `--rating`, `--key`, `--artwork <path>`, and `--clear-artwork` flags. `--key` takes a key in Camelot (`8A`), Open Key (`1m`), or by name (`Am`, `F#m`, `Bb`, `A minor`); `none` clears it. `--json` prints `{"track_id":<id>}`. Only supplied flags change fields.

`track.batch.update` accepts `identifiers` (1–500 track IDs, stable references, or exact stored paths) plus the same scalar metadata fields. It applies the supplied values to every selected track in one transaction, with one safety snapshot and one undo record. All selectors resolve before writing; aliases are deduplicated in first-seen order. A missing track fails with `not_found`; any failed write rolls the entire transaction back. Desktop protection and cancellation apply through the ordinary collection-write pipeline. The result is `{"track_ids":[5,6]}`; no post-commit entity read is needed. Omitted or null fields stay unchanged, empty text clears optional text, and numeric clearing uses the values above. Batch artwork edits are not supported.

Use `engine-cli track batch update <tr|id|path...>` with the scalar flags above for the same behavior. For example, `engine-cli track batch update 5 6 --artist "Various artists" --album "" --json` sets both artists and clears both albums. `--json` prints the affected `track_ids`; one undo restores the complete edit.

`track.batch.find-replace` reviews and applies per-track text transformations. Supply `identifiers` (1–500 selectors, with aliases deduplicated), `fields` (any of `title`, `artist`, `album`, `genre`, `comment`, `label`, `composer`, `remixer`), nonempty `find`, and `replacement` (required; an empty string removes matches). `mode` defaults to `literal`; `regex` uses Go regular expressions and `$1`/`${name}` replacement capture expansion. Dollar signs remain literal in literal mode. Matching is Unicode case-insensitive unless `match_case:true`. All nonoverlapping matches are replaced; null fields remain null. Find text is limited to 4096 bytes and replacement text to 65536 bytes.

Resource limits apply to preview and apply before safety enrollment: each source evidence text value and transformed field is limited to 1 MiB. Source evidence (library UUID, selected track origins/paths/timestamps and all eight text fields) is limited to 16 MiB total; transformed field outputs are separately limited to 16 MiB total. Selectors are limited to 1 MiB each and 16 MiB total. Metadata byte lengths are checked in the transaction before text hydration. Each field permits at most 65536 matches, further limited so its native match-index integers occupy at most 8 MiB. Regex expansion is checked before allocation using Go's native template expansion to count literal bytes and capture references, conservatively multiplying the reference count by the largest participating capture (including the full match). This can reject a mixed-capture template whose exact expansion would fit. Literal replacement uses its exact byte length. Exceeding any budget fails with `metadata_transform_too_large`; select fewer tracks, smaller fields or a smaller replacement. Context cancellation is checked between matching, expansion, fields and tracks; rejected requests create no snapshot or history entry.

1. Submit `dry_run:true`. Preview is a collection read, available while Desktop runs, and creates no snapshots or history. The result includes `selected_tracks`, `changed_tracks`, `changed_fields`, `can_apply`, `plan_token`, and `tracks`. Each track supplies `track_id`, `title`, and `changes`; each change contains `field`, `before`, `after`, and an optional stable `error_code`. A blank resulting title has `metadata_title_required` and prevents the entire apply. No matching changes also yields `can_apply:false`.
2. Review the per-track changes. Cancellation or abandoning the preview changes nothing. Changing selection, match options, or replacement text requires another preview.
3. Submit identical options with `dry_run:false` and the exact `plan_token`. The review binds library identity/location, resolved track origins, paths, metadata values including nulls, and options. Missing or stale tokens fail with `metadata_transform_stale`; a reviewed plan without applicable changes fails with `metadata_transform_unavailable`. Validation precedes Desktop checks, safety snapshots and history, and repeats within the write transaction. Fresh Desktop protection, one safety snapshot and one undo record cover the whole batch. Any failed write or cancellation before commit rolls back every replacement. Audio, artwork, numeric metadata and saved performance are preserved. The result is `{track_ids,changed_fields}`, captured from the committed edits; one undo restores the batch.

The equivalent command is `engine-cli track batch find-replace <tr|id|path...> --fields title,artist --find 'DJ ' --replacement '' --dry-run [--json]`. Apply with identical flags, omit `--dry-run`, and add `--plan-token <reviewed-token>`. Match options are `--mode literal|regex` and `--match-case`. Quote regex replacement expressions in the shell to preserve dollar signs. Human output shows each field's before/after values; JSON goes only to stdout, with progress and undo operation IDs on stderr.

`track.batch.capitalize` uses the same selection, eight text fields, exact before/after review, resource limits, Desktop protection, transaction, snapshot and undo contract as find/replace. Supply `identifiers`, `fields`, and `recipe` (`upper`, `lower`, or `title`). Optional `language` is a BCP 47 tag, for example `tr` or `nl`; empty uses language-neutral Unicode casing, independent of the server locale. Unicode mappings can expand characters. Title casing uses the maintained `golang.org/x/text/cases` word-boundary approximation; it does not preserve arbitrary acronyms, infer artist-name styling, or apply editorial minor-word rules. Null fields stay null; whitespace is preserved. Invalid UTF-8 metadata is rejected.

1. Submit `dry_run:true` and inspect the per-track proposed values. An unchanged batch has `can_apply:false` and cannot be applied.
2. Apply the identical selection, fields, recipe and language with `dry_run:false` and the exact `plan_token`. Any option or evidence change invalidates consent. Validation and output expansion limits precede safety enrollment and repeat within the transaction.
3. Inspect the affected `track_ids` and `changed_fields`. One undo restores the entire batch; audio, artwork, numeric metadata and performance data are preserved.

The equivalent command is `engine-cli track batch capitalize <tr|id|path...> --fields title,artist --recipe upper --dry-run [--language tr] [--json]`. Apply with identical flags, omit `--dry-run`, and supply `--plan-token <reviewed-token>`. Human output shows before/after values; JSON uses the same review and result shapes as find/replace.

`playlist.rename` accepts `identifier` and a nonempty `title`, preserving playlist identity, parent, and memberships. The matching command is `engine-cli playlist rename <pl|id> <name> [--json]`. Renaming uses the same snapshot, desktop protection, and undo handling as other collection mutations.

Playlist rename and move update saved Smartlist parent and sibling paths for the entire moved or renamed subtree in the same database transaction. Moves also reconnect the Smartlist chain at the old position and insert the playlist after the Smartlists preceding its new position. Playlist removal deletes Smartlists beneath the removed subtree and reconnects preceding Smartlists to the following sibling chain. A folder containing ordinary playlists or saved Smartlists requires `force:true` (`playlist rm --force`). Rule data remains opaque and unchanged; these operations do not evaluate or edit Smartlist rules. Undo restores both ordinary playlist rows and saved Smartlist paths, order, and rules.

Libraries using a native schema without the optional `Smartlist` table retain ordinary playlist rename, move, removal, and Undo behavior. These operations do not create or migrate that table. If the table exists but cannot be queried or updated, the mutation fails and rolls back rather than ignoring the saved data.

### Saved Smartlist link repair

`library.smartlist-repair.preview` accepts no parameters and returns `{plan_token,reconnected,removed,amended_deletes,rows}`. Rows contain `{uuid,name,action,delete_entry_id?}`; actions are `reconnect` or `remove`. Preview changes no collection/history state. It binds consent to the observed native database and retained history, preserving opaque rule data.

`library.smartlist-repair.apply` requires the exact string `plan_token`. Fresh Desktop protection and collection-write scheduling apply. Repair retains immutable original images and durable publication intent before replacing targets. The result adds `applied` and optional `undo_id`. Reconnects receive their own Undo entry and job `history_id`; removals owned by an original deletion amend that deletion's Undo instead. Unattributed damaged rows receive no invented Undo. Repair publication owns this recovery boundary and is excluded from generic safety-backup/history wrapping. A healthy collection returns `applied:false` without enrolling repair storage.

`library.smartlist-repair.recover` accepts no parameters and requires fresh Desktop protection. It validates library identity, all original/staged images and historical database references, and all target states before rolling back an interrupted publication. The result is `{rolled_back,restored_targets}`; absent/completed intent returns false and zero. Changed evidence refuses recovery before the first restore. Original evidence and the recovery marker remain available afterward. Pending repair refuses ordinary history observations and protected mutations; read-only operations never perform recovery automatically.

Ordinary protected collection edits repair saved Smartlist links under their history publisher lock before backup preparation and their own Before snapshot. Undo of the requested edit therefore preserves the preceding repair. Creation/restoration, explicit repair, Undo and cross-library sync retain their own operation boundaries. The CLI equivalents are `engine-cli library smartlist-repair preview`, `apply --plan-token <token>`, and `recover`, each with optional `--json`.

When an ordinary edit commits a repair, it emits a nonterminal `smartlist_repair` progress event with phase `library.smartlist-repair.apply` and the applied repair result in `result`. The HTTP job also retains that result in optional `smartlist_repair`, independently of the requested operation's `result` and `history_id`. The repair remains reported if subsequent backup preparation or the requested operation fails or is cancelled. Its optional `undo_id` belongs to the reconnect repair, not the requested edit. Healthy/no-change checks emit no repair event and omit the job field. Human and agent CLI output names repaired rows on stderr; `--progress-json` emits the structured event there, while requested-operation JSON stays on stdout.

`PUT /v1/tracks/{id}` accepts a multipart upload with one `artwork` file (PNG, JPEG, or GIF, at most 8 MiB and 40 megapixels) and an optional `metadata` JSON object using the `track.update` fields. The response is an ordinary accepted job with the same desktop guard, snapshot, progress, and undo behavior. Metadata and the artwork link are committed together. The image is center-cropped to a square, stored as a 1400×1400 JPEG cache image and a 256×256 thumbnail, and SHA-1 tracked. Audio files remain untouched. Invalid uploads return `400`; oversized requests return `413`. JSON `track.update` can clear the artwork link with `clear_artwork:true`, or use a server-local image with `artwork_path`. These artwork actions are mutually exclusive.

`GET /v1/tracks/{id}/artwork?size=full` prefers the full cached image, falling back to the thumbnail when unavailable. The default serves the thumbnail.

`GET /v1/tracks/{id}/artwork` serves the linked artwork as PNG, JPEG, WebP, or GIF. It reads inline AlbumArt bytes first, then resolves Engine's `Artwork/` cache from the linked binary or hexadecimal SHA-1 hash, followed by the track's source hash. Cache names use URL-safe base64 or hexadecimal hashes. The identifier can be a local numeric ID or stable track reference. Successful responses use `Cache-Control: private, no-cache` with a strong content-based ETag. Matching `If-None-Match` requests return `304` without image bytes; changed content receives a fresh validator even at the same URL. Missing artwork returns `404`; unsupported image content returns `415`. This endpoint is read-only and does not regenerate artwork or change the library.

### Rekordbox export

Submit `POST /v1/operations/migrate.export` with `{"source":"/path/to/Engine Library","target":"/path/to/rekordbox"}`. Both fields are optional strings: `source` defaults to the server's configured library and `target` defaults to `./rekordbox` relative to the server's working directory. The CLI equivalent is `engine-cli migrate export --source <path> --target <path> --json`.

The job runs without a terminal interface and reads current `Database2/m.db` libraries without modifying the source database or audio. It creates no source snapshot or undo history and does not require a desktop mutation check. It copies audio byte-for-byte under target `Contents/`, preserving playlists, cues, and beat grids in target-root `rekordbox.xml`. Target-root `.sync_state.json` records resumable work. Matching-size existing audio is verified by SHA-256 before reuse; interrupted tracks restart from the beginning. Unreferenced target files remain in place.

The result contains absolute `source`, `target`, and `xml` paths, plus numeric `total`, `copied`, `existing`, `failed`, and `unavailable`. Optional `failures` contains `{source,error}` items for failed tracks. `total` describes the imported collection; the outcome counts describe the tracks processed in that run. Missing and streaming audio is unavailable and excluded from XML without causing failure by itself. Only tracks copied or verified in that run and still present belong in XML. Failed/cancelled jobs retain completed audio and publish the available subset where possible; rerun to resume and rebuild the full export. Regenerate XML after relocating the target because its file URIs are absolute.

Use the ordinary job status, cancellation, and SSE endpoints. Progress phases include `migrate.read`, `migrate.copy`, and `migrate.xml`; `migrate.export` owns the terminal event, with 100% reserved for successful completion. Copy events carry `metadata: {source,target,track,total_tracks,copied_bytes,total_bytes}` with absolute paths, a one-based track index, and byte counts. Percentages are monotonic across the whole operation, and events share its stable `job_id` and increasing `sequence`. CLI `--progress-json` emits the same schema on stderr; normal CLI output shows percentages and phases. Copy, resume-state, and XML failures fail the job while retaining any available partial result.

### Library, playlist, and track operations

Duplicate scan (`track.duplicate.list`) and comparison (`track.duplicate.inspect`) tracks include a `playlists` array of current playlist names, including `[]` for tracks outside playlists. Memberships resolve through the track's origin database UUID and origin track ID, matching Engine's persistent identity rather than coincidentally equal local IDs. Duplicate-list and inspect CLI output also includes these memberships. Track IDs can be used with the metadata, audio, and beat-grid endpoints to audition each candidate before merging.

`track.list` returns every collection track, including tracks outside playlists, in ascending ID order. Its result is an array using the same track fields as `playlist.inspect`. An empty collection returns `[]`.

Every collection track object includes `playlistIds`: the numeric IDs of the playlists that directly contain the track, in ascending order without repeats, or `[]` when it belongs to none. Parent folders appear only when the track is also a direct member of them. Memberships resolve through the same origin identity as duplicate `playlists`, and IDs stay unambiguous where nested playlists share titles. The field appears in `track.list`, `track.inspect`, `playlist.inspect` member tracks, `GET /v1/tracks/{id}`, `GET /v1/tracks` and `GET /v1/playlists` batches (by `ids` or `refs`). Each request reads memberships for all its tracks in one query. The `--json` output of `track inspect` and `playlist inspect` carries the same field, for example:

```json
{"id":42,"title":"Night Drive","artist":"Example Artist","ratingStars":4,"albumArtId":17,"playlistIds":[3,12]}
```

The example omits the remaining track fields.

Every collection track object also includes `playCount` and `lastPlayed` from Engine's play history (see [Play history](#play-history)): the number of recorded plays across the listed sessions, and the latest timed play in Unix seconds. Both are `null` when the library has no play history; a track never played reports `playCount:0` and `lastPlayed:null`. They appear wherever `playlistIds` does.

Mutation results report what the operation changed, not the changed tracks. Engine never reads a track back after committing a change, so a committed change cannot be reported as failed because a follow-up read failed. Read tracks and their `playlistIds` through the read operations above after the job succeeds. Batch operations return one entry per completed item, in request order, including the entries completed before a failure.

| Operation | Result |
| --- | --- |
| `track.update`, `PUT /v1/tracks/{id}`, `track.grid.update` | `{"track_id":5}` |
| `track.analyze` | `{"tracks":[{"track_id":5,"bpm":{"status":"written","bpm":124.0065}}]}`; see track analysis above |
| `track.batch.update` | `{"track_ids":[5,6]}` |
| `track.add` | `[{"track_id":7,"path":"Music/song.mp3","playlist_id":2,"moved":true}]`; `path` is the stored path, `playlist_id` is omitted without `to_playlist`, and `analysis` reports `analyze:true` per track |
| `track.rm` | `[{"track_id":5}]` |
| `track.move` | `{"track_id":5,"path":"moved/song.mp3"}` with the stored path |
| `track.upgrade` | `{"track_id":5,"path":"…","original_path":"…","timing_cleared":false,"reanalysis_required":true}` |
| `track.tags.update`, `track.artwork.embed` | Each file report carries `track_id`, `original_path`, and the tool `result`; `path` names the installed file once the collection points to it |
| `playlist.track.add`, `playlist.track.rm` | `[{"playlist_id":3,"track_id":5}]` |
| `playlist.track.move` | `[{"from_playlist_id":3,"to_playlist_id":2,"track_id":5}]` |
| `playlist.reorder` (apply) | `{"playlist_id":3,"track_ids":[6,5,7]}`; track IDs follow the resulting saved order |
| `session.import` (apply) | `{"playlist_id":3,"name":"Friday","track_ids":[5,6],"imported_count":2,"duplicate_count":1,"skipped_count":0}` |
| `playlist.import` (apply) | `{"playlist_id":3,"track_ids":[5,6],"imported_count":2,"duplicate_count":1,"skipped_count":0}` |
| `playlist.export` (apply) | `{"playlist_id":3,"path":"/Music/set.m3u8","encoding":"utf-8","path_mode":"absolute","exported_count":2,"skipped_count":0}` |
| `staging.import` | `{"imported":[{"id":"…","track_id":7}],"failed":[]}`; each imported entry carries `analysis` with `analyze:true` |
| `intake.import` | `{"item_id":3,"history_id":"…","track_id":7}`; retained with its History ID if later cleanup fails |

Audio requests accept only positive numeric database IDs. Engine resolves the file path from that track's database record; clients cannot supply arbitrary filesystem paths. Missing tracks/files return `404`, unsupported audio file types return `415`, and unavailable libraries return `503`, using structured API errors. These endpoints read the library and stream the original file without changing or re-encoding it. Browser codec support determines which files can play. The normal loopback-host and allowed-origin checks apply to metadata and audio requests.

Cross-origin audio elements must set `crossorigin="anonymous"` so the browser sends its origin. Browser media requests that omit the origin are rejected; ordinary command-line HTTP clients can still read audio.

Successful collection and staged audio responses use `Cache-Control: private, no-cache`: only private clients may cache them, and every reuse must revalidate with the server. `HEAD` returns `Last-Modified` and the complete `Content-Length` without transferring audio. Clients may reuse derived waveform peaks when both validators match; changed or missing validators require fresh analysis. Conditional `GET` requests use native HTTP `If-Modified-Since` handling, including `304` for unchanged files. Collection and staged artwork use content-based ETags and `private, no-cache` as well. Error responses, metadata, and other API data retain `no-store`.

### Bounded PCM generations

`POST /v1/tracks/{id}/pcm` and `POST /v1/staging/{id}/pcm` prepare local audio for frame-addressed playback. The same routes are available under the selected `/v1/libraries/{library}` prefix. The body is exactly `{"sample_rate":48000}` or `{"sample_rate":44100}`, with `Content-Type: application/json`; additional fields are rejected. Clients cannot supply media paths or decoder arguments. The collection read barrier covers source resolution and opening a regular file, then releases before native decoding. Unix opens initially use native nonblocking flags so a FIFO cannot wait for a writer while holding that barrier; regular-file validation and restoring blocking mode operate on the retained descriptor. Staged opens retain the native root's path confinement. Preparing PCM does not modify the collection or create history.

Successful creation returns `201`, a library-scoped `Location`, and:

```json
{"id":"opaque generation ID","sample_rate":48000,"channels":2,"format":"f32le","length_frames":null,"max_window_frames":96000}
```

`GET /v1/pcm/{id}` returns current metadata. Generation IDs belong to their originating library; accessing or deleting them through another library returns `404`. A generation retains its opened file and observes its identity, size, and modification time before and after reads. An observed replacement or mutation invalidates it. This observation is not an immutable-file guarantee against external applications, and requests do not hash or decode the complete source.

`GET /v1/pcm/{id}/windows?start_frame=0&frame_count=96000` returns interleaved stereo little-endian float32 PCM (`application/octet-stream`, eight bytes per frame). Both query parameters are required integers, appear once, and are the only accepted query parameters. `start_frame` is nonnegative; `frame_count` is from 1 through 96000. The response exposes `X-PCM-Generation`, `X-PCM-Sample-Rate`, `X-PCM-Channels`, `X-PCM-Start-Frame`, `X-PCM-Frame-Count`, and `X-PCM-Length-Frames`. A successful end-of-source read can return fewer frames, including zero. The length header is `unknown` until an authoritative end is observed, then contains the exclusive frame end. JSON metadata uses `null` for the same unknown state. Approximate track/container duration is never substituted for this value.

Frame zero follows the native presented audio timeline, including container priming, discard, edit metadata, and resampler flush. Decoding uses fixed two-second pages with native seek warmup and a fixed resampler. Overlapping requests slice the same page realization; codecs with stochastic content are not promised byte identity to a separate whole-track decode. Page regeneration uses the same anchor and filter settings. Native packet timestamps and sample counts must be contiguous and unambiguous. Discontinuous presentations fail instead of silently closing gaps or inventing samples.

Metadata probes stop at either six seconds of emitted samples or 256 output packets, with a 64 KiB metadata bound. Hitting either cap is inconclusive, not EOF. An entirely empty far seek also does not prove the source end: it returns `416 pcm_extent_unknown`; read an earlier window to discover a nonempty tail. No track-duration cap or whole-prefix audio decode is used. Native container seeking can still scan compressed input when its format lacks a usable index.

The server shares eight open generations, two concurrent decoder owners, and a 32 MiB least-recently-used page cache across libraries. Admission returns `503 pcm_busy` instead of queuing without bounds. Idle generations expire after two minutes, checked once per minute. `DELETE /v1/pcm/{id}` cancels and joins active decoding, closes the retained file, and returns `204`; server shutdown performs the same cleanup. Every started native child is waited for, including cancellation and output-budget failures. FFmpeg receives only the retained regular-file descriptor through the `fd` input protocol; nested filesystem and network protocols are excluded. A build supporting this protocol and the declared native filters/codecs is required. The CI matrix runs real native decoding and the HTTP handoff on Linux, macOS, and Windows; qualification depends on each platform's result, and a local run qualifies only its own platform.

PCM failures use safe structured codes: `pcm_invalid_request` (`400`), `pcm_generation_unavailable` (`404`), `pcm_source_changed` (`409`), `pcm_busy`, `pcm_unavailable`, and `pcm_decoder_unavailable` (`503`), `pcm_timeline_unsupported` and `pcm_decode_failed` (`422`), `pcm_extent_unknown` (`416`), and `pcm_cancelled` (`408`). Native diagnostics are never returned. Existing library, track, staged-media, loopback-host, and browser-origin checks also apply.

Fields marked `?` are optional. Playlist identifiers are positive numeric IDs encoded as strings. Track identifiers are numeric IDs encoded as strings or exact stored paths. Names, titles, basenames, and path-prefix matches are not selectors. Creation requests still specify the new entity's display name. The live operation catalog is the authoritative list of accepted parameters.

| Operation | JSON parameters |
| --- | --- |
| `migrate.export` | `source?`, `target?`; defaults to configured library and `./rekordbox` |
| `migrate.import` | `path`, `name`, `missing_media?`, `conflicts?`, `allow_unsupported?`, `dry_run?`, `plan_token?`; see the reviewed Rekordbox import contract below |
| `library.verify`, `library.refresh`, `library.prune` | `{}` |
| `library.sync` | `source`, `destination`, `scope`, `playlist_map?`, `dry_run?`, `mirror?`, `conflict?`, `media?`, `plan_token?` |
| `library.backup`, `library.backups` | `out_dir?` |
| `library.discover` | `paths?` (array; omitted or empty uses automatic locations) |
| `library.restore` | `path` |
| `library.history` | `library?`; defaults to the server's configured library |
| `library.undo` | `library?`, `id?`, `force?`, `dry_run?`; omitted ID selects latest in that library |
| `playlist.list` | `{}` |
| `playlist.inspect`, `track.inspect` | Either `ids` (array of 1–500 positive numeric IDs) or `refs` (array of stable references) |
| `system.check` | `checks?` (array; omitted or empty runs all supported checks) |
| `playlist.create` | `name`, `parent?`, `folder?` |
| `playlist.import` | `path`, `name`, `parent?`, `encoding?`, `skip_missing?`, `dry_run?`, `plan_token?`; see the reviewed import contract below |
| `playlist.export` | `identifier`, `path`, `encoding?`, `path_mode?`, `skip_unavailable?`, `dry_run?`, `plan_token?`; see the reviewed export contract below |
| `session.list` | `{}`; see play history below |
| `session.inspect` | `session` (positive numeric session ID) |
| `session.import` | `session`, `name?`, `parent?`, `skip_missing?`, `dry_run?`, `plan_token?`; see play history below |
| `playlist.rename` | `identifier`, `title` |
| `playlist.rm` | `identifier`, `force?` |
| `playlist.move` | `identifier`, `to?`, `before?`; omitted destination moves to root; `before` is a destination sibling ID, omitted appends |
| `playlist.track.add`, `playlist.track.rm` | `playlist`, `tracks` array |
| `playlist.track.move` | `from`, `to`, `tracks` array |
| `playlist.reorder` | `playlist`, `rows` array, `drop`; optional `view` array, `descending`, `dry_run`, `plan_token`; see reviewed track ordering below |
| `track.add` | `paths` array, `to_playlist?`, `move?`, `analyze?`, `bpm_range?` |
| `track.analyze` | `identifiers` (1–500), at least one of `bpm`, `key`, `grid`, plus `missing_only?`, `bpm_range?`; see track analysis above |
| `track.grid.update` | `identifier`, `anchors`; see beat-grid edits above |
| `track.list` | `{}` |
| `track.update` | `identifier`, plus at least one metadata field; see collection editing above |
| `track.batch.update` | `identifiers` (1–500), plus at least one scalar metadata field; see collection editing above |
| `track.rm` | `tracks` array, `delete_file?` |
| `track.move` | `identifier`, `destination` |
| `track.relocate` | `from`, `to` |
| `track.upgrade` | `identifier`, `query?`, `keep_timing?`, `clear_timing?`, acquisition options, `selection_token?`, `candidate_url?`; see upgrade workflow above |
| `track.artwork.normalize` | `{}` |
| `track.duplicate.list` | `type?`: `all` (default), `metadata`, or `filename-len` |
| `track.duplicate.inspect` | Either `ids` array of positive integers or `refs` array of stable references |
| `track.duplicate.merge` | `keep`, `remove` array, `performance_from?` |

Upgrade timing options are mutually exclusive. When saved cues, loops, or beat grids exist, choose whether to keep or clear them. Upgrades retain the original audio and curated metadata; analyze the replacement with `track.analyze` or in Engine DJ.

HTTP validation errors use `{ "error": { "code": "...", "message": "..." } }`. Failures after acceptance appear in job status and terminal events; a successful submission alone does not mean the operation succeeded.

### Reviewed Rekordbox XML import

`migrate.import` reads a server-local Rekordbox XML document and creates a new named playlist root in the selected Engine library. It never replaces an existing root, relocates source audio, or changes reused Engine tracks. Submit `path`, `name` and `dry_run:true` first. The preview remains available while Engine DJ Desktop is running and creates no snapshot, history or import companion.

The preview returns the exact source `document_id`, original-order `entries` with `source_id`, `path`, `status` and optional matched `track_id`; the proposed recursive `playlists`; source-only `warnings`; and `new_count`, `reuse_count`, `retained_count`, `missing_count` and `conflict_count`. `assigned_marker_color` discloses the neutral white used for supported cue/loop records whose supported XML representation has no color. Unsupported fields remain in the exact original XML archive rather than being silently discarded or invented as native data. Preview `can_apply` and `plan_token` describe the current plan; a token alone does not make a blocked plan applicable.

`missing_media` defaults to `"reject"`; `"skip"` explicitly omits reviewed unavailable sources. `conflicts` defaults to `"reject"`; `"reuse"` explicitly retains an existing matching Engine track and its metadata/performance. Source records that ambiguously resolve to the same media remain conflicts. `allow_unsupported:true` explicitly accepts the disclosed source-only warnings. An exact document already completely mapped to extant native origins is blocked. A partial import discloses retained mappings, unavailable sources and its new root/hierarchy; it does not silently create a duplicate tree or treat stale origins as complete.

Apply the same options with `dry_run:false` and the preview's `plan_token`. Before snapshots/history, the server prepares and validates the XML, native destination and actual media extents outside the write transaction, and obtains the fresh Desktop mutation check. It revalidates captured physical file identity, size/time and exact XML bytes at application, including after acquiring the native transaction. Changed observations or consent are refused. Supported new-track metadata and performance data, source order and memberships are written in one transaction. The original XML plus document-local selector to native-origin mappings are enrolled in `.engine-cli/imports.db`, participating in backup, restore, Undo, library transfer and sync. A failure publishing that companion after the Engine commit retains pending recovery history; never interpret it as a successful import or retry blindly.

A successful apply returns only `document_id`, positive `root_id`, `created_count`, `reused_count` (including retained mappings) and `skipped_count`, projected from committed changes. One collection history entry reverses the native changes and source evidence. Preview uses the concurrent collection-read barrier; apply uses the existing serialized collection-write barrier and one automatic safety snapshot unless `--no-backup` was explicitly selected.

```sh
engine-cli migrate import /Music/rekordbox.xml --name "Reviewed Rekordbox" --dry-run
engine-cli migrate import /Music/rekordbox.xml --name "Reviewed Rekordbox" --plan-token <reviewed-token>
```

Both commands support `--json`; human output lists source dispositions, proposed hierarchy and source-only warnings. Request and job failures carry stable codes; diagnostic messages are not browser display text.

### Reviewed playlist track ordering

`playlist.reorder` moves selected displayed rows to an insertion gap within one playlist's direct saved memberships. It preserves membership identities, hidden memberships and unresolved origin references. It does not change track metadata, playlist membership counts, other playlists or child playlists. Malformed or ambiguous saved membership chains are refused.

1. Submit `playlist` (numeric ID or opaque playlist reference), `rows` (selected zero-based displayed row indexes), `drop` (the insertion gap before a displayed row, or the displayed row count for the final gap), and `dry_run:true` through the selected library's operation endpoint. Omitted or `null` `view` uses every resolved track in saved order. To reorder a filtered view, supply `view` as the displayed library-local track IDs, in saved order. With `descending:true`, supply the reversed saved-order view; the resulting saved order still uses the native ascending direction. An explicit empty view, duplicated or out-of-range selection, unknown track, or view in an unrelated sort order is invalid.
2. Review `playlist_id`, the complete resulting resolved `track_ids` in saved order, `can_apply`, and `plan_token`. Hidden and unresolved memberships remain in the native chain, but unresolved memberships have no selectable library-local track ID and do not appear in `track_ids`. The preview is a collection read, works while Desktop is running, and creates no snapshot or history record. A move that leaves the order unchanged returns `can_apply:false`.
3. Apply the same playlist, view, rows, gap and direction with `dry_run:false` and the returned `plan_token`, only when `can_apply:true`. The token binds the library's location and identity, playlist metadata, complete membership identities and links, and move intent. Missing or stale review tokens and unchanged moves are rejected before Desktop checks, snapshots and history creation. Apply repeats validation in its transaction, updates only changed links and the target playlist timestamp, and returns `playlist_id` and `track_ids` held before commit. Fresh Desktop protection, one safety snapshot and one undo record cover the change. Undo restores the saved order while preserving later unrelated track metadata edits.

The CLI uses the same operation and review token:

```sh
engine-cli playlist reorder 3 --rows 0,2 --drop 5 --dry-run
engine-cli playlist reorder 3 --rows 0,2 --drop 5 --plan-token <reviewed-token>
```

`--rows` and `--view` accept comma-separated integers and repeated flags. Include the identical `--view` and `--descending` options in both commands when applicable. `--rows` and `--drop` are required; gap zero is valid. Human output prints the proposed or saved order, and preview output includes the token and whether the move can apply. `AGENT=1` emits compact labelled values; `--json` emits the structured HTTP plan or result. Progress and the successful apply's undo operation ID go to stderr.

### Reviewed M3U/M3U8 import

`playlist.import` creates a new playlist from tracks already registered in the selected library. It never acquires audio, changes track metadata, or appends to or replaces an existing playlist. Submit through that library's operation endpoint. `path` refers to a file on the server computer; `name` is the new playlist name, and optional `parent` accepts a numeric playlist ID or an opaque playlist reference. An existing sibling with the same name blocks creation.

1. Submit `dry_run:true` with `path`, `name`, and the intended options. The preview is a collection read and remains available while Engine DJ Desktop is running. It creates no snapshot or undo record.
2. Review `entries`, in source order. Each contains its one-based `line`, resolved absolute `path`, `status`, and a `track_id` when matched. Status is `matched`, `duplicate`, `missing` (unavailable or non-regular media), `unregistered` (no collection path match), or `ambiguous` (multiple matches). `track_ids` preserves the first occurrence of each matched track; repeated entries are omitted. `duplicate_count` and `unresolved_count` describe the omissions. Unresolved entries block application unless explicitly reviewed with `skip_missing:true`; zero matched tracks always blocks application.
3. Apply the same options with `dry_run:false` and the returned `plan_token`, only when `can_apply:true`. The token binds the source bytes, options, library identity/location, and resolved track identities. Changed evidence requires a new preview. Validation runs before Desktop checks, snapshots, and history, then repeats within the apply transaction. Normal fresh Desktop protection, one safety snapshot, and one undo record cover the new playlist and its ordered memberships. Undo removes the imported playlist without deleting audio or reverting unrelated later track edits.

The preview also returns `path`, `name`, numeric `parent_id` (zero for root), effective `encoding`, `skip_missing`, and `plan_token`. Apply returns only the new `playlist_id`, ordered `track_ids`, `imported_count`, `duplicate_count`, and `skipped_count`. Consumers re-read playlists after success; a failed refresh must not resubmit the import.

UTF-8 is the default; UTF-8 BOM and CRLF are supported. `.m3u8` requires UTF-8. `.m3u` additionally accepts explicit `encoding:"windows-1252"` or `"iso-8859-1"`; decoding is never guessed. Blank lines and comments, including `#EXTINF`, do not create entries. Local absolute paths, paths relative to the playlist file, and absolute local `file:` URIs (empty host or `localhost`, case-insensitive scheme) are supported. Prefix a relative POSIX filename containing a colon with `./` to distinguish it from a URI scheme. Matching uses cleaned exact stored paths, not filenames, titles, symlink aliases, or remote URLs. Windows drive paths and separators require a Windows server. HLS `#EXT-X-` directives, remote or opaque URI schemes, URI queries/fragments, control characters, files over 10 MiB, lines of 64 KiB or more, and more than 10,000 media entries are rejected.

The CLI shares this contract:

```sh
engine-cli playlist import /Music/set.m3u8 --name "Reviewed set" --dry-run
engine-cli playlist import /Music/set.m3u8 --name "Reviewed set" --plan-token <reviewed-token>
```

If unresolved entries are intentionally omitted, include `--skip-missing` in both the reviewed and applied commands. `--encoding` and `--parent` likewise remain unchanged. Human output lists the review and counts; `--json` emits the same structured plan or apply result as HTTP.

### Reviewed M3U/M3U8 export

`playlist.export` creates a new server-local playlist file from the selected playlist’s direct memberships. It follows Engine’s saved membership links, including distinct origin identities that resolve to the same media path. It does not recurse through child playlists, change the source collection, copy audio, or provide a playlist-order editor. Malformed, branching, dangling, cyclic, or ambiguous membership links fail rather than producing a partial or reordered export.

1. Submit `identifier` (numeric ID or opaque playlist reference), a destination `path` in an existing server folder (relative destinations resolve against the server working directory), and `dry_run:true`. Review the returned ordered `entries`, each with `entity_id`, optional resolved `track_id`, output `path`, display `title`, integer-second `duration`, and `status` of `ready` or `unavailable`. No destination is created. Missing, nonregular, streaming, unavailable, or unresolved-origin media is unavailable; application is blocked unless reviewed with `skip_unavailable:true`. The preview explicitly counts every omission; an empty playlist or explicitly all-skipped export produces a header-only file.
2. Apply identical options with `dry_run:false` and the returned `plan_token`, only when `can_apply:true`. The token binds the source library identity/location, destination/options, complete ordered origin memberships, metadata, media availability, and serialized bytes. Changed evidence requires a new review. Existing files, directories, and symbolic links—including dangling links—are refused. There is no overwrite option.
3. Read the completed result’s `playlist_id`, `path`, effective `encoding`, `path_mode`, `exported_count`, and `skipped_count`. No collection refresh or automatic resubmission is required. A failed or unreadable response requires inspection of the destination before another attempt.

The preview also returns `playlist_title`, `skip_unavailable`, `unavailable_count`, `can_apply`, and `plan_token`. Output uses extended M3U headers and metadata. UTF-8 is the default and the only encoding accepted for `.m3u8`; `.m3u` additionally accepts explicit `windows-1252` or `iso-8859-1`. Encoding is strict, without replacement characters. Metadata line breaks are flattened; invalid or ambiguous path lines are rejected. The default `path_mode:"absolute"` writes absolute server media paths; `"relative"` writes paths relative to the destination folder with forward slashes and an explicit `./` prefix when needed to distinguish comments or URI schemes. Relative paths cannot cross Windows volumes. POSIX filenames containing literal backslashes, control characters, invalid UTF-8, or surrounding whitespace are rejected. Moving a relative export without its media layout breaks its references.

Export retains catalog effect `read` because it never modifies Engine data. Preview uses the collection read barrier; application also enters the shared write scheduler for its output file. Both remain available while Desktop is running and create no snapshot, history record, or undo entry. Native exclusive creation through an open destination directory supports filesystems such as FAT/exFAT without hard-link publication. The new file can be visible while being written; a successful response follows writing, syncing, and closing. Failures and cancellation attempt to remove only the created file when its identity still matches. Cleanup is best-effort and cannot guarantee rollback if the destination is concurrently changed.

The CLI shares the same review and application:

```sh
engine-cli playlist export 3 --path /Music/set.m3u8 --dry-run
engine-cli playlist export 3 --path /Music/set.m3u8 --plan-token <reviewed-token>
```

Keep `--encoding`, `--path-mode`, and `--skip-unavailable` identical between review and apply. Human output lists ordered review entries and counts; `--json` emits the structured HTTP plan or result.

## Play history

Engine DJ records played sets in `Database2/hm.db`, beside the library's `m.db`. Engine CLI reads that database read-only and never creates, changes, or repairs it. Its `Track` table holds copies of played tracks; each play resolves to a library track by exact stored path first and then by origin database UUID and origin track ID, as Engine's own Import as Playlist does. Plays that resolve to no library track remain listed as `missing`.

`GET /v1/sessions` and `session.list` return `{"available":true,"sessions":[...]}`, newest first by start time and then ID, like Engine's history list. Sessions Engine marks deleted, and sessions without a start time, are omitted. Without a history database the result is `{"available":false,"sessions":[]}`. Each session has:

| Field | Meaning |
| --- | --- |
| `id` | Session ID for inspection and import |
| `title` | Saved title, or Engine's label for an untitled session: `dd/MM/yyyy HH:mm (City)` in the recorded zone, where City is the zone ID's last segment; an empty or unknown zone uses the server's local time without a city. Engine files sessions whose start time is zero under "Older" with their raw title, so an untitled one has an empty title |
| `is_titled` | Whether `title` was saved rather than generated |
| `start_time` | Unix seconds, or `null` when the recorded start is zero |
| `timezone` | IANA zone ID Engine recorded, or empty |
| `drive` | Name of the drive the session was played from, or empty for this computer |
| `track_count` | Number of recorded plays, including repeats |

`GET /v1/sessions/{id}` and `session.inspect` add `entries` in Engine's play order (start time, then ID). Each entry has a one-based `position`, the played copy's `title`, `artist`, and `length`, `played_at` in Unix seconds, `status` (`available` or `missing`), and `track_id` for available plays. `played_at` is `null` for sessions without a timezone, which Engine shows without times. Both routes are also available under `/v1/libraries/{key}`, share the collection read barrier, and remain available while Desktop is running. Missing history returns `404 play_history_unavailable`, a missing or deleted session `404 session_not_found`, a malformed ID `400 invalid_session`, and an unreadable history database `503 play_history_unreadable`.

Play counts count history entries, because Engine keeps no play-count column; its history views count entries the same way. `lastPlayed` ignores plays without a reliable time. Plays are optional on track records: when the history database exists but cannot be read, track records report `null` like a library without history, and the session routes report `play_history_unreadable`. The session routes report a missing library as `404 library_not_found`.

### Reviewed playlist from a played session

`session.import` creates a new playlist holding a session's tracks in play order. It never changes the history, track metadata, or an existing playlist.

1. Submit `session` and `dry_run:true`. The preview is a collection read, remains available while Desktop is running, and creates no snapshot or undo record. Without `name`, the playlist takes the session's `title`; when a sibling already uses it, the name is numbered one past the highest numbered sibling (`Title 2`, `Title 3`, …), as Engine names imported history. An untitled session without a start time has no title, so it requires `name`. An explicit `name` is used exactly; a sibling with that name blocks creation. `parent` accepts a numeric playlist ID or an opaque playlist reference; omitted uses the collection root.
2. Review `entries`, in play order (start time, then ID), the order Engine's history track list numbers them. Engine's own import reads entries without an explicit order. Playlists hold each track once, so a repeated play keeps the track's first position and is marked `duplicate`. Plays of tracks no longer in the library are `missing` and block application unless reviewed with `skip_missing:true`; a session with no library track always blocks. Engine instead keeps such plays as references to their origin database; Engine CLI creates memberships only for tracks in the library. The preview returns `session`, `session_title`, `name`, `parent_id` (zero for root), `skip_missing`, `entries`, ordered `track_ids`, `duplicate_count`, `missing_count`, `can_apply`, and `plan_token`.
3. Apply the same options with `dry_run:false` and the returned `plan_token`, only when `can_apply:true`. The token binds the session's plays, options, destination, and library identity. Validation runs before Desktop checks, snapshots, and history, then repeats inside the apply transaction. Fresh Desktop protection, one safety snapshot, and one undo record cover the new playlist and its memberships; undo removes the playlist.

The CLI shares these reads and the reviewed import:

```sh
engine-cli session list
engine-cli session inspect 12
engine-cli session import 12 --dry-run
engine-cli session import 12 --plan-token <reviewed-token>
```

Keep `--name`, `--parent`, and `--skip-missing` identical between review and apply. Human output prints tables and counts; `--json` emits the structured HTTP results.
