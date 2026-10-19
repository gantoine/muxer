package com.gantoine.muxer.ui

import android.net.Uri
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.navigation.NavBackStackEntry
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.gantoine.muxer.MuxerApplication
import com.gantoine.muxer.share.ShareRoute
import com.gantoine.muxer.share.ShareRouting
import com.gantoine.muxer.share.ShareTarget
import kotlinx.coroutines.flow.first

sealed interface LaunchTarget {
    data class Pair(val url: String, val code: String) : LaunchTarget
    data class Terminal(val macId: String, val workspaceId: String, val surfaceId: String) : LaunchTarget
    data object PairComputer : LaunchTarget
    data class Share(val text: String, val shortcutId: String?, val images: List<Uri> = emptyList()) : LaunchTarget
}

private object Routes {
    const val MACS = "macs"
    const val APP_SETTINGS = "settings"
    const val PAIR = "pair?url={url}&code={code}"
    const val WORKSPACES = "mac/{macId}"
    const val SETTINGS = "mac/{macId}/settings"
    const val NOTIFICATIONS = "mac/{macId}/notifications"
    const val SESSIONS = "mac/{macId}/sessions"
    const val CHECKLIST = "mac/{macId}/workspace/{workspaceId}/checklist"
    const val METRICS = "mac/{macId}/metrics"
    const val AUTOMATIONS = "mac/{macId}/automations"
    const val AUTOMATION = "mac/{macId}/automations/{ruleId}"
    const val TERMINAL = "terminal/{macId}/{workspaceId}/{surfaceId}?message={message}&images={images}"
    const val SHARE = "share?text={text}&images={images}"

    fun pair(url: String = "", code: String = "") = "pair?url=${Uri.encode(url)}&code=${Uri.encode(code)}"
    fun workspaces(macId: String) = "mac/${Uri.encode(macId)}"
    fun settings(macId: String) = "mac/${Uri.encode(macId)}/settings"
    fun notifications(macId: String) = "mac/${Uri.encode(macId)}/notifications"
    fun sessions(macId: String) = "mac/${Uri.encode(macId)}/sessions"
    fun checklist(macId: String, workspaceId: String) = "mac/${Uri.encode(macId)}/workspace/${Uri.encode(workspaceId)}/checklist"
    fun metrics(macId: String) = "mac/${Uri.encode(macId)}/metrics"
    fun automations(macId: String) = "mac/${Uri.encode(macId)}/automations"
    fun automation(macId: String, ruleId: String) = "mac/${Uri.encode(macId)}/automations/${Uri.encode(ruleId)}"
    fun terminal(macId: String, workspaceId: String, surfaceId: String, message: String = "", images: List<Uri> = emptyList()) =
        "terminal/${Uri.encode(macId)}/${Uri.encode(workspaceId)}/${Uri.encode(surfaceId)}?message=${Uri.encode(message)}&images=${encodeImages(images)}"
    fun share(text: String, images: List<Uri>) = "share?text=${Uri.encode(text)}&images=${encodeImages(images)}"

    private fun encodeImages(images: List<Uri>) = Uri.encode(images.joinToString("\n"))
    fun decodeImages(argument: String?): List<Uri> = argument.orEmpty().lines().filter { it.isNotEmpty() }.map(Uri::parse)
}

@Composable
fun AppNavigation(app: MuxerApplication, launchTarget: LaunchTarget?, onLaunchTargetHandled: () -> Unit) {
    val navController = rememberNavController()

    LaunchedEffect(launchTarget) {
        when (launchTarget) {
            null -> return@LaunchedEffect
            is LaunchTarget.Pair -> navController.navigate(Routes.pair(launchTarget.url, launchTarget.code))
            LaunchTarget.PairComputer -> {
                navController.popBackStack(Routes.MACS, inclusive = false)
                navController.navigate(Routes.pair())
            }
            is LaunchTarget.Terminal -> navController.openTerminal(launchTarget.macId, launchTarget.workspaceId, launchTarget.surfaceId)
            is LaunchTarget.Share -> {
                val route = ShareRouting.route(
                    shortcutId = launchTarget.shortcutId,
                    snapshots = app.snapshots.current().values,
                    pairedMacIds = app.store.macs.first().map { it.id },
                )
                when (route) {
                    is ShareRoute.Open -> navController.openShared(route.target, launchTarget.text, launchTarget.images)
                    ShareRoute.Pick -> {
                        navController.popBackStack(Routes.MACS, inclusive = false)
                        navController.navigate(Routes.share(launchTarget.text, launchTarget.images))
                    }
                }
            }
        }
        onLaunchTargetHandled()
    }

    NavHost(navController = navController, startDestination = Routes.MACS) {
        composable(Routes.MACS) {
            MacsScreen(
                app = app,
                onOpenMac = { navController.navigate(Routes.workspaces(it.id)) },
                onOpenSettings = { navController.navigate(Routes.settings(it.id)) },
                onAddMac = { navController.navigate(Routes.pair()) },
                onOpenAppSettings = { navController.navigate(Routes.APP_SETTINGS) },
            )
        }
        composable(Routes.APP_SETTINGS) {
            AppSettingsScreen(app = app, onBack = { navController.popBackStack() })
        }
        composable(
            Routes.PAIR,
            arguments = listOf(
                navArgument("url") { type = NavType.StringType; defaultValue = "" },
                navArgument("code") { type = NavType.StringType; defaultValue = "" },
            ),
        ) { entry ->
            PairScreen(
                app = app,
                initialUrl = entry.arg("url"),
                initialCode = entry.arg("code"),
                onBack = { navController.popBackStack() },
                onPaired = { macId ->
                    navController.popBackStack(Routes.MACS, inclusive = false)
                    navController.navigate(Routes.workspaces(macId))
                },
            )
        }
        composable(Routes.WORKSPACES, arguments = listOf(macArgument)) { entry ->
            val macId = entry.arg("macId")
            WorkspacesScreen(
                app = app,
                macId = macId,
                onBack = { navController.popBackStack() },
                onOpenSettings = { navController.navigate(Routes.settings(macId)) },
                onOpenNotifications = { navController.navigate(Routes.notifications(macId)) },
                onOpenChecklist = { navController.navigate(Routes.checklist(macId, it)) },
                onOpenTerminal = { workspaceId, surfaceId -> navController.navigate(Routes.terminal(macId, workspaceId, surfaceId)) },
            )
        }
        composable(Routes.NOTIFICATIONS, arguments = listOf(macArgument)) { entry ->
            val macId = entry.arg("macId")
            NotificationsScreen(
                app = app,
                macId = macId,
                onBack = { navController.popBackStack() },
                onOpenTerminal = { workspaceId, surfaceId -> navController.navigate(Routes.terminal(macId, workspaceId, surfaceId)) },
            )
        }
        composable(
            Routes.CHECKLIST,
            arguments = listOf(
                macArgument,
                navArgument("workspaceId") { type = NavType.StringType },
            ),
        ) { entry ->
            ChecklistScreen(
                app = app,
                macId = entry.arg("macId"),
                workspaceId = entry.arg("workspaceId"),
                onBack = { navController.popBackStack() },
            )
        }
        composable(Routes.SESSIONS, arguments = listOf(macArgument)) { entry ->
            val macId = entry.arg("macId")
            SessionsScreen(
                app = app,
                macId = macId,
                onBack = { navController.popBackStack() },
                onOpenTerminal = { workspaceId, surfaceId -> navController.navigate(Routes.terminal(macId, workspaceId, surfaceId)) },
            )
        }
        composable(Routes.METRICS, arguments = listOf(macArgument)) { entry ->
            MetricsScreen(
                app = app,
                macId = entry.arg("macId"),
                onBack = { navController.popBackStack() },
            )
        }
        composable(Routes.AUTOMATIONS, arguments = listOf(macArgument)) { entry ->
            val macId = entry.arg("macId")
            AutomationsScreen(
                app = app,
                macId = macId,
                onBack = { navController.popBackStack() },
                onOpenRule = { navController.navigate(Routes.automation(macId, it)) },
            )
        }
        composable(
            Routes.AUTOMATION,
            arguments = listOf(
                macArgument,
                navArgument("ruleId") { type = NavType.StringType },
            ),
        ) { entry ->
            AutomationScreen(
                app = app,
                macId = entry.arg("macId"),
                ruleId = entry.arg("ruleId"),
                onBack = { navController.popBackStack() },
            )
        }
        composable(Routes.SETTINGS, arguments = listOf(macArgument)) { entry ->
            ComputerSettingsScreen(
                app = app,
                macId = entry.arg("macId"),
                onBack = { navController.popBackStack() },
                onOpenMetrics = { navController.navigate(Routes.metrics(it)) },
                onOpenSessions = { navController.navigate(Routes.sessions(it)) },
                onOpenAutomations = { navController.navigate(Routes.automations(it)) },
                onRemoved = { navController.popBackStack(Routes.MACS, inclusive = false) },
            )
        }
        composable(
            Routes.TERMINAL,
            arguments = listOf(
                macArgument,
                navArgument("workspaceId") { type = NavType.StringType },
                navArgument("surfaceId") { type = NavType.StringType },
                navArgument("message") { type = NavType.StringType; defaultValue = "" },
                navArgument("images") { type = NavType.StringType; defaultValue = "" },
            ),
        ) { entry ->
            val macId = entry.arg("macId")
            val workspaceId = entry.arg("workspaceId")
            TerminalScreen(
                app = app,
                macId = macId,
                workspaceId = workspaceId,
                surfaceId = entry.arg("surfaceId"),
                initialMessage = entry.arg("message"),
                initialImages = Routes.decodeImages(entry.arg("images")),
                onBack = { navController.popBackStack() },
                onSwitchTerminal = { surfaceId ->
                    navController.navigate(Routes.terminal(macId, workspaceId, surfaceId)) {
                        popUpTo(Routes.TERMINAL) { inclusive = true }
                    }
                },
            )
        }
        composable(
            Routes.SHARE,
            arguments = listOf(
                navArgument("text") { type = NavType.StringType; defaultValue = "" },
                navArgument("images") { type = NavType.StringType; defaultValue = "" },
            ),
        ) { entry ->
            val text = entry.arg("text")
            val images = Routes.decodeImages(entry.arg("images"))
            ShareScreen(
                app = app,
                onBack = { navController.popBackStack() },
                onPick = { target -> navController.openShared(target, text, images) },
            )
        }
    }
}

private fun NavHostController.openShared(target: ShareTarget, text: String, images: List<Uri>) =
    openTerminal(target.macId, target.workspace.id, target.surfaceId, text, images)

private fun NavHostController.openTerminal(
    macId: String,
    workspaceId: String,
    surfaceId: String,
    text: String = "",
    images: List<Uri> = emptyList(),
) {
    popBackStack(Routes.MACS, inclusive = false)
    navigate(Routes.workspaces(macId))
    navigate(Routes.terminal(macId, workspaceId, surfaceId, text, images))
}

private val macArgument = navArgument("macId") { type = NavType.StringType }

private fun NavBackStackEntry.arg(name: String): String = arguments?.getString(name).orEmpty()
