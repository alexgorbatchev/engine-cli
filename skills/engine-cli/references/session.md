# Engine CLI session command reference

All commands inherit the root flags documented in [SKILL.md](../SKILL.md).

## `engine-cli session`

Review played sessions and turn them into playlists

Usage: `engine-cli session [flags]`.

Review the sets Engine DJ recorded as play history.

  - Use 'engine-cli session list' to view played sessions, newest first.
  - Use 'engine-cli session inspect <id>' to view a session's tracks in play order.
  - Use 'engine-cli session import <id>' to review and create a playlist from a session.

History is read from the library's Database2/hm.db and is never changed.

- `--help, -h` (bool; default `false`): help for session.

## `engine-cli session import`

Review and create a playlist from a played session

Usage: `engine-cli session import <id> [flags]`.

Create a new playlist holding a played session's tracks in play order.

Review first with --dry-run, then apply the reviewed plan with --plan-token. Repeated plays keep their first position.
Plays whose track is no longer in the library block the import until --skip-missing is reviewed. Without --name the
playlist takes the session's title, numbered like Engine DJ's import when taken. Close Engine DJ first; one undo
removes the playlist.

- `--dry-run` (bool; default `false`): Review the playlist without changing the collection.
- `--help, -h` (bool; default `false`): help for import.
- `--json` (bool; default `false`): Print compact structured results.
- `--name` (string; default ``): Name for the new playlist; an existing playlist with this name is never replaced.
- `--parent` (string; default ``): Destination parent playlist ID or opaque reference; omitted uses the collection root.
- `--plan-token` (string; default ``): Apply the exact reviewed plan; required without --dry-run.
- `--skip-missing` (bool; default `false`): Explicitly omit reviewed plays whose track is no longer in the library.

## `engine-cli session inspect`

Show a played session's tracks in play order

Usage: `engine-cli session inspect <id> [flags]`.

- `--help, -h` (bool; default `false`): help for inspect.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli session list`

List played sessions, newest first

Usage: `engine-cli session list [flags]`.

- `--help, -h` (bool; default `false`): help for list.
- `--json` (bool; default `false`): Print compact structured results.
