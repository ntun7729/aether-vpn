# AetherVPN — Custom Multi-Path Encrypted VPN

Custom binary VPN protocol (`Aether v1`), server, desktop/laptop proxy client, Cloudflare Worker clean-IP relay, and native Android `VpnService` app designed specifically to outperform Xray on **Railway**, **Render**, and **Cloudflare Workers**.

## Why AetherVPN Beats Xray on Railway / Render / Cloudflare

- **End-to-End Encrypted (X25519 + XChaCha20-Poly1305 + PSK):** Even when routed through Cloudflare Workers or PaaS edge proxies, relays only see opaque encrypted binary frames with anti-DPI random padding.
- **Multi-Path Carrier Striping (Parallel WebSockets):** Multiplexes TCP & UDP across `N` parallel WebSocket carriers simultaneously instead of stalling on a single connection.
- **Zero-Drop Carrier Recovery & Automatic Railway → Render Failover:** Reliable sequence numbers + replay buffer recover dropped carriers without killing active streams, and the client automatically fails over from Railway (primary) to Render (fallback) or Cloudflare Worker on outage.
- **Full-Cone UDP Relay + Native Android TUN (`gVisor` netstack):** Real L3 VPN on Android (`VpnService`) with full TCP, UDP, and DNS tunneling—no third-party proxy apps needed.
- **Active-Probe Resistant:** Unauthenticated HTTP requests get a normal decoy webpage; `/health` returns `200 OK` for Railway/Render healthchecks.

---

## 1. Deploy the Server (Any Docker Platform — Railway / Render / Koyeb / Fly / VPS)

Prebuilt multi-arch Docker image (`linux/amd64`, `linux/arm64`) on GitHub Container Registry:

```text
ghcr.io/ntun7729/aether-server:latest
```

Run on any Docker host:
```bash
docker run -d --name aether-server -p 8080:8080 \
  -e AETHER_PSK="your-secret-key" \
  -e TUNNEL_PATH="/ws/sync" \
  ghcr.io/ntun7729/aether-server:latest
```

### Option A: Railway (Primary)
1. Push this repo to GitHub.
2. In **Railway**, click **New Project → Deploy from GitHub Repo** and select `aether-vpn`.
3. Railway automatically detects `Dockerfile` and `railway.json` (healthcheck `/health`).
4. Add environment variable:
   - `AETHER_PSK` = `<your-long-random-secret>` (generate one with `openssl rand -hex 32`)
   - `TUNNEL_PATH` = `/ws/sync` (optional, default `/ws/sync`)
5. Generate a public domain in Railway Networking → your primary URL is:
   `wss://<your-railway-domain>/ws/sync`

### Option B: Render (Fallback if Railway runs out)
1. In **Render**, click **New → Blueprint** and connect the same GitHub repo.
2. Render reads `render.yaml`, builds `Dockerfile`, and auto-generates `AETHER_PSK` (or set it to match your Railway `AETHER_PSK` so failover is seamless!).
3. Your fallback URL is:
   `wss://<your-render-domain>.onrender.com/ws/sync`

### Option C: Optional Cloudflare Worker Clean-IP Front (`cf-worker/`)
If your ISP ever throttles Railway/Render domains, deploy `cf-worker/` to Cloudflare Workers:
1. Edit `cf-worker/wrangler.toml` with your Railway + Render URLs in `UPSTREAM_URLS`.
2. Deploy with `npx wrangler deploy`.
3. Put your Worker URL (and optional Cloudflare clean IP) in the Android app or desktop config.

---

## 2. Build the Android APK for Free (GitHub Actions — No Android Studio Needed)

1. Push this folder (`aether-vpn/`) to a GitHub repository.
2. Open the **Actions** tab → **Build Android APK** runs automatically (or click **Run workflow**).
3. In ~4 minutes, download **AetherVPN-Android-APK** (`app-release.apk`) from the workflow run artifacts directly onto your Android phone and install it.
4. Open **AetherVPN** on Android, paste your:
   - **Primary Server URL** (`wss://<railway-domain>/ws/sync`)
   - **Fallback Server URL** (`wss://<render-domain>/ws/sync`)
   - **Secret Key** (`AETHER_PSK`)
5. Tap **CONNECT**.

---

## 3. Run on Laptop / Desktop (SOCKS5 + HTTP Proxy)

```bash
./aether-client \
  -server wss://<your-railway-domain>/ws/sync \
  -fallback wss://<your-render-domain>/ws/sync \
  -psk "<your-AETHER_PSK>" \
  -carriers 3 \
  -listen 127.0.0.1:1080
```

Then point any browser or tool to `socks5h://127.0.0.1:1080` or `http://127.0.0.1:1080`.
