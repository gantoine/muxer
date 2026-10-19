package com.gantoine.muxer

import com.gantoine.muxer.protocol.Automation
import com.gantoine.muxer.protocol.AutomationAction
import com.gantoine.muxer.protocol.AutomationLog
import com.gantoine.muxer.protocol.AutomationRateLimit
import com.gantoine.muxer.protocol.AutomationsResponse
import com.gantoine.muxer.protocol.RelayJson
import com.gantoine.muxer.ui.AutomationListing
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class AutomationListingTest {
    private val resume = Automation(
        id = "phone-auto-resume",
        trigger = "agent.auto_resume.sent",
        actions = listOf(AutomationAction("notify", "Notify “Agent resumed”"), AutomationAction("rpc", "Call workspace.reorder")),
        actionCount = 2,
    )

    @Test
    fun summaryNamesTheTriggerAndTheFirstAction() {
        assertEquals("When agent.auto_resume.sent", AutomationListing.trigger(resume))
        assertEquals("Notify “Agent resumed” and 1 more action", AutomationListing.actions(resume))
        assertEquals("When any event", AutomationListing.trigger(Automation(id = "bare", actionCount = 3)))
        assertEquals("3 actions", AutomationListing.actions(Automation(id = "bare", actionCount = 3)))
    }

    @Test
    fun lastRunSaysWhenNothingRan() {
        assertEquals("No run since cmux started", AutomationListing.lastRun(resume))
        val failed = resume.copy(lastRun = AutomationLog(occurredAt = "bad", status = "error"))
        assertEquals("Ran · Failed", AutomationListing.lastRun(failed))
        assertTrue(AutomationListing.failed(failed.lastRun!!))
    }

    @Test
    fun rateLimitsReadAsSentences() {
        assertEquals("At most once per second", AutomationListing.rateLimit(AutomationRateLimit()))
        assertEquals("At most twice every 30 seconds", AutomationListing.rateLimit(AutomationRateLimit(30.0, 2)))
        assertEquals("At most 5 times per minute", AutomationListing.rateLimit(AutomationRateLimit(60.0, 5)))
        assertEquals("At most once every 0.5 seconds", AutomationListing.rateLimit(AutomationRateLimit(0.5, 1)))
    }

    @Test
    fun configChangesAndSkipsHaveNames() {
        assertEquals("Turned off", AutomationListing.logTitle(AutomationLog(event = "config.disable")))
        assertEquals("Skipped by the rate limit", AutomationListing.status("skipped_rate_limit"))
        assertEquals("Something new", AutomationListing.status("something_new"))
    }

    @Test
    fun togglingChangesOnlyThatRule() {
        val response = AutomationsResponse(listOf(resume, resume.copy(id = "other")))
        val toggled = AutomationListing.withEnabled(response, "other", false)
        assertEquals(listOf(true, false), toggled.rules.map { it.enabled })
    }

    @Test
    fun relayAnswerDecodes() {
        val json = """
            {"rules":[{"id":"r1","enabled":false,"trigger":"any event in agent","category":"agent","conditions":["agent is \"claude\""],
            "actions":[{"type":"notify","summary":"Notify","fields":[{"name":"title","value":"Hi"}]}],"action_count":1,
            "rate_limit":{"interval_seconds":1,"maximum":1},
            "last_run":{"occurred_at":"2026-10-18T09:00:00Z","rule_id":"r1","event":"agent.needs_input","status":"completed","detail":"","chain":["r1"]}}],
            "config_error":"bad"}
        """.trimIndent()
        val response = RelayJson.decodeFromString(AutomationsResponse.serializer(), json)
        val rule = response.rules.single()
        assertEquals("bad", response.configError)
        assertEquals("title", rule.actions.single().fields.single().name)
        assertEquals(AutomationRateLimit(1.0, 1), rule.rateLimit)
        assertEquals("completed", rule.lastRun?.status)
    }

    @Test
    fun eventAndFieldNamesAreMarkedAsCode() {
        val trigger = "When agent.auto_resume.sent"
        assertEquals(listOf("agent.auto_resume.sent"), AutomationListing.codeRanges(trigger).map { trigger.substring(it) })
        val condition = "only if workspace.title starts with \"api-\""
        assertEquals(listOf("workspace.title"), AutomationListing.codeRanges(condition).map { condition.substring(it) })
        assertEquals(emptyList<IntRange>(), AutomationListing.codeRanges("When any event"))
    }
}
