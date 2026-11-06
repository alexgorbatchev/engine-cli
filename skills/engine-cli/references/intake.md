# Engine CLI incoming local-file command reference

All commands inherit the root flags documented in [SKILL.md](../SKILL.md). Select the library with `-l <library>`. Source paths refer to the CLI/API machine. Queue item IDs are positive integers distinct from collection track IDs and staging IDs.

The library-local durable queue preserves source files and prepares separate staged copies. An open API library resumes preparation; a standalone `process` waits for preparation to reach no eligible work. Preparation never imports into the collection. Source identity, size and modification time are observed around copying; quiet time is a heuristic, not a guarantee that another program has finished writing.

## `engine-cli intake`

Usage: `engine-cli intake [flags]`.

- `--help, -h` (bool; default `false`): display queue command help.

## `engine-cli intake list`

Usage: `engine-cli intake list [flags]`.

- `--json` (bool; default `false`): print items, durable control state and confirmed worker/recovery codes as structured data.
- `--help, -h` (bool; default `false`): display list help.

Human output lists item ID, status, attempt, source, staged ID, history ID and failure code when present. Agent output uses compact labelled values. Listing waits for receipt reconciliation, not completion of media preparation.

## `engine-cli intake enqueue`

Usage: `engine-cli intake enqueue <path...> [flags]`.

- `--json` (bool; default `false`): print the recorded items as structured data.
- `--help, -h` (bool; default `false`): display enqueue help.

Record one or more local source versions in FIFO order. Repeating an already recorded version retains its durable identity, including imported or history-owned cancelled versions. Earlier successful items remain queued if a later path fails. Closing the command joins its local worker; use `process` to wait for staging or leave the API library open.

## `engine-cli intake process`

Usage: `engine-cli intake process [flags]`.

- `--json` (bool; default `false`): print the final queue/control view as structured data.
- `--help, -h` (bool; default `false`): display processing help.

Wait for the native worker to reconcile import receipts and prepare eligible items until no work remains. Cancel the command to stop waiting; shutdown joins its worker, and another open resumes interrupted preparation. Paused queues complete without preparation. Individual preparation failures remain visible on their items; inspect the returned status before retrying. No collection snapshot or Undo entry is created.

## `engine-cli intake pause`

Usage: `engine-cli intake pause [flags]`.

- `--json` (bool; default `false`): print durable control state as structured data.
- `--help, -h` (bool; default `false`): display pause help.

Stop and join the local worker, then durably pause preparation and explicit imports. Source files and queued versions remain retained.

## `engine-cli intake resume`

Usage: `engine-cli intake resume [flags]`.

- `--json` (bool; default `false`): print durable control state as structured data.
- `--help, -h` (bool; default `false`): display resume help.

Reconcile retained import receipts and explicitly enable preparation for this library. Restored queues remain suspended until this explicit action; unavailable History ownership remains a recovery condition rather than permission to repeat an import.

## `engine-cli intake cancel`

Usage: `engine-cli intake cancel <id> [flags]`.

- `--json` (bool; default `false`): print the updated item as structured data.
- `--help, -h` (bool; default `false`): display cancel help.

Cancel a queued, preparing, staged or failed item without deleting its original source; cancelling an already cancelled item retains that state. Cancellation does not undo an already committed collection import; use its History entry for that.

## `engine-cli intake retry`

Usage: `engine-cli intake retry <id> [flags]`.

- `--json` (bool; default `false`): print the updated item as structured data.
- `--help, -h` (bool; default `false`): display retry help.

Retry a failed or cancelled item only when it has no History-owned import receipt. A retained prepared copy returns to staged; otherwise preparation resumes from queued. Inspect the failure and original source first; a changed source is a different version rather than authorization to import stale staged bytes.

## `engine-cli intake import-review`

Usage: `engine-cli intake import-review <id> [flags]`.

- `--to-playlist` (string; default empty): existing destination playlist selector, or omit for collection-only import.
- `--json` (bool; default `false`): print the review and exact plan token as structured data.
- `--help, -h` (bool; default `false`): display review help.

Review one prepared staged item. Output includes ready/skipped counts, item status and destination, playlist and plan token. Review is read-only and creates no snapshot or Undo entry.

## `engine-cli intake import`

Usage: `engine-cli intake import <id> [flags]`.

- `--plan-token` (string; default empty; required): exact token from the matching item and playlist review.
- `--to-playlist` (string; default empty): same playlist selector used for review.
- `--json` (bool; default `false`): print item ID, collection track ID and History ID as structured data.
- `--help, -h` (bool; default `false`): display import help.

Apply only the reviewed staged bytes with fresh Desktop protection, automatic backup unless `--no-backup`, and collection History/Undo. Durable receipt reconciliation resolves an interrupted post-commit result without repeating the import. Undo and Undo-of-Undo update the effective queue status while retaining source-version deduplication.

## Reviewed workflow

1. Run `engine-cli -l <library> intake enqueue <source-path...>`.
2. Run `engine-cli -l <library> intake process`, then inspect `intake list --json` for staged or failed items.
3. Run `engine-cli -l <library> intake import-review <id> [--to-playlist <selector>] --json` and inspect the proposed destination.
4. Apply identical options with `intake import <id> --plan-token <token>`. A stale/changed source or stage requires a fresh observation/review, not a forced token.

`intake_replacing` means another owner is replacing the library; close competing API/queue owners before retrying. `intake_paused` requires inspecting control state and explicit resume. `intake_import_recovery_required` or `intake_recovery_required` requires reconciling retained History/storage rather than re-enqueuing to bypass receipts. `intake_source_changed`, `intake_staged_changed` and `intake_state_changed` refuse stale observations. Inspect item status and stable codes; raw diagnostics are not a recovery instruction.
