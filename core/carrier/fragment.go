package carrier

import (
	"crypto/rand"
	"net"
	"strings"
	"sync/atomic"
	"time"
)

// fragConn splits ONLY the first Write (the TLS ClientHello record) into small randomized TCP
// segments with micro-jitter to defeat SNI-inspecting DPI firewalls on standard port 443,
// then passes all subsequent writes straight through with zero overhead.
type fragConn struct {
	net.Conn
	minSize int
	maxSize int
	minMs   int
	maxMs   int
	done    atomic.Bool
}

func wrapTLSFragment(conn net.Conn, mode string) net.Conn {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "balanced", "fast":
		return &fragConn{Conn: conn, minSize: 40, maxSize: 80, minMs: 1, maxMs: 3}
	case "strict", "stealth", "aggressive":
		return &fragConn{Conn: conn, minSize: 10, maxSize: 30, minMs: 2, maxMs: 6}
	default:
		return conn
	}
}

func randBetween(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	var b [2]byte
	_, _ = rand.Read(b[:])
	v := int(b[0])<<8 | int(b[1])
	return lo + (v % (hi - lo + 1))
}

func (f *fragConn) Write(p []byte) (int, error) {
	if f.done.Swap(true) || len(p) <= f.minSize {
		return f.Conn.Write(p)
	}
	total := 0
	for len(p) > 0 {
		sz := randBetween(f.minSize, f.maxSize)
		if sz > len(p) {
			sz = len(p)
		}
		n, err := f.Conn.Write(p[:sz])
		total += n
		if err != nil {
			return total, err
		}
		p = p[sz:]
		if len(p) > 0 {
			ms := randBetween(f.minMs, f.maxMs)
			if ms > 0 {
				time.Sleep(time.Duration(ms) * time.Millisecond)
			}
		}
	}
	return total, nil
}
