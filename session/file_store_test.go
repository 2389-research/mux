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

func TestFileStore_SaveStampsVersion(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	ctx := context.Background()
	want := sampleSnapshot("session-ver")
	if want.Version != 0 {
		t.Fatalf("fixture Version = %d, want a fresh zero value", want.Version)
	}
	if err := store.Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if want.Version != 0 {
		t.Errorf("Save mutated the caller's snapshot: Version = %d, want 0", want.Version)
	}
	got, err := store.Load(ctx, "session-ver")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != orchestrator.SnapshotVersion {
		t.Errorf("loaded Version = %d, want %d", got.Version, orchestrator.SnapshotVersion)
	}
}

func TestFileStore_SaveRejectsInvalidSnapshot(t *testing.T) {
	dir := t.TempDir()
	store := session.NewFileStore(dir)
	ctx := context.Background()

	// An already-valid file at the target path: a rejected Save must leave it alone.
	const sid = "session-keep"
	if err := store.Save(ctx, sampleSnapshot(sid)); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(dir, sid+".json"))
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}

	bad := sampleSnapshot(sid)
	bad.Status = orchestrator.Status("paused")
	if err := store.Save(ctx, bad); err == nil {
		t.Fatal("Save of an invalid snapshot = nil error, want error")
	}
	after, err := os.ReadFile(filepath.Join(dir, sid+".json"))
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Error("a rejected Save overwrote the previously stored snapshot")
	}
}

func TestFileStore_SaveRejectsNilSnapshot(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	if err := store.Save(context.Background(), nil); err == nil {
		t.Fatal("Save(nil) = nil error, want error")
	}
}

// TestFileStore_LoadRejectsInvalidSnapshot writes the exact corrupt shapes a
// hand-edited file, migration, or another producer could leave on disk and
// requires Load to reject each one rather than hand Resume a state it would act on.
func TestFileStore_LoadRejectsInvalidSnapshot(t *testing.T) {
	const sid = "session-bad"
	cases := []struct {
		name string
		json string
	}{
		{
			name: "suspended with no pending calls",
			json: `{"version":1,"session_id":"session-bad","status":"suspended","messages":[],` +
				`"suspension":{"reason":"authorization.required"},"usage":{},"iteration":0,"updated_at":"2025-06-15T00:00:00Z"}`,
		},
		{
			name: "running with a suspension",
			json: `{"version":1,"session_id":"session-bad","status":"running","messages":[],` +
				`"suspension":{"reason":"authorization.required","pending":[{"id":"c","name":"read"}]},"usage":{},"updated_at":"2025-06-15T00:00:00Z"}`,
		},
		{
			name: "complete with a suspension",
			json: `{"version":1,"session_id":"session-bad","status":"complete","messages":[],` +
				`"suspension":{"reason":"authorization.required","pending":[{"id":"c","name":"read"}]},"usage":{},"updated_at":"2025-06-15T00:00:00Z"}`,
		},
		{
			name: "suspended with empty pending array",
			json: `{"version":1,"session_id":"session-bad","status":"suspended","messages":[],` +
				`"suspension":{"reason":"authorization.required","pending":[]},"usage":{},"updated_at":"2025-06-15T00:00:00Z"}`,
		},
		{
			name: "unknown status",
			json: `{"version":1,"session_id":"session-bad","status":"paused","messages":[],"usage":{},"updated_at":"2025-06-15T00:00:00Z"}`,
		},
		{
			name: "older schema version",
			json: `{"version":0,"session_id":"session-bad","status":"running","messages":[],"usage":{},"updated_at":"2025-06-15T00:00:00Z"}`,
		},
		{
			name: "newer schema version",
			json: `{"version":999,"session_id":"session-bad","status":"running","messages":[],"usage":{},"updated_at":"2025-06-15T00:00:00Z"}`,
		},
		{
			name: "missing schema version",
			json: `{"session_id":"session-bad","status":"running","messages":[],"usage":{},"updated_at":"2025-06-15T00:00:00Z"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store := session.NewFileStore(dir)
			if err := os.WriteFile(filepath.Join(dir, sid+".json"), []byte(tc.json), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			if _, err := store.Load(context.Background(), sid); err == nil {
				t.Fatal("Load of an invalid snapshot = nil error, want error")
			} else if errors.Is(err, orchestrator.ErrSessionNotFound) {
				t.Errorf("Load = %v, want a validation error rather than not-found", err)
			}
		})
	}
}

// TestFileStore_LoadRejectsSessionIDMismatch: a file named for one session but
// carrying another's ID would restore the wrong conversation, so Load refuses it.
func TestFileStore_LoadRejectsSessionIDMismatch(t *testing.T) {
	dir := t.TempDir()
	store := session.NewFileStore(dir)
	mismatch := `{"version":1,"session_id":"session-other","status":"running","messages":[],` +
		`"usage":{},"iteration":0,"updated_at":"2025-06-15T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "session-here.json"), []byte(mismatch), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := store.Load(context.Background(), "session-here"); err == nil {
		t.Fatal("Load of a mismatched session id = nil error, want error")
	}
}
