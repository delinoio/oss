package dev.deli.devhud.widget

import android.appwidget.AppWidgetManager
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
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class WidgetConfigurationTest {
    @Test fun configurationRequestRequiresSystemActionAndOwnedWidgetId() {
        val applicationPackage = "dev.deli.devhud"
        val providerClass = DevHudWidgetProvider::class.java.name
        assertTrue(isDevHudWidgetConfigurationRequest(
            AppWidgetManager.ACTION_APPWIDGET_CONFIGURE,
            42,
            applicationPackage,
            providerClass,
            applicationPackage,
        ))
        assertFalse(isDevHudWidgetConfigurationRequest(
            null,
            42,
            applicationPackage,
            providerClass,
            applicationPackage,
        ))
        assertFalse(isDevHudWidgetConfigurationRequest(
            AppWidgetManager.ACTION_APPWIDGET_CONFIGURE,
            AppWidgetManager.INVALID_APPWIDGET_ID,
            applicationPackage,
            providerClass,
            applicationPackage,
        ))
        assertFalse(isDevHudWidgetConfigurationRequest(
            AppWidgetManager.ACTION_APPWIDGET_CONFIGURE,
            42,
            "example.attacker",
            "example.attacker.WidgetProvider",
            applicationPackage,
        ))
    }

    @Test fun dataStoreNameResolvesToContractedFile() {
        assertEquals("devhud-widget-state.preferences_pb", DevHudWidgetContract.DATASTORE_FILE)
    }

    @Test fun fixtureRoundTripsAndPreservesMinimalSnapshot() = runTest {
        withAdapter { adapter, _ ->
            val raw = fixture()
            adapter.writeRawRecord(raw)
            assertEquals(raw, adapter.readRawRecord())
            val widget = adapter.readRecord().configuration.widgets.single()
            assertEquals(DeckWidgetFamily.APPLE_MEDIUM, widget.family)
            assertEquals(2, widget.snapshot.matchingCount)
            assertEquals(DeckWidgetFreshness.STALE, widget.snapshot.freshness)
        }
    }

    @Test fun countsOnlyRejectsRepositoryAndTitles() {
        val raw = fixture().replace("\"privacy\":\"repository-and-titles\"", "\"privacy\":\"counts-only\"")
        assertEquals(
            WidgetConfigurationErrorCode.INCOMPATIBLE,
            assertThrows(WidgetConfigurationException::class.java) { WidgetConfigurationCodec.decode(raw) }.code,
        )
    }

    @Test fun actionsOnlyOpenOrRefreshAndNeverMutate() {
        val view = "018f0000-0000-7000-8000-000000000003"
        val links = listOf(
            DeckWidgetAction.OpenView(view),
            DeckWidgetAction.OpenPullRequest(view, "acme", "widgets", 42),
            DeckWidgetAction.Refresh(view),
        ).map(DeckWidgetAction::toAppLink)
        links.forEach { assertTrue(it.startsWith("https://deli.dev/devhud/deck/open?")) }
        assertFalse(links.joinToString().contains("merge"))
        assertFalse(links.joinToString().contains("mutation"))
        assertFalse(links.joinToString().contains("close"))
    }

    @Test fun notificationsRequireOpaquePayloadAndDefaultExactly() {
        assertEquals("opaque_event_123456", DeckNotificationPolicy.eventId(mapOf("eventId" to "opaque_event_123456")))
        assertNull(DeckNotificationPolicy.eventId(mapOf("eventId" to "opaque_event_123456", "title" to "private")))
        assertEquals("Deck view updated", DeckNotificationPolicy.text("private", false))
    }

    @Test fun resetIsIsolatedAndIdempotent() = runTest {
        withAdapter { adapter, dataStore ->
            val otherKey = stringPreferencesKey("devhud.settings.v1")
            dataStore.edit { it[otherKey] = "preserved" }
            adapter.writeRawRecord(fixture())
            adapter.reset(); adapter.reset()
            assertNull(adapter.readRawRecord())
            assertEquals("preserved", dataStore.data.first()[otherKey])
        }
    }

    @Test fun corruptAndFutureRecordsDoNotOverwrite() = runTest {
        withAdapter { adapter, _ ->
            val valid = fixture()
            adapter.writeRawRecord(valid)
            assertEquals(WidgetConfigurationErrorCode.CORRUPT, assertThrows(WidgetConfigurationException::class.java) {
                kotlinx.coroutines.runBlocking { adapter.writeRawRecord("{not-json}") }
            }.code)
            assertEquals(WidgetConfigurationErrorCode.FUTURE_VERSION, assertThrows(WidgetConfigurationException::class.java) {
                kotlinx.coroutines.runBlocking { adapter.writeRawRecord(valid.replace("\"version\":1", "\"version\":2")) }
            }.code)
            assertEquals(valid, adapter.readRawRecord())
        }
    }

    @Test fun encryptionBoundaryStoresNoSnapshotPlaintext() = runTest {
        val cipher = RecordingCipher()
        withAdapter(cipher) { adapter, dataStore ->
            adapter.writeRawRecord(fixture())
            val stored = dataStore.data.first()[stringPreferencesKey(DevHudWidgetContract.STORAGE_KEY)]!!
            assertFalse(stored.contains("Keep snapshot minimal"))
            assertEquals(fixture(), adapter.readRawRecord())
            assertEquals("018f0000-0000-7000-8000-000000000001", cipher.accountId)
        }
    }

    @Test fun refreshFailurePropagatesAfterStorage() = runTest {
        withAdapter { adapter, _ ->
            val service = WidgetConfigurationService(adapter) {
                throw WidgetConfigurationException(WidgetConfigurationErrorCode.REFRESH_FAILED)
            }
            assertEquals(WidgetConfigurationErrorCode.REFRESH_FAILED, assertThrows(WidgetConfigurationException::class.java) {
                kotlinx.coroutines.runBlocking { service.writeRawRecord(fixture()) }
            }.code)
            assertEquals(fixture(), adapter.readRawRecord())
        }
    }

    private class RecordingCipher : WidgetRecordEncryptor {
        var accountId: String? = null
        private var plaintext = ""
        override fun encrypt(plaintext: String, accountId: String): String {
            this.plaintext = plaintext; this.accountId = accountId; return "ciphertext-only"
        }
        override fun decrypt(envelope: String) = plaintext
        override fun reset(envelope: String?) = Unit
    }

    private fun fixture(): String = requireNotNull(
        javaClass.classLoader?.getResourceAsStream("widget-configuration.v1.json"),
    ).bufferedReader().use { it.readText().trim() }

    private suspend fun withAdapter(
        cipher: WidgetRecordEncryptor = IdentityWidgetRecordEncryptor(),
        test: suspend (AndroidWidgetSharedDataAdapter, androidx.datastore.core.DataStore<androidx.datastore.preferences.core.Preferences>) -> Unit,
    ) {
        val directory = Files.createTempDirectory("devhud-widget-test")
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val store = PreferenceDataStoreFactory.create(scope = scope) { directory.resolve("widget.preferences_pb").toFile() }
        try { test(AndroidWidgetSharedDataAdapter(store, encryptor = cipher), store) }
        finally { scope.cancel(); directory.toFile().deleteRecursively() }
    }
}
