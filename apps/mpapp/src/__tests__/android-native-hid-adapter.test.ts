import {
  MpappBluetoothAvailabilityState,
  MpappClickButton,
  MpappErrorCode,
} from "../contracts/enums";
import {
  MpappAndroidHidNativeAvailabilityState,
  MpappAndroidHidNativeButton,
  MpappAndroidHidNativeErrorCode,
  type MpappAndroidHidNativeModule,
} from "../../modules/mpapp-android-hid";
import { AndroidNativeHidAdapter } from "../transport/android-native-hid-adapter";

function createNativeModule(
  overrides: Partial<MpappAndroidHidNativeModule> = {},
): MpappAndroidHidNativeModule {
  return {
    checkBluetoothAvailability: async () => ({
      ok: true,
      details: {
        availabilityState: MpappAndroidHidNativeAvailabilityState.Available,
      },
    }),
    pairAndConnect: async () => ({ ok: true }),
    disconnect: async () => ({ ok: true }),
    sendMove: async () => ({ ok: true }),
    sendClick: async () => ({ ok: true }),
    ...overrides,
  };
}

describe("android native HID adapter", () => {
  it("handles success path for connect, move, click, and disconnect", async () => {
    const nativeModule = createNativeModule();
    const adapter = new AndroidNativeHidAdapter({
      hostAddress: "AA:BB:CC:DD:EE:FF",
      nativeModule,
    });

    await expect(adapter.pairAndConnect()).resolves.toEqual({ ok: true });
    await expect(
      adapter.sendMove({
        actionId: "move" as const,
        deltaX: 3,
        deltaY: -2,
        sensitivity: 1,
        timestampMs: 1,
      }),
    ).resolves.toEqual({ ok: true });
    await expect(
      adapter.sendClick({
        actionId: "left-click" as const,
        button: MpappClickButton.Left,
        timestampMs: 2,
      }),
    ).resolves.toEqual({ ok: true });
    await expect(adapter.disconnect()).resolves.toEqual({ ok: true });
  });

  it("maps native error codes to mpapp error codes", async () => {
    const nativeCodesToMpappCodes: Array<[string, MpappErrorCode]> = [
      [MpappAndroidHidNativeErrorCode.BluetoothUnavailable, MpappErrorCode.BluetoothUnavailable],
      [MpappAndroidHidNativeErrorCode.PermissionDenied, MpappErrorCode.PermissionDenied],
      [MpappAndroidHidNativeErrorCode.PairingTimeout, MpappErrorCode.PairingTimeout],
      [MpappAndroidHidNativeErrorCode.UnsupportedPlatform, MpappErrorCode.UnsupportedPlatform],
      [MpappAndroidHidNativeErrorCode.TransportFailure, MpappErrorCode.TransportFailure],
      [MpappAndroidHidNativeErrorCode.HostAddressRequired, MpappErrorCode.TransportFailure],
      [MpappAndroidHidNativeErrorCode.InvalidHostAddress, MpappErrorCode.TransportFailure],
    ];

    for (const [nativeCode, expectedMpappCode] of nativeCodesToMpappCodes) {
      const nativeModule = createNativeModule({
        pairAndConnect: async () => ({
          ok: false,
          code: nativeCode,
          message: `native failure: ${nativeCode}`,
        }),
      });

      const adapter = new AndroidNativeHidAdapter({
        hostAddress: "AA:BB:CC:DD:EE:FF",
        nativeModule,
      });

      const result = await adapter.pairAndConnect();
      expect(result).toEqual({
        ok: false,
        errorCode: expectedMpappCode,
        message: `native failure: ${nativeCode}`,
        nativeErrorCode: nativeCode,
      });
    }
  });

  it("maps adapter-unavailable availability check failures", async () => {
    const nativeModule = createNativeModule({
      checkBluetoothAvailability: async () => ({
        ok: false,
        code: MpappAndroidHidNativeErrorCode.BluetoothUnavailable,
        message: "Bluetooth adapter is unavailable on this device.",
        details: {
          availabilityState:
            MpappAndroidHidNativeAvailabilityState.AdapterUnavailable,
        },
      }),
    });
    const adapter = new AndroidNativeHidAdapter({
      hostAddress: "AA:BB:CC:DD:EE:FF",
      nativeModule,
    });

    await expect(adapter.checkBluetoothAvailability()).resolves.toEqual({
      ok: false,
      availabilityState: MpappBluetoothAvailabilityState.AdapterUnavailable,
      errorCode: MpappErrorCode.BluetoothUnavailable,
      message: "Bluetooth adapter is unavailable on this device.",
      nativeErrorCode: MpappAndroidHidNativeErrorCode.BluetoothUnavailable,
    });
  });

  it("maps disabled availability check failures", async () => {
    const nativeModule = createNativeModule({
      checkBluetoothAvailability: async () => ({
        ok: false,
        code: MpappAndroidHidNativeErrorCode.BluetoothUnavailable,
        message: "Bluetooth is disabled.",
        details: {
          availabilityState: MpappAndroidHidNativeAvailabilityState.Disabled,
        },
      }),
    });
    const adapter = new AndroidNativeHidAdapter({
      hostAddress: "AA:BB:CC:DD:EE:FF",
      nativeModule,
    });

    await expect(adapter.checkBluetoothAvailability()).resolves.toEqual({
      ok: false,
      availabilityState: MpappBluetoothAvailabilityState.Disabled,
      errorCode: MpappErrorCode.BluetoothUnavailable,
      message: "Bluetooth is disabled.",
      nativeErrorCode: MpappAndroidHidNativeErrorCode.BluetoothUnavailable,
    });
  });

  it("returns deterministic errors for missing or invalid host config", async () => {
    const nativeModule = createNativeModule();

    const missingHostAdapter = new AndroidNativeHidAdapter({
      hostAddress: null,
      nativeModule,
    });
    await expect(missingHostAdapter.pairAndConnect()).resolves.toMatchObject({
      ok: false,
      errorCode: MpappErrorCode.TransportFailure,
      nativeErrorCode: MpappAndroidHidNativeErrorCode.HostAddressRequired,
    });

    const invalidHostAdapter = new AndroidNativeHidAdapter({
      hostAddress: "invalid-address",
      nativeModule,
    });
    await expect(invalidHostAdapter.pairAndConnect()).resolves.toMatchObject({
      ok: false,
      errorCode: MpappErrorCode.TransportFailure,
      nativeErrorCode: MpappAndroidHidNativeErrorCode.InvalidHostAddress,
    });
  });

  it("translates native click payload to expected native button enum", async () => {
    const sentButtons: MpappAndroidHidNativeButton[] = [];
    const nativeModule = createNativeModule({
      sendClick: async (button) => {
        sentButtons.push(button);
        return { ok: true };
      },
    });

    const adapter = new AndroidNativeHidAdapter({
      hostAddress: "AA:BB:CC:DD:EE:FF",
      nativeModule,
    });

    await adapter.sendClick({
      actionId: "left-click" as const,
      button: MpappClickButton.Left,
      timestampMs: 1,
    });

    await adapter.sendClick({
      actionId: "right-click" as const,
      button: MpappClickButton.Right,
      timestampMs: 2,
    });

    expect(sentButtons).toEqual([
      MpappAndroidHidNativeButton.Left,
      MpappAndroidHidNativeButton.Right,
    ]);
  });
});
