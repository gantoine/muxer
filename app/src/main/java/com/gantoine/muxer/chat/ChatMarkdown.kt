package com.gantoine.muxer.chat

sealed interface MarkdownBlock {
    data class Paragraph(val spans: List<InlineSpan>) : MarkdownBlock
    data class Heading(val spans: List<InlineSpan>) : MarkdownBlock
    data class Code(val code: String, val language: String = "") : MarkdownBlock
    data class ListItem(val marker: String, val depth: Int, val spans: List<InlineSpan>) : MarkdownBlock
    data class Quote(val spans: List<InlineSpan>) : MarkdownBlock
    data class Table(val header: List<List<InlineSpan>>, val rows: List<List<List<InlineSpan>>>) : MarkdownBlock
    data object Rule : MarkdownBlock
}

data class InlineSpan(
    val text: String,
    val bold: Boolean = false,
    val code: Boolean = false,
    val italic: Boolean = false,
    val strike: Boolean = false,
    val url: String? = null,
)

object ChatMarkdown {
    private const val MAX_LIST_DEPTH = 3

    private val fence = Regex("^\\s*```\\s*([\\w+#.-]*)")
    private val heading = Regex("^#{1,6}\\s+(.*)$")
    private val listItem = Regex("^(\\s*)([-*+]|\\d{1,3}[.)])\\s+(.*)$")
    private val rule = Regex("^\\s*([-*_])(\\s*\\1){2,}\\s*$")
    private val quote = Regex("^\\s*>\\s?(.*)$")
    private val tableSeparator = Regex("^\\s*\\|?\\s*:?-+:?\\s*(\\|\\s*:?-+:?\\s*)*\\|?\\s*$")
    private val inlineToken = Regex(
        "`([^`]+)`" +
            "|\\*\\*(.+?)\\*\\*" +
            "|__(.+?)__" +
            "|~~(.+?)~~" +
            "|\\[([^\\]]+)]\\(([^)\\s]+)\\)" +
            "|(?<![\\w*])\\*(?![\\s*])(.+?)(?<!\\s)\\*(?![\\w*])" +
            "|(?<![\\w_])_(?![\\s_])(.+?)(?<!\\s)_(?![\\w_])" +
            "|(https?://[^\\s<>()\\[\\]`]*[^\\s<>()\\[\\]`.,;:!?'\"])",
    )

    fun blocks(text: String): List<MarkdownBlock> {
        val blocks = mutableListOf<MarkdownBlock>()
        val lines = text.lines()
        val paragraph = mutableListOf<String>()
        val quoted = mutableListOf<String>()
        var item: PendingItem? = null

        fun flush() {
            if (paragraph.isNotEmpty()) blocks += MarkdownBlock.Paragraph(inline(paragraph.joinToString("\n")))
            if (quoted.isNotEmpty()) blocks += MarkdownBlock.Quote(inline(quoted.joinToString("\n")))
            item?.let { blocks += MarkdownBlock.ListItem(it.marker, it.depth, inline(it.lines.joinToString("\n"))) }
            paragraph.clear()
            quoted.clear()
            item = null
        }

        var index = 0
        while (index < lines.size) {
            val line = lines[index]
            val opening = fence.find(line)
            if (opening != null) {
                flush()
                val code = mutableListOf<String>()
                index++
                while (index < lines.size && !fence.containsMatchIn(lines[index])) code += lines[index++]
                blocks += MarkdownBlock.Code(code.joinToString("\n"), opening.groupValues[1])
                index++
                continue
            }
            if (line.isBlank()) {
                flush()
                index++
                continue
            }
            if (isTableStart(lines, index)) {
                flush()
                val header = cells(line)
                val rows = mutableListOf<List<List<InlineSpan>>>()
                index += 2
                while (index < lines.size && lines[index].contains('|') && lines[index].isNotBlank()) {
                    rows += cells(lines[index]).let { row -> List(header.size) { row.getOrElse(it) { emptyList() } } }
                    index++
                }
                blocks += MarkdownBlock.Table(header, rows)
                continue
            }
            val title = heading.matchEntire(line.trim())
            val listed = listItem.matchEntire(line)
            val quotedLine = quote.matchEntire(line)
            when {
                title != null -> {
                    flush()
                    blocks += MarkdownBlock.Heading(inline(title.groupValues[1]))
                }
                rule.matches(line) -> {
                    flush()
                    blocks += MarkdownBlock.Rule
                }
                listed != null -> {
                    flush()
                    val (indent, marker, body) = listed.destructured
                    val depth = (indent.replace("\t", "    ").length / 2).coerceAtMost(MAX_LIST_DEPTH)
                    item = PendingItem(bulletFor(marker, depth), depth, mutableListOf(body))
                }
                quotedLine != null -> {
                    if (quoted.isEmpty()) flush()
                    quoted += quotedLine.groupValues[1]
                }
                item != null -> item!!.lines += line.trim()
                quoted.isNotEmpty() -> quoted += line.trim()
                else -> paragraph += line
            }
            index++
        }
        flush()
        return blocks
    }

    fun inline(text: String): List<InlineSpan> {
        val spans = mutableListOf<InlineSpan>()
        var position = 0
        for (match in inlineToken.findAll(text)) {
            if (match.range.first > position) spans += InlineSpan(text.substring(position, match.range.first))
            val groups = match.groupValues
            spans += when {
                groups[1].isNotEmpty() -> listOf(InlineSpan(groups[1], code = true))
                groups[2].isNotEmpty() -> inline(groups[2]).map { it.copy(bold = true) }
                groups[3].isNotEmpty() -> inline(groups[3]).map { it.copy(bold = true) }
                groups[4].isNotEmpty() -> inline(groups[4]).map { it.copy(strike = true) }
                groups[5].isNotEmpty() -> inline(groups[5]).map { it.copy(url = linkTarget(groups[6])) }
                groups[7].isNotEmpty() -> inline(groups[7]).map { it.copy(italic = true) }
                groups[8].isNotEmpty() -> inline(groups[8]).map { it.copy(italic = true) }
                else -> listOf(InlineSpan(groups[9], url = groups[9]))
            }
            position = match.range.last + 1
        }
        if (position < text.length) spans += InlineSpan(text.substring(position))
        return spans
    }

    private fun linkTarget(url: String): String? =
        url.takeIf { it.startsWith("https://") || it.startsWith("http://") || it.startsWith("mailto:") }

    private fun bulletFor(marker: String, depth: Int): String = when {
        marker.first().isDigit() -> marker.dropLast(1) + "."
        depth == 0 -> "•"
        else -> "◦"
    }

    private fun isTableStart(lines: List<String>, index: Int): Boolean =
        lines[index].contains('|') &&
            index + 1 < lines.size &&
            lines[index + 1].contains('-') &&
            tableSeparator.matches(lines[index + 1]) &&
            cells(lines[index]).size == lines[index + 1].trim().trim('|').split('|').size

    private fun cells(line: String): List<List<InlineSpan>> =
        line.trim().removePrefix("|").removeSuffix("|").split(Regex("(?<!\\\\)\\|")).map { inline(it.trim().replace("\\|", "|")) }

    private class PendingItem(val marker: String, val depth: Int, val lines: MutableList<String>)
}
