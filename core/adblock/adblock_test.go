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

func TestExtractTLSSNI(t *testing.T) {
	// Minimal valid TLS 1.2/1.3 ClientHello carrying SNI="ads.doubleclick.net"
	host := []byte("ads.doubleclick.net")
	sniExt := make([]byte, 4+2+1+2+len(host))
	sniExt[0], sniExt[1] = 0x00, 0x00 // ext type 0 (SNI)
	sniExt[2], sniExt[3] = 0x00, byte(5+len(host))
	sniExt[4], sniExt[5] = 0x00, byte(3+len(host))
	sniExt[6] = 0x00 // host_name
	sniExt[7], sniExt[8] = 0x00, byte(len(host))
	copy(sniExt[9:], host)

	body := make([]byte, 0, 128)
	body = append(body, 0x03, 0x03)              // ClientVersion TLS 1.2
	body = append(body, make([]byte, 32)...)     // Random
	body = append(body, 0x00)                    // SessionID len 0
	body = append(body, 0x00, 0x02, 0x13, 0x01)  // CipherSuites
	body = append(body, 0x01, 0x00)              // Compression
	body = append(body, 0x00, byte(len(sniExt))) // Extensions len
	body = append(body, sniExt...)

	pkt := make([]byte, 0, 5+4+len(body))
	pkt = append(pkt, 0x16, 0x03, 0x01, 0x00, byte(4+len(body))) // Record header
	pkt = append(pkt, 0x01, 0x00, 0x00, byte(len(body)))         // Handshake ClientHello
	pkt = append(pkt, body...)

	sni, ok := ExtractTLSSNI(pkt)
	if !ok || sni != "ads.doubleclick.net" {
		t.Fatalf("expected SNI ads.doubleclick.net, got %q ok=%v", sni, ok)
	}
}
