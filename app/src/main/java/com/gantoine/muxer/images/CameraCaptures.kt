package com.gantoine.muxer.images

import android.content.Context
import android.net.Uri
import androidx.core.content.FileProvider
import java.io.File
import java.util.concurrent.TimeUnit

object CameraCaptures {
    private const val FOLDER = "camera"
    private val MAX_AGE_MILLIS = TimeUnit.DAYS.toMillis(1)

    fun newPhoto(context: Context): Uri {
        val folder = File(context.cacheDir, FOLDER).apply { mkdirs() }
        val cutoff = System.currentTimeMillis() - MAX_AGE_MILLIS
        folder.listFiles()?.filter { it.lastModified() < cutoff }?.forEach { it.delete() }
        val photo = File(folder, "photo-${System.currentTimeMillis()}.jpg")
        return FileProvider.getUriForFile(context, "${context.packageName}.files", photo)
    }
}
