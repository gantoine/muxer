package com.gantoine.muxer.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.chat.ChatDiff
import com.gantoine.muxer.chat.DiffKind
import com.gantoine.muxer.protocol.ChatHunk

private val AddedTint = Color(0xFF2EA043)

@Composable
fun DiffCountsLabel(hunks: List<ChatHunk>) {
    val counts = remember(hunks) { ChatDiff.counts(hunks) }
    Row {
        if (counts.added > 0) Text("+${counts.added}", style = MaterialTheme.typography.labelMedium, color = AddedTint)
        if (counts.added > 0 && counts.removed > 0) Text(" ", style = MaterialTheme.typography.labelMedium)
        if (counts.removed > 0) Text("−${counts.removed}", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.error)
    }
}

@Composable
fun DiffView(hunks: List<ChatHunk>, modifier: Modifier = Modifier) {
    val removedTint = MaterialTheme.colorScheme.error
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    val lines = remember(hunks) { hunks.map(ChatDiff::lines) }
    SelectionContainer(modifier.clip(RoundedCornerShape(8.dp)).background(MaterialTheme.colorScheme.surfaceContainerLow)) {
        Column(Modifier.horizontalScroll(rememberScrollState()).width(IntrinsicSize.Max)) {
            lines.forEachIndexed { index, hunk ->
                if (index > 0) {
                    Text("⋯", style = MaterialTheme.typography.bodySmall, color = muted, modifier = Modifier.padding(horizontal = 8.dp))
                }
                for (line in hunk) {
                    val (tint, sign) = when (line.kind) {
                        DiffKind.Added -> AddedTint to "+"
                        DiffKind.Removed -> removedTint to "-"
                        DiffKind.Context -> Color.Transparent to " "
                    }
                    Row(Modifier.fillMaxWidth().background(tint.copy(alpha = if (line.kind == DiffKind.Context) 0f else 0.14f))) {
                        Text(
                            line.number.toString(),
                            style = MaterialTheme.typography.bodySmall,
                            fontFamily = FontFamily.Monospace,
                            color = muted,
                            textAlign = TextAlign.End,
                            modifier = Modifier.widthIn(min = 36.dp).padding(horizontal = 6.dp),
                        )
                        Text(
                            "$sign ${line.text}",
                            style = MaterialTheme.typography.bodySmall,
                            fontFamily = FontFamily.Monospace,
                            softWrap = false,
                            modifier = Modifier.padding(end = 8.dp),
                        )
                    }
                }
            }
        }
    }
}
