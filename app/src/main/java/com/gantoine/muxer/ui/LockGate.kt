package com.gantoine.muxer.ui

import android.os.Build
import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.activity.compose.LocalActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.ui.Modifier
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.withResumed
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.lock.DeviceAuth
import kotlinx.coroutines.launch

@Composable
fun LockGate(app: MuxerApplication, content: @Composable () -> Unit) {
    val locked by app.lock.locked.collectAsState()
    val holder = rememberSaveableStateHolder()
    when (locked) {
        false -> holder.SaveableStateProvider(UNLOCKED_CONTENT) { content() }
        true -> LockScreen(app)
        null -> Box(Modifier.fillMaxSize().background(MaterialTheme.colorScheme.background))
    }
}

@Composable
private fun LockScreen(app: MuxerApplication) {
    val activity = LocalActivity.current as? FragmentActivity
    val lifecycle = LocalLifecycleOwner.current.lifecycle

    fun unlock() {
        if (activity == null) return
        if (!DeviceAuth.canAuthenticate(activity)) {
            app.lock.unlock()
            return
        }
        DeviceAuth.authenticate(activity, title = "Unlock Muxer", onSuccess = app.lock::unlock)
    }

    LaunchedEffect(Unit) { lifecycle.withResumed { unlock() } }

    Box(Modifier.fillMaxSize().background(MaterialTheme.colorScheme.background)) {
        EmptyState(
            icon = Icons.Outlined.Lock,
            title = "Muxer is locked",
            body = "Unlock to see your terminals.",
        ) {
            Button(onClick = ::unlock) { Text("Unlock") }
        }
    }
}

fun ComponentActivity.hideFromRecentsWhileLockIsOn(app: MuxerApplication) {
    lifecycleScope.launch {
        app.lock.settings.collect { settings ->
            val hidden = settings?.enabled ?: true
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                setRecentsScreenshotEnabled(!hidden)
            } else if (hidden) {
                window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
            } else {
                window.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
            }
        }
    }
}

private const val UNLOCKED_CONTENT = "unlocked"
