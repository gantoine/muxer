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
import androidx.compose.material.icons.filled.CreateNewFolder
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.RocketLaunch
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Terminal
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
import com.gantoine.muxer.protocol.WorkspaceInfo
import kotlinx.coroutines.launch

private const val REQUEST = "start_agent_task"

@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun NewTaskScreen(
    app: MuxerApplication,
    macId: String,
    initialWorkspaceId: String?,
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
    var agent by rememberSaveable { mutableStateOf<String?>(null) }
    var folder by rememberSaveable { mutableStateOf("") }
    var prompt by rememberSaveable { mutableStateOf("") }
    var title by rememberSaveable { mutableStateOf("") }
    var inWorkspace by rememberSaveable { mutableStateOf(initialWorkspaceId != null) }
    var workspaceId by rememberSaveable { mutableStateOf(initialWorkspaceId) }
    var picking by rememberSaveable { mutableStateOf(initialWorkspaceId == null) }
    var choosingWhere by rememberSaveable { mutableStateOf(initialWorkspaceId == null) }
    var search by rememberSaveable { mutableStateOf("") }
    var startsInWorkspaces by remember { mutableStateOf(false) }
    var starting by remember { mutableStateOf(false) }
    val chosenWorkspace = workspaces?.firstOrNull { it.id == workspaceId }?.takeUnless { picking }
    val usesWorkspace = inWorkspace && startsInWorkspaces

    suspend fun load() {
        val target = connection?.takeIf { it.supports(REQUEST) } ?: return
        target.installedAgents()
            .onSuccess { found ->
                installed = found
                loadFailure = null
                if (agent !in found) agent = NewTask.agents.firstOrNull { it in found }
            }
            .onFailure { loadFailure = it }
    }

    fun start() {
        val target = connection ?: return
        val chosen = agent ?: return
        val workspace = chosenWorkspace.takeIf { usesWorkspace }
        if (usesWorkspace && workspace == null) return
        starting = true
        scope.launch {
            val started = if (workspace != null) {
                target.startAgentTaskInWorkspace(chosen, workspace.id, prompt)
            } else {
                target.startAgentTask(chosen, folder.trim(), prompt, title)
            }
            started
                .onSuccess { onOpenTerminal(it.workspaceId, it.surfaceId) }
                .onFailure { snackbar.showSnackbar(it.message ?: "Couldn't start the task.") }
            starting = false
        }
    }

    LaunchedEffect(connection, state is ConnectionState.Connected) {
        if (state is ConnectionState.Connected && installed == null) load()
    }

    LaunchedEffect(state) {
        (state as? ConnectionState.Connected)?.let { startsInWorkspaces = it.has(NewTask.IN_WORKSPACE) }
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
                Column(
                    Modifier.fillMaxSize().verticalScroll(rememberScrollState()).imePadding().padding(bottom = 24.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    ConnectionBanner(state)
                    if (startsInWorkspaces) {
                        TaskLabel("Where", first = true)
                        if (choosingWhere) Row(Modifier.padding(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            FilterChip(
                                selected = !inWorkspace,
                                onClick = { inWorkspace = false },
                                label = { Text("New workspace") },
                                leadingIcon = { Icon(Icons.Filled.CreateNewFolder, contentDescription = null, Modifier.size(18.dp)) },
                            )
                            FilterChip(
                                selected = inWorkspace,
                                onClick = { inWorkspace = true },
                                label = { Text("Existing workspace") },
                                leadingIcon = { Icon(Icons.Filled.Terminal, contentDescription = null, Modifier.size(18.dp)) },
                            )
                        }
                        if (inWorkspace) {
                            WorkspacePicker(
                                workspaces = workspaces.orEmpty(),
                                chosen = chosenWorkspace,
                                search = search,
                                macName = macName,
                                onSearch = { search = it },
                                onChange = {
                                    picking = true
                                    choosingWhere = true
                                },
                                onPick = { picked ->
                                    workspaceId = picked.id
                                    picking = false
                                    search = ""
                                },
                            )
                        }
                    }
                    TaskLabel("Agent", first = !startsInWorkspaces)
                    if (found.isEmpty()) {
                        Text(
                            "None of Claude Code, Codex or Pi is installed on $macName.",
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(horizontal = 16.dp),
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
                    if (!usesWorkspace) {
                        TaskLabel("Folder")
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
                    }
                    TaskLabel("Prompt")
                    LabeledTextField(
                        value = prompt,
                        onValueChange = { prompt = it },
                        label = "What should ${agent?.let(AgentNames::display) ?: "the agent"} do?",
                        keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences),
                        modifier = Modifier.fillMaxWidth().heightIn(min = 160.dp).padding(horizontal = 16.dp),
                    )
                    if (!usesWorkspace) {
                        LabeledTextField(
                            value = title,
                            onValueChange = { title = it },
                            label = "Workspace name (optional)",
                            singleLine = true,
                            keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences),
                            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp),
                        )
                    }
                    val ready = if (usesWorkspace) {
                        NewTask.canStartIn(agent, chosenWorkspace, prompt)
                    } else {
                        NewTask.canStart(agent, folder, prompt)
                    }
                    Button(
                        onClick = ::start,
                        enabled = !starting && state is ConnectionState.Connected && ready,
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
private fun TaskLabel(text: String, first: Boolean = false) {
    Text(
        text,
        style = MaterialTheme.typography.labelLarge,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(start = 16.dp, end = 16.dp, top = if (first) 8.dp else 16.dp),
    )
}

@Composable
private fun WorkspacePicker(
    workspaces: List<WorkspaceInfo>,
    chosen: WorkspaceInfo?,
    search: String,
    macName: String,
    onSearch: (String) -> Unit,
    onChange: () -> Unit,
    onPick: (WorkspaceInfo) -> Unit,
) {
    if (chosen != null) {
        WorkspaceChoice(chosen, index = 0, count = 1, onClickLabel = "Pick another workspace", onClick = onChange) {
            Text("Change", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary)
        }
        return
    }
    if (workspaces.count(NewTask::canHost) > NewTask.SEARCHABLE_WORKSPACES) {
        LabeledTextField(
            value = search,
            onValueChange = onSearch,
            label = "Search workspaces",
            singleLine = true,
            leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
            keyboardOptions = KeyboardOptions(autoCorrectEnabled = false),
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp),
        )
    }
    val choices = NewTask.workspaceChoices(workspaces, search)
    if (choices.isEmpty()) {
        Text(
            if (search.isNotBlank()) "No workspaces match \"${search.trim()}\"." else "No workspaces are open on $macName.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(horizontal = 28.dp),
        )
    }
    choices.forEachIndexed { index, workspace ->
        WorkspaceChoice(workspace, index, choices.size, onClickLabel = "Start the task here", onClick = { onPick(workspace) })
    }
}

@Composable
private fun WorkspaceChoice(
    workspace: WorkspaceInfo,
    index: Int,
    count: Int,
    onClickLabel: String,
    onClick: () -> Unit,
    trailing: @Composable () -> Unit = {},
) {
    Row(
        Modifier.groupedRow(groupedShape(index, count), onClick = onClick, onClickLabel = onClickLabel),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        WorkspaceTile(workspace, size = 36.dp)
        Spacer(Modifier.width(16.dp))
        Column(Modifier.weight(1f)) {
            Text(
                workspace.title.ifBlank { "Untitled" },
                style = MaterialTheme.typography.bodyLarge,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                workspace.currentDirectory?.let(WorkspaceListing::shortPath) ?: WorkspaceListing.subtitle(workspace),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        trailing()
    }
}
