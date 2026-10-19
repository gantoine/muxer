package com.gantoine.muxer.terminal

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.Orientation
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.calculateZoom
import androidx.compose.foundation.gestures.detectDragGesturesAfterLongPress
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.rememberScrollableState
import androidx.compose.foundation.gestures.scrollable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextMeasurer
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.drawText
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.gantoine.muxer.protocol.CellStyle
import kotlin.math.floor

data class CellMetrics(val width: Float, val height: Float)

@Stable
class TerminalViewport {
    var linesAboveLive by mutableFloatStateOf(0f)
    var selection by mutableStateOf<Selection?>(null)
    var selecting by mutableStateOf(false)
    var selectionWord: Selection? = null

    fun extendSelection(focus: CellPosition) {
        val word = selectionWord ?: return
        selection = if (focus >= word.start) Selection(word.start, maxOf(focus, word.end)) else Selection(word.end, focus)
    }

    val isLive: Boolean get() = linesAboveLive <= 0f

    fun returnToLive() {
        linesAboveLive = 0f
    }
}

@Composable
fun rememberCellMetrics(measurer: TextMeasurer, fontSize: TextUnit): CellMetrics = remember(measurer, fontSize) {
    val sample = "M".repeat(SAMPLE_LENGTH)
    val layout = measurer.measure(sample, TextStyle(fontFamily = FontFamily.Monospace, fontSize = fontSize))
    CellMetrics(width = layout.size.width.toFloat() / SAMPLE_LENGTH, height = layout.size.height.toFloat())
}

@Composable
fun TerminalCanvas(
    content: TerminalContent,
    viewport: TerminalViewport,
    fontSize: Float,
    onFontSizeChange: (Float) -> Unit,
    onNeedHistory: () -> Unit,
    onReturnToLive: () -> Unit,
    onScrollProgram: (Int) -> Unit,
    onTap: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val measurer = rememberTextMeasurer(cacheSize = 1024)
    val metrics = rememberCellMetrics(measurer, fontSize.sp)
    val colors = remember { ColorCache() }
    val density = LocalDensity.current
    val horizontal = rememberScrollState()
    val screen = content.screen
    val selectionColor = Color(0x668CAAEE)

    BoxWithConstraints(modifier = modifier.background(colors[screen.background] ?: Color.Black).clipToBounds()) {
        val viewportHeight = constraints.maxHeight.toFloat()
        val bottomPadding = with(density) { TerminalBottomPadding.toPx() }
        val viewportRows = ((viewportHeight - bottomPadding) / metrics.height).coerceAtLeast(1f)
        val cursorRow = screen.cursor?.row ?: 0
        val fitsInView = screen.rows <= viewportRows
        val usedRows = when {
            screen.altScreen && fitsInView -> screen.rows
            screen.altScreen -> cursorRow + 2
            else -> maxOf(cursorRow + 2, screen.lastUsedRow + 1)
        }
        val history = content.historyRows.toFloat()
        val highestTop = history + (screen.rows - viewportRows).coerceAtLeast(0f)
        val anchoredTop = history + usedRows.coerceAtMost(screen.rows) - viewportRows
        val liveTop = if (screen.altScreen && !fitsInView) anchoredTop.coerceIn(history, highestTop) else anchoredTop.coerceAtMost(highestTop)
        val scrollableAbove = liveTop.coerceAtLeast(0f)
        val blankBelow = !screen.altScreen && usedRows < viewportRows
        LaunchedEffect(blankBelow, content.historyRows == 0) {
            if (blankBelow && content.historyRows == 0) onNeedHistory()
        }
        fun top(): Float = liveTop - viewport.linesAboveLive.coerceIn(0f, scrollableAbove)

        val currentLiveTop = rememberUpdatedState(scrollableAbove)
        val currentTop = rememberUpdatedState(::top)
        val currentContent = rememberUpdatedState(content)
        val currentMetrics = rememberUpdatedState(metrics)
        val currentFontSize = rememberUpdatedState(fontSize)
        val needHistory = rememberUpdatedState(onNeedHistory)
        val returnToLive = rememberUpdatedState(onReturnToLive)
        val scrollProgram = rememberUpdatedState(onScrollProgram)
        val tap = rememberUpdatedState(onTap)
        val programScroll = remember { floatArrayOf(0f) }

        val scrollState = rememberScrollableState { delta ->
            val lineHeight = currentMetrics.value.height
            if (currentContent.value.screen.altScreen) {
                programScroll[0] += delta
                val lines = (programScroll[0] / lineHeight).toInt()
                if (lines != 0) {
                    scrollProgram.value(-lines)
                    programScroll[0] -= lines * lineHeight
                }
                return@rememberScrollableState delta
            }
            val before = viewport.linesAboveLive.coerceIn(0f, currentLiveTop.value)
            val after = (before + delta / lineHeight).coerceIn(0f, currentLiveTop.value)
            if (delta > 0 && (before <= 0f || after >= currentLiveTop.value)) needHistory.value()
            viewport.linesAboveLive = after
            if (after <= 0f && before > 0f) returnToLive.value()
            (after - before) * lineHeight
        }

        fun cellAt(position: Offset): CellPosition {
            val currentMetrics = currentMetrics.value
            val terminal = currentContent.value
            val column = ((position.x + horizontal.value) / currentMetrics.width).toInt().coerceIn(0, (terminal.columns - 1).coerceAtLeast(0))
            val row = floor(currentTop.value() + position.y / currentMetrics.height).toInt().coerceIn(0, (terminal.rowCount - 1).coerceAtLeast(0))
            return CellPosition(row, column)
        }

        Box(
            Modifier
                .scrollable(scrollState, Orientation.Vertical, enabled = !viewport.selecting)
                .pointerInput(Unit) {
                    detectTapGestures(onTap = {
                        if (viewport.selection != null) viewport.selection = null else tap.value()
                    })
                }
                .pointerInput(Unit) {
                    detectDragGesturesAfterLongPress(
                        onDragStart = { position ->
                            val word = currentContent.value.wordAt(cellAt(position))
                            viewport.selectionWord = word
                            viewport.selection = word
                            viewport.selecting = true
                        },
                        onDrag = { change, _ ->
                            viewport.extendSelection(cellAt(change.position))
                            change.consume()
                        },
                        onDragEnd = { viewport.selecting = false },
                        onDragCancel = { viewport.selecting = false },
                    )
                }
                .pointerInput(Unit) {
                    awaitEachGesture {
                        awaitFirstDown(requireUnconsumed = false)
                        do {
                            val event = awaitPointerEvent()
                            if (event.changes.size >= 2) {
                                val zoom = event.calculateZoom()
                                if (zoom != 1f) {
                                    onFontSizeChange((currentFontSize.value * zoom).coerceIn(MIN_FONT_SIZE, MAX_FONT_SIZE))
                                }
                                event.changes.forEach { it.consume() }
                            }
                        } while (event.changes.any { it.pressed })
                    }
                },
        ) {
            val widthDp = with(density) { (metrics.width * screen.columns).toDp() }
            val heightDp = with(density) { viewportHeight.toDp() }
            Box(Modifier.horizontalScroll(horizontal)) {
                Canvas(Modifier.size(widthDp, heightDp).semantics { contentDescription = content.screenText }) {
                    drawTerminal(content, top(), viewport.selection, selectionColor, metrics, measurer, colors, fontSize.sp)
                }
            }
        }
    }
}

private fun DrawScope.drawTerminal(
    content: TerminalContent,
    top: Float,
    selection: Selection?,
    selectionColor: Color,
    metrics: CellMetrics,
    measurer: TextMeasurer,
    colors: ColorCache,
    fontSize: TextUnit,
) {
    val screen = content.screen
    val defaultBackground = colors[screen.background] ?: Color.Black
    val defaultForeground = colors[screen.foreground] ?: Color.White
    drawRect(defaultBackground, size = size)

    val firstRow = floor(top).toInt()
    val offsetY = -(top - firstRow) * metrics.height
    val visibleRows = (size.height / metrics.height).toInt() + 2

    for (index in 0 until visibleRows) {
        val row = firstRow + index
        if (row >= content.rowCount) break
        if (row < 0) continue
        val y = offsetY + index * metrics.height
        val styles = content.styles(row)
        for (span in content.spans(row)) {
            val style = styles[span.style]
            var foreground = style?.let { colors[it.fg] } ?: defaultForeground
            var background = style?.let { colors[it.bg] } ?: defaultBackground
            if (style?.inverse == true) foreground = background.also { background = foreground }
            val left = span.column * metrics.width
            val width = span.width * metrics.width
            if (background != defaultBackground) {
                drawRect(background, topLeft = Offset(left, y), size = Size(width, metrics.height))
            }
            if (style?.invisible == true || span.text.isBlank()) continue
            if (style?.faint == true) foreground = foreground.copy(alpha = 0.6f)
            drawText(
                textMeasurer = measurer,
                text = TerminalText.asText(span.text),
                topLeft = Offset(left, y),
                style = textStyle(style, foreground, fontSize),
                softWrap = false,
            )
        }
        selection?.columnsOn(row, content.columns)?.let { columns ->
            drawRect(
                selectionColor,
                topLeft = Offset(columns.first * metrics.width, y),
                size = Size((columns.last - columns.first + 1) * metrics.width, metrics.height),
            )
        }
    }

    val cursor = screen.cursor ?: return
    if (!cursor.visible || cursor.row !in 0 until screen.rows) return
    val cursorY = offsetY + (content.historyRows + cursor.row - firstRow) * metrics.height
    if (cursorY < -metrics.height || cursorY > size.height) return
    val cursorColor = defaultForeground.copy(alpha = 0.7f)
    val left = cursor.column * metrics.width
    val stroke = 2.dp.toPx()
    when (cursor.shape) {
        "bar" -> drawRect(cursorColor, Offset(left, cursorY), Size(stroke, metrics.height))
        "underline" -> drawRect(cursorColor, Offset(left, cursorY + metrics.height - stroke), Size(metrics.width, stroke))
        "block_hollow" -> drawRect(cursorColor, Offset(left, cursorY), Size(metrics.width, metrics.height), style = Stroke(stroke))
        else -> drawRect(cursorColor, Offset(left, cursorY), Size(metrics.width, metrics.height))
    }
}

private fun textStyle(style: CellStyle?, color: Color, fontSize: TextUnit): TextStyle {
    val decorations = buildList {
        if (style?.underline == true) add(TextDecoration.Underline)
        if (style?.strikethrough == true) add(TextDecoration.LineThrough)
    }
    return TextStyle(
        color = color,
        fontFamily = FontFamily.Monospace,
        fontSize = fontSize,
        fontWeight = if (style?.bold == true) FontWeight.Bold else FontWeight.Normal,
        fontStyle = if (style?.italic == true) FontStyle.Italic else FontStyle.Normal,
        textDecoration = if (decorations.isEmpty()) null else TextDecoration.combine(decorations),
    )
}

class ColorCache {
    private val cache = HashMap<String, Color?>()

    operator fun get(hex: String): Color? = cache.getOrPut(hex) { parseHexColor(hex) }
}

fun parseHexColor(hex: String): Color? {
    val digits = hex.removePrefix("#")
    val value = digits.toLongOrNull(16) ?: return null
    return when (digits.length) {
        6 -> Color(0xFF000000 or value)
        8 -> Color(((value and 0xFF) shl 24) or (value ushr 8))
        else -> null
    }
}

fun fittingGrid(widthPx: Int, heightPx: Int, metrics: CellMetrics): Pair<Int, Int> =
    (widthPx / metrics.width).toInt().coerceIn(20, 300) to (heightPx / metrics.height).toInt().coerceIn(5, 120)

val TerminalBottomPadding = 4.dp

const val MIN_FONT_SIZE = 6f
const val MAX_FONT_SIZE = 24f
private const val SAMPLE_LENGTH = 20
