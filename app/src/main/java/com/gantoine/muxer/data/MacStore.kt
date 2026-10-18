package com.gantoine.muxer.data

import android.content.Context
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.floatPreferencesKey
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull

@Serializable
data class Mac(
    val id: String,
    val name: String,
    val url: String,
    val encryptedToken: String,
    val backgroundNotifications: Boolean = false,
    val completionNotifications: Boolean = true,
    val showActivity: Boolean = true,
)

data class Settings(val fontSize: Float, val fitToPhone: Boolean = true)

private val Context.dataStore by preferencesDataStore(name = "cmux")

class MacStore(private val context: Context, scope: CoroutineScope, private val cipher: TokenCipher = TokenCipher()) {
    private val json = Json { ignoreUnknownKeys = true }
    private val serializer = ListSerializer(Mac.serializer())

    val macs: Flow<List<Mac>> = context.dataStore.data
        .map { decodeMacs(it) }
        .distinctUntilChanged()
        .stateIn(scope, SharingStarted.Eagerly, null)
        .filterNotNull()

    val settings: Flow<Settings> = context.dataStore.data.map {
        Settings(
            fontSize = it[FONT_SIZE] ?: DEFAULT_FONT_SIZE,
            fitToPhone = it[FIT_TO_PHONE] ?: true,
        )
    }

    fun token(mac: Mac): String? = cipher.decrypt(mac.encryptedToken)

    suspend fun add(id: String, name: String, url: String, token: String, notificationSeq: Long) {
        context.dataStore.edit { preferences ->
            val macs = decodeMacs(preferences)
            val replaced = macs.filter { it.id == id || it.url == url }
            val background = replaced.any { it.backgroundNotifications }
            val completions = replaced.all { it.completionNotifications }
            val activity = replaced.all { it.showActivity }
            val added = Mac(id, Names.possessive(name), url, cipher.encrypt(token), background, completions, activity)
            preferences[MACS] = json.encodeToString(serializer, macs - replaced.toSet() + added)
            replaced.forEach { preferences.remove(seqKey(it.id)) }
            preferences[seqKey(id)] = notificationSeq
        }
    }

    suspend fun setBackgroundNotifications(id: String, enabled: Boolean) = updateMac(id) { copy(backgroundNotifications = enabled) }

    suspend fun setCompletionNotifications(id: String, enabled: Boolean) = updateMac(id) { copy(completionNotifications = enabled) }

    suspend fun setShowActivity(id: String, enabled: Boolean) = updateMac(id) { copy(showActivity = enabled) }

    suspend fun migrate() {
        context.dataStore.edit { preferences ->
            legacyNotificationSeqs(preferences).forEach { (id, seq) ->
                if (preferences[seqKey(id)] == null) preferences[seqKey(id)] = seq
            }
            if (preferences[BACKGROUND_NOTIFICATIONS] == true) {
                val migrated = decodeMacs(preferences).map { it.copy(backgroundNotifications = true) }
                preferences[MACS] = json.encodeToString(serializer, migrated)
            }
            preferences.remove(BACKGROUND_NOTIFICATIONS)
        }
    }

    private fun legacyNotificationSeqs(preferences: Preferences): Map<String, Long> =
        preferences[MACS]?.let { encoded ->
            runCatching { json.decodeFromString(ListSerializer(JsonObject.serializer()), encoded) }.getOrNull()
        }.orEmpty().mapNotNull { mac ->
            val id = mac["id"]?.jsonPrimitive?.content ?: return@mapNotNull null
            val seq = mac["lastNotificationSeq"]?.jsonPrimitive?.longOrNull ?: return@mapNotNull null
            id to seq
        }.toMap()

    suspend fun remove(id: String) {
        update { macs -> macs.filterNot { it.id == id } }
        context.dataStore.edit { it.remove(seqKey(id)) }
    }

    suspend fun notificationSeq(id: String): Long = context.dataStore.data.first()[seqKey(id)] ?: 0L

    suspend fun recordNotificationSeq(id: String, seq: Long) {
        context.dataStore.edit { preferences ->
            if (seq > (preferences[seqKey(id)] ?: 0L)) preferences[seqKey(id)] = seq
        }
    }

    suspend fun rename(id: String, name: String) = update { macs ->
        val fixed = Names.possessive(name)
        macs.map { if (it.id == id && it.name != fixed) it.copy(name = fixed) else it }
    }

    suspend fun current(): List<Mac> = macs.first()

    suspend fun find(id: String): Mac? = current().firstOrNull { it.id == id }

    suspend fun setFontSize(size: Float) {
        context.dataStore.edit { it[FONT_SIZE] = size }
    }

    suspend fun setFitToPhone(enabled: Boolean) {
        context.dataStore.edit { it[FIT_TO_PHONE] = enabled }
    }

    private suspend fun updateMac(id: String, transform: Mac.() -> Mac) = update { macs ->
        macs.map { if (it.id == id) it.transform() else it }
    }

    private suspend fun update(transform: (List<Mac>) -> List<Mac>) {
        context.dataStore.edit { preferences ->
            preferences[MACS] = json.encodeToString(serializer, transform(decodeMacs(preferences)))
        }
    }

    private fun decodeMacs(preferences: Preferences): List<Mac> =
        preferences[MACS]?.let { runCatching { json.decodeFromString(serializer, it) }.getOrNull() }.orEmpty()

    private fun seqKey(id: String) = longPreferencesKey("notification_seq:$id")

    companion object {
        const val DEFAULT_FONT_SIZE = 11f
        private val MACS = stringPreferencesKey("macs")
        private val BACKGROUND_NOTIFICATIONS = booleanPreferencesKey("background_notifications")
        private val FONT_SIZE = floatPreferencesKey("font_size")
        private val FIT_TO_PHONE = booleanPreferencesKey("fit_to_phone")
    }
}
