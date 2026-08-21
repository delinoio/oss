package io.delino.devhud.widget

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import org.json.JSONObject
import java.security.KeyStore
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

internal const val widgetStateStore = "devhud-widget-state-v1"
private const val widgetSecretStore = "devhud-widget-secret-v1"
private const val widgetKeyAlias = "io.delino.devhud.widget-credential.v1"
private const val configurationPrefix = "configuration:"
private const val snapshotPrefix = "snapshot:"
private const val selectionPrefix = "selection:"
private const val transactionPrefix = "transaction:"
private const val disableTransactionPrefix = "disable-transaction:"

internal sealed class WidgetCredential {
    object Missing : WidgetCredential()
    data class Readable(val token: String, val revision: String) : WidgetCredential()
    data class Unreadable(val revision: String) : WidgetCredential()
}

internal class DevHudWidgetStore(private val context: Context) {
    companion object {
        private val widgetStoreMutationLock = Any()
    }

    private val state get() = context.getSharedPreferences(widgetStateStore, Context.MODE_PRIVATE)
    private val secrets get() = context.getSharedPreferences(widgetSecretStore, Context.MODE_PRIVATE)

    init {
        reconcile()
    }

    fun enabledDeckIds(): List<String> {
        val entries = state.all
        return entries.keys
            .filter { it.startsWith(configurationPrefix) }
            .map { it.removePrefix(configurationPrefix) }
            .filterNot { entries.containsKey(disableTransactionPrefix + it) }
            .sorted()
    }

    fun enable(configuration: JSONObject, token: String): Boolean = synchronized(widgetStoreMutationLock) {
        val deckId = configuration.getString("deckId")
        if (state.contains(disableTransactionPrefix + deckId)) return@synchronized false
        val previous = configuration(deckId)
        val previousSecret = secrets.getString(deckId, null)
        val transactionKey = transactionPrefix + deckId
        if (!state.edit().putBoolean(transactionKey, true).commit()) {
            state.edit().remove(transactionKey).commit()
            return@synchronized false
        }
        val encrypted = try {
            encrypt(token, deckId)
        } catch (error: Exception) {
            abortEnable(deckId, previousSecret)
            throw error
        }
        if (!secrets.edit().putString(deckId, encrypted).commit()) {
            abortEnable(deckId, previousSecret)
            return@synchronized false
        }
        val editor = state.edit().putString(configurationPrefix + deckId, configuration.toString())
        if (previous != null && selectionChanged(previous, configuration)) editor.remove(snapshotPrefix + deckId)
        if (!editor.commit()) {
            abortEnable(deckId, previousSecret)
            return@synchronized false
        }
        state.edit().remove(transactionKey).commit()
    }

    fun replaceSnapshot(snapshot: JSONObject): Boolean = replaceSnapshot(snapshot, null, false)

    fun replaceSnapshot(snapshot: JSONObject, credentialRevision: String?): Boolean =
        replaceSnapshot(snapshot, credentialRevision, true)

    private fun replaceSnapshot(snapshot: JSONObject, credentialRevision: String?, verifyCredential: Boolean): Boolean = synchronized(widgetStoreMutationLock) {
        val deckId = snapshot.getString("deckId")
        val configuration = configuration(deckId) ?: return@synchronized false
        if (snapshot.getString("query") != configuration.getString("query")) return@synchronized false
        if (verifyCredential) {
            if (state.contains(transactionPrefix + deckId) || state.contains(disableTransactionPrefix + deckId)) return@synchronized false
            if (secrets.getString(deckId, null) != credentialRevision) return@synchronized false
        }
        state.edit().putString(snapshotPrefix + deckId, snapshot.toString()).commit()
    }

    fun replaceProfileToken(profileId: String, scopeId: String, token: String): Boolean = synchronized(widgetStoreMutationLock) {
        val configurations = state.all.entries
            .filter { it.key.startsWith(configurationPrefix) }
            .mapNotNull { (key, value) -> json(value as? String)?.let { key.removePrefix(configurationPrefix) to it } }
            .filter { (_, configuration) ->
                configuration.optString("profileId") == profileId && configuration.optString("scopeId") == scopeId
            }
        if (configurations.isEmpty()) return@synchronized true
        val editor = secrets.edit()
        configurations.forEach { (deckId, _) -> editor.putString(deckId, encrypt(token, deckId)) }
        editor.commit()
    }

    fun disable(deckId: String): Boolean = synchronized(widgetStoreMutationLock) {
        val transactionKey = disableTransactionPrefix + deckId
        if (!state.edit().putBoolean(transactionKey, true).commit()) {
            state.edit().remove(transactionKey).commit()
            return@synchronized false
        }
        if (!secrets.edit().remove(deckId).commit()) return@synchronized false
        val editor = state.edit()
            .remove(configurationPrefix + deckId)
            .remove(snapshotPrefix + deckId)
            .remove(transactionPrefix + deckId)
            .remove(transactionKey)
        state.all.entries.filter { it.key.startsWith(selectionPrefix) && it.value == deckId }.forEach { editor.remove(it.key) }
        editor.commit()
    }

    fun clear(): Boolean = synchronized(widgetStoreMutationLock) {
        // Always attempt both stores so a state-storage failure cannot leave a
        // widget credential behind during logout or completed deletion.
        val stateCleared = state.edit().clear().commit()
        val secretsCleared = secrets.edit().clear().commit()
        stateCleared && secretsCleared
    }

    fun configuration(deckId: String): JSONObject? {
        if (state.contains(disableTransactionPrefix + deckId)) return null
        return json(state.getString(configurationPrefix + deckId, null))
    }
    fun snapshot(deckId: String): JSONObject? {
        if (state.contains(disableTransactionPrefix + deckId)) return null
        return json(state.getString(snapshotPrefix + deckId, null))
    }
    fun credential(deckId: String): WidgetCredential? {
        if (state.contains(transactionPrefix + deckId) || state.contains(disableTransactionPrefix + deckId)) return null
        val revision = secrets.getString(deckId, null) ?: return WidgetCredential.Missing
        val token = decrypt(revision, deckId) ?: return WidgetCredential.Unreadable(revision)
        return WidgetCredential.Readable(token, revision)
    }

    fun select(appWidgetId: Int, deckId: String): Boolean {
        if (!state.contains(configurationPrefix + deckId)) return false
        return state.edit().putString(selectionPrefix + appWidgetId, deckId).commit()
    }

    fun selectedDeckId(appWidgetId: Int): String? = state.getString(selectionPrefix + appWidgetId, null)
    fun removeSelection(appWidgetId: Int) { state.edit().remove(selectionPrefix + appWidgetId).apply() }

    private fun selectionChanged(left: JSONObject, right: JSONObject): Boolean =
        listOf("query", "profileId", "profileKind", "scopeId").any { left.optString(it) != right.optString(it) } ||
            left.optJSONArray("repositories")?.toString() != right.optJSONArray("repositories")?.toString()

    private fun reconcile(): Boolean = synchronized(widgetStoreMutationLock) {
        val entries = state.all.entries
        val pendingEnableDeckIds = entries.mapNotNull { (key, _) ->
            key.takeIf { it.startsWith(transactionPrefix) }?.removePrefix(transactionPrefix)
        }.toSet()
        val pendingDisableDeckIds = entries.mapNotNull { (key, _) ->
            key.takeIf { it.startsWith(disableTransactionPrefix) }?.removePrefix(disableTransactionPrefix)
        }.toSet()
        val configuredDeckIds = entries.mapNotNull { (key, value) ->
            if (!key.startsWith(configurationPrefix)) return@mapNotNull null
            val deckId = key.removePrefix(configurationPrefix)
            deckId.takeIf { json(value as? String)?.optString("deckId") == deckId }
        }.toSet()
        val credentialDeckIds = secrets.all.keys
        val removedDeckIds = pendingEnableDeckIds + pendingDisableDeckIds + credentialDeckIds.filterNot(configuredDeckIds::contains)
        if (removedDeckIds.isNotEmpty()) {
            val editor = secrets.edit()
            removedDeckIds.forEach { editor.remove(it) }
            if (!editor.commit()) return@synchronized false
        }
        if (pendingEnableDeckIds.isEmpty() && pendingDisableDeckIds.isEmpty()) return@synchronized true
        val editor = state.edit()
        pendingEnableDeckIds.forEach { editor.remove(transactionPrefix + it) }
        pendingDisableDeckIds.forEach { deckId ->
            editor.remove(configurationPrefix + deckId)
                .remove(snapshotPrefix + deckId)
                .remove(transactionPrefix + deckId)
                .remove(disableTransactionPrefix + deckId)
        }
        entries.filter { entry -> entry.key.startsWith(selectionPrefix) && pendingDisableDeckIds.any { entry.value == it } }
            .forEach { editor.remove(it.key) }
        editor.commit()
    }

    private fun abortEnable(deckId: String, previousSecret: String?): Boolean {
        val rollback = secrets.edit()
        if (previousSecret == null) rollback.remove(deckId) else rollback.putString(deckId, previousSecret)
        if (!rollback.commit()) return false
        return state.edit().remove(transactionPrefix + deckId).commit()
    }

    private fun json(value: String?): JSONObject? = try { value?.let(::JSONObject) } catch (_: Exception) { null }

    private fun secretKey(): SecretKey {
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (keyStore.getKey(widgetKeyAlias, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").run {
            init(KeyGenParameterSpec.Builder(widgetKeyAlias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .build())
            generateKey()
        }
    }

    private fun encrypt(value: String, deckId: String): String {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply {
            init(Cipher.ENCRYPT_MODE, secretKey())
            updateAAD(deckId.toByteArray(Charsets.UTF_8))
        }
        return Base64.getEncoder().encodeToString(cipher.iv + cipher.doFinal(value.toByteArray(Charsets.UTF_8)))
    }

    private fun decrypt(encoded: String, deckId: String): String? = try {
        val payload = Base64.getDecoder().decode(encoded)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply {
            init(Cipher.DECRYPT_MODE, secretKey(), GCMParameterSpec(128, payload.copyOfRange(0, 12)))
            updateAAD(deckId.toByteArray(Charsets.UTF_8))
        }
        String(cipher.doFinal(payload.copyOfRange(12, payload.size)), Charsets.UTF_8)
    } catch (_: Exception) { null }
}
