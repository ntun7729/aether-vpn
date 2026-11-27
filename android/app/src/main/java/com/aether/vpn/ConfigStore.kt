package com.aether.vpn

import android.content.Context
import android.util.Base64
import org.json.JSONArray
import org.json.JSONObject

data class AetherConfig(
    val psk: String = "",
    val primaryUrl: String = "wss://aether-server-production.up.railway.app/ws/sync",
    val fallbackUrl: String = "wss://sync.novv.tech/ws/sync",
    val cfCleanIp: String = "",
    val transport: String = "auto", // auto | ws | h2
    val carriers: Int = 3,
    val bypassLan: Boolean = true,
    val bypassApps: String = "" // comma-separated Android package names
) {
    fun toCoreJson(): String {
        val endpoints = JSONArray()
        if (primaryUrl.isNotBlank()) {
            val u = primaryUrl.trim()
            endpoints.put(JSONObject().apply {
                put("name", "railway-primary")
                put("url", u)
                if (cfCleanIp.isNotBlank() && !u.contains("railway.app", ignoreCase = true)) {
                    put("resolve_ip", cfCleanIp.trim())
                }
                put("transport", transport.trim().ifBlank { "auto" })
                put("priority", 0)
            })
        }
        if (fallbackUrl.isNotBlank()) {
            val u = fallbackUrl.trim()
            endpoints.put(JSONObject().apply {
                put("name", "cf-fallback")
                put("url", u)
                if (cfCleanIp.isNotBlank() && !u.contains("railway.app", ignoreCase = true)) {
                    put("resolve_ip", cfCleanIp.trim())
                }
                put("transport", transport.trim().ifBlank { "auto" })
                put("priority", 1)
            })
        }
        return JSONObject().apply {
            put("psk", psk.trim())
            put("carriers", carriers.coerceIn(1, 8))
            put("endpoints", endpoints)
        }.toString()
    }

    fun toShareUri(): String {
        val j = JSONObject().apply {
            put("psk", psk.trim())
            put("primary_url", primaryUrl.trim())
            if (fallbackUrl.isNotBlank()) put("fallback_url", fallbackUrl.trim())
            if (cfCleanIp.isNotBlank()) put("cf_clean_ip", cfCleanIp.trim())
            put("transport", transport)
            put("carriers", carriers)
        }.toString()
        val encoded = Base64.encodeToString(j.toByteArray(Charsets.UTF_8), Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING)
        return "aether://$encoded"
    }

    companion object {
        fun fromShareUri(uri: String, current: AetherConfig): AetherConfig? {
            val trimmed = uri.trim()
            if (!trimmed.startsWith("aether://")) return null
            return try {
                val b64 = trimmed.removePrefix("aether://")
                val raw = String(Base64.decode(b64, Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING), Charsets.UTF_8)
                val j = JSONObject(raw)
                current.copy(
                    psk = j.optString("psk", current.psk),
                    primaryUrl = j.optString("primary_url", current.primaryUrl),
                    fallbackUrl = j.optString("fallback_url", current.fallbackUrl),
                    cfCleanIp = j.optString("cf_clean_ip", current.cfCleanIp),
                    transport = j.optString("transport", current.transport),
                    carriers = j.optInt("carriers", current.carriers).coerceIn(1, 8)
                )
            } catch (_: Exception) {
                null
            }
        }
    }
}

object ConfigStore {
    private const val PREFS = "aether_vpn_prefs"

    fun load(context: Context): AetherConfig {
        val p = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        return AetherConfig(
            psk = p.getString("psk", "") ?: "",
            primaryUrl = p.getString("primary_url", "wss://aether-server-production.up.railway.app/ws/sync") ?: "",
            fallbackUrl = p.getString("fallback_url", "wss://sync.novv.tech/ws/sync") ?: "",
            cfCleanIp = p.getString("cf_clean_ip", "") ?: "",
            transport = p.getString("transport", "auto") ?: "auto",
            carriers = p.getInt("carriers", 3),
            bypassLan = p.getBoolean("bypass_lan", true),
            bypassApps = p.getString("bypass_apps", "") ?: ""
        )
    }

    fun save(context: Context, cfg: AetherConfig) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString("psk", cfg.psk)
            .putString("primary_url", cfg.primaryUrl)
            .putString("fallback_url", cfg.fallbackUrl)
            .putString("cf_clean_ip", cfg.cfCleanIp)
            .putString("transport", cfg.transport)
            .putInt("carriers", cfg.carriers)
            .putBoolean("bypass_lan", cfg.bypassLan)
            .putString("bypass_apps", cfg.bypassApps)
            .apply()
    }
}
