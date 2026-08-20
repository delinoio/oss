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
import androidx.activity.result.ActivityResult
import androidx.appcompat.app.AppCompatActivity
import app.tauri.PermissionState
import app.tauri.annotation.ActivityCallback
import app.tauri.annotation.Command
import app.tauri.annotation.Permission
import app.tauri.annotation.PermissionCallback
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.JSObject
import app.tauri.plugin.Plugin
import java.io.FileNotFoundException
import java.security.KeyStore
import java.util.Base64
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger
import javax.crypto.Cipher
import javax.crypto.AEADBadTagException
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

private const val notificationAlias = "notifications"
private const val keyAlias = "io.delino.devhud.secure-settings.v1"
private const val storeName = "devhud-secure-settings-v1"
private const val diagnosticsCleanupStoreName = "devhud-diagnostics-cleanup-v1"
private const val diagnosticsCleanupUriKey = "pending-uri"
private const val diagnosticsCleanupReleaseOnlyKey = "release-only"
private const val notificationChannel = "deck-changes"

@TauriPlugin(
    permissions = [
        Permission(strings = [Manifest.permission.POST_NOTIFICATIONS], alias = notificationAlias)
    ]
)
class DevhudNativePlugin(private val activity: Activity) : Plugin(activity) {
    private var pendingAuthCallback: String? = null
    private var pendingDiagnosticsCleanup: Uri? = null
    private var diagnosticsCleanupReleaseOnly = false
    private var diagnosticsExportPickerActive = false
    private val diagnosticsPurgesInProgress = AtomicInteger()
    private val secureSettingsExecutor = Executors.newSingleThreadExecutor()

    override fun load(webView: android.webkit.WebView) {
        captureAuthCallback(activity.intent)
        val diagnosticsCleanup = activity.getSharedPreferences(diagnosticsCleanupStoreName, Context.MODE_PRIVATE)
        pendingDiagnosticsCleanup = diagnosticsCleanup.getString(diagnosticsCleanupUriKey, null)?.let(Uri::parse)
        diagnosticsCleanupReleaseOnly = pendingDiagnosticsCleanup != null && diagnosticsCleanup.getBoolean(diagnosticsCleanupReleaseOnlyKey, false)
        cleanupPendingDiagnosticsExport()
    }

    override fun onNewIntent(intent: Intent) {
        captureAuthCallback(intent)
    }

    override fun onDestroy(activity: AppCompatActivity) {
        cleanupPendingDiagnosticsExport()
        secureSettingsExecutor.shutdown()
    }

    @Command
    fun request(invoke: Invoke) {
        try {
            when (invoke.getArgs().getString("operation")) {
                "auth.peek-pending-callback" -> peekAuthCallback(invoke)
                "auth.take-pending-callback" -> takeAuthCallback(invoke)
                "auth.open-system-browser" -> openAuthenticationBrowser(invoke)
                "lifecycle.open-external" -> openExternal(invoke)
                "diagnostics.export" -> exportDiagnostics(invoke)
                "secure.read" -> readSecure(invoke)
                "secure.write" -> writeSecure(invoke)
                "secure.remove" -> removeSecure(invoke)
                "secure.reconcile-github-pats" -> reconcileGitHubPats(invoke)
                "secure.purge" -> purgeSecure(invoke)
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

    private fun openAuthenticationBrowser(invoke: Invoke) {
        val args = invoke.getArgs()
        val destination = Uri.parse(args.getString("url"))
        val issuer = Uri.parse(args.getString("issuer"))
        val loopback = issuer.host == "localhost" || issuer.host == "::1" || issuer.host == "[::1]" || issuer.host?.startsWith("127.") == true
        val validIssuer = (issuer.scheme == "https" || (issuer.scheme == "http" && loopback)) && issuer.query == null && issuer.fragment == null && issuer.userInfo == null
        val sameOrigin = destination.scheme == issuer.scheme && destination.host == issuer.host && destination.port == issuer.port && destination.userInfo == null && destination.fragment == null
        if (!validIssuer || !sameOrigin) throw IllegalArgumentException("issuer")
        activity.startActivity(Intent(Intent.ACTION_VIEW, destination).addCategory(Intent.CATEGORY_BROWSABLE))
        invoke.resolve(JSObject().put("kind", "ok"))
    }

    private fun exportDiagnostics(invoke: Invoke) {
        if (diagnosticsPurgesInProgress.get() > 0) {
            invoke.reject("platform-failure", "platform-failure")
            return
        }
        if (!cleanupPendingDiagnosticsExport()) {
            invoke.reject("storage-failure", "storage-failure")
            return
        }
        val args = invoke.getArgs()
        val suggestedName = args.getString("suggestedName")
        val contents = args.getString("contents")
        require(suggestedName.matches(Regex("devhud-diagnostics-[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\\.json")))
        require(contents.toByteArray(Charsets.UTF_8).size <= 1024 * 1024)
        if (diagnosticsExportPickerActive) {
            invoke.reject("platform-failure", "platform-failure")
            return
        }
        val intent = Intent(Intent.ACTION_CREATE_DOCUMENT)
            .addCategory(Intent.CATEGORY_OPENABLE)
            .addFlags(Intent.FLAG_GRANT_WRITE_URI_PERMISSION or Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION)
            .setType("application/json")
            .putExtra(Intent.EXTRA_TITLE, suggestedName)
        diagnosticsExportPickerActive = true
        try {
            startActivityForResult(invoke, intent, "diagnosticsExportResult")
        } catch (error: Exception) {
            diagnosticsExportPickerActive = false
            throw error
        }
    }

    @ActivityCallback
    private fun diagnosticsExportResult(invoke: Invoke, result: ActivityResult) {
        if (!diagnosticsExportPickerActive) {
            invoke.resolve(JSObject().put("kind", "diagnostics-export").put("outcome", "cancelled"))
            return
        }
        diagnosticsExportPickerActive = false
        if (result.resultCode == Activity.RESULT_CANCELED) {
            invoke.resolve(JSObject().put("kind", "diagnostics-export").put("outcome", "cancelled"))
            return
        }
        if (result.resultCode != Activity.RESULT_OK || result.data?.data == null) {
            invoke.reject("storage-failure", "storage-failure")
            return
        }
        val destination = result.data!!.data!!
        try {
            activity.contentResolver.takePersistableUriPermission(destination, Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
        } catch (_: Exception) {
            invoke.reject("storage-failure", "storage-failure")
            return
        }
        try {
            // Retain cleanup ownership before writing so process death cannot orphan diagnostic bytes.
            if (!retainDiagnosticsCleanup(destination)) {
                cleanupPendingDiagnosticsExport()
                invoke.reject("storage-failure", "storage-failure")
                return
            }
            val contents = invoke.getArgs().getString("contents")
            activity.contentResolver.openOutputStream(destination, "wt").use { stream ->
                requireNotNull(stream).write(contents.toByteArray(Charsets.UTF_8))
                stream.flush()
            }
            if (!forgetDiagnosticsCleanup()) {
                cleanupPendingDiagnosticsExport()
                invoke.reject("storage-failure", "storage-failure")
                return
            }
            invoke.resolve(JSObject().put("kind", "diagnostics-export").put("outcome", "saved"))
        } catch (_: Exception) {
            cleanupPendingDiagnosticsExport()
            invoke.reject("storage-failure", "storage-failure")
        }
    }

    private fun retainDiagnosticsCleanup(destination: Uri): Boolean {
        pendingDiagnosticsCleanup = destination
        diagnosticsCleanupReleaseOnly = false
        return activity.getSharedPreferences(diagnosticsCleanupStoreName, Context.MODE_PRIVATE)
            .edit()
            .putString(diagnosticsCleanupUriKey, destination.toString())
            .remove(diagnosticsCleanupReleaseOnlyKey)
            .commit()
    }

    private fun forgetDiagnosticsCleanup(): Boolean {
        val destination = pendingDiagnosticsCleanup ?: return true
        val preferences = activity.getSharedPreferences(diagnosticsCleanupStoreName, Context.MODE_PRIVATE)
        if (!diagnosticsCleanupReleaseOnly) {
            if (!preferences.edit().putBoolean(diagnosticsCleanupReleaseOnlyKey, true).commit()) return false
            diagnosticsCleanupReleaseOnly = true
        }
        if (hasPersistedDiagnosticsWriteGrant(destination)) {
            try {
                activity.contentResolver.releasePersistableUriPermission(destination, Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
            } catch (_: Exception) {
                return false
            }
        }
        val removed = preferences.edit()
            .remove(diagnosticsCleanupUriKey)
            .remove(diagnosticsCleanupReleaseOnlyKey)
            .commit()
        if (!removed) return false
        pendingDiagnosticsCleanup = null
        diagnosticsCleanupReleaseOnly = false
        return true
    }

    private fun cleanupPendingDiagnosticsExport(): Boolean {
        val destination = pendingDiagnosticsCleanup ?: return true
        if (diagnosticsCleanupReleaseOnly) return forgetDiagnosticsCleanup()
        if (!hasPersistedDiagnosticsWriteGrant(destination)) return false
        val deleted = try {
            activity.contentResolver.delete(destination, null, null) > 0
        } catch (_: Exception) {
            false
        }
        // A zero-row delete is not proof of removal. Opening for write truncates any surviving
        // partial content, while FileNotFoundException is the only confirmed-absent fallback.
        val cleaned = if (deleted) true else try {
            requireNotNull(activity.contentResolver.openFileDescriptor(destination, "wt")).use { true }
        } catch (_: FileNotFoundException) {
            true
        } catch (_: Exception) {
            false
        }
        return cleaned && forgetDiagnosticsCleanup()
    }

    private fun hasPersistedDiagnosticsWriteGrant(destination: Uri) =
        activity.contentResolver.persistedUriPermissions.any { permission ->
            permission.uri == destination && permission.isWritePermission
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

    private fun peekAuthCallback(invoke: Invoke) {
        invoke.resolve(JSObject().put("kind", "auth-callback").put("url", pendingAuthCallback))
    }

    private fun openExternal(invoke: Invoke) {
        val args = invoke.getArgs()
        val uri = when (args.getString("target")) {
            "fine-grained-pat" -> Uri.parse("https://github.com/settings/personal-access-tokens/new?contents=read&issues=write&metadata=read&pull_requests=read")
            "classic-pat" -> Uri.parse("https://github.com/settings/tokens/new?scopes=repo")
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

    private fun githubPatScopeKey(scopeId: String, profileId: String) = "github-pat-scope:$scopeId:$profileId"

    private fun githubPatScopeKeys(keys: Set<String>, profileId: String) =
        keys.filter { key -> key.startsWith("github-pat-scope:") && key.endsWith(":$profileId") }

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
                val args = invoke.getArgs()
                val setting = args.getJSObject("setting") ?: throw IllegalArgumentException("setting")
                val kind = setting.getString("kind")
                val profileId = setting.getString("profileId")
                val key = settingKey(args)
                val preferences = activity.getSharedPreferences(storeName, Context.MODE_PRIVATE)
                if (kind == "github-pat") {
                    val scopeId = setting.getString("scopeId")
                    if (!preferences.contains(githubPatScopeKey(scopeId, profileId))) {
                        invoke.resolve(JSObject().put("kind", "secure-value").put("value", null))
                        return@execute
                    }
                }
                val encoded = preferences.getString(key, null)
                if (encoded == null) {
                    invoke.resolve(JSObject().put("kind", "secure-value").put("value", null))
                    return@execute
                }
                val value = try {
                    decryptSecure(encoded, key, authenticateKey = true)
                } catch (_: AEADBadTagException) {
                    val legacy = decryptSecure(encoded, key, authenticateKey = false)
                    if (!preferences.edit().putString(key, encryptSecure(legacy, key)).commit()) {
                        throw IllegalStateException("secure migration persistence failed")
                    }
                    legacy
                }
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
            val editor = activity.getSharedPreferences(storeName, Context.MODE_PRIVATE).edit()
            if (key.startsWith("github-pat:")) {
                val setting = args.getJSObject("setting") ?: throw IllegalArgumentException("setting")
                val marker = githubPatScopeKey(setting.getString("scopeId"), setting.getString("profileId"))
                editor.putString(marker, encryptSecure("1", marker))
            }
            editor.putString(key, encryptSecure(value, key)).commit()
        }
    }

    private fun encryptSecure(value: String, key: String): String {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply {
            init(Cipher.ENCRYPT_MODE, secretKey())
            updateAAD(key.toByteArray(Charsets.UTF_8))
        }
        val encrypted = cipher.doFinal(value.toByteArray(Charsets.UTF_8))
        return Base64.getEncoder().encodeToString(cipher.iv + encrypted)
    }

    private fun decryptSecure(encoded: String, key: String, authenticateKey: Boolean): String {
        val payload = Base64.getDecoder().decode(encoded)
        val iv = payload.copyOfRange(0, 12)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply {
            init(Cipher.DECRYPT_MODE, secretKey(), GCMParameterSpec(128, iv))
            if (authenticateKey) updateAAD(key.toByteArray(Charsets.UTF_8))
        }
        return String(cipher.doFinal(payload.copyOfRange(12, payload.size)), Charsets.UTF_8)
    }

    private fun removeSecure(invoke: Invoke) {
        val args = invoke.getArgs()
        val key = settingKey(args)
        persistSecure(invoke) {
            val preferences = activity.getSharedPreferences(storeName, Context.MODE_PRIVATE)
            val editor = preferences.edit()
            if (key.startsWith("github-pat:")) {
                val setting = args.getJSObject("setting") ?: throw IllegalArgumentException("setting")
                val marker = githubPatScopeKey(setting.getString("scopeId"), setting.getString("profileId"))
                if (githubPatScopeKeys(preferences.all.keys, setting.getString("profileId")).none { it != marker }) editor.remove(key)
                editor.remove(marker)
            } else editor.remove(key)
            editor.commit()
        }
    }

    private fun reconcileGitHubPats(invoke: Invoke) {
        val args = invoke.getArgs()
        val scopeId = args.getString("scopeId")
        val profileIdsJson = args.getJSONArray("profileIds")
        val profileIds = (0 until profileIdsJson.length()).map { index -> profileIdsJson.getString(index) }
        if (profileIds.size > 25 || profileIds.toSet().size != profileIds.size) throw IllegalArgumentException("profileIds")
        persistSecure(invoke) {
            val preferences = activity.getSharedPreferences(storeName, Context.MODE_PRIVATE)
            val editor = preferences.edit()
            profileIds.forEach { profileId ->
                val pat = "github-pat:$profileId"
                val marker = githubPatScopeKey(scopeId, profileId)
                if (preferences.contains(pat) && !preferences.contains(marker)) editor.putString(marker, encryptSecure("1", marker))
            }
            preferences.all.keys.filter { key -> key.startsWith("github-pat-scope:$scopeId:") }
                .map { marker -> marker to marker.removePrefix("github-pat-scope:$scopeId:") }
                .filter { (_, profileId) -> profileId !in profileIds }
                .forEach { (marker, profileId) ->
                    if (githubPatScopeKeys(preferences.all.keys, profileId).none { it != marker }) editor.remove("github-pat:$profileId")
                    editor.remove(marker)
                }
            editor.commit()
        }
    }

    private fun purgeSecure(invoke: Invoke) {
        val args = invoke.getArgs()
        val scope = args.getString("scope")
        val profileId = if (args.has("profileId")) args.getString("profileId") else null
        if (scope !in setOf("logout", "account-deletion", "api-change") || (scope != "logout" && profileId == null)) throw IllegalArgumentException("scope")
        val destructivePurge = scope in setOf("logout", "account-deletion")
        if (destructivePurge) {
            diagnosticsPurgesInProgress.incrementAndGet()
            try {
                diagnosticsExportPickerActive = false
                if (!cleanupPendingDiagnosticsExport()) {
                    diagnosticsPurgesInProgress.decrementAndGet()
                    invoke.reject("storage-failure", "storage-failure")
                    return
                }
            } catch (error: Exception) {
                diagnosticsPurgesInProgress.decrementAndGet()
                throw error
            }
        }
        persistSecure(invoke, onComplete = {
            if (destructivePurge) diagnosticsPurgesInProgress.decrementAndGet()
        }) {
            val preferences = activity.getSharedPreferences(storeName, Context.MODE_PRIVATE)
            val editor = preferences.edit()
            preferences.all.keys.filter { key ->
                scope == "logout" ||
                    (scope == "account-deletion" && key != "logto-session:$profileId") ||
                    (scope == "api-change" && key == "logto-session:$profileId")
            }.forEach(editor::remove)
            editor.commit()
        }
    }

    private fun persistSecure(invoke: Invoke, onComplete: () -> Unit = {}, operation: () -> Boolean) {
        try {
            secureSettingsExecutor.execute {
                try {
                    if (operation()) invoke.resolve(JSObject().put("kind", "ok"))
                    else invoke.reject("storage-failure", "storage-failure")
                } catch (error: Exception) {
                    invoke.reject("storage-failure", "storage-failure", error)
                } finally {
                    onComplete()
                }
            }
        } catch (error: Exception) {
            onComplete()
            throw error
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
