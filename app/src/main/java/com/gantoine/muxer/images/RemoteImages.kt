package com.gantoine.muxer.images

import android.content.ContentValues
import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import android.util.Base64
import android.util.LruCache
import com.gantoine.muxer.protocol.ChatImageData
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

class RemoteImage(val bytes: ByteArray, val format: String) {
    val mimeType: String get() = "image/" + if (format == "jpg") "jpeg" else format
}

class RemoteImages(private val fetch: suspend (String) -> ChatImageData?) {
    private val thumbnails = object : LruCache<String, Bitmap>(CACHE_BYTES) {
        override fun sizeOf(key: String, value: Bitmap): Int = value.byteCount
    }

    suspend fun thumbnail(id: String, edge: Int): Bitmap? {
        thumbnails.get(id)?.let { return it }
        val image = original(id) ?: return null
        val bitmap = withContext(Dispatchers.Default) { decode(image.bytes, edge) } ?: return null
        thumbnails.put(id, bitmap)
        return bitmap
    }

    suspend fun original(id: String): RemoteImage? {
        val data = fetch(id) ?: return null
        val bytes = withContext(Dispatchers.Default) { runCatching { Base64.decode(data.data, Base64.DEFAULT) }.getOrNull() }
        return bytes?.takeIf { it.isNotEmpty() }?.let { RemoteImage(it, data.format) }
    }

    companion object {
        private const val CACHE_BYTES = 24 * 1024 * 1024

        val canSave: Boolean get() = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q

        fun decode(bytes: ByteArray, edge: Int): Bitmap? {
            val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
            BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
            var sampleSize = 1
            while (minOf(bounds.outWidth, bounds.outHeight) / (sampleSize * 2) >= edge) sampleSize *= 2
            return BitmapFactory.decodeByteArray(bytes, 0, bytes.size, BitmapFactory.Options().apply { inSampleSize = sampleSize })
        }

        suspend fun save(context: Context, image: RemoteImage): Boolean = withContext(Dispatchers.IO) {
            if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return@withContext false
            val resolver = context.contentResolver
            val values = ContentValues().apply {
                put(MediaStore.Images.Media.DISPLAY_NAME, "muxer-${System.currentTimeMillis()}.${image.format}")
                put(MediaStore.Images.Media.MIME_TYPE, image.mimeType)
                put(MediaStore.Images.Media.RELATIVE_PATH, Environment.DIRECTORY_PICTURES + "/Muxer")
            }
            val uri = resolver.insert(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, values) ?: return@withContext false
            runCatching { resolver.openOutputStream(uri)?.use { it.write(image.bytes) } != null }
                .getOrDefault(false)
                .also { saved -> if (!saved) resolver.delete(uri, null, null) }
        }
    }
}
