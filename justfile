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

# Run test suite and Git hook tests
test:
    go test -v ./...
    bash scripts/test-git-hooks.sh .githooks

# Build binary into bin/
build:
    mkdir -p bin
    go build -o bin/engine-cli ./cmd/engine-cli

# Install engine-cli binary to GOPATH bin directory
install:
    go install ./cmd/engine-cli

# Run static analysis and vet
lint:
    go vet ./...

# Alias for lint
vet: lint

# Format Go code
fmt:
    go fmt ./...

# Run static analysis, test suite, and Git hook tests in sequence
check:
    go vet ./...
    go test -v ./...
    bash scripts/test-git-hooks.sh .githooks

# Clean up build binaries and temporary files
clean:
    rm -rf bin coverage.out .tmp dist

# Download project dependencies and install the publishing Git hooks
setup:
    go mod download
    git config core.hooksPath .githooks

# Regenerate architecture diagrams with Archify 3.2.0
architecture:
    #!/bin/sh
    set -eu
    : "${ARCHIFY_CLI:?Set ARCHIFY_CLI to Archify 3.2.0 bin/archify.mjs}"
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
