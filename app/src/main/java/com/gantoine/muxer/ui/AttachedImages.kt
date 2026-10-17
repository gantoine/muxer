package com.gantoine.muxer.ui

import android.graphics.Bitmap
import android.net.Uri
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.AddCircleOutline
import androidx.compose.material.icons.outlined.Image
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.gantoine.muxer.images.ChatImages
import com.gantoine.muxer.images.RemoteImages

private val THUMBNAIL_SIZE = 64.dp
private val SENT_IMAGE_SIZE = 160.dp
private val SENT_IMAGES_SIZE = 72.dp

@Composable
fun AttachImageButton(enabled: Boolean, onClick: () -> Unit) {
    IconButton(onClick = onClick, enabled = enabled) {
        Icon(Icons.Outlined.AddCircleOutline, contentDescription = "Attach a screenshot or photo")
    }
}

@Composable
fun AttachedImages(images: List<Uri>, onRemove: (Uri) -> Unit, modifier: Modifier = Modifier) {
    if (images.isEmpty()) return
    Row(
        modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 12.dp, vertical = 6.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        images.forEach { uri -> AttachedImage(uri, onRemove = { onRemove(uri) }) }
    }
}

@Composable
private fun AttachedImage(uri: Uri, onRemove: () -> Unit) {
    val resolver = LocalContext.current.contentResolver
    val edge = with(LocalDensity.current) { THUMBNAIL_SIZE.roundToPx() }
    val thumbnail by produceState<Bitmap?>(null, uri) { value = ChatImages.thumbnail(resolver, uri, edge) }
    Box(Modifier.size(THUMBNAIL_SIZE).clip(RoundedCornerShape(12.dp)).background(MaterialTheme.colorScheme.surfaceContainerHigh)) {
        thumbnail?.let {
            Image(it.asImageBitmap(), contentDescription = "Attached image", contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
        }
        Box(
            Modifier
                .align(Alignment.TopEnd)
                .padding(4.dp)
                .size(24.dp)
                .clip(CircleShape)
                .background(MaterialTheme.colorScheme.surface.copy(alpha = 0.85f)),
            contentAlignment = Alignment.Center,
        ) {
            IconButton(onClick = onRemove, modifier = Modifier.size(24.dp)) {
                Icon(Icons.Filled.Close, contentDescription = "Remove this image", modifier = Modifier.size(16.dp))
            }
        }
    }
}

@Composable
fun ChatImageTiles(ids: List<String>, images: RemoteImages, onOpen: (String) -> Unit) {
    val size = if (ids.size == 1) SENT_IMAGE_SIZE else SENT_IMAGES_SIZE
    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        ids.forEach { id -> ChatImageTile(id, size, images, onClick = { onOpen(id) }) }
    }
}

@Composable
private fun ChatImageTile(id: String, size: Dp, images: RemoteImages, onClick: () -> Unit) {
    val edge = with(LocalDensity.current) { SENT_IMAGE_SIZE.roundToPx() }
    val bitmap by produceState<Bitmap?>(null, id) { value = images.thumbnail(id, edge) }
    Box(
        Modifier
            .size(size)
            .clip(RoundedCornerShape(14.dp))
            .background(MaterialTheme.colorScheme.surfaceContainerHigh)
            .clickable(onClickLabel = "View image", onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        val loaded = bitmap
        if (loaded != null) {
            Image(loaded.asImageBitmap(), contentDescription = "Image in the conversation", contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
        } else {
            Icon(Icons.Outlined.Image, contentDescription = "Image in the conversation", tint = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}
