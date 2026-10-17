package com.gantoine.muxer.ui

import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectTransformGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.HideImage
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.gantoine.muxer.images.RemoteImage
import com.gantoine.muxer.images.RemoteImages
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private const val MAX_ZOOM = 6f

private sealed interface ViewerImage {
    data object Loading : ViewerImage
    data object Missing : ViewerImage
    class Loaded(val image: RemoteImage, val bitmap: Bitmap) : ViewerImage
}

@Composable
fun ImageViewer(id: String, images: RemoteImages, onDismiss: () -> Unit) {
    val configuration = LocalConfiguration.current
    val edge = with(LocalDensity.current) { maxOf(configuration.screenWidthDp, configuration.screenHeightDp).dp.roundToPx() }
    val state by produceState<ViewerImage>(ViewerImage.Loading, id) {
        val image = images.original(id)
        val bitmap = image?.let { withContext(Dispatchers.Default) { RemoteImages.decode(it.bytes, edge) } }
        value = if (image != null && bitmap != null) ViewerImage.Loaded(image, bitmap) else ViewerImage.Missing
    }
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Box(Modifier.fillMaxSize().background(Color.Black)) {
            when (val current = state) {
                ViewerImage.Loading -> CircularProgressIndicator(Modifier.align(Alignment.Center), color = Color.White)
                ViewerImage.Missing -> Icon(
                    Icons.Outlined.HideImage,
                    contentDescription = "This image is no longer available",
                    tint = Color.White.copy(alpha = 0.7f),
                    modifier = Modifier.align(Alignment.Center),
                )
                is ViewerImage.Loaded -> ZoomableImage(current.bitmap)
            }
            ViewerBar((state as? ViewerImage.Loaded)?.image, onDismiss)
        }
    }
}

@Composable
private fun ZoomableImage(bitmap: Bitmap) {
    var scale by remember { mutableFloatStateOf(1f) }
    var offset by remember { mutableStateOf(Offset.Zero) }
    Image(
        bitmap.asImageBitmap(),
        contentDescription = "Image in the conversation",
        contentScale = ContentScale.Fit,
        modifier = Modifier
            .fillMaxSize()
            .pointerInput(Unit) {
                detectTransformGestures { _, pan, zoom, _ ->
                    scale = (scale * zoom).coerceIn(1f, MAX_ZOOM)
                    offset = if (scale == 1f) Offset.Zero else offset + pan
                }
            }
            .pointerInput(Unit) {
                detectTapGestures(onDoubleTap = {
                    if (scale > 1f) {
                        scale = 1f
                        offset = Offset.Zero
                    } else {
                        scale = 2.5f
                    }
                })
            }
            .graphicsLayer {
                scaleX = scale
                scaleY = scale
                translationX = offset.x
                translationY = offset.y
            },
    )
}

@Composable
private fun ViewerBar(image: RemoteImage?, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var status by remember { mutableStateOf<String?>(null) }
    Row(
        Modifier.fillMaxWidth().background(Color.Black.copy(alpha = 0.5f)).statusBarsPadding().padding(4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        IconButton(onClick = onDismiss) {
            Icon(Icons.Filled.Close, contentDescription = "Close", tint = Color.White)
        }
        Box(Modifier.weight(1f))
        status?.let { Text(it, color = Color.White, style = MaterialTheme.typography.bodyMedium) }
        if (image != null && RemoteImages.canSave) {
            IconButton(onClick = {
                scope.launch {
                    status = if (RemoteImages.save(context, image)) "Saved to Pictures/Muxer" else "Couldn't save the image"
                }
            }) {
                Icon(Icons.Outlined.Download, contentDescription = "Save to phone", tint = Color.White)
            }
        }
    }
}
