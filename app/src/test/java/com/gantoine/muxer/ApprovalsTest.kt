package com.gantoine.muxer

import com.gantoine.muxer.model.ApprovalReply
import com.gantoine.muxer.model.Approvals
import com.gantoine.muxer.net.ConnectionState
import com.gantoine.muxer.protocol.Approval
import com.gantoine.muxer.protocol.ApprovalOption
import com.gantoine.muxer.protocol.ApprovalQuestion
import java.time.Instant
import java.util.concurrent.CopyOnWriteArrayList
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class ApprovalsTest {
    private val server = MockWebServer()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val answers = CopyOnWriteArrayList<Map<String, Any>>()

    @Before
    fun setUp() = server.start()

    @After
    fun tearDown() {
        scope.cancel()
        server.close()
    }

    private fun fakeRelay() = object : WebSocketListener() {
        override fun onMessage(webSocket: WebSocket, text: String) {
            val message = Json.parseToJsonElement(text).jsonObject
            val id = message["id"]?.jsonPrimitive?.content
            when (message["type"]?.jsonPrimitive?.content) {
                "hello" -> {
                    webSocket.send(FakeRelay.welcome(id))
                    webSocket.send(
                        """{"type":"approvals","approvals":[
                            {"id":"r1","kind":"permission","agent":"claude","workspace_id":"w1","surface_id":"t1","tool":"Bash","detail":"npm test"},
                            {"id":"q1","kind":"question","workspace_id":"w1","surface_id":"t2","questions":[{"prompt":"Which?","multi_select":true,"options":[{"label":"A"},{"label":"B"}]}]},
                            {"id":"p1","kind":"plan","workspace_id":"w1","surface_id":"t1","plan":"# Ship it\n1. Test"}
                        ]}""",
                    )
                }
                "answer_approval" -> {
                    answers += buildMap {
                        put("approval_id", message["approval_id"]!!.jsonPrimitive.content)
                        message["mode"]?.let { put("mode", it.jsonPrimitive.content) }
                        message["answers"]?.let { put("answers", it.jsonArray.map { answer -> answer.jsonPrimitive.content }) }
                        message["feedback"]?.let { put("feedback", it.jsonPrimitive.content) }
                    }
                    webSocket.send("""{"type":"result","id":"$id","ok":true}""")
                }
            }
        }

        override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
            webSocket.close(code, null)
        }
    }

    @Test
    fun receivesPendingApprovalsAndAnswersThem() = runBlocking {
        server.enqueue(MockResponse.Builder().webSocketUpgrade(fakeRelay()).build())
        val connection = FakeRelay.connection(server, scope)
        connection.start()
        withTimeout(5_000) { connection.state.first { it is ConnectionState.Connected } }

        val pending = withTimeout(5_000) { connection.approvals.first { !it.isNullOrEmpty() } }
        assertEquals(listOf("r1", "q1", "p1"), pending!!.map { it.id })
        assertTrue(pending[2].isPlan)
        assertTrue(pending[0].isPermission)
        assertEquals("npm test", pending[0].detail)
        assertTrue(pending[1].questions.single().multiSelect)

        assertTrue(connection.reply("r1", ApprovalReply.Decision(Approvals.MODE_ONCE)).isSuccess)
        assertTrue(connection.reply("q1", ApprovalReply.Answers(listOf("A, B"))).isSuccess)
        assertTrue(connection.reply("p1", ApprovalReply.Decision(Approvals.PLAN_MANUAL, "Split step 1")).isSuccess)
        assertEquals(
            listOf(
                mapOf("approval_id" to "r1", "mode" to "once"),
                mapOf("approval_id" to "q1", "answers" to listOf("A, B")),
                mapOf("approval_id" to "p1", "mode" to "manual", "feedback" to "Split step 1"),
            ),
            answers.toList(),
        )
        assertTrue("answered requests leave the list right away", connection.approvals.value!!.isEmpty())

        connection.stop()
        assertNull(connection.approvals.value)
    }

    @Test
    fun describesRequestsTheWayTheCardAndNotificationShowThem() {
        val permission = Approval(id = "r1", kind = "permission", agent = "claude", workspaceId = "w1", surfaceId = "t1", tool = "Bash", detail = "rm -rf build")
        assertEquals("Claude wants to use Bash", Approvals.title(permission))
        assertEquals("rm -rf build", Approvals.summary(permission))

        val question = ApprovalQuestion(prompt = "Which?", multiSelect = true, options = listOf(ApprovalOption("A"), ApprovalOption("B"), ApprovalOption("C")))
        val asking = Approval(id = "q1", kind = "question", workspaceId = "w1", surfaceId = "t1", questions = listOf(question))
        assertEquals("The agent is asking", Approvals.title(asking))
        assertEquals("A, C", Approvals.answer(question, setOf(0, 2)))
        assertEquals("A, use the staging db", Approvals.answer(question, setOf(0, 3), "  use the staging db "))
        assertEquals("Mine", Approvals.answer(question.copy(multiSelect = false), setOf(3), "Mine"))
        assertTrue(Approvals.isAnswered(question, setOf(1), ""))
        assertFalse(Approvals.isAnswered(question, setOf(3), "  "))
        assertFalse(Approvals.isAnswered(question, emptySet(), ""))

        val plan = Approval(id = "p1", kind = "plan", agent = "claude", workspaceId = "w1", surfaceId = "t1", plan = "\n## Add approvals\n1. Relay")
        assertEquals("Claude has a plan ready", Approvals.title(plan))
        assertEquals("Add approvals", Approvals.summary(plan))

        assertEquals(listOf("Allow", "Always allow", "Deny"), Approvals.decisions(permission).map { it.label })
        assertEquals(listOf("once", "always", "deny"), Approvals.decisions(permission).map { it.mode })
        assertEquals(listOf("Approve", "Approve, auto-edit", "Reject"), Approvals.decisions(plan).map { it.label })
        assertEquals(listOf("Approve", "Auto-edit", "Reject"), Approvals.decisions(plan).map { it.shortLabel })
        assertTrue(Approvals.decisions(asking).isEmpty())

        val created = permission.copy(createdAt = "2026-10-15T16:08:42Z")
        assertEquals(100L, Approvals.secondsLeft(created, Instant.parse("2026-10-15T16:09:02Z")))
        assertEquals(0L, Approvals.secondsLeft(created, Instant.parse("2026-10-15T16:20:00Z")))
        assertNull(Approvals.secondsLeft(created.copy(posthogTaskId = "t1", posthogRunId = "r1"), Instant.parse("2026-10-15T16:20:00Z")))
    }
}
