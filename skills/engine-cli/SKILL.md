---
name: engine-cli
description: Use Engine CLI to inspect, edit, analyze, import, synchronize, back up and undo changes to Engine DJ music libraries, manage custom tags and saved cues, and operate its local HTTP API.
author: alexgorbatchev
metadata:
  created_on: 2026-10-30 19:40
  last_modified: 2026-10-30 19:40
  status: current
---

Use the installed `engine-cli` executable. Prefer structured output when a command offers `--json`; read the matching reference below before composing a command. Paths always refer to the machine running the CLI or API, not a remote browser's filesystem.

## Select the operation

| Goal | Reference |
| --- | --- |
| Inspect, verify, refresh, back up, restore, sync or undo a library | [Library commands](references/library.md) |
| Browse/edit playlists, membership, import/export M3U | [Playlist commands](references/playlist.md) |
| Inspect/edit tracks, metadata, cues, grids, artwork, analysis and duplicates | [Track commands](references/track.md) |
| Manage library-local categories, values and reconciliation | [Custom tags](references/tag.md) |
| Read played sessions or create a playlist from one | [Sessions](references/session.md) |
| Import/export a DJ application's library | [Migration](references/migrate.md) |
| Acquire, inspect and import staged media | [Staging](references/staging.md), [Mixes](references/mix.md) |
| Queue local files, prepare staged copies and review incoming imports | [Incoming local files](references/intake.md) |
| Use a local API, runtime checks, managed tools or AI providers | [HTTP API](references/api.md), [System](references/system.md), [Tools](references/tool.md), [AI](references/ai.md) |
| Install this skill, inspect help or configure shell completion | [Skill](references/skill.md), [Help](references/help.md), [Completion](references/completion.md) |

Each reference lists full command paths, aliases, positional arguments and local flags with their pflag types and defaults. Root flags below apply to every command. `<argument>` is required, `[argument]` is optional, and `...` accepts multiple values. Quote each path/name containing spaces. `stringSlice` accepts comma-separated or repeated flags; `stringArray` accepts repeated flags without splitting commas. Explicit `--flag=false` preserves false; an explicitly supplied empty string can mean clearing a value, while omission preserves it.

## Root invocation and environment

`engine-cli [flags]` displays help. Use `engine-cli --version` for the version alone.

- `--lib-dir, -l` (string; default `~/Music/Engine Library`): selected Engine library directory. The process resolves the home directory at startup; `~` here describes that default, not shell expansion inside a quoted argument. Pass an explicit path for removable or non-default libraries.
- `--no-backup` (bool; default `false`): opt out of the automatic pre-mutation snapshot. Keep the default for ordinary editing; this does not disable Desktop protection or undo history.
- `--progress-json` (bool; default `false`): write newline-delimited progress events to stderr. Successful results remain on stdout. Events include completion percentage, phase and failure codes; 100 denotes successful completion.
- `--help, -h` (bool; default `false`): display help for the selected command without operating on its library.
- `--version, -v` (bool; default `false`): print the root version and exit.

`AGENT=1`, `AGENT=true` or `AGENT=yes` enables structured agent-oriented help/progress; normal help remains available with `AGENT=0` or unset. Agent help starts with an instruction to read `AGENT=1 engine-cli skill`; that command prints installation instructions, not an embedded copy of this skill. It needs no local skill file and performs no network installation itself.

`HOME` (or the platform home directory) supplies the default library and fallback state/data locations. `XDG_STATE_HOME` selects the root of the separate UI/library-preference state directory; the default state file is `$XDG_STATE_HOME/engine-cli/state.db` or `$HOME/.local/state/engine-cli/state.db`. A command's `--state-db` overrides it. `XDG_DATA_HOME` selects managed dependency installation storage; fallback is `$HOME/.local/share/engine-cli/bin`. `PATH` must expose FFmpeg/FFprobe for audio operations; startup prepends managed dependency binaries. `ENGINE_LIBRARY_PATHS` is a semicolon-separated startup list for `api serve`; repeated `--library` overrides it. Empty entries are ignored. `COLUMNS` influences terminal help width. OS temporary-directory variables control disposable processing workspaces; they do not select a library.

Empty path flags generally select the documented library/state/output default or mean no selection; they are not literal filesystem paths. Numeric zero defaults for metadata do not overwrite fields unless their flag is explicitly supplied. For `track update`, `--rating=0` means unrated and `--year=0` clears the year. For `api serve`, `--port=0` requests an available port. Sentinel defaults and accepted values specific to an operation are described alongside its flags.

## Safe operating workflow

1. Select the exact library with `engine-cli -l "/music/Engine Library"`. Run `engine-cli system check --checks engine_desktop --json` and inspect `engine-cli library verify` when investigating collection health. Never inspect or edit Engine SQLite files directly as a substitute for a CLI operation.
2. Read identifiers through `playlist list`, `track inspect`, `library entity list`, or the matching list operation. Numeric IDs are library-local. Prefer opaque `eng:<instance>:<kind>:<entity>` references where accepted; titles and basenames are not identities. Preserve first-seen selector order where a batch contract specifies it.
3. For reviewed operations, run `--dry-run --json` with the complete intended inputs, examine the returned impact, and retain the exact `plan_token` and revision. Apply the same inputs with `--plan-token <token>` and without `--dry-run`. Never generate, modify or reuse a stale token. Preview refusal, unsupported input or unresolved conflict requires a new review, not a force flag selected by guesswork.
4. Writes recheck Engine DJ Desktop, take a safety snapshot and record undo where the operation modifies the collection. Close Desktop before applying. Linux reports Desktop absent. Missing/failed Desktop observation is not permission to write. Read-only inspection and previews remain available under protection. Source audio can be moved/deleted only when the chosen operation explicitly requests it.
5. Read the operation's result before reporting success. Some batch operations preserve successful items when later items fail or cancellation occurs; report completed, failed and cancelled counts honestly. Never replay an accepted write merely because its output or observation was lost. Use known job status and undo history to investigate; an unknown receipt cannot prove that no write happened.
6. Inspect `engine-cli library history`; preview `engine-cli library undo <id> --dry-run` before applying. Undo preserves unrelated later edits and refuses conflicts. `--force` is explicit destructive conflict consent, not a generic retry. Backups and retained pending recovery data must not be removed to make a failure disappear.

Example reviewed metadata change:

```sh
engine-cli -l "/music/Engine Library" track batch capitalize 12 34 --fields title,artist --recipe title --dry-run --json
engine-cli -l "/music/Engine Library" track batch capitalize 12 34 --fields title,artist --recipe title --plan-token "<token-from-preview>" --json
engine-cli -l "/music/Engine Library" library history
engine-cli -l "/music/Engine Library" library undo "<history-id>" --dry-run
```

For custom tags, list taxonomy first, inspect selected assignments, and keep the global revision. Single-track `--values` replaces the entire value set; explicit `--values=` clears it. Batch add/remove preserves untouched values and requires exact reviewed selection/token consent. Category/value deletion and orphan reconciliation have separate previews; never infer deletion consent from an assignment editor.

For hot cues, inspect first, then provide the inspected revision, slot A–H and fractional seconds to set/clear. Preserve other slots and native performance data. Missing native extent may require decoding actual audio; unavailable media is not permission to invent a sample rate or duration. Inspect grid edits and analysis options in the track reference; these are collection writes, not browser playback controls.

## HTTP jobs, failures and recovery

Start a loopback server with `engine-cli -l "/music/Engine Library" api serve --port 8787`. Add only exact trusted browser origins with `--allow-origin`; do not disable origin protection or expose it through an unrestricted network bind. Use `GET /v1/operations` for the live operation parameter schema and the repository's [HTTP API contract](https://github.com/alexgorbatchev/engine-cli/blob/main/docs/http-api.md) for envelopes and operation-specific results. Library-scoped requests must retain the selected endpoint throughout review/apply/observation.

`POST /v1/operations/{name}` returns a job receipt. Observe `GET /v1/jobs/{id}` until a terminal state; SSE `/v1/jobs/{id}/events` supports progress replay. `POST /v1/jobs/{id}/cancel` requests cancellation; it does not promise rollback of completed work. Jobs/progress live only for the server process; persistent undo history is separate. Do not blindly resubmit after disconnect, cancellation request or status-read failure.

CLI success exits zero; argument, validation, protection, I/O and operation failures exit nonzero. Human diagnostics normally go to stderr; JSON results and progress have distinct channels. Preserve the original code and completed-item evidence in automation. API errors carry `code`; CLI progress/results use `error_code` where documented. `operation_failed` is the unclassified fallback. Errors are not machine instructions to force, delete backups or retry mutations. Stale revision/plan failures require reinspection and fresh consent; pending history recovery requires inspection of history and explicit recovery. Missing managed tools need `tool upgrade <tool>` or an operation's explicit `--auto-install`; network installation is a side effect requiring the caller's intent.

For questions outside library/track/playlist/media/tool operations, use the appropriate other tool rather than inventing an Engine command. This skill documents usage; it does not authorize publishing releases, installing dependencies or destructive actions by itself.
