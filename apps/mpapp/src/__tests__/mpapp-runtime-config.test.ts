import { MpappHidTransportMode } from "../contracts/enums";
import { resolveMpappRuntimeConfig } from "../config/mpapp-runtime-config";

describe("mpapp runtime config", () => {
  it("defaults to native Android HID when no config is provided", () => {
    const config = resolveMpappRuntimeConfig({
      env: {},
      expoMpappExtra: {},
    });

    expect(config.hidTransportMode).toBe(MpappHidTransportMode.NativeAndroidHid);
    expect(config.hidTargetHostAddress).toBeNull();
  });

  it("uses env override ahead of Expo extra values", () => {
    const config = resolveMpappRuntimeConfig({
      env: {
        EXPO_PUBLIC_MPAPP_HID_TRANSPORT_MODE: "stub",
        EXPO_PUBLIC_MPAPP_HID_TARGET_HOST_ADDRESS: "AA:BB:CC:DD:EE:FF",
      },
      expoMpappExtra: {
        hidTransportMode: "native-android-hid",
        hidTargetHostAddress: "11:22:33:44:55:66",
      },
    });

    expect(config.hidTransportMode).toBe(MpappHidTransportMode.Stub);
    expect(config.hidTargetHostAddress).toBe("AA:BB:CC:DD:EE:FF");
  });

  it("falls back to Expo extra when env values are invalid", () => {
    const config = resolveMpappRuntimeConfig({
      env: {
        EXPO_PUBLIC_MPAPP_HID_TRANSPORT_MODE: "invalid-mode",
      },
      expoMpappExtra: {
        hidTransportMode: "stub",
      },
    });

    expect(config.hidTransportMode).toBe(MpappHidTransportMode.Stub);
  });
});
