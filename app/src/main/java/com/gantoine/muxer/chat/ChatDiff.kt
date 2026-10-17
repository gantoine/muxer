package com.gantoine.muxer.chat

import com.gantoine.muxer.protocol.ChatHunk

enum class DiffKind { Added, Removed, Context }

data class DiffLine(val number: Int, val kind: DiffKind, val text: String)

data class DiffCounts(val added: Int, val removed: Int)

object ChatDiff {
    fun lines(hunk: ChatHunk): List<DiffLine> {
        var old = hunk.oldStart
        var new = hunk.newStart
        return hunk.lines.mapNotNull { line ->
            val text = line.drop(1)
            when (line.firstOrNull()) {
                '+' -> DiffLine(new++, DiffKind.Added, text)
                '-' -> DiffLine(old++, DiffKind.Removed, text)
                ' ' -> DiffLine(new++, DiffKind.Context, text).also { old++ }
                else -> null
            }
        }
    }

    fun counts(hunks: List<ChatHunk>): DiffCounts {
        val lines = hunks.flatMap { it.lines }
        return DiffCounts(lines.count { it.startsWith("+") }, lines.count { it.startsWith("-") })
    }
}
