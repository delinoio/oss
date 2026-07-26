package dev.deli.devhud.widget

import android.app.Activity
import android.appwidget.AppWidgetManager
import android.content.ComponentName
import android.content.Intent
import app.tauri.annotation.Command
import app.tauri.annotation.InvokeArg
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.JSObject
import app.tauri.plugin.Plugin
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import org.json.JSONObject

@InvokeArg
class WriteConfigurationArgs {
    lateinit var record: String
}

@TauriPlugin
class DevHudWidgetPlugin(
    private val activity: Activity,
) : Plugin(activity) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val service by lazy {
        WidgetConfigurationService(
            AndroidWidgetSharedDataAdapter.live(activity),
            AndroidWidgetRefresher(activity),
        )
    }

    @Command
    fun readConfiguration(invoke: Invoke) {
        execute(invoke) {
            JSObject().apply {
                put(
                    "record",
                    service.readRawRecord() ?: JSONObject.NULL,
                )
            }
        }
    }

    @Command
    fun writeConfiguration(invoke: Invoke) {
        execute(invoke) {
            val arguments = invoke.parseArgs(WriteConfigurationArgs::class.java)
            JSObject().apply {
                put(
                    "refreshedWidgetCount",
                    service.writeRawRecord(arguments.record),
                )
            }
        }
    }

    @Command
    fun prepareReset(invoke: Invoke) {
        execute(invoke) {
            // Resolving the dedicated adapter is the only native prerequisite.
            // No value is read or changed until the separately confirmed reset.
            service
            JSObject().apply { put("prepared", true) }
        }
    }

    @Command
    fun resetConfiguration(invoke: Invoke) {
        execute(invoke) {
            JSObject().apply {
                put("refreshedWidgetCount", service.reset())
            }
        }
    }

    private fun execute(
        invoke: Invoke,
        operation: suspend () -> JSObject,
    ) {
        scope.launch {
            try {
                val response = operation()
                activity.runOnUiThread { invoke.resolve(response) }
            } catch (error: WidgetConfigurationException) {
                activity.runOnUiThread {
                    invoke.reject(
                        "The DevHud widget operation failed.",
                        error.code.wireValue,
                    )
                }
            } catch (_: Exception) {
                activity.runOnUiThread {
                    invoke.reject(
                        "The DevHud widget operation failed.",
                        WidgetConfigurationErrorCode.STORAGE_UNAVAILABLE.wireValue,
                    )
                }
            }
        }
    }
}

private class AndroidWidgetRefresher(
    private val activity: Activity,
) : WidgetRefreshing {
    private val appWidgetManager = AppWidgetManager.getInstance(activity)
    private val componentName =
        ComponentName(
            activity.packageName,
            "dev.deli.devhud.widget.DevHudWidgetProvider",
        )

    override suspend fun refresh(): Int {
        // The 0.1.0 release has no registered receiver, so this deterministically
        // returns an empty set. A future registered provider can consume the same
        // adapter and explicit component identity without widening this bridge.
        try {
            val widgetIds = appWidgetManager.getAppWidgetIds(componentName)
            if (widgetIds.isNotEmpty()) {
                activity.sendBroadcast(
                    Intent(AppWidgetManager.ACTION_APPWIDGET_UPDATE)
                        .setComponent(componentName)
                        .putExtra(AppWidgetManager.EXTRA_APPWIDGET_IDS, widgetIds),
                )
            }
            return widgetIds.size
        } catch (error: RuntimeException) {
            throw WidgetConfigurationException(
                WidgetConfigurationErrorCode.REFRESH_FAILED,
                error,
            )
        }
    }
}
