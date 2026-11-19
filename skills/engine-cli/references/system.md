# Engine CLI system command reference

All commands inherit the root flags documented in [SKILL.md](../SKILL.md).

## `engine-cli system`

Inspect desktop runtime status

Usage: `engine-cli system [flags]`.

- `--help, -h` (bool; default `false`): help for system.

## `engine-cli system check`

Check Engine DJ Desktop status without opening a library

Usage: `engine-cli system check [flags]`.

- `--checks` (stringSlice; default `[]`): Checks to run (comma-separated or repeatable); default: engine_desktop.
- `--help, -h` (bool; default `false`): help for check.
- `--json` (bool; default `false`): Print structured results for UI consumers.
