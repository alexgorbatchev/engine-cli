# Engine CLI migrate command reference

All commands inherit the root flags documented in [SKILL.md](../SKILL.md).

## `engine-cli migrate`

Import and export DJ application libraries

Usage: `engine-cli migrate [flags]`.

- `--help, -h` (bool; default `false`): help for migrate.

## `engine-cli migrate export`

Copy Engine DJ audio and export Rekordbox XML

Usage: `engine-cli migrate export [flags]`.

Runs unattended; writes Contents/, rekordbox.xml, and .sync_state.json under the target. --source defaults to --lib-dir; --target defaults to ./rekordbox. --json prints the HTTP result format.

- `--help, -h` (bool; default `false`): help for export.
- `--json` (bool; default `false`): Print the migration result as JSON.
- `--source, -s` (string; default ``): Source Engine Library directory (empty: --lib-dir).
- `--target, -t` (string; default ``): Rekordbox output directory (empty: ./rekordbox).

## `engine-cli migrate import`

Review and import a server-local Rekordbox XML library

Usage: `engine-cli migrate import <path> [flags]`.

Review with --dry-run, then apply the same options with --plan-token. Audio remains at its current paths. The original XML and source mappings are preserved with the library; one undo reverses the import. Close Engine DJ before applying.

- `--allow-unsupported` (bool; default `false`): Accept reviewed source-only fields preserved in the original XML archive.
- `--conflicts` (string; default ``): Existing audio policy: reject (default) or explicitly reviewed reuse.
- `--dry-run` (bool; default `false`): Review without creating snapshots, history or import storage.
- `--help, -h` (bool; default `false`): help for import.
- `--json` (bool; default `false`): Print compact structured results.
- `--missing-media` (string; default ``): Missing audio policy: reject (default) or explicitly reviewed skip.
- `--name` (string; default ``): Required name for a new destination playlist root; existing roots are never replaced.
- `--plan-token` (string; default ``): Apply only the exact reviewed source, destination and policies.
