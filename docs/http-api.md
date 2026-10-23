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

Other browser origins are rejected. The server has no authentication and is intended for local applications. Global `--no-backup` disables automatic safety snapshots; it does not disable undo history.

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
request. Empty playlists return `{"items":[]}` without launching a harness.

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

Results include every selected track in deterministic order:

```json
{"items":[{"id":12,"current":{"title":"Artist - Song","artist":"Artist"},"suggestion":{"title":"Song","artist":"Artist","reason":"Removed duplicated artist prefix."}},{"id":34,"current":{"title":"Clean title","artist":null},"suggestion":null}]}
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
harness. A failed or cancelled batch discards all earlier batches; the job never
returns partial success. Progress
advances after validated batches, with 100 reserved for successful completion.

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

Engine database connections wait up to five seconds for SQLite locks. Direct GET routes are outside the job scheduler; the busy timeout does not protect them from in-place backup restoration.

A disconnected client does not cancel its job. Cancellation is cooperative and can leave completed changes; use library history to inspect and undo changes. Shutdown requests cancellation and waits for workers.

The job's `result` depends on the operation. Track fields use JSON values and `null` for missing metadata. Collection tracks include `hasArtwork`: true when inline artwork bytes or a nonempty regular cache file exists, false when absent (including dangling artwork IDs), and null when availability cannot be determined. Collection lists, single/batch inspection, and playlist tracks share this field. Availability checks read artwork catalogue metadata in one batch and reuse filesystem observations for shared covers; they do not load image bytes. A mutation can return a `history_id` identifying its undo record. When a terminal event becomes visible, the final job result and state are also available.

## Failure codes

Request and job errors use `{code,message}`. The code is the stable consumer contract; `message` is diagnostic text and must not be displayed directly in a user interface. Map known codes to user-friendly guidance and use a safe generic message for unknown codes. Do the same for partial failures inside results and upstream progress details.

Progress events, acquisition results (including per-track, tag-file, and dependency failures), staged items, import and migration failures, and newly recorded history entries retain string-valued `error` and add `error_code`. Saved migration state pairs `errorMsg` with `error_code`. Successful records omit the code. Persisted records written before codes were introduced may lack it; consumers must handle that as an unclassified failure without displaying the diagnostic text.

| Code | Meaning and recovery |
| --- | --- |
| `library_not_found` | The configured library database is absent. Add or connect the library on the server computer, then refresh. No database is created by this read. |
| `library_unavailable` | The library could not be opened. Check the selected library and drive. |
| `permission_denied` | File access was denied. Check permissions on the server computer. |
| `resource_not_found` | A required file is missing. Restore access or choose another file. |
| `tool_unavailable` | A required managed tool is not installed or cannot run. Install it with `tool.upgrade`. |
| `tool_outdated` | An installed managed tool is older than the release providing Engine's machine interface, reports an unrecognized version, or lacks that interface. Update it with `tool.upgrade`. |
| `tool_install_failed` | Downloading or installing a managed tool release failed, through `tool.upgrade` or `auto_install`. Check the connection and retry. |
| `tool_failed` | A dependent music tool reported a failure, including partial result failures. Check the tool and retry. |
| `suggestion_selection_failed` | A selected entity is missing, unreadable, or has oversized metadata. Refresh the selection before retrying. No model result is applied. |
| `harness_unavailable` | The selected Claude Code or Codex executable is unavailable; install and authenticate it on the server computer. |
| `unsupported_option` | The harness/model does not support a requested option. Choose a supported setting. |
| `configuration_failed` | Native harness configuration could not be prepared. Check the supplied configuration paths. |
| `harness_failed` | The native harness call failed. Check its login, configuration, and connectivity. |
| `invalid_input`, `invalid_schema`, `input_invalid` | agent-ask-cli rejected the supplied data or schemas. These are generated by Engine; report persistent failures. |
| `output_invalid` | agent-ask-cli rejected the harness response, or Engine received an invalid wrapper result. Retry or choose another model. |
| `output_failed` | agent-ask-cli could not write its result. Retry after checking the installation. |
| `llm_response_invalid` | Suggestions failed identity or metadata checks. Retry or choose another model; no partial suggestions are returned. |
| `timed_out` | The operation exceeded its deadline. Check the connection and retry. |
| `cancelled` | Work was cancelled. Completed changes may remain. |
| `operation_failed` | Unclassified failure. Inspect current state before retrying. |
| `invalid_response` | Engine could not encode a response. Refresh or update Engine. |

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

The preview result contains `applicable`, `plan_token`, `summary`, `changes`, `conflicts`, and `warnings`. Each change identifies its entity type, stable identity, action, and available source/destination numeric IDs; labels are display-only. The summary reports track/playlist additions, updates and removals, file-copy counts, bytes to copy/preserve, available bytes, and estimated required space including database snapshots. A successful preview job may have `applicable:false`: show its conflicts before allowing application.

Apply by submitting the same request with `dry_run:false` and the returned `plan_token`. An outdated token fails before backups or mutation. Apply recomputes the comparison even without a token. Dry runs never create backups, history, media, or sync-state files. Unchanged repeated applications return no operation ID when the baseline is current.

Default `conflict:"error"` refuses divergent edits. Explicit `source` or `destination` chooses the winning side of a conflict. Destination-only changes remain unless mirroring requires reconciliation. Default `media:"changed"` copies missing/changed audio; `none` requires matching existing audio and still permits artwork transfer. `mirror:true` removes entries absent from the selected source scope, preserving tracks referenced by kept playlists. Reverse the endpoints to sync back; USB uses the same behavior as any other library path.

The applied result's `operation_id` and job's `history_id` identify undo history in the destination. Submit `library.undo` with `{"library":"/Volumes/DJ_USB/Engine Library","id":"<operation_id>"}` to undo there, or `library.history` with the same `library` field to list its operations. The server's configured library remains unchanged. Interrupted operations retain recovery data; undo a pending operation with `force:true` after inspecting it. Do not run Engine DJ during sync application or recovery.

## Saved beat grids

The beat-grid response contains `grid.sampleRate` and `grid.anchors`. Each anchor has `sample` (sample position, which can be negative for preroll), `beat` (zero-based beat index), and `beatsUntilNext` (the outgoing segment's beat count). Interpolate the interval between adjacent anchors using `beatsUntilNext`, then divide sample positions by `sampleRate` for seconds. Every fourth beat is a bar boundary. The last anchor ends the preceding segment.

The API reads the current saved grid from `PerformanceData.beatData`; it does not manufacture a grid from a track's BPM or modify analysis. Missing analysis returns `{"grid":null}`. Invalid compressed payloads return `422` with code `invalid_beatgrid`; decoded payloads are limited to 8 MiB. The Qt framing and mixed-endian anchor format follow the existing [Engine DJ app codec](https://github.com/alexgorbatchev/engine-dj-app/tree/main/src/beat_grid) and its [compression framing](https://github.com/alexgorbatchev/engine-dj-app/blob/main/src/compression.rs).

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
| `type` | `phase_start`, `progress`, `candidate_found`, `candidate_selected`, `complete`, `error`, or `cancelled`; HTTP adds `queued`, and acquisition can forward other Fetch Track event types such as `metadata_found` and `tagged` |
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

## Operations

### Staged music

Staged acquisitions live under `<lib-dir>/.engine-cli/staging/`, independently of collection rows. Each item has a stable opaque `id`, `kind` (`track` or `mix`), `title`, `status` (`pending`, `ready`, or `failed`), and UTC `created_at`. Optional fields include `artist`, `album`, `source_url`, `query`, `error`, `audio_path`, `artwork_path`, `parent_id`, and `index`. Paths are relative to staging storage so moving the library preserves acquisitions. A track's `parent_id` identifies its mix; `index` preserves mix order. Failed or missing acquisitions remain available for retry after restarting the server. A failed `track.fetch`, `mix.fetch`, or `staging.retry` whose changes stay inside its own staged items records no undo history, and its job has no `history_id`; discard the failed item with `staging.rm` instead. A failure that changed the collection or files outside those items keeps its undo record.

`GET /v1/staging` and the `staging.list` job return a flat array, including mix groups and their child tracks; empty storage returns `[]`. `staging.inspect` accepts `{"id":"..."}` and returns one item, with an additional `items` array for a mix's children. `staging.duplicates` accepts the same parameters and returns collection candidates using the existing case-insensitive, trimmed title-and-artist metadata matcher. These candidates are evidence for review, not proof of identical audio.

`GET /v1/staging/{id}/audio` and `/artwork` stream the staged item's media, with normal origin checks and HTTP byte ranges. Media paths cannot be supplied by clients; paths and symlinks are confined to staging storage. Missing items or media return `404`; unsupported media types return `415`.

`staging.import` accepts `{"ids":["..."],"to_playlist":"optional existing playlist ID"}`. Individual track IDs select tracks; a mix ID selects all its child tracks. Repeated selections are deduplicated. The result contains `imported` (`{id,track}` entries) and `failed` (`{id,error}` entries), always arrays. Failed/pending children remain staged. Each ready track is installed under `Music/staging/<id>/`, imported through the ordinary Engine track operation, and optionally assigned to the playlist before its staged item is removed. Failed imports retain their acquisition for retry. If collection import succeeds but staged cleanup fails, the same ID appears in both arrays with a `track imported but staged cleanup failed` error; the acquisition stays staged for retry, which reuses the existing collection track. Completed mix groups disappear only after all children have been imported. Partial success is represented by both arrays; inspect `failed` even when the job succeeds.

`staging.rm` accepts `{"ids":["..."]}` and returns `{"removed":["..."]}`. Selecting a mix removes its children too. Removal records the actual manifests and media in undo history and leaves collection rows untouched. Import requires the ordinary fresh desktop check and safety snapshot. Undoing import restores staged acquisitions and reverses installed files, collection records, and playlist changes. Stage-only removal does not require a desktop check or collection snapshot.

The CLI equivalents are `engine-cli staging list [--json]`, `staging inspect <id> [--json]`, `staging rm <ids...> [--json]`, and `staging import <ids...> [--to-playlist <id>] [--json]`.

The operation catalog includes `effect`: `read`, `staging`, `collection`, `filesystem`, or `system`. `mutates` identifies an operation whose base effect changes state; `effect` distinguishes its scope. Collection effects receive desktop protection, safety snapshots, and database snapshots in their undo history (`scope: "collection"` in `library.history` entries). Staging and filesystem effects never write Engine databases: their undo history (`scope: "files"`) preserves only the files they change and copies no database. Entries recorded before scopes existed list as `collection`. A fetch request that also imports is treated as a collection effect when executed. `parameter_effects:{"auto_install":"system"}` identifies the additional tool-install effect when that parameter is true, including read operations and dry runs. `staging.retry` reports `additional_effects:["system"]` because saved acquisition options may install missing tools. These additional effects do not replace collection or staging protection.

`mix.inspect` previews a tracklist without writing library files or undo history. `track.candidates` and `mix.candidates` persist selection tokens, so they have a staging effect. These three lookup operations accept query and acquisition options; import, playlist assignment, and candidate continuation parameters belong only to fetch operations.

`track.verify` accepts exactly one of `target` (URL or server-local path), `identifier` (collection track), or `staging_id`, plus `min_bandwidth_khz` and `auto_install`. Verification does not require a collection. The CLI is `track verify [url|path|identifier]`, with `--target` for an explicit URL/path, `--staging-id`, `--min-bandwidth-khz`, and `--auto-install`.

`track.tags.inspect`, `track.tags.update`, and `track.artwork.embed` accept exactly one selector: `identifier`, `staging_id`, `path`, or an ordered array `identifiers`, `staging_ids`, or `paths` (at most 200 entries). Single selectors return the ordinary single-file report. Arrays return `{status,results:[{identifier|staging_id|path,result,error?}],error?}` in request order, including repeated selectors. Any failed file makes the operation fail with an aggregate error; completed siblings and their undo record remain available in the job result. Cancellation retains results for files already processed.

Tag updates support nullable string overrides `artist`, `title`, `album`, `date`, `genre`, `comment`, plus `artwork`, `source_url`, `refresh`, `dry_run`, `no_cache`, and `auto_install`. Explicit empty strings clear tags; omitted fields retain native lookup behavior. Preview results preserve comments, including explicitly cleared comments. Artwork embedding accepts only the selector and required `artwork` URL/path. Inspect accepts only the selector. The CLI commands are `track tags inspect [path|identifier...]`, `track tags update [path|identifier...]`, and `track artwork embed [path|identifier...] --artwork <URL|path>`, with `--path`, `--paths`, `--staging-id`, and `--staging-ids` for explicit selection and hyphenated update flags.

Raw file mutations resolve collection ownership, including symlink and hard-link aliases, before execution. Collection files use the normal desktop guard, snapshot, metadata update, and undo workflow. Staged files update their manifests and retain staging undo. Other files use durable file history and an atomic replacement only after verifying compressed audio packets are unchanged. Native outputs must remain inside a private workspace and preserve the file extension. A failed validation retains the original file; undo restores completed replacements.

Engine requires `fetch-track` 1.10.1, `fetch-mix` 2.1.1, and `tag-track` 1.1.1 or newer, the releases providing its machine interfaces. Before running one, Engine reads its `--version` and refuses a missing tool with `tool_unavailable` or an older or unrecognized version with `tool_outdated`; `dev` builds pass the version check. With `auto_install:true`, Engine first installs a missing tool or updates an outdated one into the managed tool directory, then runs it only when the installed release meets the requirement; a failed download reports `tool_install_failed`, and a latest release that is still too old reports `tool_outdated`. The same option asks the tool to install its own missing dependencies.

`tool.inspect` reports `version`, `minimum_version`, executable availability (`available`), whether the tool meets Engine's requirement (`satisfied`), and machine-interface support (`native_json`). A satisfied version is confirmed by safely probing `--json dependencies`; inspection never installs anything. Supported tools include their typed dependency reports, including unsatisfied dependencies. Unsatisfied tools return `tool_unavailable` or `tool_outdated` with an actionable message and are not probed.

`tool.upgrade` installs the tool's latest GitHub release into Engine's managed tool directory (`$XDG_DATA_HOME/engine-cli/bin`, falling back to `~/.local/share/engine-cli/bin`) through godeps, without running the installed tool's own updater. Engine puts this directory ahead of `PATH` at startup, so managed releases take precedence over other copies. The result has `status` `success`, or `current` when the satisfied version did not change, and the installed `version`. Download failures return `tool_install_failed`; a release that still does not meet the requirement returns `tool_outdated`. `tool.dependencies.*` manage the tool's own upstream dependencies through that tool.

`track.upgrade` accepts the same acquisition options as `track.fetch`: `sources`, `skip_verify`, `skip_metadata`, `no_cache`, `auto_install`, `min_bandwidth_khz`, nullable `confirm_fingerprint`, and `js_runtime`. Omitting fingerprint confirmation preserves the upstream default; explicitly setting `false` disables it. Engine independently verifies replacement quality even when upstream verification is skipped. Curated metadata, playlist membership, the original audio, snapshot protection, and undo remain part of the upgrade operation.

To upgrade from a reviewed source, first call `track.candidates`, then supply its `selection_token`, a listed `candidate_url`, and the exact original `query` and acquisition options to `track.upgrade`, together with the collection `identifier` and timing choice. Tokens bind the original request and permitted candidate URLs; an arbitrary URL without a matching token is rejected before acquisition. The equivalent CLI supports `track upgrade <id|path> --query <query> --selection-token <token> --candidate-url <url>` and the corresponding hyphenated acquisition flags. `--json` writes the same normalized upgrade result as the API to stdout: `track`, `original_path`, `timing_cleared`, and `reanalysis_required`. Progress and the undo operation ID go to stderr in JSON mode.

### Collection editing and artwork

`track.update` accepts `identifier` plus any supplied `title`, `artist`, `album`, `genre`, `comment`, `label`, `composer`, `remixer`, `year`, and `rating_stars`. At least one editable field is required. Omitted fields remain unchanged. Optional text can be cleared with an empty string; titles must contain text. Year is an integer from 0 to 9999, with 0 clearing the year. Rating is an integer from 0 (unrated) to 5 stars. Track responses include `ratingStars`; the stored `rating` field uses Engine's 0–100 scale. These edits change collection metadata, leave audio files and saved timing untouched, and support ordinary snapshots and undo. JSON null does not clear a field.

The matching CLI command is `engine-cli track update <tr|id|path>` with `--title`, `--artist`, `--album`, `--genre`, `--comment`, `--label`, `--composer`, `--remixer`, `--year`, `--rating`, `--artwork <path>`, and `--clear-artwork` flags. `--json` prints the updated track. Only supplied flags change fields.

`playlist.rename` accepts `identifier` and a nonempty `title`, preserving playlist identity, parent, and memberships. The matching command is `engine-cli playlist rename <pl|id> <name> [--json]`. Renaming uses the same snapshot, desktop protection, and undo handling as other collection mutations.

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

Audio requests accept only positive numeric database IDs. Engine resolves the file path from that track's database record; clients cannot supply arbitrary filesystem paths. Missing tracks/files return `404`, unsupported audio file types return `415`, and unavailable libraries return `503`, using structured API errors. These endpoints read the library and stream the original file without changing or re-encoding it. Browser codec support determines which files can play. The normal loopback-host and allowed-origin checks apply to metadata and audio requests.

Cross-origin audio elements must set `crossorigin="anonymous"` so the browser sends its origin. Browser media requests that omit the origin are rejected; ordinary command-line HTTP clients can still read audio.

Successful collection and staged audio responses use `Cache-Control: private, no-cache`: only private clients may cache them, and every reuse must revalidate with the server. `HEAD` returns `Last-Modified` and the complete `Content-Length` without transferring audio. Clients may reuse derived waveform peaks when both validators match; changed or missing validators require fresh analysis. Conditional `GET` requests use native HTTP `If-Modified-Since` handling, including `304` for unchanged files. Collection and staged artwork use content-based ETags and `private, no-cache` as well. Error responses, metadata, and other API data retain `no-store`.

Fields marked `?` are optional. Playlist identifiers are positive numeric IDs encoded as strings. Track identifiers are numeric IDs encoded as strings or exact stored paths. Names, titles, basenames, and path-prefix matches are not selectors. Creation requests still specify the new entity's display name. The live operation catalog is the authoritative list of accepted parameters.

| Operation | JSON parameters |
| --- | --- |
| `migrate.export` | `source?`, `target?`; defaults to configured library and `./rekordbox` |
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
| `playlist.rename` | `identifier`, `title` |
| `playlist.rm` | `identifier`, `force?` |
| `playlist.move` | `identifier`, `to?`, `before?`; omitted destination moves to root; `before` is a destination sibling ID, omitted appends |
| `playlist.track.add`, `playlist.track.rm` | `playlist`, `tracks` array |
| `playlist.track.move` | `from`, `to`, `tracks` array |
| `track.add` | `paths` array, `to_playlist?`, `move?` |
| `track.list` | `{}` |
| `track.update` | `identifier`, plus at least one metadata field; see collection editing above |
| `track.rm` | `tracks` array, `delete_file?` |
| `track.move` | `identifier`, `destination` |
| `track.relocate` | `from`, `to` |
| `track.upgrade` | `identifier`, `query?`, `keep_timing?`, `clear_timing?`, acquisition options, `selection_token?`, `candidate_url?`; see upgrade workflow above |
| `track.artwork.normalize` | `{}` |
| `track.duplicate.list` | `type?`: `all` (default), `metadata`, or `filename-len` |
| `track.duplicate.inspect` | Either `ids` array of positive integers or `refs` array of stable references |
| `track.duplicate.merge` | `keep`, `remove` array, `performance_from?` |

Upgrade timing options are mutually exclusive. When saved cues, loops, or beat grids exist, choose whether to keep or clear them. Upgrades retain the original audio and curated metadata; the replacement needs analysis in Engine DJ.

HTTP validation errors use `{ "error": { "code": "...", "message": "..." } }`. Failures after acceptance appear in job status and terminal events; a successful submission alone does not mean the operation succeeded.
