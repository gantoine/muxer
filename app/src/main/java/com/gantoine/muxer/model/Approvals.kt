package com.gantoine.muxer.model

import com.gantoine.muxer.protocol.Approval
import com.gantoine.muxer.protocol.ApprovalQuestion
import java.time.Instant

sealed interface ApprovalReply {
    data class Decision(val mode: String, val feedback: String? = null) : ApprovalReply
    data class Answers(val answers: List<String>) : ApprovalReply
}

data class ApprovalDecision(val label: String, val shortLabel: String, val mode: String)

object Approvals {
    const val MODE_ONCE = "once"
    const val MODE_ALWAYS = "always"
    const val MODE_DENY = "deny"
    const val PLAN_MANUAL = "manual"
    const val PLAN_AUTO_ACCEPT = "autoAccept"
    private const val ANSWER_WINDOW_SECONDS = 120L

    private val permissionDecisions = listOf(
        ApprovalDecision("Allow", "Allow", MODE_ONCE),
        ApprovalDecision("Always allow", "Always", MODE_ALWAYS),
        ApprovalDecision("Deny", "Deny", MODE_DENY),
    )

    private val planDecisions = listOf(
        ApprovalDecision("Approve", "Approve", PLAN_MANUAL),
        ApprovalDecision("Approve, auto-edit", "Auto-edit", PLAN_AUTO_ACCEPT),
        ApprovalDecision("Reject", "Reject", MODE_DENY),
    )

    fun decisions(approval: Approval): List<ApprovalDecision> = when {
        approval.isPermission -> permissionDecisions
        approval.isPlan -> planDecisions
        else -> emptyList()
    }

    fun title(approval: Approval): String {
        val agent = AgentNames.displayOrDefault(approval.agent)
        return when {
            approval.isPermission -> "$agent wants to use ${approval.tool ?: "a tool"}"
            approval.isPlan -> "$agent has a plan ready"
            approval.questions.size == 1 -> "$agent is asking"
            else -> "$agent has ${approval.questions.size} questions"
        }
    }

    fun summary(approval: Approval): String = when {
        approval.isPermission -> approval.detail ?: approval.description.orEmpty()
        approval.isPlan -> approval.plan.orEmpty().lineSequence().map { it.trim().trimStart('#').trim() }.firstOrNull { it.isNotEmpty() }.orEmpty()
        else -> approval.questions.firstOrNull()?.prompt.orEmpty()
    }

    fun secondsLeft(approval: Approval, now: Instant): Long? {
        if (approval.posthogRunId != null) return null
        val created = parseInstant(approval.createdAt) ?: return null
        return (ANSWER_WINDOW_SECONDS - (now.epochSecond - created.epochSecond)).coerceAtLeast(0)
    }

    fun otherIndex(question: ApprovalQuestion): Int = question.options.size

    fun answer(question: ApprovalQuestion, chosen: Set<Int>, typed: String = ""): String {
        val labels = question.options.filterIndexed { index, _ -> index in chosen }.map { it.label }
        val other = typed.trim().takeIf { otherIndex(question) in chosen && it.isNotEmpty() }
        return (labels + listOfNotNull(other)).joinToString(", ")
    }

    fun isAnswered(question: ApprovalQuestion, chosen: Set<Int>, typed: String): Boolean =
        chosen.isNotEmpty() && (otherIndex(question) !in chosen || typed.isNotBlank())
}
