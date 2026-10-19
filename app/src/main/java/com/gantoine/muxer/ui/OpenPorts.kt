package com.gantoine.muxer.ui

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Lan
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.net.ConnectionState
import com.gantoine.muxer.net.RelayConnection
import com.gantoine.muxer.net.RelayException
import com.gantoine.muxer.net.Tailnet
import com.gantoine.muxer.protocol.TerminalPortLink
import kotlinx.coroutines.launch
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

sealed interface PortRow {
    val port: Int

    data class Reachable(override val port: Int, val url: String, val served: Boolean) : PortRow
    data class Exposable(override val port: Int, val url: String, val exposed: Boolean) : PortRow
    data class Unavailable(override val port: Int, val message: String) : PortRow
}

object OpenPorts {
    const val REQUEST = "port_links"
    const val MENU_LABEL = "Open ports in Tailscale"

    fun available(relayUrl: String, supports: (String) -> Boolean): Boolean = Tailnet.reaches(relayUrl) && supports(REQUEST)

    fun row(link: TerminalPortLink): PortRow {
        val error = link.error
        return when {
            error != null -> PortRow.Unavailable(link.port, failureMessage(RelayException(error.code, error.message)))
            !isWebLink(link.url) -> PortRow.Unavailable(link.port, "The computer sent an address the phone can't open.")
            link.exposed || link.needsExpose -> PortRow.Exposable(link.port, link.url, exposed = link.exposed)
            else -> PortRow.Reachable(link.port, link.url, served = link.url.startsWith("https://"))
        }
    }

    fun host(links: List<TerminalPortLink>): String? = links.firstNotNullOfOrNull { it.url.toHttpUrlOrNull()?.host }

    fun explanation(host: String?): String =
        "Ports that only accept connections from the computer itself are exposed with tailscale serve at " +
            (host?.let { "https://$it:<port>" } ?: "the computer's Tailscale name") +
            ". Every device on your tailnet can then reach them, not just this phone."

    fun isWebLink(url: String): Boolean = url.startsWith("https://") || url.startsWith("http://")

    fun failureMessage(error: Throwable): String = when ((error as? RelayException)?.code) {
        "unknown_type" -> "Update muxer on this computer to open ports from your phone."
        "tailscale_missing" -> "Opening ports needs Tailscale on this computer. Install it and sign in, then try again."
        "tailscale_offline" -> "Tailscale isn't running or signed in on this computer."
        else -> error.message ?: "Could not open that port."
    }
}

val RelayConnection.opensPortsInTailscale: Boolean get() = OpenPorts.available(baseUrl, ::supports)

fun openInBrowser(context: Context, url: String): Boolean = try {
    context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addCategory(Intent.CATEGORY_BROWSABLE))
    true
} catch (_: ActivityNotFoundException) {
    false
}

@Composable
fun OpenPortsAction(onClick: () -> Unit) {
    SheetAction(Icons.Outlined.Lan, OpenPorts.MENU_LABEL, onClick = onClick)
}

@Composable
fun OpenPortsScreen(
    app: MuxerApplication,
    macId: String,
    workspaceId: String,
    surfaceId: String,
    onBack: () -> Unit,
) {
    val connection = rememberConnection(app, macId)
    val state by connection.collectOr(ConnectionState.Stopped) { it.state }
    val workspaces by connection.collectOr(null) { it.workspaces }
    val terminal = workspaces?.firstOrNull { it.id == workspaceId }?.terminals?.firstOrNull { it.id == surfaceId }
    val macName = rememberMacName(app, macId)
    val context = LocalContext.current
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    var links by remember { mutableStateOf<List<TerminalPortLink>?>(null) }
    var loadFailure by remember { mutableStateOf<Throwable?>(null) }
    var changing by remember { mutableStateOf(emptyMap<Int, Boolean>()) }
    val online = state is ConnectionState.Connected

    suspend fun load() {
        val target = connection?.takeIf { it.supports(OpenPorts.REQUEST) } ?: return
        target.terminalPortLinks(surfaceId)
            .onSuccess {
                links = it
                loadFailure = null
            }
            .onFailure { error ->
                val unavailable = error is RelayException && (error.isUnknownType || error.isUnsupported)
                loadFailure = if (unavailable) error else Exception(OpenPorts.failureMessage(error))
            }
    }

    LaunchedEffect(connection, online, terminal?.listeningPorts) {
        if (online) load()
    }

    fun setExposed(port: Int, exposed: Boolean) {
        val target = connection ?: return
        changing = changing + (port to exposed)
        scope.launch {
            val result = if (exposed) target.exposePort(port) else target.unexposePort(port)
            result.onFailure { snackbar.showSnackbar(OpenPorts.failureMessage(it)) }
            load()
            changing = changing - port
        }
    }

    fun open(url: String) {
        if (!openInBrowser(context, url)) scope.launch { snackbar.showSnackbar("No browser on this phone can open $url.") }
    }

    Scaffold(
        topBar = { CmuxTopBar(title = OpenPorts.MENU_LABEL, onBack = onBack) },
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            RemoteContent(
                value = links,
                failure = loadFailure,
                state = state,
                requestType = OpenPorts.REQUEST,
                icon = Icons.Outlined.Lan,
                what = "open ports",
                failedTitle = "Couldn't list the ports",
                loading = "Finding the ports…",
                macName = macName,
                onRetry = {
                    loadFailure = null
                    scope.launch { load() }
                },
            ) { shown ->
                Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(bottom = 32.dp)) {
                    ConnectionBanner(state)
                    Text(
                        OpenPorts.explanation(OpenPorts.host(shown)),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(start = 28.dp, end = 28.dp, top = 8.dp),
                    )
                    if (shown.isEmpty()) {
                        EmptyState(
                            icon = Icons.Outlined.Lan,
                            title = "No open ports",
                            body = "Nothing in ${terminal?.title?.ifBlank { null } ?: "this terminal"} listens on a port right now.",
                        )
                    } else {
                        SectionLabel(terminal?.title?.ifBlank { null } ?: "Listening ports")
                        SettingsGroup {
                            shown.map(OpenPorts::row).forEachIndexed { index, row ->
                                PortRowItem(
                                    row = row,
                                    index = index,
                                    count = shown.size,
                                    changingTo = changing[row.port],
                                    online = online,
                                    onOpen = ::open,
                                    onExposedChange = { setExposed(row.port, it) },
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun PortRowItem(
    row: PortRow,
    index: Int,
    count: Int,
    changingTo: Boolean?,
    online: Boolean,
    onOpen: (String) -> Unit,
    onExposedChange: (Boolean) -> Unit,
) {
    val headline = "Port ${row.port}"
    when (row) {
        is PortRow.Reachable -> SettingsRow(
            index = index,
            count = count,
            headline = headline,
            supporting = row.url + if (row.served) " · served by tailscale serve" else " · open on every interface",
            onClick = { onOpen(row.url) },
        )
        is PortRow.Exposable -> {
            val exposed = changingTo ?: row.exposed
            SettingsRow(
                index = index,
                count = count,
                headline = headline,
                supporting = when {
                    changingTo == true -> "Exposing on your tailnet…"
                    changingTo == false -> "Stopping…"
                    row.exposed -> "${row.url} · reachable from your whole tailnet"
                    else -> "Only reachable from the computer"
                },
                onClick = if (row.exposed && changingTo == null) ({ onOpen(row.url) }) else null,
                trailing = {
                    Switch(checked = exposed, enabled = online && changingTo == null, onCheckedChange = onExposedChange)
                },
            )
        }
        is PortRow.Unavailable -> SettingsRow(
            index = index,
            count = count,
            headline = headline,
            supporting = row.message,
        )
    }
}
