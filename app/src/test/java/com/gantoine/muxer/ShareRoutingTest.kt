package com.gantoine.muxer

import com.gantoine.muxer.notifications.ShortcutPlan
import com.gantoine.muxer.share.ShareRoute
import com.gantoine.muxer.share.ShareRouting
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ShareRoutingTest {
    @Test
    fun readsTrimmedTextFromTextShares() {
        assertEquals("https://example.com", ShareRouting.sharedText("android.intent.action.SEND", "text/plain", "  https://example.com\n"))
        assertEquals("<b>hi</b>", ShareRouting.sharedText("android.intent.action.SEND", "text/html", "<b>hi</b>"))
    }

    @Test
    fun ignoresSharesWithoutText() {
        assertNull(ShareRouting.sharedText("android.intent.action.SEND", "text/plain", "   "))
        assertNull(ShareRouting.sharedText("android.intent.action.SEND", "text/plain", null))
        assertNull(ShareRouting.sharedText("android.intent.action.SEND", "image/png", "caption"))
        assertNull(ShareRouting.sharedText("android.intent.action.SEND", null, "caption"))
        assertNull(ShareRouting.sharedText("android.intent.action.VIEW", "text/plain", "hello"))
    }

    @Test
    fun recognisesImageShares() {
        assertTrue(ShareRouting.isImageShare("android.intent.action.SEND", "image/png"))
        assertTrue(ShareRouting.isImageShare("android.intent.action.SEND_MULTIPLE", "image/*"))
        assertFalse(ShareRouting.isImageShare("android.intent.action.SEND", "text/plain"))
        assertFalse(ShareRouting.isImageShare("android.intent.action.SEND", null))
        assertFalse(ShareRouting.isImageShare("android.intent.action.VIEW", "image/jpeg"))
        assertEquals("look at this", ShareRouting.caption("  look at this \n"))
        assertEquals("", ShareRouting.caption(null))
    }

    @Test
    fun onlyAgentWorkspacesOnPairedComputersAreTargets() {
        val computers = ShareRouting.computers(
            listOf(
                snapshot("a", workspace("shell", 99.0, shell("s1")), workspace("agent", 1.0, shell("s2"), claude("c2"))),
                snapshot("gone", workspace("other-agent", 5.0, claude("c3"))),
                snapshot("quiet", workspace("plain", 5.0, shell("s4"))),
            ),
            pairedMacIds = listOf("a", "quiet"),
        )
        assertEquals(listOf("a"), computers.map { it.macId })
        val target = computers.single().targets.single()
        assertEquals("agent", target.workspace.id)
        assertEquals("c2", target.surfaceId)
        assertEquals("Claude", target.agentName)
    }

    @Test
    fun groupsByComputerWithConnectedOnesFirstAndRecentWorkspacesFirst() {
        val computers = ShareRouting.computers(
            listOf(
                snapshot("z", workspace("z1", 1.0, claude("c1")), name = "Zed"),
                snapshot("off", workspace("o1", 1.0, claude("c2")), connected = false, name = "Attic"),
                snapshot("b", workspace("old", 1.0, claude("c3")), workspace("new", 9.0, claude("c4")), workspace("never", null, claude("c5")), name = "Book"),
            ),
            pairedMacIds = listOf("z", "off", "b"),
        )
        assertEquals(listOf("Book", "Zed", "Attic"), computers.map { it.macName })
        assertEquals(listOf("new", "old", "never"), computers.first().targets.map { it.workspace.id })
    }

    @Test
    fun shortcutSharesOpenThatWorkspace() {
        val snapshots = listOf(
            snapshot("a", workspace("w1", 1.0, claude("c1")), workspace("w2", 2.0, shell("s2"), claude("c2"))),
        )
        val route = ShareRouting.route(ShortcutPlan.id("a", "w2"), snapshots, listOf("a"))
        assertEquals("w2", (route as ShareRoute.Open).target.workspace.id)
        assertEquals("c2", route.target.surfaceId)
    }

    @Test
    fun shortcutSharesStillOpenWorkspacesWhoseAgentExited() {
        val route = ShareRouting.route(ShortcutPlan.id("a", "w1"), listOf(snapshot("a", workspace("w1", 1.0, shell("s1")))), listOf("a"))
        assertEquals("s1", (route as ShareRoute.Open).target.surfaceId)
    }

    @Test
    fun skipsThePickerWhenThereIsExactlyOneAgent() {
        val snapshots = listOf(
            snapshot("a", workspace("shell", 1.0, shell("s1")), workspace("agent", 1.0, claude("c1"))),
            snapshot("b", workspace("plain", 1.0, shell("s2"))),
        )
        val route = ShareRouting.route(null, snapshots, listOf("a", "b"))
        assertEquals("agent", (route as ShareRoute.Open).target.workspace.id)
    }

    @Test
    fun picksWhenThereAreSeveralAgentsOrNone() {
        val several = listOf(snapshot("a", workspace("w1", 1.0, claude("c1"))), snapshot("b", workspace("w2", 1.0, claude("c2"))))
        assertEquals(ShareRoute.Pick, ShareRouting.route(null, several, listOf("a", "b")))
        assertEquals(ShareRoute.Pick, ShareRouting.route(null, listOf(snapshot("a", workspace("w1", 1.0, shell("s1")))), listOf("a")))
    }

    @Test
    fun unknownShortcutsFallBackToThePicker() {
        val several = listOf(snapshot("a", workspace("w1", 1.0, claude("c1")), workspace("w2", 1.0, claude("c2"))))
        assertEquals(ShareRoute.Pick, ShareRouting.route(ShortcutPlan.id("a", "closed"), several, listOf("a")))
        assertEquals(ShareRoute.Pick, ShareRouting.route(ShortcutPlan.id("b", "w1"), several + snapshot("b", workspace("w1", 1.0, claude("c3"))), listOf("a")))
    }

    @Test
    fun onlyShortcutsWhoseLastUsedPaneIsAnAgentAreShareTargets() {
        val agent = ShortcutPlan.shortcut("a", "Mac", workspace("w1", 1.0, shell("s1"), claude("c1").lastUsed()))!!
        val shellLastUsed = ShortcutPlan.shortcut("a", "Mac", workspace("w2", 1.0, shell("s2").lastUsed(), claude("c2")))!!
        val nothingLastUsed = ShortcutPlan.shortcut("a", "Mac", workspace("w3", 1.0, claude("c3")))!!
        val plain = ShortcutPlan.shortcut("a", "Mac", workspace("w4", 1.0, shell("s4")))!!
        assertEquals(setOf(ShortcutPlan.CONVERSATION_CATEGORY, ShortcutPlan.SHARE_CATEGORY), ShortcutPlan.categories(agent))
        assertEquals(setOf(ShortcutPlan.CONVERSATION_CATEGORY), ShortcutPlan.categories(shellLastUsed))
        assertEquals(setOf(ShortcutPlan.CONVERSATION_CATEGORY), ShortcutPlan.categories(nothingLastUsed))
        assertEquals(setOf(ShortcutPlan.CONVERSATION_CATEGORY), ShortcutPlan.categories(plain))
    }

    @Test
    fun shortcutSharesGoToTheLastUsedAgentPane() {
        val snapshots = listOf(snapshot("a", workspace("w", 1.0, claude("c1"), claude("c2").lastUsed())))
        val route = ShareRouting.route(ShortcutPlan.id("a", "w"), snapshots, listOf("a"))
        assertEquals("c2", (route as ShareRoute.Open).target.surfaceId)
    }

    @Test
    fun shortcutSharesAskWhichAgentWhenNoneWasLastUsed() {
        val snapshots = listOf(snapshot("a", workspace("w", 1.0, shell("s1").lastUsed(), claude("c1"), claude("c2"))))
        assertEquals(ShareRoute.Pick, ShareRouting.route(ShortcutPlan.id("a", "w"), snapshots, listOf("a")))
    }

    @Test
    fun workspacesWithSeveralAgentsOfferATerminalChoice() {
        val snapshots = listOf(snapshot("a", workspace("w", 1.0, shell("s1"), claude("c1"), claude("c2"))))
        val target = ShareRouting.computers(snapshots, listOf("a")).single().targets.single()
        assertEquals(listOf("c1", "c2"), target.agentTerminals.map { it.id })
        assertEquals(true, target.needsTerminalChoice)
        assertEquals(ShareRoute.Pick, ShareRouting.route(null, snapshots, listOf("a")))
        val single = ShareRouting.computers(listOf(snapshot("a", workspace("w", 1.0, claude("c1")))), listOf("a")).single().targets.single()
        assertEquals(false, single.needsTerminalChoice)
    }
}
