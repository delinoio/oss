import type { PointerClickSample, PointerMoveSample, Result } from "../contracts/types";

export interface HidAdapter {
  pairAndConnect(): Promise<Result>;
  disconnect(): Promise<Result>;
  sendMove(sample: PointerMoveSample): Promise<Result>;
  sendClick(sample: PointerClickSample): Promise<Result>;
}
