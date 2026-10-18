package com.gantoine.muxer.ui

import androidx.annotation.DrawableRes
import com.gantoine.muxer.R
import com.gantoine.muxer.model.AgentNames

object AgentGlyphs {
    @DrawableRes
    fun agent(source: String?): Int = when (AgentNames.display(source)) {
        "Claude" -> R.drawable.ic_agent_claude
        "Codex" -> R.drawable.ic_agent_codex
        else -> R.drawable.ic_workspace_agent
    }

    @DrawableRes
    fun pane(isAgent: Boolean, agentSource: String?): Int =
        if (isAgent) agent(agentSource) else R.drawable.ic_workspace_terminal
}
