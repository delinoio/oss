package dev.deli.devhud.widget

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import androidx.datastore.core.CorruptionException
import androidx.datastore.core.DataStore
import androidx.datastore.core.handlers.ReplaceFileCorruptionHandler
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.emptyPreferences
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStoreFile
import java.io.IOException
import java.nio.charset.StandardCharsets
import java.security.KeyStore
import java.security.MessageDigest
import java.util.Base64
import java.util.concurrent.atomic.AtomicBoolean
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.json.JSONObject

internal class ConfirmedResetCorruptionHandler {
    private val resetInProgress = AtomicBoolean(false)
    val handler = ReplaceFileCorruptionHandler<Preferences> { error ->
        if (!resetInProgress.get()) throw error
        emptyPreferences()
    }
    fun beginReset() { check(resetInProgress.compareAndSet(false, true)) }
    fun finishReset() { resetInProgress.set(false) }
}

interface WidgetRecordEncryptor {
    fun encrypt(plaintext: String, accountId: String): String
    fun decrypt(envelope: String): String
    fun reset(envelope: String?)
}

/** AES-256-GCM with an account-bound Android Keystore key. */
class AndroidKeystoreWidgetRecordEncryptor : WidgetRecordEncryptor {
    private val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }

    override fun encrypt(plaintext: String, accountId: String): String {
        try {
            val binding = binding(accountId)
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.ENCRYPT_MODE, key(binding))
            cipher.updateAAD(binding.toByteArray(StandardCharsets.UTF_8))
            val ciphertext = cipher.doFinal(plaintext.toByteArray(StandardCharsets.UTF_8))
            return JSONObject()
                .put("version", 1)
                .put("accountBinding", binding)
                .put("iv", Base64.getEncoder().encodeToString(cipher.iv))
                .put("ciphertext", Base64.getEncoder().encodeToString(ciphertext))
                .toString()
        } catch (error: WidgetConfigurationException) {
            throw error
        } catch (error: Exception) {
            throw WidgetConfigurationException(WidgetConfigurationErrorCode.ENCRYPTION_FAILED, error)
        }
    }

    override fun decrypt(envelope: String): String {
        try {
            val value = JSONObject(envelope)
            if (value.keys().asSequence().toSet() != setOf("version", "accountBinding", "iv", "ciphertext") ||
                value.optInt("version", 0) != 1) corrupt()
            val binding = value.getString("accountBinding")
            val secret = keyStore.getKey(alias(binding), null) as? SecretKey ?: corrupt()
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.DECRYPT_MODE, secret, GCMParameterSpec(128, Base64.getDecoder().decode(value.getString("iv"))))
            cipher.updateAAD(binding.toByteArray(StandardCharsets.UTF_8))
            return String(cipher.doFinal(Base64.getDecoder().decode(value.getString("ciphertext"))), StandardCharsets.UTF_8)
        } catch (error: WidgetConfigurationException) {
            throw error
        } catch (error: Exception) {
            throw WidgetConfigurationException(WidgetConfigurationErrorCode.CORRUPT, error)
        }
    }

    override fun reset(envelope: String?) {
        if (envelope == null) return
        try {
            val binding = JSONObject(envelope).optString("accountBinding")
            if (binding.isNotEmpty()) keyStore.deleteEntry(alias(binding))
        } catch (_: Exception) {
            // Reset must still remove an unreadable ciphertext record.
        }
    }

    private fun key(binding: String): SecretKey {
        (keyStore.getKey(alias(binding), null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").run {
            init(KeyGenParameterSpec.Builder(
                alias(binding),
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            ).setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .setRandomizedEncryptionRequired(true)
                .build())
            generateKey()
        }
    }

    private fun alias(binding: String) = DevHudWidgetContract.KEY_ALIAS_PREFIX + binding
    private fun binding(accountId: String) = MessageDigest.getInstance("SHA-256")
        .digest(accountId.toByteArray(StandardCharsets.UTF_8)).joinToString("") { "%02x".format(it) }
    private fun corrupt(): Nothing = throw WidgetConfigurationException(WidgetConfigurationErrorCode.CORRUPT)
}

class IdentityWidgetRecordEncryptor : WidgetRecordEncryptor {
    override fun encrypt(plaintext: String, accountId: String) = plaintext
    override fun decrypt(envelope: String) = envelope
    override fun reset(envelope: String?) = Unit
}

class AndroidWidgetSharedDataAdapter internal constructor(
    private val dataStore: DataStore<Preferences>,
    private val corruptionHandler: ConfirmedResetCorruptionHandler? = null,
    private val encryptor: WidgetRecordEncryptor,
) {
    companion object {
        @Volatile private var liveInstance: AndroidWidgetSharedDataAdapter? = null
        fun live(context: Context): AndroidWidgetSharedDataAdapter = liveInstance ?: synchronized(this) {
            liveInstance ?: createLiveAdapter(ConfirmedResetCorruptionHandler(), context.applicationContext)
                .also { liveInstance = it }
        }
        private fun createLiveAdapter(handler: ConfirmedResetCorruptionHandler, context: Context) =
            AndroidWidgetSharedDataAdapter(
                PreferenceDataStoreFactory.create(corruptionHandler = handler.handler) {
                    context.preferencesDataStoreFile(DevHudWidgetContract.DATASTORE_NAME)
                },
                handler,
                AndroidKeystoreWidgetRecordEncryptor(),
            )
    }

    private val configurationKey = stringPreferencesKey(DevHudWidgetContract.STORAGE_KEY)
    private val operations = Mutex()

    suspend fun readRecord(): WidgetConfigurationRecord =
        readRawRecord()?.let(WidgetConfigurationCodec::decode) ?: WidgetConfigurationRecord.EMPTY

    suspend fun readRawRecord(): String? = operations.withLock {
        val envelope = try { dataStore.data.first()[configurationKey] }
        catch (error: CorruptionException) { throw WidgetConfigurationException(WidgetConfigurationErrorCode.CORRUPT, error) }
        catch (error: IOException) { throw WidgetConfigurationException(WidgetConfigurationErrorCode.STORAGE_UNAVAILABLE, error) }
        envelope?.let(encryptor::decrypt)?.also(WidgetConfigurationCodec::decode)
    }

    suspend fun writeRawRecord(raw: String) {
        val record = WidgetConfigurationCodec.decode(raw)
        operations.withLock {
            val previousEnvelope = try { dataStore.data.first()[configurationKey] }
            catch (error: CorruptionException) { throw WidgetConfigurationException(WidgetConfigurationErrorCode.CORRUPT, error) }
            catch (error: IOException) { throw WidgetConfigurationException(WidgetConfigurationErrorCode.STORAGE_UNAVAILABLE, error) }
            val previousAccountId = previousEnvelope?.let {
                WidgetConfigurationCodec.decode(encryptor.decrypt(it)).configuration.accountId
            }
            val envelope = encryptor.encrypt(raw, record.configuration.accountId)
            try {
                dataStore.edit { it[configurationKey] = envelope }
            } catch (error: IOException) {
                if (previousAccountId != record.configuration.accountId) encryptor.reset(envelope)
                throw WidgetConfigurationException(WidgetConfigurationErrorCode.WRITE_FAILED, error)
            }
            if (previousAccountId != null && previousAccountId != record.configuration.accountId) {
                encryptor.reset(previousEnvelope)
            }
        }
    }

    suspend fun reset() = operations.withLock {
        corruptionHandler?.beginReset()
        try {
            val envelope = try { dataStore.data.first()[configurationKey] } catch (_: CorruptionException) { null }
            encryptor.reset(envelope)
            dataStore.edit { it.remove(configurationKey) }
        } catch (error: IOException) {
            throw WidgetConfigurationException(WidgetConfigurationErrorCode.WRITE_FAILED, error)
        } finally { corruptionHandler?.finishReset() }
    }
}

fun interface WidgetRefreshing { suspend fun refresh(): Int }

class WidgetConfigurationService(
    private val adapter: AndroidWidgetSharedDataAdapter,
    private val refresher: WidgetRefreshing,
) {
    suspend fun readRawRecord() = adapter.readRawRecord()
    suspend fun writeRawRecord(raw: String): Int { adapter.writeRawRecord(raw); return refresher.refresh() }
    suspend fun reset(): Int { adapter.reset(); return refresher.refresh() }
}
