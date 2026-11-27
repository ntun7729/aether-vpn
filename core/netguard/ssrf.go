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

// Guard protects the exit server from SSRF and DNS rebinding attacks.
type Guard struct {
	AllowPrivate bool
	Resolver     *net.Resolver
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
	// Block CGNAT 100.64.0.0/10 (often used by cloud metadata/internal networks)
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

// ResolveAndValidate resolves host and ensures no resolved IP is private/blocked (unless AllowPrivate is true).
func (g *Guard) ResolveAndValidate(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !g.AllowPrivate && IsBlockedIP(ip) {
			return nil, fmt.Errorf("%w: %s", ErrBlockedAddress, host)
		}
		return ip, nil
	}

	resolver := g.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no IP addresses found for %s", host)
	}

	// Prefer IPv4 first, validate every returned IP against SSRF.
	var chosen net.IP
	for _, ipa := range ips {
		if !g.AllowPrivate && IsBlockedIP(ipa.IP) {
			return nil, fmt.Errorf("%w: %s -> %s", ErrBlockedAddress, host, ipa.IP.String())
		}
		if chosen == nil || (chosen.To4() == nil && ipa.IP.To4() != nil) {
			chosen = ipa.IP
		}
	}
	return chosen, nil
}

// DialTCP resolves, validates against SSRF, and connects directly to the validated IP (preventing DNS rebinding).
func (g *Guard) DialTCP(ctx context.Context, host string, port uint16, timeout time.Duration) (net.Conn, error) {
	ip, err := g.ResolveAndValidate(ctx, host)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	return d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(int(port))))
}
