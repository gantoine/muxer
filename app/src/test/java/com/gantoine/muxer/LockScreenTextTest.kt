package com.gantoine.muxer

import com.gantoine.muxer.notifications.ActivitySummary
import com.gantoine.muxer.notifications.LockScreenText
import com.gantoine.muxer.notifications.WaitingAgent
import com.gantoine.muxer.notifications.WorkingAgent
import com.gantoine.muxer.widget.WidgetData
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class LockScreenTextTest {
    private val waiting = WaitingAgent("mac", "ws", "s1", "Claude", "Payments API", "wants to use Bash")
    private val working = WorkingAgent("mac", "ws", "s2", "Codex", "Docs site")

    @Test
    fun saysAnAgentNeedsYouWhenOneIsWaiting() {
        assertEquals(LockScreenText.NEEDS_YOU, LockScreenText.activity(ActivitySummary(waiting = listOf(waiting), working = listOf(working))))
    }

    @Test
    fun saysAgentsAreWorkingWithoutNamingThem() {
        val text = LockScreenText.activity(ActivitySummary(working = listOf(working), macNames = mapOf("mac" to "Laptop")))
        assertEquals(LockScreenText.WORKING, text)
        listOf("Codex", "Docs site", "Laptop").forEach { assertFalse(text.contains(it)) }
    }

    @Test
    fun saysItIsRunningWhenNothingHappens() {
        assertEquals(LockScreenText.RUNNING, LockScreenText.activity(ActivitySummary(connected = 1, total = 1, macNames = mapOf("mac" to "Laptop"))))
    }

    @Test
    fun lockedApprovalsWidgetOnlyCounts() {
        assertEquals("No agent is waiting for you", WidgetData.lockedApprovalsLabel(0))
        assertEquals("1 agent is waiting. Open Muxer to answer.", WidgetData.lockedApprovalsLabel(1))
        assertEquals("3 agents are waiting. Open Muxer to answer.", WidgetData.lockedApprovalsLabel(3))
    }
}
