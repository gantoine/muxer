package com.gantoine.muxer.ui

import android.Manifest
import android.content.ActivityNotFoundException
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.SystemClock
import android.speech.RecognizerIntent
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.automirrored.outlined.Chat
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.DriveFileRenameOutline
import androidx.compose.material.icons.filled.EditNote
import androidx.compose.material.icons.filled.FitScreen
import androidx.compose.material.icons.filled.Keyboard
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.RestartAlt
import androidx.compose.material.icons.filled.Terminal
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.IconToggleButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SmallFloatingActionButton
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.chat.FileMentions
import com.gantoine.muxer.data.MacStore
import com.gantoine.muxer.images.CameraCaptures
import com.gantoine.muxer.images.ChatImages
import com.gantoine.muxer.images.RemoteImages
import com.gantoine.muxer.model.AgentMessaging
import com.gantoine.muxer.model.AgentNames
import com.gantoine.muxer.model.AgentStatus
import com.gantoine.muxer.model.ApprovalReply
import com.gantoine.muxer.model.WorkspaceListing
import com.gantoine.muxer.net.ConnectionState
import com.gantoine.muxer.net.NO_ATTACHMENT
import com.gantoine.muxer.net.RelayException
import com.gantoine.muxer.net.VisibleTerminal
import com.gantoine.muxer.notifications.ConversationShortcuts
import com.gantoine.muxer.protocol.Approval
import com.gantoine.muxer.protocol.Retry
import com.gantoine.muxer.terminal.StickyModifiers
import com.gantoine.muxer.terminal.TerminalBottomPadding
import com.gantoine.muxer.terminal.TerminalCanvas
import com.gantoine.muxer.terminal.TerminalContent
import com.gantoine.muxer.terminal.TerminalHistory
import com.gantoine.muxer.terminal.TerminalInputSink
import com.gantoine.muxer.terminal.TerminalInputView
import com.gantoine.muxer.terminal.TerminalKeys
import com.gantoine.muxer.terminal.TerminalModel
import com.gantoine.muxer.terminal.TerminalViewport
import com.gantoine.muxer.terminal.fittingGrid
import com.gantoine.muxer.terminal.rememberCellMetrics
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TerminalScreen(
    app: MuxerApplication,
    macId: String,
    workspaceId: String,
    surfaceId: String,
    onBack: () -> Unit,
    initialMessage: String = "",
    initialImages: List<Uri> = emptyList(),
    onSwitchTerminal: ((surfaceId: String) -> Unit)? = null,
    onOpenPorts: (() -> Unit)? = null,
) {
    val connections by app.connections.connectionsFlow.collectAsState()
    val connection = connections[macId]
    if (connection == null) {
        FullWidthMessage("This computer is not paired anymore.")
        return
    }
    val state by connection.state.collectAsState()
    val attachedScreen by connection.screen.collectAsState()
    val screen = attachedScreen?.takeIf { it.surfaceId == surfaceId }
    val workspaces by connection.workspaces.collectAsState()
    val chat by connection.chat.collectAsState()
    val chatImages = remember(connection) { RemoteImages { connection.chatImage(it).getOrNull() } }
    val allApprovals by connection.approvals.collectAsState()
    val approvals = allApprovals.orEmpty().filter { it.surfaceId == surfaceId }
    var sendingApprovals by remember { mutableStateOf(emptySet<String>()) }
    val retries by connection.retries.collectAsState()
    val retry = retries.firstOrNull { it.surfaceId == surfaceId }
    val agentMessages by connection.agentMessages.collectAsState()
    val inputState by connection.inputState.collectAsState()
    val busySurfaces by connection.busySurfaces.collectAsState()
    val midTurn = surfaceId in busySurfaces
    val workspace = workspaces?.firstOrNull { it.id == workspaceId }
    val terminal = workspace?.terminals?.firstOrNull { it.id == surfaceId }
    val agentStatus = terminal?.let(WorkspaceListing::agentStatus)
    val panes = if (onSwitchTerminal == null) emptyList() else workspace?.terminals.orEmpty()
    val panesNeedingYou = panes.filter { pane ->
        WorkspaceListing.agentStatus(pane) is AgentStatus.NeedsYou || allApprovals.orEmpty().any { it.surfaceId == pane.id }
    }.map { it.id }.toSet()
    val agentName = AgentNames.display(terminal?.agentSource) ?: "the agent"
    val messagingAgent = AgentMessaging.shouldMessage(agentStatus, approvals.isNotEmpty(), inputState?.waitingOnHuman == true)
    val waitingOnYou = approvals.isNotEmpty() || agentStatus is AgentStatus.NeedsYou
    val questionOpen = approvals.any { it.isQuestion }
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    val clipboard = LocalClipboardManager.current

    var fontSize by remember { mutableFloatStateOf(MacStore.DEFAULT_FONT_SIZE) }
    var fontSizeLoaded by remember { mutableStateOf(false) }
    var fitToPhone by remember { mutableStateOf(false) }
    var modifiers by remember { mutableStateOf(StickyModifiers()) }
    var composerOpen by remember { mutableStateOf(initialMessage.isNotEmpty() || initialImages.isNotEmpty()) }
    var composerText by rememberSaveable { mutableStateOf(initialMessage) }
    var dictating by rememberSaveable { mutableStateOf(false) }
    val dictation = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        dictating = false
        val spoken = result.data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)?.firstOrNull()
        if (!spoken.isNullOrBlank()) composerText = listOf(composerText.trimEnd(), spoken).filter { it.isNotEmpty() }.joinToString(" ")
    }
    var images by remember(surfaceId) { mutableStateOf(initialImages.take(ChatImages.MAX_IMAGES)) }
    var sendingImages by remember { mutableStateOf(false) }
    val pickImages = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(ChatImages.MAX_IMAGES)) { picked ->
        images = ChatImages.merge(images, picked)
    }
    var pendingPhoto by rememberSaveable { mutableStateOf<Uri?>(null) }
    val takePicture = rememberLauncherForActivityResult(ActivityResultContracts.TakePicture()) { saved ->
        val photo = pendingPhoto
        pendingPhoto = null
        if (saved && photo != null) images = ChatImages.merge(images, listOf(photo))
    }
    fun launchCamera() {
        val photo = CameraCaptures.newPhoto(context)
        pendingPhoto = photo
        takePicture.launch(photo)
    }
    val cameraPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) {
            launchCamera()
        } else {
            scope.launch { snackbar.showSnackbar("Allow the camera to take a photo for $agentName.") }
        }
    }
    val mentionQuery = FileMentions.query(composerText)
    var fileSuggestions by remember(surfaceId) { mutableStateOf<List<String>>(emptyList()) }
    LaunchedEffect(mentionQuery, chat?.sessionId) {
        if (mentionQuery == null || chat == null) {
            fileSuggestions = emptyList()
            return@LaunchedEffect
        }
        delay(FILE_SEARCH_DELAY_MILLIS)
        fileSuggestions = connection.chatFiles(mentionQuery).getOrDefault(emptyList())
    }
    val viewport = remember(surfaceId) { TerminalViewport() }
    val isLive by remember(viewport) { derivedStateOf { viewport.isLive } }
    val showSelectionBar by remember(viewport) { derivedStateOf { viewport.selection != null && !viewport.selecting } }
    var history by remember(surfaceId) { mutableStateOf<TerminalHistory?>(null) }
    var loadingHistory by remember(surfaceId) { mutableStateOf(false) }
    var historyLoadedAt by remember(surfaceId) { mutableLongStateOf(0L) }
    var inputView by remember { mutableStateOf<TerminalInputView?>(null) }
    var showChat by remember(surfaceId) { mutableStateOf(false) }
    var terminalDialog by remember(surfaceId) { mutableStateOf<TerminalDialog?>(null) }
    var closing by remember(surfaceId) { mutableStateOf(false) }
    var chatSessionShown by remember(surfaceId) { mutableStateOf<String?>(null) }
    LaunchedEffect(chat?.sessionId) {
        val sessionId = chat?.sessionId ?: return@LaunchedEffect
        if (sessionId != chatSessionShown) {
            chatSessionShown = sessionId
            showChat = true
        }
    }

    LaunchedEffect(Unit) {
        val settings = app.store.settings.first()
        fontSize = settings.fontSize
        fitToPhone = settings.fitToPhone
        fontSizeLoaded = true
    }
    LaunchedEffect(fontSize, fontSizeLoaded) {
        if (!fontSizeLoaded) return@LaunchedEffect
        delay(500)
        app.store.setFontSize(fontSize)
    }

    val visible = remember(macId, surfaceId) { VisibleTerminal(macId, surfaceId) }
    var attachment by remember(connection, surfaceId) { mutableLongStateOf(NO_ATTACHMENT) }
    DisposableEffect(connection, workspaceId, surfaceId) {
        attachment = connection.attach(workspaceId, surfaceId)
        app.connections.visibleTerminal.value = visible
        ConversationShortcuts.reportUsed(context, macId, workspaceId)
        onDispose {
            connection.detach(attachment)
            if (app.connections.visibleTerminal.value === visible) app.connections.visibleTerminal.value = null
        }
    }
    fun reclaimTerminal(): Boolean {
        if (connection.attachedSurfaceId == surfaceId) return false
        attachment = connection.attach(workspaceId, surfaceId, replacing = attachment)
        return true
    }
    LifecycleEventEffect(Lifecycle.Event.ON_START) {
        reclaimTerminal()
        app.connections.visibleTerminal.value = visible
    }
    LifecycleEventEffect(Lifecycle.Event.ON_STOP) {
        if (app.connections.visibleTerminal.value === visible) app.connections.visibleTerminal.value = null
    }
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { if (!reclaimTerminal()) connection.resync() }
    LaunchedEffect(connection) {
        connection.detached.collect { detached ->
            if (detached.surfaceId == surfaceId && !closing) {
                snackbar.showSnackbar("This terminal closed on the computer.")
                onBack()
            }
        }
    }

    fun reply(approval: Approval, reply: ApprovalReply) {
        sendingApprovals = sendingApprovals + approval.id
        scope.launch {
            val result = connection.reply(approval.id, reply)
            sendingApprovals = sendingApprovals - approval.id
            result.onFailure { snackbar.showSnackbar(it.message ?: "Couldn't send your answer.") }
        }
    }

    fun goLive() {
        viewport.returnToLive()
        viewport.selection = null
        history = null
    }

    fun loadHistory() {
        if (loadingHistory || !TerminalModel.historyOutdated(history, screen)) return
        if (history != null && SystemClock.elapsedRealtime() - historyLoadedAt < HISTORY_RELOAD_MILLIS) return
        loadingHistory = true
        scope.launch {
            connection.history()
                .onSuccess { response ->
                    val loaded = TerminalHistory(response.styles.associateBy { it.id }, response.lines, response.total)
                    if (!viewport.isLive) viewport.linesAboveLive += TerminalModel.rowsAdded(history, loaded)
                    viewport.selection = null
                    history = loaded
                }
                .onFailure { snackbar.showSnackbar(it.message ?: "Couldn't load earlier output.") }
            historyLoadedAt = SystemClock.elapsedRealtime()
            loadingHistory = false
        }
    }

    fun dictate() {
        val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH)
            .putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
        try {
            dictation.launch(intent)
            dictating = true
        } catch (_: ActivityNotFoundException) {
            scope.launch { snackbar.showSnackbar("No voice input is available on this phone.") }
        }
    }

    fun paste(text: String, submit: Boolean) {
        if (!viewport.isLive) goLive()
        scope.launch {
            connection.paste(text, submit).onFailure {
                snackbar.showSnackbar(it.message ?: "The computer didn't accept that.")
            }
        }
    }

    fun attachImages() {
        pickImages.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly))
    }

    fun takePhoto() {
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) {
            launchCamera()
        } else {
            cameraPermission.launch(Manifest.permission.CAMERA)
        }
    }

    fun mentionFile(path: String) {
        composerText = FileMentions.complete(composerText, path)
    }

    fun sendImages() {
        if (messagingAgent) {
            scope.launch { snackbar.showSnackbar("Answer the open prompt first, then send the images.") }
            return
        }
        val message = composerText.trim()
        val sending = images
        sendingImages = true
        scope.launch {
            val result = runCatching { sending.map { ChatImages.encode(context.contentResolver, it) } }
                .fold(onSuccess = { connection.sendImages(message, it) }, onFailure = { Result.failure(it) })
            sendingImages = false
            result
                .onSuccess {
                    images = images - sending.toSet()
                    if (composerText.trim() == message) composerText = ""
                }
                .onFailure { snackbar.showSnackbar(it.message ?: "The images didn't reach $agentName.") }
        }
    }

    fun sendMessage() {
        if (images.isNotEmpty()) return sendImages()
        val message = composerText.trim()
        if (message.isEmpty()) return
        composerText = ""
        val toAgent = messagingAgent
        if (!toAgent && !viewport.isLive) goLive()
        scope.launch {
            val result = if (toAgent) connection.deliverToAgent(message) else connection.paste(message, submit = true)
            result.onFailure { error ->
                if (composerText.isBlank()) composerText = message
                snackbar.showSnackbar(error.message ?: if (toAgent) "The message didn't reach the agent." else "The computer didn't accept that.")
            }
        }
    }

    fun terminalRequest(request: suspend () -> Result<Unit>, onSuccess: () -> Unit = {}) {
        scope.launch {
            request()
                .onSuccess { onSuccess() }
                .onFailure { failure ->
                    closing = false
                    val confirming = terminalDialog as? TerminalDialog.Close
                    if (failure is RelayException && failure.needsConfirmation && confirming != null) {
                        terminalDialog = confirming.copy(force = true)
                    } else {
                        terminalDialog = null
                        snackbar.showSnackbar(failure.message ?: "That didn't work.")
                    }
                }
        }
    }

    val sink = remember(connection) {
        object : TerminalInputSink {
            override fun text(text: String) {
                val input = TerminalKeys.withModifiers(text, consumeModifiers())
                when (input) {
                    is TerminalKeys.Input.Key -> connection.key(input.name)
                    is TerminalKeys.Input.Text -> connection.input(input.text)
                }
            }

            override fun key(name: String) {
                connection.key(TerminalKeys.keyWithModifiers(name, consumeModifiers()).name)
            }

            private fun consumeModifiers(): StickyModifiers {
                val held = modifiers
                modifiers = StickyModifiers()
                if (!viewport.isLive) goLive()
                return held
            }
        }
    }

    Scaffold(
        topBar = {
            CmuxTopBar(
                title = {
                    PaneSwitcherTitle(
                        title = terminal?.title?.ifBlank { null } ?: workspace?.title ?: "Terminal",
                        paneIds = panes.map { it.id },
                        currentId = surfaceId,
                        needsYou = panesNeedingYou,
                        onOpenPanes = { terminalDialog = TerminalDialog.Panes },
                        onSwitch = { onSwitchTerminal?.invoke(it) },
                    )
                },
                onBack = onBack,
                actions = {
                    Row(horizontalArrangement = Arrangement.spacedBy((-8).dp)) {
                        if (chat != null) {
                            IconButton(onClick = { showChat = !showChat }) {
                                if (showChat) {
                                    Icon(Icons.Filled.Terminal, contentDescription = "Show the terminal")
                                } else {
                                    Icon(Icons.AutoMirrored.Outlined.Chat, contentDescription = "Show the chat")
                                }
                            }
                        }
                        if (!showChat || chat == null) {
                            IconToggleButton(checked = fitToPhone, onCheckedChange = {
                                fitToPhone = it
                                scope.launch { app.store.setFitToPhone(it) }
                            }) {
                                Icon(Icons.Filled.FitScreen, contentDescription = "Fit the terminal to this phone")
                            }
                        }
                        if (terminal != null) {
                            IconButton(onClick = { terminalDialog = TerminalDialog.Actions }) {
                                Icon(Icons.Filled.MoreVert, contentDescription = "Terminal actions")
                            }
                        }
                    }
                },
            )
        },
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize().imePadding()) {
            AnimatedVisibility(visible = state !is ConnectionState.Connected) { ConnectionBanner(state) }
            val currentChat = chat
            if (showChat && currentChat != null) {
                ChatView(
                    chat = currentChat,
                    agentName = agentName,
                    status = agentStatus,
                    midTurn = midTurn,
                    answeringHere = approvals.isNotEmpty(),
                    agentMessages = agentMessages,
                    messagingAgent = messagingAgent,
                    waitingOnYou = waitingOnYou,
                    showComposer = !questionOpen,
                    draft = composerText,
                    onDraftChange = { composerText = it },
                    onSend = ::sendMessage,
                    onDictate = ::dictate,
                    dictating = dictating,
                    images = images,
                    sendingImages = sendingImages,
                    onPickPhotos = ::attachImages,
                    onTakePhoto = ::takePhoto,
                    onRemoveImage = { images = images - it },
                    fileSuggestions = fileSuggestions,
                    onPickFile = ::mentionFile,
                    onStop = { connection.key("escape") },
                    onShowTerminal = { showChat = false },
                    chatImages = chatImages,
                    modifier = Modifier.weight(1f).fillMaxWidth(),
                    overlay = { AgentPrompts(retry, approvals, sendingApprovals, ::reply) },
                )
            } else {
                BoxWithConstraints(
                    Modifier.weight(1f).fillMaxWidth().background(MaterialTheme.colorScheme.background),
                ) {
                    val measurer = rememberTextMeasurer()
                    val metrics = rememberCellMetrics(measurer, fontSize.sp)
                    val bottomPadding = with(LocalDensity.current) { TerminalBottomPadding.roundToPx() }
                    val grid = fittingGrid(constraints.maxWidth, constraints.maxHeight - bottomPadding, metrics)
                    LaunchedEffect(fitToPhone, grid, attachment) {
                        if (fitToPhone) delay(FIT_SETTLE_MS)
                        connection.setFit(if (fitToPhone) grid else null)
                    }
                    val current = screen
                    LaunchedEffect(current?.altScreen) {
                        if (current?.altScreen == true) goLive()
                    }
                    val content = remember(current, history) { current?.let { TerminalContent(it, history) } }
                    if (content == null) {
                        FullWidthMessage(if (state is ConnectionState.Connected) "Loading the terminal…" else describe(state))
                    } else {
                        TerminalCanvas(
                            content = content,
                            viewport = viewport,
                            fontSize = fontSize,
                            onFontSizeChange = { fontSize = it },
                            onNeedHistory = ::loadHistory,
                            onReturnToLive = ::goLive,
                            onScrollProgram = connection::scroll,
                            onTap = { inputView?.showKeyboard() },
                            modifier = Modifier.fillMaxSize(),
                        )
                        if (showSelectionBar) {
                            SelectionBar(
                                modifier = Modifier.align(Alignment.TopCenter).padding(8.dp),
                                onCopy = {
                                    val text = viewport.selection?.let(content::text).orEmpty()
                                    clipboard.setText(AnnotatedString(text))
                                    viewport.selection = null
                                    scope.launch { snackbar.showSnackbar("Copied ${text.lines().size} line(s).") }
                                },
                                onCancel = { viewport.selection = null },
                            )
                        }
                        if (!isLive) {
                            SmallFloatingActionButton(
                                onClick = ::goLive,
                                modifier = Modifier.align(Alignment.BottomEnd).padding(12.dp),
                            ) {
                                Row(Modifier.padding(horizontal = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                                    Icon(Icons.Filled.KeyboardArrowDown, contentDescription = null)
                                    Text("Live")
                                }
                            }
                        }
                    }
                }
                Column(Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surface).navigationBarsPadding()) {
                    AgentPrompts(retry, approvals, sendingApprovals, ::reply, Modifier.padding(top = 8.dp))
                    AgentMessageReceipts(agentMessages, waitingOnYou)
                    if (composerOpen && !questionOpen) {
                        AttachedImages(images, onRemove = { images = images - it })
                        FileSuggestions(fileSuggestions, onPick = ::mentionFile)
                        Composer(
                            text = composerText,
                            messagingAgent = messagingAgent,
                            canAttachImages = chat != null || images.isNotEmpty(),
                            hasImages = images.isNotEmpty(),
                            sendingImages = sendingImages,
                            onTextChange = { composerText = it },
                            onPickPhotos = ::attachImages,
                            onTakePhoto = ::takePhoto,
                            onDictate = ::dictate,
                            dictating = dictating,
                            onSend = ::sendMessage,
                        )
                    }
                    KeyBar(
                        modifiers = modifiers,
                        onModifiers = { modifiers = it },
                        onKey = sink::key,
                        onKeyboard = { inputView?.showKeyboard() },
                        composerOpen = composerOpen,
                        onComposer = { composerOpen = !composerOpen },
                        onPaste = {
                            clipboard.getText()?.text?.takeIf { it.isNotEmpty() }?.let { pasted ->
                                paste(pasted, submit = false)
                            }
                        },
                        inputHost = {
                            AndroidView(
                                factory = { context -> TerminalInputView(context).also { inputView = it } },
                                update = { view -> view.sink = sink },
                                modifier = Modifier.size(1.dp),
                            )
                        },
                    )
                }
            }
        }
    }

    val terminalTitle = terminal?.title?.ifBlank { null } ?: "this terminal"
    when (val current = terminalDialog) {
        null -> Unit
        TerminalDialog.Panes -> ActionSheet(onDismiss = { terminalDialog = null }) {
            panes.forEach { pane ->
                TerminalChoice(pane, selected = pane.id == surfaceId) {
                    terminalDialog = null
                    if (pane.id != surfaceId) onSwitchTerminal?.invoke(pane.id)
                }
            }
            SheetAction(Icons.Filled.Add, "New terminal") {
                terminalDialog = null
                scope.launch {
                    connection.createTerminal(workspaceId)
                        .onSuccess { if (it.isNotEmpty()) onSwitchTerminal?.invoke(it) }
                        .onFailure { snackbar.showSnackbar(it.message ?: "Couldn't open a new terminal.") }
                }
            }
        }
        TerminalDialog.Actions -> ActionSheet(onDismiss = { terminalDialog = null }) {
            if (onOpenPorts != null && terminal?.listeningPorts.orEmpty().isNotEmpty() && connection.opensPortsInTailscale) {
                OpenPortsAction {
                    terminalDialog = null
                    onOpenPorts()
                }
            }
            SheetAction(Icons.Filled.DriveFileRenameOutline, "Rename") { terminalDialog = TerminalDialog.Rename }
            SheetAction(Icons.Filled.RestartAlt, "Restart") { terminalDialog = TerminalDialog.Restart }
            if ((workspace?.terminals?.size ?: 0) > 1) {
                SheetAction(Icons.Filled.Close, "Close terminal", destructive = true) {
                    terminalDialog = TerminalDialog.Close(force = false)
                }
            }
        }
        TerminalDialog.Rename -> TextInputDialog(
            title = "Rename terminal",
            fields = listOf("Name (leave empty for the automatic one)" to (terminal?.title ?: "")),
            confirm = "Rename",
            onDismiss = { terminalDialog = null },
        ) { (title) ->
            terminalDialog = null
            terminalRequest({ connection.renameTerminal(workspaceId, surfaceId, title) })
        }
        TerminalDialog.Restart -> ConfirmDialog(
            title = "Restart $terminalTitle?",
            text = "This stops whatever is running in it and starts a new shell in the same folder on your computer.",
            confirm = "Restart",
            onConfirm = {
                terminalDialog = null
                terminalRequest({ connection.restartTerminal(workspaceId, surfaceId) }, onSuccess = { goLive(); connection.resync() })
            },
            onDismiss = { terminalDialog = null },
        )
        is TerminalDialog.Close -> CloseConfirmDialog(
            title = "Close $terminalTitle?",
            body = "This closes the terminal on your computer. The rest of the workspace stays open.",
            force = current.force,
            onConfirm = {
                closing = true
                terminalRequest({ connection.closeTerminal(workspaceId, surfaceId, current.force) }, onSuccess = {
                    terminalDialog = null
                    onBack()
                })
            },
            onDismiss = { terminalDialog = null },
        )
    }
}

private sealed interface TerminalDialog {
    data object Panes : TerminalDialog
    data object Actions : TerminalDialog
    data object Rename : TerminalDialog
    data object Restart : TerminalDialog
    data class Close(val force: Boolean) : TerminalDialog
}

@Composable
private fun AgentPrompts(
    retry: Retry?,
    approvals: List<Approval>,
    sending: Set<String>,
    onReply: (Approval, ApprovalReply) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier.padding(bottom = 4.dp)) {
        AnimatedVisibility(visible = retry != null) { retry?.let { RetryBanner(it) } }
        ApprovalCards(approvals, sending = sending, onReply = onReply)
    }
}

private const val FIT_SETTLE_MS = 300L
private const val FILE_SEARCH_DELAY_MILLIS = 150L
private const val HISTORY_RELOAD_MILLIS = 1_000L

private val KEYS = listOf(
    "Esc" to "escape",
    "Tab" to "tab",
    "↑" to "up",
    "↓" to "down",
    "←" to "left",
    "→" to "right",
    "^C" to "ctrl-c",
    "^D" to "ctrl-d",
    "⌫" to "backspace",
    "⏎" to "enter",
)

@Composable
private fun KeyBar(
    modifiers: StickyModifiers,
    onModifiers: (StickyModifiers) -> Unit,
    onKey: (String) -> Unit,
    onKeyboard: () -> Unit,
    composerOpen: Boolean,
    onComposer: () -> Unit,
    onPaste: () -> Unit,
    inputHost: @Composable () -> Unit,
) {
    Row(
        Modifier.fillMaxWidth().padding(horizontal = 4.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        inputHost()
        IconButton(onClick = onKeyboard) { Icon(Icons.Filled.Keyboard, contentDescription = "Show the keyboard") }
        IconToggleButton(checked = composerOpen, onCheckedChange = { onComposer() }) {
            Icon(Icons.Filled.EditNote, contentDescription = if (composerOpen) "Hide the message box" else "Write a message")
        }
        Row(
            Modifier.weight(1f).horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            ModifierButton("Ctrl", modifiers.ctrl) { onModifiers(modifiers.copy(ctrl = !modifiers.ctrl)) }
            ModifierButton("⌥", modifiers.alt) { onModifiers(modifiers.copy(alt = !modifiers.alt)) }
            ModifierButton("⇧", modifiers.shift) { onModifiers(modifiers.copy(shift = !modifiers.shift)) }
            KEYS.forEach { (label, name) -> KeyButton(label) { onKey(name) } }
            KeyButton("Paste", onClick = onPaste)
        }
    }
}

@Composable
private fun ModifierButton(label: String, active: Boolean, onClick: () -> Unit) {
    if (active) {
        Button(onClick = onClick, contentPadding = PaddingValues(horizontal = 12.dp, vertical = 0.dp)) {
            KeyLabel(label)
        }
    } else {
        KeyButton(label, onClick)
    }
}

@Composable
private fun Composer(
    text: String,
    messagingAgent: Boolean,
    canAttachImages: Boolean,
    hasImages: Boolean,
    sendingImages: Boolean,
    onTextChange: (String) -> Unit,
    onPickPhotos: () -> Unit,
    onTakePhoto: () -> Unit,
    onDictate: () -> Unit,
    dictating: Boolean,
    onSend: () -> Unit,
) {
    var field by rememberDraftField(text)
    Row(
        Modifier.fillMaxWidth().padding(start = 8.dp, end = 4.dp, top = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        OutlinedTextField(
            value = field,
            onValueChange = {
                field = it
                onTextChange(it.text)
            },
            placeholder = { Text(if (messagingAgent) "Send message after you answer" else "Message") },
            leadingIcon = if (canAttachImages) {
                { AttachImageButton(enabled = !sendingImages, onPickPhotos = onPickPhotos, onTakePhoto = onTakePhoto) }
            } else {
                null
            },
            maxLines = 5,
            shape = ComposerShape,
            keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences, autoCorrectEnabled = true),
            modifier = Modifier.weight(1f),
        )
        DictateButton(dictating, onClick = onDictate)
        IconButton(onClick = onSend, enabled = !sendingImages && (text.isNotBlank() || hasImages)) {
            Icon(Icons.AutoMirrored.Filled.Send, contentDescription = "Send the message")
        }
    }
}

@Composable
private fun SelectionBar(modifier: Modifier, onCopy: () -> Unit, onCancel: () -> Unit) {
    Surface(modifier = modifier, shape = RoundedCornerShape(20.dp), tonalElevation = 6.dp, shadowElevation = 6.dp) {
        Row(Modifier.padding(horizontal = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            TextButton(onClick = onCopy) { Text("Copy") }
            Spacer(Modifier.width(4.dp))
            TextButton(onClick = onCancel) { Text("Cancel") }
        }
    }
}

@Composable
private fun KeyButton(label: String, onClick: () -> Unit) {
    FilledTonalButton(onClick = onClick, contentPadding = PaddingValues(horizontal = 12.dp, vertical = 0.dp)) {
        KeyLabel(label)
    }
}

@Composable
private fun KeyLabel(label: String) {
    val symbol = label.length == 1 && !label[0].isLetterOrDigit()
    Text(label, style = if (symbol) MaterialTheme.typography.titleLarge else MaterialTheme.typography.labelLarge)
}
