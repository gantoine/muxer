package com.gantoine.muxer.ui

import androidx.compose.foundation.layout.Arrangement
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
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.AutoAwesome
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.DeleteSweep
import androidx.compose.material.icons.filled.DriveFileRenameOutline
import androidx.compose.material.icons.filled.HourglassTop
import androidx.compose.material.icons.filled.RadioButtonUnchecked
import androidx.compose.material.icons.filled.TaskAlt
import androidx.compose.material.icons.outlined.Checklist
import androidx.compose.material3.Checkbox
import androidx.compose.material3.FilledIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
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
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.net.ConnectionState
import com.gantoine.muxer.net.RelayConnection
import com.gantoine.muxer.protocol.Checklist
import com.gantoine.muxer.protocol.ChecklistItem
import kotlinx.coroutines.launch

private sealed interface ChecklistDialog {
    data class Actions(val item: ChecklistItem) : ChecklistDialog
    data class Edit(val item: ChecklistItem) : ChecklistDialog
    data object Clear : ChecklistDialog
}

@Composable
fun ChecklistScreen(app: MuxerApplication, macId: String, workspaceId: String, onBack: () -> Unit) {
    val connection = rememberConnection(app, macId)
    val state by connection.collectOr(ConnectionState.Stopped) { it.state }
    val workspaces by connection.collectOr(null) { it.workspaces }
    val macName = rememberMacName(app, macId)
    val workspaceTitle = workspaces?.firstOrNull { it.id == workspaceId }?.title?.ifBlank { null }
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    var checklist by remember { mutableStateOf<Checklist?>(null) }
    var loadFailure by remember { mutableStateOf<Throwable?>(null) }
    var draft by rememberSaveable { mutableStateOf("") }
    var dialog by remember { mutableStateOf<ChecklistDialog?>(null) }

    fun change(action: suspend RelayConnection.() -> Result<Checklist>, onSuccess: () -> Unit = {}) {
        val target = connection ?: return
        scope.launch {
            target.action()
                .onSuccess {
                    checklist = it
                    onSuccess()
                }
                .onFailure { snackbar.showSnackbar(it.message ?: "That didn't work.") }
        }
    }

    fun add() {
        val text = draft.trim()
        if (text.isEmpty()) return
        change({ addChecklistItem(workspaceId, text) }, onSuccess = { draft = "" })
    }

    suspend fun load() {
        val target = connection?.takeIf { it.supports(REQUEST) } ?: return
        target.checklist(workspaceId)
            .onSuccess {
                checklist = it
                loadFailure = null
            }
            .onFailure { loadFailure = it }
    }

    LaunchedEffect(connection, state is ConnectionState.Connected) {
        if (state is ConnectionState.Connected) load()
    }

    Scaffold(
        topBar = {
            CmuxTopBar(
                title = workspaceTitle?.let { "$it checklist" } ?: "Checklist",
                onBack = onBack,
                actions = {
                    if (checklist?.items.orEmpty().isNotEmpty()) {
                        IconButton(onClick = { dialog = ChecklistDialog.Clear }) {
                            Icon(Icons.Filled.DeleteSweep, contentDescription = "Clear the checklist")
                        }
                    }
                },
            )
        },
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize().imePadding()) {
            RemoteContent(
                value = checklist,
                failure = loadFailure,
                state = state,
                requestType = REQUEST,
                icon = Icons.Outlined.Checklist,
                what = "show checklists",
                failedTitle = "Couldn't load the checklist",
                loading = "Fetching the checklist…",
                macName = macName,
                onRetry = {
                    loadFailure = null
                    scope.launch { load() }
                },
            ) { list ->
                Column {
                    ConnectionBanner(state)
                    Row(
                        Modifier.fillMaxWidth().padding(start = 16.dp, end = 16.dp, top = 8.dp, bottom = 8.dp),
                        verticalAlignment = Alignment.Bottom,
                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        LabeledTextField(
                            value = draft,
                            onValueChange = { draft = it },
                            label = "Add an item",
                            singleLine = true,
                            keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences, imeAction = ImeAction.Done),
                            keyboardActions = KeyboardActions(onDone = { add() }),
                            modifier = Modifier.weight(1f),
                        )
                        FilledIconButton(onClick = ::add, enabled = draft.isNotBlank(), modifier = Modifier.size(56.dp)) {
                            Icon(Icons.Filled.Add, contentDescription = "Add the item")
                        }
                    }
                    if (list.items.isEmpty()) {
                        EmptyState(
                            icon = Icons.Outlined.Checklist,
                            title = "Nothing to do yet",
                            body = "Items added here or by an agent show up in cmux's checklist for this workspace.",
                        )
                    } else {
                        Text(
                            "${list.progress.completed} of ${list.progress.total} done",
                            style = MaterialTheme.typography.labelLarge,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
                        )
                        LazyColumn(
                            Modifier.fillMaxSize(),
                            contentPadding = PaddingValues(bottom = 32.dp),
                            verticalArrangement = Arrangement.spacedBy(2.dp),
                        ) {
                            itemsIndexed(list.items, key = { _, item -> item.id }) { index, item ->
                                ChecklistRow(
                                    item,
                                    index,
                                    list.items.size,
                                    onToggle = {
                                        val next = if (item.state == "completed") "pending" else "completed"
                                        change({ setChecklistItemState(workspaceId, item.id, next) })
                                    },
                                    onLongPress = { dialog = ChecklistDialog.Actions(item) },
                                )
                            }
                        }
                    }
                }
            }
        }
    }

    when (val current = dialog) {
        null -> Unit
        is ChecklistDialog.Actions -> ActionSheet(
            onDismiss = { dialog = null },
            header = { Text(current.item.text, style = MaterialTheme.typography.titleMedium, maxLines = 2) },
        ) {
            listOf(
                Triple("pending", Icons.Filled.RadioButtonUnchecked, "Mark as not started"),
                Triple("in-progress", Icons.Filled.HourglassTop, "Mark as in progress"),
                Triple("completed", Icons.Filled.TaskAlt, "Mark as done"),
            ).filter { it.first != current.item.state }.forEach { (state, icon, label) ->
                SheetAction(icon, label) {
                    dialog = null
                    change({ setChecklistItemState(workspaceId, current.item.id, state) })
                }
            }
            SheetAction(Icons.Filled.DriveFileRenameOutline, "Edit") { dialog = ChecklistDialog.Edit(current.item) }
            SheetAction(Icons.Filled.Delete, "Remove", destructive = true) {
                dialog = null
                change({ removeChecklistItem(workspaceId, current.item.id) })
            }
        }
        is ChecklistDialog.Edit -> TextInputDialog(
            title = "Edit item",
            fields = listOf("Item" to current.item.text),
            confirm = "Save",
            onDismiss = { dialog = null },
        ) { (text) ->
            dialog = null
            if (text.isNotBlank()) change({ editChecklistItem(workspaceId, current.item.id, text) })
        }
        ChecklistDialog.Clear -> ConfirmDialog(
            title = "Clear the checklist?",
            text = "This removes every item, done or not, from the workspace's checklist on your computer.",
            confirm = "Clear",
            onConfirm = {
                dialog = null
                change({ clearChecklist(workspaceId) })
            },
            onDismiss = { dialog = null },
        )
    }
}

private const val REQUEST = "checklist"

@Composable
private fun ChecklistRow(item: ChecklistItem, index: Int, count: Int, onToggle: () -> Unit, onLongPress: () -> Unit) {
    val done = item.state == "completed"
    Row(
        Modifier.groupedRow(
            groupedShape(index, count),
            onClick = onToggle,
            onLongClick = onLongPress,
            onLongClickLabel = "More for this item",
            contentPadding = PaddingValues(start = 4.dp, end = 16.dp, top = 4.dp, bottom = 4.dp),
        ),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Checkbox(checked = done, onCheckedChange = { onToggle() })
        Column(Modifier.weight(1f).padding(vertical = 8.dp)) {
            Text(
                item.text,
                style = MaterialTheme.typography.bodyLarge,
                color = if (done) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.onSurface,
                textDecoration = if (done) TextDecoration.LineThrough else null,
            )
            if (item.state == "in-progress") {
                Text("In progress", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.tertiary)
            }
        }
        if (item.origin == "agent") {
            Spacer(Modifier.width(8.dp))
            Icon(
                Icons.Filled.AutoAwesome,
                contentDescription = "Added by an agent",
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(18.dp),
            )
        }
    }
}
