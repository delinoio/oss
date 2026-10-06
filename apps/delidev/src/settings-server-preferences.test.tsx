// SPDX-License-Identifier: Apache-2.0
import { StrictMode, type ReactNode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { configure, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, ProviderInventoryCapability, ProviderService, ResourceSchema, ResourceService, newRequestId, type ListResourcesRequest, type Resource } from "@delinoio/delidev-api-client";
import { ConfigurationEditor, Settings } from "./settings";
import { newConfiguration } from "./configuration-fields";
import { encode, type Document } from "./documents";
import { MutationIntents } from "./mutation";

// Settings renders all mounted categories under Strict Mode. Keep this fixture
// bounded while allowing concurrent integration/build load on development hosts.
vi.setConfig({ testTimeout: 15000 });
configure({ asyncUtilTimeout: 5000 });

type Page = { resources: Resource[]; nextPageToken?: string };
function resource(kind: EntityKind, data: Document, revision = 8n) {
  return create(ResourceSchema, { kind, id: newRequestId(), schemaVersion: 1, revision, documentJson: encode(data) });
}
function fixture(rows: Resource[] = [], read?: (token: string) => Page | Promise<Page>) {
  const list = vi.fn((request: ListResourcesRequest) => request.filter?.kind === EntityKind.SETTINGS && read
    ? read(request.filter.pageToken) : { resources: rows.filter(row => row.kind === request.filter?.kind) });
  const save = vi.fn(async (_request: unknown) => ({ resource: resource(EntityKind.SETTINGS, newConfiguration(EntityKind.SETTINGS)) }));
  const get = vi.fn((request: { id: string }) => ({ resource: rows.find(row => row.id === request.id) }));
  const makeTransport = () => createRouterTransport(router => {
    router.service(ResourceService, { listResources: list, getResource: get });
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(ProviderService, { listProviderInventory: () => ({ entries: [], capabilities: [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER] }) });
  });
  const transport = makeTransport();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (children: ReactNode, current: Transport = transport) => <StrictMode><TransportProvider transport={current}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider></StrictMode>;
  return { list, save, get, client, transport, makeTransport, view };
}
function choosePreferences() { fireEvent.click(screen.getByRole("button", { name: "Server preferences" })); }
function details() { return screen.getByText("Remediation details").closest("details")!; }

it("shows the exact final-empty content only after a successful first read", async () => {
  let resolve!: (value: Page) => void;
  const pending = new Promise<Page>(done => { resolve = done; });
  const value = fixture([], () => pending);
  render(value.view(<Settings />)); choosePreferences();
  expect(screen.getByRole("status").textContent).toBe("Loading server preferences…");
  expect((screen.getByRole("button", { name: "New Server preferences" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.queryByRole("region", { name: "No saved server preferences" })).toBeNull();
  resolve({ resources: [] });
  const empty = await screen.findByRole("region", { name: "No saved server preferences" });
  for (const copy of ["Review the defaults, then save one preference set for this server.", "Choose the default policy for Agent Workers that inherit server routing.", "Allow fetching before Worktree preparation. Repository preferences also apply.", "Configure bounded automatic fixes for linked pull requests. All automatic policies default off.", "Choose New Server preferences to review and save."]) expect(within(empty).getByText(copy)).toBeTruthy();
  expect(screen.getByText("Default routing, Worktree fetch, and pull request remediation.")).toBeTruthy();
  expect(screen.getAllByRole("button", { name: "New Server preferences" })).toHaveLength(1);
  expect((screen.getByRole("button", { name: "New Server preferences" }) as HTMLButtonElement).disabled).toBe(false);
  expect(within(empty).queryByRole("button")).toBeNull();
  expect(screen.queryByRole("navigation", { name: "Settings pages" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  expect(document.querySelector(".settings-server-preferences")).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
});

it.each([Code.PermissionDenied, Code.Unavailable])("does not create or claim empty on an initial %s failure", async code => {
  const value = fixture([], () => { throw new ConnectError("Fixture read failure", code); });
  render(value.view(<Settings />)); choosePreferences();
  await screen.findByRole("alert");
  expect(screen.queryByRole("region", { name: "No saved server preferences" })).toBeNull();
  expect((screen.getByRole("button", { name: "New Server preferences" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.queryByText("No server preferences on this page.")).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
});

it("does not authorize creation on empty continuation or later pages", async () => {
  let failedRefresh = false;
  const value = fixture([], token => { if (failedRefresh) throw new ConnectError("Refresh unavailable", Code.Unavailable); return { resources: [], nextPageToken: token ? "" : "opaque-page-2" }; });
  render(value.view(<Settings />)); choosePreferences();
  await screen.findByText("No server preferences on this page.");
  expect((screen.getByRole("button", { name: "New Server preferences" }) as HTMLButtonElement).disabled).toBe(true);
  await waitFor(() => expect((screen.getByRole("button", { name: "Next page" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "First page" }) as HTMLButtonElement).disabled).toBe(false));
  expect((screen.getByRole("button", { name: "New Server preferences" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.queryByRole("region", { name: "No saved server preferences" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await waitFor(() => expect(value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.SETTINGS).map(([request]) => request.filter?.pageToken)).toEqual(["", "", "opaque-page-2", "opaque-page-2"]));
  await waitFor(() => expect(value.client.isFetching()).toBe(0));
  failedRefresh = true; fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await screen.findByRole("alert");
  expect(screen.getByText("No server preferences on this page.")).toBeTruthy();
  expect(screen.getByText("Refresh failed. Showing the last successfully loaded results.")).toBeTruthy();
  expect((screen.getByRole("button", { name: "New Server preferences" }) as HTMLButtonElement).disabled).toBe(true);
  expect(value.save).not.toHaveBeenCalled();
});

const known = { default_routing: "priority", automatic_fetch: false, notifications: false, remediation: { ci_failure: true, review_feedback: false, merge_conflict: true, conflict_strategy: "rebase", session_strategy: "dedicated", attempt_limit: 9, agent_id: newRequestId(), machine_id: newRequestId() } };
it.each([true, false])("retains the last successful result during and after failed refresh, empty: %s", async empty => {
  const row = resource(EntityKind.SETTINGS, known);
  let fail = false;
  let reject!: (error: ConnectError) => void;
  const pending = new Promise<Page>((_resolve, rejection) => { reject = rejection; });
  const value = fixture([row], () => fail ? pending : { resources: empty ? [] : [row] });
  render(value.view(<Settings />)); choosePreferences();
  const panel = await screen.findByRole(empty ? "region" : "article", { name: empty ? "No saved server preferences" : "Saved server preferences" });
  fail = true; fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await screen.findByText("Refreshing server preferences…");
  if (empty) expect((screen.getByRole("button", { name: "New Server preferences" }) as HTMLButtonElement).disabled).toBe(true);
  reject(new ConnectError("Refresh unavailable", Code.Unavailable));
  await screen.findByText("Refresh failed. Showing the last successfully loaded results.");
  expect(screen.getByRole(empty ? "region" : "article", { name: empty ? "No saved server preferences" : "Saved server preferences" })).toBe(panel);
  expect(screen.getByRole("alert")).toBeTruthy();
  if (empty) expect((screen.getByRole("button", { name: "New Server preferences" }) as HTMLButtonElement).disabled).toBe(true);
  fail = false; fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  expect(value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.SETTINGS).map(([request]) => request.filter?.pageToken)).toEqual(["", "", "", ""]);
  expect(value.save).not.toHaveBeenCalled();
});

it("summarizes exact known stored values and keeps one title-aligned Edit action", async () => {
  const row = resource(EntityKind.SETTINGS, known);
  const value = fixture([row]);
  render(value.view(<Settings />)); choosePreferences();
  const summary = within(await screen.findByRole("article", { name: "Saved server preferences" }));
  expect(summary.getByText(row.id)).toBeTruthy(); expect(summary.getByText("priority")).toBeTruthy(); expect(summary.getByText("Disabled")).toBeTruthy();
  expect(summary.getAllByText("On")).toHaveLength(2); expect(summary.getAllByText("Off")).toHaveLength(1);
  expect(summary.getByText("Enabled policies run bounded fixes for linked pull requests when the Agent, Runner Device, and current evidence are eligible.")).toBeTruthy();
  const edit = screen.getByRole("button", { name: "Edit Server preferences" });
  expect(edit.closest(".settings-toolbar")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "New Server preferences" })).toBeNull();
  expect(screen.queryByRole("button", { name: /Delete Server/ })).toBeNull();
});

it.each(["future", "invalid-json", "missing-policy", "unknown-routing", "invalid-fetch"])("shows unavailable rather than defaults for %s", async variant => {
  const row = resource(EntityKind.SETTINGS, variant === "missing-policy" ? {} : variant === "unknown-routing" ? { ...known, default_routing: "future-routing" } : variant === "invalid-fetch" ? { ...known, automatic_fetch: null } : known);
  if (variant === "future") row.schemaVersion = 2;
  if (variant === "invalid-json") row.documentJson = new Uint8Array([255]);
  const value = fixture([row]); render(value.view(<Settings />)); choosePreferences();
  const summary = within(await screen.findByRole("article", { name: "Saved server preferences" }));
  expect(summary.getByText(row.id)).toBeTruthy(); expect(summary.getByRole("status").textContent).toContain("Policy values are unavailable.");
  for (const invented of ["Allowed", "Disabled", "On", "Off", "priority", "sequential-exhaustion"]) expect(summary.queryByText(invented)).toBeNull();
  expect((screen.getByRole("button", { name: "Edit Server preferences" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.queryByRole("button", { name: "New Server preferences" })).toBeNull();
});

it("preserves mounted disclosure values and resource cursors through collapse and reconnect", async () => {
  const agent = resource(EntityKind.AGENT, { name: "Fix agent" });
  const machine = resource(EntityKind.MACHINE, { name: "Fix machine" });
  const secondAgent = resource(EntityKind.AGENT, { name: "Second page agent" });
  const value = fixture([agent, secondAgent, machine]);
  value.list.mockImplementation(request => request.filter?.kind === EntityKind.AGENT
    ? { resources: request.filter.pageToken ? [secondAgent] : [agent], nextPageToken: request.filter.pageToken ? "" : "agent-page-2" }
    : { resources: request.filter?.kind === EntityKind.MACHINE ? [machine] : [] });
  const element = <ConfigurationEditor kind={EntityKind.SETTINGS} active saved={() => {}} cancel={() => {}} />;
  const view = render(value.view(element));
  expect(details().open).toBe(false);
  expect(screen.getAllByRole("checkbox")).toHaveLength(4);
  fireEvent.click(screen.getByText("Remediation details")); details().open = true;
  await screen.findByRole("option", { name: "Fix agent" });
  fireEvent.click(screen.getByRole("button", { name: "More choices" }));
  await screen.findByRole("option", { name: "Second page agent" });
  const limit = screen.getByLabelText("Consecutive automatic attempt limit");
  fireEvent.change(limit, { target: { value: "9" } });
  fireEvent.change(screen.getByLabelText("Remediation Agent Worker"), { target: { value: secondAgent.id } });
  fireEvent.change(screen.getByLabelText("Remediation Runner Device"), { target: { value: machine.id } });
  fireEvent.change(screen.getByLabelText("Remediation session strategy"), { target: { value: "dedicated" } });
  fireEvent.click(screen.getByRole("button", { name: "Add reviewer selector" }));
  fireEvent.change(screen.getByLabelText("Selector 1 GitHub numeric ID"), { target: { value: "9007199254740993" } });
  fireEvent.change(screen.getByLabelText("Selector 1 GitHub node ID"), { target: { value: "BOT_exact" } });
  const reads = value.list.mock.calls.length;
  details().open = false; view.rerender(value.view(element, value.makeTransport()));
  expect(details().open).toBe(false); expect(screen.getByLabelText("Consecutive automatic attempt limit")).toBe(limit);
  details().open = true;
  expect((limit as HTMLInputElement).value).toBe("9");
  expect((screen.getByLabelText("Remediation Agent Worker") as HTMLSelectElement).value).toBe(secondAgent.id);
  expect((screen.getByLabelText("Remediation Runner Device") as HTMLSelectElement).value).toBe(machine.id);
  expect((screen.getByLabelText("Selector 1 GitHub numeric ID") as HTMLInputElement).value).toBe("9007199254740993");
  // Transport replacement may legitimately refetch. Toggling alone must not.
  await waitFor(() => expect(value.list.mock.calls.length).toBeGreaterThanOrEqual(reads));
  expect(value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.AGENT).at(-1)?.[0].filter?.pageToken).toBe("agent-page-2");
  const afterReconnect = value.list.mock.calls.length; details().open = false; details().open = true;
  expect(value.list.mock.calls.length).toBe(afterReconnect); expect(value.save).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Save Server preferences" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  expect(JSON.parse(new TextDecoder().decode((value.save.mock.calls[0][0] as { documentJson: Uint8Array }).documentJson)).remediation.reviewer_selectors[0].id).toBe("9007199254740993");
});

it.each(["", "0", "reviewer"])("reveals and focuses a hidden invalid %s input before allowing save", async invalid => {
  const value = fixture(); render(value.view(<ConfigurationEditor kind={EntityKind.SETTINGS} active saved={() => {}} cancel={() => {}} />));
  details().open = true;
  let control: HTMLElement;
  if (invalid === "reviewer") { fireEvent.click(screen.getByRole("button", { name: "Add reviewer selector" })); control = screen.getByLabelText("Selector 1 GitHub numeric ID"); }
  else { control = screen.getByLabelText("Consecutive automatic attempt limit"); fireEvent.change(control, { target: { value: invalid } }); }
  details().open = false;
  fireEvent.click(screen.getByRole("button", { name: "Save Server preferences" }));
  expect(details().open).toBe(true);
  await waitFor(() => expect(document.activeElement).toBe(control));
  expect(value.save).not.toHaveBeenCalled();
});

it("retains a draft after revision drift and blocks a fresh save", async () => {
  const row = resource(EntityKind.SETTINGS, known);
  const value = fixture([row]); render(value.view(<ConfigurationEditor kind={EntityKind.SETTINGS} initial={row} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(screen.getByLabelText("Default account routing"), { target: { value: "fixed" } });
  value.get.mockReturnValue({ resource: { ...row, revision: 9n } });
  await value.client.invalidateQueries();
  await screen.findByText(/This entry changed elsewhere/);
  expect((screen.getByLabelText("Default account routing") as HTMLSelectElement).value).toBe("fixed");
  expect((screen.getByRole("button", { name: "Save Server preferences" }) as HTMLButtonElement).disabled).toBe(true);
  expect(value.save).not.toHaveBeenCalled();
});

it("disposes a changed disclosure and ignores a late prior save in a replacement visit", async () => {
  const value = fixture();
  let resolve!: (response: { resource: Resource }) => void;
  value.save.mockImplementation(() => new Promise(done => { resolve = done; }));
  const view = render(value.view(<Settings visible />)); choosePreferences();
  await waitFor(() => expect((screen.getByRole("button", { name: "New Server preferences" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "New Server preferences" }));
  details().open = true;
  fireEvent.change(screen.getByLabelText("Consecutive automatic attempt limit"), { target: { value: "11" } });
  fireEvent.click(screen.getByRole("button", { name: "Save Server preferences" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  view.rerender(value.view(<Settings visible={false} />));
  view.rerender(value.view(<Settings visible />));
  expect(screen.getByRole("heading", { level: 1, name: "AI Subscription" })).toBeTruthy();
  choosePreferences(); await screen.findByRole("region", { name: "No saved server preferences" });
  resolve({ resource: resource(EntityKind.SETTINGS, known) });
  await waitFor(() => expect(value.client.isMutating()).toBe(0));
  expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  expect(screen.queryByRole("article", { name: "Saved server preferences" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "New Server preferences" }));
  expect(details().open).toBe(false);
  expect((screen.getByLabelText("Consecutive automatic attempt limit") as HTMLInputElement).value).toBe("3");
  expect(value.save).toHaveBeenCalledOnce();
});

it("ignores a late prior read after its Settings visit was replaced", async () => {
  let resolve!: (response: Page) => void, waiting = true;
  const pending = new Promise<Page>(done => { resolve = done; });
  const value = fixture([], () => waiting ? pending : { resources: [] });
  const view = render(value.view(<Settings visible />)); choosePreferences();
  await waitFor(() => expect(value.list.mock.calls.some(([request]) => request.filter?.kind === EntityKind.SETTINGS)).toBe(true));
  view.rerender(value.view(<Settings visible={false} />));
  waiting = false; view.rerender(value.view(<Settings visible />)); choosePreferences();
  await screen.findByRole("region", { name: "No saved server preferences" });
  resolve({ resources: [resource(EntityKind.SETTINGS, known)] });
  await waitFor(() => expect(value.client.isFetching()).toBe(0));
  expect(screen.queryByRole("article", { name: "Saved server preferences" })).toBeNull();
  expect(screen.getByRole("region", { name: "No saved server preferences" })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
});
