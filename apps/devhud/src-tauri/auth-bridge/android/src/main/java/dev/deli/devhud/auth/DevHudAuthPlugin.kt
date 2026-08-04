package dev.deli.devhud.auth

import android.app.Activity
import android.content.Intent
import android.net.Uri
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import app.tauri.annotation.Command
import app.tauri.annotation.InvokeArg
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.JSObject
import app.tauri.plugin.Plugin
import org.json.JSONObject

@InvokeArg
class SessionArgs { lateinit var record: String }

@InvokeArg
class AuthorizationArgs { lateinit var url: String }

@TauriPlugin
class DevHudAuthPlugin(private val activity: Activity) : Plugin(activity) {
    private var pendingCallback: String? = validatedCallback(activity.intent?.data)
    private val preferences by lazy {
        val key = MasterKey.Builder(activity)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()
        EncryptedSharedPreferences.create(
            activity,
            "devhud-auth-v1",
            key,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
    }

    @Command
    fun readSession(invoke: Invoke) = guarded(invoke) {
        JSObject().apply {
            put("record", preferences.getString("active-session", null) ?: JSONObject.NULL)
        }
    }

    @Command
    fun writeSession(invoke: Invoke) = guarded(invoke) {
        val value = invoke.parseArgs(SessionArgs::class.java).record
        check(value.length in 1..32_768)
        check(preferences.edit().putString("active-session", value).commit())
        completed()
    }

    @Command
    fun clearSession(invoke: Invoke) = guarded(invoke) {
        check(preferences.edit().remove("active-session").commit())
        completed()
    }

    @Command
    fun readDeviceRegistration(invoke: Invoke) = guarded(invoke) {
        JSObject().apply {
            put("record", preferences.getString("deck-device-registration", null) ?: JSONObject.NULL)
        }
    }

    @Command
    fun writeDeviceRegistration(invoke: Invoke) = guarded(invoke) {
        val value = invoke.parseArgs(SessionArgs::class.java).record
        check(value.length in 1..32_768)
        check(preferences.edit().putString("deck-device-registration", value).commit())
        completed()
    }

    @Command
    fun clearDeviceRegistration(invoke: Invoke) = guarded(invoke) {
        check(preferences.edit().remove("deck-device-registration").commit())
        completed()
    }

    @Command
    fun openAuthorization(invoke: Invoke) = guarded(invoke) {
        val value = invoke.parseArgs(AuthorizationArgs::class.java).url
        val target = Uri.parse(value)
        check(target.scheme == "https" && target.path == "/oidc/auth")
        check(target.host != null && target.userInfo == null && target.fragment == null)
        activity.startActivity(Intent(Intent.ACTION_VIEW, target))
        completed()
    }

    @Command
    fun openPullRequest(invoke: Invoke) = guarded(invoke) {
        val value = invoke.parseArgs(AuthorizationArgs::class.java).url
        val target = Uri.parse(value)
        val segments = target.pathSegments
        val repositorySegment = Regex("[A-Za-z0-9._-]{1,100}")
        check(target.scheme == "https" && target.host == "github.com" && target.port == -1)
        check(target.userInfo == null && target.query == null && target.fragment == null)
        check(segments.size == 4 && segments[2] == "pull")
        check(repositorySegment.matches(segments[0]) && repositorySegment.matches(segments[1]))
        check(segments[0] != "." && segments[0] != ".." && segments[1] != "." && segments[1] != "..")
        check(segments[3].all(Char::isDigit) && segments[3].toULongOrNull()?.let { it > 0uL } == true)
        activity.startActivity(Intent(Intent.ACTION_VIEW, target))
        completed()
    }

    @Command
    fun takeCallback(invoke: Invoke) = guarded(invoke) {
        val callback = pendingCallback
        pendingCallback = null
        JSObject().apply { put("url", callback ?: JSONObject.NULL) }
    }

    override fun onNewIntent(intent: Intent) {
        pendingCallback = pendingCallback ?: validatedCallback(intent.data)
    }

    private fun validatedCallback(callback: Uri?): String? =
        callback
            ?.takeIf {
                it.scheme == "https" &&
                    it.host == "deli.dev" &&
                    it.path == "/auth/devhud/callback" &&
                    it.fragment == null &&
                    it.userInfo == null &&
                    it.port == -1
            }
            ?.toString()

    private fun completed() = JSObject().apply { put("completed", true) }

    private fun guarded(invoke: Invoke, operation: () -> JSObject) {
        try {
            invoke.resolve(operation())
        } catch (_: Exception) {
            invoke.reject("The DevHud secure authentication operation failed.", "secure-vault-unavailable")
        }
    }
}
