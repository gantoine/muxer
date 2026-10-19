package com.gantoine.muxer.model

import com.gantoine.muxer.protocol.RecentSession
import com.gantoine.muxer.protocol.WorkspaceInfo

object NewTask {
    val agents = listOf("claude", "codex")
    const val IN_WORKSPACE = "start_agent_task.workspace_id"
    const val SEARCHABLE_WORKSPACES = 6

    fun folders(workspaces: List<WorkspaceInfo>, sessions: List<RecentSession>): List<String> {
        val latest = LinkedHashMap<String, Double>()
        fun note(path: String?, at: Double) {
            val folder = path?.trim()?.trimEnd('/')?.takeIf { it.startsWith("/") && !isTemporary(it) } ?: return
            if (at > (latest[folder] ?: Double.NEGATIVE_INFINITY)) latest[folder] = at
        }
        workspaces.forEach { workspace ->
            val at = workspace.lastActivityAt ?: workspace.previewAt ?: 0.0
            note(workspace.currentDirectory, at)
            workspace.terminals.forEach { note(it.currentDirectory, at) }
        }
        sessions.forEach { session ->
            note(session.cwd, parseInstant(session.modified)?.epochSecond?.toDouble() ?: 0.0)
        }
        return latest.entries.sortedByDescending { it.value }.map { it.key }
    }

    private val temporaryRoots = listOf("/tmp", "/private/tmp", "/var/folders", "/private/var/folders")

    private fun isTemporary(folder: String): Boolean =
        temporaryRoots.any { folder == it || folder.startsWith("$it/") }

    fun suggestions(folders: List<String>, typed: String, limit: Int = 6): List<String> {
        val query = typed.trim().trimEnd('/')
        return folders
            .filter { folder ->
                val short = WorkspaceListing.shortPath(folder)
                query.isEmpty() || (folder != query && short != query && (folder.contains(query, ignoreCase = true) || short.contains(query, ignoreCase = true)))
            }
            .take(limit)
    }

    fun canStart(agent: String?, folder: String, prompt: String): Boolean =
        agent != null && folder.isNotBlank() && prompt.isNotBlank()

    fun canStartIn(agent: String?, workspace: WorkspaceInfo?, prompt: String): Boolean =
        agent != null && workspace != null && canHost(workspace) && prompt.isNotBlank()

    fun canHost(workspace: WorkspaceInfo): Boolean = workspace.remote == null

    fun workspaceChoices(workspaces: List<WorkspaceInfo>, query: String): List<WorkspaceInfo> {
        val needle = query.trim().trimEnd('/')
        return workspaces
            .filter(::canHost)
            .filter { workspace ->
                val folder = workspace.currentDirectory.orEmpty()
                needle.isEmpty() ||
                    workspace.title.contains(needle, ignoreCase = true) ||
                    folder.contains(needle, ignoreCase = true) ||
                    WorkspaceListing.shortPath(folder).contains(needle, ignoreCase = true)
            }
            .sortedByDescending { it.lastActivityAt ?: it.previewAt ?: 0.0 }
    }
}
