---
created_on: 2026-08-23 22:20
last_modified: 2026-08-23 22:20
status: current
---

# engine-cli

Go CLI utility for managing, verifying, backing up, synchronizing, and repairing Engine DJ collection databases (`m.db`), playlist hierarchies, and album artwork caches.

## Commands
- **Build Local Binary:** `just build` (compiles to `bin/engine-cli`)
- **Run Tests:** `just test` (`go test -v ./...`)
- **Run Linter / Static Analysis:** `just vet` (`go vet ./...`)
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
- **Temporary Files:** Temporary file operations use `.tmp/` within the project root.

## Conventions
- **Database Safety First:** Never perform destructive write operations on live Engine DJ databases without an existing backup snapshot.
- **1:1 Square Artwork:** Embedded and cached album art in Engine DJ must always be 1:1 square (1400x1400 for high-res / 256x256 thumbnail BLOBs) with SHA-1 hash tracking.
- **Hermetic Unit Tests:** All unit tests (`just test`) MUST remain 100% offline, hermetic, and deterministic using synthetic SQLite databases in `t.TempDir()`.
- **Zero Transcode:** Any audio tag or artwork updates must use `-c:a copy` to preserve bitstreams byte-for-byte.
- **Output Formatting:** All CLI output must be plain text without emojis across all modes and logs.

## Boundaries
- **Always:** Back up Engine DJ SQLite databases (`Database2/` and root `.db` files) before applying modifications.
- **Always:** Maintain >= 90% statement code coverage across all Go packages.
- **Never:** Commit compiled Go binaries (e.g. `bin/engine-cli`) or master databases to git.
