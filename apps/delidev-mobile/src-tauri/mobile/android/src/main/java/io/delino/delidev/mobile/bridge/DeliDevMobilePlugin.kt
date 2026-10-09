// SPDX-License-Identifier: Apache-2.0
package io.delino.delidev.mobile.bridge
import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.AtomicFile
import androidx.appcompat.app.AppCompatActivity
import androidx.lifecycle.Lifecycle
import app.tauri.PermissionState
import app.tauri.annotation.Command
import app.tauri.annotation.Permission
import app.tauri.annotation.PermissionCallback
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.JSObject
import app.tauri.plugin.Plugin
import org.json.JSONObject
import java.io.File
import java.net.URI
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLException
import java.security.KeyStore
import java.util.concurrent.Executors
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

@TauriPlugin(permissions = [Permission(strings = [Manifest.permission.POST_NOTIFICATIONS], alias = "notifications")])
class DeliDevMobilePlugin(private val host: AppCompatActivity): Plugin(host) {
 private val queue = Executors.newSingleThreadExecutor()
 private val alias = "io.delino.delidev.mobile.state.v1"
 private val stateFile get() = AtomicFile(File(host.noBackupFilesDir, "protected-state-v1"))
 private fun key(): SecretKey {
  val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
  (store.getKey(alias, null) as? SecretKey)?.let { return it }
  return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").apply { init(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT).setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build()) }.generateKey()
 }
 private fun read(): String? {
  if (!stateFile.baseFile.exists()) return null
  val bytes = stateFile.readFully(); require(bytes.size in 29..(4*1024*1024+28))
  val cipher = Cipher.getInstance("AES/GCM/NoPadding"); cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, bytes.copyOfRange(0,12)))
  return cipher.doFinal(bytes.copyOfRange(12,bytes.size)).toString(Charsets.UTF_8)
 }
 private fun write(value: String) {
  val bytes = value.toByteArray(Charsets.UTF_8); require(bytes.size <= 4*1024*1024)
  val cipher = Cipher.getInstance("AES/GCM/NoPadding"); cipher.init(Cipher.ENCRYPT_MODE,key())
  val output = stateFile.startWrite()
  try { output.write(cipher.iv); output.write(cipher.doFinal(bytes)); stateFile.finishWrite(output) } catch(e: Exception) { stateFile.failWrite(output); throw e }
 }
 @Command fun request(invoke: Invoke) {
  val args = invoke.getArgs()
  when(args.getString("operation")) {
   "read-state", "write-state" -> queue.execute { try {
    if(args.getString("operation") == "read-state") invoke.resolve(JSObject().put("value", read() ?: JSONObject.NULL))
    else { write(args.getString("value")); invoke.resolve(JSObject().put("ok",true)) }
   } catch(e: Exception) { invoke.reject("Protected state is unavailable", "storage-failure") } }
   "tls" -> queue.execute {
    var connection: HttpsURLConnection? = null
    try {
     val uri = URI(args.getString("origin")); require(uri.scheme == "https" && uri.rawUserInfo == null && uri.rawQuery == null && uri.rawFragment == null && uri.rawPath.isEmpty())
     connection = uri.toURL().openConnection() as HttpsURLConnection
     connection.instanceFollowRedirects = false; connection.connectTimeout = 5000; connection.readTimeout = 5000; connection.requestMethod = "HEAD"
     connection.responseCode; invoke.resolve(JSObject().put("status", "ok"))
    } catch(e: SSLException) { invoke.resolve(JSObject().put("status", "certificate")) }
      catch(e: Exception) { invoke.resolve(JSObject().put("status", "network")) }
    finally { connection?.disconnect() }
   }
   "permission" -> { if(Build.VERSION.SDK_INT >= 33 && getPermissionState("notifications") != PermissionState.GRANTED) requestPermissionForAlias("notifications",invoke,"permissionResult") else permissionResult(invoke) }
   "notify" -> queue.execute { try {
    require(JSONObject(read() ?: "{}").optString("selectedProfile") == args.getString("profile_id"))
    host.runOnUiThread {
     val manager = host.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
     if(!host.lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED) || !manager.areNotificationsEnabled()) { invoke.resolve(JSObject().put("submitted",false)); return@runOnUiThread }
     manager.createNotificationChannel(NotificationChannel("foreground", "DeliDev", NotificationManager.IMPORTANCE_DEFAULT))
     val notification = android.app.Notification.Builder(host,"foreground").setSmallIcon(android.R.drawable.ic_dialog_info).setContentTitle("DeliDev").setContentText(if(args.getString("language") == "ko") "세션에 확인이 필요합니다." else "A session needs attention.").setAutoCancel(true).build()
     manager.notify(args.getString("inbox_id"),1,notification); invoke.resolve(JSObject().put("submitted",true))
    }
   } catch(e: Exception) { invoke.reject("Notification authority is unavailable", "storage-failure") } }
   else -> invoke.reject("Unsupported operation", "unsupported")
  }
 }
 @PermissionCallback private fun permissionResult(invoke: Invoke) { invoke.resolve(JSObject().put("granted", (host.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager).areNotificationsEnabled())) }
 override fun onDestroy(activity: AppCompatActivity) { queue.shutdown() }
}
