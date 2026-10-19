package com.gantoine.muxer.net

import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

object Tailnet {
    private const val IPV6_PREFIX = "fd7a:115c:a1e0:"

    fun reaches(relayUrl: String): Boolean {
        val host = relayUrl.toHttpUrlOrNull()?.host ?: return false
        return host.endsWith(".ts.net") || isTailscaleAddress(host)
    }

    fun isTailscaleAddress(host: String): Boolean {
        if (host.startsWith(IPV6_PREFIX)) return true
        val octets = host.split('.').map { it.toIntOrNull() ?: return false }
        return octets.size == 4 && octets.all { it in 0..255 } && octets[0] == 100 && octets[1] in 64..127
    }
}
