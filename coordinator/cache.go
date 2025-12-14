// ABOUTME: Thread-safe cache with TTL expiration.
// ABOUTME: Stores expensive operation results to avoid recomputation.
package coordinator

import (
	"sync"
	"time"
)

// Cache provides thread-safe caching with TTL expiration.
type Cache struct {
	items map[string]*CachedItem
	mu    sync.RWMutex
}

// CachedItem stores a value with expiration time.
type CachedItem struct {
	Value     any
	ExpiresAt time.Time
}

// NewCache creates a new cache.
func NewCache() *Cache {
	return &Cache{
		items: make(map[string]*CachedItem),
	}
}

// Get retrieves a value if it exists and hasn't expired.
func (c *Cache) Get(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, exists := c.items[key]
	if !exists {
		return nil, false
	}

	if time.Now().After(item.ExpiresAt) {
		return nil, false
	}

	return item.Value, true
}

// Set stores a value with the given TTL.
func (c *Cache) Set(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items[key] = &CachedItem{
		Value:     value,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// Delete removes a cache entry.
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.items, key)
}

// Clear removes all cache entries.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*CachedItem)
}

// Cleanup removes expired entries. Call periodically.
func (c *Cache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, item := range c.items {
		if now.After(item.ExpiresAt) {
			delete(c.items, key)
		}
	}
}
