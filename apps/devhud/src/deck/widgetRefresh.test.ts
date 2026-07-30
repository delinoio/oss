import {
  RefreshClientKind,
  RefreshOrigin,
} from "@delinoio/devhud-deck-connect";
import { describe, expect, it } from "vitest";

import {
  type DeckRefreshAttempt,
  type DeckRefreshAttemptStore,
  DeckWidgetRefreshController,
  type DeckRefreshIdentity,
  type DeckRefreshTransport,
} from "./refreshController";

function attemptStore(): DeckRefreshAttemptStore {
  const attempts = new Map<string, DeckRefreshAttempt>();
  return {
    get: (viewId) => attempts.get(viewId),
    set: (viewId, attempt) => {
      attempts.set(viewId, attempt);
    },
    delete: (viewId) => {
      attempts.delete(viewId);
    },
  };
}

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

    const controller = new DeckWidgetRefreshController(
      transport,
      () => "widget-request",
      attemptStore(),
    );
    await controller.refresh("view", new AbortController().signal);

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
    const refresh = new DeckWidgetRefreshController(
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
      attemptStore(),
    );
    const work = refresh.refresh("view", controller.signal);

    await Promise.resolve();
    controller.abort();
    resolvePreflight?.({ priceUsdMicros: 50n, token: "token" });
    await work;
    expect(dispatches).toBe(0);
  });

  it("clears a preflight attempt when cancellation wins the store write", async () => {
    const controller = new AbortController();
    const attempts = new Map<string, DeckRefreshAttempt>();
    let markSetStarted: (() => void) | undefined;
    let releaseSet: (() => void) | undefined;
    const setStarted = new Promise<void>((resolve) => {
      markSetStarted = resolve;
    });
    const store: DeckRefreshAttemptStore = {
      get: (viewId) => attempts.get(viewId),
      set: async (viewId, attempt) => {
        attempts.set(viewId, attempt);
        markSetStarted?.();
        await new Promise<void>((resolve) => {
          releaseSet = resolve;
        });
      },
      delete: (viewId) => {
        attempts.delete(viewId);
      },
    };
    let dispatches = 0;
    const refresh = new DeckWidgetRefreshController(
      {
        isAmbiguousRefreshError: () => false,
        getPreflight: async () => ({
          priceUsdMicros: 50n,
          token: "widget-token",
        }),
        refresh: async () => {
          dispatches += 1;
        },
      },
      () => "widget-request",
      store,
    );

    const work = refresh.refresh("view", controller.signal);
    await setStarted;
    controller.abort();
    releaseSet?.();
    await work;

    expect(dispatches).toBe(0);
    expect(attempts.has("view")).toBe(false);
  });

  it("retries an ambiguous failure with the same identity and token", async () => {
    const preflights: DeckRefreshIdentity[] = [];
    const refreshes: Array<
      DeckRefreshIdentity & { preflightToken: string }
    > = [];
    let sequence = 0;
    const transport: DeckRefreshTransport = {
      isAmbiguousRefreshError: () => true,
      getPreflight: async (request) => {
        preflights.push(request);
        return { priceUsdMicros: 50n, token: `token-${request.requestId}` };
      },
      refresh: async (request) => {
        refreshes.push(request);
        if (refreshes.length === 1) {
          throw new Error("response lost");
        }
      },
    };
    const attempts = attemptStore();
    const createController = () =>
      new DeckWidgetRefreshController(
        transport,
        () => `widget-request-${++sequence}`,
        attempts,
      );

    await expect(
      createController().refresh("view", new AbortController().signal),
    ).rejects.toThrow("response lost");
    await createController().refresh("view", new AbortController().signal);

    expect(preflights).toHaveLength(1);
    expect(refreshes).toHaveLength(2);
    expect(refreshes[1]).toEqual(refreshes[0]);
  });
});
