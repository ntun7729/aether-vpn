package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/aether-vpn/aether/core/adblock"
	"github.com/aether-vpn/aether/core/carrier"
	acrypto "github.com/aether-vpn/aether/core/crypto"
	adns "github.com/aether-vpn/aether/core/dns"
	"github.com/aether-vpn/aether/core/netguard"
	"github.com/aether-vpn/aether/core/proto"
	"github.com/aether-vpn/aether/core/session"
)

const decoyHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Edge Sync Service</title>
<style>body{font-family:system-ui,sans-serif;max-width:600px;margin:80px auto;color:#222;line-height:1.6}code{background:#f3f4f6;padding:2px 6px;border-radius:4px}</style>
</head>
<body><h1>Edge Sync API</h1><p>Status: <code>operational</code></p><p>Realtime telemetry ingestion node.</p></body>
</html>`

type serverSession struct {
	sess       *session.Session
	lastActive time.Time
	udpMu      sync.Mutex
	udpFlows   map[uint32]*udpRelay
}

type udpRelay struct {
	conn       *net.UDPConn
	lastActive time.Time
	cancel     context.CancelFunc
}

type Server struct {
	psk        []byte
	path       string
	h2Path     string
	guard      *netguard.Guard
	dnsCache   *adns.Cache
	blocker    *adblock.Blocker
	decoyProxy *httputil.ReverseProxy
	upgrader   websocket.Upgrader

	mu       sync.Mutex
	sessions map[[16]byte]*serverSession
}

func NewServer(psk, tunnelPath string, allowPrivate, enableAdblock bool, decoyURL string) *Server {
	var proxy *httputil.ReverseProxy
	if decoyURL != "" {
		if u, err := url.Parse(decoyURL); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			proxy = httputil.NewSingleHostReverseProxy(u)
			orig := proxy.Director
			proxy.Director = func(req *http.Request) {
				orig(req)
				req.Host = u.Host
			}
		}
	}

	s := &Server{
		psk:        []byte(psk),
		path:       tunnelPath,
		h2Path:     strings.TrimSuffix(tunnelPath, "/") + "/h2",
		guard:      &netguard.Guard{AllowPrivate: allowPrivate},
		dnsCache:   adns.NewCache(60 * time.Second),
		blocker:    adblock.New(enableAdblock),
		decoyProxy: proxy,
		upgrader: websocket.Upgrader{
			ReadBufferSize:    64 * 1024,
			WriteBufferSize:   64 * 1024,
			EnableCompression: false,
			CheckOrigin:       func(r *http.Request) bool { return true },
		},
		sessions: make(map[[16]byte]*serverSession),
	}
	go s.gcLoop()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/health":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"ts":     time.Now().Unix(),
		})
		return

	case r.URL.Path == s.path && websocket.IsWebSocketUpgrade(r):
		conn, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		s.serveCarrier(carrier.WrapConn(conn, carrier.Endpoint{Name: r.RemoteAddr}))
		return

	case r.URL.Path == s.h2Path && r.Method == http.MethodPost:
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		s.serveCarrier(carrier.WrapH2Server(r, w, flusher, carrier.Endpoint{Name: r.RemoteAddr}))
		return
	}

	// Active-probe resistance: serve live decoy reverse proxy or static HTML
	if s.decoyProxy != nil {
		s.decoyProxy.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(decoyHTML))
}

func (s *Server) serveCarrier(c carrier.Conn) {
	helloBytes, err := c.ReadPacket()
	if err != nil {
		_ = c.Close()
		return
	}
	ch, err := carrier.DecodeClientHello(helloBytes, s.psk)
	if err != nil {
		_ = c.WritePacket(carrier.EncodeServerHello(carrier.ServerHello{
			Version: proto.Version,
			Status:  carrier.StatusAuthFailed,
		}, [16]byte{}, s.psk))
		_ = c.Close()
		return
	}

	if ch.Mode == carrier.ModeAttachCarrier {
		s.mu.Lock()
		ss, ok := s.sessions[ch.SessionID]
		if ok {
			ss.lastActive = time.Now()
		}
		s.mu.Unlock()

		if !ok {
			_ = c.WritePacket(carrier.EncodeServerHello(carrier.ServerHello{
				Version: proto.Version,
				Status:  carrier.StatusUnknownSession,
			}, ch.SessionID, s.psk))
			_ = c.Close()
			return
		}

		if err := c.WritePacket(carrier.EncodeServerHello(carrier.ServerHello{
			Version: proto.Version,
			Status:  carrier.StatusOK,
		}, ch.SessionID, s.psk)); err != nil {
			_ = c.Close()
			return
		}
		done := make(chan struct{})
		ss.sess.AttachCarrier(c, func(_ carrier.Conn, _ error) {
			s.touchSession(ch.SessionID)
			close(done)
		})
		<-done
		return
	}

	kp, err := acrypto.GenerateKeyPair()
	if err != nil {
		_ = c.Close()
		return
	}
	cipher, err := acrypto.DeriveSessionCipher(s.psk, kp.Private, ch.PubKey, ch.SessionID, true)
	if err != nil {
		_ = c.Close()
		return
	}

	if err := c.WritePacket(carrier.EncodeServerHello(carrier.ServerHello{
		Version: proto.Version,
		Status:  carrier.StatusOK,
		PubKey:  kp.Public,
	}, ch.SessionID, s.psk)); err != nil {
		_ = c.Close()
		return
	}

	ss := &serverSession{
		lastActive: time.Now(),
		udpFlows:   make(map[uint32]*udpRelay),
	}
	sess := session.New(
		ch.SessionID,
		cipher,
		true,
		func(ctx context.Context, st *session.Stream, addr proto.Address) error {
			return s.handleTCPStream(ctx, ss, st, addr)
		},
		func(flowID uint32, addr proto.Address, payload []byte) {
			s.handleUDPPacket(ss, flowID, addr, payload)
		},
	)
	ss.sess = sess

	s.mu.Lock()
	s.sessions[ch.SessionID] = ss
	s.mu.Unlock()

	done := make(chan struct{})
	sess.AttachCarrier(c, func(_ carrier.Conn, _ error) {
		s.touchSession(ch.SessionID)
		close(done)
	})
	<-done
}

func (s *Server) handleTCPStream(ctx context.Context, ss *serverSession, st *session.Stream, addr proto.Address) error {
	if s.blocker.IsBlocked(addr.Host) {
		return errors.New("blocked by adblock")
	}

	// For Android TUN (which connects by raw IP on port 443), peek TLS ClientHello SNI
	// before dialing outbound so ad/tracker domains are blocked with 0ms outbound traffic!
	var firstChunk []byte
	if s.blocker.Enabled() && addr.Port == 443 && net.ParseIP(addr.Host) != nil {
		peekBuf := make([]byte, 2048)
		n, err := st.Read(peekBuf)
		if err != nil {
			return err
		}
		firstChunk = peekBuf[:n]
		if sni, ok := adblock.ExtractTLSSNI(firstChunk); ok && s.blocker.IsBlocked(sni) {
			return errors.New("blocked by sni adblock")
		}
	}

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	remote, err := s.guard.DialTCP(dialCtx, addr.Host, addr.Port, 10*time.Second)
	if err != nil {
		return err
	}
	ss.sess.ConfirmStreamOpen(st)

	if len(firstChunk) > 0 {
		if _, err := remote.Write(firstChunk); err != nil {
			_ = remote.Close()
			return err
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(remote, st)
		if tc, ok := remote.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(st, remote)
		_ = st.CloseWrite()
	}()

	wg.Wait()
	_ = remote.Close()
	_ = st.Close()
	return nil
}

func (s *Server) handleUDPPacket(ss *serverSession, flowID uint32, addr proto.Address, payload []byte) {
	// Fast DNS ad/tracker blocking on UDP port 53 (returns NXDOMAIN immediately, 0ms latency!)
	if addr.Port == 53 {
		if qname, ok := adblock.ExtractDNSQueryName(payload); ok && s.blocker.IsBlocked(qname) {
			if nx := adblock.BuildNXDomain(payload); nx != nil {
				_ = ss.sess.SendUDP(flowID, addr.Host, addr.Port, nx)
			}
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	ip, err := s.guard.ResolveAndValidate(ctx, addr.Host)
	cancel()
	if err != nil {
		return
	}

	ss.udpMu.Lock()
	relay, ok := ss.udpFlows[flowID]
	if !ok {
		if len(ss.udpFlows) > 256 {
			ss.udpMu.Unlock()
			return
		}
		udpConn, err := net.ListenUDP("udp", nil)
		if err != nil {
			ss.udpMu.Unlock()
			return
		}
		rctx, rcancel := context.WithCancel(context.Background())
		relay = &udpRelay{conn: udpConn, lastActive: time.Now(), cancel: rcancel}
		ss.udpFlows[flowID] = relay
		go s.udpReadLoop(rctx, ss, flowID, relay)
	}
	relay.lastActive = time.Now()
	ss.udpMu.Unlock()

	dst := &net.UDPAddr{IP: ip, Port: int(addr.Port)}
	_, _ = relay.conn.WriteToUDP(payload, dst)
}

func (s *Server) udpReadLoop(ctx context.Context, ss *serverSession, flowID uint32, relay *udpRelay) {
	defer func() {
		_ = relay.conn.Close()
		ss.udpMu.Lock()
		delete(ss.udpFlows, flowID)
		ss.udpMu.Unlock()
	}()

	buf := make([]byte, 64*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = relay.conn.SetReadDeadline(time.Now().Add(45 * time.Second))
		n, src, err := relay.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		ss.udpMu.Lock()
		relay.lastActive = time.Now()
		ss.udpMu.Unlock()

		_ = ss.sess.SendUDP(flowID, src.IP.String(), uint16(src.Port), buf[:n])
	}
}

func (s *Server) touchSession(id [16]byte) {
	s.mu.Lock()
	if ss, ok := s.sessions[id]; ok {
		ss.lastActive = time.Now()
	}
	s.mu.Unlock()
}

func (s *Server) gcLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for id, ss := range s.sessions {
			if ss.sess.ActiveCarriers() == 0 && now.Sub(ss.lastActive) > 90*time.Second {
				delete(s.sessions, id)
				go func(target *serverSession) {
					target.udpMu.Lock()
					for _, u := range target.udpFlows {
						u.cancel()
						_ = u.conn.Close()
					}
					target.udpMu.Unlock()
					_ = target.sess.Close()
				}(ss)
			}
		}
		s.mu.Unlock()
	}
}

func main() {
	psk := os.Getenv("AETHER_PSK")
	if psk == "" {
		log.Fatal("AETHER_PSK environment variable is required")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	tunnelPath := os.Getenv("TUNNEL_PATH")
	if tunnelPath == "" {
		tunnelPath = "/ws/sync"
	}
	if !strings.HasPrefix(tunnelPath, "/") {
		tunnelPath = "/" + tunnelPath
	}
	allowPrivate, _ := strconv.ParseBool(os.Getenv("ALLOW_PRIVATE"))
	enableAdblock := true
	if v := os.Getenv("ADBLOCK"); v != "" {
		enableAdblock, _ = strconv.ParseBool(v)
	}
	decoyURL := os.Getenv("DECOY_URL")

	srv := NewServer(psk, tunnelPath, allowPrivate, enableAdblock, decoyURL)
	h2s := &http2.Server{}
	httpSrv := &http.Server{
		Addr:              "0.0.0.0:" + port,
		Handler:           h2c.NewHandler(srv, h2s),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("[aether-server] listening on 0.0.0.0:%s (ws=%s, h2=%s, adblock=%v)", port, tunnelPath, srv.h2Path, enableAdblock)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}
