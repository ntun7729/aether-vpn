package adblock

import "testing"

func TestAdblockMatchingAndDNSNXDomain(t *testing.T) {
	b := New(true, "custom-tracker.io")

	if !b.IsBlocked("pagead2.googlesyndication.com") {
		t.Fatal("expected googlesyndication subdomain to be blocked")
	}
	if !b.IsBlocked("sub.custom-tracker.io.") {
		t.Fatal("expected custom suffix to be blocked")
	}
	if b.IsBlocked("github.com") || b.IsBlocked("google.com") {
		t.Fatal("normal domains must NOT be blocked")
	}

	// Build a minimal DNS query for ads.doubleclick.net
	query := []byte{
		0xab, 0xcd, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		3, 'a', 'd', 's', 11, 'd', 'o', 'u', 'b', 'l', 'e', 'c', 'l', 'i', 'c', 'k', 3, 'n', 'e', 't', 0,
		0x00, 0x01, 0x00, 0x01,
	}
	name, ok := ExtractDNSQueryName(query)
	if !ok || name != "ads.doubleclick.net" {
		t.Fatalf("unexpected QNAME: %q ok=%v", name, ok)
	}
	if !b.IsBlocked(name) {
		t.Fatal("expected extracted DNS name to be blocked")
	}
	nx := BuildNXDomain(query)
	if len(nx) != len(query) || nx[0] != 0xab || nx[1] != 0xcd || (nx[3]&0x0f) != 3 {
		t.Fatalf("invalid NXDOMAIN packet: %x", nx)
	}
}
