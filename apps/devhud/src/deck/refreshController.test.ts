import {
  RefreshClientKind,
  RefreshOrigin,
} from "@delinoio/devhud-deck-connect";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  DECK_REFRESH_INTERVAL_MS,
  DeckRefreshController,
  type DeckRefreshAttempt,
  type DeckRefreshAttemptStore,
  type DeckRefreshIdentity,
  type DeckRefreshTransport,
  isAutomaticRefreshEligible,
} from "./refreshController";

function attemptStore(): DeckRefreshAttemptStore {
  const attempts = new Map<string, DeckRefreshAttempt>();
  return {
    get: (viewId) => attempts.get(viewId),
    set: (viewId, attempt) => {
      attempts.set(viewId, attempt);
    },
    deleteIfMatches: (viewId, attempt) => {
      if (attempts.get(viewId)?.request.requestId === attempt.request.requestId) {
        attempts.delete(viewId);
      }
    },
  };
}

function attemptStoreWithDeferredFirstSet() {
  const attempts = new Map<string, DeckRefreshAttempt>();
  let finishFirstSet: (() => void) | undefined;
  let announceFirstSet: (() => void) | undefined;
  const firstSetStarted = new Promise<void>((resolve) => {
    announceFirstSet = resolve;
  });
  let deferSet = true;
  const store: DeckRefreshAttemptStore = {
    get: (viewId) => attempts.get(viewId),
    set: (viewId, attempt) => {
      if (!deferSet) {
        attempts.set(viewId, attempt);
        return;
      }
      deferSet = false;
      announceFirstSet?.();
      return new Promise<void>((resolve) => {
        finishFirstSet = () => {
          attempts.set(viewId, attempt);
          resolve();
        };
      });
    },
    deleteIfMatches: (viewId, attempt) => {
      if (attempts.get(viewId)?.request.requestId === attempt.request.requestId) {
        attempts.delete(viewId);
      }
    },
  };
  return {
    store,
    firstSetStarted,
    finishFirstSet: () => {
      if (finishFirstSet === undefined) {
        throw new Error("first attempt-store write has not started");
      }
      finishFirstSet();
    },
    peek: (viewId: string) => attempts.get(viewId),
  };
}

function controllerAttemptStores(): Pick<
  ConstructorParameters<typeof DeckRefreshController>[0],
  "automaticAttempts" | "manualAttempts"
> {
  return {
    automaticAttempts: attemptStore(),
    manualAttempts: attemptStore(),
  };
}

function transportRecorder() {
  const preflights: DeckRefreshIdentity[] = [];
  const refreshes: Array<DeckRefreshIdentity & { preflightToken: string }> = [];
  const transport: DeckRefreshTransport = {
    isAmbiguousRefreshError: () => false,
    async getPreflight(request) {
      preflights.push(request);
      return { priceUsdMicros: 50n, token: `token-${request.requestId}` };
    },
    async refresh(request) {
      refreshes.push(request);
    },
  };
  return { preflights, refreshes, transport };
}

describe("Deck client-owned refresh polling", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-30T00:00:00Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("polls only attached or recently opened views every five minutes", async () => {
    const recorded = transportRecorder();
    let sequence = 0;
    const controller = new DeckRefreshController({
      ...controllerAttemptStores(),
      clientKind: RefreshClientKind.DESKTOP,
      transport: recorded.transport,
      createRequestId: () => `request-${++sequence}`,
      canPoll: () => true,
      listCandidates: async () => [
        {
          viewId: "widget",
          notificationAttached: false,
          shortcutAttached: false,
          widgetAttached: true,
        },
        {
          viewId: "recent",
          lastOpenedAt: new Date("2026-07-01T00:00:01Z"),
          notificationAttached: false,
          shortcutAttached: false,
          widgetAttached: false,
        },
        {
          viewId: "old",
          lastOpenedAt: new Date("2026-06-29T23:59:59Z"),
          notificationAttached: false,
          shortcutAttached: false,
          widgetAttached: false,
        },
      ],
    });

    controller.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(recorded.refreshes.map((request) => request.viewId)).toEqual([
      "widget",
      "recent",
    ]);
    expect(
      recorded.refreshes.every(
        (request) =>
          request.origin === RefreshOrigin.AUTOMATIC &&
          request.clientKind === RefreshClientKind.DESKTOP,
      ),
    ).toBe(true);

    await vi.advanceTimersByTimeAsync(DECK_REFRESH_INTERVAL_MS - 1);
    expect(recorded.refreshes).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(recorded.refreshes).toHaveLength(4);
    controller.stop();
  });

  it("makes no requests while permission is absent or after the client stops", async () => {
    const recorded = transportRecorder();
    let permitted = false;
    const controller = new DeckRefreshController({
      ...controllerAttemptStores(),
      clientKind: RefreshClientKind.OS_BACKGROUND_TASK,
      transport: recorded.transport,
      createRequestId: () => "request",
      canPoll: () => permitted,
      listCandidates: async () => [
        {
          viewId: "view",
          notificationAttached: true,
          shortcutAttached: false,
          widgetAttached: false,
        },
      ],
    });

    controller.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(recorded.preflights).toHaveLength(0);
    permitted = true;
    await vi.advanceTimersByTimeAsync(DECK_REFRESH_INTERVAL_MS);
    expect(recorded.refreshes).toHaveLength(1);

    controller.stop();
    await vi.advanceTimersByTimeAsync(DECK_REFRESH_INTERVAL_MS * 3);
    expect(recorded.refreshes).toHaveLength(1);
  });

  it("aborts between preflight and dispatch when the client stops", async () => {
    let resolvePreflight:
      | ((value: { priceUsdMicros: bigint; token: string }) => void)
      | undefined;
    let sequence = 0;
    const preflights: string[] = [];
    const providerDispatches: string[] = [];
    const controller = new DeckRefreshController({
      ...controllerAttemptStores(),
      clientKind: RefreshClientKind.MOBILE,
      createRequestId: () => `request-${++sequence}`,
      canPoll: () => true,
      listCandidates: async () => [
        {
          viewId: "view",
          notificationAttached: true,
          shortcutAttached: false,
          widgetAttached: false,
        },
      ],
      transport: {
        isAmbiguousRefreshError: () => false,
        getPreflight: (request) => {
          preflights.push(request.requestId);
          if (preflights.length > 1) {
            return Promise.resolve({
              priceUsdMicros: 50n,
              token: `token-${request.requestId}`,
            });
          }
          return new Promise((resolve) => {
            resolvePreflight = resolve;
          });
        },
        refresh: async (request) => {
          providerDispatches.push(request.requestId);
        },
      },
    });

    controller.start();
    await vi.advanceTimersByTimeAsync(0);
    controller.stop();
    resolvePreflight?.({ priceUsdMicros: 50n, token: "token" });
    await Promise.resolve();
    await Promise.resolve();
    expect(providerDispatches).toHaveLength(0);

    controller.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(preflights).toEqual(["request-1", "request-2"]);
    expect(providerDispatches).toEqual(["request-2"]);
    controller.stop();
  });

  it("removes an automatic attempt when cancellation wins its store write", async () => {
    const automaticAttempts = attemptStoreWithDeferredFirstSet();
    const preflights: string[] = [];
    const refreshes: string[] = [];
    let attached = true;
    let sequence = 0;
    const controller = new DeckRefreshController({
      automaticAttempts: automaticAttempts.store,
      manualAttempts: attemptStore(),
      clientKind: RefreshClientKind.DESKTOP,
      createRequestId: () => `request-${++sequence}`,
      canPoll: () => true,
      listCandidates: async () => [
        {
          viewId: "view",
          notificationAttached: attached,
          shortcutAttached: false,
          widgetAttached: false,
        },
      ],
      transport: {
        isAmbiguousRefreshError: () => false,
        getPreflight: async (request) => {
          preflights.push(request.requestId);
          return { priceUsdMicros: 50n, token: "token" };
        },
        refresh: async (request) => {
          refreshes.push(request.requestId);
        },
      },
    });

    controller.start();
    await vi.advanceTimersByTimeAsync(0);
    await automaticAttempts.firstSetStarted;
    controller.stop();
    automaticAttempts.finishFirstSet();
    await vi.advanceTimersByTimeAsync(0);

    expect(automaticAttempts.peek("view")).toBeUndefined();
    attached = false;
    controller.start();
    await vi.advanceTimersByTimeAsync(0);

    expect(preflights).toEqual(["request-1"]);
    expect(refreshes).toHaveLength(0);
    controller.stop();
  });

  it("does not dispatch after an asynchronous manual confirmation is cancelled", async () => {
    let resolveConfirmation: ((confirmed: boolean) => void) | undefined;
    let providerDispatches = 0;
    const controller = new DeckRefreshController({
      ...controllerAttemptStores(),
      clientKind: RefreshClientKind.DESKTOP,
      createRequestId: () => "request",
      canPoll: () => false,
      listCandidates: async () => [],
      transport: {
        isAmbiguousRefreshError: () => false,
        getPreflight: async () => ({ priceUsdMicros: 50n, token: "token" }),
        refresh: async () => {
          providerDispatches += 1;
        },
      },
    });

    const refresh = controller.refreshManually(
      "view",
      () =>
        new Promise<boolean>((resolve) => {
          resolveConfirmation = resolve;
        }),
    );
    await Promise.resolve();
    controller.stop();
    resolveConfirmation?.(true);

    await expect(refresh).resolves.toBe(false);
    expect(providerDispatches).toBe(0);
  });

  it("removes a manual attempt when cancellation wins its store write", async () => {
    const manualAttempts = attemptStoreWithDeferredFirstSet();
    const preflights: string[] = [];
    const refreshes: string[] = [];
    let confirmations = 0;
    let sequence = 0;
    const controller = new DeckRefreshController({
      automaticAttempts: attemptStore(),
      manualAttempts: manualAttempts.store,
      clientKind: RefreshClientKind.DESKTOP,
      createRequestId: () => `request-${++sequence}`,
      canPoll: () => false,
      listCandidates: async () => [],
      transport: {
        isAmbiguousRefreshError: () => false,
        getPreflight: async (request) => {
          preflights.push(request.requestId);
          return { priceUsdMicros: 50n, token: "token" };
        },
        refresh: async (request) => {
          refreshes.push(request.requestId);
        },
      },
    });
    const confirm = () => {
      confirmations += 1;
      return true;
    };

    const firstRefresh = controller.refreshManually("view", confirm);
    await manualAttempts.firstSetStarted;
    controller.stop();
    manualAttempts.finishFirstSet();

    await expect(firstRefresh).resolves.toBe(false);
    expect(manualAttempts.peek("view")).toBeUndefined();
    await expect(
      controller.refreshManually("view", confirm),
    ).resolves.toBe(true);
    expect(preflights).toEqual(["request-1", "request-2"]);
    expect(refreshes).toEqual(["request-2"]);
    expect(confirmations).toBe(2);
  });

  it("retries an ambiguous automatic failure after controller recreation", async () => {
    const preflights: DeckRefreshIdentity[] = [];
    const refreshes: Array<
      DeckRefreshIdentity & { preflightToken: string }
    > = [];
    const automaticAttempts = attemptStore();
    let sequence = 0;
    let attached = true;
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
    const createController = () =>
      new DeckRefreshController({
        automaticAttempts,
        manualAttempts: attemptStore(),
        clientKind: RefreshClientKind.DESKTOP,
        createRequestId: () => `request-${++sequence}`,
        canPoll: () => true,
        listCandidates: async () => [
          {
            viewId: "view",
            notificationAttached: attached,
            shortcutAttached: false,
            widgetAttached: false,
          },
        ],
        transport,
      });

    let controller = createController();
    controller.start();
    await vi.advanceTimersByTimeAsync(0);
    controller.stop();

    attached = false;
    controller = createController();
    controller.start();
    await vi.advanceTimersByTimeAsync(0);

    expect(preflights).toHaveLength(1);
    expect(refreshes).toHaveLength(2);
    expect(refreshes[1]).toEqual(refreshes[0]);
    controller.stop();
  });

  it("retries an ambiguous manual failure after controller recreation", async () => {
    const preflights: DeckRefreshIdentity[] = [];
    const refreshes: Array<
      DeckRefreshIdentity & { preflightToken: string }
    > = [];
    const manualAttempts = attemptStore();
    let confirmations = 0;
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
    const createController = () =>
      new DeckRefreshController({
        automaticAttempts: attemptStore(),
        manualAttempts,
        clientKind: RefreshClientKind.DESKTOP,
        createRequestId: () => `request-${++sequence}`,
        canPoll: () => false,
        listCandidates: async () => [],
        transport,
      });
    const confirm = () => {
      confirmations += 1;
      return true;
    };

    await expect(
      createController().refreshManually("view", confirm),
    ).rejects.toThrow("response lost");
    await expect(
      createController().refreshManually("view", confirm),
    ).resolves.toBe(true);

    expect(preflights).toHaveLength(1);
    expect(refreshes).toHaveLength(2);
    expect(refreshes[1]).toEqual(refreshes[0]);
    expect(confirmations).toBe(1);
  });

  it("starts a new attempt after a terminal automatic failure", async () => {
    const preflights: DeckRefreshIdentity[] = [];
    let sequence = 0;
    let refreshes = 0;
    const controller = new DeckRefreshController({
      ...controllerAttemptStores(),
      clientKind: RefreshClientKind.DESKTOP,
      createRequestId: () => `request-${++sequence}`,
      canPoll: () => true,
      listCandidates: async () => [
        {
          viewId: "view",
          notificationAttached: true,
          shortcutAttached: false,
          widgetAttached: false,
        },
      ],
      transport: {
        isAmbiguousRefreshError: () => false,
        getPreflight: async (request) => {
          preflights.push(request);
          return { priceUsdMicros: 50n, token: `token-${request.requestId}` };
        },
        refresh: async () => {
          refreshes += 1;
          if (refreshes === 1) {
            throw new Error("terminal response");
          }
        },
      },
    });

    controller.start();
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(DECK_REFRESH_INTERVAL_MS);

    expect(preflights.map((request) => request.requestId)).toEqual([
      "request-1",
      "request-2",
    ]);
    controller.stop();
  });

  it("continues to later candidates after one refresh fails", async () => {
    const refreshes: string[] = [];
    const errors: unknown[] = [];
    let sequence = 0;
    const controller = new DeckRefreshController({
      ...controllerAttemptStores(),
      clientKind: RefreshClientKind.DESKTOP,
      createRequestId: () => `request-${++sequence}`,
      canPoll: () => true,
      listCandidates: async () => [
        {
          viewId: "deleted",
          notificationAttached: true,
          shortcutAttached: false,
          widgetAttached: false,
        },
        {
          viewId: "healthy",
          notificationAttached: true,
          shortcutAttached: false,
          widgetAttached: false,
        },
      ],
      onError: (error) => errors.push(error),
      transport: {
        isAmbiguousRefreshError: () => false,
        getPreflight: async (request) => ({
          priceUsdMicros: 50n,
          token: `token-${request.requestId}`,
        }),
        refresh: async (request) => {
          refreshes.push(request.viewId);
          if (request.viewId === "deleted") {
            throw new Error("view deleted");
          }
        },
      },
    });

    controller.start();
    await vi.advanceTimersByTimeAsync(0);

    expect(refreshes).toEqual(["deleted", "healthy"]);
    expect(errors).toHaveLength(1);
    controller.stop();
  });

  it("continues to later candidates after one preflight fails", async () => {
    const preflights: string[] = [];
    const refreshes: string[] = [];
    const errors: unknown[] = [];
    let sequence = 0;
    const controller = new DeckRefreshController({
      ...controllerAttemptStores(),
      clientKind: RefreshClientKind.DESKTOP,
      createRequestId: () => `request-${++sequence}`,
      canPoll: () => true,
      listCandidates: async () => [
        {
          viewId: "deleted",
          notificationAttached: true,
          shortcutAttached: false,
          widgetAttached: false,
        },
        {
          viewId: "healthy",
          notificationAttached: true,
          shortcutAttached: false,
          widgetAttached: false,
        },
      ],
      onError: (error) => errors.push(error),
      transport: {
        isAmbiguousRefreshError: () => false,
        getPreflight: async (request) => {
          preflights.push(request.viewId);
          if (request.viewId === "deleted") {
            throw new Error("view deleted");
          }
          return {
            priceUsdMicros: 50n,
            token: `token-${request.requestId}`,
          };
        },
        refresh: async (request) => {
          refreshes.push(request.viewId);
        },
      },
    });

    controller.start();
    await vi.advanceTimersByTimeAsync(0);

    expect(preflights).toEqual(["deleted", "healthy"]);
    expect(refreshes).toEqual(["healthy"]);
    expect(errors).toHaveLength(1);
    controller.stop();
  });

  it("does not let a stopped poll schedule into a restarted generation", async () => {
    let resolveFirst: (() => void) | undefined;
    let listCalls = 0;
    const controller = new DeckRefreshController({
      ...controllerAttemptStores(),
      clientKind: RefreshClientKind.DESKTOP,
      createRequestId: () => "request",
      canPoll: () => true,
      listCandidates: async () => {
        listCalls += 1;
        if (listCalls === 1) {
          await new Promise<void>((resolve) => {
            resolveFirst = resolve;
          });
        }
        return [];
      },
      transport: transportRecorder().transport,
    });

    controller.start();
    await vi.advanceTimersByTimeAsync(0);
    controller.stop();
    controller.start();
    await vi.advanceTimersByTimeAsync(0);
    resolveFirst?.();
    await Promise.resolve();
    await Promise.resolve();

    expect(vi.getTimerCount()).toBe(1);
    await vi.advanceTimersByTimeAsync(DECK_REFRESH_INTERVAL_MS);
    expect(listCalls).toBe(3);
    controller.stop();
  });

  it("warns with the server price and manual refresh bypass origin", async () => {
    const recorded = transportRecorder();
    const controller = new DeckRefreshController({
      ...controllerAttemptStores(),
      clientKind: RefreshClientKind.DESKTOP,
      transport: recorded.transport,
      createRequestId: () => "manual-request",
      canPoll: () => false,
      listCandidates: async () => [],
    });
    const warnings: string[] = [];

    await expect(
      controller.refreshManually("view", (warning) => {
        warnings.push(warning.text);
        return true;
      }),
    ).resolves.toBe(true);
    expect(warnings).toEqual([
      "Refresh now bypasses the cache and may make a billed GitHub provider request at $0.000050 USD per request.",
    ]);
    expect(recorded.refreshes[0]).toMatchObject({
      origin: RefreshOrigin.MANUAL,
      clientKind: RefreshClientKind.DESKTOP,
      preflightToken: "token-manual-request",
    });

    await expect(
      controller.refreshManually("view", () => false),
    ).resolves.toBe(false);
    expect(recorded.refreshes).toHaveLength(1);
  });
});

describe("automatic refresh eligibility", () => {
  const now = new Date("2026-07-30T00:00:00Z");

  it("accepts notification, shortcut, widget, and prior-30-day attachments", () => {
    const base = {
      viewId: "view",
      notificationAttached: false,
      shortcutAttached: false,
      widgetAttached: false,
    };
    expect(
      isAutomaticRefreshEligible(
        { ...base, notificationAttached: true },
        now,
      ),
    ).toBe(true);
    expect(
      isAutomaticRefreshEligible({ ...base, shortcutAttached: true }, now),
    ).toBe(true);
    expect(
      isAutomaticRefreshEligible({ ...base, widgetAttached: true }, now),
    ).toBe(true);
    expect(
      isAutomaticRefreshEligible(
        { ...base, lastOpenedAt: new Date("2026-06-30T00:00:00Z") },
        now,
      ),
    ).toBe(true);
    expect(
      isAutomaticRefreshEligible(
        { ...base, lastOpenedAt: new Date("2026-06-29T23:59:59Z") },
        now,
      ),
    ).toBe(false);
  });
});
