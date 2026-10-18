package com.gantoine.muxer.ui

import androidx.activity.compose.LocalActivity
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.RadioButtonChecked
import androidx.compose.material.icons.filled.RadioButtonUnchecked
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.fragment.app.FragmentActivity
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.lock.AppLock
import com.gantoine.muxer.lock.DeviceAuth
import com.gantoine.muxer.lock.LockSettings
import kotlinx.coroutines.launch

@Composable
fun AppSettingsScreen(app: MuxerApplication, onBack: () -> Unit) {
    val settings by app.store.lockSettings.collectAsState(initial = null)
    val activity = LocalActivity.current as? FragmentActivity
    val context = LocalContext.current
    val canAuthenticate = remember { DeviceAuth.canAuthenticate(context) }
    val scope = rememberCoroutineScope()
    var choosingTimeout by remember { mutableStateOf(false) }

    fun setLock(enabled: Boolean) {
        if (!enabled && !DeviceAuth.canAuthenticate(context)) {
            scope.launch { app.store.setAppLock(false) }
            return
        }
        activity ?: return
        DeviceAuth.authenticate(
            activity,
            title = if (enabled) "Turn on app lock" else "Turn off app lock",
            subtitle = "Confirm it's you",
        ) {
            scope.launch { app.store.setAppLock(enabled) }
        }
    }

    Scaffold(topBar = { CmuxTopBar(title = "Settings", onBack = onBack) }) { padding ->
        val current = settings ?: return@Scaffold
        Column(Modifier.padding(padding).fillMaxSize().verticalScroll(rememberScrollState()).padding(bottom = 32.dp)) {
            SectionLabel("Security")
            SettingsGroup {
                val rows = if (current.enabled) 2 else 1
                SwitchSettingsRow(
                    index = 0,
                    count = rows,
                    headline = "Lock Muxer",
                    supporting = if (canAuthenticate || current.enabled) {
                        "Ask for your fingerprint, face or screen lock to open the app and to answer from notifications."
                    } else {
                        "Set up a screen lock on this phone to use this."
                    },
                    checked = current.enabled,
                    enabled = canAuthenticate || current.enabled,
                    onCheckedChange = ::setLock,
                )
                if (current.enabled) {
                    SettingsRow(
                        index = 1,
                        count = rows,
                        headline = "Lock when away",
                        supporting = AppLock.describeTimeout(current.timeoutMs),
                        onClick = { choosingTimeout = true },
                    )
                }
            }
        }
    }

    val current = settings
    if (choosingTimeout && current != null) {
        TimeoutSheet(current, onDismiss = { choosingTimeout = false }) { timeoutMs ->
            choosingTimeout = false
            scope.launch { app.store.setAppLockTimeout(timeoutMs) }
        }
    }
}

@Composable
private fun TimeoutSheet(settings: LockSettings, onDismiss: () -> Unit, onChoose: (Long) -> Unit) {
    ActionSheet(
        onDismiss = onDismiss,
        header = { Text("Lock after leaving the app", style = MaterialTheme.typography.titleMedium) },
    ) {
        AppLock.TIMEOUT_CHOICES_MS.forEach { timeoutMs ->
            val icon = if (timeoutMs == settings.timeoutMs) Icons.Filled.RadioButtonChecked else Icons.Filled.RadioButtonUnchecked
            SheetAction(icon, AppLock.describeTimeout(timeoutMs)) { onChoose(timeoutMs) }
        }
    }
}
