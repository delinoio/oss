import { webcrypto, createHash } from "node:crypto";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { DeviceService, DeviceType, EntityKind, ResourceSchema, ResourceService, newRequestId, type CreatePairingRequest } from "@delinoio/delidev-api-client";
import { PairingGrant } from "./pairing-grant";
import { MutationIntents } from "./mutation";
import { document, encode } from "./documents";

afterEach(() => vi.unstubAllGlobals());
function fixture(expired = false) {
  vi.stubGlobal("crypto", webcrypto);
  const authority = { endpoint: "https://paired.example.test", serverId: newRequestId() };
  const resource = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PAIRING, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Other computer", type: "client", expires_at: new Date(Date.now() + (expired ? -1000 : 300000)).toISOString() }) });
  const state = { current: resource };
  const issue = vi.fn(async (input: CreatePairingRequest) => ({ pairing: resource, requestId: input.requestId }));
  const read = vi.fn(async () => ({ resource: state.current }));
  const transport = createRouterTransport((router) => { router.service(DeviceService, { createPairing: issue }); router.service(ResourceService, { getResource: read }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><PairingGrant authority={authority} active={active} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { authority, resource, state, issue, read, client, view };
}
function issue() {
  fireEvent.click(screen.getByRole("button", { name: "Create pairing document" }));
  fireEvent.change(screen.getByLabelText("Device name"), { target: { value: "Other computer" } });
  fireEvent.click(screen.getByRole("button", { name: "Issue single-use document" }));
}
it("discards pending code preparation without issuing into a replacement task", async () => {
  const value = fixture();
  let release!: (digest: ArrayBuffer) => void;
  vi.stubGlobal("crypto", { getRandomValues: webcrypto.getRandomValues.bind(webcrypto), subtle: { digest: () => new Promise<ArrayBuffer>(resolve => { release = resolve; }) } });
  render(value.view()); issue();
  fireEvent.click(screen.getByRole("button", { name: "Close Pair another device" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Create pairing document" }));
  const name = screen.getByLabelText("Device name") as HTMLInputElement;
  fireEvent.change(name, { target: { value: "Fresh device" } });
  await act(async () => release(new ArrayBuffer(32)));
  expect(name.value).toBe("Fresh device");
  expect(value.issue).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Retry original pairing issuance" })).toBeNull();
});
it("issues only a code digest, privately reveals the exact pinned grant and hides it after navigation", async () => {
  const value = fixture();
  const view = render(value.view()); issue();
  fireEvent.click(await screen.findByRole("button", { name: "Reveal private document" }));
  const raw = (screen.getByLabelText("Private pairing document") as HTMLTextAreaElement).value;
  const grant = JSON.parse(raw);
  expect(grant).toMatchObject({ version: 1, pairing_id: value.resource.id, server_id: value.authority.serverId, endpoint: value.authority.endpoint });
  expect(grant.code).toMatch(/^[A-Za-z0-9_-]{43}$/);
  expect(value.issue.mock.calls[0][0]).toMatchObject({ type: DeviceType.CLIENT, name: "Other computer" });
  expect(Array.from(value.issue.mock.calls[0][0].codeDigest)).toEqual(Array.from(createHash("sha256").update(grant.code).digest()));
  expect(JSON.stringify(value.issue.mock.calls.map(([request]) => request))).not.toContain(grant.code);
  expect(JSON.stringify(value.client.getQueryCache().getAll().map((query) => [query.queryKey, query.state.data]), (_key, value) => typeof value === "bigint" ? value.toString() : value)).not.toContain(grant.code);
  view.rerender(value.view(false));
  expect(screen.queryByLabelText("Private pairing document")).toBeNull();
  view.rerender(value.view());
  fireEvent.click(await screen.findByRole("button", { name: "Reveal private document" }));
  expect((screen.getByLabelText("Private pairing document") as HTMLTextAreaElement).value).toBe(raw);
  fireEvent.click(screen.getByRole("button", { name: "Close Pair another device" }));
  expect(screen.queryByLabelText("Private pairing document")).toBeNull();
  expect(value.issue).toHaveBeenCalledTimes(1);
});
it("retains identical issuance after a lost response and settings visibility changes", async () => {
  const value = fixture();
  value.issue.mockRejectedValueOnce(new ConnectError("lost", Code.Unavailable));
  const view = render(value.view()); issue();
  await screen.findByRole("button", { name: "Retry original pairing issuance" });
  expect((screen.getByRole("button", { name: "Close Pair another device" }) as HTMLButtonElement).disabled).toBe(false);
  view.rerender(value.view(false)); view.rerender(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Retry original pairing issuance" }));
  await screen.findByRole("button", { name: "Reveal private document" });
  expect(value.issue).toHaveBeenCalledTimes(2);
  expect(value.issue.mock.calls[0][0]).toEqual(value.issue.mock.calls[1][0]);
});
it("clears the private code after a verified consumption observation", async () => {
  const value = fixture(); render(value.view()); issue();
  fireEvent.click(await screen.findByRole("button", { name: "Reveal private document" }));
  value.state.current = create(ResourceSchema, { ...value.resource, revision: 2n, documentJson: encode({ ...document(value.resource), used_by: newRequestId() }) });
  await value.client.invalidateQueries();
  await screen.findByText(/Pairing document was used/);
  expect(screen.queryByLabelText("Private pairing document")).toBeNull();
  expect(screen.queryByRole("button", { name: "Reveal private document" })).toBeNull();
  value.state.current = value.resource;
  await value.client.invalidateQueries();
  expect(screen.queryByRole("button", { name: "Reveal private document" })).toBeNull();
});

it.each([false, true])("requires the first fresh grant read before reveal, failed: %s", async (failed) => {
  const value = fixture();
  let resolve!: (result: { resource: typeof value.resource }) => void, reject!: (error: ConnectError) => void;
  const pending = new Promise<{ resource: typeof value.resource }>((done, fail) => { resolve = done; reject = fail; });
  value.read.mockImplementationOnce(() => pending);
  render(value.view()); issue();
  await waitFor(() => expect(value.read).toHaveBeenCalledTimes(1));
  expect(screen.queryByRole("button", { name: "Reveal private document" })).toBeNull();
  if (failed) {
    reject(new ConnectError("Synthetic first-read failure", Code.Unavailable));
    await screen.findByRole("alert");
    expect(screen.queryByRole("button", { name: "Reveal private document" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Refresh pairing status" }));
  } else resolve({ resource: value.resource });
  await screen.findByRole("button", { name: "Reveal private document" });
  expect(value.issue).toHaveBeenCalledTimes(1);
});
it("blocks malformed acknowledgments and expired grant exposure", async () => {
  const value = fixture(true); render(value.view()); issue();
  await screen.findByText(/Pairing document expired/);
  expect(screen.queryByRole("button", { name: "Reveal private document" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Close Pair another device" }));
  value.issue.mockImplementationOnce(async () => ({ pairing: value.resource, requestId: newRequestId() }));
  issue();
  await screen.findByText(/acknowledged without a matching grant/);
  expect(screen.queryByRole("button", { name: "Reveal private document" })).toBeNull();
  expect(value.issue).toHaveBeenCalledTimes(2);
});

it("hides a revealed grant when its current observation fails or changes identity", async () => {
  const value = fixture(); render(value.view()); issue();
  fireEvent.click(await screen.findByRole("button", { name: "Reveal private document" }));
  value.read.mockImplementationOnce(() => { throw new ConnectError("offline", Code.Unavailable); });
  fireEvent.click(screen.getByRole("button", { name: "Refresh pairing status" }));
  await screen.findByText("Grant issued; current use status is unavailable.");
  expect(screen.queryByLabelText("Private pairing document")).toBeNull();
  value.state.current = create(ResourceSchema, { ...value.resource, id: newRequestId() });
  fireEvent.click(screen.getByRole("button", { name: "Refresh pairing status" }));
  await screen.findByText("The grant observation no longer matches the original issuance.");
  expect(screen.queryByRole("button", { name: "Reveal private document" })).toBeNull();
  expect(value.issue).toHaveBeenCalledTimes(1);
});
