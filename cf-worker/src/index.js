// Thin transparent WebSocket + HTTP/2 passthrough relay for Cloudflare Workers.
// Put Cloudflare Anycast clean IPs in front of Railway or Render with zero protocol changes.
// Because AetherVPN is end-to-end encrypted (X25519 + XChaCha20-Poly1305), Cloudflare only sees opaque frames.

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    if (url.pathname === "/health") {
      return new Response(JSON.stringify({ status: "ok" }), {
        headers: { "content-type": "application/json" },
      });
    }

    const tunnelPath = env.TUNNEL_PATH || "/ws/sync";
    const h2Path = tunnelPath.replace(/\/$/, "") + "/h2";
    const isUpgrade = (request.headers.get("Upgrade") || "").toLowerCase() === "websocket";
    const isH2 = url.pathname === h2Path && request.method === "POST";

    if ((url.pathname === tunnelPath && isUpgrade) || isH2) {
      const upstreams = (env.UPSTREAM_URLS || "")
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);

      if (upstreams.length === 0) {
        return new Response("UPSTREAM_URLS not configured", { status: 503 });
      }

      let lastErr = null;
      for (const base of upstreams) {
        try {
          const target = new URL(url.pathname, base);
          const headers = new Headers(request.headers);
          headers.set("Host", target.host);
          const resp = await fetch(target.toString(), {
            method: request.method,
            headers,
            body: isH2 ? request.body : undefined,
          });
          if (isUpgrade && resp.status === 101 && resp.webSocket) {
            return resp;
          }
          if (isH2 && resp.ok) {
            return resp;
          }
          lastErr = `HTTP ${resp.status}`;
        } catch (err) {
          lastErr = String(err);
        }
      }
      return new Response(`Upstream unavailable: ${lastErr}`, { status: 502 });
    }

    return new Response(
      `<!doctype html><html><head><title>Edge Sync</title></head><body><h1>Edge Sync</h1><p>OK</p></body></html>`,
      { headers: { "content-type": "text/html; charset=utf-8" } }
    );
  },
};
