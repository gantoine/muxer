package com.gantoine.muxer.protocol

import kotlinx.serialization.ExperimentalSerializationApi
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonNamingStrategy

@OptIn(ExperimentalSerializationApi::class)
val RelayJson = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    namingStrategy = JsonNamingStrategy.SnakeCase
}

@Serializable
data class Welcome(
    val macName: String,
    val relayVersion: String,
    val protocolVersion: Int,
    val cmuxVersion: String,
    val deviceId: String,
    val notificationSeq: Long = 0,
    val unsupported: List<String> = emptyList(),
)

@Serializable
data class TerminalInfo(
    val id: String,
    val title: String = "",
    val currentDirectory: String? = null,
    val agentSource: String? = null,
    val agentState: String? = null,
    val gitBranch: String? = null,
    val gitDirty: Boolean? = null,
    val listeningPorts: List<Int> = emptyList(),
)

@Serializable
data class WorkspaceInfo(
    val id: String,
    val title: String = "",
    val description: String? = null,
    val customColor: String? = null,
    val currentDirectory: String? = null,
    val isPinned: Boolean = false,
    val groupId: String? = null,
    val preview: String? = null,
    val previewAt: Double? = null,
    val lastActivityAt: Double? = null,
    val hasUnread: Boolean = false,
    val unreadCount: Int = 0,
    val terminals: List<TerminalInfo> = emptyList(),
    val status: LaneStatus? = null,
    val checklist: ChecklistProgress? = null,
    val remote: RemoteState? = null,
)

@Serializable
data class LaneStatus(val effective: String, val inferred: String = "", val override: Boolean = false)

@Serializable
data class ChecklistProgress(val completed: Int = 0, val total: Int = 0, val next: String? = null)

@Serializable
data class RemoteState(val state: String, val destination: String? = null, val detail: String? = null)

@Serializable
data class ChecklistItem(val id: String, val text: String, val state: String = "pending", val origin: String = "user")

@Serializable
data class Checklist(val items: List<ChecklistItem> = emptyList(), val progress: ChecklistProgress = ChecklistProgress())

@Serializable
data class WorkspaceGroupInfo(
    val id: String,
    val name: String = "",
    val anchorWorkspaceId: String? = null,
    val memberWorkspaceIds: List<String> = emptyList(),
    val isCollapsed: Boolean = false,
    val isPinned: Boolean = false,
    val customColor: String? = null,
)

@Serializable
data class WorkspacesMessage(val workspaces: List<WorkspaceInfo>, val groups: List<WorkspaceGroupInfo> = emptyList())

@Serializable
data class CellStyle(
    val id: Int,
    val fg: String,
    val bg: String,
    val bold: Boolean = false,
    val faint: Boolean = false,
    val italic: Boolean = false,
    val underline: Boolean = false,
    val strikethrough: Boolean = false,
    val inverse: Boolean = false,
    val invisible: Boolean = false,
)

@Serializable
data class Span(val column: Int, val width: Int, val style: Int, val text: String)

@Serializable
data class RowUpdate(val row: Int, val spans: List<Span>)

@Serializable
data class CursorInfo(val row: Int, val column: Int, val visible: Boolean, val shape: String = "block")

@Serializable
data class ScreenFrame(
    val workspaceId: String,
    val surfaceId: String,
    val full: Boolean,
    val columns: Int,
    val rows: Int,
    val foreground: String,
    val background: String,
    val altScreen: Boolean = false,
    val cursor: CursorInfo? = null,
    val styles: List<CellStyle> = emptyList(),
    val changed: List<RowUpdate> = emptyList(),
)

@Serializable
data class HistoryResponse(
    val styles: List<CellStyle> = emptyList(),
    val lines: List<List<Span>> = emptyList(),
    val total: Int = 0,
)

@Serializable
data class DetachedMessage(val surfaceId: String, val reason: String = "")

@Serializable
data class RelayNotification(
    val seq: Long,
    val kind: String,
    val id: String,
    val title: String? = null,
    val subtitle: String? = null,
    val body: String? = null,
    val workspaceId: String? = null,
    val surfaceId: String? = null,
    val tabTitle: String? = null,
    val createdAt: String? = null,
)

@Serializable
data class NotificationMessage(val notification: RelayNotification)

@Serializable
data class MacNotification(
    val id: String,
    val title: String = "",
    val subtitle: String = "",
    val body: String = "",
    val workspaceId: String? = null,
    val surfaceId: String? = null,
    val tabTitle: String = "",
    val createdAt: String? = null,
    val isRead: Boolean = false,
)

@Serializable
data class NotificationsResponse(val notifications: List<MacNotification> = emptyList())

@Serializable
data class ApprovalOption(val label: String, val description: String? = null)

@Serializable
data class ApprovalQuestion(
    val header: String? = null,
    val prompt: String,
    val multiSelect: Boolean = false,
    val options: List<ApprovalOption> = emptyList(),
)

@Serializable
data class Approval(
    val id: String,
    val kind: String,
    val agent: String? = null,
    val workspaceId: String,
    val surfaceId: String,
    val tool: String? = null,
    val detail: String? = null,
    val description: String? = null,
    val questions: List<ApprovalQuestion> = emptyList(),
    val plan: String? = null,
    val cwd: String? = null,
    val createdAt: String? = null,
) {
    val isPermission: Boolean get() = kind == KIND_PERMISSION
    val isQuestion: Boolean get() = kind == KIND_QUESTION
    val isPlan: Boolean get() = kind == KIND_PLAN

    companion object {
        const val KIND_PERMISSION = "permission"
        const val KIND_QUESTION = "question"
        const val KIND_PLAN = "plan"
    }
}

@Serializable
data class ApprovalsMessage(val approvals: List<Approval> = emptyList())

@Serializable
data class RequestError(val code: String, val message: String = "")

@Serializable
data class PairResponse(
    val token: String,
    val deviceId: String,
    val macName: String,
    val protocolVersion: Int,
)

@Serializable
data class ErrorResponse(val error: RequestError)

@Serializable
data class CreatedWorkspace(val workspaceId: String = "", val surfaceId: String = "")

@Serializable
data class ChatTool(
    val name: String,
    val summary: String? = null,
    val status: String = "running",
    val output: String? = null,
    val images: List<String> = emptyList(),
    val diff: List<ChatHunk> = emptyList(),
    val todos: List<ChatTodo> = emptyList(),
)

@Serializable
data class ChatTodo(val content: String = "", val status: String = "pending", val activeForm: String? = null)

@Serializable
data class ChatHunk(val oldStart: Int = 0, val newStart: Int = 0, val lines: List<String> = emptyList())

@Serializable
data class ChatEntry(
    val id: String,
    val kind: String,
    val text: String? = null,
    val images: List<String> = emptyList(),
    val tool: ChatTool? = null,
    val time: String? = null,
)

@Serializable
data class ChatImageData(@SerialName("data_b64") val data: String = "", val format: String = "")

@Serializable
data class ChatSessionMessage(val surfaceId: String, val sessionId: String? = null)

@Serializable
data class ChatMessage(val surfaceId: String, val sessionId: String, val reset: Boolean = false, val entries: List<ChatEntry> = emptyList())

@Serializable
data class Retry(
    val surfaceId: String,
    val workspaceId: String? = null,
    val agent: String? = null,
    val attempt: Int = 1,
    val dueAt: String,
)

@Serializable
data class AgentActivityMessage(val retries: List<Retry> = emptyList(), val busy: List<String> = emptyList())

@Serializable
data class AgentMessage(
    val id: String,
    val body: String = "",
    val state: String,
    val createdAt: Double = 0.0,
    val failureReason: String? = null,
) {
    companion object {
        const val QUEUED = "queued"
        const val DELIVERED = "delivered"
        const val READ = "read"
        const val FAILED = "failed"
    }
}

@Serializable
data class AgentMessagesMessage(val surfaceId: String, val messages: List<AgentMessage> = emptyList())

@Serializable
data class KeepAwakeMessage(val enabled: Boolean)

@Serializable
data class InputStateMessage(
    val surfaceId: String,
    val state: String = "",
    val blocksTyping: Boolean = false,
    val waitingOnHuman: Boolean = false,
)

@Serializable
data class TopSummary(
    val sampledAt: String? = null,
    val cpuPercent: Double = 0.0,
    val memoryBytes: Long = 0,
    val processCount: Int = 0,
    val appMemoryBytes: Long = 0,
    val agents: List<TopAgent> = emptyList(),
    val workspaces: List<TopWorkspace> = emptyList(),
)

@Serializable
data class TopAgent(val name: String, val cpuPercent: Double = 0.0, val memoryBytes: Long = 0, val processCount: Int = 0)

@Serializable
data class TopWorkspace(
    val id: String,
    val title: String = "",
    val cpuPercent: Double = 0.0,
    val memoryBytes: Long = 0,
    val processCount: Int = 0,
    val processes: List<TopProcess> = emptyList(),
)

@Serializable
data class TopProcess(val pid: Int, val name: String, val cpuPercent: Double = 0.0, val memoryBytes: Long = 0)

@Serializable
data class RecentSession(
    val agent: String,
    val sessionId: String,
    val title: String = "",
    val cwd: String? = null,
    val gitBranch: String? = null,
    val created: String? = null,
    val modified: String = "",
    val messageCount: Int? = null,
    val workspaceId: String? = null,
    val surfaceId: String? = null,
)

@Serializable
data class RecentSessionsResponse(val sessions: List<RecentSession> = emptyList())

@Serializable
data class ResumedSession(val workspaceId: String, val surfaceId: String, val alreadyOpen: Boolean = false)

@Serializable
data class CloudVmStats(
    val cpus: Int? = null,
    val cpuPercent: Double? = null,
    val memoryUsedMb: Long? = null,
    val memoryTotalMb: Long? = null,
    val diskUsedMb: Long? = null,
    val diskTotalMb: Long? = null,
)

@Serializable
data class CloudVm(
    val id: String,
    val name: String,
    val status: String,
    val image: String = "",
    val kind: String = "",
    val createdAt: String? = null,
    val stats: CloudVmStats? = null,
)

@Serializable
data class CloudVmsResponse(val vms: List<CloudVm> = emptyList(), val plan: String? = null)

@Serializable
data class HostUsage(val cpuPercent: Double = 0.0, val memoryUsedBytes: Long = 0, val memoryTotalBytes: Long = 0)
