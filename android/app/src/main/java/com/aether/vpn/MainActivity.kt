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
import androidx.activity.SystemBarStyle
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
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
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
import kotlinx.coroutines.delay
import org.json.JSONObject
import java.net.URI
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
        if (!parsed.isNullOrBlank()) parsed else trimmed.removePrefix("wss://").removePrefix("https://").substringBefore("/")
    } catch (_: Exception) {
        trimmed.removePrefix("wss://").removePrefix("https://").substringBefore("/")
    }
}

@Composable
private fun AetherApp(
    initial: AetherConfig,
    onSave: (AetherConfig) -> Unit,
    onConnect: (AetherConfig) -> Unit,
    onDisconnect: () -> Unit
) {
    val context = LocalContext.current
    var tab by remember { mutableIntStateOf(0) } // 0=Tunnel, 1=Routes, 2=Settings

    var psk by remember { mutableStateOf(initial.psk) }
    var primaryUrl by remember { mutableStateOf(initial.primaryUrl) }
    var fallbackUrl by remember { mutableStateOf(initial.fallbackUrl) }
    var cfCleanIp by remember { mutableStateOf(initial.cfCleanIp) }
    var transport by remember { mutableStateOf(initial.transport.ifBlank { "auto" }) }
    var carriers by remember { mutableIntStateOf(initial.carriers.coerceIn(1, 6)) }
    var bypassLan by remember { mutableStateOf(initial.bypassLan) }
    var bypassApps by remember { mutableStateOf(initial.bypassApps) }

    var telemetry by remember { mutableStateOf(LiveTelemetry()) }
    var speedHistory by remember { mutableStateOf(List(30) { 0f }) }
    var peakMbps by remember { mutableDoubleStateOf(0.0) }
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

    // Poll Go core telemetry every 1s & maintain rolling 30-sample throughput sparkline
    LaunchedEffect(Unit) {
        var prevTotal = 0L
        var startMs = 0L
        while (true) {
            val isRun = try {
                Aethermobile.isRunning()
            } catch (_: Throwable) {
                false
            }
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
                    val mbps = (delta * 8.0) / 1_000_000.0
                    if (mbps > peakMbps) peakMbps = mbps
                    speedHistory = speedHistory.drop(1) + mbps.toFloat()
                    telemetry = LiveTelemetry(
                        running = true,
                        endpoint = j.optString("endpoint", "connected"),
                        carriers = j.optInt("active_carriers"),
                        streams = j.optInt("active_streams"),
                        rttMs = j.optLong("rtt_ms"),
                        txBytes = tx,
                        rxBytes = rx,
                        failovers = j.optLong("failovers"),
                        liveMbps = mbps,
                        connectedSec = (System.currentTimeMillis() - startMs) / 1000L
                    )
                } catch (_: Throwable) {
                    telemetry = telemetry.copy(running = true)
                }
            } else {
                startMs = 0L
                prevTotal = 0L
                if (peakMbps != 0.0) peakMbps = 0.0
                if (speedHistory.any { it > 0f }) speedHistory = List(30) { 0f }
                telemetry = LiveTelemetry(running = false)
            }
            delay(1000)
        }
    }

    val ambientGlowColor by animateColorAsState(
        targetValue = if (telemetry.running) {
            SignalEmerald.copy(alpha = if (tab == 0) 0.14f else 0.08f)
        } else {
            TextDim.copy(alpha = if (tab == 0) 0.08f else 0.04f)
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
            // Ambient top radial glow behind the hero instrument
            Canvas(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(380.dp)
                    .align(Alignment.TopCenter)
            ) {
                drawRect(
                    brush = Brush.radialGradient(
                        colors = listOf(ambientGlowColor, Color.Transparent),
                        center = Offset(size.width * 0.5f, size.height * 0.42f),
                        radius = size.width * 0.78f
                    )
                )
            }

            // Main vertical scaffold: pinned Top Bar + Animated tab body
            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .statusBarsPadding()
            ) {
                TopBrandBar(running = telemetry.running)

                AnimatedContent(
                    targetState = tab,
                    transitionSpec = {
                        fadeIn(animationSpec = tween(180)) togetherWith fadeOut(animationSpec = tween(140))
                    },
                    modifier = Modifier.weight(1f),
                    label = "tabContent"
                ) { activeTab ->
                    Column(
                        modifier = Modifier
                            .fillMaxSize()
                            .verticalScroll(rememberScrollState())
                            .padding(horizontal = 20.dp)
                    ) {
                        Spacer(Modifier.height(6.dp))
                        when (activeTab) {
                            0 -> TunnelTab(
                                telemetry = telemetry,
                                speedHistory = speedHistory,
                                peakMbps = peakMbps,
                                targetCarriers = carriers,
                                errorText = errorText,
                                onToggle = {
                                    val cfg = currentConfig()
                                    if (telemetry.running) onDisconnect() else onConnect(cfg)
                                }
                            )
                            1 -> RoutesTab(
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
                                onSwapRoutes = {
                                    val oldPrimary = primaryUrl
                                    primaryUrl = fallbackUrl
                                    fallbackUrl = oldPrimary
                                    onSave(currentConfig())
                                    Toast.makeText(context, "Swapped Primary & Fallback routes", Toast.LENGTH_SHORT).show()
                                },
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
                                    Toast.makeText(context, "Routes saved", Toast.LENGTH_SHORT).show()
                                }
                            )
                            2 -> SettingsTab(
                                psk = psk,
                                onPskChange = { psk = it },
                                onCopyPsk = {
                                    if (psk.isBlank()) {
                                        Toast.makeText(context, "Secret key is empty", Toast.LENGTH_SHORT).show()
                                    } else {
                                        val cm = context.getSystemService(ClipboardManager::class.java)
                                        cm?.setPrimaryClip(ClipData.newPlainText("AETHER_PSK", psk.trim()))
                                        Toast.makeText(context, "Copied AETHER_PSK", Toast.LENGTH_SHORT).show()
                                    }
                                },
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
                        // Ensure floating pill dock never obscures bottom cards or save buttons
                        Spacer(Modifier.height(104.dp))
                        Spacer(Modifier.navigationBarsPadding())
                    }
                }
            }

            // Soft bottom scrim so scrolling content dissolves cleanly under the floating dock
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(108.dp)
                    .align(Alignment.BottomCenter)
                    .background(
                        Brush.verticalGradient(
                            colors = listOf(Color.Transparent, BgCarbon.copy(alpha = 0.88f), BgCarbon)
                        )
                    )
            )

            // Floating Pill Bottom Navigation Dock
            FloatingNavDock(
                selectedTab = tab,
                onSelect = { tab = it },
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .navigationBarsPadding()
                    .padding(horizontal = 20.dp)
                    .padding(bottom = 12.dp)
            )
        }
    }
}

@Composable
private fun TopBrandBar(running: Boolean) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 20.dp, vertical = 12.dp),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            BrandMarkIcon(
                active = running,
                modifier = Modifier.size(28.dp)
            )
            Spacer(Modifier.width(10.dp))
            Text(
                text = "Aether",
                fontSize = 20.sp,
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
                    text = "v3.2",
                    fontSize = 10.sp,
                    fontFamily = FontFamily.Monospace,
                    fontWeight = FontWeight.Medium,
                    color = TextSecondary
                )
            }
        }
        StatusBadge(running = running)
    }
}

@Composable
private fun StatusBadge(running: Boolean) {
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
        targetValue = if (running) SignalEmerald.copy(alpha = 0.14f) else SurfaceCard,
        animationSpec = tween(350),
        label = "badgeBg"
    )
    val border by animateColorAsState(
        targetValue = if (running) SignalEmerald.copy(alpha = 0.45f) else BorderHairline,
        animationSpec = tween(350),
        label = "badgeBorder"
    )
    val labelColor by animateColorAsState(
        targetValue = if (running) SignalEmeraldBright else TextSecondary,
        animationSpec = tween(350),
        label = "badgeText"
    )

    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(999.dp))
            .background(bg)
            .border(1.dp, border, RoundedCornerShape(999.dp))
            .padding(horizontal = 11.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(7.dp)
    ) {
        Box(
            modifier = Modifier
                .size(7.dp)
                .clip(CircleShape)
                .background(if (running) SignalEmeraldBright.copy(alpha = dotAlpha) else TextDim)
        )
        Text(
            text = if (running) "PROTECTED" else "STANDBY",
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
    val tabs = listOf("Tunnel", "Routes", "Settings")
    Row(
        modifier = modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(28.dp))
            .background(SurfaceCard)
            .border(1.dp, BorderHairline, RoundedCornerShape(28.dp))
            .padding(6.dp),
        horizontalArrangement = Arrangement.spacedBy(4.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        tabs.forEachIndexed { idx, label ->
            val selected = selectedTab == idx
            val pillBg by animateColorAsState(
                targetValue = if (selected) SignalEmerald.copy(alpha = 0.14f) else Color.Transparent,
                animationSpec = tween(220),
                label = "pillBg$idx"
            )
            val pillBorder by animateColorAsState(
                targetValue = if (selected) SignalEmerald.copy(alpha = 0.28f) else Color.Transparent,
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
                    1 -> RoutesTabIcon(color = contentColor, modifier = Modifier.size(18.dp))
                    else -> SettingsTabIcon(color = contentColor, modifier = Modifier.size(18.dp))
                }
                Spacer(Modifier.width(7.dp))
                Text(
                    text = label,
                    fontSize = 12.sp,
                    fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Medium,
                    color = contentColor,
                    maxLines = 1
                )
            }
        }
    }
}

// ---------------------------------------------------------------------------
// TAB 0: TUNNEL (Hero Instrument Screen)
// ---------------------------------------------------------------------------

@Composable
private fun TunnelTab(
    telemetry: LiveTelemetry,
    speedHistory: List<Float>,
    peakMbps: Double,
    targetCarriers: Int,
    errorText: String?,
    onToggle: () -> Unit
) {
    val titleColor by animateColorAsState(
        targetValue = if (telemetry.running) SignalEmeraldBright else TextPrimary,
        animationSpec = tween(350),
        label = "statusTitleColor"
    )

    Column(
        modifier = Modifier.fillMaxWidth(),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        Spacer(Modifier.height(2.dp))

        // Machined 220dp Hero Power Dial
        MachinedPowerDial(
            running = telemetry.running,
            onToggle = onToggle
        )

        // Status Headline + Mono Telemetry Chip
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            Text(
                text = if (telemetry.running) "Tunnel Protected" else "Ready to Connect",
                fontSize = 20.sp,
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
                        .background(if (telemetry.running) SignalEmerald else TextDim)
                )
                Text(
                    text = if (telemetry.running) {
                        "${telemetry.endpoint} · ${fmtDuration(telemetry.connectedSec)}"
                    } else {
                        "X25519 · XChaCha20-Poly1305 · uTLS"
                    },
                    fontSize = 11.sp,
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
                    .clip(RoundedCornerShape(14.dp))
                    .background(SignalRose.copy(alpha = 0.10f))
                    .border(1.dp, SignalRose.copy(alpha = 0.42f), RoundedCornerShape(14.dp))
                    .padding(horizontal = 14.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(10.dp)
            ) {
                Box(
                    modifier = Modifier
                        .size(8.dp)
                        .clip(CircleShape)
                        .background(SignalRose)
                )
                Text(
                    text = errorText,
                    color = Color(0xFFFDA4AF),
                    fontSize = 12.sp,
                    lineHeight = 17.sp
                )
            }
        }

        // Live 30-Second Throughput Sparkline Card
        ThroughputSparklineCard(
            telemetry = telemetry,
            samples = speedHistory,
            peakMbps = peakMbps
        )

        // Live Carrier Matrix Bar
        CarrierMatrixBar(
            activeCarriers = telemetry.carriers,
            targetCarriers = targetCarriers,
            streams = telemetry.streams,
            failovers = telemetry.failovers,
            running = telemetry.running
        )
    }
}

@Composable
private fun MachinedPowerDial(
    running: Boolean,
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
        targetValue = if (running) SignalEmeraldBright else TextPrimary,
        animationSpec = tween(350),
        label = "glyphColor"
    )
    val rimTopColor by animateColorAsState(
        targetValue = if (running) SignalEmeraldBright.copy(alpha = 0.65f) else Color(0xFF3F3F46),
        animationSpec = tween(350),
        label = "rimTopColor"
    )
    val rimBottomColor by animateColorAsState(
        targetValue = if (running) SignalEmerald.copy(alpha = 0.18f) else BorderHairline,
        animationSpec = tween(350),
        label = "rimBottomColor"
    )
    val coreTop by animateColorAsState(
        targetValue = if (running) Color(0xFF10241D) else Color(0xFF1C1C21),
        animationSpec = tween(350),
        label = "coreTop"
    )
    val coreBottom by animateColorAsState(
        targetValue = if (running) Color(0xFF09120F) else Color(0xFF0C0C0F),
        animationSpec = tween(350),
        label = "coreBottom"
    )

    Box(
        modifier = Modifier.size(220.dp),
        contentAlignment = Alignment.Center
    ) {
        // Outer precision chronometer ticks, rotating sweep arc & radar wave
        Canvas(modifier = Modifier.fillMaxSize()) {
            val center = Offset(size.width / 2f, size.height / 2f)
            val tickOuterR = size.minDimension * 0.475f
            val tickInnerMinor = size.minDimension * 0.438f
            val tickInnerMajor = size.minDimension * 0.422f
            val sweepRadius = size.minDimension * 0.392f

            // Subtle expanding radar wave when connected
            if (running) {
                val baseR = size.minDimension * 0.34f
                val maxR = size.minDimension * 0.495f
                val waveR = baseR + (maxR - baseR) * radarProgress
                drawCircle(
                    color = SignalEmerald.copy(alpha = (1f - radarProgress) * 0.26f),
                    radius = waveR,
                    center = center,
                    style = Stroke(width = 1.5.dp.toPx())
                )
            }

            // 36 precision radial ticks around the circumference
            val totalTicks = 36
            for (i in 0 until totalTicks) {
                val angleDeg = i * (360.0 / totalTicks) - 90.0
                val rad = angleDeg * (PI / 180.0)
                val cosA = cos(rad).toFloat()
                val sinA = sin(rad).toFloat()
                val isMajor = i % 9 == 0
                val innerR = if (isMajor) tickInnerMajor else tickInnerMinor

                val tickColor = if (running) {
                    if (isMajor) SignalEmeraldBright.copy(alpha = 0.75f) else SignalEmerald.copy(alpha = 0.30f)
                } else {
                    if (isMajor) TextDim.copy(alpha = 0.65f) else BorderHairline
                }

                drawLine(
                    color = tickColor,
                    start = Offset(center.x + innerR * cosA, center.y + innerR * sinA),
                    end = Offset(center.x + tickOuterR * cosA, center.y + tickOuterR * sinA),
                    strokeWidth = if (isMajor) 2.dp.toPx() else 1.2.dp.toPx(),
                    cap = StrokeCap.Round
                )
            }

            // Recessed track ring
            drawCircle(
                color = if (running) SignalEmerald.copy(alpha = 0.14f) else BorderHairline.copy(alpha = 0.7f),
                radius = sweepRadius,
                center = center,
                style = Stroke(width = 2.dp.toPx())
            )

            // Rotating emerald sweep arc when connected
            if (running) {
                val arcTopLeft = Offset(center.x - sweepRadius, center.y - sweepRadius)
                val arcSize = Size(sweepRadius * 2f, sweepRadius * 2f)
                drawArc(
                    color = SignalEmerald.copy(alpha = 0.35f),
                    startAngle = sweepAngle - 95f,
                    sweepAngle = 95f,
                    useCenter = false,
                    topLeft = arcTopLeft,
                    size = arcSize,
                    style = Stroke(width = 3.dp.toPx(), cap = StrokeCap.Round)
                )
                drawArc(
                    color = SignalEmeraldBright,
                    startAngle = sweepAngle - 28f,
                    sweepAngle = 28f,
                    useCenter = false,
                    topLeft = arcTopLeft,
                    size = arcSize,
                    style = Stroke(width = 3.dp.toPx(), cap = StrokeCap.Round)
                )
            }
        }

        // Recessed machined well
        Box(
            modifier = Modifier
                .size(156.dp)
                .clip(CircleShape)
                .background(SurfaceRecessed)
                .border(1.dp, BorderHairline, CircleShape),
            contentAlignment = Alignment.Center
        ) {
            // Center 144dp tactile dark machined power button
            Box(
                modifier = Modifier
                    .size(144.dp)
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
                    Canvas(modifier = Modifier.size(40.dp)) {
                        val strokeW = 3.2.dp.toPx()
                        val inset = 5.dp.toPx()
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
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = if (running) "ACTIVE" else "ENGAGE",
                        fontSize = 9.sp,
                        fontFamily = FontFamily.Monospace,
                        fontWeight = FontWeight.Bold,
                        letterSpacing = 1.6.sp,
                        color = if (running) SignalEmeraldBright else TextDim
                    )
                }
            }
        }
    }
}

@Composable
private fun ThroughputSparklineCard(
    telemetry: LiveTelemetry,
    samples: List<Float>,
    peakMbps: Double
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(18.dp))
            .background(SurfaceCard)
            .border(1.dp, BorderHairline, RoundedCornerShape(18.dp))
    ) {
        // Header Row: Live Mbps + RTT & Peak mono pills
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(start = 16.dp, end = 16.dp, top = 14.dp, bottom = 8.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.Top
        ) {
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(
                    text = "LIVE THROUGHPUT",
                    fontSize = 10.sp,
                    fontWeight = FontWeight.SemiBold,
                    letterSpacing = 1.sp,
                    color = TextDim
                )
                Row(verticalAlignment = Alignment.Bottom) {
                    Text(
                        text = String.format(Locale.US, "%.1f", telemetry.liveMbps),
                        fontSize = 26.sp,
                        fontWeight = FontWeight.Bold,
                        fontFamily = FontFamily.Monospace,
                        letterSpacing = (-0.8).sp,
                        color = if (telemetry.running) TextPrimary else TextSecondary
                    )
                    Spacer(Modifier.width(5.dp))
                    Text(
                        text = "Mbps",
                        fontSize = 12.sp,
                        fontFamily = FontFamily.Monospace,
                        color = TextDim,
                        modifier = Modifier.padding(bottom = 4.dp)
                    )
                }
            }

            Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                MonoTelemetryPill(
                    label = "RTT",
                    value = if (telemetry.running && telemetry.rttMs > 0) "${telemetry.rttMs} ms" else "--"
                )
                MonoTelemetryPill(
                    label = "Peak",
                    value = if (telemetry.running) String.format(Locale.US, "%.1f", peakMbps) else "--"
                )
            }
        }

        // 72dp Custom Canvas Area Sparkline
        Canvas(
            modifier = Modifier
                .fillMaxWidth()
                .height(72.dp)
                .padding(horizontal = 16.dp, vertical = 6.dp)
        ) {
            val w = size.width
            val h = size.height
            val baseY = h - 2.dp.toPx()
            val topPad = 4.dp.toPx()
            val drawH = (baseY - topPad).coerceAtLeast(1f)

            val dash = PathEffect.dashPathEffect(floatArrayOf(6f, 6f), 0f)
            // Midline guide
            drawLine(
                color = BorderHairline.copy(alpha = 0.5f),
                start = Offset(0f, topPad + drawH * 0.5f),
                end = Offset(w, topPad + drawH * 0.5f),
                strokeWidth = 1.dp.toPx(),
                pathEffect = dash
            )
            // Baseline rule
            drawLine(
                color = BorderHairline,
                start = Offset(0f, baseY),
                end = Offset(w, baseY),
                strokeWidth = 1.dp.toPx(),
                pathEffect = dash
            )

            val count = samples.size
            if (count >= 2) {
                val maxVal = max(1f, (samples.maxOrNull() ?: 0f) * 1.15f)
                val stepX = w / (count - 1).toFloat()

                val points = List(count) { idx ->
                    val ratio = (samples[idx] / maxVal).coerceIn(0f, 1f)
                    Offset(
                        x = idx * stepX,
                        y = baseY - (ratio * drawH)
                    )
                }

                val linePath = Path().apply {
                    moveTo(points.first().x, points.first().y)
                    for (i in 0 until points.lastIndex) {
                        val p0 = points[i]
                        val p1 = points[i + 1]
                        val midX = (p0.x + p1.x) / 2f
                        cubicTo(midX, p0.y, midX, p1.y, p1.x, p1.y)
                    }
                }

                val fillPath = Path().apply {
                    addPath(linePath)
                    lineTo(points.last().x, baseY)
                    lineTo(points.first().x, baseY)
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

                drawPath(
                    path = linePath,
                    color = if (telemetry.running) SignalEmeraldBright else TextDim.copy(alpha = 0.45f),
                    style = Stroke(
                        width = 2.dp.toPx(),
                        cap = StrokeCap.Round,
                        join = StrokeJoin.Round
                    )
                )

                // Live terminal beacon at newest sample
                if (telemetry.running) {
                    val last = points.last()
                    drawCircle(
                        color = SignalEmerald.copy(alpha = 0.30f),
                        radius = 6.dp.toPx(),
                        center = last
                    )
                    drawCircle(
                        color = SignalEmeraldBright,
                        radius = 2.8.dp.toPx(),
                        center = last
                    )
                }
            }
        }

        Spacer(Modifier.height(4.dp))
        HorizontalDivider(color = BorderHairline, thickness = 1.dp)

        // Footer Split Row: Download | Upload
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Row(
                modifier = Modifier.weight(1f),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(10.dp)
            ) {
                Box(
                    modifier = Modifier
                        .size(30.dp)
                        .clip(RoundedCornerShape(8.dp))
                        .background(SignalEmerald.copy(alpha = 0.12f)),
                    contentAlignment = Alignment.Center
                ) {
                    DownloadArrowIcon(color = SignalEmeraldBright, modifier = Modifier.size(15.dp))
                }
                Column {
                    Text(
                        text = "DOWNLOAD",
                        fontSize = 9.sp,
                        fontWeight = FontWeight.SemiBold,
                        letterSpacing = 0.8.sp,
                        color = TextDim
                    )
                    Text(
                        text = fmtBytes(telemetry.rxBytes),
                        fontSize = 14.sp,
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
                    .height(28.dp)
                    .background(BorderHairline)
            )

            Spacer(Modifier.width(14.dp))

            Row(
                modifier = Modifier.weight(1f),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(10.dp)
            ) {
                Box(
                    modifier = Modifier
                        .size(30.dp)
                        .clip(RoundedCornerShape(8.dp))
                        .background(SurfaceElevated),
                    contentAlignment = Alignment.Center
                ) {
                    UploadArrowIcon(color = TextSecondary, modifier = Modifier.size(15.dp))
                }
                Column {
                    Text(
                        text = "UPLOAD",
                        fontSize = 9.sp,
                        fontWeight = FontWeight.SemiBold,
                        letterSpacing = 0.8.sp,
                        color = TextDim
                    )
                    Text(
                        text = fmtBytes(telemetry.txBytes),
                        fontSize = 14.sp,
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
            .padding(horizontal = 8.dp, vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(4.dp)
    ) {
        Text(
            text = label,
            fontSize = 10.sp,
            fontFamily = FontFamily.Monospace,
            color = TextDim
        )
        Text(
            text = value,
            fontSize = 10.sp,
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
            .padding(horizontal = 16.dp, vertical = 13.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp)
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text(
                text = "CARRIER MATRIX",
                fontSize = 10.sp,
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
                        .height(8.dp)
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
// TAB 1: ROUTES (Nodes & Config)
// ---------------------------------------------------------------------------

@Composable
private fun RoutesTab(
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
    onSwapRoutes: () -> Unit,
    onImport: () -> Unit,
    onCopy: () -> Unit,
    onSave: () -> Unit
) {
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
                    .height(46.dp),
                shape = RoundedCornerShape(14.dp),
                colors = ButtonDefaults.buttonColors(
                    containerColor = TextPrimary,
                    contentColor = BgCarbon
                ),
                contentPadding = PaddingValues(horizontal = 14.dp)
            ) {
                ClipboardImportIcon(color = BgCarbon, modifier = Modifier.size(16.dp))
                Spacer(Modifier.width(8.dp))
                Text(
                    text = "Import Link",
                    fontSize = 13.sp,
                    fontWeight = FontWeight.Bold,
                    color = BgCarbon
                )
            }

            OutlinedButton(
                onClick = onCopy,
                modifier = Modifier
                    .weight(1f)
                    .height(46.dp),
                shape = RoundedCornerShape(14.dp),
                border = BorderStroke(1.dp, BorderHairline),
                colors = ButtonDefaults.outlinedButtonColors(
                    containerColor = SurfaceElevated,
                    contentColor = TextPrimary
                ),
                contentPadding = PaddingValues(horizontal = 14.dp)
            ) {
                ShareCopyIcon(color = TextPrimary, modifier = Modifier.size(16.dp))
                Spacer(Modifier.width(8.dp))
                Text(
                    text = "Copy Link",
                    fontSize = 13.sp,
                    fontWeight = FontWeight.SemiBold,
                    color = TextPrimary
                )
            }
        }

        // Active Failover Route Summary Card
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
                    verticalArrangement = Arrangement.spacedBy(3.dp)
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
                        fontSize = 12.sp,
                        fontFamily = FontFamily.Monospace,
                        color = TextPrimary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                }

                Text(
                    text = "→",
                    fontSize = 15.sp,
                    color = TextDim,
                    modifier = Modifier.padding(horizontal = 8.dp)
                )

                Column(
                    modifier = Modifier.weight(1f),
                    verticalArrangement = Arrangement.spacedBy(3.dp)
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
                        fontSize = 12.sp,
                        fontFamily = FontFamily.Monospace,
                        color = TextSecondary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                }
            }

            OutlinedButton(
                onClick = onSwapRoutes,
                modifier = Modifier
                    .fillMaxWidth()
                    .height(40.dp),
                shape = RoundedCornerShape(10.dp),
                border = BorderStroke(1.dp, BorderHairline),
                colors = ButtonDefaults.outlinedButtonColors(
                    containerColor = SurfaceElevated,
                    contentColor = TextPrimary
                ),
                contentPadding = PaddingValues(horizontal = 12.dp)
            ) {
                SwapArrowsIcon(color = SignalEmeraldBright, modifier = Modifier.size(15.dp))
                Spacer(Modifier.width(8.dp))
                Text(
                    text = "Swap Primary ↔ Fallback",
                    fontSize = 12.sp,
                    fontWeight = FontWeight.SemiBold,
                    color = TextPrimary
                )
            }
        }

        // Endpoints Editor Card
        SectionCard(title = "ENDPOINTS") {
            AetherTextField(
                value = primaryUrl,
                onValueChange = onPrimaryChange,
                label = "Primary Endpoint URL",
                placeholder = "wss://node.example.com/ws/sync"
            )
            AetherTextField(
                value = fallbackUrl,
                onValueChange = onFallbackChange,
                label = "Fallback Endpoint URL",
                placeholder = "wss://backup.example.com/ws/sync"
            )
            AetherTextField(
                value = cfCleanIp,
                onValueChange = onCleanIpChange,
                label = "Optional Cloudflare Clean IP",
                placeholder = "Leave blank for automatic DNS"
            )
        }

        // Transport & Parallel Carriers Card
        SectionCard(title = "TRANSPORT & PARALLEL CARRIERS") {
            Text(
                text = "Transport Mode",
                fontSize = 12.sp,
                fontWeight = FontWeight.Medium,
                color = TextSecondary
            )

            // 3-Way Segmented Control inside recessed track
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(12.dp))
                    .background(SurfaceRecessed)
                    .border(1.dp, BorderHairline, RoundedCornerShape(12.dp))
                    .padding(4.dp),
                horizontalArrangement = Arrangement.spacedBy(4.dp)
            ) {
                listOf("auto" to "AUTO", "ws" to "WebSocket", "h2" to "HTTP/2").forEach { (key, label) ->
                    val sel = transport.equals(key, ignoreCase = true)
                    Box(
                        modifier = Modifier
                            .weight(1f)
                            .clip(RoundedCornerShape(9.dp))
                            .background(if (sel) SignalEmerald.copy(alpha = 0.16f) else Color.Transparent)
                            .border(
                                width = 1.dp,
                                color = if (sel) SignalEmerald.copy(alpha = 0.50f) else Color.Transparent,
                                shape = RoundedCornerShape(9.dp)
                            )
                            .clickable { onTransportChange(key) }
                            .padding(vertical = 9.dp),
                        contentAlignment = Alignment.Center
                    ) {
                        Text(
                            text = label,
                            fontSize = 12.sp,
                            fontWeight = if (sel) FontWeight.Bold else FontWeight.Medium,
                            color = if (sel) SignalEmeraldBright else TextDim
                        )
                    }
                }
            }

            Spacer(Modifier.height(2.dp))

            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text(
                    text = "Parallel Carriers",
                    fontSize = 12.sp,
                    fontWeight = FontWeight.Medium,
                    color = TextSecondary
                )
                Text(
                    text = "$carriers active pool",
                    fontSize = 11.sp,
                    fontFamily = FontFamily.Monospace,
                    color = TextDim
                )
            }

            // 4 Selectable Carrier Chips (2, 3, 4 optimal, 6)
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
                    val sel = carriers == count
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
                            .clickable { onCarriersChange(count) }
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

        // Primary Save CTA (WCAG AAA dark-on-emerald contrast)
        Button(
            onClick = onSave,
            modifier = Modifier
                .fillMaxWidth()
                .height(50.dp),
            shape = RoundedCornerShape(14.dp),
            colors = ButtonDefaults.buttonColors(
                containerColor = SignalEmerald,
                contentColor = OnEmeraldDark
            )
        ) {
            Text(
                text = "Save Routes",
                fontSize = 14.sp,
                fontWeight = FontWeight.Bold,
                color = OnEmeraldDark
            )
        }
    }
}

// ---------------------------------------------------------------------------
// TAB 2: SETTINGS
// ---------------------------------------------------------------------------

@Composable
private fun SettingsTab(
    psk: String,
    onPskChange: (String) -> Unit,
    onCopyPsk: () -> Unit,
    bypassLan: Boolean,
    onBypassLanChange: (Boolean) -> Unit,
    bypassApps: String,
    onBypassAppsChange: (String) -> Unit,
    onSave: () -> Unit
) {
    var showKey by remember { mutableStateOf(false) }

    Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
        // Authentication Card
        SectionCard(
            title = "AUTHENTICATION",
            trailing = {
                Row(
                    modifier = Modifier
                        .clip(RoundedCornerShape(8.dp))
                        .background(SurfaceElevated)
                        .border(1.dp, BorderHairline, RoundedCornerShape(8.dp))
                        .clickable(onClick = onCopyPsk)
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
                value = psk,
                onValueChange = onPskChange,
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
        }

        // Split Tunneling Card
        SectionCard(title = "SPLIT TUNNELING") {
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
                        text = "Bypass Local LAN",
                        fontSize = 14.sp,
                        fontWeight = FontWeight.SemiBold,
                        color = TextPrimary
                    )
                    Text(
                        text = "Keep local Wi-Fi devices direct (saves server quota)",
                        fontSize = 11.sp,
                        color = TextDim
                    )
                }
                Switch(
                    checked = bypassLan,
                    onCheckedChange = onBypassLanChange,
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

            HorizontalDivider(color = BorderHairline, thickness = 1.dp)

            AetherTextField(
                value = bypassApps,
                onValueChange = onBypassAppsChange,
                label = "Bypass Apps (comma-separated packages)",
                placeholder = "com.example.bank, com.local.app"
            )
        }

        // Protocol & Engine Spec Card (Structured 2-column rows)
        SectionCard(title = "PROTOCOL & ENGINE") {
            val specs = listOf(
                "Protocol" to "Aether v2 · 0-RTT",
                "Encryption" to "X25519 · XChaCha20-Poly1305",
                "Stealth" to "uTLS Chrome · Anti-DPI",
                "DNS Engine" to "AdGuard Hedged · 60s Cache",
                "UDP / QUIC" to "Full-Cone · MTU 1500",
                "Build" to "v3.2.0 (13)"
            )
            Column {
                specs.forEachIndexed { idx, (key, value) ->
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(vertical = 9.dp),
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
                            fontSize = 12.sp,
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

        // Primary Save CTA (WCAG AAA dark-on-emerald contrast)
        Button(
            onClick = onSave,
            modifier = Modifier
                .fillMaxWidth()
                .height(50.dp),
            shape = RoundedCornerShape(14.dp),
            colors = ButtonDefaults.buttonColors(
                containerColor = SignalEmerald,
                contentColor = OnEmeraldDark
            )
        ) {
            Text(
                text = "Save Settings",
                fontSize = 14.sp,
                fontWeight = FontWeight.Bold,
                color = OnEmeraldDark
            )
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
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
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
            { Text(placeholder, fontSize = 13.sp, color = TextDim) }
        } else {
            null
        },
        textStyle = TextStyle(
            fontSize = 13.sp,
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

        // Minimal geometric shield silhouette
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

        // Inner pulse node + subtle halo
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
private fun SettingsTabIcon(color: Color, modifier: Modifier = Modifier) {
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
        // Top arrow pointing right
        drawLine(color = color, start = Offset(w * 0.16f, h * 0.34f), end = Offset(w * 0.84f, h * 0.34f), strokeWidth = sw, cap = StrokeCap.Round)
        val rightHead = Path().apply {
            moveTo(w * 0.66f, h * 0.18f)
            lineTo(w * 0.84f, h * 0.34f)
            lineTo(w * 0.66f, h * 0.50f)
        }
        drawPath(path = rightHead, color = color, style = stroke)
        // Bottom arrow pointing left
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
        // Clipboard body
        drawRoundRect(
            color = color,
            topLeft = Offset(w * 0.18f, h * 0.22f),
            size = Size(w * 0.64f, h * 0.68f),
            cornerRadius = CornerRadius(2.5.dp.toPx(), 2.5.dp.toPx()),
            style = stroke
        )
        // Top clip
        drawRoundRect(
            color = color,
            topLeft = Offset(w * 0.36f, h * 0.10f),
            size = Size(w * 0.28f, h * 0.16f),
            cornerRadius = CornerRadius(1.5.dp.toPx(), 1.5.dp.toPx()),
            style = stroke
        )
        // Downward import arrow inside clipboard
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
        // Front card
        drawRoundRect(
            color = color,
            topLeft = Offset(w * 0.34f, h * 0.34f),
            size = Size(w * 0.52f, h * 0.54f),
            cornerRadius = CornerRadius(2.5.dp.toPx(), 2.5.dp.toPx()),
            style = stroke
        )
        // Back overlapping frame
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
