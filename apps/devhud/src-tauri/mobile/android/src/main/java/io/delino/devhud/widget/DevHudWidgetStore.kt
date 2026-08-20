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

internal class DevHudWidgetStore(private val context: Context) {
    private val state get() = context.getSharedPreferences(widgetStateStore, Context.MODE_PRIVATE)
    private val secrets get() = context.getSharedPreferences(widgetSecretStore, Context.MODE_PRIVATE)

    fun enabledDeckIds(): List<String> = state.all.keys
        .filter { it.startsWith(configurationPrefix) }
        .map { it.removePrefix(configurationPrefix) }
        .sorted()

    fun enable(configuration: JSONObject, token: String): Boolean {
        val deckId = configuration.getString("deckId")
        val previous = configuration(deckId)
        val encrypted = encrypt(token, deckId)
        if (!secrets.edit().putString(deckId, encrypted).commit()) return false
        val editor = state.edit().putString(configurationPrefix + deckId, configuration.toString())
        if (previous != null && selectionChanged(previous, configuration)) editor.remove(snapshotPrefix + deckId)
        if (editor.commit()) return true
        secrets.edit().remove(deckId).commit()
        return false
    }

    fun replaceSnapshot(snapshot: JSONObject): Boolean {
        val deckId = snapshot.getString("deckId")
        val configuration = configuration(deckId) ?: return false
        if (snapshot.getString("query") != configuration.getString("query")) return false
        return state.edit().putString(snapshotPrefix + deckId, snapshot.toString()).commit()
    }

    fun replaceProfileToken(profileId: String, scopeId: String, token: String): Boolean {
        val configurations = state.all.entries
            .filter { it.key.startsWith(configurationPrefix) }
            .mapNotNull { (key, value) -> json(value as? String)?.let { key.removePrefix(configurationPrefix) to it } }
            .filter { (_, configuration) ->
                configuration.optString("profileId") == profileId && configuration.optString("scopeId") == scopeId
            }
        if (configurations.isEmpty()) return true
        val editor = secrets.edit()
        configurations.forEach { (deckId, _) -> editor.putString(deckId, encrypt(token, deckId)) }
        return editor.commit()
    }

    fun disable(deckId: String): Boolean {
        if (!secrets.edit().remove(deckId).commit()) return false
        val editor = state.edit().remove(configurationPrefix + deckId).remove(snapshotPrefix + deckId)
        state.all.entries.filter { it.key.startsWith(selectionPrefix) && it.value == deckId }.forEach { editor.remove(it.key) }
        return editor.commit()
    }

    fun clear(): Boolean {
        // Always attempt both stores so a state-storage failure cannot leave a
        // widget credential behind during logout or completed deletion.
        val stateCleared = state.edit().clear().commit()
        val secretsCleared = secrets.edit().clear().commit()
        return stateCleared && secretsCleared
    }

    fun configuration(deckId: String): JSONObject? = json(state.getString(configurationPrefix + deckId, null))
    fun snapshot(deckId: String): JSONObject? = json(state.getString(snapshotPrefix + deckId, null))
    fun token(deckId: String): String? = secrets.getString(deckId, null)?.let { decrypt(it, deckId) }

    fun select(appWidgetId: Int, deckId: String): Boolean {
        if (!state.contains(configurationPrefix + deckId)) return false
        return state.edit().putString(selectionPrefix + appWidgetId, deckId).commit()
    }

    fun selectedDeckId(appWidgetId: Int): String? = state.getString(selectionPrefix + appWidgetId, null)
    fun removeSelection(appWidgetId: Int) { state.edit().remove(selectionPrefix + appWidgetId).apply() }

    private fun selectionChanged(left: JSONObject, right: JSONObject): Boolean =
        listOf("query", "profileId", "profileKind", "scopeId").any { left.optString(it) != right.optString(it) }

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
