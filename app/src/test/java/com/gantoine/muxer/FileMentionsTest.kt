package com.gantoine.muxer

import com.gantoine.muxer.chat.FileMentions
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class FileMentionsTest {
    @Test
    fun onlyATrailingMentionOpensThePicker() {
        assertEquals("", FileMentions.query("@"))
        assertEquals("chat", FileMentions.query("look at @chat"))
        assertEquals("app/src/", FileMentions.query("fix\n@app/src/"))
        assertNull(FileMentions.query("look at @chat "))
        assertNull(FileMentions.query("mail me@example.com"))
        assertNull(FileMentions.query("no mention"))
    }

    @Test
    fun completingReplacesTheMention() {
        assertEquals("look at @app/ChatView.kt ", FileMentions.complete("look at @chat", "app/ChatView.kt"))
        assertEquals("@app/src/", FileMentions.complete("@ap", "app/src/"))
        assertEquals("unchanged", FileMentions.complete("unchanged", "x.kt"))
    }
}
