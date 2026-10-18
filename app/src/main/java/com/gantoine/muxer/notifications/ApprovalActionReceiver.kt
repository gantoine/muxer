package com.gantoine.muxer.notifications

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import androidx.core.app.RemoteInput
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.lock.DeviceAuth
import com.gantoine.muxer.model.ApprovalReply
import kotlinx.coroutines.launch

data class ApprovalTarget(val macId: String, val approvalId: String, val workspaceId: String, val surfaceId: String)

class ApprovalActionReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val app = context.applicationContext as MuxerApplication
        val target = ApprovalTarget(
            macId = intent.getStringExtra(Notifier.EXTRA_MAC_ID) ?: return,
            approvalId = intent.getStringExtra(EXTRA_APPROVAL_ID) ?: return,
            workspaceId = intent.getStringExtra(Notifier.EXTRA_WORKSPACE_ID).orEmpty(),
            surfaceId = intent.getStringExtra(Notifier.EXTRA_SURFACE_ID).orEmpty(),
        )
        val typed = intent.getBooleanExtra(EXTRA_TYPED, false)
        val answer = if (typed) typedAnswer(intent) ?: return else intent.getStringExtra(EXTRA_ANSWER)
        val reply = intent.getStringExtra(EXTRA_MODE)?.let { ApprovalReply.Decision(it) }
            ?: answer?.let { ApprovalReply.Answers(listOf(it)) }
            ?: return
        val deviceLocked = DeviceAuth.isDeviceLocked(context)
        val pending = goAsync()
        app.scope.launch {
            try {
                val result = if (app.lock.blocksActions(deviceLocked)) {
                    Result.failure(IllegalStateException("Unlock your phone to answer."))
                } else {
                    app.answerApproval(target, reply)
                }
                result.onFailure { error ->
                    val macName = app.store.find(target.macId)?.name ?: "your computer"
                    app.notifier.showApprovalFailed(macName, target, error.message ?: "The computer did not accept it.")
                }
            } finally {
                pending.finish()
            }
        }
    }

    private fun typedAnswer(intent: Intent): String? =
        RemoteInput.getResultsFromIntent(intent)?.getCharSequence(KEY_TYPED_ANSWER)?.toString()?.trim()?.takeIf { it.isNotEmpty() }

    companion object {
        const val KEY_TYPED_ANSWER = "typed_answer"
        private const val ACTION_ANSWER = "com.gantoine.muxer.ANSWER_APPROVAL"
        private const val ACTION_TYPED_ANSWER = "com.gantoine.muxer.TYPE_APPROVAL_ANSWER"
        private const val EXTRA_TYPED = "typed"
        private const val EXTRA_APPROVAL_ID = "approval_id"
        private const val EXTRA_MODE = "mode"
        private const val EXTRA_ANSWER = "answer"

        fun intent(context: Context, target: ApprovalTarget, mode: String? = null, answer: String? = null): PendingIntent {
            val requestCode = listOf(target.approvalId, mode, answer).hashCode()
            return PendingIntent.getBroadcast(
                context,
                requestCode,
                broadcastIntent(context, target, mode, answer),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )
        }

        fun broadcastIntent(context: Context, target: ApprovalTarget, mode: String? = null, answer: String? = null): Intent =
            targetIntent(context, target, ACTION_ANSWER)
                .putExtra(EXTRA_MODE, mode)
                .putExtra(EXTRA_ANSWER, answer)

        fun typedAnswerIntent(context: Context, target: ApprovalTarget): PendingIntent {
            val intent = targetIntent(context, target, ACTION_TYPED_ANSWER).putExtra(EXTRA_TYPED, true)
            val requestCode = listOf(target.approvalId, ACTION_TYPED_ANSWER).hashCode()
            return PendingIntent.getBroadcast(context, requestCode, intent, Notifier.mutableFlag() or PendingIntent.FLAG_UPDATE_CURRENT)
        }

        private fun targetIntent(context: Context, target: ApprovalTarget, action: String): Intent =
            Intent(context, ApprovalActionReceiver::class.java)
                .setAction(action)
                .putTarget(ConversationTarget(target.macId, target.workspaceId, target.surfaceId))
                .putExtra(EXTRA_APPROVAL_ID, target.approvalId)
    }
}
