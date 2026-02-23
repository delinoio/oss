import { MpappHidTransportMode } from "../contracts/enums";

type RuntimeEnvironment = Record<string, string | undefined>;

type ExpoMpappExtra = {
  hidTransportMode?: unknown;
  hidTargetHostAddress?: unknown;
};

type ExpoConstantsShape = {
  expoConfig?: {
    extra?: {
      mpapp?: ExpoMpappExtra;
    };
  };
};

export type MpappRuntimeConfig = {
  hidTransportMode: MpappHidTransportMode;
  hidTargetHostAddress: string | null;
};

export type ResolveMpappRuntimeConfigOptions = {
  env?: RuntimeEnvironment;
  expoMpappExtra?: ExpoMpappExtra;
};

function parseTransportMode(value: unknown): MpappHidTransportMode | null {
  if (typeof value !== "string") {
    return null;
  }

  const normalizedValue = value.trim().toLowerCase();
  if (normalizedValue === MpappHidTransportMode.NativeAndroidHid) {
    return MpappHidTransportMode.NativeAndroidHid;
  }

  if (normalizedValue === MpappHidTransportMode.Stub) {
    return MpappHidTransportMode.Stub;
  }

  return null;
}

function parseHostAddress(value: unknown): string | null {
  if (typeof value !== "string") {
    return null;
  }

  const normalizedValue = value.trim();
  if (!normalizedValue) {
    return null;
  }

  return normalizedValue;
}

function readExpoMpappExtraFromConstants(): ExpoMpappExtra {
  try {
    const moduleRef = require("expo-constants");
    const constants = (moduleRef?.default ?? moduleRef) as
      | ExpoConstantsShape
      | undefined;
    const mpappExtra = constants?.expoConfig?.extra?.mpapp;

    if (mpappExtra && typeof mpappExtra === "object") {
      return mpappExtra;
    }
  } catch {
    // Expo constants may be unavailable during node-only tests.
  }

  return {};
}

export function resolveMpappRuntimeConfig(
  options: ResolveMpappRuntimeConfigOptions = {},
): MpappRuntimeConfig {
  const explicitEnvironment = options.env;
  const expoMpappExtra = options.expoMpappExtra ?? readExpoMpappExtraFromConstants();

  // Keep static EXPO_PUBLIC_* access so Expo can inline values in app bundles.
  const envTransportMode = parseTransportMode(
    explicitEnvironment?.EXPO_PUBLIC_MPAPP_HID_TRANSPORT_MODE ??
      process.env.EXPO_PUBLIC_MPAPP_HID_TRANSPORT_MODE,
  );
  const extraTransportMode = parseTransportMode(expoMpappExtra.hidTransportMode);

  // Keep static EXPO_PUBLIC_* access so Expo can inline values in app bundles.
  const envTargetHostAddress = parseHostAddress(
    explicitEnvironment?.EXPO_PUBLIC_MPAPP_HID_TARGET_HOST_ADDRESS ??
      process.env.EXPO_PUBLIC_MPAPP_HID_TARGET_HOST_ADDRESS,
  );
  const extraTargetHostAddress = parseHostAddress(
    expoMpappExtra.hidTargetHostAddress,
  );

  return {
    hidTransportMode:
      envTransportMode ??
      extraTransportMode ??
      MpappHidTransportMode.NativeAndroidHid,
    hidTargetHostAddress: envTargetHostAddress ?? extraTargetHostAddress ?? null,
  };
}
