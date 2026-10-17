package com.gantoine.muxer.chat

import com.gantoine.muxer.protocol.ChatEntry
import com.gantoine.muxer.protocol.ChatMessage

data class ChatState(val sessionId: String, val entries: List<ChatEntry> = emptyList(), val loaded: Boolean = false)

object ChatModel {
    fun startSession(previous: ChatState?, sessionId: String?): ChatState? = when {
        sessionId == null -> null
        previous?.sessionId == sessionId -> previous
        else -> ChatState(sessionId)
    }

    fun apply(previous: ChatState?, message: ChatMessage): ChatState? {
        if (previous == null || previous.sessionId != message.sessionId) return previous
        if (message.reset) return previous.copy(entries = message.entries, loaded = true)
        val entries = previous.entries.toMutableList()
        val positions = entries.withIndex().associateTo(mutableMapOf()) { (index, entry) -> entry.id to index }
        for (entry in message.entries) {
            val position = positions[entry.id]
            if (position != null) {
                entries[position] = entry
            } else {
                positions[entry.id] = entries.size
                entries.add(entry)
            }
        }
        return previous.copy(entries = entries, loaded = true)
    }

    fun turnTexts(entries: List<ChatEntry>): Map<String, String> {
        val texts = mutableMapOf<String, String>()
        val turn = mutableListOf<ChatEntry>()
        fun close() {
            turn.lastOrNull()?.let { last -> texts[last.id] = turn.joinToString("\n\n") { it.text.orEmpty() } }
            turn.clear()
        }
        for (entry in entries) {
            when (entry.kind) {
                "user" -> close()
                "assistant" -> if (!entry.text.isNullOrBlank()) turn += entry
            }
        }
        close()
        return texts
    }
}
