package com.gantoine.muxer

import com.gantoine.muxer.model.WorkspaceListing
import com.gantoine.muxer.protocol.RelayJson
import com.gantoine.muxer.protocol.TerminalInfo
import com.gantoine.muxer.protocol.WorkspacesMessage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class GitSummaryTest {
    private fun terminal(branch: String? = null, dirty: Boolean? = null) =
        TerminalInfo(id = "t1", gitBranch = branch, gitDirty = dirty)

    @Test
    fun summarisesEachTerminalsBranch() {
        assertNull(WorkspaceListing.gitSummary(terminal()))
        assertNull(WorkspaceListing.gitSummary(terminal(branch = " ")))
        assertEquals("main", WorkspaceListing.gitSummary(terminal(branch = "main", dirty = false)))
        assertEquals("feat/x*", WorkspaceListing.gitSummary(terminal(branch = "feat/x", dirty = true)))
    }

    @Test
    fun spellsOutUncommittedChangesForScreenReaders() {
        assertNull(WorkspaceListing.gitDescription(terminal()))
        assertEquals("main", WorkspaceListing.gitDescription(terminal(branch = "main", dirty = false)))
        assertEquals("feat/x, uncommitted changes", WorkspaceListing.gitDescription(terminal(branch = "feat/x", dirty = true)))
    }

    @Test
    fun decodesPerTerminalGitStateFromTheRelay() {
        val listing = RelayJson.decodeFromString(
            WorkspacesMessage.serializer(),
            """{"workspaces":[{"id":"w1","terminals":[{"id":"t1","git_branch":"main","git_dirty":true,"listening_ports":[8000]},{"id":"t2","git_branch":"feat/y"}]}]}""",
        )
        val (first, second) = listing.workspaces.single().terminals
        assertEquals("main*", WorkspaceListing.gitSummary(first))
        assertEquals(listOf(8000), first.listeningPorts)
        assertEquals("feat/y", WorkspaceListing.gitSummary(second))
    }
}
