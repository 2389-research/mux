// ABOUTME: Defines durable-session value types, the Store persistence interface,
// ABOUTME: and the suspend/resume vocabulary shared by the loop and its callers.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/2389-research/mux/llm"
)

// Status is the lifecycle state of a persisted session snapshot.
type Status string

const (
	StatusRunning   Status = "running"
	StatusSuspended Status = "suspended"
	StatusComplete  Status = "complete"
)

// Reason explains why a session suspended. Values mirror the eve.dev vocabulary.
type Reason string

const (
	// ReasonApprovalRequired: the loop paused because a tool needs human approval.
	ReasonApprovalRequired Reason = "authorization.required"
	// ReasonInputRequired is reserved for a future gene (the loop does not yet produce it).
	ReasonInputRequired Reason = "input.requested"
)

// PendingToolCall is a caller-facing projection of one tool call from the
// suspending assistant turn. It is informational: re-execution on Resume reads
// the authoritative tool_use blocks back out of the persisted messages, not this.
type PendingToolCall struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Params        map[string]any `json:"params,omitempty"`
	NeedsApproval bool           `json:"needs_approval"`
}

// Suspension describes why and on what the loop paused.
type Suspension struct {
	Reason  Reason            `json:"reason"`
	Pending []PendingToolCall `json:"pending,omitempty"`
}

// Snapshot is the complete persisted state of a session at a checkpoint.
type Snapshot struct {
	SessionID  string        `json:"session_id"`
	Status     Status        `json:"status"`
	Messages   []llm.Message `json:"messages"`
	Suspension *Suspension   `json:"suspension,omitempty"`
	Usage      TokenUsage    `json:"usage"`
	Iteration  int           `json:"iteration"`
	UpdatedAt  time.Time     `json:"updated_at"`
}

// Store persists and retrieves session snapshots. Implementations must be safe
// for use by one orchestrator at a time per session ID.
type Store interface {
	Save(ctx context.Context, snap *Snapshot) error
	Load(ctx context.Context, sessionID string) (*Snapshot, error)
	List(ctx context.Context) ([]string, error)
	Delete(ctx context.Context, sessionID string) error
}

// ErrSessionNotFound is returned by Store.Load when no snapshot exists.
var ErrSessionNotFound = errors.New("session not found")

// ApprovalMode selects how the loop handles a tool that requires approval.
type ApprovalMode int

const (
	// ApprovalSync calls the executor's approval func inline (the default, pre-existing behavior).
	ApprovalSync ApprovalMode = iota
	// ApprovalSuspend checkpoints and returns *Suspended instead of calling the approval func.
	ApprovalSuspend
)

// Suspended is returned by Run/Continue/Resume when the loop pauses awaiting a
// decision. Callers detect it with errors.As(err, &target).
type Suspended struct {
	SessionID  string
	Suspension Suspension
}

func (s *Suspended) Error() string {
	return fmt.Sprintf("orchestrator: session %s suspended (%s)", s.SessionID, s.Suspension.Reason)
}

// Decision carries the caller's approval choices into Resume. Approvals is keyed
// by PendingToolCall.ID; DefaultApprove is the fallback for IDs not present.
type Decision struct {
	Approvals      map[string]bool
	DefaultApprove bool
}

// Approve returns a Decision that approves (all=true) or denies (all=false) every
// pending tool call by default.
func Approve(all bool) Decision { return Decision{DefaultApprove: all} }

func (d Decision) approves(id string) bool {
	if v, ok := d.Approvals[id]; ok {
		return v
	}
	return d.DefaultApprove
}
