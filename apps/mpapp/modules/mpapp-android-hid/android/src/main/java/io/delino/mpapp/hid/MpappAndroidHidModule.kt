package io.delino.mpapp.hid

import android.Manifest
import android.bluetooth.BluetoothAdapter
import android.bluetooth.BluetoothDevice
import android.bluetooth.BluetoothHidDevice
import android.bluetooth.BluetoothHidDeviceAppSdpSettings
import android.bluetooth.BluetoothManager
import android.bluetooth.BluetoothProfile
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.content.ContextCompat
import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

class MpappAndroidHidModule : Module() {
  private var hidDeviceProxy: BluetoothHidDevice? = null
  private var hidServiceListener: BluetoothProfile.ServiceListener? = null
  private var hidCallback: BluetoothHidDevice.Callback? = null
  private var connectedHost: BluetoothDevice? = null

  override fun definition() = ModuleDefinition {
    Name(MODULE_NAME)

    AsyncFunction("checkBluetoothAvailability") {
      checkBluetoothAvailability()
    }

    AsyncFunction("pairAndConnect") { hostAddress: String ->
      pairAndConnect(hostAddress)
    }

    AsyncFunction("disconnect") {
      disconnect()
    }

    AsyncFunction("sendMove") { deltaX: Double, deltaY: Double ->
      sendMove(deltaX, deltaY)
    }

    AsyncFunction("sendClick") { button: String ->
      sendClick(button)
    }

    OnDestroy {
      cleanup()
    }
  }

  private fun pairAndConnect(hostAddress: String): Map<String, Any?> {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.P) {
      return failure(
        code = NativeErrorCode.UnsupportedPlatform,
        message = "Bluetooth HID requires Android API 28+.",
      )
    }

    val context = appContext.reactContext
      ?: return failure(NativeErrorCode.TransportFailure, "React context is unavailable.")

    if (!hasBluetoothPermission(context, Manifest.permission.BLUETOOTH_CONNECT) ||
      !hasBluetoothPermission(context, Manifest.permission.BLUETOOTH_SCAN)
    ) {
      return failure(
        code = NativeErrorCode.PermissionDenied,
        message = "Bluetooth permissions are required before HID pairing.",
      )
    }

    val availabilityResult = checkBluetoothAvailability(context)
    if (availabilityResult["ok"] == false) {
      return availabilityResult
    }

    val bluetoothAdapter = getBluetoothAdapter(context)
      ?: return failure(
        code = NativeErrorCode.BluetoothUnavailable,
        message = "Bluetooth adapter is unavailable on this device.",
      )

    val normalizedAddress = hostAddress.trim()
    if (normalizedAddress.isEmpty()) {
      return failure(
        code = NativeErrorCode.HostAddressRequired,
        message = "A target host Bluetooth address is required.",
      )
    }

    if (!BLUETOOTH_ADDRESS_REGEX.matches(normalizedAddress)) {
      return failure(
        code = NativeErrorCode.InvalidHostAddress,
        message = "Target host Bluetooth address is invalid.",
      )
    }

    val hostDevice = try {
      bluetoothAdapter.getRemoteDevice(normalizedAddress)
    } catch (_: IllegalArgumentException) {
      return failure(
        code = NativeErrorCode.InvalidHostAddress,
        message = "Target host Bluetooth address is invalid.",
      )
    }

    val hidDevice = resolveHidDeviceProxy(bluetoothAdapter, context)
      ?: return failure(
        code = NativeErrorCode.UnsupportedPlatform,
        message = "HID profile is unavailable on this Android runtime.",
      )

    val registrationLatch = CountDownLatch(1)
    val connectionLatch = CountDownLatch(1)
    var registered = false
    var connected = false

    val callback = object : BluetoothHidDevice.Callback() {
      override fun onAppStatusChanged(pluggedDevice: BluetoothDevice?, registeredState: Boolean) {
        registered = registeredState
        registrationLatch.countDown()
      }

      override fun onConnectionStateChanged(device: BluetoothDevice?, state: Int) {
        if (device?.address != hostDevice.address) {
          return
        }

        when (state) {
          BluetoothProfile.STATE_CONNECTED -> {
            connected = true
            connectedHost = device
            connectionLatch.countDown()
          }

          BluetoothProfile.STATE_DISCONNECTED -> {
            connected = false
            connectionLatch.countDown()
          }
        }
      }
    }

    hidCallback = callback

    val registerRequested = try {
      hidDevice.registerApp(
        createSdpSettings(),
        null,
        null,
        context.mainExecutor,
        callback,
      )
    } catch (_: SecurityException) {
      return failure(
        code = NativeErrorCode.PermissionDenied,
        message = "Bluetooth permissions are required to register HID app.",
      )
    } catch (_: Throwable) {
      return failure(
        code = NativeErrorCode.TransportFailure,
        message = "Failed to register Android HID app.",
      )
    }

    if (!registerRequested) {
      return failure(
        code = NativeErrorCode.TransportFailure,
        message = "Android HID app registration was rejected.",
      )
    }

    val registrationCompleted = registrationLatch.await(
      APP_REGISTRATION_TIMEOUT_SECONDS,
      TimeUnit.SECONDS,
    )

    if (!registrationCompleted || !registered) {
      return failure(
        code = NativeErrorCode.PairingTimeout,
        message = "Timed out waiting for HID app registration.",
      )
    }

    val connectRequested = try {
      hidDevice.connect(hostDevice)
    } catch (_: SecurityException) {
      return failure(
        code = NativeErrorCode.PermissionDenied,
        message = "Bluetooth permissions are required to connect HID host.",
      )
    } catch (_: Throwable) {
      return failure(
        code = NativeErrorCode.TransportFailure,
        message = "Failed to initiate HID host connection.",
      )
    }

    if (!connectRequested) {
      return failure(
        code = NativeErrorCode.TransportFailure,
        message = "HID host connection request was rejected.",
      )
    }

    val connectedWithinTimeout = connectionLatch.await(
      CONNECTION_TIMEOUT_SECONDS,
      TimeUnit.SECONDS,
    )

    if (!connectedWithinTimeout || !connected) {
      return failure(
        code = NativeErrorCode.PairingTimeout,
        message = "Timed out waiting for HID host connection.",
      )
    }

    return success()
  }

  private fun checkBluetoothAvailability(): Map<String, Any?> {
    val context = appContext.reactContext
      ?: return failure(
        code = NativeErrorCode.TransportFailure,
        message = "React context is unavailable.",
        details = mapOf(
          "availabilityState" to BluetoothAvailabilityState.Unknown.value,
        ),
      )

    return checkBluetoothAvailability(context)
  }

  private fun checkBluetoothAvailability(context: Context): Map<String, Any?> {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.P) {
      return failure(
        code = NativeErrorCode.UnsupportedPlatform,
        message = "Bluetooth HID requires Android API 28+.",
        details = mapOf(
          "availabilityState" to BluetoothAvailabilityState.Unknown.value,
        ),
      )
    }

    val bluetoothAdapter = getBluetoothAdapter(context)
      ?: return failure(
        code = NativeErrorCode.BluetoothUnavailable,
        message = "Bluetooth adapter is unavailable on this device.",
        details = mapOf(
          "availabilityState" to BluetoothAvailabilityState.AdapterUnavailable.value,
        ),
      )

    if (!bluetoothAdapter.isEnabled) {
      return failure(
        code = NativeErrorCode.BluetoothUnavailable,
        message = "Bluetooth is disabled.",
        details = mapOf(
          "availabilityState" to BluetoothAvailabilityState.Disabled.value,
        ),
      )
    }

    return success(
      details = mapOf(
        "availabilityState" to BluetoothAvailabilityState.Available.value,
      ),
    )
  }

  private fun disconnect(): Map<String, Any?> {
    val hidDevice = hidDeviceProxy
      ?: return success()

    val host = connectedHost
    if (host != null) {
      try {
        hidDevice.disconnect(host)
      } catch (_: SecurityException) {
        return failure(
          code = NativeErrorCode.PermissionDenied,
          message = "Bluetooth permissions are required to disconnect HID host.",
        )
      } catch (_: Throwable) {
        return failure(
          code = NativeErrorCode.TransportFailure,
          message = "Failed to disconnect HID host.",
        )
      }
    }

    try {
      hidDevice.unregisterApp()
    } catch (_: Throwable) {
      // Ignore unregister errors while tearing down.
    }

    connectedHost = null

    return success()
  }

  private fun sendMove(deltaX: Double, deltaY: Double): Map<String, Any?> {
    val hidDevice = hidDeviceProxy
      ?: return failure(NativeErrorCode.TransportFailure, "HID device proxy is unavailable.")

    val host = connectedHost
      ?: return failure(NativeErrorCode.TransportFailure, "No connected HID host.")

    val moveReport = byteArrayOf(
      BUTTONS_NONE,
      toSignedByte(deltaX),
      toSignedByte(deltaY),
    )

    return sendMouseReport(hidDevice, host, moveReport)
  }

  private fun sendClick(button: String): Map<String, Any?> {
    val hidDevice = hidDeviceProxy
      ?: return failure(NativeErrorCode.TransportFailure, "HID device proxy is unavailable.")

    val host = connectedHost
      ?: return failure(NativeErrorCode.TransportFailure, "No connected HID host.")

    val buttonMask = when (button.trim().lowercase()) {
      "left" -> BUTTON_LEFT
      "right" -> BUTTON_RIGHT
      else -> {
        return failure(
          code = NativeErrorCode.TransportFailure,
          message = "Unsupported HID button '$button'.",
        )
      }
    }

    val pressedResult = sendMouseReport(
      hidDevice,
      host,
      byteArrayOf(buttonMask, 0, 0),
    )

    if (pressedResult["ok"] == false) {
      return pressedResult
    }

    return sendMouseReport(
      hidDevice,
      host,
      byteArrayOf(BUTTONS_NONE, 0, 0),
    )
  }

  private fun sendMouseReport(
    hidDevice: BluetoothHidDevice,
    host: BluetoothDevice,
    reportData: ByteArray,
  ): Map<String, Any?> {
    val sent = try {
      hidDevice.sendReport(host, REPORT_ID_MOUSE, reportData)
    } catch (_: SecurityException) {
      return failure(
        code = NativeErrorCode.PermissionDenied,
        message = "Bluetooth permissions are required to send HID report.",
      )
    } catch (_: Throwable) {
      return failure(
        code = NativeErrorCode.TransportFailure,
        message = "Failed to send HID report.",
      )
    }

    if (!sent) {
      return failure(
        code = NativeErrorCode.TransportFailure,
        message = "Android rejected HID report transmission.",
      )
    }

    return success()
  }

  private fun resolveHidDeviceProxy(
    bluetoothAdapter: BluetoothAdapter,
    context: Context,
  ): BluetoothHidDevice? {
    hidDeviceProxy?.let { return it }

    val latch = CountDownLatch(1)
    var resolvedProfile: BluetoothHidDevice? = null

    val serviceListener = object : BluetoothProfile.ServiceListener {
      override fun onServiceConnected(profile: Int, proxy: BluetoothProfile?) {
        if (profile == BluetoothProfile.HID_DEVICE) {
          resolvedProfile = proxy as? BluetoothHidDevice
          hidDeviceProxy = resolvedProfile
        }
        latch.countDown()
      }

      override fun onServiceDisconnected(profile: Int) {
        if (profile == BluetoothProfile.HID_DEVICE) {
          hidDeviceProxy = null
          connectedHost = null
        }
      }
    }

    hidServiceListener = serviceListener

    val requestAccepted = try {
      bluetoothAdapter.getProfileProxy(context, serviceListener, BluetoothProfile.HID_DEVICE)
    } catch (_: SecurityException) {
      return null
    }

    if (!requestAccepted) {
      return null
    }

    val connected = latch.await(PROFILE_PROXY_TIMEOUT_SECONDS, TimeUnit.SECONDS)
    if (!connected) {
      return null
    }

    return resolvedProfile
  }

  private fun getBluetoothAdapter(context: Context): BluetoothAdapter? {
    val manager = context.getSystemService(BluetoothManager::class.java) ?: return null
    return manager.adapter
  }

  private fun hasBluetoothPermission(context: Context, permission: String): Boolean {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) {
      return true
    }

    return ContextCompat.checkSelfPermission(context, permission) ==
      PackageManager.PERMISSION_GRANTED
  }

  private fun toSignedByte(value: Double): Byte {
    val rounded = value.toInt().coerceIn(-127, 127)
    return rounded.toByte()
  }

  private fun createSdpSettings(): BluetoothHidDeviceAppSdpSettings {
    return BluetoothHidDeviceAppSdpSettings(
      "mpapp Android Mouse",
      "Phone-based Bluetooth HID pointer",
      "delinoio",
      BluetoothHidDevice.SUBCLASS1_COMBO,
      MOUSE_REPORT_DESCRIPTOR,
    )
  }

  private fun cleanup() {
    try {
      disconnect()
    } catch (_: Throwable) {
      // Best-effort teardown.
    }

    val context = appContext.reactContext ?: return
    val bluetoothAdapter = getBluetoothAdapter(context) ?: return
    val hidProxy = hidDeviceProxy ?: return

    try {
      bluetoothAdapter.closeProfileProxy(BluetoothProfile.HID_DEVICE, hidProxy)
    } catch (_: Throwable) {
      // Ignore close failures during destroy.
    }

    hidDeviceProxy = null
    hidServiceListener = null
    hidCallback = null
    connectedHost = null
  }

  private fun success(details: Map<String, Any?> = emptyMap()): Map<String, Any?> {
    return if (details.isEmpty()) {
      mapOf("ok" to true)
    } else {
      mapOf(
        "ok" to true,
        "details" to details,
      )
    }
  }

  private fun failure(
    code: NativeErrorCode,
    message: String,
    details: Map<String, Any?> = emptyMap(),
  ): Map<String, Any?> {
    return mapOf(
      "ok" to false,
      "code" to code.value,
      "message" to message,
      "details" to details,
    )
  }

  private enum class NativeErrorCode(val value: String) {
    BluetoothUnavailable("bluetooth-unavailable"),
    PermissionDenied("permission-denied"),
    PairingTimeout("pairing-timeout"),
    UnsupportedPlatform("unsupported-platform"),
    TransportFailure("transport-failure"),
    HostAddressRequired("host-address-required"),
    InvalidHostAddress("invalid-host-address"),
  }

  private enum class BluetoothAvailabilityState(val value: String) {
    Available("available"),
    AdapterUnavailable("adapter-unavailable"),
    Disabled("disabled"),
    Unknown("unknown"),
  }

  companion object {
    private const val MODULE_NAME = "MpappAndroidHid"
    private const val PROFILE_PROXY_TIMEOUT_SECONDS = 3L
    private const val APP_REGISTRATION_TIMEOUT_SECONDS = 3L
    private const val CONNECTION_TIMEOUT_SECONDS = 10L
    // Descriptor does not define a Report ID (0x85), so report ID must be 0.
    private const val REPORT_ID_MOUSE = 0

    private const val BUTTONS_NONE: Byte = 0x00
    private const val BUTTON_LEFT: Byte = 0x01
    private const val BUTTON_RIGHT: Byte = 0x02

    private val BLUETOOTH_ADDRESS_REGEX =
      Regex("^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$")

    // USB HID mouse report descriptor (3-byte input: buttons, deltaX, deltaY).
    private val MOUSE_REPORT_DESCRIPTOR = byteArrayOf(
      0x05,
      0x01,
      0x09,
      0x02,
      0xA1.toByte(),
      0x01,
      0x09,
      0x01,
      0xA1.toByte(),
      0x00,
      0x05,
      0x09,
      0x19,
      0x01,
      0x29,
      0x03,
      0x15,
      0x00,
      0x25,
      0x01,
      0x95.toByte(),
      0x03,
      0x75,
      0x01,
      0x81.toByte(),
      0x02,
      0x95.toByte(),
      0x01,
      0x75,
      0x05,
      0x81.toByte(),
      0x03,
      0x05,
      0x01,
      0x09,
      0x30,
      0x09,
      0x31,
      0x15,
      0x81.toByte(),
      0x25,
      0x7F,
      0x75,
      0x08,
      0x95.toByte(),
      0x02,
      0x81.toByte(),
      0x06,
      0xC0.toByte(),
      0xC0.toByte(),
    )
  }
}
