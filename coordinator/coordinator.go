// ABOUTME: Resource coordinator for multi-agent systems providing resource locking.
// ABOUTME: Ensures thread-safe coordination across agent hierarchy.
package coordinator

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Coordinator manages shared resources across agents.
type Coordinator struct {
	locks   map[string]*ResourceLock
	locksMu sync.Mutex
}

// ResourceLock represents a lock on a shared resource.
type ResourceLock struct {
	ResourceID string
	OwnerID    string
	AcquiredAt time.Time
}

// New creates a new resource coordinator.
func New() *Coordinator {
	return &Coordinator{
		locks: make(map[string]*ResourceLock),
	}
}

// Acquire attempts to acquire a lock on a resource.
// Idempotent: if the agent already owns the lock, returns nil.
func (c *Coordinator) Acquire(ctx context.Context, agentID, resourceID string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled: %w", err)
	}

	c.locksMu.Lock()
	defer c.locksMu.Unlock()

	if lock, exists := c.locks[resourceID]; exists {
		if lock.OwnerID == agentID {
			return nil // Idempotent
		}
		return fmt.Errorf("resource %s is locked by agent %s", resourceID, lock.OwnerID)
	}

	c.locks[resourceID] = &ResourceLock{
		ResourceID: resourceID,
		OwnerID:    agentID,
		AcquiredAt: time.Now(),
	}
	return nil
}

// Release releases a lock on a resource.
func (c *Coordinator) Release(agentID, resourceID string) error {
	c.locksMu.Lock()
	defer c.locksMu.Unlock()

	lock, exists := c.locks[resourceID]
	if !exists {
		return nil // Idempotent
	}

	if lock.OwnerID != agentID {
		return fmt.Errorf("agent %s does not own lock on %s", agentID, resourceID)
	}

	delete(c.locks, resourceID)
	return nil
}

// ReleaseAll releases all locks held by the given agent.
func (c *Coordinator) ReleaseAll(agentID string) {
	c.locksMu.Lock()
	defer c.locksMu.Unlock()

	for resourceID, lock := range c.locks {
		if lock.OwnerID == agentID {
			delete(c.locks, resourceID)
		}
	}
}
