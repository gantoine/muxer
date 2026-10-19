package com.gantoine.muxer.notifications

import android.app.Notification
import android.content.Context
import androidx.core.app.NotificationCompat
import com.gantoine.muxer.R

object LockScreenText {
    const val TITLE = "Muxer"
    const val NEEDS_YOU = "Agent needs you"
    const val NEW_NOTIFICATION = "New notification"
    const val WORKING = "Agents are working"
    const val RUNNING = "Running in the background"

    fun activity(summary: ActivitySummary): String = when {
        summary.waiting.isNotEmpty() -> NEEDS_YOU
        summary.activities.isNotEmpty() -> WORKING
        else -> RUNNING
    }

    fun publicVersion(context: Context, channel: String, text: String): Notification =
        NotificationCompat.Builder(context, channel)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(TITLE)
            .setContentText(text)
            .build()

    fun NotificationCompat.Builder.hiddenOnLockScreen(context: Context, hidden: Boolean, channel: String, text: String): NotificationCompat.Builder =
        if (hidden) setVisibility(NotificationCompat.VISIBILITY_PRIVATE).setPublicVersion(publicVersion(context, channel, text)) else this
}
