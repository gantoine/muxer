package com.gantoine.muxer.model

import com.gantoine.muxer.protocol.TerminalInfo
import com.gantoine.muxer.protocol.WorkspaceGroupInfo
import com.gantoine.muxer.protocol.WorkspaceInfo
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.time.format.TextStyle
import java.time.temporal.ChronoUnit
import java.util.Locale

val TerminalInfo.isAgent: Boolean get() = agentSource != null || agentState != null

enum class WorkspaceFilter(val label: String, val lane: String? = null) {
    All("All"),
    NeedsYou("Needs you"),
    Agents("Agents"),
    Pinned("Pinned"),
    ToDo("To do", "todo"),
    Review("Review", "review"),
    Done("Done", "done"),
}

sealed interface AgentStatus {
    val label: String

    data class NeedsYou(override val label: String) : AgentStatus
    data class Working(override val label: String) : AgentStatus
    data class Idle(override val label: String) : AgentStatus
    data class Other(override val label: String) : AgentStatus
}

sealed interface WorkspaceEntry {
    val key: String

    data class Single(val workspace: WorkspaceInfo) : WorkspaceEntry {
        override val key get() = workspace.id
    }

    data class Group(
        val group: WorkspaceGroupInfo,
        val anchor: WorkspaceInfo?,
        val members: List<WorkspaceInfo>,
        val showAnchor: Boolean = anchor != null,
    ) : WorkspaceEntry {
        override val key get() = "group:${group.id}"
    }
}

object WorkspaceListing {
    private val macHome = Regex("^/Users/[^/]+")
    val fallbackColors = listOf("#8CAAEE", "#A6D189", "#EF9F76", "#CA9EE6", "#81C8BE", "#E5C890", "#F4B8E4", "#99D1DB")

    private val attentionWords = listOf("input", "attention", "permission", "approv", "question", "blocked", "need", "review")
    private val workingWords = listOf("run", "work", "think", "busy", "generat", "active", "progress", "tool")
    private val idleWords = listOf("wait", "idle")
    private val terminalPromptWords = listOf("permission", "approv", "question")

    fun needsTerminalAnswer(status: AgentStatus?): Boolean =
        status is AgentStatus.NeedsYou && terminalPromptWords.any { status.label.contains(it, ignoreCase = true) }

    fun gitSummary(terminal: TerminalInfo): String? {
        val branch = branch(terminal) ?: return null
        return if (terminal.gitDirty == true) "$branch*" else branch
    }

    fun gitDescription(terminal: TerminalInfo): String? {
        val branch = branch(terminal) ?: return null
        return if (terminal.gitDirty == true) "$branch, uncommitted changes" else branch
    }

    private fun branch(terminal: TerminalInfo): String? = terminal.gitBranch?.trim()?.takeIf { it.isNotEmpty() }

    fun portsSummary(terminal: TerminalInfo): String? = when (terminal.listeningPorts.size) {
        0 -> null
        1 -> "port ${terminal.listeningPorts.first()}"
        else -> "ports " + terminal.listeningPorts.joinToString(", ")
    }

    fun matches(workspace: WorkspaceInfo, query: String): Boolean {
        val needle = query.trim()
        if (needle.isEmpty()) return true
        val haystack = buildList {
            add(workspace.title)
            workspace.currentDirectory?.let(::add)
            workspace.preview?.let(::add)
            workspace.description?.let(::add)
            workspace.terminals.forEach { terminal ->
                add(terminal.title)
                terminal.currentDirectory?.let(::add)
                terminal.agentState?.let(::add)
                branch(terminal)?.let(::add)
            }
        }
        return haystack.any { it.contains(needle, ignoreCase = true) }
    }

    fun passes(workspace: WorkspaceInfo, filter: WorkspaceFilter): Boolean = when (filter) {
        WorkspaceFilter.All -> true
        WorkspaceFilter.NeedsYou -> needsYou(workspace)
        WorkspaceFilter.Agents -> isAgent(workspace)
        WorkspaceFilter.Pinned -> workspace.isPinned
        WorkspaceFilter.ToDo, WorkspaceFilter.Review, WorkspaceFilter.Done -> workspace.status?.effective == filter.lane
    }

    fun entries(
        workspaces: List<WorkspaceInfo>,
        groups: List<WorkspaceGroupInfo>,
        filter: WorkspaceFilter = WorkspaceFilter.All,
        query: String = "",
    ): List<WorkspaceEntry> {
        val byId = workspaces.associateBy { it.id }
        val groupsById = groups.associateBy { it.id }
        val emitted = mutableSetOf<String>()
        val placed = mutableSetOf<String>()
        fun shows(workspace: WorkspaceInfo) = passes(workspace, filter) && matches(workspace, query)
        return workspaces.mapNotNull { workspace ->
            val group = workspace.groupId?.let(groupsById::get)
                ?: return@mapNotNull WorkspaceEntry.Single(workspace).takeIf { shows(workspace) && placed.add(workspace.id) }
            if (!emitted.add(group.id)) return@mapNotNull null
            val anchor = group.anchorWorkspaceId?.let(byId::get)?.takeIf { it.id !in placed }
            val members = (group.memberWorkspaceIds.mapNotNull(byId::get) + workspaces.filter { it.groupId == group.id })
                .distinctBy { it.id }
                .filter { it.id != anchor?.id && it.id !in placed }
            anchor?.let { placed += it.id }
            placed += members.map { it.id }
            val wholeGroup = (filter == WorkspaceFilter.All || filter == WorkspaceFilter.Pinned && group.isPinned) &&
                (query.isBlank() || group.name.contains(query.trim(), ignoreCase = true))
            val shown = if (wholeGroup) members else members.filter(::shows)
            val showAnchor = anchor != null && (wholeGroup || shows(anchor))
            WorkspaceEntry.Group(group, anchor, shown, showAnchor).takeIf { showAnchor || shown.isNotEmpty() }
        }
    }

    fun anchorTitle(group: WorkspaceGroupInfo, anchor: WorkspaceInfo): String {
        val title = anchor.title.trim()
        return if (title.isEmpty() || title.equals(group.name.trim(), ignoreCase = true)) "${group.name.ifBlank { "Group" }} terminals" else title
    }

    fun count(workspaces: List<WorkspaceInfo>, groups: List<WorkspaceGroupInfo>, filter: WorkspaceFilter): Int {
        val pinnedGroups = groups.filter { it.isPinned }.map { it.id }.toSet()
        return workspaces.count { passes(it, filter) || filter == WorkspaceFilter.Pinned && it.groupId in pinnedGroups }
    }

    fun isAgent(workspace: WorkspaceInfo): Boolean = workspace.terminals.any { it.isAgent }

    fun needsAttention(text: String?): Boolean =
        text?.let { candidate -> attentionWords.any { candidate.contains(it, ignoreCase = true) } } ?: false

    fun isUnread(workspace: WorkspaceInfo): Boolean = workspace.unreadCount > 0 || workspace.hasUnread

    fun workspaceFor(workspaces: List<WorkspaceInfo>, workspaceId: String?, surfaceId: String): WorkspaceInfo? =
        workspaces.firstOrNull { it.id == workspaceId }
            ?: workspaces.firstOrNull { workspace -> workspace.terminals.any { it.id == surfaceId } }

    fun needsYou(workspace: WorkspaceInfo): Boolean = isUnread(workspace) || agentStatus(workspace) is AgentStatus.NeedsYou

    fun agentStatus(workspace: WorkspaceInfo): AgentStatus? =
        classify(workspace.terminals.mapNotNull { it.agentState?.trim()?.takeIf(String::isNotEmpty) }.distinct())

    fun agentStatus(terminal: TerminalInfo): AgentStatus? =
        classify(listOfNotNull(terminal.agentState?.trim()?.takeIf(String::isNotEmpty)))

    private fun classify(states: List<String>): AgentStatus? {
        if (states.isEmpty()) return null
        states.firstOrNull(::needsAttention)?.let { return AgentStatus.NeedsYou(humanize(it)) }
        states.firstOrNull { state -> workingWords.any { state.contains(it, ignoreCase = true) } }
            ?.let { return AgentStatus.Working(humanize(it)) }
        if (states.any { state -> idleWords.any { state.contains(it, ignoreCase = true) } }) return AgentStatus.Idle("Idle")
        return AgentStatus.Other(humanize(states.first()))
    }

    fun subtitle(workspace: WorkspaceInfo): String =
        workspace.preview?.trim()?.takeIf { it.isNotEmpty() && (isAgent(workspace) || !needsAttention(it)) }
            ?: workspace.currentDirectory?.let(::shortPath)
            ?: when (val count = workspace.terminals.size) {
                0, 1 -> "Terminal"
                else -> "$count terminals"
            }

    fun color(workspace: WorkspaceInfo): String =
        workspace.customColor ?: fallbackColors[Math.floorMod(workspace.id.hashCode(), fallbackColors.size)]

    fun shortPath(path: String): String {
        val home = macHome.find(path)?.value ?: return path
        return "~" + path.removePrefix(home)
    }

    fun time(
        epochSeconds: Double?,
        now: Instant = Instant.now(),
        zone: ZoneId = ZoneId.systemDefault(),
        locale: Locale = Locale.getDefault(),
    ): String? {
        if (epochSeconds == null || epochSeconds <= 0) return null
        return time(Instant.ofEpochMilli((epochSeconds * 1000).toLong()), now, zone, locale)
    }

    fun time(
        instant: Instant?,
        now: Instant = Instant.now(),
        zone: ZoneId = ZoneId.systemDefault(),
        locale: Locale = Locale.getDefault(),
    ): String? {
        val moment = instant?.atZone(zone) ?: return null
        val today = now.atZone(zone).toLocalDate()
        val day = moment.toLocalDate()
        val daysAgo = ChronoUnit.DAYS.between(day, today)
        return when {
            daysAgo <= 0L -> moment.format(DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withLocale(locale))
            daysAgo == 1L -> "Yesterday"
            daysAgo < 7L -> day.dayOfWeek.getDisplayName(TextStyle.SHORT, locale)
            else -> moment.format(DateTimeFormatter.ofPattern("MMM d", locale))
        }
    }

    private fun humanize(state: String): String =
        state.replace('_', ' ').replace('-', ' ').trim().replaceFirstChar { it.uppercase() }
}
