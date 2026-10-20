A fast, reliable CLI tool for DJs to audit, repair, normalize artwork, synchronize, and back up Engine DJ collection databases (`m.db`) and artwork caches.

# What It Does

- Finds current Engine DJ libraries in Music and attached local drives on macOS and Windows, with friendly names for choosing a library.
- Audits Engine DJ SQLite collection databases for missing audio files, file size mismatches, and broken playlist links.
- Imports audio files into collection with automatic metadata tag probing, optional file relocation, and instant playlist assignment.
- Upgrades tracks through Fetch Track CLI while preserving curated track information, artwork, and playlist membership; extended mixes may replace radio edits.
- Manages nested playlist and folder hierarchies with full creation, deletion, moving, and track membership control.
- Extracts and center-crops embedded album artwork to 1:1 square (1400x1400) for crisp jog wheel and media player displays.
- Prunes unreferenced artwork database records and stale thumbnail cache files to reclaim storage space.
- Synchronizes on-disk audio metadata tags and file sizes with database track records.
- Creates timestamped backup snapshots of collection databases and artwork caches with one-command restore, including committed database changes still in SQLite's write-ahead log (WAL).
- Visualizes nested playlist and folder hierarchies with track counts and durations.
- Finds duplicate candidates, compares their quality and playlists, and merges selected database records while retaining audio files.
- Records undo history for every library-modifying command, including audio and artwork changes, with persistent identities for affected entities.
- Exposes library, playlist, and track operations through a local HTTP API with job status, cancellation, and streamed progress. See the [HTTP API guide](docs/http-api.md).
- **Export to Rekordbox:** Copy local audio and export playlists, cues, and beat grids with verified resume through the CLI or HTTP API.

# How It Works

- Scans your Engine DJ library directory on internal storage or external USB/SD drives.
- Use `engine-cli migrate export` to copy the selected library into `./rekordbox`, or choose paths with `--source` and `--target`. Export runs unattended in every mode and leaves the source databases and audio unchanged.
- Inspects SQLite database tables to detect corrupt file paths, size discrepancies, and missing artwork.
- Imports audio files and links them to playlists while maintaining linked-list playback order.
- Normalizes non-square album covers to 1:1 square dimensions without re-encoding your audio files.
- Updates database track records from modified on-disk tags and discovers unindexed music files.
- Automatically saves a safety backup snapshot before performing any database-modifying operation.
- Close Engine DJ before modifying the library or undoing an operation. Commands check desktop status on macOS and Windows and refuse changes while Engine DJ is running or its status is unknown. Other platforms report unsupported detection and refuse mutations. Read-only commands and dry runs remain available. Use `system check --json` for status and `library undo <id> --dry-run` to check undo conflicts.
- Keeps the original audio file when upgrading a track. Close Engine DJ before running the upgrade, then analyze the replacement in Engine DJ before playing it.
- When an upgraded track has saved cues, loops, or beat grids, requires `--keep-timing` or `--clear-timing`. An extended mix can shift those positions, so check retained timing before playing.
- Track upgrades require at least 16 kHz of measured audio bandwidth and either a higher measured bandwidth than the original or a longer mix with equal bandwidth. This is a screening measure, not a guarantee of audible improvement.
- Track upgrades keep the original audio file and reject formats outside Engine DJ's supported codec/container combinations, including Opus. Audio is never re-encoded to make an unsupported download fit.

# How it Really Works

- Existing playlists are selected by numeric ID. Tracks are selected by numeric ID or their exact stored path; titles and file basenames never establish a match. Use playlist listings and the HTTP `track.list` operation to obtain IDs.
- `track inspect <id...> --json` and `playlist inspect <id...> --json` return details for up to 500 IDs in one call, preserving request order and reporting missing IDs individually. The HTTP API offers the same results through `GET /v1/tracks?ids=12,34` and `GET /v1/playlists?ids=7,9`.
- `library sync --from <path> --to <path>` requires source `--playlist-id <id>` (repeatable, including descendants) or `--root`. Ancestors preserve hierarchy without copying their other tracks. Root sync includes tracks outside playlists, smartlist definitions, and the prepare queue. Both paths must contain existing libraries with matching, verified Engine database schema 3.0.2 and distinct database UUIDs.
- Sync matches track origins and explicit playlist mappings. Use `--map-playlist <source-id>:<destination-id>` to match existing playlists. Names never establish identity. Unmapped playlists are created unless a sibling title collision prevents it. Source changes propagate while destination-only edits remain. Conflicting edits are refused unless `--conflict source` or `--conflict destination` chooses a side.
- Sync preserves destination-only entries by default. `--mirror` propagates removals within the selected scope while retaining tracks referenced by kept playlists. Removed audio is preserved for undo. `--media none` requires matching destination audio and copies no audio; artwork may still be transferred. New audio goes under `Music/engine-cli/` inside the destination, without re-encoding. Audio changes outside the destination library are refused.
- `--dry-run --json` returns the HTTP preview format: entity changes, conflicts, copy sizes, free space, and an approximate space requirement including database snapshots. It creates no backups, history, media, or sync-state files. Apply recomputes the comparison; `--plan-token` additionally rejects an outdated preview. Mappings and baselines live in the destination's `.engine-cli/sync/` and participate in undo. Reverse the paths to sync back. No-op repeats create no history when their baseline is current.
- Sync publishes verified audio before committing database changes. Failed or interrupted operations can leave completed file changes; destination history preserves recovery data. Use `engine-cli -l <destination> library undo <operation-id>` to reverse them. Pending interrupted entries require `--force` recovery. Keep Engine DJ closed during application and recovery.
- `library discover [path...]` checks current `Database2/m.db` libraries without opening SQLite or writing library state. Automatic discovery uses Music (including redirected Windows Music folders) and mounted local drives; Windows scans fixed/removable drive letters. Optional paths replace automatic locations and check each path directly, `Engine Library/`, and `Music/Engine Library/`. It does not search recursively, scan network drives automatically, or list legacy root-level databases. Results include `name`, `source` (`local`, `drive`, or `folder`), `path`, and `database`; unreadable or invalid candidates appear in `warnings`. Use `--json` for structured output.
- Connects to SQLite collection databases (`Database2/m.db`, `Database2/p.db`) using modern zero-CGO SQLite drivers.
- Executes `ffprobe` JSON stream analysis to detect attached picture stream dimensions and extract audio tags.
- Invokes `ffmpeg` with `-c:a copy` to ensure zero lossy-to-lossy audio transcoding during metadata operations.
- Uses `fetch-track --json` to acquire replacement audio, then copies the original track information into the replacement without re-encoding its audio.
- Generates SHA-1 binary BLOB hashes and writes raw URL-safe Base64 thumbnail files into the `Artwork/` cache directory.
- Manages nested playlist trees and linked-list entities through recursive foreign key traversal on `Playlist` and `PlaylistEntity` tables.
- Captures each database independently through SQLite's online backup API and publishes the completed snapshot directory in the library’s `.engine-cli/backups/` folder. Failed snapshots are removed, and successive backups use unique names.
- Close Engine DJ for a backup with matching state across all database files and artwork, and before restoring. WAL-backed databases can be backed up while open, but the whole library is not captured in a single transaction.
- Automatic safety snapshots contain databases and live under `<lib-dir>/.engine-cli/backups/`. An explicit `library backup` includes databases and artwork and accepts a custom destination.
- Undo history lives under `<lib-dir>/.engine-cli/history/`. It retains operation records, database snapshots, and deduplicated copies of files that an operation overwrites or deletes. Database-only operations copy no audio or artwork. File moves retain paths and a fingerprint; only an overwritten destination needs a content backup. Undo preserves any contents it replaces so that undoing the undo can restore them.
- Disposable audio and artwork workspaces use the OS temporary directory and are removed after use. Atomic publication files are staged beside their destination.
- History is retained without automatic pruning. Its storage grows with database snapshots and affected file contents, without copying or scanning the whole audio collection. `--no-backup` disables the separate safety snapshot, but keeps undo history. For a moved file edited afterward, undo refuses by default; `--force` moves the edited contents back. A missing moved file cannot be recovered from a path record.
- Undo preserves unrelated later database edits and refuses conflicting changes to affected values or files. `--force` restores prior values over those conflicts; it still refuses ambiguous track identities and broken database references. Schema changes and tables without primary keys require a whole-database restore, so forcing those operations can replace later edits throughout that database.
- History identifies tracks by Engine's origin database UUID and origin track ID, and other records by their database and primary key. Their history hashes stay stable across renames, file relocations, and playlist moves. Membership changes also list the affected track and playlists. History follows the library directory when moved, including its database snapshots and affected files inside the library. Audio outside the library retains its external path.
- Undo records its own changes: undoing that operation ID reapplies them. Interrupted operations retain their original data and block further modifications until recovered with `library undo <id> --force`. Files and separate databases are restored individually; keep Engine DJ closed throughout recovery. Symbolic links and other non-regular files are refused before modification.

- Migration reads a current `Database2/m.db` library, writes byte-for-byte audio copies under the target's `Contents/`, and publishes target-root `rekordbox.xml` and `.sync_state.json`. Existing target content is verified by SHA-256 before reuse; interrupted tracks restart from the beginning. Stable source-path filename digests distinguish identical basenames. Unreferenced destination files are retained.
- Migration XML contains only audio copied or verified during that run and still present on the target. Missing and streaming tracks are counted as unavailable and excluded from XML; unavailable tracks alone do not fail the operation. Failure or cancellation retains completed files and publishes the available subset where possible. Rerun the command to resume and rebuild the full export.
- XML locations use absolute destination paths, so rerun migration after moving the target. Migration performs no source-library mutation, snapshot, or undo-history write. It works offline without `ffmpeg`, `ffprobe`, Fetch Track, or a standalone migration program. Progress goes to stderr; `--json` writes the same structured report returned by the HTTP API to stdout.

# Installation

Download the [latest release](https://github.com/alexgorbatchev/engine-cli/releases/latest) for your platform; this example installs version 1.1.0 on an Apple silicon Mac:

```bash
curl -fL -o engine-cli_1.1.0_darwin_arm64.tar.gz \
  https://github.com/alexgorbatchev/engine-cli/releases/download/v1.1.0/engine-cli_1.1.0_darwin_arm64.tar.gz
tar -xzf engine-cli_1.1.0_darwin_arm64.tar.gz engine-cli
mkdir -p "$HOME/.local/bin"
install -m 755 engine-cli "$HOME/.local/bin/engine-cli"
export PATH="$HOME/.local/bin:$PATH"
engine-cli --version
```

Keep `~/.local/bin` on your PATH in future terminal sessions. Intel Macs use the `darwin_amd64` archive from the release page.

# Setup

- [`ffmpeg` & `ffprobe`](https://ffmpeg.org/download.html) are required for audio tag probing and artwork processing. Make both commands available on your PATH.
- [Fetch Track CLI](https://github.com/alexgorbatchev/fetch-track-cli) is required for `track upgrade`. Install a version supporting JSON download results, `--progress-target`, and overall `percent` on every progress event, and make `fetch-track` available on your PATH.

# Quick Start

```sh
engine-cli library discover --json
```

```bash
# Verify library health and check for missing files
engine-cli library verify

# Import an audio file, move it to library storage, and add to a playlist
engine-cli track add --move --to-playlist 2 "/Downloads/song.mp3"

# Display playlist hierarchy tree
engine-cli playlist list

# Fetch replacement audio while keeping your track information and playlists
engine-cli track upgrade 42

# Customize the search, including an extended mix
engine-cli track upgrade 42 --query "Artist Track Title extended mix"

# Create a new playlist or folder
engine-cli playlist create "Melodic Techno" --parent 2

# Add an existing track to a playlist
engine-cli playlist track add 2 42

# Normalize cover artwork to 1:1 square
engine-cli track artwork normalize

# Synchronize on-disk audio tags into database
engine-cli library refresh

# Create a backup snapshot
engine-cli library backup "/Volumes/BackupDrive/EngineBackups"

# Serve the local HTTP API with streamed job progress
engine-cli api serve

# Copy audio and export a Rekordbox library
engine-cli migrate export --target ./rekordbox

# Select a different source and return structured results
engine-cli migrate export --source "$HOME/Music/Engine Library" --target ./rekordbox --json
```

Duplicate candidates match normalized title/artist or filename/duration; a match does not prove identical audio. Inspect the candidates before merging. For example, if tracks 42 and 87 are candidates:

```bash
engine-cli track duplicate list
engine-cli track duplicate inspect 42 87
engine-cli track duplicate merge 87 --keep 42
engine-cli library history
engine-cli library undo --dry-run
engine-cli library undo
```

Merge retains the keeper's cues, loops, and beat grid by default. To explicitly transfer all performance data from a selected track, add `--performance-from 87`; check timing in Engine DJ afterward. Both audio files remain on disk. Memberships are combined without adding duplicate playlist entries, and the latest play history is retained.

# Options & Flags

`engine-cli migrate export` (no positional arguments)

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--source <path>` | `-s` | Empty: selected `--lib-dir` | Source Engine Library with `Database2/m.db` |
| `--target <path>` | `-t` | Empty: `./rekordbox` | Destination for copied audio, XML, and resume state |
| `--json` | `-` | `false` | Structured migration report on stdout; paths remain absolute |

Human and agent text results contain `copied`, `existing`, `failed`, `unavailable`, and `xml` fields, with home paths folded to `~`. JSON/API results also contain `source`, `target`, `total`, and an optional `failures` array of `{source,error}` items. Counts describe the tracks processed during that run. Copy, state, or XML failures return an error alongside any available result. Progress reports read, copy, and XML phases on stderr; use global `--progress-json` for NDJSON events with stable job identity, increasing sequence, monotonic completion, and per-copy `metadata` containing `source`, `target`, `track`, `total_tracks`, `copied_bytes`, and `total_bytes`. The API streams the same events through its job/SSE endpoints. Only successful completion reaches 100%.

`engine-cli system check`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--checks <ids>` | `-` | all supported checks | Comma-separated or repeated check IDs; supports `engine_desktop` |
| `--json` | `-` | `false` | Structured timestamped runtime facts |

`engine-cli track inspect <id...>` and `engine-cli playlist inspect <id...>`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--json` | `-` | `false` | Ordered details with per-item errors |

`engine-cli library sync`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--from <path>` | `-` | required | Source library |
| `--to <path>` | `-` | required | Destination library |
| `--playlist-id <id>` | `-` | none | Source playlist ID; repeatable; includes descendants |
| `--root` | `-` | `false` | Whole library; mutually exclusive with playlist IDs |
| `--map-playlist <source-id>:<destination-id>` | `-` | none | Explicit existing playlist match; repeatable |
| `--dry-run` | `-` | `false` | Preview without modifying either library |
| `--json` | `-` | `false` | Structured preview or result on stdout |
| `--mirror` | `-` | `false` | Remove destination entries absent from selected source scope |
| `--conflict <policy>` | `-` | `error` | `error`, `source`, or `destination` |
| `--media <mode>` | `-` | `changed` | `changed` or `none` |
| `--plan-token <token>` | `-` | none | Reject an outdated preview before applying |

`engine-cli library discover [path...]`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--json` | `-` | `false` | Print libraries and warnings as JSON; displayed home paths use `~` |

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--lib-dir <path>` | `-l` | `~/Music/Engine Library` | Path to the Engine Library directory |
| `--no-backup` | `-` | `false` | Disable automatic backup snapshot before modifying database |
| `--progress-json` | `-` | `false` | Emit NDJSON progress on stderr with overall 0–100 completion; keep results on stdout |
| `--to-playlist <id>` | `-` | `""` | Assign imported track directly to a playlist in `track add` |
| `--move` | `-` | `false` | Move audio file into library directory in `track add` |
| `--query <text>` | `-` | `""` | Override the artist/title search in `track upgrade` |
| `--keep-timing` | `-` | `false` | Keep saved cues, loops, and beat grids during `track upgrade` |
| `--clear-timing` | `-` | `false` | Remove saved cues, loops, and beat grids during `track upgrade`; cannot combine with `--keep-timing` |
| `--delete-file` | `-` | `false` | Delete physical audio file from disk in `track rm` |
| `--parent <id>` | `-p` | `""` | Parent playlist or folder in `playlist create` |
| `--folder` | `-` | `false` | Create a folder container instead of a playlist in `playlist create` |
| `--force` | `-f` | `false` | Recursively delete folder and nested playlists in `playlist rm` |
| `--to <id|path>` | `-` | `""` | Destination parent in `playlist move` or destination prefix in `track relocate` |
| `--from <path>` | `-` | `""` | Old directory prefix to replace in `track relocate` |
| `--json` | `-` | `false` | Output verification report in JSON format |
| `--out-dir <path>` | `-o` | `<lib-dir>/.engine-cli/backups` | Destination directory for backup snapshots |
| `--list` | `-` | `false` | List available backup snapshots without creating a new backup |
| `--version` | `-v` | `false` | Print version information and exit |
| `--help` | `-h` | `false` | Print command line help |

`engine-cli api serve`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--port` | `-` | `8787` | Listen on this loopback port; 0 selects an available port |
| `--allow-origin` | `-` | none | Permit exact browser origins, such as `http://localhost:3000` |

`engine-cli track duplicate list`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--type` | `-` | `all` | Match by `metadata`, `filename-len`, or both |

`engine-cli track duplicate merge <id...>`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--keep <id>` | `-` | required | Track to retain; positional IDs are removed from the database |
| `--performance-from <id>` | `-` | `0` | Explicitly copy cues, loops, grid, and analysis from a selected track |

`engine-cli library undo [id]`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--dry-run` | `-` | `false` | Check conflicts without changing the library |
| `--force` | `-` | `false` | Restore prior values over conflicting later edits |

# License

MIT License (c) 2026 Alex Gorbatchev
