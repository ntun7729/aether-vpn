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
    val protocolProfile: String = "mux", // mux | fast | ghost | custom
    val transport: String = "auto", // auto | ws | xhttp | grpc | h2
    val cipher: String = "xchacha20-poly1305", // xchacha20-poly1305 | aes-256-gcm | aes-128-gcm | chacha20-poly1305 | none
    val fingerprint: String = "chrome", // chrome | firefox | safari | ios | randomized
    val paddingMode: String = "balanced", // balanced | stealth | off
    val fragmentMode: String = "off", // off | balanced | strict
    val quicMode: String = "full", // full | force_tcp | dns_only
    val windowProfile: String = "max", // max | balanced | gaming
    val customPath: String = "/ws/sync", // e.g. /ws/sync, /api/v2/events, /cdn/assets/sync
    val idlePower: Boolean = false,
    val carriers: Int = 4,
    val dnsPreset: String = "adguard", // adguard | cloudflare | google | custom
    val customDns: String = "",
    val blockDot: Boolean = true,
    val adblockSni: Boolean = false,
    val autoSync: Boolean = true,
    val configUpdatedAt: Long = 0L,
    val bypassLan: Boolean = true,
    val bypassApps: String = "" // comma-separated Android package names
) {
    fun withProtocolProfile(profile: String): AetherConfig = when (profile) {
        "fast" -> copy(
            protocolProfile = "fast",
            cipher = "none",
            transport = "ws",
            paddingMode = "off",
            fragmentMode = "off",
            windowProfile = "max",
            carriers = 4
        )
        "ghost" -> copy(
            protocolProfile = "ghost",
            cipher = "xchacha20-poly1305",
            transport = "xhttp",
            fingerprint = "randomized",
            paddingMode = "stealth",
            fragmentMode = "strict",
            windowProfile = "balanced",
            carriers = 4
        )
        "mux" -> copy(
            protocolProfile = "mux",
            cipher = "xchacha20-poly1305",
            transport = "auto",
            fingerprint = "chrome",
            paddingMode = "balanced",
            fragmentMode = "off",
            windowProfile = "max",
            carriers = 4
        )
        else -> copy(protocolProfile = "custom")
    }

    fun toCoreJson(): String {
        val endpoints = JSONArray()
        val fp = fingerprint.trim().ifBlank { "chrome" }
        val tr = transport.trim().ifBlank { "auto" }
        val fm = fragmentMode.trim().ifBlank { "off" }
        val cp = customPath.trim().let { if (it.isNotBlank() && !it.startsWith("/")) "/$it" else it }
        if (primaryUrl.isNotBlank()) {
            val u = primaryUrl.trim()
            endpoints.put(JSONObject().apply {
                put("name", "railway-primary")
                put("url", u)
                if (cfCleanIp.isNotBlank() && !u.contains("railway.app", ignoreCase = true)) {
                    put("resolve_ip", cfCleanIp.trim())
                }
                put("transport", tr)
                put("fingerprint", fp)
                put("fragment_mode", fm)
                if (cp.isNotBlank()) put("custom_path", cp)
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
                put("transport", tr)
                put("fingerprint", fp)
                put("fragment_mode", fm)
                if (cp.isNotBlank()) put("custom_path", cp)
                put("priority", 1)
            })
        }
        return JSONObject().apply {
            put("psk", psk.trim())
            put("carriers", carriers.coerceIn(1, 6))
            put("cipher", cipher.trim().ifBlank { "xchacha20-poly1305" })
            put("padding_mode", paddingMode.trim().ifBlank { "balanced" })
            put("window_profile", windowProfile.trim().ifBlank { "max" })
            put("quic_mode", quicMode.trim().ifBlank { "full" })
            put("idle_power", idlePower)
            put("endpoints", endpoints)
        }.toString()
    }

    fun toSyncPatchJson(): String = JSONObject().apply {
        put("protocol_profile", protocolProfile.ifBlank { "mux" })
        put("dns_preset", dnsPreset.ifBlank { "adguard" })
        if (customDns.isNotBlank()) put("custom_dns", customDns.trim())
        put("block_dot", blockDot)
        put("adblock_sni", adblockSni)
        put("cipher", cipher.ifBlank { "xchacha20-poly1305" })
        put("transport", transport.ifBlank { "auto" })
        put("fingerprint", fingerprint.ifBlank { "chrome" })
        put("padding_mode", paddingMode.ifBlank { "balanced" })
        put("fragment_mode", fragmentMode.ifBlank { "off" })
        put("quic_mode", quicMode.ifBlank { "full" })
        put("window_profile", windowProfile.ifBlank { "max" })
        put("custom_path", customPath.trim().ifBlank { "/ws/sync" })
        put("idle_power", idlePower)
        put("carriers", carriers.coerceIn(1, 6))
        put("fallback_url", fallbackUrl.trim())
    }.toString()

    fun mergeFromServerSync(rawJson: String): Pair<AetherConfig, Boolean> {
        return try {
            val j = JSONObject(rawJson)
            val srvUpdated = j.optLong("updated_at", 0L)
            val nextProfile = j.optString("protocol_profile", protocolProfile).ifBlank { protocolProfile }
            val nextCipher = j.optString("cipher", cipher).ifBlank { cipher }
            val nextTransport = j.optString("transport", transport).ifBlank { transport }
            val nextFingerprint = j.optString("fingerprint", fingerprint).ifBlank { fingerprint }
            val nextPadding = j.optString("padding_mode", paddingMode).ifBlank { paddingMode }
            val nextFragment = j.optString("fragment_mode", fragmentMode).ifBlank { fragmentMode }
            val nextQuic = j.optString("quic_mode", quicMode).ifBlank { quicMode }
            val nextWindow = j.optString("window_profile", windowProfile).ifBlank { windowProfile }
            val nextPath = j.optString("custom_path", customPath).ifBlank { customPath }
            val nextIdlePower = if (j.has("idle_power")) j.optBoolean("idle_power", idlePower) else idlePower
            val nextCarriers = j.optInt("carriers", carriers).coerceIn(1, 6)
            val nextDns = j.optString("dns_preset", dnsPreset).ifBlank { dnsPreset }
            val nextCustomDns = j.optString("custom_dns", customDns)
            val nextBlockDot = if (j.has("block_dot")) j.optBoolean("block_dot", blockDot) else blockDot
            val nextAdblockSni = if (j.has("adblock_sni")) j.optBoolean("adblock_sni", adblockSni) else adblockSni
            val nextFallback = if (j.has("fallback_url") && j.optString("fallback_url").isNotBlank()) {
                j.optString("fallback_url")
            } else fallbackUrl

            val needsReconnect = nextCipher != cipher ||
                nextTransport != transport ||
                nextFingerprint != fingerprint ||
                nextFragment != fragmentMode ||
                nextPath != customPath ||
                nextCarriers != carriers ||
                nextFallback != fallbackUrl

            val updated = copy(
                protocolProfile = nextProfile,
                cipher = nextCipher,
                transport = nextTransport,
                fingerprint = nextFingerprint,
                paddingMode = nextPadding,
                fragmentMode = nextFragment,
                quicMode = nextQuic,
                windowProfile = nextWindow,
                customPath = nextPath,
                idlePower = nextIdlePower,
                carriers = nextCarriers,
                dnsPreset = nextDns,
                customDns = nextCustomDns,
                blockDot = nextBlockDot,
                adblockSni = nextAdblockSni,
                fallbackUrl = nextFallback,
                configUpdatedAt = if (srvUpdated > 0L) srvUpdated else System.currentTimeMillis()
            )
            updated to needsReconnect
        } catch (_: Exception) {
            this to false
        }
    }

    fun toShareUri(): String {
        val j = JSONObject().apply {
            put("psk", psk.trim())
            put("primary_url", primaryUrl.trim())
            if (fallbackUrl.isNotBlank()) put("fallback_url", fallbackUrl.trim())
            if (cfCleanIp.isNotBlank()) put("cf_clean_ip", cfCleanIp.trim())
            put("protocol_profile", protocolProfile)
            put("transport", transport)
            put("cipher", cipher)
            put("fingerprint", fingerprint)
            put("padding_mode", paddingMode)
            put("fragment_mode", fragmentMode)
            put("quic_mode", quicMode)
            put("window_profile", windowProfile)
            put("custom_path", customPath)
            put("idle_power", idlePower)
            put("carriers", carriers)
        }.toString()
        val encoded = Base64.encodeToString(
            j.toByteArray(Charsets.UTF_8),
            Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING
        )
        return "aether://$encoded"
    }

    fun bypassSet(): Set<String> = bypassApps
        .split(",")
        .map { it.trim() }
        .filter { it.isNotEmpty() }
        .toSet()

    companion object {
        fun fromShareUri(uri: String, current: AetherConfig): AetherConfig? {
            val trimmed = uri.trim()
            if (!trimmed.startsWith("aether://")) return null
            return try {
                val b64 = trimmed.removePrefix("aether://")
                val raw = String(
                    Base64.decode(b64, Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING),
                    Charsets.UTF_8
                )
                val j = JSONObject(raw)
                current.copy(
                    psk = j.optString("psk", current.psk),
                    primaryUrl = j.optString("primary_url", current.primaryUrl),
                    fallbackUrl = j.optString("fallback_url", current.fallbackUrl),
                    cfCleanIp = j.optString("cf_clean_ip", current.cfCleanIp),
                    protocolProfile = j.optString("protocol_profile", current.protocolProfile),
                    transport = j.optString("transport", current.transport),
                    cipher = j.optString("cipher", current.cipher),
                    fingerprint = j.optString("fingerprint", current.fingerprint),
                    paddingMode = j.optString("padding_mode", current.paddingMode),
                    fragmentMode = j.optString("fragment_mode", current.fragmentMode),
                    quicMode = j.optString("quic_mode", current.quicMode),
                    windowProfile = j.optString("window_profile", current.windowProfile),
                    customPath = j.optString("custom_path", current.customPath),
                    idlePower = j.optBoolean("idle_power", current.idlePower),
                    carriers = j.optInt("carriers", current.carriers).coerceIn(1, 6)
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
            protocolProfile = p.getString("protocol_profile", "mux") ?: "mux",
            transport = p.getString("transport", "auto") ?: "auto",
            cipher = p.getString("cipher", "xchacha20-poly1305") ?: "xchacha20-poly1305",
            fingerprint = p.getString("fingerprint", "chrome") ?: "chrome",
            paddingMode = p.getString("padding_mode", "balanced") ?: "balanced",
            fragmentMode = p.getString("fragment_mode", "off") ?: "off",
            quicMode = p.getString("quic_mode", "full") ?: "full",
            windowProfile = p.getString("window_profile", "max") ?: "max",
            customPath = p.getString("custom_path", "/ws/sync") ?: "/ws/sync",
            idlePower = p.getBoolean("idle_power", false),
            carriers = p.getInt("carriers", 4).coerceIn(1, 6),
            dnsPreset = p.getString("dns_preset", "adguard") ?: "adguard",
            customDns = p.getString("custom_dns", "") ?: "",
            blockDot = p.getBoolean("block_dot", true),
            adblockSni = p.getBoolean("adblock_sni", false),
            autoSync = p.getBoolean("auto_sync", true),
            configUpdatedAt = p.getLong("config_updated_at", 0L),
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
            .putString("protocol_profile", cfg.protocolProfile)
            .putString("transport", cfg.transport)
            .putString("cipher", cfg.cipher)
            .putString("fingerprint", cfg.fingerprint)
            .putString("padding_mode", cfg.paddingMode)
            .putString("fragment_mode", cfg.fragmentMode)
            .putString("quic_mode", cfg.quicMode)
            .putString("window_profile", cfg.windowProfile)
            .putString("custom_path", cfg.customPath)
            .putBoolean("idle_power", cfg.idlePower)
            .putInt("carriers", cfg.carriers.coerceIn(1, 6))
            .putString("dns_preset", cfg.dnsPreset)
            .putString("custom_dns", cfg.customDns)
            .putBoolean("block_dot", cfg.blockDot)
            .putBoolean("adblock_sni", cfg.adblockSni)
            .putBoolean("auto_sync", cfg.autoSync)
            .putLong("config_updated_at", cfg.configUpdatedAt)
            .putBoolean("bypass_lan", cfg.bypassLan)
            .putString("bypass_apps", cfg.bypassApps)
            .apply()
    }
}
