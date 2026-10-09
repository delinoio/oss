import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, ErrorDetailSchema, ResourceSchema, ResourceService, EntityKind, SystemCapability, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { ConfigurationTransfer, formatConfigurationReview } from "./configuration-transfer";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";

function fixture() {
  const source = newRequestId(), target = newRequestId(), jobId = newRequestId();
  const bundle = { version: 4, entries: [{ id: source, kind: "template", document: { name: "Instructions", contents: "Exact text\n한국어 <script>never execute</script>\n" } }], machines: [] };
  const previewDocument = { plan: { version: 4, changes: [{ source_id: source, id: target, kind: "template", action: "create", expected_revision: 0, after: bundle.entries[0]!.document }], machines: [] }, token: "server-scoped-preview" };
  const previewBytes = encode(previewDocument);
  const exported = vi.fn(async () => ({ documentJson: encode(bundle) }));
  const preview = vi.fn(async (_input: unknown) => ({ previewJson: previewBytes }));
  const apply = vi.fn(async (_input: unknown) => ({ resultJson: encode({ job_id: jobId, state: "queued", resources: [] }) }));
  const status = vi.fn(async () => ({ capabilities: [SystemCapability.REMOTE_REPOSITORIES_V1] }));
  let state = "queued";
  const getResource = vi.fn((_input: { id: string; kind: EntityKind }) => ({ resource: create(ResourceSchema, { id: jobId, kind: EntityKind.JOB, schemaVersion: 1, revision: 1n, documentJson: encode({ type: "import-configuration", state }) }) }));
  const transport = createRouterTransport((router) => {
    router.service(ConfigurationService, { exportConfiguration: exported, previewConfigurationImport: preview, applyConfigurationImport: apply });
    router.service(SystemService, { getStatus: status });
    router.service(ResourceService, { getResource, listResources: () => ({ resources: [] }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ConfigurationTransfer active={active} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { bundle, previewBytes, exported, preview, apply, client, view, status, getResource, jobId, state: (next: string) => { state = next; } };
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
  value.state("succeeded"); await value.client.invalidateQueries();
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
  const model = `{"version":4,"entries":[{"id":"${newRequestId()}","kind":"template","document":{"name":"Model","context_limit":18446744073709551615}}],"machines":[]}`;
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
  const raw = JSON.stringify({ version: 4, entries: [{ id: newRequestId(), kind: "template", document: { name: "한국어", contents: 'Exact "quoted" text\n', revision: "18446744073709551615" } }], machines: [] }).replace('"18446744073709551615"', "18446744073709551615");
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

it("accepts current service-native v4 exports and preserves original preview bytes", async () => {
  const value = fixture();
  const body = { version: 4, entries: [{ id: value.bundle.entries[0].id, kind: "account", document: { type: "subscription", subscription_service: "chatgpt", alias: "Native account" } }], machines: [] };
  const preview = encode({ token: "server-preview", plan: { version: 4, changes: [{ source_id: body.entries[0].id, id: newRequestId(), kind: "account", action: "create", after: body.entries[0].document }], machines: [] } });
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

it.each([1,2,3,5,6])("refuses portable version %s before requesting an import preview", version => {
 const value=fixture();render(value.view());load({...value.bundle,version});expect(screen.getByText(/Use a current version 4/)).toBeTruthy();expect(value.preview).not.toHaveBeenCalled();
});
it("refuses retired Model entries in the current portable layout",()=>{const value=fixture();render(value.view());load({...value.bundle,entries:[{id:newRequestId(),kind:"model",document:{native_id:"retired"}}]});expect(screen.getByRole("alert")).toBeTruthy();expect(value.preview).not.toHaveBeenCalled();});

it("separates capability-read failure from unsupported repository imports and offers retry", async () => {
  const value = fixture();
  const repositoryBundle = { version: 4, entries: [{ id: newRequestId(), kind: "repository", document: { name: "Remote", remote_url: "https://example.com/remote.git", checkouts: [], base: {}, starting: {}, auto_fetch: true } }], machines: [] };
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

it("keeps legacy checkout-backed repository imports available without capability 37", async () => {
  const value = fixture();
  const repositoryBundle = { version: 4, entries: [{ id: newRequestId(), kind: "repository", document: { name: "Legacy", remote_url: "", checkouts: [{ machine_id: newRequestId(), path: "/owned/legacy" }], base: {}, starting: {}, auto_fetch: true } }], machines: [] };
  render(value.view()); load(repositoryBundle);
  fireEvent.click(await screen.findByRole("button", { name: "Preview configuration changes" }));
  await screen.findByRole("button", { name: "Apply reviewed configuration" });
  expect(value.status).not.toHaveBeenCalled();
  expect(value.preview).toHaveBeenCalledTimes(1);
});


it.each(["foreign ID", "wrong kind"])("keeps polling and retries the original import after a %s terminal response", async (invalid) => {
  const value = fixture();
  value.getResource.mockImplementation(() => ({ resource: create(ResourceSchema, { id: invalid === "foreign ID" ? newRequestId() : value.jobId, kind: invalid === "wrong kind" ? EntityKind.TEMPLATE : EntityKind.JOB, schemaVersion: 1, revision: 1n, documentJson: encode({ state: "succeeded" }) }) }));
  render(value.view()); load(value.bundle);
  fireEvent.click(screen.getByRole("button", { name: "Preview configuration changes" }));
  fireEvent.click(await screen.findByRole("button", { name: "Apply reviewed configuration" }));
  await screen.findByRole("button", { name: "Retry original status read" });
  expect(screen.queryByRole("button", { name: "Return to retained import document" })).toBeNull();
  const reads = value.getResource.mock.calls.length;
  await waitFor(() => expect(value.getResource.mock.calls.length).toBeGreaterThan(reads), { timeout: 3500 });
  value.getResource.mockImplementation(() => ({ resource: create(ResourceSchema, { id: value.jobId, kind: EntityKind.JOB, schemaVersion: 1, revision: 2n, documentJson: encode({ state: "succeeded" }) }) }));
  fireEvent.click(screen.getByRole("button", { name: "Retry original status read" }));
  await screen.findByText(/Configuration import completed/);
  expect(value.getResource.mock.calls.every(([request]) => request.id === value.jobId && request.kind === EntityKind.JOB)).toBe(true);
  expect(value.apply).toHaveBeenCalledTimes(1);
});

it("requires explicit confirmation of every unique suggested import target", async () => {
  const value = fixture(), sourceA = newRequestId(), sourceB = newRequestId(), targetA = newRequestId(), targetB = newRequestId();
  const bundle = { ...value.bundle, machines: [{ id: sourceA, name: "Source A", os: "darwin", architecture: "arm64" }, { id: sourceB, name: "Source B", os: "darwin", architecture: "arm64" }] };
  const targets = [targetA, targetB].map(id => create(ResourceSchema, { id, kind: EntityKind.MACHINE, schemaVersion: 1, revision: 1n, documentJson: encode({name:id,enabled:true}) }));
  const get = vi.fn((input: {id:string}) => ({ resource: targets.find(row=>row.id===input.id) }));
  const transport = createRouterTransport(router => {
    router.service(ConfigurationService, { previewConfigurationImport:value.preview });
    router.service(ResourceService, { getResource:get, listResources:()=>({resources:targets}) });
  });
  const bridge = {read:vi.fn(async()=>({revision:1,scope:{server_id:newRequestId(),device_id:newRequestId()},machine_id:targetA,problem:null})),update:vi.fn()};
  const { RunnerPreferenceProvider } = await import("./runner-device-preferences");
  render(<TransportProvider transport={transport}><QueryClientProvider client={value.client}><MutationIntents><RunnerPreferenceProvider bridge={bridge} readLocalWorker={async()=>({machineId:targetB,token:"discard-me"})}><ConfigurationTransfer active/></RunnerPreferenceProvider></MutationIntents></QueryClientProvider></TransportProvider>);
  load(bundle);
  await waitFor(()=>expect(get).toHaveBeenCalledWith(expect.objectContaining({id:targetB}),expect.anything()));
  const preview=screen.getByRole("button",{name:"Preview configuration changes"}) as HTMLButtonElement;
  expect(preview.disabled).toBe(true);
  const confirmations=screen.getAllByRole("checkbox",{name:"Confirm this target device"});
  fireEvent.click(confirmations[0]);expect(preview.disabled).toBe(true);fireEvent.click(confirmations[1]);expect(preview.disabled).toBe(false);
  fireEvent.click(preview);await waitFor(()=>expect(value.preview).toHaveBeenCalledTimes(1));
  const selection=JSON.parse(new TextDecoder().decode((value.preview.mock.calls[0][0] as {selectionJson:Uint8Array}).selectionJson));
  expect(selection.machines).toEqual([{source_id:sourceA,target_id:targetA},{source_id:sourceB,target_id:targetB}]);expect(bridge.update).not.toHaveBeenCalled();
});


it("retains sanitized typed export guidance and clears it for the next export generation", async () => {
 const value=fixture(),reference=newRequestId();
 const message="The configuration transfer exceeds its bound.",guidance="Use at most 256 configuration entries, 64 checkouts and a 384 KiB export document.";
 value.exported.mockRejectedValueOnce(new ConnectError(message,Code.ResourceExhausted,undefined,[{desc:ErrorDetailSchema,value:{code:"resource_exhausted",guidance,correlationId:reference}}]));
 render(value.view());fireEvent.click(screen.getByRole("button",{name:"Export configuration"}));
 await screen.findByText("A capacity limit was reached. Inspect capacity before submitting another request.");
 expect(screen.queryByText("Export failed.")).toBeNull();expect(screen.queryByRole("textbox",{name:"Exported configuration"})).toBeNull();
 fireEvent.click(screen.getByText("Technical details"));expect(screen.getByText(message)).toBeTruthy();expect(screen.getByText(guidance)).toBeTruthy();expect(screen.getByText("resource_exhausted")).toBeTruthy();expect(screen.getByText(new RegExp(reference))).toBeTruthy();
 let finish!: (result:{documentJson:Uint8Array})=>void;
 value.exported.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve;}));
 fireEvent.click(screen.getByRole("button",{name:"Export configuration"}));
 await waitFor(()=>expect(value.exported).toHaveBeenCalledTimes(2));expect(screen.queryByRole("alert")).toBeNull();expect(screen.queryByText(message)).toBeNull();expect(screen.queryByText(guidance)).toBeNull();
 const raw=JSON.stringify(value.bundle).replace('"version":4','"version":4,"exact":18446744073709551615');
 await act(async()=>finish({documentJson:new TextEncoder().encode(raw)}));
 const exported=await screen.findByRole("textbox",{name:"Exported configuration"}) as HTMLTextAreaElement;expect(exported.value).toBe(raw);
 fireEvent.click(screen.getByRole("button",{name:"Select export for copying"}));expect(exported.selectionEnd).toBe(raw.length);expect(screen.queryByRole("alert")).toBeNull();expect(value.preview).not.toHaveBeenCalled();expect(value.apply).not.toHaveBeenCalled();
});

it("sanitizes untyped export errors and preserves local document validation messages",async()=>{
 const value=fixture();value.exported.mockRejectedValueOnce(new ConnectError("credential=private-token /private/user/config provider response",Code.Unavailable));
 const mounted=render(value.view());fireEvent.click(screen.getByRole("button",{name:"Export configuration"}));
 await screen.findByRole("alert");fireEvent.click(screen.getByText("Technical details"));expect(screen.getByText("The DeliDev request could not complete.")).toBeTruthy();expect(document.body.textContent).not.toMatch(/private-token|private\/user|provider response/);expect(screen.queryByRole("textbox",{name:"Exported configuration"})).toBeNull();
 value.exported.mockResolvedValueOnce({documentJson:encode({version:999,entries:[],machines:[]})});fireEvent.click(screen.getByRole("button",{name:"Export configuration"}));
 await waitFor(()=>expect(screen.queryByText("Technical details")).toBeNull());expect(screen.getByRole("alert").textContent).toContain("Use a current version 4 DeliDev configuration export.");expect(screen.queryByText("The DeliDev request could not complete.")).toBeNull();
 mounted.unmount();
});

it("does not publish an export failure after the original Settings controller is disposed",async()=>{
 const value=fixture();let reject!: (reason:unknown)=>void;value.exported.mockImplementationOnce(()=>new Promise((_resolve,rejected)=>{reject=rejected;}));
 const mounted=render(value.view());fireEvent.click(screen.getByRole("button",{name:"Export configuration"}));await waitFor(()=>expect(value.exported).toHaveBeenCalledOnce());mounted.unmount();
 await act(async()=>reject(new ConnectError("old export generation",Code.ResourceExhausted)));
 render(value.view());expect(screen.queryByRole("alert")).toBeNull();expect(screen.queryByRole("textbox",{name:"Exported configuration"})).toBeNull();
});
