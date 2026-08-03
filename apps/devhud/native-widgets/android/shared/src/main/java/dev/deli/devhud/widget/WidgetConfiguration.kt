package dev.deli.devhud.widget

import java.net.URLEncoder
import java.nio.charset.StandardCharsets
import java.time.Instant
import java.util.UUID
import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject

object DevHudWidgetContract {
    const val EXTENSION_IDENTIFIER = "dev.deli.devhud.widget"
    const val STORAGE_KEY = "devhud.widget-configuration.v1"
    const val DATASTORE_NAME = "devhud-widget-state"
    const val DATASTORE_FILE = "$DATASTORE_NAME.preferences_pb"
    const val KEY_ALIAS_PREFIX = "devhud.deck-widget.snapshot.v1."
    const val SCHEMA_VERSION = 1
    const val MAX_WIDGETS = 20
    const val MAX_PULL_REQUESTS = 10
    const val GENERIC_NOTIFICATION_TEXT = "Deck view updated"
    const val APP_LINK_PATH = "/devhud/deck/open"
}

enum class DeckWidgetFamily(val wireValue: String) {
    APPLE_SMALL("apple-small"), APPLE_MEDIUM("apple-medium"), APPLE_LARGE("apple-large"),
    ANDROID_COMPACT("android-compact"), ANDROID_WIDE("android-wide"), ANDROID_LIST("android-list");
    companion object {
        fun fromWireValue(value: String) = entries.firstOrNull { it.wireValue == value }
    }
}

enum class DeckWidgetPrivacy(val wireValue: String) {
    COUNTS_ONLY("counts-only"), REPOSITORY_AND_TITLES("repository-and-titles");
    companion object {
        fun fromWireValue(value: String) = entries.firstOrNull { it.wireValue == value }
    }
}

enum class DeckWidgetFreshness(val wireValue: String) {
    FRESH("fresh"), STALE("stale"), OFFLINE("offline"), DISCONNECTED("disconnected"),
    NEVER_REFRESHED("never-refreshed");
    companion object {
        fun fromWireValue(value: String) = entries.firstOrNull { it.wireValue == value }
    }
}

data class WidgetPullRequest(
    val repositoryOwner: String,
    val repositoryName: String,
    val number: Long,
    val title: String,
)

data class WidgetSnapshot(
    val matchingCount: Int,
    val pullRequests: List<WidgetPullRequest>,
    val freshness: DeckWidgetFreshness,
    val offline: Boolean,
    val generatedAt: String,
)

data class DeckWidgetInstance(
    val widgetId: String,
    val viewId: String,
    val family: DeckWidgetFamily,
    val privacy: DeckWidgetPrivacy,
    val snapshot: WidgetSnapshot,
)

data class WidgetConfiguration(val accountId: String, val widgets: List<DeckWidgetInstance>)

data class WidgetConfigurationRecord(val version: Int, val configuration: WidgetConfiguration) {
    companion object {
        val EMPTY = WidgetConfigurationRecord(
            DevHudWidgetContract.SCHEMA_VERSION,
            WidgetConfiguration("", emptyList()),
        )
    }
}

enum class WidgetConfigurationErrorCode(val wireValue: String) {
    CORRUPT("corrupt"), FUTURE_VERSION("future-version"), INCOMPATIBLE("incompatible"),
    REFRESH_FAILED("refresh-failed"), STORAGE_UNAVAILABLE("storage-unavailable"),
    WRITE_FAILED("write-failed"), ENCRYPTION_FAILED("encryption-failed"),
}

class WidgetConfigurationException(
    val code: WidgetConfigurationErrorCode,
    cause: Throwable? = null,
) : Exception(code.wireValue, cause)

object WidgetConfigurationCodec {
    fun decode(raw: String): WidgetConfigurationRecord {
        val root = try { JSONObject(raw) } catch (error: JSONException) {
            throw WidgetConfigurationException(WidgetConfigurationErrorCode.CORRUPT, error)
        }
        exactKeys(root, setOf("version", "configuration"))
        val version = integer(root.opt("version")) ?: incompatible()
        if (version > DevHudWidgetContract.SCHEMA_VERSION) {
            throw WidgetConfigurationException(WidgetConfigurationErrorCode.FUTURE_VERSION)
        }
        if (version != DevHudWidgetContract.SCHEMA_VERSION) incompatible()
        val configuration = root.opt("configuration") as? JSONObject ?: incompatible()
        exactKeys(configuration, setOf("accountId", "widgets"))
        val accountId = configuration.opt("accountId") as? String ?: incompatible()
        val array = configuration.opt("widgets") as? JSONArray ?: incompatible()
        if (array.length() > DevHudWidgetContract.MAX_WIDGETS) incompatible()
        if (array.length() > 0 && !validUuid(accountId)) incompatible()
        val widgets = mutableListOf<DeckWidgetInstance>()
        val widgetIds = mutableSetOf<String>()
        for (index in 0 until array.length()) {
            val value = array.opt(index) as? JSONObject ?: incompatible()
            exactKeys(value, setOf("widgetId", "viewId", "family", "privacy", "snapshot"))
            val widgetId = value.opt("widgetId") as? String ?: incompatible()
            val viewId = value.opt("viewId") as? String ?: incompatible()
            if (!validUuid(widgetId) || !validUuid(viewId) || !widgetIds.add(widgetId)) incompatible()
            val family = (value.opt("family") as? String)?.let(DeckWidgetFamily::fromWireValue) ?: incompatible()
            val privacy = (value.opt("privacy") as? String)?.let(DeckWidgetPrivacy::fromWireValue) ?: incompatible()
            val snapshot = decodeSnapshot(value.opt("snapshot") as? JSONObject ?: incompatible(), privacy)
            widgets += DeckWidgetInstance(widgetId, viewId, family, privacy, snapshot)
        }
        return WidgetConfigurationRecord(version, WidgetConfiguration(accountId, widgets))
    }

    fun encode(record: WidgetConfigurationRecord): String {
        val widgets = JSONArray()
        record.configuration.widgets.forEach { widget ->
            val pullRequests = JSONArray()
            widget.snapshot.pullRequests.forEach { pullRequest ->
                pullRequests.put(JSONObject()
                    .put("repositoryOwner", pullRequest.repositoryOwner)
                    .put("repositoryName", pullRequest.repositoryName)
                    .put("number", pullRequest.number)
                    .put("title", pullRequest.title))
            }
            widgets.put(JSONObject()
                .put("widgetId", widget.widgetId)
                .put("viewId", widget.viewId)
                .put("family", widget.family.wireValue)
                .put("privacy", widget.privacy.wireValue)
                .put("snapshot", JSONObject()
                    .put("matchingCount", widget.snapshot.matchingCount)
                    .put("pullRequests", pullRequests)
                    .put("freshness", widget.snapshot.freshness.wireValue)
                    .put("offline", widget.snapshot.offline)
                    .put("generatedAt", widget.snapshot.generatedAt)))
        }
        return JSONObject().put("version", record.version).put(
            "configuration",
            JSONObject().put("accountId", record.configuration.accountId).put("widgets", widgets),
        ).toString().also(::decode)
    }

    private fun decodeSnapshot(value: JSONObject, privacy: DeckWidgetPrivacy): WidgetSnapshot {
        exactKeys(value, setOf("matchingCount", "pullRequests", "freshness", "offline", "generatedAt"))
        val count = integer(value.opt("matchingCount")) ?: incompatible()
        if (count < 0) incompatible()
        val array = value.opt("pullRequests") as? JSONArray ?: incompatible()
        if (array.length() > DevHudWidgetContract.MAX_PULL_REQUESTS ||
            privacy == DeckWidgetPrivacy.COUNTS_ONLY && array.length() != 0) incompatible()
        val pullRequests = mutableListOf<WidgetPullRequest>()
        for (index in 0 until array.length()) {
            val pullRequest = array.opt(index) as? JSONObject ?: incompatible()
            exactKeys(pullRequest, setOf("repositoryOwner", "repositoryName", "number", "title"))
            val owner = pullRequest.opt("repositoryOwner") as? String ?: incompatible()
            val repository = pullRequest.opt("repositoryName") as? String ?: incompatible()
            val number = integer(pullRequest.opt("number")) ?: incompatible()
            val title = pullRequest.opt("title") as? String ?: incompatible()
            if (owner.isEmpty() || repository.isEmpty() || number <= 0 || title.isEmpty()) incompatible()
            pullRequests += WidgetPullRequest(owner, repository, number.toLong(), title)
        }
        val freshness = (value.opt("freshness") as? String)?.let(DeckWidgetFreshness::fromWireValue) ?: incompatible()
        val offline = value.opt("offline") as? Boolean ?: incompatible()
        val generatedAt = value.opt("generatedAt") as? String ?: incompatible()
        try { Instant.parse(generatedAt) } catch (_: Exception) { incompatible() }
        return WidgetSnapshot(count, pullRequests, freshness, offline, generatedAt)
    }

    private fun integer(value: Any?): Int? =
        (value as? Number)?.takeIf { it.toDouble() % 1.0 == 0.0 }?.toInt()
    private fun validUuid(value: String) = try { UUID.fromString(value); true } catch (_: Exception) { false }
    private fun exactKeys(value: JSONObject, expected: Set<String>) {
        if (value.keys().asSequence().toSet() != expected) incompatible()
    }
    private fun incompatible(): Nothing = throw WidgetConfigurationException(WidgetConfigurationErrorCode.INCOMPATIBLE)
}

sealed interface DeckWidgetAction {
    val viewId: String
    data class OpenView(override val viewId: String) : DeckWidgetAction
    data class OpenPullRequest(
        override val viewId: String,
        val owner: String,
        val repository: String,
        val number: Long,
    ) : DeckWidgetAction
    data class Refresh(override val viewId: String) : DeckWidgetAction
    data class ResolveEvent(val eventId: String) : DeckWidgetAction { override val viewId = "" }
}

fun DeckWidgetAction.toAppLink(): String {
    val values = mutableListOf(
        "action" to when (this) {
            is DeckWidgetAction.OpenView -> "open-view"
            is DeckWidgetAction.OpenPullRequest -> "open-pr"
            is DeckWidgetAction.Refresh -> "refresh"
            is DeckWidgetAction.ResolveEvent -> "resolve-event"
        },
    )
    if (this is DeckWidgetAction.ResolveEvent) values += "event" to eventId
    else values += "view" to viewId
    if (this is DeckWidgetAction.OpenPullRequest) {
        values += listOf("owner" to owner, "repository" to repository, "number" to number.toString())
    }
    val query = values.joinToString("&") { (key, value) ->
        "${encode(key)}=${encode(value)}"
    }
    return "https://deli.dev/devhud/deck/open?$query"
}

private fun encode(value: String) = URLEncoder.encode(value, StandardCharsets.UTF_8.toString())

object DeckNotificationPolicy {
    private val opaqueEvent = Regex("^[A-Za-z0-9_-]{16,128}$")
    fun eventId(payload: Map<String, String>): String? =
        payload["eventId"]?.takeIf { payload.keys == setOf("eventId") && opaqueEvent.matches(it) }
    fun text(detailedText: String?, localDetailEnabled: Boolean): String =
        detailedText?.takeIf { localDetailEnabled && it.isNotEmpty() }
            ?: DevHudWidgetContract.GENERIC_NOTIFICATION_TEXT
}
