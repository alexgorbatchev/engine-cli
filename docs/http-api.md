# HTTP API and progress

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

`track.list` returns every collection track, including tracks outside playlists, in ascending ID order. Its result is an array using the same track fields as `playlist.inspect`. An empty collection returns `[]`.

Audio requests accept only positive numeric database IDs. Engine resolves the file path from that track's database record; clients cannot supply arbitrary filesystem paths. Missing tracks/files return `404`, unsupported audio file types return `415`, and unavailable libraries return `503`, using structured API errors. These endpoints read the library and stream the original file without changing or re-encoding it. Browser codec support determines which files can play. The normal loopback-host and allowed-origin checks apply to metadata and audio requests.

Cross-origin audio elements must set `crossorigin="anonymous"` so the browser sends its origin. Browser media requests that omit the origin are rejected; ordinary command-line HTTP clients can still read audio.

Fields marked `?` are optional. Identifiers accept the same track or playlist names, numeric IDs, and applicable paths as the CLI. The live operation catalog is the authoritative list of accepted parameters.

| Operation | JSON parameters |
| --- | --- |
| `library.verify`, `library.sync`, `library.prune` | `{}` |
| `library.backup`, `library.backups` | `out_dir?` |
| `library.discover` | `paths?` (array; omitted or empty uses automatic locations) |
| `library.restore` | `path` |
| `library.history` | `{}` |
| `library.undo` | `id?`, `force?`, `dry_run?`; omitted ID selects latest |
| `playlist.list` | `{}` |
| `playlist.inspect` | `identifier` |
| `playlist.create` | `name`, `parent?`, `folder?` |
| `playlist.rm` | `identifier`, `force?` |
| `playlist.move` | `identifier`, `to?`; omitted destination moves to root |
| `playlist.track.add`, `playlist.track.rm` | `playlist`, `tracks` array |
| `playlist.track.move` | `from`, `to`, `tracks` array |
| `track.add` | `paths` array, `to_playlist?`, `move?` |
| `track.list` | `{}` |
| `track.rm` | `tracks` array, `delete_file?` |
| `track.move` | `identifier`, `destination` |
| `track.relocate` | `from`, `to` |
| `track.upgrade` | `identifier`, `query?`, `keep_timing?`, `clear_timing?` |
| `track.artwork.normalize` | `{}` |
| `track.duplicate.list` | `type?`: `all` (default), `metadata`, or `filename-len` |
| `track.duplicate.inspect` | `ids` array of positive integers |
| `track.duplicate.merge` | `keep`, `remove` array, `performance_from?` |

Upgrade timing options are mutually exclusive. When saved cues, loops, or beat grids exist, choose whether to keep or clear them. Upgrades retain the original audio and curated metadata; the replacement needs analysis in Engine DJ.

HTTP validation errors use `{ "error": { "code": "...", "message": "..." } }`. Failures after acceptance appear in job status and terminal events; a successful submission alone does not mean the operation succeeded.
