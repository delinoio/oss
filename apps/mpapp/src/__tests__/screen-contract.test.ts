import { MpappScreenId } from "../contracts/enums";
import {
  DEFAULT_MPAPP_SCREEN_ID,
  listMpappScreenContracts,
  resolveMpappScreenContract,
  resolveMpappScreenId,
} from "../navigation/screen-contract";

describe("screen contract", () => {
  it("resolves the default screen id", () => {
    expect(DEFAULT_MPAPP_SCREEN_ID).toBe(MpappScreenId.MainConsole);

    const contract = resolveMpappScreenContract(DEFAULT_MPAPP_SCREEN_ID);
    expect(contract.id).toBe(MpappScreenId.MainConsole);
    expect(contract.routePath).toBe("/main");
    expect(contract.deepLinkPath).toBe("main");
  });

  it("falls back unknown screen id to default", () => {
    expect(resolveMpappScreenId("unknown-screen")).toBe(DEFAULT_MPAPP_SCREEN_ID);

    const fallbackContract = resolveMpappScreenContract("unknown-screen");
    expect(fallbackContract.id).toBe(DEFAULT_MPAPP_SCREEN_ID);
  });

  it("keeps route and deep-link identifiers stable", () => {
    const contracts = listMpappScreenContracts();
    expect(contracts).toEqual([
      {
        id: MpappScreenId.MainConsole,
        routePath: "/main",
        deepLinkPath: "main",
        title: "mpapp Android MVP",
      },
    ]);
  });
});
