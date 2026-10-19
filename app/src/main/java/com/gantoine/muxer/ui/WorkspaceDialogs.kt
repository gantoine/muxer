package com.gantoine.muxer.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.AddToHomeScreen
import androidx.compose.material.icons.automirrored.filled.Notes
import androidx.compose.material.icons.automirrored.filled.OpenInNew
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Checklist
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.CreateNewFolder
import androidx.compose.material.icons.filled.DriveFileRenameOutline
import androidx.compose.material.icons.filled.Flag
import androidx.compose.material.icons.filled.FolderOff
import androidx.compose.material.icons.filled.MarkEmailRead
import androidx.compose.material.icons.filled.MarkEmailUnread
import androidx.compose.material.icons.filled.Palette
import androidx.compose.material.icons.filled.PushPin
import androidx.compose.material.icons.filled.RocketLaunch
import androidx.compose.material.icons.filled.Sync
import androidx.compose.material.icons.filled.Terminal
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.model.Organizing
import com.gantoine.muxer.model.WorkspaceListing
import com.gantoine.muxer.net.RelayConnection
import com.gantoine.muxer.protocol.CreatedWorkspace
import com.gantoine.muxer.protocol.WorkspaceGroupInfo
import com.gantoine.muxer.protocol.WorkspaceInfo

internal sealed interface WorkspaceDialog {
    data object New : WorkspaceDialog
    data object Create : WorkspaceDialog
    data class Actions(val workspace: WorkspaceInfo, val group: WorkspaceGroupInfo? = null) : WorkspaceDialog
    data class Rename(val workspace: WorkspaceInfo) : WorkspaceDialog
    data class Close(val workspace: WorkspaceInfo, val force: Boolean) : WorkspaceDialog
    data class PickTerminal(val workspace: WorkspaceInfo) : WorkspaceDialog
    data class Lane(val workspace: WorkspaceInfo) : WorkspaceDialog
    data class Color(val workspace: WorkspaceInfo) : WorkspaceDialog
    data class Describe(val workspace: WorkspaceInfo) : WorkspaceDialog
    data class PickGroup(val workspace: WorkspaceInfo) : WorkspaceDialog
    data class NewGroup(val workspace: WorkspaceInfo) : WorkspaceDialog
    data class GroupActions(val group: WorkspaceGroupInfo) : WorkspaceDialog
    data class RenameGroup(val group: WorkspaceGroupInfo) : WorkspaceDialog
    data class GroupColor(val group: WorkspaceGroupInfo) : WorkspaceDialog
    data class NewInGroup(val group: WorkspaceGroupInfo) : WorkspaceDialog
    data class Ungroup(val group: WorkspaceGroupInfo) : WorkspaceDialog
}

@Composable
internal fun WorkspaceDialogs(
    dialog: WorkspaceDialog?,
    connection: RelayConnection?,
    groups: List<WorkspaceGroupInfo>,
    canPinShortcuts: Boolean,
    onDialog: (WorkspaceDialog?) -> Unit,
    run: (suspend RelayConnection.() -> Result<*>) -> Unit,
    onOpen: (WorkspaceInfo) -> Unit,
    onNewTerminal: (WorkspaceInfo) -> Unit,
    onNewTask: () -> Unit,
    onOpenCreated: (CreatedWorkspace) -> Unit,
    onOpenTerminal: (workspaceId: String, surfaceId: String) -> Unit,
    onOpenChecklist: (workspaceId: String) -> Unit,
    onOpenPorts: (workspaceId: String, surfaceId: String) -> Unit,
    onPinShortcut: (WorkspaceInfo) -> Unit,
) {
    fun dismiss() = onDialog(null)

    when (dialog) {
        null -> Unit
        WorkspaceDialog.New -> ActionSheet(onDismiss = ::dismiss) {
            SheetAction(Icons.Filled.RocketLaunch, "New task") {
                dismiss()
                onNewTask()
            }
            SheetAction(Icons.Filled.Terminal, "New workspace") { onDialog(WorkspaceDialog.Create) }
        }
        WorkspaceDialog.Create -> TextInputDialog(
            title = "New workspace",
            fields = listOf("Name" to "", "Folder on the computer (optional)" to ""),
            confirm = "Create",
            onDismiss = ::dismiss,
        ) { (title, cwd) ->
            dismiss()
            run { createWorkspace(title, cwd).onSuccess(onOpenCreated) }
        }
        is WorkspaceDialog.Actions -> ActionSheet(
            onDismiss = ::dismiss,
            header = { WorkspaceHeader(dialog.workspace, dialog.group) },
        ) {
            val workspace = dialog.workspace
            SheetAction(Icons.AutoMirrored.Filled.OpenInNew, "Open") { dismiss(); onOpen(workspace) }
            val onlyTerminal = workspace.terminals.singleOrNull()
            if (onlyTerminal != null && onlyTerminal.listeningPorts.isNotEmpty() && connection?.opensPortsInTailscale == true) {
                OpenPortsAction {
                    dismiss()
                    onOpenPorts(workspace.id, onlyTerminal.id)
                }
            }
            SheetAction(Icons.Filled.Add, "New terminal") {
                dismiss()
                onNewTerminal(workspace)
            }
            if (canPinShortcuts && workspace.terminals.isNotEmpty()) {
                SheetAction(Icons.AutoMirrored.Filled.AddToHomeScreen, "Add to Home screen") {
                    dismiss()
                    onPinShortcut(workspace)
                }
            }
            OrganizeActions(
                workspace = workspace,
                groups = groups,
                onChecklist = {
                    dismiss()
                    onOpenChecklist(workspace.id)
                },
                onDialog = onDialog,
                onAction = { action ->
                    dismiss()
                    run { workspaceAction(workspace.id, action) }
                },
                onRemoveFromGroup = { group ->
                    dismiss()
                    run { removeFromGroup(group.id, workspace.id) }
                },
                onReconnect = {
                    dismiss()
                    run { reconnectWorkspace(workspace.id) }
                },
            )
            if (dialog.group == null) {
                SheetAction(Icons.Filled.DriveFileRenameOutline, "Rename") { onDialog(WorkspaceDialog.Rename(workspace)) }
                SheetAction(Icons.Filled.Close, "Close workspace", destructive = true) {
                    onDialog(WorkspaceDialog.Close(workspace, force = false))
                }
            }
        }
        is WorkspaceDialog.Lane -> LanePickerSheet(dialog.workspace.status, onDismiss = ::dismiss) { lane ->
            dismiss()
            run { setWorkspaceStatus(dialog.workspace.id, lane) }
        }
        is WorkspaceDialog.Color -> ColorPickerSheet("Workspace color", dialog.workspace.customColor, onDismiss = ::dismiss) { hex ->
            dismiss()
            run { workspaceAction(dialog.workspace.id, if (hex == null) "clear_color" else "set_color", color = hex) }
        }
        is WorkspaceDialog.Describe -> TextInputDialog(
            title = "Description",
            fields = listOf("Description" to dialog.workspace.description.orEmpty()),
            confirm = "Save",
            onDismiss = ::dismiss,
        ) { (description) ->
            dismiss()
            run { workspaceAction(dialog.workspace.id, "set_description", description = description) }
        }
        is WorkspaceDialog.PickGroup -> GroupPickerSheet(
            groups = groups,
            onDismiss = ::dismiss,
            onPick = { group ->
                dismiss()
                run { addToGroup(group.id, dialog.workspace.id) }
            },
            onNew = { onDialog(WorkspaceDialog.NewGroup(dialog.workspace)) },
        )
        is WorkspaceDialog.NewGroup -> TextInputDialog(
            title = "New group",
            fields = listOf("Name" to ""),
            confirm = "Create",
            onDismiss = ::dismiss,
        ) { (name) ->
            dismiss()
            run { createGroup(name, listOf(dialog.workspace.id)) }
        }
        is WorkspaceDialog.GroupActions -> ActionSheet(
            onDismiss = ::dismiss,
            header = { Text(dialog.group.name.ifBlank { "Group" }, style = MaterialTheme.typography.titleMedium) },
        ) {
            val group = dialog.group
            SheetAction(Icons.Filled.Add, "New workspace in group") { onDialog(WorkspaceDialog.NewInGroup(group)) }
            SheetAction(Icons.Filled.PushPin, if (group.isPinned) "Unpin group" else "Pin group") {
                dismiss()
                run { pinGroup(group.id, !group.isPinned) }
            }
            SheetAction(Icons.Filled.Palette, "Color") { onDialog(WorkspaceDialog.GroupColor(group)) }
            SheetAction(Icons.Filled.DriveFileRenameOutline, "Rename group") { onDialog(WorkspaceDialog.RenameGroup(group)) }
            SheetAction(Icons.Filled.FolderOff, "Ungroup", destructive = true) { onDialog(WorkspaceDialog.Ungroup(group)) }
        }
        is WorkspaceDialog.RenameGroup -> TextInputDialog(
            title = "Rename group",
            fields = listOf("Name" to dialog.group.name),
            confirm = "Rename",
            onDismiss = ::dismiss,
        ) { (name) ->
            dismiss()
            run { renameGroup(dialog.group.id, name) }
        }
        is WorkspaceDialog.GroupColor -> ColorPickerSheet("Group color", dialog.group.customColor, onDismiss = ::dismiss) { hex ->
            dismiss()
            run { setGroupColor(dialog.group.id, hex) }
        }
        is WorkspaceDialog.NewInGroup -> TextInputDialog(
            title = "New workspace in ${dialog.group.name.ifBlank { "group" }}",
            fields = listOf("Name (optional)" to ""),
            confirm = "Create",
            onDismiss = ::dismiss,
        ) { (title) ->
            dismiss()
            run { createWorkspaceInGroup(dialog.group.id, title).onSuccess(onOpenCreated) }
        }
        is WorkspaceDialog.Ungroup -> ConfirmDialog(
            title = "Ungroup ${dialog.group.name.ifBlank { "this group" }}?",
            text = "The workspaces stay open on your computer. Only the group goes away.",
            confirm = "Ungroup",
            onConfirm = {
                dismiss()
                run { ungroup(dialog.group.id) }
            },
            onDismiss = ::dismiss,
        )
        is WorkspaceDialog.Rename -> TextInputDialog(
            title = "Rename workspace",
            fields = listOf("Name" to dialog.workspace.title),
            confirm = "Rename",
            onDismiss = ::dismiss,
        ) { (title) ->
            dismiss()
            run { renameWorkspace(dialog.workspace.id, title) }
        }
        is WorkspaceDialog.Close -> CloseConfirmDialog(
            title = "Close ${dialog.workspace.title.ifBlank { "workspace" }}?",
            body = "This closes the workspace and its terminals on your computer.",
            force = dialog.force,
            onConfirm = { run { closeWorkspace(dialog.workspace.id, dialog.force).onSuccess { dismiss() } } },
            onDismiss = ::dismiss,
        )
        is WorkspaceDialog.PickTerminal -> ActionSheet(onDismiss = ::dismiss) {
            dialog.workspace.terminals.forEach { terminal ->
                TerminalChoice(terminal) {
                    dismiss()
                    onOpenTerminal(dialog.workspace.id, terminal.id)
                }
            }
        }
    }
}

@Composable
private fun OrganizeActions(
    workspace: WorkspaceInfo,
    groups: List<WorkspaceGroupInfo>,
    onChecklist: () -> Unit,
    onDialog: (WorkspaceDialog) -> Unit,
    onAction: (String) -> Unit,
    onRemoveFromGroup: (WorkspaceGroupInfo) -> Unit,
    onReconnect: () -> Unit,
) {
    val lane = workspace.status?.let(Organizing::laneSummary)
    SheetAction(Icons.Filled.Flag, lane?.let { "Status: $it" } ?: "Status") { onDialog(WorkspaceDialog.Lane(workspace)) }
    val progress = Organizing.checklistSummary(workspace.checklist)
    SheetAction(Icons.Filled.Checklist, progress?.let { "Checklist ($it)" } ?: "Checklist", onClick = onChecklist)
    SheetAction(Icons.Filled.PushPin, if (workspace.isPinned) "Unpin" else "Pin") { onAction(if (workspace.isPinned) "unpin" else "pin") }
    if (WorkspaceListing.isUnread(workspace)) {
        SheetAction(Icons.Filled.MarkEmailRead, "Mark as read") { onAction("mark_read") }
    } else {
        SheetAction(Icons.Filled.MarkEmailUnread, "Mark as unread") { onAction("mark_unread") }
    }
    SheetAction(Icons.Filled.Palette, Organizing.colorName(workspace.customColor)?.let { "Color: $it" } ?: "Color") {
        onDialog(WorkspaceDialog.Color(workspace))
    }
    SheetAction(Icons.AutoMirrored.Filled.Notes, if (workspace.description.isNullOrBlank()) "Add description" else "Edit description") {
        onDialog(WorkspaceDialog.Describe(workspace))
    }
    val group = Organizing.groupOf(workspace, groups)
    when {
        group == null -> SheetAction(Icons.Filled.CreateNewFolder, "Add to group") { onDialog(WorkspaceDialog.PickGroup(workspace)) }
        !Organizing.isAnchor(workspace, group) -> SheetAction(Icons.Filled.FolderOff, "Remove from ${group.name.ifBlank { "group" }}") {
            onRemoveFromGroup(group)
        }
    }
    if (Organizing.canReconnect(workspace.remote)) {
        SheetAction(Icons.Filled.Sync, "Reconnect") { onReconnect() }
    }
}

@Composable
private fun WorkspaceHeader(workspace: WorkspaceInfo, group: WorkspaceGroupInfo? = null) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        WorkspaceTile(workspace, size = 44.dp, color = group?.let { MaterialTheme.colorScheme.primaryContainer })
        Spacer(Modifier.width(16.dp))
        Column {
            Text(group?.let { WorkspaceListing.anchorTitle(it, workspace) } ?: workspace.title.ifBlank { "Untitled" }, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(
                workspace.currentDirectory?.let(WorkspaceListing::shortPath) ?: WorkspaceListing.subtitle(workspace),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}
