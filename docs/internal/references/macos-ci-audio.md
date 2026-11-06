---
created_on: 2026-11-06 04:17
last_modified: 2026-11-06 04:17
status: current
---

# Native macOS CI audio tools

The shared [audio setup action](../../../.github/actions/setup-audio/action.yml) installs a pinned native ARM64 FFmpeg/FFprobe pair for CI and release coverage. This reference is for maintainers updating that distribution; local developer installation and the Desktop fixture workflow are separate.

[macos.json](../../../scripts/audio-setup/macos.json) pins Martin Riedl's build, both HTTPS URLs and SHA-256 digests, the reported version, and the Apple signing team. The [setup helper](../../../scripts/audio-setup/macos.sh) keys its archive cache by the provider, build, architecture, and the complete manifest and helper bytes. An exact hit avoids downloads. Every run checks both archive digests before extracting, verifies regular executable entries, native architecture, signature and signing team, system-only direct dylib dependencies, version, and required FFmpeg capabilities. Only a successful pair is added to PATH. Unsupported architectures and failed verification stop setup; there is no Homebrew fallback.

The cache holds only the ZIP pair. Executables are extracted into a fresh runner temporary directory, removed on setup failure and retained for the rest of a successful job. GitHub's cache action saves only after a successful job. The provider describes ZIP binaries as signed and its separate installers as notarized; ZIP setup does not claim installer notarization or Gatekeeper acceptance.

## Update and qualify a pin

1. Select a fixed native ARM64 build from the [provider](https://ffmpeg.martin-riedl.de/). Download both archives, compare their digests with publisher metadata, and inspect their entries and executable permissions. Update the manifest together with the independently inspected signing identity; do not use a mutable latest URL.
2. Run `go test -count=1 ./scripts/audio-setup` and `just check`. The setup tests exercise isolated archive and command fixtures offline. They verify setup control flow, not native code-signing trust or real codec behavior.
3. Run the PR's existing Linux, macOS, and Windows coverage workflow. Retain the native macOS signature output, dylib list, executable hashes, versions, cache key, runner image version, and step timings. Require the unchanged audio qualification tests and source-bound coverage gate to pass.
4. After the successful cold job saves the new cache, rerun the same PR source to measure an exact warm hit. Confirm that it downloads neither archive and performs no Homebrew installation. Compare cold and warm setup and total job times with a baseline on the same runner image; repeat when image drift prevents comparison. Do not claim a speed improvement from cache presence alone.

Apple's [code-signing requirements](https://developer.apple.com/library/archive/technotes/tn2206/_index.html) describe signature and identity checks. Native validation remains necessary even when an offline inspection can read the embedded certificate and timestamp.
