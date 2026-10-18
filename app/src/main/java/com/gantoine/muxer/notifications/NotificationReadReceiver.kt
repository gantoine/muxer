package com.gantoine.muxer.notifications

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.lock.DeviceAuth
import kotlinx.coroutines.launch

class NotificationReadReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val app = context.applicationContext as MuxerApplication
        val macId = intent.getStringExtra(Notifier.EXTRA_MAC_ID) ?: return
        val notificationId = intent.getStringExtra(EXTRA_NOTIFICATION_ID)
        val surfaceId = intent.getStringExtra(Notifier.EXTRA_SURFACE_ID)
        if (notificationId == null && surfaceId == null) return
        val deviceLocked = DeviceAuth.isDeviceLocked(context)
        val pending = goAsync()
        app.scope.launch {
            try {
                if (app.lock.blocksActions(deviceLocked)) return@launch
                app.connections.wakeAndAwait(macId, WAKE_MS)
                    ?.markNotificationsRead(notificationId = notificationId, surfaceId = surfaceId.takeIf { notificationId == null })
            } finally {
                pending.finish()
            }
        }
    }

    companion object {
        private const val ACTION_MARK_READ = "com.gantoine.muxer.MARK_NOTIFICATION_READ"
        private const val EXTRA_NOTIFICATION_ID = "notification_id"
        private const val WAKE_MS = 15_000L

        fun notificationIntent(context: Context, macId: String, notificationId: String): PendingIntent =
            pendingIntent(context, "read:$notificationId".hashCode()) {
                putExtra(Notifier.EXTRA_MAC_ID, macId)
                putExtra(EXTRA_NOTIFICATION_ID, notificationId)
            }

        fun terminalIntent(context: Context, macId: String, surfaceId: String): PendingIntent =
            pendingIntent(context, "read:$macId:$surfaceId".hashCode()) {
                putExtra(Notifier.EXTRA_MAC_ID, macId)
                putExtra(Notifier.EXTRA_SURFACE_ID, surfaceId)
            }

        private fun pendingIntent(context: Context, requestCode: Int, extras: Intent.() -> Unit): PendingIntent =
            PendingIntent.getBroadcast(
                context,
                requestCode,
                Intent(context, NotificationReadReceiver::class.java).apply {
                    action = ACTION_MARK_READ
                    extras()
                },
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )
    }
}
