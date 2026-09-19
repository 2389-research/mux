// ABOUTME: Pins that a large tool-input integer survives a persistence
// ABOUTME: round-trip through the session store without rounding.
package session_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/session"
)

// bigID is one past float64's exact-integer range: a plain float64 decode
// rounds it down to ...000.
const bigID = "10000000000000001"

func TestFileStore_ToolInputLargeIntegerSurvivesRoundTrip(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	ctx := context.Background()

	snap := &orchestrator.Snapshot{
		SessionID: "session-big",
		Status:    orchestrator.StatusSuspended,
		Messages: []llm.Message{
			llm.NewUserMessage("look it up"),
			{Role: llm.RoleAssistant, Blocks: []llm.ContentBlock{
				{Type: llm.ContentTypeToolUse, ID: "tool_big", Name: "lookup",
					Input: map[string]any{"id": json.Number(bigID)}},
			}},
		},
		Suspension: &orchestrator.Suspension{
			Reason:  orchestrator.ReasonApprovalRequired,
			Pending: []orchestrator.PendingToolCall{{ID: "tool_big", Name: "lookup", NeedsApproval: true}},
		},
		Usage:     orchestrator.NewTokenUsage().Snapshot(),
		UpdatedAt: time.Unix(1750000000, 0).UTC(),
	}

	if err := store.Save(ctx, snap); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Load(ctx, "session-big")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Messages) != 2 || len(got.Messages[1].Blocks) != 1 {
		t.Fatalf("unexpected restored shape: %#v", got.Messages)
	}
	assertExactID(t, "FileStore", got.Messages[1].Blocks[0].Input)
}

// assertExactID reports the actual dynamic type and value when the stored
// integer did not survive, so a regression names the rounding rather than
// just failing a deep-equal.
func assertExactID(t *testing.T, label string, input map[string]any) {
	t.Helper()
	got, ok := input["id"]
	if !ok {
		t.Fatalf("%s: input has no id: %#v", label, input)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("%s: marshal id: %v", label, err)
	}
	if string(encoded) != bigID {
		t.Fatalf("%s: id = %s (%T), want %s", label, encoded, got, bigID)
	}
}
