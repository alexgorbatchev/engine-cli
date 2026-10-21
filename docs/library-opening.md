# Open libraries at explicit paths

## Create a library

Run `bin/engine-cli library create "/Music/New library" --json`, or submit the
`library.create` job with `{"path":"/Music/New library"}`. The path is on the
computer running Engine. The parent path is created as needed; existing audio
files in that directory remain untouched. Creation refuses any existing
`Database2` directory, including an empty directory or symlink.

The result contains the absolute `path` and the new Engine `uuid`. Native schema
3.0.2 includes origin identities, performance-data creation, playlist ordering,
and uniqueness constraints. Creation requires a fresh desktop status check and
records undo history inside the new library. There is no pre-existing database
to snapshot. Use `library.undo` with the new library's path to undo creation;
later edits can cause conflicts and are not silently discarded.

Creation does not change the currently selected library. Open the result path
using `/v1/libraries` to operate on it. The UI exposes creation in All tools and
the library sidebar with a required path and explicit confirmation. Sidebar
creation opens the completed library automatically.

## Open existing libraries

Automatic `library.discover` remains a read-only search of expected locations.
Opening a library is a separate action and accepts its exact directory, containing
`Database2/m.db`. Opening does not create a database, register an identity, write
history, or change collection data.

Start the compiled API with one or more explicit libraries:

```sh
bin/engine-cli api serve --library "/Music/Engine Library" --library "/Volumes/DJ/Engine Library"
```

Repeat `--library` for each path. Commas are part of a path, not separators.
Alternatively, set `ENGINE_LIBRARY_PATHS="/Music/Engine Library;/Volumes/DJ/Engine Library"`
before starting `api serve`. Semicolons separate exact library directories;
surrounding whitespace and empty entries are ignored. Explicit `--library` flags
replace the environment list. Invalid environment paths prevent startup just like
invalid flag paths. This setting does not recursively discover libraries.
The global `--lib-dir` continues to select the library for unscoped `/v1/*`
requests. Explicit libraries receive independent URLs; they do not change that
selection. An invalid startup path prevents the server from starting.

While the server is running, send `POST /v1/libraries` with
`Content-Type: application/json`:

```json
{"paths":["/Music/Engine Library","/Volumes/DJ/Engine Library"]}
```

The response is `200` with an array of `{path,url,default}` objects, in input order with
duplicate paths removed. Paths are absolute and symlinks are resolved. Each
`url` is a server-relative API base, such as `/v1/libraries/<opaque-key>`.
Append `/operations`, `/operations/playlist.list`, `/tracks/12/audio`, or the
other existing resource paths directly to that base, without another `/v1`.
Job `Location` headers use the same library base, as do status, cancellation,
and event-stream requests. Numeric track and playlist IDs and job IDs remain
scoped to their library.

`GET /v1/libraries` returns opened libraries sorted by path, including the configured
default library when its database is readable. Its `default` field is true;
other libraries have false. Reopening a path
reuses its server and jobs. The open list lasts for this server process; supply
startup flags or reopen paths after a restart. Manage this list through the
top-level `/v1/libraries` endpoint, not through a scoped library URL.

Requests accept 1–64 paths, with at most 64 libraries open per server. Missing
paths, unreadable databases, missing track/playlist tables, unknown JSON fields,
and empty lists are rejected. The request validates all paths before adding any
libraries. Opening reads existing databases with SQLite read-only mode.

Opened libraries retain browser-origin protection, fresh desktop checks before
mutations, safety snapshots, and undo history in their own directories.
Operations share the server's execution gate so cross-library operations do not
overlap other jobs. Shutdown cancels and joins jobs from every opened library.
Discovery results can be passed to this opening API; discovery itself does not
open libraries.
