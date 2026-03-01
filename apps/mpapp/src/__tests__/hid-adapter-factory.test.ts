import { MpappHidTransportMode } from "../contracts/enums";
import { AndroidHidStubAdapter } from "../transport/android-hid-stub-adapter";
import { AndroidNativeHidAdapter } from "../transport/android-native-hid-adapter";
import { createHidAdapter } from "../transport/hid-adapter-factory";

describe("hid adapter factory", () => {
  it("creates native adapter when runtime config is native mode", () => {
    const adapter = createHidAdapter({
      runtimeConfig: {
        hidTransportMode: MpappHidTransportMode.NativeAndroidHid,
        hidTargetHostAddress: "AA:BB:CC:DD:EE:FF",
      },
      nativeOptions: {
        nativeModule: {
          checkBluetoothAvailability: async () => ({ ok: true }),
          pairAndConnect: async () => ({ ok: true }),
          disconnect: async () => ({ ok: true }),
          sendMove: async () => ({ ok: true }),
          sendClick: async () => ({ ok: true }),
        },
      },
    });

    expect(adapter).toBeInstanceOf(AndroidNativeHidAdapter);
  });

  it("creates stub adapter when runtime config is stub mode", () => {
    const adapter = createHidAdapter({
      runtimeConfig: {
        hidTransportMode: MpappHidTransportMode.Stub,
        hidTargetHostAddress: null,
      },
    });

    expect(adapter).toBeInstanceOf(AndroidHidStubAdapter);
  });
});
