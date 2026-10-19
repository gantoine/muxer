package com.gantoine.muxer.widget

import android.content.Context
import android.content.Intent
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.glance.GlanceId
import androidx.glance.GlanceModifier
import androidx.glance.GlanceTheme
import androidx.glance.LocalContext
import androidx.glance.LocalSize
import androidx.glance.action.clickable
import androidx.glance.appwidget.GlanceAppWidget
import androidx.glance.appwidget.SizeMode
import androidx.glance.appwidget.action.actionStartActivity
import androidx.glance.appwidget.appWidgetBackground
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
import androidx.glance.layout.size
import androidx.glance.layout.width
import androidx.glance.text.FontWeight
import androidx.glance.text.Text
import androidx.glance.text.TextAlign
import androidx.glance.text.TextStyle
import androidx.glance.unit.ColorProvider
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.model.WorkspaceListing
import com.gantoine.muxer.notifications.Notifier
import com.gantoine.muxer.terminal.parseHexColor
import kotlinx.coroutines.flow.first

class AgentBoardWidget : GlanceAppWidget() {
    override val sizeMode = SizeMode.Responsive(setOf(COUNTS_SIZE, LIST_SIZE))

    override suspend fun provideGlance(context: Context, id: GlanceId) {
        val app = context.applicationContext as MuxerApplication
        val initial = Widgets.inputs(app).first()
        provideContent {
            val inputs by remember { Widgets.inputs(app) }.collectAsState(initial)
            MuxerWidgetTheme { BoardContent(inputs) }
        }
    }

    companion object {
        val COUNTS_SIZE = DpSize(110.dp, 40.dp)
        val LIST_SIZE = DpSize(180.dp, 110.dp)
    }
}

class AgentBoardWidgetReceiver : MuxerWidgetReceiver() {
    override val glanceAppWidget = AgentBoardWidget()
}

private sealed interface BoardEntry {
    val key: Long

    data class Computer(val section: BoardSection) : BoardEntry {
        override val key get() = "computer:${section.macId}".hashCode().toLong()
    }

    data class Workspace(val row: BoardRow) : BoardEntry {
        override val key get() = "workspace:${row.macId}:${row.workspace.id}".hashCode().toLong()
    }
}

@Composable
private fun BoardContent(inputs: WidgetInputs) {
    val now = System.currentTimeMillis()
    val snapshots = inputs.paired
    val sections = WidgetData.board(snapshots, showComputerNames = inputs.macIds.size > 1)
    val asOf = WidgetData.asOf(snapshots, now)
    val size = LocalSize.current
    if (inputs.locked || size.height < AgentBoardWidget.LIST_SIZE.height || size.width < AgentBoardWidget.LIST_SIZE.width) {
        CountsContent(WidgetData.countsLabel(WidgetData.counts(sections)), asOf)
    } else {
        ListContent(sections, asOf)
    }
}

@Composable
private fun CountsContent(label: String, asOf: String?) {
    val context = LocalContext.current
    Row(
        GlanceModifier
            .fillMaxSize()
            .appWidgetBackground()
            .background(GlanceTheme.colors.widgetBackground)
            .cornerRadius(20.dp)
            .padding(start = 14.dp, end = 2.dp)
            .clickable(actionStartActivity(Widgets.openApp(context))),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(GlanceModifier.defaultWeight()) {
            Text(
                label,
                maxLines = 2,
                style = TextStyle(color = GlanceTheme.colors.onSurface, fontSize = 15.sp, fontWeight = FontWeight.Medium),
            )
            asOf?.let { AsOf(it) }
        }
        RefreshButton()
    }
}

@Composable
private fun ListContent(sections: List<BoardSection>, asOf: String?) {
    val entries = sections.flatMap { section ->
        listOfNotNull(section.title?.let { BoardEntry.Computer(section) }) + section.rows.map { BoardEntry.Workspace(it) }
    }
    WidgetSurface {
        WidgetHeader("Workspaces", asOf)
        if (entries.none { it is BoardEntry.Workspace }) {
            Box(GlanceModifier.fillMaxSize().padding(16.dp), contentAlignment = Alignment.Center) {
                Text("No workspaces yet. Open Muxer to connect.", style = secondaryText(14).copy(textAlign = TextAlign.Center))
            }
            return@WidgetSurface
        }
        LazyColumn(GlanceModifier.fillMaxSize().padding(horizontal = 8.dp)) {
            items(entries, itemId = { it.key }) { entry ->
                when (entry) {
                    is BoardEntry.Computer -> Text(
                        entry.section.title.orEmpty(),
                        maxLines = 1,
                        style = TextStyle(color = GlanceTheme.colors.primary, fontSize = 12.sp, fontWeight = FontWeight.Medium),
                        modifier = GlanceModifier.padding(start = 6.dp, top = 8.dp, bottom = 2.dp),
                    )
                    is BoardEntry.Workspace -> WorkspaceRow(entry.row)
                }
            }
        }
    }
}

@Composable
private fun WorkspaceRow(row: BoardRow) {
    val context = LocalContext.current
    val workspace = row.workspace
    val accent = parseHexColor(WorkspaceListing.color(workspace))?.let { ColorProvider(it) } ?: GlanceTheme.colors.primary
    val details = listOfNotNull(row.state.label, WorkspaceListing.time(workspace.lastActivityAt)).joinToString(" · ")
    Row(
        GlanceModifier
            .fillMaxWidth()
            .padding(horizontal = 6.dp, vertical = 6.dp)
            .clickable(actionStartActivity(openWorkspace(context, row))),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(GlanceModifier.size(10.dp).background(accent).cornerRadius(5.dp)) {}
        Spacer(GlanceModifier.width(10.dp))
        Column(GlanceModifier.defaultWeight()) {
            Text(
                workspace.title.ifBlank { "Workspace" },
                maxLines = 1,
                style = TextStyle(color = GlanceTheme.colors.onSurface, fontSize = 14.sp, fontWeight = FontWeight.Medium),
            )
            Text(details, maxLines = 1, style = TextStyle(color = stateColor(row.state), fontSize = 12.sp))
        }
        if (workspace.unreadCount > 0) {
            Spacer(GlanceModifier.width(8.dp))
            UnreadBadge(workspace.unreadCount)
        }
    }
}

@Composable
private fun UnreadBadge(count: Int) {
    Box(
        GlanceModifier.background(GlanceTheme.colors.primary).cornerRadius(10.dp).padding(horizontal = 7.dp, vertical = 1.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            if (count > 99) "99+" else count.toString(),
            style = TextStyle(color = GlanceTheme.colors.onPrimary, fontSize = 11.sp, fontWeight = FontWeight.Bold),
        )
    }
}

@Composable
private fun stateColor(state: WorkspaceState): ColorProvider = when (state) {
    WorkspaceState.Waiting -> GlanceTheme.colors.error
    WorkspaceState.NeedsYou -> GlanceTheme.colors.tertiary
    WorkspaceState.Retrying -> GlanceTheme.colors.tertiary
    WorkspaceState.Working -> GlanceTheme.colors.primary
    WorkspaceState.Idle -> GlanceTheme.colors.onSurfaceVariant
}

private fun openWorkspace(context: Context, row: BoardRow): Intent =
    row.surfaceId?.let { Notifier.openTerminal(context, row.macId, row.workspace.id, it) } ?: Widgets.openApp(context)
