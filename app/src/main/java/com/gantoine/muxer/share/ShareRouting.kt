package com.gantoine.muxer.share

import com.gantoine.muxer.data.MacSnapshot
import com.gantoine.muxer.notifications.ShortcutPlan
import com.gantoine.muxer.protocol.TerminalInfo
import com.gantoine.muxer.protocol.WorkspaceInfo

data class ShareTarget(
    val macId: String,
    val workspace: WorkspaceInfo,
    val surfaceId: String,
    val title: String,
    val agentName: String?,
    val agentTerminals: List<TerminalInfo> = emptyList(),
    val toLastUsedAgent: Boolean = false,
) {
    val needsTerminalChoice: Boolean get() = agentTerminals.size > 1
}

data class ShareComputer(
    val macId: String,
    val macName: String,
    val connected: Boolean,
    val targets: List<ShareTarget>,
)

sealed interface ShareRoute {
    data class Open(val target: ShareTarget) : ShareRoute
    data object Pick : ShareRoute
}

object ShareRouting {
    private const val SEND_ACTION = "android.intent.action.SEND"
    private const val SEND_MULTIPLE_ACTION = "android.intent.action.SEND_MULTIPLE"
    private const val TEXT_TYPE_PREFIX = "text/"
    private const val IMAGE_TYPE_PREFIX = "image/"

    fun sharedText(action: String?, mimeType: String?, text: CharSequence?): String? {
        if (action != SEND_ACTION) return null
        if (mimeType?.startsWith(TEXT_TYPE_PREFIX) != true) return null
        return text?.toString()?.trim()?.takeIf { it.isNotEmpty() }
    }

    fun isImageShare(action: String?, mimeType: String?): Boolean =
        (action == SEND_ACTION || action == SEND_MULTIPLE_ACTION) && mimeType?.startsWith(IMAGE_TYPE_PREFIX) == true

    fun caption(text: CharSequence?): String = text?.toString()?.trim().orEmpty()

    fun computers(snapshots: Collection<MacSnapshot>, pairedMacIds: Collection<String>): List<ShareComputer> =
        snapshots
            .filter { it.macId in pairedMacIds }
            .mapNotNull { snapshot ->
                val targets = byRecency(snapshot.workspaces.filter(ShortcutPlan::isShareTarget)).mapNotNull { target(snapshot, it) }
                if (targets.isEmpty()) null else ShareComputer(snapshot.macId, snapshot.macName, snapshot.connected, targets)
            }
            .sortedWith(compareByDescending<ShareComputer> { it.connected }.thenBy { it.macName.lowercase() })

    fun route(shortcutId: String?, snapshots: Collection<MacSnapshot>, pairedMacIds: Collection<String>): ShareRoute {
        shortcutId?.let { forShortcut(it, snapshots, pairedMacIds) }?.let { return ShareRoute.Open(it) }
        val only = computers(snapshots, pairedMacIds).flatMap { it.targets }.singleOrNull()
        return if (only != null && !only.needsTerminalChoice) ShareRoute.Open(only) else ShareRoute.Pick
    }

    private fun forShortcut(shortcutId: String, snapshots: Collection<MacSnapshot>, pairedMacIds: Collection<String>): ShareTarget? =
        snapshots
            .filter { it.macId in pairedMacIds }
            .firstNotNullOfOrNull { snapshot ->
                snapshot.workspaces
                    .firstOrNull { ShortcutPlan.id(snapshot.macId, it.id) == shortcutId }
                    ?.let { target(snapshot, it) }
                    ?.takeUnless { it.needsTerminalChoice && !it.toLastUsedAgent }
            }

    private fun target(snapshot: MacSnapshot, workspace: WorkspaceInfo): ShareTarget? {
        val shortcut = ShortcutPlan.shortcut(snapshot.macId, snapshot.macName, workspace) ?: return null
        val agentTerminals = workspace.terminals.filter { it.agentSource != null }
        return ShareTarget(snapshot.macId, workspace, shortcut.surfaceId, shortcut.title, shortcut.agentName, agentTerminals, shortcut.shareTarget)
    }

    private fun byRecency(workspaces: List<WorkspaceInfo>): List<WorkspaceInfo> =
        workspaces.sortedByDescending { it.lastActivityAt ?: Double.NEGATIVE_INFINITY }
}
