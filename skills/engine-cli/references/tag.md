# Engine CLI tag command reference

All commands inherit the root flags documented in [SKILL.md](../SKILL.md).

Category/value IDs are opaque UUIDs returned by `tag category list --json`; `<name>` is a name, never an ID selector. Names are trimmed, normalized to NFC, limited to 100 Unicode code points and reject control characters. Names must be unique ignoring case across categories, or within a value's category. Mutations require the current global revision, including create. A stale revision requires reloading the taxonomy and reviewing again.

List output includes categories, their values, current live assignment counts, the global revision and unavailable-track count. `--json` emits the structured result instead of the human summary. Create/rename returns the ID, new revision and whether it changed. Delete previews expose affected tracks, removed assignments/values and an exact plan token; apply must retain the revision, target and token. Counts include stored assignments even when their origins are unavailable, while affected-track counts include currently present origins. Reconcile removes only explicitly reviewed unavailable assignments. These writes use protected companion storage with snapshot and undo; browsing never creates or prunes it. Categories and values list in their saved order, which `move` changes; new ones go last. A single-choice category (`mode single`) holds at most one value per track: adding one of its values replaces the track's other value in a batch update (reported as `replaced`), assigning two is refused with `tag_single_conflict`, and switching a category to single is refused with `tag_mode_conflict` while any track holds two of its values.

## `engine-cli tag`

Manage library-local custom categories and named values

Usage: `engine-cli tag [flags]`.

- `--help, -h` (bool; default `false`): help for tag.

## `engine-cli tag category`

Manage custom tag categories

Usage: `engine-cli tag category [flags]`.

- `--help, -h` (bool; default `false`): help for category.

## `engine-cli tag category create`

Create a named custom tag category

Usage: `engine-cli tag category create <name> [flags]`.

- `--help, -h` (bool; default `false`): help for create.
- `--json` (bool; default `false`): Print compact structured results.
- `--revision` (string; default ``): Current custom-tag revision from category list; rejects concurrent changes.

## `engine-cli tag category delete`

Review and delete a custom tag category and its assignments

Usage: `engine-cli tag category delete <id> [flags]`.

- `--dry-run` (bool; default `false`): Review affected tracks and assignments without changing the library.
- `--help, -h` (bool; default `false`): help for delete.
- `--json` (bool; default `false`): Print compact structured results.
- `--plan-token` (string; default ``): Exact deletion review token; required without --dry-run.
- `--revision` (string; default ``): Current custom-tag revision from category list; rejects concurrent changes.

## `engine-cli tag category list`

List categories, values, assignment counts and current revision

Usage: `engine-cli tag category list [flags]`.

- `--help, -h` (bool; default `false`): help for list.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli tag category mode`

Make a category multiple-choice or single-choice, one value per track

Usage: `engine-cli tag category mode <id> [flags]`.

- `--help, -h` (bool; default `false`): help for mode.
- `--json` (bool; default `false`): Print compact structured results.
- `--mode` (string; default ``): multiple or single.
- `--revision` (string; default ``): Current custom-tag revision.

## `engine-cli tag category move`

Move a custom tag category before another, or to the end

Usage: `engine-cli tag category move <id> [flags]`.

- `--before` (string; default ``): ID of the category to move before; omit to move to the end.
- `--help, -h` (bool; default `false`): help for move.
- `--json` (bool; default `false`): Print compact structured results.
- `--revision` (string; default ``): Current custom-tag revision from category list; rejects concurrent changes.

## `engine-cli tag category update`

Rename a custom tag category

Usage: `engine-cli tag category update <id> [flags]`.

- `--help, -h` (bool; default `false`): help for update.
- `--json` (bool; default `false`): Print compact structured results.
- `--name` (string; default ``): New name.
- `--revision` (string; default ``): Current custom-tag revision from category list; rejects concurrent changes.

## `engine-cli tag reconcile`

Review and remove assignments for tracks removed outside Engine CLI

Usage: `engine-cli tag reconcile [flags]`.

- `--dry-run` (bool; default `false`): Review unavailable tracks without changing assignments.
- `--help, -h` (bool; default `false`): help for reconcile.
- `--json` (bool; default `false`): Print compact structured results.
- `--plan-token` (string; default ``): Exact reconciliation review token; required without --dry-run.
- `--revision` (string; default ``): Current custom-tag revision.

## `engine-cli tag value`

Manage named values within custom tag categories

Usage: `engine-cli tag value [flags]`.

- `--help, -h` (bool; default `false`): help for value.

## `engine-cli tag value create`

Create a named custom tag value

Usage: `engine-cli tag value create <name> [flags]`.

- `--category` (string; default ``): Destination category ID.
- `--help, -h` (bool; default `false`): help for create.
- `--json` (bool; default `false`): Print compact structured results.
- `--revision` (string; default ``): Current custom-tag revision from category list; rejects concurrent changes.

## `engine-cli tag value delete`

Review and delete a custom tag value and its assignments

Usage: `engine-cli tag value delete <id> [flags]`.

- `--dry-run` (bool; default `false`): Review affected tracks and assignments without changing the library.
- `--help, -h` (bool; default `false`): help for delete.
- `--json` (bool; default `false`): Print compact structured results.
- `--plan-token` (string; default ``): Exact deletion review token; required without --dry-run.
- `--revision` (string; default ``): Current custom-tag revision from category list; rejects concurrent changes.

## `engine-cli tag value move`

Move a custom tag value before another, or to the end

Usage: `engine-cli tag value move <id> [flags]`.

- `--before` (string; default ``): ID of the value to move before; omit to move to the end.
- `--help, -h` (bool; default `false`): help for move.
- `--json` (bool; default `false`): Print compact structured results.
- `--revision` (string; default ``): Current custom-tag revision from category list; rejects concurrent changes.

## `engine-cli tag value update`

Rename a custom tag value

Usage: `engine-cli tag value update <id> [flags]`.

- `--help, -h` (bool; default `false`): help for update.
- `--json` (bool; default `false`): Print compact structured results.
- `--name` (string; default ``): New name.
- `--revision` (string; default ``): Current custom-tag revision from category list; rejects concurrent changes.
