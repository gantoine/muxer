package com.gantoine.muxer.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.OpenInNew
import androidx.compose.material.icons.outlined.CloudSync
import androidx.compose.material.icons.outlined.Merge
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExperimentalMaterial3ExpressiveApi
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.images.RemoteImages
import com.gantoine.muxer.model.AgentNames
import com.gantoine.muxer.model.WorkspaceListing
import com.gantoine.muxer.model.parseInstant
import com.gantoine.muxer.net.ConnectionState
import com.gantoine.muxer.net.RelayConnection
import com.gantoine.muxer.net.RelayException
import com.gantoine.muxer.net.isUnavailable
import com.gantoine.muxer.protocol.PostHogCloudRun
import com.gantoine.muxer.protocol.PostHogCloudTask
import com.gantoine.muxer.protocol.PostHogCloudTasksResponse

data class HostedCloudTask(val task: PostHogCloudTask, val macId: String)

enum class CloudRunPhase { Running, Waiting, Queued, Done, Failed, Stopped }

object PostHogCloud {
    const val FEATURE = "posthog_cloud"
    const val REFRESH_MS = 15_000L

    fun phase(status: String, state: String? = null): CloudRunPhase = when (status) {
        "in_progress" -> if (state == "waiting") CloudRunPhase.Waiting else CloudRunPhase.Running
        "queued", "not_started" -> CloudRunPhase.Queued
        "completed" -> CloudRunPhase.Done
        "failed" -> CloudRunPhase.Failed
        else -> CloudRunPhase.Stopped
    }

    fun statusLabel(status: String, state: String? = null): String = when (phase(status, state)) {
        CloudRunPhase.Running -> if (state == "idle") "Waiting for your reply" else "Running"
        CloudRunPhase.Waiting -> "Needs your approval"
        CloudRunPhase.Queued -> if (status == "queued") "Queued" else "Not started"
        CloudRunPhase.Done -> "Done"
        CloudRunPhase.Failed -> "Failed"
        CloudRunPhase.Stopped -> "Stopped"
    }

    fun summary(task: PostHogCloudTask): String = buildList {
        add(if (task.status == "failed" && !task.error.isNullOrBlank()) task.error else statusLabel(task.status, task.state))
        task.repository?.takeIf { it.isNotBlank() }?.let(::add)
        WorkspaceListing.time(parseInstant(task.updatedAt))?.let(::add)
    }.joinToString(" · ")

    fun title(task: PostHogCloudTask): String = task.title.ifBlank { task.number?.let { "Task #$it" } ?: "Untitled task" }

    fun runSubtitle(run: PostHogCloudRun?): String? = run?.let {
        buildList {
            add(statusLabel(it.status, it.state))
            AgentNames.display(it.agent)?.let(::add)
            it.branch?.takeIf { branch -> branch.isNotBlank() }?.let(::add)
        }.joinToString(" · ")
    }

    fun midTurn(run: PostHogCloudRun?): Boolean = run != null && when (phase(run.status, run.state)) {
        CloudRunPhase.Queued -> true
        CloudRunPhase.Running -> run.state == null || run.state == "working"
        else -> false
    }

    fun answers(previous: Map<String, PostHogCloudTasksResponse>, macId: String, result: Result<PostHogCloudTasksResponse>): Map<String, PostHogCloudTasksResponse> =
        result.fold(
            onSuccess = { previous + (macId to it) },
            onFailure = { if (it.isUnavailable || (it as? RelayException)?.code?.startsWith("posthog_cloud_") == true) previous - macId else previous },
        )

    fun pick(answers: Map<String, PostHogCloudTasksResponse>, macOrder: List<String>): List<HostedCloudTask>? {
        val macId = macOrder.firstOrNull { answers[it]?.connected == true } ?: return null
        return answers.getValue(macId).tasks.map { HostedCloudTask(it, macId) }
    }

    suspend fun fetch(connection: RelayConnection): Result<PostHogCloudTasksResponse> {
        val state = connection.state.value
        if (state !is ConnectionState.Connected || !state.has(FEATURE)) {
            return Result.failure(RelayException("unsupported", "This computer's relay has no PostHog Cloud."))
        }
        return connection.postHogCloudTasks()
    }
}

@Stable
class PostHogCloudModel {
    var tasks by mutableStateOf<List<HostedCloudTask>?>(null)
    var answers = emptyMap<String, PostHogCloudTasksResponse>()
}

@Composable
fun rememberPostHogCloud(app: MuxerApplication, macOrder: List<String>): PostHogCloudModel {
    val model = remember { PostHogCloudModel() }
    val currentOrder by rememberUpdatedState(macOrder)
    PollConnectedMacs(app, PostHogCloud.REFRESH_MS, fetch = PostHogCloud::fetch) { connected, results ->
        model.answers = results.fold(model.answers.filterKeys { it in connected }) { answers, (id, result) -> PostHogCloud.answers(answers, id, result) }
        model.tasks = PostHogCloud.pick(model.answers, currentOrder)
    }
    return model
}

@Composable
fun cloudRunPhaseColor(phase: CloudRunPhase): Color = when (phase) {
    CloudRunPhase.Running -> BrandGreen
    CloudRunPhase.Waiting -> MaterialTheme.colorScheme.tertiary
    CloudRunPhase.Queued -> MaterialTheme.colorScheme.tertiary
    CloudRunPhase.Done, CloudRunPhase.Stopped -> MaterialTheme.colorScheme.outline
    CloudRunPhase.Failed -> MaterialTheme.colorScheme.error
}

@Composable
fun CloudTaskAvatar(phase: CloudRunPhase, size: Dp = 52.dp) {
    StatusAvatar(
        Icons.Outlined.CloudSync,
        MaterialTheme.colorScheme.primaryContainer,
        MaterialTheme.colorScheme.onPrimaryContainer,
        cloudRunPhaseColor(phase),
        size,
    )
}

@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun CloudTaskRow(task: PostHogCloudTask, shape: Shape, onOpen: () -> Unit, modifier: Modifier = Modifier) {
    val phase = PostHogCloud.phase(task.status, task.state)
    Row(
        modifier.groupedRow(shape, onClick = onOpen, contentPadding = PaddingValues(16.dp)),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        CloudTaskAvatar(phase)
        Spacer(Modifier.width(16.dp))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            Text(PostHogCloud.title(task), style = MaterialTheme.typography.titleMediumEmphasized, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(
                PostHogCloud.summary(task),
                style = MaterialTheme.typography.bodyMedium,
                color = if (phase == CloudRunPhase.Failed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PostHogCloudRunScreen(
    app: MuxerApplication,
    macId: String,
    taskId: String,
    runId: String,
    title: String,
    taskUrl: String,
    onBack: () -> Unit,
) {
    val connections by app.connections.connectionsFlow.collectAsState()
    val connection = connections[macId]
    val uriHandler = LocalUriHandler.current
    DisposableEffect(connection, runId) {
        connection?.watchPostHogCloudRun(taskId, runId)
        onDispose { connection?.unwatchPostHogCloudRun(runId) }
    }
    val chat by connection.collectOr(null) { it.cloudChat }
    val run by connection.collectOr(null) { it.cloudRun }
    val failure by connection.collectOr(null) { it.cloudRunError }
    val chatImages = remember { RemoteImages { null } }

    Scaffold(
        topBar = {
            CmuxTopBar(
                title = {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        (failure ?: PostHogCloud.runSubtitle(run))?.let {
                            Text(
                                it,
                                style = MaterialTheme.typography.labelMedium,
                                color = if (failure != null) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                    }
                },
                onBack = onBack,
                actions = {
                    run?.pullRequest?.takeIf { it.isNotBlank() }?.let { url ->
                        IconButton(onClick = { uriHandler.openUri(url) }) { Icon(Icons.Outlined.Merge, contentDescription = "Open the pull request") }
                    }
                    if (taskUrl.isNotBlank()) {
                        IconButton(onClick = { uriHandler.openUri(taskUrl) }) { Icon(Icons.AutoMirrored.Filled.OpenInNew, contentDescription = "Open in PostHog") }
                    }
                },
            )
        },
    ) { padding ->
        val current = chat
        if (current == null) {
            Box(Modifier.padding(padding).fillMaxSize(), contentAlignment = Alignment.Center) {
                Text(
                    "Connecting to the computer…",
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            return@Scaffold
        }
        ChatView(
            chat = current,
            agentName = run?.agent?.let(AgentNames::display) ?: "the agent",
            status = null,
            midTurn = PostHogCloud.midTurn(run),
            answeringHere = false,
            agentMessages = emptyList(),
            messagingAgent = false,
            waitingOnYou = false,
            showComposer = false,
            draft = "",
            onDraftChange = {},
            onSend = {},
            onDictate = {},
            dictating = false,
            images = emptyList(),
            sendingImages = false,
            onPickPhotos = {},
            onTakePhoto = {},
            onRemoveImage = {},
            fileSuggestions = emptyList(),
            onPickFile = {},
            onStop = {},
            onShowTerminal = {},
            chatImages = chatImages,
            modifier = Modifier.padding(padding).fillMaxWidth(),
        )
    }
}
