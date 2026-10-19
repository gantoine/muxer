package com.gantoine.muxer.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.outlined.Bolt
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.model.WorkspaceListing
import com.gantoine.muxer.model.parseInstant
import com.gantoine.muxer.net.ConnectionState
import com.gantoine.muxer.net.RelayConnection
import com.gantoine.muxer.protocol.Automation
import com.gantoine.muxer.protocol.AutomationAction
import com.gantoine.muxer.protocol.AutomationLog
import com.gantoine.muxer.protocol.AutomationRateLimit
import com.gantoine.muxer.protocol.AutomationsResponse
import java.time.Instant
import kotlin.math.roundToLong
import kotlinx.coroutines.launch

object AutomationListing {
    const val GUIDE_URL = "https://github.com/gantoine/muxer#custom-alerts-with-cmux-automations"
    const val FILE = "~/.cmuxterm/automations.json"

    fun actions(rule: Automation): String {
        val summaries = rule.actions.map { it.summary.ifBlank { it.type } }.filter { it.isNotBlank() }
        return when {
            summaries.isEmpty() -> count(rule.actionCount, "action")
            summaries.size == 1 -> summaries.single()
            else -> "${summaries.first()} and ${count(summaries.size - 1, "more action")}"
        }
    }

    fun summary(rule: Automation): String = "When ${rule.trigger.ifBlank { "any event" }} · ${actions(rule)}"

    fun status(status: String): String = when (status) {
        "completed" -> "Done"
        "started" -> "Running"
        "error" -> "Failed"
        "skipped_rate_limit" -> "Skipped by the rate limit"
        "skipped_loop" -> "Skipped to avoid a loop"
        "skipped_backpressure" -> "Skipped, cmux was busy"
        "gap" -> "Missed some events"
        else -> status.replace('_', ' ').replaceFirstChar { it.uppercase() }
    }

    fun failed(log: AutomationLog): Boolean = log.status == "error"

    fun lastRun(rule: Automation, now: Instant = Instant.now()): String {
        val run = rule.lastRun ?: return "Hasn't run since cmux started"
        val time = WorkspaceListing.time(parseInstant(run.occurredAt), now)
        return listOfNotNull(time?.let { "Last ran $it" } ?: "Ran", status(run.status)).joinToString(" · ")
    }

    fun rateLimit(limit: AutomationRateLimit): String {
        val times = when (limit.maximum) {
            1 -> "once"
            2 -> "twice"
            else -> "${limit.maximum} times"
        }
        val seconds = limit.intervalSeconds
        val window = when {
            seconds == 1.0 -> "per second"
            seconds == 60.0 -> "per minute"
            seconds == 3600.0 -> "per hour"
            seconds >= 1 && seconds == seconds.roundToLong().toDouble() -> "every ${seconds.roundToLong()} seconds"
            else -> "every $seconds seconds"
        }
        return "At most $times $window"
    }

    fun logTitle(log: AutomationLog): String = when (log.event) {
        "config.enable" -> "Turned on"
        "config.disable" -> "Turned off"
        else -> log.event.ifBlank { "Event" }
    }

    fun withEnabled(response: AutomationsResponse, ruleId: String, enabled: Boolean): AutomationsResponse =
        response.copy(rules = response.rules.map { if (it.id == ruleId) it.copy(enabled = enabled) else it })

    private fun count(n: Int, noun: String): String = if (n == 1) "1 $noun" else "$n ${noun}s"
}

private const val LIST_REQUEST = "automations"
private const val TOGGLE_REQUEST = "set_automation_enabled"
private const val RELOAD_REQUEST = "reload_automations"
private const val LOGS_REQUEST = "automation_logs"
private const val LOG_LINES = 50

private class AutomationsModel(private val snackbar: SnackbarHostState) {
    var response by mutableStateOf<AutomationsResponse?>(null)
    var failure by mutableStateOf<Throwable?>(null)
    var busy by mutableStateOf(false)
    var toggling by mutableStateOf(emptySet<String>())

    suspend fun load(connection: RelayConnection?) {
        val target = connection?.takeIf { it.supports(LIST_REQUEST) } ?: return
        target.automations()
            .onSuccess {
                response = it
                failure = null
            }
            .onFailure { if (response == null) failure = it else snackbar.showSnackbar(it.message ?: "Couldn't load the automations.") }
    }

    suspend fun reload(connection: RelayConnection?) {
        val target = connection ?: return
        busy = true
        target.reloadAutomations()
            .onSuccess {
                response = it
                failure = null
                snackbar.showSnackbar(if (it.configError != null) "cmux couldn't read the rules." else "Reloaded the rules.")
            }
            .onFailure { snackbar.showSnackbar(it.message ?: "Couldn't reload the rules.") }
        busy = false
    }

    suspend fun setEnabled(connection: RelayConnection?, rule: Automation, enabled: Boolean) {
        val target = connection ?: return
        val before = response ?: return
        toggling = toggling + rule.id
        response = AutomationListing.withEnabled(before, rule.id, enabled)
        target.setAutomationEnabled(rule.id, enabled)
            .onSuccess { response = it }
            .onFailure {
                response = AutomationListing.withEnabled(response ?: before, rule.id, rule.enabled)
                snackbar.showSnackbar(it.message ?: "Couldn't change ${rule.id}.")
            }
        toggling = toggling - rule.id
    }
}

@Composable
private fun rememberAutomations(connection: RelayConnection?, state: ConnectionState, snackbar: SnackbarHostState): AutomationsModel {
    val model = remember { AutomationsModel(snackbar) }
    LaunchedEffect(connection, state is ConnectionState.Connected) {
        if (state is ConnectionState.Connected) model.load(connection)
    }
    return model
}

@Composable
private fun ReloadAction(connection: RelayConnection?, model: AutomationsModel, onReload: () -> Unit) {
    if (connection?.supports(RELOAD_REQUEST) == false) return
    IconButton(onClick = onReload, enabled = connection != null && !model.busy) {
        Icon(Icons.Filled.Refresh, contentDescription = "Reload the rules")
    }
}

@Composable
fun AutomationsScreen(
    app: MuxerApplication,
    macId: String,
    onBack: () -> Unit,
    onOpenRule: (ruleId: String) -> Unit,
) {
    val connection = rememberConnection(app, macId)
    val state by connection.collectOr(ConnectionState.Stopped) { it.state }
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    val macName = rememberMacName(app, macId, fallback = "the computer")
    val model = rememberAutomations(connection, state, snackbar)
    val canToggle = connection?.supports(TOGGLE_REQUEST) != false

    Scaffold(
        topBar = {
            CmuxTopBar(title = "Automations", onBack = onBack) {
                ReloadAction(connection, model) { scope.launch { model.reload(connection) } }
            }
        },
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            if (model.busy) LinearProgressIndicator(Modifier.fillMaxWidth())
            RemoteContent(
                value = model.response,
                failure = model.failure,
                state = state,
                requestType = LIST_REQUEST,
                icon = Icons.Outlined.Bolt,
                what = "list automations",
                failedTitle = "Couldn't load automations",
                loading = "Fetching automations…",
                macName = macName,
                onRetry = {
                    model.failure = null
                    scope.launch { model.load(connection) }
                },
            ) { response ->
                Column {
                    ConnectionBanner(state)
                    val error = response.configError
                    when {
                        response.rules.isEmpty() && error != null -> EmptyState(
                            icon = Icons.Outlined.ErrorOutline,
                            title = "cmux couldn't read the rules",
                            body = "$error\n\nFix ${AutomationListing.FILE} on $macName, then reload.",
                        ) {
                            Button(onClick = { scope.launch { model.reload(connection) } }, enabled = !model.busy) { Text("Reload") }
                        }
                        response.rules.isEmpty() -> NoAutomations(macName)
                        else -> LazyColumn(
                            Modifier.fillMaxSize(),
                            contentPadding = PaddingValues(top = 8.dp, bottom = 32.dp),
                            verticalArrangement = Arrangement.spacedBy(2.dp),
                        ) {
                            itemsIndexed(response.rules, key = { _, rule -> rule.id }) { index, rule ->
                                AutomationRow(
                                    rule,
                                    index,
                                    response.rules.size,
                                    switchEnabled = canToggle && rule.id !in model.toggling,
                                    onOpen = { onOpenRule(rule.id) },
                                    onEnabledChange = { enabled -> scope.launch { model.setEnabled(connection, rule, enabled) } },
                                )
                            }
                            item {
                                Text(
                                    "Edit rules in ${AutomationListing.FILE} on $macName, then reload.",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    modifier = Modifier.padding(horizontal = 28.dp, vertical = 16.dp),
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun NoAutomations(macName: String) {
    val uriHandler = LocalUriHandler.current
    EmptyState(
        icon = Icons.Outlined.Bolt,
        title = "No Automations",
        body = "Automations run when something happens, like an agent being resumed. " +
            "Add rules to ${AutomationListing.FILE} on $macName and their notifications reach this phone.",
    ) {
        Button(onClick = { uriHandler.openUri(AutomationListing.GUIDE_URL) }) { Text("See how") }
    }
}

@Composable
private fun AutomationRow(
    rule: Automation,
    index: Int,
    count: Int,
    switchEnabled: Boolean,
    onOpen: () -> Unit,
    onEnabledChange: (Boolean) -> Unit,
) {
    Row(
        Modifier.groupedRow(
            groupedShape(index, count),
            onClick = onOpen,
            contentPadding = PaddingValues(start = 16.dp, end = 12.dp, top = 14.dp, bottom = 14.dp),
        ),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            Text(rule.id, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(
                AutomationListing.summary(rule),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            val failed = rule.lastRun?.let(AutomationListing::failed) == true
            Text(
                AutomationListing.lastRun(rule),
                style = MaterialTheme.typography.labelMedium,
                color = if (failed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.width(12.dp))
        Switch(checked = rule.enabled, enabled = switchEnabled, onCheckedChange = onEnabledChange)
    }
}

@Composable
fun AutomationScreen(
    app: MuxerApplication,
    macId: String,
    ruleId: String,
    onBack: () -> Unit,
) {
    val connection = rememberConnection(app, macId)
    val state by connection.collectOr(ConnectionState.Stopped) { it.state }
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    val macName = rememberMacName(app, macId, fallback = "the computer")
    val model = rememberAutomations(connection, state, snackbar)
    var logs by remember { mutableStateOf<List<AutomationLog>?>(null) }
    val canToggle = connection?.supports(TOGGLE_REQUEST) != false
    val showLogs = connection?.supports(LOGS_REQUEST) != false

    suspend fun loadLogs() {
        val target = connection?.takeIf { it.supports(LOGS_REQUEST) } ?: return
        target.automationLogs(ruleId, LOG_LINES).onSuccess { logs = it }
    }

    LaunchedEffect(connection, state is ConnectionState.Connected, model.response) {
        if (state is ConnectionState.Connected) loadLogs()
    }

    Scaffold(
        topBar = {
            CmuxTopBar(title = ruleId, onBack = onBack) {
                ReloadAction(connection, model) { scope.launch { model.reload(connection) } }
            }
        },
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            if (model.busy) LinearProgressIndicator(Modifier.fillMaxWidth())
            RemoteContent(
                value = model.response,
                failure = model.failure,
                state = state,
                requestType = LIST_REQUEST,
                icon = Icons.Outlined.Bolt,
                what = "list automations",
                failedTitle = "Couldn't load the rule",
                loading = "Fetching the rule…",
                macName = macName,
                onRetry = {
                    model.failure = null
                    scope.launch { model.load(connection) }
                },
            ) { response ->
                val rule = response.rules.firstOrNull { it.id == ruleId }
                Column {
                    ConnectionBanner(state)
                    if (rule == null) {
                        FullWidthMessage(
                            response.configError?.let { "cmux couldn't read the rules: $it" }
                                ?: "This rule is gone from ${AutomationListing.FILE} on $macName.",
                            Modifier.padding(top = 48.dp),
                        )
                    } else {
                        AutomationDetails(
                            rule,
                            logs = logs,
                            showLogs = showLogs,
                            switchEnabled = canToggle && rule.id !in model.toggling,
                            onEnabledChange = { enabled -> scope.launch { model.setEnabled(connection, rule, enabled) } },
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun AutomationDetails(
    rule: Automation,
    logs: List<AutomationLog>?,
    showLogs: Boolean,
    switchEnabled: Boolean,
    onEnabledChange: (Boolean) -> Unit,
) {
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 32.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
        item {
            Row(
                Modifier.padding(top = 8.dp).groupedRow(
                    groupedShape(0, 1),
                    onClick = if (switchEnabled) ({ onEnabledChange(!rule.enabled) }) else null,
                    contentPadding = PaddingValues(start = 16.dp, end = 12.dp, top = 14.dp, bottom = 14.dp),
                ),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(Modifier.weight(1f)) {
                    Text(if (rule.enabled) "On" else "Off", style = MaterialTheme.typography.titleMedium)
                    Text(
                        AutomationListing.lastRun(rule),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Switch(checked = rule.enabled, enabled = switchEnabled, onCheckedChange = onEnabledChange)
            }
        }

        item { SectionLabel("When") }
        val whenRows = listOf(rule.trigger.ifBlank { "any event" }) + rule.conditions.map { "only if $it" } +
            listOfNotNull(rule.rateLimit?.let(AutomationListing::rateLimit))
        itemsIndexed(whenRows) { index, text ->
            Text(text, style = MaterialTheme.typography.bodyLarge, modifier = Modifier.groupedRow(groupedShape(index, whenRows.size)))
        }

        item { SectionLabel("Then") }
        if (rule.actions.isEmpty()) {
            item {
                Text(
                    AutomationListing.actions(rule),
                    style = MaterialTheme.typography.bodyLarge,
                    modifier = Modifier.groupedRow(groupedShape(0, 1)),
                )
            }
        }
        itemsIndexed(rule.actions) { index, action -> ActionRow(action, index, rule.actions.size) }

        if (showLogs) {
            item { SectionLabel("Recent activity") }
            val lines = logs
            when {
                lines == null -> item { FullWidthMessage("Fetching the log…") }
                lines.isEmpty() -> item {
                    FullWidthMessage("Nothing yet. cmux keeps this log in memory, so it starts over when cmux restarts.")
                }
                else -> itemsIndexed(lines) { index, log -> LogRow(log, index, lines.size) }
            }
        }
    }
}

@Composable
private fun ActionRow(action: AutomationAction, index: Int, count: Int) {
    Column(Modifier.groupedRow(groupedShape(index, count)), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text(action.summary.ifBlank { action.type }, style = MaterialTheme.typography.titleMedium)
        action.fields.forEach { field ->
            Column {
                Text(field.name, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Text(
                    field.value,
                    style = MaterialTheme.typography.bodyMedium,
                    fontFamily = FontFamily.Monospace,
                    modifier = Modifier
                        .fillMaxWidth()
                        .background(MaterialTheme.colorScheme.surfaceContainerHigh, RoundedCornerShape(8.dp))
                        .padding(horizontal = 8.dp, vertical = 4.dp),
                )
            }
        }
    }
}

@Composable
private fun LogRow(log: AutomationLog, index: Int, count: Int) {
    val failed = AutomationListing.failed(log)
    Column(Modifier.groupedRow(groupedShape(index, count)), verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                AutomationListing.logTitle(log),
                style = MaterialTheme.typography.titleSmall,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            WorkspaceListing.time(parseInstant(log.occurredAt))?.let {
                Spacer(Modifier.width(12.dp))
                Text(it, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
        Text(
            AutomationListing.status(log.status),
            style = MaterialTheme.typography.bodyMedium,
            color = if (failed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (log.detail.isNotBlank()) {
            Text(log.detail, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}
