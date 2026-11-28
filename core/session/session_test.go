package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/aether-vpn/aether/core/carrier"
	acrypto "github.com/aether-vpn/aether/core/crypto"
	"github.com/aether-vpn/aether/core/proto"
)

// startMockServer runs an in-memory Aether WebSocket server that echoes TCP streams and UDP packets.
func startMockServer(t *testing.T, psk string) (*httptest.Server, func()) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	var mu sync.Mutex
	sessions := make(map[[16]byte]*Session)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := carrier.WrapConn(conn, carrier.Endpoint{Name: "mock"})
		helloBytes, err := c.ReadPacket()
		if err != nil {
			_ = c.Close()
			return
		}
		ch, err := carrier.DecodeClientHello(helloBytes, []byte(psk))
		if err != nil {
			_ = c.Close()
			return
		}

		if ch.Mode == carrier.ModeAttachCarrier {
			mu.Lock()
			sess, ok := sessions[ch.SessionID]
			mu.Unlock()
			if !ok {
				_ = c.WritePacket(carrier.EncodeServerHello(carrier.ServerHello{
					Version: proto.Version, Status: carrier.StatusUnknownSession,
				}, ch.SessionID, []byte(psk)))
				_ = c.Close()
				return
			}
			_ = c.WritePacket(carrier.EncodeServerHello(carrier.ServerHello{
				Version: proto.Version, Status: carrier.StatusOK,
			}, ch.SessionID, []byte(psk)))
			sess.AttachCarrier(c, nil)
			return
		}

		kp, _ := acrypto.GenerateKeyPair()
		cipher, _ := acrypto.DeriveSessionCipher([]byte(psk), kp.Private, ch.PubKey, ch.SessionID, true)
		_ = c.WritePacket(carrier.EncodeServerHello(carrier.ServerHello{
			Version: proto.Version, Status: carrier.StatusOK, PubKey: kp.Public,
		}, ch.SessionID, []byte(psk)))

		var sess *Session
		sess = New(ch.SessionID, cipher, true,
			func(ctx context.Context, st *Stream, addr proto.Address) error {
				sess.ConfirmStreamOpen(st)
				_, _ = io.Copy(st, st) // Echo server
				_ = st.CloseWrite()
				return nil
			},
			func(flowID uint32, addr proto.Address, payload []byte) {
				_ = sess.SendUDP(flowID, addr.Host, addr.Port, payload) // Echo UDP
			},
		)
		mu.Lock()
		sessions[ch.SessionID] = sess
		mu.Unlock()
		sess.AttachCarrier(c, nil)
	}))

	killAll := func() {
		mu.Lock()
		for id, s := range sessions {
			_ = s.Close()
			delete(sessions, id)
		}
		mu.Unlock()
		srv.CloseClientConnections()
		srv.Close()
	}
	return srv, killAll
}

func TestMultiCarrierStreamAndUDPEcho(t *testing.T) {
	psk := "test-psk-12345"
	srv, killPrimary := startMockServer(t, psk)
	defer killPrimary()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	udpCh := make(chan []byte, 1)
	mgr, err := NewClientManager(ClientConfig{
		PSK:      psk,
		Carriers: 3,
		Endpoints: []carrier.Endpoint{
			{Name: "primary", URL: wsURL, Priority: 0},
		},
	}, func(flowID uint32, addr proto.Address, payload []byte) {
		udpCh <- append([]byte(nil), payload...)
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer mgr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait briefly for all 3 parallel carriers to attach
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && mgr.Stats().ActiveCarriers < 3 {
		time.Sleep(25 * time.Millisecond)
	}
	if got := mgr.Stats().ActiveCarriers; got != 3 {
		t.Fatalf("expected 3 parallel carriers, got %d", got)
	}

	// Test 256 KB TCP stream striped across 3 parallel carriers
	st, err := mgr.OpenStream(ctx, "example.com", 443)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	payload := make([]byte, 256*1024)
	_, _ = rand.Read(payload)

	go func() {
		_, _ = st.Write(payload)
		_ = st.CloseWrite()
	}()
	echoed, err := io.ReadAll(st)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if !bytes.Equal(echoed, payload) {
		t.Fatalf("stream data corrupted: got %d bytes, want %d bytes", len(echoed), len(payload))
	}

	// Test full-cone UDP echo
	if err := mgr.SendUDP(7, "1.1.1.1", 53, []byte("dns-query-bytes")); err != nil {
		t.Fatalf("send udp: %v", err)
	}
	select {
	case got := <-udpCh:
		if string(got) != "dns-query-bytes" {
			t.Fatalf("udp mismatch: got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for UDP echo")
	}
}

func TestAutomaticFailoverPrimaryToFallback(t *testing.T) {
	psk := "failover-psk-999"
	primarySrv, killPrimary := startMockServer(t, psk)
	fallbackSrv, killFallback := startMockServer(t, psk)
	defer killFallback()

	mgr, err := NewClientManager(ClientConfig{
		PSK:      psk,
		Carriers: 2,
		Endpoints: []carrier.Endpoint{
			{Name: "railway-primary", URL: "ws" + strings.TrimPrefix(primarySrv.URL, "http"), Priority: 0},
			{Name: "render-fallback", URL: "ws" + strings.TrimPrefix(fallbackSrv.URL, "http"), Priority: 1},
		},
	}, nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer mgr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if ep := mgr.Stats().Endpoint; ep != "railway-primary" {
		t.Fatalf("expected railway-primary, got %s", ep)
	}

	// Simulate Railway going down completely (including hijacked WebSocket connections)
	killPrimary()

	// Wait for automatic failover to Render fallback
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if mgr.Stats().Endpoint == "render-fallback" && mgr.Stats().ActiveCarriers > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if ep := mgr.Stats().Endpoint; ep != "render-fallback" {
		t.Fatalf("expected automatic failover to render-fallback, got %s", ep)
	}

	// Verify traffic flows cleanly on the Render fallback
	st, err := mgr.OpenStream(ctx, "example.com", 80)
	if err != nil {
		t.Fatalf("open stream after failover: %v", err)
	}
	_, _ = st.Write([]byte("after-failover"))
	_ = st.CloseWrite()
	out, err := io.ReadAll(st)
	if err != nil || string(out) != "after-failover" {
		t.Fatalf("failover stream failed: out=%q err=%v", out, err)
	}
}

func TestOrderedStripingOutOfOrderAnd4MB(t *testing.T) {
	// 1. Direct out-of-order reassembly + FIN arriving BEFORE missing chunks
	s := New(NewSessionID(), nil, false, nil, nil)
	st := newStream(s, 1, nil, true)
	st.deliverEOF(4)               // FIN arrives early!
	st.deliverData(3, []byte("CC")) // Seq 3 arrives before 1 & 2
	st.deliverData(1, []byte("AA"))
	st.deliverData(2, []byte("BB"))
	got, err := io.ReadAll(st)
	if err != nil {
		t.Fatalf("out-of-order ReadAll failed: %v", err)
	}
	if string(got) != "AABBCC" {
		t.Fatalf("out-of-order mismatch: got %q, want %q", got, "AABBCC")
	}

	// 2. Full 4 MB striped echo over 4 live parallel WebSocket carriers
	psk := "stripe-4mb-psk"
	srv, kill := startMockServer(t, psk)
	defer kill()
	mgr, err := NewClientManager(ClientConfig{
		PSK:      psk,
		Carriers: 4,
		Endpoints: []carrier.Endpoint{
			{Name: "primary", URL: "ws" + strings.TrimPrefix(srv.URL, "http"), Priority: 0},
		},
	}, nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer mgr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && mgr.Stats().ActiveCarriers < 4 {
		time.Sleep(25 * time.Millisecond)
	}
	stream, err := mgr.OpenStream(ctx, "speed.test", 443)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	payload := make([]byte, 4*1024*1024)
	_, _ = rand.Read(payload)
	go func() {
		_, _ = stream.Write(payload)
		_ = stream.CloseWrite()
	}()
	echoed, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("4MB striped read failed: %v", err)
	}
	if !bytes.Equal(echoed, payload) {
		t.Fatalf("4MB striped data mismatch: got %d bytes, want %d bytes", len(echoed), len(payload))
	}
}
