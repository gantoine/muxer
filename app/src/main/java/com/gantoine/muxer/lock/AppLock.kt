package com.gantoine.muxer.lock

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.first

data class LockSettings(val enabled: Boolean = false, val timeoutMs: Long = AppLock.DEFAULT_TIMEOUT_MS)

class AppLock(private val now: () -> Long) {
    private val current = MutableStateFlow<LockSettings?>(null)
    private val lockedState = MutableStateFlow<Boolean?>(null)
    private var startedActivities = 0
    private var backgroundedAt: Long? = null

    val settings: StateFlow<LockSettings?> = current.asStateFlow()
    val locked: StateFlow<Boolean?> = lockedState.asStateFlow()

    val requiresUnlockForActions: Boolean get() = current.value?.enabled ?: true

    @Synchronized
    fun apply(settings: LockSettings) {
        val firstLoad = current.value == null
        current.value = settings
        when {
            !settings.enabled -> lockedState.value = false
            firstLoad -> lockedState.value = true
        }
    }

    @Synchronized
    fun activityStarted() {
        startedActivities++
        if (startedActivities != 1) return
        val since = backgroundedAt ?: return
        backgroundedAt = null
        val settings = current.value ?: return
        if (settings.enabled && now() - since >= settings.timeoutMs) lockedState.value = true
    }

    @Synchronized
    fun activityStopped(changingConfigurations: Boolean) {
        startedActivities = (startedActivities - 1).coerceAtLeast(0)
        if (startedActivities == 0 && !changingConfigurations) backgroundedAt = now()
    }

    @Synchronized
    fun unlock() {
        lockedState.value = false
    }

    suspend fun blocksActions(deviceLocked: Boolean): Boolean = deviceLocked && current.filterNotNull().first().enabled

    companion object {
        const val DEFAULT_TIMEOUT_MS = 60_000L
        val TIMEOUT_CHOICES_MS = listOf(60_000L, 5 * 60_000L, 15 * 60_000L, 60 * 60_000L)

        fun describeTimeout(timeoutMs: Long): String {
            val minutes = timeoutMs / 60_000L
            return when {
                minutes >= 60 && minutes % 60 == 0L -> if (minutes == 60L) "After 1 hour" else "After ${minutes / 60} hours"
                minutes == 1L -> "After 1 minute"
                else -> "After $minutes minutes"
            }
        }
    }
}
