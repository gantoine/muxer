package com.gantoine.muxer

import com.gantoine.muxer.net.RelayException
import com.gantoine.muxer.net.Tailnet
import com.gantoine.muxer.protocol.ExposedPortsResponse
import com.gantoine.muxer.protocol.RelayJson
import com.gantoine.muxer.protocol.RequestError
import com.gantoine.muxer.protocol.TerminalPortLink
import com.gantoine.muxer.protocol.TerminalPortLinksResponse
import com.gantoine.muxer.ui.OpenPorts
import com.gantoine.muxer.ui.PortRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class OpenPortsTest {
    @Test
    fun recognisesComputersReachedThroughTailscale() {
        assertTrue(Tailnet.reaches("https://my-mac.tailnet.ts.net:8765"))
        assertTrue(Tailnet.reaches("https://MY-MAC.TAILNET.TS.NET"))
        assertTrue(Tailnet.reaches("https://100.64.0.7:8765"))
        assertTrue(Tailnet.reaches("https://100.127.255.254"))
        assertTrue(Tailnet.reaches("https://[fd7a:115c:a1e0::1]:8765"))
    }

    @Test
    fun otherAddressesAreNotTailscale() {
        assertFalse(Tailnet.reaches("https://my-mac.local:8765"))
        assertFalse(Tailnet.reaches("https://ts.net.example.com"))
        assertFalse(Tailnet.reaches("https://100.63.255.255"))
        assertFalse(Tailnet.reaches("https://100.128.0.1"))
        assertFalse(Tailnet.reaches("https://192.168.1.20:8765"))
        assertFalse(Tailnet.reaches("http://10.0.2.2:18765"))
        assertFalse(Tailnet.reaches("https://[fd00::1]"))
        assertFalse(Tailnet.reaches("not a url"))
    }

    @Test
    fun onlyOffersPortsWhenTheRelayCanServeThem() {
        val url = "https://my-mac.tailnet.ts.net:8765"
        assertTrue(OpenPorts.available(url) { true })
        assertFalse(OpenPorts.available(url) { it != OpenPorts.REQUEST })
        assertFalse(OpenPorts.available("https://my-mac.local") { true })
    }

    @Test
    fun describesEachPort() {
        val direct = TerminalPortLink(port = 3000, url = "http://mac.tailnet.ts.net:3000")
        assertEquals(PortRow.Reachable(3000, direct.url, served = false), OpenPorts.row(direct))
        val served = TerminalPortLink(port = 8080, url = "https://mac.tailnet.ts.net:8080")
        assertEquals(PortRow.Reachable(8080, served.url, served = true), OpenPorts.row(served))
        val loopback = TerminalPortLink(port = 5173, url = "https://mac.tailnet.ts.net:5173", needsExpose = true)
        assertEquals(PortRow.Exposable(5173, loopback.url, exposed = false), OpenPorts.row(loopback))
        val exposed = loopback.copy(needsExpose = false, exposed = true)
        assertEquals(PortRow.Exposable(5173, loopback.url, exposed = true), OpenPorts.row(exposed))
    }

    @Test
    fun portsThatCantBeOpenedSayWhy() {
        val conflict = TerminalPortLink(port = 2222, error = RequestError("serve_conflict", "Tailscale already serves something else on port 2222."))
        assertEquals(PortRow.Unavailable(2222, "Tailscale already serves something else on port 2222."), OpenPorts.row(conflict))
        assertTrue(OpenPorts.row(TerminalPortLink(port = 22, url = "intent://evil")) is PortRow.Unavailable)
    }

    @Test
    fun explainsWhereExposedPortsGo() {
        val links = listOf(TerminalPortLink(port = 22), TerminalPortLink(port = 5173, url = "https://mac.tailnet.ts.net:5173"))
        assertEquals("mac.tailnet.ts.net", OpenPorts.host(links))
        assertNull(OpenPorts.host(emptyList()))
        val explanation = OpenPorts.explanation("mac.tailnet.ts.net")
        assertTrue(explanation.contains("https://mac.tailnet.ts.net:<port>"))
        assertTrue(explanation.contains("Every device on your tailnet"))
    }

    @Test
    fun failuresExplainWhatToDo() {
        assertEquals(
            "Opening ports needs Tailscale on this computer. Install it and sign in, then try again.",
            OpenPorts.failureMessage(RelayException("tailscale_missing", "raw")),
        )
        assertEquals("Tailscale isn't running or signed in on this computer.", OpenPorts.failureMessage(RelayException("tailscale_offline", "raw")))
        assertEquals("That port is the relay itself.", OpenPorts.failureMessage(RelayException("port_not_allowed", "That port is the relay itself.")))
    }

    @Test
    fun decodesTheRelaysAnswers() {
        val links = RelayJson.decodeFromString(
            TerminalPortLinksResponse.serializer(),
            """{"ports":[{"port":5173,"url":"https://m.ts.net:5173","exposed":false,"needs_expose":true},""" +
                """{"port":4000,"url":"","exposed":false,"needs_expose":false,"error":{"code":"not_listening","message":"gone"}}]}""",
        )
        assertTrue(links.ports[0].needsExpose)
        assertEquals("not_listening", links.ports[1].error?.code)
        val exposed = RelayJson.decodeFromString(ExposedPortsResponse.serializer(), """{"ports":[{"port":5173,"url":"https://m.ts.net:5173"}]}""")
        assertEquals(listOf(5173), exposed.ports.map { it.port })
    }
}
