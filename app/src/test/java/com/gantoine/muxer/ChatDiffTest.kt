package com.gantoine.muxer

import com.gantoine.muxer.chat.ChatDiff
import com.gantoine.muxer.chat.DiffCounts
import com.gantoine.muxer.chat.DiffKind
import com.gantoine.muxer.chat.DiffLine
import com.gantoine.muxer.protocol.ChatHunk
import org.junit.Assert.assertEquals
import org.junit.Test

class ChatDiffTest {
    private val hunk = ChatHunk(oldStart = 10, newStart = 12, lines = listOf(" keep", "-old", "+new", "+extra", " tail"))

    @Test
    fun numbersRemovedLinesFromTheOldFileAndTheRestFromTheNew() {
        assertEquals(
            listOf(
                DiffLine(12, DiffKind.Context, "keep"),
                DiffLine(11, DiffKind.Removed, "old"),
                DiffLine(13, DiffKind.Added, "new"),
                DiffLine(14, DiffKind.Added, "extra"),
                DiffLine(15, DiffKind.Context, "tail"),
            ),
            ChatDiff.lines(hunk),
        )
    }

    @Test
    fun countsAddedAndRemovedLines() {
        assertEquals(DiffCounts(added = 2, removed = 1), ChatDiff.counts(listOf(hunk)))
    }
}
