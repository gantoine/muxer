package com.gantoine.muxer

import com.gantoine.muxer.model.AgentNames
import com.gantoine.muxer.notifications.ShortcutPlan
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ShortcutPlanTest {
    @Test
    fun idsNameTheComputerAndWorkspace() {
        assertEquals("ws:mac-1:w-1", ShortcutPlan.id("mac-1", "w-1"))
    }

    @Test
    fun onlyAgentWorkspacesGetShortcutsNewestFirstAcrossComputers() {
        val plan = ShortcutPlan.plan(
            listOf(
                snapshot("a", workspace("quiet", 50.0, shell("s1")), workspace("agent-old", 10.0, claude("c1"))),
                snapshot("b", workspace("agent-new", 20.0, shell("s2"), claude("c2")), workspace("recent", 90.0, shell("s3"))),
            ),
            limit = 10,
        )
        assertEquals(listOf("agent-new", "agent-old"), plan.map { it.workspaceId })
        assertEquals("b", plan.first().macId)
        assertEquals(listOf("Claude", "Claude"), plan.map { it.agentName })
    }

    @Test
    fun capsAtTheLimit() {
        val plan = ShortcutPlan.plan(
            listOf(snapshot("a", workspace("w1", 1.0, claude("c1")), workspace("w2", 2.0, claude("c2")), workspace("w3", 3.0, claude("c3")))),
            limit = 2,
        )
        assertEquals(listOf("w3", "w2"), plan.map { it.workspaceId })
        assertEquals(emptyList<Any>(), ShortcutPlan.plan(listOf(snapshot("a", workspace("w1", 1.0, claude("c1")))), limit = 0))
    }

    @Test
    fun opensTheAgentTerminalElseTheFirstOne() {
        val withAgent = ShortcutPlan.shortcut("a", "Mac", workspace("w", null, shell("s1"), claude("c1")))
        val withoutAgent = ShortcutPlan.shortcut("a", "Mac", workspace("w", null, shell("s1"), shell("s2")))
        assertEquals("c1", withAgent?.surfaceId)
        assertEquals("s1", withoutAgent?.surfaceId)
        assertEquals("c2", ShortcutPlan.shortcut("a", "Mac", workspace("w", null, claude("c1"), claude("c2").lastUsed()))?.surfaceId)
        assertEquals("s2", ShortcutPlan.shortcut("a", "Mac", workspace("w", null, shell("s1"), shell("s2").lastUsed()))?.surfaceId)
        assertNull(ShortcutPlan.shortcut("a", "Mac", workspace("empty", null)))
    }

    @Test
    fun fallsBackToTerminalTitleAndListingColor() {
        val shortcut = ShortcutPlan.shortcut("a", "Mac", workspace("w", null, claude("c1"), title = " ", color = null))
        assertEquals("claude", shortcut?.title)
        assertEquals(true, shortcut?.color?.startsWith("#"))
    }

    @Test
    fun findsShortcutsForClosedWorkspacesAndRemovedComputers() {
        val existing = listOf("ws:a:kept", "ws:a:closed", "ws:gone:w", "ws:b:w", "pair")
        val snapshots = listOf(
            snapshot("a", workspace("kept", null, shell("s"))),
            snapshot("b"),
        )
        assertEquals(listOf("ws:a:closed", "ws:gone:w"), ShortcutPlan.gone(existing, snapshots))
    }

    @Test
    fun namesAgentsFromTheirSource() {
        assertEquals("Claude", AgentNames.display("claude"))
        assertEquals("Claude", AgentNames.display("claude-code"))
        assertEquals("Codex", AgentNames.display("codex"))
        assertEquals("Claude", AgentNames.display("claude_code"))
        assertEquals("Gemini", AgentNames.display("gemini-cli"))
        assertEquals("OpenCode", AgentNames.display("opencode"))
        assertNull(AgentNames.display(null))
        assertNull(AgentNames.display(" "))
    }
}
