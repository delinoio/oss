import { MpappScreenId } from "../contracts/enums";

export type MpappScreenContract = {
  id: MpappScreenId;
  routePath: `/${string}`;
  deepLinkPath: string;
  title: string;
};

const MPAPP_SCREEN_CONTRACTS: Record<MpappScreenId, MpappScreenContract> = {
  [MpappScreenId.MainConsole]: {
    id: MpappScreenId.MainConsole,
    routePath: "/main",
    deepLinkPath: "main",
    title: "mpapp Android MVP",
  },
};

const MPAPP_SCREEN_ID_VALUES = Object.values(MpappScreenId);

export const DEFAULT_MPAPP_SCREEN_ID = MpappScreenId.MainConsole;

export function isMpappScreenId(value: unknown): value is MpappScreenId {
  return (
    typeof value === "string" &&
    MPAPP_SCREEN_ID_VALUES.includes(value as MpappScreenId)
  );
}

export function resolveMpappScreenId(value: unknown): MpappScreenId {
  if (isMpappScreenId(value)) {
    return value;
  }

  return DEFAULT_MPAPP_SCREEN_ID;
}

export function resolveMpappScreenContract(value: unknown): MpappScreenContract {
  const resolvedScreenId = resolveMpappScreenId(value);
  return MPAPP_SCREEN_CONTRACTS[resolvedScreenId];
}

export function listMpappScreenContracts(): MpappScreenContract[] {
  return MPAPP_SCREEN_ID_VALUES.map((screenId) => MPAPP_SCREEN_CONTRACTS[screenId]);
}
