package com.gantoine.muxer.ui

import android.net.Uri
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.StartOffset
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material.icons.filled.Stop
import androidx.compose.material.icons.filled.Terminal
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilledIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.chat.ChatMarkdown
import com.gantoine.muxer.chat.ChatState
import com.gantoine.muxer.images.RemoteImages
import com.gantoine.muxer.chat.InlineSpan
import com.gantoine.muxer.chat.MarkdownBlock
import com.gantoine.muxer.model.AgentStatus
import com.gantoine.muxer.model.WorkspaceListing
import com.gantoine.muxer.protocol.AgentMessage
import com.gantoine.muxer.protocol.ChatEntry
import com.gantoine.muxer.protocol.ChatTool

@Composable
fun ChatView(
    chat: ChatState,
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
    onAttachImages: () -> Unit,
    onRemoveImage: (Uri) -> Unit,
    onStop: () -> Unit,
    onShowTerminal: () -> Unit,
    chatImages: RemoteImages,
    modifier: Modifier = Modifier,
    overlay: @Composable () -> Unit = {},
) {
    val working = midTurn || status is AgentStatus.Working
    val thinking = working && chat.loaded && chat.entries.lastOrNull()?.tool?.status != "running"
    val listState = rememberLazyListState()
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
                chat.entries.isEmpty() && !thinking -> FullWidthMessage("Send a message to start talking to Claude.")
                else -> LazyColumn(
                    Modifier.fillMaxSize(),
                    state = listState,
                    reverseLayout = true,
                    contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 12.dp, bottom = 12.dp + overlayInset),
                    verticalArrangement = Arrangement.spacedBy(10.dp, Alignment.Bottom),
                ) {
                    if (thinking) item(key = "thinking") { ThinkingIndicator() }
                    items(chat.entries.asReversed(), key = { it.id }) { entry ->
                        ChatItem(entry, working, chatImages, onOpenImage = { viewing = it })
                    }
                }
            }
            Box(
                Modifier
                    .align(Alignment.BottomCenter)
                    .fillMaxWidth()
                    .onSizeChanged { overlayHeight = it.height },
            ) { overlay() }
        }
        AgentMessageReceipts(agentMessages, waitingOnYou)
        AnimatedVisibility(visible = !answeringHere && WorkspaceListing.needsTerminalAnswer(status)) {
            NeedsYouCard(status?.label.orEmpty(), onShowTerminal)
        }
        if (!showComposer) {
            Spacer(Modifier.navigationBarsPadding())
            return@Column
        }
        ChatComposer(
            draft = draft,
            onDraftChange = onDraftChange,
            working = working,
            messagingAgent = messagingAgent,
            images = images,
            sendingImages = sendingImages,
            onAttachImages = onAttachImages,
            onRemoveImage = onRemoveImage,
            onSend = onSend,
            onDictate = onDictate,
            dictating = dictating,
            onStop = onStop,
        )
    }
}

@Composable
private fun ChatItem(entry: ChatEntry, working: Boolean, images: RemoteImages, onOpenImage: (String) -> Unit) {
    when (entry.kind) {
        "user" -> UserMessage(entry.text.orEmpty(), entry.images, images, onOpenImage)
        "assistant" -> MarkdownText(entry.text.orEmpty())
        "tool" -> entry.tool?.let { ToolCall(it, working, images, onOpenImage) }
        else -> Notice(entry.text.orEmpty())
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
fun MarkdownText(text: String) {
    val codeBackground = MaterialTheme.colorScheme.surfaceContainerHigh
    SelectionContainer {
        Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            for (block in remember(text) { ChatMarkdown.blocks(text) }) {
                when (block) {
                    is MarkdownBlock.Heading -> Text(
                        annotated(block.spans, codeBackground),
                        style = MaterialTheme.typography.titleMedium,
                    )
                    is MarkdownBlock.Paragraph -> Text(
                        annotated(block.spans, codeBackground),
                        style = MaterialTheme.typography.bodyLarge,
                    )
                    is MarkdownBlock.Code -> Surface(
                        shape = RoundedCornerShape(12.dp),
                        color = codeBackground,
                        modifier = Modifier.fillMaxWidth(),
                    ) {
                        Text(
                            block.code,
                            fontFamily = FontFamily.Monospace,
                            style = MaterialTheme.typography.bodySmall,
                            softWrap = false,
                            modifier = Modifier.horizontalScroll(rememberScrollState()).padding(12.dp),
                        )
                    }
                }
            }
        }
    }
}

private fun annotated(spans: List<InlineSpan>, codeBackground: androidx.compose.ui.graphics.Color): AnnotatedString = buildAnnotatedString {
    for (span in spans) {
        when {
            span.code -> withStyle(SpanStyle(fontFamily = FontFamily.Monospace, background = codeBackground)) { append(span.text) }
            span.bold -> withStyle(SpanStyle(fontWeight = FontWeight.SemiBold)) { append(span.text) }
            else -> append(span.text)
        }
    }
}

@Composable
private fun ToolCall(tool: ChatTool, working: Boolean, images: RemoteImages, onOpenImage: (String) -> Unit) {
    var expanded by remember { mutableStateOf(false) }
    val output = tool.output?.takeIf { it.isNotBlank() }
    Surface(
        shape = RoundedCornerShape(14.dp),
        color = MaterialTheme.colorScheme.surfaceContainer,
        modifier = Modifier.fillMaxWidth().clickable(enabled = output != null) { expanded = !expanded },
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
                    )
                }
            }
            if (tool.images.isNotEmpty()) {
                Box(Modifier.padding(top = 8.dp)) { ChatImageTiles(tool.images, images, onOpenImage) }
            }
            if (expanded && output != null) {
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
    Text(
        text,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
        maxLines = 4,
        overflow = TextOverflow.Ellipsis,
        modifier = Modifier.fillMaxWidth(),
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
            Text(label.ifBlank { "Claude needs your answer" }, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.weight(1f))
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
    draft: String,
    onDraftChange: (String) -> Unit,
    working: Boolean,
    messagingAgent: Boolean,
    images: List<Uri>,
    sendingImages: Boolean,
    onAttachImages: () -> Unit,
    onRemoveImage: (Uri) -> Unit,
    onSend: () -> Unit,
    onDictate: () -> Unit,
    dictating: Boolean,
    onStop: () -> Unit,
) {
    Column(Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surface).navigationBarsPadding()) {
        AttachedImages(images, onRemove = onRemoveImage)
        Row(
            Modifier
                .fillMaxWidth()
                .padding(start = 12.dp, end = 6.dp, top = 8.dp, bottom = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            OutlinedTextField(
                value = draft,
                onValueChange = onDraftChange,
                placeholder = { Text(if (messagingAgent) "Send message after you answer" else "Message Claude") },
            leadingIcon = { AttachImageButton(enabled = !sendingImages, onClick = onAttachImages) },
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
                    Icon(Icons.Filled.Stop, contentDescription = "Stop Claude")
                }
                else -> FilledIconButton(onClick = onSend, enabled = false) {
                    Icon(Icons.AutoMirrored.Filled.Send, contentDescription = "Send the message")
                }
            }
        }
    }
}
