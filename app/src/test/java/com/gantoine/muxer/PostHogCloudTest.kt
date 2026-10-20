package com.gantoine.muxer

import com.gantoine.muxer.protocol.PostHogCloudRun
import com.gantoine.muxer.ui.CloudRunPhase
import com.gantoine.muxer.ui.PostHogCloud
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PostHogCloudTest {
    @Test
    fun runStatusesGroupIntoPhases() {
        assertEquals(CloudRunPhase.Running, PostHogCloud.phase("in_progress", "working"))
        assertEquals(CloudRunPhase.Waiting, PostHogCloud.phase("in_progress", "waiting"))
        assertEquals(CloudRunPhase.Queued, PostHogCloud.phase("not_started"))
        assertEquals(CloudRunPhase.Done, PostHogCloud.phase("completed"))
        assertEquals(CloudRunPhase.Failed, PostHogCloud.phase("failed"))
        assertEquals(CloudRunPhase.Stopped, PostHogCloud.phase("cancelled"))
    }

    @Test
    fun theHeaderNamesTheTurnAgentAndBranch() {
        val run = PostHogCloudRun(taskId = "t1", runId = "r1", status = "in_progress", state = "idle", agent = "claude", branch = "fix-login")
        assertEquals("Waiting for your reply · Claude · fix-login", PostHogCloud.runSubtitle(run))
        assertFalse(PostHogCloud.midTurn(run))
        assertTrue(PostHogCloud.midTurn(run.copy(state = "working")))
        assertTrue(PostHogCloud.midTurn(run.copy(status = "queued", state = null)))
        assertFalse(PostHogCloud.midTurn(run.copy(status = "completed", state = null)))
    }
}
