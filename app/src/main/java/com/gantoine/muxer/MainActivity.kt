package com.gantoine.muxer

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.core.content.IntentCompat
import androidx.core.content.pm.ShortcutManagerCompat
import androidx.fragment.app.FragmentActivity
import com.gantoine.muxer.notifications.Notifier
import com.gantoine.muxer.notifications.conversationTarget
import com.gantoine.muxer.share.ShareRouting
import com.gantoine.muxer.ui.AppNavigation
import com.gantoine.muxer.ui.CmuxTheme
import com.gantoine.muxer.ui.LaunchTarget
import com.gantoine.muxer.ui.LockGate
import com.gantoine.muxer.ui.hideFromRecentsWhileLockIsOn

class MainActivity : FragmentActivity() {
    private var launchTarget by mutableStateOf<LaunchTarget?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        val app = application as MuxerApplication
        hideFromRecentsWhileLockIsOn(app)
        if (savedInstanceState == null) launchTarget = targetFrom(intent)
        setContent {
            CmuxTheme {
                LockGate(app) {
                    AppNavigation(app, launchTarget, onLaunchTargetHandled = { launchTarget = null })
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        targetFrom(intent)?.let { launchTarget = it }
    }

    private fun targetFrom(intent: Intent?): LaunchTarget? {
        intent ?: return null
        if (intent.action == Intent.ACTION_VIEW) PairLink.parse(intent.data)?.let { return it }
        if (intent.action == ACTION_PAIR) return LaunchTarget.PairComputer
        if (intent.action == Intent.ACTION_SEND || intent.action == Intent.ACTION_SEND_MULTIPLE) return shareTarget(intent)
        if (intent.action == Notifier.ACTION_OPEN_TERMINAL && intent.component?.className == Notifier.OPEN_TERMINAL_ALIAS) {
            val target = intent.conversationTarget() ?: return null
            return LaunchTarget.Terminal(target.macId, target.workspaceId, target.surfaceId)
        }
        return null
    }

    private fun shareTarget(intent: Intent): LaunchTarget? {
        val shortcutId = intent.getStringExtra(ShortcutManagerCompat.EXTRA_SHORTCUT_ID)
        if (ShareRouting.isImageShare(intent.action, intent.type)) {
            val images = sharedImages(intent).takeIf { it.isNotEmpty() } ?: return null
            return LaunchTarget.Share(ShareRouting.caption(intent.getCharSequenceExtra(Intent.EXTRA_TEXT)), shortcutId, images)
        }
        val text = ShareRouting.sharedText(intent.action, intent.type, intent.getCharSequenceExtra(Intent.EXTRA_TEXT))
            ?: return null
        return LaunchTarget.Share(text, shortcutId)
    }

    private fun sharedImages(intent: Intent): List<Uri> =
        if (intent.action == Intent.ACTION_SEND_MULTIPLE) {
            IntentCompat.getParcelableArrayListExtra(intent, Intent.EXTRA_STREAM, Uri::class.java).orEmpty()
        } else {
            listOfNotNull(IntentCompat.getParcelableExtra(intent, Intent.EXTRA_STREAM, Uri::class.java))
        }

    companion object {
        const val ACTION_PAIR = "com.gantoine.muxer.PAIR"
    }
}
