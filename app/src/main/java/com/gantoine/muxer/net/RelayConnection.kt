package com.gantoine.muxer.net

import com.gantoine.muxer.chat.ChatModel
import com.gantoine.muxer.chat.ChatState
import com.gantoine.muxer.images.EncodedImage
import com.gantoine.muxer.model.ApprovalReply
import com.gantoine.muxer.protocol.AgentActivityMessage
import com.gantoine.muxer.protocol.AgentMessage
import com.gantoine.muxer.protocol.AgentMessagesMessage
import com.gantoine.muxer.protocol.Approval
import com.gantoine.muxer.protocol.AutomationLog
import com.gantoine.muxer.protocol.AutomationLogsResponse
import com.gantoine.muxer.protocol.AutomationsResponse
import com.gantoine.muxer.protocol.ApprovalsMessage
import com.gantoine.muxer.protocol.ChatFiles
import com.gantoine.muxer.protocol.ChatImageData
import com.gantoine.muxer.protocol.ChatMessage
import com.gantoine.muxer.protocol.ChatSessionMessage
import com.gantoine.muxer.protocol.Checklist
import com.gantoine.muxer.protocol.CloudVm
import com.gantoine.muxer.protocol.CloudVmsResponse
import com.gantoine.muxer.protocol.CreatedWorkspace
import com.gantoine.muxer.protocol.DetachedMessage
import com.gantoine.muxer.protocol.HistoryResponse
import com.gantoine.muxer.protocol.HostUsage
import com.gantoine.muxer.protocol.InputStateMessage
import com.gantoine.muxer.protocol.KeepAwakeMessage
import com.gantoine.muxer.protocol.MacNotification
import com.gantoine.muxer.protocol.NotificationMessage
import com.gantoine.muxer.protocol.NotificationsResponse
import com.gantoine.muxer.protocol.RecentSession
import com.gantoine.muxer.protocol.RecentSessionsResponse
import com.gantoine.muxer.protocol.RelayJson
import com.gantoine.muxer.protocol.RelayNotification
import com.gantoine.muxer.protocol.RequestError
import com.gantoine.muxer.protocol.ResumedSession
import com.gantoine.muxer.protocol.Retry
import com.gantoine.muxer.protocol.ScreenFrame
import com.gantoine.muxer.protocol.TopSummary
import com.gantoine.muxer.protocol.Welcome
import com.gantoine.muxer.protocol.WorkspaceGroupInfo
import com.gantoine.muxer.protocol.WorkspaceInfo
import com.gantoine.muxer.protocol.WorkspacesMessage
import com.gantoine.muxer.terminal.TerminalModel
import com.gantoine.muxer.terminal.TerminalScreen
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonObjectBuilder
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.add
import kotlinx.serialization.json.addJsonObject
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray
import kotlinx.serialization.serializer
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener

sealed interface ConnectionState {
    data object Connecting : ConnectionState
    data class Connected(val welcome: Welcome) : ConnectionState {
        fun supports(type: String): Boolean = type !in welcome.unsupported
    }
    data class Disconnected(val reason: String) : ConnectionState
    data object Unauthorized : ConnectionState
    data object Stopped : ConnectionState
}

class RelayException(val code: String, message: String) : Exception(message) {
    val isUnknownType: Boolean get() = code == "unknown_type"
    val isUnsupported: Boolean get() = code == "unsupported"
    val needsConfirmation: Boolean get() = code == "confirmation_required"
}

val Throwable.isUnavailable: Boolean get() = this is RelayException && (isUnknownType || isUnsupported)

const val NO_ATTACHMENT = -1L

data class Attachment(val id: Long, val workspaceId: String, val surfaceId: String, val fit: Pair<Int, Int>? = null)

class RelayConnection(
    private val macId: String,
    private val baseUrl: String,
    private val token: String,
    private val client: OkHttpClient,
    private val scope: CoroutineScope,
    private val notificationsAfter: suspend () -> Long,
    private val onNotification: suspend (String, RelayNotification) -> Unit,
    private val onWelcome: suspend (String, Welcome) -> Unit = { _, _ -> },
) {
    private val _state = MutableStateFlow<ConnectionState>(ConnectionState.Stopped)
    val state: StateFlow<ConnectionState> = _state.asStateFlow()

    private val _workspaces = MutableStateFlow<List<WorkspaceInfo>?>(null)
    val workspaces: StateFlow<List<WorkspaceInfo>?> = _workspaces.asStateFlow()

    private val _groups = MutableStateFlow<List<WorkspaceGroupInfo>>(emptyList())
    val groups: StateFlow<List<WorkspaceGroupInfo>> = _groups.asStateFlow()

    private val _screen = MutableStateFlow<TerminalScreen?>(null)
    val screen: StateFlow<TerminalScreen?> = _screen.asStateFlow()

    private val _chat = MutableStateFlow<ChatState?>(null)
    val chat: StateFlow<ChatState?> = _chat.asStateFlow()

    private val _approvals = MutableStateFlow<List<Approval>?>(null)
    val approvals: StateFlow<List<Approval>?> = _approvals.asStateFlow()

    private val _retries = MutableStateFlow<List<Retry>>(emptyList())
    val retries: StateFlow<List<Retry>> = _retries.asStateFlow()

    private val _busySurfaces = MutableStateFlow<Set<String>>(emptySet())
    val busySurfaces: StateFlow<Set<String>> = _busySurfaces.asStateFlow()

    private val _agentMessages = MutableStateFlow<List<AgentMessage>>(emptyList())
    val agentMessages: StateFlow<List<AgentMessage>> = _agentMessages.asStateFlow()

    private val _inputState = MutableStateFlow<InputStateMessage?>(null)
    val inputState: StateFlow<InputStateMessage?> = _inputState.asStateFlow()

    private val _keepAwake = MutableStateFlow<Boolean?>(null)
    val keepAwake: StateFlow<Boolean?> = _keepAwake.asStateFlow()

    private val _detached = MutableSharedFlow<DetachedMessage>(extraBufferCapacity = 8)
    val detached: SharedFlow<DetachedMessage> = _detached.asSharedFlow()

    private val pending = ConcurrentHashMap<String, CompletableDeferred<Result<JsonElement?>>>()
    private val nextId = AtomicLong()
    private val nextAttachmentId = AtomicLong()
    private val notifications = Channel<RelayNotification>(Channel.UNLIMITED)
    @Volatile private var socket: WebSocket? = null
    private var loop: Job? = null
    @Volatile private var attachment: Attachment? = null
    private val attachments = mutableListOf<Attachment>()
    @Volatile private var suspended = false

    fun supports(type: String): Boolean = (state.value as? ConnectionState.Connected)?.supports(type) ?: true

    fun start() {
        if (loop?.isActive == true) return
        loop = scope.launch {
            val delivery = launch { for (notification in notifications) onNotification(macId, notification) }
            try {
                runLoop()
            } finally {
                delivery.cancel()
            }
        }
    }

    fun stop() {
        loop?.cancel()
        loop = null
        socket?.close(1000, "closing")
        socket = null
        failPending("The connection closed.")
        clearLiveState()
        _state.value = ConnectionState.Stopped
    }

    private fun clearLiveState() {
        _approvals.value = null
        _retries.value = emptyList()
        _busySurfaces.value = emptySet()
        _keepAwake.value = null
    }

    private suspend fun runLoop() {
        var backoffMs = 1_000L
        while (true) {
            _state.value = ConnectionState.Connecting
            val closed = CompletableDeferred<CloseReason>()
            val request = Request.Builder()
                .url("$baseUrl/v1/ws")
                .header("Authorization", "Bearer $token")
                .build()
            val webSocket = client.newWebSocket(request, Listener(closed))
            socket = webSocket
            val reason = try {
                closed.await()
            } finally {
                webSocket.cancel()
                if (socket === webSocket) {
                    socket = null
                    failPending("The connection to the computer dropped.")
                }
            }
            if (reason.unauthorized) {
                _state.value = ConnectionState.Unauthorized
                return
            }
            if (reason.wasConnected) backoffMs = 1_000L
            clearLiveState()
            _state.value = ConnectionState.Disconnected(reason.message)
            delay(backoffMs)
            backoffMs = (backoffMs * 2).coerceAtMost(30_000L)
        }
    }

    private data class CloseReason(val message: String, val unauthorized: Boolean = false, val wasConnected: Boolean = false)

    private inner class Listener(private val closed: CompletableDeferred<CloseReason>) : WebSocketListener() {
        @Volatile private var opened = false
        @Volatile var welcomed = false
        @Volatile var rejection: String? = null

        override fun onOpen(webSocket: WebSocket, response: Response) {
            opened = true
            scope.launch { onOpened(webSocket, this@Listener) }
        }

        override fun onMessage(webSocket: WebSocket, text: String) {
            try {
                handleMessage(text)
            } catch (_: SerializationException) {
            }
        }

        override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
            val message = if (code == 1008) "This phone was removed on the computer. Pair it again." else rejection ?: "The computer closed the connection."
            closed.complete(CloseReason(message, unauthorized = code == 1008, wasConnected = welcomed))
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
            closed.complete(CloseReason(rejection ?: "The computer closed the connection.", wasConnected = welcomed))
        }

        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
            if (response?.code == 401) {
                closed.complete(CloseReason("This phone is no longer paired with the computer.", unauthorized = true))
                return
            }
            closed.complete(CloseReason(rejection ?: connectionFailureReason(t, opened), wasConnected = welcomed))
        }
    }

    private suspend fun onOpened(webSocket: WebSocket, listener: Listener) {
        val hello = request("hello", buildJsonObject { put("notifications_after", notificationsAfter()) })
        if (socket !== webSocket) return
        hello.onSuccess { data ->
            val welcome = data?.decode<Welcome>() ?: return
            listener.welcomed = true
            _state.value = ConnectionState.Connected(welcome)
            if (!suspended) attachment?.let { sendAttach(it) }
            onWelcome(macId, welcome)
        }.onFailure {
            val message = it.message ?: "The computer rejected the connection."
            listener.rejection = message
            _state.value = ConnectionState.Disconnected(message)
            webSocket.close(1000, "hello failed")
        }
    }

    private fun handleMessage(text: String) {
        val message = runCatching { RelayJson.parseToJsonElement(text).jsonObject }.getOrNull() ?: return
        when (message["type"]?.jsonPrimitive?.contentOrNull) {
            "result" -> completeRequest(message)
            "workspaces" -> {
                val listing = message.decode<WorkspacesMessage>()
                _groups.value = listing.groups
                _workspaces.value = listing.workspaces
            }
            "screen" -> {
                val frame = message.decode<ScreenFrame>()
                if (suspended || frame.surfaceId != attachment?.surfaceId) return
                val next = TerminalModel.apply(_screen.value, frame)
                if (next == null) send("resync") else _screen.value = next
            }
            "chat_session" -> {
                val session = message.decode<ChatSessionMessage>()
                if (session.surfaceId == attachment?.surfaceId) _chat.value = ChatModel.startSession(_chat.value, session.sessionId)
            }
            "chat" -> {
                val chat = message.decode<ChatMessage>()
                if (chat.surfaceId == attachment?.surfaceId) _chat.value = ChatModel.apply(_chat.value, chat)
            }
            "detached" -> {
                val detached = message.decode<DetachedMessage>()
                val previous = synchronized(attachments) {
                    attachments.removeAll { it.surfaceId == detached.surfaceId }
                    attachments.lastOrNull()
                }
                if (detached.surfaceId == attachment?.surfaceId) {
                    attachment = previous
                    suspended = false
                    clearTerminalState()
                    previous?.let { sendAttach(it) }
                }
                _detached.tryEmit(detached)
            }
            "approvals" -> _approvals.value = message.decode<ApprovalsMessage>().approvals
            "agent_activity" -> {
                val activity = message.decode<AgentActivityMessage>()
                _retries.value = activity.retries
                _busySurfaces.value = activity.busy.toSet()
            }
            "agent_messages" -> {
                val listing = message.decode<AgentMessagesMessage>()
                if (listing.surfaceId == attachment?.surfaceId) _agentMessages.value = listing.messages
            }
            "input_state" -> {
                val state = message.decode<InputStateMessage>()
                if (state.surfaceId == attachment?.surfaceId) _inputState.value = state
            }
            "keep_awake" -> _keepAwake.value = message.decode<KeepAwakeMessage>().enabled
            "notification" -> notifications.trySend(message.decode<NotificationMessage>().notification)
        }
    }

    private fun completeRequest(message: JsonObject) {
        val id = message["id"]?.jsonPrimitive?.contentOrNull ?: return
        val deferred = pending.remove(id) ?: return
        if (message["ok"]?.jsonPrimitive?.booleanOrNull == true) {
            deferred.complete(Result.success(message["data"]))
        } else {
            val error = message["error"]?.let { runCatching { it.decode<RequestError>() }.getOrNull() }
            deferred.complete(Result.failure(RelayException(error?.code ?: "error", error?.message ?: "The computer could not do that.")))
        }
    }

    private fun failPending(message: String) {
        val failures = pending.keys.mapNotNull { pending.remove(it) }
        failures.forEach { it.complete(Result.failure(RelayException("disconnected", message))) }
    }

    suspend fun request(type: String, fields: JsonObject = JsonObject(emptyMap())): Result<JsonElement?> {
        val id = nextId.incrementAndGet().toString()
        val deferred = CompletableDeferred<Result<JsonElement?>>()
        pending[id] = deferred
        try {
            val sent = socket?.send(message(type, fields, id).toString()) ?: false
            if (!sent) return Result.failure(RelayException("disconnected", "Not connected to the computer."))
            return withTimeoutOrNull(REQUEST_TIMEOUT_MS) { deferred.await() }
                ?: Result.failure(RelayException("timeout", "The computer took too long to answer."))
        } finally {
            pending.remove(id)
        }
    }

    fun send(type: String, fields: JsonObject = JsonObject(emptyMap())) {
        socket?.send(message(type, fields, id = null).toString())
    }

    private fun message(type: String, fields: JsonObject, id: String?): JsonObject = JsonObject(
        buildMap<String, JsonElement> {
            putAll(fields)
            put("type", JsonPrimitive(type))
            if (id != null) put("id", JsonPrimitive(id))
        },
    )

    fun attach(workspaceId: String, surfaceId: String, replacing: Long = NO_ATTACHMENT): Long {
        val next = Attachment(nextAttachmentId.incrementAndGet(), workspaceId, surfaceId)
        synchronized(attachments) {
            attachments.removeAll { it.id == replacing }
            attachments += next
        }
        if (attachment?.surfaceId != surfaceId) clearTerminalState()
        attachment = next
        suspended = false
        sendAttach(next)
        return next.id
    }

    val attachedSurfaceId: String? get() = attachment?.surfaceId

    fun isAttachedTo(surfaceId: String): Boolean = !suspended && attachment?.surfaceId == surfaceId

    fun suspendAttachment() {
        if (attachment == null || suspended) return
        suspended = true
        clearTerminalState()
        send("detach")
    }

    fun resumeAttachment() {
        if (!suspended) return
        suspended = false
        attachment?.let { sendAttach(it) }
    }

    fun setFit(fit: Pair<Int, Int>?) {
        val current = attachment ?: return
        val fitted = current.copy(fit = fit)
        attachment = fitted
        synchronized(attachments) {
            val index = attachments.indexOfFirst { it.id == current.id }
            if (index >= 0) attachments[index] = fitted
        }
        if (suspended) return
        if (fit == null) send("unfit") else send("fit", fitJson(fit))
    }

    fun detach(attachmentId: Long) {
        val previous = synchronized(attachments) {
            attachments.removeAll { it.id == attachmentId }
            attachments.lastOrNull()
        }
        if (attachment?.id != attachmentId) return
        if (previous != null) {
            if (attachment?.surfaceId != previous.surfaceId) clearTerminalState()
            attachment = previous
            if (!suspended) sendAttach(previous)
            return
        }
        attachment = null
        clearTerminalState()
        if (suspended) suspended = false else send("detach")
    }

    private fun clearTerminalState() {
        _screen.value = null
        _chat.value = null
        _agentMessages.value = emptyList()
        _inputState.value = null
    }

    fun resync() = send("resync")

    fun key(name: String) = send("key", buildJsonObject { put("key", name) })

    fun input(text: String) = send("input", buildJsonObject { put("text", text) })

    fun scroll(lines: Int) = send("scroll", buildJsonObject { put("lines", lines) })

    suspend fun paste(text: String, submit: Boolean): Result<Unit> =
        request("paste", buildJsonObject {
            put("text", text)
            put("submit", submit)
        }).map { }

    suspend fun sendImages(text: String, images: List<EncodedImage>): Result<Unit> =
        request("send_images", buildJsonObject {
            if (text.isNotEmpty()) put("text", text)
            putJsonArray("images") {
                images.forEach { image ->
                    addJsonObject {
                        put("data_b64", image.data)
                        put("format", image.format)
                    }
                }
            }
        }).map { }

    suspend fun chatFiles(query: String): Result<List<String>> =
        requestAs<ChatFiles>("chat_files", buildJsonObject { put("query", query) }, "The computer sent no files.").map { it.files }

    suspend fun chatImage(image: String): Result<ChatImageData> =
        requestAs("chat_image", buildJsonObject { put("image", image) }, "The computer sent no image.")

    suspend fun history(): Result<HistoryResponse> =
        requestOr("history", buildJsonObject { put("lines", HISTORY_LINES) }, HistoryResponse())

    suspend fun reply(approvalId: String, reply: ApprovalReply): Result<Unit> =
        request("answer_approval", buildJsonObject {
            put("approval_id", approvalId)
            when (reply) {
                is ApprovalReply.Decision -> {
                    put("mode", reply.mode)
                    if (!reply.feedback.isNullOrBlank()) put("feedback", reply.feedback)
                }
                is ApprovalReply.Answers -> putJsonArray("answers") { reply.answers.forEach { add(it) } }
            }
        }).map { forget(approvalId) }

    suspend fun sendAgentMessage(text: String): Result<AgentMessage> =
        requestAs<AgentMessage>("send_agent_message", buildJsonObject { put("text", text) }, "The computer did not confirm the message.")
            .onSuccess { sent ->
                if (_agentMessages.value.none { it.id == sent.id }) _agentMessages.value = listOf(sent) + _agentMessages.value
            }

    suspend fun deliverToAgent(text: String): Result<Unit> {
        if (!supports("send_agent_message")) return paste(text, submit = true)
        val error = sendAgentMessage(text).exceptionOrNull() ?: return Result.success(Unit)
        return if (error.isUnavailable) paste(text, submit = true) else Result.failure(error)
    }

    suspend fun setKeepAwake(enabled: Boolean): Result<Boolean> =
        request("set_keep_awake", buildJsonObject { put("enabled", enabled) }).mapCatching { data ->
            val confirmed = data?.decode<KeepAwakeMessage>()?.enabled ?: enabled
            _keepAwake.value = confirmed
            confirmed
        }

    suspend fun top(): Result<TopSummary> = requestAs("top", missing = "The computer sent no metrics.")

    suspend fun hostUsage(): Result<HostUsage> = requestAs("host_usage", missing = "The computer sent no usage.")

    suspend fun cloudVms(): Result<CloudVmsResponse> = requestAs("vms", missing = "The computer sent no VMs.")

    suspend fun setCloudVmPaused(vmId: String, paused: Boolean): Result<CloudVm> =
        requestAs(
            "set_vm_paused",
            buildJsonObject {
                put("vm_id", vmId)
                put("paused", paused)
            },
            "The computer did not say what the VM is doing.",
        )

    suspend fun automations(): Result<AutomationsResponse> = requestOr("automations", default = AutomationsResponse())

    suspend fun setAutomationEnabled(ruleId: String, enabled: Boolean): Result<AutomationsResponse> =
        requestOr(
            "set_automation_enabled",
            buildJsonObject {
                put("rule_id", ruleId)
                put("enabled", enabled)
            },
            AutomationsResponse(),
        )

    suspend fun automationLogs(ruleId: String? = null, limit: Int? = null): Result<List<AutomationLog>> =
        requestOr(
            "automation_logs",
            buildJsonObject {
                ruleId?.let { put("rule_id", it) }
                limit?.let { put("limit", it) }
            },
            AutomationLogsResponse(),
        ).map { it.logs }

    suspend fun reloadAutomations(): Result<AutomationsResponse> = requestOr("reload_automations", default = AutomationsResponse())

    suspend fun notifications(): Result<List<MacNotification>> =
        requestOr("notifications", default = NotificationsResponse()).map { it.notifications }

    suspend fun markNotificationsRead(notificationId: String? = null, surfaceId: String? = null, all: Boolean = false): Result<Unit> =
        command("mark_notifications_read") {
            notificationId?.let { put("notification_id", it) }
            surfaceId?.let { put("surface_id", it) }
            if (all) put("all", true)
        }

    suspend fun checklist(workspaceId: String): Result<Checklist> = checklistRequest("checklist", workspaceId)

    suspend fun addChecklistItem(workspaceId: String, text: String): Result<Checklist> =
        checklistRequest("add_checklist_item", workspaceId) { put("text", text) }

    suspend fun editChecklistItem(workspaceId: String, itemId: String, text: String): Result<Checklist> =
        checklistRequest("edit_checklist_item", workspaceId) {
            put("item_id", itemId)
            put("text", text)
        }

    suspend fun setChecklistItemState(workspaceId: String, itemId: String, state: String): Result<Checklist> =
        checklistRequest("set_checklist_item_state", workspaceId) {
            put("item_id", itemId)
            put("state", state)
        }

    suspend fun removeChecklistItem(workspaceId: String, itemId: String): Result<Checklist> =
        checklistRequest("remove_checklist_item", workspaceId) { put("item_id", itemId) }

    suspend fun clearChecklist(workspaceId: String): Result<Checklist> = checklistRequest("clear_checklist", workspaceId)

    private suspend fun checklistRequest(
        type: String,
        workspaceId: String,
        fields: JsonObjectBuilder.() -> Unit = {},
    ): Result<Checklist> =
        requestOr(
            type,
            buildJsonObject {
                put("workspace_id", workspaceId)
                fields()
            },
            Checklist(),
        )

    suspend fun recentSessions(query: String = ""): Result<List<RecentSession>> =
        requestOr("recent_sessions", buildJsonObject { if (query.isNotBlank()) put("query", query.trim()) }, RecentSessionsResponse())
            .map { it.sessions }

    suspend fun resumeSession(session: RecentSession): Result<ResumedSession> =
        requestAs(
            "resume_session",
            buildJsonObject {
                put("agent", session.agent)
                put("session_id", session.sessionId)
            },
            "The computer did not say where the session opened.",
        )

    suspend fun createTerminal(workspaceId: String): Result<String> =
        requestOr("create_terminal", buildJsonObject { put("workspace_id", workspaceId) }, CreatedWorkspace()).map { it.surfaceId }

    suspend fun createWorkspace(title: String, cwd: String): Result<CreatedWorkspace> =
        requestOr(
            "create_workspace",
            buildJsonObject {
                put("title", title)
                put("cwd", cwd)
            },
            CreatedWorkspace(),
        )

    suspend fun createWorkspaceInGroup(groupId: String, title: String): Result<CreatedWorkspace> =
        requestOr(
            "create_workspace_in_group",
            buildJsonObject {
                put("group_id", groupId)
                put("title", title)
            },
            CreatedWorkspace(),
        )

    suspend fun workspaceAction(workspaceId: String, action: String, color: String? = null, description: String? = null): Result<Unit> =
        command("workspace_action") {
            put("workspace_id", workspaceId)
            put("action", action)
            color?.let { put("color", it) }
            description?.let { put("description", it) }
        }

    suspend fun renameWorkspace(workspaceId: String, title: String): Result<Unit> =
        command("rename_workspace") {
            put("workspace_id", workspaceId)
            put("title", title)
        }

    suspend fun closeWorkspace(workspaceId: String, force: Boolean): Result<Unit> =
        command("close_workspace") {
            put("workspace_id", workspaceId)
            put("force", force)
        }

    suspend fun setWorkspaceStatus(workspaceId: String, status: String): Result<Unit> =
        command("set_workspace_status") {
            put("workspace_id", workspaceId)
            put("status", status)
        }

    suspend fun reconnectWorkspace(workspaceId: String): Result<Unit> = command("reconnect_workspace") { put("workspace_id", workspaceId) }

    suspend fun addToGroup(groupId: String, workspaceId: String): Result<Unit> =
        command("add_to_group") {
            put("group_id", groupId)
            put("workspace_id", workspaceId)
        }

    suspend fun removeFromGroup(groupId: String, workspaceId: String): Result<Unit> =
        command("remove_from_group") {
            put("group_id", groupId)
            put("workspace_id", workspaceId)
        }

    suspend fun createGroup(name: String, workspaceIds: List<String>): Result<Unit> =
        command("create_group") {
            put("name", name)
            putJsonArray("workspace_ids") { workspaceIds.forEach { add(it) } }
        }

    suspend fun pinGroup(groupId: String, pinned: Boolean): Result<Unit> =
        command("pin_group") {
            put("group_id", groupId)
            put("pinned", pinned)
        }

    suspend fun renameGroup(groupId: String, name: String): Result<Unit> =
        command("rename_group") {
            put("group_id", groupId)
            put("name", name)
        }

    suspend fun setGroupColor(groupId: String, color: String?): Result<Unit> =
        command("set_group_color") {
            put("group_id", groupId)
            put("color", color)
        }

    suspend fun ungroup(groupId: String): Result<Unit> = command("ungroup") { put("group_id", groupId) }

    suspend fun renameTerminal(workspaceId: String, surfaceId: String, title: String): Result<Unit> =
        terminalCommand("rename_terminal", workspaceId, surfaceId) { put("title", title) }

    suspend fun restartTerminal(workspaceId: String, surfaceId: String): Result<Unit> =
        terminalCommand("restart_terminal", workspaceId, surfaceId)

    suspend fun closeTerminal(workspaceId: String, surfaceId: String, force: Boolean): Result<Unit> =
        terminalCommand("close_terminal", workspaceId, surfaceId) { if (force) put("force", true) }

    private suspend fun terminalCommand(
        type: String,
        workspaceId: String,
        surfaceId: String,
        fields: JsonObjectBuilder.() -> Unit = {},
    ): Result<Unit> =
        command(type) {
            put("workspace_id", workspaceId)
            put("surface_id", surfaceId)
            fields()
        }

    private suspend fun command(type: String, fields: JsonObjectBuilder.() -> Unit): Result<Unit> =
        request(type, buildJsonObject(fields)).map { }

    private suspend inline fun <reified T> requestAs(type: String, fields: JsonObject = JsonObject(emptyMap()), missing: String): Result<T> =
        request(type, fields).mapCatching { (it ?: error(missing)).decode<T>() }

    private suspend inline fun <reified T> requestOr(type: String, fields: JsonObject = JsonObject(emptyMap()), default: T): Result<T> =
        request(type, fields).mapCatching { it?.decode<T>() ?: default }

    private fun forget(approvalId: String) {
        _approvals.value = _approvals.value?.filterNot { it.id == approvalId }
    }

    private fun sendAttach(target: Attachment) {
        send("attach", buildJsonObject {
            put("workspace_id", target.workspaceId)
            put("surface_id", target.surfaceId)
            target.fit?.let { put("fit", fitJson(it)) }
        })
    }

    private fun fitJson(fit: Pair<Int, Int>): JsonObject = buildJsonObject {
        put("columns", fit.first)
        put("rows", fit.second)
    }

    private companion object {
        const val REQUEST_TIMEOUT_MS = 20_000L
        const val HISTORY_LINES = 2_000
    }
}

private inline fun <reified T> JsonElement.decode(): T = RelayJson.decodeFromJsonElement(serializer<T>(), this)
