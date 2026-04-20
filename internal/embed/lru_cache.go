package embed

import (
	"container/list"
	"sync"
	"time"
)

type cacheEntry struct {
	key       string
	value     []float32
	expiresAt time.Time
}

// LRUCache is a thread-safe LRU cache with per-entry TTL.
// Capacity: 1 000 entries. TTL: 5 minutes (configurable for tests).
// Key must be the SHA-256 hex of the input text.
type LRUCache struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	list     *list.List
	items    map[string]*list.Element
}

// NewLRUCache returns an LRUCache with the given capacity and TTL.
func NewLRUCache(capacity int, ttl time.Duration) *LRUCache {
	return &LRUCache{
		capacity: capacity,
		ttl:      ttl,
		list:     list.New(),
		items:    make(map[string]*list.Element, capacity),
	}
}

// Get returns the cached embedding for key, or (nil, false) on miss/expiry.
func (c *LRUCache) Get(key string) ([]float32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return nil, false
	}

	entry := elem.Value.(*cacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.list.Remove(elem)
		delete(c.items, key)
		return nil, false
	}

	c.list.MoveToFront(elem)
	return entry.value, true
}

// Set stores an embedding under key, evicting the LRU entry if at capacity.
func (c *LRUCache) Set(key string, value []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.list.MoveToFront(elem)
		e := elem.Value.(*cacheEntry)
		e.value = value
		e.expiresAt = time.Now().Add(c.ttl)
		return
	}

	if c.list.Len() >= c.capacity {
		oldest := c.list.Back()
		if oldest != nil {
			c.list.Remove(oldest)
			delete(c.items, oldest.Value.(*cacheEntry).key)
		}
	}

	entry := &cacheEntry{
		key:       key,
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
	elem := c.list.PushFront(entry)
	c.items[key] = elem
}

// Len returns the current number of cache entries.
func (c *LRUCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.list.Len()
}
