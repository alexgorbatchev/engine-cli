# HTTP API and progress

See [audio duplicates, stable references, and sync](fingerprints.md) for library registration, fingerprint scans, reference-based batch selection, and audio reuse during sync.

## Runtime checks and mutation protection

`GET /v1/system/check` returns runtime facts without selecting or opening a library. Use `?checks=engine_desktop` to select checks, or omit it for all supported checks. Repeated/comma-separated check IDs are accepted and deduplicated. Unknown IDs return `400`. The equivalent job operation is `system.check` with `{"checks":["engine_desktop"]}`; the CLI is `engine-cli system check --json` (`--checks` selects checks).

The response contains `checked_at` (UTC timestamp), `platform`, and a `checks` object keyed by check ID. `engine_desktop` contains `status` (`ok`, `error`, or `unsupported`), `running` (boolean when known, otherwise `null`), and `pids` (matching process IDs, always an array). Failed checks also contain `error: {code, message}`. A completed HTTP check returns `200` even when a particular check is unavailable; inspect its status rather than treating HTTP success as “safe to edit.”

Desktop detection uses native process snapshots on macOS and Windows and matches the Engine DJ executable name, not command-line substrings. Other platforms report `unsupported`; they do not report “not running.” The check is an observation, not a library lock or a guarantee that Engine DJ will not start later.

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

Jobs move from `queued` to `running`, then `succeeded`, `failed`, or `cancelled`. Operations run one at a time. A disconnected client does not cancel its job. Cancellation is cooperative and can leave completed changes; use library history to inspect and undo changes. Shutdown requests cancellation and waits for workers.

The job's `result` depends on the operation. Track fields use JSON values and `null` for missing metadata. A mutation can return a `history_id` identifying its undo record. When a terminal event becomes visible, the final job result and state are also available.

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

### Collection editing and artwork

`track.update` accepts `identifier` plus any supplied `title`, `artist`, `album`, `genre`, `comment`, `label`, `composer`, `remixer`, `year`, and `rating_stars`. At least one editable field is required. Omitted fields remain unchanged. Optional text can be cleared with an empty string; titles must contain text. Year is an integer from 0 to 9999, with 0 clearing the year. Rating is an integer from 0 (unrated) to 5 stars. Track responses include `ratingStars`; the stored `rating` field uses Engine's 0–100 scale. These edits change collection metadata, leave audio files and saved timing untouched, and support ordinary snapshots and undo. JSON null does not clear a field.

The matching CLI command is `engine-cli track update <tr|id|path>` with `--title`, `--artist`, `--album`, `--genre`, `--comment`, `--label`, `--composer`, `--remixer`, `--year`, `--rating`, `--artwork <path>`, and `--clear-artwork` flags. `--json` prints the updated track. Only supplied flags change fields.

`playlist.rename` accepts `identifier` and a nonempty `title`, preserving playlist identity, parent, and memberships. The matching command is `engine-cli playlist rename <pl|id> <name> [--json]`. Renaming uses the same snapshot, desktop protection, and undo handling as other collection mutations.

`PUT /v1/tracks/{id}` accepts a multipart upload with one `artwork` file (PNG, JPEG, or GIF, at most 8 MiB and 40 megapixels) and an optional `metadata` JSON object using the `track.update` fields. The response is an ordinary accepted job with the same desktop guard, snapshot, progress, and undo behavior. Metadata and the artwork link are committed together. The image is center-cropped to a square, stored as a 1400×1400 JPEG cache image and a 256×256 thumbnail, and SHA-1 tracked. Audio files remain untouched. Invalid uploads return `400`; oversized requests return `413`. JSON `track.update` can clear the artwork link with `clear_artwork:true`, or use a server-local image with `artwork_path`. These artwork actions are mutually exclusive.

`GET /v1/tracks/{id}/artwork?size=full` prefers the full cached image, falling back to the thumbnail when unavailable. The default serves the thumbnail.

`GET /v1/tracks/{id}/artwork` serves the linked artwork as PNG, JPEG, WebP, or GIF. It reads inline AlbumArt bytes first, then resolves Engine's `Artwork/` cache from the linked binary or hexadecimal SHA-1 hash, followed by the track's source hash. Cache names use URL-safe base64 or hexadecimal hashes. The identifier can be a local numeric ID or stable track reference. Missing artwork returns `404`; unsupported image content returns `415`. This endpoint is read-only and does not regenerate artwork or change the library.

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
| `playlist.move` | `identifier`, `to?`; omitted destination moves to root |
| `playlist.track.add`, `playlist.track.rm` | `playlist`, `tracks` array |
| `playlist.track.move` | `from`, `to`, `tracks` array |
| `track.add` | `paths` array, `to_playlist?`, `move?` |
| `track.list` | `{}` |
| `track.update` | `identifier`, plus at least one metadata field; see collection editing above |
| `track.rm` | `tracks` array, `delete_file?` |
| `track.move` | `identifier`, `destination` |
| `track.relocate` | `from`, `to` |
| `track.upgrade` | `identifier`, `query?`, `keep_timing?`, `clear_timing?` |
| `track.artwork.normalize` | `{}` |
| `track.duplicate.list` | `type?`: `all` (default), `metadata`, or `filename-len` |
| `track.duplicate.inspect` | Either `ids` array of positive integers or `refs` array of stable references |
| `track.duplicate.merge` | `keep`, `remove` array, `performance_from?` |

Upgrade timing options are mutually exclusive. When saved cues, loops, or beat grids exist, choose whether to keep or clear them. Upgrades retain the original audio and curated metadata; the replacement needs analysis in Engine DJ.

HTTP validation errors use `{ "error": { "code": "...", "message": "..." } }`. Failures after acceptance appear in job status and terminal events; a successful submission alone does not mean the operation succeeded.
