import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, ResourceSchema, ResourceService, EntityKind, SystemCapability, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { ConfigurationTransfer, formatConfigurationReview } from "./configuration-transfer";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";

function fixture() {
  const source = newRequestId(), target = newRequestId(), jobId = newRequestId();
  const bundle = { version: 2, entries: [{ id: source, kind: "template", document: { name: "Instructions", contents: "Exact text\n한국어 <script>never execute</script>\n" } }], machines: [] };
  const previewDocument = { plan: { version: 2, changes: [{ source_id: source, id: target, kind: "template", action: "create", expected_revision: 0, after: bundle.entries[0]!.document }], machines: [] }, token: "server-scoped-preview" };
  const previewBytes = encode(previewDocument);
  const exported = vi.fn(async () => ({ documentJson: encode(bundle) }));
  const preview = vi.fn(async (_input: unknown) => ({ previewJson: previewBytes }));
  const apply = vi.fn(async (_input: unknown) => ({ resultJson: encode({ job_id: jobId, state: "queued", resources: [] }) }));
  const status = vi.fn(async () => ({ capabilities: [SystemCapability.REMOTE_REPOSITORIES_V1] }));
  let state = "queued";
  const transport = createRouterTransport((router) => {
    router.service(ConfigurationService, { exportConfiguration: exported, previewConfigurationImport: preview, applyConfigurationImport: apply });
    router.service(SystemService, { getStatus: status });
    router.service(ResourceService, { getResource: () => ({ resource: create(ResourceSchema, { id: jobId, kind: EntityKind.JOB, schemaVersion: 1, revision: 1n, documentJson: encode({ type: "import-configuration", state }) }) }), listResources: () => ({ resources: [] }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ConfigurationTransfer active={active} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { bundle, previewBytes, exported, preview, apply, client, view, status, state: (next: string) => { state = next; } };
}
function load(bundle: unknown) {
  fireEvent.change(screen.getByRole("textbox", { name: "Configuration JSON" }), { target: { value: typeof bundle === "string" ? bundle : JSON.stringify(bundle) } });
  fireEvent.click(screen.getByRole("button", { name: "Load configuration document" }));
}
it("requires a preview, preserves exact review bytes and retries only the same uncertain import", async () => {
  const value = fixture(); value.apply.mockRejectedValueOnce(new ConnectError("acknowledgement lost", Code.Unavailable));
  render(value.view()); load(value.bundle);
  expect(screen.queryByRole("button", { name: "Apply reviewed configuration" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Preview configuration changes" }));
  fireEvent.click(await screen.findByRole("button", { name: "Apply reviewed configuration" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same configuration import" }));
  await waitFor(() => expect(value.apply).toHaveBeenCalledTimes(2));
  expect(value.apply.mock.calls[0]![0]).toEqual(value.apply.mock.calls[1]![0]);
  expect(Array.from((value.apply.mock.calls[0]![0] as { previewJson: Uint8Array }).previewJson)).toEqual(Array.from(value.previewBytes));
  await screen.findByText(/Import accepted. Waiting for confirmation of every repository validation/);
  expect((screen.getByRole("button", { name: "Apply reviewed configuration" }) as HTMLButtonElement).disabled).toBe(true);
  value.state("succeeded"); fireEvent.click(screen.getByRole("button", { name: "Refresh configuration import" }));
  await screen.findByText(/Configuration import completed/);
  expect(value.apply).toHaveBeenCalledTimes(2);
  expect(JSON.stringify(value.client.getQueryCache().getAll().map((query) => query.queryKey))).not.toContain("Exact text");
});
it("invalidates reviewed changes after editing while retaining the original document across hiding", async () => {
  const value = fixture(); const view = render(value.view()); load(value.bundle);
  fireEvent.click(screen.getByRole("button", { name: "Preview configuration changes" }));
  await screen.findByRole("button", { name: "Apply reviewed configuration" });
  view.rerender(value.view(false)); view.rerender(value.view(true));
  expect((screen.getByRole("textbox", { name: "Configuration JSON" }) as HTMLTextAreaElement).value).toContain("한국어");
  fireEvent.change(screen.getByRole("combobox", { name: "Action for Instructions" }), { target: { value: "reuse" } });
  expect(screen.queryByRole("button", { name: "Apply reviewed configuration" })).toBeNull();
  expect(value.apply).not.toHaveBeenCalled();
});
it("preserves integers outside JavaScript's safe range in exported and submitted documents", async () => {
  const value = fixture();
  const model = `{"version":2,"entries":[{"id":"${newRequestId()}","kind":"model","document":{"name":"Model","context_limit":18446744073709551615}}],"machines":[]}`;
  value.exported.mockResolvedValueOnce({ documentJson: new TextEncoder().encode(model) });
  render(value.view()); fireEvent.click(screen.getByRole("button", { name: "Export configuration" }));
  expect((await screen.findByRole("textbox", { name: "Exported configuration" }) as HTMLTextAreaElement).value).toBe(model);
  load(model); fireEvent.click(screen.getByRole("button", { name: "Preview configuration changes" }));
  await waitFor(() => expect(value.preview).toHaveBeenCalledTimes(1));
  expect(new TextDecoder().decode((value.preview.mock.calls[0]![0] as { selectionJson: Uint8Array }).selectionJson)).toContain("18446744073709551615");
});
it("keeps an acknowledged malformed result blocked instead of enabling a replacement import", async () => {
  const value = fixture(); value.apply.mockResolvedValueOnce({ resultJson: new TextEncoder().encode("invalid") });
  render(value.view()); load(value.bundle); fireEvent.click(screen.getByRole("button", { name: "Preview configuration changes" }));
  fireEvent.click(await screen.findByRole("button", { name: "Apply reviewed configuration" }));
  await screen.findByText(/The import outcome is unavailable/);
  expect((screen.getByRole("button", { name: "Apply reviewed configuration" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.queryByRole("button", { name: "Return to retained import document" })).toBeNull();
});
it("does not apply an old document after loading invalid replacement JSON", async () => {
  const value = fixture(); render(value.view()); load(value.bundle);
  fireEvent.click(screen.getByRole("button", { name: "Preview configuration changes" })); await screen.findByRole("button", { name: "Apply reviewed configuration" });
  load("{}"); expect(screen.queryByRole("button", { name: "Apply reviewed configuration" })).toBeNull(); expect(value.apply).not.toHaveBeenCalled();
});

it("formats complete review values without changing integer or escaped string tokens", () => {
  const raw = '{"context_limit":18446744073709551615,"text":"Keep \\"quoted\\" {braces}\\n한국어","items":[true,null,{}]}';
  const formatted = formatConfigurationReview(raw);
  expect(formatted).toContain("18446744073709551615");
  expect(JSON.parse(formatted)).toEqual(JSON.parse(raw));
});

it("rejects prototype-named kinds and malformed machine descriptors before showing mappings", () => {
  const value = fixture(); render(value.view());
  load({ ...value.bundle, entries: [{ ...value.bundle.entries[0], kind: "toString" }] });
  expect(screen.queryByRole("button", { name: "Preview configuration changes" })).toBeNull();
  load({ ...value.bundle, machines: [{ id: newRequestId(), name: "Worker", os: {}, architecture: "arm64" }] });
  expect(screen.queryByRole("button", { name: "Preview configuration changes" })).toBeNull();
});

it("invalidates a prior preview when a replacement file exceeds the import limit", async () => {
  const value = fixture(); render(value.view()); load(value.bundle);
  fireEvent.click(screen.getByRole("button", { name: "Preview configuration changes" }));
  await screen.findByRole("button", { name: "Apply reviewed configuration" });
  const file = new File(["x".repeat(384 * 1024 + 1)], "too-large.json", { type: "application/json" });
  fireEvent.change(screen.getByLabelText("Configuration file"), { target: { files: [file] } });
  expect(screen.queryByRole("button", { name: "Apply reviewed configuration" })).toBeNull();
  expect(value.apply).not.toHaveBeenCalled();
});

it("keeps both inputs mounted and exposes state-derived stages without navigation or implicit requests", async () => {
  const value = fixture(); const rendered = render(value.view());
  expect(screen.getByRole("heading", { level: 1, name: "Import / Export" })).toBeTruthy();
  expect(screen.getByText("Move configuration between DeliDev servers.")).toBeTruthy();
  expect(screen.getByText("Includes providers, models, account preferences, Agent Workers, instructions, repositories, projects and server preferences.")).toBeTruthy();
  expect(screen.getByText("Imported accounts are disconnected and need a new connection.")).toBeTruthy();
  expect(screen.getByText("Authentication, device registrations, observed quotas, discovered model evidence and session history are excluded.")).toBeTruthy();
  const stages = screen.getByRole("list", { name: "Configuration import stages" });
  const current = () => stages.querySelector('[aria-current="step"]')!;
  expect(current().textContent).toContain("Load document");
  expect(stages.querySelectorAll("button, a, input, [tabindex]")).toHaveLength(0);
  const json = screen.getByRole("textbox", { name: "Configuration JSON" });
  const file = screen.getByLabelText("Configuration file");
  expect(json.getAttribute("placeholder")).toBe("Paste a DeliDev configuration export…");
  expect((screen.getByRole("button", { name: "Load configuration document" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.change(json, { target: { value: JSON.stringify(value.bundle) } });
  expect(current().textContent).toContain("Load document");
  expect(screen.queryByRole("button", { name: "Preview configuration changes" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Load configuration document" }));
  expect(current().textContent).toContain("Map configuration");
  expect(value.preview).not.toHaveBeenCalled(); expect(value.apply).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Preview configuration changes" }));
  await screen.findByRole("textbox", { name: "Complete change details" });
  expect(current().textContent).toContain("Review & apply");
  rendered.rerender(value.view(false)); rendered.rerender(value.view(true));
  expect(screen.getByRole("textbox", { name: "Configuration JSON" })).toBe(json);
  expect(screen.getByLabelText("Configuration file")).toBe(file);
  expect(value.preview).toHaveBeenCalledTimes(1); expect(value.apply).not.toHaveBeenCalled();
  fireEvent.change(json, { target: { value: "invalid replacement" } });
  expect(current().textContent).toContain("Load document");
  expect(screen.queryByRole("textbox", { name: "Complete change details" })).toBeNull();
});

it("selects the exact exported Unicode and uint64 document for copying without importing", async () => {
  const value = fixture();
  const raw = JSON.stringify({ version: 2, entries: [{ id: newRequestId(), kind: "template", document: { name: "한국어", contents: 'Exact "quoted" text\n', revision: "18446744073709551615" } }], machines: [] }).replace('"18446744073709551615"', "18446744073709551615");
  value.exported.mockResolvedValueOnce({ documentJson: new TextEncoder().encode(raw) });
  render(value.view()); fireEvent.click(screen.getByRole("button", { name: "Export configuration" }));
  const exported = await screen.findByRole("textbox", { name: "Exported configuration" }) as HTMLTextAreaElement;
  expect(exported.value).toBe(raw); expect(exported.readOnly).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Select export for copying" }));
  expect(document.activeElement).toBe(exported);
  expect(exported.selectionStart).toBe(0); expect(exported.selectionEnd).toBe(raw.length);
  expect(value.preview).not.toHaveBeenCalled(); expect(value.apply).not.toHaveBeenCalled();
});

it("loads a UTF-8 file immediately and retains editable input after invalid UTF-8 or oversized paste", async () => {
  const value = fixture(); render(value.view());
  const raw = JSON.stringify(value.bundle);
  const file = new File([raw], "configuration.json", { type: "application/json" });
  Object.defineProperty(file, "arrayBuffer", { value: async () => new TextEncoder().encode(raw).buffer });
  fireEvent.change(screen.getByLabelText("Configuration file"), { target: { files: [file] } });
  expect(screen.getByRole("status").textContent).toBe("Reading configuration file…");
  await screen.findByRole("button", { name: "Preview configuration changes" });
  const json = screen.getByRole("textbox", { name: "Configuration JSON" }) as HTMLTextAreaElement;
  expect(json.value).toBe(raw); expect(json.matches(":disabled")).toBe(false);
  expect(value.preview).not.toHaveBeenCalled(); expect(value.apply).not.toHaveBeenCalled();
  fireEvent.change(json, { target: { value: "x".repeat(384 * 1024 + 1) } });
  expect(json.value).toBe(raw);
  const invalid = new File([new Uint8Array([0xc3, 0x28])], "invalid.json", { type: "application/json" });
  Object.defineProperty(invalid, "arrayBuffer", { value: async () => new Uint8Array([0xc3, 0x28]).buffer });
  fireEvent.change(screen.getByLabelText("Configuration file"), { target: { files: [invalid] } });
  await screen.findByText("The configuration file could not be read as UTF-8.");
  expect(json.value).toBe(raw); expect(json.matches(":disabled")).toBe(false);
  expect(screen.queryByRole("button", { name: "Preview configuration changes" })).toBeNull();
  expect(value.apply).not.toHaveBeenCalled();
});

it("accepts service-native v2 exports and original v2 preview bytes", async () => {
  const value = fixture();
  const body = { version: 2, entries: [{ id: value.bundle.entries[0].id, kind: "account", document: { type: "subscription", subscription_service: "chatgpt", alias: "Native account" } }], machines: [] };
  const preview = encode({ token: "server-preview", plan: { version: 2, changes: [{ source_id: body.entries[0].id, id: newRequestId(), kind: "account", action: "create", after: body.entries[0].document }], machines: [] } });
  value.preview.mockResolvedValue({ previewJson: preview }); render(value.view());
  load(body);
  fireEvent.click(screen.getByRole("button", { name: "Preview configuration changes" }));
  await screen.findByRole("button", { name: "Apply reviewed configuration" });
  expect(value.preview).toHaveBeenCalledTimes(1);
  const request = value.preview.mock.calls[0][0] as { selectionJson: Uint8Array };
  expect(new TextDecoder().decode(request.selectionJson).startsWith(`{"bundle":${JSON.stringify(body)},"bindings":`)).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Apply reviewed configuration" }));
  await waitFor(() => expect(value.apply).toHaveBeenCalledTimes(1));
  expect(Array.from((value.apply.mock.calls[0][0] as { previewJson: Uint8Array }).previewJson)).toEqual(Array.from(preview));
});

it("refuses a service-native v1 graph before requesting an import preview", () => {
  const value = fixture(); render(value.view());
  load({ ...value.bundle, version: 1, entries: [{ id: value.bundle.entries[0].id, kind: "account", document: { type: "subscription", subscription_service: "chatgpt" } }] });
  expect(screen.queryByRole("button", { name: "Preview configuration changes" })).toBeNull(); expect(value.preview).not.toHaveBeenCalled();
});

it("separates capability-read failure from unsupported repository imports and offers retry", async () => {
  const value = fixture();
  const repositoryBundle = { version: 2, entries: [{ id: newRequestId(), kind: "repository", document: { name: "Remote", remote_url: "https://example.com/remote.git", checkouts: [], base: {}, starting: {}, auto_fetch: true } }], machines: [] };
  value.status.mockRejectedValueOnce(new ConnectError("status unavailable", Code.Unavailable));
  render(value.view()); load(repositoryBundle);
  await screen.findByRole("button", { name: "Retry server capability check" });
  expect(screen.queryByText("Update the selected server before importing repositories.")).toBeNull();
  value.status.mockResolvedValueOnce({ capabilities: [SystemCapability.REMOTE_REPOSITORIES_V1] });
  fireEvent.click(screen.getByRole("button", { name: "Retry server capability check" }));
  await waitFor(() => expect(value.status).toHaveBeenCalledTimes(2));
  fireEvent.click(await screen.findByRole("button", { name: "Preview configuration changes" }));
  await screen.findByRole("button", { name: "Apply reviewed configuration" });
  expect(value.preview).toHaveBeenCalledTimes(1);
});

it("rejects checkout-only repositories before an import preview", () => {
 const value = fixture(); render(value.view());
 load({ version: 2, entries: [{ id: newRequestId(), kind: "repository", document: { name: "Old checkout", checkouts: [{ machine_id: newRequestId(), path: "/owned/checkout" }] } }], machines: [] });
 expect(screen.queryByRole("button", { name: "Preview configuration changes" })).toBeNull();
 expect(value.preview).not.toHaveBeenCalled();
});
