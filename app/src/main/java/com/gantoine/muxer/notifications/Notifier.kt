package com.gantoine.muxer.notifications

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.service.notification.StatusBarNotification
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.Person
import androidx.core.app.RemoteInput
import androidx.core.content.ContextCompat
import com.gantoine.muxer.BubbleActivity
import com.gantoine.muxer.R
import com.gantoine.muxer.data.Mac
import com.gantoine.muxer.model.AgentNames
import com.gantoine.muxer.model.Approvals
import com.gantoine.muxer.model.parseInstant
import com.gantoine.muxer.notifications.LockScreenText.hiddenOnLockScreen
import com.gantoine.muxer.protocol.Approval
import com.gantoine.muxer.protocol.RelayNotification
import java.util.concurrent.ConcurrentHashMap

class Notifier(
    private val context: Context,
    private val shortcuts: ConversationShortcuts,
    private val requireUnlock: () -> Boolean,
) {
    private data class Conversation(val target: ConversationTarget, val macName: String, val agentName: String, val channel: String) {
        val notificationId: Int get() = "conversation:${target.key}".hashCode()
    }

    private val history = ConversationHistory()
    private val conversations = ConcurrentHashMap<String, Conversation>()
    private val you = Person.Builder().setName("You").setKey(YOU_KEY).build()
    private val app = Person.Builder().setName("Muxer").setKey(APP_KEY).build()

    fun createChannels() {
        val manager = context.getSystemService(NotificationManager::class.java)
        manager.createNotificationChannels(
            listOf(
                channel(APPROVALS_CHANNEL, R.string.channel_approvals, R.string.channel_approvals_description, NotificationManager.IMPORTANCE_HIGH),
                channel(COMPLETIONS_CHANNEL, R.string.channel_completions, R.string.channel_completions_description, NotificationManager.IMPORTANCE_HIGH),
                channel(ALERTS_CHANNEL, R.string.channel_alerts, R.string.channel_alerts_description, NotificationManager.IMPORTANCE_HIGH),
            ),
        )
        manager.deleteNotificationChannel(LEGACY_AGENT_CHANNEL)
        manager.createNotificationChannel(
            NotificationChannel(ACTIVITY_CHANNEL, context.getString(R.string.channel_activity), NotificationManager.IMPORTANCE_LOW)
                .apply { description = context.getString(R.string.channel_activity_description) },
        )
        manager.deleteNotificationChannel(LEGACY_CONNECTION_CHANNEL)
    }

    private fun channel(id: String, name: Int, description: Int, importance: Int) =
        NotificationChannel(id, context.getString(name), importance).apply { this.description = context.getString(description) }

    fun canNotify(): Boolean =
        Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU ||
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED

    fun show(mac: Mac, notification: RelayNotification) {
        if (!canNotify()) return
        val workspaceId = notification.workspaceId
        val surfaceId = notification.surfaceId
        if (!NotificationFilter.isConversation(notification) || workspaceId == null || surfaceId == null || !shortcuts.isAgentWorkspace(mac.id, workspaceId)) {
            showPlain(mac, notification)
            return
        }
        val target = ConversationTarget(mac.id, workspaceId, surfaceId)
        val agentName = shortcuts.agentName(mac.id, workspaceId)
            ?: notification.title?.takeIf { it.isNotBlank() }
            ?: notification.tabTitle?.takeIf { it.isNotBlank() }
            ?: "Agent"
        val conversation = Conversation(target, mac.name, agentName, NotificationFilter.channel(notification))
        conversations[target.key] = conversation
        val active = activeNotification(conversation.notificationId)
        if (active == null) history.clear(target.key) else restoreHistory(conversation, active)
        val text = ConversationText.message(notification, agentName)
        history.add(target.key, ConversationMessage(notification.id, text, Sender.Agent, time(notification.createdAt)))
        postConversation(conversation, silent = false)
    }

    private fun showPlain(mac: Mac, notification: RelayNotification) {
        val title = notification.title?.takeIf { it.isNotBlank() } ?: notification.tabTitle ?: mac.name
        val subText = listOfNotNull(mac.name, notification.tabTitle?.takeIf { it != title }).joinToString(" · ")
        val body = listOfNotNull(notification.subtitle?.takeIf { it.isNotBlank() }, notification.body).joinToString("\n")
        val channel = NotificationFilter.channel(notification)
        val built = NotificationCompat.Builder(context, channel)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setSubText(subText)
            .setContentText(body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(body))
            .setAutoCancel(true)
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setGroup(mac.id)
            .setContentIntent(openTerminalIntent(mac.id, notification.workspaceId, notification.surfaceId, notification.id.hashCode()))
            .setDeleteIntent(NotificationReadReceiver.notificationIntent(context, mac.id, notification.id))
            .hiddenOnLockScreen(context, requireUnlock(), channel, LockScreenText.NEW_NOTIFICATION)
            .build()
        post(notification.id.hashCode(), built)
    }

    fun dismiss(notification: RelayNotification) {
        NotificationManagerCompat.from(context).cancel(notification.id.hashCode())
        val (key, remaining) = history.remove(notification.id) ?: return
        val conversation = conversations[key] ?: return
        if (!ConversationHistory.hasAgentMessages(remaining) || activeNotification(conversation.notificationId) == null) {
            NotificationManagerCompat.from(context).cancel(conversation.notificationId)
            history.clear(key)
            return
        }
        postConversation(conversation, silent = true)
    }

    fun showReply(target: ConversationTarget, macName: String, agentName: String, text: String, failure: String?) {
        if (!canNotify()) return
        val conversation = conversations[target.key] ?: Conversation(target, macName, agentName, ALERTS_CHANNEL).also { conversations[target.key] = it }
        if (history.messages(target.key).isEmpty()) activeNotification(conversation.notificationId)?.let { restoreHistory(conversation, it) }
        val now = System.currentTimeMillis()
        history.add(target.key, ConversationMessage("reply:$now", text, Sender.You, now))
        if (failure != null) {
            history.add(target.key, ConversationMessage("failure:$now", "Not sent to ${conversation.agentName}: $failure", Sender.App, now))
        }
        postConversation(conversation, silent = true)
    }

    private fun postConversation(conversation: Conversation, silent: Boolean) {
        val messages = history.messages(conversation.target.key)
        val last = messages.lastOrNull() ?: return
        val agent = ConversationShortcuts.agentPerson(conversation.target.macId, conversation.target.workspaceId, conversation.agentName)
        val style = NotificationCompat.MessagingStyle(you)
        messages.forEach { message -> style.addMessage(message.text, message.time, personFor(message.sender, agent)) }
        val target = conversation.target
        val builder = NotificationCompat.Builder(context, conversation.channel)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(conversation.agentName)
            .setContentText(last.text)
            .setSubText(conversation.macName)
            .setStyle(style)
            .setShortcutId(ConversationShortcuts.id(target.macId, target.workspaceId))
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setAutoCancel(true)
            .setSilent(silent)
            .setGroup(target.macId)
            .setContentIntent(openTerminalIntent(target.macId, target.workspaceId, target.surfaceId, conversation.notificationId))
            .setDeleteIntent(NotificationReadReceiver.terminalIntent(context, target.macId, target.surfaceId))
            .addAction(messageAction(target, conversation.agentName))
            .hiddenOnLockScreen(context, requireUnlock(), conversation.channel, LockScreenText.NEW_NOTIFICATION)
        bubble(target, conversation.notificationId)?.let(builder::setBubbleMetadata)
        post(conversation.notificationId, builder.build())
    }

    fun showApproval(mac: Mac, approval: Approval) {
        if (!canNotify()) return
        val target = ConversationTarget(mac.id, approval.workspaceId, approval.surfaceId)
        val agentName = AgentNames.display(approval.agent) ?: shortcuts.agentName(mac.id, approval.workspaceId) ?: "Agent"
        val summary = Approvals.summary(approval)
        val body = listOfNotNull(
            approval.description?.takeIf { approval.isPermission && it.isNotBlank() && it != approval.detail },
            summary.takeIf { it.isNotBlank() },
            optionsLine(approval),
        ).joinToString("\n")
        val text = listOf(Approvals.title(approval), body).filter { it.isNotBlank() }.joinToString("\n")
        val style = NotificationCompat.MessagingStyle(you)
            .addMessage(text, time(approval.createdAt), ConversationShortcuts.agentPerson(mac.id, approval.workspaceId, agentName))
        val notificationId = approvalNotificationId(approval.id)
        val builder = NotificationCompat.Builder(context, APPROVALS_CHANNEL)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(Approvals.title(approval))
            .setSubText(mac.name)
            .setContentText(summary)
            .setStyle(style)
            .setShortcutId(ConversationShortcuts.id(mac.id, approval.workspaceId))
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setGroup(mac.id)
            .setContentIntent(openTerminalIntent(mac.id, approval.workspaceId, approval.surfaceId, notificationId))
            .hiddenOnLockScreen(context, requireUnlock(), APPROVALS_CHANNEL, LockScreenText.NEEDS_YOU)
        approvalActions(mac.id, approval).forEach(builder::addAction)
        bubble(target, notificationId)?.let(builder::setBubbleMetadata)
        post(notificationId, builder.build())
    }

    fun showApprovalFailed(macName: String, approval: ApprovalTarget, message: String) {
        if (!canNotify()) return
        val built = NotificationCompat.Builder(context, APPROVALS_CHANNEL)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("Your answer didn't reach $macName")
            .setContentText(message)
            .setStyle(NotificationCompat.BigTextStyle().bigText("$message\nOpen the terminal to answer there."))
            .setAutoCancel(true)
            .setGroup(approval.macId)
            .setContentIntent(openTerminalIntent(approval.macId, approval.workspaceId, approval.surfaceId, approvalNotificationId(approval.approvalId)))
            .hiddenOnLockScreen(context, requireUnlock(), APPROVALS_CHANNEL, LockScreenText.NEW_NOTIFICATION)
            .build()
        post(approvalNotificationId(approval.approvalId), built)
    }

    fun dismissApproval(approvalId: String) {
        NotificationManagerCompat.from(context).cancel(approvalNotificationId(approvalId))
    }

    private fun optionsLine(approval: Approval): String? {
        val question = approval.questions.singleOrNull()?.takeIf { approval.isQuestion } ?: return null
        if (NotificationActions.oneTapAnswers(question).size == question.options.size) return null
        return question.options.joinToString(" · ") { it.label }.takeIf { it.isNotBlank() }
    }

    private fun approvalActions(macId: String, approval: Approval): List<NotificationCompat.Action> {
        val target = ApprovalTarget(macId, approval.id, approval.workspaceId, approval.surfaceId)
        fun action(label: String, intent: PendingIntent) =
            NotificationCompat.Action.Builder(0, label, intent).setAuthenticationRequired(requireUnlock()).build()
        val decisions = Approvals.decisions(approval)
        if (decisions.isNotEmpty()) {
            return decisions.map { action(it.label, ApprovalActionReceiver.intent(context, target, mode = it.mode)) }
        }
        val question = approval.questions.singleOrNull() ?: return emptyList()
        val options = NotificationActions.oneTapAnswers(question).map { label ->
            action(label, ApprovalActionReceiver.intent(context, target, answer = label))
        }
        val typed = replyAction("Type an answer", ApprovalActionReceiver.typedAnswerIntent(context, target), ApprovalActionReceiver.KEY_TYPED_ANSWER, "Your answer")
        return if (NotificationActions.offersTypedAnswer(question)) options + typed else options
    }

    private fun messageAction(target: ConversationTarget, agentName: String): NotificationCompat.Action =
        replyAction("Message", AgentReplyReceiver.intent(context, target, agentName), AgentReplyReceiver.KEY_TEXT, "Message $agentName")

    private fun replyAction(label: String, intent: PendingIntent, key: String, inputLabel: String): NotificationCompat.Action =
        NotificationCompat.Action.Builder(0, label, intent)
            .addRemoteInput(RemoteInput.Builder(key).setLabel(inputLabel).build())
            .setSemanticAction(NotificationCompat.Action.SEMANTIC_ACTION_REPLY)
            .setShowsUserInterface(false)
            .setAllowGeneratedReplies(false)
            .setAuthenticationRequired(requireUnlock())
            .build()

    private fun bubble(target: ConversationTarget, requestCode: Int): NotificationCompat.BubbleMetadata? {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.R) return null
        val intent = PendingIntent.getActivity(
            context,
            requestCode,
            BubbleActivity.intent(context, target),
            mutableFlag() or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return NotificationCompat.BubbleMetadata.Builder(intent, shortcuts.icon(target.macId, target.workspaceId))
            .setDesiredHeight(BUBBLE_HEIGHT_DP)
            .setAutoExpandBubble(false)
            .setSuppressNotification(false)
            .build()
    }

    private fun personFor(sender: Sender, agent: Person): Person? = when (sender) {
        Sender.Agent -> agent
        Sender.You -> null
        Sender.App -> app
    }

    private fun senderOf(person: Person?): Sender = when (person?.key) {
        null, YOU_KEY -> Sender.You
        APP_KEY -> Sender.App
        else -> Sender.Agent
    }

    private fun restoreHistory(conversation: Conversation, active: StatusBarNotification) {
        if (history.messages(conversation.target.key).isNotEmpty()) return
        val style = NotificationCompat.MessagingStyle.extractMessagingStyleFromNotification(active.notification) ?: return
        history.restore(
            conversation.target.key,
            style.messages.mapIndexed { index, message ->
                ConversationMessage("restored:$index", message.text?.toString().orEmpty(), senderOf(message.person), message.timestamp)
            },
        )
    }

    private fun activeNotification(id: Int): StatusBarNotification? =
        runCatching { context.getSystemService(NotificationManager::class.java).activeNotifications.firstOrNull { it.id == id } }.getOrNull()

    private fun post(id: Int, notification: android.app.Notification) {
        @Suppress("MissingPermission")
        NotificationManagerCompat.from(context).notify(id, notification)
    }

    private fun time(createdAt: String?): Long = parseInstant(createdAt)?.toEpochMilli() ?: System.currentTimeMillis()

    fun openTerminalIntent(macId: String, workspaceId: String?, surfaceId: String?, requestCode: Int): PendingIntent =
        PendingIntent.getActivity(
            context,
            requestCode,
            openTerminal(context, macId, workspaceId, surfaceId),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )

    private fun approvalNotificationId(approvalId: String): Int = "approval:$approvalId".hashCode()

    companion object {
        const val APPROVALS_CHANNEL = "approvals"
        const val COMPLETIONS_CHANNEL = "completions"
        const val ALERTS_CHANNEL = "alerts"
        private const val LEGACY_AGENT_CHANNEL = "agents"
        const val ACTIVITY_CHANNEL = "activity"
        private const val LEGACY_CONNECTION_CHANNEL = "connection"
        const val ACTION_OPEN_TERMINAL = "com.gantoine.muxer.OPEN_TERMINAL"
        const val EXTRA_MAC_ID = "mac_id"
        const val EXTRA_WORKSPACE_ID = "workspace_id"
        const val EXTRA_SURFACE_ID = "surface_id"

        const val OPEN_TERMINAL_ALIAS = "com.gantoine.muxer.OpenTerminalActivity"

        fun openTerminalComponent(context: Context): ComponentName = ComponentName(context.packageName, OPEN_TERMINAL_ALIAS)

        fun openTerminal(context: Context, macId: String, workspaceId: String?, surfaceId: String?): Intent =
            Intent().apply {
                component = openTerminalComponent(context)
                action = ACTION_OPEN_TERMINAL
                flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP
                putExtra(EXTRA_MAC_ID, macId)
                putExtra(EXTRA_WORKSPACE_ID, workspaceId)
                putExtra(EXTRA_SURFACE_ID, surfaceId)
            }
        private const val YOU_KEY = "you"
        private const val APP_KEY = "muxer"
        private const val BUBBLE_HEIGHT_DP = 600

        fun mutableFlag(): Int = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) PendingIntent.FLAG_MUTABLE else 0
    }
}
