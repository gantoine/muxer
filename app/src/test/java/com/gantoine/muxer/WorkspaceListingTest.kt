package com.gantoine.muxer

import com.gantoine.muxer.model.AgentStatus
import com.gantoine.muxer.model.WorkspaceEntry
import com.gantoine.muxer.model.WorkspaceFilter
import com.gantoine.muxer.model.WorkspaceListing
import com.gantoine.muxer.protocol.TerminalInfo
import com.gantoine.muxer.protocol.WorkspaceGroupInfo
import com.gantoine.muxer.protocol.WorkspaceInfo
import java.time.Instant
import java.time.ZoneId
import java.util.Locale
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class WorkspaceListingTest {
    private val workspace = WorkspaceInfo(
        id = "w1",
        title = "API refactor",
        currentDirectory = "/Users/me/code/api",
        terminals = listOf(TerminalInfo(id = "t1", title = "claude", agentState = "Waiting for input")),
    )

    @Test
    fun searchMatchesTitlesFoldersAndTerminalsIgnoringCase() {
        assertTrue(WorkspaceListing.matches(workspace, ""))
        assertTrue(WorkspaceListing.matches(workspace, "refactor"))
        assertTrue(WorkspaceListing.matches(workspace, "CODE/API"))
        assertTrue(WorkspaceListing.matches(workspace, "  claude "))
        assertTrue(WorkspaceListing.matches(workspace, "waiting"))
        assertFalse(WorkspaceListing.matches(workspace, "billing"))
    }

    @Test
    fun agentSourcePrefersTheLastUsedAgentPane() {
        val mixed = workspace.copy(
            terminals = listOf(
                TerminalInfo(id = "t1", title = "zsh"),
                TerminalInfo(id = "t2", title = "claude", agentSource = "claude"),
                TerminalInfo(id = "t3", title = "codex", agentSource = "codex", isFocused = true),
            ),
        )
        assertEquals("codex", WorkspaceListing.agentSource(mixed))
        assertEquals("claude", WorkspaceListing.agentSource(mixed.copy(terminals = mixed.terminals.take(2))))
        assertNull(WorkspaceListing.agentSource(workspace))
    }

    @Test
    fun searchMatchesTerminalBranches() {
        val onBranch = workspace.copy(
            terminals = listOf(TerminalInfo(id = "t1", title = "zsh", gitBranch = "feat/approvals", gitDirty = true)),
        )
        assertTrue(WorkspaceListing.matches(onBranch, "feat/approvals"))
        assertTrue(WorkspaceListing.matches(onBranch, "APPROVALS"))
        assertFalse(WorkspaceListing.matches(onBranch, "approvals*"))
    }

    @Test
    fun timesReadLikeAMessagingApp() {
        val zone = ZoneId.of("America/New_York")
        val now = Instant.parse("2026-10-14T18:00:00Z")
        fun at(iso: String) = Instant.parse(iso).epochSecond.toDouble()

        assertEquals("1:05 PM", WorkspaceListing.time(at("2026-10-14T17:05:00Z"), now, zone, Locale.US))
        assertEquals("Yesterday", WorkspaceListing.time(at("2026-10-13T15:00:00Z"), now, zone, Locale.US))
        assertEquals("Sun", WorkspaceListing.time(at("2026-10-11T15:00:00Z"), now, zone, Locale.US))
        assertEquals("Sep 30", WorkspaceListing.time(at("2026-09-30T15:00:00Z"), now, zone, Locale.US))
        assertNull(WorkspaceListing.time(null as Double?, now, zone, Locale.US))
    }

    @Test
    fun workspacesWithoutAColorKeepTheSameFallbackColor() {
        assertEquals(WorkspaceListing.color(workspace), WorkspaceListing.color(workspace.copy(title = "renamed")))
        assertEquals("#123456", WorkspaceListing.color(workspace.copy(customColor = "#123456")))
        assertTrue(WorkspaceListing.isAgent(workspace))
        assertFalse(WorkspaceListing.isAgent(workspace.copy(terminals = listOf(TerminalInfo(id = "t2")))))
    }
}

class WorkspaceFilteringTest {
    private val waiting = WorkspaceInfo(
        id = "a",
        title = "agent",
        currentDirectory = "/Users/me/fun/cmux",
        terminals = listOf(TerminalInfo(id = "t1", agentSource = "claude", agentState = "waiting_for_input")),
    )
    private val working = waiting.copy(id = "b", terminals = listOf(TerminalInfo(id = "t2", agentState = "running")))
    private val plain = WorkspaceInfo(id = "c", title = "shell", isPinned = true, terminals = listOf(TerminalInfo(id = "t3"), TerminalInfo(id = "t4")))
    private val unread = WorkspaceInfo(id = "d", title = "logs", unreadCount = 2, preview = "  tail -f server.log  ")

    @Test
    fun agentStatesAreClassifiedAndHumanized() {
        assertEquals(AgentStatus.NeedsYou("Waiting for input"), WorkspaceListing.agentStatus(waiting))
        assertEquals(AgentStatus.Working("Running"), WorkspaceListing.agentStatus(working))
        assertEquals(AgentStatus.Idle("Idle"), WorkspaceListing.agentStatus(working.copy(terminals = listOf(TerminalInfo(id = "t6", agentState = "Waiting")))))
        assertEquals(AgentStatus.NeedsYou("Permission request"), WorkspaceListing.agentStatus(working.copy(terminals = listOf(TerminalInfo(id = "t7", agentState = "permission_request")))))
        assertNull(WorkspaceListing.agentStatus(plain))
        assertEquals(AgentStatus.Other("Done"), WorkspaceListing.agentStatus(plain.copy(terminals = listOf(TerminalInfo(id = "t5", agentState = "done")))))
    }

    @Test
    fun filtersSelectTheRightWorkspaces() {
        val all = listOf(waiting, working, plain, unread)
        assertEquals(all, all.filter { WorkspaceListing.passes(it, WorkspaceFilter.All) })
        assertEquals(listOf(waiting, unread), all.filter { WorkspaceListing.passes(it, WorkspaceFilter.NeedsYou) })
        assertEquals(listOf(waiting, working), all.filter { WorkspaceListing.passes(it, WorkspaceFilter.Agents) })
        assertEquals(listOf(plain), all.filter { WorkspaceListing.passes(it, WorkspaceFilter.Pinned) })
        assertEquals(2, WorkspaceListing.count(all, emptyList(), WorkspaceFilter.NeedsYou))
    }

    @Test
    fun subtitlesPreferPreviewThenFolderThenTerminals() {
        assertEquals("tail -f server.log", WorkspaceListing.subtitle(unread))
        assertEquals("~/fun/cmux", WorkspaceListing.subtitle(waiting))
        assertEquals("2 terminals", WorkspaceListing.subtitle(plain))
        assertEquals("Terminal", WorkspaceListing.subtitle(plain.copy(terminals = emptyList())))
        val stale = plain.copy(preview = "Claude is waiting for your input", currentDirectory = "/Users/me/fun/cmux")
        assertEquals("~/fun/cmux", WorkspaceListing.subtitle(stale))
        val live = stale.copy(terminals = listOf(TerminalInfo(id = "t", agentSource = "claude", agentState = "Waiting")))
        assertEquals("Claude is waiting for your input", WorkspaceListing.subtitle(live))
    }

    @Test
    fun groupsCollectTheirMembersUnderTheAnchorInCmuxOrder() {
        val anchor = WorkspaceInfo(id = "g-anchor", title = "Q4", groupId = "g", terminals = listOf(TerminalInfo(id = "t8")))
        val first = working.copy(id = "m1", title = "merge queue", groupId = "g")
        val second = plain.copy(id = "m2", title = "ci", groupId = "g", isPinned = false)
        val group = WorkspaceGroupInfo(id = "g", name = "Q4", anchorWorkspaceId = "g-anchor", memberWorkspaceIds = listOf("g-anchor", "m2", "m1"))
        val all = listOf(plain, anchor, first, second, unread)

        assertEquals(
            listOf(WorkspaceEntry.Single(plain), WorkspaceEntry.Group(group, anchor, listOf(second, first)), WorkspaceEntry.Single(unread)),
            WorkspaceListing.entries(all, listOf(group)),
        )
        assertEquals(
            listOf(WorkspaceEntry.Group(group, anchor, listOf(first), showAnchor = false)),
            WorkspaceListing.entries(all, listOf(group), WorkspaceFilter.Agents),
        )
        assertEquals(
            listOf(WorkspaceEntry.Group(group, anchor, listOf(second, first))),
            WorkspaceListing.entries(all, listOf(group), query = "q4"),
        )
        val pinnedGroup = group.copy(isPinned = true)
        assertEquals(
            listOf(WorkspaceEntry.Single(plain), WorkspaceEntry.Group(pinnedGroup, anchor, listOf(second, first))),
            WorkspaceListing.entries(all, listOf(pinnedGroup), WorkspaceFilter.Pinned),
        )
        assertEquals(4, WorkspaceListing.count(all, listOf(pinnedGroup), WorkspaceFilter.Pinned))
        assertEquals("Q4 terminals", WorkspaceListing.anchorTitle(group, anchor))
        assertEquals("api", WorkspaceListing.anchorTitle(group, anchor.copy(title = "api")))
        assertEquals(listOf(WorkspaceEntry.Single(first.copy(groupId = "missing"))), WorkspaceListing.entries(listOf(first.copy(groupId = "missing")), emptyList()))
    }
}
