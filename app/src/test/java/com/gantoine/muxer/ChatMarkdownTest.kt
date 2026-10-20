package com.gantoine.muxer

import com.gantoine.muxer.chat.ChatMarkdown
import com.gantoine.muxer.chat.InlineSpan
import com.gantoine.muxer.chat.MarkdownBlock
import org.junit.Assert.assertEquals
import org.junit.Test

class ChatMarkdownTest {
    @Test
    fun splitsParagraphsHeadingsListsAndCode() {
        val blocks = ChatMarkdown.blocks("## Plan\n\nFirst line\n- item one\n  - nested\n\n```kotlin\nval x = 1\n```\nAfter")
        assertEquals(
            listOf(
                MarkdownBlock.Heading(listOf(InlineSpan("Plan"))),
                MarkdownBlock.Paragraph(listOf(InlineSpan("First line"))),
                MarkdownBlock.ListItem("•", 0, listOf(InlineSpan("item one"))),
                MarkdownBlock.ListItem("◦", 1, listOf(InlineSpan("nested"))),
                MarkdownBlock.Code("val x = 1", "kotlin"),
                MarkdownBlock.Paragraph(listOf(InlineSpan("After"))),
            ),
            blocks,
        )
    }

    @Test
    fun numberedListsKeepTheirNumbersAndWrappedLines() {
        assertEquals(
            listOf(
                MarkdownBlock.ListItem("1.", 0, listOf(InlineSpan("first\ncontinues"))),
                MarkdownBlock.ListItem("2.", 0, listOf(InlineSpan("second"))),
            ),
            ChatMarkdown.blocks("1. first\n   continues\n2) second"),
        )
    }

    @Test
    fun tablesQuotesAndRules() {
        val blocks = ChatMarkdown.blocks("| Name | Size |\n|---|:--:|\n| a | 1 |\n| b |\n\n> quoted\n> more\n\n---")
        assertEquals(
            listOf(
                MarkdownBlock.Table(
                    header = listOf(listOf(InlineSpan("Name")), listOf(InlineSpan("Size"))),
                    rows = listOf(
                        listOf(listOf(InlineSpan("a")), listOf(InlineSpan("1"))),
                        listOf(listOf(InlineSpan("b")), emptyList()),
                    ),
                ),
                MarkdownBlock.Quote(listOf(InlineSpan("quoted\nmore"))),
                MarkdownBlock.Rule,
            ),
            blocks,
        )
    }

    @Test
    fun pipesWithoutASeparatorStayText() {
        assertEquals(listOf(MarkdownBlock.Paragraph(listOf(InlineSpan("a | b")))), ChatMarkdown.blocks("a | b"))
    }

    @Test
    fun unclosedCodeStillShows() {
        assertEquals(listOf(MarkdownBlock.Code("partial")), ChatMarkdown.blocks("```\npartial"))
    }

    @Test
    fun inlineBoldCodeAndLinks() {
        assertEquals(
            listOf(
                InlineSpan("Run "),
                InlineSpan("go test", code = true),
                InlineSpan(", it is "),
                InlineSpan("fast", bold = true),
                InlineSpan(". See "),
                InlineSpan("docs", url = "https://x.y"),
            ),
            ChatMarkdown.inline("Run `go test`, it is **fast**. See [docs](https://x.y)"),
        )
    }

    @Test
    fun italicsStrikeAndNesting() {
        assertEquals(
            listOf(
                InlineSpan("a", italic = true),
                InlineSpan(" "),
                InlineSpan("b", italic = true),
                InlineSpan(" "),
                InlineSpan("gone", strike = true),
                InlineSpan(" "),
                InlineSpan("very ", bold = true),
                InlineSpan("much", bold = true, italic = true),
            ),
            ChatMarkdown.inline("*a* _b_ ~~gone~~ **very _much_**"),
        )
    }

    @Test
    fun snakeCaseAndArithmeticAreNotItalic() {
        assertEquals(listOf(InlineSpan("call my_var_name with 2 * 3 * 4")), ChatMarkdown.inline("call my_var_name with 2 * 3 * 4"))
    }

    @Test
    fun bareUrlsBecomeLinksWithoutTrailingPunctuation() {
        assertEquals(
            listOf(InlineSpan("See "), InlineSpan("https://x.y/a?b=1", url = "https://x.y/a?b=1"), InlineSpan(".")),
            ChatMarkdown.inline("See https://x.y/a?b=1."),
        )
    }

    @Test
    fun linksWhoseTextNamesAnotherSiteShowTheirRealTarget() {
        assertEquals(
            listOf(InlineSpan("https://evil.example/login", url = "https://evil.example/login")),
            ChatMarkdown.inline("[https://github.com/you/repo](https://evil.example/login)"),
        )
        assertEquals(
            listOf(InlineSpan("https://evil.example", url = "https://evil.example")),
            ChatMarkdown.inline("[www.github.com](https://evil.example)"),
        )
        assertEquals(
            listOf(InlineSpan("mailto:a@evil.example", url = "mailto:a@evil.example")),
            ChatMarkdown.inline("[https://github.com](mailto:a@evil.example)"),
        )
    }

    @Test
    fun linksWhoseTextNamesTheSameSiteKeepTheirText() {
        assertEquals(
            listOf(InlineSpan("https://github.com/you", url = "https://www.github.com/you/repo")),
            ChatMarkdown.inline("[https://github.com/you](https://www.github.com/you/repo)"),
        )
        assertEquals(
            listOf(InlineSpan("README.md", url = "https://x.y/README.md")),
            ChatMarkdown.inline("[README.md](https://x.y/README.md)"),
        )
    }

    @Test
    fun relativeLinksStayPlainText() {
        assertEquals(listOf(InlineSpan("main.go")), ChatMarkdown.inline("[main.go](src/main.go)"))
    }

    @Test
    fun linkTargetsPaddedWithSpacesStillParse() {
        assertEquals(
            listOf(InlineSpan("Fixed "), InlineSpan("main.go"), InlineSpan(".")),
            ChatMarkdown.inline("Fixed [main.go]( /src/app/main.go ).")
        )
    }
}
