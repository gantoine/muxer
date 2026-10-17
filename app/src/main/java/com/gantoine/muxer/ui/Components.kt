package com.gantoine.muxer.ui

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Clear
import androidx.compose.material.icons.filled.CloudOff
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CenterAlignedTopAppBar
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExperimentalMaterial3ExpressiveApi
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextFieldColors
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.State
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.data.Mac
import com.gantoine.muxer.net.ConnectionState
import com.gantoine.muxer.net.RelayConnection
import com.gantoine.muxer.net.RelayException
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

val ComposerShape = RoundedCornerShape(28.dp)

fun groupedShape(index: Int, count: Int): Shape {
    val large = 24.dp
    val small = 6.dp
    val top = if (index == 0) large else small
    val bottom = if (index == count - 1) large else small
    return RoundedCornerShape(topStart = top, topEnd = top, bottomStart = bottom, bottomEnd = bottom)
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun Modifier.groupedRow(
    shape: Shape,
    onClick: (() -> Unit)? = null,
    onLongClick: (() -> Unit)? = null,
    onClickLabel: String? = null,
    onLongClickLabel: String? = null,
    enabled: Boolean = true,
    color: Color = MaterialTheme.colorScheme.surfaceContainer,
    contentPadding: PaddingValues = PaddingValues(horizontal = 16.dp, vertical = 14.dp),
): Modifier {
    val haptics = LocalHapticFeedback.current
    val clicks = when {
        onLongClick != null -> Modifier.combinedClickable(
            enabled = enabled,
            onClickLabel = onClickLabel,
            onClick = onClick ?: {},
            onLongClickLabel = onLongClickLabel,
            onLongClick = {
                haptics.performHapticFeedback(HapticFeedbackType.LongPress)
                onLongClick()
            },
        )
        onClick != null -> Modifier.clickable(enabled = enabled, onClickLabel = onClickLabel, onClick = onClick)
        else -> Modifier
    }
    return fillMaxWidth()
        .padding(horizontal = 16.dp)
        .clip(shape)
        .background(color)
        .then(clicks)
        .padding(contentPadding)
}

@Composable
fun rememberConnection(app: MuxerApplication, macId: String): RelayConnection? {
    val connections by app.connections.connectionsFlow.collectAsState()
    return connections[macId]
}

@Composable
fun <T> RelayConnection?.collectOr(fallback: T, select: (RelayConnection) -> StateFlow<T>): State<T> {
    val missing = remember { MutableStateFlow(fallback) }
    return (this?.let(select) ?: missing).collectAsState()
}

@Composable
fun rememberMacName(app: MuxerApplication, macId: String, fallback: String = "The computer"): String {
    val macs by app.store.macs.collectAsState(initial = emptyList())
    return macs.firstOrNull { it.id == macId }?.name ?: fallback
}

@Composable
fun rememberNow(key: Any?, periodMs: Long = 1_000L): Long {
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(key) {
        while (true) {
            now = System.currentTimeMillis()
            delay(periodMs)
        }
    }
    return now
}

@Composable
fun <T> PollConnectedMacs(
    app: MuxerApplication,
    periodMs: Long,
    fetch: suspend (RelayConnection) -> Result<T>,
    onResults: (connected: Set<String>, results: List<Pair<String, Result<T>>>) -> Unit,
) {
    val connectedIds by app.connections.connectedMacIds.collectAsState(initial = emptySet())
    val connections by app.connections.connectionsFlow.collectAsState()
    val currentConnections by rememberUpdatedState(connections)
    val currentFetch by rememberUpdatedState(fetch)
    val currentOnResults by rememberUpdatedState(onResults)
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(connectedIds) {
        currentOnResults(connectedIds, emptyList())
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                val results = coroutineScope {
                    connectedIds.mapNotNull { id -> currentConnections[id]?.let { id to it } }
                        .map { (id, connection) -> async { id to currentFetch(connection) } }
                        .awaitAll()
                }
                currentOnResults(connectedIds, results)
                delay(periodMs)
            }
        }
    }
}

@Composable
fun <T> RemoteContent(
    value: T?,
    failure: Throwable?,
    state: ConnectionState,
    requestType: String,
    icon: ImageVector,
    what: String,
    failedTitle: String,
    loading: String,
    macName: String,
    onRetry: () -> Unit,
    content: @Composable (T) -> Unit,
) {
    val cmuxTooOld = (state as? ConnectionState.Connected)?.supports(requestType) == false
    when {
        value != null -> content(value)
        failure is RelayException && failure.isUnknownType -> EmptyState(
            icon = icon,
            title = "Update the relay",
            body = "$macName's relay is too old to $what. Update it to see them here.",
        )
        cmuxTooOld || failure is RelayException && failure.isUnsupported -> EmptyState(
            icon = icon,
            title = "Update cmux",
            body = failure?.message ?: "This version of cmux can't $what.",
        )
        failure != null -> Column {
            ConnectionBanner(state)
            EmptyState(icon = icon, title = failedTitle, body = failure.message ?: "$macName didn't answer.") {
                Button(onClick = onRetry) { Text("Try again") }
            }
        }
        else -> Column {
            ConnectionBanner(state)
            LoadingState(loading)
        }
    }
}

@Composable
fun SectionLabel(text: String, modifier: Modifier = Modifier, top: Dp = 20.dp) {
    Text(
        text,
        style = MaterialTheme.typography.labelLarge,
        color = MaterialTheme.colorScheme.primary,
        modifier = modifier.padding(start = 28.dp, end = 28.dp, top = top, bottom = 8.dp),
    )
}

@Composable
fun EmptyState(
    icon: ImageVector,
    title: String,
    body: String,
    modifier: Modifier = Modifier,
    action: @Composable () -> Unit = {},
) {
    Column(
        modifier.fillMaxSize().padding(horizontal = 32.dp, vertical = 24.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Box(
            Modifier.size(96.dp).background(MaterialTheme.colorScheme.primaryContainer, RoundedCornerShape(32.dp)),
            contentAlignment = Alignment.Center,
        ) {
            Icon(icon, contentDescription = null, tint = MaterialTheme.colorScheme.onPrimaryContainer, modifier = Modifier.size(44.dp))
        }
        Spacer(Modifier.height(24.dp))
        Text(title, style = MaterialTheme.typography.headlineSmall, textAlign = TextAlign.Center)
        Spacer(Modifier.height(8.dp))
        Text(
            body,
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(24.dp))
        action()
    }
}

@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun LoadingState(text: String, modifier: Modifier = Modifier) {
    Column(
        modifier.fillMaxSize().padding(32.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        LoadingIndicator(Modifier.size(56.dp))
        Spacer(Modifier.height(16.dp))
        Text(text, style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun ConnectionBanner(state: ConnectionState, modifier: Modifier = Modifier) {
    if (state is ConnectionState.Connected) return
    val connecting = state == ConnectionState.Connecting
    val container = if (connecting) MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.errorContainer
    val content = if (connecting) MaterialTheme.colorScheme.onSecondaryContainer else MaterialTheme.colorScheme.onErrorContainer
    Surface(
        shape = RoundedCornerShape(20.dp),
        color = container,
        contentColor = content,
        modifier = modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp),
    ) {
        Row(Modifier.padding(horizontal = 16.dp, vertical = 12.dp), verticalAlignment = Alignment.CenterVertically) {
            if (connecting) {
                LoadingIndicator(Modifier.size(28.dp), color = content)
            } else {
                Icon(Icons.Filled.CloudOff, contentDescription = null, Modifier.size(24.dp))
            }
            Spacer(Modifier.width(14.dp))
            Text(describe(state), style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
        }
    }
}

@Composable
fun StatusDot(state: ConnectionState, modifier: Modifier = Modifier) {
    Box(modifier.size(10.dp).background(statusColor(state), CircleShape))
}

@Composable
fun statusColor(state: ConnectionState): Color = when (state) {
    is ConnectionState.Connected -> BrandGreen
    ConnectionState.Connecting -> MaterialTheme.colorScheme.tertiary
    is ConnectionState.Disconnected, ConnectionState.Unauthorized -> MaterialTheme.colorScheme.error
    ConnectionState.Stopped -> MaterialTheme.colorScheme.outline
}

fun describe(state: ConnectionState): String = when (state) {
    is ConnectionState.Connected -> "Connected · cmux ${state.welcome.cmuxVersion}"
    ConnectionState.Connecting -> "Connecting…"
    is ConnectionState.Disconnected -> "Offline · ${state.reason}"
    ConnectionState.Unauthorized -> "Not paired anymore. Remove it, then pair again."
    ConnectionState.Stopped -> "Idle"
}

@Composable
fun FullWidthMessage(text: String, modifier: Modifier = Modifier) {
    Box(modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) {
        Text(text, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center)
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ActionSheet(
    onDismiss: () -> Unit,
    header: (@Composable () -> Unit)? = null,
    actions: @Composable () -> Unit,
) {
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(bottom = 12.dp).navigationBarsPadding()) {
            if (header != null) {
                Box(Modifier.padding(horizontal = 16.dp, vertical = 8.dp)) { header() }
                Spacer(Modifier.height(8.dp))
            }
            actions()
        }
    }
}

@Composable
fun SheetAction(icon: ImageVector, label: String, destructive: Boolean = false, onClick: () -> Unit) {
    val tint = if (destructive) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface
    ListItem(
        headlineContent = { Text(label, color = tint) },
        leadingContent = { Icon(icon, contentDescription = null, tint = tint) },
        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
        modifier = Modifier.clickable(onClick = onClick),
    )
}

@OptIn(ExperimentalMaterial3Api::class, ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun CmuxTopBar(
    title: String,
    onBack: (() -> Unit)? = null,
    actions: @Composable RowScope.() -> Unit = {},
) {
    val background = MaterialTheme.colorScheme.background
    CenterAlignedTopAppBar(
        title = { Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis) },
        navigationIcon = {
            if (onBack != null) IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
        },
        actions = actions,
        colors = TopAppBarDefaults.centerAlignedTopAppBarColors(containerColor = background, scrolledContainerColor = background),
    )
}

@Composable
fun RemoveComputerDialog(mac: Mac, onDismiss: () -> Unit, onConfirm: () -> Unit) {
    ConfirmDialog(
        title = "Remove ${mac.name}?",
        text = "This phone forgets the computer. To also revoke its access on the computer, run `muxer devices` and `muxer revoke`.",
        confirm = "Remove",
        onConfirm = onConfirm,
        onDismiss = onDismiss,
    )
}

@Composable
fun ConfirmDialog(title: String, text: String, confirm: String, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(text) },
        confirmButton = { TextButton(onClick = onConfirm) { Text(confirm) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

@Composable
fun CloseConfirmDialog(title: String, body: String, force: Boolean, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    ConfirmDialog(
        title = title,
        text = if (force) "Something is still running in it. Closing stops it on your computer." else body,
        confirm = if (force) "Close anyway" else "Close",
        onConfirm = onConfirm,
        onDismiss = onDismiss,
    )
}

@Composable
fun TextInputDialog(
    title: String,
    fields: List<Pair<String, String>>,
    confirm: String,
    onDismiss: () -> Unit,
    maxLength: Int = Int.MAX_VALUE,
    canConfirm: (List<String>) -> Boolean = { true },
    onConfirm: (List<String>) -> Unit,
) {
    var values by remember { mutableStateOf(fields.map { it.second }) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                fields.forEachIndexed { index, (label, _) ->
                    LabeledTextField(
                        value = values[index],
                        onValueChange = { changed -> values = values.toMutableList().also { it[index] = changed.take(maxLength) } },
                        label = label,
                        singleLine = true,
                    )
                }
            }
        },
        confirmButton = {
            TextButton(onClick = { onConfirm(values.map { it.trim() }) }, enabled = canConfirm(values)) { Text(confirm) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

@Composable
fun SearchField(
    query: String,
    onQueryChange: (String) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
    colors: TextFieldColors = OutlinedTextFieldDefaults.colors(),
) {
    LabeledTextField(
        value = query,
        onValueChange = onQueryChange,
        label = label,
        singleLine = true,
        leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
        trailingIcon = {
            if (query.isNotEmpty()) {
                IconButton(onClick = { onQueryChange("") }) { Icon(Icons.Filled.Clear, contentDescription = "Clear search") }
            }
        },
        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
        colors = colors,
        modifier = modifier,
    )
}

@Composable
fun StatusAvatar(icon: ImageVector, container: Color, onContainer: Color, dot: Color, size: Dp = 52.dp) {
    Box(Modifier.size(size)) {
        Box(Modifier.fillMaxSize().background(container, CircleShape), contentAlignment = Alignment.Center) {
            Icon(icon, contentDescription = null, tint = onContainer, modifier = Modifier.size(size / 2))
        }
        Box(
            Modifier
                .align(Alignment.BottomEnd)
                .size(size * 0.3f)
                .border(2.dp, MaterialTheme.colorScheme.surfaceContainer, CircleShape)
                .padding(2.dp)
                .background(dot, CircleShape),
        )
    }
}

@Composable
fun rememberDraftField(draft: String): MutableState<TextFieldValue> {
    val field = remember { mutableStateOf(TextFieldValue(draft, TextRange(draft.length))) }
    if (field.value.text != draft) field.value = TextFieldValue(draft, TextRange(draft.length))
    return field
}
