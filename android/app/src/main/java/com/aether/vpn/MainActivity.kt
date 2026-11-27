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
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay
import org.json.JSONObject

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
            MaterialTheme(colorScheme = darkColorScheme()) {
                Surface(modifier = Modifier.fillMaxSize()) {
                    AetherScreen(
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

@Composable
fun AetherScreen(
    initial: AetherConfig,
    onSave: (AetherConfig) -> Unit,
    onConnect: (AetherConfig) -> Unit,
    onDisconnect: () -> Unit
) {
    val context = LocalContext.current
    var psk by remember { mutableStateOf(initial.psk) }
    var primaryUrl by remember { mutableStateOf(initial.primaryUrl) }
    var fallbackUrl by remember { mutableStateOf(initial.fallbackUrl) }
    var cfCleanIp by remember { mutableStateOf(initial.cfCleanIp) }
    var transport by remember { mutableStateOf(initial.transport) }
    var carriers by remember { mutableStateOf(initial.carriers.toString()) }
    var bypassLan by remember { mutableStateOf(initial.bypassLan) }
    var bypassApps by remember { mutableStateOf(initial.bypassApps) }

    var running by remember { mutableStateOf(false) }
    var statsText by remember { mutableStateOf("Disconnected") }
    var errorText by remember { mutableStateOf<String?>(null) }

    fun currentConfig() = AetherConfig(
        psk = psk,
        primaryUrl = primaryUrl,
        fallbackUrl = fallbackUrl,
        cfCleanIp = cfCleanIp,
        transport = transport,
        carriers = carriers.toIntOrNull() ?: 3,
        bypassLan = bypassLan,
        bypassApps = bypassApps
    )

    LaunchedEffect(Unit) {
        while (true) {
            running = try { Aethermobile.isRunning() } catch (_: Throwable) { false }
            errorText = AetherVpnService.lastError
            statsText = if (running) {
                try {
                    val j = JSONObject(Aethermobile.getStatsJSON())
                    val txKb = j.optLong("tx_bytes") / 1024
                    val rxKb = j.optLong("rx_bytes") / 1024
                    "Endpoint: ${j.optString("endpoint")}\n" +
                        "Carriers: ${j.optInt("active_carriers")} | Streams: ${j.optInt("active_streams")} | RTT: ${j.optLong("rtt_ms")}ms\n" +
                        "TX: ${txKb} KB | RX: ${rxKb} KB | Failovers: ${j.optLong("failovers")}"
                } catch (_: Throwable) {
                    "Connected"
                }
            } else {
                "Disconnected"
            }
            delay(1000)
        }
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(20.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Text("AetherVPN v1.1", fontSize = 26.sp, fontWeight = FontWeight.Bold)
        Text("uTLS Chrome · Multi-Path WS/H2 · Auto-Failover · AdBlock", color = Color.Gray, fontSize = 12.sp)

        Card(modifier = Modifier.fillMaxWidth()) {
            Column(modifier = Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text(
                    if (running) "CONNECTED" else "DISCONNECTED",
                    color = if (running) Color(0xFF4ADE80) else Color(0xFFF87171),
                    fontWeight = FontWeight.Bold
                )
                Text(statsText, fontSize = 13.sp)
                if (!errorText.isNullOrBlank()) {
                    Text("Error: $errorText", color = Color(0xFFF87171), fontSize = 12.sp)
                }
            }
        }

        Button(
            onClick = {
                val cfg = currentConfig()
                if (running) onDisconnect() else onConnect(cfg)
            },
            modifier = Modifier.fillMaxWidth().height(52.dp),
            colors = ButtonDefaults.buttonColors(
                containerColor = if (running) Color(0xFFDC2626) else Color(0xFF2563EB)
            )
        ) {
            Text(if (running) "DISCONNECT" else "CONNECT", fontSize = 16.sp, fontWeight = FontWeight.SemiBold)
        }

        // 1-Tap aether:// URI Import / Copy
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp), modifier = Modifier.fillMaxWidth()) {
            OutlinedButton(
                onClick = {
                    val cm = context.getSystemService(ClipboardManager::class.java)
                    val clip = cm?.primaryClip?.getItemAt(0)?.text?.toString() ?: ""
                    val imported = AetherConfig.fromShareUri(clip, currentConfig())
                    if (imported != null) {
                        psk = imported.psk
                        primaryUrl = imported.primaryUrl
                        fallbackUrl = imported.fallbackUrl
                        cfCleanIp = imported.cfCleanIp
                        transport = imported.transport
                        carriers = imported.carriers.toString()
                        onSave(imported)
                        Toast.makeText(context, "Imported aether:// config!", Toast.LENGTH_SHORT).show()
                    } else {
                        Toast.makeText(context, "Copy an aether:// link first", Toast.LENGTH_SHORT).show()
                    }
                },
                modifier = Modifier.weight(1f)
            ) {
                Text("Import Link", fontSize = 13.sp)
            }
            OutlinedButton(
                onClick = {
                    val uri = currentConfig().toShareUri()
                    val cm = context.getSystemService(ClipboardManager::class.java)
                    cm?.setPrimaryClip(ClipData.newPlainText("AetherVPN", uri))
                    Toast.makeText(context, "Copied aether:// link!", Toast.LENGTH_SHORT).show()
                },
                modifier = Modifier.weight(1f)
            ) {
                Text("Copy Share Link", fontSize = 13.sp)
            }
        }

        OutlinedTextField(
            value = primaryUrl,
            onValueChange = { primaryUrl = it },
            label = { Text("Primary Server URL (Railway)") },
            singleLine = true,
            modifier = Modifier.fillMaxWidth()
        )

        OutlinedTextField(
            value = fallbackUrl,
            onValueChange = { fallbackUrl = it },
            label = { Text("Fallback URL (Render / CF Worker)") },
            singleLine = true,
            modifier = Modifier.fillMaxWidth()
        )

        OutlinedTextField(
            value = psk,
            onValueChange = { psk = it },
            label = { Text("Secret Key (AETHER_PSK)") },
            singleLine = true,
            modifier = Modifier.fillMaxWidth()
        )

        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.fillMaxWidth()) {
            OutlinedTextField(
                value = cfCleanIp,
                onValueChange = { cfCleanIp = it },
                label = { Text("Clean IP") },
                singleLine = true,
                modifier = Modifier.weight(2f)
            )
            OutlinedTextField(
                value = transport,
                onValueChange = { transport = it },
                label = { Text("Mode") },
                singleLine = true,
                modifier = Modifier.weight(1.1f)
            )
            OutlinedTextField(
                value = carriers,
                onValueChange = { carriers = it },
                label = { Text("Pool") },
                singleLine = true,
                modifier = Modifier.weight(0.9f)
            )
        }

        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween,
            modifier = Modifier.fillMaxWidth()
        ) {
            Text("Bypass Local LAN (Save Quota)", fontSize = 14.sp)
            Switch(checked = bypassLan, onCheckedChange = { bypassLan = it })
        }

        OutlinedTextField(
            value = bypassApps,
            onValueChange = { bypassApps = it },
            label = { Text("Bypass Apps (comma-separated packages)") },
            singleLine = true,
            modifier = Modifier.fillMaxWidth()
        )

        OutlinedButton(
            onClick = {
                onSave(currentConfig())
                Toast.makeText(context, "Settings saved", Toast.LENGTH_SHORT).show()
            },
            modifier = Modifier.fillMaxWidth()
        ) {
            Text("Save Settings")
        }
    }
}
