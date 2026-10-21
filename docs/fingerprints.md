# Audio duplicates, stable references, and sync

Use these commands and API operations to identify repeated recordings, select entities without relying on names, and reuse existing destination audio during sync.

## Register each library instance

```sh
engine-cli -l "$HOME/Music/Engine Library" library register --json
engine-cli -l "$HOME/Music/Engine Library" library entity list --json
```

Registration stores an instance ID and playlist identities in `.engine-cli/identity.json`. Moving the whole library, including this folder, preserves its identity. Sync identifies an unregistered library by its Engine database UUID and a registered one by its instance ID; registering a library that has already synced keeps the sync baselines recorded under its UUID, so its next sync still recognizes earlier edits and mirror removals stay protected. After copying a registered library to USB, register the copy explicitly:

```sh
engine-cli -l "/Volumes/PRIME-128GB/Engine Library" library register --new-copy --json
```

`--new-copy` assigns a distinct instance ID and preserves existing playlist lineage. Repeating ordinary registration is idempotent. Discovery reports `id` and `registration` (`registered`, `unregistered`, `invalid_identity`, or `identity_conflict`) for library pickers. Two discovered copies with the same instance ID both report a conflict.

Treat `ref` as opaque. A full reference identifies an entity in one library instance; `entity_id` identifies its lineage. Tracks use Engine's origin database UUID and origin track ID. Synced playlists share persisted lineage. The same entity on USB therefore has a different full reference from its desktop copy. Independently imported copies have distinct track identities even when their audio matches. Fingerprints provide matching evidence; they do not replace entry identity.

## Scan and inspect duplicates

```sh
engine-cli track duplicate scan --json
engine-cli track duplicate scan --refs "$TRACK_REF_A,$TRACK_REF_B" --json
engine-cli track duplicate list --type audio --json
engine-cli track inspect "$TRACK_REF_A" "$TRACK_REF_B" --json
engine-cli track duplicate merge "$TRACK_REF_B" --keep-ref "$TRACK_REF_A"
```

Scanning decodes full audio with FFmpeg and fingerprints it with Go Chromaprint. It never rewrites audio. The cache lives in `.engine-cli/fingerprints/index.json`, keyed by current file SHA-256. Changed file contents invalidate reuse even when names, paths, timestamps, or Engine IDs are unchanged. Selected scans accept either `--refs` or `--ids` (up to 500); omitting both scans the library.

The report contains `profile`, per-track `items` with cache status and errors, duplicate `groups`, and pairwise `matches`. Missing or undecodable audio produces a per-item error so other tracks remain inspectable. Audio listing only reads cached evidence and reports `fingerprint_missing` where another scan is needed.

Audio candidates require at least 98% duration agreement, at least 98% fingerprint agreement across each recording, and mean bit errors no greater than 2. Each agreeing fingerprint word has at most 5 differing bits. Low-information fingerprints are excluded. Pairwise reports include alignment offset, aligned coverage, agreement coverage, and mean bit errors; these values are evidence, not confidence probabilities. Groups require every member to match every other member, preventing transitive similarity from combining different versions.

Merge accepts metadata candidates or currently verified acoustic candidates. It retains audio files and the keeper's timing by default. Use `--performance-from-ref` explicitly to transfer cues/grid from a selected entry. Registration, fingerprint-cache changes, merge, and applied sync participate in existing history and undo; database-only work does not back up audio.

## Preview sync using references

```sh
engine-cli library sync \
  --from "$HOME/Music/Engine Library" \
  --to "/Volumes/PRIME-128GB/Engine Library" \
  --playlist-ref "$PLAYLIST_REF" --dry-run --json
```

Use `--root` for the whole library. Scan both libraries first to enable acoustic matching; exact file matches do not require fingerprints. Preview never decodes audio or creates cache/history files. It reads media to verify hashes, so large collections can take time even with cached fingerprints.

For independent imports, sync reuses a unique destination match by identical file hash or near-full acoustic evidence. It preserves that destination entry's audio, metadata, cues, and grid; playlist membership points to its native Engine identity. `audio_matches` explains each match and whether it is reused, and `summary.tracks_reused` counts reuse. Multiple possible matches block apply. Persisted associations are revalidated on later sync; changed or unavailable evidence produces a conflict. Default sync preserves destination-only content; `--mirror` explicitly enables removals within the selected scope.

## HTTP operations

Submit parameters as the JSON body of `POST /v1/operations/{operation}`. The response is `202` with a job object; its `Location` header points to job status. Results and progress use the same job contract as other operations:

| Operation | Parameters |
| --- | --- |
| `library.register` | `{"new_copy":false}` |
| `library.entity.list` | `{}` |
| `track.duplicate.scan` | `{}`, `{"ids":[1,2]}`, or `{"refs":["<ref-a>","<ref-b>"]}` |
| `track.duplicate.list` | `{"type":"audio"}` |
| `track.duplicate.inspect` | `{"refs":["<ref-a>","<ref-b>"]}` or `ids` |
| `track.duplicate.merge` | `{"keep_ref":"<keeper>","remove_refs":["<duplicate>"]}`; optional `performance_from_ref` |
| `track.inspect` / `playlist.inspect` | `{"refs":["<ref-a>","<ref-b>"]}` or `ids` |
| `library.sync` | `{"source":"<path>","destination":"<path>","scope":{"playlist_refs":["<ref>"]},"dry_run":true}` |

`GET /v1/tracks?refs=<ref-a>,<ref-b>` and `GET /v1/playlists?refs=<ref-a>,<ref-b>` also provide batch details. Detail batches accept 1–500 references, preserve order and repeated selections, and return per-item `not_found` for missing or wrong-library references. Choose either `ids` or `refs`. Registered numeric detail reads include references too, including playlist member tracks. Entity enumeration provides the complete mapping of native IDs to references.

Library-changing operations require Engine DJ Desktop to be closed. Read-only previews and details remain available while it is running. Scanning writes the fingerprint cache and is therefore guarded like other mutations.
