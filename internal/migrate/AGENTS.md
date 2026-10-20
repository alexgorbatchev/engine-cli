---
created_on: 2026-10-18 16:45
last_modified: 2026-10-18 16:52
status: current
---

# Library migration

- Keep Engine DJ to Rekordbox export in `engine-cli`, exposed through `migrate export` and the HTTP `migrate.export` operation. Use one shared implementation for both; run without a TUI or a standalone migration executable.
- Read source databases without modifying them. Copy audio byte-for-byte, verify existing content before resuming, and retain playlists, cues, beat grids, and exact absolute destination XML URIs.
- Keep copied media in the target's `Contents/`, with `rekordbox.xml` and `.sync_state.json` at the target root. Preserve source-path filename digests and exclude unavailable or unfinished audio from XML.
- Report progress through both CLI and HTTP job/SSE interfaces using Engine's existing reporter. Include intermediate read/copy/XML phases, per-track paths and byte counts in copy-event metadata, stable job identity/sequence, and monotonic percentages. Reserve terminal events and 100% for the CLI/API operation owner. Persist completed work on cancellation or copy failure.
- Use offline synthetic library/audio fixtures and update CLI/API documentation with interface or behavior changes.
