package com.gantoine.muxer.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.withLink
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.chat.ChatMarkdown
import com.gantoine.muxer.chat.InlineSpan
import com.gantoine.muxer.chat.MarkdownBlock
import kotlinx.coroutines.delay

private const val COPIED_FEEDBACK_MILLIS = 1500L

@Composable
fun MarkdownText(text: String) {
    val styles = rememberSpanStyles()
    SelectionContainer {
        Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            for (block in remember(text) { ChatMarkdown.blocks(text) }) {
                when (block) {
                    is MarkdownBlock.Heading -> Text(annotated(block.spans, styles), style = MaterialTheme.typography.titleMedium)
                    is MarkdownBlock.Paragraph -> Text(annotated(block.spans, styles), style = MaterialTheme.typography.bodyLarge)
                    is MarkdownBlock.ListItem -> ListItem(block, styles)
                    is MarkdownBlock.Quote -> Quote(block, styles)
                    is MarkdownBlock.Code -> CodeBlock(block, styles.codeBackground)
                    is MarkdownBlock.Table -> Table(block, styles)
                    MarkdownBlock.Rule -> HorizontalDivider(Modifier.padding(vertical = 4.dp))
                }
            }
        }
    }
}

@Composable
fun CopyButton(text: String, description: String, modifier: Modifier = Modifier) {
    val clipboard = LocalClipboardManager.current
    var copied by remember { mutableStateOf(false) }
    LaunchedEffect(copied) {
        if (copied) {
            delay(COPIED_FEEDBACK_MILLIS)
            copied = false
        }
    }
    IconButton(
        onClick = {
            clipboard.setText(AnnotatedString(text))
            copied = true
        },
        modifier = modifier.size(32.dp),
    ) {
        Icon(
            if (copied) Icons.Filled.Check else Icons.Outlined.ContentCopy,
            contentDescription = if (copied) "Copied" else description,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(16.dp),
        )
    }
}

private class SpanStyles(val codeBackground: Color, val link: Color)

@Composable
private fun rememberSpanStyles(): SpanStyles {
    val colors = MaterialTheme.colorScheme
    return remember(colors) { SpanStyles(colors.surfaceContainerHigh, colors.primary) }
}

@Composable
private fun ListItem(item: MarkdownBlock.ListItem, styles: SpanStyles) {
    Row(Modifier.fillMaxWidth().padding(start = (item.depth * 16).dp)) {
        Text(
            item.marker,
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.widthIn(min = if (item.marker.length > 1) 28.dp else 18.dp).padding(end = 6.dp),
        )
        Text(annotated(item.spans, styles), style = MaterialTheme.typography.bodyLarge)
    }
}

@Composable
private fun Quote(quote: MarkdownBlock.Quote, styles: SpanStyles) {
    Row(Modifier.fillMaxWidth().height(IntrinsicSize.Min)) {
        Box(Modifier.width(3.dp).fillMaxHeight().background(MaterialTheme.colorScheme.outlineVariant, RoundedCornerShape(2.dp)))
        Text(
            annotated(quote.spans, styles),
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(start = 12.dp),
        )
    }
}

@Composable
private fun CodeBlock(block: MarkdownBlock.Code, background: Color) {
    Surface(shape = RoundedCornerShape(12.dp), color = background, modifier = Modifier.fillMaxWidth()) {
        Column {
            Row(Modifier.fillMaxWidth().padding(start = 12.dp, end = 4.dp, top = 2.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(
                    block.language,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.weight(1f),
                )
                CopyButton(block.code, "Copy the code")
            }
            Text(
                block.code,
                fontFamily = FontFamily.Monospace,
                style = MaterialTheme.typography.bodySmall,
                softWrap = false,
                modifier = Modifier.horizontalScroll(rememberScrollState()).padding(start = 12.dp, end = 12.dp, bottom = 12.dp),
            )
        }
    }
}

@Composable
private fun Table(table: MarkdownBlock.Table, styles: SpanStyles) {
    val outline = MaterialTheme.colorScheme.outlineVariant
    BoxWithConstraints(Modifier.fillMaxWidth().border(1.dp, outline, RoundedCornerShape(12.dp))) {
        Row(Modifier.horizontalScroll(rememberScrollState()).widthIn(min = maxWidth)) {
            table.header.forEachIndexed { column, header ->
                Column(Modifier.width(IntrinsicSize.Max)) {
                    TableCell(annotated(header, styles), header = true)
                    HorizontalDivider(color = outline)
                    for (row in table.rows) TableCell(annotated(row[column], styles), header = false)
                }
            }
            Column(Modifier.weight(1f)) {
                TableCell(AnnotatedString(""), header = true)
                HorizontalDivider(color = outline)
            }
        }
    }
}

@Composable
private fun TableCell(text: AnnotatedString, header: Boolean) {
    Text(
        text,
        style = MaterialTheme.typography.bodyMedium,
        fontWeight = if (header) FontWeight.SemiBold else null,
        softWrap = false,
        modifier = Modifier.padding(horizontal = 12.dp, vertical = 8.dp),
    )
}

private fun annotated(spans: List<InlineSpan>, styles: SpanStyles): AnnotatedString = buildAnnotatedString {
    for (span in spans) {
        val style = SpanStyle(
            fontFamily = if (span.code) FontFamily.Monospace else null,
            background = if (span.code) styles.codeBackground else Color.Unspecified,
            fontWeight = if (span.bold) FontWeight.SemiBold else null,
            fontStyle = if (span.italic) FontStyle.Italic else null,
            textDecoration = if (span.strike) TextDecoration.LineThrough else null,
        )
        val url = span.url
        if (url == null) {
            withStyle(style) { append(span.text) }
        } else {
            val linkStyle = SpanStyle(color = styles.link, textDecoration = TextDecoration.Underline)
            withLink(LinkAnnotation.Url(url, TextLinkStyles(linkStyle))) { withStyle(style) { append(span.text) } }
        }
    }
}
