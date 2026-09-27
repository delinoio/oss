import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, ResourceSchema, ResourceService, EntityKind, newRequestId } from "@delinoio/delidev-api-client";
import { ConfigurationTransfer, formatConfigurationReview } from "./configuration-transfer";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";

function fixture() {
  const source = newRequestId(), target = newRequestId(), jobId = newRequestId();
  const bundle = { version: 1, entries: [{ id: source, kind: "template", document: { name: "Instructions", contents: "Exact text\n한국어 <script>never execute</script>\n" } }], machines: [] };
  const previewDocument = { plan: { version: 1, changes: [{ source_id: source, id: target, kind: "template", action: "create", expected_revision: 0, after: bundle.entries[0]!.document }], machines: [] }, token: "server-scoped-preview" };
  const previewBytes = encode(previewDocument);
  const exported = vi.fn(async () => ({ documentJson: encode(bundle) }));
  const preview = vi.fn(async (_input: unknown) => ({ previewJson: previewBytes }));
  const apply = vi.fn(async (_input: unknown) => ({ resultJson: encode({ job_id: jobId, state: "queued", resources: [] }) }));
  let state = "queued";
  const transport = createRouterTransport((router) => {
    router.service(ConfigurationService, { exportConfiguration: exported, previewConfigurationImport: preview, applyConfigurationImport: apply });
    router.service(ResourceService, { getResource: () => ({ resource: create(ResourceSchema, { id: jobId, kind: EntityKind.JOB, schemaVersion: 1, revision: 1n, documentJson: encode({ type: "import-configuration", state }) }) }), listResources: () => ({ resources: [] }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ConfigurationTransfer active={active} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { bundle, previewBytes, exported, preview, apply, client, view, state: (next: string) => { state = next; } };
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
  const model = `{"version":1,"entries":[{"id":"${newRequestId()}","kind":"model","document":{"name":"Model","context_limit":18446744073709551615}}],"machines":[]}`;
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
