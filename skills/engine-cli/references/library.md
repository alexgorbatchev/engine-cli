# Engine CLI library command reference

All commands inherit the root flags documented in [SKILL.md](../SKILL.md).

## `engine-cli library`

Manage, audit, synchronize, and back up Engine DJ databases

Usage: `engine-cli library [flags]`.

Aliases: `lib`, `db`.

Manage, audit, synchronize, and back up Engine DJ databases and library files.

  - Use 'engine-cli library verify' to audit database integrity and missing files.
  - Use 'engine-cli library refresh' to synchronize on-disk metadata tags with database.
  - Use 'engine-cli library backup [path]' to create or list backup snapshots.
  - Use 'engine-cli library restore <path>' to restore from a backup snapshot.
  - Use 'engine-cli library prune' to delete orphaned artwork rows and cache files.

Command group for auditing integrity, synchronizing tags, creating snapshots, restoring backups, and pruning caches.

- `--help, -h` (bool; default `false`): help for library.

## `engine-cli library backup`

Create a safety backup snapshot of your Engine DJ library

Usage: `engine-cli library backup [path] [flags]`.

Create a timestamped backup snapshot of your Engine DJ SQLite databases (Database2/m.db, p.db) and Artwork/ thumbnail cache.

Includes committed changes even while databases are open. Each database is captured independently.
Close Engine DJ if you need matching state across all database files and artwork.

By default, backups are saved in <lib-dir>/.engine-cli/backups, or to an optional [path] specified as an argument or via -o.

Captures each database's committed SQLite content, including WAL pages, independently into Database2/*.db and Root/*.db, copies Artwork/*, then atomically publishes a unique timestamped snapshot directory in the library’s .engine-cli/backups directory or custom path.

- `--help, -h` (bool; default `false`): help for backup.
- `--list` (bool; default `false`): List available backup snapshots without creating a new backup.
- `--out-dir, -o` (string; default ``): Destination directory for backup snapshots (default: <lib-dir>/.engine-cli/backups).

## `engine-cli library create`

Create a new Engine library at this path

Usage: `engine-cli library create <path> [flags]`.

Requires Engine DJ Desktop to be closed. Never replaces an existing Database2 directory. Records undo history in the new library; no pre-existing database needs a snapshot.

- `--help, -h` (bool; default `false`): help for create.
- `--json` (bool; default `false`): Print compact structured results.
- `--state-db` (string; default ``): Library preferences database (default: XDG state directory).

## `engine-cli library delete`

Delete Engine databases and artwork, retaining recovery data and optionally media

Usage: `engine-cli library delete <path> [flags]`.

- `--confirm-path` (string; default ``): Confirm deletion by repeating the complete library path.
- `--delete-media` (bool; default `false`): Also delete referenced audio files.
- `--dry-run` (bool; default `false`): Preview files and known libraries without changing them.
- `--help, -h` (bool; default `false`): help for delete.
- `--json` (bool; default `false`): Print compact structured results.
- `--keep-shared` (bool; default `false`): Keep files used by other known or unavailable libraries.
- `--plan-token` (string; default ``): Apply the exact reviewed preview.
- `--state-db` (string; default ``): Library preferences database (default: XDG state directory).

## `engine-cli library discover`

Find Engine DJ libraries on this computer and attached drives

Usage: `engine-cli library discover [path...] [flags]`.

Find current Engine DJ libraries in Music and attached local drives on macOS and Windows. Optional paths replace automatic locations. Checks each path directly and its Engine Library and Music/Engine Library folders. Does not scan recursively or modify libraries.

Read-only, nonrecursive discovery. Optional paths replace automatic locations. Returns libraries and warnings; does not require a selected library.

- `--help, -h` (bool; default `false`): help for discover.
- `--json` (bool; default `false`): Print discovered libraries and warnings as JSON.
- `--state-db` (string; default ``): Persistent known-library and sidebar preferences database.

## `engine-cli library entity`

Read stable library entity references

Usage: `engine-cli library entity [flags]`.

- `--help, -h` (bool; default `false`): help for entity.

## `engine-cli library entity list`

List stable track and playlist references

Usage: `engine-cli library entity list [flags]`.

- `--help, -h` (bool; default `false`): help for list.
- `--json` (bool; default `false`): Return entity references as JSON.

## `engine-cli library history`

List reversible library operations and changed entity IDs

Usage: `engine-cli library history [flags]`.

- `--help, -h` (bool; default `false`): help for history.

## `engine-cli library media`

Gather collection audio inside its library folder

Usage: `engine-cli library media [flags]`.

- `--help, -h` (bool; default `false`): help for media.

## `engine-cli library media aggregate`

Copy or move external audio into the library and update its track paths

Usage: `engine-cli library media aggregate <path> [flags]`.

- `--confirm-path` (string; default ``): Confirm deletion by repeating the complete library path.
- `--dry-run` (bool; default `false`): Preview files and known libraries without changing them.
- `--help, -h` (bool; default `false`): help for aggregate.
- `--json` (bool; default `false`): Print compact structured results.
- `--mode` (string; default ``): Copy or move audio into the library: copy or move.
- `--plan-token` (string; default ``): Apply the exact reviewed preview.
- `--shared-mode` (string; default ``): When moving, copy shared audio or move it too: copy or move.
- `--state-db` (string; default ``): Library preferences database (default: XDG state directory).

## `engine-cli library move`

Move a complete library and preserve its identity and undo history

Usage: `engine-cli library move <path> [flags]`.

- `--confirm-path` (string; default ``): Confirm deletion by repeating the complete library path.
- `--destination` (string; default ``): New library folder; must not already exist.
- `--dry-run` (bool; default `false`): Preview files and known libraries without changing them.
- `--help, -h` (bool; default `false`): help for move.
- `--json` (bool; default `false`): Print compact structured results.
- `--plan-token` (string; default ``): Apply the exact reviewed preview.
- `--state-db` (string; default ``): Library preferences database (default: XDG state directory).

## `engine-cli library preferences`

Manage cosmetic sidebar names and hidden library entries

Usage: `engine-cli library preferences [flags]`.

- `--help, -h` (bool; default `false`): help for preferences.

## `engine-cli library preferences list`

List all known library paths and UI preferences

Usage: `engine-cli library preferences list [flags]`.

- `--help, -h` (bool; default `false`): help for list.
- `--json` (bool; default `false`): Print compact structured results.
- `--state-db` (string; default ``): Library preferences database (default: XDG state directory).

## `engine-cli library preferences update`

Change a sidebar name or visibility without changing Engine data

Usage: `engine-cli library preferences update <path> [flags]`.

- `--help, -h` (bool; default `false`): help for update.
- `--hidden` (bool; default `false`): Hide this entry from the UI; false restores it.
- `--json` (bool; default `false`): Print compact structured results.
- `--name` (string; default ``): Cosmetic sidebar name.
- `--state-db` (string; default ``): Library preferences database (default: XDG state directory).

## `engine-cli library prune`

Clean up unused cover art and reclaim disk space

Usage: `engine-cli library prune [flags]`.

Scan your collection to find and delete old, unneeded cover art records and cached thumbnail files that are no longer used by any songs in your library.

Automatically saves a safety backup of your library before applying changes unless --no-backup is specified.

Deletes rows in AlbumArt table not referenced by any Track.albumArtId and deletes unreferenced .jpg files in Artwork/ cache directory.

- `--help, -h` (bool; default `false`): help for prune.

## `engine-cli library refresh`

Update song information in Engine DJ from audio files on disk

Usage: `engine-cli library refresh [flags]`.

Scan your audio files and update song titles, artists, BPM, and file sizes in your Engine DJ collection if you modified them outside Engine DJ. Also finds and lists any new songs on disk that are not yet added to your collection.

Automatically saves a safety backup of your library before making changes unless --no-backup is specified.

Scans on-disk audio files referenced in Track table, updates title, artist, BPM, and fileBytes columns when modified on disk, and detects unindexed audio files.

- `--help, -h` (bool; default `false`): help for refresh.

## `engine-cli library register`

Register a library for stable references and syncing

Usage: `engine-cli library register [flags]`.

- `--help, -h` (bool; default `false`): help for register.
- `--json` (bool; default `false`): Return registration as JSON.
- `--new-copy` (bool; default `false`): Assign this copied library a separate instance identity.

## `engine-cli library restore`

Restore your Engine DJ library from a previous backup snapshot

Usage: `engine-cli library restore <path> [flags]`.

Restore your Engine DJ SQLite databases and Artwork/ cache from a previous backup snapshot directory.

Close Engine DJ before restoring.

Arguments:
  <path>   The full path to the backup snapshot folder. Use 'engine-cli library backup --list' to find snapshots.

Overwrites target Engine Library databases and Artwork/ cache with files from specified snapshot directory.

- `--help, -h` (bool; default `false`): help for restore.

## `engine-cli library smartlist-repair`

Usage: `engine-cli library smartlist-repair [flags]`.

Review, repair, or recover damaged smart playlists. Each child command accepts no positional arguments and inherits the root library selection and protection flags.

- `--help, -h` (bool; default `false`): display the command subtree.

## `engine-cli library smartlist-repair preview`

Usage: `engine-cli library smartlist-repair preview [flags]`.

Read the native saved Smartlist links and retained history without changing them. The review returns `plan_token`, `reconnected`, `removed`, `amended_deletes`, and `rows`. Each row contains `uuid`, `name`, `action` (`reconnect` or `remove`), and an optional `delete_entry_id`. Human output names each affected Smartlist; agent output uses compact fields. Rules remain opaque and unchanged.

- `--help, -h` (bool; default `false`): help for preview.
- `--json` (bool; default `false`): print the structured review to stdout; progress remains on stderr.
- `--state-db` (string; default ``): library-preference state file; empty selects the XDG state default described in the main skill.

## `engine-cli library smartlist-repair apply`

Usage: `engine-cli library smartlist-repair apply [flags]`.

Apply the exact reviewed collection and history state. A missing or changed token fails without applying the repair. Close Desktop first. Publication retains immutable original database/history evidence before changing targets. Reconnected links receive a separate Undo operation; removal attributable to an original deletion updates that deletion's Undo so one Undo restores its folder and saved Smartlists together. Unattributed damaged rows are removed without inventing an Undo operation. Healthy collections have no repair to publish. This operation owns its recovery evidence rather than creating an ordinary safety-backup snapshot; `--no-backup` does not disable that evidence.

Ordinary protected collection edits publish an applied automatic repair before their own backup and mutation. Human and agent modes name the repaired rows on stderr; `--progress-json` emits a `smartlist_repair` event there. The requested command retains its original stdout result. A later failure or cancellation does not undo the completed repair. Treat any `repair_undo_id` as separate from the requested operation’s Undo ID. Healthy collections emit no repair outcome. HTTP jobs retain this outcome in the optional `smartlist_repair` field, independently of their ordinary result and history ID. Native names and UUIDs are quoted in terminal output.

The result adds `applied` and optional `undo_id` to the review fields. A reconnect operation also reports `operation_id` on stderr. A successful removal need not have an Undo ID.

- `--help, -h` (bool; default `false`): help for apply.
- `--json` (bool; default `false`): print the structured result to stdout.
- `--plan-token` (string; default ``): required exact token from preview.
- `--state-db` (string; default ``): library-preference state file; empty selects the XDG state default.

## `engine-cli library smartlist-repair recover`

Usage: `engine-cli library smartlist-repair recover [flags]`.

Close Desktop, then explicitly roll back an interrupted publication. Recovery validates the native library identity, all retained original and staged images, referenced historical databases, and every current target before restoring any target. Changed or missing evidence refuses recovery; retain it for investigation. The result contains `rolled_back` and `restored_targets`. A completed repair or absent pending repair returns false and zero. Recovery retains the original evidence and completion marker.

- `--help, -h` (bool; default `false`): help for recover.
- `--json` (bool; default `false`): print the structured recovery result to stdout.
- `--state-db` (string; default ``): library-preference state file; empty selects the XDG state default.

1. Run `engine-cli -l "/music/Engine Library" library smartlist-repair preview --json` and review every named reconnect/removal.
2. Apply with `engine-cli -l "/music/Engine Library" library smartlist-repair apply --plan-token "<reviewed-token>" --json`. A stale review requires another preview.
3. For `smartlist_repair_pending`, run `engine-cli -l "/music/Engine Library" library smartlist-repair recover --json` before reviewing again. `smartlist_repair_ambiguous` requires investigating conflicting native history rather than choosing a guessed folder.

## `engine-cli library sync`

Sync playlists or a whole library to another library

Usage: `engine-cli library sync [flags]`.

Sync from an explicit source library to a destination library. Select source playlist IDs or --root. Use --dry-run to preview without writing anything. Destination-only content is retained unless --mirror is set. Close Engine DJ before applying changes.

Directional sync with read-only dry runs, conflict detection, reversible destination changes, and explicit mirroring.

- `--conflict` (string; default `error`): Resolve conflicting edits: error, source, or destination.
- `--dry-run` (bool; default `false`): Preview changes and conflicts without writing to either library.
- `--from` (string; default ``): Source Engine Library path.
- `--help, -h` (bool; default `false`): help for sync.
- `--json` (bool; default `false`): Print the preview or result as JSON.
- `--map-playlist` (stringSlice; default `[]`): Explicitly match source-id:destination-id for existing playlists (repeatable).
- `--media` (string; default `changed`): Audio copying: changed or none.
- `--mirror` (bool; default `false`): Remove destination entries absent from the selected source scope.
- `--plan-token` (string; default ``): Require the library state to match a previous preview token.
- `--playlist-id` (int64Slice; default `[]`): Source playlist IDs, including descendants (repeatable).
- `--playlist-ref` (stringSlice; default `[]`): Stable source playlist references, including descendants.
- `--root` (bool; default `false`): Sync the whole library, including tracks outside playlists.
- `--to` (string; default ``): Destination Engine Library path.

## `engine-cli library undo`

Undo a library operation, preserving unrelated later edits

Usage: `engine-cli library undo [id] [flags]`.

Undo the latest operation, or an operation ID from library history. Conflicts are refused unless --force is given. Use --dry-run to check first. Close Engine DJ before modifying the library. Forced undo also retains recovery history.

- `--dry-run` (bool; default `false`): Check undo conflicts without changing the library.
- `--force` (bool; default `false`): Restore prior values even when affected records or files have changed.
- `--help, -h` (bool; default `false`): help for undo.
- `--state-db` (string; default ``): Persistent library preferences database.

## `engine-cli library verify`

Check your library for missing songs, broken links, and empty playlists

Usage: `engine-cli library verify [flags]`.

Perform a read-only audit of your Engine DJ collection to find missing audio files, file size mismatches, broken playlist links, and missing album artwork.

Outputs a clear plain text summary by default or JSON when --json is passed.
If any collection or playlist check cannot finish, the command fails without a report.
Retry the check; if it still fails, inspect the library in Engine DJ before making changes.

Audits Track, Playlist, PlaylistEntity, and AlbumArt tables in Database2/m.db for missing audio files on disk, fileBytes mismatches, unresolved native membership origins, missing album art, and empty playlists. Membership origins resolve by both origin database UUID and track ID, independently of the local track row ID. Unreadable tables, membership rows, or invalid playlist chains fail with `library_verify_incomplete`; no partial report or automatic repair is returned. Cancellation retains its cancellation or timeout classification. This read-only operation does not create a safety snapshot or Undo entry.

- `--help, -h` (bool; default `false`): help for verify.
- `--json` (bool; default `false`): Output verification report in JSON format.
