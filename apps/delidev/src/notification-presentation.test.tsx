import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { InboxService, NotificationKind, NotificationState, newRequestId } from "@delinoio/delidev-api-client";
import { NativeNotificationSettings, NotificationPresentation } from "./notification-presentation";
import { Settings } from "./settings";
import { NotificationSettings } from "./notification-settings";
import { MutationIntents } from "./mutation";

const native = vi.hoisted(() => ({ invoke: vi.fn(), desktop: true }));
vi.mock("@tauri-apps/api/core", () => ({ isTauri: () => native.desktop, invoke: native.invoke }));
beforeEach(() => { native.invoke.mockReset(); native.desktop = true; });

it("drops a closed Settings permission wait without applying its late result to a new opening or the shared cache", async () => {
  let finish!: (value: unknown) => void;
  native.invoke.mockImplementation(async (command) => command === "request_notification_permission"
    ? new Promise((resolve) => { finish = resolve; })
    : { permission: "not-determined", problem: "none" });
  const transport = createRouterTransport(() => {});
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { gcTime: 0 } } });
  client.setQueryData(["native-notification-readiness"], { permission: "shared-sentinel" });
  const body = (visible: boolean) => <TransportProvider transport={transport}><QueryClientProvider client={client}><Settings visible={visible} /></QueryClientProvider></TransportProvider>;
  const view = render(body(true));
  fireEvent.click(screen.getByRole("button", { name: "Notifications" }));
  fireEvent.click(await screen.findByRole("button", { name: "Allow desktop notifications" }));
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  view.rerender(body(false));
  expect(client.getMutationCache().getAll()).toHaveLength(0);
  view.rerender(body(true));
  fireEvent.click(screen.getByRole("button", { name: "Notifications" }));
  await screen.findByRole("button", { name: "Allow desktop notifications" });
  await act(async () => finish({ permission: "granted", problem: "none" }));
  expect(screen.getByText("Notification permission has not been requested.")).toBeTruthy();
  expect(client.getQueryData(["native-notification-readiness"])).toEqual({ permission: "shared-sentinel" });
  expect(native.invoke.mock.calls.filter(([command]) => command === "request_notification_permission")).toHaveLength(1);
});

it("never prompts from polling and leaves the inbox untouched when permission is denied", async () => {
  let permission = "not-determined";
  native.invoke.mockImplementation(async (command) => {
    if (command === "request_notification_permission") permission = "denied";
    return { permission, problem: "none" };
  });
  const list = vi.fn(() => ({})), claim = vi.fn(() => ({}));
  const transport = createRouterTransport((router) => router.service(InboxService, { listNotificationCandidates: list, claimNotification: claim }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><NotificationPresentation /><NativeNotificationSettings active /></QueryClientProvider></TransportProvider>);
  await screen.findByText("Notification permission has not been requested.");
  expect(native.invoke.mock.calls.every(([command]) => command === "notification_permission")).toBe(true);
  expect(list).not.toHaveBeenCalled(); expect(claim).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Allow desktop notifications" }));
  await screen.findByText(/Notifications are disabled/);
  await act(() => client.invalidateQueries());
  expect(native.invoke.mock.calls.filter(([command]) => command === "request_notification_permission")).toHaveLength(1);
  expect(list).not.toHaveBeenCalled(); expect(claim).not.toHaveBeenCalled();
});

it("connects direct RPC claims to closed native presentation and disposes only its original scope", async () => {
  const scope = newRequestId(), candidate = { inboxId: newRequestId(), sessionId: newRequestId(), kind: NotificationKind.REQUEST };
  let claimed = false;
  native.invoke.mockImplementation(async (command) => command === "notification_permission" ? { permission: "service-available", problem: "none" } : command === "begin_notifications" ? scope : command === "present_notification" ? "submitted" : undefined);
  const report = vi.fn(() => ({}));
  const transport = createRouterTransport((router) => router.service(InboxService, {
    listNotificationCandidates: () => ({ candidates: claimed ? [] : [candidate] }),
    claimNotification: (request) => { claimed = true; return { requestId: request.requestId, mayPresent: true, delivery: { claimId: request.requestId, state: NotificationState.CLAIMED, candidate } }; },
    reportNotification: report,
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<TransportProvider transport={transport}><QueryClientProvider client={client}><NotificationPresentation /></QueryClientProvider></TransportProvider>);
  await waitFor(() => expect(report).toHaveBeenCalledTimes(1));
  await act(() => client.invalidateQueries());
  expect(native.invoke.mock.calls.filter(([command]) => command === "present_notification")).toHaveLength(1);
  expect(native.invoke.mock.calls.find(([command]) => command === "present_notification")?.[1]).toEqual({ scope, notice: { claim_id: expect.any(String), inbox_id: candidate.inboxId, kind: "request" } });
  expect(native.invoke.mock.calls.some(([command]) => command === "request_notification_permission")).toBe(false);
  view.unmount();
  await waitFor(() => expect(native.invoke).toHaveBeenCalledWith("end_notifications", { scope }));
});

const states = [
  ["not-determined", "none", "Notification permission has not been requested."],
  ["denied", "none", "Notifications are disabled. Enable DeliDev in your operating system's notification settings."],
  ["granted", "none", "Notifications allowed"],
  ["service-available", "none", "The desktop notification service supports actions. This service does not report user permission or whether a banner was shown."],
  ["unavailable", "bundle-required", "Native notifications require the installed DeliDev app bundle."],
  ["unavailable", "actions-unavailable", "This desktop notification service cannot open notification actions."],
  ["unavailable", "capacity", "The native notification limit is reached for this app process. Requests remain in the inbox; restart DeliDev to clear its native presentation state."],
  ["unavailable", "os-unavailable", "Native notification service is unavailable."],
] as const;
for (const [permission, problem, status] of states) it(`truthfully presents ${permission}/${problem} and refreshes without prompting`, async () => {
  native.invoke.mockResolvedValue({ permission, problem });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><NativeNotificationSettings active /></QueryClientProvider>);
  await screen.findByText(status);
  expect(Boolean(screen.queryByRole("button", { name: "Allow desktop notifications" }))).toBe(permission === "not-determined");
  if (permission === "granted") expect(screen.getByText("Focus or Do Not Disturb may still suppress banners.")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Refresh status for native notifications" }));
  await waitFor(() => expect(native.invoke).toHaveBeenCalledTimes(2));
  expect(native.invoke.mock.calls.every(([command]) => command === "notification_permission")).toBe(true);
});

it("exposes checking/pending/error states without treating cached success as current permission", async () => {
  let finish!: (value: unknown) => void;
  native.invoke.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><NativeNotificationSettings active /></QueryClientProvider>);
  expect(screen.getByText("Checking native notification availability…")).toBeTruthy();
  expect((screen.getByRole("button", { name: "Refresh status for native notifications" }) as HTMLButtonElement).disabled).toBe(true);
  await act(async () => finish({ permission: "granted", problem: "none" }));
  await screen.findByText("Notifications allowed");
  native.invoke.mockRejectedValueOnce(new Error("Unavailable"));
  fireEvent.click(screen.getByRole("button", { name: "Refresh status for native notifications" }));
  await screen.findByText("Native notification permission could not be confirmed.");
  expect(screen.queryByText("Notifications allowed")).toBeNull();
  expect(screen.queryByText("Focus or Do Not Disturb may still suppress banners.")).toBeNull();
});

it("disables request/Refresh while permission is pending and removes failed request confirmation", async () => {
  let reject!: (error: unknown) => void;
  native.invoke.mockImplementation(async (command) => command === "request_notification_permission" ? new Promise((_, no) => { reject = no; }) : { permission: "not-determined", problem: "none" });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><NativeNotificationSettings active /></QueryClientProvider>);
  fireEvent.click(await screen.findByRole("button", { name: "Allow desktop notifications" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Allow desktop notifications" }) as HTMLButtonElement).disabled).toBe(true));
  expect((screen.getByRole("button", { name: "Refresh status for native notifications" }) as HTMLButtonElement).disabled).toBe(true);
  await act(async () => reject(new Error("Unconfirmed")));
  await screen.findByText("Native notification permission could not be confirmed.");
  expect(screen.queryByRole("button", { name: "Allow desktop notifications" })).toBeNull();
});

it("allows independently readable preference editing under denied native permission", async () => {
  native.invoke.mockResolvedValue({ permission: "denied", problem: "none" });
  const save = vi.fn(() => ({}));
  const transport = createRouterTransport((router) => router.service(InboxService, { getNotificationPreferences: () => ({ preferences: { revision: 1n, interactions: true, terminals: false } }), setNotificationPreferences: save }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><NotificationSettings active /></MutationIntents></QueryClientProvider></TransportProvider>);
  await screen.findByText(/Notifications are disabled/);
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  fireEvent.click(screen.getByRole("button", { name: "Save notification preferences" }));
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
  expect(native.invoke.mock.calls.every(([command]) => command === "notification_permission")).toBe(true);
});

it("shows desktop guidance without a native read or request in non-desktop execution", () => {
  native.desktop = false;
  const client = new QueryClient(); render(<QueryClientProvider client={client}><NativeNotificationSettings active /></QueryClientProvider>);
  expect(screen.getByText("Open DeliDev on your desktop to manage native notifications.")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Refresh status for native notifications" })).toBeNull(); expect(native.invoke).not.toHaveBeenCalled();
});
