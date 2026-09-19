//go:build integration || live

// ABOUTME: Shared helpers for the root integration and live-provider suites -
// ABOUTME: builds the real echo MCP server subprocess those tests drive.

package mux_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// echoServerPackage is the real MCP stdio server the assembled tests launch as
// a child process. It lives under testdata, so it is a separate main package
// that `go build ./...`, `go vet ./...` and `golangci-lint` never treat as part
// of the library build.
const echoServerPackage = "github.com/2389-research/mux/mcp/testdata/echo_server"

// buildEchoServer compiles the echo MCP server into a temp dir and returns its
// path. Building at test time (rather than committing a binary) keeps the
// fixture reproducible on the runner's own toolchain and architecture.
func buildEchoServer(ctx context.Context, t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("go"); err != nil {
		t.Fatalf("integration test needs the go toolchain to build the echo MCP server: %v", err)
	}

	bin := filepath.Join(t.TempDir(), "echo_server")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, echoServerPackage)
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build echo MCP server: %v\n%s", err, out)
	}
	return bin
}

// randomNonce returns a fresh hex nonce so an assertion cannot pass on stale
// state from another run.
func randomNonce(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("generate nonce: %v", err)
	}
	return hex.EncodeToString(b)
}

// repoRoot returns the directory holding go.mod for this module, so the build
// does not depend on the test process's working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository root from test source")
	}
	return filepath.Dir(file)
}
