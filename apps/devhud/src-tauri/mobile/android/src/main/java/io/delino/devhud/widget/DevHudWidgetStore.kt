package io.delino.devhud.widget

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import org.json.JSONArray
import org.json.JSONObject
import java.security.KeyStore
import java.time.Instant
import java.util.Base64
import javax.crypto.AEADBadTagException
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

internal const val widgetStateStore = "devhud-widget-state-v1"
private const val widgetSecretStore = "devhud-widget-secret-v1"
private const val widgetKeyAlias = "io.delino.devhud.widget-credential.v1"
private const val mainSecretStore = "devhud-secure-settings-v1"
private const val mainKeyAlias = "io.delino.devhud.secure-settings.v1"
private const val configurationPrefix = "configuration:"
private const val snapshotPrefix = "snapshot:"
private const val selectionPrefix = "selection:"
private const val transactionPrefix = "transaction:"
private const val disableTransactionPrefix = "disable-transaction:"
private const val credentialReplacementKey = "credential-replacement:v1"

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
    private var reconciliationSucceeded = false

    init {
        reconciliationSucceeded = reconcile()
    }

    fun enabledDeckIds(): List<String>? = synchronized(widgetStoreMutationLock) {
        if (!ensureReconciled()) return@synchronized null
        val entries = state.all
        entries.keys
            .filter { it.startsWith(configurationPrefix) }
            .map { it.removePrefix(configurationPrefix) }
            .filterNot { entries.containsKey(disableTransactionPrefix + it) }
            .sorted()
    }

    fun enable(configuration: JSONObject, token: String): Boolean = synchronized(widgetStoreMutationLock) {
        if (!ensureReconciled()) return@synchronized false
        val deckId = configuration.getString("deckId")
        if (state.contains(disableTransactionPrefix + deckId)) return@synchronized false
        val previous = configuration(deckId)
        val previousSecret = secrets.getString(deckId, null)
        val reusableSecret = previousSecret?.takeIf { decrypt(it, deckId) == token }
        if (previous != null && reusableSecret != null && sameConfiguration(previous, configuration)) return@synchronized true
        val transactionKey = transactionPrefix + deckId
        if (!state.edit().putBoolean(transactionKey, true).commit()) {
            state.edit().remove(transactionKey).commit()
            return@synchronized false
        }
        if (reusableSecret == null) {
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
        if (!ensureReconciled()) return@synchronized false
        val deckId = snapshot.getString("deckId")
        val configuration = configuration(deckId) ?: return@synchronized false
        if (snapshot.getString("query") != configuration.getString("query")) return@synchronized false
        if (verifyCredential) {
            if (state.contains(transactionPrefix + deckId) || state.contains(disableTransactionPrefix + deckId) || credentialReplacementBlocks(deckId)) return@synchronized false
            if (secrets.getString(deckId, null) != credentialRevision) return@synchronized false
        }
        val current = this.snapshot(deckId)
        val merged = mergeSnapshot(current, snapshot)
        if (current != null && merged.attempt === current && merged.success === current) return@synchronized true
        state.edit().putString(snapshotPrefix + deckId, merged.snapshot.toString()).commit()
    }

    private data class MergedSnapshot(val snapshot: JSONObject, val attempt: JSONObject, val success: JSONObject)

    private fun mergeSnapshot(current: JSONObject?, incoming: JSONObject): MergedSnapshot {
        if (current == null || current.optString("deckId") != incoming.optString("deckId") || current.optString("query") != incoming.optString("query")) return MergedSnapshot(incoming, incoming, incoming)
        val currentAttempt = timestamp(current, "lastAttemptedAt") ?: return MergedSnapshot(incoming, incoming, incoming)
        val incomingAttempt = timestamp(incoming, "lastAttemptedAt") ?: return MergedSnapshot(current, current, current)
        val now = Instant.now()
        val attempt = if (incomingTimestampIsNewer(currentAttempt, incomingAttempt, now)) incoming else current
        val currentSuccess = timestamp(current, "lastSuccessfulAt")
        val incomingSuccess = timestamp(incoming, "lastSuccessfulAt")
        val success = when {
            incomingSuccess == null -> current
            currentSuccess == null || incomingTimestampIsNewer(currentSuccess, incomingSuccess, now) -> incoming
            else -> current
        }
        val merged = JSONObject(incoming.toString())
            .put("counts", success.getJSONObject("counts"))
            .put("results", success.getJSONArray("results"))
            .put("lastSuccessfulAt", success.opt("lastSuccessfulAt") ?: JSONObject.NULL)
            .put("state", attempt.getString("state"))
            .put("lastAttemptedAt", attempt.getString("lastAttemptedAt"))
            .put("rate", attempt.opt("rate") ?: JSONObject.NULL)
        return MergedSnapshot(merged, attempt, success)
    }

    private fun incomingTimestampIsNewer(current: Instant, incoming: Instant, now: Instant): Boolean {
        // After a backward clock correction, prefer the post-correction side until both timestamps share the same time basis.
        val currentIsFuture = current.isAfter(now)
        val incomingIsFuture = incoming.isAfter(now)
        if (currentIsFuture != incomingIsFuture) return currentIsFuture
        return incoming.isAfter(current)
    }

    private fun timestamp(snapshot: JSONObject, key: String): Instant? {
        val value = snapshot.optString(key).takeIf { it.isNotBlank() && it != "null" } ?: return null
        return try { Instant.parse(value) } catch (_: Exception) { null }
    }

    fun beginProfileTokenReplacement(profileId: String, scopeId: String): Boolean = synchronized(widgetStoreMutationLock) {
        if (!ensureReconciled()) return@synchronized false
        val deckIds = state.all.entries
            .filter { it.key.startsWith(configurationPrefix) }
            .mapNotNull { (key, value) -> json(value as? String)?.let { key.removePrefix(configurationPrefix) to it } }
            .filter { (_, configuration) ->
                configuration.optString("profileId") == profileId && configuration.optString("scopeId") == scopeId
            }
            .map { (deckId, _) -> deckId }
            .sorted()
        if (deckIds.isEmpty()) return@synchronized true
        val transaction = JSONObject()
            .put("version", 1)
            .put("profileId", profileId)
            .put("scopeId", scopeId)
            .put("deckIds", org.json.JSONArray(deckIds))
        state.edit().putString(credentialReplacementKey, transaction.toString()).commit()
    }

    fun replaceProfileToken(profileId: String, scopeId: String, token: String?): Boolean = synchronized(widgetStoreMutationLock) {
        val transaction = credentialReplacement() ?: return@synchronized !state.contains(credentialReplacementKey)
        if (transaction.optString("profileId") != profileId || transaction.optString("scopeId") != scopeId) return@synchronized false
        applyProfileTokenReplacement(transaction, token)
    }

    fun cancelProfileTokenReplacement(): Boolean = synchronized(widgetStoreMutationLock) {
        state.edit().remove(credentialReplacementKey).commit()
    }

    fun disable(deckId: String): Boolean = synchronized(widgetStoreMutationLock) {
        if (!ensureReconciled()) return@synchronized false
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
        reconciliationSucceeded = stateCleared && secretsCleared
        reconciliationSucceeded
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
        if (state.contains(transactionPrefix + deckId) || state.contains(disableTransactionPrefix + deckId) || credentialReplacementBlocks(deckId)) return null
        val revision = secrets.getString(deckId, null) ?: return WidgetCredential.Missing
        val token = decrypt(revision, deckId) ?: return WidgetCredential.Unreadable(revision)
        return WidgetCredential.Readable(token, revision)
    }

    fun select(appWidgetId: Int, deckId: String): Boolean = synchronized(widgetStoreMutationLock) {
        if (!ensureReconciled()) return@synchronized false
        if (!state.contains(configurationPrefix + deckId)) return@synchronized false
        state.edit().putString(selectionPrefix + appWidgetId, deckId).commit()
    }

    fun selectedDeckId(appWidgetId: Int): String? = state.getString(selectionPrefix + appWidgetId, null)
    fun removeSelection(appWidgetId: Int) { state.edit().remove(selectionPrefix + appWidgetId).apply() }

    private fun selectionChanged(left: JSONObject, right: JSONObject): Boolean =
        listOf("query", "profileId", "profileKind", "scopeId").any { left.optString(it) != right.optString(it) } ||
            left.optJSONArray("repositories")?.toString() != right.optJSONArray("repositories")?.toString()

    private fun sameConfiguration(left: JSONObject, right: JSONObject): Boolean =
        left.optInt("version", -1) == right.optInt("version", -1) &&
            listOf("deckId", "name", "query", "profileId", "profileKind", "scopeId", "language")
                .all { left.optString(it) == right.optString(it) } &&
            sameRepositories(left.optJSONArray("repositories"), right.optJSONArray("repositories"))

    private fun sameRepositories(left: JSONArray?, right: JSONArray?): Boolean {
        if (left == null || right == null || left.length() != right.length()) return false
        for (index in 0 until left.length()) {
            val leftRepository = left.optJSONObject(index) ?: return false
            val rightRepository = right.optJSONObject(index) ?: return false
            if (listOf("owner", "name").any { leftRepository.optString(it) != rightRepository.optString(it) }) return false
        }
        return true
    }

    private fun reconcile(): Boolean = synchronized(widgetStoreMutationLock) {
        if (!reconcileProfileTokenReplacement()) return@synchronized false
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

    private fun ensureReconciled(): Boolean {
        if (reconciliationSucceeded) return true
        reconciliationSucceeded = reconcile()
        return reconciliationSucceeded
    }

    private fun reconcileProfileTokenReplacement(): Boolean {
        if (!state.contains(credentialReplacementKey)) return true
        val transaction = credentialReplacement() ?: return false
        val profileId = transaction.optString("profileId")
        val scopeId = transaction.optString("scopeId")
        if (profileId.isBlank() || scopeId.isBlank()) return false
        val preferences = context.getSharedPreferences(mainSecretStore, Context.MODE_PRIVATE)
        val marker = "github-pat-scope:$scopeId:$profileId"
        val encoded = preferences.getString("github-pat:$profileId", null)
        val token = if (!preferences.contains(marker) || encoded == null) null else {
            val mainKey = (try { mainSecretKey() } catch (_: Exception) { return false }) ?: return false
            try {
                decryptMainSecure(encoded, "github-pat:$profileId", mainKey, authenticateKey = true)
            } catch (_: AEADBadTagException) {
                try {
                    decryptMainSecure(encoded, "github-pat:$profileId", mainKey, authenticateKey = false)
                } catch (_: Exception) {
                    return false
                }
            } catch (_: Exception) {
                return false
            }
        }
        return applyProfileTokenReplacement(transaction, token)
    }

    private fun applyProfileTokenReplacement(transaction: JSONObject, token: String?): Boolean {
        if (transaction.optInt("version") != 1) return false
        val profileId = transaction.optString("profileId")
        val scopeId = transaction.optString("scopeId")
        val deckIds = transaction.optJSONArray("deckIds") ?: return false
        val editor = secrets.edit()
        for (index in 0 until deckIds.length()) {
            val deckId = deckIds.optString(index)
            if (deckId.isBlank()) return false
            val configuration = configuration(deckId) ?: continue
            if (configuration.optString("profileId") != profileId || configuration.optString("scopeId") != scopeId) continue
            if (token == null) editor.remove(deckId) else {
                val encrypted = try { encrypt(token, deckId) } catch (_: Exception) { return false }
                editor.putString(deckId, encrypted)
            }
        }
        if (!editor.commit()) return false
        return state.edit().remove(credentialReplacementKey).commit()
    }

    private fun credentialReplacement(): JSONObject? = json(state.getString(credentialReplacementKey, null))

    private fun credentialReplacementBlocks(deckId: String): Boolean {
        if (!state.contains(credentialReplacementKey)) return false
        val deckIds = credentialReplacement()?.takeIf { it.optInt("version") == 1 }?.optJSONArray("deckIds") ?: return true
        return (0 until deckIds.length()).any { deckIds.optString(it) == deckId }
    }

    private fun abortEnable(deckId: String, previousSecret: String?): Boolean {
        val rollback = secrets.edit()
        if (previousSecret == null) rollback.remove(deckId) else rollback.putString(deckId, previousSecret)
        if (!rollback.commit()) return false
        return state.edit().remove(transactionPrefix + deckId).commit()
    }

    private fun json(value: String?): JSONObject? = try { value?.let(::JSONObject) } catch (_: Exception) { null }

    private fun mainSecretKey(): SecretKey? {
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        return keyStore.getKey(mainKeyAlias, null) as? SecretKey
    }

    private fun decryptMainSecure(encoded: String, key: String, secretKey: SecretKey, authenticateKey: Boolean): String {
        val payload = Base64.getDecoder().decode(encoded)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply {
            init(Cipher.DECRYPT_MODE, secretKey, GCMParameterSpec(128, payload.copyOfRange(0, 12)))
            if (authenticateKey) updateAAD(key.toByteArray(Charsets.UTF_8))
        }
        return String(cipher.doFinal(payload.copyOfRange(12, payload.size)), Charsets.UTF_8)
    }

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
