package dev.deli.devhud.widget

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStoreFile
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import java.io.IOException
import kotlinx.coroutines.flow.first

class AndroidWidgetSharedDataAdapter(
    private val dataStore: DataStore<Preferences>,
) {
    companion object {
        @Volatile
        private var liveInstance: AndroidWidgetSharedDataAdapter? = null

        fun live(context: Context): AndroidWidgetSharedDataAdapter =
            liveInstance
                ?: synchronized(this) {
                    liveInstance
                        ?: AndroidWidgetSharedDataAdapter(
                            PreferenceDataStoreFactory.create {
                                context.applicationContext.preferencesDataStoreFile(
                                    DevHudWidgetContract.DATASTORE_FILE,
                                )
                            },
                        ).also { liveInstance = it }
                }
    }

    private val configurationKey =
        stringPreferencesKey(DevHudWidgetContract.STORAGE_KEY)

    suspend fun readRecord(): WidgetConfigurationRecord {
        val raw = readRawRecord() ?: return WidgetConfigurationRecord.EMPTY
        return WidgetConfigurationCodec.decode(raw)
    }

    suspend fun readRawRecord(): String? {
        val raw =
            try {
                dataStore.data.first()[configurationKey]
            } catch (error: IOException) {
                throw WidgetConfigurationException(
                    WidgetConfigurationErrorCode.STORAGE_UNAVAILABLE,
                    error,
                )
            }
        if (raw != null) WidgetConfigurationCodec.decode(raw)
        return raw
    }

    suspend fun writeRawRecord(raw: String) {
        WidgetConfigurationCodec.decode(raw)
        try {
            dataStore.edit { preferences -> preferences[configurationKey] = raw }
        } catch (error: IOException) {
            throw WidgetConfigurationException(
                WidgetConfigurationErrorCode.WRITE_FAILED,
                error,
            )
        }
    }

    suspend fun reset() {
        try {
            dataStore.edit { preferences -> preferences.remove(configurationKey) }
        } catch (error: IOException) {
            throw WidgetConfigurationException(
                WidgetConfigurationErrorCode.WRITE_FAILED,
                error,
            )
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
