package com.gantoine.muxer

import android.content.Context
import android.content.Intent
import android.os.Bundle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.fragment.app.FragmentActivity
import com.gantoine.muxer.notifications.ConversationTarget
import com.gantoine.muxer.notifications.conversationTarget
import com.gantoine.muxer.notifications.putTarget
import com.gantoine.muxer.ui.CmuxTheme
import com.gantoine.muxer.ui.LockGate
import com.gantoine.muxer.ui.TerminalScreen
import com.gantoine.muxer.ui.hideFromRecentsWhileLockIsOn

class BubbleActivity : FragmentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        val app = application as MuxerApplication
        val target = intent.conversationTarget()
        if (target == null) {
            finish()
            return
        }
        hideFromRecentsWhileLockIsOn(app)
        setContent {
            CmuxTheme {
                LockGate(app) {
                    TerminalScreen(app, target.macId, target.workspaceId, target.surfaceId, onBack = ::finish)
                }
            }
        }
    }

    companion object {
        private const val ACTION_BUBBLE = "com.gantoine.muxer.BUBBLE"

        fun intent(context: Context, target: ConversationTarget): Intent =
            Intent(context, BubbleActivity::class.java).setAction(ACTION_BUBBLE).putTarget(target)
    }
}
