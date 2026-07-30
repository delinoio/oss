import {
  RefreshClientKind,
  RefreshOrigin,
} from "@delinoio/devhud-deck-connect";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  DECK_REFRESH_INTERVAL_MS,
  DeckRefreshController,
  type DeckRefreshIdentity,
  type DeckRefreshTransport,
  isAutomaticRefreshEligible,
} from "./refreshController";

function transportRecorder() {
  const preflights: DeckRefreshIdentity[] = [];
  const refreshes: Array<DeckRefreshIdentity & { preflightToken: string }> = [];
  const transport: DeckRefreshTransport = {
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
    let providerDispatches = 0;
    const controller = new DeckRefreshController({
      clientKind: RefreshClientKind.MOBILE,
      createRequestId: () => "request",
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
        getPreflight: () =>
          new Promise((resolve) => {
            resolvePreflight = resolve;
          }),
        refresh: async () => {
          providerDispatches += 1;
        },
      },
    });

    controller.start();
    await vi.advanceTimersByTimeAsync(0);
    controller.stop();
    resolvePreflight?.({ priceUsdMicros: 50n, token: "token" });
    await Promise.resolve();
    await Promise.resolve();
    expect(providerDispatches).toBe(0);
  });

  it("warns with the server price and manual refresh bypass origin", async () => {
    const recorded = transportRecorder();
    const controller = new DeckRefreshController({
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
