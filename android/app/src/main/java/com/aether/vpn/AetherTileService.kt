package com.aether.vpn

import aethermobile.Aethermobile
import android.content.Intent
import android.net.VpnService
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService

class AetherTileService : TileService() {

    override fun onStartListening() {
        super.onStartListening()
        syncTileState()
    }

    override fun onClick() {
        super.onClick()
        val running = try { Aethermobile.isRunning() } catch (_: Throwable) { false }
        if (running) {
            val intent = Intent(this, AetherVpnService::class.java).apply {
                action = AetherVpnService.ACTION_STOP
            }
            startService(intent)
        } else {
            if (VpnService.prepare(this) != null) {
                // First-time VPN permission required — open MainActivity
                val ui = Intent(this, MainActivity::class.java).apply {
                    addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                }
                startActivityAndCollapse(ui)
                return
            }
            val intent = Intent(this, AetherVpnService::class.java).apply {
                action = AetherVpnService.ACTION_START
            }
            startForegroundService(intent)
        }
        syncTileState()
    }

    private fun syncTileState() {
        val tile = qsTile ?: return
        val running = try { Aethermobile.isRunning() } catch (_: Throwable) { false }
        tile.state = if (running) Tile.STATE_ACTIVE else Tile.STATE_INACTIVE
        tile.label = "AetherVPN"
        tile.updateTile()
    }
}
