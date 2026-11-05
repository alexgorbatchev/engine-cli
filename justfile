set dotenv-load := false

export ENGINE_DEV_HOST_SUFFIX := env("ENGINE_DEV_HOST_SUFFIX", ".localhost")

# Default recipe: list available recipes
default:
    @just --list

# Run CLI in human mode (default)
run *args:
    go run ./cmd/engine-cli {{ args }}

# Run CLI in agent-facing mode
run-ai *args:
    AGENT=1 go run ./cmd/engine-cli {{ args }}

# Run host tests with native PCM and report aggregate coverage; retain a new collection
[positional-arguments]
test output="":
    go run ./scripts/coverage collect --host-check --output "$1"

# Require matching Linux, macOS and Windows collections: >=90% per package and aggregate
[positional-arguments]
coverage-check +directories:
    go run ./scripts/coverage check "$@"

# Build binary into bin/
build:
    mkdir -p bin
    go build -o bin/engine-cli ./cmd/engine-cli

# Maintainer-only native macOS synthetic Desktop acceptance bundle
desktop-repair-handoff-test:
    bun test scripts/desktop-repair/process.test.mjs

desktop-repair-handoff:
    bun scripts/qualify-desktop-repair-handoff.mjs

# Install engine-cli binary to GOPATH bin directory
install:
    go install ./cmd/engine-cli

# Run static analysis and vet
lint:
    go mod tidy -diff
    go vet ./...

# Alias for lint
vet: lint

# Format Go code
fmt:
    go fmt ./...

# Fail when a tracked or new Go file is not gofmt-formatted
fmt-check:
    sh scripts/check-gofmt.sh

# Check the host; optional collection directories additionally require full native certification
[positional-arguments]
check *directories: fmt-check lint test
    @if [ "$#" -gt 0 ]; then go run ./scripts/coverage check "$@"; fi

# Clean build outputs while preserving session-owned scratch
clean:
    rm -rf bin coverage.out dist

# Download project dependencies
setup:
    go mod download

# Regenerate architecture diagrams with Archify 3.2.1-dev.4
architecture:
    #!/bin/sh
    set -eu
    : "${ARCHIFY_CLI:?Set ARCHIFY_CLI to Archify 3.2.1-dev.4 bin/archify.mjs}"
    mkdir -p .tmp/architecture-review
    review_dir=$(mktemp -d .tmp/architecture-review/run.XXXXXX)
    for candidate in docs/internal/architecture/*.json; do
        case "$candidate" in *.delivery*.json) continue ;; esac
        kind=$(node -e 'console.log(JSON.parse(require("node:fs").readFileSync(process.argv[1], "utf8")).diagram_type)' "$candidate")
        name=$(basename "$candidate" .json)
        node "$ARCHIFY_CLI" finalize "$kind" "$candidate" "${candidate%.json}.html" --repo-root . --quality showcase --out-dir "$review_dir/$name" --json
    done

# Serve the architecture index and diagrams over local HTTPS (Ctrl-C stops serving)
architecture-serve:
    devhost serve --dir docs/internal/architecture --domain "engine-cli-archify${ENGINE_DEV_HOST_SUFFIX}"
