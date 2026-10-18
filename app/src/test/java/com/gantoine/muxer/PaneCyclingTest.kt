package com.gantoine.muxer

import com.gantoine.muxer.ui.PaneCycling
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class PaneCyclingTest {
    private val panes = listOf("a", "b", "c")

    @Test
    fun swipingMovesToTheNeighborAndWrapsAround() {
        assertEquals("b", PaneCycling.neighbor(panes, "a", 1))
        assertEquals("a", PaneCycling.neighbor(panes, "c", 1))
        assertEquals("c", PaneCycling.neighbor(panes, "a", -1))
    }

    @Test
    fun aSinglePaneOrAMissingPaneHasNoNeighbor() {
        assertNull(PaneCycling.neighbor(listOf("a"), "a", 1))
        assertNull(PaneCycling.neighbor(panes, "gone", 1))
    }
}
