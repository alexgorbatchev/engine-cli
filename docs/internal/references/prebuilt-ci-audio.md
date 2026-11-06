---
created_on: 2026-11-06 04:54
last_modified: 2026-11-06 05:26
status: current
---

# Linux and Windows CI audio tools

The shared [audio setup action](../../../.github/actions/setup-audio/action.yml) installs pinned BtbN FFmpeg/FFprobe builds for native Linux and Windows x64 CI and release coverage. This reference is for maintainers updating that distribution. Developer installation is separate; [macOS setup](macos-ci-audio.md) uses its own native signed distribution.

[prebuilt.json](../../../scripts/audio-setup/prebuilt.json) pins a month-end release, fixed HTTPS asset URLs, archive roots, SHA-256 digests, and the full reported version. [prebuilt.sh](../../../scripts/audio-setup/prebuilt.sh) selects the host platform and extracts only the two expected executable entries. It uses existing runner Bash, curl, jq, hash and archive tools; Windows paths are converted with Git Bash's `cygpath` before publication. Manifest reads use jq's binary output mode to preserve LF under native Windows jq. Archive verification hashes binary stdin so Windows path escaping cannot alter the parsed digest.

The cache contains the original archive, keyed by build, platform, architecture, and all manifest and helper bytes. An exact hit avoids downloading; it does not bypass integrity checks. Every run verifies the archive digest before extraction, requires both regular executable files, and verifies both versions and the required codecs, containers, filters, protocols, and `lavfi` input device before adding the directory to PATH. Failed downloads, incomplete caches, changed bytes, invalid executables, or failed validation stop setup. There is no latest-version discovery, mutable README parsing, or system-tool fallback.

Downloads use a temporary file and a bounded HTTPS request. Cancellation stops and waits for the owned downloader. Failed setup removes its download and extraction directory; successful executables remain in the runner temporary directory for subsequent job steps. GitHub's cache action saves the archive only after a successful job.

## Distribution lifetime and platform requirements

The selected [BtbN distribution documentation](https://github.com/BtbN/FFmpeg-Builds/blob/5031c6f1082414e53688d39e521849905071070d/README.md) retains the last build of each month for two years; ordinary daily builds have much shorter retention. The current October 2026 pin therefore requires replacement before its retention window expires. Release assets are not immutable, so the checked-in digests remain authoritative. Removed assets fail cold setup, and altered assets fail verification.

The documented platform floors are Linux x86_64 with glibc 2.28 and kernel 4.18, and Windows 10 22H2. These GPL builds include third-party codecs; the setup checks the specific capabilities used by Engine CLI. Native CI remains necessary to establish runtime behavior on the supported runner images.

## Update and qualify a pin

1. Select a retained month-end build from [BtbN releases](https://github.com/BtbN/FFmpeg-Builds/releases). Download both platform archives and compare their SHA-256 values with the publisher's checksum file and GitHub asset metadata. Inspect archive paths, regular executable entries, permissions, and reported versions before updating the manifest. Do not derive expected digests from unchecked downloads alone.
2. Run `go test -count=1 ./scripts/audio-setup` and `just check`. Offline fixtures exercise the actual helpers with real archives and controlled platform commands; Windows emulation on Linux does not establish native Windows execution. Shared-helper edits also require the macOS regressions.
3. Run the PR's existing Linux, macOS, and Windows coverage workflow and the Windows 2022 audio job. Retain archive and executable hashes, reported versions, capability output, source revision, runner image version, cache key, and setup timings. Require the existing native audio suites and source-bound coverage gate to pass without changing thresholds or selections.
4. After successful cold jobs save the caches, rerun the same source to qualify exact warm hits. Confirm no download occurs and integrity and capability checks still run. Compare setup and job timings only across matching runner images. Missing or corrupted cache contents must fail before path publication.
