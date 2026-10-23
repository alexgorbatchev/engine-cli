---
created_on: 2026-08-23 22:20
last_modified: 2026-10-23 10:04
status: current
---

# engine-cli

Go CLI utility for managing, verifying, backing up, synchronizing, and repairing Engine DJ collection databases (`m.db`), playlist hierarchies, and album artwork caches.

## Commands
- **Build Local Binary:** `just build` (compiles to `bin/engine-cli`)
- **Run CLI with Arguments:** `just run [args...]`
- **Run CLI in Agent Mode:** `just run-ai [args...]` (`AGENT=1`)
- **Setup:** `just setup` (`go mod download` and `.githooks` as `core.hooksPath`)
- **Run Tests:** `just test` (`go test -v ./...` and `bash scripts/test-git-hooks.sh .githooks`)
- **Run Static Analysis & Tests:** `just check` (`go vet ./...`, `go test -v ./...`, and the Git hook tests)
- **Run Linter / Static Analysis:** `just vet` or `just lint` (`go vet ./...`)
- **Format Source Code:** `just fmt` (`go fmt ./...`)
- **Verify Library Integrity:** `bin/engine-cli library verify`
- **Check Desktop Status:** `bin/engine-cli system check [--checks engine_desktop] [--json]`; HTTP `GET /v1/system/check` or `system.check`.
- **Discover Libraries:** `bin/engine-cli library discover` and HTTP `library.discover` inspect common macOS/Windows locations and attached drives without modifying libraries.
- **Library State:** `library preferences list` and `library preferences update <path> --name <text> --hidden=<bool>` store cosmetic names and visibility in a separate SQLite state database. `--state-db` overrides `$XDG_STATE_HOME/engine-cli/state.db` (fallback `$HOME/.local/state/engine-cli/state.db`). Discovery continues reporting hidden libraries.
- **Library Lifecycle:** `library delete <path>`, `library move <path> --destination <new-path>`, and `library media aggregate <path> --mode copy|move --shared-mode copy|move` share API operations of the same names. Review with `--dry-run`, then apply with its `--plan-token`; deletion also requires `--confirm-path <complete-path>`. Delete removes managed databases/artwork, retains recovery/unrelated files, and optionally removes referenced media using `--delete-media`, with `--keep-shared` protection. Shared-media checks include previously opened/discovered hidden libraries; unavailable peers block unsafe shared-file changes. Moves preserve library identity and undo history, including across filesystems.
- **Create Library:** `bin/engine-cli library create <path> [--json]` and HTTP `library.create` initialize native schema 3.0.2 at an explicit path, with desktop protection and undo history in the new library. Never overwrite an existing `Database2` directory. Use the canonical `internal/db/library_schema.sql`; test-only synthetic schemas are not suitable for user libraries.
- **Backup Library:** `bin/engine-cli library backup [path] [--list]`
- **Restore Library:** `bin/engine-cli library restore <path>`
- **Refresh On-Disk Metadata:** `bin/engine-cli library refresh`
- **Sync Libraries:** `bin/engine-cli library sync --from <path> --to <path> (--playlist-ref <ref> | --playlist-id <id> | --root) [--dry-run]`
- **Register Library Identity:** `bin/engine-cli library register [--new-copy] [--json]`; list opaque references with `library entity list --json`.
- **Prune Orphaned Artwork:** `bin/engine-cli library prune`
- **List Playlists:** `bin/engine-cli playlist list`
- **Inspect Playlists:** `bin/engine-cli playlist inspect <id...> [--json]`
- **Inspect Tracks:** `bin/engine-cli track inspect <id...> [--json]`
- **Suggest Metadata Cleanup:** `bin/engine-cli track suggest <id|ref...> [--json]` or `track suggest --playlists <id|ref,...>`; HTTP `track.suggest` accepts `tracks` or `playlists` string arrays. Configure `ENGINE_LLM_PROVIDER` (default `openai`), required `ENGINE_LLM_MODEL`, optional `ENGINE_LLM_BASE_URL`, and `ENGINE_LLM_API_KEY` or a provider-specific key. The same `--llm-provider`, `--llm-model`, and `--llm-base-url` flags work on `track suggest` and `api serve`. Playlist selection includes direct members only, with first-seen deduplication and complete batched processing. Suggestions never write the library.
- **Create Playlist:** `bin/engine-cli playlist create <name> [--parent <id>] [--folder]`
- **Rename Playlist:** `bin/engine-cli playlist rename <pl|id> <name> [--json]`
- **Edit Track Information:** `bin/engine-cli track update <tr|id|path> [--title <text>] [--artist <text>] [--album <text>] [--genre <text>] [--comment <text>] [--label <text>] [--composer <text>] [--remixer <text>] [--year <year>] [--rating <0-5>] [--artwork <path> | --clear-artwork] [--json]`
- **Remove Playlist:** `bin/engine-cli playlist rm <id> [--force]`
- **Move/Reorder Playlist:** `bin/engine-cli playlist move <id> --to <id> [--before <sibling-id>]` inserts before a sibling or appends when omitted. Listings follow Engine's saved sibling links; moving updates both folders atomically and preserves memberships and undo.
- **Add Track to Playlist:** `bin/engine-cli playlist track add <id> <id|path...>`
- **Remove Track from Playlist:** `bin/engine-cli playlist track rm <id> <id|path...>`
- **Move Track Between Playlists:** `bin/engine-cli playlist track move <src-id> <dst-id> <id|path...>`
- **Add Track to Library:** `bin/engine-cli track add <path...> [--to-playlist <id>] [--move]`
- **Upgrade Track Audio:** `bin/engine-cli track upgrade <id|path> [--query <text>] [--keep-timing | --clear-timing]`
- **Remove Track from Library:** `bin/engine-cli track rm <id|path...> [--delete-file]`
- **Move Track File on Disk:** `bin/engine-cli track move <id|path> <dest-path>`
- **Relocate Track Paths:** `bin/engine-cli track relocate --from <path> --to <path>`
- **Normalize Artwork to 1:1 Square:** `bin/engine-cli track artwork normalize`
- **Find and Compare Duplicates:** `bin/engine-cli track duplicate list [--type all|metadata|filename-len|audio] [--json]`; `bin/engine-cli track duplicate inspect <id|ref...>`.
- **Fingerprint Audio:** `bin/engine-cli track duplicate scan [--ids <ids> | --refs <refs>] [--json]`; fingerprints are SHA-256 keyed in `.engine-cli/fingerprints/index.json`, use full audio through Go Chromaprint and FFmpeg decoding, and participate in undo. Audio listing only reads cached evidence.
- **Merge Duplicates:** `bin/engine-cli track duplicate merge <id...> --keep <id> [--performance-from <id>]`
- **Inspect Undo History:** `bin/engine-cli library history`
- **Undo an Operation:** `bin/engine-cli library undo [id] [--dry-run] [--force]`

## Setup & Environment
- **Prerequisites:** Go 1.27.2+, `ffmpeg`, `ffprobe`, `just`. `fetch-track`, `fetch-mix`, and `tag-track` are managed dependencies.
- **Managed Tool Dependencies:** Install, update, and verify `fetch-track`, `fetch-mix`, and `tag-track` through `github.com/alexgorbatchev/godeps`; never call a tool's own updater. `internal/acquire/requirements.go` owns minimum versions and release repositories. Managed releases live in `$XDG_DATA_HOME/engine-cli/bin`, which startup puts ahead of `PATH`. Running a missing or outdated tool fails with `tool_unavailable` or `tool_outdated` unless the request sets `auto_install`, which installs or updates it through godeps first; install failures use `tool_install_failed`. Rely on godeps for version parsing; it never lets an unreadable version satisfy a requirement. When godeps lacks behavior or misbehaves, fix and release godeps rather than working around it here.
- **Default Library Directory:** `$HOME/Music/Engine Library` (override with `-l <dir>` / `--lib-dir`).
- **API Startup Libraries:** `ENGINE_LIBRARY_PATHS` supplies semicolon-separated existing library directories to `api serve`; ignore empty entries and trim surrounding whitespace. Explicit `--library` flags replace the environment list; `--lib-dir` still selects the unscoped default library.
- **Portable Library Storage:** Store default backups in `<lib-dir>/.engine-cli/backups/` and undo history in `<lib-dir>/.engine-cli/history/`. Library-contained history paths must follow the library when it moves.
- **Temporary Files:** Production code uses the OS temporary directory for disposable workspaces, never project `.tmp/`. Agent development artifacts may use project `.tmp/`. Atomic publication staging stays beside its destination to support same-filesystem rename.

## Conventions
- **LLM Integration:** Use Charm's Fantasy (`charm.land/fantasy`) for model calls, typed structured output, and agent/tool workflows. Use its native provider and schema APIs rather than a custom compatibility layer.
- **LLM Metadata Suggestions:** Accept explicit track or playlist identifiers through shared CLI/API behavior. Send selected metadata with a cleanup prompt to the LLM and return identifiable title/artist suggestions for user review. Generating suggestions is read-only; never apply suggestions automatically.
- **UI-Driven Contracts:** UI workflows drive HTTP API design and associated CLI output. Extend or reshape shared results when the UI needs richer data rather than constraining the UI to an existing response shape. Keep CLI and HTTP results aligned, preserve stable entity identity, and retain snapshot/undo guarantees for mutations.
- **Collection Editing:** Track metadata updates change only explicitly supplied fields, preserve audio and performance data, and support snapshot/undo through CLI and API. Expose `ratingStars` from 0 to 5 and store Engine ratings in units of 20. Playlist renaming preserves identity, hierarchy, and memberships. Artwork previews read linked inline bytes or Engine’s hash-addressed cache without modifying the collection. Artwork edits stage with metadata, support upload/unset, preserve audio files, normalize to square 1400×1400 cache images and 256×256 thumbnails, and participate in the same snapshot/undo operation.
- **Batch UI Reads:** When a UI needs details for multiple entities, accept multiple explicit identifiers in one CLI/API request and return identifiable results together; do not require one request per item. Define ordering, limits, and per-item failure behavior. Ask for clarification when the UI workflow or required response is unclear.
- **Runtime Checks:** Check whether Engine DJ Desktop is running, not whether it supports the host platform. Linux always reports `status: "ok"`, `running: false`, and empty PIDs without scanning processes. macOS and Windows inspect their native process snapshots; distinguish a negative result from a failed check there. Keep CLI JSON and HTTP check results aligned.
- **Desktop Mutation Guard:** Recheck Engine DJ Desktop immediately before CLI/API library mutations, before snapshots or history writes. Refuse mutations while it is running or its status is unknown. Read-only operations and dry runs remain available. A UI status observation is not a lock or authorization to skip the fresh check.
- **Library Discovery:** Recognize current `Database2/m.db` libraries; ignore legacy root-level database leftovers. Keep enumeration read-only and provide a friendly `name`, `source`, and absolute path for UI library pickers through the HTTP API.
- **Reversible Library Operations:** Every operation that modifies the source library must support undo, including database changes and changes to audio files or artwork. Apply this requirement to existing operations as well as duplicate-track operations, through both the CLI and HTTP API.
- **Failed Acquisitions:** A failed acquisition is retryable staging state, not an undoable change. Acquisitions declare each staged item's directory with `history.Workspace`; a failed operation whose only changes are inside declared workspaces leaves no history entry. Failures that changed databases or other files keep their undo record.
- **History Scope:** `history.Run` takes a scope. `ScopeCollection` snapshots every Engine database before and after the operation; `ScopeFiles` operations (API `staging` and `filesystem` effects) never write Engine databases and copy none. Undo inherits the scope of the entry it reverses.
- **Unreferenced History Storage:** Under the history lock, each operation removes saved objects, entry work directories, and temporary entries that no remaining entry references before it runs. Leave referenced storage and unrecognized names in place.
- **Affected Files Only:** Plan and preserve only files an operation will modify or delete. Database-only operations must not copy audio or artwork. Record file moves as reversible path changes rather than audio backups. Explicit library backups may include all databases and artwork.
- **Undo Conflicts:** Reverse an operation even after later library changes when there are no conflicts. Refuse conflicting undo by default; provide an explicit `--force` override.
- **Entity Identity:** Track changed tracks, playlists, and other entities using stable UUIDs or hashes so history and undo retain identity across moves, relocations, and playlist membership changes; names, paths, and membership alone are not identities.
- **Audio-Based Duplicate Recognition:** Account for independently imported copies of the same audio using acoustic fingerprint evidence, such as Chromaprint. Engine row IDs and origin identities alone do not establish musical uniqueness; duplicate detection must go beyond titles, filenames, and import lineage.
- **Library Listings Identity:** Opened-library and known-library listings expose the opaque path `key`, Engine `engine_uuid`, and the sync `instance` identity with its `registration` state, read fresh on each request; clients build links on `instance`, never on paths or keys.
- **Copied Library Registration:** Require explicit registration to assign a copied library its own instance identity. Preserve identity when a library is relocated; do not derive instance identity from mount paths or silently treat cloned sidecar IDs as distinct sync peers.
- **HTTP API:** Expose all library, playlist, and track operations over loopback HTTP. Incorporate Fetch Track progress into overall Engine progress; every progress event includes a monotonic completion percentage from 0 to 100, with 100 reserved for successful completion.
- **Operation Scheduling:** Share write serialization and collection access across all opened libraries. Read jobs may overlap each other and non-collection writes, including staging fetches and tool installs. Collection writes, restores, syncs, and unresolved filesystem writes exclude read jobs; pending collection writers must not starve behind new reads. Serialize all writes, including `library.backup`, `migrate.export`, and requests with `auto_install:true`. Backup and export retain their catalog `read` effect because they leave the source collection unchanged. Classify bound parameter effects before execution; keep cancellation and shutdown responsive while waiting. Engine database connections use a five-second busy timeout on every pooled connection.
- **Shared Progress Schema:** Keep CLI and HTTP progress field names and types aligned with Fetch Track, including `job_id`, `sequence`, and string-valued `error`. Preserve upstream event details under `source`; retain structured `{code,message}` errors for HTTP job/status and request responses.
- **Failure Codes:** Every reported failure must carry a stable code: `code` in structured HTTP errors and `error_code` alongside string-valued progress, acquisition, staging, migration, and history errors. Classify typed causes rather than parsing messages. Use `operation_failed` for unclassified failures and document new codes in `docs/http-api.md`. Diagnostic messages are not UI copy; browser consumers must map codes to user-friendly guidance and never render raw server errors.
- **Track Upgrades:** Use Fetch Track CLI to acquire replacement audio. Preserve the original track's curated metadata. Replacing a radio edit with an extended mix is intended upgrade behavior.
- **Entity Selection:** Prefer opaque `eng:<instance>:<kind>:<entity>` references from registered libraries. Numeric IDs remain library-scoped; exact track paths remain explicit selectors. Names, titles, and file basenames are display/search fields only. Track entity identity derives from Engine's origin pair; playlist lineage is persisted in `.engine-cli/identity.json`. Acoustic matches are evidence, not an immutable entity identity. See [fingerprints and references](docs/fingerprints.md) for batch selectors and sync behavior.
- **Library Sync:** Sync direction is explicit source to destination; USB selection resolves to the same path-based operation. Scope is selected playlist IDs (including descendants) or the library root. Dry runs are read-only previews shared by CLI and HTTP. Preserve destination-only content by default, require explicit mirroring for removals, and refuse unresolved conflicts. Sync baselines and mappings live in portable `.engine-cli/sync/` storage and must participate in undo.
- **Distribution for Non-Technical Users:** `engine-cli` is distributed for non-technical users (working and bedroom DJs, music collectors). All installation instructions, CLI outputs, error messages, and documentation must remain straightforward, accessible, and actionable without requiring developer knowledge. Pre-compiled binaries are the primary release and distribution vehicle.
- **Automated Database Snapshots:** All database modification operations (`library sync`, `library refresh`, `library restore`, `library prune`, `library undo`, `playlist create`, `playlist rm`, `playlist move`, `playlist track add`, `playlist track rm`, `playlist track move`, `track add`, `track upgrade`, `track rm`, `track move`, `track relocate`, `track artwork normalize`, `track duplicate merge`) must automatically create a snapshot backup in `<lib-dir>/.engine-cli/backups/` before applying changes, unless the user opts out with `--no-backup`. Read-only undo previews do not create safety snapshots.
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

## Git Workflow
- Start every unit of work from the latest revision: `git fetch`, then fast-forward local `main` to `origin/main` in the main checkout (`git merge --ff-only origin/main`). If it cannot fast-forward, `main` holds commits from a failed push; run `git pull --rebase` and `git push` before anything else. Never begin work on a stale `main`.
- Make every change in a linked worktree under `.workspaces/<name>` on a new branch created from that up-to-date `main` (`git worktree add -b <branch> .workspaces/<name> main`). Never edit the main checkout directly; other sessions share it.
- Validate and commit in the worktree, then bring `main` level with `origin/main` again and fast-forward it from the main checkout (`git merge --ff-only <branch>`; rebase the branch onto `main` first when `main` has moved). Afterwards remove the worktree and delete the branch (`git worktree remove .workspaces/<name>`, `git branch -d <branch>`). Finish by confirming `main` is level with `origin/main` (`git fetch`, then `git status -sb` shows no ahead or behind count); the hook only attempts the push. Yield the turn to the user only after the change is merged into `main`, synced with `origin/main`, and its worktree removed.
- `just setup` installs `.githooks` as `core.hooksPath`. Its post-commit and post-merge hooks push a branch to its same-named upstream after every commit or merge, so `main` reaches `origin/main` immediately; branches without one, including worktree branches, stay local. Hooks act only on this repository, wherever it is checked out; never invoke enclosing repositories or their recipes. `bash scripts/test-git-hooks.sh .githooks` tests them.
- A merge into `main` publishes it on GitHub, though not as a release. Never amend, rebase, or force-push commits already on `origin/main`; fix mistakes with a follow-up change. Inspect staged changes for private hostnames before committing. When a push is rejected because `origin/main` moved, run `git pull --rebase` on `main`, then `git push`.

## Boundaries
- **Always:** Automatically record all new instructions in the most appropriate `AGENTS.md` file immediately upon receipt (check with user if existing instructions conflict).
- **Always:** Back up Engine DJ SQLite databases (`Database2/` and root `.db` files) before applying modifications.
- **Always:** Any time code is changed such that results from running that code are changed, a test file must be changed as well; 90% code coverage is required (the `scripts/` folder is explicitly excluded from this rule).
- **Always:** Maintain >= 90% statement code coverage across all Go packages.
- **Always:** Run `just test` and `just vet` before committing changes.
- **Never:** Perform lossy-to-lossy audio re-encoding when updating artwork or metadata.
- **Never:** Commit compiled Go binaries (e.g. `bin/engine-cli`) or master databases to git.
