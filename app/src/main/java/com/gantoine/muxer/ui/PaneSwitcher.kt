package com.gantoine.muxer.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

object PaneCycling {
    fun neighbor(paneIds: List<String>, currentId: String, step: Int): String? {
        val index = paneIds.indexOf(currentId)
        if (index < 0 || paneIds.size < 2) return null
        return paneIds[Math.floorMod(index + step, paneIds.size)]
    }
}

@Composable
fun PaneSwitcherTitle(
    title: String,
    paneIds: List<String>,
    currentId: String,
    needsYou: Set<String>,
    onOpenPanes: () -> Unit,
    onSwitch: (String) -> Unit,
) {
    if (paneIds.size < 2) {
        Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis)
        return
    }
    val haptics = LocalHapticFeedback.current
    val threshold = with(LocalDensity.current) { 48.dp.toPx() }
    val panes by rememberUpdatedState(paneIds)
    val current by rememberUpdatedState(currentId)
    val switch by rememberUpdatedState(onSwitch)
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .widthIn(min = 160.dp)
            .clickable(onClickLabel = "Switch terminal", onClick = onOpenPanes)
            .pointerInput(Unit) {
                var dragged = 0f
                detectHorizontalDragGestures(
                    onDragStart = { dragged = 0f },
                    onDragEnd = {
                        val step = when {
                            dragged <= -threshold -> 1
                            dragged >= threshold -> -1
                            else -> 0
                        }
                        if (step != 0) PaneCycling.neighbor(panes, current, step)?.let {
                            haptics.performHapticFeedback(HapticFeedbackType.SegmentTick)
                            switch(it)
                        }
                    },
                    onHorizontalDrag = { change, amount ->
                        change.consume()
                        dragged += amount
                    },
                )
            }
            .padding(vertical = 12.dp),
    ) {
        Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Row(horizontalArrangement = Arrangement.spacedBy(5.dp), modifier = Modifier.align(Alignment.BottomCenter).offset(y = 9.dp)) {
            paneIds.forEach { id ->
                val color = when {
                    id == currentId -> MaterialTheme.colorScheme.onSurface
                    id in needsYou -> MaterialTheme.colorScheme.error
                    else -> MaterialTheme.colorScheme.outlineVariant
                }
                Box(Modifier.size(5.dp).background(color, CircleShape))
            }
        }
    }
}
