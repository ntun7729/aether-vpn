package dns

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
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

type udpDNSEntry struct {
	raw       []byte
	expiresAt time.Time
}

// UDPCache caches raw UDP DNS wire responses (including AdGuard's blocked responses)
// so repeat queries return in <0.1ms with rewritten 2-byte Transaction IDs.
type UDPCache struct {
	mu      sync.RWMutex
	entries map[string]udpDNSEntry
	ttl     time.Duration
}

func NewUDPCache(ttl time.Duration) *UDPCache {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &UDPCache{
		entries: make(map[string]udpDNSEntry),
		ttl:     ttl,
	}
}

// QueryKey extracts "qname|qtype" from a standard UDP DNS query packet.
func QueryKey(pkt []byte) (string, bool) {
	if len(pkt) < 17 {
		return "", false
	}
	// QR must be 0 (query) and QDCOUNT == 1
	if pkt[2]&0x80 != 0 || binary.BigEndian.Uint16(pkt[4:6]) != 1 {
		return "", false
	}
	pos := 12
	var parts []string
	for pos < len(pkt) {
		l := int(pkt[pos])
		if l == 0 {
			pos++
			break
		}
		if l&0xc0 != 0 || pos+1+l >= len(pkt) {
			return "", false
		}
		parts = append(parts, strings.ToLower(string(pkt[pos+1:pos+1+l])))
		pos += 1 + l
	}
	if len(parts) == 0 || pos+4 > len(pkt) {
		return "", false
	}
	qtype := binary.BigEndian.Uint16(pkt[pos : pos+2])
	return fmt.Sprintf("%s|%d", strings.Join(parts, "."), qtype), true
}

// IsAAAAQuery returns true if pkt is a standard DNS query for QTYPE=28 (AAAA / IPv6).
func IsAAAAQuery(pkt []byte) bool {
	key, ok := QueryKey(pkt)
	return ok && strings.HasSuffix(key, "|28")
}

// BuildEmptyNoError builds a minimal valid DNS NOERROR response with 0 answers.
// Used on IPv4-only container hosts (Railway/Render) so apps use IPv4 instead of blackholed IPv6 QUIC.
func BuildEmptyNoError(query []byte) []byte {
	if len(query) < 12 {
		return nil
	}
	resp := make([]byte, len(query))
	copy(resp, query)
	resp[2] = (query[2] & 0x01) | 0x81 // QR=1, RD preserved
	resp[3] = 0x80                     // RA=1, RCODE=0 (NOERROR)
	binary.BigEndian.PutUint16(resp[6:8], 0)
	binary.BigEndian.PutUint16(resp[8:10], 0)
	binary.BigEndian.PutUint16(resp[10:12], 0)
	return resp
}

// Get returns a cached DNS response with its Transaction ID rewritten to match query.
func (u *UDPCache) Get(query []byte) ([]byte, bool) {
	key, ok := QueryKey(query)
	if !ok {
		return nil, false
	}
	now := time.Now()
	u.mu.RLock()
	ent, found := u.entries[key]
	u.mu.RUnlock()
	if !found || now.After(ent.expiresAt) {
		return nil, false
	}
	out := make([]byte, len(ent.raw))
	copy(out, ent.raw)
	out[0] = query[0]
	out[1] = query[1]
	return out, true
}

// Put stores a raw DNS wire response under the query's key.
func (u *UDPCache) Put(query, resp []byte) {
	if len(resp) < 12 {
		return
	}
	key, ok := QueryKey(query)
	if !ok {
		return
	}
	buf := make([]byte, len(resp))
	copy(buf, resp)
	now := time.Now()
	u.mu.Lock()
	if len(u.entries) > 4096 {
		for k, v := range u.entries {
			if now.After(v.expiresAt) {
				delete(u.entries, k)
			}
		}
	}
	u.entries[key] = udpDNSEntry{raw: buf, expiresAt: now.Add(u.ttl)}
	u.mu.Unlock()
}
