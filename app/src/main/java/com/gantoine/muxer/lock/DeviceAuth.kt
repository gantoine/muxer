package com.gantoine.muxer.lock

import android.app.KeyguardManager
import android.content.Context
import android.os.Build
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricManager.Authenticators.BIOMETRIC_STRONG
import androidx.biometric.BiometricManager.Authenticators.BIOMETRIC_WEAK
import androidx.biometric.BiometricManager.Authenticators.DEVICE_CREDENTIAL
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity

object DeviceAuth {
    private val authenticators: Int
        get() = if (Build.VERSION.SDK_INT in Build.VERSION_CODES.P..Build.VERSION_CODES.Q) {
            BIOMETRIC_WEAK or DEVICE_CREDENTIAL
        } else {
            BIOMETRIC_STRONG or DEVICE_CREDENTIAL
        }

    fun canAuthenticate(context: Context): Boolean =
        BiometricManager.from(context).canAuthenticate(authenticators) == BiometricManager.BIOMETRIC_SUCCESS

    fun isDeviceLocked(context: Context): Boolean =
        context.getSystemService(KeyguardManager::class.java)?.isDeviceLocked ?: true

    fun authenticate(activity: FragmentActivity, title: String, subtitle: String? = null, onSuccess: () -> Unit) {
        val callback = object : BiometricPrompt.AuthenticationCallback() {
            override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) = onSuccess()
        }
        val info = BiometricPrompt.PromptInfo.Builder()
            .setTitle(title)
            .setSubtitle(subtitle)
            .setAllowedAuthenticators(authenticators)
            .setConfirmationRequired(false)
            .build()
        BiometricPrompt(activity, ContextCompat.getMainExecutor(activity), callback).authenticate(info)
    }
}
