package com.gantoine.muxer

import com.gantoine.muxer.lock.AppLock
import com.gantoine.muxer.lock.LockSettings
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class AppLockTest {
    private var now = 0L
    private val lock = AppLock { now }

    private fun leaveFor(ms: Long) {
        lock.activityStopped(changingConfigurations = false)
        now += ms
        lock.activityStarted()
    }

    private fun unlockedWith(settings: LockSettings) {
        lock.apply(settings)
        lock.activityStarted()
        lock.unlock()
    }

    @Test
    fun staysUndecidedUntilSettingsLoad() {
        assertNull(lock.locked.value)
    }

    @Test
    fun locksOnColdStartWhenOn() {
        lock.apply(LockSettings(enabled = true))
        assertEquals(true, lock.locked.value)
    }

    @Test
    fun staysOpenOnColdStartWhenOff() {
        lock.apply(LockSettings(enabled = false))
        assertEquals(false, lock.locked.value)
    }

    @Test
    fun turningTheLockOnDoesNotLockRightAway() {
        unlockedWith(LockSettings(enabled = false))
        lock.apply(LockSettings(enabled = true))
        assertEquals(false, lock.locked.value)
    }

    @Test
    fun turningTheLockOffUnlocks() {
        lock.apply(LockSettings(enabled = true))
        lock.apply(LockSettings(enabled = false))
        assertEquals(false, lock.locked.value)
    }

    @Test
    fun shortTripsAwayKeepItOpen() {
        unlockedWith(LockSettings(enabled = true, timeoutMs = 60_000L))
        leaveFor(59_999L)
        assertEquals(false, lock.locked.value)
    }

    @Test
    fun locksAfterTheTimeout() {
        unlockedWith(LockSettings(enabled = true, timeoutMs = 60_000L))
        leaveFor(60_000L)
        assertEquals(true, lock.locked.value)
    }

    @Test
    fun usesTheLatestTimeout() {
        unlockedWith(LockSettings(enabled = true, timeoutMs = 60_000L))
        lock.apply(LockSettings(enabled = true, timeoutMs = 5 * 60_000L))
        leaveFor(2 * 60_000L)
        assertEquals(false, lock.locked.value)
    }

    @Test
    fun neverLocksWhenOff() {
        unlockedWith(LockSettings(enabled = false, timeoutMs = 60_000L))
        leaveFor(60 * 60_000L)
        assertEquals(false, lock.locked.value)
    }

    @Test
    fun rotatingDoesNotCountAsLeaving() {
        unlockedWith(LockSettings(enabled = true, timeoutMs = 60_000L))
        lock.activityStopped(changingConfigurations = true)
        now += 60 * 60_000L
        lock.activityStarted()
        assertEquals(false, lock.locked.value)
    }

    @Test
    fun movingBetweenItsOwnScreensDoesNotCountAsLeaving() {
        unlockedWith(LockSettings(enabled = true, timeoutMs = 60_000L))
        lock.activityStarted()
        lock.activityStopped(changingConfigurations = false)
        now += 60 * 60_000L
        lock.activityStopped(changingConfigurations = false)
        lock.activityStarted()
        assertEquals(false, lock.locked.value)
    }

    @Test
    fun timesFromWhenTheLastScreenClosed() {
        unlockedWith(LockSettings(enabled = true, timeoutMs = 60_000L))
        lock.activityStarted()
        lock.activityStopped(changingConfigurations = false)
        now += 60 * 60_000L
        leaveFor(30_000L)
        assertEquals(false, lock.locked.value)
    }

    @Test
    fun requiresUnlockForActionsUntilSettingsLoad() {
        assertTrue(lock.requiresUnlockForActions)
        lock.apply(LockSettings(enabled = false))
        assertFalse(lock.requiresUnlockForActions)
    }

    @Test
    fun blocksActionsOnlyOnALockedPhoneWithTheLockOn() = runTest {
        lock.apply(LockSettings(enabled = true))
        assertTrue(lock.blocksActions(deviceLocked = true))
        assertFalse(lock.blocksActions(deviceLocked = false))
        lock.apply(LockSettings(enabled = false))
        assertFalse(lock.blocksActions(deviceLocked = true))
    }

    @Test
    fun describesTimeouts() {
        assertEquals(
            listOf("After 1 minute", "After 5 minutes", "After 15 minutes", "After 1 hour"),
            AppLock.TIMEOUT_CHOICES_MS.map(AppLock::describeTimeout),
        )
    }
}
