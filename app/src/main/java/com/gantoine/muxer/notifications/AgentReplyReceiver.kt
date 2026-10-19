package com.gantoine.muxer.notifications

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import androidx.core.app.RemoteInput
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.lock.DeviceAuth
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

class AgentReplyReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val app = context.applicationContext as MuxerApplication
        val target = intent.conversationTarget() ?: return
        val agentName = intent.getStringExtra(EXTRA_AGENT_NAME) ?: "Agent"
        val text = RemoteInput.getResultsFromIntent(intent)?.getCharSequence(KEY_TEXT)?.toString()?.trim()
        if (text.isNullOrEmpty()) return
        val deviceLocked = DeviceAuth.isDeviceLocked(context)
        val pending = goAsync()
        val delivery = app.scope.launch {
            val result = if (app.lock.blocksActions(deviceLocked)) {
                Result.failure(IllegalStateException("Unlock your phone to send it."))
            } else {
                app.connections.sendAgentMessage(target, text)
            }
            val macName = app.store.find(target.macId)?.name ?: "your computer"
            val failure = result.exceptionOrNull()?.let { it.message ?: "The computer didn't accept it." }
            app.notifier.showReply(target, macName, agentName, text, failure)
        }
        app.scope.launch {
            withTimeoutOrNull(RECEIVER_BUDGET_MS) { delivery.join() }
            pending.finish()
        }
    }

    companion object {
        const val KEY_TEXT = "agent_message"
        private const val ACTION_MESSAGE = "com.gantoine.muxer.MESSAGE_AGENT"
        private const val EXTRA_AGENT_NAME = "agent_name"
        private const val RECEIVER_BUDGET_MS = 8_000L

        fun intent(context: Context, target: ConversationTarget, agentName: String): PendingIntent {
            val intent = Intent(context, AgentReplyReceiver::class.java)
                .setAction(ACTION_MESSAGE)
                .putTarget(target)
                .putExtra(EXTRA_AGENT_NAME, agentName)
            return PendingIntent.getBroadcast(context, target.key.hashCode(), intent, Notifier.mutableFlag() or PendingIntent.FLAG_UPDATE_CURRENT)
        }
    }
}
