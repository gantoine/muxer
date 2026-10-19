package com.gantoine.muxer

import com.gantoine.muxer.model.NewTask
import com.gantoine.muxer.protocol.InstalledAgentsResponse
import com.gantoine.muxer.protocol.RecentSession
import com.gantoine.muxer.protocol.RelayJson
import com.gantoine.muxer.protocol.TerminalInfo
import com.gantoine.muxer.protocol.WorkspaceInfo
import java.time.Instant
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class NewTaskTest {
    private val now = Instant.parse("2026-10-18T12:00:00Z").epochSecond.toDouble()

    @Test
    fun foldersComeFromWorkspacesAndSessionsNewestFirstWithoutRepeats() {
        val workspaces = listOf(
            WorkspaceInfo(
                id = "w1",
                currentDirectory = "/Users/me/code/api/",
                lastActivityAt = now - 60,
                terminals = listOf(TerminalInfo(id = "t1", currentDirectory = "/Users/me/code/api/server")),
            ),
            WorkspaceInfo(id = "w2", currentDirectory = "/Users/me/code/docs", lastActivityAt = now - 7_200),
            WorkspaceInfo(id = "w3", currentDirectory = null, terminals = listOf(TerminalInfo(id = "t3", currentDirectory = "relative"))),
        )
        val sessions = listOf(
            RecentSession(agent = "claude", sessionId = "s1", cwd = "/Users/me/code/docs", modified = "2026-10-18T11:30:00Z"),
            RecentSession(agent = "codex", sessionId = "s2", cwd = "/Users/me/code/web", modified = "2026-10-17T09:00:00Z"),
            RecentSession(agent = "codex", sessionId = "s3", cwd = null, modified = "2026-10-18T11:59:00Z"),
        )
        assertEquals(
            listOf("/Users/me/code/api", "/Users/me/code/api/server", "/Users/me/code/docs", "/Users/me/code/web"),
            NewTask.folders(workspaces, sessions),
        )
    }

    @Test
    fun temporaryFoldersAreNotSuggested() {
        val workspaces = listOf(WorkspaceInfo(id = "w1", currentDirectory = "/Users/me/code/api", lastActivityAt = now - 86_400))
        val sessions = listOf(
            RecentSession(agent = "claude", sessionId = "s1", cwd = "/private/tmp/scratch/agentdir", modified = "2026-10-18T11:30:00Z"),
            RecentSession(agent = "claude", sessionId = "s2", cwd = "/tmp", modified = "2026-10-18T11:31:00Z"),
            RecentSession(agent = "codex", sessionId = "s3", cwd = "/var/folders/xy/T/run", modified = "2026-10-18T11:32:00Z"),
            RecentSession(agent = "codex", sessionId = "s4", cwd = "/tmpfiles/keep", modified = "2026-10-18T11:33:00Z"),
        )
        assertEquals(listOf("/tmpfiles/keep", "/Users/me/code/api"), NewTask.folders(workspaces, sessions))
    }

    @Test
    fun suggestionsMatchWhatWasTypedButNotTheExactFolder() {
        val folders = listOf("/Users/me/code/api", "/Users/me/code/docs", "/Users/me/notes")
        assertEquals(folders, NewTask.suggestions(folders, ""))
        assertEquals(listOf("/Users/me/code/api", "/Users/me/code/docs"), NewTask.suggestions(folders, "~/code"))
        assertEquals(listOf("/Users/me/code/docs"), NewTask.suggestions(folders, "DOC"))
        assertEquals(emptyList<String>(), NewTask.suggestions(folders, "~/notes/"))
        assertEquals(listOf("/Users/me/code/api"), NewTask.suggestions(folders, "", limit = 1))
    }

    @Test
    fun aTaskNeedsAnAgentAFolderAndAPrompt() {
        assertTrue(NewTask.canStart("claude", "~/code/api", "Fix the tests"))
        assertFalse(NewTask.canStart(null, "~/code/api", "Fix the tests"))
        assertFalse(NewTask.canStart("codex", " ", "Fix the tests"))
        assertFalse(NewTask.canStart("codex", "~/code/api", "\n "))
    }

    @Test
    fun installedAgentsDecode() {
        val decoded = RelayJson.decodeFromString(InstalledAgentsResponse.serializer(), """{"agents":["codex"]}""")
        assertEquals(listOf("codex"), decoded.agents)
    }
}
