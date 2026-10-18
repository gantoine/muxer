package com.gantoine.muxer

import com.gantoine.muxer.model.AgentMessaging
import com.gantoine.muxer.model.AgentStatus
import com.gantoine.muxer.protocol.AgentMessage
import com.gantoine.muxer.protocol.AgentMessagesMessage
import com.gantoine.muxer.protocol.RelayJson
import com.gantoine.muxer.protocol.Retry
import java.time.Instant
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class AgentMessagingTest {
    private fun message(id: String, state: String, createdAt: Double, reason: String? = null) =
        AgentMessage(id = id, body = "use staging", state = state, createdAt = createdAt, failureReason = reason)

    @Test
    fun messagesTheAgentOnlyWhileAPromptIsOpen() {
        assertTrue(AgentMessaging.shouldMessage(AgentStatus.Idle("Idle"), approvalsPending = true))
        assertTrue(AgentMessaging.shouldMessage(AgentStatus.NeedsYou("Needs permission"), approvalsPending = false))
        assertFalse(AgentMessaging.shouldMessage(AgentStatus.Working("Running"), approvalsPending = false))
        assertFalse(AgentMessaging.shouldMessage(AgentStatus.NeedsYou("Needs input"), approvalsPending = false))
        assertFalse(AgentMessaging.shouldMessage(AgentStatus.Idle("Idle"), approvalsPending = false))
        assertFalse(AgentMessaging.shouldMessage(null, approvalsPending = false))
        assertTrue(AgentMessaging.shouldMessage(AgentStatus.Working("Running"), approvalsPending = false, promptOpen = true))
    }

    @Test
    fun receiptsHideReadMessagesAndRunOldestFirst() {
        val unread = AgentMessaging.unread(
            listOf(message("c", AgentMessage.QUEUED, 3.0), message("b", AgentMessage.READ, 2.0), message("a", AgentMessage.DELIVERED, 1.0)),
        )
        assertEquals(listOf("a", "c"), unread.map { it.id })
    }

    @Test
    fun explainsEachDeliveryState() {
        assertEquals("The agent gets this after you answer", AgentMessaging.statusLine(message("a", AgentMessage.QUEUED, 1.0), waitingOnYou = true))
        assertEquals("Waiting for the agent to pick it up", AgentMessaging.statusLine(message("a", AgentMessage.QUEUED, 1.0), waitingOnYou = false))
        assertEquals(
            "Not delivered: messages are turned off for this agent",
            AgentMessaging.statusLine(message("a", AgentMessage.FAILED, 1.0, "recipient_disabled"), waitingOnYou = false),
        )
    }

    @Test
    fun countsDownToTheRetry() {
        val retry = Retry(surfaceId = "s1", attempt = 2, dueAt = "2026-10-15T12:00:45Z")
        assertEquals(45L, AgentMessaging.secondsUntil(retry, Instant.parse("2026-10-15T12:00:00Z")))
        assertEquals(0L, AgentMessaging.secondsUntil(retry, Instant.parse("2026-10-15T12:01:00Z")))
        assertNull(AgentMessaging.secondsUntil(retry.copy(dueAt = "soon"), Instant.EPOCH))
    }

    @Test
    fun decodesTheRelayMessageList() {
        val decoded = RelayJson.decodeFromString(
            AgentMessagesMessage.serializer(),
            """{"type":"agent_messages","surface_id":"s1","messages":[{"id":"m1","sender_name":"Muxer on Pixel","body":"hi","state":"failed","created_at":1760000000.5,"delivered_at":null,"failure_reason":"messages_disabled"}]}""",
        )
        assertEquals("s1", decoded.surfaceId)
        assertEquals("messages_disabled", decoded.messages.single().failureReason)
    }
}
