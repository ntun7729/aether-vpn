// Lightweight edge sync gateway for real-time collaborative notes (WS, XHTTP, gRPC, H2, custom paths).

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    if (url.pathname === "/health") {
      return new Response(JSON.stringify({ status: "ok" }), {
        headers: { "content-type": "application/json" },
      });
    }

    const isRealtime = (request.headers.get("Upgrade") || "").toLowerCase() === "websocket";
    const isBatch = url.pathname.endsWith("/h2") && request.method === "POST";
    const isSplit = url.pathname.endsWith("/xhttp") && (request.method === "GET" || request.method === "POST");
    const isRpc = url.pathname === "/aether.v1.SyncService/Tunnel" && request.method === "POST";

    if ((url.pathname !== "/" && isRealtime) || isBatch || isSplit || isRpc) {
      const origins = (env.ORIGIN_POOL || "")
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);

      if (origins.length === 0) {
        return new Response("Origin not configured", { status: 503 });
      }

      let lastErr = null;
      for (const originBase of origins) {
        try {
          const target = new URL(url.pathname + url.search, originBase);
          const headers = new Headers(request.headers);
          headers.set("Host", target.host);
          const resp = await fetch(target.toString(), {
            method: request.method,
            headers,
            body: request.method === "POST" ? request.body : undefined,
          });
          if (isRealtime && resp.status === 101 && resp.webSocket) {
            return resp;
          }
          if (!isRealtime && resp.ok) {
            return resp;
          }
          lastErr = `HTTP ${resp.status}`;
        } catch (err) {
          lastErr = String(err);
        }
      }
      return new Response(`Origin unavailable: ${lastErr}`, { status: 502 });
    }

    return new Response(
      `<!doctype html><html><head><title>Note Sync</title></head><body><h1>Note Sync</h1><p>Service Ready</p></body></html>`,
      { headers: { "content-type": "text/html; charset=utf-8" } }
    );
  },
};
