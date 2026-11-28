package com.aether.vpn

import aethermobile.Aethermobile
import android.app.Activity
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.Canvas as AndroidCanvas
import android.net.VpnService
import android.os.Bundle
import android.os.SystemClock
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableDoubleStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL
import java.net.URLEncoder
import java.util.Locale
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.max
import kotlin.math.sin

// Carbon Obsidian + Signal Emerald palette (Color Consistency Lock)
private val BgCarbon = Color(0xFF09090B)
private val SurfaceCard = Color(0xFF111114)
private val SurfaceElevated = Color(0xFF18181B)
private val SurfaceRecessed = Color(0xFF0D0D10)
private val BorderHairline = Color(0xFF242429)
private val TextPrimary = Color(0xFFFAFAFA)
private val TextSecondary = Color(0xFFA1A1AA)
private val TextDim = Color(0xFF71717A)
private val CoolSilver = Color(0xFFE4E4E7)
private val SignalEmerald = Color(0xFF10B981)
private val SignalEmeraldBright = Color(0xFF34D399)
private val SignalAmber = Color(0xFFF59E0B)
private val SignalRose = Color(0xFFF43F5E)
private val OnEmeraldDark = Color(0xFF04140E)

private val AetherDarkScheme = darkColorScheme(
    primary = SignalEmerald,
    onPrimary = OnEmeraldDark,
    secondary = SignalEmeraldBright,
    onSecondary = OnEmeraldDark,
    background = BgCarbon,
    onBackground = TextPrimary,
    surface = SurfaceCard,
    onSurface = TextPrimary,
    surfaceVariant = SurfaceElevated,
    onSurfaceVariant = TextSecondary,
    outline = BorderHairline,
    error = SignalRose,
    onError = TextPrimary
)

class MainActivity : ComponentActivity() {

    private val vpnPermLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == Activity.RESULT_OK) {
            startVpnService()
        }
    }

    private fun requestAndStartVpn() {
        val prepareIntent = VpnService.prepare(this)
        if (prepareIntent != null) {
            vpnPermLauncher.launch(prepareIntent)
        } else {
            startVpnService()
        }
    }

    private fun startVpnService() {
        val intent = Intent(this, AetherVpnService::class.java).apply {
            action = AetherVpnService.ACTION_START
        }
        startForegroundService(intent)
    }

    private fun stopVpnService() {
        val intent = Intent(this, AetherVpnService::class.java).apply {
            action = AetherVpnService.ACTION_STOP
        }
        startService(intent)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.dark(android.graphics.Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.dark(android.graphics.Color.TRANSPARENT)
        )
        setContent {
            MaterialTheme(colorScheme = AetherDarkScheme) {
                Surface(modifier = Modifier.fillMaxSize(), color = BgCarbon) {
                    AetherApp(
                        initial = ConfigStore.load(this),
                        onSave = { ConfigStore.save(this, it) },
                        onConnect = { cfg ->
                            ConfigStore.save(this, cfg)
                            requestAndStartVpn()
                        },
                        onDisconnect = { stopVpnService() }
                    )
                }
            }
        }
    }
}

private enum class MenuRoute(val title: String) {
    ROUTES("Server & Routes"),
    PROTOCOL("Protocol & Transport"),
    ENCRYPTION("Encryption & Stealth"),
    DNS_SHIELD("DNS & Shield"),
    BYPASS_APPS("Split Tunneling & Apps"),
    ACCESS_KEY("Access Key & Engine")
}

private data class LiveTelemetry(
    val running: Boolean = false,
    val connecting: Boolean = false,
    val endpoint: String = "Disconnected",
    val carriers: Int = 0,
    val streams: Int = 0,
    val rttMs: Long = 0,
    val txBytes: Long = 0,
    val rxBytes: Long = 0,
    val failovers: Long = 0,
    val downMbps: Double = 0.0,
    val upMbps: Double = 0.0,
    val connectedSec: Long = 0
)

private data class InstalledAppItem(
    val packageName: String,
    val label: String,
    val isSystem: Boolean,
    val icon: ImageBitmap?
)

private fun fmtBytes(b: Long): String = when {
    b >= 1_000_000_000L -> String.format(Locale.US, "%.2f GB", b / 1e9)
    b >= 1_000_000L -> String.format(Locale.US, "%.2f MB", b / 1e6)
    b >= 1_000L -> String.format(Locale.US, "%.1f KB", b / 1e3)
    else -> "$b B"
}

private fun fmtDuration(sec: Long): String {
    val h = sec / 3600
    val m = (sec % 3600) / 60
    val s = sec % 60
    return if (h > 0) {
        String.format(Locale.US, "%02d:%02d:%02d", h, m, s)
    } else {
        String.format(Locale.US, "%02d:%02d", m, s)
    }
}

private fun extractHost(url: String): String {
    val trimmed = url.trim()
    if (trimmed.isEmpty()) return "Not configured"
    return try {
        val parsed = URI(trimmed).host
        if (!parsed.isNullOrBlank()) {
            parsed
        } else {
            trimmed.removePrefix("wss://").removePrefix("ws://").removePrefix("https://").removePrefix("http://").substringBefore("/")
        }
    } catch (_: Exception) {
        trimmed.removePrefix("wss://").removePrefix("ws://").removePrefix("https://").removePrefix("http://").substringBefore("/")
    }
}

private fun transportLabel(transport: String): String = when (transport.lowercase(Locale.US)) {
    "ws" -> "WebSocket"
    "xhttp" -> "XHTTP"
    "grpc" -> "gRPC"
    "h2" -> "HTTP/2"
    else -> "AUTO"
}

private fun shortCipherName(cipher: String): String = when (cipher.lowercase(Locale.US)) {
    "aes-256-gcm" -> "AES-256"
    "aes-128-gcm" -> "AES-128"
    "chacha20-poly1305" -> "ChaCha20"
    "none" -> "TLS-Only"
    else -> "XChaCha20"
}

private fun fullCipherName(cipher: String): String = when (cipher.lowercase(Locale.US)) {
    "aes-256-gcm" -> "AES-256-GCM"
    "aes-128-gcm" -> "AES-128-GCM"
    "chacha20-poly1305" -> "ChaCha20-Poly1305"
    "none" -> "TLS-Only (VLESS)"
    else -> "XChaCha20-Poly1305"
}

private fun fingerprintLabel(fp: String): String = when (fp.lowercase(Locale.US)) {
    "firefox" -> "Firefox"
    "safari" -> "Safari"
    "ios" -> "iOS"
    "randomized", "random" -> "Random"
    else -> "Chrome"
}

private fun paddingLabel(mode: String): String = when (mode.lowercase(Locale.US)) {
    "stealth" -> "Stealth"
    "off" -> "Off"
    else -> "Balanced"
}

private fun protocolProfileLabel(profile: String): String = when (profile.lowercase(Locale.US)) {
    "fast" -> "Aether-Fast"
    "ghost" -> "Aether-Ghost"
    "custom" -> "Custom"
    else -> "Aether-Mux"
}

private fun fragmentLabel(mode: String): String = when (mode.lowercase(Locale.US)) {
    "balanced" -> "Balanced"
    "strict" -> "Strict"
    else -> "Off"
}

private fun quicLabel(mode: String): String = when (mode.lowercase(Locale.US)) {
    "force_tcp" -> "Force TCP"
    "dns_only" -> "DNS Only"
    else -> "Full QUIC"
}

private fun windowProfileShort(profile: String): String = when (profile.lowercase(Locale.US)) {
    "balanced" -> "Balanced"
    "gaming" -> "Gaming"
    else -> "Max Speed"
}

private fun windowProfileDetail(profile: String): String = when (profile.lowercase(Locale.US)) {
    "balanced" -> "32KB / 4MB"
    "gaming" -> "16KB / 1MB"
    else -> "60KB / 8MB"
}

private fun normalizedPath(path: String): String {
    val trimmed = path.trim()
    if (trimmed.isEmpty()) return "/ws/sync"
    return if (trimmed.startsWith("/")) trimmed else "/$trimmed"
}

private fun inferProtocolProfile(c: AetherConfig): String = when {
    c.cipher.equals("none", ignoreCase = true) &&
        c.transport.equals("ws", ignoreCase = true) &&
        c.paddingMode.equals("off", ignoreCase = true) &&
        c.fragmentMode.equals("off", ignoreCase = true) &&
        c.windowProfile.equals("max", ignoreCase = true) &&
        c.carriers == 4 -> "fast"
    c.cipher.equals("xchacha20-poly1305", ignoreCase = true) &&
        c.transport.equals("xhttp", ignoreCase = true) &&
        c.fingerprint.equals("randomized", ignoreCase = true) &&
        c.paddingMode.equals("stealth", ignoreCase = true) &&
        c.fragmentMode.equals("strict", ignoreCase = true) &&
        c.windowProfile.equals("balanced", ignoreCase = true) &&
        c.carriers == 4 -> "ghost"
    c.cipher.equals("xchacha20-poly1305", ignoreCase = true) &&
        c.transport.equals("auto", ignoreCase = true) &&
        c.fingerprint.equals("chrome", ignoreCase = true) &&
        c.paddingMode.equals("balanced", ignoreCase = true) &&
        c.fragmentMode.equals("off", ignoreCase = true) &&
        c.windowProfile.equals("max", ignoreCase = true) &&
        c.carriers == 4 -> "mux"
    else -> "custom"
}

private fun dnsLabel(preset: String): String = when (preset.lowercase(Locale.US)) {
    "cloudflare" -> "Cloudflare"
    "google" -> "Google"
    "custom" -> "Custom"
    else -> "AdGuard"
}

private fun buildAdminApiUrl(endpointUrl: String, psk: String, apiPath: String): String? {
    val cleanPsk = psk.trim()
    val raw = endpointUrl.trim()
    if (cleanPsk.isEmpty() || raw.isEmpty()) return null
    val httpUrl = when {
        raw.startsWith("wss://", ignoreCase = true) -> "https://" + raw.substring(6)
        raw.startsWith("ws://", ignoreCase = true) -> "http://" + raw.substring(5)
        raw.startsWith("https://", ignoreCase = true) || raw.startsWith("http://", ignoreCase = true) -> raw
        else -> "https://$raw"
    }
    return try {
        val uri = URI(httpUrl)
        val scheme = uri.scheme ?: "https"
        val authority = uri.rawAuthority ?: return null
        val encodedPsk = URLEncoder.encode(cleanPsk, "UTF-8").replace("+", "%20")
        "$scheme://$authority/$encodedPsk$apiPath"
    } catch (_: Exception) {
        null
    }
}

private fun postConfigOverHttps(cfg: AetherConfig): Boolean {
    val candidates = listOf(cfg.primaryUrl, cfg.fallbackUrl).filter { it.isNotBlank() }
    val payload = cfg.toSyncPatchJson().toByteArray(Charsets.UTF_8)
    for (ep in candidates) {
        val urlStr = buildAdminApiUrl(ep, cfg.psk, "/api/config") ?: continue
        var conn: HttpURLConnection? = null
        try {
            conn = (URL(urlStr).openConnection() as HttpURLConnection).apply {
                requestMethod = "POST"
                connectTimeout = 5000
                readTimeout = 5000
                doOutput = true
                setRequestProperty("Content-Type", "application/json; charset=utf-8")
                setRequestProperty("Accept", "application/json")
            }
            conn.outputStream.use { it.write(payload) }
            val code = conn.responseCode
            if (code in 200..299) {
                return true
            }
        } catch (_: Throwable) {
        } finally {
            try {
                conn?.disconnect()
            } catch (_: Throwable) {
            }
        }
    }
    return false
}

private fun fetchSyncOverHttps(cfg: AetherConfig): String? {
    val candidates = listOf(cfg.primaryUrl, cfg.fallbackUrl).filter { it.isNotBlank() }
    for (ep in candidates) {
        val urlStr = buildAdminApiUrl(ep, cfg.psk, "/api/sync") ?: continue
        var conn: HttpURLConnection? = null
        try {
            conn = (URL(urlStr).openConnection() as HttpURLConnection).apply {
                requestMethod = "GET"
                connectTimeout = 5000
                readTimeout = 5000
                setRequestProperty("Accept", "application/json")
            }
            val code = conn.responseCode
            if (code in 200..299) {
                return conn.inputStream.bufferedReader(Charsets.UTF_8).use { it.readText() }
            }
        } catch (_: Throwable) {
        } finally {
            try {
                conn?.disconnect()
            } catch (_: Throwable) {
            }
        }
    }
    return null
}

@Composable
private fun AetherApp(
    initial: AetherConfig,
    onSave: (AetherConfig) -> Unit,
    onConnect: (AetherConfig) -> Unit,
    onDisconnect: () -> Unit
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    var tab by remember { mutableIntStateOf(0) } // 0 = Tunnel, 1 = Menu
    var menuSubPage by remember { mutableStateOf<MenuRoute?>(null) }

    var cfg by remember { mutableStateOf(initial) }
    val latestCfg by rememberUpdatedState(cfg)

    var telemetry by remember { mutableStateOf(LiveTelemetry()) }
    var downHistory by remember { mutableStateOf(List(30) { 0f }) }
    var upHistory by remember { mutableStateOf(List(30) { 0f }) }
    var peakMbps by remember { mutableDoubleStateOf(0.0) }
    var errorText by remember { mutableStateOf<String?>(null) }
    var isReconnecting by remember { mutableStateOf(false) }

    fun reconnectTunnel(nextCfg: AetherConfig) {
        if (isReconnecting) return
        isReconnecting = true
        scope.launch {
            try {
                onSave(nextCfg)
                onDisconnect()
                delay(400)
                onConnect(nextCfg)
            } finally {
                delay(300)
                isReconnecting = false
            }
        }
    }

    fun syncToRailway(newCfg: AetherConfig, reconnectIfNeeded: Boolean) {
        val stamped = newCfg.copy(configUpdatedAt = System.currentTimeMillis())
        cfg = stamped
        onSave(stamped)

        val wasRunning = telemetry.running
        scope.launch {
            val synced = if (stamped.psk.isNotBlank()) {
                withContext(Dispatchers.IO) {
                    val runningNow = try {
                        Aethermobile.isRunning()
                    } catch (_: Throwable) {
                        false
                    }
                    var pushed = false
                    if (runningNow) {
                        pushed = try {
                            Aethermobile.pushConfigJSON(stamped.toSyncPatchJson())
                            true
                        } catch (_: Throwable) {
                            false
                        }
                    }
                    if (pushed) true else postConfigOverHttps(stamped)
                }
            } else {
                false
            }

            Toast.makeText(
                context,
                if (synced) "Saved & synced with Railway" else "Saved locally",
                Toast.LENGTH_SHORT
            ).show()

            if (reconnectIfNeeded && wasRunning) {
                reconnectTunnel(stamped)
            }
        }
    }

    // System back inside a Menu sub-screen returns cleanly to the Menu Hub
    BackHandler(enabled = tab == 1 && menuSubPage != null) {
        menuSubPage = null
    }

    // 1-second telemetry & live bidirectional Railway sync loop
    LaunchedEffect(Unit) {
        var prevTx = 0L
        var prevRx = 0L
        var prevElapsedMs = 0L
        var startElapsedMs = 0L
        var smoothDown = 0.0
        var smoothUp = 0.0
        var lastSyncSeq = 0L

        while (true) {
            val isConnectingNow = AetherVpnService.isConnecting
            val isRun = withContext(Dispatchers.IO) {
                try {
                    Aethermobile.isRunning()
                } catch (_: Throwable) {
                    false
                }
            }
            errorText = AetherVpnService.lastError

            if (isRun) {
                val nowMs = SystemClock.elapsedRealtime()
                if (startElapsedMs == 0L) startElapsedMs = nowMs

                try {
                    val statsRaw = withContext(Dispatchers.IO) {
                        Aethermobile.getStatsJSON()
                    }
                    val j = JSONObject(statsRaw)
                    val tx = j.optLong("tx_bytes")
                    val rx = j.optLong("rx_bytes")

                    val dtSec = if (prevElapsedMs > 0L) (nowMs - prevElapsedMs) / 1000.0 else 0.0
                    if (prevElapsedMs > 0L && dtSec in 0.4..2.5) {
                        val rawDownMbps = ((rx - prevRx).coerceAtLeast(0L) * 8.0) / (dtSec * 1_000_000.0)
                        val rawUpMbps = ((tx - prevTx).coerceAtLeast(0L) * 8.0) / (dtSec * 1_000_000.0)
                        smoothDown = 0.65 * rawDownMbps + 0.35 * smoothDown
                        smoothUp = 0.65 * rawUpMbps + 0.35 * smoothUp
                    } else if (prevElapsedMs > 0L && dtSec > 2.5) {
                        // Discard background-resume spike bursts when dtSec > 2.5s
                        smoothDown *= 0.35
                        smoothUp *= 0.35
                    }
                    prevTx = tx
                    prevRx = rx
                    prevElapsedMs = nowMs

                    val samplePeak = max(smoothDown, smoothUp)
                    if (samplePeak > peakMbps) peakMbps = samplePeak

                    downHistory = downHistory.drop(1) + smoothDown.toFloat()
                    upHistory = upHistory.drop(1) + smoothUp.toFloat()

                    telemetry = LiveTelemetry(
                        running = true,
                        connecting = false,
                        endpoint = j.optString("endpoint", "connected"),
                        carriers = j.optInt("active_carriers"),
                        streams = j.optInt("active_streams"),
                        rttMs = j.optLong("rtt_ms"),
                        txBytes = tx,
                        rxBytes = rx,
                        failovers = j.optLong("failovers"),
                        downMbps = smoothDown,
                        upMbps = smoothUp,
                        connectedSec = ((nowMs - startElapsedMs) / 1000L).coerceAtLeast(0L)
                    )
                } catch (_: Throwable) {
                    telemetry = telemetry.copy(running = true, connecting = false)
                }

                // Auto-apply live config updates pushed from Railway over the encrypted tunnel
                val seq = withContext(Dispatchers.IO) {
                    try {
                        Aethermobile.getSyncedConfigSeq()
                    } catch (_: Throwable) {
                        0L
                    }
                }
                if (seq > lastSyncSeq) {
                    lastSyncSeq = seq
                    val current = latestCfg
                    if (current.autoSync) {
                        val rawSync = withContext(Dispatchers.IO) {
                            try {
                                Aethermobile.getSyncedConfigJSON()
                            } catch (_: Throwable) {
                                ""
                            }
                        }
                        if (rawSync.isNotBlank()) {
                            val (merged, needsReconnect) = current.mergeFromServerSync(rawSync)
                            if (merged != current) {
                                cfg = merged
                                onSave(merged)
                                Toast.makeText(context, "Synced settings from Railway", Toast.LENGTH_SHORT).show()
                                if (needsReconnect) {
                                    reconnectTunnel(merged)
                                }
                            }
                        }
                    }
                }
            } else {
                prevTx = 0L
                prevRx = 0L
                prevElapsedMs = 0L
                startElapsedMs = 0L
                smoothDown = 0.0
                smoothUp = 0.0
                lastSyncSeq = 0L
                if (peakMbps != 0.0) peakMbps = 0.0
                if (downHistory.any { it > 0f }) downHistory = List(30) { 0f }
                if (upHistory.any { it > 0f }) upHistory = List(30) { 0f }
                telemetry = LiveTelemetry(
                    running = false,
                    connecting = isConnectingNow
                )
            }

            delay(if (isConnectingNow && !isRun) 250L else 1000L)
        }
    }

    val ambientGlowColor by animateColorAsState(
        targetValue = when {
            telemetry.running -> SignalEmerald.copy(alpha = if (tab == 0) 0.14f else 0.07f)
            telemetry.connecting -> SignalAmber.copy(alpha = if (tab == 0) 0.13f else 0.06f)
            else -> TextDim.copy(alpha = if (tab == 0) 0.08f else 0.04f)
        },
        animationSpec = tween(durationMillis = 550),
        label = "ambientGlow"
    )

    Scaffold(
        containerColor = BgCarbon,
        contentWindowInsets = WindowInsets(0, 0, 0, 0)
    ) { innerPad ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(innerPad)
                .background(BgCarbon)
        ) {
            // Ambient radial glow behind the hero dial / header
            Canvas(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(360.dp)
                    .align(Alignment.TopCenter)
            ) {
                drawRect(
                    brush = Brush.radialGradient(
                        colors = listOf(ambientGlowColor, Color.Transparent),
                        center = Offset(size.width * 0.5f, size.height * 0.40f),
                        radius = size.width * 0.78f
                    )
                )
            }

            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .statusBarsPadding()
            ) {
                TopBrandBar(running = telemetry.running, connecting = telemetry.connecting)

                // Pinned Back Bar for Menu Sub-Screens
                if (tab == 1 && menuSubPage != null) {
                    SubPageTopBar(
                        title = menuSubPage!!.title,
                        onBack = { menuSubPage = null }
                    )
                }

                AnimatedContent(
                    targetState = Pair(tab, menuSubPage),
                    transitionSpec = {
                        fadeIn(animationSpec = tween(160)) togetherWith fadeOut(animationSpec = tween(120))
                    },
                    modifier = Modifier.weight(1f),
                    label = "screenTransition"
                ) { (activeTab, activeSub) ->
                    when {
                        activeTab == 0 -> {
                            Column(
                                modifier = Modifier
                                    .fillMaxSize()
                                    .verticalScroll(rememberScrollState())
                                    .padding(horizontal = 20.dp)
                            ) {
                                Spacer(Modifier.height(2.dp))
                                TunnelTab(
                                    cfg = cfg,
                                    telemetry = telemetry,
                                    downHistory = downHistory,
                                    upHistory = upHistory,
                                    peakMbps = peakMbps,
                                    errorText = errorText,
                                    onToggle = {
                                        if (telemetry.running || telemetry.connecting) onDisconnect() else onConnect(cfg)
                                    }
                                )
                                Spacer(Modifier.height(98.dp))
                                Spacer(Modifier.navigationBarsPadding())
                            }
                        }

                        activeSub == null -> {
                            Column(
                                modifier = Modifier
                                    .fillMaxSize()
                                    .verticalScroll(rememberScrollState())
                                    .padding(horizontal = 20.dp)
                            ) {
                                Spacer(Modifier.height(4.dp))
                                MenuHubTab(
                                    cfg = cfg,
                                    onOpen = { menuSubPage = it }
                                )
                                Spacer(Modifier.height(98.dp))
                                Spacer(Modifier.navigationBarsPadding())
                            }
                        }

                        activeSub == MenuRoute.BYPASS_APPS -> {
                            // Dedicated non-scrollable root so LazyColumn scrolls at 120 FPS
                            BypassAppsSubScreen(
                                cfg = cfg,
                                running = telemetry.running,
                                onUpdateBypass = { updated ->
                                    cfg = updated
                                    onSave(updated)
                                },
                                onReconnectNow = { updated ->
                                    reconnectTunnel(updated)
                                },
                                modifier = Modifier
                                    .fillMaxSize()
                                    .padding(horizontal = 20.dp)
                            )
                        }

                        else -> {
                            Column(
                                modifier = Modifier
                                    .fillMaxSize()
                                    .verticalScroll(rememberScrollState())
                                    .padding(horizontal = 20.dp)
                            ) {
                                Spacer(Modifier.height(6.dp))
                                when (activeSub) {
                                    MenuRoute.ROUTES -> RoutesSubScreen(
                                        cfg = cfg,
                                        onImport = {
                                            val cm = context.getSystemService(ClipboardManager::class.java)
                                            val clip = cm?.primaryClip?.getItemAt(0)?.text?.toString() ?: ""
                                            val imported = AetherConfig.fromShareUri(clip, cfg)
                                            if (imported != null) {
                                                val needsReconnect = imported.primaryUrl != cfg.primaryUrl ||
                                                    imported.fallbackUrl != cfg.fallbackUrl ||
                                                    imported.cfCleanIp != cfg.cfCleanIp ||
                                                    imported.cipher != cfg.cipher ||
                                                    imported.transport != cfg.transport ||
                                                    imported.fingerprint != cfg.fingerprint ||
                                                    imported.fragmentMode != cfg.fragmentMode ||
                                                    imported.customPath != cfg.customPath ||
                                                    imported.carriers != cfg.carriers ||
                                                    imported.psk != cfg.psk
                                                cfg = imported
                                                onSave(imported)
                                                Toast.makeText(context, "Imported aether:// config", Toast.LENGTH_SHORT).show()
                                                if (needsReconnect && telemetry.running) {
                                                    reconnectTunnel(imported)
                                                }
                                            } else {
                                                Toast.makeText(context, "Copy an aether:// link first", Toast.LENGTH_SHORT).show()
                                            }
                                        },
                                        onCopy = {
                                            val uri = cfg.toShareUri()
                                            val cm = context.getSystemService(ClipboardManager::class.java)
                                            cm?.setPrimaryClip(ClipData.newPlainText("AetherVPN", uri))
                                            Toast.makeText(context, "Copied aether:// share link", Toast.LENGTH_SHORT).show()
                                        },
                                        onSwap = { swapped ->
                                            syncToRailway(swapped, reconnectIfNeeded = true)
                                        },
                                        onSaveAndSync = { updated ->
                                            val reconnect = updated.primaryUrl != cfg.primaryUrl ||
                                                updated.fallbackUrl != cfg.fallbackUrl ||
                                                updated.cfCleanIp != cfg.cfCleanIp
                                            syncToRailway(updated, reconnectIfNeeded = reconnect)
                                        }
                                    )

                                    MenuRoute.PROTOCOL -> ProtocolSubScreen(
                                        cfg = cfg,
                                        onApplyAndSync = { updated ->
                                            val reconnect = updated.transport != cfg.transport ||
                                                updated.carriers != cfg.carriers ||
                                                updated.cipher != cfg.cipher ||
                                                updated.fingerprint != cfg.fingerprint ||
                                                updated.fragmentMode != cfg.fragmentMode ||
                                                updated.customPath != cfg.customPath
                                            syncToRailway(updated, reconnectIfNeeded = reconnect)
                                        }
                                    )

                                    MenuRoute.ENCRYPTION -> EncryptionSubScreen(
                                        cfg = cfg,
                                        onApplyAndSync = { updated ->
                                            val reconnect = updated.cipher != cfg.cipher ||
                                                updated.fingerprint != cfg.fingerprint ||
                                                updated.fragmentMode != cfg.fragmentMode ||
                                                updated.customPath != cfg.customPath
                                            syncToRailway(updated, reconnectIfNeeded = reconnect)
                                        }
                                    )

                                    MenuRoute.DNS_SHIELD -> DnsShieldSubScreen(
                                        cfg = cfg,
                                        onSyncToRailway = { updated ->
                                            syncToRailway(updated, reconnectIfNeeded = false)
                                        },
                                        onPullFromRailway = { onDone ->
                                            scope.launch {
                                                if (cfg.psk.isBlank()) {
                                                    Toast.makeText(context, "Set AETHER_PSK first", Toast.LENGTH_SHORT).show()
                                                    onDone()
                                                    return@launch
                                                }
                                                val raw = withContext(Dispatchers.IO) {
                                                    fetchSyncOverHttps(cfg)
                                                }
                                                if (raw != null && raw.isNotBlank()) {
                                                    val (merged, needsReconnect) = cfg.mergeFromServerSync(raw)
                                                    cfg = merged
                                                    onSave(merged)
                                                    Toast.makeText(context, "Pulled settings from Railway", Toast.LENGTH_SHORT).show()
                                                    if (needsReconnect && telemetry.running) {
                                                        reconnectTunnel(merged)
                                                    }
                                                } else {
                                                    Toast.makeText(context, "Could not reach Railway (check URL & PSK)", Toast.LENGTH_SHORT).show()
                                                }
                                                onDone()
                                            }
                                        }
                                    )

                                    MenuRoute.ACCESS_KEY -> AccessKeySubScreen(
                                        cfg = cfg,
                                        onCopyPsk = { keyVal ->
                                            if (keyVal.isBlank()) {
                                                Toast.makeText(context, "Secret key is empty", Toast.LENGTH_SHORT).show()
                                            } else {
                                                val cm = context.getSystemService(ClipboardManager::class.java)
                                                cm?.setPrimaryClip(ClipData.newPlainText("AETHER_PSK", keyVal.trim()))
                                                Toast.makeText(context, "Copied AETHER_PSK", Toast.LENGTH_SHORT).show()
                                            }
                                        },
                                        onSaveKey = { updated ->
                                            val pskChanged = updated.psk.trim() != cfg.psk.trim()
                                            cfg = updated
                                            onSave(updated)
                                            Toast.makeText(context, "Access key saved", Toast.LENGTH_SHORT).show()
                                            if (pskChanged && telemetry.running) {
                                                reconnectTunnel(updated)
                                            }
                                        }
                                    )

                                    MenuRoute.BYPASS_APPS -> Unit
                                }
                                Spacer(Modifier.height(104.dp))
                                Spacer(Modifier.navigationBarsPadding())
                            }
                        }
                    }
                }
            }

            // Soft bottom scrim so content dissolves cleanly under the floating dock
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(102.dp)
                    .align(Alignment.BottomCenter)
                    .background(
                        Brush.verticalGradient(
                            colors = listOf(Color.Transparent, BgCarbon.copy(alpha = 0.88f), BgCarbon)
                        )
                    )
            )

            // 2-Tab Floating Pill Bottom Dock: Tunnel | Menu
            FloatingNavDock(
                selectedTab = tab,
                onSelect = { target ->
                    if (target == 1 && tab == 1 && menuSubPage != null) {
                        menuSubPage = null
                    } else {
                        tab = target
                    }
                },
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .navigationBarsPadding()
                    .padding(horizontal = 24.dp)
                    .padding(bottom = 12.dp)
            )
        }
    }
}

// ---------------------------------------------------------------------------
// TOP BRAND BAR, SUB-PAGE BAR & FLOATING 2-TAB DOCK
// ---------------------------------------------------------------------------

@Composable
private fun TopBrandBar(running: Boolean, connecting: Boolean = false) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 20.dp, vertical = 10.dp),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            BrandMarkIcon(
                active = running,
                modifier = Modifier.size(26.dp)
            )
            Spacer(Modifier.width(10.dp))
            Text(
                text = "Aether",
                fontSize = 19.sp,
                fontWeight = FontWeight.Bold,
                letterSpacing = (-0.5).sp,
                color = TextPrimary
            )
            Spacer(Modifier.width(8.dp))
            Box(
                modifier = Modifier
                    .clip(RoundedCornerShape(6.dp))
                    .background(SurfaceElevated)
                    .border(1.dp, BorderHairline, RoundedCornerShape(6.dp))
                    .padding(horizontal = 6.dp, vertical = 2.dp)
            ) {
                Text(
                    text = "v4.1.1",
                    fontSize = 10.sp,
                    fontFamily = FontFamily.Monospace,
                    fontWeight = FontWeight.Medium,
                    color = SignalEmeraldBright
                )
            }
        }
        StatusBadge(running = running, connecting = connecting)
    }
}

@Composable
private fun SubPageTopBar(
    title: String,
    onBack: () -> Unit
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 20.dp)
            .padding(bottom = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Row(
            modifier = Modifier
                .clip(RoundedCornerShape(10.dp))
                .background(SurfaceCard)
                .border(1.dp, BorderHairline, RoundedCornerShape(10.dp))
                .clickable(onClick = onBack)
                .padding(horizontal = 10.dp, vertical = 7.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(5.dp)
        ) {
            ChevronLeftIcon(color = SignalEmeraldBright, modifier = Modifier.size(14.dp))
            Text(
                text = "Menu",
                fontSize = 12.sp,
                fontWeight = FontWeight.SemiBold,
                color = TextPrimary
            )
        }
        Text(
            text = title,
            fontSize = 16.sp,
            fontWeight = FontWeight.Bold,
            letterSpacing = (-0.3).sp,
            color = TextPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis
        )
    }
}

@Composable
private fun StatusBadge(running: Boolean, connecting: Boolean = false) {
    val infinite = rememberInfiniteTransition(label = "badgePulse")
    val dotAlpha by infinite.animateFloat(
        initialValue = 1f,
        targetValue = 0.35f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 1200, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Reverse
        ),
        label = "dotAlpha"
    )

    val bg by animateColorAsState(
        targetValue = when {
            running -> SignalEmerald.copy(alpha = 0.14f)
            connecting -> SignalAmber.copy(alpha = 0.14f)
            else -> SurfaceCard
        },
        animationSpec = tween(350),
        label = "badgeBg"
    )
    val border by animateColorAsState(
        targetValue = when {
            running -> SignalEmerald.copy(alpha = 0.45f)
            connecting -> SignalAmber.copy(alpha = 0.50f)
            else -> BorderHairline
        },
        animationSpec = tween(350),
        label = "badgeBorder"
    )
    val labelColor by animateColorAsState(
        targetValue = when {
            running -> SignalEmeraldBright
            connecting -> SignalAmber
            else -> TextSecondary
        },
        animationSpec = tween(350),
        label = "badgeText"
    )

    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(999.dp))
            .background(bg)
            .border(1.dp, border, RoundedCornerShape(999.dp))
            .padding(horizontal = 11.dp, vertical = 5.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(7.dp)
    ) {
        Box(
            modifier = Modifier
                .size(7.dp)
                .clip(CircleShape)
                .background(
                    when {
                        running -> SignalEmeraldBright.copy(alpha = dotAlpha)
                        connecting -> SignalAmber.copy(alpha = dotAlpha)
                        else -> TextDim
                    }
                )
        )
        Text(
            text = when {
                running -> "PROTECTED"
                connecting -> "LINKING"
                else -> "STANDBY"
            },
            fontSize = 10.sp,
            fontWeight = FontWeight.Bold,
            fontFamily = FontFamily.Monospace,
            letterSpacing = 0.8.sp,
            color = labelColor
        )
    }
}

@Composable
private fun FloatingNavDock(
    selectedTab: Int,
    onSelect: (Int) -> Unit,
    modifier: Modifier = Modifier
) {
    val tabs = listOf("Tunnel", "Menu")
    Row(
        modifier = modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(28.dp))
            .background(SurfaceCard)
            .border(1.dp, BorderHairline, RoundedCornerShape(28.dp))
            .padding(6.dp),
        horizontalArrangement = Arrangement.spacedBy(6.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        tabs.forEachIndexed { idx, label ->
            val selected = selectedTab == idx
            val pillBg by animateColorAsState(
                targetValue = if (selected) SignalEmerald.copy(alpha = 0.15f) else Color.Transparent,
                animationSpec = tween(220),
                label = "pillBg$idx"
            )
            val pillBorder by animateColorAsState(
                targetValue = if (selected) SignalEmerald.copy(alpha = 0.34f) else Color.Transparent,
                animationSpec = tween(220),
                label = "pillBorder$idx"
            )
            val contentColor by animateColorAsState(
                targetValue = if (selected) SignalEmeraldBright else TextDim,
                animationSpec = tween(220),
                label = "pillContent$idx"
            )

            Row(
                modifier = Modifier
                    .weight(1f)
                    .height(44.dp)
                    .clip(RoundedCornerShape(22.dp))
                    .background(pillBg)
                    .border(1.dp, pillBorder, RoundedCornerShape(22.dp))
                    .clickable { onSelect(idx) },
                horizontalArrangement = Arrangement.Center,
                verticalAlignment = Alignment.CenterVertically
            ) {
                when (idx) {
                    0 -> TunnelTabIcon(color = contentColor, modifier = Modifier.size(18.dp))
                    else -> MenuHubIcon(color = contentColor, modifier = Modifier.size(18.dp))
                }
                Spacer(Modifier.width(8.dp))
                Text(
                    text = label,
                    fontSize = 13.sp,
                    fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Medium,
                    color = contentColor,
                    maxLines = 1
                )
            }
        }
    }
}

// ---------------------------------------------------------------------------
// TAB 0: TUNNEL (Zero-Scroll Hero Instrument Screen)
// ---------------------------------------------------------------------------

@Composable
private fun TunnelTab(
    cfg: AetherConfig,
    telemetry: LiveTelemetry,
    downHistory: List<Float>,
    upHistory: List<Float>,
    peakMbps: Double,
    errorText: String?,
    onToggle: () -> Unit
) {
    val titleColor by animateColorAsState(
        targetValue = when {
            telemetry.running -> SignalEmeraldBright
            telemetry.connecting -> SignalAmber
            else -> TextPrimary
        },
        animationSpec = tween(350),
        label = "statusTitleColor"
    )

    Column(
        modifier = Modifier.fillMaxWidth(),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        // Machined 200dp Hero Power Dial
        MachinedPowerDial(
            running = telemetry.running,
            connecting = telemetry.connecting,
            onToggle = onToggle
        )

        // Status Headline + Active Cipher / Transport Telemetry Chip
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(6.dp)
        ) {
            Text(
                text = when {
                    telemetry.running -> "Tunnel Protected"
                    telemetry.connecting -> "Connecting (${transportLabel(cfg.transport)})..."
                    else -> "Ready to Connect"
                },
                fontSize = 19.sp,
                fontWeight = FontWeight.Bold,
                letterSpacing = (-0.4).sp,
                color = titleColor
            )
            Row(
                modifier = Modifier
                    .clip(RoundedCornerShape(999.dp))
                    .background(SurfaceCard)
                    .border(1.dp, BorderHairline, RoundedCornerShape(999.dp))
                    .padding(horizontal = 12.dp, vertical = 5.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(7.dp)
            ) {
                Box(
                    modifier = Modifier
                        .size(6.dp)
                        .clip(CircleShape)
                        .background(
                            when {
                                telemetry.running -> SignalEmerald
                                telemetry.connecting -> SignalAmber
                                else -> TextDim
                            }
                        )
                )
                Text(
                    text = when {
                        telemetry.running -> "${telemetry.endpoint} · ${protocolProfileLabel(cfg.protocolProfile)} · ${transportLabel(cfg.transport)} · ${fmtDuration(telemetry.connectedSec)}"
                        telemetry.connecting -> "Establishing ${transportLabel(cfg.transport)} · tap dial to cancel"
                        else -> "${protocolProfileLabel(cfg.protocolProfile)} · ${shortCipherName(cfg.cipher)} · ${transportLabel(cfg.transport)} · uTLS ${fingerprintLabel(cfg.fingerprint)}"
                    },
                    fontSize = 10.5.sp,
                    fontFamily = FontFamily.Monospace,
                    color = if (telemetry.running) TextSecondary else TextDim,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )
            }
        }

        // Inline Rose Error Banner (only when non-blank)
        if (!errorText.isNullOrBlank()) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(12.dp))
                    .background(SignalRose.copy(alpha = 0.10f))
                    .border(1.dp, SignalRose.copy(alpha = 0.42f), RoundedCornerShape(12.dp))
                    .padding(horizontal = 12.dp, vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(10.dp)
            ) {
                Box(
                    modifier = Modifier
                        .size(7.dp)
                        .clip(CircleShape)
                        .background(SignalRose)
                )
                Text(
                    text = errorText,
                    color = Color(0xFFFDA4AF),
                    fontSize = 11.5.sp,
                    lineHeight = 16.sp
                )
            }
        }

        // Dual-Series Live 30-Second Throughput Sparkline Card (Down Emerald + Up Silver)
        ThroughputSparklineCard(
            telemetry = telemetry,
            downSamples = downHistory,
            upSamples = upHistory,
            peakMbps = peakMbps
        )

        // Compact 4-Carrier Health Matrix Bar
        CarrierMatrixBar(
            activeCarriers = telemetry.carriers,
            targetCarriers = cfg.carriers,
            streams = telemetry.streams,
            failovers = telemetry.failovers,
            running = telemetry.running
        )
    }
}

@Composable
private fun MachinedPowerDial(
    running: Boolean,
    connecting: Boolean = false,
    onToggle: () -> Unit
) {
    val haptic = LocalHapticFeedback.current
    val infinite = rememberInfiniteTransition(label = "dialAnim")

    val sweepAngle by infinite.animateFloat(
        initialValue = 0f,
        targetValue = 360f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 2800, easing = LinearEasing),
            repeatMode = RepeatMode.Restart
        ),
        label = "sweepAngle"
    )

    val radarProgress by infinite.animateFloat(
        initialValue = 0f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 2400, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Restart
        ),
        label = "radarProgress"
    )

    val glyphColor by animateColorAsState(
        targetValue = when {
            running -> SignalEmeraldBright
            connecting -> SignalAmber
            else -> TextPrimary
        },
        animationSpec = tween(350),
        label = "glyphColor"
    )
    val rimTopColor by animateColorAsState(
        targetValue = when {
            running -> SignalEmeraldBright.copy(alpha = 0.65f)
            connecting -> SignalAmber.copy(alpha = 0.65f)
            else -> Color(0xFF3F3F46)
        },
        animationSpec = tween(350),
        label = "rimTopColor"
    )
    val rimBottomColor by animateColorAsState(
        targetValue = when {
            running -> SignalEmerald.copy(alpha = 0.18f)
            connecting -> SignalAmber.copy(alpha = 0.18f)
            else -> BorderHairline
        },
        animationSpec = tween(350),
        label = "rimBottomColor"
    )
    val coreTop by animateColorAsState(
        targetValue = when {
            running -> Color(0xFF10241D)
            connecting -> Color(0xFF231B0E)
            else -> Color(0xFF1C1C21)
        },
        animationSpec = tween(350),
        label = "coreTop"
    )
    val coreBottom by animateColorAsState(
        targetValue = when {
            running -> Color(0xFF09120F)
            connecting -> Color(0xFF120E09)
            else -> Color(0xFF0C0C0F)
        },
        animationSpec = tween(350),
        label = "coreBottom"
    )

    Box(
        modifier = Modifier.size(200.dp),
        contentAlignment = Alignment.Center
    ) {
        Canvas(modifier = Modifier.fillMaxSize()) {
            val center = Offset(size.width / 2f, size.height / 2f)
            val tickOuterR = size.minDimension * 0.475f
            val tickInnerMinor = size.minDimension * 0.438f
            val tickInnerMajor = size.minDimension * 0.422f
            val sweepRadius = size.minDimension * 0.392f

            if (running || connecting) {
                val baseR = size.minDimension * 0.34f
                val maxR = size.minDimension * 0.495f
                val waveR = baseR + (maxR - baseR) * radarProgress
                val pulseColor = if (running) SignalEmerald else SignalAmber
                drawCircle(
                    color = pulseColor.copy(alpha = (1f - radarProgress) * 0.26f),
                    radius = waveR,
                    center = center,
                    style = Stroke(width = 1.5.dp.toPx())
                )
            }

            val totalTicks = 36
            for (i in 0 until totalTicks) {
                val angleDeg = i * (360.0 / totalTicks) - 90.0
                val rad = angleDeg * (PI / 180.0)
                val cosA = cos(rad).toFloat()
                val sinA = sin(rad).toFloat()
                val isMajor = i % 9 == 0
                val innerR = if (isMajor) tickInnerMajor else tickInnerMinor

                val tickColor = when {
                    running -> if (isMajor) SignalEmeraldBright.copy(alpha = 0.75f) else SignalEmerald.copy(alpha = 0.30f)
                    connecting -> if (isMajor) SignalAmber.copy(alpha = 0.75f) else SignalAmber.copy(alpha = 0.30f)
                    else -> if (isMajor) TextDim.copy(alpha = 0.65f) else BorderHairline
                }

                drawLine(
                    color = tickColor,
                    start = Offset(center.x + innerR * cosA, center.y + innerR * sinA),
                    end = Offset(center.x + tickOuterR * cosA, center.y + tickOuterR * sinA),
                    strokeWidth = if (isMajor) 2.dp.toPx() else 1.2.dp.toPx(),
                    cap = StrokeCap.Round
                )
            }

            drawCircle(
                color = when {
                    running -> SignalEmerald.copy(alpha = 0.14f)
                    connecting -> SignalAmber.copy(alpha = 0.18f)
                    else -> BorderHairline.copy(alpha = 0.7f)
                },
                radius = sweepRadius,
                center = center,
                style = Stroke(width = 2.dp.toPx())
            )

            if (running || connecting) {
                val arcTopLeft = Offset(center.x - sweepRadius, center.y - sweepRadius)
                val arcSize = Size(sweepRadius * 2f, sweepRadius * 2f)
                val tailColor = if (running) SignalEmerald.copy(alpha = 0.35f) else SignalAmber.copy(alpha = 0.35f)
                val headColor = if (running) SignalEmeraldBright else SignalAmber
                drawArc(
                    color = tailColor,
                    startAngle = sweepAngle - 95f,
                    sweepAngle = 95f,
                    useCenter = false,
                    topLeft = arcTopLeft,
                    size = arcSize,
                    style = Stroke(width = 3.dp.toPx(), cap = StrokeCap.Round)
                )
                drawArc(
                    color = headColor,
                    startAngle = sweepAngle - 28f,
                    sweepAngle = 28f,
                    useCenter = false,
                    topLeft = arcTopLeft,
                    size = arcSize,
                    style = Stroke(width = 3.dp.toPx(), cap = StrokeCap.Round)
                )
            }
        }

        Box(
            modifier = Modifier
                .size(142.dp)
                .clip(CircleShape)
                .background(SurfaceRecessed)
                .border(1.dp, BorderHairline, CircleShape),
            contentAlignment = Alignment.Center
        ) {
            Box(
                modifier = Modifier
                    .size(130.dp)
                    .clip(CircleShape)
                    .background(Brush.verticalGradient(listOf(coreTop, coreBottom)))
                    .border(
                        width = 1.5.dp,
                        brush = Brush.verticalGradient(listOf(rimTopColor, rimBottomColor)),
                        shape = CircleShape
                    )
                    .clickable {
                        haptic.performHapticFeedback(HapticFeedbackType.LongPress)
                        onToggle()
                    },
                contentAlignment = Alignment.Center
            ) {
                Column(
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center
                ) {
                    Canvas(modifier = Modifier.size(36.dp)) {
                        val strokeW = 3.dp.toPx()
                        val inset = 4.5.dp.toPx()
                        drawArc(
                            color = glyphColor,
                            startAngle = -55f,
                            sweepAngle = 290f,
                            useCenter = false,
                            topLeft = Offset(inset, inset),
                            size = Size(size.width - inset * 2f, size.height - inset * 2f),
                            style = Stroke(width = strokeW, cap = StrokeCap.Round)
                        )
                        drawLine(
                            color = glyphColor,
                            start = Offset(size.width / 2f, 2.dp.toPx()),
                            end = Offset(size.width / 2f, size.height * 0.48f),
                            strokeWidth = strokeW,
                            cap = StrokeCap.Round
                        )
                    }
                    Spacer(Modifier.height(5.dp))
                    Text(
                        text = when {
                            running -> "ACTIVE"
                            connecting -> "CANCEL"
                            else -> "ENGAGE"
                        },
                        fontSize = 9.sp,
                        fontFamily = FontFamily.Monospace,
                        fontWeight = FontWeight.Bold,
                        letterSpacing = 1.6.sp,
                        color = when {
                            running -> SignalEmeraldBright
                            connecting -> SignalAmber
                            else -> TextDim
                        }
                    )
                }
            }
        }
    }
}

@Composable
private fun ThroughputSparklineCard(
    telemetry: LiveTelemetry,
    downSamples: List<Float>,
    upSamples: List<Float>,
    peakMbps: Double
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(18.dp))
            .background(SurfaceCard)
            .border(1.dp, BorderHairline, RoundedCornerShape(18.dp))
    ) {
        // Header Row: Live ↓ Down & ↑ Up Mbps + RTT & Peak mono pills
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(start = 15.dp, end = 15.dp, top = 12.dp, bottom = 6.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.Top
        ) {
            Column(verticalArrangement = Arrangement.spacedBy(3.dp)) {
                Text(
                    text = "LIVE THROUGHPUT",
                    fontSize = 9.5.sp,
                    fontWeight = FontWeight.SemiBold,
                    letterSpacing = 1.sp,
                    color = TextDim
                )
                Row(
                    verticalAlignment = Alignment.Bottom,
                    horizontalArrangement = Arrangement.spacedBy(10.dp)
                ) {
                    Row(verticalAlignment = Alignment.Bottom) {
                        Text(
                            text = "↓",
                            fontSize = 14.sp,
                            fontWeight = FontWeight.Bold,
                            color = if (telemetry.running) SignalEmeraldBright else TextDim,
                            modifier = Modifier.padding(bottom = 2.dp, end = 2.dp)
                        )
                        Text(
                            text = String.format(Locale.US, "%.1f", telemetry.downMbps),
                            fontSize = 20.sp,
                            fontWeight = FontWeight.Bold,
                            fontFamily = FontFamily.Monospace,
                            letterSpacing = (-0.6).sp,
                            color = if (telemetry.running) TextPrimary else TextSecondary
                        )
                    }
                    Row(verticalAlignment = Alignment.Bottom) {
                        Text(
                            text = "↑",
                            fontSize = 14.sp,
                            fontWeight = FontWeight.Bold,
                            color = if (telemetry.running) CoolSilver else TextDim,
                            modifier = Modifier.padding(bottom = 2.dp, end = 2.dp)
                        )
                        Text(
                            text = String.format(Locale.US, "%.1f", telemetry.upMbps),
                            fontSize = 17.sp,
                            fontWeight = FontWeight.SemiBold,
                            fontFamily = FontFamily.Monospace,
                            letterSpacing = (-0.4).sp,
                            color = if (telemetry.running) CoolSilver else TextDim
                        )
                        Spacer(Modifier.width(4.dp))
                        Text(
                            text = "Mbps",
                            fontSize = 10.sp,
                            fontFamily = FontFamily.Monospace,
                            color = TextDim,
                            modifier = Modifier.padding(bottom = 2.dp)
                        )
                    }
                }
            }

            Row(horizontalArrangement = Arrangement.spacedBy(5.dp)) {
                MonoTelemetryPill(
                    label = "RTT",
                    value = if (telemetry.running && telemetry.rttMs > 0) "${telemetry.rttMs}ms" else "--"
                )
                MonoTelemetryPill(
                    label = "Peak",
                    value = if (telemetry.running) String.format(Locale.US, "%.1f", peakMbps) else "--"
                )
            }
        }

        // 62dp Dual-Series Canvas Sparkline (Down = Emerald Filled Area, Up = Cool Silver Line)
        Canvas(
            modifier = Modifier
                .fillMaxWidth()
                .height(62.dp)
                .padding(horizontal = 15.dp, vertical = 4.dp)
        ) {
            val w = size.width
            val h = size.height
            val baseY = h - 2.dp.toPx()
            val topPad = 4.dp.toPx()
            val drawH = (baseY - topPad).coerceAtLeast(1f)

            val dash = PathEffect.dashPathEffect(floatArrayOf(6f, 6f), 0f)
            drawLine(
                color = BorderHairline.copy(alpha = 0.5f),
                start = Offset(0f, topPad + drawH * 0.5f),
                end = Offset(w, topPad + drawH * 0.5f),
                strokeWidth = 1.dp.toPx(),
                pathEffect = dash
            )
            drawLine(
                color = BorderHairline,
                start = Offset(0f, baseY),
                end = Offset(w, baseY),
                strokeWidth = 1.dp.toPx(),
                pathEffect = dash
            )

            val count = downSamples.size
            if (count >= 2 && upSamples.size == count) {
                val maxDown = downSamples.maxOrNull() ?: 0f
                val maxUp = upSamples.maxOrNull() ?: 0f
                val maxVal = max(1f, max(maxDown, maxUp) * 1.18f)
                val stepX = w / (count - 1).toFloat()

                fun buildSmoothPath(series: List<Float>): Pair<Path, List<Offset>> {
                    val pts = List(count) { idx ->
                        val ratio = (series[idx] / maxVal).coerceIn(0f, 1f)
                        Offset(x = idx * stepX, y = baseY - (ratio * drawH))
                    }
                    val path = Path().apply {
                        moveTo(pts.first().x, pts.first().y)
                        for (i in 0 until pts.lastIndex) {
                            val p0 = pts[i]
                            val p1 = pts[i + 1]
                            val midX = (p0.x + p1.x) / 2f
                            cubicTo(midX, p0.y, midX, p1.y, p1.x, p1.y)
                        }
                    }
                    return path to pts
                }

                val (downPath, downPts) = buildSmoothPath(downSamples)
                val (upPath, upPts) = buildSmoothPath(upSamples)

                val fillPath = Path().apply {
                    addPath(downPath)
                    lineTo(downPts.last().x, baseY)
                    lineTo(downPts.first().x, baseY)
                    close()
                }

                if (telemetry.running) {
                    drawPath(
                        path = fillPath,
                        brush = Brush.verticalGradient(
                            colors = listOf(SignalEmerald.copy(alpha = 0.28f), Color.Transparent),
                            startY = topPad,
                            endY = baseY
                        )
                    )
                }

                // Upload crisp silver line
                drawPath(
                    path = upPath,
                    color = if (telemetry.running) CoolSilver.copy(alpha = 0.85f) else TextDim.copy(alpha = 0.30f),
                    style = Stroke(
                        width = 1.5.dp.toPx(),
                        cap = StrokeCap.Round,
                        join = StrokeJoin.Round
                    )
                )

                // Download primary Signal Emerald line
                drawPath(
                    path = downPath,
                    color = if (telemetry.running) SignalEmeraldBright else TextDim.copy(alpha = 0.45f),
                    style = Stroke(
                        width = 2.dp.toPx(),
                        cap = StrokeCap.Round,
                        join = StrokeJoin.Round
                    )
                )

                // Live terminal beacons
                if (telemetry.running) {
                    val lastUp = upPts.last()
                    drawCircle(
                        color = CoolSilver,
                        radius = 2.2.dp.toPx(),
                        center = lastUp
                    )
                    val lastDown = downPts.last()
                    drawCircle(
                        color = SignalEmerald.copy(alpha = 0.32f),
                        radius = 5.5.dp.toPx(),
                        center = lastDown
                    )
                    drawCircle(
                        color = SignalEmeraldBright,
                        radius = 2.6.dp.toPx(),
                        center = lastDown
                    )
                }
            }
        }

        HorizontalDivider(color = BorderHairline, thickness = 1.dp)

        // Footer Split Row: Total Download | Total Upload
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 15.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Row(
                modifier = Modifier.weight(1f),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(9.dp)
            ) {
                Box(
                    modifier = Modifier
                        .size(28.dp)
                        .clip(RoundedCornerShape(8.dp))
                        .background(SignalEmerald.copy(alpha = 0.12f)),
                    contentAlignment = Alignment.Center
                ) {
                    DownloadArrowIcon(color = SignalEmeraldBright, modifier = Modifier.size(14.dp))
                }
                Column {
                    Text(
                        text = "DOWNLOAD",
                        fontSize = 8.5.sp,
                        fontWeight = FontWeight.SemiBold,
                        letterSpacing = 0.8.sp,
                        color = TextDim
                    )
                    Text(
                        text = fmtBytes(telemetry.rxBytes),
                        fontSize = 13.sp,
                        fontWeight = FontWeight.SemiBold,
                        fontFamily = FontFamily.Monospace,
                        color = TextPrimary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                }
            }

            Box(
                modifier = Modifier
                    .width(1.dp)
                    .height(24.dp)
                    .background(BorderHairline)
            )

            Spacer(Modifier.width(12.dp))

            Row(
                modifier = Modifier.weight(1f),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(9.dp)
            ) {
                Box(
                    modifier = Modifier
                        .size(28.dp)
                        .clip(RoundedCornerShape(8.dp))
                        .background(SurfaceElevated),
                    contentAlignment = Alignment.Center
                ) {
                    UploadArrowIcon(color = CoolSilver, modifier = Modifier.size(14.dp))
                }
                Column {
                    Text(
                        text = "UPLOAD",
                        fontSize = 8.5.sp,
                        fontWeight = FontWeight.SemiBold,
                        letterSpacing = 0.8.sp,
                        color = TextDim
                    )
                    Text(
                        text = fmtBytes(telemetry.txBytes),
                        fontSize = 13.sp,
                        fontWeight = FontWeight.SemiBold,
                        fontFamily = FontFamily.Monospace,
                        color = TextPrimary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                }
            }
        }
    }
}

@Composable
private fun MonoTelemetryPill(label: String, value: String) {
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(8.dp))
            .background(SurfaceElevated)
            .border(1.dp, BorderHairline, RoundedCornerShape(8.dp))
            .padding(horizontal = 7.dp, vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(4.dp)
    ) {
        Text(
            text = label,
            fontSize = 9.5.sp,
            fontFamily = FontFamily.Monospace,
            color = TextDim
        )
        Text(
            text = value,
            fontSize = 9.5.sp,
            fontFamily = FontFamily.Monospace,
            fontWeight = FontWeight.SemiBold,
            color = TextPrimary
        )
    }
}

@Composable
private fun CarrierMatrixBar(
    activeCarriers: Int,
    targetCarriers: Int,
    streams: Int,
    failovers: Long,
    running: Boolean
) {
    val totalSlots = targetCarriers.coerceIn(1, 6)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(16.dp))
            .background(SurfaceCard)
            .border(1.dp, BorderHairline, RoundedCornerShape(16.dp))
            .padding(horizontal = 15.dp, vertical = 11.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp)
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text(
                text = "CARRIER MATRIX",
                fontSize = 9.5.sp,
                fontWeight = FontWeight.SemiBold,
                letterSpacing = 1.sp,
                color = TextDim
            )
            Text(
                text = "$activeCarriers/$totalSlots carriers · $streams streams · $failovers failovers",
                fontSize = 10.sp,
                fontFamily = FontFamily.Monospace,
                color = if (running && activeCarriers > 0) TextSecondary else TextDim,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
        }

        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(6.dp)
        ) {
            for (slot in 0 until totalSlots) {
                val lit = running && slot < activeCarriers
                val segColor by animateColorAsState(
                    targetValue = when {
                        lit -> SignalEmerald
                        running && activeCarriers == 0 -> SignalAmber.copy(alpha = 0.25f)
                        else -> SurfaceElevated
                    },
                    animationSpec = tween(260),
                    label = "carrierSeg$slot"
                )
                Box(
                    modifier = Modifier
                        .weight(1f)
                        .height(7.dp)
                        .clip(RoundedCornerShape(4.dp))
                        .background(segColor)
                        .border(
                            width = 1.dp,
                            color = if (lit) SignalEmeraldBright.copy(alpha = 0.55f) else BorderHairline,
                            shape = RoundedCornerShape(4.dp)
                        )
                )
            }
        }
    }
}

// ---------------------------------------------------------------------------
// TAB 1: MENU HUB (Zero-Scroll 6-Item Control Center)
// ---------------------------------------------------------------------------

@Composable
private fun MenuHubTab(
    cfg: AetherConfig,
    onOpen: (MenuRoute) -> Unit
) {
    val bypassCount = cfg.bypassSet().size

    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(bottom = 2.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(
                    text = "CONTROL CENTER",
                    fontSize = 10.sp,
                    fontWeight = FontWeight.Bold,
                    letterSpacing = 1.1.sp,
                    color = TextDim
                )
                Text(
                    text = "Configuration & Control",
                    fontSize = 18.sp,
                    fontWeight = FontWeight.Bold,
                    letterSpacing = (-0.4).sp,
                    color = TextPrimary
                )
            }
            Row(
                modifier = Modifier
                    .clip(RoundedCornerShape(999.dp))
                    .background(if (cfg.autoSync) SignalEmerald.copy(alpha = 0.12f) else SurfaceCard)
                    .border(
                        1.dp,
                        if (cfg.autoSync) SignalEmerald.copy(alpha = 0.35f) else BorderHairline,
                        RoundedCornerShape(999.dp)
                    )
                    .padding(horizontal = 9.dp, vertical = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(5.dp)
            ) {
                Box(
                    modifier = Modifier
                        .size(6.dp)
                        .clip(CircleShape)
                        .background(if (cfg.autoSync) SignalEmeraldBright else TextDim)
                )
                Text(
                    text = if (cfg.autoSync) "SYNC LIVE" else "LOCAL",
                    fontSize = 9.5.sp,
                    fontFamily = FontFamily.Monospace,
                    fontWeight = FontWeight.Bold,
                    color = if (cfg.autoSync) SignalEmeraldBright else TextDim
                )
            }
        }

        MenuHubRow(
            title = "Server & Routes",
            subtitle = "${extractHost(cfg.primaryUrl)} → ${extractHost(cfg.fallbackUrl)}",
            icon = { RoutesTabIcon(color = SignalEmeraldBright, modifier = Modifier.size(18.dp)) },
            onClick = { onOpen(MenuRoute.ROUTES) }
        )
        MenuHubRow(
            title = "Protocol & Transport",
            subtitle = "${protocolProfileLabel(cfg.protocolProfile)} · ${transportLabel(cfg.transport)} · ${cfg.carriers}C · ${windowProfileShort(cfg.windowProfile)}${if (cfg.idlePower) " · Idle" else ""}",
            icon = { ProtocolIcon(color = SignalEmeraldBright, modifier = Modifier.size(18.dp)) },
            onClick = { onOpen(MenuRoute.PROTOCOL) }
        )
        MenuHubRow(
            title = "Encryption & Stealth",
            subtitle = "${shortCipherName(cfg.cipher)} · ${fingerprintLabel(cfg.fingerprint)} · Frag ${fragmentLabel(cfg.fragmentMode)} · ${normalizedPath(cfg.customPath)}",
            icon = { ShieldLockIcon(color = SignalEmeraldBright, modifier = Modifier.size(18.dp)) },
            onClick = { onOpen(MenuRoute.ENCRYPTION) }
        )
        MenuHubRow(
            title = "DNS & Shield",
            subtitle = "${dnsLabel(cfg.dnsPreset)} · ${quicLabel(cfg.quicMode)} · ${if (cfg.blockDot) "DoT Blocked" else "DoT Open"}",
            icon = { DnsGlobeIcon(color = SignalEmeraldBright, modifier = Modifier.size(18.dp)) },
            onClick = { onOpen(MenuRoute.DNS_SHIELD) }
        )
        MenuHubRow(
            title = "Split Tunneling & Bypass Apps",
            subtitle = "LAN Bypass ${if (cfg.bypassLan) "ON" else "OFF"} · $bypassCount ${if (bypassCount == 1) "app" else "apps"} excluded",
            icon = { AppsGridIcon(color = SignalEmeraldBright, modifier = Modifier.size(18.dp)) },
            onClick = { onOpen(MenuRoute.BYPASS_APPS) }
        )
        MenuHubRow(
            title = "Access Key & Engine",
            subtitle = "${if (cfg.psk.isNotBlank()) "PSK Configured" else "PSK Missing"} · Aether v4.1",
            icon = { KeyIcon(color = SignalEmeraldBright, modifier = Modifier.size(18.dp)) },
            onClick = { onOpen(MenuRoute.ACCESS_KEY) }
        )
    }
}

@Composable
private fun MenuHubRow(
    title: String,
    subtitle: String,
    icon: @Composable () -> Unit,
    onClick: () -> Unit
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(16.dp))
            .background(SurfaceCard)
            .border(1.dp, BorderHairline, RoundedCornerShape(16.dp))
            .clickable(onClick = onClick)
            .padding(horizontal = 14.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Box(
            modifier = Modifier
                .size(38.dp)
                .clip(RoundedCornerShape(11.dp))
                .background(SignalEmerald.copy(alpha = 0.11f))
                .border(1.dp, SignalEmerald.copy(alpha = 0.24f), RoundedCornerShape(11.dp)),
            contentAlignment = Alignment.Center
        ) {
            icon()
        }
        Column(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(2.dp)
        ) {
            Text(
                text = title,
                fontSize = 14.sp,
                fontWeight = FontWeight.SemiBold,
                color = TextPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
            Text(
                text = subtitle,
                fontSize = 11.sp,
                fontFamily = FontFamily.Monospace,
                color = TextSecondary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
        }
        ChevronRightIcon(color = TextDim, modifier = Modifier.size(15.dp))
    }
}

// ---------------------------------------------------------------------------
// SUB-SCREEN 1: SERVER & ROUTES
// ---------------------------------------------------------------------------

@Composable
private fun RoutesSubScreen(
    cfg: AetherConfig,
    onImport: () -> Unit,
    onCopy: () -> Unit,
    onSwap: (AetherConfig) -> Unit,
    onSaveAndSync: (AetherConfig) -> Unit
) {
    val scope = rememberCoroutineScope()
    var primaryUrl by remember(cfg.primaryUrl) { mutableStateOf(cfg.primaryUrl) }
    var fallbackUrl by remember(cfg.fallbackUrl) { mutableStateOf(cfg.fallbackUrl) }
    var cfCleanIp by remember(cfg.cfCleanIp) { mutableStateOf(cfg.cfCleanIp) }

    var pinging by remember { mutableStateOf(false) }
    var primaryPingMs by remember { mutableStateOf<Long?>(null) }
    var fallbackPingMs by remember { mutableStateOf<Long?>(null) }

    Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
        // 1-Tap Provisioning Row
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(10.dp)
        ) {
            Button(
                onClick = onImport,
                modifier = Modifier
                    .weight(1f)
                    .height(44.dp),
                shape = RoundedCornerShape(13.dp),
                colors = ButtonDefaults.buttonColors(
                    containerColor = TextPrimary,
                    contentColor = BgCarbon
                ),
                contentPadding = PaddingValues(horizontal = 12.dp)
            ) {
                ClipboardImportIcon(color = BgCarbon, modifier = Modifier.size(15.dp))
                Spacer(Modifier.width(7.dp))
                Text(
                    text = "Import aether://",
                    fontSize = 12.sp,
                    fontWeight = FontWeight.Bold,
                    color = BgCarbon,
                    maxLines = 1
                )
            }

            OutlinedButton(
                onClick = onCopy,
                modifier = Modifier
                    .weight(1f)
                    .height(44.dp),
                shape = RoundedCornerShape(13.dp),
                border = BorderStroke(1.dp, BorderHairline),
                colors = ButtonDefaults.outlinedButtonColors(
                    containerColor = SurfaceElevated,
                    contentColor = TextPrimary
                ),
                contentPadding = PaddingValues(horizontal = 12.dp)
            ) {
                ShareCopyIcon(color = TextPrimary, modifier = Modifier.size(15.dp))
                Spacer(Modifier.width(7.dp))
                Text(
                    text = "Copy Share Link",
                    fontSize = 12.sp,
                    fontWeight = FontWeight.SemiBold,
                    color = TextPrimary,
                    maxLines = 1
                )
            }
        }

        // Visual PRIMARY → FALLBACK Route Card + Swap & Ping
        SectionCard(title = "ACTIVE FAILOVER ROUTE") {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(12.dp))
                    .background(SurfaceRecessed)
                    .border(1.dp, BorderHairline, RoundedCornerShape(12.dp))
                    .padding(12.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                Column(
                    modifier = Modifier.weight(1f),
                    verticalArrangement = Arrangement.spacedBy(4.dp)
                ) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(6.dp)
                    ) {
                        Box(
                            modifier = Modifier
                                .size(6.dp)
                                .clip(CircleShape)
                                .background(SignalEmerald)
                        )
                        Text(
                            text = "PRIMARY",
                            fontSize = 9.sp,
                            fontFamily = FontFamily.Monospace,
                            fontWeight = FontWeight.Bold,
                            letterSpacing = 0.8.sp,
                            color = SignalEmeraldBright
                        )
                    }
                    Text(
                        text = extractHost(primaryUrl),
                        fontSize = 11.5.sp,
                        fontFamily = FontFamily.Monospace,
                        color = TextPrimary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                    PingBadge(ms = primaryPingMs, pinging = pinging)
                }

                Text(
                    text = "→",
                    fontSize = 15.sp,
                    color = TextDim,
                    modifier = Modifier.padding(horizontal = 8.dp)
                )

                Column(
                    modifier = Modifier.weight(1f),
                    verticalArrangement = Arrangement.spacedBy(4.dp)
                ) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(6.dp)
                    ) {
                        Box(
                            modifier = Modifier
                                .size(6.dp)
                                .clip(CircleShape)
                                .background(TextDim)
                        )
                        Text(
                            text = "FALLBACK",
                            fontSize = 9.sp,
                            fontFamily = FontFamily.Monospace,
                            fontWeight = FontWeight.Bold,
                            letterSpacing = 0.8.sp,
                            color = TextSecondary
                        )
                    }
                    Text(
                        text = extractHost(fallbackUrl),
                        fontSize = 11.5.sp,
                        fontFamily = FontFamily.Monospace,
                        color = TextSecondary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                    PingBadge(ms = fallbackPingMs, pinging = pinging)
                }
            }

            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                OutlinedButton(
                    onClick = {
                        val oldP = primaryUrl
                        primaryUrl = fallbackUrl
                        fallbackUrl = oldP
                        val oldPing = primaryPingMs
                        primaryPingMs = fallbackPingMs
                        fallbackPingMs = oldPing
                        onSwap(
                            cfg.copy(
                                primaryUrl = primaryUrl,
                                fallbackUrl = fallbackUrl,
                                cfCleanIp = cfCleanIp
                            )
                        )
                    },
                    modifier = Modifier
                        .weight(1f)
                        .height(40.dp),
                    shape = RoundedCornerShape(10.dp),
                    border = BorderStroke(1.dp, BorderHairline),
                    colors = ButtonDefaults.outlinedButtonColors(
                        containerColor = SurfaceElevated,
                        contentColor = TextPrimary
                    ),
                    contentPadding = PaddingValues(horizontal = 10.dp)
                ) {
                    SwapArrowsIcon(color = SignalEmeraldBright, modifier = Modifier.size(14.dp))
                    Spacer(Modifier.width(6.dp))
                    Text(
                        text = "Swap",
                        fontSize = 12.sp,
                        fontWeight = FontWeight.SemiBold,
                        color = TextPrimary
                    )
                }

                OutlinedButton(
                    enabled = !pinging,
                    onClick = {
                        pinging = true
                        scope.launch {
                            val (pMs, fMs) = withContext(Dispatchers.IO) {
                                coroutineScope {
                                    val d1 = async {
                                        try {
                                            Aethermobile.pingEndpoint(primaryUrl)
                                        } catch (_: Throwable) {
                                            -1L
                                        }
                                    }
                                    val d2 = async {
                                        try {
                                            Aethermobile.pingEndpoint(fallbackUrl)
                                        } catch (_: Throwable) {
                                            -1L
                                        }
                                    }
                                    d1.await() to d2.await()
                                }
                            }
                            primaryPingMs = pMs
                            fallbackPingMs = fMs
                            pinging = false
                        }
                    },
                    modifier = Modifier
                        .weight(1f)
                        .height(40.dp),
                    shape = RoundedCornerShape(10.dp),
                    border = BorderStroke(1.dp, SignalEmerald.copy(alpha = 0.42f)),
                    colors = ButtonDefaults.outlinedButtonColors(
                        containerColor = SignalEmerald.copy(alpha = 0.10f),
                        contentColor = SignalEmeraldBright
                    ),
                    contentPadding = PaddingValues(horizontal = 10.dp)
                ) {
                    PulseIcon(color = SignalEmeraldBright, modifier = Modifier.size(14.dp))
                    Spacer(Modifier.width(6.dp))
                    Text(
                        text = if (pinging) "Testing..." else "Test Latency",
                        fontSize = 12.sp,
                        fontWeight = FontWeight.SemiBold,
                        color = SignalEmeraldBright,
                        maxLines = 1
                    )
                }
            }
        }

        // Endpoints Editor Card
        SectionCard(title = "ENDPOINTS") {
            AetherTextField(
                value = primaryUrl,
                onValueChange = { primaryUrl = it },
                label = "Primary Endpoint URL",
                placeholder = "wss://aether-server-production.up.railway.app/ws/sync"
            )
            AetherTextField(
                value = fallbackUrl,
                onValueChange = { fallbackUrl = it },
                label = "Fallback Endpoint URL",
                placeholder = "wss://sync.novv.tech/ws/sync"
            )
            AetherTextField(
                value = cfCleanIp,
                onValueChange = { cfCleanIp = it },
                label = "Optional Cloudflare Clean IP",
                placeholder = "Leave blank for automatic DNS"
            )
        }

        PrimaryActionButton(
            label = "Save & Sync",
            onClick = {
                onSaveAndSync(
                    cfg.copy(
                        primaryUrl = primaryUrl.trim(),
                        fallbackUrl = fallbackUrl.trim(),
                        cfCleanIp = cfCleanIp.trim()
                    )
                )
            }
        )
    }
}

@Composable
private fun PingBadge(ms: Long?, pinging: Boolean) {
    val (text, color) = when {
        pinging -> "Pinging..." to TextDim
        ms == null -> "Untested" to TextDim
        ms < 0L -> "Unreachable" to SignalRose
        ms <= 120L -> "$ms ms" to SignalEmeraldBright
        ms <= 280L -> "$ms ms" to SignalAmber
        else -> "$ms ms" to SignalRose
    }
    Box(
        modifier = Modifier
            .clip(RoundedCornerShape(6.dp))
            .background(color.copy(alpha = 0.12f))
            .border(1.dp, color.copy(alpha = 0.30f), RoundedCornerShape(6.dp))
            .padding(horizontal = 7.dp, vertical = 2.dp)
    ) {
        Text(
            text = text,
            fontSize = 9.5.sp,
            fontFamily = FontFamily.Monospace,
            fontWeight = FontWeight.SemiBold,
            color = color
        )
    }
}

// ---------------------------------------------------------------------------
// SUB-SCREEN 2: PROTOCOL & TRANSPORT
// ---------------------------------------------------------------------------

@Composable
private fun ProtocolSubScreen(
    cfg: AetherConfig,
    onApplyAndSync: (AetherConfig) -> Unit
) {
    val activeProfile = cfg.protocolProfile.ifBlank { "mux" }
    var selectedTransport by remember(cfg.transport) { mutableStateOf(cfg.transport.ifBlank { "auto" }) }
    var selectedCarriers by remember(cfg.carriers) { mutableIntStateOf(cfg.carriers.coerceIn(1, 6)) }
    var selectedWindow by remember(cfg.windowProfile) { mutableStateOf(cfg.windowProfile.ifBlank { "max" }) }
    var idlePower by remember(cfg.idlePower) { mutableStateOf(cfg.idlePower) }

    val protocolModes = listOf(
        Triple("mux", "Aether-Mux", "Default X25519 + AEAD multi-path (Auto · Chrome · Balanced)"),
        Triple("fast", "Aether-Fast", "Max speed, zero double-encrypt (TLS-Only · WS · Off pad · 60KB/8MB)"),
        Triple("ghost", "Aether-Ghost", "Heavy anti-DPI stealth (XHTTP · Random uTLS · Strict TLS Frag · Stealth pad)")
    )

    val transports = listOf(
        Triple("auto", "AUTO", "Smart fallback (WS → XHTTP → gRPC → H2)"),
        Triple("ws", "WebSocket", "Fastest primary on Railway & Cloudflare"),
        Triple("xhttp", "XHTTP", "Xray-style SplitHTTP (bypasses strict WS blocks)"),
        Triple("grpc", "gRPC", "HTTP/2 gRPC binary stream disguise"),
        Triple("h2", "HTTP/2", "Direct HTTP/2 duplex tunnel")
    )

    val windows = listOf(
        Triple("max", "Max Speed", "60KB / 8MB"),
        Triple("balanced", "Balanced", "32KB / 4MB"),
        Triple("gaming", "Gaming", "16KB / 1MB")
    )

    Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
        SectionCard(
            title = "1-TAP AETHER PROTOCOL MODE",
            trailing = {
                Text(
                    text = protocolProfileLabel(activeProfile),
                    fontSize = 11.sp,
                    fontFamily = FontFamily.Monospace,
                    fontWeight = FontWeight.SemiBold,
                    color = SignalEmeraldBright
                )
            }
        ) {
            protocolModes.forEach { (key, title, desc) ->
                SelectableOptionCard(
                    title = title,
                    description = desc,
                    selected = activeProfile.equals(key, ignoreCase = true),
                    onClick = {
                        val applied = cfg.withProtocolProfile(key)
                        selectedTransport = applied.transport
                        selectedCarriers = applied.carriers
                        selectedWindow = applied.windowProfile
                        onApplyAndSync(applied)
                    }
                )
            }
        }

        SectionCard(title = "TRANSPORT PROTOCOL") {
            transports.forEach { (key, title, desc) ->
                SelectableOptionCard(
                    title = title,
                    description = desc,
                    selected = selectedTransport.equals(key, ignoreCase = true),
                    onClick = { selectedTransport = key }
                )
            }
        }

        SectionCard(
            title = "PARALLEL CARRIERS",
            trailing = {
                Text(
                    text = "$selectedCarriers active pool",
                    fontSize = 11.sp,
                    fontFamily = FontFamily.Monospace,
                    color = SignalEmeraldBright
                )
            }
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                listOf(
                    2 to "LIGHT",
                    3 to "BALANCED",
                    4 to "OPTIMAL",
                    6 to "MAX"
                ).forEach { (count, tag) ->
                    val sel = selectedCarriers == count
                    Column(
                        modifier = Modifier
                            .weight(1f)
                            .clip(RoundedCornerShape(12.dp))
                            .background(if (sel) SignalEmerald.copy(alpha = 0.14f) else SurfaceRecessed)
                            .border(
                                width = 1.dp,
                                color = if (sel) SignalEmerald else BorderHairline,
                                shape = RoundedCornerShape(12.dp)
                            )
                            .clickable { selectedCarriers = count }
                            .padding(vertical = 10.dp),
                        horizontalAlignment = Alignment.CenterHorizontally,
                        verticalArrangement = Arrangement.spacedBy(2.dp)
                    ) {
                        Text(
                            text = "$count",
                            fontSize = 16.sp,
                            fontFamily = FontFamily.Monospace,
                            fontWeight = FontWeight.Bold,
                            color = if (sel) SignalEmeraldBright else TextPrimary
                        )
                        Text(
                            text = tag,
                            fontSize = 8.sp,
                            fontFamily = FontFamily.Monospace,
                            fontWeight = FontWeight.SemiBold,
                            letterSpacing = 0.5.sp,
                            color = if (sel) SignalEmeraldBright else TextDim
                        )
                    }
                }
            }
        }

        SectionCard(
            title = "CHUNK & WINDOW PROFILE",
            trailing = {
                Text(
                    text = windowProfileDetail(selectedWindow),
                    fontSize = 10.5.sp,
                    fontFamily = FontFamily.Monospace,
                    color = SignalEmeraldBright
                )
            }
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                windows.forEach { (key, title, detail) ->
                    SegmentedChipWithSub(
                        label = title,
                        sublabel = detail,
                        selected = selectedWindow.equals(key, ignoreCase = true),
                        onClick = { selectedWindow = key },
                        modifier = Modifier.weight(1f)
                    )
                }
            }
        }

        SectionCard(title = "POWER & CARRIER SCALING") {
            SwitchSettingRow(
                title = "Idle Power Mode",
                subtitle = "Scale 4 → 1 carrier when idle > 60s to save battery",
                checked = idlePower,
                onCheckedChange = { idlePower = it }
            )
        }

        PrimaryActionButton(
            label = "Apply & Sync",
            onClick = {
                val next = cfg.copy(
                    transport = selectedTransport,
                    carriers = selectedCarriers.coerceIn(1, 6),
                    windowProfile = selectedWindow,
                    idlePower = idlePower
                )
                onApplyAndSync(next.copy(protocolProfile = inferProtocolProfile(next)))
            }
        )
    }
}

// ---------------------------------------------------------------------------
// SUB-SCREEN 3: ENCRYPTION & STEALTH
// ---------------------------------------------------------------------------

@Composable
private fun EncryptionSubScreen(
    cfg: AetherConfig,
    onApplyAndSync: (AetherConfig) -> Unit
) {
    var selectedCipher by remember(cfg.cipher) { mutableStateOf(cfg.cipher.ifBlank { "xchacha20-poly1305" }) }
    var selectedFingerprint by remember(cfg.fingerprint) { mutableStateOf(cfg.fingerprint.ifBlank { "chrome" }) }
    var selectedPadding by remember(cfg.paddingMode) { mutableStateOf(cfg.paddingMode.ifBlank { "balanced" }) }
    var selectedFragment by remember(cfg.fragmentMode) { mutableStateOf(cfg.fragmentMode.ifBlank { "off" }) }
    var customPathDraft by remember(cfg.customPath) { mutableStateOf(normalizedPath(cfg.customPath)) }

    val ciphers = listOf(
        Triple("xchacha20-poly1305", "XChaCha20-Poly1305", "Default, balanced ARM64 speed"),
        Triple("aes-256-gcm", "AES-256-GCM", "Hardware-accelerated AES-NI / ARMv8"),
        Triple("aes-128-gcm", "AES-128-GCM", "Ultra-low-latency hardware AEAD"),
        Triple("chacha20-poly1305", "ChaCha20-Poly1305", "Standard RFC 8439 AEAD"),
        Triple("none", "TLS-Only (VLESS)", "Zero double-encryption, max speed over uTLS 1.3")
    )

    val fragments = listOf(
        Triple("off", "Off", "Standard"),
        Triple("balanced", "Balanced", "40–80B"),
        Triple("strict", "Strict", "10–30B")
    )

    val pathPresets = listOf("/ws/sync", "/api/v2/events", "/cdn/assets/sync")

    Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
        SectionCard(title = "CIPHER SUITE") {
            ciphers.forEach { (key, title, desc) ->
                SelectableOptionCard(
                    title = title,
                    description = desc,
                    selected = selectedCipher.equals(key, ignoreCase = true),
                    onClick = { selectedCipher = key }
                )
            }
        }

        SectionCard(title = "uTLS BROWSER FINGERPRINT") {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                listOf("chrome" to "Chrome", "firefox" to "Firefox", "safari" to "Safari").forEach { (key, label) ->
                    SelectableChip(
                        label = label,
                        selected = selectedFingerprint.equals(key, ignoreCase = true),
                        onClick = { selectedFingerprint = key },
                        modifier = Modifier.weight(1f)
                    )
                }
            }
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                listOf("ios" to "iOS", "randomized" to "Random").forEach { (key, label) ->
                    SelectableChip(
                        label = label,
                        selected = selectedFingerprint.equals(key, ignoreCase = true),
                        onClick = { selectedFingerprint = key },
                        modifier = Modifier.weight(1f)
                    )
                }
            }
        }

        SectionCard(title = "ANTI-DPI PADDING") {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                listOf(
                    "balanced" to "Balanced",
                    "stealth" to "Stealth",
                    "off" to "Off"
                ).forEach { (key, label) ->
                    SelectableChip(
                        label = label,
                        selected = selectedPadding.equals(key, ignoreCase = true),
                        onClick = { selectedPadding = key },
                        modifier = Modifier.weight(1f)
                    )
                }
            }
        }

        SectionCard(title = "TLS CLIENTHELLO FRAGMENTATION") {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                fragments.forEach { (key, label, range) ->
                    SegmentedChipWithSub(
                        label = label,
                        sublabel = range,
                        selected = selectedFragment.equals(key, ignoreCase = true),
                        onClick = { selectedFragment = key },
                        modifier = Modifier.weight(1f)
                    )
                }
            }
        }

        SectionCard(title = "CUSTOM CAMOUFLAGE PATH") {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(7.dp)
            ) {
                pathPresets.forEach { preset ->
                    val sel = normalizedPath(customPathDraft).equals(preset, ignoreCase = true)
                    Box(
                        modifier = Modifier
                            .weight(1f)
                            .clip(RoundedCornerShape(10.dp))
                            .background(if (sel) SignalEmerald.copy(alpha = 0.14f) else SurfaceRecessed)
                            .border(
                                width = 1.dp,
                                color = if (sel) SignalEmerald else BorderHairline,
                                shape = RoundedCornerShape(10.dp)
                            )
                            .clickable { customPathDraft = preset }
                            .padding(horizontal = 6.dp, vertical = 9.dp),
                        contentAlignment = Alignment.Center
                    ) {
                        Text(
                            text = preset,
                            fontSize = 10.sp,
                            fontFamily = FontFamily.Monospace,
                            fontWeight = if (sel) FontWeight.Bold else FontWeight.Medium,
                            color = if (sel) SignalEmeraldBright else TextSecondary,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis
                        )
                    }
                }
            }

            AetherTextField(
                value = customPathDraft,
                onValueChange = { customPathDraft = it },
                label = "Custom Endpoint Path",
                placeholder = "/ws/sync"
            )
        }

        PrimaryActionButton(
            label = "Apply & Sync",
            onClick = {
                val next = cfg.copy(
                    cipher = selectedCipher,
                    fingerprint = selectedFingerprint,
                    paddingMode = selectedPadding,
                    fragmentMode = selectedFragment,
                    customPath = normalizedPath(customPathDraft)
                )
                onApplyAndSync(next.copy(protocolProfile = inferProtocolProfile(next)))
            }
        )
    }
}

// ---------------------------------------------------------------------------
// SUB-SCREEN 4: DNS & SHIELD
// ---------------------------------------------------------------------------

@Composable
private fun DnsShieldSubScreen(
    cfg: AetherConfig,
    onSyncToRailway: (AetherConfig) -> Unit,
    onPullFromRailway: (onDone: () -> Unit) -> Unit
) {
    var selectedDns by remember(cfg.dnsPreset) { mutableStateOf(cfg.dnsPreset.ifBlank { "adguard" }) }
    var customDns by remember(cfg.customDns) { mutableStateOf(cfg.customDns) }
    var selectedQuic by remember(cfg.quicMode) { mutableStateOf(cfg.quicMode.ifBlank { "full" }) }
    var blockDot by remember(cfg.blockDot) { mutableStateOf(cfg.blockDot) }
    var adblockSni by remember(cfg.adblockSni) { mutableStateOf(cfg.adblockSni) }
    var autoSync by remember(cfg.autoSync) { mutableStateOf(cfg.autoSync) }
    var pulling by remember { mutableStateOf(false) }

    val dnsPresets = listOf(
        Triple("adguard", "AdGuard DNS", "94.140.14.14 · Ad & tracker blocking"),
        Triple("cloudflare", "Cloudflare", "1.1.1.1 · Ultra-low-latency resolver"),
        Triple("google", "Google", "8.8.8.8 · Global Anycast resolver"),
        Triple("custom", "Custom", "Use your own upstream DNS server(s)")
    )

    val quicModes = listOf(
        Triple("full", "Full QUIC", "All UDP"),
        Triple("force_tcp", "Force TCP", "Block UDP 443"),
        Triple("dns_only", "DNS Only", "UDP 53 Only")
    )

    Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
        SectionCard(title = "UPSTREAM DNS PRESET") {
            dnsPresets.forEach { (key, title, desc) ->
                SelectableOptionCard(
                    title = title,
                    description = desc,
                    selected = selectedDns.equals(key, ignoreCase = true),
                    onClick = { selectedDns = key }
                )
            }

            if (selectedDns.equals("custom", ignoreCase = true)) {
                AetherTextField(
                    value = customDns,
                    onValueChange = { customDns = it },
                    label = "Custom DNS Servers",
                    placeholder = "9.9.9.9:53, 149.112.112.112:53"
                )
            }
        }

        SectionCard(
            title = "QUIC / UDP MODE",
            trailing = {
                Text(
                    text = "LIVE · NO RESTART",
                    fontSize = 9.5.sp,
                    fontFamily = FontFamily.Monospace,
                    fontWeight = FontWeight.SemiBold,
                    color = SignalEmeraldBright
                )
            }
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                quicModes.forEach { (key, title, detail) ->
                    SegmentedChipWithSub(
                        label = title,
                        sublabel = detail,
                        selected = selectedQuic.equals(key, ignoreCase = true),
                        onClick = {
                            selectedQuic = key
                            onSyncToRailway(
                                cfg.copy(
                                    dnsPreset = selectedDns,
                                    customDns = customDns.trim(),
                                    quicMode = key,
                                    blockDot = blockDot,
                                    adblockSni = adblockSni,
                                    autoSync = autoSync
                                )
                            )
                        },
                        modifier = Modifier.weight(1f)
                    )
                }
            }
        }

        SectionCard(title = "SHIELD & LIVE SYNC") {
            SwitchSettingRow(
                title = "Block DoT (Port 853)",
                subtitle = "Force apps through tunnel DNS without silent bypass",
                checked = blockDot,
                onCheckedChange = { blockDot = it }
            )
            HorizontalDivider(color = BorderHairline, thickness = 1.dp)
            SwitchSettingRow(
                title = "SNI AdBlock",
                subtitle = "Drop ad & tracker TLS handshakes on Railway server",
                checked = adblockSni,
                onCheckedChange = { adblockSni = it }
            )
            HorizontalDivider(color = BorderHairline, thickness = 1.dp)
            SwitchSettingRow(
                title = "Auto-Sync with Railway",
                subtitle = "Auto-apply live config changes pushed from dashboard",
                checked = autoSync,
                onCheckedChange = { autoSync = it }
            )
        }

        PrimaryActionButton(
            label = "Sync to Railway",
            onClick = {
                onSyncToRailway(
                    cfg.copy(
                        dnsPreset = selectedDns,
                        customDns = customDns.trim(),
                        quicMode = selectedQuic,
                        blockDot = blockDot,
                        adblockSni = adblockSni,
                        autoSync = autoSync
                    )
                )
            }
        )

        OutlinedButton(
            enabled = !pulling,
            onClick = {
                pulling = true
                onPullFromRailway { pulling = false }
            },
            modifier = Modifier
                .fillMaxWidth()
                .height(48.dp),
            shape = RoundedCornerShape(14.dp),
            border = BorderStroke(1.dp, BorderHairline),
            colors = ButtonDefaults.outlinedButtonColors(
                containerColor = SurfaceElevated,
                contentColor = TextPrimary
            )
        ) {
            SyncArrowsIcon(color = SignalEmeraldBright, modifier = Modifier.size(16.dp))
            Spacer(Modifier.width(8.dp))
            Text(
                text = if (pulling) "Pulling from Railway..." else "Pull from Railway",
                fontSize = 13.sp,
                fontWeight = FontWeight.SemiBold,
                color = TextPrimary
            )
        }
    }
}

// ---------------------------------------------------------------------------
// SUB-SCREEN 5: SPLIT TUNNELING & BYPASS APPS (120 FPS LazyColumn)
// ---------------------------------------------------------------------------

@Composable
private fun BypassAppsSubScreen(
    cfg: AetherConfig,
    running: Boolean,
    onUpdateBypass: (AetherConfig) -> Unit,
    onReconnectNow: (AetherConfig) -> Unit,
    modifier: Modifier = Modifier
) {
    val context = LocalContext.current
    var searchQuery by remember { mutableStateOf("") }
    var showSystemApps by remember { mutableStateOf(false) }
    var loading by remember { mutableStateOf(true) }
    var installedApps by remember { mutableStateOf<List<InstalledAppItem>>(emptyList()) }
    var dirtyWhileConnected by remember { mutableStateOf(false) }

    val bypassedSet = remember(cfg.bypassApps) { cfg.bypassSet() }

    LaunchedEffect(Unit) {
        val loaded = withContext(Dispatchers.IO) {
            val pm = context.packageManager
            val selfPkg = context.packageName
            val rawApps = try {
                pm.getInstalledApplications(PackageManager.GET_META_DATA)
            } catch (_: Throwable) {
                emptyList()
            }
            rawApps
                .asSequence()
                .filter { it.packageName != selfPkg }
                .map { appInfo ->
                    val label = try {
                        pm.getApplicationLabel(appInfo)?.toString()?.ifBlank { appInfo.packageName }
                            ?: appInfo.packageName
                    } catch (_: Throwable) {
                        appInfo.packageName
                    }
                    val isSys = (appInfo.flags and ApplicationInfo.FLAG_SYSTEM) != 0 &&
                        (appInfo.flags and ApplicationInfo.FLAG_UPDATED_SYSTEM_APP) == 0
                    val iconBmp: ImageBitmap? = try {
                        val drawable = pm.getApplicationIcon(appInfo)
                        val bmp = Bitmap.createBitmap(72, 72, Bitmap.Config.ARGB_8888)
                        val c = AndroidCanvas(bmp)
                        drawable.setBounds(0, 0, 72, 72)
                        drawable.draw(c)
                        bmp.asImageBitmap()
                    } catch (_: Throwable) {
                        null
                    }
                    InstalledAppItem(
                        packageName = appInfo.packageName,
                        label = label,
                        isSystem = isSys,
                        icon = iconBmp
                    )
                }
                .toList()
        }
        installedApps = loaded
        loading = false
    }

    val filteredApps = remember(installedApps, searchQuery, showSystemApps, bypassedSet) {
        val q = searchQuery.trim().lowercase(Locale.US)
        installedApps
            .filter { item ->
                val visOk = showSystemApps || !item.isSystem || (item.packageName in bypassedSet)
                val qOk = q.isEmpty() ||
                    item.label.lowercase(Locale.US).contains(q) ||
                    item.packageName.lowercase(Locale.US).contains(q)
                visOk && qOk
            }
            .sortedWith(
                compareByDescending<InstalledAppItem> { it.packageName in bypassedSet }
                    .thenBy { it.label.lowercase(Locale.US) }
            )
    }

    Column(
        modifier = modifier,
        verticalArrangement = Arrangement.spacedBy(10.dp)
    ) {
        // Compact LAN Bypass Switch Card
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(15.dp))
                .background(SurfaceCard)
                .border(1.dp, BorderHairline, RoundedCornerShape(15.dp))
                .padding(horizontal = 14.dp, vertical = 10.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            Column(
                modifier = Modifier
                    .weight(1f)
                    .padding(end = 10.dp),
                verticalArrangement = Arrangement.spacedBy(2.dp)
            ) {
                Text(
                    text = "Bypass Local LAN",
                    fontSize = 13.5.sp,
                    fontWeight = FontWeight.SemiBold,
                    color = TextPrimary
                )
                Text(
                    text = "Keep local Wi-Fi devices direct",
                    fontSize = 11.sp,
                    color = TextDim
                )
            }
            Switch(
                checked = cfg.bypassLan,
                onCheckedChange = { checked ->
                    onUpdateBypass(cfg.copy(bypassLan = checked))
                    if (running) dirtyWhileConnected = true
                },
                colors = SwitchDefaults.colors(
                    checkedThumbColor = OnEmeraldDark,
                    checkedTrackColor = SignalEmerald,
                    checkedBorderColor = SignalEmerald,
                    uncheckedThumbColor = TextSecondary,
                    uncheckedTrackColor = SurfaceRecessed,
                    uncheckedBorderColor = BorderHairline
                )
            )
        }

        if (running && dirtyWhileConnected) {
            PrimaryActionButton(
                label = "Apply Bypass (Reconnect Tunnel)",
                onClick = {
                    dirtyWhileConnected = false
                    onReconnectNow(cfg)
                }
            )
        }

        // Search Bar
        AetherTextField(
            value = searchQuery,
            onValueChange = { searchQuery = it },
            label = "Search installed apps...",
            placeholder = "App name or package",
            trailingIcon = if (searchQuery.isNotEmpty()) {
                {
                    TextButton(onClick = { searchQuery = "" }) {
                        Text(
                            text = "Clear",
                            fontSize = 11.sp,
                            color = TextSecondary
                        )
                    }
                }
            } else {
                null
            }
        )

        // Filter & Excluded Count Strip
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            Row(
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                Box(
                    modifier = Modifier
                        .clip(RoundedCornerShape(9.dp))
                        .background(if (showSystemApps) SignalEmerald.copy(alpha = 0.14f) else SurfaceCard)
                        .border(
                            1.dp,
                            if (showSystemApps) SignalEmerald.copy(alpha = 0.55f) else BorderHairline,
                            RoundedCornerShape(9.dp)
                        )
                        .clickable { showSystemApps = !showSystemApps }
                        .padding(horizontal = 10.dp, vertical = 6.dp)
                ) {
                    Text(
                        text = "System Apps",
                        fontSize = 11.sp,
                        fontWeight = FontWeight.SemiBold,
                        color = if (showSystemApps) SignalEmeraldBright else TextSecondary
                    )
                }

                Box(
                    modifier = Modifier
                        .clip(RoundedCornerShape(9.dp))
                        .background(SignalEmerald.copy(alpha = 0.12f))
                        .border(1.dp, SignalEmerald.copy(alpha = 0.35f), RoundedCornerShape(9.dp))
                        .padding(horizontal = 9.dp, vertical = 6.dp)
                ) {
                    Text(
                        text = "Excluded: ${bypassedSet.size}",
                        fontSize = 11.sp,
                        fontFamily = FontFamily.Monospace,
                        fontWeight = FontWeight.SemiBold,
                        color = SignalEmeraldBright
                    )
                }
            }

            if (bypassedSet.isNotEmpty()) {
                Box(
                    modifier = Modifier
                        .clip(RoundedCornerShape(9.dp))
                        .background(SignalRose.copy(alpha = 0.12f))
                        .border(1.dp, SignalRose.copy(alpha = 0.35f), RoundedCornerShape(9.dp))
                        .clickable {
                            onUpdateBypass(cfg.copy(bypassApps = ""))
                            if (running) dirtyWhileConnected = true
                        }
                        .padding(horizontal = 10.dp, vertical = 6.dp)
                ) {
                    Text(
                        text = "Clear",
                        fontSize = 11.sp,
                        fontWeight = FontWeight.SemiBold,
                        color = Color(0xFFFDA4AF)
                    )
                }
            }
        }

        // High-FPS App List
        if (loading) {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f),
                contentAlignment = Alignment.Center
            ) {
                Text(
                    text = "Scanning installed apps...",
                    fontSize = 12.sp,
                    fontFamily = FontFamily.Monospace,
                    color = TextDim
                )
            }
        } else if (filteredApps.isEmpty()) {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f),
                contentAlignment = Alignment.Center
            ) {
                Text(
                    text = "No matching apps found",
                    fontSize = 12.sp,
                    color = TextDim
                )
            }
        } else {
            LazyColumn(
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f),
                contentPadding = PaddingValues(top = 2.dp, bottom = 116.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                items(filteredApps, key = { it.packageName }) { app ->
                    val excluded = app.packageName in bypassedSet
                    AppBypassRow(
                        app = app,
                        excluded = excluded,
                        onToggle = {
                            val next = bypassedSet.toMutableSet()
                            if (excluded) next.remove(app.packageName) else next.add(app.packageName)
                            val csv = next.sorted().joinToString(",")
                            onUpdateBypass(cfg.copy(bypassApps = csv))
                            if (running) dirtyWhileConnected = true
                        }
                    )
                }
            }
        }
    }
}

@Composable
private fun AppBypassRow(
    app: InstalledAppItem,
    excluded: Boolean,
    onToggle: () -> Unit
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(14.dp))
            .background(if (excluded) SignalEmerald.copy(alpha = 0.09f) else SurfaceCard)
            .border(
                width = 1.dp,
                color = if (excluded) SignalEmerald.copy(alpha = 0.45f) else BorderHairline,
                shape = RoundedCornerShape(14.dp)
            )
            .clickable(onClick = onToggle)
            .padding(horizontal = 12.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(11.dp)
    ) {
        if (app.icon != null) {
            Image(
                bitmap = app.icon,
                contentDescription = app.label,
                modifier = Modifier
                    .size(36.dp)
                    .clip(RoundedCornerShape(9.dp))
            )
        } else {
            Box(
                modifier = Modifier
                    .size(36.dp)
                    .clip(RoundedCornerShape(9.dp))
                    .background(SurfaceElevated)
                    .border(1.dp, BorderHairline, RoundedCornerShape(9.dp)),
                contentAlignment = Alignment.Center
            ) {
                Text(
                    text = app.label.firstOrNull()?.uppercaseChar()?.toString() ?: "A",
                    fontSize = 14.sp,
                    fontWeight = FontWeight.Bold,
                    color = SignalEmeraldBright
                )
            }
        }

        Column(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(1.dp)
        ) {
            Text(
                text = app.label,
                fontSize = 13.sp,
                fontWeight = FontWeight.SemiBold,
                color = TextPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
            Text(
                text = app.packageName,
                fontSize = 10.sp,
                fontFamily = FontFamily.Monospace,
                color = TextDim,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
        }

        // Crisp custom checkbox badge
        Box(
            modifier = Modifier
                .size(22.dp)
                .clip(RoundedCornerShape(6.dp))
                .background(if (excluded) SignalEmerald else SurfaceRecessed)
                .border(
                    width = 1.2.dp,
                    color = if (excluded) SignalEmeraldBright else BorderHairline,
                    shape = RoundedCornerShape(6.dp)
                ),
            contentAlignment = Alignment.Center
        ) {
            if (excluded) {
                CheckmarkIcon(color = OnEmeraldDark, modifier = Modifier.size(13.dp))
            }
        }
    }
}

// ---------------------------------------------------------------------------
// SUB-SCREEN 6: ACCESS KEY & ENGINE
// ---------------------------------------------------------------------------

@Composable
private fun AccessKeySubScreen(
    cfg: AetherConfig,
    onCopyPsk: (String) -> Unit,
    onSaveKey: (AetherConfig) -> Unit
) {
    var pskDraft by remember(cfg.psk) { mutableStateOf(cfg.psk) }
    var showKey by remember { mutableStateOf(false) }

    Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
        SectionCard(
            title = "AUTHENTICATION",
            trailing = {
                Row(
                    modifier = Modifier
                        .clip(RoundedCornerShape(8.dp))
                        .background(SurfaceElevated)
                        .border(1.dp, BorderHairline, RoundedCornerShape(8.dp))
                        .clickable { onCopyPsk(pskDraft) }
                        .padding(horizontal = 9.dp, vertical = 4.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(5.dp)
                ) {
                    ShareCopyIcon(color = TextSecondary, modifier = Modifier.size(12.dp))
                    Text(
                        text = "Copy",
                        fontSize = 11.sp,
                        fontWeight = FontWeight.Medium,
                        color = TextSecondary
                    )
                }
            }
        ) {
            AetherTextField(
                value = pskDraft,
                onValueChange = { pskDraft = it },
                label = "Secret Key (AETHER_PSK)",
                placeholder = "Paste shared tunnel key",
                visualTransformation = if (showKey) VisualTransformation.None else PasswordVisualTransformation(),
                trailingIcon = {
                    TextButton(onClick = { showKey = !showKey }) {
                        Text(
                            text = if (showKey) "Hide" else "Show",
                            fontSize = 12.sp,
                            fontWeight = FontWeight.SemiBold,
                            color = SignalEmeraldBright
                        )
                    }
                }
            )

            PrimaryActionButton(
                label = "Save Key",
                onClick = {
                    onSaveKey(cfg.copy(psk = pskDraft.trim()))
                }
            )
        }

        SectionCard(title = "ENGINE SPECIFICATION") {
            val specs = listOf(
                "Protocol" to "Aether v4.1 · ${protocolProfileLabel(cfg.protocolProfile)}",
                "Active Cipher" to fullCipherName(cfg.cipher),
                "Transport" to "${transportLabel(cfg.transport)} · ${cfg.carriers} Carriers",
                "Window & Power" to "${windowProfileShort(cfg.windowProfile)} (${windowProfileDetail(cfg.windowProfile)}) · ${if (cfg.idlePower) "Idle Save" else "Full Pool"}",
                "Stealth uTLS" to "${fingerprintLabel(cfg.fingerprint)} · ${paddingLabel(cfg.paddingMode)} · Frag ${fragmentLabel(cfg.fragmentMode)}",
                "Camouflage Path" to normalizedPath(cfg.customPath),
                "DNS Cache" to "${dnsLabel(cfg.dnsPreset)} · 60s Hedged",
                "UDP / QUIC" to "${quicLabel(cfg.quicMode)} · MTU 1500",
                "Build" to "v4.1.1 (16)"
            )
            Column {
                specs.forEachIndexed { idx, (key, value) ->
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(vertical = 8.dp),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Text(
                            text = key,
                            fontSize = 12.sp,
                            color = TextDim
                        )
                        Spacer(Modifier.width(12.dp))
                        Text(
                            text = value,
                            fontSize = 11.5.sp,
                            fontFamily = FontFamily.Monospace,
                            fontWeight = FontWeight.Medium,
                            color = TextPrimary,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis
                        )
                    }
                    if (idx < specs.lastIndex) {
                        HorizontalDivider(
                            color = BorderHairline.copy(alpha = 0.75f),
                            thickness = 1.dp
                        )
                    }
                }
            }
        }
    }
}

// ---------------------------------------------------------------------------
// SHARED UI PRIMITIVES
// ---------------------------------------------------------------------------

@Composable
private fun SectionCard(
    title: String,
    trailing: (@Composable () -> Unit)? = null,
    content: @Composable ColumnScope.() -> Unit
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(18.dp))
            .background(SurfaceCard)
            .border(1.dp, BorderHairline, RoundedCornerShape(18.dp))
            .padding(15.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp)
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text(
                text = title,
                fontSize = 10.sp,
                fontWeight = FontWeight.Bold,
                letterSpacing = 1.1.sp,
                color = TextDim
            )
            trailing?.invoke()
        }
        content()
    }
}

@Composable
private fun SelectableOptionCard(
    title: String,
    description: String,
    selected: Boolean,
    onClick: () -> Unit
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(13.dp))
            .background(if (selected) SignalEmerald.copy(alpha = 0.11f) else SurfaceRecessed)
            .border(
                width = 1.dp,
                color = if (selected) SignalEmerald.copy(alpha = 0.75f) else BorderHairline,
                shape = RoundedCornerShape(13.dp)
            )
            .clickable(onClick = onClick)
            .padding(horizontal = 13.dp, vertical = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(11.dp)
    ) {
        Box(
            modifier = Modifier
                .size(18.dp)
                .clip(CircleShape)
                .border(
                    width = 1.8.dp,
                    color = if (selected) SignalEmeraldBright else TextDim,
                    shape = CircleShape
                ),
            contentAlignment = Alignment.Center
        ) {
            if (selected) {
                Box(
                    modifier = Modifier
                        .size(9.dp)
                        .clip(CircleShape)
                        .background(SignalEmeraldBright)
                )
            }
        }
        Column(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(2.dp)
        ) {
            Text(
                text = title,
                fontSize = 13.5.sp,
                fontWeight = FontWeight.SemiBold,
                color = if (selected) SignalEmeraldBright else TextPrimary
            )
            Text(
                text = description,
                fontSize = 11.sp,
                color = TextSecondary,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis
            )
        }
    }
}

@Composable
private fun SelectableChip(
    label: String,
    selected: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier
) {
    Box(
        modifier = modifier
            .clip(RoundedCornerShape(11.dp))
            .background(if (selected) SignalEmerald.copy(alpha = 0.14f) else SurfaceRecessed)
            .border(
                width = 1.dp,
                color = if (selected) SignalEmerald else BorderHairline,
                shape = RoundedCornerShape(11.dp)
            )
            .clickable(onClick = onClick)
            .padding(vertical = 10.dp),
        contentAlignment = Alignment.Center
    ) {
        Text(
            text = label,
            fontSize = 12.sp,
            fontWeight = if (selected) FontWeight.Bold else FontWeight.Medium,
            color = if (selected) SignalEmeraldBright else TextSecondary,
            maxLines = 1
        )
    }
}

@Composable
private fun SegmentedChipWithSub(
    label: String,
    sublabel: String,
    selected: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier
) {
    Column(
        modifier = modifier
            .clip(RoundedCornerShape(11.dp))
            .background(if (selected) SignalEmerald.copy(alpha = 0.14f) else SurfaceRecessed)
            .border(
                width = 1.dp,
                color = if (selected) SignalEmerald else BorderHairline,
                shape = RoundedCornerShape(11.dp)
            )
            .clickable(onClick = onClick)
            .padding(horizontal = 6.dp, vertical = 9.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(2.dp)
    ) {
        Text(
            text = label,
            fontSize = 11.5.sp,
            fontWeight = if (selected) FontWeight.Bold else FontWeight.SemiBold,
            color = if (selected) SignalEmeraldBright else TextPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis
        )
        Text(
            text = sublabel,
            fontSize = 9.sp,
            fontFamily = FontFamily.Monospace,
            fontWeight = FontWeight.Medium,
            color = if (selected) SignalEmeraldBright.copy(alpha = 0.9f) else TextDim,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis
        )
    }
}

@Composable
private fun SwitchSettingRow(
    title: String,
    subtitle: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Column(
            modifier = Modifier
                .weight(1f)
                .padding(end = 12.dp),
            verticalArrangement = Arrangement.spacedBy(2.dp)
        ) {
            Text(
                text = title,
                fontSize = 13.5.sp,
                fontWeight = FontWeight.SemiBold,
                color = TextPrimary
            )
            Text(
                text = subtitle,
                fontSize = 11.sp,
                color = TextDim
            )
        }
        Switch(
            checked = checked,
            onCheckedChange = onCheckedChange,
            colors = SwitchDefaults.colors(
                checkedThumbColor = OnEmeraldDark,
                checkedTrackColor = SignalEmerald,
                checkedBorderColor = SignalEmerald,
                uncheckedThumbColor = TextSecondary,
                uncheckedTrackColor = SurfaceRecessed,
                uncheckedBorderColor = BorderHairline
            )
        )
    }
}

@Composable
private fun PrimaryActionButton(
    label: String,
    onClick: () -> Unit
) {
    Button(
        onClick = onClick,
        modifier = Modifier
            .fillMaxWidth()
            .height(48.dp),
        shape = RoundedCornerShape(14.dp),
        colors = ButtonDefaults.buttonColors(
            containerColor = SignalEmerald,
            contentColor = OnEmeraldDark
        )
    ) {
        Text(
            text = label,
            fontSize = 14.sp,
            fontWeight = FontWeight.Bold,
            color = OnEmeraldDark
        )
    }
}

@Composable
private fun AetherTextField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    placeholder: String = "",
    visualTransformation: VisualTransformation = VisualTransformation.None,
    trailingIcon: (@Composable () -> Unit)? = null
) {
    OutlinedTextField(
        value = value,
        onValueChange = onValueChange,
        label = { Text(label, fontSize = 12.sp) },
        placeholder = if (placeholder.isNotEmpty()) {
            { Text(placeholder, fontSize = 12.5.sp, color = TextDim) }
        } else {
            null
        },
        textStyle = TextStyle(
            fontSize = 12.5.sp,
            fontFamily = FontFamily.Monospace,
            color = TextPrimary
        ),
        singleLine = true,
        visualTransformation = visualTransformation,
        trailingIcon = trailingIcon,
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(12.dp),
        colors = OutlinedTextFieldDefaults.colors(
            focusedContainerColor = SurfaceRecessed,
            unfocusedContainerColor = SurfaceRecessed,
            focusedBorderColor = SignalEmerald,
            unfocusedBorderColor = BorderHairline,
            focusedTextColor = TextPrimary,
            unfocusedTextColor = TextPrimary,
            focusedLabelColor = SignalEmeraldBright,
            unfocusedLabelColor = TextDim,
            cursorColor = SignalEmeraldBright,
            focusedPlaceholderColor = TextDim,
            unfocusedPlaceholderColor = TextDim
        )
    )
}

// ---------------------------------------------------------------------------
// CUSTOM CRISP VECTOR ICONS VIA COMPOSE CANVAS (1.8dp rounded strokes)
// ---------------------------------------------------------------------------

@Composable
private fun BrandMarkIcon(active: Boolean, modifier: Modifier = Modifier) {
    val strokeColor by animateColorAsState(
        targetValue = if (active) SignalEmeraldBright else TextPrimary,
        animationSpec = tween(350),
        label = "brandStroke"
    )
    val nodeColor by animateColorAsState(
        targetValue = if (active) SignalEmeraldBright else SignalEmerald,
        animationSpec = tween(350),
        label = "brandNode"
    )

    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val stroke = Stroke(width = 1.8.dp.toPx(), cap = StrokeCap.Round, join = StrokeJoin.Round)

        val shield = Path().apply {
            moveTo(w * 0.50f, h * 0.10f)
            lineTo(w * 0.86f, h * 0.24f)
            lineTo(w * 0.86f, h * 0.52f)
            cubicTo(w * 0.86f, h * 0.74f, w * 0.68f, h * 0.86f, w * 0.50f, h * 0.92f)
            cubicTo(w * 0.32f, h * 0.86f, w * 0.14f, h * 0.74f, w * 0.14f, h * 0.52f)
            lineTo(w * 0.14f, h * 0.24f)
            close()
        }
        drawPath(path = shield, color = strokeColor, style = stroke)

        val center = Offset(w * 0.50f, h * 0.50f)
        drawCircle(
            color = nodeColor.copy(alpha = 0.25f),
            radius = w * 0.17f,
            center = center
        )
        drawCircle(
            color = nodeColor,
            radius = w * 0.085f,
            center = center
        )
    }
}

@Composable
private fun TunnelTabIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val stroke = Stroke(width = 1.8.dp.toPx(), cap = StrokeCap.Round, join = StrokeJoin.Round)
        val shield = Path().apply {
            moveTo(w * 0.50f, h * 0.10f)
            lineTo(w * 0.84f, h * 0.23f)
            lineTo(w * 0.84f, h * 0.52f)
            cubicTo(w * 0.84f, h * 0.73f, w * 0.68f, h * 0.85f, w * 0.50f, h * 0.91f)
            cubicTo(w * 0.32f, h * 0.85f, w * 0.16f, h * 0.73f, w * 0.16f, h * 0.52f)
            lineTo(w * 0.16f, h * 0.23f)
            close()
        }
        drawPath(path = shield, color = color, style = stroke)
        drawLine(
            color = color,
            start = Offset(w * 0.50f, h * 0.36f),
            end = Offset(w * 0.50f, h * 0.64f),
            strokeWidth = 1.8.dp.toPx(),
            cap = StrokeCap.Round
        )
    }
}

@Composable
private fun MenuHubIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        val stroke = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round)
        val knobR = w * 0.105f

        val rows = listOf(
            0.24f to 0.34f,
            0.50f to 0.68f,
            0.76f to 0.42f
        )
        rows.forEach { (yFrac, knobXFrac) ->
            val y = h * yFrac
            val kx = w * knobXFrac
            val leftEnd = (kx - knobR).coerceAtLeast(w * 0.12f)
            val rightStart = (kx + knobR).coerceAtMost(w * 0.88f)
            if (leftEnd > w * 0.12f) {
                drawLine(color = color, start = Offset(w * 0.12f, y), end = Offset(leftEnd, y), strokeWidth = sw, cap = StrokeCap.Round)
            }
            if (rightStart < w * 0.88f) {
                drawLine(color = color, start = Offset(rightStart, y), end = Offset(w * 0.88f, y), strokeWidth = sw, cap = StrokeCap.Round)
            }
            drawCircle(color = color, radius = knobR, center = Offset(kx, y), style = stroke)
        }
    }
}

@Composable
private fun RoutesTabIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        val stroke = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round)

        val n1 = Offset(w * 0.24f, h * 0.28f)
        val n2 = Offset(w * 0.24f, h * 0.74f)
        val n3 = Offset(w * 0.78f, h * 0.51f)
        val r = w * 0.115f

        drawLine(color = color, start = Offset(n1.x + r, n1.y + r * 0.35f), end = Offset(n3.x - r, n3.y - r * 0.35f), strokeWidth = sw, cap = StrokeCap.Round)
        drawLine(color = color, start = Offset(n2.x + r, n2.y - r * 0.35f), end = Offset(n3.x - r, n3.y + r * 0.35f), strokeWidth = sw, cap = StrokeCap.Round)
        drawCircle(color = color, radius = r, center = n1, style = stroke)
        drawCircle(color = color, radius = r, center = n2, style = stroke)
        drawCircle(color = color, radius = r, center = n3, style = stroke)
    }
}

@Composable
private fun ProtocolIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        val stroke = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round)
        // Stacked parallel stream layers
        val topLayer = Path().apply {
            moveTo(w * 0.50f, h * 0.14f)
            lineTo(w * 0.88f, h * 0.34f)
            lineTo(w * 0.50f, h * 0.54f)
            lineTo(w * 0.12f, h * 0.34f)
            close()
        }
        drawPath(path = topLayer, color = color, style = stroke)
        val midLayer = Path().apply {
            moveTo(w * 0.14f, h * 0.55f)
            lineTo(w * 0.50f, h * 0.74f)
            lineTo(w * 0.86f, h * 0.55f)
        }
        drawPath(path = midLayer, color = color, style = stroke)
        val botLayer = Path().apply {
            moveTo(w * 0.18f, h * 0.73f)
            lineTo(w * 0.50f, h * 0.90f)
            lineTo(w * 0.82f, h * 0.73f)
        }
        drawPath(path = botLayer, color = color, style = stroke)
    }
}

@Composable
private fun ShieldLockIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        val stroke = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round)
        drawRoundRect(
            color = color,
            topLeft = Offset(w * 0.20f, h * 0.44f),
            size = Size(w * 0.60f, h * 0.44f),
            cornerRadius = CornerRadius(3.dp.toPx(), 3.dp.toPx()),
            style = stroke
        )
        drawArc(
            color = color,
            startAngle = 180f,
            sweepAngle = 180f,
            useCenter = false,
            topLeft = Offset(w * 0.31f, h * 0.16f),
            size = Size(w * 0.38f, h * 0.44f),
            style = stroke
        )
        drawLine(
            color = color,
            start = Offset(w * 0.31f, h * 0.38f),
            end = Offset(w * 0.31f, h * 0.44f),
            strokeWidth = sw,
            cap = StrokeCap.Round
        )
        drawLine(
            color = color,
            start = Offset(w * 0.69f, h * 0.38f),
            end = Offset(w * 0.69f, h * 0.44f),
            strokeWidth = sw,
            cap = StrokeCap.Round
        )
        drawCircle(
            color = color,
            radius = w * 0.06f,
            center = Offset(w * 0.50f, h * 0.66f)
        )
    }
}

@Composable
private fun DnsGlobeIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        val stroke = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round)
        val r = size.minDimension * 0.38f
        val c = Offset(w * 0.50f, h * 0.50f)
        drawCircle(color = color, radius = r, center = c, style = stroke)
        drawOval(
            color = color,
            topLeft = Offset(c.x - r * 0.46f, c.y - r),
            size = Size(r * 0.92f, r * 2f),
            style = stroke
        )
        drawLine(
            color = color,
            start = Offset(c.x - r, c.y),
            end = Offset(c.x + r, c.y),
            strokeWidth = sw,
            cap = StrokeCap.Round
        )
    }
}

@Composable
private fun AppsGridIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        val stroke = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round)
        val cell = w * 0.30f
        val cr = CornerRadius(2.2.dp.toPx(), 2.2.dp.toPx())
        drawRoundRect(color = color, topLeft = Offset(w * 0.14f, h * 0.14f), size = Size(cell, cell), cornerRadius = cr, style = stroke)
        drawRoundRect(color = color, topLeft = Offset(w * 0.56f, h * 0.14f), size = Size(cell, cell), cornerRadius = cr, style = stroke)
        drawRoundRect(color = color, topLeft = Offset(w * 0.14f, h * 0.56f), size = Size(cell, cell), cornerRadius = cr, style = stroke)
        drawRoundRect(color = color, topLeft = Offset(w * 0.56f, h * 0.56f), size = Size(cell, cell), cornerRadius = cr, style = stroke)
    }
}

@Composable
private fun KeyIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        val stroke = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round)
        val headCenter = Offset(w * 0.31f, h * 0.50f)
        val headR = w * 0.17f
        drawCircle(color = color, radius = headR, center = headCenter, style = stroke)
        drawLine(
            color = color,
            start = Offset(headCenter.x + headR, h * 0.50f),
            end = Offset(w * 0.88f, h * 0.50f),
            strokeWidth = sw,
            cap = StrokeCap.Round
        )
        drawLine(
            color = color,
            start = Offset(w * 0.72f, h * 0.50f),
            end = Offset(w * 0.72f, h * 0.67f),
            strokeWidth = sw,
            cap = StrokeCap.Round
        )
        drawLine(
            color = color,
            start = Offset(w * 0.86f, h * 0.50f),
            end = Offset(w * 0.86f, h * 0.64f),
            strokeWidth = sw,
            cap = StrokeCap.Round
        )
    }
}

@Composable
private fun ChevronRightIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        val path = Path().apply {
            moveTo(w * 0.36f, h * 0.20f)
            lineTo(w * 0.68f, h * 0.50f)
            lineTo(w * 0.36f, h * 0.80f)
        }
        drawPath(path = path, color = color, style = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round))
    }
}

@Composable
private fun ChevronLeftIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        val path = Path().apply {
            moveTo(w * 0.66f, h * 0.20f)
            lineTo(w * 0.34f, h * 0.50f)
            lineTo(w * 0.66f, h * 0.80f)
        }
        drawPath(path = path, color = color, style = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round))
    }
}

@Composable
private fun CheckmarkIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 2.1.dp.toPx()
        val path = Path().apply {
            moveTo(w * 0.16f, h * 0.52f)
            lineTo(w * 0.42f, h * 0.78f)
            lineTo(w * 0.86f, h * 0.24f)
        }
        drawPath(path = path, color = color, style = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round))
    }
}

@Composable
private fun PulseIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        val path = Path().apply {
            moveTo(w * 0.10f, h * 0.54f)
            lineTo(w * 0.30f, h * 0.54f)
            lineTo(w * 0.42f, h * 0.20f)
            lineTo(w * 0.58f, h * 0.82f)
            lineTo(w * 0.70f, h * 0.54f)
            lineTo(w * 0.90f, h * 0.54f)
        }
        drawPath(path = path, color = color, style = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round))
    }
}

@Composable
private fun SyncArrowsIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        val stroke = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round)
        val inset = w * 0.18f
        drawArc(
            color = color,
            startAngle = -150f,
            sweepAngle = 140f,
            useCenter = false,
            topLeft = Offset(inset, inset),
            size = Size(w - inset * 2f, h - inset * 2f),
            style = stroke
        )
        drawArc(
            color = color,
            startAngle = 30f,
            sweepAngle = 140f,
            useCenter = false,
            topLeft = Offset(inset, inset),
            size = Size(w - inset * 2f, h - inset * 2f),
            style = stroke
        )
        val topHead = Path().apply {
            moveTo(w * 0.72f, h * 0.22f)
            lineTo(w * 0.84f, h * 0.36f)
            lineTo(w * 0.68f, h * 0.38f)
        }
        drawPath(path = topHead, color = color, style = stroke)
        val botHead = Path().apply {
            moveTo(w * 0.28f, h * 0.78f)
            lineTo(w * 0.16f, h * 0.64f)
            lineTo(w * 0.32f, h * 0.62f)
        }
        drawPath(path = botHead, color = color, style = stroke)
    }
}

@Composable
private fun DownloadArrowIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        drawLine(color = color, start = Offset(w * 0.50f, h * 0.16f), end = Offset(w * 0.50f, h * 0.72f), strokeWidth = sw, cap = StrokeCap.Round)
        val chev = Path().apply {
            moveTo(w * 0.26f, h * 0.50f)
            lineTo(w * 0.50f, h * 0.74f)
            lineTo(w * 0.74f, h * 0.50f)
        }
        drawPath(path = chev, color = color, style = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round))
        drawLine(color = color, start = Offset(w * 0.22f, h * 0.88f), end = Offset(w * 0.78f, h * 0.88f), strokeWidth = sw, cap = StrokeCap.Round)
    }
}

@Composable
private fun UploadArrowIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        drawLine(color = color, start = Offset(w * 0.50f, h * 0.76f), end = Offset(w * 0.50f, h * 0.20f), strokeWidth = sw, cap = StrokeCap.Round)
        val chev = Path().apply {
            moveTo(w * 0.26f, h * 0.42f)
            lineTo(w * 0.50f, h * 0.18f)
            lineTo(w * 0.74f, h * 0.42f)
        }
        drawPath(path = chev, color = color, style = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round))
        drawLine(color = color, start = Offset(w * 0.22f, h * 0.88f), end = Offset(w * 0.78f, h * 0.88f), strokeWidth = sw, cap = StrokeCap.Round)
    }
}

@Composable
private fun SwapArrowsIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        val stroke = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round)
        drawLine(color = color, start = Offset(w * 0.16f, h * 0.34f), end = Offset(w * 0.84f, h * 0.34f), strokeWidth = sw, cap = StrokeCap.Round)
        val rightHead = Path().apply {
            moveTo(w * 0.66f, h * 0.18f)
            lineTo(w * 0.84f, h * 0.34f)
            lineTo(w * 0.66f, h * 0.50f)
        }
        drawPath(path = rightHead, color = color, style = stroke)
        drawLine(color = color, start = Offset(w * 0.84f, h * 0.68f), end = Offset(w * 0.16f, h * 0.68f), strokeWidth = sw, cap = StrokeCap.Round)
        val leftHead = Path().apply {
            moveTo(w * 0.34f, h * 0.52f)
            lineTo(w * 0.16f, h * 0.68f)
            lineTo(w * 0.34f, h * 0.84f)
        }
        drawPath(path = leftHead, color = color, style = stroke)
    }
}

@Composable
private fun ClipboardImportIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        val stroke = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round)
        drawRoundRect(
            color = color,
            topLeft = Offset(w * 0.18f, h * 0.22f),
            size = Size(w * 0.64f, h * 0.68f),
            cornerRadius = CornerRadius(2.5.dp.toPx(), 2.5.dp.toPx()),
            style = stroke
        )
        drawRoundRect(
            color = color,
            topLeft = Offset(w * 0.36f, h * 0.10f),
            size = Size(w * 0.28f, h * 0.16f),
            cornerRadius = CornerRadius(1.5.dp.toPx(), 1.5.dp.toPx()),
            style = stroke
        )
        drawLine(color = color, start = Offset(w * 0.50f, h * 0.42f), end = Offset(w * 0.50f, h * 0.72f), strokeWidth = sw, cap = StrokeCap.Round)
        val chev = Path().apply {
            moveTo(w * 0.36f, h * 0.58f)
            lineTo(w * 0.50f, h * 0.73f)
            lineTo(w * 0.64f, h * 0.58f)
        }
        drawPath(path = chev, color = color, style = stroke)
    }
}

@Composable
private fun ShareCopyIcon(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val w = size.width
        val h = size.height
        val sw = 1.8.dp.toPx()
        val stroke = Stroke(width = sw, cap = StrokeCap.Round, join = StrokeJoin.Round)
        drawRoundRect(
            color = color,
            topLeft = Offset(w * 0.34f, h * 0.34f),
            size = Size(w * 0.52f, h * 0.54f),
            cornerRadius = CornerRadius(2.5.dp.toPx(), 2.5.dp.toPx()),
            style = stroke
        )
        val back = Path().apply {
            moveTo(w * 0.24f, h * 0.64f)
            lineTo(w * 0.18f, h * 0.64f)
            cubicTo(w * 0.14f, h * 0.64f, w * 0.14f, h * 0.60f, w * 0.14f, h * 0.56f)
            lineTo(w * 0.14f, h * 0.20f)
            cubicTo(w * 0.14f, h * 0.14f, w * 0.18f, h * 0.14f, w * 0.22f, h * 0.14f)
            lineTo(w * 0.58f, h * 0.14f)
            cubicTo(w * 0.64f, h * 0.14f, w * 0.64f, h * 0.18f, w * 0.64f, h * 0.24f)
        }
        drawPath(path = back, color = color, style = stroke)
    }
}
