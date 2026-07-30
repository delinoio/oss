import {
  RefreshClientKind,
  RefreshOrigin,
} from "@delinoio/devhud-deck-connect";
import { describe, expect, it } from "vitest";

import {
  type DeckRefreshIdentity,
  type DeckRefreshTransport,
  refreshFromWidget,
} from "./refreshController";

describe("Deck widget refresh", () => {
  it("binds preflight and dispatch to one active widget request", async () => {
    const preflights: DeckRefreshIdentity[] = [];
    const refreshes: Array<DeckRefreshIdentity & { preflightToken: string }> = [];
    const transport: DeckRefreshTransport = {
      isAmbiguousRefreshError: () => false,
      async getPreflight(request) {
        preflights.push(request);
        return { priceUsdMicros: 50n, token: "widget-token" };
      },
      async refresh(request) {
        refreshes.push(request);
      },
    };

    await refreshFromWidget(
      "view",
      transport,
      () => "widget-request",
      new AbortController().signal,
    );

    expect(preflights).toEqual([
      {
        viewId: "view",
        requestId: "widget-request",
        origin: RefreshOrigin.WIDGET,
        clientKind: RefreshClientKind.WIDGET,
      },
    ]);
    expect(refreshes[0]).toMatchObject({
      requestId: "widget-request",
      origin: RefreshOrigin.WIDGET,
      clientKind: RefreshClientKind.WIDGET,
      preflightToken: "widget-token",
    });
  });

  it("does not dispatch after its active widget request is cancelled", async () => {
    const controller = new AbortController();
    let resolvePreflight:
      | ((value: { priceUsdMicros: bigint; token: string }) => void)
      | undefined;
    let dispatches = 0;
    const work = refreshFromWidget(
      "view",
      {
        isAmbiguousRefreshError: () => false,
        getPreflight: () =>
          new Promise((resolve) => {
            resolvePreflight = resolve;
          }),
        refresh: async () => {
          dispatches += 1;
        },
      },
      () => "widget-request",
      controller.signal,
    );

    controller.abort();
    resolvePreflight?.({ priceUsdMicros: 50n, token: "token" });
    await work;
    expect(dispatches).toBe(0);
  });
});
