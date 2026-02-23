import { MpappHidTransportMode } from "../contracts/enums";

const HID_TRANSPORT_MODE_ENV_KEY = "EXPO_PUBLIC_MPAPP_HID_TRANSPORT_MODE";
const HID_TARGET_HOST_ADDRESS_ENV_KEY = "EXPO_PUBLIC_MPAPP_HID_TARGET_HOST_ADDRESS";

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
  const environment = options.env ?? process.env;
  const expoMpappExtra = options.expoMpappExtra ?? readExpoMpappExtraFromConstants();

  const envTransportMode = parseTransportMode(
    environment[HID_TRANSPORT_MODE_ENV_KEY],
  );
  const extraTransportMode = parseTransportMode(expoMpappExtra.hidTransportMode);

  const envTargetHostAddress = parseHostAddress(
    environment[HID_TARGET_HOST_ADDRESS_ENV_KEY],
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
