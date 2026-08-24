---
created_on: 2026-08-23 22:20
last_modified: 2026-08-24 07:55
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
- **Verify Library:** `bin/engine-cli verify`
- **Backup Library:** `bin/engine-cli backup`
- **Sync On-Disk Metadata:** `bin/engine-cli sync`
- **Normalize Artwork to 1:1 Square:** `bin/engine-cli artwork normalize`
- **Prune Orphaned Artwork:** `bin/engine-cli artwork prune`
- **List Playlists:** `bin/engine-cli playlist list`

## Setup & Environment
- **Prerequisites:** Go 1.26+, `ffmpeg`, `ffprobe`, `just`.
- **Default Library Directory:** `$HOME/Music/Engine Library` (override with `-l <dir>` / `--lib-dir`).
- **Default Backup Directory:** Follows XDG Base Directory specification (`$XDG_DATA_HOME/engine-cli/backups` or `~/.local/share/engine-cli/backups`).
- **Temporary Files:** Temporary file operations use `.tmp/` within the project root.

## Conventions
- **Distribution for Non-Technical Users:** `engine-cli` is distributed for non-technical users (working and bedroom DJs, music collectors). All installation instructions, CLI outputs, error messages, and documentation must remain straightforward, accessible, and actionable without requiring developer knowledge. Pre-compiled binaries are the primary release and distribution vehicle.
- **Automated Database Snapshots:** All database modification operations (`artwork normalize`, `artwork prune`, `sync`, `restore`) must automatically create a snapshot backup in the XDG backup directory before applying changes, unless the user opts out with `--no-backup`.
- **Output Formatting & No Decorative Headers:** All CLI output must be plain text without emojis across all modes and logs. Static decorative title headers (e.g. `=== ... ===`) are prohibited; output meaning must be obvious directly from the structured content lines.
- **ASCII Tree Rendering:** All tree and hierarchy views (such as playlist listings) must render using standard ASCII tree characters (`├── `, `└── `, `│   `), with each node on a single line formatted as `<title> [id:<id> tracks:<trackCount>]`.
- **Agent Mode (`AGENT=1`):** When the `AGENT=1`, `AGENT=true`, or `AGENT=yes` environment variable is present, commands output compact, token-conservative key-value pairs or simple indented bulleted lists (e.g. for `playlist list`) without interactive noise, multi-byte Unicode tree symbols, or formatting banners.
- **1:1 Square Artwork:** Embedded and cached album art in Engine DJ must always be 1:1 square (1400x1400 for high-res / 256x256 thumbnail BLOBs) with SHA-1 hash tracking.
- **Zero Transcode Policy:** Any audio tag or artwork updates must use `-c:a copy` via `ffmpeg` to preserve bitstreams byte-for-byte without re-encoding.
- **Hermetic Unit Tests:** All unit tests (`just test`) MUST remain 100% offline, hermetic, and deterministic using synthetic SQLite databases in `t.TempDir()`.
- **No Stack Traces in Output:** User-facing output and error messages must never expose raw stack traces, Go panic dumps, or internal runtime frames.

## Gotchas
- **Zero-Reencoding Tagging:** When embedding artwork and tags via `ffmpeg`, always pass `-c:a copy` to prevent accidental audio transcoding.
- **Temporary Folder Cleanup:** `.tmp` directories created during execution must be cleaned up on completion if `.tmp` did not exist prior to execution.

## Boundaries
- **Always:** Automatically record all new instructions in the most appropriate `AGENTS.md` file immediately upon receipt (check with user if existing instructions conflict).
- **Always:** Back up Engine DJ SQLite databases (`Database2/` and root `.db` files) before applying modifications.
- **Always:** Any time code is changed such that results from running that code are changed, a test file must be changed as well; 90% code coverage is required (the `scripts/` folder is explicitly excluded from this rule).
- **Always:** Maintain >= 90% statement code coverage across all Go packages.
- **Always:** Run `just test` and `just vet` before committing changes.
- **Never:** Perform lossy-to-lossy audio re-encoding when updating artwork or metadata.
- **Never:** Commit compiled Go binaries (e.g. `bin/engine-cli`) or master databases to git.
