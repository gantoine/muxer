package com.gantoine.muxer.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.outlined.History
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.model.AgentNames
import com.gantoine.muxer.model.WorkspaceListing
import com.gantoine.muxer.model.parseInstant
import com.gantoine.muxer.net.ConnectionState
import com.gantoine.muxer.protocol.RecentSession
import java.time.Instant
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

object SessionListing {
    fun agentName(agent: String): String = AgentNames.display(agent) ?: agent

    fun label(session: RecentSession): String =
        session.title.ifBlank { session.cwd?.trimEnd('/')?.substringAfterLast('/').orEmpty() }.ifBlank { "Untitled session" }

    fun detail(session: RecentSession): String =
        listOfNotNull(
            agentName(session.agent),
            session.cwd?.takeIf { it.isNotBlank() }?.let(WorkspaceListing::shortPath),
            session.gitBranch?.takeIf { it.isNotBlank() },
        ).joinToString(" · ")

    fun modifiedAt(session: RecentSession): Instant? = parseInstant(session.modified)

    fun isOpen(session: RecentSession): Boolean = !session.workspaceId.isNullOrEmpty() && !session.surfaceId.isNullOrEmpty()
}

private const val SEARCH_DELAY_MS = 300L

@Composable
fun SessionsScreen(
    app: MuxerApplication,
    macId: String,
    onBack: () -> Unit,
    onOpenTerminal: (workspaceId: String, surfaceId: String) -> Unit,
) {
    val connection = rememberConnection(app, macId)
    val state by connection.collectOr(ConnectionState.Stopped) { it.state }
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    var query by rememberSaveable { mutableStateOf("") }
    var sessions by remember { mutableStateOf<List<RecentSession>?>(null) }
    var loadFailure by remember { mutableStateOf<Throwable?>(null) }
    var confirming by remember { mutableStateOf<RecentSession?>(null) }
    var resuming by remember { mutableStateOf(false) }
    val macName = rememberMacName(app, macId, fallback = "the computer")

    suspend fun load() {
        val target = connection?.takeIf { it.supports(REQUEST) } ?: return
        target.recentSessions(query)
            .onSuccess {
                sessions = it
                loadFailure = null
            }
            .onFailure { failure ->
                if (sessions == null) loadFailure = failure else snackbar.showSnackbar(failure.message ?: "Couldn't load the sessions.")
            }
    }

    fun resume(session: RecentSession) {
        val target = connection ?: return
        resuming = true
        scope.launch {
            target.resumeSession(session)
                .onSuccess { onOpenTerminal(it.workspaceId, it.surfaceId) }
                .onFailure { snackbar.showSnackbar(it.message ?: "Couldn't resume that session.") }
            resuming = false
        }
    }

    fun open(session: RecentSession) {
        val workspaceId = session.workspaceId
        val surfaceId = session.surfaceId
        if (SessionListing.isOpen(session) && workspaceId != null && surfaceId != null) {
            onOpenTerminal(workspaceId, surfaceId)
        } else {
            confirming = session
        }
    }

    LaunchedEffect(connection, state is ConnectionState.Connected, query) {
        if (state !is ConnectionState.Connected) return@LaunchedEffect
        if (sessions != null) delay(SEARCH_DELAY_MS)
        load()
    }

    Scaffold(
        topBar = {
            CmuxTopBar(title = "Recent sessions", onBack = onBack)
        },
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            if (resuming) LinearProgressIndicator(Modifier.fillMaxWidth())
            RemoteContent(
                value = sessions,
                failure = loadFailure,
                state = state,
                requestType = REQUEST,
                icon = Icons.Outlined.History,
                what = "list past sessions",
                failedTitle = "Couldn't load sessions",
                loading = "Fetching sessions…",
                macName = macName,
                onRetry = {
                    loadFailure = null
                    scope.launch { load() }
                },
            ) { list ->
                Column {
                    ConnectionBanner(state)
                    SearchField(
                        query,
                        onQueryChange = { query = it },
                        label = "Search sessions",
                        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
                    )
                    if (list.isEmpty()) {
                        if (query.isNotBlank()) {
                            FullWidthMessage("No sessions match \"${query.trim()}\".", Modifier.padding(top = 48.dp))
                        } else {
                            EmptyState(
                                icon = Icons.Outlined.History,
                                title = "No past sessions",
                                body = "Agent sessions cmux has seen on $macName show up here.",
                            )
                        }
                    } else {
                        LazyColumn(
                            Modifier.fillMaxSize(),
                            contentPadding = PaddingValues(top = 8.dp, bottom = 32.dp),
                            verticalArrangement = Arrangement.spacedBy(2.dp),
                        ) {
                            itemsIndexed(list, key = { _, session -> session.agent + "/" + session.sessionId }) { index, session ->
                                SessionRow(session, index, list.size, enabled = !resuming, onOpen = { open(session) })
                            }
                        }
                    }
                }
            }
        }
    }

    confirming?.let { session ->
        val folder = session.cwd?.takeIf { it.isNotBlank() }?.let(WorkspaceListing::shortPath)
        ConfirmDialog(
            title = "Resume this session?",
            text = buildString {
                append("“${SessionListing.label(session)}” opens in a new workspace")
                if (folder != null) append(" in $folder")
                append(" on $macName, and ${SessionListing.agentName(session.agent)} picks up where it left off.")
            },
            confirm = "Resume",
            onConfirm = {
                confirming = null
                resume(session)
            },
            onDismiss = { confirming = null },
        )
    }
}

private const val REQUEST = "recent_sessions"

@Composable
private fun SessionRow(session: RecentSession, index: Int, count: Int, enabled: Boolean, onOpen: () -> Unit) {
    val open = SessionListing.isOpen(session)
    val time = WorkspaceListing.time(SessionListing.modifiedAt(session))
    Row(
        Modifier.groupedRow(groupedShape(index, count), onClick = onOpen, enabled = enabled),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            painterResource(AgentGlyphs.agent(session.agent)),
            contentDescription = null,
            tint = if (open) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(20.dp),
        )
        Spacer(Modifier.width(16.dp))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    SessionListing.label(session),
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Medium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                if (time != null) {
                    Spacer(Modifier.width(12.dp))
                    Text(time, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            Text(
                SessionListing.detail(session),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (open) {
                Text("Running in a terminal", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
            }
        }
    }
}
