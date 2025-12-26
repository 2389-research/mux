# ABOUTME: Build automation for mux library and examples.
# ABOUTME: Run 'make help' to see available targets.

.PHONY: all build examples test lint clean help \
	examples/simple examples/minimal examples/full

# Default target
all: build

# Build all packages
build:
	go build ./...

# Build all examples
examples: examples/simple examples/minimal examples/full

examples/simple:
	go build -o bin/simple ./examples/simple

examples/minimal:
	go build -o bin/minimal ./examples/minimal

examples/full:
	go build -o bin/full ./examples/full

# Run tests
test:
	go test -race -short ./...

# Run tests with coverage
test-cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Run linter
lint:
	golangci-lint run --timeout=2m

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f coverage.out coverage.html

# Show help
help:
	@echo "Available targets:"
	@echo "  all            - Build all packages (default)"
	@echo "  build          - Build all packages"
	@echo "  examples       - Build all examples to bin/"
	@echo "  examples/simple   - Build simple example"
	@echo "  examples/minimal  - Build minimal example"
	@echo "  examples/full     - Build full example"
	@echo "  test           - Run tests with race detector"
	@echo "  test-cover     - Run tests with coverage report"
	@echo "  lint           - Run golangci-lint"
	@echo "  clean          - Remove build artifacts"
