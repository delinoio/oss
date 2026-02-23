import { MpappClickButton, MpappErrorCode } from "../contracts/enums";
import {
  MpappAndroidHidNativeButton,
  MpappAndroidHidNativeErrorCode,
  type MpappAndroidHidNativeModule,
} from "../../modules/mpapp-android-hid";
import { AndroidNativeHidAdapter } from "../transport/android-native-hid-adapter";

function createNativeModule(
  overrides: Partial<MpappAndroidHidNativeModule> = {},
): MpappAndroidHidNativeModule {
  return {
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
