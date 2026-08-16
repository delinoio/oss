package io.delino.devhud.bridge

import android.Manifest
import android.app.Activity
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import androidx.appcompat.app.AppCompatActivity
import app.tauri.PermissionState
import app.tauri.annotation.Command
import app.tauri.annotation.Permission
import app.tauri.annotation.PermissionCallback
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.JSObject
import app.tauri.plugin.Plugin
import java.security.KeyStore
import java.util.Base64
import java.util.concurrent.Executors
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

private const val notificationAlias = "notifications"
private const val keyAlias = "io.delino.devhud.secure-settings.v1"
private const val storeName = "devhud-secure-settings-v1"
private const val notificationChannel = "deck-changes"

@TauriPlugin(
    permissions = [
        Permission(strings = [Manifest.permission.POST_NOTIFICATIONS], alias = notificationAlias)
    ]
)
class DevhudNativePlugin(private val activity: Activity) : Plugin(activity) {
    private var pendingAuthCallback: String? = null
    private val secureSettingsExecutor = Executors.newSingleThreadExecutor()

    override fun load(webView: android.webkit.WebView) {
        captureAuthCallback(activity.intent)
    }

    override fun onNewIntent(intent: Intent) {
        captureAuthCallback(intent)
    }

    override fun onDestroy(activity: AppCompatActivity) {
        secureSettingsExecutor.shutdown()
    }

    @Command
    fun request(invoke: Invoke) {
        try {
            when (invoke.getArgs().getString("operation")) {
                "auth.take-pending-callback" -> takeAuthCallback(invoke)
                "lifecycle.open-external" -> openExternal(invoke)
                "secure.read" -> readSecure(invoke)
                "secure.write" -> writeSecure(invoke)
                "secure.remove" -> removeSecure(invoke)
                "notifications.permission" -> resolveNotificationPermission(invoke)
                "notifications.request-permission" -> requestNotificationPermission(invoke)
                "notifications.publish-deck-change" -> publishNotification(invoke)
                "notifications.cancel-deck" -> cancelNotification(invoke)
                "updates.status" -> resolveUpdateStatus(invoke)
                "updates.open-store" -> openStore(invoke)
                else -> invoke.reject("invalid-argument", "invalid-argument")
            }
        } catch (error: Exception) {
            invoke.reject("platform-failure", "platform-failure", error)
        }
    }

    private fun captureAuthCallback(intent: Intent?) {
        val candidate = intent?.dataString ?: return
        if (isAuthCallback(candidate)) pendingAuthCallback = candidate
    }

    private fun isAuthCallback(candidate: String): Boolean {
        if (candidate != candidate.trim()) return false
        val uri = Uri.parse(candidate)
        return uri.scheme == "devhud" && uri.host == "auth" && uri.path == "/callback" && uri.fragment == null && uri.userInfo == null && uri.port == -1
    }

    private fun takeAuthCallback(invoke: Invoke) {
        val callback = pendingAuthCallback
        val response = JSObject().put("kind", "auth-callback").put("url", callback)
        if (callback != null && activity.intent?.dataString == callback) {
            activity.intent = Intent(activity.intent).setData(null)
        }
        pendingAuthCallback = null
        invoke.resolve(response)
    }

    private fun openExternal(invoke: Invoke) {
        val args = invoke.getArgs()
        val uri = when (args.getString("target")) {
            "pat" -> Uri.parse("https://github.com/settings/personal-access-tokens/new")
            "authentication" -> Uri.parse(args.getString("apiOrigin")).also {
                val loopback = it.host == "localhost" || it.host == "::1" || it.host == "[::1]" || it.host?.startsWith("127.") == true
                val validScheme = it.scheme == "https" || (it.scheme == "http" && loopback)
                if (!validScheme || (it.path != "" && it.path != "/") || it.query != null || it.fragment != null || it.userInfo != null) {
                    throw IllegalArgumentException("apiOrigin")
                }
            }
            else -> throw IllegalArgumentException("target")
        }
        activity.startActivity(Intent(Intent.ACTION_VIEW, uri).addCategory(Intent.CATEGORY_BROWSABLE))
        invoke.resolve(JSObject().put("kind", "ok"))
    }

    private fun settingKey(args: JSObject): String {
        val setting = args.getJSObject("setting") ?: throw IllegalArgumentException("setting")
        return "${setting.getString("kind")}:${setting.getString("profileId")}"
    }

    private fun secretKey(): SecretKey {
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (keyStore.getKey(keyAlias, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").run {
            init(
                KeyGenParameterSpec.Builder(keyAlias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                    .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                    .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                    .build()
            )
            generateKey()
        }
    }

    private fun readSecure(invoke: Invoke) {
        secureSettingsExecutor.execute {
            try {
                val encoded = activity.getSharedPreferences(storeName, Context.MODE_PRIVATE).getString(settingKey(invoke.getArgs()), null)
                if (encoded == null) {
                    invoke.resolve(JSObject().put("kind", "secure-value").put("value", null))
                    return@execute
                }
                val payload = Base64.getDecoder().decode(encoded)
                val iv = payload.copyOfRange(0, 12)
                val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply {
                    init(Cipher.DECRYPT_MODE, secretKey(), GCMParameterSpec(128, iv))
                }
                val value = String(cipher.doFinal(payload.copyOfRange(12, payload.size)), Charsets.UTF_8)
                invoke.resolve(JSObject().put("kind", "secure-value").put("value", value))
            } catch (error: Exception) {
                invoke.reject("storage-failure", "storage-failure", error)
            }
        }
    }

    private fun writeSecure(invoke: Invoke) {
        val args = invoke.getArgs()
        val key = settingKey(args)
        val value = args.getString("value")
        persistSecure(invoke) {
            val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.ENCRYPT_MODE, secretKey()) }
            val encrypted = cipher.doFinal(value.toByteArray(Charsets.UTF_8))
            val payload = cipher.iv + encrypted
            activity.getSharedPreferences(storeName, Context.MODE_PRIVATE).edit()
                .putString(key, Base64.getEncoder().encodeToString(payload)).commit()
        }
    }

    private fun removeSecure(invoke: Invoke) {
        val key = settingKey(invoke.getArgs())
        persistSecure(invoke) {
            activity.getSharedPreferences(storeName, Context.MODE_PRIVATE).edit().remove(key).commit()
        }
    }

    private fun persistSecure(invoke: Invoke, operation: () -> Boolean) {
        secureSettingsExecutor.execute {
            try {
                if (operation()) invoke.resolve(JSObject().put("kind", "ok"))
                else invoke.reject("storage-failure", "storage-failure")
            } catch (error: Exception) {
                invoke.reject("storage-failure", "storage-failure", error)
            }
        }
    }

    private fun permissionValue(): String {
        if (Build.VERSION.SDK_INT < 33) {
            val manager = activity.getSystemService(NotificationManager::class.java)
            return if (manager.areNotificationsEnabled()) "authorized" else "denied"
        }
        return when (getPermissionState(notificationAlias)) {
            PermissionState.GRANTED -> "authorized"
            PermissionState.PROMPT -> "not-determined"
            else -> "denied"
        }
    }

    private fun resolveNotificationPermission(invoke: Invoke) {
        invoke.resolve(JSObject().put("kind", "notification-permission").put("permission", permissionValue()))
    }

    private fun requestNotificationPermission(invoke: Invoke) {
        if (Build.VERSION.SDK_INT < 33) {
            resolveNotificationPermission(invoke)
        } else {
            requestPermissionForAlias(notificationAlias, invoke, "notificationPermissionResult")
        }
    }

    @PermissionCallback
    private fun notificationPermissionResult(invoke: Invoke) {
        resolveNotificationPermission(invoke)
    }

    private fun publishNotification(invoke: Invoke) {
        if (permissionValue() != "authorized") {
            invoke.reject("permission-denied", "permission-denied")
            return
        }
        val notification = invoke.getArgs().getJSObject("notification") ?: throw IllegalArgumentException("notification")
        val notificationId = notification.getString("id")
        val deckId = notification.getString("deckId")
        val manager = activity.getSystemService(NotificationManager::class.java)
        val channelNameId = activity.resources.getIdentifier("devhud_notification_channel_deck_changes", "string", activity.packageName)
        require(channelNameId != 0) { "notification channel name resource" }
        manager.createNotificationChannel(NotificationChannel(notificationChannel, activity.getString(channelNameId), NotificationManager.IMPORTANCE_DEFAULT))
        if (manager.getNotificationChannel(notificationChannel)?.importance == NotificationManager.IMPORTANCE_NONE) {
            invoke.reject("permission-denied", "permission-denied")
            return
        }
        val launchIntent = activity.packageManager.getLaunchIntentForPackage(activity.packageName)
        val pendingIntent = PendingIntent.getActivity(activity, 0, launchIntent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val built = android.app.Notification.Builder(activity, notificationChannel)
            .setSmallIcon(activity.applicationInfo.icon)
            .setContentTitle(notification.getString("title"))
            .setContentText(notification.getString("body"))
            .setContentIntent(pendingIntent)
            .setGroup(deckId)
            .setAutoCancel(true)
            .build()
        manager.notify(notificationId, 0, built)
        invoke.resolve(JSObject().put("kind", "ok"))
    }

    private fun cancelNotification(invoke: Invoke) {
        val deckId = invoke.getArgs().getString("deckId")
        val manager = activity.getSystemService(NotificationManager::class.java)
        manager.activeNotifications
            .filter { it.notification.group == deckId }
            .forEach { manager.cancel(it.tag, it.id) }
        invoke.resolve(JSObject().put("kind", "ok"))
    }

    private fun resolveUpdateStatus(invoke: Invoke) {
        val version = activity.packageManager.getPackageInfo(activity.packageName, 0).versionName ?: "0"
        val configured = storeIntent().resolveActivity(activity.packageManager) != null
        invoke.resolve(JSObject().put("kind", "update-status").put("store", "play-store").put("installedVersion", version).put("configured", configured))
    }

    private fun openStore(invoke: Invoke) {
        val intent = storeIntent()
        if (intent.resolveActivity(activity.packageManager) == null) {
            invoke.reject("not-configured", "not-configured")
            return
        }
        activity.startActivity(intent)
        invoke.resolve(JSObject().put("kind", "ok"))
    }

    private fun storeIntent(): Intent =
        Intent(Intent.ACTION_VIEW, Uri.parse("market://details?id=${activity.packageName}"))
            .addCategory(Intent.CATEGORY_BROWSABLE)
}
