package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"
)

var ErrBlockedAddress = errors.New("aether/netguard: destination address is blocked (private/loopback/metadata)")

// Guard protects the exit server from SSRF and DNS rebinding attacks,
// and supports multi-IP fast-fail dialing with TCP_NODELAY.
type Guard struct {
	AllowPrivate bool
	Resolver     *net.Resolver
	LookupFn     func(ctx context.Context, host string) ([]net.IP, error)
}

// IsBlockedIP returns true if the IP is loopback, private, link-local, multicast, or unspecified.
func IsBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	if addr.Is4() {
		b := addr.As4()
		if b[0] == 100 && b[1] >= 64 && b[1] <= 127 {
			return true
		}
		if b[0] == 0 {
			return true
		}
	}
	return false
}

// ResolveAllAndValidate resolves host, validates all IPs against SSRF, and returns IPv4-first order.
func (g *Guard) ResolveAllAndValidate(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !g.AllowPrivate && IsBlockedIP(ip) {
			return nil, fmt.Errorf("%w: %s", ErrBlockedAddress, host)
		}
		return []net.IP{ip}, nil
	}

	var rawIPs []net.IP
	if g.LookupFn != nil {
		var err error
		rawIPs, err = g.LookupFn(ctx, host)
		if err != nil {
			return nil, err
		}
	} else {
		resolver := g.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		ips, err := resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		rawIPs = make([]net.IP, 0, len(ips))
		for _, ipa := range ips {
			rawIPs = append(rawIPs, ipa.IP)
		}
	}
	if len(rawIPs) == 0 {
		return nil, fmt.Errorf("no IP addresses found for %s", host)
	}

	var v4, v6 []net.IP
	for _, ip := range rawIPs {
		if !g.AllowPrivate && IsBlockedIP(ip) {
			return nil, fmt.Errorf("%w: %s -> %s", ErrBlockedAddress, host, ip.String())
		}
		if ip.To4() != nil {
			v4 = append(v4, ip)
		} else {
			v6 = append(v6, ip)
		}
	}
	return append(v4, v6...), nil
}

// ResolveAndValidate resolves host and returns the preferred validated IP.
func (g *Guard) ResolveAndValidate(ctx context.Context, host string) (net.IP, error) {
	ips, err := g.ResolveAllAndValidate(ctx, host)
	if err != nil {
		return nil, err
	}
	return ips[0], nil
}

// DialTCP resolves, validates against SSRF, and fast-fails across up to 3 resolved IPs with TCP_NODELAY.
func (g *Guard) DialTCP(ctx context.Context, host string, port uint16, timeout time.Duration) (net.Conn, error) {
	ips, err := g.ResolveAllAndValidate(ctx, host)
	if err != nil {
		return nil, err
	}
	maxTries := len(ips)
	if maxTries > 3 {
		maxTries = 3
	}
	perTry := 4 * time.Second
	if timeout > 0 && timeout < perTry {
		perTry = timeout
	}
	portStr := strconv.Itoa(int(port))
	var lastErr error
	for i := 0; i < maxTries; i++ {
		d := &net.Dialer{Timeout: perTry, KeepAlive: 30 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ips[i].String(), portStr))
		if err == nil {
			if tc, ok := conn.(*net.TCPConn); ok {
				_ = tc.SetNoDelay(true)
			}
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
