package dev.deli.devhud.widget

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import java.nio.file.Files
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Test

class WidgetConfigurationTest {
    @Test
    fun fixtureRoundTripsThroughTypedDataStoreAdapter() = runTest {
        withAdapter { adapter, _ ->
            val raw =
                requireNotNull(
                    javaClass.classLoader
                        ?.getResourceAsStream("widget-configuration.v1.json"),
                ).bufferedReader().use { it.readText().trim() }

            adapter.writeRawRecord(raw)

            assertEquals(raw, adapter.readRawRecord())
            assertEquals(
                "fixture-diagnostics",
                adapter.readRecord().configuration.slots.first().toolId.value,
            )
        }
    }

    @Test
    fun resetIsIsolatedToWidgetConfiguration() = runTest {
        withAdapter { adapter, dataStore ->
            val otherKey = stringPreferencesKey("devhud.settings.v1")
            dataStore.edit { it[otherKey] = "preserved" }
            adapter.writeRawRecord(
                """{"version":1,"configuration":{"slots":[]}}""",
            )

            adapter.reset()

            assertNull(adapter.readRawRecord())
            assertEquals("preserved", dataStore.data.first()[otherKey])
        }
    }

    @Test
    fun corruptAndFutureRecordsAreRejectedWithoutOverwrite() = runTest {
        withAdapter { adapter, _ ->
            val valid = """{"version":1,"configuration":{"slots":[]}}"""
            adapter.writeRawRecord(valid)

            assertEquals(
                WidgetConfigurationErrorCode.CORRUPT,
                assertThrows(WidgetConfigurationException::class.java) {
                    kotlinx.coroutines.runBlocking {
                        adapter.writeRawRecord("{not-json}")
                    }
                }.code,
            )
            assertEquals(
                WidgetConfigurationErrorCode.FUTURE_VERSION,
                assertThrows(WidgetConfigurationException::class.java) {
                    kotlinx.coroutines.runBlocking {
                        adapter.writeRawRecord(
                            """{"version":2,"configuration":{"slots":[]}}""",
                        )
                    }
                }.code,
            )
            assertEquals(valid, adapter.readRawRecord())
        }
    }

    @Test
    fun refreshFailuresPropagateAfterValidStateIsStored() = runTest {
        withAdapter { adapter, _ ->
            val service =
                WidgetConfigurationService(adapter) {
                    throw WidgetConfigurationException(
                        WidgetConfigurationErrorCode.REFRESH_FAILED,
                    )
                }
            val raw = """{"version":1,"configuration":{"slots":[]}}"""

            val error =
                assertThrows(WidgetConfigurationException::class.java) {
                    kotlinx.coroutines.runBlocking {
                        service.writeRawRecord(raw)
                    }
                }

            assertEquals(WidgetConfigurationErrorCode.REFRESH_FAILED, error.code)
            assertEquals(raw, adapter.readRawRecord())
        }
    }

    private suspend fun withAdapter(
        test: suspend (
            AndroidWidgetSharedDataAdapter,
            androidx.datastore.core.DataStore<
                androidx.datastore.preferences.core.Preferences
            >,
        ) -> Unit,
    ) {
        val directory = Files.createTempDirectory("devhud-widget-test")
        val dataStoreScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val dataStore =
            PreferenceDataStoreFactory.create(scope = dataStoreScope) {
                directory.resolve("widget.preferences_pb").toFile()
            }
        try {
            test(AndroidWidgetSharedDataAdapter(dataStore), dataStore)
        } finally {
            dataStoreScope.cancel()
            directory.toFile().deleteRecursively()
        }
    }
}
