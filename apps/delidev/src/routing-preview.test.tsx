import { chooseScrollOption, waitScrollChoices } from "./test-scroll-picker";
// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, ResourceSchema, ResourceService, configurationSchemaVersion, newRequestId, type GetResourceRequest, type PreviewRoutingRequest, type Resource } from "@delinoio/delidev-api-client";
import { RoutingPreview } from "./routing-preview";
import { SettingsDialogFocus, SettingsDialogSize, SettingsTaskDialog } from "./settings-task";
import { encode, type Document } from "./documents";
import { i18n, SupportedLanguage } from "./localization";

function resource(kind: EntityKind, data: Document) { return create(ResourceSchema, { id: newRequestId(), kind, schemaVersion: configurationSchemaVersion(kind, data), revision: 1n, documentJson: encode(data) }); }
function account(alias = "ChatGPT Personal") { return resource(EntityKind.ACCOUNT, { alias, type: "subscription", subscription_service: "chatgpt" }); }
function candidate(row: Resource, eligibility = "unauthenticated") { return { id: row.id, weight: 1, quota_state: "unknown", eligibility }; }
function setup(rows: Resource[], route: Document) {
  const logs = vi.spyOn(console, "warn").mockImplementation(() => {});
  const agent = resource(EntityKind.AGENT, { name: "Luna MAX" });
  const preview = vi.fn(async (_request: PreviewRoutingRequest): Promise<{ routeJson: Uint8Array }> => ({ routeJson: encode(route) }));
  const get = vi.fn(async (request: GetResourceRequest): Promise<{ resource?: Resource }> => ({ resource: rows.find(row => row.id === request.id) }));
  const save = vi.fn(() => ({}));
  const transport = createRouterTransport(router => {
    router.service(ConfigurationService, { previewRouting: preview, saveConfiguration: save });
    router.service(ResourceService, { getResource: get, listResources: request => ({ resources: rows.filter(row => row.kind === request.filter?.kind) }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Fixture() {
    const [open, setOpen] = useState(true);
    return <QueryClientProvider client={client}><TransportProvider transport={transport}>{open ? <SettingsTaskDialog size={SettingsDialogSize.Form} title="Preview routing" subtitle="Luna MAX · Saved on the selected server." focus={SettingsDialogFocus.Heading} close={() => setOpen(false)}><RoutingPreview agent={agent} active close={() => setOpen(false)} /></SettingsTaskDialog> : <p>Closed preview</p>}</TransportProvider></QueryClientProvider>;
  }
  return { Fixture, preview, get, save, client, logs };
}

it("uses a saved alias and service, keeping the full identity in a closed disclosure", async () => {
  const row = account(), value = setup([row], { policy: "priority", candidates: [candidate(row)] });
  render(<value.Fixture />);
  const dialog = screen.getByRole("dialog");
  await within(dialog).findByText("ChatGPT Personal");
  expect(within(dialog).getByText("ChatGPT")).toBeTruthy();
  expect(within(dialog).getByText("Unauthenticated")).toBeTruthy();
  expect(within(dialog).getByText("No eligible account")).toBeTruthy();
  expect(dialog.querySelector(".routing-account-id")?.hasAttribute("open")).toBe(false);
  expect(within(dialog).getByText(row.id)).toBeTruthy();
  expect(within(dialog).getByText("Weight")).toBeTruthy();
  expect(within(dialog).getByText("unknown")).toBeTruthy();
  expect(value.get).toHaveBeenCalledTimes(1);
  expect(value.save).not.toHaveBeenCalled();
  expect(within(dialog).queryByRole("button", { name: "Back to Agent Workers" })).toBeNull();
  fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("resolves selected API accounts through validated provider metadata without inspecting endpoints or credentials", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "OpenRouter", endpoint: "http://private.invalid/secret", authentication: "bearer" });
  const row = resource(EntityKind.ACCOUNT, { alias: "Work API", type: "api", provider_id: provider.id, credential: "not-for-display" });
  const value = setup([row, provider], { policy: "priority", selected: row.id, candidates: [candidate(row, "eligible")] });
  render(<value.Fixture />);
  await waitFor(() => expect(screen.getAllByText("OpenRouter")).toHaveLength(2));
  expect(screen.getAllByText("Work API")).toHaveLength(2);
  expect(screen.queryByText("No eligible account")).toBeNull();
  expect(document.body.textContent).not.toContain("private.invalid");
  expect(document.body.textContent).not.toContain("not-for-display");
  expect(value.get.mock.calls.map(([request]) => request.kind)).toEqual([EntityKind.ACCOUNT, EntityKind.PROVIDER]);
});

it("deduplicates source and selected identities while preserving ordered evidence and duplicate aliases", async () => {
  const first = account("Work account"), second = account("Work account"), model = newRequestId();
  const inner = { policy: "remaining-quota", selected: second.id, fallback: true, candidates: [candidate(first), { ...candidate(second, "eligible"), score: 0.75, reset_at: "2026-10-07T00:00:00Z" }] };
  const value = setup([first, second], { ...inner, source_index: 1, sources: [{ source: "subscription:chatgpt", model_id: model, native_model: "model-one", route: { policy: "priority", candidates: [candidate(first, "exhausted")], problem: { code: "missing-input" } } }, { source: "subscription:chatgpt", model_id: model, native_model: "model-two", route: inner }] });
  render(<value.Fixture />);
  await waitFor(() => expect(screen.getAllByText("Work account")).toHaveLength(6));
  expect(value.get).toHaveBeenCalledTimes(2);
  const sources = document.querySelectorAll(".routing-source");
  expect(sources[0].textContent).toContain("model-one");
  expect(sources[1].textContent).toContain("model-two");
  expect(sources[1].textContent).toContain("Selected source");
  expect(screen.getAllByText("0.75")).toHaveLength(2);
  expect(screen.getAllByText(/Insufficient comparable quota evidence/)).toHaveLength(2);
});

it.each(["missing", "denied", "wrong-id", "wrong-kind", "unsupported", "retired", "empty-alias"])("keeps routing evidence when account metadata is %s", async failure => {
  const row = account(), value = setup([row], { policy: "priority", candidates: [candidate(row)] });
  value.get.mockImplementation(async () => {
    if (failure === "denied") throw new ConnectError("Denied", Code.PermissionDenied);
    if (failure === "missing") return {};
    return { resource: create(ResourceSchema, { ...row, ...(failure === "wrong-id" ? { id: newRequestId() } : {}), ...(failure === "wrong-kind" ? { kind: EntityKind.PROJECT } : {}), ...(failure === "unsupported" ? { schemaVersion: 999 } : {}), ...(failure === "retired" ? { schemaVersion: 1, documentJson: encode({ alias: "Retired alias", type: "subscription", subscription_service: "chatgpt", retired: true }) } : {}), ...(failure === "empty-alias" ? { documentJson: encode({ type: "subscription", subscription_service: "chatgpt", alias: "" }) } : {}) }) };
  });
  render(<value.Fixture />);
  await screen.findByText("Account information unavailable");
  expect(screen.getByText("Unauthenticated")).toBeTruthy();
  expect(screen.getByText("unknown")).toBeTruthy();
  expect(screen.queryByText("ChatGPT Personal")).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
  expect(JSON.stringify(value.logs.mock.calls)).not.toContain(row.id);
  expect(JSON.stringify(value.logs.mock.calls)).not.toContain("ChatGPT Personal");
});

it("keeps an API alias when provider metadata is invalid and reads a shared provider only once", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "Provider" });
  const rows = ["Work", "Personal"].map(alias => resource(EntityKind.ACCOUNT, { alias, type: "api", provider_id: provider.id }));
  const value = setup(rows, { policy: "priority", candidates: rows.map(row => candidate(row)) });
  render(<value.Fixture />);
  await screen.findByText("Personal");
  expect(screen.getByText("Work")).toBeTruthy();
  await waitFor(() => expect(screen.getAllByText("Service information unavailable")).toHaveLength(2));
  expect(value.get.mock.calls.filter(([request]) => request.kind === EntityKind.PROVIDER)).toHaveLength(1);
});

it("bounds concurrent metadata reads to four and ignores a disposed late account before provider lookup", async () => {
  const rows = Array.from({ length: 7 }, (_, index) => account(`Account ${index}`));
  const value = setup(rows, { policy: "priority", candidates: rows.map(row => candidate(row)) });
  const releases: (() => void)[] = [];
  let inFlight = 0, peak = 0;
  value.get.mockImplementation(async request => {
    peak = Math.max(peak, ++inFlight);
    await new Promise<void>(resolve => releases.push(() => { inFlight--; resolve(); }));
    return { resource: rows.find(row => row.id === request.id) };
  });
  render(<value.Fixture />);
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(4));
  await act(async () => { releases.splice(0).forEach(release => release()); });
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(7));
  expect(peak).toBe(4);
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  await act(async () => { releases.splice(0).forEach(release => release()); });
  expect(screen.getByText("Closed preview")).toBeTruthy();
  expect(value.get).toHaveBeenCalledTimes(7);
});

it("fences a late provider continuation on top-level close", async () => {
  const row = resource(EntityKind.ACCOUNT, { alias: "Work", type: "api", provider_id: newRequestId() });
  const value = setup([row], { policy: "priority", candidates: [candidate(row)] });
  let release!: () => void;
  value.get.mockImplementation(async () => { await new Promise<void>(resolve => { release = resolve; }); return { resource: row }; });
  render(<value.Fixture />);
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Close Preview routing" }));
  await act(async () => release());
  expect(value.get).toHaveBeenCalledTimes(1);
  expect(screen.queryByText("Work")).toBeNull();
});

it("retains a same-project result on refresh failure but removes it when a different project fails", async () => {
  const row = account(), project = resource(EntityKind.PROJECT, { name: "Restricted project" });
  const value = setup([row, project], { policy: "priority", candidates: [candidate(row)] });
  render(<value.Fixture />);
  await screen.findByText("ChatGPT Personal");
  value.preview.mockRejectedValue(new ConnectError("Offline", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Refresh routing preview" }));
  await screen.findByText("Refresh failed. Showing the previous result for this project.");
  expect(screen.getByText("ChatGPT Personal")).toBeTruthy();
  await chooseScrollOption(screen.getByLabelText("Project"), project.id);
  await waitFor(() => expect(value.preview).toHaveBeenLastCalledWith(expect.objectContaining({ projectId: project.id }), expect.anything()));
  await screen.findByRole("alert");
  expect(screen.queryByText("ChatGPT Personal")).toBeNull();
  expect(screen.queryByText("Refresh failed. Showing the previous result for this project.")).toBeNull();
});

it("distinguishes loading, empty, malformed and denied routing responses", async () => {
  const value = setup([], { policy: "priority", candidates: [] });
  let release!: () => void;
  value.preview.mockImplementationOnce(async () => { await new Promise<void>(resolve => { release = resolve; }); return { routeJson: encode({ policy: "priority", candidates: [] }) }; });
  render(<value.Fixture />);
  await screen.findByText("Loading routing preview…");
  await act(async () => release());
  await screen.findByText("No account candidates in this result.");
  value.preview.mockResolvedValueOnce({ routeJson: encode({}) });
  fireEvent.click(screen.getByRole("button", { name: "Refresh routing preview" }));
  await screen.findByText("Routing evidence is unavailable.");
  expect(screen.queryByText("No eligible account")).toBeNull();
  value.preview.mockRejectedValueOnce(new ConnectError("Denied", Code.PermissionDenied));
  fireEvent.click(screen.getByRole("button", { name: "Refresh routing preview" }));
  await screen.findByText("This connection is not authorized for the action. Check its access.");
});

it("localizes names and evidence labels without another read on language change", async () => {
  const row = account("업무용 계정"), value = setup([row], { policy: "priority", candidates: [candidate(row)] });
  render(<value.Fixture />);
  await screen.findByText("업무용 계정");
  await act(async () => { await i18n.changeLanguage(SupportedLanguage.Korean); });
  expect(screen.getByText("후보 계정")).toBeTruthy();
  expect(screen.getByText("인증되지 않음")).toBeTruthy();
  expect(screen.getByText("계정 ID")).toBeTruthy();
  expect(value.get).toHaveBeenCalledTimes(1);
  expect(value.preview).toHaveBeenCalledTimes(1);
});

it("shows an API alias while its provider name is still loading", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "Saved provider" }), row = resource(EntityKind.ACCOUNT, { alias: "Saved API alias", type: "api", provider_id: provider.id });
  const value = setup([row, provider], { policy: "priority", candidates: [candidate(row)] });
  let release!: () => void;
  value.get.mockImplementation(async request => {
    if (request.kind === EntityKind.PROVIDER) await new Promise<void>(resolve => { release = resolve; });
    return { resource: request.kind === EntityKind.PROVIDER ? provider : row };
  });
  render(<value.Fixture />);
  await screen.findByText("Saved API alias");
  expect(screen.getByText("Loading service information…")).toBeTruthy();
  await act(async () => release());
  await screen.findByText("Saved provider");
});

it("keeps nullable quota evidence unknown and displays an observed zero score", async () => {
  const first = account("Unknown quota"), second = account("Observed zero");
  const value = setup([first, second], { policy: "remaining-quota", candidates: [{ ...candidate(first), score: null, reset_at: null }, { ...candidate(second), score: 0, quota_state: "observed" }] });
  render(<value.Fixture />);
  await screen.findByText("Unknown quota");
  const cards = document.querySelectorAll(".routing-candidate");
  expect(within(cards[0] as HTMLElement).getByText("unknown")).toBeTruthy();
  expect(within(cards[0] as HTMLElement).queryByText("Score")).toBeNull();
  expect(within(cards[1] as HTMLElement).getByText("0")).toBeTruthy();
});
