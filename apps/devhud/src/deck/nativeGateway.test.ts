import { create, toBinary } from "@bufbuild/protobuf";
import { invoke } from "@tauri-apps/api/core";
import {
  ErrorDetailSchema,
  ErrorReason,
  ShortcutKey,
  ShortcutModifier,
  ShortcutState,
} from "@delinoio/devhud-deck-connect";
import { afterEach, describe, expect, it, vi } from "vitest";

import { DeckFailureCode, DeckProductError, type DeckView } from "./contracts";
import { mapFailure, NativeDeckGateway } from "./nativeGateway";
import { DeckProcedure, invokeDeckProcedure } from "./transport";

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(),
  isTauri: () => true,
}));

vi.mock("./transport", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./transport")>();
  return { ...actual, invokeDeckProcedure: vi.fn() };
});

afterEach(() => {
  vi.clearAllMocks();
});

function base64(bytes: Uint8Array): string {
  return btoa(String.fromCharCode(...bytes));
}

describe("native Deck gateway", () => {
  it("reloads account shortcuts for empty pages and refreshes only active native registrations", async () => {
    const accountId = "018f0000-0000-7000-8000-000000000001";
    const deviceId = "018f0000-0000-7000-8000-000000000002";
    const activeViewId = "018f0000-0000-7000-8000-000000000003";
    const inactiveViewId = "018f0000-0000-7000-8000-000000000004";
    vi.mocked(invoke).mockImplementation(async (command) => {
      if (command === "deck_device_id") return deviceId as never;
      if (command === "synchronize_deck_shortcuts") {
        return [
          { viewId: activeViewId, outcome: "active" },
          { viewId: inactiveViewId, outcome: "inactive-conflict" },
        ] as never;
      }
      throw new Error(`Unexpected command: ${command}`);
    });
    vi.mocked(invokeDeckProcedure).mockImplementation(async (procedure) => {
      if (procedure === DeckProcedure.ListOwners) {
        return {
          owners: [{
            owner: { ownerId: { case: "accountId", value: { value: accountId } } },
            canManage: true,
            billingSelections: [],
          }],
        } as never;
      }
      if (procedure === DeckProcedure.GetDevice) {
        return {
          registration: {
            device: {
              shortcuts: [activeViewId, inactiveViewId].map((viewId) => ({
                state: ShortcutState.ACTIVE,
                viewId: { value: viewId },
                binding: {
                  modifiers: [ShortcutModifier.CONTROL],
                  key: ShortcutKey.K,
                },
              })),
            },
          },
        } as never;
      }
      if (procedure === DeckProcedure.GetRefreshPreflight) {
        return { providerRefreshPrice: { value: 50n }, preflightToken: "token" } as never;
      }
      if (procedure === DeckProcedure.RefreshView) return {} as never;
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway();
    await gateway.listOwners();

    await gateway.synchronizeShortcuts();

    expect(invokeDeckProcedure).toHaveBeenCalledWith(
      DeckProcedure.GetDevice,
      expect.anything(),
      expect.anything(),
      expect.anything(),
    );
    const candidate = (viewId: string) => ({
      viewId,
      notificationPreference: { enabled: false, transitions: [] },
      widgetAttached: false,
    }) as unknown as DeckView;
    const stop = gateway.startEligibleRefreshes(
      [candidate(activeViewId), candidate(inactiveViewId)],
      () => undefined,
    );
    await vi.waitFor(() => {
      expect(vi.mocked(invokeDeckProcedure).mock.calls.filter(
        ([procedure]) => procedure === DeckProcedure.GetRefreshPreflight,
      )).toHaveLength(1);
    });
    stop();
    const preflightCall = vi.mocked(invokeDeckProcedure).mock.calls.find(
      ([procedure]) => procedure === DeckProcedure.GetRefreshPreflight,
    );
    expect(preflightCall?.[3]).toMatchObject({ viewId: { value: activeViewId } });
  });

  it("shares one preflight, confirmation, and dispatch across concurrent manual refreshes", async () => {
    let resolveConfirmation: ((confirmed: boolean) => void) | undefined;
    const confirm = vi.fn(() => new Promise<boolean>((resolve) => {
      resolveConfirmation = resolve;
    }));
    vi.mocked(invokeDeckProcedure).mockImplementation(async (procedure) => {
      if (procedure === DeckProcedure.GetRefreshPreflight) {
        return { providerRefreshPrice: { value: 50n }, preflightToken: "token" } as never;
      }
      if (procedure === DeckProcedure.RefreshView) return {} as never;
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway();

    const first = gateway.refreshView("view", confirm);
    const second = gateway.refreshView("view", confirm);

    expect(second).toBe(first);
    await vi.waitFor(() => expect(confirm).toHaveBeenCalledTimes(1));
    resolveConfirmation?.(true);
    await Promise.all([first, second]);
    expect(vi.mocked(invokeDeckProcedure).mock.calls.filter(
      ([procedure]) => procedure === DeckProcedure.GetRefreshPreflight,
    )).toHaveLength(1);
    expect(vi.mocked(invokeDeckProcedure).mock.calls.filter(
      ([procedure]) => procedure === DeckProcedure.RefreshView,
    )).toHaveLength(1);
  });

  it("maps typed Connect details and retry-after into the stable product error", () => {
    const detail = create(ErrorDetailSchema, {
      reason: ErrorReason.PROVIDER_RATE_LIMITED,
      retryAfter: { seconds: 30n, nanos: 1 },
    });
    let thrown: unknown;

    try {
      mapFailure({
        code: "rate-limited",
        detailBodyBase64: base64(toBinary(ErrorDetailSchema, detail)),
      });
    } catch (error) {
      thrown = error;
    }

    expect(thrown).toBeInstanceOf(DeckProductError);
    expect(thrown).toMatchObject({
      code: DeckFailureCode.ProviderRateLimited,
      retryAfterSeconds: 31,
    });
  });
});
