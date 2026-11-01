---
created_on: 2026-10-31 21:14
last_modified: 2026-10-31 21:33
status: current
---

# Native Desktop repair handoff

This maintainer-only harness prepares disposable synthetic Smartlist libraries for human Engine DJ Desktop acceptance. It is not a public Engine CLI command. Native qualification does not establish Desktop compatibility or complete the separate interrupted-repair acceptance work.

## Maintainer preparation

1. Review and commit the harness, captured golden and all affected implementation. Complete the required module checks and independent review. Keep the historical Linux fixtures and receipts unchanged.
2. Dispatch **Desktop repair handoff** with full reviewed `source_revision` and ancestor `golden_revision` commits. Inputs are passed as data. The workflow builds on both native macOS architectures; it never cross-compiles a substitute for native execution.
3. Require both jobs to pass. Each job builds the existing CLI and private helper from a clean complete checkout, binds native binary metadata and exact golden bytes, and refuses existing output namespaces. Failed or cancelled runs retain partial local evidence; never reuse a partial root.
4. Download the matching `desktop-repair-evidence-darwin-<arch>-<run-id>` maintainer artifact. Require a successful terminal receipt, unchanged source inventories, the reviewed source/golden revisions, native host/build metadata, and the expected binary/helper hashes before delivering either archive. Inspect the retained baseline manifests and command, History, media and Undo receipts. The helper requires all four distinct cases and authentic opaque T1/T3 rules. Every baseline is verified before the first repair. Qualification then exercises repair, idempotence, complete native row preservation, History ownership, real relocation with the original root absent, and real CLI audio streaming. Reconnect creates one repair record; original deletion amends its original entry; healthy and unknown-removal cases create none. Exact-ID Undo uses the reconnect repair record and original deletion record respectively.
5. Deliver only a successful architecture-matching synthetic archive and its sanitized receipt. The delivery seed is independent of Undo qualification and remains repaired. Archive extraction is checked through the native helper and the extracted CLI's actual audio endpoint. Private full-row source/build evidence stays outside the archive. Do not add returned databases, private libraries, credentials, or arbitrary scratch directories to workflow artifacts.

## Desktop evaluation

1. Extract the matching archive into a new disposable directory. Keep `delivery.json` and `README.txt`; the recorded IDs belong to this delivery, not a previous qualification run. Close Engine DJ Desktop before any CLI write.
2. Record the actual Desktop version, macOS version and CPU architecture; the reference observations used Engine DJ Desktop 5.1.0 on macOS 26.6.2. Open each of `healthy`, `reconnect`, `unknown-removal`, and `original-deletion` in Desktop. Record actual visible Smartlist names, rules, memberships, sibling order and media playback. The T1 title rule selects `Deep House`, `House Party`, `Warehouse`, and `HOUSE`; the independent T3 title rule selects `Techno`. Record visible titles rather than inferring membership from the fixture design.
3. Record repaired-first observations before Undo. Healthy rows must remain unchanged; reconnected lists must resolve under `Renamed`; unknown damaged lists must be absent; the original-deletion case must retain its independently healthy component.
4. Close Desktop. Run the two exact commands in the delivered `README.txt`: reconnect's repair Undo and original-deletion's original operation Undo. The bundled script checks the CLI hash and actual library identity through a temporary loopback API, stops and joins that API, checks that the exact-ID preview preserves History, and retains preview/application logs. It stops on refusal. Do not use latest Undo or substitute IDs from another bundle.
5. Reopen those two libraries and record their distinct Undo states. Reconnect Undo restores the original broken saved-list paths under `House`, while the ordinary playlist remains `Renamed`; do not expect that deliberately damaged component to resolve or play through its saved lists. Its independent healthy component must remain unchanged. Original-deletion Undo restores the healthy `House`/`Leaf` playlists and their saved-to-saved-to-ordinary sibling chain, rules, membership and playable media. Stop on an error or unexpected change; preserve the bundle and output. Do not repair or reseed it to make an observation fit the expected result.

Keep native automated results and human Desktop observations separate. A successful workflow produces an acceptance candidate, not a Desktop acceptance claim.
