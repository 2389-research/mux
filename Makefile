# ABOUTME: Build automation for mux library and examples.
# ABOUTME: Run 'make help' to see available targets.

.PHONY: all build examples test test-cover test-live check-live lint vulncheck clean help \
	examples/simple examples/minimal examples/full

# govulncheck version, pinned so the gate can't drift green silently.
# Renew deliberately: bump this, re-run `make vulncheck` locally, and
# review any new findings before committing the bump.
GOVULNCHECK_VERSION := v1.8.0

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
#
# This is the canonical local suite and it must match the CI job's flags: the
# root suite carries the integration build tag, so a plain `go test ./...` would
# silently skip every assembled case. -short is deliberately omitted to keep
# local and CI coverage identical; run `go test -race -short ./...` directly for
# a quicker loop.
test:
	go test -race -tags=integration -timeout=300s ./...

# Run tests with coverage
test-cover:
	go test -race -tags=integration -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Live provider end-to-end checks. Opt-in: they call a real provider and cost
# money, so they are never part of `make test` or CI. Configure the inputs and
# run `make check-live`, which fails loudly when the contract is incomplete
# rather than letting the tests skip and look green.
test-live:
	go test -race -tags=live -timeout=300s ./...

# Preflight for the live suite: validate the provider, model and matching API
# key are all present, then run the checks. Names are printed; values never are.
check-live:
	@provider="$${MUX_LIVE_PROVIDER:-}"; model="$${MUX_LIVE_MODEL:-}"; \
	if [ -z "$$provider" ] || [ -z "$$model" ]; then \
	  echo "check-live: missing MUX_LIVE_PROVIDER or MUX_LIVE_MODEL"; \
	  echo "  MUX_LIVE_PROVIDER  one of: anthropic, openai, gemini, openrouter"; \
	  echo "  MUX_LIVE_MODEL     the model id to test"; \
	  echo "  API key            ANTHROPIC_API_KEY, OPENAI_API_KEY, GEMINI_API_KEY or OPENROUTER_API_KEY"; \
	  exit 1; \
	fi; \
	case "$$provider" in \
	  anthropic)  key="$${ANTHROPIC_API_KEY:-}";  keyname=ANTHROPIC_API_KEY;; \
	  openai)     key="$${OPENAI_API_KEY:-}";     keyname=OPENAI_API_KEY;; \
	  gemini)     key="$${GEMINI_API_KEY:-}";     keyname=GEMINI_API_KEY;; \
	  openrouter) key="$${OPENROUTER_API_KEY:-}"; keyname=OPENROUTER_API_KEY;; \
	  *) echo "check-live: unknown MUX_LIVE_PROVIDER: $$provider"; exit 1;; \
	esac; \
	if [ -z "$$key" ]; then echo "check-live: $$keyname is not set"; exit 1; fi; \
	echo "check-live: running live provider checks for $$provider/$$model"; \
	go test -race -tags=live -timeout=300s ./...

# Run linter
lint:
	golangci-lint run --timeout=2m

# Check the dependency graph for known vulnerabilities
vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

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
	@echo "  test           - Run the canonical suite (race, integration tag)"
	@echo "  test-cover     - Run tests with coverage report"
	@echo "  test-live      - Run opt-in live provider checks (needs credentials)"
	@echo "  check-live     - Validate live provider inputs, then run live checks"
	@echo "  lint           - Run golangci-lint"
	@echo "  vulncheck      - Run govulncheck against the dependency graph"
	@echo "  clean          - Remove build artifacts"
