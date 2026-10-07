import { chooseScrollOption, scrollChoiceValue, waitScrollChoices } from "./test-scroll-picker";
import { create } from "@bufbuild/protobuf";
import { StrictMode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ActivityService, EntityKind, InboxService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";

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
  const detail = vi.fn((request: { kind: EntityKind; id: string }) => ({ resource: [...projects, ...sessions].find(row => row.kind === request.kind && row.id === request.id) }));
  const activity = vi.fn((request: { projectId: string; sessionId: string; pageToken: string }) => ({ entries: [], nextPageToken: request.pageToken ? "" : "activity-next" }));
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

it("keeps filter drafts independent and sends exact applied/reset IDs and only resets the result cursor", async () => {
  const value = fixture();
  render(<StrictMode><App transport={value.transport()} localServer={<span>Fixture server footer</span>} /></StrictMode>);
  const panel = await enterActivity();
  const controls = within(panel);
  expect(panel.classList.contains("activity-sidebar")).toBe(true);
  expect(controls.getByRole("heading", { name: "FILTERS" })).toBeTruthy();
  const all = controls.getByRole("button", { name: "All activity" });
  expect(all.getAttribute("aria-pressed")).toBe("true");
  expect(all.querySelectorAll('svg[aria-hidden="true"]')).toHaveLength(2);
  const before = value.activity.mock.calls.length;
  await chooseScrollOption(controls.getByRole("combobox", { name: "Project" }), value.projects[1].id);
  await chooseScrollOption(controls.getByRole("combobox", { name: "Session" }), value.sessions[0].id);
  await chooseScrollOption(controls.getByRole("combobox", { name: "Project" }), value.projects[2].id);
  expect(scrollChoiceValue(controls.getByRole("combobox", { name: "Session" }))).toBe(value.sessions[0].id);
  expect(all.getAttribute("aria-pressed")).toBe("true");
  expect(value.activity.mock.calls).toHaveLength(before);
  fireEvent.click(controls.getByRole("button", { name: "Apply filters" }));
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
  expect(screen.queryByText("Fixture server footer")).toBeNull();
  expect(screen.getByText("This computer · Connected")).toBeTruthy();
}, 15000);

it("retains long labels, exact off-page identities, independent choice pages, drafts and scroll across navigation and reconnect", async () => {
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
  expect(value.activity.mock.calls.slice(activityCount).every(([request]) => !request.projectId && !request.sessionId)).toBe(true);
  const resourceCount = value.calls.length;
  fireEvent.click(controls.getByRole("button", { name: "Apply filters" }));
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

it("keeps compact filter edits open and closes only on Apply or Reset without replacing controllers", async () => {
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
    fireEvent.click(within(panel).getByRole("button", { name: "Apply filters" }));
    expect(opener.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(opener);
    fireEvent.click(within(panel).getByRole("button", { name: "Reset" }));
    expect(opener.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(opener);
    await chooseScrollOption(within(panel).getByRole("combobox", { name: "Project" }), value.projects[2].id);
    await act(async () => { compact = false; listeners.forEach((listener) => listener()); });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(scrollChoiceValue(within(panel).getByRole("combobox", { name: "Project" }))).toBe(value.projects[2].id);
    expect(value.detail.mock.calls.every(([request]) => [...value.projects, ...value.sessions].some(row => row.id === request.id && row.kind === request.kind))).toBe(true);
  } finally { vi.unstubAllGlobals(); }
});
