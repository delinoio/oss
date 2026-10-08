// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, act } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountService, ProviderService, EntityKind, ResourceSchema, ResourceService, newRequestId, type Resource, type ValidateAccountRequest } from "@delinoio/delidev-api-client";
import { ApiVerification } from "./api-verification";
import { document, encode, type Document } from "./documents";
import { MutationIntents } from "./mutation";
import { i18n } from "./localization";

function fixture(extra: Document = {}, discovery = true) {
  const provider = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROVIDER, schemaVersion: 1, documentJson: encode({ enabled: true, discovery }) });
  const connection = newRequestId();
  let row = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 1, revision: 1n, documentJson: encode({ type: "api", provider_id: provider.id, enabled: true, connection: { id: connection }, ...extra }) });
  const order: string[] = [];
  const validate = vi.fn(async (request: ValidateAccountRequest) => {
    order.push("validate");
    const validation = { request_id: request.mutation!.requestId, connection_id: connection, state: "observed", authentication: "credential-accepted", observed_at: "2026-10-07T01:00:00Z" };
    row = create(ResourceSchema, { ...row, revision: 2n, documentJson: encode({ ...document(row), validation }) });
    return { account: row, requestId: request.mutation!.requestId, validationJson: encode(validation) };
  });
  const discover = vi.fn(async (request: { mutation?: { expectedRevision: bigint; requestId: string } }) => {
    order.push("discover"); expect(request.mutation!.expectedRevision).toBe(2n);
    return { account: row, requestId: request.mutation!.requestId, observationJson: encode({ request_id: request.mutation!.requestId, connection_id: connection, state: "observed" }) };
  });
  const getProvider = vi.fn(async (_request: { id: string; kind: EntityKind }) => ({ resource: provider }));
  const transport = createRouterTransport(router => { router.service(ResourceService, { getResource: getProvider }); router.service(AccountService, { validateAccount: validate }); router.service(ProviderService, { discoverModels: discover }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const changed = vi.fn();
  const view = (resource: Resource = row, active = true, suppliedProvider: Resource | null = provider) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ApiVerification row={resource} provider={suppliedProvider ?? undefined} active={active} changed={changed} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { row, provider, connection, validate, discover, order, changed, view, getProvider };
}

it("does not verify on opening and checks validation before discovery with its confirmed revision", async () => {
  const f = fixture(); render(f.view()); expect(screen.getByText("Awaiting verification")).toBeTruthy(); expect(f.validate).not.toHaveBeenCalled(); expect(f.discover).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Check again" })); await waitFor(() => expect(f.discover).toHaveBeenCalledTimes(1)); expect(f.order).toEqual(["validate", "discover"]); expect(screen.getByText("API authentication verified")).toBeTruthy();
});
it("retains an uncertain validation identity and blocks discovery until an explicit exact retry", async () => {
  const f = fixture(); f.validate.mockRejectedValueOnce(new ConnectError("fixture lost response", Code.Unavailable)); render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Check again" }));
  await screen.findByRole("button", { name: "Retry original authentication check" }); expect(screen.getByRole("button", { name: "Check again" })).toHaveProperty("disabled", true); expect(f.discover).not.toHaveBeenCalled(); const original = f.validate.mock.calls[0][0];
  fireEvent.click(screen.getByRole("button", { name: "Retry original authentication check" })); await waitFor(() => expect(f.discover).toHaveBeenCalledTimes(1)); expect(f.validate.mock.calls[1][0]).toEqual(original);
});
it("does not discover when disabled and preserves focus while localized observations change", async () => {
  const f = fixture({}, false); render(f.view()); const button = screen.getByRole("button", { name: "Check again" }); button.focus(); fireEvent.click(button); await screen.findByText("API authentication verified"); expect(f.discover).not.toHaveBeenCalled(); expect(globalThis.document.activeElement).toBe(button);
  await act(async () => { await i18n.changeLanguage("ko"); }); expect(screen.getByText("모델 검색이 비활성화되었습니다")).toBeTruthy(); expect(f.validate).toHaveBeenCalledTimes(1); await act(async () => { await i18n.changeLanguage("en"); });
});
it.each(["failed", "unsupported"])("keeps authentication %s and retained catalog failures separate", async state => {
  const f = fixture(); const row = create(ResourceSchema, { ...f.row, documentJson: encode({ ...document(f.row), validation: { connection_id: f.connection, state, authentication: "unknown" }, catalog: { connection_id: f.connection, state: "failed", last_success_at: "2026-10-06T01:00:00Z" } }) });
  render(f.view(row)); expect(screen.queryByText("API authentication verified")).toBeNull(); expect(screen.getByText(/Previous models retained/)).toBeTruthy(); expect(f.validate).not.toHaveBeenCalled();
});
it("ignores mismatched connection evidence and disables Check again for cleanup state", async () => {
  const f = fixture({ validation: { connection_id: newRequestId(), state: "observed", authentication: "credential-accepted" }, removal: { request_id: newRequestId() } }); render(f.view()); expect(screen.queryByText("API authentication verified")).toBeNull(); expect(screen.getByText("Credential cleanup is pending.")).toBeTruthy(); expect(screen.getByRole("button", { name: "Check again" })).toHaveProperty("disabled", true);
});

it("reconciles an old validation replay without discovering the replacement connection", async () => {
  const f = fixture();
  f.validate.mockRejectedValueOnce(new ConnectError("lost acknowledgment", Code.Unavailable));
  const view = render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check again" }));
  await screen.findByRole("button", { name: "Retry original authentication check" });
  const original = f.validate.mock.calls[0][0];
  const replacement = create(ResourceSchema, { ...f.row, revision: 5n, documentJson: encode({ ...document(f.row), connection: { id: newRequestId() } }) });
  view.rerender(f.view(replacement));
  f.validate.mockResolvedValueOnce({ account: replacement, requestId: original.mutation!.requestId, validationJson: encode({ request_id: original.mutation!.requestId, connection_id: f.connection, state: "observed", authentication: "credential-accepted" }) });
  fireEvent.click(screen.getByRole("button", { name: "Retry original authentication check" }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry original authentication check" })).toBeNull());
  expect(f.validate.mock.calls[1][0]).toEqual(original);
  expect(f.discover).not.toHaveBeenCalled();
  expect(screen.getByRole("button", { name: "Check again" })).toHaveProperty("disabled", false);
  expect(screen.queryByText("API authentication verified")).toBeNull();
});

it.each(["request", "connection"])("settles original discovery replay after reconnect and rejects a wrong %s receipt", async mismatch => {
  const f = fixture();
  f.discover.mockRejectedValueOnce(new ConnectError("lost acknowledgment", Code.Unavailable));
  const view = render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check again" }));
  await screen.findByRole("button", { name: "Retry original model refresh" });
  const original = f.discover.mock.calls[0][0];
  const replacement = create(ResourceSchema, { ...f.row, revision: 5n, documentJson: encode({ ...document(f.row), connection: { id: newRequestId() } }) });
  view.rerender(f.view(replacement));
  f.discover.mockResolvedValueOnce({ account: replacement, requestId: original.mutation!.requestId, observationJson: encode({ request_id: mismatch === "request" ? newRequestId() : original.mutation!.requestId, connection_id: mismatch === "connection" ? newRequestId() : f.connection, state: "observed" }) });
  fireEvent.click(screen.getByRole("button", { name: "Retry original model refresh" }));
  await waitFor(() => expect(f.discover).toHaveBeenCalledTimes(2));
  await screen.findByRole("button", { name: "Retry original model refresh" });
  f.discover.mockResolvedValueOnce({ account: replacement, requestId: original.mutation!.requestId, observationJson: encode({ request_id: original.mutation!.requestId, connection_id: f.connection, state: "observed" }) });
  fireEvent.click(screen.getByRole("button", { name: "Retry original model refresh" }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry original model refresh" })).toBeNull());
  expect(f.discover.mock.calls[1][0]).toEqual(original);
  expect(f.discover.mock.calls[2][0]).toEqual(original);
  expect(f.validate).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "Check again" })).toHaveProperty("disabled", false);
  expect(screen.queryByText("API authentication verified")).toBeNull();
});

it("resolves a provider outside the bounded inventory by exact identity before checking", async () => {
  const f = fixture(); render(f.view(f.row, true, null));
  await waitFor(() => expect(screen.getByRole("button", { name: "Check again" })).toHaveProperty("disabled", false));
  expect(f.getProvider).toHaveBeenCalledTimes(1);
  expect(f.getProvider.mock.calls[0][0]).toMatchObject({ id: f.provider.id, kind: EntityKind.PROVIDER });
  expect(f.validate).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Check again" }));
  await waitFor(() => expect(f.discover).toHaveBeenCalledTimes(1));
  expect(f.order).toEqual(["validate", "discover"]);
});
it("retains explicit provider-read recovery and rejects foreign provider proof", async () => {
  const f = fixture();
  f.getProvider.mockResolvedValueOnce({ resource: create(ResourceSchema, { ...f.provider, id: newRequestId() }) });
  render(f.view(f.row, true, null));
  await screen.findByRole("button", { name: "Retry provider details" });
  expect(screen.getByRole("button", { name: "Check again" })).toHaveProperty("disabled", true);
  expect(f.validate).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Retry provider details" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Check again" })).toHaveProperty("disabled", false));
  expect(f.getProvider.mock.calls.every(([request]) => request.id === f.provider.id && request.kind === EntityKind.PROVIDER)).toBe(true);
  expect(f.discover).not.toHaveBeenCalled();
});

it("pauses referenced provider reads with the background verification owner", async () => {
  const f = fixture(); const view = render(f.view(f.row, false, null));
  expect(f.getProvider).not.toHaveBeenCalled();
  expect(screen.getByRole("button", { name: "Check again" })).toHaveProperty("disabled", true);
  view.rerender(f.view(f.row, true, null));
  await waitFor(() => expect(screen.getByRole("button", { name: "Check again" })).toHaveProperty("disabled", false));
  expect(f.getProvider).toHaveBeenCalledTimes(1);
  view.rerender(f.view(f.row, false, null));
  expect(screen.getByRole("button", { name: "Check again" })).toHaveProperty("disabled", true);
  expect(f.validate).not.toHaveBeenCalled();
  expect(f.discover).not.toHaveBeenCalled();
});


it("keeps complete observations in distinct groups and the disclaimer outside the band", () => {
  const f = fixture();
  const row = create(ResourceSchema, { ...f.row, documentJson: encode({ ...document(f.row), validation: { connection_id: f.connection, state: "observed", authentication: "credential-accepted", observed_at: "2026-10-08T00:08:14Z" }, catalog: { connection_id: f.connection, state: "observed", received: 467, observed_at: "2026-10-08T00:08:14Z" } }) });
  render(f.view(row));
  const band = window.document.querySelector(".api-verification-band")!;
  expect(band.querySelectorAll(".api-verification-group")).toHaveLength(3);
  expect(screen.getByText("API authentication verified").closest(".api-verification-group")).not.toBeNull();
  expect(screen.getByText("Models synced · 467 models").closest(".api-verification-group")).not.toBeNull();
  expect(screen.getByText(/Last checked/).closest(".api-verification-times")).not.toBeNull();
  const disclaimer = screen.getByText("Verification does not establish model inference permission, harness readiness, usage or quota recovery.");
  expect(band.contains(disclaimer)).toBe(false);
  expect(band.querySelectorAll('[aria-hidden="true"]')).toHaveLength(2);
  expect(f.validate).not.toHaveBeenCalled();
  expect(f.discover).not.toHaveBeenCalled();
});
