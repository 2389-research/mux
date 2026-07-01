// ABOUTME: Tests for the file-backed session Store: round-trip, missing, list, delete.
package session_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/session"
)

func sampleSnapshot(id string) *orchestrator.Snapshot {
	usage := orchestrator.NewTokenUsage()
	usage.Add(llm.Usage{InputTokens: 10, OutputTokens: 20})
	return &orchestrator.Snapshot{
		SessionID: id,
		Status:    orchestrator.StatusSuspended,
		Messages:  []llm.Message{llm.NewUserMessage("hello")},
		Suspension: &orchestrator.Suspension{
			Reason:  orchestrator.ReasonApprovalRequired,
			Pending: []orchestrator.PendingToolCall{{ID: "t1", Name: "write", NeedsApproval: true}},
		},
		Usage:     usage.Snapshot(),
		Iteration: 2,
		UpdatedAt: time.Unix(1750000000, 0).UTC(),
	}
}

func TestFileStore_SaveLoadRoundTrip(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	ctx := context.Background()
	want := sampleSnapshot("session-aaa")
	if err := store.Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Load(ctx, "session-aaa")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.SessionID != want.SessionID || got.Status != want.Status || got.Iteration != want.Iteration {
		t.Errorf("scalar mismatch: got %+v", got)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, want.UpdatedAt)
	}
	if got.Usage.InputTokens != 10 || got.Usage.OutputTokens != 20 {
		t.Errorf("Usage input=%d output=%d, want input=10 output=20", got.Usage.InputTokens, got.Usage.OutputTokens)
	}
	if got.Suspension == nil || got.Suspension.Reason != orchestrator.ReasonApprovalRequired {
		t.Errorf("Suspension = %+v", got.Suspension)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "hello" {
		t.Errorf("Messages = %+v", got.Messages)
	}
}

func TestFileStore_LoadMissing(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	_, err := store.Load(context.Background(), "session-nope")
	if !errors.Is(err, orchestrator.ErrSessionNotFound) {
		t.Fatalf("Load missing: err = %v, want ErrSessionNotFound", err)
	}
}

func TestFileStore_ListSorted(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	ctx := context.Background()
	for _, id := range []string{"session-c", "session-a", "session-b"} {
		if err := store.Save(ctx, sampleSnapshot(id)); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"session-a", "session-b", "session-c"}
	if len(ids) != len(want) {
		t.Fatalf("List = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("List = %v, want %v", ids, want)
		}
	}
}

func TestFileStore_DeleteIdempotent(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	ctx := context.Background()
	if err := store.Save(ctx, sampleSnapshot("session-x")); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "session-x"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Load(ctx, "session-x"); !errors.Is(err, orchestrator.ErrSessionNotFound) {
		t.Errorf("after Delete, Load err = %v, want ErrSessionNotFound", err)
	}
	// Second delete is a no-op, not an error.
	if err := store.Delete(ctx, "session-x"); err != nil {
		t.Errorf("second Delete: %v, want nil", err)
	}
}

func TestFileStore_ListEmptyDir(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	ids, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List on empty dir: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("List = %v, want empty", ids)
	}
}

func TestFileStore_SaveCleansTempOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	store := session.NewFileStore(dir)
	const sid = "session-collide"
	// Make the target path a directory so os.Rename(tmp, target) fails.
	target := filepath.Join(dir, sid+".json")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("Mkdir target: %v", err)
	}
	if err := store.Save(context.Background(), sampleSnapshot(sid)); err == nil {
		t.Fatal("Save onto a directory target = nil error, want error")
	}
	// The temp file must not be left behind after the failed rename.
	if _, statErr := os.Stat(target + ".tmp"); !os.IsNotExist(statErr) {
		t.Errorf("temp file %q not cleaned up after rename failure (stat err = %v)", target+".tmp", statErr)
	}
}
