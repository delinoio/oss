package dev.deli.devhud.widget

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject

object DevHudWidgetContract {
    const val EXTENSION_IDENTIFIER = "dev.deli.devhud.widget"
    const val STORAGE_KEY = "devhud.widget-configuration.v1"
    const val DATASTORE_NAME = "devhud-widget-state"
    const val DATASTORE_FILE = "$DATASTORE_NAME.preferences_pb"
    const val SCHEMA_VERSION = 1
}

enum class WidgetSlot(val wireValue: String) {
    PRIMARY("primary"),
    SECONDARY("secondary"),
    TERTIARY("tertiary");

    companion object {
        fun fromWireValue(value: String): WidgetSlot? =
            entries.firstOrNull { it.wireValue == value }
    }
}

@JvmInline
value class StableToolId private constructor(val value: String) {
    companion object {
        private val pattern = Regex("^[a-z]+(?:-[a-z0-9]+)*$")

        fun parse(value: String): StableToolId? =
            value.takeIf(pattern::matches)?.let(::StableToolId)
    }
}

data class WidgetSlotReference(
    val slot: WidgetSlot,
    val toolId: StableToolId,
)

data class WidgetConfiguration(val slots: List<WidgetSlotReference>) {
    init {
        require(slots.map { it.slot }.toSet().size == slots.size)
    }
}

data class WidgetConfigurationRecord(
    val version: Int,
    val configuration: WidgetConfiguration,
) {
    companion object {
        val EMPTY =
            WidgetConfigurationRecord(
                DevHudWidgetContract.SCHEMA_VERSION,
                WidgetConfiguration(emptyList()),
            )
    }
}

enum class WidgetConfigurationErrorCode(val wireValue: String) {
    CORRUPT("corrupt"),
    FUTURE_VERSION("future-version"),
    INCOMPATIBLE("incompatible"),
    REFRESH_FAILED("refresh-failed"),
    STORAGE_UNAVAILABLE("storage-unavailable"),
    WRITE_FAILED("write-failed"),
}

class WidgetConfigurationException(
    val code: WidgetConfigurationErrorCode,
    cause: Throwable? = null,
) : Exception(code.wireValue, cause)

object WidgetConfigurationCodec {
    fun decode(raw: String): WidgetConfigurationRecord {
        val root =
            try {
                JSONObject(raw)
            } catch (error: JSONException) {
                throw WidgetConfigurationException(
                    WidgetConfigurationErrorCode.CORRUPT,
                    error,
                )
            }
        requireExactKeys(root, setOf("version", "configuration"))
        val versionValue = root.opt("version")
        if (versionValue !is Number || versionValue.toDouble() % 1.0 != 0.0) {
            incompatible()
        }
        val version = versionValue.toInt()
        if (version > DevHudWidgetContract.SCHEMA_VERSION) {
            throw WidgetConfigurationException(
                WidgetConfigurationErrorCode.FUTURE_VERSION,
            )
        }
        if (version != DevHudWidgetContract.SCHEMA_VERSION) incompatible()

        val configurationObject =
            root.opt("configuration") as? JSONObject ?: incompatible()
        requireExactKeys(configurationObject, setOf("slots"))
        val slotsArray = configurationObject.opt("slots") as? JSONArray ?: incompatible()
        val slots = mutableListOf<WidgetSlotReference>()
        val seenSlots = mutableSetOf<WidgetSlot>()
        for (index in 0 until slotsArray.length()) {
            val reference = slotsArray.opt(index) as? JSONObject ?: incompatible()
            requireExactKeys(reference, setOf("slot", "toolId"))
            val slot =
                (reference.opt("slot") as? String)
                    ?.let(WidgetSlot::fromWireValue)
                    ?: incompatible()
            if (!seenSlots.add(slot)) incompatible()
            val toolId =
                (reference.opt("toolId") as? String)
                    ?.let(StableToolId::parse)
                    ?: incompatible()
            slots += WidgetSlotReference(slot, toolId)
        }
        return WidgetConfigurationRecord(version, WidgetConfiguration(slots))
    }

    fun encode(record: WidgetConfigurationRecord): String {
        if (record.version != DevHudWidgetContract.SCHEMA_VERSION) incompatible()
        if (
            record.configuration.slots.map { it.slot }.toSet().size !=
                record.configuration.slots.size
        ) {
            incompatible()
        }
        val slots = JSONArray()
        for (reference in record.configuration.slots) {
            slots.put(
                JSONObject()
                    .put("slot", reference.slot.wireValue)
                    .put("toolId", reference.toolId.value),
            )
        }
        return JSONObject()
            .put("version", record.version)
            .put("configuration", JSONObject().put("slots", slots))
            .toString()
    }

    private fun requireExactKeys(
        objectValue: JSONObject,
        keys: Set<String>,
    ) {
        val actual = mutableSetOf<String>()
        val iterator = objectValue.keys()
        while (iterator.hasNext()) actual += iterator.next()
        if (actual != keys) incompatible()
    }

    private fun incompatible(): Nothing =
        throw WidgetConfigurationException(
            WidgetConfigurationErrorCode.INCOMPATIBLE,
        )
}
