A fast, reliable CLI tool for DJs to audit, repair, normalize artwork, synchronize, and back up Engine DJ collection databases (`m.db`) and artwork caches.

# What It Does

- Audits Engine DJ SQLite collection databases for missing audio files, file size mismatches, and broken playlist links.
- Extracts and center-crops embedded album artwork to 1:1 square (1400x1400) for crisp jog wheel and media player displays.
- Prunes unreferenced artwork database records and stale thumbnail cache files to reclaim storage space.
- Synchronizes on-disk audio metadata tags and file sizes with database track records.
- Creates timestamped atomic backup snapshots of collection databases and artwork caches with one-command restore.
- Visualizes nested playlist and folder hierarchies with track counts and durations.

# How It Works

- Scans your Engine DJ library directory on internal storage or external USB/SD drives.
- Inspects SQLite database tables to detect corrupt file paths, size discrepancies, and missing artwork.
- Normalizes non-square album covers to 1:1 square dimensions without re-encoding your audio files.
- Updates database track records from modified on-disk tags and discovers unindexed music files.
- Automatically saves a safety backup snapshot before performing any database-modifying operation.

# How it Really Works

- Connects to SQLite collection databases (`Database2/m.db`, `Database2/p.db`) using modern zero-CGO SQLite drivers.
- Executes `ffprobe` JSON stream analysis to detect attached picture stream dimensions and extract audio tags.
- Invokes `ffmpeg` with `-c:a copy` to ensure zero lossy-to-lossy audio transcoding during metadata operations.
- Generates SHA-1 binary BLOB hashes and writes raw URL-safe Base64 thumbnail files into the `Artwork/` cache directory.
- Manages nested playlist trees through recursive foreign key traversal on `Playlist` and `PlaylistEntity` tables.
- Packages database directories into timestamped XDG backup snapshots with atomic directory replacement on restore.

# Prerequisites

- [`ffmpeg` & `ffprobe`](https://ffmpeg.org/download.html) - Version 4.4 or newer required for audio tag probing and artwork processing.
- [GitHub CLI (`gh`)](https://cli.github.com/) - Optional, for automated binary updates.

# Installation

Download the latest prebuilt binary from GitHub Releases:

```bash
# Using GitHub CLI
gh release download --repo alexgorbatchev/engine-cli --pattern 'engine-cli_*_darwin_arm64.tar.gz'
tar -xzf engine-cli_*_darwin_arm64.tar.gz
chmod +x engine-cli
mv engine-cli ~/.local/bin/
```

Or via direct download:

```bash
curl -sSL https://github.com/alexgorbatchev/engine-cli/releases/latest/download/engine-cli_darwin_arm64.tar.gz | tar -xz
chmod +x engine-cli
mv engine-cli ~/.local/bin/
```

# Quick Start

```bash
# Verify library health and check for missing files
engine-cli verify

# Normalize cover artwork to 1:1 square
engine-cli artwork normalize

# Synchronize on-disk audio tags into database
engine-cli sync

# Display playlist hierarchy tree
engine-cli playlist list
```

# Options & Flags

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--lib-dir <path>` | `-l` | `~/Music/Engine Library` | Path to the Engine Library directory |
| `--no-backup` | `-` | `false` | Disable automatic backup snapshot before modifying database |
| `--json` | `-` | `false` | Output verification report in JSON format |
| `--out-dir <path>` | `-o` | `~/.local/share/engine-cli/backups` | Destination directory for backup snapshots |
| `--list` | `-` | `false` | List available backup snapshots without creating a new backup |
| `--version` | `-v` | `false` | Print version information and exit |
| `--help` | `-h` | `false` | Print command line help |

# License

MIT License (c) 2026 Alex Gorbatchev
