package com.gantoine.muxer

import com.gantoine.muxer.net.RelayException
import com.gantoine.muxer.protocol.ExposedPortsResponse
import com.gantoine.muxer.protocol.PortLink
import com.gantoine.muxer.protocol.RelayJson
import com.gantoine.muxer.ui.OpenPorts
import com.gantoine.muxer.ui.PortStep
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class OpenPortsTest {
    @Test
    fun reachablePortsOpenRightAway() {
        val direct = PortLink(port = 3000, url = "http://mac.tailnet.ts.net:3000")
        assertEquals(PortStep.Open(direct.url), OpenPorts.afterLink(Result.success(direct)))
        val exposed = PortLink(port = 5173, url = "https://mac.tailnet.ts.net:5173", exposed = true)
        assertEquals(PortStep.Open(exposed.url), OpenPorts.afterLink(Result.success(exposed)))
    }

    @Test
    fun loopbackPortsAskBeforeExposing() {
        val link = PortLink(port = 5173, url = "https://mac.tailnet.ts.net:5173", needsExpose = true)
        val step = OpenPorts.afterLink(Result.success(link))
        assertEquals(PortStep.Confirm(link), step)
        assertTrue(OpenPorts.exposeExplanation(link).contains("Every device on your tailnet"))
        assertEquals(PortStep.Open(link.url), OpenPorts.afterExpose(Result.success(link.copy(exposed = true))))
    }

    @Test
    fun onlyWebAddressesAreOpened() {
        val step = OpenPorts.afterLink(Result.success(PortLink(port = 22, url = "intent://evil")))
        assertTrue(step is PortStep.Failed)
    }

    @Test
    fun failuresExplainWhatToDo() {
        val missing = OpenPorts.afterLink(Result.failure(RelayException("tailscale_missing", "raw")))
        assertEquals(PortStep.Failed("Opening ports needs Tailscale on this computer. Install it and sign in, then try again."), missing)
        val oldRelay = OpenPorts.afterLink(Result.failure(RelayException("unknown_type", "raw")))
        assertEquals(PortStep.Failed("Update muxer on this computer to open ports from your phone."), oldRelay)
        val refused = OpenPorts.afterLink(Result.failure(RelayException("port_not_allowed", "That port is the relay itself.")))
        assertEquals(PortStep.Failed("That port is the relay itself."), refused)
    }

    @Test
    fun decodesTheRelaysAnswers() {
        val link = RelayJson.decodeFromString(PortLink.serializer(), """{"port":5173,"url":"https://m.ts.net:5173","exposed":false,"needs_expose":true}""")
        assertTrue(link.needsExpose)
        val exposed = RelayJson.decodeFromString(ExposedPortsResponse.serializer(), """{"ports":[{"port":5173,"url":"https://m.ts.net:5173"}]}""")
        assertEquals(listOf(5173), exposed.ports.map { it.port })
    }
}
