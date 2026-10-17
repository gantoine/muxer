package com.gantoine.muxer.chat

object FileMentions {
    private val trailingMention = Regex("(?:^|\\s)@([^\\s@]*)$")

    fun query(draft: String): String? = trailingMention.find(draft)?.groupValues?.get(1)

    fun complete(draft: String, path: String): String {
        val match = trailingMention.find(draft) ?: return draft
        val at = match.groups[1]!!.range.first - 1
        return draft.substring(0, at) + "@" + path + if (path.endsWith("/")) "" else " "
    }
}
