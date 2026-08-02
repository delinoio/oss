import { create, toBinary } from "@bufbuild/protobuf";
import { invoke } from "@tauri-apps/api/core";
import {
  ErrorDetailSchema,
  ErrorReason,
  DevicePlatform,
  RefreshOrigin,
  ShortcutKey,
  ShortcutModifier,
  ShortcutState,
} from "@delinoio/devhud-deck-connect";
import { afterEach, describe, expect, it, vi } from "vitest";

import { DeckFailureCode, DeckProductError } from "./contracts";
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
  it("registers fresh devices and refreshes eligible views outside loaded pages", async () => {
    const accountId = "018f0000-0000-7000-8000-000000000001";
    const deviceId = "018f0000-0000-7000-8000-000000000002";
    const activeViewId = "018f0000-0000-7000-8000-000000000003";
    const inactiveViewId = "018f0000-0000-7000-8000-000000000004";
    const recentlyOpenedViewId = "018f0000-0000-7000-8000-000000000005";
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
      if (procedure === DeckProcedure.RegisterDevice) {
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
    const gateway = new NativeDeckGateway(undefined, () => DevicePlatform.LINUX);
    await gateway.listOwners();

    await gateway.synchronizeShortcuts();

    const registrationCall = vi.mocked(invokeDeckProcedure).mock.calls.find(
      ([procedure]) => procedure === DeckProcedure.RegisterDevice,
    );
    const registrationRequest = registrationCall?.[3] as {
      deviceId?: { value: string };
      expectedRevision?: unknown;
      platform: DevicePlatform;
    } | undefined;
    expect(registrationRequest?.deviceId?.value).toBe(deviceId);
    expect(registrationRequest?.expectedRevision).toBeUndefined();
    expect(registrationRequest?.platform).toBe(DevicePlatform.LINUX);
    gateway.recordViewOpened(recentlyOpenedViewId);
    const refreshedViewIds: string[] = [];
    let stop: () => void = () => undefined;
    await new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(() => {
        stop();
        reject(new Error(`Timed out after refreshing ${refreshedViewIds.join(", ")}`));
      }, 1_000);
      stop = gateway.startEligibleRefreshes([], (viewId) => {
        refreshedViewIds.push(viewId);
        if (refreshedViewIds.length === 2) {
          stop();
          clearTimeout(timeout);
          resolve();
        }
      });
    });
    expect(refreshedViewIds).toEqual([activeViewId, recentlyOpenedViewId]);
  });

  it("reloads and renews an existing device with its server revision", async () => {
    const accountId = "018f0000-0000-7000-8000-000000000001";
    const deviceId = "018f0000-0000-7000-8000-000000000002";
    const device = {
      platform: DevicePlatform.LINUX,
      displayName: "Existing desktop",
      detailedNotificationTextEnabled: false,
      shortcuts: [],
      widgets: [],
      revision: { value: 2n, etag: "device-2" },
    };
    vi.mocked(invoke).mockImplementation(async (command) => {
      if (command === "deck_device_id") return deviceId as never;
      if (command === "synchronize_deck_shortcuts") return [] as never;
      throw new Error(`Unexpected command: ${command}`);
    });
    let registrations = 0;
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
      if (procedure === DeckProcedure.RegisterDevice) {
        registrations += 1;
        if (registrations === 1) throw { code: "conflict" };
        return { registration: { device } } as never;
      }
      if (procedure === DeckProcedure.GetDevice) {
        return { registration: { device } } as never;
      }
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway(undefined, () => DevicePlatform.LINUX);
    await gateway.listOwners();

    await gateway.synchronizeShortcuts();
    await gateway.synchronizeShortcuts();

    const renewals = vi.mocked(invokeDeckProcedure).mock.calls.filter(
      ([procedure]) => procedure === DeckProcedure.RegisterDevice,
    );
    expect(renewals[1]?.[3]).toMatchObject({ expectedRevision: device.revision });
    expect(renewals[2]?.[3]).toMatchObject({ expectedRevision: device.revision });
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

  it("reuses the refresh identity after an ambiguous billing failure", async () => {
    const refreshRequests: unknown[] = [];
    vi.mocked(invokeDeckProcedure).mockImplementation(async (procedure, _input, _output, request) => {
      if (procedure === DeckProcedure.GetRefreshPreflight) {
        return { providerRefreshPrice: { value: 50n }, preflightToken: "token" } as never;
      }
      if (procedure === DeckProcedure.RefreshView) {
        refreshRequests.push(request);
        if (refreshRequests.length === 1) throw { code: "billing-unavailable" };
        return {} as never;
      }
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway();
    const confirm = vi.fn(async () => true);

    await expect(gateway.refreshView("view", confirm)).rejects.toMatchObject({
      code: DeckFailureCode.BillingUnavailable,
    });
    await gateway.refreshView("view", confirm);

    expect(confirm).toHaveBeenCalledOnce();
    expect(refreshRequests).toHaveLength(2);
    expect(refreshRequests[1]).toEqual(refreshRequests[0]);
  });

  it("bypasses the view-open cache for post-mutation recovery refreshes", async () => {
    const requests: unknown[] = [];
    vi.mocked(invokeDeckProcedure).mockImplementation(async (procedure, _input, _output, request) => {
      requests.push(request);
      if (procedure === DeckProcedure.GetRefreshPreflight) {
        return { providerRefreshPrice: { value: 50n }, preflightToken: "token" } as never;
      }
      if (procedure === DeckProcedure.RefreshView) return {} as never;
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway();

    await gateway.refreshAfterMutation("view");

    expect(requests).toHaveLength(2);
    expect(requests[0]).toMatchObject({ origin: RefreshOrigin.MANUAL });
    expect(requests[1]).toMatchObject({ origin: RefreshOrigin.MANUAL });
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
