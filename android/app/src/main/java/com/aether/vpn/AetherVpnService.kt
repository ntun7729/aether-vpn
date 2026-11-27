package com.aether.vpn

import aethermobile.Aethermobile
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.ComponentName
import android.content.Intent
import android.net.ConnectivityManager
import android.net.IpPrefix
import android.net.Network
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.service.quicksettings.TileService
import android.util.Log
import androidx.core.app.NotificationCompat
import java.net.InetAddress

class AetherVpnService : VpnService() {

    companion object {
        const val ACTION_START = "com.aether.vpn.START"
        const val ACTION_STOP = "com.aether.vpn.STOP"
        private const val CHANNEL_ID = "aether_vpn_channel"
        private const val NOTIF_ID = 1001
        private const val MTU = 1400

        @Volatile
        var lastError: String? = null
    }

    private var tunInterface: ParcelFileDescriptor? = null
    private var networkCallback: ConnectivityManager.NetworkCallback? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                stopTunnel()
                stopSelf()
                return START_NOT_STICKY
            }
            ACTION_START -> {
                startForeground(NOTIF_ID, buildNotification("Connecting to AetherVPN..."))
                Thread { startTunnel() }.start()
                return START_STICKY
            }
        }
        return START_NOT_STICKY
    }

    private fun startTunnel() {
        try {
            lastError = null
            val cfg = ConfigStore.load(this)
            val builder = Builder()
                .setSession("AetherVPN")
                .setMtu(MTU)
                .addAddress("10.88.0.2", 30)
                .addAddress("fd00:ae7e::2", 126)
                .addRoute("0.0.0.0", 0)
                .addRoute("::", 0)
                .addDnsServer("1.1.1.1")
                .addDnsServer("8.8.8.8")
                .addDisallowedApplication(packageName)

            // Split tunneling: exclude private LAN ranges on Android 13+ so local traffic stays direct
            if (cfg.bypassLan && Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                try {
                    builder.excludeRoute(IpPrefix(InetAddress.getByName("10.0.0.0"), 8))
                    builder.excludeRoute(IpPrefix(InetAddress.getByName("172.16.0.0"), 12))
                    builder.excludeRoute(IpPrefix(InetAddress.getByName("192.168.0.0"), 16))
                    builder.excludeRoute(IpPrefix(InetAddress.getByName("fc00::"), 7))
                    builder.excludeRoute(IpPrefix(InetAddress.getByName("fe80::"), 10))
                } catch (_: Exception) {
                }
            }

            // Per-app bypass list (e.g. local banking apps)
            if (cfg.bypassApps.isNotBlank()) {
                cfg.bypassApps.split(",").map { it.trim() }.filter { it.isNotEmpty() }.forEach { pkg ->
                    try {
                        builder.addDisallowedApplication(pkg)
                    } catch (_: Exception) {
                    }
                }
            }

            val pfd = builder.establish() ?: throw IllegalStateException("VpnService.Builder.establish() returned null")
            tunInterface = pfd

            val fd = pfd.detachFd()
            tunInterface = null

            Aethermobile.startVPN(fd.toLong(), MTU.toLong(), cfg.toCoreJson())
            registerRoamingCallback()
            updateNotification("AetherVPN Connected (Multi-Path + uTLS)")
            refreshQuickTile()
        } catch (e: Exception) {
            Log.e("AetherVPN", "Failed to start VPN", e)
            lastError = e.message ?: e.toString()
            stopTunnel()
            stopSelf()
        }
    }

    private fun registerRoamingCallback() {
        val cm = getSystemService(ConnectivityManager::class.java) ?: return
        unregisterRoamingCallback()
        var firstCallback = true
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                if (firstCallback) {
                    firstCallback = false
                    return
                }
                // Wi-Fi <-> 5G switch: immediately attach fresh carriers on the new interface!
                try {
                    Aethermobile.onNetworkChanged()
                } catch (_: Throwable) {
                }
            }
        }
        try {
            cm.registerDefaultNetworkCallback(cb)
            networkCallback = cb
        } catch (_: Exception) {
        }
    }

    private fun unregisterRoamingCallback() {
        val cb = networkCallback ?: return
        networkCallback = null
        try {
            getSystemService(ConnectivityManager::class.java)?.unregisterNetworkCallback(cb)
        } catch (_: Exception) {
        }
    }

    private fun stopTunnel() {
        unregisterRoamingCallback()
        try {
            Aethermobile.stopVPN()
        } catch (_: Exception) {
        }
        try {
            tunInterface?.close()
        } catch (_: Exception) {
        }
        tunInterface = null
        stopForeground(STOP_FOREGROUND_REMOVE)
        refreshQuickTile()
    }

    private fun refreshQuickTile() {
        try {
            TileService.requestListeningState(this, ComponentName(this, AetherTileService::class.java))
        } catch (_: Exception) {
        }
    }

    override fun onDestroy() {
        stopTunnel()
        super.onDestroy()
    }

    private fun buildNotification(text: String): Notification {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val nm = getSystemService(NotificationManager::class.java)
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL_ID, "AetherVPN Tunnel", NotificationManager.IMPORTANCE_LOW)
            )
        }
        val pi = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("AetherVPN")
            .setContentText(text)
            .setSmallIcon(android.R.drawable.ic_lock_lock)
            .setContentIntent(pi)
            .setOngoing(true)
            .build()
    }

    private fun updateNotification(text: String) {
        val nm = getSystemService(NotificationManager::class.java)
        nm.notify(NOTIF_ID, buildNotification(text))
    }
}
