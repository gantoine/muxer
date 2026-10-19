package com.gantoine.muxer

import android.app.Activity
import android.app.Application
import android.os.Bundle
import android.os.SystemClock
import android.util.Log
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import com.gantoine.muxer.data.MacStore
import com.gantoine.muxer.data.SnapshotStore
import com.gantoine.muxer.lock.AppLock
import com.gantoine.muxer.net.ConnectionManager
import com.gantoine.muxer.net.PairingClient
import com.gantoine.muxer.notifications.ConnectionService
import com.gantoine.muxer.notifications.ConversationShortcuts
import com.gantoine.muxer.notifications.Notifier
import com.gantoine.muxer.tile.NotificationsTile
import com.gantoine.muxer.tile.NotificationsTileService
import com.gantoine.muxer.tile.TileStatus
import com.gantoine.muxer.widget.Widgets
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import okhttp3.OkHttpClient

class MuxerApplication : Application() {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    val httpClient: OkHttpClient by lazy {
        OkHttpClient.Builder()
            .connectTimeout(10, TimeUnit.SECONDS)
            .pingInterval(25, TimeUnit.SECONDS)
            .build()
    }

    val lock = AppLock(SystemClock::elapsedRealtime)
    val store: MacStore by lazy { MacStore(this, scope) }
    val snapshots: SnapshotStore by lazy { SnapshotStore(this, scope) }
    val shortcuts: ConversationShortcuts by lazy { ConversationShortcuts(this) }
    val notifier: Notifier by lazy { Notifier(this, shortcuts, requireUnlock = { lock.requiresUnlockForActions }) }
    val pairing: PairingClient by lazy { PairingClient(httpClient) }

    val connections: ConnectionManager by lazy {
        ConnectionManager(
            store = store,
            snapshots = snapshots,
            client = httpClient,
            scope = scope,
            notify = notifier::show,
            dismiss = notifier::dismiss,
            notifyApproval = notifier::showApproval,
            dismissApproval = notifier::dismissApproval,
            widgetsPlaced = { Widgets.anyPlaced(this) },
        )
    }

    val tileStatus: Flow<TileStatus> by lazy {
        combine(store.macs, connections.connectedMacIds) { macs, connected -> NotificationsTile.status(macs, connected) }
            .distinctUntilChanged()
    }

    private val notificationPermission = MutableStateFlow(false)

    override fun onCreate() {
        super.onCreate()
        notifier.createChannels()
        notificationPermission.value = notifier.canNotify()
        registerActivityLifecycleCallbacks(LockTracker(lock))
        scope.launch { store.lockSettings.collect(lock::apply) }
        scope.launch {
            store.migrate()
            connections.start()
            launch { keepQuickSettingsTileInSync() }
            launch { Widgets.keepFresh(this@MuxerApplication) }
            launch { shortcuts.keepInSync(snapshots.snapshots) }
            keepBackgroundServiceInSync()
        }
        ProcessLifecycleOwner.get().lifecycle.addObserver(object : DefaultLifecycleObserver {
            override fun onStart(owner: LifecycleOwner) {
                notificationPermission.value = notifier.canNotify()
                connections.setAppVisible(true)
            }

            override fun onStop(owner: LifecycleOwner) = connections.setAppVisible(false)
        })
    }

    fun notificationPermissionChanged() {
        notificationPermission.value = notifier.canNotify()
    }

    private suspend fun keepQuickSettingsTileInSync() {
        tileStatus.collect { NotificationsTileService.requestRefresh(this) }
    }

    private suspend fun keepBackgroundServiceInSync() {
        combine(store.macs, notificationPermission) { macs, allowed -> allowed && macs.any { it.backgroundNotifications } }
            .distinctUntilChanged()
            .collect { wanted ->
                runCatching { if (wanted) ConnectionService.start(this) else ConnectionService.stop(this) }
                    .onFailure { Log.w("cmux", "Could not change the background connection service", it) }
            }
    }
}

private class LockTracker(private val lock: AppLock) : Application.ActivityLifecycleCallbacks {
    override fun onActivityStarted(activity: Activity) = lock.activityStarted()

    override fun onActivityStopped(activity: Activity) = lock.activityStopped(activity.isChangingConfigurations)

    override fun onActivityCreated(activity: Activity, savedInstanceState: Bundle?) = Unit

    override fun onActivityResumed(activity: Activity) = Unit

    override fun onActivityPaused(activity: Activity) = Unit

    override fun onActivitySaveInstanceState(activity: Activity, outState: Bundle) = Unit

    override fun onActivityDestroyed(activity: Activity) = Unit
}
