package com.gantoine.muxer

import com.gantoine.muxer.net.RelayException
import com.gantoine.muxer.protocol.PostHogCloudRun
import com.gantoine.muxer.protocol.PostHogCloudTask
import com.gantoine.muxer.protocol.PostHogCloudTasksResponse
import com.gantoine.muxer.ui.CloudRunPhase
import com.gantoine.muxer.ui.PostHogCloud
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PostHogCloudTest {
    private val running = PostHogCloudTask(id = "t1", title = "Fix login", repository = "acme/app", runId = "r1", status = "in_progress")
    private val failed = PostHogCloudTask(id = "t2", title = "", number = 12, runId = "r2", status = "failed", error = "Sandbox ran out of memory")

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
    fun rowsShowTheErrorOfAFailedRunAndFallBackToTheTaskNumber() {
        assertEquals("Running · acme/app", PostHogCloud.summary(running))
        assertEquals("Sandbox ran out of memory", PostHogCloud.summary(failed))
        assertEquals("Task #12", PostHogCloud.title(failed))
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

    @Test
    fun theFirstConnectedComputerHostsTheSection() {
        var answers = PostHogCloud.answers(emptyMap(), "mac_a", Result.success(PostHogCloudTasksResponse(connected = false)))
        answers = PostHogCloud.answers(answers, "mac_b", Result.success(PostHogCloudTasksResponse(connected = true, tasks = listOf(running))))
        val picked = PostHogCloud.pick(answers, listOf("mac_a", "mac_b"))!!
        assertEquals(listOf("t1" to "mac_b"), picked.map { it.task.id to it.macId })
        assertNull(PostHogCloud.pick(PostHogCloud.answers(emptyMap(), "mac_a", Result.success(PostHogCloudTasksResponse())), listOf("mac_a")))
    }

    @Test
    fun aTimeoutKeepsTheListButARefusedKeyDropsIt() {
        val answers = PostHogCloud.answers(emptyMap(), "mac_a", Result.success(PostHogCloudTasksResponse(connected = true, tasks = listOf(running))))
        assertEquals(answers, PostHogCloud.answers(answers, "mac_a", Result.failure(RelayException("timeout", "slow"))))
        val refused = PostHogCloud.answers(answers, "mac_a", Result.failure(RelayException("posthog_cloud_unauthorized", "bad key")))
        assertNull(PostHogCloud.pick(refused, listOf("mac_a")))
    }
}
