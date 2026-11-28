package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"
)

const serverVersion = "4.1.0"

// handleAdminRoute authenticates /{ADMIN_KEY}/... in constant time.
// Returns true if the request was handled as an authenticated admin route; false falls through to decoy HTML.
func (s *Server) handleAdminRoute(w http.ResponseWriter, r *http.Request) bool {
	if len(s.adminKey) == 0 {
		return false
	}
	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	if trimmed == "" {
		return false
	}
	var keyPart, subPath string
	if idx := strings.IndexByte(trimmed, '/'); idx >= 0 {
		keyPart = trimmed[:idx]
		subPath = trimmed[idx:]
	} else {
		keyPart = trimmed
		subPath = "/"
	}
	if subtle.ConstantTimeCompare([]byte(keyPart), s.adminKey) != 1 {
		return false
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")

	switch {
	case subPath == "/" || subPath == "":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(dashboardHTML))
		return true

	case subPath == "/api/stats" && r.Method == http.MethodGet:
		s.handleAPIStats(w, r)
		return true

	case subPath == "/api/sync" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.getSyncConfig())
		return true

	case (subPath == "/api/config" || subPath == "/api/sync") && r.Method == http.MethodPost:
		s.handleAPIConfig(w, r)
		return true

	case subPath == "/api/kick" && r.Method == http.MethodPost:
		s.handleAPIKick(w, r)
		return true

	case subPath == "/api/dns/flush" && r.Method == http.MethodPost:
		s.dnsCache.Flush()
		s.udpDNSCache.Flush()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return true
	}

	http.NotFound(w, r)
	return true
}

func (s *Server) handleAPIStats(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	type sessInfo struct {
		ID       string `json:"id"`
		Cipher   string `json:"cipher"`
		Carriers int    `json:"carriers"`
		Streams  int    `json:"streams"`
		UDPFlows int    `json:"udp_flows"`
		TxBytes  uint64 `json:"tx_bytes"`
		RxBytes  uint64 `json:"rx_bytes"`
		RTTMs    int64  `json:"rtt_ms"`
		AgeSec   int64  `json:"age_sec"`
	}

	now := time.Now()
	s.mu.Lock()
	sessions := make([]sessInfo, 0, len(s.sessions))
	var totalCarriers, totalStreams, totalUDP int
	var liveTx, liveRx uint64
	for id, ss := range s.sessions {
		st := ss.sess.Stats()
		ss.udpMu.Lock()
		uCount := len(ss.udpFlows)
		ss.udpMu.Unlock()
		totalCarriers += st.ActiveCarriers
		totalStreams += st.ActiveStreams
		totalUDP += uCount
		liveTx += st.TxBytes
		liveRx += st.RxBytes
		sessions = append(sessions, sessInfo{
			ID:       fmt.Sprintf("%x", id[:6]),
			Cipher:   st.Cipher,
			Carriers: st.ActiveCarriers,
			Streams:  st.ActiveStreams,
			UDPFlows: uCount,
			TxBytes:  st.TxBytes,
			RxBytes:  st.RxBytes,
			RTTMs:    st.RTTMs,
			AgeSec:   int64(now.Sub(ss.createdAt).Seconds()),
		})
	}
	s.mu.Unlock()

	totalTx := s.archivedTx.Load() + liveTx
	totalRx := s.archivedRx.Load() + liveRx

	s.rateMu.RLock()
	downBps := s.liveDownBps
	upBps := s.liveUpBps
	s.rateMu.RUnlock()

	sc := s.getSyncConfig()

	dnsQ := s.dnsQueries.Load()
	dnsHits := s.dnsCacheHits.Load()
	dnsAAAA := s.dnsAAAAFiltered.Load()
	var hitPct float64
	if dnsQ > 0 {
		hitPct = float64(dnsHits) * 100.0 / float64(dnsQ)
	}

	scheme := "wss"
	if r.TLS == nil && strings.HasPrefix(r.Host, "127.0.0.1") {
		scheme = "ws"
	}
	wsURL := fmt.Sprintf("%s://%s%s", scheme, r.Host, s.path)

	writeJSON(w, http.StatusOK, map[string]any{
		"version":           serverVersion,
		"server_time_ms":    now.UnixMilli(),
		"uptime_sec":        int64(now.Sub(s.startedAt).Seconds()),
		"goroutines":        runtime.NumGoroutine(),
		"mem_alloc_mb":      float64(ms.Alloc) / (1024 * 1024),
		"mem_sys_mb":        float64(ms.Sys) / (1024 * 1024),
		"active_sessions":   len(sessions),
		"active_carriers":   totalCarriers,
		"active_streams":    totalStreams,
		"active_udp_flows":  totalUDP,
		"total_tx_bytes":    totalTx,
		"total_rx_bytes":    totalRx,
		"live_down_bps":     downBps,
		"live_up_bps":       upBps,
		"live_down_mbps":    (downBps * 8.0) / 1e6,
		"live_up_mbps":      (upBps * 8.0) / 1e6,
		"dns_queries":       dnsQ,
		"dns_cache_hits":    dnsHits,
		"dns_aaaa_filtered": dnsAAAA,
		"dns_cache_size":    s.udpDNSCache.Size(),
		"dns_cache_hit_pct": hitPct,
		"ws_url":            wsURL,
		"fallback_default":  sc.FallbackURL,
		"psk":               string(s.psk),
		"sync_config":       sc,
		"config": map[string]any{
			"tunnel_path":      s.path,
			"protocol_profile": sc.ProtocolProfile,
			"upstream_dns":     sc.UpstreamDNS,
			"block_dot":        sc.BlockDoT,
			"adblock_sni":      sc.AdblockSNI,
			"cipher":           sc.Cipher,
			"transport":        sc.Transport,
			"fingerprint":      sc.Fingerprint,
			"padding_mode":     sc.PaddingMode,
			"fragment_mode":    sc.FragmentMode,
			"quic_mode":        sc.QUICMode,
			"window_profile":   sc.WindowProfile,
			"custom_path":      sc.CustomPath,
			"idle_power":       sc.IdlePower,
			"carriers":         sc.Carriers,
			"dns_preset":       sc.DNSPreset,
			"custom_dns":       sc.CustomDNS,
			"fallback_url":     sc.FallbackURL,
			"updated_at":       sc.UpdatedAt,
		},
		"sessions": sessions,
	})
}

func (s *Server) handleAPIConfig(w http.ResponseWriter, r *http.Request) {
	var body SyncPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}

	updated := s.applySyncPatch(body, [16]byte{}, false)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"sync_config":  updated,
		"upstream_dns": updated.UpstreamDNS,
		"block_dot":    updated.BlockDoT,
		"adblock_sni":  updated.AdblockSNI,
	})
}

func (s *Server) handleAPIKick(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}

	var kicked *serverSession
	s.mu.Lock()
	for id, ss := range s.sessions {
		if fmt.Sprintf("%x", id[:6]) == body.SessionID {
			kicked = ss
			delete(s.sessions, id)
			break
		}
	}
	s.mu.Unlock()

	if kicked == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
		return
	}

	st := kicked.sess.Stats()
	s.archivedTx.Add(st.TxBytes)
	s.archivedRx.Add(st.RxBytes)
	kicked.udpMu.Lock()
	for _, u := range kicked.udpFlows {
		u.cancel()
		_ = u.conn.Close()
	}
	kicked.udpMu.Unlock()
	_ = kicked.sess.Close()

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "kicked": body.SessionID})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

const dashboardHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="dark">
<title>AetherVPN · Control Plane</title>
<style>
  :root {
    color-scheme: dark;
    accent-color: #10b981;
    scrollbar-color: #71717a #09090b;
    scrollbar-width: auto;

    --bg: #09090b;
    --surface: #111114;
    --surface-hover: #16161a;
    --well: #0c0c0f;
    --border: rgba(255, 255, 255, 0.08);
    --border-strong: rgba(255, 255, 255, 0.14);
    --specular: inset 0 1px 0 rgba(255, 255, 255, 0.04);

    --text: #fafafa;
    --text-2: #a1a1aa;
    --text-3: #85858f;

    --emerald: #10b981;
    --emerald-hi: #34d399;
    --emerald-tint: rgba(16, 185, 129, 0.12);
    --emerald-border: rgba(16, 185, 129, 0.34);
    --emerald-ink: #04150e;

    --silver: #e4e4e7;
    --amber: #f59e0b;
    --amber-tint: rgba(245, 158, 11, 0.12);
    --amber-border: rgba(245, 158, 11, 0.32);
    --rose: #f43f5e;
    --rose-hi: #fda4af;
    --rose-tint: rgba(244, 63, 94, 0.12);
    --rose-border: rgba(244, 63, 94, 0.34);

    --radius-lg: 12px;
    --radius-md: 8px;
    --radius-sm: 6px;
    --font-sans: ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    --font-mono: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  }

  *, *::before, *::after {
    box-sizing: border-box;
  }

  html, body {
    margin: 0;
    padding: 0;
    background: var(--bg);
    color: var(--text);
    font-family: var(--font-sans);
    font-size: 14px;
    line-height: 1.5;
    -webkit-font-smoothing: antialiased;
    text-rendering: optimizeLegibility;
  }

  ::selection {
    background: rgba(16, 185, 129, 0.32);
    color: #fafafa;
  }

  :focus-visible {
    outline: 2px solid var(--emerald);
    outline-offset: 2px;
  }

  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

  .mono, .tabular {
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
  }

  .num {
    font-variant-numeric: tabular-nums;
  }

  /* ── Top Sticky Header ─────────────────────────────────────────────────── */
  .topbar {
    position: sticky;
    top: 0;
    z-index: 40;
    background: rgba(9, 9, 11, 0.84);
    backdrop-filter: blur(12px);
    -webkit-backdrop-filter: blur(12px);
    border-bottom: 1px solid var(--border);
  }

  .topbar-inner {
    max-width: 1280px;
    margin: 0 auto;
    min-height: 64px;
    padding: 10px 24px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
  }

  .brand-cluster {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
    flex-wrap: wrap;
  }

  .brand-mark {
    width: 28px;
    height: 28px;
    flex-shrink: 0;
    display: grid;
    place-items: center;
    border-radius: 7px;
    background: var(--surface);
    border: 1px solid var(--border-strong);
    box-shadow: var(--specular);
  }

  .brand-name {
    margin: 0;
    font-size: 15px;
    font-weight: 600;
    letter-spacing: -0.02em;
    color: var(--text);
  }

  .pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 3px 9px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 500;
    line-height: 1.4;
    background: rgba(255, 255, 255, 0.04);
    border: 1px solid var(--border);
    color: var(--text-2);
    white-space: nowrap;
  }

  .pill-mono {
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
  }

  .status-pill {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    padding: 3px 10px;
    border-radius: 999px;
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.05em;
    background: var(--emerald-tint);
    border: 1px solid var(--emerald-border);
    color: var(--emerald-hi);
  }

  .status-pill[data-state="degraded"] {
    background: var(--amber-tint);
    border-color: var(--amber-border);
    color: #fcd34d;
  }

  .status-pill[data-state="offline"] {
    background: var(--rose-tint);
    border-color: var(--rose-border);
    color: var(--rose-hi);
  }

  .status-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--emerald);
    position: relative;
    flex-shrink: 0;
  }

  .status-pill[data-state="degraded"] .status-dot {
    background: var(--amber);
  }

  .status-pill[data-state="offline"] .status-dot {
    background: var(--rose);
  }

  @media (prefers-reduced-motion: no-preference) {
    .status-pill[data-state="ok"] .status-dot::after {
      content: "";
      position: absolute;
      inset: -3px;
      border-radius: 50%;
      border: 1.5px solid var(--emerald);
      opacity: 0;
      animation: statusPulse 2.2s cubic-bezier(0.2, 0.7, 0.3, 1) infinite;
    }

    @keyframes statusPulse {
      0% { transform: scale(0.8); opacity: 0.85; }
      75%, 100% { transform: scale(2.1); opacity: 0; }
    }
  }

  .header-right {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-shrink: 0;
  }

  .runtime-cluster {
    display: inline-flex;
    align-items: center;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    box-shadow: var(--specular);
    overflow: hidden;
  }

  .runtime-chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 5px 10px;
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
    font-size: 11.5px;
    color: var(--text);
  }

  .runtime-chip + .runtime-chip {
    border-left: 1px solid var(--border);
  }

  .runtime-chip .k {
    color: var(--text-3);
    font-size: 10.5px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  /* ── Buttons ───────────────────────────────────────────────────────────── */
  button {
    font-family: inherit;
    cursor: pointer;
  }

  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    height: 32px;
    padding: 0 12px;
    border-radius: var(--radius-md);
    font-size: 12.5px;
    font-weight: 500;
    border: 1px solid var(--border-strong);
    background: var(--surface);
    color: var(--text);
    box-shadow: var(--specular);
    transition: background-color 140ms ease, border-color 140ms ease, color 140ms ease, transform 80ms ease;
    white-space: nowrap;
  }

  .btn:hover:not(:disabled) {
    background: #1b1b20;
    border-color: rgba(255, 255, 255, 0.22);
  }

  .btn:active:not(:disabled) {
    transform: translateY(0.5px);
  }

  .btn:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  .btn-primary {
    background: var(--emerald);
    border-color: var(--emerald);
    color: var(--emerald-ink);
    font-weight: 600;
    box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.24);
  }

  .btn-primary:hover:not(:disabled) {
    background: var(--emerald-hi);
    border-color: var(--emerald-hi);
    color: var(--emerald-ink);
  }

  .btn-ghost {
    background: rgba(255, 255, 255, 0.03);
    border-color: var(--border);
    color: var(--text-2);
  }

  .btn-ghost:hover:not(:disabled) {
    background: rgba(255, 255, 255, 0.07);
    border-color: var(--border-strong);
    color: var(--text);
  }

  .btn-danger {
    height: 28px;
    padding: 0 10px;
    font-size: 12px;
    background: var(--rose-tint);
    border-color: var(--rose-border);
    color: var(--rose-hi);
  }

  .btn-danger:hover:not(:disabled) {
    background: rgba(244, 63, 94, 0.22);
    border-color: rgba(244, 63, 94, 0.55);
    color: #fff1f2;
  }

  .btn svg {
    width: 14px;
    height: 14px;
    flex-shrink: 0;
  }

  @media (prefers-reduced-motion: no-preference) {
    .btn.is-spinning svg {
      animation: spin360 650ms linear infinite;
    }
    @keyframes spin360 {
      to { transform: rotate(360deg); }
    }
  }

  /* ── Main Container & Layout Grids ─────────────────────────────────────── */
  .shell {
    max-width: 1280px;
    margin: 0 auto;
    padding: 20px 24px 48px;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  .hero-grid {
    display: grid;
    grid-template-columns: minmax(0, 1.75fr) minmax(0, 1fr);
    gap: 16px;
    align-items: stretch;
  }

  .ops-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 16px;
    align-items: stretch;
  }

  .panel-span-2 {
    grid-column: 1 / -1;
  }

  .sync-badge {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 3px 10px;
    border-radius: 999px;
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.03em;
    background: var(--emerald-tint);
    border: 1px solid var(--emerald-border);
    color: var(--emerald-hi);
    white-space: nowrap;
  }

  .sync-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--emerald);
    flex-shrink: 0;
  }

  .sync-grid {
    display: grid;
    grid-template-columns: repeat(6, minmax(0, 1fr));
    gap: 14px 16px;
    align-items: start;
  }

  .sync-col-6 {
    grid-column: span 6;
  }

  .sync-col-3 {
    grid-column: span 3;
  }

  .sync-col-2 {
    grid-column: span 2;
  }

  .path-presets {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 8px;
  }

  .btn-xs {
    height: 26px;
    padding: 0 9px;
    font-size: 11.5px;
    border-radius: var(--radius-sm);
  }

  .btn-xs.is-active {
    background: var(--emerald-tint);
    border-color: var(--emerald-border);
    color: var(--emerald-hi);
  }

  .seg-group.seg-wrap {
    display: flex;
    flex-wrap: wrap;
    width: 100%;
  }

  .seg-group.seg-wrap .seg-pill {
    flex: 1 0 auto;
    white-space: nowrap;
  }

  .prov-profile-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    flex-wrap: wrap;
    padding: 8px 11px;
    border-radius: var(--radius-md);
    background: var(--well);
    border: 1px solid var(--border);
    font-family: var(--font-mono);
    font-size: 11.5px;
    color: var(--text-2);
  }

  .prov-profile-bar .k {
    color: var(--text-3);
    text-transform: uppercase;
    font-size: 10.5px;
    letter-spacing: 0.05em;
  }

  .prov-profile-bar .v {
    color: var(--emerald-hi);
  }

  .panel {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    box-shadow: var(--specular);
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .panel-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 14px 18px;
    border-bottom: 1px solid var(--border);
    flex-wrap: wrap;
  }

  .panel-title-group {
    display: flex;
    align-items: baseline;
    gap: 10px;
    flex-wrap: wrap;
    min-width: 0;
  }

  .panel-title {
    margin: 0;
    font-size: 13px;
    font-weight: 600;
    letter-spacing: -0.01em;
    color: var(--text);
  }

  .panel-sub {
    font-size: 11.5px;
    color: var(--text-3);
  }

  .panel-body {
    padding: 16px 18px 18px;
    display: flex;
    flex-direction: column;
    gap: 14px;
    flex: 1;
    min-width: 0;
  }

  /* ── Hero Chart Panel ──────────────────────────────────────────────────── */
  .chart-legend {
    display: inline-flex;
    align-items: center;
    gap: 14px;
    font-size: 11.5px;
    color: var(--text-2);
  }

  .legend-item {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }

  .legend-swatch {
    width: 12px;
    height: 2px;
    border-radius: 2px;
  }

  .legend-swatch.down {
    background: var(--emerald);
  }

  .legend-swatch.up {
    background: repeating-linear-gradient(90deg, var(--silver) 0 4px, transparent 4px 7px);
  }

  .chart-metrics {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 12px;
    padding: 14px 18px 0;
  }

  .chart-stat {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  .chart-stat-label {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text-3);
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }

  .swatch-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .swatch-dot.down { background: var(--emerald); }
  .swatch-dot.up { background: var(--silver); }
  .swatch-dot.peak { background: var(--text-3); }

  .chart-stat-value {
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
    font-size: 22px;
    font-weight: 600;
    letter-spacing: -0.03em;
    line-height: 1.2;
    color: var(--text);
  }

  .chart-stat-value .unit {
    font-size: 12px;
    font-weight: 500;
    color: var(--text-2);
    margin-left: 3px;
  }

  .chart-stat-sub {
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
    font-size: 11px;
    color: var(--text-3);
  }

  .chart-stage {
    position: relative;
    padding: 10px 18px 16px;
    flex: 1;
    min-height: 210px;
    display: flex;
  }

  .chart-canvas {
    width: 100%;
    height: 210px;
    display: block;
    cursor: crosshair;
  }

  .chart-tooltip {
    position: absolute;
    pointer-events: none;
    z-index: 10;
    background: rgba(9, 9, 11, 0.94);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-md);
    padding: 7px 10px;
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
    font-size: 11px;
    color: var(--text);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
    white-space: nowrap;
  }

  .chart-tooltip-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }

  .chart-tooltip-time {
    color: var(--text-3);
    margin-bottom: 3px;
    font-size: 10.5px;
  }

  /* ── 2x2 Hairline KPI Matrix ───────────────────────────────────────────── */
  .kpi-matrix {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    grid-template-rows: repeat(2, minmax(0, 1fr));
    gap: 1px;
    background: var(--border);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    overflow: hidden;
    box-shadow: var(--specular);
  }

  .kpi-cell {
    background: var(--surface);
    padding: 18px;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    gap: 10px;
    min-width: 0;
  }

  .kpi-label {
    font-size: 11px;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text-2);
  }

  .kpi-value {
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
    font-size: 28px;
    font-weight: 600;
    letter-spacing: -0.03em;
    line-height: 1.1;
    color: var(--text);
  }

  .kpi-value.emerald {
    color: var(--emerald-hi);
  }

  .kpi-sub {
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
    font-size: 11.5px;
    color: var(--text-3);
    line-height: 1.4;
  }

  .progress-track {
    width: 100%;
    height: 4px;
    border-radius: 999px;
    background: rgba(255, 255, 255, 0.08);
    overflow: hidden;
    margin-top: 2px;
  }

  .progress-fill {
    height: 100%;
    width: 0%;
    border-radius: 999px;
    background: var(--emerald);
    transition: width 320ms cubic-bezier(0.22, 1, 0.36, 1);
  }

  @media (prefers-reduced-motion: reduce) {
    .progress-fill { transition: none; }
  }

  /* ── Forms, Radio Cards & Native Switches ──────────────────────────────── */
  fieldset {
    border: 0;
    margin: 0;
    padding: 0;
    min-width: 0;
  }

  .field-label {
    display: block;
    font-size: 11px;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text-2);
    margin-bottom: 8px;
  }

  .radio-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 8px;
  }

  .radio-card {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: 3px;
    padding: 10px 12px;
    border-radius: var(--radius-md);
    background: var(--well);
    border: 1px solid var(--border);
    cursor: pointer;
    transition: border-color 140ms ease, background-color 140ms ease;
  }

  .radio-card:hover {
    border-color: var(--border-strong);
    background: #141418;
  }

  .radio-card:has(:focus-visible) {
    outline: 2px solid var(--emerald);
    outline-offset: 2px;
  }

  .radio-card:has(:checked) {
    background: var(--emerald-tint);
    border-color: var(--emerald-border);
  }

  .radio-card-top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 6px;
  }

  .radio-card-name {
    font-size: 13px;
    font-weight: 600;
    color: var(--text);
  }

  .radio-card-ip {
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
    font-size: 11.5px;
    color: var(--text-2);
  }

  .radio-card-meta {
    font-size: 11px;
    color: var(--text-3);
  }

  .radio-card:has(:checked) .radio-card-ip {
    color: var(--emerald-hi);
  }

  .radio-card input[type="radio"] {
    appearance: none;
    -webkit-appearance: none;
    width: 14px;
    height: 14px;
    margin: 0;
    border-radius: 50%;
    border: 1.5px solid rgba(255, 255, 255, 0.28);
    background: transparent;
    display: grid;
    place-items: center;
    flex-shrink: 0;
    cursor: pointer;
    transition: border-color 140ms ease;
  }

  .radio-card input[type="radio"]::after {
    content: "";
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--emerald);
    transform: scale(0);
    transition: transform 140ms ease;
  }

  .radio-card input[type="radio"]:checked {
    border-color: var(--emerald);
  }

  .radio-card input[type="radio"]:checked::after {
    transform: scale(1);
  }

  .inline-form {
    display: flex;
    gap: 8px;
    align-items: stretch;
  }

  .text-input {
    width: 100%;
    min-width: 0;
    height: 34px;
    padding: 0 11px;
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    background: var(--well);
    color: var(--text);
    font-family: var(--font-mono);
    font-size: 12.5px;
    transition: border-color 140ms ease, background-color 140ms ease;
  }

  .text-input::placeholder {
    color: var(--text-3);
    font-family: var(--font-sans);
  }

  .text-input:hover {
    border-color: var(--border-strong);
  }

  .text-input:focus {
    outline: none;
    border-color: var(--emerald);
    background: #0e0e12;
  }

  .text-input:not([type="range"]):user-invalid {
    border-color: var(--rose);
  }

  .text-input:not([type="range"]):not(:placeholder-shown):user-valid {
    border-color: var(--emerald-border);
  }

  /* Native accessible pill toggle switches */
  .switch-list {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    background: var(--well);
    overflow: hidden;
  }

  .switch-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 14px;
    padding: 11px 13px;
    cursor: pointer;
    transition: background-color 140ms ease;
  }

  .switch-row + .switch-row {
    border-top: 1px solid var(--border);
  }

  .switch-row:hover {
    background: rgba(255, 255, 255, 0.025);
  }

  .switch-row:has(:checked) {
    background: rgba(16, 185, 129, 0.05);
  }

  .switch-copy {
    display: flex;
    flex-direction: column;
    gap: 1px;
    min-width: 0;
  }

  .switch-title {
    font-size: 13px;
    font-weight: 500;
    color: var(--text);
  }

  .switch-desc {
    font-size: 11.5px;
    color: var(--text-3);
  }

  .toggle-switch {
    appearance: none;
    -webkit-appearance: none;
    position: relative;
    width: 38px;
    height: 22px;
    margin: 0;
    border-radius: 999px;
    background: rgba(255, 255, 255, 0.14);
    border: 1px solid rgba(255, 255, 255, 0.14);
    flex-shrink: 0;
    cursor: pointer;
    transition: background-color 160ms ease, border-color 160ms ease;
  }

  .toggle-switch::before {
    content: "";
    position: absolute;
    top: 2px;
    left: 2px;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: #fafafa;
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.45);
    transition: transform 160ms cubic-bezier(0.22, 1, 0.36, 1);
  }

  .toggle-switch:checked {
    background: var(--emerald);
    border-color: var(--emerald);
  }

  .toggle-switch:checked::before {
    transform: translateX(16px);
  }

  @media (prefers-reduced-motion: reduce) {
    .toggle-switch, .toggle-switch::before { transition: none; }
  }

  .policy-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-top: auto;
    padding-top: 4px;
    flex-wrap: wrap;
  }

  .policy-meta {
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
    font-size: 11.5px;
    color: var(--text-3);
  }

  /* ── Client Provisioning ───────────────────────────────────────────────── */
  .prov-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 10px;
    align-items: end;
  }

  .seg-group {
    display: inline-flex;
    padding: 3px;
    border-radius: var(--radius-md);
    background: var(--well);
    border: 1px solid var(--border);
    gap: 2px;
  }

  .seg-pill {
    position: relative;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 34px;
    height: 26px;
    padding: 0 10px;
    border-radius: 5px;
    font-size: 12px;
    font-weight: 500;
    color: var(--text-2);
    cursor: pointer;
    transition: background-color 140ms ease, color 140ms ease;
  }

  .seg-pill:hover {
    color: var(--text);
  }

  .seg-pill input[type="radio"] {
    position: absolute;
    opacity: 0;
    inset: 0;
    margin: 0;
    cursor: pointer;
  }

  .seg-pill:has(:focus-visible) {
    outline: 2px solid var(--emerald);
    outline-offset: 1px;
  }

  .seg-pill:has(:checked) {
    background: var(--emerald-tint);
    color: var(--emerald-hi);
    box-shadow: inset 0 0 0 1px var(--emerald-border);
  }

  .prov-toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    flex-wrap: wrap;
  }

  .prov-actions {
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }

  .code-well {
    margin: 0;
    padding: 13px 14px;
    border-radius: var(--radius-md);
    background: var(--well);
    border: 1px solid var(--border);
    font-family: var(--font-mono);
    font-size: 12px;
    line-height: 1.55;
    color: var(--emerald-hi);
    overflow-x: auto;
    word-break: break-all;
    white-space: pre-wrap;
    min-height: 82px;
    flex: 1;
  }

  .code-well[data-mode="cli"] {
    color: var(--silver);
  }

  /* ── Active Sessions Responsive Table ──────────────────────────────────── */
  .table-wrapper {
    overflow: auto;
    max-inline-size: 100%;
    max-block-size: min(500px, 75vh);
    container-type: inline-size;
    border-bottom-left-radius: var(--radius-lg);
    border-bottom-right-radius: var(--radius-lg);
  }

  .sessions-table {
    width: 100%;
    border-collapse: separate;
    border-spacing: 0;
    font-size: 13px;
    --label-1: "Session";
    --label-2: "Cipher";
    --label-3: "Carriers";
    --label-4: "TCP";
    --label-5: "UDP / QUIC";
    --label-6: "Down (TX)";
    --label-7: "Up (RX)";
    --label-8: "RTT";
    --label-9: "Age";
    --label-10: "Action";
  }

  .sessions-table thead {
    position: sticky;
    inset-block-start: 0;
    z-index: 2;
    background: #141418;
  }

  .sessions-table thead th {
    text-align: left;
    padding: 10px 14px;
    font-size: 11px;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text-3);
    background: #141418;
    border-bottom: 1px solid var(--border);
    white-space: nowrap;
  }

  .sessions-table thead th:first-child {
    position: sticky;
    inset-inline-start: 0;
    z-index: 3;
  }

  .sessions-table tbody th,
  .sessions-table tbody td {
    padding: 11px 14px;
    border-bottom: 1px solid var(--border);
    vertical-align: middle;
    white-space: nowrap;
  }

  .sessions-table tbody tr:last-child th,
  .sessions-table tbody tr:last-child td {
    border-bottom: 0;
  }

  .sessions-table tbody th {
    position: sticky;
    inset-inline-start: 0;
    z-index: 1;
    background: var(--surface);
    text-align: left;
    font-weight: 500;
  }

  .sessions-table tbody tr:hover th,
  .sessions-table tbody tr:hover td {
    background: var(--surface-hover);
  }

  .sess-id {
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--text);
    background: var(--well);
    border: 1px solid var(--border);
    padding: 3px 7px;
    border-radius: 5px;
    display: inline-block;
  }

  .cipher-pill {
    display: inline-flex;
    align-items: center;
    padding: 2px 7px;
    border-radius: 5px;
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 500;
    background: var(--emerald-tint);
    border: 1px solid var(--emerald-border);
    color: var(--emerald-hi);
  }

  .cipher-pill.plain {
    background: var(--amber-tint);
    border-color: var(--amber-border);
    color: #fcd34d;
  }

  .carrier-dots {
    display: inline-flex;
    align-items: center;
    gap: 5px;
  }

  .c-dots {
    display: inline-flex;
    align-items: center;
    gap: 3px;
  }

  .c-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: rgba(255, 255, 255, 0.12);
  }

  .c-dot.on {
    background: var(--emerald);
  }

  .c-count {
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
    font-size: 12px;
    color: var(--text-2);
    margin-left: 3px;
  }

  .rtt-pill {
    display: inline-flex;
    align-items: center;
    padding: 2px 8px;
    border-radius: 999px;
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
    font-size: 11.5px;
    font-weight: 500;
    border: 1px solid var(--border);
    background: rgba(255, 255, 255, 0.04);
    color: var(--text-2);
  }

  .rtt-pill.good {
    background: var(--emerald-tint);
    border-color: var(--emerald-border);
    color: var(--emerald-hi);
  }

  .rtt-pill.warn {
    background: var(--amber-tint);
    border-color: var(--amber-border);
    color: #fcd34d;
  }

  .rtt-pill.bad {
    background: var(--rose-tint);
    border-color: var(--rose-border);
    color: var(--rose-hi);
  }

  .empty-cell {
    padding: 32px 18px;
    text-align: center;
    color: var(--text-3);
    font-size: 13px;
  }

  @container (width < 640px) {
    .sessions-table,
    .sessions-table thead,
    .sessions-table tbody,
    .sessions-table tr,
    .sessions-table th,
    .sessions-table td {
      display: block;
    }

    .sessions-table thead {
      display: none;
    }

    .sessions-table tbody {
      padding: 12px;
    }

    .sessions-table tbody tr {
      margin-block-end: 12px;
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      background: var(--well);
      overflow: hidden;
    }

    .sessions-table tbody tr:last-child {
      margin-block-end: 0;
    }

    .sessions-table tbody tr.empty-row {
      border: 0;
      background: transparent;
    }

    .sessions-table tbody th {
      position: sticky;
      inset-block-start: 0;
      z-index: 2;
      background: #16161a;
      border-bottom: 1px solid var(--border);
      padding: 10px 12px;
      display: flex;
      align-items: center;
      justify-content: space-between;
    }

    .sessions-table tbody th::before {
      content: var(--label-1);
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.06em;
      color: var(--text-3);
      font-weight: 500;
    }

    .sessions-table tbody td {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      padding: 8px 12px;
      border-bottom: 1px solid rgba(255, 255, 255, 0.05);
      white-space: normal;
    }

    .sessions-table tbody td:last-child {
      border-bottom: 0;
    }

    .sessions-table tbody td::before {
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      color: var(--text-3);
      flex-shrink: 0;
    }

    .sessions-table tbody td:nth-child(2)::before { content: var(--label-2); }
    .sessions-table tbody td:nth-child(3)::before { content: var(--label-3); }
    .sessions-table tbody td:nth-child(4)::before { content: var(--label-4); }
    .sessions-table tbody td:nth-child(5)::before { content: var(--label-5); }
    .sessions-table tbody td:nth-child(6)::before { content: var(--label-6); }
    .sessions-table tbody td:nth-child(7)::before { content: var(--label-7); }
    .sessions-table tbody td:nth-child(8)::before { content: var(--label-8); }
    .sessions-table tbody td:nth-child(9)::before { content: var(--label-9); }
    .sessions-table tbody td:nth-child(10)::before { content: var(--label-10); }

    .sessions-table tbody tr.empty-row td {
      display: block;
      text-align: center;
    }

    .sessions-table tbody tr.empty-row td::before {
      content: none;
    }
  }

  /* ── Popover Toast Notifications ───────────────────────────────────────── */
  .toast-stack {
    position: fixed;
    inset-block-end: 0;
    inset-inline-end: 0;
    pointer-events: none;
    z-index: 60;
  }

  @supports not selector(:popover-open) {
    [popover]:not(.\:popover-open) {
      display: none;
    }
  }

  [popover].toast-card {
    position: fixed;
    inset: auto 20px 20px auto;
    margin: 0;
    padding: 10px 12px 10px 14px;
    max-inline-size: min(380px, calc(100vw - 24px));
    border-radius: var(--radius-md);
    background: #16161b;
    border: 1px solid var(--border-strong);
    box-shadow: 0 12px 32px rgba(0, 0, 0, 0.55), var(--specular);
    color: var(--text);
    font-size: 13px;
    line-height: 1.4;
    overflow-wrap: anywhere;
    pointer-events: auto;
    align-items: center;
    gap: 10px;
    opacity: 0;
    transition: opacity 0.22s ease-out, transform 0.22s ease-out, display 0.22s ease-out, overlay 0.22s ease-out;
    transition-behavior: allow-discrete;
  }

  @media (prefers-reduced-motion: no-preference) {
    [popover].toast-card {
      transform: translateY(10px) scale(0.98);
    }
  }

  [popover].toast-card:is(:popover-open, .\:popover-open):where(:not([data-closing])) {
    display: flex;
    opacity: 1;
  }

  @starting-style {
    [popover].toast-card:is(:popover-open, .\:popover-open):where(:not([data-closing])) {
      opacity: 0;
    }
  }

  @media (prefers-reduced-motion: no-preference) {
    [popover].toast-card:is(:popover-open, .\:popover-open):where(:not([data-closing])) {
      transform: translateY(0) scale(1);
    }

    @starting-style {
      [popover].toast-card:is(:popover-open, .\:popover-open):where(:not([data-closing])) {
        transform: translateY(10px) scale(0.98);
      }
    }
  }

  @media (prefers-reduced-motion: reduce) {
    [popover].toast-card {
      transition: opacity 0.08s ease-out, display 0.08s ease-out, overlay 0.08s ease-out;
      transition-behavior: allow-discrete;
    }
  }

  .toast-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--emerald);
    flex-shrink: 0;
  }

  .toast-card[data-tone="warn"] .toast-dot {
    background: var(--amber);
  }

  .toast-card[data-tone="error"] .toast-dot {
    background: var(--rose);
  }

  .toast-msg {
    flex: 1;
    min-width: 0;
  }

  .toast-close {
    display: inline-grid;
    place-items: center;
    width: 26px;
    height: 26px;
    padding: 0;
    border-radius: var(--radius-sm);
    border: 1px solid transparent;
    background: transparent;
    color: var(--text-2);
    flex-shrink: 0;
    transition: background-color 120ms ease, color 120ms ease;
  }

  .toast-close:hover {
    background: rgba(255, 255, 255, 0.08);
    color: var(--text);
  }

  /* ── Responsive Breakpoints ────────────────────────────────────────────── */
  @media (max-width: 900px) {
    .hero-grid,
    .ops-grid,
    .sync-grid {
      grid-template-columns: minmax(0, 1fr);
    }

    .sync-col-6,
    .sync-col-3,
    .sync-col-2 {
      grid-column: auto;
    }

    .topbar-inner {
      padding: 10px 16px;
      flex-wrap: wrap;
    }

    .shell {
      padding: 16px 16px 40px;
    }
  }

  @media (max-width: 600px) {
    .radio-grid {
      grid-template-columns: minmax(0, 1fr);
    }

    .prov-row {
      grid-template-columns: minmax(0, 1fr);
    }

    .chart-metrics {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .kpi-matrix {
      grid-template-columns: minmax(0, 1fr);
      grid-template-rows: auto;
    }

    .runtime-cluster {
      display: none;
    }

    [popover].toast-card {
      inset: auto 12px 12px 12px;
      max-inline-size: none;
    }
  }
</style>
</head>
<body>

<header class="topbar">
  <div class="topbar-inner">
    <div class="brand-cluster">
      <div class="brand-mark" aria-hidden="true">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 2L3 7v10l9 5 9-5V7l-9-5z" stroke="#fafafa" stroke-opacity="0.88"/>
          <path d="M12 7v10" stroke="#10b981"/>
          <circle cx="12" cy="12" r="2.2" fill="#10b981" stroke="none"/>
        </svg>
      </div>
      <h1 class="brand-name">AetherVPN</h1>
      <span class="pill pill-mono" id="verPill">v4.1.0</span>
      <span class="status-pill" id="statusPill" data-state="ok">
        <span class="status-dot" aria-hidden="true"></span>
        <span id="statusText">OPERATIONAL</span>
      </span>
    </div>

    <div class="header-right">
      <div class="runtime-cluster" aria-label="Server runtime summary">
        <span class="runtime-chip"><span class="k">UP</span><span id="uptimePill">--</span></span>
        <span class="runtime-chip"><span class="k">RAM</span><span id="ramPill">--</span></span>
        <span class="runtime-chip"><span class="k">GO</span><span id="goPill">--</span></span>
      </div>
      <button type="button" class="btn btn-ghost" id="btnRefresh" aria-label="Refresh telemetry">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M21 12a9 9 0 1 1-2.64-6.36"/>
          <polyline points="21 3 21 9 15 9"/>
        </svg>
        <span>Refresh</span>
      </button>
    </div>
  </div>
</header>

<main class="shell">
  <!-- Hero Telemetry Split -->
  <section class="hero-grid" aria-label="Live telemetry overview">
    <div class="panel">
      <div class="panel-head">
        <div class="panel-title-group">
          <h2 class="panel-title">Live Throughput</h2>
          <span class="panel-sub mono">90s rolling · 3s server window</span>
        </div>
        <div class="chart-legend" aria-hidden="true">
          <span class="legend-item"><span class="legend-swatch down"></span>Download</span>
          <span class="legend-item"><span class="legend-swatch up"></span>Upload</span>
        </div>
      </div>

      <div class="chart-metrics">
        <div class="chart-stat">
          <span class="chart-stat-label"><span class="swatch-dot down" aria-hidden="true"></span>Download (TX)</span>
          <div class="chart-stat-value"><span id="liveDownMbps">0.00</span><span class="unit">Mbps</span></div>
          <span class="chart-stat-sub" id="liveDownRate">0.0 KB/s</span>
        </div>
        <div class="chart-stat">
          <span class="chart-stat-label"><span class="swatch-dot up" aria-hidden="true"></span>Upload (RX)</span>
          <div class="chart-stat-value"><span id="liveUpMbps">0.00</span><span class="unit">Mbps</span></div>
          <span class="chart-stat-sub" id="liveUpRate">0.0 KB/s</span>
        </div>
        <div class="chart-stat">
          <span class="chart-stat-label"><span class="swatch-dot peak" aria-hidden="true"></span>90s Peak</span>
          <div class="chart-stat-value"><span id="livePeakMbps">0.00</span><span class="unit">Mbps</span></div>
          <span class="chart-stat-sub" id="liveTotalMbps">Combined: 0.00 Mbps</span>
        </div>
      </div>

      <div class="chart-stage" id="chartStage">
        <canvas id="throughputChart" class="chart-canvas" role="img" aria-label="Live download and upload throughput in Mbps over the last 90 seconds"></canvas>
        <div id="chartTooltip" class="chart-tooltip" hidden></div>
      </div>
    </div>

    <div class="kpi-matrix" aria-label="Key performance indicators">
      <div class="kpi-cell">
        <div class="kpi-label">Active Sessions</div>
        <div class="kpi-value" id="kSessions">0</div>
        <div class="kpi-sub" id="kCarriers">0 carriers · 0 TCP · 0 UDP</div>
      </div>

      <div class="kpi-cell">
        <div class="kpi-label">Total Transferred</div>
        <div class="kpi-value" id="kTotal">0 B</div>
        <div class="kpi-sub" id="kTotalSub">Down 0 B · Up 0 B</div>
      </div>

      <div class="kpi-cell">
        <div class="kpi-label">DNS Cache Hit Rate</div>
        <div>
          <div class="kpi-value emerald" id="kDnsHit">0%</div>
          <div class="progress-track" aria-hidden="true">
            <div class="progress-fill" id="kDnsBar"></div>
          </div>
        </div>
        <div class="kpi-sub" id="kDnsSub">0 queries · 0 cached · 0 AAAA</div>
      </div>

      <div class="kpi-cell">
        <div class="kpi-label">Server Runtime</div>
        <div class="kpi-value" id="kRuntime">0s</div>
        <div class="kpi-sub" id="kRuntimeSub">0.0 / 0.0 MB RAM · 0 goroutines</div>
      </div>
    </div>
  </section>

  <!-- Operations Split -->
  <section class="ops-grid" aria-label="Policy, synced profile, and client provisioning">
    <!-- Full-Width: Synced Tunnel Profile (Live Server ↔ Client Control) -->
    <div class="panel panel-span-2">
      <div class="panel-head">
        <div class="panel-title-group">
          <h2 class="panel-title">Synced Tunnel Profile</h2>
          <span class="panel-sub">Live Server ↔ Client Control</span>
        </div>
        <span class="sync-badge"><span class="sync-dot" aria-hidden="true"></span>Auto-Sync Active</span>
      </div>

      <div class="panel-body">
        <div class="sync-grid">
          <fieldset class="sync-col-6">
            <legend class="field-label">Aether Protocol Profile (1-Tap)</legend>
            <div class="seg-group seg-wrap" role="radiogroup" aria-label="Aether protocol profile">
              <label class="seg-pill"><input type="radio" name="protocol_profile" value="mux" data-label="Aether-Mux" checked>Aether-Mux</label>
              <label class="seg-pill"><input type="radio" name="protocol_profile" value="fast" data-label="Aether-Fast">Aether-Fast</label>
              <label class="seg-pill"><input type="radio" name="protocol_profile" value="ghost" data-label="Aether-Ghost">Aether-Ghost</label>
            </div>
          </fieldset>

          <fieldset class="sync-col-3">
            <legend class="field-label">Encryption Cipher</legend>
            <div class="seg-group seg-wrap" role="radiogroup" aria-label="Encryption cipher">
              <label class="seg-pill"><input type="radio" name="cipher" value="xchacha20-poly1305" data-label="XChaCha20" checked>XChaCha20</label>
              <label class="seg-pill"><input type="radio" name="cipher" value="aes-256-gcm" data-label="AES-256-GCM">AES-256-GCM</label>
              <label class="seg-pill"><input type="radio" name="cipher" value="aes-128-gcm" data-label="AES-128-GCM">AES-128-GCM</label>
              <label class="seg-pill"><input type="radio" name="cipher" value="chacha20-poly1305" data-label="ChaCha20">ChaCha20</label>
              <label class="seg-pill"><input type="radio" name="cipher" value="none" data-label="TLS-Only">TLS-Only</label>
            </div>
          </fieldset>

          <fieldset class="sync-col-3">
            <legend class="field-label">Transport Mode</legend>
            <div class="seg-group seg-wrap" role="radiogroup" aria-label="Transport mode">
              <label class="seg-pill"><input type="radio" name="transport" value="auto" data-label="Auto" checked>Auto</label>
              <label class="seg-pill"><input type="radio" name="transport" value="ws" data-label="WebSocket">WebSocket</label>
              <label class="seg-pill"><input type="radio" name="transport" value="xhttp" data-label="XHTTP">XHTTP</label>
              <label class="seg-pill"><input type="radio" name="transport" value="grpc" data-label="gRPC">gRPC</label>
              <label class="seg-pill"><input type="radio" name="transport" value="h2" data-label="HTTP/2">HTTP/2</label>
            </div>
          </fieldset>

          <fieldset class="sync-col-2">
            <legend class="field-label">uTLS Fingerprint</legend>
            <div class="seg-group seg-wrap" role="radiogroup" aria-label="uTLS fingerprint">
              <label class="seg-pill"><input type="radio" name="fingerprint" value="chrome" data-label="Chrome" checked>Chrome</label>
              <label class="seg-pill"><input type="radio" name="fingerprint" value="firefox" data-label="Firefox">Firefox</label>
              <label class="seg-pill"><input type="radio" name="fingerprint" value="safari" data-label="Safari">Safari</label>
              <label class="seg-pill"><input type="radio" name="fingerprint" value="ios" data-label="iOS">iOS</label>
              <label class="seg-pill"><input type="radio" name="fingerprint" value="randomized" data-label="Random">Random</label>
            </div>
          </fieldset>

          <fieldset class="sync-col-2">
            <legend class="field-label">Anti-DPI Padding</legend>
            <div class="seg-group seg-wrap" role="radiogroup" aria-label="Anti-DPI padding">
              <label class="seg-pill"><input type="radio" name="padding_mode" value="balanced" data-label="Balanced" checked>Balanced</label>
              <label class="seg-pill"><input type="radio" name="padding_mode" value="stealth" data-label="Stealth">Stealth</label>
              <label class="seg-pill"><input type="radio" name="padding_mode" value="off" data-label="Off">Off</label>
            </div>
          </fieldset>

          <fieldset class="sync-col-2">
            <legend class="field-label">Parallel Carriers</legend>
            <div class="seg-group seg-wrap" role="radiogroup" aria-label="Parallel carriers">
              <label class="seg-pill mono"><input type="radio" name="carriers" value="2" data-label="2">2</label>
              <label class="seg-pill mono"><input type="radio" name="carriers" value="3" data-label="3">3</label>
              <label class="seg-pill mono"><input type="radio" name="carriers" value="4" data-label="4" checked>4</label>
              <label class="seg-pill mono"><input type="radio" name="carriers" value="6" data-label="6">6</label>
            </div>
          </fieldset>

          <fieldset class="sync-col-2">
            <legend class="field-label">TLS Fragmentation (Anti-DPI)</legend>
            <div class="seg-group seg-wrap" role="radiogroup" aria-label="TLS fragmentation">
              <label class="seg-pill"><input type="radio" name="fragment_mode" value="off" data-label="Off" checked>Off</label>
              <label class="seg-pill"><input type="radio" name="fragment_mode" value="balanced" data-label="Balanced 40–80B">Balanced 40–80B</label>
              <label class="seg-pill"><input type="radio" name="fragment_mode" value="strict" data-label="Strict 10–30B">Strict 10–30B</label>
            </div>
          </fieldset>

          <fieldset class="sync-col-2">
            <legend class="field-label">QUIC / UDP Mode</legend>
            <div class="seg-group seg-wrap" role="radiogroup" aria-label="QUIC and UDP mode">
              <label class="seg-pill"><input type="radio" name="quic_mode" value="full" data-label="Full QUIC" checked>Full QUIC</label>
              <label class="seg-pill"><input type="radio" name="quic_mode" value="force_tcp" data-label="Force TCP">Force TCP</label>
              <label class="seg-pill"><input type="radio" name="quic_mode" value="dns_only" data-label="DNS Only">DNS Only</label>
            </div>
          </fieldset>

          <fieldset class="sync-col-2">
            <legend class="field-label">Chunk &amp; Window</legend>
            <div class="seg-group seg-wrap" role="radiogroup" aria-label="Chunk and window profile">
              <label class="seg-pill"><input type="radio" name="window_profile" value="max" data-label="Max Speed" checked>Max Speed</label>
              <label class="seg-pill"><input type="radio" name="window_profile" value="balanced" data-label="Balanced">Balanced</label>
              <label class="seg-pill"><input type="radio" name="window_profile" value="gaming" data-label="Gaming">Gaming</label>
            </div>
          </fieldset>

          <div class="sync-col-3">
            <label class="field-label" for="customPathInput">Custom Camouflage Path</label>
            <form id="customPathForm" class="inline-form" novalidate>
              <input
                type="text"
                id="customPathInput"
                class="text-input"
                value="/ws/sync"
                placeholder="/ws/sync"
                pattern="^/[a-zA-Z0-9._~/-]+$"
                autocomplete="off"
                spellcheck="false"
              >
              <button type="submit" class="btn" id="btnApplyPath">Apply Path</button>
            </form>
            <div class="path-presets" role="group" aria-label="Quick camouflage path presets">
              <button type="button" class="btn btn-ghost btn-xs mono is-active" data-path-preset="/ws/sync" aria-pressed="true">/ws/sync</button>
              <button type="button" class="btn btn-ghost btn-xs mono" data-path-preset="/api/v2/events" aria-pressed="false">/api/v2/events</button>
              <button type="button" class="btn btn-ghost btn-xs mono" data-path-preset="/cdn/assets/sync" aria-pressed="false">/cdn/assets/sync</button>
            </div>
          </div>

          <div class="sync-col-3">
            <span class="field-label">Power Scaling</span>
            <div class="switch-list" role="group" aria-label="Idle power scaling">
              <label class="switch-row" for="chkIdlePower">
                <span class="switch-copy">
                  <span class="switch-title">Idle Power Mode</span>
                  <span class="switch-desc">Scale carriers 4 → 1 when idle &gt; 60s</span>
                </span>
                <input type="checkbox" role="switch" id="chkIdlePower" class="toggle-switch">
              </label>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Left: DNS & Shield Policy -->
    <div class="panel">
      <div class="panel-head">
        <div class="panel-title-group">
          <h2 class="panel-title">DNS &amp; Shield Policy</h2>
        </div>
        <span class="pill pill-mono" id="curDnsPill">--</span>
      </div>

      <div class="panel-body">
        <fieldset>
          <legend class="field-label">Upstream Resolver</legend>
          <div class="radio-grid">
            <label class="radio-card">
              <span class="radio-card-top">
                <span class="radio-card-name">AdGuard</span>
                <input type="radio" name="dns_preset" value="adguard">
              </span>
              <span class="radio-card-ip">94.140.14.14</span>
              <span class="radio-card-meta">DNS AdBlock</span>
            </label>

            <label class="radio-card">
              <span class="radio-card-top">
                <span class="radio-card-name">Cloudflare</span>
                <input type="radio" name="dns_preset" value="cloudflare">
              </span>
              <span class="radio-card-ip">1.1.1.1</span>
              <span class="radio-card-meta">Low Latency</span>
            </label>

            <label class="radio-card">
              <span class="radio-card-top">
                <span class="radio-card-name">Google</span>
                <input type="radio" name="dns_preset" value="google">
              </span>
              <span class="radio-card-ip">8.8.8.8</span>
              <span class="radio-card-meta">Public DNS</span>
            </label>
          </div>
        </fieldset>

        <form id="customDnsForm" class="inline-form" novalidate>
          <label class="sr-only" for="customDnsInput">Custom upstream DNS servers</label>
          <input
            type="text"
            id="customDnsInput"
            class="text-input"
            placeholder="Custom DNS (e.g. 9.9.9.9:53, 1.1.1.2:53)"
            pattern="^[0-9a-zA-Z.:,\s-]+$"
            autocomplete="off"
            spellcheck="false"
          >
          <button type="submit" class="btn" id="btnApplyCustomDns">Apply</button>
        </form>

        <div class="switch-list" role="group" aria-label="Traffic shield policies">
          <label class="switch-row" for="chkBlockDot">
            <span class="switch-copy">
              <span class="switch-title">Block DoT (Port 853)</span>
              <span class="switch-desc">Force client DNS through server resolver policy</span>
            </span>
            <input type="checkbox" role="switch" id="chkBlockDot" class="toggle-switch">
          </label>

          <label class="switch-row" for="chkAdblockSni">
            <span class="switch-copy">
              <span class="switch-title">SNI AdBlock</span>
              <span class="switch-desc">Drop TLS ClientHello handshakes to known ad hosts</span>
            </span>
            <input type="checkbox" role="switch" id="chkAdblockSni" class="toggle-switch">
          </label>
        </div>

        <div class="policy-footer">
          <span class="policy-meta" id="cacheCountMeta">0 cached records</span>
          <button type="button" class="btn btn-ghost" id="btnFlushDns">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <polyline points="3 6 5 6 21 6"/>
              <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
              <path d="M10 11v6"/>
              <path d="M14 11v6"/>
            </svg>
            <span>Flush DNS Cache</span>
          </button>
        </div>
      </div>
    </div>

    <!-- Right: 1-Tap Client Provisioning -->
    <div class="panel">
      <div class="panel-head">
        <div class="panel-title-group">
          <h2 class="panel-title">1-Tap Client Provisioning</h2>
        </div>
        <span class="pill pill-mono" id="tunnelPathPill">/ws/sync</span>
      </div>

      <div class="panel-body">
        <div>
          <label class="field-label" for="fallbackInput">Fallback Tunnel URL</label>
          <input
            type="url"
            id="fallbackInput"
            class="text-input"
            value="wss://sync.novv.tech/ws/sync"
            placeholder="wss://sync.novv.tech/ws/sync"
            autocomplete="off"
            spellcheck="false"
          >
        </div>

        <div>
          <label class="field-label" for="cleanIpInput">Cloudflare Clean IP (Optional)</label>
          <input
            type="text"
            id="cleanIpInput"
            class="text-input"
            placeholder="e.g. 162.159.192.1"
            pattern="^[0-9a-fA-F.:]*$"
            autocomplete="off"
            spellcheck="false"
          >
        </div>

        <div class="prov-profile-bar" aria-live="polite">
          <span class="k">Synced Profile</span>
          <span class="v" id="provProfileSummary">xchacha20-poly1305 · auto · chrome · balanced · 4c</span>
        </div>

        <div class="prov-toolbar">
          <div class="seg-group" role="radiogroup" aria-label="Provisioning format">
            <label class="seg-pill"><input type="radio" name="prov_mode" value="android" checked>Android aether://</label>
            <label class="seg-pill"><input type="radio" name="prov_mode" value="cli">Laptop CLI</label>
          </div>

          <div class="prov-actions">
            <button type="button" class="btn btn-ghost" id="btnToggleSecret" aria-pressed="false">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/>
                <circle cx="12" cy="12" r="3"/>
              </svg>
              <span id="secretBtnLabel">Show Secret</span>
            </button>
            <button type="button" class="btn btn-primary" id="btnCopyProv">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
                <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
              </svg>
              <span id="copyBtnLabel">Copy</span>
            </button>
          </div>
        </div>

        <pre class="code-well" id="provCodeWell" data-mode="android"><code id="provOutput">Waiting for server telemetry...</code></pre>
      </div>
    </div>
  </section>

  <!-- Active Sessions Table -->
  <section class="panel" aria-label="Active sessions">
    <div class="panel-head">
      <div class="panel-title-group">
        <h2 class="panel-title">Active Sessions</h2>
        <span class="panel-sub">Multiplexed carrier tunnels</span>
      </div>
      <span class="pill pill-mono" id="sessCountPill">0 active</span>
    </div>

    <div tabindex="0" role="region" aria-labelledby="sessions-caption" class="table-wrapper">
      <table class="sessions-table">
        <caption id="sessions-caption" class="sr-only">Active VPN Sessions</caption>
        <thead>
          <tr>
            <th scope="col">Session</th>
            <th scope="col">Cipher</th>
            <th scope="col">Carriers</th>
            <th scope="col">TCP</th>
            <th scope="col">UDP / QUIC</th>
            <th scope="col">Down (TX)</th>
            <th scope="col">Up (RX)</th>
            <th scope="col">RTT</th>
            <th scope="col">Age</th>
            <th scope="col">Action</th>
          </tr>
        </thead>
        <tbody id="sessBody">
          <tr class="empty-row"><td colspan="10" class="empty-cell">No active sessions</td></tr>
        </tbody>
      </table>
    </div>
  </section>
</main>

<!-- Popover Toast Notification Region -->
<div role="region" aria-label="Notifications" class="toast-stack">
  <div id="toastPopover" popover="manual" class="toast-card" data-tone="ok">
    <span class="toast-dot" aria-hidden="true"></span>
    <span class="toast-msg" id="toastMsg"></span>
    <button
      type="button"
      class="toast-close"
      id="toastCloseBtn"
      popovertarget="toastPopover"
      popovertargetaction="hide"
      aria-label="Close notification"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <line x1="18" y1="6" x2="6" y2="18"/>
        <line x1="6" y1="6" x2="18" y2="18"/>
      </svg>
    </button>
  </div>
</div>

<p class="sr-only">Screen reader announcements</p>
<div id="srAnnounce" class="sr-only" aria-live="assertive"></div>

<script>
(function () {
  'use strict';

  // Self-contained popover fallback guarded identically to @oddbird/popover-polyfill
  // (keeps the embedded control plane 100% zero-CDN and offline-capable).
  var hasNativePopover = Object.hasOwn(HTMLElement.prototype, "popover");
  if (!Object.hasOwn(HTMLElement.prototype, "popover")) {
    document.addEventListener('click', function (ev) {
      var btn = ev.target && ev.target.closest ? ev.target.closest('[popovertarget]') : null;
      if (!btn) return;
      var target = document.getElementById(btn.getAttribute('popovertarget'));
      if (!target) return;
      var action = btn.getAttribute('popovertargetaction') || 'toggle';
      if (action === 'hide') {
        target.classList.remove(':popover-open');
      } else if (action === 'show') {
        target.classList.add(':popover-open');
      } else {
        target.classList.toggle(':popover-open');
      }
    });
  }

  var base = location.pathname.replace(/\/$/, '');
  var POLL_MS = 1500;
  var MAX_SAMPLES = 60;

  var lastData = null;
  var lastSyncSig = '';
  var lastLocalMutationAt = 0;
  var inFlight = false;
  var failCount = 0;
  var secretVisible = false;
  var fallbackTouched = false;
  var rawAndroidUri = '';
  var rawCliCmd = '';

  var downSeries = new Array(MAX_SAMPLES).fill(0);
  var upSeries = new Array(MAX_SAMPLES).fill(0);
  var hoverIndex = -1;

  // ── Formatting helpers ──────────────────────────────────────────────────
  function fmtBytes(b) {
    if (!Number.isFinite(b) || b <= 0) return '0 B';
    if (b >= 1e9) return (b / 1e9).toFixed(2) + ' GB';
    if (b >= 1e6) return (b / 1e6).toFixed(2) + ' MB';
    if (b >= 1e3) return (b / 1e3).toFixed(1) + ' KB';
    return Math.round(b) + ' B';
  }

  function fmtRate(bps) {
    if (!Number.isFinite(bps) || bps <= 0) return '0.0 KB/s';
    if (bps >= 1e6) return (bps / 1e6).toFixed(2) + ' MB/s';
    return (bps / 1024).toFixed(1) + ' KB/s';
  }

  function fmtUptime(s) {
    s = Math.max(0, Math.floor(s || 0));
    var d = Math.floor(s / 86400);
    var h = Math.floor((s % 86400) / 3600);
    var m = Math.floor((s % 3600) / 60);
    var sec = s % 60;
    if (d > 0) return d + 'd ' + h + 'h ' + m + 'm';
    if (h > 0) return h + 'h ' + m + 'm ' + sec + 's';
    if (m > 0) return m + 'm ' + sec + 's';
    return sec + 's';
  }

  function escHtml(str) {
    return String(str)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  function b64url(str) {
    return btoa(unescape(encodeURIComponent(str)))
      .replace(/\+/g, '-')
      .replace(/\//g, '_')
      .replace(/=+$/, '');
  }

  // ── Popover Toast + Top-Layer Exit Fallback + ariaNotify() ──────────────
  var toastEl = document.getElementById('toastPopover');
  var toastMsgEl = document.getElementById('toastMsg');
  var toastCloseBtn = document.getElementById('toastCloseBtn');
  var srAnnounceEl = document.getElementById('srAnnounce');
  var toastTimer = null;
  var priorFocusEl = null;
  var supportsDisplayTransition;

  function canTransitionDisplay() {
    if (supportsDisplayTransition !== undefined) return supportsDisplayTransition;
    if (!window.CSS || !window.CSS.supports || !window.CSS.supports('transition-behavior', 'allow-discrete') || !document.body) {
      return false;
    }
    var probe = document.createElement('div');
    probe.style.cssText = 'transition: display 1s allow-discrete !important; display: block;';
    document.body.appendChild(probe);
    getComputedStyle(probe).display;
    probe.style.display = 'none';
    supportsDisplayTransition = getComputedStyle(probe).display === 'block';
    probe.remove();
    return supportsDisplayTransition;
  }

  function supportsTopLayerExit() {
    return Boolean(window.CSS && window.CSS.supports && window.CSS.supports('overlay', 'auto') && canTransitionDisplay());
  }

  function isToastOpen() {
    if (hasNativePopover) {
      try { return toastEl.matches(':popover-open'); } catch (e) {}
    }
    return toastEl.classList.contains(':popover-open');
  }

  function hideToastNow(restoreFocus) {
    var hadFocus = toastEl.contains(document.activeElement);
    clearTimeout(toastTimer);
    toastTimer = null;

    function finishHide() {
      if (hasNativePopover && typeof toastEl.hidePopover === 'function') {
        try {
          if (toastEl.matches(':popover-open')) toastEl.hidePopover();
        } catch (e) {}
      } else {
        toastEl.classList.remove(':popover-open');
      }
      if ((restoreFocus || hadFocus) && priorFocusEl && typeof priorFocusEl.focus === 'function' && document.contains(priorFocusEl)) {
        priorFocusEl.focus();
      }
    }

    if (!isToastOpen()) return;
    if (hasNativePopover && !supportsTopLayerExit()) {
      toastEl.setAttribute('data-closing', '');
      var anims = typeof toastEl.getAnimations === 'function' ? toastEl.getAnimations({ subtree: true }) : [];
      if (anims.length > 0) {
        Promise.race([
          Promise.allSettled(anims.map(function (a) { return a.finished; })),
          new Promise(function (r) { setTimeout(r, 400); })
        ]).then(function () {
          if (!toastEl.hasAttribute('data-closing')) return;
          toastEl.removeAttribute('data-closing');
          finishHide();
        });
        return;
      }
      toastEl.removeAttribute('data-closing');
    }
    finishHide();
  }

  function scheduleToastDismiss() {
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () {
      if (toastEl.contains(document.activeElement)) return;
      hideToastNow(false);
    }, 3200);
  }

  function announceSR(msg) {
    if (typeof document.ariaNotify === 'function') {
      try {
        document.ariaNotify(msg);
        return;
      } catch (e) {}
    }
    srAnnounceEl.textContent = '';
    setTimeout(function () {
      srAnnounceEl.textContent = msg;
    }, 20);
  }

  function toast(msg, tone) {
    priorFocusEl = document.activeElement;
    toastEl.removeAttribute('data-closing');
    toastEl.setAttribute('data-tone', tone || 'ok');
    toastMsgEl.textContent = msg;

    if (hasNativePopover && typeof toastEl.showPopover === 'function') {
      try {
        if (!toastEl.matches(':popover-open')) toastEl.showPopover();
      } catch (e) {}
    } else {
      toastEl.classList.add(':popover-open');
    }

    announceSR(msg);
    scheduleToastDismiss();
  }

  toastEl.addEventListener('focusin', function () {
    clearTimeout(toastTimer);
  });

  toastEl.addEventListener('focusout', function () {
    setTimeout(function () {
      if (isToastOpen() && !toastEl.contains(document.activeElement)) {
        scheduleToastDismiss();
      }
    }, 0);
  });

  toastCloseBtn.addEventListener('click', function (ev) {
    if (hasNativePopover && !supportsTopLayerExit()) {
      ev.preventDefault();
      hideToastNow(true);
    } else {
      clearTimeout(toastTimer);
      setTimeout(function () {
        if (priorFocusEl && typeof priorFocusEl.focus === 'function' && document.contains(priorFocusEl)) {
          priorFocusEl.focus();
        }
      }, 0);
    }
  });

  // ── High-DPI Canvas Throughput Chart ────────────────────────────────────
  var canvas = document.getElementById('throughputChart');
  var chartStage = document.getElementById('chartStage');
  var chartTooltip = document.getElementById('chartTooltip');
  var ctx = canvas.getContext('2d');
  var chartW = 0;
  var chartH = 0;
  var pad = { top: 14, right: 12, bottom: 26, left: 48 };

  function niceCeiling(maxVal) {
    if (maxVal <= 0.8) return 1;
    var steps = [2, 5, 10, 20, 50, 100, 200, 500, 1000, 2500, 5000];
    for (var i = 0; i < steps.length; i++) {
      if (maxVal * 1.15 <= steps[i]) return steps[i];
    }
    return Math.ceil(maxVal * 1.2 / 1000) * 1000;
  }

  function resizeChart() {
    var rect = canvas.getBoundingClientRect();
    var dpr = Math.min(window.devicePixelRatio || 1, 3);
    chartW = Math.max(240, Math.floor(rect.width));
    chartH = Math.max(180, Math.floor(rect.height));
    canvas.width = Math.floor(chartW * dpr);
    canvas.height = Math.floor(chartH * dpr);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    drawChart();
  }

  function buildPoints(series, maxY, plotW, plotH) {
    var pts = [];
    var n = series.length;
    for (var i = 0; i < n; i++) {
      var x = pad.left + (i / (n - 1)) * plotW;
      var ratio = Math.min(1, Math.max(0, series[i] / maxY));
      var y = pad.top + plotH - ratio * plotH;
      pts.push({ x: x, y: y });
    }
    return pts;
  }

  function traceSmoothPath(pts) {
    if (pts.length === 0) return;
    ctx.beginPath();
    ctx.moveTo(pts[0].x, pts[0].y);
    for (var i = 0; i < pts.length - 1; i++) {
      var p0 = pts[Math.max(0, i - 1)];
      var p1 = pts[i];
      var p2 = pts[i + 1];
      var p3 = pts[Math.min(pts.length - 1, i + 2)];
      var cp1x = p1.x + (p2.x - p0.x) / 6;
      var cp1y = p1.y + (p2.y - p0.y) / 6;
      var cp2x = p2.x - (p3.x - p1.x) / 6;
      var cp2y = p2.y - (p3.y - p1.y) / 6;
      var minY = Math.min(p1.y, p2.y);
      var maxY = Math.max(p1.y, p2.y);
      cp1y = Math.min(maxY, Math.max(minY, cp1y));
      cp2y = Math.min(maxY, Math.max(minY, cp2y));
      ctx.bezierCurveTo(cp1x, cp1y, cp2x, cp2y, p2.x, p2.y);
    }
  }

  function drawChart() {
    if (!ctx || chartW <= 0 || chartH <= 0) return;
    ctx.clearRect(0, 0, chartW, chartH);

    var plotW = chartW - pad.left - pad.right;
    var plotH = chartH - pad.top - pad.bottom;
    if (plotW <= 10 || plotH <= 10) return;

    var maxSample = 0;
    for (var i = 0; i < MAX_SAMPLES; i++) {
      if (downSeries[i] > maxSample) maxSample = downSeries[i];
      if (upSeries[i] > maxSample) maxSample = upSeries[i];
    }
    var yCeil = niceCeiling(maxSample);

    // Horizontal gridlines & Y-axis labels
    var gridRows = 4;
    ctx.font = '11px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace';
    ctx.textAlign = 'right';
    ctx.textBaseline = 'middle';

    for (var r = 0; r <= gridRows; r++) {
      var frac = r / gridRows;
      var y = Math.round(pad.top + frac * plotH) + 0.5;
      var val = yCeil * (1 - frac);

      ctx.strokeStyle = r === gridRows ? 'rgba(255, 255, 255, 0.12)' : 'rgba(255, 255, 255, 0.055)';
      ctx.lineWidth = 1;
      ctx.setLineDash([]);
      ctx.beginPath();
      ctx.moveTo(pad.left, y);
      ctx.lineTo(pad.left + plotW, y);
      ctx.stroke();

      ctx.fillStyle = '#85858f';
      var lbl = yCeil >= 10 ? Math.round(val).toString() : val.toFixed(1);
      ctx.fillText(lbl + ' M', pad.left - 8, y);
    }

    // X-axis time markers
    var xMarks = [
      { idx: 0, label: '-90s', align: 'left' },
      { idx: 20, label: '-60s', align: 'center' },
      { idx: 40, label: '-30s', align: 'center' },
      { idx: MAX_SAMPLES - 1, label: 'now', align: 'right' }
    ];
    ctx.textBaseline = 'top';
    ctx.fillStyle = '#71717a';
    for (var m = 0; m < xMarks.length; m++) {
      var xm = xMarks[m];
      var xx = pad.left + (xm.idx / (MAX_SAMPLES - 1)) * plotW;
      ctx.textAlign = xm.align;
      ctx.fillText(xm.label, xx, pad.top + plotH + 8);
    }

    var downPts = buildPoints(downSeries, yCeil, plotW, plotH);
    var upPts = buildPoints(upSeries, yCeil, plotW, plotH);

    // Download gradient fill
    ctx.save();
    traceSmoothPath(downPts);
    ctx.lineTo(downPts[downPts.length - 1].x, pad.top + plotH);
    ctx.lineTo(downPts[0].x, pad.top + plotH);
    ctx.closePath();
    var grad = ctx.createLinearGradient(0, pad.top, 0, pad.top + plotH);
    grad.addColorStop(0, 'rgba(16, 185, 129, 0.24)');
    grad.addColorStop(0.75, 'rgba(16, 185, 129, 0.04)');
    grad.addColorStop(1, 'rgba(16, 185, 129, 0.0)');
    ctx.fillStyle = grad;
    ctx.fill();
    ctx.restore();

    // Upload dashed silver curve
    ctx.save();
    traceSmoothPath(upPts);
    ctx.strokeStyle = 'rgba(228, 228, 231, 0.85)';
    ctx.lineWidth = 1.5;
    ctx.setLineDash([4, 3]);
    ctx.stroke();
    ctx.restore();

    // Download solid Signal Emerald curve
    ctx.save();
    traceSmoothPath(downPts);
    ctx.strokeStyle = '#10b981';
    ctx.lineWidth = 2;
    ctx.setLineDash([]);
    ctx.stroke();
    ctx.restore();

    // Live endpoint markers
    var lastDown = downPts[downPts.length - 1];
    var lastUp = upPts[upPts.length - 1];

    ctx.beginPath();
    ctx.arc(lastUp.x, lastUp.y, 2.5, 0, Math.PI * 2);
    ctx.fillStyle = '#e4e4e7';
    ctx.fill();

    ctx.beginPath();
    ctx.arc(lastDown.x, lastDown.y, 6, 0, Math.PI * 2);
    ctx.fillStyle = 'rgba(16, 185, 129, 0.24)';
    ctx.fill();

    ctx.beginPath();
    ctx.arc(lastDown.x, lastDown.y, 3, 0, Math.PI * 2);
    ctx.fillStyle = '#34d399';
    ctx.fill();

    // Hover crosshair & point highlight
    if (hoverIndex >= 0 && hoverIndex < MAX_SAMPLES) {
      var hd = downPts[hoverIndex];
      var hu = upPts[hoverIndex];

      ctx.save();
      ctx.strokeStyle = 'rgba(255, 255, 255, 0.22)';
      ctx.lineWidth = 1;
      ctx.setLineDash([2, 2]);
      ctx.beginPath();
      ctx.moveTo(hd.x + 0.5, pad.top);
      ctx.lineTo(hd.x + 0.5, pad.top + plotH);
      ctx.stroke();
      ctx.restore();

      ctx.beginPath();
      ctx.arc(hu.x, hu.y, 3.5, 0, Math.PI * 2);
      ctx.fillStyle = '#e4e4e7';
      ctx.fill();

      ctx.beginPath();
      ctx.arc(hd.x, hd.y, 4, 0, Math.PI * 2);
      ctx.fillStyle = '#34d399';
      ctx.strokeStyle = '#09090b';
      ctx.lineWidth = 1.5;
      ctx.fill();
      ctx.stroke();
    }
  }

  canvas.addEventListener('pointermove', function (ev) {
    var rect = canvas.getBoundingClientRect();
    var x = ev.clientX - rect.left;
    var plotW = chartW - pad.left - pad.right;
    if (x < pad.left || x > pad.left + plotW) {
      hoverIndex = -1;
      chartTooltip.hidden = true;
      drawChart();
      return;
    }
    var ratio = (x - pad.left) / plotW;
    var idx = Math.max(0, Math.min(MAX_SAMPLES - 1, Math.round(ratio * (MAX_SAMPLES - 1))));
    hoverIndex = idx;
    drawChart();

    var secAgo = Math.round((MAX_SAMPLES - 1 - idx) * (POLL_MS / 1000));
    var timeText = secAgo === 0 ? 'now' : ('-' + secAgo + 's');
    chartTooltip.innerHTML =
      '<div class="chart-tooltip-time">' + timeText + '</div>' +
      '<div class="chart-tooltip-row"><span style="color:#34d399">Down</span><span>' + downSeries[idx].toFixed(2) + ' Mbps</span></div>' +
      '<div class="chart-tooltip-row"><span style="color:#e4e4e7">Up</span><span>' + upSeries[idx].toFixed(2) + ' Mbps</span></div>';
    chartTooltip.hidden = false;

    var stageRect = chartStage.getBoundingClientRect();
    var tipW = chartTooltip.offsetWidth || 132;
    var px = ev.clientX - stageRect.left + 12;
    if (px + tipW > stageRect.width - 8) px = ev.clientX - stageRect.left - tipW - 12;
    var py = Math.max(8, ev.clientY - stageRect.top - 48);
    chartTooltip.style.left = Math.max(8, px) + 'px';
    chartTooltip.style.top = py + 'px';
  });

  canvas.addEventListener('pointerleave', function () {
    hoverIndex = -1;
    chartTooltip.hidden = true;
    drawChart();
  });

  if (typeof ResizeObserver === 'function') {
    new ResizeObserver(resizeChart).observe(canvas);
  } else {
    window.addEventListener('resize', resizeChart);
  }

  // ── Provisioning & Synced Profile Logic ─────────────────────────────────
  function getCheckedVal(name, fallback) {
    var checked = document.querySelector('input[name="' + name + '"]:checked');
    return checked ? checked.value : fallback;
  }

  function setRadioGroup(name, value) {
    if (value === undefined || value === null || value === '') return;
    var target = String(value);
    var radios = document.querySelectorAll('input[name="' + name + '"]');
    for (var i = 0; i < radios.length; i++) {
      radios[i].checked = (radios[i].value === target);
    }
  }

  function getSelectedCarriers() {
    var n = parseInt(getCheckedVal('carriers', '4'), 10);
    return Number.isFinite(n) && n > 0 ? n : 4;
  }

  function getProvisionMode() {
    return getCheckedVal('prov_mode', 'android');
  }

  function updatePathPresetHighlights(activePath) {
    var btns = document.querySelectorAll('[data-path-preset]');
    for (var i = 0; i < btns.length; i++) {
      var match = btns[i].getAttribute('data-path-preset') === activePath;
      btns[i].classList.toggle('is-active', match);
      btns[i].setAttribute('aria-pressed', match ? 'true' : 'false');
    }
  }

  function syncProfileControls(sc) {
    if (!sc) return;
    if (sc.protocol_profile) setRadioGroup('protocol_profile', sc.protocol_profile);
    if (sc.cipher) setRadioGroup('cipher', sc.cipher);
    if (sc.transport) setRadioGroup('transport', sc.transport);
    if (sc.fingerprint) setRadioGroup('fingerprint', sc.fingerprint);
    if (sc.padding_mode) setRadioGroup('padding_mode', sc.padding_mode);
    if (sc.fragment_mode) setRadioGroup('fragment_mode', sc.fragment_mode);
    if (sc.quic_mode) setRadioGroup('quic_mode', sc.quic_mode);
    if (sc.window_profile) setRadioGroup('window_profile', sc.window_profile);
    if (sc.carriers) setRadioGroup('carriers', sc.carriers);
    var idleChk = document.getElementById('chkIdlePower');
    if (idleChk && typeof sc.idle_power === 'boolean') {
      idleChk.checked = sc.idle_power;
    }
    var pathInp = document.getElementById('customPathInput');
    if (pathInp && sc.custom_path && document.activeElement !== pathInp) {
      pathInp.value = sc.custom_path;
    }
    if (sc.custom_path) {
      updatePathPresetHighlights(sc.custom_path);
    }
  }

  function renderLinks() {
    if (!lastData) return;
    var sc = lastData.sync_config || lastData.config || {};
    var fb = document.getElementById('fallbackInput').value.trim();
    var cip = document.getElementById('cleanIpInput').value.trim();
    var protocolProfile = sc.protocol_profile || getCheckedVal('protocol_profile', 'mux');
    var cipher = sc.cipher || getCheckedVal('cipher', 'xchacha20-poly1305');
    var transport = sc.transport || getCheckedVal('transport', 'auto');
    var fingerprint = sc.fingerprint || getCheckedVal('fingerprint', 'chrome');
    var paddingMode = sc.padding_mode || getCheckedVal('padding_mode', 'balanced');
    var fragmentMode = sc.fragment_mode || getCheckedVal('fragment_mode', 'off');
    var quicMode = sc.quic_mode || getCheckedVal('quic_mode', 'full');
    var windowProfile = sc.window_profile || getCheckedVal('window_profile', 'max');
    var defaultPath = (lastData.config && lastData.config.tunnel_path) || '/ws/sync';
    var pathInp = document.getElementById('customPathInput');
    var customPath = sc.custom_path || (pathInp ? pathInp.value.trim() : '') || defaultPath;
    var idleChk = document.getElementById('chkIdlePower');
    var idlePower = typeof sc.idle_power === 'boolean' ? sc.idle_power : Boolean(idleChk && idleChk.checked);
    var carriers = (Number.isFinite(sc.carriers) && sc.carriers > 0) ? sc.carriers : getSelectedCarriers();
    var mode = getProvisionMode();

    var summaryParts = [
      protocolProfile,
      cipher,
      transport,
      fingerprint,
      'pad:' + paddingMode,
      'frag:' + fragmentMode,
      'quic:' + quicMode,
      'win:' + windowProfile,
      carriers + 'c'
    ];
    if (idlePower) summaryParts.push('idle-pwr');

    var summaryEl = document.getElementById('provProfileSummary');
    if (summaryEl) {
      summaryEl.textContent = summaryParts.join(' · ');
    }

    var obj = {
      psk: lastData.psk,
      primary_url: lastData.ws_url,
      protocol_profile: protocolProfile,
      carriers: carriers,
      cipher: cipher,
      transport: transport,
      fingerprint: fingerprint,
      padding_mode: paddingMode,
      fragment_mode: fragmentMode,
      quic_mode: quicMode,
      window_profile: windowProfile,
      custom_path: customPath,
      idle_power: idlePower
    };
    if (fb) obj.fallback_url = fb;
    if (cip) obj.cf_clean_ip = cip;

    rawAndroidUri = 'aether://' + b64url(JSON.stringify(obj));

    var cliFlags = ' -carriers ' + carriers +
      ' -cipher ' + cipher +
      ' -transport ' + transport +
      ' -fingerprint ' + fingerprint +
      ' -padding ' + paddingMode;
    if (fragmentMode && fragmentMode !== 'off') cliFlags += ' -fragment ' + fragmentMode;
    if (windowProfile && windowProfile !== 'max') cliFlags += ' -window ' + windowProfile;
    if (customPath && customPath !== defaultPath) cliFlags += ' -path ' + customPath;
    if (idlePower) cliFlags += ' -idle-power';
    if (fb) cliFlags += ' -fallback ' + fb;

    rawCliCmd = './aether-client -server ' + lastData.ws_url + ' -psk "' + lastData.psk + '"' + cliFlags;

    var outEl = document.getElementById('provOutput');
    var wellEl = document.getElementById('provCodeWell');
    wellEl.setAttribute('data-mode', mode);

    if (mode === 'android') {
      if (secretVisible) {
        outEl.textContent = rawAndroidUri;
      } else {
        outEl.textContent = 'aether://••••••••••••••••••••••••••••••••  (secret hidden · Copy uses real link)';
      }
    } else {
      if (secretVisible) {
        outEl.textContent = rawCliCmd;
      } else {
        outEl.textContent = './aether-client -server ' + lastData.ws_url + ' -psk "••••••••••••••••"' + cliFlags;
      }
    }
  }

  // ── API Actions ─────────────────────────────────────────────────────────
  async function setSyncField(field, rawVal, settingLabel) {
    lastLocalMutationAt = Date.now();
    var val = field === 'carriers' ? parseInt(rawVal, 10) : (field === 'idle_power' ? Boolean(rawVal) : rawVal);
    var payload = {};
    payload[field] = val;

    if (lastData) {
      if (!lastData.sync_config) lastData.sync_config = {};
      lastData.sync_config[field] = val;
      if (lastData.config) lastData.config[field] = val;
      if (field === 'custom_path' && val) {
        document.getElementById('tunnelPathPill').textContent = val;
      }
      renderLinks();
    }

    try {
      var r = await fetch(base + '/api/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      if (!r.ok) {
        toast('Failed to sync ' + settingLabel, 'error');
        poll(false);
        return;
      }
      var res = await r.json();
      if (lastData && res && res.sync_config) {
        lastData.sync_config = res.sync_config;
        if (res.sync_config.custom_path) {
          document.getElementById('tunnelPathPill').textContent = res.sync_config.custom_path;
        }
        syncProfileControls(res.sync_config);
        renderLinks();
      }
      toast('Synced ' + settingLabel + ' to server & connected clients', 'ok');
      poll(false);
    } catch (e) {
      toast('Network error syncing ' + settingLabel, 'error');
      poll(false);
    }
  }

  async function setDNS(preset, customVal) {
    lastLocalMutationAt = Date.now();
    var payload = { dns_preset: preset };
    if (preset === 'custom') payload.custom_dns = customVal;
    try {
      var r = await fetch(base + '/api/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      if (!r.ok) {
        toast('Failed to update DNS policy', 'error');
        return;
      }
      toast(preset === 'custom' ? ('Applied custom DNS: ' + customVal) : ('Switched upstream DNS to ' + preset.toUpperCase()), 'ok');
      poll(false);
    } catch (e) {
      toast('Network error updating DNS', 'error');
    }
  }

  async function setFlag(name, nextVal) {
    lastLocalMutationAt = Date.now();
    var payload = {};
    payload[name] = nextVal;
    try {
      var r = await fetch(base + '/api/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      if (!r.ok) {
        toast('Failed to update shield policy', 'error');
        poll(false);
        return;
      }
      var label = name === 'block_dot' ? 'Block DoT (853)' : 'SNI AdBlock';
      toast(label + ' ' + (nextVal ? 'enabled' : 'disabled'), 'ok');
      poll(false);
    } catch (e) {
      toast('Network error updating shield policy', 'error');
      poll(false);
    }
  }

  async function flushDNS() {
    var btn = document.getElementById('btnFlushDns');
    btn.disabled = true;
    try {
      var r = await fetch(base + '/api/dns/flush', { method: 'POST' });
      if (r.ok) {
        toast('DNS cache flushed', 'ok');
        poll(false);
      } else {
        toast('Failed to flush DNS cache', 'error');
      }
    } catch (e) {
      toast('Network error flushing DNS cache', 'error');
    } finally {
      btn.disabled = false;
    }
  }

  async function kickSession(id, btnEl) {
    if (btnEl) btnEl.disabled = true;
    try {
      var r = await fetch(base + '/api/kick', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: id })
      });
      if (r.ok) {
        toast('Terminated session ' + id, 'warn');
        poll(false);
      } else {
        toast('Session ' + id + ' not found', 'error');
        if (btnEl) btnEl.disabled = false;
      }
    } catch (e) {
      toast('Network error kicking session', 'error');
      if (btnEl) btnEl.disabled = false;
    }
  }

  // ── Sessions Rendering ──────────────────────────────────────────────────
  function renderCipherPill(cipher) {
    var c = cipher || 'xchacha20-poly1305';
    var cls = c === 'none' ? 'cipher-pill plain' : 'cipher-pill';
    var label = c === 'none' ? 'tls-only' : c;
    return '<span class="' + cls + '">' + escHtml(label) + '</span>';
  }

  function renderCarrierDots(count) {
    var slots = count > 4 ? 6 : 4;
    var active = Math.min(slots, Math.max(0, count));
    var html = '<span class="carrier-dots"><span class="c-dots" aria-hidden="true">';
    for (var i = 0; i < slots; i++) {
      html += '<span class="c-dot' + (i < active ? ' on' : '') + '"></span>';
    }
    html += '</span><span class="c-count">' + count + '</span></span>';
    return html;
  }

  function renderRttPill(rtt) {
    if (!Number.isFinite(rtt) || rtt <= 0) {
      return '<span class="rtt-pill">--</span>';
    }
    var cls = rtt < 80 ? 'good' : (rtt <= 180 ? 'warn' : 'bad');
    return '<span class="rtt-pill ' + cls + '">' + rtt + ' ms</span>';
  }

  function renderSessions(sessions) {
    var tb = document.getElementById('sessBody');
    var count = sessions ? sessions.length : 0;
    document.getElementById('sessCountPill').textContent = count + (count === 1 ? ' session' : ' sessions');

    if (!sessions || sessions.length === 0) {
      tb.innerHTML = '<tr class="empty-row"><td colspan="10" class="empty-cell">No active sessions</td></tr>';
      return;
    }

    tb.innerHTML = sessions.map(function (s) {
      var sid = escHtml(s.id);
      return '<tr>' +
        '<th scope="row"><span class="sess-id">' + sid + '</span></th>' +
        '<td>' + renderCipherPill(s.cipher) + '</td>' +
        '<td>' + renderCarrierDots(s.carriers) + '</td>' +
        '<td class="mono">' + s.streams + '</td>' +
        '<td class="mono">' + s.udp_flows + '</td>' +
        '<td class="mono">' + fmtBytes(s.tx_bytes) + '</td>' +
        '<td class="mono">' + fmtBytes(s.rx_bytes) + '</td>' +
        '<td>' + renderRttPill(s.rtt_ms) + '</td>' +
        '<td class="mono">' + fmtUptime(s.age_sec) + '</td>' +
        '<td><button type="button" class="btn btn-danger" data-kick="' + sid + '">Kick</button></td>' +
        '</tr>';
    }).join('');
  }

  // ── Status Indicator ────────────────────────────────────────────────────
  function setConnState(state, text) {
    var pill = document.getElementById('statusPill');
    var txt = document.getElementById('statusText');
    pill.setAttribute('data-state', state);
    txt.textContent = text;
  }

  // ── Telemetry Poll Loop ─────────────────────────────────────────────────
  async function poll(isManual) {
    if (inFlight) return;
    inFlight = true;
    var pollStartedAt = Date.now();
    var refreshBtn = document.getElementById('btnRefresh');
    if (isManual) refreshBtn.classList.add('is-spinning');

    try {
      var r = await fetch(base + '/api/stats', { cache: 'no-store' });
      if (!r.ok) throw new Error('HTTP ' + r.status);
      var d = await r.json();
      failCount = 0;
      setConnState('ok', 'OPERATIONAL');

      var first = !lastData;
      var sc = d.sync_config || d.config || {};
      var staleConfig = lastLocalMutationAt > pollStartedAt;
      if (staleConfig && lastData && lastData.sync_config) {
        d.sync_config = lastData.sync_config;
        sc = d.sync_config;
      }
      lastData = d;

      var fbInp = document.getElementById('fallbackInput');
      if (d.fallback_default && !fallbackTouched && document.activeElement !== fbInp) {
        fbInp.value = d.fallback_default;
      }

      document.getElementById('verPill').textContent = 'v' + d.version;
      var upStr = fmtUptime(d.uptime_sec);
      document.getElementById('uptimePill').textContent = upStr;
      document.getElementById('ramPill').textContent = d.mem_alloc_mb.toFixed(1) + ' MB';
      document.getElementById('goPill').textContent = String(d.goroutines);

      // KPI Matrix
      document.getElementById('kSessions').textContent = String(d.active_sessions);
      document.getElementById('kCarriers').textContent =
        d.active_carriers + ' carriers · ' + d.active_streams + ' TCP · ' + d.active_udp_flows + ' UDP';

      document.getElementById('kTotal').textContent = fmtBytes(d.total_tx_bytes + d.total_rx_bytes);
      document.getElementById('kTotalSub').textContent =
        'Down ' + fmtBytes(d.total_tx_bytes) + ' · Up ' + fmtBytes(d.total_rx_bytes);

      var hitPct = Math.max(0, Math.min(100, d.dns_cache_hit_pct || 0));
      document.getElementById('kDnsHit').textContent = hitPct.toFixed(1) + '%';
      document.getElementById('kDnsBar').style.width = hitPct.toFixed(1) + '%';
      document.getElementById('kDnsSub').textContent =
        d.dns_queries + ' queries · ' + d.dns_cache_size + ' cached · ' + d.dns_aaaa_filtered + ' AAAA';

      document.getElementById('kRuntime').textContent = upStr;
      document.getElementById('kRuntimeSub').textContent =
        d.mem_alloc_mb.toFixed(1) + ' / ' + d.mem_sys_mb.toFixed(1) + ' MB RAM · ' + d.goroutines + ' goroutines';

      document.getElementById('cacheCountMeta').textContent = d.dns_cache_size + ' cached records';
      var activePath = sc.custom_path || (d.config && (d.config.custom_path || d.config.tunnel_path));
      if (activePath) {
        document.getElementById('tunnelPathPill').textContent = activePath;
      }

      // Live throughput directly from server's smooth 3-second monotonic sliding window
      var txBps = Number.isFinite(d.live_down_bps) ? Math.max(0, d.live_down_bps) : 0;
      var rxBps = Number.isFinite(d.live_up_bps) ? Math.max(0, d.live_up_bps) : 0;
      var downMbps = Number.isFinite(d.live_down_mbps) ? Math.max(0, d.live_down_mbps) : ((txBps * 8) / 1e6);
      var upMbps = Number.isFinite(d.live_up_mbps) ? Math.max(0, d.live_up_mbps) : ((rxBps * 8) / 1e6);

      downSeries.push(downMbps);
      if (downSeries.length > MAX_SAMPLES) downSeries.shift();
      upSeries.push(upMbps);
      if (upSeries.length > MAX_SAMPLES) upSeries.shift();

      var peak = 0;
      for (var i = 0; i < MAX_SAMPLES; i++) {
        var combined = downSeries[i] + upSeries[i];
        if (combined > peak) peak = combined;
      }

      document.getElementById('liveDownMbps').textContent = downMbps.toFixed(2);
      document.getElementById('liveDownRate').textContent = fmtRate(txBps);
      document.getElementById('liveUpMbps').textContent = upMbps.toFixed(2);
      document.getElementById('liveUpRate').textContent = fmtRate(rxBps);
      document.getElementById('livePeakMbps').textContent = peak.toFixed(2);
      document.getElementById('liveTotalMbps').textContent = 'Combined: ' + (downMbps + upMbps).toFixed(2) + ' Mbps';
      drawChart();

      // Sync DNS, Shield & Tunnel Profile controls from server state
      if (!staleConfig) {
        var dnsList = sc.upstream_dns || (d.config && d.config.upstream_dns) || [];
        var dnsStr = dnsList.join(', ');
        document.getElementById('curDnsPill').textContent = dnsStr || 'Direct';

        var presetVal = sc.dns_preset || '';
        if (!presetVal) {
          if (dnsStr.indexOf('94.140.') !== -1) presetVal = 'adguard';
          else if (dnsStr.indexOf('1.1.1.1') !== -1) presetVal = 'cloudflare';
          else if (dnsStr.indexOf('8.8.8.8') !== -1) presetVal = 'google';
        }
        var radios = document.querySelectorAll('input[name="dns_preset"]');
        for (var rIdx = 0; rIdx < radios.length; rIdx++) {
          radios[rIdx].checked = (radios[rIdx].value === presetVal);
        }

        var customDnsInp = document.getElementById('customDnsInput');
        if (sc.custom_dns && document.activeElement !== customDnsInp) {
          customDnsInp.value = sc.custom_dns;
        }

        document.getElementById('chkBlockDot').checked = Boolean(sc.block_dot);
        document.getElementById('chkAdblockSni').checked = Boolean(sc.adblock_sni);

        syncProfileControls(sc);

        var nextSyncSig = [
          sc.protocol_profile || '',
          sc.cipher || '',
          sc.transport || '',
          sc.fingerprint || '',
          sc.padding_mode || '',
          sc.fragment_mode || '',
          sc.quic_mode || '',
          sc.window_profile || '',
          sc.custom_path || '',
          sc.idle_power ? '1' : '0',
          sc.carriers || '',
          d.fallback_default || ''
        ].join('|');

        if (first || nextSyncSig !== lastSyncSig) {
          lastSyncSig = nextSyncSig;
          renderLinks();
        }
      } else if (first) {
        renderLinks();
      }

      renderSessions(d.sessions);
      if (isManual) toast('Telemetry refreshed', 'ok');
    } catch (e) {
      failCount++;
      if (failCount >= 3) {
        setConnState('offline', 'OFFLINE');
      } else {
        setConnState('degraded', 'RECONNECTING');
      }
      if (isManual) toast('Failed to fetch server stats', 'error');
    } finally {
      inFlight = false;
      if (isManual) {
        setTimeout(function () { refreshBtn.classList.remove('is-spinning'); }, 220);
      }
    }
  }

  // ── Event Wiring ────────────────────────────────────────────────────────
  document.getElementById('btnRefresh').addEventListener('click', function () {
    poll(true);
  });

  var dnsRadios = document.querySelectorAll('input[name="dns_preset"]');
  for (var i = 0; i < dnsRadios.length; i++) {
    dnsRadios[i].addEventListener('change', function (ev) {
      if (ev.target.checked) setDNS(ev.target.value);
    });
  }

  document.getElementById('customDnsForm').addEventListener('submit', function (ev) {
    ev.preventDefault();
    var inp = document.getElementById('customDnsInput');
    var val = inp.value.trim();
    if (!val || !inp.checkValidity()) {
      inp.focus();
      toast('Enter a valid DNS host:port (e.g. 9.9.9.9:53)', 'warn');
      return;
    }
    setDNS('custom', val);
  });

  document.getElementById('chkBlockDot').addEventListener('change', function (ev) {
    setFlag('block_dot', ev.target.checked);
  });

  document.getElementById('chkAdblockSni').addEventListener('change', function (ev) {
    setFlag('adblock_sni', ev.target.checked);
  });

  document.getElementById('btnFlushDns').addEventListener('click', flushDNS);

  document.getElementById('fallbackInput').addEventListener('input', function () {
    fallbackTouched = true;
    renderLinks();
  });

  document.getElementById('cleanIpInput').addEventListener('input', renderLinks);

  var syncGroups = [
    { name: 'protocol_profile', title: 'Protocol Profile' },
    { name: 'cipher', title: 'Cipher' },
    { name: 'transport', title: 'Transport' },
    { name: 'fingerprint', title: 'Fingerprint' },
    { name: 'padding_mode', title: 'Padding' },
    { name: 'fragment_mode', title: 'TLS Fragmentation' },
    { name: 'quic_mode', title: 'QUIC / UDP' },
    { name: 'window_profile', title: 'Chunk & Window' },
    { name: 'carriers', title: 'Carriers' }
  ];
  for (var sg = 0; sg < syncGroups.length; sg++) {
    (function (grp) {
      var inputs = document.querySelectorAll('input[name="' + grp.name + '"]');
      for (var k = 0; k < inputs.length; k++) {
        inputs[k].addEventListener('change', function (ev) {
          if (!ev.target.checked) return;
          var lbl = ev.target.getAttribute('data-label') || ev.target.value;
          setSyncField(grp.name, ev.target.value, grp.title + ' (' + lbl + ')');
        });
      }
    })(syncGroups[sg]);
  }

  document.getElementById('chkIdlePower').addEventListener('change', function (ev) {
    setSyncField('idle_power', ev.target.checked, 'Idle Power (' + (ev.target.checked ? 'On' : 'Off') + ')');
  });

  function applyCustomPath(raw) {
    var val = (raw || '').trim();
    if (!val) {
      toast('Enter a camouflage path (e.g. /ws/sync)', 'warn');
      return;
    }
    if (val.charAt(0) !== '/') val = '/' + val;
    val = val.replace(/\/+$/, '');
    if (!val || val === '/health' || !/^\/[a-zA-Z0-9._~/-]+$/.test(val)) {
      toast('Invalid camouflage path (avoid /health or spaces)', 'warn');
      return;
    }
    var inp = document.getElementById('customPathInput');
    if (inp) inp.value = val;
    updatePathPresetHighlights(val);
    setSyncField('custom_path', val, 'Camouflage Path (' + val + ')');
  }

  document.getElementById('customPathForm').addEventListener('submit', function (ev) {
    ev.preventDefault();
    var inp = document.getElementById('customPathInput');
    applyCustomPath(inp ? inp.value : '');
  });

  var pathPresetBtns = document.querySelectorAll('[data-path-preset]');
  for (var pp = 0; pp < pathPresetBtns.length; pp++) {
    pathPresetBtns[pp].addEventListener('click', function (ev) {
      var presetPath = ev.currentTarget.getAttribute('data-path-preset');
      applyCustomPath(presetPath);
    });
  }

  var modeRadios = document.querySelectorAll('input[name="prov_mode"]');
  for (var m = 0; m < modeRadios.length; m++) {
    modeRadios[m].addEventListener('change', renderLinks);
  }

  document.getElementById('btnToggleSecret').addEventListener('click', function () {
    secretVisible = !secretVisible;
    this.setAttribute('aria-pressed', secretVisible ? 'true' : 'false');
    document.getElementById('secretBtnLabel').textContent = secretVisible ? 'Hide Secret' : 'Show Secret';
    renderLinks();
  });

  document.getElementById('btnCopyProv').addEventListener('click', function () {
    var mode = getProvisionMode();
    var text = mode === 'android' ? rawAndroidUri : rawCliCmd;
    if (!text) {
      toast('Payload not ready yet', 'warn');
      return;
    }
    var msg = mode === 'android' ? 'Copied Android aether:// link' : 'Copied Laptop CLI command';
    var copyLabel = document.getElementById('copyBtnLabel');

    function onCopied() {
      copyLabel.textContent = 'Copied';
      toast(msg, 'ok');
      setTimeout(function () { copyLabel.textContent = 'Copy'; }, 1600);
    }

    if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
      navigator.clipboard.writeText(text).then(onCopied).catch(function () {
        toast('Clipboard permission denied', 'error');
      });
    } else {
      var ta = document.createElement('textarea');
      ta.value = text;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      try {
        document.execCommand('copy');
        onCopied();
      } catch (e) {
        toast('Unable to copy to clipboard', 'error');
      }
      ta.remove();
    }
  });

  document.getElementById('sessBody').addEventListener('click', function (ev) {
    var btn = ev.target && ev.target.closest ? ev.target.closest('button[data-kick]') : null;
    if (!btn) return;
    kickSession(btn.getAttribute('data-kick'), btn);
  });

  // ── Init ────────────────────────────────────────────────────────────────
  resizeChart();
  poll(false);
  setInterval(function () { poll(false); }, POLL_MS);
})();
</script>
</body>
</html>`
