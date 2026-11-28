package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/aether-vpn/aether/core/adblock"
)

const serverVersion = "3.1.0"

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

	case subPath == "/api/config" && r.Method == http.MethodPost:
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

	s.cfgMu.RLock()
	upDNS := append([]string(nil), s.upstreamDNS...)
	blockDoT := s.blockDoT
	adblockSNI := s.adblockSNI
	s.cfgMu.RUnlock()

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
		"dns_queries":       dnsQ,
		"dns_cache_hits":    dnsHits,
		"dns_aaaa_filtered": dnsAAAA,
		"dns_cache_size":    s.udpDNSCache.Size(),
		"dns_cache_hit_pct": hitPct,
		"ws_url":            wsURL,
		"psk":               string(s.psk),
		"config": map[string]any{
			"tunnel_path":  s.path,
			"upstream_dns": upDNS,
			"block_dot":    blockDoT,
			"adblock_sni":  adblockSNI,
		},
		"sessions": sessions,
	})
}

func (s *Server) handleAPIConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DNSPreset  string `json:"dns_preset"` // adguard | cloudflare | google | custom
		CustomDNS  string `json:"custom_dns"`
		BlockDoT   *bool  `json:"block_dot"`
		AdblockSNI *bool  `json:"adblock_sni"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}

	s.cfgMu.Lock()
	if body.DNSPreset != "" {
		switch strings.ToLower(body.DNSPreset) {
		case "adguard":
			s.upstreamDNS = []string{"94.140.14.14:53", "94.140.15.15:53"}
		case "cloudflare":
			s.upstreamDNS = []string{"1.1.1.1:53", "1.0.0.1:53"}
		case "google":
			s.upstreamDNS = []string{"8.8.8.8:53", "8.8.4.4:53"}
		case "custom":
			if parsed := parseUpstreams(body.CustomDNS); len(parsed) > 0 {
				s.upstreamDNS = parsed
			}
		}
		s.udpDNSCache.Flush()
	}
	if body.BlockDoT != nil {
		s.blockDoT = *body.BlockDoT
	}
	if body.AdblockSNI != nil {
		s.adblockSNI = *body.AdblockSNI
		s.blocker = adblock.New(*body.AdblockSNI)
	}
	up := append([]string(nil), s.upstreamDNS...)
	bdot := s.blockDoT
	asni := s.adblockSNI
	s.cfgMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"upstream_dns": up,
		"block_dot":    bdot,
		"adblock_sni":  asni,
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
<title>AetherVPN Console</title>
<style>
  :root{--bg:#0b0f19;--card:#131a2a;--border:#1f2a44;--text:#e6edf7;--muted:#8b9bb4;--accent:#3b82f6;--green:#22c55e;--red:#ef4444;--amber:#f59e0b}
  *{box-sizing:border-box;margin:0;padding:0}
  body{font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;background:var(--bg);color:var(--text);padding:20px;max-width:1100px;margin:0 auto;line-height:1.5}
  header{display:flex;flex-wrap:wrap;justify-content:space-between;align-items:center;gap:12px;margin-bottom:20px;padding-bottom:16px;border-bottom:1px solid var(--border)}
  .brand{display:flex;align-items:center;gap:10px}
  .dot{width:10px;height:10px;border-radius:50%;background:var(--green);box-shadow:0 0 10px var(--green)}
  h1{font-size:20px;font-weight:700;letter-spacing:-0.3px}
  .pill{font-size:12px;padding:4px 10px;border-radius:999px;background:#1b2640;color:var(--muted);border:1px solid var(--border)}
  .grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(210px,1fr));gap:14px;margin-bottom:18px}
  .card{background:var(--card);border:1px solid var(--border);border-radius:14px;padding:16px}
  .label{font-size:12px;color:var(--muted);text-transform:uppercase;letter-spacing:0.6px;margin-bottom:6px}
  .val{font-size:24px;font-weight:700}
  .sub{font-size:12px;color:var(--muted);margin-top:4px}
  .section{background:var(--card);border:1px solid var(--border);border-radius:14px;padding:18px;margin-bottom:16px}
  .section h2{font-size:15px;margin-bottom:12px;display:flex;justify-content:space-between;align-items:center}
  .row{display:flex;flex-wrap:wrap;gap:10px;align-items:center}
  button{background:#1d2942;color:var(--text);border:1px solid var(--border);padding:8px 14px;border-radius:8px;cursor:pointer;font-size:13px;font-weight:600;transition:.15s}
  button:hover{border-color:var(--accent);background:#243354}
  button.primary{background:var(--accent);border-color:var(--accent);color:#fff}
  button.primary:hover{background:#2563eb}
  button.active{background:rgba(34,197,94,.16);border-color:var(--green);color:#86efac}
  button.danger{background:rgba(239,68,68,.15);border-color:var(--red);color:#fca5a5;padding:5px 10px;font-size:12px}
  input{background:#0d1322;border:1px solid var(--border);color:var(--text);padding:8px 12px;border-radius:8px;font-size:13px;flex:1;min-width:180px}
  pre{background:#0a0f1c;border:1px solid var(--border);padding:12px;border-radius:8px;font-size:12px;overflow-x:auto;color:#93c5fd;margin-top:10px;word-break:break-all;white-space:pre-wrap}
  table{width:100%;border-collapse:collapse;font-size:13px}
  th,td{text-align:left;padding:10px 8px;border-bottom:1px solid var(--border)}
  th{color:var(--muted);font-size:11px;text-transform:uppercase}
  .toast{position:fixed;bottom:20px;right:20px;background:#1e293b;border:1px solid var(--accent);padding:10px 16px;border-radius:10px;font-size:13px;opacity:0;transition:.2s;pointer-events:none}
  .toast.show{opacity:1}
</style>
</head>
<body>
<header>
  <div class="brand">
    <div class="dot" id="statusDot"></div>
    <h1>AetherVPN Server Console</h1>
    <span class="pill" id="verPill">v2.3.0</span>
  </div>
  <div class="row">
    <span class="pill" id="uptimePill">Uptime: --</span>
    <span class="pill" id="sysPill">RAM: -- | Goroutines: --</span>
  </div>
</header>

<div class="grid">
  <div class="card">
    <div class="label">Active Connections</div>
    <div class="val" id="kSessions">0</div>
    <div class="sub" id="kCarriers">0 carriers · 0 TCP · 0 UDP</div>
  </div>
  <div class="card">
    <div class="label">Live Speed</div>
    <div class="val" id="kSpeed">0.0 Mbps</div>
    <div class="sub" id="kSpeedSub">Down: 0 KB/s · Up: 0 KB/s</div>
  </div>
  <div class="card">
    <div class="label">Total Bandwidth</div>
    <div class="val" id="kTotal">0 MB</div>
    <div class="sub" id="kTotalSub">TX: 0 MB · RX: 0 MB</div>
  </div>
  <div class="card">
    <div class="label">DNS & AdGuard Cache</div>
    <div class="val" id="kDns">0</div>
    <div class="sub" id="kDnsSub">Hit rate: 0% · AAAA filtered: 0</div>
  </div>
</div>

<div class="section">
  <h2>Runtime DNS & Filter Controls <span class="pill" id="curDnsPill">--</span></h2>
  <div class="row">
    <button id="btnAdguard" onclick="setDNS('adguard')">AdGuard DNS (AdBlock)</button>
    <button id="btnCloudflare" onclick="setDNS('cloudflare')">Cloudflare (1.1.1.1)</button>
    <button id="btnGoogle" onclick="setDNS('google')">Google (8.8.8.8)</button>
    <button id="btnDot" onclick="toggleFlag('block_dot')">Block DoT 853: ON</button>
    <button id="btnSni" onclick="toggleFlag('adblock_sni')">SNI AdBlock: OFF</button>
    <button onclick="flushDNS()">Flush DNS Cache</button>
  </div>
</div>

<div class="section">
  <h2>1-Tap Client Provisioning (Android & Laptop)</h2>
  <div class="row" style="margin-bottom:10px">
    <input id="fallbackInput" placeholder="Optional Fallback URL (e.g. wss://your-render.onrender.com/ws/sync)" oninput="renderLinks()">
    <input id="cleanIpInput" placeholder="Optional Cloudflare Clean IP" oninput="renderLinks()" style="max-width:220px">
    <button class="primary" onclick="copyText('shareUriBox','Copied aether:// link!')">Copy aether:// Link</button>
    <button onclick="copyText('cliBox','Copied CLI command!')">Copy Laptop CLI</button>
  </div>
  <pre id="shareUriBox">Loading...</pre>
  <pre id="cliBox">Loading...</pre>
</div>

<div class="section">
  <h2>Active Sessions</h2>
  <div style="overflow-x:auto">
    <table>
      <thead><tr><th>Session</th><th>Carriers</th><th>TCP</th><th>UDP/QUIC</th><th>Down (TX)</th><th>Up (RX)</th><th>RTT</th><th>Age</th><th>Action</th></tr></thead>
      <tbody id="sessBody"><tr><td colspan="9" style="color:var(--muted)">No active sessions</td></tr></tbody>
    </table>
  </div>
</div>

<div class="toast" id="toast"></div>

<script>
const base = location.pathname.replace(/\/$/, '');
let lastData = null, prevTx = 0, prevRx = 0, prevTime = 0;

function fmtBytes(b) {
  if (b >= 1e9) return (b/1e9).toFixed(2) + ' GB';
  if (b >= 1e6) return (b/1e6).toFixed(2) + ' MB';
  if (b >= 1e3) return (b/1e3).toFixed(1) + ' KB';
  return b + ' B';
}
function fmtUptime(s) {
  const h = Math.floor(s/3600), m = Math.floor((s%3600)/60), sec = s%60;
  return (h?h+'h ':'') + (m?m+'m ':'') + sec + 's';
}
function toast(msg) {
  const t = document.getElementById('toast');
  t.textContent = msg; t.classList.add('show');
  setTimeout(() => t.classList.remove('show'), 2200);
}
function copyText(id, msg) {
  navigator.clipboard.writeText(document.getElementById(id).textContent).then(() => toast(msg));
}
function b64url(str) {
  return btoa(unescape(encodeURIComponent(str))).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'');
}
function renderLinks() {
  if (!lastData) return;
  const fb = document.getElementById('fallbackInput').value.trim();
  const cip = document.getElementById('cleanIpInput').value.trim();
  const obj = { psk: lastData.psk, primary_url: lastData.ws_url, carriers: 3, transport: 'auto' };
  if (fb) obj.fallback_url = fb;
  if (cip) obj.cf_clean_ip = cip;
  document.getElementById('shareUriBox').textContent = 'aether://' + b64url(JSON.stringify(obj));
  let cli = './aether-client -server ' + lastData.ws_url + ' -psk "' + lastData.psk + '" -carriers 3';
  if (fb) cli += ' -fallback ' + fb;
  document.getElementById('cliBox').textContent = cli;
}

async function setDNS(preset) {
  const r = await fetch(base + '/api/config', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({dns_preset: preset})});
  if (r.ok) { toast('Switched DNS to ' + preset.toUpperCase()); poll(); }
}
async function toggleFlag(name) {
  if (!lastData) return;
  const next = !lastData.config[name];
  const r = await fetch(base + '/api/config', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({[name]: next})});
  if (r.ok) { toast(name + ' = ' + (next ? 'ON' : 'OFF')); poll(); }
}
async function flushDNS() {
  const r = await fetch(base + '/api/dns/flush', {method:'POST'});
  if (r.ok) { toast('DNS cache flushed'); poll(); }
}
async function kick(id) {
  const r = await fetch(base + '/api/kick', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({session_id: id})});
  if (r.ok) { toast('Kicked session ' + id); poll(); }
}

async function poll() {
  try {
    const r = await fetch(base + '/api/stats');
    if (!r.ok) return;
    const d = await r.json();
    const first = !lastData;
    lastData = d;
    if (first) renderLinks();

    document.getElementById('verPill').textContent = 'v' + d.version;
    document.getElementById('uptimePill').textContent = 'Uptime: ' + fmtUptime(d.uptime_sec);
    document.getElementById('sysPill').textContent = 'RAM: ' + d.mem_alloc_mb.toFixed(1) + ' MB | Goroutines: ' + d.goroutines;
    document.getElementById('kSessions').textContent = d.active_sessions;
    document.getElementById('kCarriers').textContent = d.active_carriers + ' carriers · ' + d.active_streams + ' TCP · ' + d.active_udp_flows + ' UDP';
    document.getElementById('kTotal').textContent = fmtBytes(d.total_tx_bytes + d.total_rx_bytes);
    document.getElementById('kTotalSub').textContent = 'Down: ' + fmtBytes(d.total_tx_bytes) + ' · Up: ' + fmtBytes(d.total_rx_bytes);
    document.getElementById('kDns').textContent = d.dns_queries;
    document.getElementById('kDnsSub').textContent = 'Hit: ' + d.dns_cache_hit_pct.toFixed(0) + '% (' + d.dns_cache_size + ' cached) · AAAA: ' + d.dns_aaaa_filtered;

    const now = Date.now();
    if (prevTime > 0) {
      const dt = Math.max(0.5, (now - prevTime) / 1000);
      const txBps = Math.max(0, (d.total_tx_bytes - prevTx) / dt);
      const rxBps = Math.max(0, (d.total_rx_bytes - prevRx) / dt);
      const totalMbps = ((txBps + rxBps) * 8 / 1e6).toFixed(2);
      document.getElementById('kSpeed').textContent = totalMbps + ' Mbps';
      document.getElementById('kSpeedSub').textContent = 'Down: ' + (txBps/1024).toFixed(1) + ' KB/s · Up: ' + (rxBps/1024).toFixed(1) + ' KB/s';
    }
    prevTx = d.total_tx_bytes; prevRx = d.total_rx_bytes; prevTime = now;

    const dnsStr = (d.config.upstream_dns || []).join(', ');
    document.getElementById('curDnsPill').textContent = dnsStr || 'Direct';
    document.getElementById('btnAdguard').className = dnsStr.includes('94.140.') ? 'active' : '';
    document.getElementById('btnCloudflare').className = dnsStr.includes('1.1.1.1') ? 'active' : '';
    document.getElementById('btnGoogle').className = dnsStr.includes('8.8.8.8') ? 'active' : '';
    const bDot = document.getElementById('btnDot');
    bDot.textContent = 'Block DoT 853: ' + (d.config.block_dot ? 'ON' : 'OFF');
    bDot.className = d.config.block_dot ? 'active' : '';
    const bSni = document.getElementById('btnSni');
    bSni.textContent = 'SNI AdBlock: ' + (d.config.adblock_sni ? 'ON' : 'OFF');
    bSni.className = d.config.adblock_sni ? 'active' : '';

    const tb = document.getElementById('sessBody');
    if (!d.sessions || d.sessions.length === 0) {
      tb.innerHTML = '<tr><td colspan="9" style="color:var(--muted)">No active sessions</td></tr>';
    } else {
      tb.innerHTML = d.sessions.map(s =>
        '<tr><td><code>' + s.id + '</code></td><td>' + s.carriers + '</td><td>' + s.streams + '</td><td>' + s.udp_flows + '</td><td>' +
        fmtBytes(s.tx_bytes) + '</td><td>' + fmtBytes(s.rx_bytes) + '</td><td>' + s.rtt_ms + 'ms</td><td>' + fmtUptime(s.age_sec) +
        '</td><td><button class="danger" onclick="kick(\'' + s.id + '\')">Kick</button></td></tr>'
      ).join('');
    }
  } catch(e) {}
}
poll();
setInterval(poll, 2000);
</script>
</body>
</html>`
