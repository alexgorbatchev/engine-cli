set dotenv-load := false

# Run CLI in human mode (default)
run *args:
    go run ./cmd/engine-cli {{args}}

# Run CLI in agent-facing mode
run-ai *args:
    AGENT=1 go run ./cmd/engine-cli {{args}}

# Run test suite
test:
    go test -v ./...

# Build binary into bin/
build:
    mkdir -p bin
    go build -o bin/engine-cli ./cmd/engine-cli

# Run static analysis and vet
lint:
    go vet ./...

# Alias for lint
vet: lint

# Format Go code
fmt:
    go fmt ./...

# Run static analysis and test suite in sequence
check:
    go vet ./...
    go test -v ./...

# Clean up build binaries and temporary files
clean:
    rm -rf bin coverage.out .tmp
