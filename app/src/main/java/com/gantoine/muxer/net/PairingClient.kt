package com.gantoine.muxer.net

import com.gantoine.muxer.protocol.ErrorResponse
import com.gantoine.muxer.protocol.PairResponse
import com.gantoine.muxer.protocol.RelayJson
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException

class PairingException(message: String) : Exception(message)

class PairingClient(private val client: OkHttpClient) {
    suspend fun pair(address: String, code: String, deviceName: String): Pair<String, PairResponse> =
        withContext(Dispatchers.IO) {
            val base = normalizeRelayUrl(address) ?: throw PairingException(
                "Enter the address that `muxer pair` printed, like https://my-mac.tailnet.ts.net:8765",
            )
            val body = buildJsonObject {
                put("code", code.trim())
                put("device_name", deviceName)
            }.toString().toRequestBody(JSON)
            val request = Request.Builder().url(base.newBuilder().addPathSegments("v1/pair").build()).post(body).build()
            try {
                client.newCall(request).execute().use { response ->
                    val text = response.body.string()
                    if (!response.isSuccessful) {
                        val error = runCatching { RelayJson.decodeFromString<ErrorResponse>(text).error.message }.getOrNull()
                        throw PairingException(error ?: "The computer answered with HTTP ${response.code}.")
                    }
                    val paired = runCatching { RelayJson.decodeFromString<PairResponse>(text) }.getOrElse {
                        throw PairingException("${base.host} answered, but not like muxer. Check the address.")
                    }
                    base.toString().trimEnd('/') to paired
                }
            } catch (error: IOException) {
                throw PairingException(
                    "Couldn't reach ${base.host}. Check that this phone is on the network that reaches the computer, such as Tailscale or your VPN, and that the relay is running.",
                )
            }
        }

    companion object {
        private val JSON = "application/json".toMediaType()
        private val LOCAL_DEV_HOSTS = setOf("10.0.2.2", "127.0.0.1", "localhost")

        fun normalizeRelayUrl(address: String): HttpUrl? {
            val trimmed = address.trim()
            if (trimmed.isEmpty()) return null
            val withScheme = if ("://" in trimmed) trimmed else "https://$trimmed"
            val url = withScheme.toHttpUrlOrNull() ?: return null
            if (!url.isHttps && url.host !in LOCAL_DEV_HOSTS) return null
            return url.newBuilder().encodedPath("/").query(null).fragment(null).build()
        }
    }
}
