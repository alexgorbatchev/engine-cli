# Engine CLI ai command reference

All commands inherit the root flags documented in [SKILL.md](../SKILL.md).

Run `engine-cli ai list --json` to obtain current provider identifiers, then pass an exact returned identifier to `ai inspect`. These commands inspect provider availability and configuration; they do not configure credentials or perform collection edits.

## `engine-cli ai`

Inspect tracklist AI providers

Usage: `engine-cli ai [flags]`.

- `--help, -h` (bool; default `false`): help for ai.

## `engine-cli ai inspect`

Inspect an AI provider

Usage: `engine-cli ai inspect <provider> [flags]`.

- `--help, -h` (bool; default `false`): help for inspect.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli ai list`

List available AI providers

Usage: `engine-cli ai list [flags]`.

- `--help, -h` (bool; default `false`): help for list.
- `--json` (bool; default `false`): Print compact structured results.
