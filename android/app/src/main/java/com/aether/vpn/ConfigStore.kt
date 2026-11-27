package com.aether.vpn

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject

data class AetherConfig(
    val psk: String = "",
    val primaryUrl: String = "wss://your-app.up.railway.app/ws/sync",
    val fallbackUrl: String = "",
    val cfCleanIp: String = "",
    val carriers: Int = 3
) {
    fun toCoreJson(): String {
        val endpoints = JSONArray()
        if (primaryUrl.isNotBlank()) {
            endpoints.put(JSONObject().apply {
                put("name", "railway-primary")
                put("url", primaryUrl.trim())
                if (cfCleanIp.isNotBlank()) put("resolve_ip", cfCleanIp.trim())
                put("priority", 0)
            })
        }
        if (fallbackUrl.isNotBlank()) {
            endpoints.put(JSONObject().apply {
                put("name", "render-fallback")
                put("url", fallbackUrl.trim())
                put("priority", 1)
            })
        }
        return JSONObject().apply {
            put("psk", psk.trim())
            put("carriers", carriers.coerceIn(1, 8))
            put("endpoints", endpoints)
        }.toString()
    }
}

object ConfigStore {
    private const val PREFS = "aether_vpn_prefs"

    fun load(context: Context): AetherConfig {
        val p = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        return AetherConfig(
            psk = p.getString("psk", "") ?: "",
            primaryUrl = p.getString("primary_url", "wss://your-app.up.railway.app/ws/sync") ?: "",
            fallbackUrl = p.getString("fallback_url", "") ?: "",
            cfCleanIp = p.getString("cf_clean_ip", "") ?: "",
            carriers = p.getInt("carriers", 3)
        )
    }

    fun save(context: Context, cfg: AetherConfig) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString("psk", cfg.psk)
            .putString("primary_url", cfg.primaryUrl)
            .putString("fallback_url", cfg.fallbackUrl)
            .putString("cf_clean_ip", cfg.cfCleanIp)
            .putInt("carriers", cfg.carriers)
            .apply()
    }
}
