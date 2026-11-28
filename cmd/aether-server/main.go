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
	"sync/atomic"
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

const (
	decoyHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Edge Sync Service</title>
<style>body{font-family:system-ui,sans-serif;max-width:600px;margin:80px auto;color:#222;line-height:1.6}code{background:#f3f4f6;padding:2px 6px;border-radius:4px}</style>
</head>
<body><h1>Edge Sync API</h1><p>Status: <code>operational</code></p><p>Realtime telemetry ingestion node.</p></body>
</html>`

	serverUDPIdleTTL = 65 * time.Second
	maxServerUDP     = 256
)

type serverSession struct {
	sess       *session.Session
	createdAt  time.Time
	lastActive time.Time
	udpMu      sync.Mutex
	udpFlows   map[uint32]*udpRelay
}

type udpRelay struct {
	conn       *net.UDPConn
	lastActive time.Time
	cancel     context.CancelFunc
}

type SyncConfig struct {
	UpdatedAt   int64    `json:"updated_at"`
	DNSPreset   string   `json:"dns_preset"`
	UpstreamDNS []string `json:"upstream_dns"`
	CustomDNS   string   `json:"custom_dns,omitempty"`
	BlockDoT    bool     `json:"block_dot"`
	AdblockSNI  bool     `json:"adblock_sni"`
	Cipher      string   `json:"cipher"`
	Transport   string   `json:"transport"`
	Fingerprint string   `json:"fingerprint"`
	PaddingMode string   `json:"padding_mode"`
	Carriers    int      `json:"carriers"`
	FallbackURL string   `json:"fallback_url"`
}

type rateSample struct {
	at time.Time
	tx uint64
	rx uint64
}

type Server struct {
	psk         []byte
	adminKey    []byte
	path        string
	h2Path      string
	xhttpPath   string
	startedAt   time.Time
	guard       *netguard.Guard
	dnsCache    *adns.Cache
	udpDNSCache *adns.UDPCache
	decoyProxy  *httputil.ReverseProxy
	upgrader    websocket.Upgrader

	// Runtime-configurable settings protected by cfgMu (synced bidirectionally with clients!)
	cfgMu       sync.RWMutex
	upstreamDNS []string
	dnsPreset   string
	customDNS   string
	blockDoT    bool
	adblockSNI  bool
	blocker     *adblock.Blocker
	cipherName  string
	transport   string
	fingerprint string
	paddingMode string
	carriers    int
	fallbackURL string
	cfgUpdated  int64

	// Smooth 3-second rolling throughput meter (eliminates Envoy burst & browser poll jitter!)
	rateMu       sync.RWMutex
	rateHistory  []rateSample
	liveDownBps  float64
	liveUpBps    float64

	// Active SplitHTTP (XHTTP) downlink carriers keyed by stream id
	xhttpMu  sync.Mutex
	xhttpMap map[string]*carrier.XHTTPServerCarrier

	// Telemetry counters
	archivedTx      atomic.Uint64
	archivedRx      atomic.Uint64
	dnsQueries      atomic.Uint64
	dnsCacheHits    atomic.Uint64
	dnsAAAAFiltered atomic.Uint64

	mu       sync.Mutex
	sessions map[[16]byte]*serverSession
}

func NewServer(psk, adminKey, tunnelPath string, allowPrivate, adblockSNI, blockDoT bool, upstreamDNS []string, decoyURL string) *Server {
	if adminKey == "" {
		adminKey = psk
	}
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

	dnsCache := adns.NewCache(60 * time.Second)
	preset := "adguard"
	if len(upstreamDNS) > 0 && strings.HasPrefix(upstreamDNS[0], "1.1.1.1") {
		preset = "cloudflare"
	} else if len(upstreamDNS) > 0 && strings.HasPrefix(upstreamDNS[0], "8.8.8.8") {
		preset = "google"
	}
	fallback := os.Getenv("FALLBACK_URL")
	if fallback == "" {
		fallback = "wss://sync.novv.tech/ws/sync"
	}
	s := &Server{
		psk:         []byte(psk),
		adminKey:    []byte(adminKey),
		path:        tunnelPath,
		h2Path:      strings.TrimSuffix(tunnelPath, "/") + "/h2",
		xhttpPath:   strings.TrimSuffix(tunnelPath, "/") + "/xhttp",
		startedAt:   time.Now(),
		guard:       &netguard.Guard{AllowPrivate: allowPrivate, LookupFn: dnsCache.LookupIP},
		dnsCache:    dnsCache,
		udpDNSCache: adns.NewUDPCache(60 * time.Second),
		upstreamDNS: upstreamDNS,
		dnsPreset:   preset,
		blockDoT:    blockDoT,
		adblockSNI:  adblockSNI,
		blocker:     adblock.New(adblockSNI),
		cipherName:  "xchacha20-poly1305",
		transport:   "auto",
		fingerprint: "chrome",
		paddingMode: "balanced",
		carriers:    4,
		fallbackURL: fallback,
		cfgUpdated:  time.Now().UnixMilli(),
		decoyProxy:  proxy,
		upgrader: websocket.Upgrader{
			ReadBufferSize:    256 * 1024,
			WriteBufferSize:   256 * 1024,
			EnableCompression: false,
			CheckOrigin:       func(r *http.Request) bool { return true },
		},
		xhttpMap: make(map[string]*carrier.XHTTPServerCarrier),
		sessions: make(map[[16]byte]*serverSession),
	}
	go s.gcLoop()
	go s.rateLoop()
	return s
}

func (s *Server) currentTotals() (uint64, uint64) {
	var liveTx, liveRx uint64
	s.mu.Lock()
	for _, ss := range s.sessions {
		st := ss.sess.Stats()
		liveTx += st.TxBytes
		liveRx += st.RxBytes
	}
	s.mu.Unlock()
	return s.archivedTx.Load() + liveTx, s.archivedRx.Load() + liveRx
}

// rateLoop computes a smooth 3-second sliding-window Mbps rate on the server's monotonic clock.
func (s *Server) rateLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		tx, rx := s.currentTotals()
		s.rateMu.Lock()
		s.rateHistory = append(s.rateHistory, rateSample{at: now, tx: tx, rx: rx})
		if len(s.rateHistory) > 4 {
			s.rateHistory = s.rateHistory[len(s.rateHistory)-4:]
		}
		if len(s.rateHistory) >= 2 {
			first := s.rateHistory[0]
			dt := now.Sub(first.at).Seconds()
			if dt >= 0.5 && tx >= first.tx && rx >= first.rx {
				s.liveDownBps = float64(tx-first.tx) / dt
				s.liveUpBps = float64(rx-first.rx) / dt
			}
		}
		s.rateMu.Unlock()
	}
}

func (s *Server) getSyncConfig() SyncConfig {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return SyncConfig{
		UpdatedAt:   s.cfgUpdated,
		DNSPreset:   s.dnsPreset,
		UpstreamDNS: append([]string(nil), s.upstreamDNS...),
		CustomDNS:   s.customDNS,
		BlockDoT:    s.blockDoT,
		AdblockSNI:  s.adblockSNI,
		Cipher:      s.cipherName,
		Transport:   s.transport,
		Fingerprint: s.fingerprint,
		PaddingMode: s.paddingMode,
		Carriers:    s.carriers,
		FallbackURL: s.fallbackURL,
	}
}

func (s *Server) broadcastSyncConfig(excludeID [16]byte, hasExclude bool) {
	raw, err := json.Marshal(s.getSyncConfig())
	if err != nil {
		return
	}
	padBytes := 24
	switch s.getSyncConfig().PaddingMode {
	case "off":
		padBytes = 0
	case "stealth":
		padBytes = 96
	}
	s.mu.Lock()
	targets := make([]*session.Session, 0, len(s.sessions))
	for id, ss := range s.sessions {
		ss.sess.SetMaxPad(padBytes)
		if hasExclude && id == excludeID {
			continue
		}
		targets = append(targets, ss.sess)
	}
	s.mu.Unlock()
	for _, sess := range targets {
		go func(target *session.Session) {
			_ = target.SendConfigSync(raw)
		}(sess)
	}
}

func (s *Server) applySyncPatch(patch struct {
	DNSPreset   string `json:"dns_preset"`
	CustomDNS   string `json:"custom_dns"`
	BlockDoT    *bool  `json:"block_dot"`
	AdblockSNI  *bool  `json:"adblock_sni"`
	Cipher      string `json:"cipher"`
	Transport   string `json:"transport"`
	Fingerprint string `json:"fingerprint"`
	PaddingMode string `json:"padding_mode"`
	Carriers    *int   `json:"carriers"`
	FallbackURL *string `json:"fallback_url"`
}, excludeID [16]byte, hasExclude bool) SyncConfig {
	s.cfgMu.Lock()
	if patch.DNSPreset != "" {
		switch strings.ToLower(strings.TrimSpace(patch.DNSPreset)) {
		case "adguard":
			s.dnsPreset = "adguard"
			s.upstreamDNS = []string{"94.140.14.14:53", "94.140.15.15:53"}
		case "cloudflare":
			s.dnsPreset = "cloudflare"
			s.upstreamDNS = []string{"1.1.1.1:53", "1.0.0.1:53"}
		case "google":
			s.dnsPreset = "google"
			s.upstreamDNS = []string{"8.8.8.8:53", "8.8.4.4:53"}
		case "custom":
			if parsed := parseUpstreams(patch.CustomDNS); len(parsed) > 0 {
				s.dnsPreset = "custom"
				s.customDNS = strings.TrimSpace(patch.CustomDNS)
				s.upstreamDNS = parsed
			}
		}
		s.udpDNSCache.Flush()
	}
	if patch.BlockDoT != nil {
		s.blockDoT = *patch.BlockDoT
	}
	if patch.AdblockSNI != nil {
		s.adblockSNI = *patch.AdblockSNI
		s.blocker = adblock.New(*patch.AdblockSNI)
	}
	if patch.Cipher != "" {
		s.cipherName = acrypto.CipherName(acrypto.ParseCipher(patch.Cipher))
	}
	if patch.Transport != "" {
		switch strings.ToLower(strings.TrimSpace(patch.Transport)) {
		case "auto", "ws", "xhttp", "grpc", "h2":
			s.transport = strings.ToLower(strings.TrimSpace(patch.Transport))
		}
	}
	if patch.Fingerprint != "" {
		switch strings.ToLower(strings.TrimSpace(patch.Fingerprint)) {
		case "chrome", "firefox", "safari", "ios", "randomized":
			s.fingerprint = strings.ToLower(strings.TrimSpace(patch.Fingerprint))
		}
	}
	if patch.PaddingMode != "" {
		switch strings.ToLower(strings.TrimSpace(patch.PaddingMode)) {
		case "balanced", "stealth", "off":
			s.paddingMode = strings.ToLower(strings.TrimSpace(patch.PaddingMode))
		}
	}
	if patch.Carriers != nil {
		n := *patch.Carriers
		if n < 1 {
			n = 1
		}
		if n > 6 {
			n = 6
		}
		s.carriers = n
	}
	if patch.FallbackURL != nil {
		s.fallbackURL = strings.TrimSpace(*patch.FallbackURL)
	}
	s.cfgUpdated = time.Now().UnixMilli()
	s.cfgMu.Unlock()

	s.broadcastSyncConfig(excludeID, hasExclude)
	return s.getSyncConfig()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/health":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"version": serverVersion,
			"ts":      time.Now().Unix(),
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
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		s.serveCarrier(carrier.WrapH2Server(r, w, flusher, carrier.Endpoint{Name: r.RemoteAddr}))
		return

	case r.URL.Path == s.xhttpPath && r.Method == http.MethodGet:
		id := r.URL.Query().Get("id")
		if len(id) < 8 || len(id) > 64 {
			http.Error(w, "bad stream id", http.StatusBadRequest)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		xc := carrier.NewXHTTPServerCarrier(w, flusher, carrier.Endpoint{Name: r.RemoteAddr})
		s.xhttpMu.Lock()
		s.xhttpMap[id] = xc
		s.xhttpMu.Unlock()
		defer func() {
			s.xhttpMu.Lock()
			delete(s.xhttpMap, id)
			s.xhttpMu.Unlock()
			_ = xc.Close()
		}()
		s.serveCarrier(xc)
		return

	case r.URL.Path == s.xhttpPath && r.Method == http.MethodPost:
		id := r.URL.Query().Get("id")
		s.xhttpMu.Lock()
		xc, ok := s.xhttpMap[id]
		s.xhttpMu.Unlock()
		if !ok {
			http.Error(w, "unknown stream id", http.StatusGone)
			return
		}
		if err := xc.FeedPostBody(r.Body); err != nil {
			http.Error(w, "stream closed", http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusOK)
		return

	case r.URL.Path == carrier.GRPCPath && r.Method == http.MethodPost:
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		s.serveCarrier(carrier.WrapGRPCServer(r, w, flusher, carrier.Endpoint{Name: r.RemoteAddr}))
		return
	}

	// Private admin dashboard (constant-time auth; wrong key falls through silently to decoy HTML!)
	if s.handleAdminRoute(w, r) {
		return
	}

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
	cipher, err := acrypto.DeriveSessionCipherWithID(s.psk, kp.Private, ch.PubKey, ch.SessionID, true, ch.CipherID)
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

	now := time.Now()
	ss := &serverSession{
		createdAt:  now,
		lastActive: now,
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
	// Apply current server padding mode and wire bidirectional live config sync!
	curCfg := s.getSyncConfig()
	switch curCfg.PaddingMode {
	case "off":
		sess.SetMaxPad(0)
	case "stealth":
		sess.SetMaxPad(96)
	default:
		sess.SetMaxPad(session.DefaultMaxPad)
	}
	sess.SetConfigSyncHandler(func(payload []byte) {
		var patch struct {
			DNSPreset   string  `json:"dns_preset"`
			CustomDNS   string  `json:"custom_dns"`
			BlockDoT    *bool   `json:"block_dot"`
			AdblockSNI  *bool   `json:"adblock_sni"`
			Cipher      string  `json:"cipher"`
			Transport   string  `json:"transport"`
			Fingerprint string  `json:"fingerprint"`
			PaddingMode string  `json:"padding_mode"`
			Carriers    *int    `json:"carriers"`
			FallbackURL *string `json:"fallback_url"`
		}
		if err := json.Unmarshal(payload, &patch); err == nil {
			s.applySyncPatch(patch, ch.SessionID, true)
		}
	})
	ss.sess = sess

	s.mu.Lock()
	s.sessions[ch.SessionID] = ss
	s.mu.Unlock()

	done := make(chan struct{})
	sess.AttachCarrier(c, func(_ carrier.Conn, _ error) {
		s.touchSession(ch.SessionID)
		close(done)
	})
	// Immediately sync current Railway settings down to the newly connected client!
	go func() {
		time.Sleep(150 * time.Millisecond)
		if raw, err := json.Marshal(s.getSyncConfig()); err == nil {
			_ = sess.SendConfigSync(raw)
		}
	}()
	<-done
}

func (s *Server) handleTCPStream(ctx context.Context, ss *serverSession, st *session.Stream, addr proto.Address) error {
	s.cfgMu.RLock()
	blockDoT := s.blockDoT
	adblockSNI := s.adblockSNI
	blocker := s.blocker
	s.cfgMu.RUnlock()

	if blockDoT && addr.Port == 853 {
		return errors.New("dot blocked")
	}

	var firstChunk []byte
	if adblockSNI && blocker != nil {
		if blocker.IsBlocked(addr.Host) {
			return errors.New("blocked by adblock")
		}
		if addr.Port == 443 && net.ParseIP(addr.Host) != nil {
			peekBuf := make([]byte, 2048)
			n, err := st.Read(peekBuf)
			if err != nil {
				return err
			}
			firstChunk = peekBuf[:n]
			if sni, ok := adblock.ExtractTLSSNI(firstChunk); ok && blocker.IsBlocked(sni) {
				return errors.New("blocked by sni adblock")
			}
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
		bufPtr := session.CopyBufPool.Get().(*[]byte)
		defer session.CopyBufPool.Put(bufPtr)
		_, _ = io.CopyBuffer(remote, st, *bufPtr)
		if tc, ok := remote.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()

	go func() {
		defer wg.Done()
		bufPtr := session.CopyBufPool.Get().(*[]byte)
		defer session.CopyBufPool.Put(bufPtr)
		_, _ = io.CopyBuffer(st, remote, *bufPtr)
		_ = st.CloseWrite()
	}()

	wg.Wait()
	_ = remote.Close()
	_ = st.Close()
	return nil
}

// resolveUDP53 serves cached DNS responses in <0.1ms and races upstream AdGuard DNS servers in parallel.
func (s *Server) resolveUDP53(ss *serverSession, flowID uint32, origAddr proto.Address, query []byte, upstreams []string) {
	s.dnsQueries.Add(1)

	if adns.IsAAAAQuery(query) {
		s.dnsAAAAFiltered.Add(1)
		if empty := adns.BuildEmptyNoError(query); empty != nil {
			_ = ss.sess.SendUDP(flowID, origAddr.Host, origAddr.Port, empty)
		}
		return
	}

	if cached, ok := s.udpDNSCache.Get(query); ok {
		s.dnsCacheHits.Add(1)
		_ = ss.sess.SendUDP(flowID, origAddr.Host, origAddr.Port, cached)
		return
	}

	resCh := make(chan []byte, len(upstreams))
	for _, upstream := range upstreams {
		go func(addr string) {
			conn, err := net.DialTimeout("udp", addr, 1500*time.Millisecond)
			if err != nil {
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			if _, err := conn.Write(query); err != nil {
				return
			}
			resp := make([]byte, 4096)
			n, err := conn.Read(resp)
			if err != nil || n < 12 {
				return
			}
			resCh <- resp[:n]
		}(upstream)
	}

	select {
	case resp := <-resCh:
		s.udpDNSCache.Put(query, resp)
		_ = ss.sess.SendUDP(flowID, origAddr.Host, origAddr.Port, resp)
	case <-time.After(2200 * time.Millisecond):
	}
}

func (s *Server) handleUDPPacket(ss *serverSession, flowID uint32, addr proto.Address, payload []byte) {
	s.cfgMu.RLock()
	blockDoT := s.blockDoT
	upstreams := s.upstreamDNS
	s.cfgMu.RUnlock()

	if blockDoT && addr.Port == 853 {
		return
	}

	if addr.Port == 53 && len(upstreams) > 0 {
		qCopy := append([]byte(nil), payload...)
		go s.resolveUDP53(ss, flowID, addr, qCopy, upstreams)
		return
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
		if len(ss.udpFlows) >= maxServerUDP {
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
		_ = relay.conn.SetReadDeadline(time.Now().Add(serverUDPIdleTTL))
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
				st := ss.sess.Stats()
				s.archivedTx.Add(st.TxBytes)
				s.archivedRx.Add(st.RxBytes)
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

func parseUpstreams(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{"94.140.14.14:53", "94.140.15.15:53"}
	}
	if strings.EqualFold(strings.TrimSpace(raw), "off") {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if !strings.Contains(p, ":") {
			p += ":53"
		}
		out = append(out, p)
	}
	return out
}

func main() {
	psk := os.Getenv("AETHER_PSK")
	if psk == "" {
		log.Fatal("AETHER_PSK environment variable is required")
	}
	adminKey := os.Getenv("ADMIN_KEY")
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
	adblockSNI, _ := strconv.ParseBool(os.Getenv("ADBLOCK_SNI"))
	blockDoT := true
	if v := os.Getenv("BLOCK_DOT"); v != "" {
		blockDoT, _ = strconv.ParseBool(v)
	}
	upstreamDNS := parseUpstreams(os.Getenv("UPSTREAM_DNS"))
	decoyURL := os.Getenv("DECOY_URL")

	srv := NewServer(psk, adminKey, tunnelPath, allowPrivate, adblockSNI, blockDoT, upstreamDNS, decoyURL)
	h2s := &http2.Server{}
	httpSrv := &http.Server{
		Addr:              "0.0.0.0:" + port,
		Handler:           h2c.NewHandler(srv, h2s),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("[aether-server] v%s listening on 0.0.0.0:%s (ws=%s, h2=%s, dns=%v, block_dot=%v, sni_adblock=%v)",
			serverVersion, port, tunnelPath, srv.h2Path, upstreamDNS, blockDoT, adblockSNI)
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
