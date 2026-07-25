package dev.deli.devhud.widget

import android.content.Context
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
import java.util.concurrent.atomic.AtomicBoolean
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

internal class ConfirmedResetCorruptionHandler {
    private val resetInProgress = AtomicBoolean(false)

    val handler =
        ReplaceFileCorruptionHandler<Preferences> { error ->
            if (!resetInProgress.get()) throw error
            emptyPreferences()
        }

    fun beginReset() {
        check(resetInProgress.compareAndSet(false, true))
    }

    fun finishReset() {
        resetInProgress.set(false)
    }
}

class AndroidWidgetSharedDataAdapter internal constructor(
    private val dataStore: DataStore<Preferences>,
    private val corruptionHandler: ConfirmedResetCorruptionHandler? = null,
) {
    companion object {
        @Volatile
        private var liveInstance: AndroidWidgetSharedDataAdapter? = null

        fun live(context: Context): AndroidWidgetSharedDataAdapter =
            liveInstance
                ?: synchronized(this) {
                    liveInstance
                        ?: createLiveAdapter(
                            ConfirmedResetCorruptionHandler(),
                            context.applicationContext,
                        ).also { liveInstance = it }
                }

        private fun createLiveAdapter(
            corruptionHandler: ConfirmedResetCorruptionHandler,
            context: Context,
        ) = AndroidWidgetSharedDataAdapter(
            PreferenceDataStoreFactory.create(
                corruptionHandler = corruptionHandler.handler,
            ) {
                context.preferencesDataStoreFile(
                    DevHudWidgetContract.DATASTORE_NAME,
                )
            },
            corruptionHandler,
        )
    }

    private val configurationKey =
        stringPreferencesKey(DevHudWidgetContract.STORAGE_KEY)
    private val operations = Mutex()

    suspend fun readRecord(): WidgetConfigurationRecord {
        val raw = readRawRecord() ?: return WidgetConfigurationRecord.EMPTY
        return WidgetConfigurationCodec.decode(raw)
    }

    suspend fun readRawRecord(): String? =
        operations.withLock {
            val raw =
                try {
                    dataStore.data.first()[configurationKey]
                } catch (error: CorruptionException) {
                    throw WidgetConfigurationException(
                        WidgetConfigurationErrorCode.CORRUPT,
                        error,
                    )
                } catch (error: IOException) {
                    throw WidgetConfigurationException(
                        WidgetConfigurationErrorCode.STORAGE_UNAVAILABLE,
                        error,
                    )
                }
            if (raw != null) WidgetConfigurationCodec.decode(raw)
            raw
        }

    suspend fun writeRawRecord(raw: String) {
        WidgetConfigurationCodec.decode(raw)
        operations.withLock {
            try {
                dataStore.edit { preferences -> preferences[configurationKey] = raw }
            } catch (error: IOException) {
                throw WidgetConfigurationException(
                    WidgetConfigurationErrorCode.WRITE_FAILED,
                    error,
                )
            }
        }
    }

    suspend fun reset() {
        operations.withLock {
            corruptionHandler?.beginReset()
            try {
                dataStore.edit { preferences -> preferences.remove(configurationKey) }
            } catch (error: IOException) {
                throw WidgetConfigurationException(
                    WidgetConfigurationErrorCode.WRITE_FAILED,
                    error,
                )
            } finally {
                corruptionHandler?.finishReset()
            }
        }
    }
}

fun interface WidgetRefreshing {
    suspend fun refresh(): Int
}

class WidgetConfigurationService(
    private val adapter: AndroidWidgetSharedDataAdapter,
    private val refresher: WidgetRefreshing,
) {
    suspend fun readRawRecord(): String? = adapter.readRawRecord()

    suspend fun writeRawRecord(raw: String): Int {
        adapter.writeRawRecord(raw)
        return refresher.refresh()
    }

    suspend fun reset(): Int {
        adapter.reset()
        return refresher.refresh()
    }
}
