# Engine CLI playlist command reference

All commands inherit the root flags documented in [SKILL.md](../SKILL.md).

## `engine-cli playlist`

Inspect and manage playlists and folders

Usage: `engine-cli playlist [flags]`.

Inspect and manage playlists and folders in your Engine DJ collection.

  - Use 'engine-cli playlist list' to view the full folder and playlist tree.
  - Use 'engine-cli playlist inspect <id>' to view the songs inside a playlist.
  - Use 'engine-cli playlist create <name>' to create a playlist or folder.
  - Use 'engine-cli playlist rm <id>' to remove a playlist.
  - Use 'engine-cli playlist move <id>' to move a playlist under a parent folder.
  - Use 'engine-cli playlist track' to manage song memberships inside playlists.

Command group for listing playlist hierarchy tree and inspecting track entities.

- `--help, -h` (bool; default `false`): help for playlist.

## `engine-cli playlist create`

Create a new playlist or folder

Usage: `engine-cli playlist create <name> [flags]`.

Create a new playlist or folder in your Engine DJ collection hierarchy.

Arguments:
  <name>   The title for the new playlist or folder.

Inserts a new record into Playlist table in Database2/m.db under optional parent folder.

- `--folder` (bool; default `false`): Create a folder container instead of a regular playlist.
- `--help, -h` (bool; default `false`): help for create.
- `--parent, -p` (string; default ``): Parent playlist or folder numeric ID.

## `engine-cli playlist export`

Review and export a playlist to a new server-local M3U file

Usage: `engine-cli playlist export <id|ref> [flags]`.

- `--dry-run` (bool; default `false`): Review saved membership order and output without creating files.
- `--encoding` (string; default ``): File encoding: utf-8 (default), windows-1252, or iso-8859-1; M3U8 requires utf-8.
- `--help, -h` (bool; default `false`): help for export.
- `--json` (bool; default `false`): Print compact structured results.
- `--path` (string; default ``): New server-local M3U/M3U8 destination; parent must exist and existing files are never replaced.
- `--path-mode` (string; default ``): Media paths: absolute (default), or relative to the destination directory.
- `--plan-token` (string; default ``): Export the exact reviewed plan; required without --dry-run.
- `--skip-unavailable` (bool; default `false`): Explicitly omit reviewed unavailable media; no copying or acquisition.

## `engine-cli playlist import`

Review and import an M3U playlist using existing collection tracks

Usage: `engine-cli playlist import <path> [flags]`.

- `--dry-run` (bool; default `false`): Review ordered entries without changing the collection.
- `--encoding` (string; default ``): File encoding: utf-8 (default), windows-1252, or iso-8859-1; M3U8 requires utf-8.
- `--help, -h` (bool; default `false`): help for import.
- `--json` (bool; default `false`): Print compact structured results.
- `--name` (string; default ``): Name for the new playlist; existing playlists are never replaced.
- `--parent` (string; default ``): Destination parent playlist ID or opaque reference; omitted uses the collection root.
- `--plan-token` (string; default ``): Apply the exact reviewed plan; required without --dry-run.
- `--skip-missing` (bool; default `false`): Explicitly omit reviewed missing, unregistered, or ambiguous entries.

## `engine-cli playlist inspect`

Read details for one or more playlist IDs

Usage: `engine-cli playlist inspect <id...> [flags]`.

Queries PlaylistEntity joined with Track in Database2/m.db for specified playlist numeric ID and outputs track list.

- `--help, -h` (bool; default `false`): help for inspect.
- `--json` (bool; default `false`): Print ordered details and per-item errors as JSON.

## `engine-cli playlist list`

Display all playlists and folders in your library

Usage: `engine-cli playlist list [flags]`.

Display the full folder and playlist tree of your Engine DJ collection with song counts, starting from Collection.

In standard mode, displays a clean ASCII tree.
In AGENT=1 mode, displays a compact token-conservative bulleted list.

Queries Playlist and PlaylistEntity tables in Database2/m.db to construct tree rooted at Collection. Emits bulleted list in AGENT=1 mode.

- `--help, -h` (bool; default `false`): help for list.

## `engine-cli playlist move`

Relocate a playlist or folder under a parent folder

Usage: `engine-cli playlist move <id> [flags]`.

Move a playlist or folder under a new parent folder in your Engine DJ collection hierarchy.

Arguments:
  <id>   The numeric ID of the playlist to move.

Atomically updates parentListId and sibling nextListId links, preventing circular loops. --before inserts before a destination sibling; omitted appends.

- `--before` (string; default ``): Insert before this destination sibling playlist ID; omitted appends at the end.
- `--help, -h` (bool; default `false`): help for move.
- `--to` (string; default ``): Destination parent playlist ID, or 0 for the library root.

## `engine-cli playlist rename`

Rename a playlist or folder

Usage: `engine-cli playlist rename <pl|id> <name> [flags]`.

- `--help, -h` (bool; default `false`): help for rename.
- `--json` (bool; default `false`): Print the renamed playlist as JSON.

## `engine-cli playlist reorder`

Review and change the saved track order within a playlist

Usage: `engine-cli playlist reorder <id|ref> [flags]`.

Move selected displayed rows to a gap in the saved playlist order. Rows and gaps are zero-based; the gap after the final row equals the displayed row count. Review with `--dry-run`, then repeat the same selection and view with `--plan-token`. Filtered views preserve hidden memberships; `--descending` describes a reversed saved-order view.

Apply is Desktop-protected, creates one safety snapshot and undo record, and rejects a stale review. The displayed view must follow saved order or its reverse; unrelated column sorts are invalid.

- `--descending` (bool; default `false`): The displayed view reverses the saved membership order.
- `--drop` (int; default `0`; required): Zero-based insertion gap before a row, or the gap after the last row.
- `--dry-run` (bool; default `false`): Review the resulting order without writing.
- `--help, -h` (bool; default `false`): help for reorder.
- `--json` (bool; default `false`): Print compact structured results.
- `--plan-token` (string; default ``): Apply the exact reviewed plan; required without `--dry-run`.
- `--rows` (intSlice; default `[]`; required): Selected zero-based displayed row indexes; repeat or separate with commas.
- `--view` (int64Slice; default `[]`): Displayed library-local track IDs in saved order; omit for the full playlist.

## `engine-cli playlist rm`

Delete a playlist or folder

Usage: `engine-cli playlist rm <id> [flags]`.

Delete a playlist or folder from your Engine DJ collection.

Songs inside the playlist will remain in your master collection.

Arguments:
  <id>   The numeric ID of the playlist to delete.

Deletes PlaylistEntity associations and Playlist record from Database2/m.db without deleting underlying Track records.

- `--force, -f` (bool; default `false`): Recursively delete folder, nested child playlists, and saved Smartlists.
- `--help, -h` (bool; default `false`): help for rm.

## `engine-cli playlist track`

Manage track memberships inside playlists

Usage: `engine-cli playlist track [flags]`.

Manage track memberships inside Engine DJ playlists.

  - Use 'engine-cli playlist track add <id> <id|path...>' to add tracks to a playlist.
  - Use 'engine-cli playlist track rm <id> <id|path...>' to remove tracks from a playlist.
  - Use 'engine-cli playlist track move <src-id> <dst-id> <id|path...>' to move tracks between playlists.

Command group for adding, removing, and transferring track membership entities in playlists.

- `--help, -h` (bool; default `false`): help for track.

## `engine-cli playlist track add`

Add track(s) to a playlist

Usage: `engine-cli playlist track add <id> <id|path...> [flags]`.

Add one or more tracks to a playlist.

Arguments:
  <id>           The numeric ID of the playlist.
  <id|path...>   One or more track IDs, or file paths already in the collection.

Creates PlaylistEntity linked-list records in Database2/m.db linking specified tracks to target playlist.

- `--help, -h` (bool; default `false`): help for add.

## `engine-cli playlist track move`

Move track(s) from one playlist to another

Usage: `engine-cli playlist track move <src-id> <dst-id> <id|path...> [flags]`.

Move track memberships from a source playlist to a destination playlist.

Arguments:
  <src-id>       The numeric ID of the source playlist.
  <dst-id>       The numeric ID of the destination playlist.
  <id|path...>   One or more track IDs, or file paths to transfer.

Transfers PlaylistEntity associations from source playlist to destination playlist in Database2/m.db.

- `--help, -h` (bool; default `false`): help for move.

## `engine-cli playlist track rm`

Remove track(s) from a playlist

Usage: `engine-cli playlist track rm <id> <id|path...> [flags]`.

Remove one or more tracks from a playlist.

This removes the track from the playlist only; the track remains in your master collection.

Arguments:
  <id>           The numeric ID of the playlist.
  <id|path...>   One or more track IDs, or file paths to remove from the playlist.

Deletes PlaylistEntity records for specified tracks in target playlist and relinks adjacent entities.

- `--help, -h` (bool; default `false`): help for rm.
