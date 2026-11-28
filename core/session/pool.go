package session

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aether-vpn/aether/core/carrier"
	acrypto "github.com/aether-vpn/aether/core/crypto"
	"github.com/aether-vpn/aether/core/proto"
)

// ClientConfig configures the multi-endpoint Aether client manager.
type ClientConfig struct {
	PSK          string             `json:"psk"`
	Endpoints    []carrier.Endpoint `json:"endpoints"` // Ordered by priority: Railway (0), CF Worker / Render (1)
	Carriers     int                `json:"carriers"`  // Parallel carriers per session (default 4)
	Cipher       string             `json:"cipher"`    // xchacha20-poly1305 | aes-256-gcm | aes-128-gcm | chacha20-poly1305 | none
	PaddingMode  string             `json:"padding_mode"` // balanced (default) | stealth | off
	DialTimeout  time.Duration      `json:"-"`
	MaxPad       int                `json:"max_pad"`
	OnConfigSync ConfigSyncHandler  `json:"-"`
}

func paddingBytesForMode(mode string, fallback int) int {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "off", "none":
		return 0
	case "stealth", "high":
		return 96
	case "balanced", "default":
		return DefaultMaxPad
	default:
		if fallback > 0 {
			return fallback
		}
		return DefaultMaxPad
	}
}

// ClientManager maintains a resilient Aether Session across multiple carriers and endpoints,
// with instant Wi-Fi<->5G roaming (TriggerRoaming) and automatic recovery back to primary.
type ClientManager struct {
	cfg   ClientConfig
	psk   []byte
	onUDP UDPHandler

	mu         sync.RWMutex
	failoverMu sync.Mutex
	sess       *Session
	epIndex    int

	ctx    context.Context
	cancel context.CancelFunc
	closed atomic.Bool
}

// NewClientManager creates a ClientManager and sorts endpoints by Priority.
func NewClientManager(cfg ClientConfig, onUDP UDPHandler) (*ClientManager, error) {
	if cfg.PSK == "" {
		return nil, errors.New("aether: PSK is required")
	}
	if len(cfg.Endpoints) == 0 {
		return nil, errors.New("aether: at least one endpoint is required")
	}
	if cfg.Carriers <= 0 {
		cfg.Carriers = 3
	}
	if cfg.Carriers > 8 {
		cfg.Carriers = 8
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 8 * time.Second
	}

	eps := make([]carrier.Endpoint, len(cfg.Endpoints))
	copy(eps, cfg.Endpoints)
	sort.SliceStable(eps, func(i, j int) bool {
		return eps[i].Priority < eps[j].Priority
	})
	cfg.Endpoints = eps

	ctx, cancel := context.WithCancel(context.Background())
	return &ClientManager{
		cfg:    cfg,
		psk:    []byte(cfg.PSK),
		onUDP:  onUDP,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

// Start establishes the initial session + parallel carrier pool and starts the watchdog.
func (m *ClientManager) Start(ctx context.Context) error {
	if err := m.connectAnyEndpoint(ctx); err != nil {
		return err
	}
	go m.watchdogLoop()
	return nil
}

// TriggerRoaming is called on Android Wi-Fi <-> 5G network changes.
// Probes existing carriers first so noisy Android network callbacks never kill healthy streams!
func (m *ClientManager) TriggerRoaming() {
	if m.closed.Load() {
		return
	}
	m.mu.RLock()
	s := m.sess
	epIdx := m.epIndex
	m.mu.RUnlock()
	if s == nil {
		return
	}
	ep := m.cfg.Endpoints[epIdx]
	go func() {
		// Probe existing carriers; any dead carrier will immediately fail write and detach.
		_ = s.Ping()
		time.Sleep(500 * time.Millisecond)
		missing := m.cfg.Carriers - s.ActiveCarriers()
		for k := 0; k < missing; k++ {
			_, _ = m.attachExtraCarrierWithID(s, ep)
		}
	}()
}

// Session returns the currently active Session.
func (m *ClientManager) Session() (*Session, error) {
	m.mu.RLock()
	s := m.sess
	m.mu.RUnlock()
	if s == nil {
		return nil, ErrNoCarriers
	}
	return s, nil
}

// OpenStream opens a multiplexed TCP stream on the active session.
func (m *ClientManager) OpenStream(ctx context.Context, host string, port uint16) (*Stream, error) {
	s, err := m.Session()
	if err != nil {
		return nil, err
	}
	return s.OpenStream(ctx, host, port)
}

// SendUDP sends a UDP datagram on the active session.
func (m *ClientManager) SendUDP(flowID uint32, host string, port uint16, payload []byte) error {
	s, err := m.Session()
	if err != nil {
		return err
	}
	return s.SendUDP(flowID, host, port, payload)
}

// SendConfigSync pushes a live encrypted JSON config update to the server over the active session.
func (m *ClientManager) SendConfigSync(payload []byte) error {
	s, err := m.Session()
	if err != nil {
		return err
	}
	return s.SendConfigSync(payload)
}

// SetPaddingMode updates anti-DPI padding live on the active session without reconnecting.
func (m *ClientManager) SetPaddingMode(mode string) {
	pad := paddingBytesForMode(mode, DefaultMaxPad)
	m.mu.RLock()
	s := m.sess
	m.mu.RUnlock()
	if s != nil {
		s.SetMaxPad(pad)
	}
}

// Stats returns live telemetry for the active session.
func (m *ClientManager) Stats() Stats {
	m.mu.RLock()
	s := m.sess
	m.mu.RUnlock()
	if s == nil {
		return Stats{}
	}
	return s.Stats()
}

// Close shuts down the manager and active session.
func (m *ClientManager) Close() error {
	if m.closed.Swap(true) {
		return nil
	}
	m.cancel()
	m.mu.Lock()
	s := m.sess
	m.sess = nil
	m.mu.Unlock()
	if s != nil {
		return s.Close()
	}
	return nil
}

func (m *ClientManager) connectAnyEndpoint(ctx context.Context) error {
	var lastErr error
	for i, ep := range m.cfg.Endpoints {
		s, err := m.dialNewSession(ctx, ep)
		if err != nil {
			lastErr = err
			log.Printf("[aether] endpoint %s (%s) failed: %v", ep.Name, ep.URL, err)
			continue
		}
		m.mu.Lock()
		old := m.sess
		m.sess = s
		m.epIndex = i
		m.mu.Unlock()
		if old != nil {
			s.RecordFailover()
			_ = old.Close()
		}
		log.Printf("[aether] session %x established on %s (%s)", s.ID[:6], ep.Name, ep.URL)
		for k := 1; k < m.cfg.Carriers; k++ {
			go m.attachExtraCarrier(s, ep)
		}
		return nil
	}
	return fmt.Errorf("all endpoints failed: %w", lastErr)
}

func (m *ClientManager) dialNewSession(ctx context.Context, ep carrier.Endpoint) (*Session, error) {
	c, err := carrier.Dial(ctx, ep, m.cfg.DialTimeout)
	if err != nil {
		return nil, err
	}

	kp, err := acrypto.GenerateKeyPair()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	cipherID := acrypto.ParseCipher(m.cfg.Cipher)
	sessID := NewSessionID()
	hello := carrier.EncodeClientHello(carrier.ClientHello{
		Version:   proto.Version,
		Mode:      carrier.ModeNewSession,
		CipherID:  cipherID,
		SessionID: sessID,
		PubKey:    kp.Public,
		Timestamp: time.Now().Unix(),
	}, m.psk)

	if err := c.WritePacket(hello); err != nil {
		_ = c.Close()
		return nil, err
	}
	respBytes, err := c.ReadPacket()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	sh, err := carrier.DecodeServerHello(respBytes, sessID, m.psk)
	if err != nil || sh.Status != carrier.StatusOK {
		_ = c.Close()
		if err == nil {
			err = fmt.Errorf("server rejected handshake (status=%d)", sh.Status)
		}
		return nil, err
	}

	cipher, err := acrypto.DeriveSessionCipherWithID(m.psk, kp.Private, sh.PubKey, sessID, false, cipherID)
	if err != nil {
		_ = c.Close()
		return nil, err
	}

	s := New(sessID, cipher, false, nil, m.onUDP)
	s.SetMaxPad(paddingBytesForMode(m.cfg.PaddingMode, m.cfg.MaxPad))
	if m.cfg.OnConfigSync != nil {
		s.SetConfigSyncHandler(m.cfg.OnConfigSync)
	}
	s.AttachCarrier(c, m.onCarrierDetach(s, ep))
	return s, nil
}

func (m *ClientManager) attachExtraCarrier(s *Session, ep carrier.Endpoint) error {
	_, err := m.attachExtraCarrierWithID(s, ep)
	return err
}

func (m *ClientManager) attachExtraCarrierWithID(s *Session, ep carrier.Endpoint) (uint64, error) {
	if s.closed.Load() || m.closed.Load() {
		return 0, ErrSessionClosed
	}
	ctx, cancel := context.WithTimeout(m.ctx, m.cfg.DialTimeout)
	defer cancel()

	c, err := carrier.Dial(ctx, ep, m.cfg.DialTimeout)
	if err != nil {
		return 0, err
	}

	hello := carrier.EncodeClientHello(carrier.ClientHello{
		Version:   proto.Version,
		Mode:      carrier.ModeAttachCarrier,
		SessionID: s.ID,
		Timestamp: time.Now().Unix(),
	}, m.psk)

	if err := c.WritePacket(hello); err != nil {
		_ = c.Close()
		return 0, err
	}
	respBytes, err := c.ReadPacket()
	if err != nil {
		_ = c.Close()
		return 0, err
	}
	sh, err := carrier.DecodeServerHello(respBytes, s.ID, m.psk)
	if err != nil {
		_ = c.Close()
		return 0, err
	}
	if sh.Status == carrier.StatusUnknownSession {
		_ = c.Close()
		return 0, carrier.ErrUnknownSession
	}
	if sh.Status != carrier.StatusOK {
		_ = c.Close()
		return 0, fmt.Errorf("attach rejected (status=%d)", sh.Status)
	}

	s.AttachCarrier(c, m.onCarrierDetach(s, ep))
	log.Printf("[aether] carrier #%d attached to %s (active=%d/%d)", c.CarrierID(), ep.Name, s.ActiveCarriers(), m.cfg.Carriers)
	return c.CarrierID(), nil
}

func (m *ClientManager) onCarrierDetach(s *Session, ep carrier.Endpoint) func(carrier.Conn, error) {
	return func(c carrier.Conn, err error) {
		if m.closed.Load() || s.closed.Load() {
			return
		}
		log.Printf("[aether] carrier #%d detached from %s: %v (remaining=%d)", c.CarrierID(), ep.Name, err, s.ActiveCarriers())
		go func() {
			time.Sleep(300 * time.Millisecond)
			if err := m.attachExtraCarrier(s, ep); err != nil {
				if errors.Is(err, carrier.ErrUnknownSession) || s.ActiveCarriers() == 0 {
					ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
					defer cancel()
					_ = m.failover(ctx)
				}
			}
		}()
	}
}

func (m *ClientManager) failover(ctx context.Context) error {
	m.failoverMu.Lock()
	defer m.failoverMu.Unlock()

	m.mu.RLock()
	curSess := m.sess
	curIdx := m.epIndex
	n := len(m.cfg.Endpoints)
	m.mu.RUnlock()

	if curSess != nil && curSess.ActiveCarriers() > 0 {
		return nil
	}

	for offset := 1; offset <= n; offset++ {
		idx := (curIdx + offset) % n
		ep := m.cfg.Endpoints[idx]
		s, err := m.dialNewSession(ctx, ep)
		if err != nil {
			log.Printf("[aether] failover candidate %s failed: %v", ep.Name, err)
			continue
		}
		m.mu.Lock()
		old := m.sess
		m.sess = s
		m.epIndex = idx
		m.mu.Unlock()
		if old != nil {
			s.RecordFailover()
			_ = old.Close()
		}
		log.Printf("[aether] failed over to %s (%s)", ep.Name, ep.URL)
		for k := 1; k < m.cfg.Carriers; k++ {
			go m.attachExtraCarrier(s, ep)
		}
		return nil
	}
	return ErrNoCarriers
}

func (m *ClientManager) probeHealth(ep carrier.Endpoint) bool {
	u := strings.Replace(ep.URL, "wss://", "https://", 1)
	u = strings.Replace(u, "ws://", "http://", 1)
	if idx := strings.Index(u, "://"); idx > 0 {
		rest := u[idx+3:]
		if slash := strings.IndexByte(rest, '/'); slash > 0 {
			u = u[:idx+3+slash] + "/health"
		} else {
			u += "/health"
		}
	}
	hc := &http.Client{Timeout: 4 * time.Second}
	resp, err := hc.Get(u)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (m *ClientManager) watchdogLoop() {
	pingTicker := time.NewTicker(15 * time.Second)
	healTicker := time.NewTicker(5 * time.Second)
	primaryProbeTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()
	defer healTicker.Stop()
	defer primaryProbeTicker.Stop()

	for {
		select {
		case <-pingTicker.C:
			if s, err := m.Session(); err == nil {
				_ = s.Ping()
			}
		case <-healTicker.C:
			m.mu.RLock()
			s := m.sess
			epIdx := m.epIndex
			m.mu.RUnlock()
			if s == nil {
				continue
			}
			active := s.ActiveCarriers()
			if active == 0 {
				ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
				_ = m.failover(ctx)
				cancel()
			} else if active < m.cfg.Carriers {
				ep := m.cfg.Endpoints[epIdx]
				go m.attachExtraCarrier(s, ep)
			}
		case <-primaryProbeTicker.C:
			// Auto-return to primary endpoint (e.g. Railway) once it recovers after a fallback!
			m.mu.RLock()
			epIdx := m.epIndex
			m.mu.RUnlock()
			if epIdx > 0 && len(m.cfg.Endpoints) > 0 {
				primary := m.cfg.Endpoints[0]
				if m.probeHealth(primary) {
					ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
					if s, err := m.dialNewSession(ctx, primary); err == nil {
						m.mu.Lock()
						old := m.sess
						m.sess = s
						m.epIndex = 0
						m.mu.Unlock()
						if old != nil {
							s.RecordFailover()
							_ = old.Close()
						}
						log.Printf("[aether] primary %s recovered — switched back automatically", primary.Name)
						for k := 1; k < m.cfg.Carriers; k++ {
							go m.attachExtraCarrier(s, primary)
						}
					}
					cancel()
				}
			}
		case <-m.ctx.Done():
			return
		}
	}
}
