package com.gantoine.muxer.model

import com.gantoine.muxer.protocol.WorkspaceInfo

object NewTask {
    val agents = listOf("claude", "codex")
    const val IN_WORKSPACE = "start_agent_task.workspace_id"
    const val SEARCHABLE_WORKSPACES = 6

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
