package service

import (
	"sync"

	tb "github.com/tigerbeetle/tigerbeetle-go"
)

// metadataCache is a bounded round-robin cache. TigerBeetle account metadata
// is immutable, so eviction only costs another lookup and cannot affect ledger
// correctness.
type metadataCache struct {
	mu      sync.RWMutex
	entries map[tb.Uint128]accountMetadata
	keys    []tb.Uint128
	next    int
	limit   int
}

func newMetadataCache(limit int) *metadataCache {
	if limit < 0 {
		limit = 0
	}
	return &metadataCache{
		entries: make(map[tb.Uint128]accountMetadata, min(limit, 1024)),
		keys:    make([]tb.Uint128, 0, min(limit, 1024)),
		limit:   limit,
	}
}

func (c *metadataCache) Load(id tb.Uint128) (accountMetadata, bool) {
	if c.limit == 0 {
		return accountMetadata{}, false
	}
	c.mu.RLock()
	metadata, ok := c.entries[id]
	c.mu.RUnlock()
	return metadata, ok
}

func (c *metadataCache) Store(id tb.Uint128, metadata accountMetadata) {
	if c.limit == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[id]; ok {
		c.entries[id] = metadata
		return
	}
	if len(c.entries) < c.limit {
		c.entries[id] = metadata
		c.keys = append(c.keys, id)
		return
	}
	evicted := c.keys[c.next]
	delete(c.entries, evicted)
	c.entries[id] = metadata
	c.keys[c.next] = id
	c.next = (c.next + 1) % c.limit
}

func (c *metadataCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
