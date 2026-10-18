package com.gantoine.muxer.ui

import android.net.Uri
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateContentSize
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.StartOffset
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.ArrowDownward
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material.icons.filled.ExpandLess
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material.icons.filled.RadioButtonChecked
import androidx.compose.material.icons.filled.RadioButtonUnchecked
import androidx.compose.material.icons.filled.Stop
import androidx.compose.material.icons.filled.Terminal
import androidx.compose.material.icons.outlined.Checklist
import androidx.compose.material.icons.outlined.Lightbulb
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilledIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SmallFloatingActionButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.chat.ChatModel
import com.gantoine.muxer.chat.ChatState
import com.gantoine.muxer.images.RemoteImages
import com.gantoine.muxer.model.AgentStatus
import com.gantoine.muxer.model.WorkspaceListing
import com.gantoine.muxer.protocol.AgentMessage
import com.gantoine.muxer.protocol.ChatEntry
import com.gantoine.muxer.protocol.ChatTodo
import com.gantoine.muxer.protocol.ChatTool
import kotlin.math.roundToInt
import kotlinx.coroutines.launch

private const val AUTO_EXPANDED_DIFF_LINES = 20
private val TimeRevealWidth = 76.dp

@Composable
fun ChatView(
    chat: ChatState,
    agentName: String,
    status: AgentStatus?,
    midTurn: Boolean,
    answeringHere: Boolean,
    agentMessages: List<AgentMessage>,
    messagingAgent: Boolean,
    waitingOnYou: Boolean,
    showComposer: Boolean,
    draft: String,
    onDraftChange: (String) -> Unit,
    onSend: () -> Unit,
    onDictate: () -> Unit,
    dictating: Boolean,
    images: List<Uri>,
    sendingImages: Boolean,
    onPickPhotos: () -> Unit,
    onTakePhoto: () -> Unit,
    onRemoveImage: (Uri) -> Unit,
    fileSuggestions: List<String>,
    onPickFile: (String) -> Unit,
    onStop: () -> Unit,
    onShowTerminal: () -> Unit,
    chatImages: RemoteImages,
    modifier: Modifier = Modifier,
    overlay: @Composable () -> Unit = {},
) {
    val working = midTurn || status is AgentStatus.Working
    val thinking = working && chat.loaded && chat.entries.lastOrNull()?.tool?.status != "running"
    val listState = rememberLazyListState()
    val scope = rememberCoroutineScope()
    val scrolledAway by remember { derivedStateOf { listState.firstVisibleItemIndex > 1 } }
    val turnTexts = remember(chat.entries) { ChatModel.turnTexts(chat.entries) }
    val lastEntryId = chat.entries.lastOrNull()?.id
    val latestTodosId = chat.entries.lastOrNull { it.tool?.todos?.isNotEmpty() == true }?.id
    val reveal = remember { Animatable(0f) }
    val maxReveal = with(LocalDensity.current) { TimeRevealWidth.toPx() }
    var viewing by remember { mutableStateOf<String?>(null) }
    viewing?.let { id -> ImageViewer(id, chatImages, onDismiss = { viewing = null }) }
    var overlayHeight by remember { mutableIntStateOf(0) }
    val overlayInset = with(LocalDensity.current) { overlayHeight.toDp() }
    val itemCount = chat.entries.size + if (thinking) 1 else 0
    var previousItemCount by remember { mutableIntStateOf(itemCount) }
    LaunchedEffect(chat.entries.lastOrNull()?.id, thinking) {
        val added = (itemCount - previousItemCount).coerceAtLeast(1)
        previousItemCount = itemCount
        if (listState.firstVisibleItemIndex <= added) listState.animateScrollToItem(0)
    }
    Column(modifier) {
        Box(Modifier.weight(1f).fillMaxWidth()) {
            when {
                !chat.loaded -> FullWidthMessage("Loading the conversation…")
                chat.entries.isEmpty() && !thinking -> FullWidthMessage("Send a message to start talking to $agentName.")
                else -> LazyColumn(
                    Modifier.fillMaxSize().pointerInput(maxReveal) {
                        val settle: () -> Unit = { scope.launch { reveal.animateTo(0f) } }
                        detectHorizontalDragGestures(onDragEnd = settle, onDragCancel = settle) { change, dragAmount ->
                            change.consume()
                            scope.launch { reveal.snapTo((reveal.value - dragAmount).coerceIn(0f, maxReveal)) }
                        }
                    },
                    state = listState,
                    reverseLayout = true,
                    contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 12.dp, bottom = 12.dp + overlayInset),
                    verticalArrangement = Arrangement.spacedBy(10.dp, Alignment.Bottom),
                ) {
                    if (thinking) item(key = "thinking") { ThinkingIndicator() }
                    items(chat.entries.asReversed(), key = { it.id }) { entry ->
                        val copyText = turnTexts[entry.id]?.takeUnless { working && entry.id == lastEntryId }
                        RevealsTime(entry.time, reveal = { reveal.value }, maxReveal) {
                            ChatItem(entry, working, copyText, entry.id == latestTodosId, chatImages, onOpenImage = { viewing = it })
                        }
                    }
                }
            }
            JumpToLatestButton(
                visible = scrolledAway,
                onClick = { scope.launch { listState.animateScrollToItem(0) } },
                modifier = Modifier.align(Alignment.BottomEnd).padding(end = 16.dp, bottom = 12.dp + overlayInset),
            )
            Box(
                Modifier
                    .align(Alignment.BottomCenter)
                    .fillMaxWidth()
                    .onSizeChanged { overlayHeight = it.height },
            ) { overlay() }
        }
        AgentMessageReceipts(agentMessages, waitingOnYou)
        AnimatedVisibility(visible = !answeringHere && WorkspaceListing.needsTerminalAnswer(status)) {
            NeedsYouCard(status?.label.orEmpty().ifBlank { "${agentName.replaceFirstChar { it.uppercase() }} needs your answer" }, onShowTerminal)
        }
        if (!showComposer) {
            Spacer(Modifier.navigationBarsPadding())
            return@Column
        }
        FileSuggestions(fileSuggestions, onPick = onPickFile)
        ChatComposer(
            agentName = agentName,
            draft = draft,
            onDraftChange = onDraftChange,
            working = working,
            messagingAgent = messagingAgent,
            images = images,
            sendingImages = sendingImages,
            onPickPhotos = onPickPhotos,
            onTakePhoto = onTakePhoto,
            onRemoveImage = onRemoveImage,
            onSend = onSend,
            onDictate = onDictate,
            dictating = dictating,
            onStop = onStop,
        )
    }
}

@Composable
private fun JumpToLatestButton(visible: Boolean, onClick: () -> Unit, modifier: Modifier) {
    AnimatedVisibility(visible = visible, enter = fadeIn(), exit = fadeOut(), modifier = modifier) {
        SmallFloatingActionButton(onClick = onClick, containerColor = MaterialTheme.colorScheme.surfaceContainerHighest) {
            Icon(Icons.Filled.ArrowDownward, contentDescription = "Jump to the latest message")
        }
    }
}

@Composable
private fun RevealsTime(iso: String?, reveal: () -> Float, maxReveal: Float, content: @Composable () -> Unit) {
    val time = remember(iso) { ChatModel.time(iso) }
    Box(Modifier.fillMaxWidth()) {
        Box(Modifier.offset { IntOffset(-reveal().roundToInt(), 0) }) { content() }
        if (time != null) {
            Column(
                horizontalAlignment = Alignment.End,
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .width(TimeRevealWidth)
                    .offset { IntOffset((maxReveal - reveal()).roundToInt(), 0) }
                    .graphicsLayer { alpha = reveal() / maxReveal },
            ) {
                time.day?.let { Text(it, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                Text(time.time, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}

@Composable
private fun ChatItem(
    entry: ChatEntry,
    working: Boolean,
    copyText: String?,
    latestTodos: Boolean,
    images: RemoteImages,
    onOpenImage: (String) -> Unit,
) {
    when (entry.kind) {
        "user" -> UserMessage(entry.text.orEmpty(), entry.images, images, onOpenImage)
        "assistant" -> Column {
            MarkdownText(entry.text.orEmpty())
            if (copyText != null) CopyButton(copyText, "Copy the reply", Modifier.padding(top = 2.dp))
        }
        "thinking" -> ThinkingBlock(entry.text.orEmpty())
        "tool" -> entry.tool?.let { tool ->
            if (tool.todos.isNotEmpty()) TodoCard(tool.todos, working && latestTodos, initiallyExpanded = latestTodos)
            else ToolCall(tool, working, images, onOpenImage)
        }
        "notice" -> Notice(entry.text.orEmpty())
    }
}

@Composable
private fun ThinkingBlock(text: String) {
    var expanded by remember { mutableStateOf(false) }
    Column(Modifier.fillMaxWidth().animateContentSize()) {
        Row(
            Modifier.clip(RoundedCornerShape(8.dp)).clickable { expanded = !expanded }.padding(vertical = 4.dp, horizontal = 2.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(Icons.Outlined.Lightbulb, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.size(16.dp))
            Spacer(Modifier.size(8.dp))
            Text("Thinking", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Icon(
                if (expanded) Icons.Filled.ExpandLess else Icons.Filled.ExpandMore,
                contentDescription = if (expanded) "Hide the thinking" else "Show the thinking",
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(18.dp),
            )
        }
        if (expanded) {
            SelectionContainer {
                Text(
                    text,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(start = 26.dp, top = 4.dp),
                )
            }
        }
    }
}

@Composable
private fun ThinkingIndicator() {
    val transition = rememberInfiniteTransition(label = "thinking")
    Row(
        Modifier.fillMaxWidth().padding(vertical = 6.dp),
        horizontalArrangement = Arrangement.spacedBy(6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        repeat(3) { index ->
            val alpha by transition.animateFloat(
                initialValue = 0.25f,
                targetValue = 1f,
                animationSpec = infiniteRepeatable(
                    animation = tween(durationMillis = 600),
                    repeatMode = RepeatMode.Reverse,
                    initialStartOffset = StartOffset(index * 200),
                ),
                label = "dot$index",
            )
            Box(
                Modifier
                    .size(8.dp)
                    .alpha(alpha)
                    .background(MaterialTheme.colorScheme.onSurfaceVariant, CircleShape),
            )
        }
    }
}

@Composable
private fun UserMessage(text: String, imageIds: List<String>, images: RemoteImages, onOpenImage: (String) -> Unit) {
    Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.End, verticalArrangement = Arrangement.spacedBy(6.dp)) {
        if (imageIds.isNotEmpty()) ChatImageTiles(imageIds, images, onOpenImage)
        if (text.isNotBlank()) {
            Surface(
                shape = RoundedCornerShape(20.dp, 20.dp, 6.dp, 20.dp),
                color = MaterialTheme.colorScheme.primaryContainer,
                contentColor = MaterialTheme.colorScheme.onPrimaryContainer,
                modifier = Modifier.widthIn(max = 320.dp),
            ) {
                SelectionContainer {
                    Text(text, style = MaterialTheme.typography.bodyLarge, modifier = Modifier.padding(horizontal = 14.dp, vertical = 10.dp))
                }
            }
        }
    }
}

@Composable
private fun ToolCall(tool: ChatTool, working: Boolean, images: RemoteImages, onOpenImage: (String) -> Unit) {
    val diff = tool.diff
    var expanded by remember(diff.isNotEmpty()) { mutableStateOf(diff.sumOf { it.lines.size } in 1..AUTO_EXPANDED_DIFF_LINES) }
    val output = tool.output?.takeIf { it.isNotBlank() }
    Surface(
        shape = RoundedCornerShape(14.dp),
        color = MaterialTheme.colorScheme.surfaceContainer,
        modifier = Modifier.fillMaxWidth().clickable(enabled = output != null || diff.isNotEmpty()) { expanded = !expanded },
    ) {
        Column(Modifier.padding(horizontal = 12.dp, vertical = 10.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                ToolStatusIcon(tool.status, working)
                Spacer(Modifier.size(10.dp))
                Text(tool.name, style = MaterialTheme.typography.labelLarge)
                tool.summary?.let { summary ->
                    Spacer(Modifier.size(8.dp))
                    Text(
                        summary,
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false),
                    )
                }
                if (diff.isNotEmpty()) {
                    Spacer(Modifier.size(8.dp))
                    DiffCountsLabel(diff)
                }
            }
            if (tool.images.isNotEmpty()) {
                Box(Modifier.padding(top = 8.dp)) { ChatImageTiles(tool.images, images, onOpenImage) }
            }
            if (expanded && diff.isNotEmpty()) {
                DiffView(diff, Modifier.padding(top = 8.dp).fillMaxWidth())
            } else if (expanded && output != null) {
                SelectionContainer {
                    Text(
                        output,
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(top = 8.dp),
                    )
                }
            }
        }
    }
}

@Composable
private fun TodoCard(todos: List<ChatTodo>, working: Boolean, initiallyExpanded: Boolean) {
    var expanded by remember(initiallyExpanded) { mutableStateOf(initiallyExpanded) }
    val done = todos.count { it.status == "completed" }
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    Surface(
        shape = RoundedCornerShape(14.dp),
        color = MaterialTheme.colorScheme.surfaceContainer,
        modifier = Modifier.fillMaxWidth().clickable { expanded = !expanded },
    ) {
        Column(Modifier.padding(horizontal = 12.dp, vertical = 10.dp).animateContentSize()) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.Checklist, contentDescription = null, tint = muted, modifier = Modifier.size(16.dp))
                Spacer(Modifier.size(10.dp))
                Text("To-dos", style = MaterialTheme.typography.labelLarge, modifier = Modifier.weight(1f))
                Text("$done of ${todos.size} done", style = MaterialTheme.typography.bodySmall, color = muted)
                Icon(
                    if (expanded) Icons.Filled.ExpandLess else Icons.Filled.ExpandMore,
                    contentDescription = if (expanded) "Hide the to-dos" else "Show the to-dos",
                    tint = muted,
                    modifier = Modifier.padding(start = 4.dp).size(18.dp),
                )
            }
            if (expanded) {
                Column(Modifier.padding(top = 10.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    for (todo in todos) TodoRow(todo, working)
                }
            }
        }
    }
}

@Composable
private fun TodoRow(todo: ChatTodo, working: Boolean) {
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    val iconSize = Modifier.size(18.dp)
    Row(verticalAlignment = Alignment.Top) {
        Box(Modifier.padding(top = 1.dp)) {
            when (todo.status) {
                "completed" -> Icon(Icons.Filled.CheckCircle, contentDescription = "Done", tint = BrandGreen, modifier = iconSize)
                "in_progress" -> if (working) {
                    CircularProgressIndicator(strokeWidth = 2.dp, modifier = iconSize.padding(2.dp))
                } else {
                    Icon(Icons.Filled.RadioButtonChecked, contentDescription = "In progress", tint = MaterialTheme.colorScheme.primary, modifier = iconSize)
                }
                else -> Icon(Icons.Filled.RadioButtonUnchecked, contentDescription = "To do", tint = muted, modifier = iconSize)
            }
        }
        Spacer(Modifier.size(10.dp))
        when (todo.status) {
            "completed" -> Text(todo.content, style = MaterialTheme.typography.bodyMedium, color = muted, textDecoration = TextDecoration.LineThrough)
            "in_progress" -> Text(todo.activeForm?.takeIf { it.isNotBlank() } ?: todo.content, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold)
            else -> Text(todo.content, style = MaterialTheme.typography.bodyMedium)
        }
    }
}

@Composable
private fun ToolStatusIcon(status: String, working: Boolean) {
    val size = Modifier.size(16.dp)
    when {
        status == "failed" -> Icon(Icons.Filled.ErrorOutline, contentDescription = "Failed", tint = MaterialTheme.colorScheme.error, modifier = size)
        status == "done" -> Icon(Icons.Filled.Check, contentDescription = "Done", tint = BrandGreen, modifier = size)
        working -> CircularProgressIndicator(strokeWidth = 2.dp, modifier = size)
        else -> Box(size.padding(4.dp).background(MaterialTheme.colorScheme.outline, CircleShape))
    }
}

@Composable
private fun Notice(text: String) {
    var expanded by remember { mutableStateOf(false) }
    Text(
        text,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
        maxLines = if (expanded) Int.MAX_VALUE else 4,
        overflow = TextOverflow.Ellipsis,
        modifier = Modifier.fillMaxWidth().clickable { expanded = !expanded },
    )
}

@Composable
private fun NeedsYouCard(label: String, onShowTerminal: () -> Unit) {
    Surface(
        shape = RoundedCornerShape(20.dp),
        color = MaterialTheme.colorScheme.tertiaryContainer,
        contentColor = MaterialTheme.colorScheme.onTertiaryContainer,
        modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 4.dp),
    ) {
        Row(Modifier.padding(start = 16.dp, end = 8.dp, top = 6.dp, bottom = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(label, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.weight(1f))
            TextButton(onClick = onShowTerminal) {
                Icon(Icons.Filled.Terminal, contentDescription = null, Modifier.size(18.dp))
                Spacer(Modifier.size(6.dp))
                Text("Answer")
            }
        }
    }
}

@Composable
private fun ChatComposer(
    agentName: String,
    draft: String,
    onDraftChange: (String) -> Unit,
    working: Boolean,
    messagingAgent: Boolean,
    images: List<Uri>,
    sendingImages: Boolean,
    onPickPhotos: () -> Unit,
    onTakePhoto: () -> Unit,
    onRemoveImage: (Uri) -> Unit,
    onSend: () -> Unit,
    onDictate: () -> Unit,
    dictating: Boolean,
    onStop: () -> Unit,
) {
    var field by rememberDraftField(draft)
    Column(Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surface).navigationBarsPadding()) {
        AttachedImages(images, onRemove = onRemoveImage)
        Row(
            Modifier
                .fillMaxWidth()
                .padding(start = 12.dp, end = 6.dp, top = 8.dp, bottom = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            OutlinedTextField(
                value = field,
                onValueChange = {
                    field = it
                    onDraftChange(it.text)
                },
                placeholder = { Text(if (messagingAgent) "Send message after you answer" else "Message $agentName") },
                leadingIcon = { AttachImageButton(enabled = !sendingImages, onPickPhotos = onPickPhotos, onTakePhoto = onTakePhoto) },
                maxLines = 6,
                shape = ComposerShape,
                keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences, autoCorrectEnabled = true),
                modifier = Modifier.weight(1f),
            )
            DictateButton(dictating, onClick = onDictate)
            when {
                sendingImages -> FilledIconButton(onClick = {}, enabled = false) {
                    CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
                }
                draft.isNotBlank() || images.isNotEmpty() -> FilledIconButton(onClick = onSend) {
                    Icon(Icons.AutoMirrored.Filled.Send, contentDescription = "Send the message")
                }
                working -> FilledIconButton(onClick = onStop) {
                    Icon(Icons.Filled.Stop, contentDescription = "Stop $agentName")
                }
                else -> FilledIconButton(onClick = onSend, enabled = false) {
                    Icon(Icons.AutoMirrored.Filled.Send, contentDescription = "Send the message")
                }
            }
        }
    }
}
