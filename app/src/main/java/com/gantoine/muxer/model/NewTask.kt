package com.gantoine.muxer.model

import com.gantoine.muxer.protocol.RecentSession
import com.gantoine.muxer.protocol.WorkspaceInfo

object NewTask {
    val agents = listOf("claude", "codex")

    fun folders(workspaces: List<WorkspaceInfo>, sessions: List<RecentSession>): List<String> {
        val latest = LinkedHashMap<String, Double>()
        fun note(path: String?, at: Double) {
            val folder = path?.trim()?.trimEnd('/')?.takeIf { it.startsWith("/") } ?: return
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
}
