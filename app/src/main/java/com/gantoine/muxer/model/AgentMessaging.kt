package com.gantoine.muxer.model

import com.gantoine.muxer.protocol.AgentMessage
import com.gantoine.muxer.protocol.Retry
import java.time.Instant

object AgentMessaging {
    fun shouldMessage(status: AgentStatus?, approvalsPending: Boolean, promptOpen: Boolean = false): Boolean =
        approvalsPending || promptOpen || WorkspaceListing.needsTerminalAnswer(status)

    fun unread(messages: List<AgentMessage>): List<AgentMessage> =
        messages.filter { it.state != AgentMessage.READ }.sortedBy { it.createdAt }

    fun statusLine(message: AgentMessage, waitingOnYou: Boolean): String = when (message.state) {
        AgentMessage.QUEUED -> if (waitingOnYou) "The agent gets this after you answer" else "Waiting for the agent to pick it up"
        AgentMessage.DELIVERED -> "The agent has it"
        AgentMessage.FAILED -> "Not delivered: ${failureText(message.failureReason)}"
        else -> "Read"
    }

    private fun failureText(reason: String?): String = when (reason) {
        "messages_disabled" -> "agent messages are turned off in cmux"
        "recipient_disabled" -> "messages are turned off for this agent"
        "workspace_disabled" -> "messages are turned off for this workspace"
        null, "" -> "cmux gave no reason"
        else -> reason.replace('_', ' ')
    }

    fun secondsUntil(retry: Retry, now: Instant): Long? = parseInstant(retry.dueAt)?.let { secondsBetween(now, it) }
}
