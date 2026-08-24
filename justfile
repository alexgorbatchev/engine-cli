# engine-cli justfile

# Default recipe: list available recipes
default:
    @just --list

# Build the engine-cli binary in bin/
build:
	mkdir -p bin
	go build -o bin/engine-cli ./cmd/engine-cli

# Install engine-cli binary to GOPATH bin directory
install:
    go install ./cmd/engine-cli

# Run all unit tests with coverage
test:
    go test -v ./...

# Run static code analysis
vet:
    go vet ./...

# Format Go code
fmt:
    go fmt ./...

# Clean up build binaries and temporary files
clean:
	rm -rf bin coverage.out .tmp
