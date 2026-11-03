# Shared tools and dependencies

All commands inherit the root flags documented in [SKILL.md](../SKILL.md).

1. Run `engine-cli tools inspect --json` to read all managed tools and one row per dependency, including the highest known minimum version and every requiring tool context. `verified:false` or `complete:false` means a check was incomplete; `satisfied:false` also covers known missing or outdated copies.
2. Run `engine-cli tools dependencies install --dependencies ffmpeg --json` or `engine-cli tools dependencies update --dependencies ffmpeg --json` to change every known requiring context in one operation. An absent copy is installed and an installed copy is updated regardless of the chosen verb. Selected names are deduplicated. Each tool's installer owns its copies, so multiple package-manager invocations may occur. An empty or unknown selection fails before mutation.
3. Read the returned `status` and per-context `receipts`, including successful receipts when the command exits unsuccessfully or is cancelled. Incomplete preflight coverage reports `dependency_check_incomplete` while permitting repair of known contexts. Run inspection again after any terminal outcome; a successful action is not a readiness report.

Human output is indented structured data; `--json` prints compact JSON. Both contain the same inspection or mutation result as the HTTP API. The scoped [tool commands](tool.md) remain available for an explicitly selected owner.

## `engine-cli tools`

Manage shared dependencies across all tools

Usage: `engine-cli tools [flags]`.

- `--help, -h` (bool; default `false`): help for tools.

## `engine-cli tools inspect`

Inspect all tools and their shared requirements

Usage: `engine-cli tools inspect [flags]`.

- `--help, -h` (bool; default `false`): help for inspect.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli tools dependencies`

Install or update every known copy of a dependency

Usage: `engine-cli tools dependencies [flags]`.

- `--help, -h` (bool; default `false`): help for dependencies.

## `engine-cli tools dependencies install`

Manage selected dependencies across all requiring tools

Usage: `engine-cli tools dependencies install [flags]`.

- `--dependencies` (stringSlice; default `[]`; required): Required dependency names; repeat or use commas.
- `--help, -h` (bool; default `false`): help for install.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli tools dependencies update`

Manage selected dependencies across all requiring tools

Usage: `engine-cli tools dependencies update [flags]`.

- `--dependencies` (stringSlice; default `[]`; required): Required dependency names; repeat or use commas.
- `--help, -h` (bool; default `false`): help for update.
- `--json` (bool; default `false`): Print compact structured results.
