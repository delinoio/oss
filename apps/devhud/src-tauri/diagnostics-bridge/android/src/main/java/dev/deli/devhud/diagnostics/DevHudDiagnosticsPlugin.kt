package dev.deli.devhud.diagnostics

import android.app.Activity
import android.content.Intent
import androidx.activity.result.ActivityResult
import app.tauri.annotation.ActivityCallback
import app.tauri.annotation.Command
import app.tauri.annotation.InvokeArg
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.JSObject
import app.tauri.plugin.Plugin

@InvokeArg
class ExportDiagnosticsArgs {
    lateinit var fileName: String
    lateinit var bundle: String
}

@TauriPlugin
class DevHudDiagnosticsPlugin(
    private val activity: Activity,
) : Plugin(activity) {
    private var pendingBundle: String? = null

    @Command
    fun exportDiagnostics(invoke: Invoke) {
        synchronized(this) {
            if (pendingBundle != null) {
                reject(invoke, DiagnosticsExportErrorCode.BUSY)
                return
            }
            val arguments =
                try {
                    invoke.parseArgs(ExportDiagnosticsArgs::class.java)
                } catch (_: Exception) {
                    reject(invoke, DiagnosticsExportErrorCode.PICKER_UNAVAILABLE)
                    return
                }
            pendingBundle = arguments.bundle
            val intent =
                Intent(Intent.ACTION_CREATE_DOCUMENT)
                    .addCategory(Intent.CATEGORY_OPENABLE)
                    .setType("application/x-ndjson")
                    .putExtra(Intent.EXTRA_TITLE, arguments.fileName)
            try {
                startActivityForResult(invoke, intent, "exportDiagnosticsResult")
            } catch (_: Exception) {
                pendingBundle = null
                reject(invoke, DiagnosticsExportErrorCode.PICKER_UNAVAILABLE)
            }
        }
    }

    @ActivityCallback
    fun exportDiagnosticsResult(
        invoke: Invoke,
        result: ActivityResult,
    ) {
        val bundle =
            synchronized(this) {
                pendingBundle.also { pendingBundle = null }
            } ?: run {
                reject(invoke, DiagnosticsExportErrorCode.WRITE_FAILED)
                return
            }
        if (result.resultCode == Activity.RESULT_CANCELED) {
            invoke.resolve(status("cancelled"))
            return
        }
        val destination = result.data?.data
        if (result.resultCode != Activity.RESULT_OK || destination == null) {
            reject(invoke, DiagnosticsExportErrorCode.PICKER_UNAVAILABLE)
            return
        }
        try {
            activity.contentResolver.openOutputStream(destination, "wt").use { stream ->
                requireNotNull(stream)
                stream.write(bundle.toByteArray(Charsets.UTF_8))
                stream.flush()
            }
            invoke.resolve(status("exported"))
        } catch (_: Exception) {
            reject(invoke, DiagnosticsExportErrorCode.WRITE_FAILED)
        }
    }

    private fun status(value: String): JSObject =
        JSObject().apply {
            put("status", value)
        }

    private fun reject(
        invoke: Invoke,
        code: DiagnosticsExportErrorCode,
    ) {
        invoke.reject("The DevHud diagnostics export failed.", code.wireValue)
    }
}

private enum class DiagnosticsExportErrorCode(
    val wireValue: String,
) {
    BUSY("busy"),
    PICKER_UNAVAILABLE("picker-unavailable"),
    WRITE_FAILED("write-failed"),
}
