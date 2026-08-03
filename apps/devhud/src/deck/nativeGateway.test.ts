import { create, toBinary } from "@bufbuild/protobuf";
import { invoke } from "@tauri-apps/api/core";
import {
  ErrorDetailSchema,
  ErrorReason,
  DevicePlatform,
  RefreshClientKind,
  RefreshOrigin,
  ShortcutKey,
  ShortcutModifier,
  ShortcutState,
} from "@delinoio/devhud-deck-connect";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  DeckFailureCode,
  DeckNotificationTransition,
  DeckProductError,
} from "./contracts";
import { mapFailure, NativeDeckGateway } from "./nativeGateway";
import { DeckProcedure, invokeDeckProcedure } from "./transport";
import { DeckWidgetFamily, DeckWidgetPrivacy } from "../persistence/contracts";

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(),
  isTauri: () => true,
}));

vi.mock("./transport", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./transport")>();
  return { ...actual, invokeDeckProcedure: vi.fn() };
});

const originalUserAgent = navigator.userAgent;

afterEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(navigator, "userAgent", {
    configurable: true,
    value: originalUserAgent,
  });
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

  it("does not restore shortcuts when teardown clears an in-flight synchronization", async () => {
    const accountId = "018f0000-0000-7000-8000-000000000001";
    const deviceId = "018f0000-0000-7000-8000-000000000002";
    const viewId = "018f0000-0000-7000-8000-000000000003";
    let finishRegistration: ((registration: unknown) => void) | undefined;
    vi.mocked(invoke).mockImplementation(async (command) => {
      if (command === "deck_device_id") return deviceId as never;
      if (command === "synchronize_deck_shortcuts") return [] as never;
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
        return await new Promise<unknown>((resolve) => { finishRegistration = resolve; }) as never;
      }
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway(undefined, () => DevicePlatform.LINUX);
    await gateway.listOwners();

    const synchronization = gateway.synchronizeShortcuts();
    await vi.waitFor(() => expect(finishRegistration).toBeTypeOf("function"));
    await gateway.clearShortcuts();
    finishRegistration?.({
      registration: {
        device: {
          shortcuts: [{
            state: ShortcutState.ACTIVE,
            viewId: { value: viewId },
            binding: {
              modifiers: [ShortcutModifier.CONTROL],
              key: ShortcutKey.K,
            },
          }],
        },
      },
    });
    await synchronization;

    const shortcutCalls = vi.mocked(invoke).mock.calls.filter(
      ([command]) => command === "synchronize_deck_shortcuts",
    );
    expect(shortcutCalls).toEqual([[
      "synchronize_deck_shortcuts",
      { accountId, definitions: [] },
    ]]);
  });

  it("writes explicit per-view notification preferences for the registered mobile device", async () => {
    const accountId = "018f0000-0000-7000-8000-000000000001";
    const deviceId = "018f0000-0000-7000-8000-000000000002";
    const viewId = "018f0000-0000-7000-8000-000000000003";
    const registrationId = "018f0000-0000-7000-8000-000000000004";
    Object.defineProperty(navigator, "userAgent", { configurable: true, value: "Android" });
    vi.mocked(invoke).mockImplementation(async (command) => {
      if (command === "deck_device_id") return deviceId as never;
      if (command === "write_widget_configuration") return 0 as never;
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
            registrationId: { value: registrationId },
            device: { widgets: [], shortcuts: [] },
          },
        } as never;
      }
      if (procedure === DeckProcedure.UpdateViewNotificationPreference) return {} as never;
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway(RefreshClientKind.MOBILE);
    await gateway.listOwners();
    await gateway.synchronizeShortcuts();

    await gateway.updateNativeNotificationPreference(viewId, {
      enabled: false,
      transitions: [],
    });

    const update = vi.mocked(invokeDeckProcedure).mock.calls.find(
      ([procedure]) => procedure === DeckProcedure.UpdateViewNotificationPreference,
    )?.[3];
    expect(update).toMatchObject({
      registrationId: { value: registrationId },
      viewId: { value: viewId },
      preference: { enabled: false, transitions: [] },
    });
    expect(update).not.toHaveProperty("expectedRevision");
    expect(vi.mocked(invokeDeckProcedure).mock.calls.some(
      ([procedure]) => procedure === DeckProcedure.MutatePullRequest,
    )).toBe(false);
  });

  it("writes explicit per-view notification preferences for the registered desktop device", async () => {
    const accountId = "018f0000-0000-7000-8000-000000000001";
    const deviceId = "018f0000-0000-7000-8000-000000000002";
    const viewId = "018f0000-0000-7000-8000-000000000003";
    const registrationId = "018f0000-0000-7000-8000-000000000004";
    vi.mocked(invoke).mockImplementation(async (command) => {
      if (command === "deck_device_id") return deviceId as never;
      if (command === "synchronize_deck_shortcuts") return [] as never;
      throw new Error(`Unexpected command: ${command}`);
    });
    vi.mocked(invokeDeckProcedure).mockImplementation(async (procedure) => {
      if (procedure === DeckProcedure.ListOwners) {
        return { owners: [{
          owner: { ownerId: { case: "accountId", value: { value: accountId } } },
          canManage: true,
          billingSelections: [],
        }] } as never;
      }
      if (procedure === DeckProcedure.RegisterDevice) {
        return { registration: {
          registrationId: { value: registrationId },
          device: { widgets: [], shortcuts: [] },
        } } as never;
      }
      if (procedure === DeckProcedure.UpdateViewNotificationPreference) return {} as never;
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway(
      RefreshClientKind.DESKTOP,
      () => DevicePlatform.LINUX,
    );
    await gateway.listOwners();

    await gateway.updateNativeNotificationPreference(viewId, {
      enabled: true,
      transitions: [DeckNotificationTransition.Assigned],
    });

    expect(vi.mocked(invokeDeckProcedure).mock.calls.find(
      ([procedure]) => procedure === DeckProcedure.UpdateViewNotificationPreference,
    )?.[3]).toMatchObject({
      registrationId: { value: registrationId },
      viewId: { value: viewId },
      preference: { enabled: true, transitions: [1] },
    });
  });

  it("requires native notification authorization before enabling a mobile preference", async () => {
    Object.defineProperty(navigator, "userAgent", { configurable: true, value: "Android" });
    vi.mocked(invoke).mockResolvedValueOnce(false as never).mockResolvedValueOnce(true as never);
    const gateway = new NativeDeckGateway(RefreshClientKind.MOBILE);
    const preference = {
      enabled: true,
      transitions: [DeckNotificationTransition.Assigned],
    };

    await expect(gateway.ensureNativeNotificationPermission(preference)).rejects.toMatchObject({
      code: DeckFailureCode.NotificationPermissionRequired,
    });
    await expect(gateway.ensureNativeNotificationPermission(preference)).rejects.toMatchObject({
      code: DeckFailureCode.ServiceUnavailable,
    });
    expect(invoke).toHaveBeenNthCalledWith(1, "deck_notification_authorization_enabled");
  });

  it("registers a configured mobile widget and polls its view outside loaded pages", async () => {
    const accountId = "018f0000-0000-7000-8000-000000000001";
    const deviceId = "018f0000-0000-7000-8000-000000000002";
    const viewId = "018f0000-0000-7000-8000-000000000003";
    Object.defineProperty(navigator, "userAgent", { configurable: true, value: "Android" });
    let widgetWrites = 0;
    vi.mocked(invoke).mockImplementation(async (command) => {
      if (command === "deck_device_id") return deviceId as never;
      if (command === "write_widget_configuration") {
        widgetWrites += 1;
        return 0 as never;
      }
      throw new Error(`Unexpected command: ${command}`);
    });
    let registeredWidgets: Array<Record<string, unknown>> = [];
    let deviceRevision = 0n;
    const currentRegistration = () => ({
      registrationId: { value: "018f0000-0000-7000-8000-000000000004" },
      device: {
        deviceId: { value: deviceId },
        platform: DevicePlatform.ANDROID,
        displayName: "DevHud mobile",
        shortcuts: [],
        widgets: registeredWidgets,
        revision: { value: deviceRevision, etag: `device-${deviceRevision}` },
      },
    });
    vi.mocked(invokeDeckProcedure).mockImplementation(async (procedure, _input, _output, request) => {
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
        deviceRevision += 1n;
        registeredWidgets = (request as unknown as {
          widgets: Array<Record<string, unknown>>;
        }).widgets.map((widget) => ({
          ...widget,
          snapshot: {
            matchingCount: 0,
            pullRequests: [],
            freshness: 1,
            offline: false,
            generatedAt: { seconds: 0n, nanos: 0 },
          },
        }));
        return { registration: currentRegistration() } as never;
      }
      if (procedure === DeckProcedure.GetDevice) {
        return { registration: currentRegistration() } as never;
      }
      if (procedure === DeckProcedure.GetRefreshPreflight) {
        return { providerRefreshPrice: { value: 50n }, preflightToken: "token" } as never;
      }
      if (procedure === DeckProcedure.RefreshView) return {} as never;
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway(RefreshClientKind.MOBILE);
    await gateway.listOwners();

    await gateway.createWidgetConfiguration(
      viewId,
      DeckWidgetFamily.AndroidCompact,
      DeckWidgetPrivacy.CountsOnly,
    );

    const registration = vi.mocked(invokeDeckProcedure).mock.calls.find(
      ([procedure, , , request]) => procedure === DeckProcedure.RegisterDevice &&
        (request as { widgets?: unknown[] }).widgets?.length === 1,
    )?.[3] as unknown as {
      widgets: Array<{ widgetId?: { value: string }; viewId?: { value: string } }>;
    };
    expect(registration.widgets).toHaveLength(1);
    expect(registration.widgets[0]?.widgetId?.value).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u,
    );
    expect(registration.widgets[0]?.viewId?.value).toBe(viewId);

    await new Promise<void>((resolve, reject) => {
      let stop: () => void = () => undefined;
      const timeout = setTimeout(() => {
        stop();
        reject(new Error("Timed out waiting for widget-attached refresh"));
      }, 1_000);
      stop = gateway.startEligibleRefreshes([], (refreshedViewId) => {
        if (refreshedViewId !== viewId) return;
        stop();
        clearTimeout(timeout);
        resolve();
      });
    });
    expect(widgetWrites).toBeGreaterThanOrEqual(3);
  });

  it("recovers the twentieth registered widget after a native record write failure", async () => {
    const accountId = "018f0000-0000-7000-8000-000000000001";
    const deviceId = "018f0000-0000-7000-8000-000000000002";
    const viewId = "018f0000-0000-7000-8000-000000000003";
    Object.defineProperty(navigator, "userAgent", { configurable: true, value: "Android" });
    let failWidgetWrite = false;
    vi.mocked(invoke).mockImplementation(async (command) => {
      if (command === "deck_device_id") return deviceId as never;
      if (command === "write_widget_configuration") {
        if (failWidgetWrite) {
          failWidgetWrite = false;
          throw new Error("native write failed");
        }
        return 0 as never;
      }
      throw new Error(`Unexpected command: ${command}`);
    });
    let widgets: Array<Record<string, unknown>> = [];
    vi.mocked(invokeDeckProcedure).mockImplementation(async (procedure, _input, _output, request) => {
      if (procedure === DeckProcedure.ListOwners) {
        return { owners: [{
          owner: { ownerId: { case: "accountId", value: { value: accountId } } },
          canManage: true,
          billingSelections: [],
        }] } as never;
      }
      if (procedure === DeckProcedure.RegisterDevice) {
        widgets = (request as unknown as { widgets: Array<Record<string, unknown>> }).widgets;
        return { registration: {
          registrationId: { value: "018f0000-0000-7000-8000-000000000004" },
          device: { widgets, shortcuts: [], revision: { value: 1n, etag: "device-1" } },
        } } as never;
      }
      if (procedure === DeckProcedure.GetDevice) {
        return { registration: {
          registrationId: { value: "018f0000-0000-7000-8000-000000000004" },
          device: { widgets, shortcuts: [], revision: { value: 1n, etag: "device-1" } },
        } } as never;
      }
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway(RefreshClientKind.MOBILE);
    await gateway.listOwners();
    await gateway.synchronizeShortcuts();
    widgets = Array.from({ length: 19 }, (_, index) => ({
      widgetId: { value: `018f0000-0000-7000-8000-${String(index).padStart(12, "0")}` },
      viewId: { value: `018f0000-0000-7000-9000-${String(index).padStart(12, "0")}` },
      family: 4,
      privacy: 1,
    }));
    failWidgetWrite = true;

    await expect(gateway.createWidgetConfiguration(
      viewId,
      DeckWidgetFamily.AndroidCompact,
      DeckWidgetPrivacy.CountsOnly,
    )).rejects.toThrow("native write failed");
    await gateway.createWidgetConfiguration(
      viewId,
      DeckWidgetFamily.AndroidCompact,
      DeckWidgetPrivacy.CountsOnly,
    );

    expect(widgets).toHaveLength(20);
  });

  it("reloads and rewrites native widget snapshots after widget and manual refreshes", async () => {
    const accountId = "018f0000-0000-7000-8000-000000000001";
    const deviceId = "018f0000-0000-7000-8000-000000000002";
    const viewId = "018f0000-0000-7000-8000-000000000003";
    const widgetId = "018f0000-0000-7000-8000-000000000004";
    const registrationId = "018f0000-0000-7000-8000-000000000005";
    const records: string[] = [];
    Object.defineProperty(navigator, "userAgent", { configurable: true, value: "Android" });
    vi.mocked(invoke).mockImplementation(async (command, args) => {
      if (command === "deck_device_id") return deviceId as never;
      if (command === "write_widget_configuration") {
        records.push((args as { record: string }).record);
        return 1 as never;
      }
      throw new Error(`Unexpected command: ${command}`);
    });
    const registration = (matchingCount: number, revision: bigint) => ({
      registrationId: { value: registrationId },
      device: {
        deviceId: { value: deviceId },
        platform: DevicePlatform.ANDROID,
        displayName: "DevHud mobile",
        widgets: [{
          widgetId: { value: widgetId },
          viewId: { value: viewId },
          family: 4,
          privacy: 1,
          snapshot: {
            matchingCount,
            pullRequests: [],
            freshness: 1,
            offline: false,
            generatedAt: { seconds: 0n, nanos: 0 },
          },
        }],
        shortcuts: [],
        revision: { value: revision, etag: `device-${revision}` },
      },
    });
    let registrations = 0;
    vi.mocked(invokeDeckProcedure).mockImplementation(async (procedure) => {
      if (procedure === DeckProcedure.ListOwners) {
        return { owners: [{
          owner: { ownerId: { case: "accountId", value: { value: accountId } } },
          canManage: true,
          billingSelections: [],
        }] } as never;
      }
      if (procedure === DeckProcedure.RegisterDevice) {
        registrations += 1;
        return { registration: registration(registrations === 1 ? 1 : 7, BigInt(registrations)) } as never;
      }
      if (procedure === DeckProcedure.GetRefreshPreflight) {
        return { providerRefreshPrice: { value: 50n }, preflightToken: "token" } as never;
      }
      if (procedure === DeckProcedure.RefreshView) return {} as never;
      if (procedure === DeckProcedure.GetDevice) return { registration: registration(7, 2n) } as never;
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway(RefreshClientKind.MOBILE);
    await gateway.listOwners();
    await gateway.synchronizeShortcuts();

    await gateway.requestWidgetRefresh(viewId);

    expect(records).toHaveLength(2);
    expect(JSON.parse(records.at(-1)!).configuration.widgets[0].snapshot.matchingCount).toBe(7);
    const procedures = vi.mocked(invokeDeckProcedure).mock.calls.map(([procedure]) => procedure);
    expect(procedures.lastIndexOf(DeckProcedure.GetDevice)).toBeGreaterThan(
      procedures.indexOf(DeckProcedure.RefreshView),
    );

    await gateway.refreshView(viewId, async () => true);

    expect(records).toHaveLength(3);
    expect(JSON.parse(records.at(-1)!).configuration.widgets[0].snapshot.matchingCount).toBe(7);
    const refreshedProcedures = vi.mocked(invokeDeckProcedure).mock.calls.map(([procedure]) => procedure);
    expect(refreshedProcedures.lastIndexOf(DeckProcedure.GetDevice)).toBeGreaterThan(
      refreshedProcedures.lastIndexOf(DeckProcedure.RefreshView),
    );
  });

  it("waits for cold-start registration before resolving a notification event", async () => {
    const accountId = "018f0000-0000-7000-8000-000000000001";
    const deviceId = "018f0000-0000-7000-8000-000000000002";
    const registrationId = "018f0000-0000-7000-8000-000000000003";
    Object.defineProperty(navigator, "userAgent", { configurable: true, value: "Android" });
    vi.mocked(invoke).mockImplementation(async (command) => {
      if (command === "deck_device_id") return deviceId as never;
      if (command === "write_widget_configuration") return 0 as never;
      throw new Error(`Unexpected command: ${command}`);
    });
    vi.mocked(invokeDeckProcedure).mockImplementation(async (procedure) => {
      if (procedure === DeckProcedure.ListOwners) {
        return { owners: [{
          owner: { ownerId: { case: "accountId", value: { value: accountId } } },
          canManage: true,
          billingSelections: [],
        }] } as never;
      }
      if (procedure === DeckProcedure.RegisterDevice) {
        return { registration: {
          registrationId: { value: registrationId },
          device: { widgets: [], shortcuts: [] },
        } } as never;
      }
      if (procedure === DeckProcedure.ResolveNotificationEvent) return {} as never;
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway(RefreshClientKind.MOBILE);
    await gateway.listOwners();

    await gateway.resolveNotificationEvent("opaque-notification-event");

    const procedures = vi.mocked(invokeDeckProcedure).mock.calls.map(([procedure]) => procedure);
    expect(procedures.indexOf(DeckProcedure.RegisterDevice)).toBeLessThan(
      procedures.indexOf(DeckProcedure.ResolveNotificationEvent),
    );
    expect(vi.mocked(invokeDeckProcedure).mock.calls.find(
      ([procedure]) => procedure === DeckProcedure.ResolveNotificationEvent,
    )?.[3]).toMatchObject({ registrationId: { value: registrationId } });
  });

  it("rejects a twenty-first widget before registering it", async () => {
    const accountId = "018f0000-0000-7000-8000-000000000001";
    const deviceId = "018f0000-0000-7000-8000-000000000002";
    Object.defineProperty(navigator, "userAgent", { configurable: true, value: "Android" });
    vi.mocked(invoke).mockImplementation(async (command) => {
      if (command === "deck_device_id") return deviceId as never;
      if (command === "write_widget_configuration") return 0 as never;
      throw new Error(`Unexpected command: ${command}`);
    });
    const widgets = Array.from({ length: 20 }, (_, index) => ({
      widgetId: { value: `018f0000-0000-7000-8000-${String(index).padStart(12, "0")}` },
      viewId: { value: `018f0000-0000-7000-9000-${String(index).padStart(12, "0")}` },
      family: 4,
      privacy: 1,
    }));
    vi.mocked(invokeDeckProcedure).mockImplementation(async (procedure) => {
      if (procedure === DeckProcedure.ListOwners) {
        return { owners: [{
          owner: { ownerId: { case: "accountId", value: { value: accountId } } },
          canManage: true,
          billingSelections: [],
        }] } as never;
      }
      if (procedure === DeckProcedure.RegisterDevice) {
        return { registration: { device: { widgets, shortcuts: [] } } } as never;
      }
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway(RefreshClientKind.MOBILE);
    await gateway.listOwners();

    await expect(gateway.createWidgetConfiguration(
      widgets[0]!.viewId.value,
      DeckWidgetFamily.AndroidCompact,
      DeckWidgetPrivacy.CountsOnly,
    )).rejects.toMatchObject({ code: DeckFailureCode.WidgetLimitReached });
    expect(vi.mocked(invokeDeckProcedure).mock.calls.filter(
      ([procedure]) => procedure === DeckProcedure.RegisterDevice,
    )).toHaveLength(1);
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

  it("starts a new preflight after a pre-attempt preflight rejection", async () => {
    const preflightTokens = ["expired-token", "fresh-token"];
    const refreshRequests: unknown[] = [];
    const expired = create(ErrorDetailSchema, {
      reason: ErrorReason.BILLING_PREFLIGHT_EXPIRED,
    });
    vi.mocked(invokeDeckProcedure).mockImplementation(async (procedure, _input, _output, request) => {
      if (procedure === DeckProcedure.GetRefreshPreflight) {
        return {
          providerRefreshPrice: { value: 50n },
          preflightToken: preflightTokens.shift(),
        } as never;
      }
      if (procedure === DeckProcedure.RefreshView) {
        refreshRequests.push(request);
        if (refreshRequests.length === 1) {
          throw {
            code: "billing-unavailable",
            detailBodyBase64: base64(toBinary(ErrorDetailSchema, expired)),
          };
        }
        return {} as never;
      }
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway();
    const confirm = vi.fn(async () => true);

    await expect(gateway.refreshView("view", confirm)).rejects.toMatchObject({
      code: DeckFailureCode.BillingPreflightRejected,
    });
    await gateway.refreshView("view", confirm);

    expect(confirm).toHaveBeenCalledTimes(2);
    expect(refreshRequests).toHaveLength(2);
    expect(refreshRequests[1]).not.toEqual(refreshRequests[0]);
    expect(refreshRequests[1]).toMatchObject({ billingPreflightToken: "fresh-token" });
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

  it.each([
    ["reauthentication-required", DeckFailureCode.AuthenticationRequired],
    ["browser-unavailable", DeckFailureCode.BrowserUnavailable],
    ["invalid-pull-request", DeckFailureCode.UnsupportedAction],
  ])("maps the %s GitHub handoff failure", async (code, expected) => {
    vi.mocked(invoke).mockRejectedValueOnce({ code });
    const gateway = new NativeDeckGateway();

    await expect(gateway.openPullRequest({
      repositoryOwner: "delinoio",
      repositoryName: "oss",
      number: 795,
    } as never)).rejects.toMatchObject({ code: expected });
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
