# Engine CLI tool command reference

All commands inherit the root flags documented in [SKILL.md](../SKILL.md).

`<tool>` selects `fetch-track`, `fetch-mix`, `tag-track` or `agent-ask-cli`; inspect it first to discover its current version and dependency names. Inspect is read-only. `tool upgrade` installs a missing managed tool or updates an installed one. Dependency install/update may also download executables. The empty `--dependencies` list selects all dependencies rather than none. `agent-ask-cli` does not expose dependency management here; configure Claude Code or Codex directly. Human output summarizes the returned data; `--json` prints the structured result. Missing, outdated or failed installation results require resolving the reported tool/dependency problem, not retrying an unrelated collection write.

## `engine-cli tool`

Manage tools and their dependencies

Usage: `engine-cli tool [flags]`.

- `--help, -h` (bool; default `false`): help for tool.

## `engine-cli tool dependencies`

Inspect, install, or update upstream dependencies

Usage: `engine-cli tool dependencies [flags]`.

- `--help, -h` (bool; default `false`): help for dependencies.

## `engine-cli tool dependencies inspect`

Manage native upstream dependencies

Usage: `engine-cli tool dependencies inspect <tool> [flags]`.

- `--help, -h` (bool; default `false`): help for inspect.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli tool dependencies install`

Manage native upstream dependencies

Usage: `engine-cli tool dependencies install <tool> [flags]`.

- `--dependencies` (stringSlice; default `[]`): Dependency names; repeat or use commas (default: all).
- `--help, -h` (bool; default `false`): help for install.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli tool dependencies update`

Manage native upstream dependencies

Usage: `engine-cli tool dependencies update <tool> [flags]`.

- `--dependencies` (stringSlice; default `[]`): Dependency names; repeat or use commas (default: all).
- `--help, -h` (bool; default `false`): help for update.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli tool inspect`

Inspect or upgrade a managed tool

Usage: `engine-cli tool inspect <tool> [flags]`.

- `--help, -h` (bool; default `false`): help for inspect.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli tool upgrade`

Inspect or upgrade a managed tool

Usage: `engine-cli tool upgrade <tool> [flags]`.

- `--help, -h` (bool; default `false`): help for upgrade.
- `--json` (bool; default `false`): Print compact structured results.
