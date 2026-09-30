import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { InboxService, NotificationKind, NotificationState, newRequestId } from "@delinoio/delidev-api-client";
import { NativeNotificationSettings, NotificationPresentation } from "./notification-presentation";
import { Settings } from "./settings";

const native = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ isTauri: () => true, invoke: native.invoke }));
beforeEach(() => native.invoke.mockReset());

it("drops a closed Settings permission wait without applying its late result to a new opening or the shared cache", async () => {
  let finish!: (value: unknown) => void;
  native.invoke.mockImplementation(async (command) => command === "request_notification_permission"
    ? new Promise((resolve) => { finish = resolve; })
    : { permission: "not-determined", problem: "none" });
  const transport = createRouterTransport(() => {});
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { gcTime: 0 } } });
  client.setQueryData(["native-notification-readiness"], { permission: "shared-sentinel" });
  const body = (visible: boolean) => <TransportProvider transport={transport}><QueryClientProvider client={client}><Settings visible={visible} close={() => {}} /></QueryClientProvider></TransportProvider>;
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
