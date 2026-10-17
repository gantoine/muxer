package com.gantoine.muxer

import com.gantoine.muxer.chat.ChatModel
import com.gantoine.muxer.chat.ChatState
import com.gantoine.muxer.protocol.ChatEntry
import com.gantoine.muxer.protocol.ChatMessage
import com.gantoine.muxer.protocol.ChatTool
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Test

class ChatModelTest {
    private val hello = ChatEntry("u1", "user", text = "hello")
    private val running = ChatEntry("t1", "tool", tool = ChatTool("Bash", "ls", "running"))

    @Test
    fun sessionsStartEmptyAndEndWithNull() {
        val started = ChatModel.startSession(null, "s1")
        assertEquals(ChatState("s1"), started)
        assertSame(started, ChatModel.startSession(started, "s1"))
        assertEquals(ChatState("s2"), ChatModel.startSession(started, "s2"))
        assertNull(ChatModel.startSession(started, null))
    }

    @Test
    fun resetReplacesAndUpdatesUpsertById() {
        val loaded = ChatModel.apply(ChatState("s1"), ChatMessage("t", "s1", reset = true, entries = listOf(hello, running)))!!
        assertEquals(listOf(hello, running), loaded.entries)
        val done = running.copy(tool = running.tool!!.copy(status = "done"))
        val reply = ChatEntry("a1", "assistant", text = "done")
        val updated = ChatModel.apply(loaded, ChatMessage("t", "s1", entries = listOf(done, reply)))!!
        assertEquals(listOf(hello, done, reply), updated.entries)
    }

    @Test
    fun anEntryUpdatedTwiceInOneMessageAppearsOnce() {
        val loaded = ChatModel.apply(ChatState("s1"), ChatMessage("t", "s1", reset = true, entries = listOf(hello)))!!
        val done = running.copy(tool = running.tool!!.copy(status = "done"))
        val updated = ChatModel.apply(loaded, ChatMessage("t", "s1", entries = listOf(running, done)))!!
        assertEquals(listOf(hello, done), updated.entries)
    }

    @Test
    fun messagesForAnotherSessionAreIgnored() {
        val state = ChatState("s1")
        assertSame(state, ChatModel.apply(state, ChatMessage("t", "old", reset = true, entries = listOf(hello))))
        assertNull(ChatModel.apply(null, ChatMessage("t", "s1", entries = listOf(hello))))
    }

    @Test
    fun turnTextsJoinEachTurnsRepliesUnderItsLastReply() {
        val entries = listOf(
            hello,
            ChatEntry("a1", "assistant", text = "Looking."),
            running,
            ChatEntry("a2", "assistant", text = "Done."),
            ChatEntry("u2", "user", text = "thanks"),
            ChatEntry("a3", "assistant", text = "Anytime."),
            ChatEntry("n1", "notice", text = "Interrupted"),
        )
        assertEquals(mapOf("a2" to "Looking.\n\nDone.", "a3" to "Anytime."), ChatModel.turnTexts(entries))
    }
}
