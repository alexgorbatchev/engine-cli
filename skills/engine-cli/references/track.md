# Engine CLI track command reference

All commands inherit the root flags documented in [SKILL.md](../SKILL.md).

## `engine-cli track`

Manage audio files and collection tracks

Usage: `engine-cli track [flags]`.

Manage audio files and track entries in your master Engine DJ collection.

  - Use 'engine-cli track add <path...>' to import audio files into your collection.
  - Use 'engine-cli track upgrade <id|path>' to fetch replacement audio while keeping your track information.
  - Use 'engine-cli track rm <id|path...>' to remove tracks from your collection.
  - Use 'engine-cli track move <id|path> <dest-path>' to move audio files on disk and update their database paths.
  - Use 'engine-cli track relocate' to batch-fix broken audio file paths across drives or folders.
  - Use 'engine-cli track artwork' to manage embedded cover art.

Command group for adding, removing, moving, and relocating master audio track records.

- `--help, -h` (bool; default `false`): help for track.

## `engine-cli track add`

Import audio file(s) into your collection

Usage: `engine-cli track add <path...> [flags]`.

Import one or more audio files into your Engine DJ collection.

Extracts metadata tags (title, artist, album, genre, year, BPM, duration, bitrate) using ffprobe.
Optionally moves the audio file to your Engine Library storage and/or assigns it to a playlist.

Automatically saves a safety backup of your library before making changes unless --no-backup is specified.

Probes audio tags via ffprobe, writes Track records to Database2/m.db, and optionally moves files to library or assigns to playlist.

- `--analyze` (bool; default `false`): Analyze BPM, musical key, and beat grid of the added tracks.
- `--bpm-range` (string; default `88-175`): Tempo range for --analyze: 58-115, 68-135, 78-155, 88-175, or 98-195.
- `--help, -h` (bool; default `false`): help for add.
- `--move` (bool; default `false`): Move audio file(s) into the Engine Library folder.
- `--to-playlist` (string; default ``): Assign imported track(s) to the specified playlist (creates if missing).

## `engine-cli track analyze`

Analyze BPM, musical key, and beat grid from the audio

Usage: `engine-cli track analyze <tr|id|path...> [flags]`.

Analyze tracks' audio and write their BPM, musical key, and beat grid.

Without --bpm, --key, or --grid every analysis runs. --missing-only keeps values tracks already have, and locked
beat grids are never replaced. Each track is written as soon as its analysis finishes, after rechecking that Engine DJ
is closed; one undo record covers every written track.

- `--bpm` (bool; default `false`): Analyze BPM.
- `--bpm-range` (string; default `88-175`): Tempo range: 58-115, 68-135, 78-155, 88-175, or 98-195.
- `--grid` (bool; default `false`): Analyze the beat grid.
- `--help, -h` (bool; default `false`): help for analyze.
- `--json` (bool; default `false`): Print compact structured results.
- `--key` (bool; default `false`): Analyze the musical key.
- `--missing-only` (bool; default `false`): Keep BPM, keys, and beat grids tracks already have.

## `engine-cli track artwork`

Manage and fix embedded track cover artwork

Usage: `engine-cli track artwork [flags]`.

Manage and fix embedded album cover artwork for tracks in your collection.

  - Use 'engine-cli track artwork normalize' to crop wide covers to 1:1 square for DJ jog wheels.

Command group for normalizing embedded picture streams on tracks.

- `--help, -h` (bool; default `false`): help for artwork.

## `engine-cli track artwork embed`

Embed artwork without re-encoding audio

Usage: `engine-cli track artwork embed [path|identifier...] [flags]`.

- `--artwork` (string; default ``): Artwork file path or URL.
- `--help, -h` (bool; default `false`): help for embed.
- `--json` (bool; default `false`): Print compact structured results.
- `--path` (string; default ``): Select a local audio file.
- `--paths` (stringSlice; default `[]`): Select local audio paths; repeat or separate with commas.
- `--staging-id` (string; default ``): Select a staged item.
- `--staging-ids` (stringSlice; default `[]`): Select staged items; repeat or separate with commas.

## `engine-cli track artwork normalize`

Crop album cover art to 1:1 square for DJ jog wheels and player displays

Usage: `engine-cli track artwork normalize [flags]`.

Scan your music collection, extract embedded album cover art, and crop wide or banner images into clean 1:1 square covers so they look crisp on DJ jog wheels and player screens without modifying your audio files.

Automatically saves a safety backup of your library before applying changes unless --no-backup is specified.

Extracts video stream v:0 via ffmpeg, crops to 1:1 square, computes SHA-1 hash, updates AlbumArt and Track records in Database2/m.db, and writes Artwork/*.jpg cache files without audio transcoding (-c:a copy).

- `--help, -h` (bool; default `false`): help for normalize.

## `engine-cli track batch`

Edit selected tracks together

Usage: `engine-cli track batch [flags]`.

- `--help, -h` (bool; default `false`): help for batch.

## `engine-cli track batch capitalize`

Review and apply per-track capitalization as one undoable edit

Usage: `engine-cli track batch capitalize <tr|id|path...> [flags]`.

- `--dry-run` (bool; default `false`): Review capitalization without changing the collection.
- `--fields` (stringSlice; default `[]`): Text fields: title, artist, album, genre, comment, label, composer, remixer.
- `--help, -h` (bool; default `false`): help for capitalize.
- `--json` (bool; default `false`): Print the review or affected track IDs as JSON.
- `--language` (string; default ``): Language tag such as tr or nl; empty uses neutral Unicode rules.
- `--plan-token` (string; default ``): Apply the exact reviewed plan; required without --dry-run.
- `--recipe` (string; default ``): Capitalization: upper, lower or title (required).

## `engine-cli track batch find-replace`

Review and apply per-track text replacements as one undoable edit

Usage: `engine-cli track batch find-replace <tr|id|path...> [flags]`.

- `--dry-run` (bool; default `false`): Review replacements without changing the collection.
- `--fields` (stringSlice; default `[]`): Text fields: title, artist, album, genre, comment, label, composer, remixer.
- `--find` (string; default ``): Nonempty text or Go regular expression to find.
- `--help, -h` (bool; default `false`): help for find-replace.
- `--json` (bool; default `false`): Print the review or affected track IDs as JSON.
- `--match-case` (bool; default `false`): Match case exactly (default: Unicode case-insensitive).
- `--mode` (string; default `literal`): Match mode: literal or regex.
- `--plan-token` (string; default ``): Apply the exact reviewed plan; required without --dry-run.
- `--replacement` (string; default ``): Replacement text; empty removes matches; regex supports $1 and ${name}.

## `engine-cli track batch update`

Edit selected tracks together with one undo record

Usage: `engine-cli track batch update <tr|id|path...> [flags]`.

- `--album` (string; default ``): Update the track's album.
- `--artist` (string; default ``): Update the track's artist.
- `--comment` (string; default ``): Update the track's comment.
- `--composer` (string; default ``): Update the track's composer.
- `--genre` (string; default ``): Update the track's genre.
- `--help, -h` (bool; default `false`): help for update.
- `--json` (bool; default `false`): Print the edited track IDs as JSON.
- `--key` (string; default ``): Musical key in Camelot (8A), Open Key (1m), or by name (Am, F#m, Bb); none clears it.
- `--label` (string; default ``): Update the track's label.
- `--rating` (int64; default `0`): Star rating from 0 (unrated) to 5.
- `--remixer` (string; default ``): Update the track's remixer.
- `--title` (string; default ``): Update the track's title.
- `--year` (int64; default `0`): Release year (0 clears the year).

## `engine-cli track candidates`

Find and acquire individual audio tracks

Usage: `engine-cli track candidates <query> [flags]`.

- `--auto-install` (bool; default `false`): Install missing upstream dependencies.
- `--confirm-fingerprint` (bool; default `false`): Require fingerprint confirmation (explicit true or false).
- `--help, -h` (bool; default `false`): help for candidates.
- `--js-runtime` (string; default ``): JavaScript runtime used by the downloader.
- `--json` (bool; default `false`): Print compact structured results.
- `--min-bandwidth-khz` (float64; default `0`): Minimum accepted spectral bandwidth in kHz.
- `--no-cache` (bool; default `false`): Bypass cached results.
- `--skip-metadata` (bool; default `false`): Skip online metadata lookup.
- `--skip-verify` (bool; default `false`): Skip audio quality verification.
- `--sources` (string; default ``): Comma-separated sources: youtube, soundcloud, bandcamp.

## `engine-cli track cue`

Inspect and edit native saved hot cues A–H

Usage: `engine-cli track cue [flags]`.

- `--help, -h` (bool; default `false`): help for cue.

## `engine-cli track cue clear`

Clear one saved hot cue without changing other performance data

Usage: `engine-cli track cue clear <tr|id|path> [flags]`.

- `--help, -h` (bool; default `false`): help for clear.
- `--json` (bool; default `false`): Output the verified write result as JSON.
- `--revision` (string; default ``): Exact saved-performance revision from cue inspect.
- `--slot` (string; default ``): Native hot-cue slot A through H.

## `engine-cli track cue inspect`

Read saved cues, loops, and the revision required to edit a cue

Usage: `engine-cli track cue inspect <tr|id|path> [flags]`.

- `--help, -h` (bool; default `false`): help for inspect.
- `--json` (bool; default `false`): Output the saved performance as JSON.

## `engine-cli track cue set`

Set a hot cue's exact source position, name and color

Usage: `engine-cli track cue set <tr|id|path> [flags]`.

- `--color` (string; default ``): Cue color as #RRGGBB.
- `--help, -h` (bool; default `false`): help for set.
- `--json` (bool; default `false`): Output the verified write result as JSON.
- `--name` (string; default ``): Cue name; an empty name is allowed.
- `--position` (float64; default `0`): Exact source position in seconds, independent of playback speed.
- `--revision` (string; default ``): Exact saved-performance revision from cue inspect.
- `--slot` (string; default ``): Native hot-cue slot A through H.

## `engine-cli track custom-tags`

Inspect and change library-local custom values for selected tracks

Usage: `engine-cli track custom-tags [flags]`.

- `--help, -h` (bool; default `false`): help for custom-tags.

## `engine-cli track custom-tags batch-inspect`

Inspect mixed custom-tag assignments on selected tracks

Usage: `engine-cli track custom-tags batch-inspect [flags]`.

- `--help, -h` (bool; default `false`): help for batch-inspect.
- `--json` (bool; default `false`): Print compact structured results.
- `--tracks` (stringSlice; default `[]`): Selected track IDs or selectors, in selection order (1–500).

## `engine-cli track custom-tags batch-update`

Review and atomically add or remove selected custom-tag values

Usage: `engine-cli track custom-tags batch-update [flags]`.

- `--add` (stringSlice; default `[]`): Value IDs to add; explicit empty preserves all other assignments.
- `--dry-run` (bool; default `false`): Review exact assignment impact without changing the library.
- `--help, -h` (bool; default `false`): help for batch-update.
- `--json` (bool; default `false`): Print compact structured results.
- `--plan-token` (string; default ``): Exact batch review token; required without --dry-run.
- `--remove` (stringSlice; default `[]`): Value IDs to remove; explicit empty preserves all other assignments.
- `--revision` (string; default ``): Current custom-tag revision.
- `--tracks` (stringSlice; default `[]`): Selected track IDs or selectors, in selection order (1–500).

## `engine-cli track custom-tags inspect`

Inspect custom values and current taxonomy revision

Usage: `engine-cli track custom-tags inspect <track> [flags]`.

- `--help, -h` (bool; default `false`): help for inspect.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli track custom-tags list`

List library taxonomy and current track assignments in one revision

Usage: `engine-cli track custom-tags list [flags]`.

- `--help, -h` (bool; default `false`): help for list.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli track custom-tags update`

Replace all custom values on one track; --values= clears them

Usage: `engine-cli track custom-tags update <track> [flags]`.

- `--help, -h` (bool; default `false`): help for update.
- `--json` (bool; default `false`): Print compact structured results.
- `--revision` (string; default ``): Current custom-tag revision from inspection or category list.
- `--values` (stringSlice; default `[]`): Complete value ID list; repeat or use commas, explicit empty clears all.

## `engine-cli track color`

Manage library-local track colors independently of custom tags and embedded audio tags.

Usage: `engine-cli track color [flags]`.

- `--help, -h` (bool; default `false`): help for color.

## `engine-cli track color list`

Usage: `engine-cli track color list [flags]`.

Read assigned live tracks, the current color revision and the count of unavailable native origins. Reading never creates or repairs color storage.

- `--help, -h` (bool; default `false`): help for list.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli track color inspect`

Usage: `engine-cli track color inspect <track> [flags]`.

Read one selected track's color and current revision. An empty color means unassigned.

- `--help, -h` (bool; default `false`): help for inspect.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli track color update`

Usage: `engine-cli track color update <track> --revision <revision> --color <color> [flags]`.

Use the revision from list or inspection. Colors use `#RRGGBB`, accepted case-insensitively and returned uppercase; explicit `--color=` clears the saved color. Whitespace is invalid. Writes are Desktop-protected, snapshotted and undoable. An unchanged color is rejected with `color_no_changes` before backup or history; reload after a stale revision instead of replaying the old command.

- `--color` (string; default ``): Required color; explicit empty clears it.
- `--revision` (string; default ``): Required current color revision.
- `--help, -h` (bool; default `false`): help for update.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli track color reconcile`

Usage: `engine-cli track color reconcile --revision <revision> --dry-run [flags]`.

Review colors retained for origins removed outside Engine CLI. Apply the same revision without `--dry-run`, supplying its exact `--plan-token`. Preview is read-only; apply is Desktop-protected, snapshotted and undoable. A stale plan requires a new review. A zero-removal apply is rejected with `color_no_changes`; browsing never silently prunes assignments.

- `--revision` (string; default ``): Required current color revision.
- `--dry-run` (bool; default `false`): Review without changing the library.
- `--plan-token` (string; default ``): Exact review token required for apply.
- `--help, -h` (bool; default `false`): help for reconcile.
- `--json` (bool; default `false`): Print compact structured results.

## `engine-cli track duplicate`

Find and compare possible duplicate tracks

Usage: `engine-cli track duplicate [flags]`.

- `--help, -h` (bool; default `false`): help for duplicate.

## `engine-cli track duplicate inspect`

Compare track quality, saved cues, and playlists

Usage: `engine-cli track duplicate inspect <id...> [flags]`.

- `--help, -h` (bool; default `false`): help for inspect.

## `engine-cli track duplicate list`

List possible duplicates without changing the library

Usage: `engine-cli track duplicate list [flags]`.

Find candidates by artist/title or filename/duration. Matching metadata does not prove identical audio. Overlapping matches appear together once.

- `--help, -h` (bool; default `false`): help for list.
- `--json` (bool; default `false`): Return structured results as JSON.
- `--type` (string; default `all`): Match by all, metadata, filename-len, or indexed audio.

## `engine-cli track duplicate merge`

Merge selected duplicates into a keeper, retaining audio files

Usage: `engine-cli track duplicate merge <id...> [flags]`.

Keep --keep and merge the listed duplicate IDs into it. Transfers playlists and playback history. Audio files stay in place. Keeper cues and grids are preserved unless --performance-from explicitly selects another track's complete performance data. Check timing before playing. Close Engine DJ first. Every merge is recorded for undo.

- `--help, -h` (bool; default `false`): help for merge.
- `--keep` (int64; default `0`): Track ID to retain.
- `--keep-ref` (string; default ``): Stable reference of the track to retain.
- `--performance-from` (int64; default `0`): Explicitly copy cues, loops, and grids from this selected track ID.
- `--performance-from-ref` (string; default ``): Stable reference to explicitly copy performance data from.

## `engine-cli track duplicate scan`

Fingerprint audio and group duplicate candidates

Usage: `engine-cli track duplicate scan [flags]`.

- `--help, -h` (bool; default `false`): help for scan.
- `--ids` (int64Slice; default `[]`): Track IDs to scan; omit to scan the whole library.
- `--json` (bool; default `false`): Return structured results as JSON.
- `--refs` (stringSlice; default `[]`): Stable track references to scan.

## `engine-cli track fetch`

Find and acquire individual audio tracks

Usage: `engine-cli track fetch <query> [flags]`.

- `--auto-install` (bool; default `false`): Install missing upstream dependencies.
- `--candidate-url` (string; default ``): URL from the selected candidate set.
- `--confirm-fingerprint` (bool; default `false`): Require fingerprint confirmation (explicit true or false).
- `--help, -h` (bool; default `false`): help for fetch.
- `--import` (bool; default `false`): Import successful downloads into the collection.
- `--js-runtime` (string; default ``): JavaScript runtime used by the downloader.
- `--json` (bool; default `false`): Print compact structured results.
- `--min-bandwidth-khz` (float64; default `0`): Minimum accepted spectral bandwidth in kHz.
- `--no-cache` (bool; default `false`): Bypass cached results.
- `--selection-token` (string; default ``): Persisted candidate selection token.
- `--skip-metadata` (bool; default `false`): Skip online metadata lookup.
- `--skip-verify` (bool; default `false`): Skip audio quality verification.
- `--sources` (string; default ``): Comma-separated sources: youtube, soundcloud, bandcamp.
- `--to-playlist` (string; default ``): Assign imported tracks to this playlist.

## `engine-cli track grid`

Inspect and edit beat grids

Usage: `engine-cli track grid [flags]`.

- `--help, -h` (bool; default `false`): help for grid.

## `engine-cli track grid update`

Apply one beat-grid edit and save it with undo

Usage: `engine-cli track grid update <tr|id|path> [flags]`.

Apply one edit to a track's saved beat grid and save it, updating the track's BPM to match.

Choose exactly one edit. Times are seconds on the track's timeline; markers count from 0 at the start of the grid.
--dry-run prints the edited grid without saving it. Saving is Desktop-protected and undoable.

- `--bar-shift` (int64; default `0`): Move every downbeat by 1 to 3 beats (negative moves earlier).
- `--double` (bool; default `false`): Double the tempo.
- `--downbeat` (float64; default `0`): Make the beat nearest this time start a bar.
- `--dry-run` (bool; default `false`): Print the edited grid without saving it.
- `--half` (bool; default `false`): Halve the tempo.
- `--help, -h` (bool; default `false`): help for update.
- `--json` (bool; default `false`): Print compact structured results.
- `--marker` (int; default `0`): Tempo segment for --tempo, or the marker for --marker-move.
- `--marker-add` (float64; default `0`): Add a tempo marker on the beat nearest this time.
- `--marker-move` (float64; default `0`): Move the marker chosen by --marker to this time.
- `--marker-remove` (int; default `0`): Remove this tempo marker.
- `--reset` (bool; default `false`): Restore the grid from the last analysis.
- `--shift` (float64; default `0`): Move every beat by this many seconds.
- `--tap` (float64Slice; default `[]`): Fit the tempo and beat phase to these tap times.
- `--tempo` (float64; default `0`): Set the BPM of the segment chosen by --marker.

## `engine-cli track inspect`

Read details for one or more track IDs

Usage: `engine-cli track inspect <id...> [flags]`.

- `--help, -h` (bool; default `false`): help for inspect.
- `--json` (bool; default `false`): Print ordered details and per-item errors as JSON.

## `engine-cli track move`

Move an audio file on disk and update its collection path

Usage: `engine-cli track move <id|path> <dest-path> [flags]`.

Move an audio file to a new location on disk and update its path in the Engine DJ collection database.

Arguments:
  <id|path>   The track ID or exact stored path to move.
  <dest-path>    The destination file or directory path.

Moves physical audio file on filesystem and updates path and filename columns in Database2/m.db.

- `--help, -h` (bool; default `false`): help for move.

## `engine-cli track relocate`

Batch-fix audio file paths across drives or directories

Usage: `engine-cli track relocate [flags]`.

Batch-replace directory path prefixes for tracks in the Engine DJ collection.

Useful when moving your library to a new USB drive, different volume name, or reorganized folder.

Automatically saves a safety backup of your library before making changes unless --no-backup is specified.

Updates Track.path prefixes across collection in Database2/m.db when drive mount points or folders change.

- `--from` (string; default ``): Old directory prefix to replace.
- `--help, -h` (bool; default `false`): help for relocate.
- `--to` (string; default ``): New directory prefix.

## `engine-cli track rm`

Remove track(s) from your collection

Usage: `engine-cli track rm <id|path...> [flags]`.

Remove one or more tracks from your Engine DJ collection and all playlists.

Optionally deletes the physical audio file from disk with --delete-file.

Automatically saves a safety backup of your library before making changes unless --no-backup is specified.

Deletes Track and cascading PlaylistEntity records from Database2/m.db, optionally deleting audio file on disk.

- `--delete-file` (bool; default `false`): Also delete the physical audio file from disk.
- `--help, -h` (bool; default `false`): help for rm.

## `engine-cli track suggest`

Suggest title and artist fixes for review

Usage: `engine-cli track suggest [id|ref...] [flags]`.

Review AI suggestions for title and artist cleanup. Choose track IDs/references or --playlists and --harness claude|codex. Selected title, artist, album, and filename are sent through agent-ask-cli using the harness's existing authentication. Your library and audio files remain unchanged.

Use track arguments or --playlists with up to 500 IDs/references. All direct playlist members are included and deduplicated. Choose --harness claude or codex; optional --model, --effort, --thinking and --harness-config-* options follow agent-ask-cli. Returns original metadata and reviewable suggestions; no backup or history is created.

- `--auto-install` (bool; default `false`): Install or update agent-ask-cli when needed.
- `--effort` (string; default ``): Native harness reasoning effort.
- `--harness` (string; default ``): Authenticated harness: claude or codex.
- `--harness-config-file` (string; default ``): Native harness configuration file.
- `--harness-config-mode` (string; default ``): Harness configuration: inherit (default) or clean.
- `--harness-home-dir` (string; default ``): Alternate harness configuration and authentication directory.
- `--help, -h` (bool; default `false`): help for suggest.
- `--json` (bool; default `false`): Stream one JSON record per completed batch, with processed/total counts.
- `--model` (string; default ``): Harness model (default: harness configuration).
- `--playlists` (stringSlice; default `[]`): Suggest fixes for direct members of these playlist IDs/references (comma-separated or repeated).
- `--thinking` (string; default ``): Native harness thinking: auto, on, or off.
- `--timeout` (string; default ``): Timeout per batch, such as 5m (default: agent-ask-cli's 5m).

## `engine-cli track tags`

Inspect and update tags embedded in audio files

Usage: `engine-cli track tags [flags]`.

- `--help, -h` (bool; default `false`): help for tags.

## `engine-cli track tags inspect`

Inspect actual audio file tags

Usage: `engine-cli track tags inspect [path|identifier...] [flags]`.

- `--help, -h` (bool; default `false`): help for inspect.
- `--json` (bool; default `false`): Print compact structured results.
- `--path` (string; default ``): Select a local audio file.
- `--paths` (stringSlice; default `[]`): Select local audio paths; repeat or separate with commas.
- `--staging-id` (string; default ``): Select a staged item.
- `--staging-ids` (stringSlice; default `[]`): Select staged items; repeat or separate with commas.

## `engine-cli track tags update`

Update embedded tags while preserving original audio and collection metadata

Usage: `engine-cli track tags update [path|identifier...] [flags]`.

- `--album` (string; default ``): Set album; an explicit empty value clears it.
- `--artist` (string; default ``): Set artist; an explicit empty value clears it.
- `--artwork` (string; default ``): Artwork file path or URL.
- `--auto-install` (bool; default `false`): Install missing upstream dependencies.
- `--comment` (string; default ``): Set comment; an explicit empty value clears it.
- `--date` (string; default ``): Set release date; an explicit empty value clears it.
- `--dry-run` (bool; default `false`): Preview the tag update.
- `--genre` (string; default ``): Set genre; an explicit empty value clears it.
- `--help, -h` (bool; default `false`): help for update.
- `--json` (bool; default `false`): Print compact structured results.
- `--no-cache` (bool; default `false`): Bypass cached metadata.
- `--path` (string; default ``): Select a local audio file.
- `--paths` (stringSlice; default `[]`): Select local audio paths; repeat or separate with commas.
- `--refresh` (bool; default `false`): Refresh metadata using online sources.
- `--source-url` (string; default ``): Metadata lookup source URL.
- `--staging-id` (string; default ``): Select a staged item.
- `--staging-ids` (stringSlice; default `[]`): Select staged items; repeat or separate with commas.
- `--title` (string; default ``): Set title; an explicit empty value clears it.

## `engine-cli track update`

Edit track information and star rating

Usage: `engine-cli track update <tr|id|path> [flags]`.

- `--album` (string; default ``): Update the track's album.
- `--artist` (string; default ``): Update the track's artist.
- `--artwork` (string; default ``): Set cover artwork from a PNG, JPEG, or GIF image.
- `--clear-artwork` (bool; default `false`): Remove the track's artwork association.
- `--comment` (string; default ``): Update the track's comment.
- `--composer` (string; default ``): Update the track's composer.
- `--genre` (string; default ``): Update the track's genre.
- `--help, -h` (bool; default `false`): help for update.
- `--json` (bool; default `false`): Print the edited track IDs as JSON.
- `--key` (string; default ``): Musical key in Camelot (8A), Open Key (1m), or by name (Am, F#m, Bb); none clears it.
- `--label` (string; default ``): Update the track's label.
- `--rating` (int64; default `0`): Star rating from 0 (unrated) to 5.
- `--remixer` (string; default ``): Update the track's remixer.
- `--title` (string; default ``): Update the track's title.
- `--year` (int64; default `0`): Release year (0 clears the year).

## `engine-cli track upgrade`

Fetch replacement audio while keeping track information and playlists

Usage: `engine-cli track upgrade <id|path> [flags]`.

Uses Fetch Track CLI JSON download output to replace audio, retaining the original file and Track ID. Extended mixes are permitted. Reanalyze the replacement in Engine DJ.

- `--auto-install` (bool; default `false`): Install missing upstream dependencies.
- `--candidate-url` (string; default ``): URL from the selected candidate set.
- `--clear-timing` (bool; default `false`): Remove saved cues, loops, and beat grids.
- `--confirm-fingerprint` (bool; default `false`): Require fingerprint confirmation (explicit true or false).
- `--help, -h` (bool; default `false`): help for upgrade.
- `--js-runtime` (string; default ``): JavaScript runtime used by the downloader.
- `--json` (bool; default `false`): Print compact structured results.
- `--keep-timing` (bool; default `false`): Keep saved cues, loops, and beat grids.
- `--min-bandwidth-khz` (float64; default `0`): Minimum accepted spectral bandwidth in kHz.
- `--no-cache` (bool; default `false`): Bypass cached results.
- `--query` (string; default ``): Search words instead of the saved artist and title.
- `--selection-token` (string; default ``): Persisted candidate selection token.
- `--skip-metadata` (bool; default `false`): Skip online metadata lookup.
- `--skip-verify` (bool; default `false`): Skip audio quality verification.
- `--sources` (string; default ``): Comma-separated sources: youtube, soundcloud, bandcamp.

## `engine-cli track verify`

Verify the spectral quality of remote, local, collection, or staged audio

Usage: `engine-cli track verify [url|path|identifier] [flags]`.

- `--auto-install` (bool; default `false`): Install missing verification dependencies.
- `--help, -h` (bool; default `false`): help for verify.
- `--json` (bool; default `false`): Print compact structured results.
- `--min-bandwidth-khz` (float64; default `0`): Minimum accepted bandwidth in kHz.
- `--staging-id` (string; default ``): Verify this staged item instead of a collection track.
- `--target` (string; default ``): Verify this URL or local path.
