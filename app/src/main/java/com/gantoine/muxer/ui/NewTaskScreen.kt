package com.gantoine.muxer.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.RocketLaunch
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3ExpressiveApi
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.model.AgentNames
import com.gantoine.muxer.model.NewTask
import com.gantoine.muxer.model.WorkspaceListing
import com.gantoine.muxer.net.ConnectionState
import com.gantoine.muxer.protocol.RecentSession
import kotlinx.coroutines.launch

private const val REQUEST = "start_agent_task"

@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun NewTaskScreen(
    app: MuxerApplication,
    macId: String,
    onBack: () -> Unit,
    onOpenTerminal: (workspaceId: String, surfaceId: String) -> Unit,
) {
    val connection = rememberConnection(app, macId)
    val state by connection.collectOr(ConnectionState.Stopped) { it.state }
    val workspaces by connection.collectOr(null) { it.workspaces }
    val macName = rememberMacName(app, macId, fallback = "the computer")
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    var installed by remember { mutableStateOf<List<String>?>(null) }
    var loadFailure by remember { mutableStateOf<Throwable?>(null) }
    var sessions by remember { mutableStateOf(emptyList<RecentSession>()) }
    var agent by rememberSaveable { mutableStateOf<String?>(null) }
    var folder by rememberSaveable { mutableStateOf("") }
    var prompt by rememberSaveable { mutableStateOf("") }
    var title by rememberSaveable { mutableStateOf("") }
    var starting by remember { mutableStateOf(false) }

    suspend fun load() {
        val target = connection?.takeIf { it.supports(REQUEST) } ?: return
        target.installedAgents()
            .onSuccess { found ->
                installed = found
                loadFailure = null
                if (agent !in found) agent = NewTask.agents.firstOrNull { it in found }
            }
            .onFailure { loadFailure = it }
        if (target.supports("recent_sessions")) target.recentSessions().onSuccess { sessions = it }
    }

    fun start() {
        val target = connection ?: return
        val chosen = agent ?: return
        starting = true
        scope.launch {
            target.startAgentTask(chosen, folder.trim(), prompt, title)
                .onSuccess { onOpenTerminal(it.workspaceId, it.surfaceId) }
                .onFailure { snackbar.showSnackbar(it.message ?: "Couldn't start the task.") }
            starting = false
        }
    }

    LaunchedEffect(connection, state is ConnectionState.Connected) {
        if (state is ConnectionState.Connected && installed == null) load()
    }

    Scaffold(
        topBar = { CmuxTopBar(title = "New task", onBack = onBack) },
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            RemoteContent(
                value = installed,
                failure = loadFailure,
                state = state,
                requestType = REQUEST,
                icon = Icons.Filled.RocketLaunch,
                what = "start agent tasks",
                failedTitle = "Couldn't reach $macName",
                loading = "Checking which agents $macName has…",
                macName = macName,
                onRetry = {
                    loadFailure = null
                    scope.launch { load() }
                },
            ) { found ->
                val folders = remember(workspaces, sessions) { NewTask.folders(workspaces.orEmpty(), sessions) }
                Column(
                    Modifier.fillMaxSize().verticalScroll(rememberScrollState()).imePadding().padding(bottom = 24.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    ConnectionBanner(state)
                    SectionLabel("Agent", top = 8.dp)
                    if (found.isEmpty()) {
                        Text(
                            "Neither Claude Code nor Codex is installed on $macName.",
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(horizontal = 28.dp),
                        )
                    } else {
                        Row(Modifier.padding(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            NewTask.agents.filter { it in found }.forEach { option ->
                                FilterChip(
                                    selected = agent == option,
                                    onClick = { agent = option },
                                    label = { Text(AgentNames.display(option) ?: option) },
                                    leadingIcon = {
                                        Icon(painterResource(AgentGlyphs.agent(option)), contentDescription = null, Modifier.size(18.dp))
                                    },
                                )
                            }
                        }
                    }
                    SectionLabel("Folder")
                    LabeledTextField(
                        value = folder,
                        onValueChange = { folder = it },
                        label = "Folder on $macName",
                        placeholder = "~/code/app",
                        singleLine = true,
                        leadingIcon = { Icon(Icons.Filled.Folder, contentDescription = null) },
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, autoCorrectEnabled = false),
                        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp),
                    )
                    val suggestions = NewTask.suggestions(folders, folder)
                    suggestions.forEachIndexed { index, path ->
                        FolderSuggestion(path, index, suggestions.size) { folder = WorkspaceListing.shortPath(path) }
                    }
                    SectionLabel("Prompt")
                    LabeledTextField(
                        value = prompt,
                        onValueChange = { prompt = it },
                        label = "What should ${agent?.let(AgentNames::display) ?: "the agent"} do?",
                        keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences),
                        modifier = Modifier.fillMaxWidth().heightIn(min = 160.dp).padding(horizontal = 16.dp),
                    )
                    LabeledTextField(
                        value = title,
                        onValueChange = { title = it },
                        label = "Workspace name (optional)",
                        singleLine = true,
                        keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences),
                        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp),
                    )
                    Button(
                        onClick = ::start,
                        enabled = !starting && state is ConnectionState.Connected && NewTask.canStart(agent, folder, prompt),
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp).fillMaxWidth().height(56.dp),
                    ) {
                        if (starting) {
                            LoadingIndicator(Modifier.size(32.dp), color = MaterialTheme.colorScheme.onPrimary)
                        } else {
                            Text("Start task", style = MaterialTheme.typography.titleMedium)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun FolderSuggestion(path: String, index: Int, count: Int, onPick: () -> Unit) {
    Row(
        Modifier.groupedRow(groupedShape(index, count), onClick = onPick),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Filled.Folder, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.size(20.dp))
        Spacer(Modifier.width(16.dp))
        Text(
            WorkspaceListing.shortPath(path),
            style = MaterialTheme.typography.bodyLarge,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}
