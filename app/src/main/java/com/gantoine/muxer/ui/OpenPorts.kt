package com.gantoine.muxer.ui

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.OpenInNew
import androidx.compose.material3.AssistChip
import androidx.compose.material3.AssistChipDefaults
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3ExpressiveApi
import androidx.compose.material3.Icon
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.net.RelayConnection
import com.gantoine.muxer.net.RelayException
import com.gantoine.muxer.protocol.PortLink
import kotlinx.coroutines.launch

sealed interface PortStep {
    data object Loading : PortStep
    data class Open(val url: String) : PortStep
    data class Confirm(val link: PortLink) : PortStep
    data class Exposing(val link: PortLink) : PortStep
    data class Failed(val message: String) : PortStep
}

object OpenPorts {
    fun afterLink(result: Result<PortLink>): PortStep = result.fold(
        onSuccess = { link ->
            when {
                !isWebLink(link.url) -> PortStep.Failed("The computer sent an address the phone can't open.")
                link.needsExpose -> PortStep.Confirm(link)
                else -> PortStep.Open(link.url)
            }
        },
        onFailure = { PortStep.Failed(failureMessage(it)) },
    )

    fun afterExpose(result: Result<PortLink>): PortStep = afterLink(result.map { it.copy(needsExpose = false) })

    fun isWebLink(url: String): Boolean = url.startsWith("https://") || url.startsWith("http://")

    fun failureMessage(error: Throwable): String = when ((error as? RelayException)?.code) {
        "unknown_type" -> "Update muxer on this computer to open ports from your phone."
        "tailscale_missing" -> "Opening ports needs Tailscale on this computer. Install it and sign in, then try again."
        "tailscale_offline" -> "Tailscale isn't running or signed in on this computer."
        else -> error.message ?: "Could not open that port."
    }

    fun exposeExplanation(link: PortLink): String =
        "Port ${link.port} only accepts connections from the computer itself. To open it, Muxer exposes it with tailscale serve at " +
            "${link.url}. Every device on your tailnet can then reach it, not just this phone. " +
            "You can stop exposing it in this computer's settings."
}

fun openInBrowser(context: Context, url: String): Boolean = try {
    context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addCategory(Intent.CATEGORY_BROWSABLE))
    true
} catch (_: ActivityNotFoundException) {
    false
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
fun PortChips(ports: List<Int>, onPort: (Int) -> Unit) {
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        ports.forEach { port ->
            AssistChip(
                onClick = { onPort(port) },
                label = { Text(port.toString()) },
                leadingIcon = {
                    Icon(
                        Icons.AutoMirrored.Filled.OpenInNew,
                        contentDescription = null,
                        modifier = Modifier.size(AssistChipDefaults.IconSize),
                    )
                },
            )
        }
    }
}

@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun PortSheet(connection: RelayConnection, port: Int, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var step by remember(port) { mutableStateOf<PortStep>(PortStep.Loading) }

    LaunchedEffect(port) { step = OpenPorts.afterLink(connection.portLink(port)) }

    LaunchedEffect(step) {
        val open = step as? PortStep.Open ?: return@LaunchedEffect
        if (openInBrowser(context, open.url)) onDismiss() else step = PortStep.Failed("No browser on this phone can open ${open.url}.")
    }

    ActionSheet(onDismiss = onDismiss) {
        Column(
            Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Text("Port $port", style = MaterialTheme.typography.titleLarge)
            when (val current = step) {
                PortStep.Loading, is PortStep.Open -> Working("Finding the address…")
                is PortStep.Exposing -> Working("Exposing port $port on your tailnet…")
                is PortStep.Failed -> {
                    Text(current.message, style = MaterialTheme.typography.bodyLarge)
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                        TextButton(onClick = onDismiss) { Text("Close") }
                    }
                }
                is PortStep.Confirm -> {
                    Text(OpenPorts.exposeExplanation(current.link), style = MaterialTheme.typography.bodyLarge)
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.End)) {
                        TextButton(onClick = onDismiss) { Text("Cancel") }
                        Button(onClick = {
                            step = PortStep.Exposing(current.link)
                            scope.launch { step = OpenPorts.afterExpose(connection.exposePort(port)) }
                        }) { Text("Expose and open") }
                    }
                }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
private fun Working(text: String) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
        LoadingIndicator(Modifier.size(40.dp))
        Text(text, style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}
