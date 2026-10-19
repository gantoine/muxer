package com.gantoine.muxer.widget

import android.appwidget.AppWidgetManager
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import androidx.glance.appwidget.GlanceAppWidgetReceiver
import androidx.glance.appwidget.updateAll
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.gantoine.muxer.MainActivity
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.data.MacSnapshot
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.FlowPreview
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.debounce

data class WidgetInputs(val macIds: List<String>, val snapshots: Map<String, MacSnapshot>, val locked: Boolean = false) {
    val paired: List<MacSnapshot> get() = WidgetData.paired(snapshots, macIds)
}

object Widgets {
    const val WAKE_MS = 30_000L
    private const val UPDATE_DEBOUNCE_MS = 1_000L
    private const val PERIODIC_WORK = "widget-refresh"
    private const val REFRESH_NOW_WORK = "widget-refresh-now"
    private val receivers = listOf(
        ApprovalsWidgetReceiver::class.java,
        AgentBoardWidgetReceiver::class.java,
    )

    fun inputs(app: MuxerApplication): Flow<WidgetInputs> =
        combine(app.store.macs, app.snapshots.snapshots, app.lock.settings) { macs, snapshots, lock ->
            WidgetInputs(macs.map { it.id }, snapshots, locked = lock?.enabled ?: true)
        }

    @OptIn(FlowPreview::class)
    suspend fun keepFresh(app: MuxerApplication) {
        syncRefreshSchedule(app)
        combine(app.snapshots.snapshots, app.lock.settings) { snapshots, lock -> snapshots to lock }
            .debounce(UPDATE_DEBOUNCE_MS)
            .collect { updateAll(app) }
    }

    suspend fun updateAll(context: Context) {
        ApprovalsWidget().updateAll(context)
        AgentBoardWidget().updateAll(context)
    }

    fun refreshNow(context: Context) {
        (context.applicationContext as MuxerApplication).connections.wake(WAKE_MS)
        WorkManager.getInstance(context).enqueueUniqueWork(
            REFRESH_NOW_WORK,
            ExistingWorkPolicy.REPLACE,
            OneTimeWorkRequestBuilder<WidgetRefreshWorker>().build(),
        )
    }

    fun schedule(context: Context) {
        val request = PeriodicWorkRequestBuilder<WidgetRefreshWorker>(15, TimeUnit.MINUTES, 5, TimeUnit.MINUTES)
            .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
            .build()
        WorkManager.getInstance(context).enqueueUniquePeriodicWork(PERIODIC_WORK, ExistingPeriodicWorkPolicy.KEEP, request)
    }

    fun syncRefreshSchedule(context: Context) {
        if (anyPlaced(context)) schedule(context) else WorkManager.getInstance(context).cancelUniqueWork(PERIODIC_WORK)
    }

    fun openApp(context: Context): Intent =
        Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

    fun anyPlaced(context: Context): Boolean {
        val manager = AppWidgetManager.getInstance(context) ?: return false
        return receivers.any { manager.getAppWidgetIds(ComponentName(context, it)).isNotEmpty() }
    }
}

class WidgetRefreshWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val app = applicationContext as MuxerApplication
        if (app.store.current().isEmpty()) return Result.success()
        app.connections.wake(Widgets.WAKE_MS)
        delay(Widgets.WAKE_MS)
        return Result.success()
    }
}

abstract class MuxerWidgetReceiver : GlanceAppWidgetReceiver() {
    override fun onEnabled(context: Context) {
        super.onEnabled(context)
        Widgets.schedule(context)
    }

    override fun onDisabled(context: Context) {
        super.onDisabled(context)
        Widgets.syncRefreshSchedule(context)
    }
}
