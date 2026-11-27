package com.aether.vpn

import aethermobile.Aethermobile
import android.app.Activity
import android.content.Intent
import android.net.VpnService
import android.os.Bundle
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
    var psk by remember { mutableStateOf(initial.psk) }
    var primaryUrl by remember { mutableStateOf(initial.primaryUrl) }
    var fallbackUrl by remember { mutableStateOf(initial.fallbackUrl) }
    var cfCleanIp by remember { mutableStateOf(initial.cfCleanIp) }
    var carriers by remember { mutableStateOf(initial.carriers.toString()) }

    var running by remember { mutableStateOf(false) }
    var statsText by remember { mutableStateOf("Disconnected") }
    var errorText by remember { mutableStateOf<String?>(null) }

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
        verticalArrangement = Arrangement.spacedBy(14.dp)
    ) {
        Text("AetherVPN", fontSize = 28.sp, fontWeight = FontWeight.Bold)
        Text("Multi-Path Encrypted Tunnel (Railway / Render / CF)", color = Color.Gray, fontSize = 13.sp)

        Card(modifier = Modifier.fillMaxWidth()) {
            Column(modifier = Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
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
                val cfg = AetherConfig(
                    psk = psk,
                    primaryUrl = primaryUrl,
                    fallbackUrl = fallbackUrl,
                    cfCleanIp = cfCleanIp,
                    carriers = carriers.toIntOrNull() ?: 3
                )
                if (running) onDisconnect() else onConnect(cfg)
            },
            modifier = Modifier.fillMaxWidth().height(52.dp),
            colors = ButtonDefaults.buttonColors(
                containerColor = if (running) Color(0xFFDC2626) else Color(0xFF2563EB)
            )
        ) {
            Text(if (running) "DISCONNECT" else "CONNECT", fontSize = 16.sp, fontWeight = FontWeight.SemiBold)
        }

        OutlinedTextField(
            value = primaryUrl,
            onValueChange = { primaryUrl = it },
            label = { Text("Primary Server URL (Railway WSS)") },
            singleLine = true,
            modifier = Modifier.fillMaxWidth()
        )

        OutlinedTextField(
            value = fallbackUrl,
            onValueChange = { fallbackUrl = it },
            label = { Text("Fallback Server URL (Render / CF Worker WSS)") },
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

        Row(horizontalArrangement = Arrangement.spacedBy(12.dp), modifier = Modifier.fillMaxWidth()) {
            OutlinedTextField(
                value = cfCleanIp,
                onValueChange = { cfCleanIp = it },
                label = { Text("Optional Clean IP") },
                singleLine = true,
                modifier = Modifier.weight(2f)
            )
            OutlinedTextField(
                value = carriers,
                onValueChange = { carriers = it },
                label = { Text("Carriers") },
                singleLine = true,
                modifier = Modifier.weight(1f)
            )
        }

        OutlinedButton(
            onClick = {
                onSave(
                    AetherConfig(
                        psk = psk,
                        primaryUrl = primaryUrl,
                        fallbackUrl = fallbackUrl,
                        cfCleanIp = cfCleanIp,
                        carriers = carriers.toIntOrNull() ?: 3
                    )
                )
            },
            modifier = Modifier.fillMaxWidth().align(Alignment.CenterHorizontally)
        ) {
            Text("Save Settings")
        }
    }
}
