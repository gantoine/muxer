package com.gantoine.muxer.notifications

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import androidx.lifecycle.LifecycleService
import androidx.lifecycle.lifecycleScope
import com.gantoine.muxer.MainActivity
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.R
import com.gantoine.muxer.notifications.LockScreenText.hiddenOnLockScreen
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.launch
import java.time.Instant

class ConnectionService : LifecycleService() {
    private val app get() = application as MuxerApplication

    override fun onCreate() {
        super.onCreate()
        startInForeground(TimedSummary(ActivitySummary(), Instant.now()), app.lock.requiresUnlockForActions)
        lifecycleScope.launch {
            combine(ActivityFeed(app.store, app.connections).summaries(), app.lock.settings) { timed, lock -> timed to (lock?.enabled ?: true) }
                .collect { (timed, hidden) -> startInForeground(timed, hidden) }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        super.onStartCommand(intent, flags, startId)
        return START_STICKY
    }

    private fun startInForeground(timed: TimedSummary, hidden: Boolean) {
        val (summary, now) = timed
        val lines = LiveActivities.lines(summary, now)
        val builder = NotificationCompat.Builder(this, Notifier.ACTIVITY_CHANNEL)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(LiveActivities.title(summary))
            .setOngoing(true)
            .setSilent(true)
            .setOnlyAlertOnce(true)
            .setContentIntent(contentIntent(summary))
            .hiddenOnLockScreen(this, hidden, Notifier.ACTIVITY_CHANNEL, LockScreenText.activity(summary))
        if (lines.isEmpty()) {
            builder.setContentText("Watching for terminal notifications")
        } else {
            builder
                .setContentText(lines.first())
                .setStyle(NotificationCompat.InboxStyle().also { style -> lines.forEach { style.addLine(it) } })
                .setCategory(NotificationCompat.CATEGORY_PROGRESS)
                .setRequestPromotedOngoing(true)
            LiveActivities.chipText(summary, now)?.let { builder.setShortCriticalText(it) }
            LiveActivities.chronometer(summary)?.let { chronometer ->
                builder.setWhen(chronometer.at).setShowWhen(true).setUsesChronometer(true).setChronometerCountDown(chronometer.countDown)
            }
        }
        val type = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE else 0
        ServiceCompat.startForeground(this, NOTIFICATION_ID, builder.build(), type)
    }

    private fun contentIntent(summary: ActivitySummary): PendingIntent {
        val only = summary.activities.singleOrNull()
        if (only != null) return app.notifier.openTerminalIntent(only.macId, only.workspaceId, only.surfaceId, NOTIFICATION_ID)
        return PendingIntent.getActivity(this, NOTIFICATION_ID, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
    }

    companion object {
        private const val NOTIFICATION_ID = 1

        fun start(context: Context) {
            ContextCompat.startForegroundService(context, Intent(context, ConnectionService::class.java))
        }

        fun stop(context: Context) {
            context.stopService(Intent(context, ConnectionService::class.java))
        }
    }
}
