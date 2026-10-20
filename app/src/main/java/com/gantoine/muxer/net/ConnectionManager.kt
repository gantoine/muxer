package com.gantoine.muxer.net

import com.gantoine.muxer.data.Mac
import com.gantoine.muxer.data.MacSnapshot
import com.gantoine.muxer.data.MacStore
import com.gantoine.muxer.data.SnapshotStore
import com.gantoine.muxer.notifications.ConversationTarget
import com.gantoine.muxer.notifications.NotificationFilter
import com.gantoine.muxer.protocol.Approval
import com.gantoine.muxer.protocol.RelayNotification
import com.gantoine.muxer.protocol.Retry
import com.gantoine.muxer.protocol.Welcome
import com.gantoine.muxer.protocol.WorkspaceInfo
import com.gantoine.muxer.ui.PostHogCloud
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.FlowPreview
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.sample
import kotlinx.coroutines.flow.transformLatest
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import okhttp3.OkHttpClient

data class VisibleTerminal(val macId: String, val surfaceId: String)

class ConnectionManager(
    private val store: MacStore,
    private val snapshots: SnapshotStore,
    private val client: OkHttpClient,
    private val scope: CoroutineScope,
    private val notify: (Mac, RelayNotification) -> Unit,
    private val dismiss: (RelayNotification) -> Unit,
    private val notifyApproval: (Mac, Approval) -> Unit,
    private val dismissApproval: (String) -> Unit,
    private val widgetsPlaced: () -> Boolean,
) {
    private val connections = mutableMapOf<String, RelayConnection>()
    private val watchers = mutableMapOf<String, Job>()
    private val shownBySurface = mutableMapOf<Pair<String, String>, List<RelayNotification>>()
    private val _connectionsFlow = MutableStateFlow<Map<String, RelayConnection>>(emptyMap())
    val connectionsFlow: StateFlow<Map<String, RelayConnection>> = _connectionsFlow.asStateFlow()

    private val appVisible = MutableStateFlow(false)
    val visibleTerminal = MutableStateFlow<VisibleTerminal?>(null)

    @OptIn(ExperimentalCoroutinesApi::class)
    val connectedMacIds: Flow<Set<String>> = connectionsFlow.flatMapLatest { connections ->
        if (connections.isEmpty()) {
            flowOf(emptySet())
        } else {
            combine(connections.map { (macId, connection) -> connection.state.map { macId to (it is ConnectionState.Connected) } }) { states ->
                states.filter { it.second }.map { it.first }.toSet()
            }
        }
    }.distinctUntilChanged()

    private val awake = MutableStateFlow<Set<String>>(emptySet())
    private val awakeUntil = mutableMapOf<String, Long>()
    private val sleepJobs = mutableMapOf<String, Job>()
    private var backgroundDetach: Job? = null

    fun start() {
        scope.launch {
            combine(store.macs, appVisible, awake) { macs, visible, awake -> Triple(macs, visible, awake) }
                .collect { (macs, visible, awake) -> reconcile(macs) { mac -> visible || ALL_MACS in awake || mac.id in awake } }
        }
    }

    fun setAppVisible(visible: Boolean) {
        appVisible.value = visible
        backgroundDetach?.cancel()
        backgroundDetach = null
        if (visible) {
            allConnections().forEach { it.resumeAttachment() }
        } else {
            backgroundDetach = scope.launch {
                delay(BACKGROUND_DETACH_MS)
                allConnections().forEach { it.suspendAttachment() }
            }
        }
    }

    private fun allConnections(): List<RelayConnection> = synchronized(connections) { connections.values.toList() }

    fun wake(durationMs: Long, macId: String = ALL_MACS) {
        synchronized(awakeUntil) {
            val until = maxOf(awakeUntil[macId] ?: 0L, System.currentTimeMillis() + durationMs)
            awakeUntil[macId] = until
            awake.update { it + macId }
            sleepJobs.remove(macId)?.cancel()
            sleepJobs[macId] = scope.launch {
                delay(until - System.currentTimeMillis())
                awake.update { it - macId }
            }
        }
    }

    suspend fun wakeAndAwait(macId: String, wakeMs: Long, timeoutMs: Long = CONNECT_TIMEOUT_MS): RelayConnection? {
        wake(wakeMs, macId)
        return awaitConnected(macId, timeoutMs)
    }

    fun connection(macId: String): RelayConnection? = synchronized(connections) { connections[macId] }

    suspend fun awaitConnected(macId: String, timeoutMs: Long): RelayConnection? = withTimeoutOrNull(timeoutMs) {
        val connection = connectionsFlow.map { it[macId] }.filterNotNull().first()
        connection.state.first { it is ConnectionState.Connected }
        connection
    }

    suspend fun sendAgentMessage(target: ConversationTarget, text: String): Result<Unit> {
        val shared = connection(target.macId) ?: return Result.failure(IllegalStateException("This computer is not paired anymore."))
        if (shared.state.value is ConnectionState.Connected && shared.isAttachedTo(target.surfaceId)) return shared.deliverToAgent(text)
        return withSeparateConnection(target.macId) { separate ->
            val attachment = separate.attach(target.workspaceId, target.surfaceId)
            separate.deliverToAgent(text).also { separate.detach(attachment) }
        }
    }

    private suspend fun <T> withSeparateConnection(macId: String, action: suspend (RelayConnection) -> Result<T>): Result<T> {
        val mac = store.find(macId)
            ?: return Result.failure(IllegalStateException("This computer is not paired anymore."))
        val token = store.token(mac) ?: return Result.failure(IllegalStateException("Pair ${mac.name} again."))
        val separate = RelayConnection(
            macId = mac.id,
            baseUrl = mac.url,
            token = token,
            client = client,
            scope = scope,
            notificationsAfter = { store.notificationSeq(mac.id) },
            onNotification = { _, _ -> },
        )
        return try {
            separate.start()
            val state = withTimeoutOrNull(SEPARATE_CONNECT_TIMEOUT_MS) {
                separate.state.first { it is ConnectionState.Connected || it is ConnectionState.Unauthorized }
            }
            if (state is ConnectionState.Connected) action(separate) else Result.failure(IllegalStateException("Couldn't reach ${mac.name}."))
        } finally {
            separate.stop()
        }
    }

    private fun reconcile(macs: List<Mac>, wanted: (Mac) -> Boolean) {
        synchronized(connections) {
            val ids = macs.map { it.id }.toSet()
            connections.keys.filterNot { it in ids }.forEach { macId ->
                connections.remove(macId)?.stop()
                watchers.remove(macId)?.cancel()
                scope.launch { snapshots.remove(macId) }
            }
            for (mac in macs) {
                val existing = connections[mac.id]
                if (existing == null) {
                    val token = store.token(mac) ?: continue
                    connections[mac.id] = RelayConnection(
                        macId = mac.id,
                        baseUrl = mac.url,
                        token = token,
                        client = client,
                        scope = scope,
                        notificationsAfter = { store.notificationSeq(mac.id) },
                        onNotification = ::handleNotification,
                        onWelcome = ::handleWelcome,
                    ).also { connection ->
                        watchers[mac.id] = scope.launch {
                            launch { watchApprovals(mac.id, connection) }
                            launch { watchRetries(mac.id, connection) }
                            launch { writeSnapshots(mac.id, connection) }
                        }
                    }
                }
            }
            for (mac in macs) {
                val connection = connections[mac.id] ?: continue
                if (wanted(mac) || mac.backgroundNotifications) connection.start() else connection.stop()
            }
            _connectionsFlow.value = connections.toMap()
        }
    }

    private fun isLookingAt(macId: String, surfaceId: String?): Boolean {
        val visible = visibleTerminal.value
        return appVisible.value && visible?.macId == macId && visible.surfaceId == surfaceId
    }

    private suspend fun watchApprovals(macId: String, connection: RelayConnection) {
        val alerts = ApprovalAlerts(
            alert = { fresh ->
                val mac = store.find(macId)
                if (mac != null) fresh.filterNot { isLookingAt(macId, it.surfaceId) }.forEach { notifyApproval(mac, it) }
            },
            dismiss = dismissApproval,
        )
        alerts.follow(connection.approvals)
    }

    @OptIn(FlowPreview::class, ExperimentalCoroutinesApi::class)
    private suspend fun writeSnapshots(macId: String, connection: RelayConnection) {
        var lastWritten: SnapshotInputs? = null
        var lastWrittenAt = 0L
        combine(
            connection.state,
            connection.workspaces,
            connection.approvals,
            connection.retries,
            connection.busySurfaces,
        ) { state, workspaces, approvals, retries, busy ->
            SnapshotInputs(state is ConnectionState.Connected, workspaces, approvals, retries, busy)
        }
            .flatMapLatest { inputs -> repeatedWhileConnected(inputs) }
            .sample(SNAPSHOT_INTERVAL_MS)
            .transformLatest { inputs ->
                val written = lastWritten
                if (written != null && written.copy(workspaces = inputs.workspaces) == inputs) {
                    delay(WORKSPACE_SNAPSHOT_INTERVAL_MS - (System.currentTimeMillis() - lastWrittenAt))
                }
                emit(inputs)
            }
            .collect { inputs ->
                lastWritten = inputs
                lastWrittenAt = System.currentTimeMillis()
                val mac = store.find(macId) ?: return@collect
                if (!inputs.connected) {
                    snapshots.markDisconnected(macId)
                    return@collect
                }
                snapshots.write(
                    MacSnapshot(
                        macId = macId,
                        macName = mac.name,
                        connected = true,
                        updatedAt = System.currentTimeMillis(),
                        workspaces = inputs.workspaces.orEmpty(),
                        approvals = inputs.approvals.orEmpty(),
                        retries = inputs.retries,
                        busySurfaces = inputs.busy,
                    ),
                )
            }
    }

    private fun repeatedWhileConnected(inputs: SnapshotInputs) = flow {
        emit(inputs)
        while (inputs.connected && widgetsPlaced()) {
            delay(SNAPSHOT_HEARTBEAT_MS)
            emit(inputs)
        }
    }

    private data class SnapshotInputs(
        val connected: Boolean,
        val workspaces: List<WorkspaceInfo>?,
        val approvals: List<Approval>?,
        val retries: List<Retry>,
        val busy: Set<String>,
    )

    private suspend fun watchRetries(macId: String, connection: RelayConnection) {
        connection.retries.collect { retries ->
            retries.forEach { retry -> dismissShown(macId, retry.surfaceId) }
        }
    }

    private fun dismissShown(macId: String, surfaceId: String) {
        val shown = synchronized(shownBySurface) { shownBySurface.remove(macId to surfaceId) }.orEmpty()
        shown.forEach(dismiss)
    }

    private fun recordShown(macId: String, notification: RelayNotification) {
        val surfaceId = notification.surfaceId ?: return
        synchronized(shownBySurface) {
            val key = macId to surfaceId
            shownBySurface[key] = (shownBySurface[key].orEmpty() + notification).takeLast(MAX_TRACKED_PER_TERMINAL)
        }
    }

    private fun forgetShown(macId: String, notification: RelayNotification) {
        synchronized(shownBySurface) {
            shownBySurface.keys.filter { it.first == macId }.forEach { key ->
                val remaining = shownBySurface[key].orEmpty().filterNot { it.id == notification.id }
                if (remaining.isEmpty()) shownBySurface.remove(key) else shownBySurface[key] = remaining
            }
        }
    }

    private suspend fun handleWelcome(macId: String, welcome: Welcome) {
        if (store.find(macId) == null) return
        if (store.notificationSeq(macId) == 0L) store.recordNotificationSeq(macId, welcome.notificationSeq)
    }

    private suspend fun handleNotification(macId: String, notification: RelayNotification) {
        val mac = store.find(macId) ?: return
        if (notification.seq <= store.notificationSeq(macId)) return
        store.recordNotificationSeq(macId, notification.seq)
        when (notification.kind) {
            "created" -> {
                val connection = connection(macId)
                val answerableHere = connection?.approvals?.value.orEmpty().any { it.surfaceId == notification.surfaceId }
                val retrying = connection?.retries?.value.orEmpty().any { it.surfaceId == notification.surfaceId }
                val wanted = NotificationFilter.shouldShow(mac, notification)
                val shownOn = notification.surfaceId?.takeIf { it.isNotBlank() } ?: notification.posthogRunId?.let(PostHogCloud::visibleKey)
                if (wanted && !isLookingAt(macId, shownOn) && !answerableHere && !retrying) {
                    notify(mac, notification)
                    recordShown(macId, notification)
                }
            }
            "dismissed" -> {
                dismiss(notification)
                forgetShown(macId, notification)
            }
        }
    }

    private companion object {
        const val MAX_TRACKED_PER_TERMINAL = 10
        const val SNAPSHOT_INTERVAL_MS = 500L
        const val WORKSPACE_SNAPSHOT_INTERVAL_MS = 5_000L
        const val SNAPSHOT_HEARTBEAT_MS = 60_000L
        const val SEPARATE_CONNECT_TIMEOUT_MS = 6_000L
        const val BACKGROUND_DETACH_MS = 30_000L
        const val CONNECT_TIMEOUT_MS = 8_000L
        const val ALL_MACS = "*"
    }
}
