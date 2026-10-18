---
created_on: 2026-08-23 22:20
last_modified: 2026-10-18 12:45
status: current
---

# engine-cli

Go CLI utility for managing, verifying, backing up, synchronizing, and repairing Engine DJ collection databases (`m.db`), playlist hierarchies, and album artwork caches.

## Commands
- **Build Local Binary:** `just build` (compiles to `bin/engine-cli`)
- **Run CLI with Arguments:** `just run [args...]`
- **Run CLI in Agent Mode:** `just run-ai [args...]` (`AGENT=1`)
- **Run Tests:** `just test` (`go test -v ./...`)
- **Run Static Analysis & Tests:** `just check` (`go vet ./... && go test -v ./...`)
- **Run Linter / Static Analysis:** `just vet` or `just lint` (`go vet ./...`)
- **Format Source Code:** `just fmt` (`go fmt ./...`)
- **Verify Library Integrity:** `bin/engine-cli library verify`
- **Discover Libraries:** `bin/engine-cli library discover` and HTTP `library.discover` inspect common macOS/Windows locations and attached drives without modifying libraries.
- **Backup Library:** `bin/engine-cli library backup [path] [--list]`
- **Restore Library:** `bin/engine-cli library restore <path>`
- **Sync On-Disk Metadata:** `bin/engine-cli library sync`
- **Prune Orphaned Artwork:** `bin/engine-cli library prune`
- **List Playlists:** `bin/engine-cli playlist list`
- **Inspect Playlist:** `bin/engine-cli playlist inspect <pl|id>`
- **Create Playlist:** `bin/engine-cli playlist create <name> [--parent <pl|id>] [--folder]`
- **Remove Playlist:** `bin/engine-cli playlist rm <pl|id> [--force]`
- **Move/Reparent Playlist:** `bin/engine-cli playlist move <pl|id> --to <pl|id>`
- **Add Track to Playlist:** `bin/engine-cli playlist track add <pl|id> <tr|id|path...>`
- **Remove Track from Playlist:** `bin/engine-cli playlist track rm <pl|id> <tr|id|path...>`
- **Move Track Between Playlists:** `bin/engine-cli playlist track move <src-pl|id> <dst-pl|id> <tr|id|path...>`
- **Add Track to Library:** `bin/engine-cli track add <path...> [--to-playlist <pl|id>] [--move]`
- **Upgrade Track Audio:** `bin/engine-cli track upgrade <tr|id|path> [--query <text>] [--keep-timing | --clear-timing]`
- **Remove Track from Library:** `bin/engine-cli track rm <tr|id|path...> [--delete-file]`
- **Move Track File on Disk:** `bin/engine-cli track move <tr|id|path> <dest-path>`
- **Relocate Track Paths:** `bin/engine-cli track relocate --from <path> --to <path>`
- **Normalize Artwork to 1:1 Square:** `bin/engine-cli track artwork normalize`
- **Find and Compare Duplicates:** `bin/engine-cli track duplicate list [--type all|metadata|filename-len]`; `bin/engine-cli track duplicate inspect <id...>`
- **Merge Duplicates:** `bin/engine-cli track duplicate merge <id...> --keep <id> [--performance-from <id>]`
- **Inspect Undo History:** `bin/engine-cli library history`
- **Undo an Operation:** `bin/engine-cli library undo [id] [--dry-run] [--force]`

## Setup & Environment
- **Prerequisites:** Go 1.26+, `ffmpeg`, `ffprobe`, `just`; `fetch-track` with JSON download output for track upgrades.
- **Default Library Directory:** `$HOME/Music/Engine Library` (override with `-l <dir>` / `--lib-dir`).
- **Portable Library Storage:** Store default backups in `<lib-dir>/.engine-cli/backups/` and undo history in `<lib-dir>/.engine-cli/history/`. Library-contained history paths must follow the library when it moves.
- **Temporary Files:** Production code uses the OS temporary directory for disposable workspaces, never project `.tmp/`. Agent development artifacts may use project `.tmp/`. Atomic publication staging stays beside its destination to support same-filesystem rename.

## Conventions
- **Library Discovery:** Recognize current `Database2/m.db` libraries; ignore legacy root-level database leftovers. Keep enumeration read-only and provide a friendly `name`, `source`, and absolute path for UI library pickers through the HTTP API.
- **Reversible Library Operations:** Every operation that modifies the source library must support undo, including database changes and changes to audio files or artwork. Apply this requirement to existing operations as well as duplicate-track operations, through both the CLI and HTTP API.
- **Affected Files Only:** Plan and preserve only files an operation will modify or delete. Database-only operations must not copy audio or artwork. Record file moves as reversible path changes rather than audio backups. Explicit library backups may include all databases and artwork.
- **Undo Conflicts:** Reverse an operation even after later library changes when there are no conflicts. Refuse conflicting undo by default; provide an explicit `--force` override.
- **Entity Identity:** Track changed tracks, playlists, and other entities using stable UUIDs or hashes so history and undo retain identity across moves, relocations, and playlist membership changes; names, paths, and membership alone are not identities.
- **HTTP API:** Expose all library, playlist, and track operations over loopback HTTP. Incorporate Fetch Track progress into overall Engine progress; every progress event includes a monotonic completion percentage from 0 to 100, with 100 reserved for successful completion.
- **Shared Progress Schema:** Keep CLI and HTTP progress field names and types aligned with Fetch Track, including `job_id`, `sequence`, and string-valued `error`. Preserve upstream event details under `source`; retain structured `{code,message}` errors for HTTP job/status and request responses.
- **Track Upgrades:** Use Fetch Track CLI to acquire replacement audio. Preserve the original track's curated metadata. Replacing a radio edit with an extended mix is intended upgrade behavior.
- **CLI Argument Placeholder Conventions:** All command argument placeholders and help documentation strictly follow the standard abbreviations: `pl` for playlist name, `tr` for track title, `id` for numeric database ID, `path` for filesystem path (file or folder), `pl|id` for playlist name or ID, `tr|id|path` for track title, ID, or path, and `src-pl|id` / `dst-pl|id` for source and destination playlists.
- **Distribution for Non-Technical Users:** `engine-cli` is distributed for non-technical users (working and bedroom DJs, music collectors). All installation instructions, CLI outputs, error messages, and documentation must remain straightforward, accessible, and actionable without requiring developer knowledge. Pre-compiled binaries are the primary release and distribution vehicle.
- **Automated Database Snapshots:** All database modification operations (`library sync`, `library restore`, `library prune`, `library undo`, `playlist create`, `playlist rm`, `playlist move`, `playlist track add`, `playlist track rm`, `playlist track move`, `track add`, `track upgrade`, `track rm`, `track move`, `track relocate`, `track artwork normalize`, `track duplicate merge`) must automatically create a snapshot backup in `<lib-dir>/.engine-cli/backups/` before applying changes, unless the user opts out with `--no-backup`. Read-only undo previews do not create safety snapshots.
- **Output Formatting & No Decorative Headers:** All CLI output must be plain text without emojis across all modes and logs. Static decorative title headers (e.g. `=== ... ===`) are prohibited; output meaning must be obvious directly from the structured content lines.
- **ASCII Tree Rendering:** All tree and hierarchy views (such as playlist listings) must render using standard ASCII tree characters (`├── `, `└── `, `│   `), with each node on a single line formatted as `<title> [id:<id> tracks:<trackCount>]`.
- **Agent Mode (`AGENT=1`):** When the `AGENT=1`, `AGENT=true`, or `AGENT=yes` environment variable is present, commands output compact, token-conservative key-value pairs or simple indented bulleted lists (e.g. for `playlist list`) without interactive noise, multi-byte Unicode tree symbols, or formatting banners.
- **1:1 Square Artwork:** Embedded and cached album art in Engine DJ must always be 1:1 square (1400x1400 for high-res / 256x256 thumbnail BLOBs) with SHA-1 hash tracking.
- **Zero Transcode Policy:** Any audio tag or artwork updates must use `-c:a copy` via `ffmpeg` to preserve bitstreams byte-for-byte without re-encoding.
- **Hermetic Unit Tests:** All unit tests (`just test`) MUST remain 100% offline, hermetic, and deterministic using synthetic SQLite databases in `t.TempDir()`.
- **No Stack Traces in Output:** User-facing output and error messages must never expose raw stack traces, Go panic dumps, or internal runtime frames.

## Gotchas
- **SQLite WAL Backups:** Use the SQLite online backup API to capture committed database content, including WAL pages. Publish a snapshot only after all copies succeed; never copy live `.db` files directly or checkpoint the source to make a backup.
- **Zero-Reencoding Tagging:** When embedding artwork and tags via `ffmpeg`, always pass `-c:a copy` to prevent accidental audio transcoding.
- **Temporary Folder Cleanup:** Remove production temporary workspaces on completion; never remove their shared OS temporary parent directory.

## Boundaries
- **Always:** Automatically record all new instructions in the most appropriate `AGENTS.md` file immediately upon receipt (check with user if existing instructions conflict).
- **Always:** Back up Engine DJ SQLite databases (`Database2/` and root `.db` files) before applying modifications.
- **Always:** Any time code is changed such that results from running that code are changed, a test file must be changed as well; 90% code coverage is required (the `scripts/` folder is explicitly excluded from this rule).
- **Always:** Maintain >= 90% statement code coverage across all Go packages.
- **Always:** Run `just test` and `just vet` before committing changes.
- **Never:** Perform lossy-to-lossy audio re-encoding when updating artwork or metadata.
- **Never:** Commit compiled Go binaries (e.g. `bin/engine-cli`) or master databases to git.
