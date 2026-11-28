package com.aether.vpn

import aethermobile.Aethermobile
import android.app.Activity
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.core.*
import androidx.compose.foundation.*
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay
import org.json.JSONObject

// Flagship Obsidian + Electric Cyan / Emerald palette
private val BgObsidian = Color(0xFF070B14)
private val CardSurface = Color(0xFF101726)
private val CardElevated = Color(0xFF162033)
private val BorderSubtle = Color(0xFF1F2E4A)
private val TextPrimary = Color(0xFFF1F5F9)
private val TextMuted = Color(0xFF8B9BB4)
private val AccentCyan = Color(0xFF38BDF8)
private val AccentBlue = Color(0xFF2563EB)
private val AccentEmerald = Color(0xFF10B981)
private val AccentAmber = Color(0xFFF59E0B)
private val AccentRose = Color(0xFFF43F5E)

private val AetherDarkScheme = darkColorScheme(
    primary = AccentCyan,
    secondary = AccentEmerald,
    background = BgObsidian,
    surface = CardSurface,
    surfaceVariant = CardElevated,
    onPrimary = Color(0xFF031018),
    onBackground = TextPrimary,
    onSurface = TextPrimary,
    outline = BorderSubtle
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
        setContent {
            MaterialTheme(colorScheme = AetherDarkScheme) {
                Surface(modifier = Modifier.fillMaxSize(), color = BgObsidian) {
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

private data class LiveTelemetry(
    val running: Boolean = false,
    val endpoint: String = "Disconnected",
    val carriers: Int = 0,
    val streams: Int = 0,
    val rttMs: Long = 0,
    val txBytes: Long = 0,
    val rxBytes: Long = 0,
    val failovers: Long = 0,
    val liveMbps: Double = 0.0,
    val connectedSec: Long = 0
)

private fun fmtBytes(b: Long): String = when {
    b >= 1_000_000_000L -> String.format("%.2f GB", b / 1e9)
    b >= 1_000_000L -> String.format("%.2f MB", b / 1e6)
    b >= 1_000L -> String.format("%.1f KB", b / 1e3)
    else -> "$b B"
}

private fun fmtDuration(sec: Long): String {
    val h = sec / 3600
    val m = (sec % 3600) / 60
    val s = sec % 60
    return if (h > 0) String.format("%02d:%02d:%02d", h, m, s) else String.format("%02d:%02d", m, s)
}

@Composable
private fun AetherApp(
    initial: AetherConfig,
    onSave: (AetherConfig) -> Unit,
    onConnect: (AetherConfig) -> Unit,
    onDisconnect: () -> Unit
) {
    val context = LocalContext.current
    var tab by remember { mutableIntStateOf(0) } // 0=Home, 1=Nodes, 2=Settings

    var psk by remember { mutableStateOf(initial.psk) }
    var primaryUrl by remember { mutableStateOf(initial.primaryUrl) }
    var fallbackUrl by remember { mutableStateOf(initial.fallbackUrl) }
    var cfCleanIp by remember { mutableStateOf(initial.cfCleanIp) }
    var transport by remember { mutableStateOf(initial.transport.ifBlank { "auto" }) }
    var carriers by remember { mutableIntStateOf(initial.carriers.coerceIn(1, 6)) }
    var bypassLan by remember { mutableStateOf(initial.bypassLan) }
    var bypassApps by remember { mutableStateOf(initial.bypassApps) }

    var telemetry by remember { mutableStateOf(LiveTelemetry()) }
    var errorText by remember { mutableStateOf<String?>(null) }

    fun currentConfig() = AetherConfig(
        psk = psk,
        primaryUrl = primaryUrl,
        fallbackUrl = fallbackUrl,
        cfCleanIp = cfCleanIp,
        transport = transport,
        carriers = carriers,
        bypassLan = bypassLan,
        bypassApps = bypassApps
    )

    // Poll Go core telemetry every 1s
    LaunchedEffect(Unit) {
        var prevTotal = 0L
        var startMs = 0L
        while (true) {
            val isRun = try { Aethermobile.isRunning() } catch (_: Throwable) { false }
            errorText = AetherVpnService.lastError
            if (isRun) {
                if (startMs == 0L) startMs = System.currentTimeMillis()
                try {
                    val j = JSONObject(Aethermobile.getStatsJSON())
                    val tx = j.optLong("tx_bytes")
                    val rx = j.optLong("rx_bytes")
                    val tot = tx + rx
                    val delta = if (prevTotal > 0 && tot >= prevTotal) tot - prevTotal else 0L
                    prevTotal = tot
                    telemetry = LiveTelemetry(
                        running = true,
                        endpoint = j.optString("endpoint", "connected"),
                        carriers = j.optInt("active_carriers"),
                        streams = j.optInt("active_streams"),
                        rttMs = j.optLong("rtt_ms"),
                        txBytes = tx,
                        rxBytes = rx,
                        failovers = j.optLong("failovers"),
                        liveMbps = (delta * 8.0) / 1_000_000.0,
                        connectedSec = (System.currentTimeMillis() - startMs) / 1000L
                    )
                } catch (_: Throwable) {
                    telemetry = telemetry.copy(running = true)
                }
            } else {
                startMs = 0L
                prevTotal = 0L
                telemetry = LiveTelemetry(running = false)
            }
            delay(1000)
        }
    }

    Scaffold(
        containerColor = BgObsidian,
        bottomBar = {
            NavigationBar(
                containerColor = CardSurface,
                tonalElevation = 0.dp
            ) {
                val items = listOf("Home", "Nodes", "Settings")
                items.forEachIndexed { idx, label ->
                    val selected = tab == idx
                    NavigationBarItem(
                        selected = selected,
                        onClick = { tab = idx },
                        icon = {
                            Box(
                                modifier = Modifier
                                    .size(if (selected) 10.dp else 7.dp)
                                    .clip(CircleShape)
                                    .background(if (selected) AccentCyan else TextMuted.copy(alpha = 0.4f))
                            )
                        },
                        label = {
                            Text(
                                label,
                                fontSize = 12.sp,
                                fontWeight = if (selected) FontWeight.Bold else FontWeight.Medium,
                                color = if (selected) AccentCyan else TextMuted
                            )
                        },
                        colors = NavigationBarItemDefaults.colors(indicatorColor = AccentCyan.copy(alpha = 0.14f))
                    )
                }
            }
        }
    ) { innerPad ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(innerPad)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 16.dp)
        ) {
            // Top Brand Header
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Column {
                    Text(
                        "AetherVPN",
                        fontSize = 24.sp,
                        fontWeight = FontWeight.ExtraBold,
                        letterSpacing = (-0.5).sp,
                        color = TextPrimary
                    )
                    Text(
                        "v3.1 · Striped Multi-Path uTLS · AdGuard DNS",
                        fontSize = 11.sp,
                        color = TextMuted
                    )
                }
                StatusBadge(running = telemetry.running)
            }

            Spacer(Modifier.height(18.dp))

            when (tab) {
                0 -> HomeTab(
                    telemetry = telemetry,
                    targetCarriers = carriers,
                    errorText = errorText,
                    onToggle = {
                        val cfg = currentConfig()
                        if (telemetry.running) onDisconnect() else onConnect(cfg)
                    }
                )
                1 -> NodesTab(
                    primaryUrl = primaryUrl,
                    onPrimaryChange = { primaryUrl = it },
                    fallbackUrl = fallbackUrl,
                    onFallbackChange = { fallbackUrl = it },
                    cfCleanIp = cfCleanIp,
                    onCleanIpChange = { cfCleanIp = it },
                    transport = transport,
                    onTransportChange = { transport = it },
                    carriers = carriers,
                    onCarriersChange = { carriers = it.coerceIn(1, 6) },
                    onImport = {
                        val cm = context.getSystemService(ClipboardManager::class.java)
                        val clip = cm?.primaryClip?.getItemAt(0)?.text?.toString() ?: ""
                        val imported = AetherConfig.fromShareUri(clip, currentConfig())
                        if (imported != null) {
                            psk = imported.psk
                            primaryUrl = imported.primaryUrl
                            fallbackUrl = imported.fallbackUrl
                            cfCleanIp = imported.cfCleanIp
                            transport = imported.transport
                            carriers = imported.carriers.coerceIn(1, 6)
                            onSave(imported)
                            Toast.makeText(context, "Imported aether:// config", Toast.LENGTH_SHORT).show()
                        } else {
                            Toast.makeText(context, "Copy an aether:// link first", Toast.LENGTH_SHORT).show()
                        }
                    },
                    onCopy = {
                        val uri = currentConfig().toShareUri()
                        val cm = context.getSystemService(ClipboardManager::class.java)
                        cm?.setPrimaryClip(ClipData.newPlainText("AetherVPN", uri))
                        Toast.makeText(context, "Copied aether:// share link", Toast.LENGTH_SHORT).show()
                    },
                    onSave = {
                        onSave(currentConfig())
                        Toast.makeText(context, "Node settings saved", Toast.LENGTH_SHORT).show()
                    }
                )
                2 -> SettingsTab(
                    psk = psk,
                    onPskChange = { psk = it },
                    bypassLan = bypassLan,
                    onBypassLanChange = { bypassLan = it },
                    bypassApps = bypassApps,
                    onBypassAppsChange = { bypassApps = it },
                    onSave = {
                        onSave(currentConfig())
                        Toast.makeText(context, "Settings saved", Toast.LENGTH_SHORT).show()
                    }
                )
            }
        }
    }
}

@Composable
private fun StatusBadge(running: Boolean) {
    val bg = if (running) AccentEmerald.copy(alpha = 0.16f) else CardElevated
    val border = if (running) AccentEmerald.copy(alpha = 0.55f) else BorderSubtle
    val dot = if (running) AccentEmerald else TextMuted
    val label = if (running) "PROTECTED" else "READY"
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(999.dp))
            .background(bg)
            .border(1.dp, border, RoundedCornerShape(999.dp))
            .padding(horizontal = 12.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(6.dp)
    ) {
        Box(modifier = Modifier.size(7.dp).clip(CircleShape).background(dot))
        Text(label, fontSize = 11.sp, fontWeight = FontWeight.Bold, color = if (running) AccentEmerald else TextMuted)
    }
}

@Composable
private fun HomeTab(
    telemetry: LiveTelemetry,
    targetCarriers: Int,
    errorText: String?,
    onToggle: () -> Unit
) {
    Column(
        modifier = Modifier.fillMaxWidth(),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(18.dp)
    ) {
        // Animated Hero Power Orb
        PowerOrb(running = telemetry.running, onClick = onToggle)

        // Connection status & timer
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(
                if (telemetry.running) "Encrypted Tunnel Active" else "Tap to Connect",
                fontSize = 17.sp,
                fontWeight = FontWeight.Bold,
                color = if (telemetry.running) AccentEmerald else TextPrimary
            )
            Spacer(Modifier.height(4.dp))
            Text(
                if (telemetry.running) "${telemetry.endpoint}  ·  ${fmtDuration(telemetry.connectedSec)}"
                else "X25519 + XChaCha20-Poly1305 · AdGuard DNS",
                fontSize = 12.sp,
                color = TextMuted
            )
        }

        if (!errorText.isNullOrBlank()) {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(14.dp))
                    .background(AccentRose.copy(alpha = 0.12f))
                    .border(1.dp, AccentRose.copy(alpha = 0.45f), RoundedCornerShape(14.dp))
                    .padding(12.dp)
            ) {
                Text("Error: $errorText", color = Color(0xFFFDA4AF), fontSize = 12.sp)
            }
        }

        // 2x2 Live Telemetry Grid
        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            StatCard(
                modifier = Modifier.weight(1f),
                title = "LIVE SPEED",
                value = String.format("%.1f Mbps", telemetry.liveMbps),
                sub = "RTT: ${if (telemetry.rttMs > 0) "${telemetry.rttMs} ms" else "--"}",
                accent = if (telemetry.running) AccentCyan else TextMuted
            )
            StatCard(
                modifier = Modifier.weight(1f),
                title = "CARRIERS",
                value = "${telemetry.carriers} / $targetCarriers",
                sub = "${telemetry.streams} streams · ${telemetry.failovers} failovers",
                accent = if (telemetry.carriers > 0) AccentEmerald else TextMuted
            )
        }
        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            StatCard(
                modifier = Modifier.weight(1f),
                title = "DOWNLOADED",
                value = fmtBytes(telemetry.rxBytes),
                sub = "Encrypted RX",
                accent = AccentCyan
            )
            StatCard(
                modifier = Modifier.weight(1f),
                title = "UPLOADED",
                value = fmtBytes(telemetry.txBytes),
                sub = "Encrypted TX",
                accent = AccentBlue
            )
        }
    }
}

@Composable
private fun PowerOrb(running: Boolean, onClick: () -> Unit) {
    val infinite = rememberInfiniteTransition(label = "orb")
    val pulse by infinite.animateFloat(
        initialValue = 0f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(2200, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Restart
        ),
        label = "pulse"
    )

    val ringColor = if (running) AccentEmerald else AccentCyan
    val gradColors = if (running) {
        listOf(Color(0xFF059669), Color(0xFF047857))
    } else {
        listOf(Color(0xFF1E3A8A), Color(0xFF0F172A))
    }

    Box(
        modifier = Modifier.size(200.dp),
        contentAlignment = Alignment.Center
    ) {
        // Outer animated radar pulse rings
        Canvas(modifier = Modifier.fillMaxSize()) {
            val c = Offset(size.width / 2f, size.height / 2f)
            val baseR = size.minDimension * 0.36f
            val maxR = size.minDimension * 0.49f
            if (running) {
                val r1 = baseR + (maxR - baseR) * pulse
                drawCircle(color = ringColor.copy(alpha = (1f - pulse) * 0.35f), radius = r1, center = c, style = Stroke(width = 3.dp.toPx()))
                val p2 = (pulse + 0.5f) % 1f
                val r2 = baseR + (maxR - baseR) * p2
                drawCircle(color = ringColor.copy(alpha = (1f - p2) * 0.25f), radius = r2, center = c, style = Stroke(width = 2.dp.toPx()))
            } else {
                drawCircle(color = BorderSubtle, radius = baseR + 12.dp.toPx(), center = c, style = Stroke(width = 1.5.dp.toPx()))
            }
        }

        // Tactile Power Sphere
        Box(
            modifier = Modifier
                .size(136.dp)
                .clip(CircleShape)
                .background(Brush.verticalGradient(gradColors))
                .border(2.dp, ringColor.copy(alpha = if (running) 0.9f else 0.5f), CircleShape)
                .clickable(onClick = onClick),
            contentAlignment = Alignment.Center
        ) {
            Canvas(modifier = Modifier.size(48.dp)) {
                val stroke = Stroke(width = 4.5.dp.toPx(), cap = StrokeCap.Round)
                val arcInset = 6.dp.toPx()
                drawArc(
                    color = Color.White,
                    startAngle = -55f,
                    sweepAngle = 290f,
                    useCenter = false,
                    topLeft = Offset(arcInset, arcInset),
                    size = Size(size.width - arcInset * 2, size.height - arcInset * 2),
                    style = stroke
                )
                drawLine(
                    color = Color.White,
                    start = Offset(size.width / 2f, 2.dp.toPx()),
                    end = Offset(size.width / 2f, size.height * 0.48f),
                    strokeWidth = 4.5.dp.toPx(),
                    cap = StrokeCap.Round
                )
            }
        }
    }
}

@Composable
private fun StatCard(
    modifier: Modifier,
    title: String,
    value: String,
    sub: String,
    accent: Color
) {
    Column(
        modifier = modifier
            .clip(RoundedCornerShape(16.dp))
            .background(CardSurface)
            .border(1.dp, BorderSubtle, RoundedCornerShape(16.dp))
            .padding(14.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp)
    ) {
        Text(title, fontSize = 10.sp, fontWeight = FontWeight.Bold, color = TextMuted, letterSpacing = 0.8.sp)
        Text(value, fontSize = 19.sp, fontWeight = FontWeight.ExtraBold, color = accent, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Text(sub, fontSize = 11.sp, color = TextMuted, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

@Composable
private fun NodesTab(
    primaryUrl: String,
    onPrimaryChange: (String) -> Unit,
    fallbackUrl: String,
    onFallbackChange: (String) -> Unit,
    cfCleanIp: String,
    onCleanIpChange: (String) -> Unit,
    transport: String,
    onTransportChange: (String) -> Unit,
    carriers: Int,
    onCarriersChange: (Int) -> Unit,
    onImport: () -> Unit,
    onCopy: () -> Unit,
    onSave: () -> Unit
) {
    Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
        // 1-Tap Import / Copy Bar
        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Button(
                onClick = onImport,
                modifier = Modifier.weight(1f).height(44.dp),
                shape = RoundedCornerShape(12.dp),
                colors = ButtonDefaults.buttonColors(containerColor = AccentBlue)
            ) {
                Text("Import Link", fontWeight = FontWeight.SemiBold, color = Color.White)
            }
            OutlinedButton(
                onClick = onCopy,
                modifier = Modifier.weight(1f).height(44.dp),
                shape = RoundedCornerShape(12.dp),
                border = BorderStroke(1.dp, BorderSubtle)
            ) {
                Text("Copy Share Link", color = AccentCyan)
            }
        }

        SectionCard("ENDPOINTS") {
            OutlinedTextField(
                value = primaryUrl,
                onValueChange = onPrimaryChange,
                label = { Text("Primary Endpoint (Railway / CF Worker)") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
                shape = RoundedCornerShape(12.dp)
            )
            OutlinedTextField(
                value = fallbackUrl,
                onValueChange = onFallbackChange,
                label = { Text("Fallback Endpoint (Render / Backup)") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
                shape = RoundedCornerShape(12.dp)
            )
            OutlinedTextField(
                value = cfCleanIp,
                onValueChange = onCleanIpChange,
                label = { Text("Optional Cloudflare Clean IP") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
                shape = RoundedCornerShape(12.dp)
            )
        }

        SectionCard("TRANSPORT & CARRIER POOL") {
            Text("Transport Mode", fontSize = 12.sp, color = TextMuted)
            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                listOf("auto" to "AUTO", "ws" to "WebSocket", "h2" to "HTTP/2").forEach { (key, label) ->
                    val sel = transport.equals(key, ignoreCase = true)
                    Box(
                        modifier = Modifier
                            .weight(1f)
                            .clip(RoundedCornerShape(10.dp))
                            .background(if (sel) AccentCyan.copy(alpha = 0.18f) else BgObsidian)
                            .border(1.dp, if (sel) AccentCyan else BorderSubtle, RoundedCornerShape(10.dp))
                            .clickable { onTransportChange(key) }
                            .padding(vertical = 10.dp),
                        contentAlignment = Alignment.Center
                    ) {
                        Text(label, fontSize = 13.sp, fontWeight = FontWeight.SemiBold, color = if (sel) AccentCyan else TextMuted)
                    }
                }
            }

            Spacer(Modifier.height(4.dp))
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Column {
                    Text("Parallel Carriers", fontSize = 14.sp, fontWeight = FontWeight.SemiBold, color = TextPrimary)
                    Text("Recommended: 4 for max striped speed", fontSize = 11.sp, color = TextMuted)
                }
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    OutlinedButton(
                        onClick = { onCarriersChange(carriers - 1) },
                        contentPadding = PaddingValues(0.dp),
                        modifier = Modifier.size(36.dp),
                        shape = CircleShape
                    ) { Text("-", fontSize = 18.sp) }
                    Text("$carriers", fontSize = 16.sp, fontWeight = FontWeight.Bold, color = AccentCyan)
                    OutlinedButton(
                        onClick = { onCarriersChange(carriers + 1) },
                        contentPadding = PaddingValues(0.dp),
                        modifier = Modifier.size(36.dp),
                        shape = CircleShape
                    ) { Text("+", fontSize = 18.sp) }
                }
            }
        }

        Button(
            onClick = onSave,
            modifier = Modifier.fillMaxWidth().height(48.dp),
            shape = RoundedCornerShape(12.dp),
            colors = ButtonDefaults.buttonColors(containerColor = AccentEmerald)
        ) {
            Text("Save Node Config", fontWeight = FontWeight.Bold, color = Color.White)
        }
    }
}

@Composable
private fun SettingsTab(
    psk: String,
    onPskChange: (String) -> Unit,
    bypassLan: Boolean,
    onBypassLanChange: (Boolean) -> Unit,
    bypassApps: String,
    onBypassAppsChange: (String) -> Unit,
    onSave: () -> Unit
) {
    var showKey by remember { mutableStateOf(false) }

    Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
        SectionCard("AUTHENTICATION") {
            OutlinedTextField(
                value = psk,
                onValueChange = onPskChange,
                label = { Text("Secret Key (AETHER_PSK)") },
                singleLine = true,
                visualTransformation = if (showKey) VisualTransformation.None else PasswordVisualTransformation(),
                trailingIcon = {
                    TextButton(onClick = { showKey = !showKey }) {
                        Text(if (showKey) "Hide" else "Show", fontSize = 12.sp, color = AccentCyan)
                    }
                },
                modifier = Modifier.fillMaxWidth(),
                shape = RoundedCornerShape(12.dp)
            )
        }

        SectionCard("SPLIT TUNNELING") {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text("Bypass Local LAN", fontSize = 14.sp, fontWeight = FontWeight.SemiBold, color = TextPrimary)
                    Text("Keep local Wi-Fi devices direct (saves server quota)", fontSize = 11.sp, color = TextMuted)
                }
                Switch(checked = bypassLan, onCheckedChange = onBypassLanChange)
            }
            OutlinedTextField(
                value = bypassApps,
                onValueChange = onBypassAppsChange,
                label = { Text("Bypass Apps (comma-separated packages)") },
                placeholder = { Text("com.example.bank, com.local.app") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
                shape = RoundedCornerShape(12.dp)
            )
        }

        SectionCard("ENGINE STACK") {
            Text(
                "Protocol: Aether v2 (0-RTT · Per-Stream Pinning)\n" +
                    "Crypto: X25519 + HKDF-SHA256 + XChaCha20-Poly1305\n" +
                    "Stealth: uTLS Chrome Fingerprint + Anti-DPI Padding\n" +
                    "DNS: Server-Side Hedged AdGuard + 60s Local Cache\n" +
                    "UDP: Full-Cone + Google/YouTube QUIC (MTU 1500)",
                fontSize = 12.sp,
                fontFamily = FontFamily.Monospace,
                color = TextMuted,
                lineHeight = 19.sp
            )
        }

        Button(
            onClick = onSave,
            modifier = Modifier.fillMaxWidth().height(48.dp),
            shape = RoundedCornerShape(12.dp),
            colors = ButtonDefaults.buttonColors(containerColor = AccentEmerald)
        ) {
            Text("Save Settings", fontWeight = FontWeight.Bold, color = Color.White)
        }
    }
}

@Composable
private fun SectionCard(title: String, content: @Composable ColumnScope.() -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(16.dp))
            .background(CardSurface)
            .border(1.dp, BorderSubtle, RoundedCornerShape(16.dp))
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Text(title, fontSize = 11.sp, fontWeight = FontWeight.Bold, color = AccentCyan, letterSpacing = 1.sp)
        content()
    }
}
