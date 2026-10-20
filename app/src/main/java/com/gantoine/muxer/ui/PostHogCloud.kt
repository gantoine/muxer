package com.gantoine.muxer.ui

import android.content.ActivityNotFoundException
import android.content.Intent
import android.speech.RecognizerIntent
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.OpenInNew
import androidx.compose.material.icons.outlined.Merge
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.images.RemoteImages
import com.gantoine.muxer.model.AgentNames
import com.gantoine.muxer.model.ApprovalReply
import com.gantoine.muxer.net.ConnectionState
import com.gantoine.muxer.net.VisibleTerminal
import com.gantoine.muxer.protocol.Approval
import com.gantoine.muxer.protocol.PostHogCloudRun
import kotlinx.coroutines.launch

enum class CloudRunPhase { Running, Waiting, Queued, Done, Failed, Stopped }

object PostHogCloud {
    const val ACTIONS_FEATURE = "posthog_cloud_actions"

    fun visibleKey(runId: String): String = "posthog-cloud:$runId"

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
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }
    val visible = remember(macId, runId) { VisibleTerminal(macId, PostHogCloud.visibleKey(runId)) }
    DisposableEffect(connection, runId) {
        connection?.watchPostHogCloudRun(taskId, runId)
        app.connections.visibleTerminal.value = visible
        onDispose {
            connection?.unwatchPostHogCloudRun(runId)
            if (app.connections.visibleTerminal.value === visible) app.connections.visibleTerminal.value = null
        }
    }
    LifecycleEventEffect(Lifecycle.Event.ON_START) { app.connections.visibleTerminal.value = visible }
    LifecycleEventEffect(Lifecycle.Event.ON_STOP) {
        if (app.connections.visibleTerminal.value === visible) app.connections.visibleTerminal.value = null
    }
    val chat by connection.collectOr(null) { it.cloudChat }
    val run by connection.collectOr(null) { it.cloudRun }
    val failure by connection.collectOr(null) { it.cloudRunError }
    val state by connection.collectOr(ConnectionState.Stopped) { it.state }
    val allApprovals by connection.collectOr(null) { it.approvals }
    val approvals = allApprovals.orEmpty().filter { it.posthogRunId == runId }
    val canAct = (state as? ConnectionState.Connected)?.has(PostHogCloud.ACTIONS_FEATURE) == true
    val chatImages = remember { RemoteImages { null } }
    var draft by rememberSaveable { mutableStateOf("") }
    var sending by remember { mutableStateOf(false) }
    var answering by remember { mutableStateOf(emptySet<String>()) }
    var dictating by rememberSaveable { mutableStateOf(false) }
    val dictation = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        dictating = false
        val spoken = result.data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)?.firstOrNull()
        if (!spoken.isNullOrBlank()) draft = listOf(draft.trimEnd(), spoken).filter { it.isNotEmpty() }.joinToString(" ")
    }
    val shownTitle = title.ifBlank { run?.title.orEmpty() }.ifBlank { "PostHog Cloud run" }
    val shownUrl = taskUrl.ifBlank { run?.url.orEmpty() }

    fun send() {
        val text = draft.trim()
        val current = connection ?: return
        if (text.isEmpty() || sending) return
        sending = true
        scope.launch {
            current.postHogCloudSend(taskId, runId, text)
                .onSuccess { draft = "" }
                .onFailure { snackbar.showSnackbar(it.message ?: "Couldn't send your message.") }
            sending = false
        }
    }

    fun stop() {
        val current = connection ?: return
        scope.launch {
            current.postHogCloudStop(taskId, runId).onFailure { snackbar.showSnackbar(it.message ?: "Couldn't stop the agent.") }
        }
    }

    fun reply(approval: Approval, reply: ApprovalReply) {
        val current = connection ?: return
        answering = answering + approval.id
        scope.launch {
            val result = current.reply(approval.id, reply)
            answering = answering - approval.id
            result.onFailure { snackbar.showSnackbar(it.message ?: "Couldn't send your answer.") }
        }
    }

    fun dictate() {
        val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH)
            .putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
        try {
            dictation.launch(intent)
            dictating = true
        } catch (_: ActivityNotFoundException) {
            scope.launch { snackbar.showSnackbar("No voice input is available on this phone.") }
        }
    }

    Scaffold(
        topBar = {
            CmuxTopBar(
                title = {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Text(shownTitle, maxLines = 1, overflow = TextOverflow.Ellipsis)
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
                    if (shownUrl.isNotBlank()) {
                        IconButton(onClick = { uriHandler.openUri(shownUrl) }) { Icon(Icons.AutoMirrored.Filled.OpenInNew, contentDescription = "Open in PostHog") }
                    }
                },
            )
        },
        snackbarHost = { SnackbarHost(snackbar) },
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
            midTurn = sending || PostHogCloud.midTurn(run),
            answeringHere = approvals.isNotEmpty(),
            agentMessages = emptyList(),
            messagingAgent = false,
            waitingOnYou = approvals.isNotEmpty(),
            showComposer = canAct,
            draft = draft,
            onDraftChange = { draft = it },
            onSend = ::send,
            onDictate = ::dictate,
            dictating = dictating,
            images = emptyList(),
            sendingImages = false,
            onPickPhotos = {},
            onTakePhoto = {},
            onRemoveImage = {},
            fileSuggestions = emptyList(),
            onPickFile = {},
            onStop = ::stop,
            onShowTerminal = {},
            chatImages = chatImages,
            modifier = Modifier.padding(padding).fillMaxWidth(),
            canAttachImages = false,
            overlay = { ApprovalCards(approvals, sending = answering, onReply = ::reply, modifier = Modifier.padding(bottom = 4.dp)) },
        )
    }
}
