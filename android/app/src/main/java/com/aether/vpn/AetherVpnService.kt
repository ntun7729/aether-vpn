package com.aether.vpn

import aethermobile.Aethermobile
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log
import androidx.core.app.NotificationCompat

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
                .addRoute("0.0.0.0", 0)
                .addDnsServer("1.1.1.1")
                .addDnsServer("8.8.8.8")
                .addDisallowedApplication(packageName) // Prevent VPN routing loop

            val pfd = builder.establish() ?: throw IllegalStateException("VpnService.Builder.establish() returned null")
            tunInterface = pfd

            // Detach FD so Go's os.NewFile owns its lifecycle cleanly
            val fd = pfd.detachFd()
            tunInterface = null

            Aethermobile.startVPN(fd.toLong(), MTU.toLong(), cfg.toCoreJson())
            updateNotification("AetherVPN Connected (Multi-Path Active)")
        } catch (e: Exception) {
            Log.e("AetherVPN", "Failed to start VPN", e)
            lastError = e.message ?: e.toString()
            stopTunnel()
            stopSelf()
        }
    }

    private fun stopTunnel() {
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
