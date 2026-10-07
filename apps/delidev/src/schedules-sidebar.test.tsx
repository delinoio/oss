// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { StrictMode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, InboxService, ResourceSchema, ResourceService, ScheduleService, SessionService, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { chooseScrollOption } from "./test-scroll-picker";
import { ResourceChoice } from "./configuration-fields";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { Schedules } from "./schedules";
import { SidebarOutletProvider } from "./sidebar-context";

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => { resolve = done; });
  return { promise, resolve };
}

function fixture() {
  const project = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Selected project" }) });
  const definition = { prompt: "Retained prompt", project_id: project.id, agent_id: newRequestId(), machine_id: newRequestId(), workspace: "worktree", mode: "plan", cron: "0 9 * * 1-5", timezone: "UTC", overlap: "wait" };
  const row = (name: string, enabled: boolean, next_run_at: string) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SCHEDULE, revision: 3n, schemaVersion: 1, documentJson: encode({ definition: { ...definition, name, enabled }, next_run_at }) });
  const schedules = [row("Morning review", true, "2026-10-01T00:00:00Z"), row("Weekly cleanup", false, ""), row("Release check", true, "2026-10-02T01:00:00Z")];
  const list = vi.fn(async (request: { pageSize: number; pageToken: string; projectId: string; enabled?: boolean }) => ({ schedules, nextPageToken: request.pageToken ? "" : "schedule-next" }));
  const choices = vi.fn(async (_request: { filter?: { pageSize: number; pageToken: string; kind: EntityKind } }) => ({ resources: [project], nextPageToken: "project-next" }));
  const history = vi.fn(async (_request: { scheduleId: string; pageSize: number; pageToken: string }) => ({ occurrences: [] }));
  const run = vi.fn(async (_request: unknown) => ({ occurrence: create(ResourceSchema, { id: newRequestId(), kind: EntityKind.OCCURRENCE, revision: 1n, schemaVersion: 1 }) }));
  const transport = () => createRouterTransport((router) => {
    router.service(ScheduleService, { listSchedules: list, getSchedule: (request) => ({ schedule: schedules.find((schedule) => schedule.id === request.id) }), listScheduleOccurrences: history, runScheduleNow: run });
    router.service(ResourceService, { listResources: choices, getResource: request => ({ resource: request.id === project.id ? project : undefined }) });
    router.service(SessionService, { listSessions: () => ({ sessions: [] }) });
    router.service(SystemService, { getStatus: () => ({ version: "0.1.0", protocolVersion: 1 }) });
    router.service(InboxService, { listInbox: () => ({ entries: [] }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const initialTransport = transport();
  const closeDrawer = vi.fn();
  const view = (active = true, target: HTMLElement | null = null) => <StrictMode><TransportProvider transport={initialTransport}><QueryClientProvider client={client}><MutationIntents><SidebarOutletProvider target={target} closeDrawer={closeDrawer} drawerOpen><Schedules active={active} open={() => {}} /></SidebarOutletProvider></MutationIntents></QueryClientProvider></TransportProvider></StrictMode>;
  return { project, schedules, list, choices, history, run, transport, initialTransport, client, closeDrawer, view };
}

function pane() { return within(screen.getByRole("region", { name: "Schedules navigation and filters" })); }
function disclosure() { return pane().getByRole("button", { name: "Retained history" }); }
function selectMorning() { fireEvent.click(pane().getByRole("button", { name: /^Morning review/ })); }

it("shows explicit state and complete UTC with selection over the whole row", async () => {
  const value = fixture();
  render(value.view());
  const morning = await screen.findByRole("button", { name: /^Morning review/ });
  expect(morning.textContent).toContain("Enabled");
  expect(morning.textContent).toContain("Next run (UTC): 2026-10-01T00:00:00Z");
  expect(pane().getByRole("button", { name: /^Weekly cleanup/ }).textContent).toContain("PausedNext run (UTC): None scheduled");
  expect(pane().getByRole("heading", { name: "Saved schedules" })).toBeTruthy();
  expect(pane().getByRole("button", { name: "Refresh" }).querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  fireEvent.click(morning);
  expect(morning.getAttribute("aria-current")).toBe("true");
  expect(value.closeDrawer).toHaveBeenCalledTimes(1);
});

it("retains independent selector pages while immediate status and project filters reset only schedules", async () => {
  const value = fixture();
  render(value.view());
  await screen.findByRole("button", { name: /^Morning review/ });
  fireEvent.click(pane().getByRole("button", { name: "Load more Saved schedules" }));
  const projectPicker = pane().getByRole("combobox", { name: "Filter by project" });
  fireEvent.click(projectPicker);
  fireEvent.click(await screen.findByRole("button", { name: "Load more Filter by project" }));
  fireEvent.keyDown(projectPicker, { key: "Escape" });
  await waitFor(() => expect(value.list.mock.lastCall?.[0].pageToken).toBe("schedule-next"));
  await waitFor(() => expect(value.choices.mock.lastCall?.[0].filter?.pageToken).toBe("project-next"));
  for (const [name, enabled] of [["Enabled", true], ["Paused", false], ["All schedules", undefined]] as const) {
    fireEvent.click(pane().getByRole("button", { name }));
    await waitFor(() => expect(value.list.mock.calls.some(([request]) => request.pageToken === "" && request.pageSize === 50 && request.enabled === enabled)).toBe(true));
    expect(pane().getByRole("button", { name }).getAttribute("aria-pressed")).toBe("true");
    expect(value.choices.mock.lastCall?.[0].filter).toMatchObject({ pageToken: "project-next", pageSize: 50 });
  }
  await chooseScrollOption(projectPicker, value.project.id);
  await waitFor(() => expect(value.list.mock.lastCall?.[0]).toMatchObject({ pageToken: "", projectId: value.project.id, pageSize: 50 }));
  expect(value.choices.mock.lastCall?.[0].filter?.pageToken).toBe("project-next");
  expect(value.history).not.toHaveBeenCalled();
  expect(value.run).not.toHaveBeenCalled();
});

it("refreshes only accepted schedule ranges without discovering an unseen tail", async () => {
  const value = fixture();
  value.list.mockImplementation(async request => ({ schedules: value.schedules, nextPageToken: request.pageToken ? "unseen-tail" : "schedule-next" }));
  render(value.view());
  await screen.findByRole("button", { name: /^Morning review/ });
  fireEvent.click(pane().getByRole("button", { name: "Load more Saved schedules" }));
  await waitFor(() => expect(value.list.mock.lastCall?.[0].pageToken).toBe("schedule-next"));
  await waitFor(() => expect((pane().getByRole("button", { name: "Refresh" }) as HTMLButtonElement).disabled).toBe(false));
  const before = value.list.mock.calls.length;
  fireEvent.click(pane().getByRole("button", { name: "Refresh" }));
  await waitFor(() => expect(value.list.mock.calls.length).toBe(before + 2));
  expect(value.list.mock.calls.slice(before).map(([request]) => request.pageToken)).toEqual(["", "schedule-next"]);
  expect(pane().queryByRole("button", { name: "First" })).toBeNull();
});

it("distinguishes initial loading and successful empty first and later pages", async () => {
  const value = fixture(), gate = deferred();
  value.list.mockImplementation(async (request) => { await gate.promise; return { schedules: [], nextPageToken: request.pageToken ? "" : "schedule-next" }; });
  render(value.view());
  expect(screen.getByText("Loading schedules…")).toBeTruthy();
  expect(screen.queryByText("No saved schedules.")).toBeNull();
  await act(async () => gate.resolve());
  const empty = await screen.findByText("No saved schedules.");
  expect(empty.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  await waitFor(() => expect((pane().getByRole("button", { name: "Load more Saved schedules" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(pane().getByRole("button", { name: "Load more Saved schedules" }));
  await screen.findByText("All loaded Saved schedules are shown.");
  expect(pane().queryByRole("button", { name: "First" })).toBeNull();
  expect(pane().queryByRole("button", { name: "Load more Saved schedules" })).toBeNull();
});

it.each([Code.PermissionDenied, Code.Unauthenticated, Code.Unavailable])("keeps first-load failure %s separate from successful empty claims", async (code) => {
  const value = fixture();
  value.list.mockRejectedValue(new ConnectError("Untrusted server text", code));
  render(value.view());
  expect(await screen.findByRole("alert")).toBeTruthy();
  expect(screen.queryByText("Untrusted server text")).toBeNull();
  expect(screen.queryByText("No saved schedules.")).toBeNull();
  expect(screen.queryByText("No schedules on this page.")).toBeNull();
});

it("keeps same-scope cached rows after failure and drops them when the filter scope changes", async () => {
  const value = fixture();
  render(value.view());
  await screen.findByRole("button", { name: /^Morning review/ });
  value.list.mockRejectedValue(new ConnectError("Offline", Code.Unavailable));
  fireEvent.click(pane().getByRole("button", { name: "Refresh" }));
  await screen.findByText("Refresh failed. Showing the previous page.");
  expect(pane().getByRole("button", { name: /^Morning review/ }).textContent).toContain("2026-10-01T00:00:00Z");
  fireEvent.click(pane().getByRole("button", { name: "Enabled" }));
  await waitFor(() => expect(value.list.mock.lastCall?.[0].enabled).toBe(true));
  expect(pane().queryByRole("button", { name: /^Morning review/ })).toBeNull();
  expect(screen.queryByText("Refresh failed. Showing the previous page.")).toBeNull();
  expect(screen.queryByText("No saved schedules.")).toBeNull();
});

it("focuses only explicit history expansion, keeps the hidden form and draft, and submits the trimmed ID", async () => {
  const value = fixture(), rendered = render(value.view());
  await screen.findByRole("button", { name: /^Morning review/ });
  const toggle = disclosure(), regionId = toggle.getAttribute("aria-controls")!;
  const region = document.getElementById(regionId)!;
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  expect(region.hidden).toBe(true);
  expect(pane().queryByRole("textbox", { name: "Retained schedule ID" })).toBeNull();
  fireEvent.click(toggle);
  const input = pane().getByRole("textbox", { name: "Retained schedule ID" }) as HTMLInputElement;
  expect(document.activeElement).toBe(input);
  expect(input.maxLength).toBe(36);
  const id = newRequestId();
  fireEvent.change(input, { target: { value: id } });
  fireEvent.click(toggle);
  expect(region.hidden).toBe(true);
  expect(region.querySelector("input")).toBe(input);
  fireEvent.click(toggle);
  expect(input.value).toBe(id);
  const refresh = pane().getByRole("button", { name: "Refresh" });
  refresh.focus();
  fireEvent.click(refresh);
  rendered.rerender(value.view(false));
  rendered.rerender(value.view());
  expect(document.activeElement).toBe(refresh);
  expect(disclosure().getAttribute("aria-controls")).toBe(regionId);
  expect(value.history).not.toHaveBeenCalled();
  expect(value.closeDrawer).not.toHaveBeenCalled();
  fireEvent.change(input, { target: { value: ` ${id} ` } });
  fireEvent.submit(input.closest("form")!);
  await waitFor(() => expect(value.history.mock.lastCall?.[0]).toMatchObject({ scheduleId: id, pageSize: 50, pageToken: "" }));
  expect(screen.getByRole("heading", { name: "Occurrence history" })).toBeTruthy();
  expect(value.closeDrawer).toHaveBeenCalledTimes(1);
});

it("distinguishes the history disclosure and submit action by accessible name", async () => {
  const value = fixture();
  render(value.view());
  await screen.findByRole("button", { name: /^Morning review/ });
  const toggle = disclosure();
  expect(pane().queryByRole("button", { name: "Open retained history" })).toBeNull();
  fireEvent.click(toggle);
  expect(pane().getAllByRole("button", { name: "Retained history" })).toHaveLength(1);
  const submit = pane().getByRole("button", { name: "Open retained history" });
  expect(submit.textContent).toBe("Retained history");
  expect(value.history).not.toHaveBeenCalled();
  const id = newRequestId();
  fireEvent.change(pane().getByRole("textbox", { name: "Retained schedule ID" }), { target: { value: id } });
  fireEvent.click(submit);
  await waitFor(() => expect(value.history.mock.lastCall?.[0]).toMatchObject({ scheduleId: id, pageSize: 50, pageToken: "" }));
  expect(value.closeDrawer).toHaveBeenCalledTimes(1);
});

it("does not refocus history when the retained portal is moved or navigation returns", async () => {
  const value = fixture(), target = document.createElement("div");
  document.body.append(target);
  const rendered = render(value.view());
  fireEvent.click(disclosure());
  const id = newRequestId();
  fireEvent.change(pane().getByLabelText("Retained schedule ID"), { target: { value: id } });
  const outsider = document.createElement("button");
  document.body.append(outsider);
  outsider.focus();
  rendered.rerender(value.view(true, target));
  expect(document.activeElement).toBe(outsider);
  expect((pane().getByLabelText("Retained schedule ID") as HTMLInputElement).value).toBe(id);
  expect(disclosure().getAttribute("aria-expanded")).toBe("true");
  rendered.rerender(value.view(false, target));
  rendered.rerender(value.view(true, target));
  expect(document.activeElement).toBe(outsider);
  expect(value.history).not.toHaveBeenCalled();
  rendered.unmount(); target.remove(); outsider.remove();
});

it.each(["editor", "confirmation", "in-flight", "uncertain"] as const)("protects history and every schedule replacement during %s across navigation", async (state) => {
  const value = fixture(), gate = deferred(), rendered = render(value.view());
  await screen.findByRole("button", { name: /^Morning review/ });
  fireEvent.click(disclosure());
  const id = newRequestId();
  fireEvent.change(pane().getByLabelText("Retained schedule ID"), { target: { value: id } });
  if (state === "editor") fireEvent.click(pane().getByRole("button", { name: "New schedule" }));
  else {
    selectMorning();
    fireEvent.click(screen.getByRole("button", { name: "Run now" }));
    if (state === "in-flight") value.run.mockImplementationOnce(async () => { await gate.promise; return { occurrence: create(ResourceSchema, { id: newRequestId(), kind: EntityKind.OCCURRENCE, revision: 1n, schemaVersion: 1 }) }; });
    if (state === "uncertain") value.run.mockRejectedValueOnce(new ConnectError("Lost acknowledgment", Code.Unavailable));
    if (state !== "confirmation") fireEvent.click(screen.getByRole("button", { name: "Confirm Run now" }));
    if (state === "uncertain") await screen.findByRole("button", { name: "Retry the same Run now" });
  }
  rendered.rerender(value.view(false));
  rendered.rerender(value.view());
  for (const button of pane().getAllByRole("button")) {
    expect((button as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(button);
  }
  const input = pane().getByLabelText("Retained schedule ID") as HTMLInputElement;
  expect(input.disabled).toBe(true);
  expect((pane().getByLabelText("Filter by project") as HTMLSelectElement).disabled).toBe(true);
  fireEvent.submit(input.closest("form")!);
  expect(value.history.mock.calls.some(([request]) => request.scheduleId === id)).toBe(false);
  expect(screen.getByRole("heading", { name: state === "editor" ? "New schedule" : "Morning review" })).toBeTruthy();
  expect(input.value).toBe(id);
  if (state === "uncertain") {
    fireEvent.click(screen.getByRole("button", { name: "Retry the same Run now" }));
    await waitFor(() => expect(value.run).toHaveBeenCalledTimes(2));
    expect(value.run.mock.calls[0][0]).toEqual(value.run.mock.calls[1][0]);
  }
  if (state === "in-flight") await act(async () => gate.resolve());
});

it("limits the All projects empty option to Schedules and preserves other selector defaults", async () => {
  const value = fixture();
  render(<TransportProvider transport={value.initialTransport}><QueryClientProvider client={value.client}><MutationIntents><Schedules active open={() => {}} /><ResourceChoice label="Project" kind={EntityKind.PROJECT} value="" active change={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  await screen.findByRole("button", { name: /^Morning review/ });
  const filtered = pane().getByRole("combobox", { name: "Filter by project" });
  fireEvent.click(filtered);
  expect(await screen.findByRole("option", { name: "All projects" })).toBeTruthy();
  fireEvent.keyDown(filtered, { key: "Escape" });
  fireEvent.click(screen.getByRole("combobox", { name: "Project", exact: true }));
  expect(await screen.findByRole("option", { name: "Select project" })).toBeTruthy();
});

it("retains Schedules connection memory on same-identity reconnect and resets on identity replacement", async () => {
  const value = fixture(), deviceId = newRequestId();
  const authority = { endpoint: "http://127.0.0.1:46312", serverId: newRequestId() };
  const rendered = render(<App transport={value.initialTransport} pairingAuthority={authority} currentDeviceId={deviceId} />);
  fireEvent.click(screen.getByRole("button", { name: "Schedules" }));
  await screen.findByRole("button", { name: /^Morning review/ });
  fireEvent.click(pane().getByRole("button", { name: "Enabled" }));
  await waitFor(() => expect(value.list.mock.lastCall?.[0].enabled).toBe(true));
  selectMorning();
  fireEvent.click(disclosure());
  const id = newRequestId();
  fireEvent.change(pane().getByLabelText("Retained schedule ID"), { target: { value: id } });
  fireEvent.click(screen.getByRole("button", { name: "Activity" }));
  fireEvent.click(screen.getByRole("button", { name: "Schedules" }));
  rendered.rerender(<App transport={value.transport()} pairingAuthority={{ ...authority }} currentDeviceId={deviceId} connectionEpoch={1} />);
  expect(disclosure().getAttribute("aria-expanded")).toBe("true");
  expect((pane().getByLabelText("Retained schedule ID") as HTMLInputElement).value).toBe(id);
  expect(pane().getByRole("button", { name: "Enabled" }).getAttribute("aria-pressed")).toBe("true");
  expect((await pane().findByRole("button", { name: /^Morning review/ })).getAttribute("aria-current")).toBe("true");
  rendered.rerender(<App transport={value.transport()} pairingAuthority={{ ...authority, serverId: newRequestId() }} currentDeviceId={deviceId} />);
  fireEvent.click(screen.getByRole("button", { name: "Schedules" }));
  expect(disclosure().getAttribute("aria-expanded")).toBe("false");
  expect(pane().getByRole("button", { name: "All schedules" }).getAttribute("aria-pressed")).toBe("true");
  expect(document.querySelector<HTMLInputElement>(".schedules-sidebar input")?.value).toBe("");
  await screen.findByRole("button", { name: /^Morning review/ });
  expect(pane().getByRole("button", { name: /^Morning review/ }).getAttribute("aria-current")).toBeNull();
});
