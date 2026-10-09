import { chooseScrollOption, scrollChoiceValue, waitScrollChoices } from "./test-scroll-picker";
import { create } from "@bufbuild/protobuf";
import { StrictMode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ActivityEntrySchema, ActivityKind, type ActivityEntry, ActivityService, EntityKind, InboxService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { LocalConnectionPresentation } from "./local-connection-presentation";
import { encode } from "./documents";
import { i18n, copy } from "./localization";

function resource(kind: EntityKind, name: string): Resource {
  const id = newRequestId();
  return create(ResourceSchema, { id, kind, sessionId: kind === EntityKind.SESSION ? id : "", revision: 1n, schemaVersion: 1, documentJson: encode({ name }) });
}

function fixture() {
  const projects = [resource(EntityKind.PROJECT, "Long project ".repeat(20)), resource(EntityKind.PROJECT, "Same name"), resource(EntityKind.PROJECT, "Same name")];
  const sessions = [resource(EntityKind.SESSION, "Long session ".repeat(20))];
  const calls: { kind: EntityKind; pageToken: string }[] = [];
  const failures = new Map<EntityKind, Code>();
  const gates = new Map<EntityKind, Promise<void>>();
  const empty = new Set<EntityKind>();
  const detail = vi.fn(async (request: { kind: EntityKind; id: string }) => ({ resource: [...projects, ...sessions].find(row => row.kind === request.kind && row.id === request.id) }));
  const activity = vi.fn(async (request: { projectId: string; sessionId: string; pageToken: string }): Promise<{ entries: ActivityEntry[]; nextPageToken: string }> => ({ entries: [], nextPageToken: request.pageToken ? "" : "activity-next" }));
  const stop = vi.fn(() => ({}));
  const preferences = create(NotificationPreferencesSchema, { revision: 1n });
  const transport = () => createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ version: "0.1.0", protocolVersion: 1 }), stopServer: stop });
    router.service(SessionService, { listSessions: () => ({ sessions: [] }) });
    router.service(InboxService, { getNotificationPreferences: () => ({ preferences }), listInbox: () => ({ entries: [] }) });
    router.service(ActivityService, { listActivity: activity });
    router.service(ResourceService, {
      getResource: detail,
      listResources: async (request) => {
        const kind = request.filter!.kind;
        const pageToken = request.filter!.pageToken;
        calls.push({ kind, pageToken });
        if (gates.has(kind)) await gates.get(kind);
        if (failures.has(kind)) throw new ConnectError("Fixture read failed", failures.get(kind));
        const rows = kind === EntityKind.PROJECT ? projects : kind === EntityKind.SESSION ? sessions : [];
        return { resources: empty.has(kind) ? [] : pageToken ? kind === EntityKind.PROJECT ? [projects[2]] : [] : rows, nextPageToken: pageToken || !rows.length ? "" : kind === EntityKind.PROJECT ? "projects-next" : "sessions-next" };
      },
      async *watchEvents(_request, context) {
        await new Promise<void>((resolve) => { if (context.signal.aborted) resolve(); else context.signal.addEventListener("abort", () => resolve(), { once: true }); });
      },
    });
  });
  return { projects, sessions, calls, failures, gates, empty, detail, activity, stop, transport };
}

async function enterActivity() {
  fireEvent.click(await screen.findByRole("button", { name: "Activity" }));
  const panel = screen.getByRole("region", { name: "Activity navigation and filters" });
  await waitScrollChoices(within(panel).getByRole("combobox", { name: "Session" }));
  return panel;
}

it("applies accepted filter pairs immediately and resets only the result cursor", async () => {
  const value = fixture();
  render(<StrictMode><App transport={value.transport()} localServer={<LocalConnectionPresentation><span>Fixture server footer</span></LocalConnectionPresentation>} /></StrictMode>);
  const panel = await enterActivity();
  const controls = within(panel);
  expect(panel.classList.contains("activity-sidebar")).toBe(true);
  expect(controls.getByRole("heading", { name: "FILTERS" })).toBeTruthy();
  const all = controls.getByRole("button", { name: "All activity" });
  expect(all.getAttribute("aria-pressed")).toBe("true");
  expect(all.querySelectorAll('svg[aria-hidden="true"]')).toHaveLength(2);
  expect(controls.queryByRole("button", { name: "Apply filters" })).toBeNull();
  await chooseScrollOption(controls.getByRole("combobox", { name: "Project" }), value.projects[1].id);
  await waitFor(() => expect(value.activity.mock.calls.at(-1)?.[0]).toMatchObject({ projectId: value.projects[1].id, sessionId: "", pageToken: "" }));
  await chooseScrollOption(controls.getByRole("combobox", { name: "Session" }), value.sessions[0].id);
  await waitFor(() => expect(value.activity.mock.calls.at(-1)?.[0]).toMatchObject({ projectId: value.projects[1].id, sessionId: value.sessions[0].id, pageToken: "" }));
  await chooseScrollOption(controls.getByRole("combobox", { name: "Project" }), value.projects[2].id);
  expect(scrollChoiceValue(controls.getByRole("combobox", { name: "Session" }))).toBe(value.sessions[0].id);

  await waitFor(() => expect(value.activity.mock.calls.at(-1)?.[0]).toMatchObject({ projectId: value.projects[2].id, sessionId: value.sessions[0].id, pageToken: "" }));
  expect(all.getAttribute("aria-pressed")).toBe("false");
  expect(all.querySelectorAll("svg")).toHaveLength(1);
  for (const reset of ["All activity", "Reset"]) {
    fireEvent.click(screen.getByRole("button", { name: "Load more Activity" }));
    await waitFor(() => expect(value.activity.mock.calls.at(-1)?.[0].pageToken).toBe("activity-next"));
    fireEvent.click(controls.getByRole("button", { name: reset }));
    // A fresh first page may already be cached. Verify its visible result, then
    // explicitly refresh to observe the exact applied query on the transport.
    expect(await screen.findByText("No activity yet.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(value.activity.mock.calls.at(-1)?.[0]).toMatchObject({ projectId: "", sessionId: "", pageToken: "" }));
    expect(scrollChoiceValue(controls.getByRole("combobox", { name: "Project" }))).toBe("");
    expect(scrollChoiceValue(controls.getByRole("combobox", { name: "Session" }))).toBe("");
    expect(controls.queryByText(/Last selected/)).toBeNull();
  }
  expect(value.detail.mock.calls.every(([request]) => [...value.projects, ...value.sessions].some(row => row.id === request.id && row.kind === request.kind))).toBe(true);
  expect(value.stop).not.toHaveBeenCalled();
  // Issue #1137 moves lifecycle controls to the persistent advanced panel;
  // Activity preserves the ordinary footer's generic connection presentation.
  expect(screen.getByText("Fixture server footer").closest("[hidden]")).toBeTruthy();
  expect(screen.getByText("This computer · Connected")).toBeTruthy();
}, 15000);

it("retains long labels, exact off-page identities, independent choice pages, accepted selections and scroll across navigation and reconnect", async () => {
  const value = fixture();
  const authority = { endpoint: "https://fixture.invalid", serverId: newRequestId() };
  const deviceId = newRequestId();
  const view = render(<App transport={value.transport()} pairingAuthority={authority} currentDeviceId={deviceId} />);
  const panel = await enterActivity();
  const controls = within(panel);
  await chooseScrollOption(controls.getByRole("combobox", { name: "Project" }), value.projects[0].id);
  await chooseScrollOption(controls.getByRole("combobox", { name: "Session" }), value.sessions[0].id);
  expect(controls.getByText(/Last selected project label:/).textContent).toContain("Long project ".repeat(20));
  expect(controls.getByText(/Selected session ID:/).textContent).toContain(value.sessions[0].id);
  const projectChoices = controls.getByRole("combobox", { name: "Project" }).closest(".resource-choice")!;
  fireEvent.click(controls.getByRole("combobox", { name: "Project" }));
  fireEvent.click(within(projectChoices as HTMLElement).getByRole("button", { name: "Load more Project" }));
  await waitFor(() => expect(value.calls).toContainEqual({ kind: EntityKind.PROJECT, pageToken: "projects-next" }));
  expect(value.calls.some((call) => call.kind === EntityKind.SESSION && call.pageToken)).toBe(false);
  expect(scrollChoiceValue(controls.getByRole("combobox", { name: "Project" }))).toBe(value.projects[0].id);
  expect(controls.getByRole("option", { name: "Long project ".repeat(20).trim() })).toBeTruthy();
  fireEvent.keyDown(controls.getByRole("combobox", { name: "Project" }), { key: "Escape" });
  const sessionChoices = controls.getByRole("combobox", { name: "Session" }).closest(".resource-choice")!;
  fireEvent.click(controls.getByRole("combobox", { name: "Session" }));
  fireEvent.click(within(sessionChoices as HTMLElement).getByRole("button", { name: "Load more Session" }));
  fireEvent.keyDown(controls.getByRole("combobox", { name: "Session" }), { key: "Escape" });
  await waitFor(() => expect(value.calls).toContainEqual({ kind: EntityKind.SESSION, pageToken: "sessions-next" }));
  const list = panel.closest(".sidebar-list") as HTMLElement;
  list.scrollTop = 123;
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expect(list.scrollTop).toBe(0);
  const activityCount = value.activity.mock.calls.length;
  fireEvent.click(screen.getByRole("button", { name: "Activity" }));
  expect(list.scrollTop).toBe(123);
  expect(scrollChoiceValue(controls.getByRole("combobox", { name: "Session" }))).toBe(value.sessions[0].id);
  view.rerender(<App transport={value.transport()} pairingAuthority={authority} currentDeviceId={deviceId} connectionEpoch={1} />);
  await waitFor(() => expect(value.calls.filter((call) => call.kind === EntityKind.SESSION && call.pageToken === "sessions-next").length).toBeGreaterThan(1));
  expect(list.scrollTop).toBe(123);
  expect(controls.getByText(/Last selected project label:/).textContent).toContain("Long project ".repeat(20));
  expect(value.activity.mock.calls.slice(activityCount).every(([request]) => request.projectId === value.projects[0].id && request.sessionId === value.sessions[0].id)).toBe(true);
  const resourceCount = value.calls.length;
  await waitFor(() => expect(value.activity.mock.calls.at(-1)?.[0].projectId).toBe(value.projects[0].id));
  expect(value.calls).toHaveLength(resourceCount);
  expect(within(projectChoices as HTMLElement).queryByRole("button", { name: "First choices" })).toBeNull();
  expect(within(sessionChoices as HTMLElement).queryByRole("button", { name: "First choices" })).toBeNull();
  expect(value.detail.mock.calls.every(([request]) => [...value.projects, ...value.sessions].some(row => row.id === request.id && row.kind === request.kind))).toBe(true);
  view.rerender(<App transport={value.transport()} pairingAuthority={{ ...authority, serverId: newRequestId() }} currentDeviceId={deviceId} />);
  await enterActivity();
  expect(screen.getByRole("region", { name: "Activity navigation and filters" }).querySelector<HTMLElement>("[role=combobox]")!.dataset.value).toBe("");
  expect(screen.queryByText(/Last selected project label:/)).toBeNull();
}, 15000);

it.each([Code.PermissionDenied, Code.Unauthenticated, Code.Unavailable, Code.DeadlineExceeded])("distinguishes failed choice reads (%s) from successful empty pages", async (code) => {
  const value = fixture();
  value.failures.set(EntityKind.SESSION, code);
  render(<App transport={value.transport()} />);
  fireEvent.click(await screen.findByRole("button", { name: "Activity" }));
  const panel = within(screen.getByRole("region", { name: "Activity navigation and filters" }));
  const message = code === Code.PermissionDenied || code === Code.Unauthenticated ? /server denied access to these choices/ : /server connection failed while loading these choices/;
  expect(await panel.findByText(message)).toBeTruthy();
  expect(panel.queryByText(/No selectable Session choices/)).toBeNull();
});

it("keeps cached choices and exact selected text after a failed refresh", async () => {
  const value = fixture();
  const transport = value.transport();
  const view = render(<App transport={transport} />);
  const panel = await enterActivity();
  await chooseScrollOption(within(panel).getByRole("combobox", { name: "Session" }), value.sessions[0].id);
  value.failures.set(EntityKind.SESSION, Code.Unavailable);
  view.rerender(<App transport={transport} connectionEpoch={1} />);
  expect(await within(panel).findByText(/Showing cached Session choices/)).toBeTruthy();
  expect(within(panel).queryByText(/No selectable Session choices/)).toBeNull();
  expect(scrollChoiceValue(within(panel).getByRole("combobox", { name: "Session" }))).toBe(value.sessions[0].id);
  expect(within(panel).getByText(/Last selected session label:/).textContent).toContain("Long session ".repeat(20));
  expect(value.detail.mock.calls.every(([request]) => [...value.projects, ...value.sessions].some(row => row.id === request.id && row.kind === request.kind))).toBe(true);
});

it("shows initial loading and successful empty current pages separately", async () => {
  const value = fixture();
  let finish!: () => void;
  value.gates.set(EntityKind.SESSION, new Promise<void>((resolve) => { finish = resolve; }));
  value.empty.add(EntityKind.SESSION);
  render(<App transport={value.transport()} />);
  fireEvent.click(await screen.findByRole("button", { name: "Activity" }));
  const panel = within(screen.getByRole("region", { name: "Activity navigation and filters" }));
  expect(panel.getByText("Loading Session choices…")).toBeTruthy();
  expect(panel.queryByText(/No selectable Session choices/)).toBeNull();
  await act(async () => finish());
  expect(await panel.findByText("No selectable Session choices are on this page. More choices are available.")).toBeTruthy();
  const choices = panel.getByRole("combobox", { name: "Session" }).closest(".resource-choice") as HTMLElement;
  fireEvent.click(panel.getByRole("combobox", { name: "Session" }));
  fireEvent.click(within(choices).getByRole("button", { name: "Load more Session" }));
  expect(await panel.findByText("No selectable Session choices are on this page.")).toBeTruthy();
  expect(within(choices).queryByRole("button", { name: "First choices" })).toBeNull();
});

it("keeps accepted compact filters and focus open; closes on explicit Reset or All activity", async () => {
  const value = fixture();
  let compact = true;
  const listeners = new Set<() => void>();
  vi.stubGlobal("matchMedia", () => ({ get matches() { return compact; }, addEventListener: (_name: string, listener: () => void) => listeners.add(listener), removeEventListener: (_name: string, listener: () => void) => listeners.delete(listener) }));
  try {
    render(<App transport={value.transport()} />);
    fireEvent.click(await screen.findByRole("button", { name: "Activity" }));
    const opener = screen.getByRole("button", { name: "Open activity filters" });
    fireEvent.click(opener);
    const panel = screen.getByRole("region", { name: "Activity navigation and filters" });
    await waitScrollChoices(within(panel).getByRole("combobox", { name: "Session" }));
    expect(screen.getAllByRole("dialog")).toHaveLength(1);
    await chooseScrollOption(within(panel).getByRole("combobox", { name: "Project" }), value.projects[1].id);
    expect(opener.getAttribute("aria-expanded")).toBe("true");
    await waitFor(() => expect(value.activity.mock.calls.at(-1)?.[0].projectId).toBe(value.projects[1].id));
    expect(document.activeElement).toBe(within(panel).getByRole("combobox", { name: "Project" }));
    fireEvent.click(within(panel).getByRole("button", { name: "Reset" }));
    expect(opener.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(opener);
    await chooseScrollOption(within(panel).getByRole("combobox", { name: "Project" }), value.projects[2].id);
    fireEvent.click(within(panel).getByRole("button", { name: "All activity" }));
    expect(opener.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(opener);
    await chooseScrollOption(within(panel).getByRole("combobox", { name: "Project" }), value.projects[2].id);
    await act(async () => { compact = false; listeners.forEach((listener) => listener()); });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(scrollChoiceValue(within(panel).getByRole("combobox", { name: "Project" }))).toBe(value.projects[2].id);
    expect(value.detail.mock.calls.every(([request]) => [...value.projects, ...value.sessions].some(row => row.id === request.id && row.kind === request.kind))).toBe(true);
  } finally { vi.unstubAllGlobals(); }
});

it.each(["Reset", "All activity"])("fences pending exact selection acceptance on %s without resetting selector pages", async reset => {
 const value = fixture();
 let release!: () => void;
 const pending = new Promise<void>(resolve => { release = resolve; });
 value.detail.mockImplementation(async request => { if (request.id === value.projects[1].id) await pending; return { resource: [...value.projects, ...value.sessions].find(row => row.id === request.id && row.kind === request.kind) }; });
 render(<App transport={value.transport()} />);
 const panel = await enterActivity(), controls = within(panel);
 const project = controls.getByRole("combobox", { name: "Project" });
 fireEvent.click(project);
 fireEvent.click(controls.getByRole("button", { name: "Load more Project" }));
 await waitFor(() => expect(value.calls).toContainEqual({ kind: EntityKind.PROJECT, pageToken: "projects-next" }));
 const reads = [...value.calls];
 fireEvent.click(document.querySelector<HTMLElement>(`[data-picker-id="${value.projects[1].id}"]`)!);
 await waitFor(() => expect((project as HTMLButtonElement).disabled).toBe(true));
 fireEvent.click(controls.getByRole("button", { name: reset }));
 expect(controls.getByRole("combobox", { name: "Project" })).toBe(project);
 await act(async () => release());
 await waitFor(() => expect((project as HTMLButtonElement).disabled).toBe(false));
 expect(scrollChoiceValue(project)).toBe("");
 expect(controls.queryByText(/Last selected project label:/)).toBeNull();
 expect(value.activity.mock.calls.every(([request]) => request.projectId === "" && request.sessionId === "")).toBe(true);
 expect(value.calls).toEqual(reads);
 fireEvent.click(project);
 expect(controls.getAllByRole("option", { name: "Same name" })).toHaveLength(2);
 expect(controls.queryByRole("button", { name: "Load more Project" })).toBeNull();
});

it("clears one accepted filter while preserving the other and restarts only Activity from the first page", async () => {
 const value = fixture(); render(<App transport={value.transport()} />);
 const controls = within(await enterActivity());
 const project = controls.getByRole("combobox", { name: "Project" }), session = controls.getByRole("combobox", { name: "Session" });
 await chooseScrollOption(project, value.projects[1].id); await chooseScrollOption(session, value.sessions[0].id);
 fireEvent.click(screen.getByRole("button", { name: "Load more Activity" }));
 await waitFor(() => expect(value.activity.mock.calls.at(-1)?.[0].pageToken).toBe("activity-next"));
 const reads = [...value.calls];
 await chooseScrollOption(project, "");
 await waitFor(() => expect(value.activity.mock.calls.at(-1)?.[0]).toMatchObject({ projectId: "", sessionId: value.sessions[0].id, pageToken: "" }));
 expect(scrollChoiceValue(session)).toBe(value.sessions[0].id);
 await chooseScrollOption(session, "");
 expect(scrollChoiceValue(project)).toBe("");
 expect(controls.getByRole("button", { name: "All activity" }).getAttribute("aria-pressed")).toBe("true");
 expect(value.calls).toEqual(reads);
 await chooseScrollOption(project, value.projects[1].id); await chooseScrollOption(session, value.sessions[0].id);
 await chooseScrollOption(session, "");
 await waitFor(() => expect(value.activity.mock.calls.at(-1)?.[0]).toMatchObject({ projectId: value.projects[1].id, sessionId: "", pageToken: "" }));
 expect(scrollChoiceValue(project)).toBe(value.projects[1].id);
});

function activityEntry(accountId: string) {
 return create(ActivityEntrySchema, { id: newRequestId(), accountId, sourceKind: EntityKind.SESSION, sourceRevision: 1n, observedAtUnixMs: 1790640000000n, kind: ActivityKind.EXECUTION_ACCEPTED });
}

it("rejects older filtered Activity responses after the newer accepted pair", async () => {
 const value = fixture(), older = newRequestId(), current = newRequestId();
 let release!: () => void;
 const pending = new Promise<void>(resolve => { release = resolve; });
 value.activity.mockImplementation(async request => {
  if (request.projectId === value.projects[1].id) { await pending; return { entries: [activityEntry(older)], nextPageToken: "" }; }
  return { entries: request.projectId === value.projects[2].id ? [activityEntry(current)] : [], nextPageToken: "" };
 });
 render(<App transport={value.transport()} />); const controls = within(await enterActivity());
 await chooseScrollOption(controls.getByRole("combobox", { name: "Project" }), value.projects[1].id);
 await waitFor(() => expect(value.activity.mock.calls.at(-1)?.[0].projectId).toBe(value.projects[1].id));
 await chooseScrollOption(controls.getByRole("combobox", { name: "Project" }), value.projects[2].id);
 await screen.findByText(new RegExp(current));
 await act(async () => release());
 expect(screen.queryByText(new RegExp(older))).toBeNull();
 expect(screen.getByText(new RegExp(current))).toBeTruthy();
});

it("retains accepted filters on rejected exact validation and separates new-filter failure from same-filter refresh", async () => {
 const value = fixture(), accountId = newRequestId(); let failed = false;
 value.activity.mockImplementation(async request => { if (failed || request.projectId === value.projects[2].id) throw new ConnectError("Fixture Activity unavailable", Code.Unavailable); return { entries: [activityEntry(accountId)], nextPageToken: "" }; });
 render(<App transport={value.transport()} />); const controls = within(await enterActivity());
 const project = controls.getByRole("combobox", { name: "Project" });
 await chooseScrollOption(project, value.projects[1].id); await screen.findByText(new RegExp(accountId));
 value.detail.mockImplementationOnce(async () => { throw new ConnectError("Fixture selected resource unavailable", Code.Unavailable); });
 fireEvent.click(project); fireEvent.click(document.querySelector<HTMLElement>(`[data-picker-id="${value.projects[2].id}"]`)!);
 await controls.findByText(/server connection failed while loading these choices/);
 expect(scrollChoiceValue(project)).toBe(value.projects[1].id);
 expect(value.activity.mock.calls.at(-1)?.[0].projectId).toBe(value.projects[1].id);
 await chooseScrollOption(project, value.projects[2].id);
 await within(screen.getAllByRole("heading", { name: "Activity", level: 2 }).find(heading => heading.closest(".page"))!.closest("section")!).findByRole("alert");
 expect(screen.queryByText(new RegExp(accountId))).toBeNull();
 expect(screen.queryByText(/refresh failed.*last activity rows/)).toBeNull();
 await chooseScrollOption(project, value.projects[1].id); await screen.findByText(new RegExp(accountId));
 const before = value.activity.mock.calls.length;
 await chooseScrollOption(project, value.projects[1].id);
 expect(value.activity.mock.calls).toHaveLength(before);
 failed = true; fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
 await screen.findByText(/refresh failed.*last activity rows/);
 expect(screen.getByText(new RegExp(accountId))).toBeTruthy();
});

it("has no Apply control in either language and keeps accepted filters during translation", async () => {
 const value = fixture();render(<App transport={value.transport()} />);
 const panel=await enterActivity();const controls=within(panel);
 await chooseScrollOption(controls.getByRole("combobox",{name:"Project"}),value.projects[1].id);
 await waitFor(()=>expect(value.activity.mock.calls.at(-1)?.[0].projectId).toBe(value.projects[1].id));
 const before=value.activity.mock.calls.length;
 expect(controls.queryByRole("button",{name:"Apply filters"})).toBeNull();
 await act(()=>i18n.changeLanguage("ko"));
 expect(controls.queryByRole("button",{name:"필터 적용"})).toBeNull();
 expect(controls.getByRole("button",{name:copy("views.reset_daee76")})).toBeTruthy();
 expect(scrollChoiceValue(controls.getByRole("combobox",{name:copy("views.project_985959")}))).toBe(value.projects[1].id);
 expect(value.activity.mock.calls).toHaveLength(before);
});
