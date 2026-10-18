package com.gantoine.muxer

import com.gantoine.muxer.data.MacSnapshot
import com.gantoine.muxer.net.RelayConnection
import com.gantoine.muxer.protocol.RelayNotification
import com.gantoine.muxer.protocol.TerminalInfo
import com.gantoine.muxer.protocol.WorkspaceInfo
import com.gantoine.muxer.terminal.TerminalScreen
import kotlinx.coroutines.CoroutineScope
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient

fun workspace(id: String, lastActivity: Double?, vararg terminals: TerminalInfo, title: String = id, color: String? = "#112233") =
    WorkspaceInfo(id = id, title = title, customColor = color, lastActivityAt = lastActivity, terminals = terminals.toList())

fun shell(id: String) = TerminalInfo(id = id, title = "zsh")

fun claude(id: String) = TerminalInfo(id = id, title = "claude", agentSource = "claude")

fun TerminalInfo.lastUsed() = copy(isFocused = true)

fun snapshot(macId: String, vararg workspaces: WorkspaceInfo, connected: Boolean = true, name: String = "$macId's Mac") =
    MacSnapshot(macId = macId, macName = name, connected = connected, updatedAt = 0, workspaces = workspaces.toList())

fun TerminalScreen.text(row: Int): String {
    val builder = StringBuilder()
    for (span in lines.getOrNull(row).orEmpty()) {
        while (builder.length < span.column) builder.append(' ')
        builder.append(span.text)
    }
    return builder.toString().trimEnd()
}

object FakeRelay {
    fun welcome(requestId: String?, notificationSeq: Long? = null, unsupported: List<String> = emptyList()): String {
        val seq = notificationSeq?.let { ""","notification_seq":$it""" }.orEmpty()
        val missing = unsupported.joinToString(",", prefix = "[", postfix = "]") { "\"$it\"" }
        return """{"type":"result","id":"$requestId","ok":true,"data":{"mac_name":"Studio","relay_version":"1","protocol_version":1,"cmux_version":"0.65.1","device_id":"d1","unsupported":$missing$seq}}"""
    }

    fun connection(
        server: MockWebServer,
        scope: CoroutineScope,
        token: String = "secret-token",
        client: OkHttpClient = OkHttpClient(),
        notificationsAfter: Long = 0L,
        onNotification: suspend (String, RelayNotification) -> Unit = { _, _ -> },
    ) = RelayConnection(
        macId = "mac",
        baseUrl = server.url("/").toString().trimEnd('/'),
        token = token,
        client = client,
        scope = scope,
        notificationsAfter = { notificationsAfter },
        onNotification = onNotification,
    )
}
