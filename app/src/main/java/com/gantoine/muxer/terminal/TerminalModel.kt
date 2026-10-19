package com.gantoine.muxer.terminal

import com.gantoine.muxer.protocol.CellStyle
import com.gantoine.muxer.protocol.CursorInfo
import com.gantoine.muxer.protocol.ScreenFrame
import com.gantoine.muxer.protocol.Span

data class TerminalScreen(
    val surfaceId: String,
    val columns: Int,
    val rows: Int,
    val foreground: String,
    val background: String,
    val cursor: CursorInfo?,
    val styles: Map<Int, CellStyle>,
    val lines: List<List<Span>>,
    val altScreen: Boolean = false,
    val historyRows: Int? = null,
) {
    val lastUsedRow: Int by lazy {
        val lastText = lines.indexOfLast { line -> line.any { it.text.isNotBlank() } }
        maxOf(lastText, cursor?.row ?: 0)
    }

    override fun equals(other: Any?): Boolean = this === other

    override fun hashCode(): Int = System.identityHashCode(this)
}

object TerminalModel {
    fun apply(previous: TerminalScreen?, frame: ScreenFrame): TerminalScreen? {
        val sameTerminal = previous != null && previous.surfaceId == frame.surfaceId
        if (!frame.full && !sameTerminal) return null

        val base = if (frame.full || previous == null) {
            TerminalScreen(
                surfaceId = frame.surfaceId,
                columns = frame.columns,
                rows = frame.rows,
                foreground = frame.foreground,
                background = frame.background,
                cursor = frame.cursor,
                styles = frame.styles.associateBy { it.id },
                lines = List(frame.rows) { emptyList() },
            )
        } else {
            if (frame.styles.isEmpty()) previous else previous.copy(styles = previous.styles + frame.styles.associateBy { it.id })
        }

        val lines = base.lines.toMutableList()
        for (update in frame.changed) {
            if (update.row in lines.indices) lines[update.row] = update.spans
        }
        return base.copy(lines = lines, cursor = frame.cursor ?: base.cursor, altScreen = frame.altScreen, historyRows = frame.historyRows)
    }

    fun rowsAdded(previous: TerminalHistory?, loaded: TerminalHistory): Int =
        if (previous == null) loaded.lines.size else maxOf(loaded.total - previous.total, loaded.lines.size - previous.lines.size)

    fun historyOutdated(history: TerminalHistory?, screen: TerminalScreen?): Boolean {
        val historyRows = screen?.historyRows
        return when {
            history == null -> true
            historyRows != null -> historyRows != history.total
            else -> history.lines.isEmpty()
        }
    }
}

data class TerminalHistory(val styles: Map<Int, CellStyle>, val lines: List<List<Span>>, val total: Int = lines.size)
