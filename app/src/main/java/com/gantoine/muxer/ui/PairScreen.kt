package com.gantoine.muxer.ui

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.HelpOutline
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material.icons.filled.QrCodeScanner
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExperimentalMaterial3ExpressiveApi
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.net.PairingClient
import com.gantoine.muxer.net.PairingException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class, ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun PairScreen(app: MuxerApplication, initialUrl: String, initialCode: String, onBack: () -> Unit, onPaired: (String) -> Unit) {
    var address by remember { mutableStateOf(initialUrl) }
    var code by remember { mutableStateOf(initialCode) }
    var error by remember { mutableStateOf<String?>(null) }
    var working by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val host = PairingClient.normalizeRelayUrl(address)?.host
    val context = LocalContext.current
    var scanning by remember { mutableStateOf(false) }
    val cameraPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) scanning = true else error = "Allow camera access to scan, or type the address and code."
    }
    var pairedId by rememberSaveable { mutableStateOf<String?>(null) }
    val notificationPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {
        app.notificationPermissionChanged()
        pairedId?.let(onPaired)
    }

    fun scan() {
        error = null
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) {
            scanning = true
        } else {
            cameraPermission.launch(Manifest.permission.CAMERA)
        }
    }

    fun pair() {
        working = true
        error = null
        scope.launch {
            try {
                val deviceName = "${Build.MANUFACTURER} ${Build.MODEL}".trim()
                val (url, response) = app.pairing.pair(address, code, deviceName)
                val firstComputer = app.store.macs.first().isEmpty()
                app.store.add(response.deviceId, response.macName, url, response.token, notificationSeq = 0)
                if (firstComputer && Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !app.notifier.canNotify()) {
                    pairedId = response.deviceId
                    notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
                } else {
                    onPaired(response.deviceId)
                }
            } catch (failure: PairingException) {
                error = failure.message
            } catch (failure: CancellationException) {
                throw failure
            } catch (failure: Exception) {
                error = "Pairing failed on this phone: ${failure.message ?: failure::class.simpleName}"
            } finally {
                working = false
            }
        }
    }

    Box(Modifier.fillMaxSize()) {
        Scaffold(
            topBar = {
                CmuxTopBar(
                    title = "Pair a computer",
                    onBack = onBack,
                )
            },
        ) { padding ->
            Column(
                Modifier.padding(padding).fillMaxSize().verticalScroll(rememberScrollState()).imePadding()
                    .padding(start = 20.dp, end = 20.dp, top = 4.dp, bottom = 20.dp),
                verticalArrangement = Arrangement.spacedBy(16.dp),
            ) {
                var showSteps by rememberSaveable { mutableStateOf(false) }
                val chevron by animateFloatAsState(if (showSteps) 180f else 0f, label = "chevron")
                Surface(
                    shape = RoundedCornerShape(24.dp),
                    color = MaterialTheme.colorScheme.surfaceContainer,
                    onClick = { showSteps = !showSteps },
                ) {
                    Column(Modifier.padding(horizontal = 20.dp, vertical = 16.dp)) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Icon(
                                Icons.AutoMirrored.Outlined.HelpOutline,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.primary,
                                modifier = Modifier.size(28.dp),
                            )
                            Spacer(Modifier.width(14.dp))
                            Text("How to set up the relay", style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                            Icon(
                                Icons.Filled.ExpandMore,
                                contentDescription = if (showSteps) "Hide steps" else "Show steps",
                                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.rotate(chevron),
                            )
                        }
                        AnimatedVisibility(visible = showSteps) {
                            Column(Modifier.padding(top = 18.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
                                Step(1, "Install the relay on your computer", "brew install gantoine/tap/muxer")
                                Step(2, "Start pairing", "muxer pair")
                                Step(3, "Scan the QR code it shows, or type the address and code", null)
                            }
                        }
                    }
                }
                Spacer(Modifier.height(8.dp))
                FilledTonalButton(
                    onClick = ::scan,
                    enabled = !working,
                    modifier = Modifier.fillMaxWidth().height(56.dp),
                ) {
                    Icon(Icons.Filled.QrCodeScanner, contentDescription = null, Modifier.size(24.dp))
                    Spacer(Modifier.width(12.dp))
                    Text("Scan QR code", style = MaterialTheme.typography.titleMedium)
                }
                OrDivider()
                LabeledTextField(
                    value = address,
                    onValueChange = { address = it },
                    label = "Computer address",
                    placeholder = "https://my-mac.tailnet.ts.net:8765",
                    singleLine = true,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, autoCorrectEnabled = false),
                    modifier = Modifier.fillMaxWidth(),
                )
                LabeledTextField(
                    value = code,
                    onValueChange = { code = it.uppercase().take(9) },
                    label = "Pairing code",
                    placeholder = "ABCD-EFGH",
                    singleLine = true,
                    keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Characters, autoCorrectEnabled = false),
                    modifier = Modifier.fillMaxWidth(),
                )
                error?.let {
                    Surface(shape = RoundedCornerShape(16.dp), color = MaterialTheme.colorScheme.errorContainer) {
                        Text(it, color = MaterialTheme.colorScheme.onErrorContainer, modifier = Modifier.padding(16.dp))
                    }
                }
                Button(
                    onClick = ::pair,
                    enabled = !working && host != null && code.replace("-", "").length == 8,
                    modifier = Modifier.padding(top = 8.dp).fillMaxWidth().height(56.dp),
                ) {
                    if (working) {
                        LoadingIndicator(Modifier.size(32.dp), color = MaterialTheme.colorScheme.onPrimary)
                    } else {
                        Text("Pair", style = MaterialTheme.typography.titleMedium)
                    }
                }
                Text(
                    "Only pair with a computer you own.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
        if (scanning) {
            QrScanner(
                onDismiss = { scanning = false },
                onScanned = { link ->
                    scanning = false
                    address = link.url
                    code = link.code.uppercase().take(9)
                },
            )
        }
    }
}

@Composable
private fun Step(number: Int, title: String, command: String?) {
    Row(verticalAlignment = Alignment.Top) {
        Box(Modifier.size(28.dp), contentAlignment = Alignment.Center) { StepBadge("$number") }
        Spacer(Modifier.width(14.dp))
        Column {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            if (command != null) {
                Spacer(Modifier.height(6.dp))
                Surface(shape = RoundedCornerShape(10.dp), color = MaterialTheme.colorScheme.surfaceContainerHighest) {
                    Text(
                        command,
                        fontFamily = FontFamily.Monospace,
                        style = MaterialTheme.typography.bodyMedium,
                        modifier = Modifier.padding(horizontal = 12.dp, vertical = 8.dp),
                    )
                }
            }
        }
    }
}

@Composable
private fun StepBadge(label: String) {
    Box(
        Modifier.size(24.dp).background(MaterialTheme.colorScheme.primary, CircleShape),
        contentAlignment = Alignment.Center,
    ) {
        Text(label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onPrimary)
    }
}

@Composable
private fun OrDivider() {
    Row(Modifier.padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        HorizontalDivider(Modifier.weight(1f), color = MaterialTheme.colorScheme.outlineVariant)
        Text(
            "or",
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(horizontal = 16.dp),
        )
        HorizontalDivider(Modifier.weight(1f), color = MaterialTheme.colorScheme.outlineVariant)
    }
}
