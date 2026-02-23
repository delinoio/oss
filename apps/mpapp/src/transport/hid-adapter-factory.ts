import type { MpappRuntimeConfig } from "../config/mpapp-runtime-config";
import { MpappHidTransportMode } from "../contracts/enums";
import { AndroidHidStubAdapter, type AndroidHidStubAdapterOptions } from "./android-hid-stub-adapter";
import {
  AndroidNativeHidAdapter,
  type AndroidNativeHidAdapterOptions,
} from "./android-native-hid-adapter";
import type { HidAdapter } from "./hid-adapter";

export type CreateHidAdapterOptions = {
  runtimeConfig: MpappRuntimeConfig;
  stubOptions?: AndroidHidStubAdapterOptions;
  nativeOptions?: Omit<AndroidNativeHidAdapterOptions, "hostAddress">;
};

export function createHidAdapter(options: CreateHidAdapterOptions): HidAdapter {
  if (options.runtimeConfig.hidTransportMode === MpappHidTransportMode.Stub) {
    return new AndroidHidStubAdapter(options.stubOptions);
  }

  return new AndroidNativeHidAdapter({
    hostAddress: options.runtimeConfig.hidTargetHostAddress,
    ...options.nativeOptions,
  });
}
