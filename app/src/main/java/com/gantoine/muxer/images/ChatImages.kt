package com.gantoine.muxer.images

import android.content.ContentResolver
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.ImageDecoder
import android.net.Uri
import android.os.Build
import android.util.Base64
import android.util.Log
import java.io.ByteArrayOutputStream
import kotlin.math.max
import kotlin.math.roundToInt
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

data class EncodedImage(val data: String, val format: String)

class UnreadableImageException : Exception("Couldn't read that image.")

object ChatImages {
    const val MAX_IMAGES = 4
    const val MAX_EDGE = 2048
    private const val JPEG_QUALITY = 90
    private const val TAG = "ChatImages"

    fun scaleFor(width: Int, height: Int, maxEdge: Int): Float {
        val longest = max(width, height)
        return if (longest <= maxEdge || longest <= 0) 1f else maxEdge.toFloat() / longest
    }

    fun merge(current: List<Uri>, added: List<Uri>): List<Uri> = (current + added).distinct().take(MAX_IMAGES)

    suspend fun encode(resolver: ContentResolver, uri: Uri): EncodedImage = withContext(Dispatchers.IO) {
        val bitmap = runCatching { decode(resolver, uri, MAX_EDGE) }.getOrElse { error ->
            Log.w(TAG, "Couldn't decode $uri", error)
            throw UnreadableImageException()
        }
        val opaque = if (bitmap.hasAlpha()) onWhite(bitmap).also { bitmap.recycle() } else bitmap
        val bytes = ByteArrayOutputStream().use { out ->
            opaque.compress(Bitmap.CompressFormat.JPEG, JPEG_QUALITY, out)
            out.toByteArray()
        }
        opaque.recycle()
        EncodedImage(Base64.encodeToString(bytes, Base64.NO_WRAP), "jpeg")
    }

    suspend fun thumbnail(resolver: ContentResolver, uri: Uri, edge: Int): Bitmap? = withContext(Dispatchers.IO) {
        runCatching { decode(resolver, uri, edge) }.getOrNull()
    }

    private fun decode(resolver: ContentResolver, uri: Uri, maxEdge: Int): Bitmap =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            ImageDecoder.decodeBitmap(ImageDecoder.createSource(resolver, uri)) { decoder, info, _ ->
                val scale = scaleFor(info.size.width, info.size.height, maxEdge)
                if (scale < 1f) decoder.setTargetSize(scaled(info.size.width, scale), scaled(info.size.height, scale))
                decoder.allocator = ImageDecoder.ALLOCATOR_SOFTWARE
            }
        } else {
            decodeWithBitmapFactory(resolver, uri, maxEdge)
        }

    private fun decodeWithBitmapFactory(resolver: ContentResolver, uri: Uri, maxEdge: Int): Bitmap {
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        resolver.openInputStream(uri).use { BitmapFactory.decodeStream(it, null, bounds) }
        var sampleSize = 1
        while (max(bounds.outWidth, bounds.outHeight) / (sampleSize * 2) >= maxEdge) sampleSize *= 2
        val options = BitmapFactory.Options().apply { inSampleSize = sampleSize }
        val decoded = resolver.openInputStream(uri).use { BitmapFactory.decodeStream(it, null, options) }
            ?: throw UnreadableImageException()
        val scale = scaleFor(decoded.width, decoded.height, maxEdge)
        if (scale >= 1f) return decoded
        return Bitmap.createScaledBitmap(decoded, scaled(decoded.width, scale), scaled(decoded.height, scale), true)
            .also { decoded.recycle() }
    }

    private fun onWhite(bitmap: Bitmap): Bitmap {
        val opaque = Bitmap.createBitmap(bitmap.width, bitmap.height, Bitmap.Config.ARGB_8888)
        Canvas(opaque).apply {
            drawColor(Color.WHITE)
            drawBitmap(bitmap, 0f, 0f, null)
        }
        return opaque
    }

    private fun scaled(size: Int, scale: Float): Int = (size * scale).roundToInt().coerceAtLeast(1)
}
