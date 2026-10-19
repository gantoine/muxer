package com.gantoine.muxer.widget

import android.content.Context
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.glance.GlanceId
import androidx.glance.GlanceModifier
import androidx.glance.GlanceTheme
import androidx.glance.LocalContext
import androidx.glance.action.Action
import androidx.glance.action.clickable
import androidx.glance.appwidget.GlanceAppWidget
import androidx.glance.appwidget.action.actionSendBroadcast
import androidx.glance.appwidget.action.actionStartActivity
import androidx.glance.appwidget.components.FilledButton
import androidx.glance.appwidget.components.OutlineButton
import androidx.glance.appwidget.cornerRadius
import androidx.glance.appwidget.lazy.LazyColumn
import androidx.glance.appwidget.lazy.items
import androidx.glance.appwidget.provideContent
import androidx.glance.background
import androidx.glance.layout.Alignment
import androidx.glance.layout.Box
import androidx.glance.layout.Column
import androidx.glance.layout.Row
import androidx.glance.layout.Spacer
import androidx.glance.layout.fillMaxSize
import androidx.glance.layout.fillMaxWidth
import androidx.glance.layout.padding
import androidx.glance.layout.width
import androidx.glance.text.FontWeight
import androidx.glance.text.Text
import androidx.glance.text.TextAlign
import androidx.glance.text.TextStyle
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.model.Approvals
import com.gantoine.muxer.notifications.ApprovalActionReceiver
import com.gantoine.muxer.notifications.ApprovalTarget
import com.gantoine.muxer.notifications.Notifier
import java.time.Instant
import kotlinx.coroutines.flow.first

class ApprovalsWidget : GlanceAppWidget() {
    override suspend fun provideGlance(context: Context, id: GlanceId) {
        val app = context.applicationContext as MuxerApplication
        val initial = Widgets.inputs(app).first()
        provideContent {
            val inputs by remember { Widgets.inputs(app) }.collectAsState(initial)
            MuxerWidgetTheme { ApprovalsContent(inputs) }
        }
    }
}

class ApprovalsWidgetReceiver : MuxerWidgetReceiver() {
    override val glanceAppWidget = ApprovalsWidget()
}

private sealed interface ApprovalsEntry {
    val key: Long

    data class Waiting(val item: ApprovalItem) : ApprovalsEntry {
        override val key get() = "approval:${item.macId}:${item.approval.id}".hashCode().toLong()
    }

    data class Retrying(val item: RetryItem) : ApprovalsEntry {
        override val key get() = "retry:${item.macId}:${item.retry.surfaceId}".hashCode().toLong()
    }

    data class NeedsYou(val item: NeedsYouItem) : ApprovalsEntry {
        override val key get() = "needs:${item.macId}:${item.surfaceId}".hashCode().toLong()
    }
}

@Composable
private fun ApprovalsContent(inputs: WidgetInputs) {
    val now = System.currentTimeMillis()
    val snapshots = inputs.paired
    val entries = WidgetData.approvals(snapshots).map { ApprovalsEntry.Waiting(it) } +
        WidgetData.retries(snapshots).map { ApprovalsEntry.Retrying(it) } +
        WidgetData.needsYou(snapshots).map { ApprovalsEntry.NeedsYou(it) }
    WidgetSurface {
        WidgetHeader("Waiting for you", WidgetData.asOf(snapshots, now))
        if (inputs.locked) {
            LockedApprovals(entries.size)
        } else if (entries.isEmpty()) {
            Box(GlanceModifier.fillMaxSize().padding(16.dp), contentAlignment = Alignment.Center) {
                Text(
                    "No agent is waiting for you",
                    style = secondaryText(14).copy(textAlign = TextAlign.Center),
                )
            }
        } else {
            LazyColumn(GlanceModifier.fillMaxSize().padding(horizontal = 8.dp)) {
                items(entries, itemId = { it.key }) { entry ->
                    Box(GlanceModifier.fillMaxWidth().padding(vertical = 3.dp)) {
                        when (entry) {
                            is ApprovalsEntry.Waiting -> ApprovalRow(entry.item)
                            is ApprovalsEntry.Retrying -> RetryRow(entry.item, Instant.ofEpochMilli(now))
                            is ApprovalsEntry.NeedsYou -> NeedsYouRow(entry.item)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun LockedApprovals(waiting: Int) {
    val context = LocalContext.current
    Box(
        GlanceModifier.fillMaxSize().padding(16.dp).clickable(actionStartActivity(Widgets.openApp(context))),
        contentAlignment = Alignment.Center,
    ) {
        Text(WidgetData.lockedApprovalsLabel(waiting), style = secondaryText(14).copy(textAlign = TextAlign.Center))
    }
}

@Composable
private fun ApprovalRow(item: ApprovalItem) {
    val context = LocalContext.current
    val approval = item.approval
    val open = actionStartActivity(Notifier.openTerminal(context, item.macId, approval.workspaceId, approval.surfaceId))
    Column(
        GlanceModifier
            .fillMaxWidth()
            .background(GlanceTheme.colors.surfaceVariant)
            .cornerRadius(16.dp)
            .padding(10.dp),
    ) {
        Column(GlanceModifier.fillMaxWidth().clickable(open)) {
            Text(
                Approvals.title(approval),
                maxLines = 2,
                style = TextStyle(color = GlanceTheme.colors.onSurface, fontSize = 14.sp, fontWeight = FontWeight.Medium),
            )
            Approvals.summary(approval).takeIf { it.isNotBlank() }?.let { summary ->
                Text(summary, maxLines = 2, style = TextStyle(color = GlanceTheme.colors.onSurfaceVariant, fontSize = 13.sp))
            }
            Text(item.macName, maxLines = 1, style = secondaryText(11))
        }
        Row(GlanceModifier.fillMaxWidth().padding(top = 6.dp)) {
            item.buttons.forEachIndexed { index, button ->
                if (index > 0) Spacer(GlanceModifier.width(6.dp))
                val action = buttonAction(context, item, button, open)
                val modifier = GlanceModifier.defaultWeight()
                if (index == 0) {
                    FilledButton(text = button.label, onClick = action, modifier = modifier, maxLines = 1)
                } else {
                    OutlineButton(
                        text = button.label,
                        contentColor = GlanceTheme.colors.primary,
                        onClick = action,
                        modifier = modifier,
                        maxLines = 1,
                    )
                }
            }
        }
    }
}

private fun buttonAction(context: Context, item: ApprovalItem, button: ApprovalButton, open: Action): Action {
    val target = ApprovalTarget(item.macId, item.approval.id, item.approval.workspaceId, item.approval.surfaceId)
    return when (button) {
        is ApprovalButton.Decide ->
            actionSendBroadcast(ApprovalActionReceiver.broadcastIntent(context, target, mode = button.mode))
        is ApprovalButton.Choose -> actionSendBroadcast(ApprovalActionReceiver.broadcastIntent(context, target, answer = button.label))
        is ApprovalButton.OpenTerminal -> open
    }
}

@Composable
private fun NeedsYouRow(item: NeedsYouItem) {
    val context = LocalContext.current
    val open = actionStartActivity(Notifier.openTerminal(context, item.macId, item.workspaceId, item.surfaceId))
    Column(
        GlanceModifier
            .fillMaxWidth()
            .background(GlanceTheme.colors.surfaceVariant)
            .cornerRadius(16.dp)
            .padding(10.dp)
            .clickable(open),
    ) {
        Text(
            item.label,
            maxLines = 2,
            style = TextStyle(color = GlanceTheme.colors.onSurface, fontSize = 14.sp, fontWeight = FontWeight.Medium),
        )
        Text("${item.workspaceTitle} · ${item.macName}", maxLines = 1, style = secondaryText(11))
    }
}

@Composable
private fun RetryRow(item: RetryItem, now: Instant) {
    val context = LocalContext.current
    val open = actionStartActivity(Notifier.openTerminal(context, item.macId, item.workspaceId, item.retry.surfaceId))
    Column(
        GlanceModifier
            .fillMaxWidth()
            .background(GlanceTheme.colors.secondaryContainer)
            .cornerRadius(16.dp)
            .padding(10.dp)
            .clickable(open),
    ) {
        Text(
            WidgetData.retryLabel(item.retry, now),
            maxLines = 1,
            style = TextStyle(color = GlanceTheme.colors.onSecondaryContainer, fontSize = 14.sp, fontWeight = FontWeight.Medium),
        )
        Text(
            "${item.macName} · attempt ${item.retry.attempt}",
            maxLines = 1,
            style = TextStyle(color = GlanceTheme.colors.onSecondaryContainer, fontSize = 11.sp),
        )
    }
}
