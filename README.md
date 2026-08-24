# engine-cli

`engine-cli` is a high-performance Go command-line tool designed for DJs to manage, audit, repair, synchronize, and back up Engine DJ collection databases (`m.db`) and album artwork caches.

## What It Does

- **Integrity Auditing (`verify`)**: Finds missing audio files on disk, file size (`fileBytes`) mismatches, orphaned playlist records, and tracks with missing album artwork.
- **1:1 Square Artwork Normalization (`artwork normalize`)**: Extracts embedded album art, center-crops non-square 16:9 thumbnails and gatefold scans to 1:1 square (1400x1400), and writes clean SHA-1 BLOBs directly to the `AlbumArt` database table and disk cache.
- **Artwork Cache Pruning (`artwork prune`)**: Removes unreferenced `AlbumArt` table rows and stale cached thumbnail files on disk.
- **Database & Tag Synchronization (`sync`)**: Scans on-disk audio tags and file sizes, updating `Track` records in `m.db` while detecting unindexed audio files.
- **Atomic Backups & Restore (`backup` / `restore`)**: Creates timestamped snapshot archives of `Database2/` databases, root databases, and `Artwork/` cache with one-command restoration.
- **Playlist Inspection (`playlist`)**: Visualizes full nested playlist and folder hierarchies with track counts and durations.

## Prerequisites

- [Go](https://golang.org/dl/) (version 1.26 or newer)
- [`ffmpeg`](https://ffmpeg.org/download.html) (version 4.4 or newer, including `ffprobe`)
- [`just`](https://github.com/casey/just) (task automation)

## Installation

```bash
cd engine-cli
just build
```

The compiled binary will be placed into `bin/engine-cli`.

## Quick Start

### 1. Verify Library Health

```bash
engine-cli verify
```

Sample Output:
```
=== Engine DJ Library Integrity Report ===
  Total Tracks in DB:   943
  Total Playlists:      41
  Missing Files on Disk:0
  File Size Mismatches: 0
  Missing Album Art:    0
  Orphaned Entities:    0
  Empty Playlists:      0

STATUS: Library is 100% HEALTHY.
```

### 2. Normalize Cover Art to 1:1 Square

```bash
engine-cli artwork normalize
```

### 3. Create a Backup Snapshot

```bash
engine-cli backup
```

### 4. Sync On-Disk Audio Tags into Database

```bash
engine-cli sync
```

### 5. Inspect Playlist Hierarchy

```bash
engine-cli playlist list
```

## Options & Flags

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--lib-dir` | `-l` | `~/Music/Engine Library` | Path to the Engine DJ Library directory |
| `--help` | `-h` | | Help for engine-cli |
| `--version` | `-v` | | Show version |

## License

This project is licensed under the [MIT License](LICENSE).
