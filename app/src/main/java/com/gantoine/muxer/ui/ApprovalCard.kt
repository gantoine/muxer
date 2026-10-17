package com.gantoine.muxer.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.model.ApprovalDecision
import com.gantoine.muxer.model.ApprovalReply
import com.gantoine.muxer.model.Approvals
import com.gantoine.muxer.notifications.LiveActivities
import com.gantoine.muxer.protocol.Approval
import java.time.Instant

@Composable
fun ApprovalCard(
    approval: Approval,
    sending: Boolean,
    onReply: (ApprovalReply) -> Unit,
    modifier: Modifier = Modifier,
) {
    Surface(
        shape = RoundedCornerShape(20.dp),
        color = MaterialTheme.colorScheme.surfaceContainerHigh,
        contentColor = MaterialTheme.colorScheme.onSurface,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        shadowElevation = 8.dp,
        modifier = modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 4.dp),
    ) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                val leadingQuestion = approval.questions.firstOrNull()?.takeIf { approval.isQuestion }
                if (leadingQuestion != null) {
                    Text(leadingQuestion.prompt, style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                } else {
                    Text(
                        Approvals.title(approval),
                        style = MaterialTheme.typography.titleSmall,
                        color = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.weight(1f),
                    )
                }
                TimeLeft(approval)
            }
            when {
                approval.isPermission -> PermissionBody(approval, sending, onReply)
                approval.isPlan -> PlanBody(approval, sending, onReply)
                else -> QuestionBody(approval, sending, onReply)
            }
        }
    }
}

@Composable
private fun TimeLeft(approval: Approval) {
    val now = rememberNow(approval.id)
    val seconds = Approvals.secondsLeft(approval, Instant.ofEpochMilli(now)) ?: return
    Text(
        if (seconds == 0L) "Answer in the terminal" else LiveActivities.countdown(seconds),
        style = MaterialTheme.typography.labelMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
}

@Composable
private fun PermissionBody(approval: Approval, sending: Boolean, onReply: (ApprovalReply) -> Unit) {
    approval.description?.takeIf { it.isNotBlank() && it != approval.detail }?.let {
        Text(it, style = MaterialTheme.typography.bodyMedium)
    }
    approval.detail?.takeIf { it.isNotBlank() }?.let { detail ->
        Surface(shape = RoundedCornerShape(12.dp), color = MaterialTheme.colorScheme.surfaceContainerLowest, contentColor = MaterialTheme.colorScheme.onSurface) {
            Text(
                detail,
                style = MaterialTheme.typography.bodySmall,
                fontFamily = FontFamily.Monospace,
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(max = 160.dp)
                    .verticalScroll(rememberScrollState())
                    .horizontalScroll(rememberScrollState())
                    .padding(10.dp),
            )
        }
    }
    DecisionButtons(Approvals.decisions(approval), sending, onReply)
}

@Composable
private fun DecisionButtons(decisions: List<ApprovalDecision>, sending: Boolean, onReply: (ApprovalReply) -> Unit, extra: @Composable () -> Unit = {}) {
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        decisions.forEachIndexed { index, decision ->
            val reply = { onReply(ApprovalReply.Decision(decision.mode)) }
            when (index) {
                0 -> Button(onClick = reply, enabled = !sending) { Text(decision.label) }
                1 -> OutlinedButton(onClick = reply, enabled = !sending) { Text(decision.label) }
                else -> {
                    extra()
                    TextButton(onClick = reply, enabled = !sending) { Text(decision.label) }
                }
            }
        }
    }
}

@Composable
private fun PlanBody(approval: Approval, sending: Boolean, onReply: (ApprovalReply) -> Unit) {
    var revising by remember(approval.id) { mutableStateOf(false) }
    var feedback by remember(approval.id) { mutableStateOf("") }
    Surface(shape = RoundedCornerShape(12.dp), color = MaterialTheme.colorScheme.surfaceContainerLowest, contentColor = MaterialTheme.colorScheme.onSurface) {
        Column(Modifier.fillMaxWidth().heightIn(max = 320.dp).verticalScroll(rememberScrollState()).padding(12.dp)) {
            MarkdownText(approval.plan.orEmpty())
        }
    }
    if (revising) {
        LabeledTextField(
            value = feedback,
            onValueChange = { feedback = it },
            label = "What should change?",
            maxLines = 5,
            enabled = !sending,
            keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences),
            modifier = Modifier.fillMaxWidth(),
        )
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Button(
                onClick = { onReply(ApprovalReply.Decision(Approvals.MODE_DENY, feedback.trim())) },
                enabled = feedback.isNotBlank() && !sending,
            ) { Text("Send changes") }
            TextButton(onClick = { revising = false }, enabled = !sending) { Text("Cancel") }
        }
    } else {
        DecisionButtons(Approvals.decisions(approval), sending, onReply) {
            TextButton(onClick = { revising = true }, enabled = !sending) { Text("Revise") }
        }
    }
}

@Composable
private fun QuestionBody(approval: Approval, sending: Boolean, onReply: (ApprovalReply) -> Unit) {
    var chosen by remember(approval.id) { mutableStateOf(approval.questions.map { emptySet<Int>() }) }
    var typed by remember(approval.id) { mutableStateOf(approval.questions.map { "" }) }
    Column(
        Modifier.heightIn(max = 360.dp).verticalScroll(rememberScrollState()),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        approval.questions.forEachIndexed { questionIndex, question ->
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                if (questionIndex > 0) {
                    question.header?.takeIf { it.isNotBlank() }?.let {
                        Text(it, style = MaterialTheme.typography.labelLarge)
                    }
                    Text(question.prompt, style = MaterialTheme.typography.bodyMedium)
                }
                fun choose(optionIndex: Int) {
                    val current = chosen[questionIndex]
                    val next = when {
                        !question.multiSelect -> setOf(optionIndex)
                        optionIndex in current -> current - optionIndex
                        else -> current + optionIndex
                    }
                    chosen = chosen.toMutableList().also { it[questionIndex] = next }
                }
                question.options.forEachIndexed { optionIndex, option ->
                    OptionRow(
                        label = option.label,
                        description = option.description,
                        selected = optionIndex in chosen[questionIndex],
                        multiSelect = question.multiSelect,
                        enabled = !sending,
                        onClick = { choose(optionIndex) },
                    )
                }
                val otherIndex = Approvals.otherIndex(question)
                val typing = otherIndex in chosen[questionIndex]
                OptionRow(
                    label = "Type something",
                    description = null,
                    selected = typing,
                    multiSelect = question.multiSelect,
                    enabled = !sending,
                    onClick = { choose(otherIndex) },
                )
                if (typing) {
                    LabeledTextField(
                        value = typed[questionIndex],
                        onValueChange = { text -> typed = typed.toMutableList().also { it[questionIndex] = text } },
                        label = "Your answer",
                        maxLines = 4,
                        enabled = !sending,
                        keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences),
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        }
    }
    val complete = approval.questions.indices.all { Approvals.isAnswered(approval.questions[it], chosen[it], typed[it]) }
    Button(
        onClick = {
            onReply(ApprovalReply.Answers(approval.questions.mapIndexed { index, question -> Approvals.answer(question, chosen[index], typed[index]) }))
        },
        enabled = complete && !sending,
    ) { Text("Send answer") }
}

@Composable
private fun OptionRow(
    label: String,
    description: String?,
    selected: Boolean,
    multiSelect: Boolean,
    enabled: Boolean,
    onClick: () -> Unit,
) {
    val selection = if (multiSelect) {
        Modifier.toggleable(value = selected, enabled = enabled, role = Role.Checkbox, onValueChange = { onClick() })
    } else {
        Modifier.selectable(selected = selected, enabled = enabled, role = Role.RadioButton, onClick = onClick)
    }
    val background = if (selected) MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surfaceContainerHighest
    Row(
        Modifier.fillMaxWidth().background(background, RoundedCornerShape(12.dp)).then(selection),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (multiSelect) {
            Checkbox(checked = selected, onCheckedChange = null, modifier = Modifier.padding(12.dp))
        } else {
            RadioButton(selected = selected, onClick = null, modifier = Modifier.padding(12.dp))
        }
        Column(Modifier.padding(end = 12.dp, top = 6.dp, bottom = 6.dp)) {
            Text(label, style = MaterialTheme.typography.bodyLarge)
            description?.takeIf { it.isNotBlank() }?.let {
                Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}

@Composable
fun ApprovalCards(
    approvals: List<Approval>,
    sending: Set<String>,
    onReply: (Approval, ApprovalReply) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (approvals.isEmpty()) return
    Column(modifier) {
        approvals.forEach { approval ->
            ApprovalCard(approval, sending = approval.id in sending, onReply = { reply -> onReply(approval, reply) })
        }
    }
}
