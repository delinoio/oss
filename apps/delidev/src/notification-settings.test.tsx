import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { InboxService, NotificationPreferencesSchema, SituationNotificationPreferencesSchema } from "@delinoio/delidev-api-client";
import { NotificationSettings } from "./notification-settings";
import { MutationIntents } from "./mutation";
import { StrictMode } from "react";
import { SettingsLifetime } from "./settings-lifetime";
import { SidebarOutletProvider } from "./sidebar-context";
import { NotificationProvider } from "./toast-notifications";
import { SettingsTasks, SettingsTaskBackground } from "./settings-task";

function fixture(granular = false) {
  let preferences = create(NotificationPreferencesSchema, { revision: 1n, interactions: true, terminals: false, situations: granular ? create(SituationNotificationPreferencesSchema, { questions:true,approvals:true,serverLost:true,workerUnavailable:true,quotaExhausted:true,scheduleStartFailed:true,scheduleOffline:true }) : undefined });
  const save = vi.fn(async (request: { preferences?: typeof preferences }) => {
    preferences = create(NotificationPreferencesSchema, { ...request.preferences!, revision: preferences.revision + 1n });
    return { preferences };
  });
  const read = vi.fn(async () => ({ preferences }));
  const transport = createRouterTransport((router) => router.service(InboxService, { getNotificationPreferences: read, setNotificationPreferences: save }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><NotificationSettings active={active} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { view, save, read, transport, client, change: async () => { preferences = create(NotificationPreferencesSchema, { revision: 9n, interactions: false, terminals: false }); await client.invalidateQueries(); } };
}

it.each(["X", "Escape"] as const)("keeps the notification summary mounted and restores its original opener after %s", async dismissal => {
  const value = fixture();
  render(<SettingsTasks><div className="settings-content"><h1>Notifications category</h1><SettingsTaskBackground>{value.view()}</SettingsTaskBackground></div></SettingsTasks>);
  const opener = await screen.findByRole("button", { name: "Edit notification preferences" });
  const summary = opener.closest("section.notification-preferences")!;
  expect(summary.tagName).toBe("SECTION");
  expect(summary.getAttribute("aria-labelledby")).toBe(summary.querySelector("h2")!.id);
  expect(summary.querySelector("form")).toBeNull();
  fireEvent.click(opener);
  expect(summary.isConnected).toBe(true); expect(opener.isConnected).toBe(true);
  const dialog = screen.getByRole("dialog");
  if (dismissal === "X") fireEvent.click(screen.getByRole("button", { name: "Close Edit notification preferences" }));
  else fireEvent(dialog, new Event("cancel", { cancelable: true }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByRole("button", { name: "Edit notification preferences" })).toBe(opener);
  await waitFor(() => expect(document.activeElement).toBe(opener));
  expect(value.save).not.toHaveBeenCalled();
});

it("shows one toast only after notification preferences are acknowledged", async () => {
  const value = fixture();
  value.save.mockRejectedValueOnce(new ConnectError("Acknowledgment lost", Code.Unavailable));
  render(<NotificationProvider>{value.view()}</NotificationProvider>);
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Save notification preferences" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Save notification preferences" }));
  const retry = await screen.findByRole("button", { name: "Retry the same notification preferences" });
  expect(screen.queryByText("Notification preferences saved.")).toBeNull();
  fireEvent.click(retry);
  expect(await screen.findByText("Notification preferences saved.")).toBeTruthy();
  expect(screen.getAllByText("Notification preferences saved.")).toHaveLength(1);
  expect(value.save).toHaveBeenCalledTimes(2);
});

it("never shows a success toast for a late save after the Settings visit is disposed", async () => {
  const value = fixture();
  const pending = deferred<Awaited<ReturnType<typeof value.save>>>();
  value.save.mockImplementationOnce(() => pending.promise);
  const view = (visible: boolean) => <NotificationProvider><TransportProvider transport={value.transport}><QueryClientProvider client={value.client}>{visible ? <SettingsLifetime>{() => <MutationIntents><NotificationSettings active /></MutationIntents>}</SettingsLifetime> : <p>Other destination</p>}</QueryClientProvider></TransportProvider></NotificationProvider>;
  const mounted = render(view(true));
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Save notification preferences" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Save notification preferences" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  mounted.rerender(view(false));
  await act(async () => pending.resolve({ preferences: create(NotificationPreferencesSchema, { revision: 2n, interactions: true, terminals: false }) }));
  expect(screen.queryByText("Notification preferences saved.")).toBeNull();
});

it("retains stale notification drafts across settings visibility and never saves over a changed revision", async () => {
  const value = fixture(), mounted = render(value.view());
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Save notification preferences" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("checkbox", { name: "Execution completion, failure and interruption" }));
  mounted.rerender(value.view(false)); mounted.rerender(value.view());
  expect((screen.getByRole("checkbox", { name: "Execution completion, failure and interruption" }) as HTMLInputElement).checked).toBe(true);
  await act(value.change);
  expect(await screen.findByText(/These preferences changed elsewhere/)).toBeTruthy();
  expect((screen.getByRole("button", { name: "Save notification preferences" }) as HTMLButtonElement).disabled).toBe(true);
  expect(value.save).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Close Edit notification preferences" }));
  expect(screen.queryByRole("checkbox")).toBeNull();
  expect(screen.getAllByText("Disabled")).toHaveLength(2);
});

it("retries only the original uncertain preference request after current preferences change", async () => {
  const value = fixture(); value.save.mockRejectedValueOnce(new ConnectError("Acknowledgment lost", Code.Unavailable));
  const mounted = render(value.view());
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Save notification preferences" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("checkbox", { name: "Questions and approval requests" }));
  fireEvent.click(screen.getByRole("button", { name: "Save notification preferences" }));
  await screen.findByRole("button", { name: "Retry the same notification preferences" });
  mounted.rerender(value.view(false)); mounted.rerender(value.view());
  await act(value.change);
  fireEvent.click(screen.getByRole("button", { name: "Retry the same notification preferences" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[0][0]).toEqual(value.save.mock.calls[1][0]);
  expect(value.save.mock.calls[0][0].preferences).toMatchObject({ revision: 1n, interactions: false, terminals: false });
});

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

it.each(["success", "failure"] as const)("ignores a closed notification editor's late %s while a fresh draft is open", async outcome => {
  const value = fixture(), pending = deferred<Awaited<ReturnType<typeof value.save>>>();
  value.save.mockReturnValueOnce(pending.promise);
  render(<NotificationProvider>{value.view()}</NotificationProvider>);
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Save notification preferences" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Save notification preferences" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Close Edit notification preferences" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Edit notification preferences" }));
  const fresh = screen.getByRole("dialog"), checkbox = screen.getByRole("checkbox", { name: "Execution completion, failure and interruption" }) as HTMLInputElement;
  fireEvent.click(checkbox); checkbox.focus();
  await act(async () => outcome === "success" ? pending.resolve({ preferences: create(NotificationPreferencesSchema, { revision: 2n }) }) : pending.reject(new ConnectError("Lost acknowledgment", Code.Unavailable)));
  expect(screen.getByRole("dialog")).toBe(fresh); expect(checkbox.checked).toBe(true);
  expect(document.activeElement).toBe(checkbox);
  expect(screen.queryByText("Notification preferences saved.")).toBeNull();
  expect(screen.queryByRole("button", { name: "Retry the same notification preferences" })).toBeNull();
  expect(value.save).toHaveBeenCalledTimes(1);
});

it("shows confirmed text values, exact guidance and no implicit write controls", async () => {
  const value = fixture(); render(value.view());
  await screen.findByRole("button", { name: "Edit notification preferences" });
  expect(screen.getByRole("heading", { name: "Notifications" })).toBeTruthy();
  for (const text of ["Choose which updates this client receives.", "For this client on the selected server", "Enabled", "Disabled", "Inbox requests stay available even when notifications are off.", "Opening a notification never marks an item read, answers a request, approves work or resumes a session."]) expect(screen.getByText(text)).toBeTruthy();
  expect(screen.queryByRole("checkbox")).toBeNull(); expect(screen.queryByRole("switch")).toBeNull();
  const details = screen.getByText("About notification delivery").closest("details")!;
  expect(details.open).toBe(false);
  expect(details.textContent).toContain("A submitted notification does not prove that its banner was displayed.");
  expect(details.textContent).toContain("Reading an inbox item never answers it.");
  expect(value.save).not.toHaveBeenCalled();
});

it("focuses labeled checkboxes after Edit and waits for Save refetch before returning focus", async () => {
  const value = fixture(); render(value.view());
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Save notification preferences" }) as HTMLButtonElement).disabled).toBe(false));
  const first = screen.getByRole("checkbox", { name: "Questions and approval requests" });
  expect(document.activeElement).toBe(first);
  expect(document.getElementById(first.getAttribute("aria-describedby")!)?.textContent).toBe("When a session needs your answer or approval.");
  fireEvent.click(screen.getByRole("checkbox", { name: "Execution completion, failure and interruption" }));
  expect(value.save).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Close Edit notification preferences" }));
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Edit notification preferences" }));
  expect(screen.getByText("Disabled")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Edit notification preferences" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Save notification preferences" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("checkbox", { name: "Execution completion, failure and interruption" }));
  const refetch = deferred<Awaited<ReturnType<typeof value.read>>>();
  value.read.mockImplementationOnce(() => refetch.promise);
  fireEvent.click(screen.getByRole("button", { name: "Save notification preferences" }));
  await waitFor(() => expect(screen.queryByRole("checkbox")).toBeNull());
  const returned = screen.getByRole("button", { name: "Edit notification preferences" }) as HTMLButtonElement;
  expect(returned.disabled).toBe(true); expect(document.activeElement).not.toBe(returned);
  expect(value.save.mock.calls[0][0].preferences).toMatchObject({ revision: 1n, interactions: true, terminals: true });
  await act(async () => refetch.resolve(await value.read()));
  await waitFor(() => expect(returned.disabled).toBe(false));
  expect(document.activeElement).toBe(returned);
});

it("never fabricates initial values and preserves visibly stale cached values after a failed refresh", async () => {
  const value = fixture(); const initial = deferred<Awaited<ReturnType<typeof value.read>>>();
  value.read.mockImplementationOnce(() => initial.promise); render(value.view());
  expect(screen.getByText("Loading notification preferences…")).toBeTruthy();
  expect(screen.queryByText("Enabled")).toBeNull(); expect(screen.queryByText("Disabled")).toBeNull();
  await act(async () => initial.reject(new ConnectError("Read unavailable", Code.Unavailable)));
  expect(screen.getByText("Notification preferences are unavailable until this server can be read.")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Edit notification preferences" })).toBeNull();
  await act(() => value.client.invalidateQueries()); await screen.findByText("Enabled");
  value.read.mockRejectedValueOnce(new ConnectError("Refresh unavailable", Code.Unavailable));
  await act(() => value.client.invalidateQueries());
  expect(screen.getByText("Enabled")).toBeTruthy(); expect(screen.getByText("Disabled")).toBeTruthy();
  expect(await screen.findByText(/displayed values may be out of date/)).toBeTruthy();
  expect((screen.getByRole("button", { name: "Edit notification preferences" }) as HTMLButtonElement).disabled).toBe(true);
  expect(value.save).not.toHaveBeenCalled();
});

for (const interruption of ["focus", "modal", "drawer", "inactivity"] as const) it(`discards pending Save focus after ${interruption} and never replays it`, async () => {
  const value = fixture();
  const body = (active = true, drawerOpen = false) => <SidebarOutletProvider target={null} closeDrawer={() => {}} drawerOpen={drawerOpen}>{value.view(active)}<button>Elsewhere</button></SidebarOutletProvider>;
  const mounted = render(body());
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Save notification preferences" }) as HTMLButtonElement).disabled).toBe(false));
  const refetch = deferred<Awaited<ReturnType<typeof value.read>>>(); value.read.mockImplementationOnce(() => refetch.promise);
  fireEvent.click(screen.getByRole("button", { name: "Save notification preferences" }));
  await waitFor(() => expect(screen.queryByRole("checkbox")).toBeNull());
  let modal: HTMLDialogElement | undefined;
  if (interruption === "focus") screen.getByRole("button", { name: "Elsewhere" }).focus();
  if (interruption === "modal") { modal = document.createElement("dialog"); modal.setAttribute("open", ""); document.body.append(modal); await act(async () => {}); }
  if (interruption === "drawer") mounted.rerender(body(true, true));
  if (interruption === "inactivity") mounted.rerender(body(false));
  await act(async () => refetch.resolve(await value.read()));
  modal?.remove(); mounted.rerender(body());
  await waitFor(() => expect((screen.getByRole("button", { name: "Edit notification preferences" }) as HTMLButtonElement).disabled).toBe(false));
  expect(document.activeElement).not.toBe(screen.getByRole("button", { name: "Edit notification preferences" }));
});

it("retains disclosure/draft on reconnect but discards departed state and late save focus under Strict Mode", async () => {
  const value = fixture();
  const body = (visible: boolean, transport = value.transport) => <StrictMode><TransportProvider transport={transport}><QueryClientProvider client={value.client}>{visible ? <SettingsLifetime>{() => <MutationIntents><NotificationSettings active /></MutationIntents>}</SettingsLifetime> : null}<button>Sibling</button></QueryClientProvider></TransportProvider></StrictMode>;
  const mounted = render(body(true));
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Save notification preferences" }) as HTMLButtonElement).disabled).toBe(false));
  const details = screen.getByText("About notification delivery").closest("details")!; details.open = true;
  const replacement = createRouterTransport((router) => router.service(InboxService, { getNotificationPreferences: value.read, setNotificationPreferences: value.save }));
  mounted.rerender(body(true, replacement));
  expect(details.open).toBe(true); expect(screen.getByRole("checkbox", { name: "Questions and approval requests" })).toBeTruthy();
  const save = deferred<Awaited<ReturnType<typeof value.save>>>(); value.save.mockImplementationOnce(() => save.promise);
  fireEvent.click(screen.getByRole("button", { name: "Save notification preferences" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  mounted.rerender(body(false)); screen.getByRole("button", { name: "Sibling" }).focus();
  mounted.rerender(body(true)); await screen.findByRole("button", { name: "Edit notification preferences" });
  const readCount = value.read.mock.calls.length;
  await act(async () => save.resolve({ preferences: create(NotificationPreferencesSchema, { revision: 2n, interactions: true, terminals: false }) }));
  expect(value.read.mock.calls.length).toBe(readCount); expect(value.save).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("checkbox")).toBeNull(); expect(screen.queryByRole("button", { name: "Retry the same notification preferences" })).toBeNull();
  expect(screen.getByText("About notification delivery").closest("details")!.open).toBe(false);
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Sibling" }));
});

it.each(["focus", "pointer"])("discards deferred Edit focus after in-panel %s interaction during refetch", async (interaction) => {
  const value = fixture();
  let finishRead!: (result: Awaited<ReturnType<typeof value.read>>) => void;
  render(value.view());
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Save notification preferences" }) as HTMLButtonElement).disabled).toBe(false));
  value.read.mockImplementationOnce(() => new Promise(resolve => { finishRead = resolve; }));
  fireEvent.click(screen.getByRole("button", { name: "Save notification preferences" }));
  const edit = await screen.findByRole("button", { name: "Edit notification preferences" }) as HTMLButtonElement;
  await waitFor(() => { expect(finishRead).toBeTypeOf("function"); expect(edit.disabled).toBe(true); });
  const returned = vi.spyOn(edit, "focus");
  const disclosure = screen.getByText("About notification delivery");
  if (interaction === "focus") disclosure.focus();
  else fireEvent.pointerDown(disclosure);
  await act(async () => finishRead({ preferences: create(NotificationPreferencesSchema, { revision: 2n, interactions: true, terminals: false }) }));
  await waitFor(() => expect(edit.disabled).toBe(false));
  expect(returned).not.toHaveBeenCalled();
  if (interaction === "focus") expect(document.activeElement).toBe(disclosure);
});

 it("edits a complete twelve-choice generation with separate account recovery consent", async () => {
  const value=fixture(true);render(value.view());
  await screen.findByText("Managed per account");
  expect(screen.queryAllByRole("checkbox")).toHaveLength(0);
  expect(screen.getByRole("heading",{name:"Connections"})).toBeTruthy();
  fireEvent.click(screen.getByRole("button",{name:"Edit notification preferences"}));
  const dialog=screen.getByRole("dialog");
  expect(screen.getAllByRole("checkbox")).toHaveLength(12);
  const questions=dialog.querySelector('input[data-settings-search-target="notification-questions"]') ?? screen.getAllByRole("checkbox",{name:"Questions"}).find(v=>!(v as HTMLInputElement).disabled)!;
  fireEvent.click(questions);
  expect(value.save).not.toHaveBeenCalled();
  await waitFor(()=>expect((screen.getByRole("button",{name:"Save notification preferences"}) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button",{name:"Save notification preferences"}));
  await waitFor(()=>expect(value.save).toHaveBeenCalledTimes(1));
  const sent=value.save.mock.calls[0][0].preferences!;
  expect(sent.revision).toBe(1n);expect(sent.situations?.questions).toBe(false);
  expect(sent.situations?.approvals).toBe(true);expect(sent.situations?.serverRestored).toBe(false);
  expect(sent.interactions).toBe(true);expect(sent.terminals).toBe(false);
 });
