package com.gantoine.muxer.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.CallSplit
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.CloudOff
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.PushPin
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Terminal
import androidx.compose.material.icons.outlined.Notifications
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material3.Badge
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExperimentalMaterial3ExpressiveApi
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.core.content.pm.ShortcutManagerCompat
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.model.Organizing
import com.gantoine.muxer.model.WorkspaceEntry
import com.gantoine.muxer.model.WorkspaceFilter
import com.gantoine.muxer.model.WorkspaceListing
import com.gantoine.muxer.model.isAgent
import com.gantoine.muxer.net.ConnectionState
import com.gantoine.muxer.net.RelayConnection
import com.gantoine.muxer.net.RelayException
import com.gantoine.muxer.protocol.TerminalInfo
import com.gantoine.muxer.protocol.WorkspaceGroupInfo
import com.gantoine.muxer.protocol.WorkspaceInfo
import com.gantoine.muxer.terminal.parseHexColor
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

private sealed interface WorkspaceLine {
    val key: String

    data class Plain(val workspace: WorkspaceInfo) : WorkspaceLine {
        override val key get() = workspace.id
    }

    data class Header(val entry: WorkspaceEntry.Group, val collapsed: Boolean) : WorkspaceLine {
        override val key get() = entry.key
    }

    data class Anchor(val group: WorkspaceGroupInfo, val workspace: WorkspaceInfo) : WorkspaceLine {
        override val key get() = workspace.id
    }
}

private fun cards(entries: List<WorkspaceEntry>, collapsed: (WorkspaceGroupInfo) -> Boolean): List<List<WorkspaceLine>> {
    val cards = mutableListOf<MutableList<WorkspaceLine>>()
    entries.forEach { entry ->
        when (entry) {
            is WorkspaceEntry.Single -> {
                val open = cards.lastOrNull()?.takeIf { it.first() is WorkspaceLine.Plain }
                if (open != null) open += WorkspaceLine.Plain(entry.workspace) else cards += mutableListOf<WorkspaceLine>(WorkspaceLine.Plain(entry.workspace))
            }
            is WorkspaceEntry.Group -> {
                val folded = collapsed(entry.group)
                val card = mutableListOf<WorkspaceLine>(WorkspaceLine.Header(entry, folded))
                if (!folded) {
                    if (entry.showAnchor && entry.anchor != null) card += WorkspaceLine.Anchor(entry.group, entry.anchor)
                    entry.members.forEach { card += WorkspaceLine.Plain(it) }
                }
                cards += card
            }
        }
    }
    return cards
}

@OptIn(ExperimentalMaterial3Api::class, ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun WorkspacesScreen(
    app: MuxerApplication,
    macId: String,
    onBack: () -> Unit,
    onOpenSettings: () -> Unit,
    onOpenNotifications: () -> Unit,
    onOpenChecklist: (workspaceId: String) -> Unit,
    onOpenTerminal: (workspaceId: String, surfaceId: String) -> Unit,
) {
    val connection = rememberConnection(app, macId)
    val macName = rememberMacName(app, macId, fallback = "Computer")
    val snackbar = remember { SnackbarHostState() }
    var dialog by remember { mutableStateOf<WorkspaceDialog?>(null) }
    var query by rememberSaveable { mutableStateOf("") }
    var filter by rememberSaveable { mutableStateOf(WorkspaceFilter.All) }
    var toggledGroups by rememberSaveable { mutableStateOf(listOf<String>()) }
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    val canPinShortcuts = remember { ShortcutManagerCompat.isRequestPinShortcutSupported(context) }

    fun run(action: suspend RelayConnection.() -> Result<*>) {
        val target = connection ?: return
        scope.launch {
            target.action().onFailure { failure ->
                val closing = dialog as? WorkspaceDialog.Close
                if (failure is RelayException && failure.needsConfirmation && closing != null) {
                    dialog = closing.copy(force = true)
                } else {
                    snackbar.showSnackbar(failure.message ?: "That did not work.")
                }
            }
        }
    }

    fun openNewTerminal(workspace: WorkspaceInfo) {
        run { createTerminal(workspace.id).onSuccess { if (it.isNotEmpty()) onOpenTerminal(workspace.id, it) } }
    }

    fun open(workspace: WorkspaceInfo) {
        when (workspace.terminals.size) {
            0 -> openNewTerminal(workspace)
            1 -> onOpenTerminal(workspace.id, workspace.terminals.first().id)
            else -> dialog = WorkspaceDialog.PickTerminal(workspace)
        }
    }

    val state by connection.collectOr(ConnectionState.Stopped) { it.state }
    val workspaces by connection.collectOr(null) { it.workspaces }
    val groups by connection.collectOr(emptyList()) { it.groups }

    Scaffold(
        topBar = {
            CmuxTopBar(
                title = macName,
                onBack = onBack,
                actions = {
                    IconButton(onClick = onOpenNotifications) {
                        Icon(Icons.Outlined.Notifications, contentDescription = "Notifications from $macName")
                    }
                    IconButton(onClick = onOpenSettings) {
                        Icon(Icons.Outlined.Settings, contentDescription = "Settings for $macName")
                    }
                },
            )
        },
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        Box(Modifier.padding(padding).fillMaxSize()) {
            val all = workspaces
            when {
                connection == null -> EmptyState(
                    icon = Icons.Filled.Terminal,
                    title = "Not paired anymore",
                    body = "This phone no longer has access to $macName. Remove it and pair again.",
                )
                state == ConnectionState.Unauthorized && all == null -> EmptyState(
                    icon = Icons.Filled.CloudOff,
                    title = "Not paired anymore",
                    body = "$macName no longer accepts this phone. Remove it from this phone, then pair it again.",
                ) {
                    Button(onClick = onOpenSettings) { Text("Open settings") }
                }
                all == null -> Column {
                    ConnectionBanner(state)
                    LoadingState(if (state is ConnectionState.Connected) "Fetching workspaces…" else "Waiting for $macName…")
                }
                all.isEmpty() -> Column {
                    ConnectionBanner(state)
                    EmptyState(
                        icon = Icons.Filled.Terminal,
                        title = "No workspaces yet",
                        body = "Create one here and it opens in cmux on $macName.",
                    ) {
                        Button(onClick = { dialog = WorkspaceDialog.Create }) {
                            Icon(Icons.Filled.Add, contentDescription = null, Modifier.size(18.dp))
                            Spacer(Modifier.width(8.dp))
                            Text("New workspace")
                        }
                    }
                }
                else -> {
                    val narrowed = filter != WorkspaceFilter.All || query.isNotBlank()
                    val cards = remember(all, groups, filter, query, toggledGroups) {
                        cards(WorkspaceListing.entries(all, groups, filter, query)) { group ->
                            !narrowed && group.isCollapsed != (group.id in toggledGroups)
                        }
                    }
                    fun toggle(group: WorkspaceGroupInfo) {
                        toggledGroups = if (group.id in toggledGroups) toggledGroups - group.id else toggledGroups + group.id
                    }
                    LazyColumn(
                        Modifier.fillMaxSize(),
                        contentPadding = PaddingValues(bottom = 112.dp),
                        verticalArrangement = Arrangement.spacedBy(2.dp),
                    ) {
                        item(key = "banner") {
                            AnimatedVisibility(visible = state !is ConnectionState.Connected) { ConnectionBanner(state) }
                        }
                        item(key = "filters") { FilterRow(all, groups, filter, onSelect = { filter = it }) }
                        if (cards.isEmpty()) {
                            item(key = "empty") {
                                FullWidthMessage(
                                    if (query.isNotBlank()) "No workspaces match \"${query.trim()}\"." else "Nothing here right now.",
                                    Modifier.padding(top = 48.dp),
                                )
                            }
                        }
                        cards.forEachIndexed { cardIndex, card ->
                            itemsIndexed(card, key = { _, line -> line.key }) { index, line ->
                                val shape = groupedShape(index, card.size)
                                val modifier = Modifier.animateItem().then(
                                    if (index == 0 && cardIndex > 0) Modifier.padding(top = 12.dp) else Modifier,
                                )
                                when (line) {
                                    is WorkspaceLine.Plain -> WorkspaceRow(
                                        workspace = line.workspace,
                                        shape = shape,
                                        onOpen = { open(line.workspace) },
                                        onLongPress = { dialog = WorkspaceDialog.Actions(line.workspace) },
                                        modifier = modifier,
                                    )
                                    is WorkspaceLine.Header -> GroupHeaderRow(
                                        entry = line.entry,
                                        collapsed = line.collapsed,
                                        canCollapse = !narrowed,
                                        shape = shape,
                                        onToggle = { toggle(line.entry.group) },
                                        onLongPress = { dialog = WorkspaceDialog.GroupActions(line.entry.group) },
                                        modifier = modifier,
                                    )
                                    is WorkspaceLine.Anchor -> GroupTerminalsRow(
                                        workspace = line.workspace,
                                        shape = shape,
                                        onOpen = { open(line.workspace) },
                                        onLongPress = { dialog = WorkspaceDialog.Actions(line.workspace, line.group) },
                                        modifier = modifier,
                                    )
                                }
                            }
                        }
                    }
                }
            }
            if (connection != null && all != null) {
                SearchDock(
                    query = query,
                    onQueryChange = { query = it },
                    onCreate = { dialog = WorkspaceDialog.Create },
                    modifier = Modifier.align(Alignment.BottomCenter).imePadding(),
                )
            }
        }
    }

    WorkspaceDialogs(
        dialog = dialog,
        groups = groups,
        canPinShortcuts = canPinShortcuts,
        onDialog = { dialog = it },
        run = ::run,
        onOpen = ::open,
        onNewTerminal = ::openNewTerminal,
        onOpenCreated = { created -> if (created.surfaceId.isNotEmpty()) onOpenTerminal(created.workspaceId, created.surfaceId) },
        onOpenTerminal = onOpenTerminal,
        onOpenChecklist = onOpenChecklist,
        onPinShortcut = { workspace ->
            if (!app.shortcuts.requestPin(macId, macName, workspace)) {
                scope.launch { snackbar.showSnackbar("Your home screen can't add this shortcut.") }
            }
        },
    )
}

@Composable
private fun FilterRow(all: List<WorkspaceInfo>, groups: List<WorkspaceGroupInfo>, selected: WorkspaceFilter, onSelect: (WorkspaceFilter) -> Unit) {
    Row(
        Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        val counts = remember(all, groups) { WorkspaceFilter.entries.associateWith { WorkspaceListing.count(all, groups, it) } }
        WorkspaceFilter.entries.forEach { filter ->
            val count = counts.getValue(filter)
            if (filter == WorkspaceFilter.All || count > 0 || filter == selected) {
                val active = filter == selected
                FilterChip(
                    selected = active,
                    onClick = { onSelect(filter) },
                    label = { Text("${filter.label} · $count") },
                    leadingIcon = if (active) {
                        { Icon(Icons.Filled.Check, contentDescription = null, Modifier.size(18.dp)) }
                    } else {
                        null
                    },
                )
            }
        }
    }
}

@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
private fun WorkspaceRow(
    workspace: WorkspaceInfo,
    shape: Shape,
    onOpen: () -> Unit,
    onLongPress: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val unread = WorkspaceListing.isUnread(workspace)
    Row(
        modifier.groupedRow(shape, onClick = onOpen, onLongClick = onLongPress),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        WorkspaceTile(workspace, animated = true)
        Spacer(Modifier.width(16.dp))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (workspace.isPinned) {
                    Icon(
                        Icons.Filled.PushPin,
                        contentDescription = "Pinned",
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.size(14.dp),
                    )
                    Spacer(Modifier.width(4.dp))
                }
                Text(
                    workspace.title.ifBlank { "Untitled" },
                    style = MaterialTheme.typography.titleMediumEmphasized,
                    fontWeight = if (unread) FontWeight.Bold else FontWeight.Medium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                when {
                    workspace.unreadCount > 0 -> Badge(
                        containerColor = MaterialTheme.colorScheme.primary,
                        contentColor = MaterialTheme.colorScheme.onPrimary,
                        modifier = Modifier.padding(start = 12.dp),
                    ) { Text("${workspace.unreadCount}", modifier = Modifier.padding(horizontal = 2.dp)) }
                    workspace.hasUnread -> Box(
                        Modifier.padding(start = 12.dp).size(10.dp).background(MaterialTheme.colorScheme.primary, CircleShape),
                    )
                }
            }
            SubtitleLine(
                WorkspaceListing.subtitle(workspace),
                WorkspaceListing.time(workspace.lastActivityAt ?: workspace.previewAt),
                highlightTime = unread,
            )
            WorkspaceMetaLine(workspace)
        }
    }
}

@Composable
private fun SubtitleLine(subtitle: String, time: String?, highlightTime: Boolean = false) {
    Row {
        Text(
            subtitle,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f).alignByBaseline(),
        )
        if (time != null) {
            Spacer(Modifier.width(12.dp))
            Text(
                time,
                style = MaterialTheme.typography.labelMedium,
                color = if (highlightTime) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                modifier = Modifier.alignByBaseline(),
            )
        }
    }
}

@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
private fun GroupHeaderRow(
    entry: WorkspaceEntry.Group,
    collapsed: Boolean,
    canCollapse: Boolean,
    shape: Shape,
    onToggle: () -> Unit,
    onLongPress: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val count = entry.members.size
    val background = MaterialTheme.colorScheme.surfaceContainerHigh
    Row(
        modifier.groupedRow(
            shape,
            onClick = { if (canCollapse) onToggle() },
            onLongClick = onLongPress,
            onClickLabel = if (canCollapse) (if (collapsed) "Show workspaces" else "Hide workspaces") else null,
            onLongClickLabel = "Group actions",
            color = background,
            contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp),
        ),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            Modifier.width(48.dp).semantics { contentDescription = if (count == 1) "1 workspace" else "$count workspaces" },
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                Icons.Filled.Folder,
                contentDescription = null,
                tint = entry.group.customColor?.let(::parseHexColor) ?: MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(24.dp),
            )
            Text(
                "$count",
                color = background,
                fontSize = with(LocalDensity.current) { 10.dp.toSp() },
                fontWeight = FontWeight.Bold,
                maxLines = 1,
                modifier = Modifier.padding(top = 2.dp),
            )
        }
        Spacer(Modifier.width(16.dp))
        if (entry.group.isPinned) {
            Icon(
                Icons.Filled.PushPin,
                contentDescription = "Pinned",
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(14.dp),
            )
            Spacer(Modifier.width(4.dp))
        }
        Text(
            entry.group.name.ifBlank { entry.anchor?.title?.ifBlank { null } ?: "Group" },
            style = MaterialTheme.typography.titleMediumEmphasized,
            fontWeight = FontWeight.SemiBold,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (canCollapse) {
            val rotation by animateFloatAsState(if (collapsed) 0f else 180f, tween(200), label = "chevron")
            Icon(
                Icons.Filled.ExpandMore,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.graphicsLayer { rotationZ = rotation },
            )
        }
    }
}

@Composable
private fun GroupTerminalsRow(
    workspace: WorkspaceInfo,
    shape: Shape,
    onOpen: () -> Unit,
    onLongPress: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val unread = WorkspaceListing.isUnread(workspace)
    Row(
        modifier.groupedRow(
            shape,
            onClick = onOpen,
            onLongClick = onLongPress,
            contentPadding = PaddingValues(horizontal = 16.dp, vertical = 10.dp),
        ),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.width(48.dp), contentAlignment = Alignment.Center) {
            Icon(
                painterResource(AgentGlyphs.pane(WorkspaceListing.isAgent(workspace), WorkspaceListing.agentSource(workspace))),
                contentDescription = WorkspaceListing.agentStatus(workspace)?.label,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(24.dp),
            )
        }
        Spacer(Modifier.width(16.dp))
        Column(Modifier.weight(1f)) {
            SubtitleLine(
                WorkspaceListing.subtitle(workspace),
                WorkspaceListing.time(workspace.lastActivityAt ?: workspace.previewAt),
                highlightTime = unread,
            )
            WorkspaceMetaLine(workspace)
        }
    }
}

@Composable
fun WorkspaceTile(
    workspace: WorkspaceInfo,
    size: Dp = 48.dp,
    animated: Boolean = false,
    color: Color? = null,
) {
    val accent = color ?: parseHexColor(WorkspaceListing.color(workspace)) ?: MaterialTheme.colorScheme.primary
    val corner = size * 0.3f
    val lane = if (animated) Organizing.shownLane(workspace.status) else null
    val shown = rememberSettledLane(lane?.key)
    val working by animateFloatAsState(if (shown == "working") 1f else 0f, tween(600), label = "working")
    val waiting by animateFloatAsState(if (shown == "needs-attention") 1f else 0f, tween(600), label = "waiting")
    val review by animateFloatAsState(if (shown == "review") 1f else 0f, tween(600), label = "review")
    val animating = working > 0f || waiting > 0f || review > 0f
    var seconds by remember { mutableFloatStateOf(0f) }
    LaunchedEffect(animating) {
        if (!animating) return@LaunchedEffect
        val start = withFrameNanos { it }
        while (true) withFrameNanos { seconds = (it - start) / 1_000_000_000f }
    }
    val motion = remember(accent) { TileMotion(accent) }
    val agent = WorkspaceListing.isAgent(workspace)
    Box(
        Modifier
            .size(size)
            .then(if (lane != null) Modifier.semantics { contentDescription = lane.label } else Modifier)
            .drawBehind {
                if (waiting > 0f) motion.drawRipples(this, seconds, corner.toPx(), waiting)
                if (working > 0f) motion.drawHalo(this, seconds, corner.toPx(), working)
            }
            .graphicsLayer {
                val beat = if (waiting > 0f) motion.heartbeat(seconds) * waiting else 0f
                scaleX = 1f + beat
                scaleY = 1f + beat
            }
            .drawBehind {
                drawRoundRect(accent, cornerRadius = CornerRadius(corner.toPx()))
                if (working > 0f) motion.drawAurora(this, seconds, corner.toPx(), working)
                if (review > 0f) motion.drawSheen(this, seconds, corner.toPx(), review)
            },
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            painterResource(AgentGlyphs.pane(agent, WorkspaceListing.agentSource(workspace))),
            contentDescription = null,
            tint = Color.White,
            modifier = Modifier.size(size / 2).graphicsLayer {
                val breathe = if (working > 0f) motion.breathe(seconds) * working else 0f
                scaleX = 1f + 0.05f * breathe
                scaleY = 1f + 0.05f * breathe
            },
        )
    }
}

@Composable
private fun rememberSettledLane(lane: String?): String? {
    var shown by remember { mutableStateOf(lane) }
    LaunchedEffect(lane) {
        if (shown == "working" && lane != "needs-attention") delay(WORKING_HOLD_MILLIS)
        shown = lane
    }
    return shown
}

private const val WORKING_HOLD_MILLIS = 8_000L

@Composable
private fun TerminalGitLine(terminal: TerminalInfo) {
    val branch = WorkspaceListing.gitSummary(terminal)
    val ports = WorkspaceListing.portsSummary(terminal)
    if (branch == null && ports == null) return
    Row(verticalAlignment = Alignment.CenterVertically) {
        if (branch != null) {
            Icon(Icons.AutoMirrored.Filled.CallSplit, contentDescription = "Git branch", modifier = Modifier.size(14.dp))
            Spacer(Modifier.width(4.dp))
        }
        Text(
            listOfNotNull(branch, ports).joinToString(" · "),
            style = MaterialTheme.typography.bodySmall,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.semantics {
                contentDescription = listOfNotNull(WorkspaceListing.gitDescription(terminal), ports).joinToString(", ")
            },
        )
    }
}

@Composable
private fun SearchDock(query: String, onQueryChange: (String) -> Unit, onCreate: () -> Unit, modifier: Modifier = Modifier) {
    Box(
        modifier
            .fillMaxWidth()
            .background(
                Brush.verticalGradient(
                    0f to Color.Transparent,
                    0.55f to MaterialTheme.colorScheme.background.copy(alpha = 0.92f),
                    1f to MaterialTheme.colorScheme.background,
                ),
            ),
    ) {
        Row(
            Modifier.fillMaxWidth().padding(start = 16.dp, end = 16.dp, top = 40.dp, bottom = 16.dp),
            verticalAlignment = Alignment.Bottom,
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            SearchField(
                query = query,
                onQueryChange = onQueryChange,
                label = "Search workspaces",
                colors = OutlinedTextFieldDefaults.colors(
                    focusedContainerColor = MaterialTheme.colorScheme.background,
                    unfocusedContainerColor = MaterialTheme.colorScheme.background,
                ),
                modifier = Modifier.weight(1f),
            )
            FloatingActionButton(onClick = onCreate) {
                Icon(Icons.Filled.Add, contentDescription = "New workspace")
            }
        }
    }
}

@Composable
internal fun TerminalChoice(terminal: TerminalInfo, selected: Boolean = false, onClick: () -> Unit) {
    ListItem(
        headlineContent = { Text(terminal.title.ifBlank { "Terminal" }, maxLines = 1, overflow = TextOverflow.Ellipsis) },
        supportingContent = {
            val title = terminal.title.ifBlank { "Terminal" }
            val path = terminal.currentDirectory?.let(WorkspaceListing::shortPath)?.takeIf { it != title }
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                listOfNotNull(terminal.agentState, path)
                    .takeIf { it.isNotEmpty() }
                    ?.let { Text(it.joinToString(" · "), maxLines = 1, overflow = TextOverflow.Ellipsis) }
                TerminalGitLine(terminal)
            }
        },
        leadingContent = {
            Icon(
                painterResource(AgentGlyphs.pane(terminal.isAgent, terminal.agentSource)),
                contentDescription = null,
            )
        },
        trailingContent = if (selected) {
            { Icon(Icons.Filled.Check, contentDescription = "Current terminal") }
        } else {
            null
        },
        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
        modifier = Modifier.combinedClickable(onClick = onClick),
    )
}
