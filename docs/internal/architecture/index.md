---
created_on: 2026-10-25 08:41
last_modified: 2026-10-29 20:03
status: current
---

# engine-cli architecture

These diagrams explain collection management, asynchronous jobs, and mutation recovery for contributors.

| Diagram | What it explains |
| --- | --- |
| [System overview](overview.html) | CLI and HTTP entry points, operation execution, collection services, library storage, recovery, and managed tools |
| [HTTP job lifecycle](job-lifecycle.html) | Queued and running jobs, success, failure, cooperative cancellation, scheduling, and retained results |
| [Collection write safeguards](collection-write.html) | The ordinary API collection-write path: access control, Desktop checks, safety backups, before/after history, failure recovery, and exact-plan M3U import |

All three specifications cite committed engine-cli `b85be4d87dc1045eca6133f6868e0b13f7bceb62`. Their source links and claims describe that revision. Documentation and viewing configuration added afterward do not change the runtime evidence. The overview groups cooperating packages; it is not a complete package inventory. The collection-write drawing identifies its specialized sync, creation, undo, and preview exclusions explicitly.

The collection-write HTML was regenerated with the CDP-enabled [Archify fork](https://github.com/alexgorbatchev/archify) at `6bd7822dcda53c7a87978ac73f413a9b1968c25b` (3.2.1-dev.1). Its embedded software notices preserve the Apache 2.0 license and historical MIT grants. Other diagrams retain their original renderer and notices.

## Viewing

The collection-write page's bulk-metadata card describes the `track.batch.update` addition committed in `07441d1`, after the pinned source revision: all selected tracks share one transaction, snapshot, and undo record under the existing collection-write safeguards.

The collection-write page's M3U-import card cites committed engine-cli `cbac846503e303552ed223020d47a37f3c3783a9`: read-only review and pre-artifact validation precede the ordinary protected write path, while playlist creation and ordered memberships commit atomically and share one undo record. It covers new playlists from existing collection tracks, not acquisition, append, or replacement. The original diagram source pin and bulk-metadata evidence remain unchanged. Uncommitted working-tree changes are outside this review.

The overview's saved-marker card describes the working-tree performance-read endpoint separately from the pinned evidence: Engine CLI owns database access and the collection barrier, while `djtools` decodes supplied blobs without opening a database.

1. Install [devhost](https://alexgorbatchev.github.io/devhost/) with its native `serve` command. Follow its HTTPS setup instructions if certificate trust has not been configured; that step can require an interactive terminal.
2. From this repository, run `just architecture-serve`. Open the URL devhost prints; the default is `https://engine-cli-archify.localhost`.
3. Stop serving with Ctrl-C. Devhost removes this session's routes and leaves its shared Caddy proxy running.

Devhost serves this Markdown index and the generated HTML directly. No Bun server or Archify installation is needed to view the committed diagrams. Set `ENGINE_DEV_HOST_SUFFIX` in the invoking environment to use another hostname suffix; that hostname must resolve to the development machine. Workstation-specific hostnames belong in local environment configuration, never tracked files. Each concurrent viewing session needs a distinct hostname.

The generated HTML also opens as standalone files. The diagram source links are local-only because the pinned repository identity uses SSH.

## Maintaining

1. Review the diagrams when changing CLI/API ownership, scheduling, mutation safeguards, tool execution, or persistence. Trace affected calls to their actual execution or write sites. Inspect committed bytes before advancing a specification's `meta.repository.revision` or source ranges; do not label working-tree changes as evidence for a commit.
2. Keep JSON specifications and generated HTML together here and update the table above when adding or removing diagrams. The recipe processes each top-level JSON specification except delivery receipts; extend it when adding diagrams in subdirectories. Preserve the generated HTML bytes rather than formatting renderer output.
3. Set `ARCHIFY_CLI` to an installed Archify 3.2.0 `bin/archify.mjs`, ensure Node is available, then run `just architecture`. Set `ARCHIFY_CHROME` to a supported Chrome/Chromium executable if browser discovery needs help. An unset renderer path, invalid evidence, failed artifact check, or failed/skipped browser gate fails regeneration.
4. Inspect changed drawings before claiming visual review. Automated browser checks validate browser behavior and geometry; they do not establish perceptual quality. Run `just architecture-serve` and verify each changed page's exact URL before delivering it.

Each regeneration run writes its validation and browser receipts into a separate `.tmp/architecture-review/run.*` directory; delivery receipts beside the HTML are ignored. `.archify/` is scratch space. Keep browser captures, renderer installation paths, and verification receipts out of commits.
