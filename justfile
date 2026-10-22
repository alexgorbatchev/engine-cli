set dotenv-load := false

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
