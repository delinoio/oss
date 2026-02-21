import { MpappErrorCode } from "../contracts/enums";
import type { PointerClickSample, PointerMoveSample, Result } from "../contracts/types";
import type { HidAdapter } from "./hid-adapter";

type AndroidHidStubAdapterOptions = {
  failConnect?: boolean;
  connectLatencyMs?: number;
  ioLatencyMs?: number;
};

const DEFAULT_CONNECT_LATENCY_MS = 60;
const DEFAULT_IO_LATENCY_MS = 5;

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}

export class AndroidHidStubAdapter implements HidAdapter {
  private readonly failConnect: boolean;
  private readonly connectLatencyMs: number;
  private readonly ioLatencyMs: number;
  private connected = false;

  constructor(options: AndroidHidStubAdapterOptions = {}) {
    this.failConnect = options.failConnect ?? false;
    this.connectLatencyMs = options.connectLatencyMs ?? DEFAULT_CONNECT_LATENCY_MS;
    this.ioLatencyMs = options.ioLatencyMs ?? DEFAULT_IO_LATENCY_MS;
  }

  public async pairAndConnect(): Promise<Result> {
    console.info("[mpapp][hid-stub] pairAndConnect:start");
    await delay(this.connectLatencyMs);

    if (this.failConnect) {
      console.warn("[mpapp][hid-stub] pairAndConnect:failed");
      return {
        ok: false,
        errorCode: MpappErrorCode.PairingTimeout,
        message: "Stub adapter configured to fail while connecting.",
      };
    }

    this.connected = true;
    console.info("[mpapp][hid-stub] pairAndConnect:success");
    return { ok: true };
  }

  public async disconnect(): Promise<Result> {
    console.info("[mpapp][hid-stub] disconnect");
    this.connected = false;
    await delay(this.ioLatencyMs);
    return { ok: true };
  }

  public async sendMove(sample: PointerMoveSample): Promise<Result> {
    await delay(this.ioLatencyMs);

    if (!this.connected) {
      return {
        ok: false,
        errorCode: MpappErrorCode.TransportFailure,
        message: "Cannot send movement when adapter is disconnected.",
      };
    }

    console.info("[mpapp][hid-stub] sendMove", sample.deltaX, sample.deltaY);
    return { ok: true };
  }

  public async sendClick(sample: PointerClickSample): Promise<Result> {
    await delay(this.ioLatencyMs);

    if (!this.connected) {
      return {
        ok: false,
        errorCode: MpappErrorCode.TransportFailure,
        message: "Cannot send click when adapter is disconnected.",
      };
    }

    console.info("[mpapp][hid-stub] sendClick", sample.actionId);
    return { ok: true };
  }
}
