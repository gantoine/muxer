package com.gantoine.muxer

import com.gantoine.muxer.model.NewTask
import com.gantoine.muxer.net.ConnectionState
import com.gantoine.muxer.protocol.InstalledAgentsResponse
import com.gantoine.muxer.protocol.RecentSession
import com.gantoine.muxer.protocol.RelayJson
import com.gantoine.muxer.protocol.RemoteState
import com.gantoine.muxer.protocol.TerminalInfo
import com.gantoine.muxer.protocol.Welcome
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

    @Test
    fun aTaskInAWorkspaceNeedsAnAgentAWorkspaceOnTheComputerAndAPrompt() {
        val local = WorkspaceInfo(id = "w1", currentDirectory = "/Users/me/code/api")
        val ssh = WorkspaceInfo(id = "w2", remote = RemoteState(state = "connected", destination = "box"))
        assertTrue(NewTask.canStartIn("claude", local, "Fix the tests"))
        assertFalse(NewTask.canStartIn("claude", null, "Fix the tests"))
        assertFalse(NewTask.canStartIn(null, local, "Fix the tests"))
        assertFalse(NewTask.canStartIn("codex", local, " "))
        assertFalse(NewTask.canStartIn("codex", ssh, "Fix the tests"))
    }

    @Test
    fun workspaceChoicesSkipSshWorkspacesAndMatchTitleOrFolderNewestFirst() {
        val workspaces = listOf(
            WorkspaceInfo(id = "w1", title = "API", currentDirectory = "/Users/me/code/api", lastActivityAt = now - 600),
            WorkspaceInfo(id = "w2", title = "Docs site", currentDirectory = "/Users/me/code/docs", lastActivityAt = now - 60),
            WorkspaceInfo(id = "w3", title = "Box", remote = RemoteState(state = "connected"), lastActivityAt = now),
            WorkspaceInfo(id = "w4", title = "Notes", currentDirectory = null, previewAt = now - 30),
        )
        assertEquals(listOf("w4", "w2", "w1"), NewTask.workspaceChoices(workspaces, "").map { it.id })
        assertEquals(listOf("w2"), NewTask.workspaceChoices(workspaces, "docs").map { it.id })
        assertEquals(listOf("w2", "w1"), NewTask.workspaceChoices(workspaces, "~/code/").map { it.id })
        assertEquals(emptyList<String>(), NewTask.workspaceChoices(workspaces, "box").map { it.id })
    }

    @Test
    fun tasksStartInWorkspacesOnlyWhenTheRelayUnderstandsItAndCmuxCanServeIt() {
        fun connected(features: List<String>, unsupported: List<String> = emptyList()) =
            ConnectionState.Connected(Welcome("Studio", "1", 1, "0.65.2", "d1", unsupported = unsupported, features = features))
        assertFalse(connected(emptyList()).has(NewTask.IN_WORKSPACE))
        assertTrue(connected(listOf(NewTask.IN_WORKSPACE)).has(NewTask.IN_WORKSPACE))
        assertFalse(connected(listOf(NewTask.IN_WORKSPACE), unsupported = listOf(NewTask.IN_WORKSPACE)).has(NewTask.IN_WORKSPACE))
        val decoded = RelayJson.decodeFromString(
            Welcome.serializer(),
            """{"mac_name":"Studio","relay_version":"1","protocol_version":1,"cmux_version":"0.65.2","device_id":"d1","features":["start_agent_task.workspace_id"]}""",
        )
        assertTrue(ConnectionState.Connected(decoded).has(NewTask.IN_WORKSPACE))
    }
}
