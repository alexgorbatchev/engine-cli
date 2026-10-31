# Engine CLI staging command reference

All commands inherit the root flags documented in [SKILL.md](../SKILL.md).

Use IDs returned by `staging list`; they identify staged items, not collection track IDs. Inspect a mix to select its child items and review ready/failed states. List/inspect are reads; retry can run acquisition/downloads, import installs selected audio into the collection, and rm discards the selected staged items under undo history. Import does not silently import failed items. `--to-playlist` is an existing playlist ID. Results are human summaries by default or structured data with `--json`; partial acquisition failures can retain completed staging items. Inspect those states before retrying.

## `engine-cli staging`

Review downloaded music before importing it

Usage: `engine-cli staging [flags]`.

- `--help, -h` (bool; default `false`): help for staging.

## `engine-cli staging import`

Import selected staged tracks into your collection

Usage: `engine-cli staging import <ids...> [flags]`.

- `--help, -h` (bool; default `false`): help for import.
- `--json` (bool; default `false`): Print structured results.
- `--to-playlist` (string; default ``): Assign imported tracks to an existing playlist ID.

## `engine-cli staging inspect`

Inspect a staged track or mix

Usage: `engine-cli staging inspect <id> [flags]`.

- `--help, -h` (bool; default `false`): help for inspect.
- `--json` (bool; default `false`): Print structured results.

## `engine-cli staging list`

List staged tracks and mixes

Usage: `engine-cli staging list [flags]`.

- `--help, -h` (bool; default `false`): help for list.
- `--json` (bool; default `false`): Print structured results.

## `engine-cli staging retry`

Retry a staged acquisition or its failed mix tracks

Usage: `engine-cli staging retry <id> [flags]`.

- `--help, -h` (bool; default `false`): help for retry.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli staging rm`

Remove staged tracks or mixes

Usage: `engine-cli staging rm <ids...> [flags]`.

- `--help, -h` (bool; default `false`): help for rm.
- `--json` (bool; default `false`): Print structured results.
