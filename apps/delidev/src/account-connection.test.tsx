// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountService, EntityKind, ResourceSchema, ResourceService, newRequestId, type DisconnectAccountRequest, type Resource } from "@delinoio/delidev-api-client";
import { AccountConnection } from "./account-connection";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";

const cleanupLabel = "Retry original credential cleanup";
function fixture(revision: bigint, removal?: string) {
  const id = newRequestId(), providerId = newRequestId(), connectionId = newRequestId();
  const account = (marker?: string) => create(ResourceSchema, {
    id, kind: EntityKind.ACCOUNT, schemaVersion: 1, revision: marker === undefined ? revision : revision + 1n,
    documentJson: new TextEncoder().encode(`{"alias":"Fixture API entry","type":"api","provider_id":"${providerId}","health":"${marker === undefined ? "unverified" : "disconnected"}",${marker === undefined ? `"connection":{"id":"${connectionId}"}` : `"removal":${marker}`}}`),
  });
  let current = account(removal), receipt: DisconnectAccountRequest["mutation"], secureDeletionConfirmed = false;
  const initial = current;
  const provider = create(ResourceSchema, { id: providerId, kind: EntityKind.PROVIDER, schemaVersion: 1, documentJson: encode({ name: "Fixture provider", enabled: true, authentication: "api-key" }) });
  const disconnect = vi.fn(async (request: DisconnectAccountRequest) => {
    receipt ??= request.mutation;
    expect(request.mutation).toEqual(receipt);
    current = account(`{"request_id":"${request.mutation!.requestId}","expected_revision":${request.mutation!.expectedRevision}}`);
    if (secureDeletionConfirmed) current = create(ResourceSchema, { ...current, revision: current.revision + 1n, documentJson: encode({ alias: "Fixture API entry", type: "api", provider_id: providerId, health: "disconnected" }) });
    return { account: current, requestId: request.mutation!.requestId, cleanupProblemJson: secureDeletionConfirmed ? new Uint8Array() : encode({ message: "Fixture vault cleanup is pending." }) };
  });
  const status = vi.fn(async () => ({ account: current }));
  const transport = createRouterTransport((router) => {
    router.service(AccountService, { getAccountStatus: status, disconnectAccount: disconnect });
    router.service(ResourceService, { getResource: () => ({ resource: provider }) });
  });
  const mount = (row: Resource = initial) => {
    // A new query client and mutation registry model loss of all renderer memory.
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const view = render(<StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><AccountConnection initial={row} active close={vi.fn()} /></MutationIntents></QueryClientProvider></TransportProvider></StrictMode>);
    return { unmount: () => { view.unmount(); client.clear(); } };
  };
  return { id, initial, disconnect, status, mount, confirmSecureDeletion: () => { secureDeletionConfirmed = true; }, get current() { return current; } };
}

it("reconstructs a high-revision cleanup request after vault failure and a fresh mount", async () => {
  const revision = 9007199254740993n, value = fixture(revision);
  const first = value.mount();
  fireEvent.click(await screen.findByRole("button", { name: "Disconnect" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm disconnection" }));
  await screen.findByText("Fixture vault cleanup is pending.");
  expect(value.disconnect).toHaveBeenCalledTimes(1);
  const original = value.disconnect.mock.calls[0][0].mutation!;
  expect(original).toMatchObject({ id: value.id, expectedRevision: revision });
  expect(value.current.revision).not.toBe(original.expectedRevision);
  expect(screen.queryByLabelText("API key")).toBeNull();
  first.unmount();

  const fresh = value.mount();
  const retry = await screen.findByRole("button", { name: cleanupLabel });
  await waitFor(() => expect((retry as HTMLButtonElement).disabled).toBe(false));
  expect(value.disconnect).toHaveBeenCalledTimes(1);
  fireEvent.click(retry);
  await screen.findByText("Fixture vault cleanup is pending.");
  expect(value.disconnect).toHaveBeenCalledTimes(2);
  expect(value.disconnect.mock.calls[1][0].mutation).toEqual(original);
  expect(screen.queryByLabelText("API key")).toBeNull();

  value.confirmSecureDeletion();
  fireEvent.click(screen.getByRole("button", { name: cleanupLabel }));
  await screen.findByLabelText("API key");
  expect(screen.queryByRole("button", { name: cleanupLabel })).toBeNull();
  expect(value.disconnect).toHaveBeenCalledTimes(3);
  expect(value.disconnect.mock.calls[2][0].mutation).toEqual(original);
  fresh.unmount();
});

it.each(["42", "9007199254740993", "18446744073709551615"])("sends the exact durable revision %s through Connect after a fresh mount", async (revision) => {
  const requestId = newRequestId();
  const value = fixture(8n, `{"request_id":"${requestId}","expected_revision":${revision}}`);
  const view = value.mount();
  const retry = await screen.findByRole("button", { name: cleanupLabel });
  expect((retry as HTMLButtonElement).disabled).toBe(false);
  expect(value.disconnect).not.toHaveBeenCalled();
  fireEvent.click(retry);
  await waitFor(() => expect(value.disconnect).toHaveBeenCalledTimes(1));
  expect(value.disconnect.mock.calls[0][0].mutation).toMatchObject({ id: value.id, requestId, expectedRevision: BigInt(revision) });
  view.unmount();
});

it("retains the original high revision and UUID when cleanup transport delivery is uncertain", async () => {
  const requestId = newRequestId(), value = fixture(8n, `{"request_id":"${requestId}","expected_revision":9007199254740993}`);
  value.disconnect.mockRejectedValueOnce(new ConnectError("Fixture response lost", Code.Unavailable));
  const view = value.mount();
  fireEvent.click(await screen.findByRole("button", { name: cleanupLabel }));
  const retry = await screen.findByRole("button", { name: "Retry the same disconnection" });
  expect((screen.getByRole("button", { name: cleanupLabel }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(retry);
  await screen.findByText("Fixture vault cleanup is pending.");
  expect(value.disconnect).toHaveBeenCalledTimes(2);
  expect(value.disconnect.mock.calls[1][0].mutation).toEqual(value.disconnect.mock.calls[0][0].mutation);
  expect(value.disconnect.mock.calls[1][0].mutation).toMatchObject({ requestId, expectedRevision: 9007199254740993n });
  view.unmount();
});

it.each([
  '{"expected_revision":42}',
  '{"request_id":"invalid","expected_revision":42}',
  `{"request_id":"${newRequestId()}","expected_revision":18446744073709551616}`,
  `{"request_id":"${newRequestId()}","expected_revision":"42"}`,
  `{"request_id":"${newRequestId()}","expected_revision":42,"expected_revision":43}`,
])("keeps an invalid durable marker non-actionable: %s", async (removal) => {
  const value = fixture(8n, removal), view = value.mount();
  const retry = await screen.findByRole("button", { name: cleanupLabel });
  expect((retry as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(retry);
  expect(value.disconnect).not.toHaveBeenCalled();
  expect(screen.queryByLabelText("API key")).toBeNull();
  view.unmount();
});
