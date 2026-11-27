package dns

import (
	"context"
	"net"
	"sync"
	"time"
)

type cacheEntry struct {
	ips       []net.IP
	expiresAt time.Time
}

// Cache is a lightweight concurrency-safe DNS cache for the exit server.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

// NewCache creates a DNS cache with a default TTL.
func NewCache(ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &Cache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
	}
}

// LookupIP resolves host using the cache, falling back to net.DefaultResolver.
func (c *Cache) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}

	now := time.Now()
	c.mu.RLock()
	if ent, ok := c.entries[host]; ok && now.Before(ent.expiresAt) {
		c.mu.RUnlock()
		return ent.ips, nil
	}
	c.mu.RUnlock()

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}

	c.mu.Lock()
	if len(c.entries) > 4096 {
		// Simple bounded eviction
		for k, v := range c.entries {
			if now.After(v.expiresAt) {
				delete(c.entries, k)
			}
		}
	}
	c.entries[host] = cacheEntry{ips: ips, expiresAt: now.Add(c.ttl)}
	c.mu.Unlock()

	return ips, nil
}
