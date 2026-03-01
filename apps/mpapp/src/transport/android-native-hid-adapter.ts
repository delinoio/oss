import {
  MpappBluetoothAvailabilityState,
  MpappClickButton,
  MpappErrorCode,
} from "../contracts/enums";
import type {
  BluetoothAvailabilityResult,
  PointerClickSample,
  PointerMoveSample,
  Result,
} from "../contracts/types";
import type { HidAdapter } from "./hid-adapter";
import {
  getMpappAndroidHidNativeModule,
  MpappAndroidHidNativeAvailabilityState,
  MpappAndroidHidNativeButton,
  MpappAndroidHidNativeErrorCode,
  type MpappAndroidHidNativeModule,
  type MpappAndroidHidNativeResult,
} from "../../modules/mpapp-android-hid";

export type AndroidNativeHidAdapterOptions = {
  hostAddress: string | null;
  nativeModule?: MpappAndroidHidNativeModule;
};

const BLUETOOTH_ADDRESS_REGEX = /^([0-9A-F]{2}:){5}[0-9A-F]{2}$/i;

function mapNativeErrorCode(nativeErrorCode: string): MpappErrorCode {
  switch (nativeErrorCode) {
    case MpappAndroidHidNativeErrorCode.BluetoothUnavailable:
      return MpappErrorCode.BluetoothUnavailable;
    case MpappAndroidHidNativeErrorCode.PermissionDenied:
      return MpappErrorCode.PermissionDenied;
    case MpappAndroidHidNativeErrorCode.PairingTimeout:
      return MpappErrorCode.PairingTimeout;
    case MpappAndroidHidNativeErrorCode.UnsupportedPlatform:
      return MpappErrorCode.UnsupportedPlatform;
    case MpappAndroidHidNativeErrorCode.HostAddressRequired:
    case MpappAndroidHidNativeErrorCode.InvalidHostAddress:
    case MpappAndroidHidNativeErrorCode.TransportFailure:
    default:
      return MpappErrorCode.TransportFailure;
  }
}

function mapNativeResult(nativeResult: MpappAndroidHidNativeResult): Result {
  if (nativeResult.ok) {
    return { ok: true };
  }

  return {
    ok: false,
    errorCode: mapNativeErrorCode(nativeResult.code),
    message: nativeResult.message,
    nativeErrorCode: nativeResult.code,
  };
}

function parseNativeAvailabilityState(
  nativeResult: MpappAndroidHidNativeResult,
): MpappBluetoothAvailabilityState {
  const availabilityState = nativeResult.details?.availabilityState;

  switch (availabilityState) {
    case MpappAndroidHidNativeAvailabilityState.Available:
      return MpappBluetoothAvailabilityState.Available;
    case MpappAndroidHidNativeAvailabilityState.AdapterUnavailable:
      return MpappBluetoothAvailabilityState.AdapterUnavailable;
    case MpappAndroidHidNativeAvailabilityState.Disabled:
      return MpappBluetoothAvailabilityState.Disabled;
    case MpappAndroidHidNativeAvailabilityState.Unknown:
      return MpappBluetoothAvailabilityState.Unknown;
    default:
      return nativeResult.ok
        ? MpappBluetoothAvailabilityState.Available
        : MpappBluetoothAvailabilityState.Unknown;
  }
}

function createModuleUnavailableResult() {
  return {
    ok: false as const,
    errorCode: MpappErrorCode.UnsupportedPlatform,
    message: "Native Android HID module is unavailable on this runtime.",
    nativeErrorCode: MpappAndroidHidNativeErrorCode.UnsupportedPlatform,
  };
}

function maskBluetoothAddress(hostAddress: string): string {
  if (!BLUETOOTH_ADDRESS_REGEX.test(hostAddress)) {
    return "invalid";
  }

  return `${hostAddress.slice(0, 8)}:**:**:**`;
}

export class AndroidNativeHidAdapter implements HidAdapter {
  private readonly hostAddress: string | null;
  private readonly nativeModule: MpappAndroidHidNativeModule | null;

  constructor(options: AndroidNativeHidAdapterOptions) {
    this.hostAddress = options.hostAddress;

    if (options.nativeModule) {
      this.nativeModule = options.nativeModule;
      return;
    }

    try {
      this.nativeModule = getMpappAndroidHidNativeModule();
    } catch (error) {
      this.nativeModule = null;
      console.warn("[mpapp][hid-native] module unavailable", {
        message: error instanceof Error ? error.message : String(error),
      });
    }
  }

  public async checkBluetoothAvailability(): Promise<BluetoothAvailabilityResult> {
    if (!this.nativeModule) {
      return {
        ...createModuleUnavailableResult(),
        availabilityState: MpappBluetoothAvailabilityState.Unknown,
      };
    }

    try {
      const nativeResult = await this.nativeModule.checkBluetoothAvailability();
      const availabilityState = parseNativeAvailabilityState(nativeResult);

      if (nativeResult.ok) {
        return {
          ok: true,
          availabilityState: MpappBluetoothAvailabilityState.Available,
        };
      }

      const failureAvailabilityState =
        availabilityState === MpappBluetoothAvailabilityState.Available
          ? MpappBluetoothAvailabilityState.Unknown
          : availabilityState;

      return {
        ok: false,
        availabilityState: failureAvailabilityState,
        errorCode: mapNativeErrorCode(nativeResult.code),
        message: nativeResult.message,
        nativeErrorCode: nativeResult.code,
      };
    } catch (error) {
      return {
        ok: false,
        availabilityState: MpappBluetoothAvailabilityState.Unknown,
        errorCode: MpappErrorCode.TransportFailure,
        message: `Native HID availability check failed: ${error instanceof Error ? error.message : String(error)}`,
        nativeErrorCode: MpappAndroidHidNativeErrorCode.TransportFailure,
      };
    }
  }

  public async pairAndConnect(): Promise<Result> {
    console.info("[mpapp][hid-native] pairAndConnect:start", {
      hostConfigured: Boolean(this.hostAddress),
      hostMasked: this.hostAddress ? maskBluetoothAddress(this.hostAddress) : null,
    });

    if (!this.hostAddress) {
      return {
        ok: false,
        errorCode: MpappErrorCode.TransportFailure,
        message: "A target host Bluetooth address is required for native HID mode.",
        nativeErrorCode: MpappAndroidHidNativeErrorCode.HostAddressRequired,
      };
    }

    if (!BLUETOOTH_ADDRESS_REGEX.test(this.hostAddress)) {
      return {
        ok: false,
        errorCode: MpappErrorCode.TransportFailure,
        message: "Configured target host Bluetooth address is invalid.",
        nativeErrorCode: MpappAndroidHidNativeErrorCode.InvalidHostAddress,
      };
    }

    if (!this.nativeModule) {
      return createModuleUnavailableResult();
    }

    try {
      const nativeResult = await this.nativeModule.pairAndConnect(this.hostAddress);
      return mapNativeResult(nativeResult);
    } catch (error) {
      return {
        ok: false,
        errorCode: MpappErrorCode.TransportFailure,
        message: `Native HID connection failed: ${error instanceof Error ? error.message : String(error)}`,
        nativeErrorCode: MpappAndroidHidNativeErrorCode.TransportFailure,
      };
    }
  }

  public async disconnect(): Promise<Result> {
    if (!this.nativeModule) {
      return createModuleUnavailableResult();
    }

    try {
      const nativeResult = await this.nativeModule.disconnect();
      return mapNativeResult(nativeResult);
    } catch (error) {
      return {
        ok: false,
        errorCode: MpappErrorCode.TransportFailure,
        message: `Native HID disconnect failed: ${error instanceof Error ? error.message : String(error)}`,
        nativeErrorCode: MpappAndroidHidNativeErrorCode.TransportFailure,
      };
    }
  }

  public async sendMove(sample: PointerMoveSample): Promise<Result> {
    if (!this.nativeModule) {
      return createModuleUnavailableResult();
    }

    try {
      const nativeResult = await this.nativeModule.sendMove(sample.deltaX, sample.deltaY);
      return mapNativeResult(nativeResult);
    } catch (error) {
      return {
        ok: false,
        errorCode: MpappErrorCode.TransportFailure,
        message: `Native HID move failed: ${error instanceof Error ? error.message : String(error)}`,
        nativeErrorCode: MpappAndroidHidNativeErrorCode.TransportFailure,
      };
    }
  }

  public async sendClick(sample: PointerClickSample): Promise<Result> {
    if (!this.nativeModule) {
      return createModuleUnavailableResult();
    }

    const nativeButton =
      sample.button === MpappClickButton.Left
        ? MpappAndroidHidNativeButton.Left
        : MpappAndroidHidNativeButton.Right;

    try {
      const nativeResult = await this.nativeModule.sendClick(nativeButton);
      return mapNativeResult(nativeResult);
    } catch (error) {
      return {
        ok: false,
        errorCode: MpappErrorCode.TransportFailure,
        message: `Native HID click failed: ${error instanceof Error ? error.message : String(error)}`,
        nativeErrorCode: MpappAndroidHidNativeErrorCode.TransportFailure,
      };
    }
  }
}
