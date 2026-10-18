package com.gantoine.muxer.ui

import android.Manifest
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.outlined.DriveFileRenameOutline
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExperimentalMaterial3ExpressiveApi
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.net.ConnectionState
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class, ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun ComputerSettingsScreen(
    app: MuxerApplication,
    macId: String,
    onBack: () -> Unit,
    onOpenMetrics: (macId: String) -> Unit,
    onOpenSessions: (macId: String) -> Unit,
    onRemoved: () -> Unit,
) {
    val macs by app.store.macs.collectAsState(initial = null)
    val mac = macs?.firstOrNull { it.id == macId }
    val connection = rememberConnection(app, macId)
    val state by connection.collectOr(ConnectionState.Stopped) { it.state }
    val keepAwake by connection.collectOr(null) { it.keepAwake }
    var changingKeepAwake by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }
    var renaming by remember { mutableStateOf(false) }
    var removing by remember { mutableStateOf(false) }

    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        app.notificationPermissionChanged()
        if (granted) {
            scope.launch { app.store.setBackgroundNotifications(macId, true) }
        } else {
            scope.launch { snackbar.showSnackbar("Allow notifications for cmux in Android settings to get alerts.") }
        }
    }

    fun setBackground(enabled: Boolean) {
        if (enabled && Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !app.notifier.canNotify()) {
            permission.launch(Manifest.permission.POST_NOTIFICATIONS)
            return
        }
        scope.launch { app.store.setBackgroundNotifications(macId, enabled) }
    }

    fun setKeepAwake(enabled: Boolean) {
        val connection = connection ?: return
        changingKeepAwake = true
        scope.launch {
            connection.setKeepAwake(enabled).onFailure { snackbar.showSnackbar(it.message ?: "The computer didn't change its sleep setting.") }
            changingKeepAwake = false
        }
    }

    Scaffold(
        topBar = {
            CmuxTopBar(
                title = mac?.name ?: "Settings",
                onBack = onBack,
            )
        },
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        if (mac == null) {
            if (macs != null) FullWidthMessage("This computer is not paired anymore.", Modifier.padding(padding))
            return@Scaffold
        }
        Column(Modifier.padding(padding).fillMaxSize().verticalScroll(rememberScrollState()).padding(bottom = 32.dp)) {
            SectionLabel("Notifications")
            SettingsGroup {
                SwitchSettingsRow(
                    index = 0,
                    count = 3,
                    headline = "Stay connected in the background",
                    supporting = "Get alerts while the app is closed.",
                    checked = mac.backgroundNotifications,
                    onCheckedChange = ::setBackground,
                )
                SwitchSettingsRow(
                    index = 1,
                    count = 3,
                    headline = "Alert me when an agent finishes",
                    supporting = "Notify me with its last message.",
                    checked = mac.completionNotifications,
                    onCheckedChange = { enabled -> scope.launch { app.store.setCompletionNotifications(macId, enabled) } },
                )
                SwitchSettingsRow(
                    index = 2,
                    count = 3,
                    headline = "Show what agents are doing",
                    supporting = "List agents in the background notification.",
                    checked = mac.showActivity,
                    onCheckedChange = { enabled -> scope.launch { app.store.setShowActivity(macId, enabled) } },
                )
            }

            SectionLabel("Power")
            SettingsGroup {
                val connected = state is ConnectionState.Connected
                val awake = keepAwake
                SwitchSettingsRow(
                    index = 0,
                    count = 1,
                    headline = "Keep this computer awake",
                    supporting = when {
                        !connected -> "Connect to this computer to change this."
                        awake == null -> "Needs a newer relay and cmux on this computer."
                        else -> "Stop idle sleep while cmux is open."
                    },
                    checked = awake == true,
                    enabled = connected && awake != null && !changingKeepAwake,
                    onCheckedChange = ::setKeepAwake,
                )
            }

            SectionLabel("This computer")
            val welcome = (state as? ConnectionState.Connected)?.welcome
            val rows = 5 + if (welcome != null) 1 else 0
            SettingsGroup {
                SettingsRow(
                    index = 0,
                    count = rows,
                    headline = "Name",
                    supporting = mac.name,
                    onClick = { renaming = true },
                    trailing = {
                        Icon(
                            Icons.Outlined.DriveFileRenameOutline,
                            contentDescription = "Rename",
                            tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    },
                )
                SettingsRow(index = 1, count = rows, headline = "Address", supporting = mac.url)
                SettingsRow(
                    index = 2,
                    count = rows,
                    headline = "Status",
                    supporting = if (state is ConnectionState.Connected) "Connected" else describe(state),
                    supportingIcon = { StatusDot(state) },
                )
                SettingsRow(
                    index = 3,
                    count = rows,
                    headline = "Metrics",
                    supporting = "CPU and memory use",
                    onClick = { onOpenMetrics(macId) },
                    trailing = { Chevron() },
                )
                SettingsRow(
                    index = 4,
                    count = rows,
                    headline = "Recent sessions",
                    supporting = "Find and resume past agent sessions",
                    onClick = { onOpenSessions(macId) },
                    trailing = { Chevron() },
                )
                if (welcome != null) {
                    SettingsRow(
                        index = 5,
                        count = rows,
                        headline = "Versions",
                        supporting = "cmux ${welcome.cmuxVersion} · relay ${welcome.relayVersion}",
                    )
                }
            }

            Spacer(Modifier.height(24.dp))
            SettingsGroup {
                SettingsRow(
                    index = 0,
                    count = 1,
                    headline = "Remove this computer",
                    tint = MaterialTheme.colorScheme.error,
                    leading = { Icon(Icons.Filled.Delete, contentDescription = null, tint = MaterialTheme.colorScheme.error) },
                    onClick = { removing = true },
                )
            }
        }
    }

    if (renaming && mac != null) {
        TextInputDialog(
            title = "Rename computer",
            fields = listOf("Name" to mac.name),
            confirm = "Rename",
            onDismiss = { renaming = false },
            maxLength = MAX_NAME_LENGTH,
            canConfirm = { (name) -> name.isNotBlank() },
        ) { (name) ->
            renaming = false
            scope.launch { app.store.rename(mac.id, name) }
        }
    }

    if (removing && mac != null) RemoveComputerDialog(mac, onDismiss = { removing = false }) {
        removing = false
        scope.launch {
            app.store.remove(mac.id)
            onRemoved()
        }
    }
}

@Composable
fun SettingsGroup(content: @Composable () -> Unit) {
    Column(
        Modifier.fillMaxWidth().padding(horizontal = 16.dp),
        verticalArrangement = androidx.compose.foundation.layout.Arrangement.spacedBy(2.dp),
    ) { content() }
}

@Composable
fun SettingsRow(
    index: Int,
    count: Int,
    headline: String,
    supporting: String? = null,
    tint: Color = MaterialTheme.colorScheme.onSurface,
    supportingIcon: (@Composable () -> Unit)? = null,
    leading: (@Composable () -> Unit)? = null,
    trailing: (@Composable () -> Unit)? = null,
    onClick: (() -> Unit)? = null,
) {
    val base = Modifier
        .fillMaxWidth()
        .clip(groupedShape(index, count))
        .background(MaterialTheme.colorScheme.surfaceContainer)
    ListItem(
        headlineContent = { Text(headline, color = tint) },
        supportingContent = supporting?.let {
            {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    if (supportingIcon != null) {
                        supportingIcon()
                        Spacer(Modifier.width(8.dp))
                    }
                    Text(it, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        },
        leadingContent = leading,
        trailingContent = trailing,
        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
        modifier = if (onClick != null) base.clickable(onClick = onClick) else base,
    )
}

@Composable
fun SwitchSettingsRow(
    index: Int,
    count: Int,
    headline: String,
    supporting: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit,
    enabled: Boolean = true,
) {
    SettingsRow(
        index = index,
        count = count,
        headline = headline,
        supporting = supporting,
        onClick = if (enabled) ({ onCheckedChange(!checked) }) else null,
        trailing = { Switch(checked = checked, enabled = enabled, onCheckedChange = onCheckedChange) },
    )
}

@Composable
private fun Chevron() {
    Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
}

private const val MAX_NAME_LENGTH = 60
