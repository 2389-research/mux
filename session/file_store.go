// ABOUTME: File-backed implementation of orchestrator.Store that persists each
// ABOUTME: session snapshot as one JSON file, written atomically via temp+rename.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/2389-research/mux/orchestrator"
)

// FileStore persists session snapshots as JSON files under a directory.
type FileStore struct {
	dir string
}

var _ orchestrator.Store = (*FileStore)(nil)

// NewFileStore returns a FileStore rooted at dir. The directory is created on
// first Save if it does not exist.
func NewFileStore(dir string) *FileStore {
	return &FileStore{dir: dir}
}

// path returns the on-disk path for a session ID, rejecting IDs that could
// escape the store directory.
func (s *FileStore) path(sessionID string) (string, error) {
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) || strings.Contains(sessionID, "..") {
		return "", fmt.Errorf("session: invalid session id %q", sessionID)
	}
	return filepath.Join(s.dir, sessionID+".json"), nil
}

// Save writes snap atomically: marshal to a temp file in the same directory,
// then rename over the target so a crash never leaves a half-written snapshot.
func (s *FileStore) Save(_ context.Context, snap *orchestrator.Snapshot) error {
	candidate, err := snapshotForSave(snap)
	if err != nil {
		return err
	}
	target, err := s.path(candidate.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return err
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp) // best-effort: don't leave an orphan temp file behind
		return err
	}
	return nil
}

// snapshotForSave returns a validated shallow copy of snap with the current
// schema version. Copying field-by-field (rather than *snap) avoids duplicating
// TokenUsage's mutex; the caller's snapshot is never mutated. A freshly
// constructed Version-0 value is stamped here, but Load never guesses a version.
func snapshotForSave(snap *orchestrator.Snapshot) (*orchestrator.Snapshot, error) {
	if snap == nil {
		return nil, errors.New("session: cannot save a nil snapshot")
	}
	candidate := &orchestrator.Snapshot{
		Version:    snap.Version,
		SessionID:  snap.SessionID,
		Status:     snap.Status,
		Messages:   snap.Messages,
		Suspension: snap.Suspension,
		Usage:      snap.Usage.Snapshot(),
		Iteration:  snap.Iteration,
		UpdatedAt:  snap.UpdatedAt,
	}
	if candidate.Version == 0 {
		candidate.Version = orchestrator.SnapshotVersion
	}
	if err := candidate.Validate(); err != nil {
		return nil, err
	}
	return candidate, nil
}

// Load reads and decodes a snapshot, returning ErrSessionNotFound if absent.
func (s *FileStore) Load(_ context.Context, sessionID string) (*orchestrator.Snapshot, error) {
	target, err := s.path(sessionID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, orchestrator.ErrSessionNotFound
		}
		return nil, err
	}
	var snap orchestrator.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("session: decode %s: %w", sessionID, err)
	}
	// Reject an invariant-violating or foreign-schema file instead of handing
	// Resume state it would act on. Pre-existing bad files stay on disk for the
	// operator; nothing is silently repaired or migrated here.
	if err := snap.Validate(); err != nil {
		return nil, fmt.Errorf("session: load %s: %w", sessionID, err)
	}
	if snap.SessionID != sessionID {
		return nil, fmt.Errorf("session: load %s: snapshot names session %q", sessionID, snap.SessionID)
	}
	return &snap, nil
}

// List returns the session IDs with snapshots, sorted. A missing dir lists empty.
func (s *FileStore) List(_ context.Context) ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(ids)
	return ids, nil
}

// Delete removes a snapshot. Deleting a non-existent session is a no-op.
func (s *FileStore) Delete(_ context.Context, sessionID string) error {
	target, err := s.path(sessionID)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
