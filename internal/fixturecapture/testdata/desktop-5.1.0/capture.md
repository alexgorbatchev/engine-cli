---
created_on: 2026-10-31 18:05
last_modified: 2026-10-31 18:05
status: current
---

# Desktop 5.1.0 Smartlist evidence

This data is for maintainers implementing Smartlist serialization, evaluation and tree operations. The user created the 38 Smartlists in Engine DJ Desktop 5.1.0 on macOS 26.6.2 using the original v1 synthetic track pack on October 30–31, 2026.

## Evidence and ownership

`cases.json` contains the exact SQL TEXT bytes for all 38 stored rule documents, their hashes, screenshot-observed memberships, and the 31 captured tracks' filter-relevant fields. SQLite types and NULLs are retained. Fixture numbers identify the synthetic pack, not Engine database row IDs. The source export hashes and screenshot-capture hash bind the curated data to retained private evidence.

Engine CLI's `library fixture capture` command archived both offline inputs before opening any database, recovered journals only in separate working copies, and exported standalone snapshots through the existing native SQLite backup API. The completed capture contains seven databases and 41 input files per side. Its original input inventories were identical before and after capture. The extraction command's reviewed source and actual binary are bound in private qualification receipts; its full publication qualification is tracked separately.

All 38 rule documents are byte-identical between the before and after captures. `links.json` records the Smartlist paths with native UUID references resolved to synthetic names, plus the two converted playlists' memberships. UUIDs, source paths, origin identities, binary databases, audio, screenshots and personal library content are excluded from these curated fixtures.

## Interpretation limits

- The original pack stored MP3 comments in TXXX frames rather than COMM. Every captured Track.comment is NULL. T7, T8 and C1 reflect that defect; they do not prove cleared-empty-string behavior.
- Desktop's analyzed BPM and keys differ from the generator's intended values. Two unanalyzed tracks were reimported on October 31. Use captured metadata and observed memberships rather than generated predictions.
- Actual B1–B4 and K1–K2 contain one numeric/key rule each; the planned title-prefix restriction is absent. The saved rule document is authoritative.
- T11 stores the escaped equality parameter for 100% and displays zero tracks without a reported error. This observation does not establish the internal SQL failure path.
- Track metadata is the before-state projection. It omits filesystem paths and origin identifiers; stemsStatus requires other tables and is not reconstructed here.
- Hardware and unsupported rule versions were not exercised.

## Tree and conversion observations

The Desktop procedure performs distinct operations: move Beta into Gamma, move P3 after Gamma, then convert T1 twice. P2's parent changes from Beta;Child; to Gamma;Beta;Child;. P3's final link points to the original Smartlist T1. P1's final next-playlist link points to the newly converted ordinary playlist T1. These are the combined final effects, not the result of the Beta move alone.

The two added ordinary playlists are T1 and T1 2; each contains fixture tracks 1, 2, 3 and 4. The original T1 Smartlist remains. This membership is established by native captured rows; the supplied screenshots do not select the converted playlists.

## Pack source

`pack/manifest-v1.json` preserves the original input intentions and predictions. They are not expected results. `pack/generate.ts` and `pack/verify.ts` preserve the corrected generator and verifier, which write and validate COMM frames. They do not reproduce the original v1 comment defect, Desktop analysis, manual edits or imported timestamps.

The verifier reads the manifest.json produced by the generator; manifest-v1.json is retained evidence rather than its runtime input.

The archived checklist template is retained as provenance of the handover procedure. The scripts depend on Bun, ffmpeg/ffprobe and the pinned mutagen CLI declared in their source; they are not run by hermetic Go tests. Generated audio, returned libraries and generated local checklists/manifests stay ignored. Never use the historical procedure as an unattended operation on a personal library.