// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, NativeModelService, ResourceSchema, ResourceService, SystemCapability, SystemService, newRequestId, type DiscoverNativeModelsRequest, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { NativeModelSettings } from "./native-model-settings";
import { chooseScrollOption } from "./test-scroll-picker";

function fixture(loseFirst = false, scoped = false) {
  const provider = newRequestId();
  const machine = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, revision: 7n, schemaVersion: 1, documentJson: encode({ name: "Runner fixture" }) });
  const account = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 8n, schemaVersion: 1, documentJson: encode({ alias: "Account fixture", provider_id: provider, connection: { id: newRequestId() } }) });
  const job = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.JOB, revision: 3n, schemaVersion: 1, documentJson: encode({ type: "native-codex-models", state: "succeeded", input: { machine_id: machine.id, account_id: account.id, provider_id: provider, installation_generation: 1 }, output: { observed_at: "2026-10-01T00:00:00Z" } }) });
  const requests: DiscoverNativeModelsRequest[] = [];
  const discover = vi.fn(async (request: DiscoverNativeModelsRequest) => {
    requests.push(request);
    if (loseFirst && requests.length === 1) throw new ConnectError("Fixture lost acknowledgment", Code.Unavailable);
    return { job };
  });
  const createModel = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.NATIVE_CODEX_MODEL_DISCOVERY_V1] }) });
    router.service(ResourceService, { getResource: request => ({ resource: request.id === machine.id ? machine : request.id === account.id ? account : undefined }), listResources: (request) => ({ resources: request.filter?.kind === EntityKind.MACHINE ? [machine] : request.filter?.kind === EntityKind.ACCOUNT ? [account] : [] }) });
    router.service(NativeModelService, {
      discoverNativeModels: discover,
      getNativeModelObservation: () => ({ job }),
      listNativeModels: () => ({ job, modelsJson: encode([{ id: "picker-only", model: "executable-only", display_name: "Fixture model", description: "Advisory", reasoning: ["medium"], modalities: ["text"], service_tiers: [] }]) }),
    });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = (selectedAccounts?: Resource[]) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><NativeModelSettings active createModel={createModel} selectedAccounts={selectedAccounts} /></MutationIntents></QueryClientProvider></TransportProvider>;
  const rendered = render(view(scoped ? [account] : undefined));
  const details = screen.getByText("Native Codex model observations").parentElement as HTMLDetailsElement;
  details.open = true; fireEvent(details, new Event("toggle"));
  return { machine, account, provider, discover, requests, createModel, scoped, selectAccounts: (rows: Resource[]) => rendered.rerender(view(rows)) };
}

async function choose(value: ReturnType<typeof fixture>) {
  await chooseScrollOption(screen.getByRole("combobox", { name: "Runner Device" }), value.machine.id);
  if (value.scoped) fireEvent.change(screen.getByRole("combobox", { name: "Connected selected account" }), { target: { value: value.account.id } });
  else await chooseScrollOption(screen.getByRole("combobox", { name: "Connected account" }), value.account.id);
  fireEvent.click(screen.getByRole("button", { name: "Observe models" }));
}

it("observes exact revisions and prepares registration with the executable ID only after explicit selection", async () => {
  const value = fixture();
  expect(value.discover).not.toHaveBeenCalled();
  await choose(value);
  const register = await screen.findByRole("button", { name: "Register Fixture model…" });
  expect(value.createModel).not.toHaveBeenCalled();
  expect(value.requests[0]).toMatchObject({ mutation: { id: value.machine.id, expectedRevision: 7n }, accountId: value.account.id, accountRevision: 8n, includeHidden: false });
  fireEvent.click(register);
  expect(value.createModel).toHaveBeenCalledWith(expect.objectContaining({ provider_id: value.provider, native_id: "executable-only", manual: true, harnesses: ["codex"], metadata_source: "unknown" }));
});

it("blocks a new native observation and model choice after its account leaves the Worker selection", async () => {
  const value = fixture(false, true);
  await choose(value);
  await screen.findByRole("button", { name: "Use model Fixture model…" });
  value.selectAccounts([]);
  expect((screen.getByRole("button", { name: "Observe models" }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByRole("button", { name: "Use model Fixture model…" }) as HTMLButtonElement).disabled).toBe(true);
  expect(value.discover).toHaveBeenCalledTimes(1);
  expect(value.createModel).not.toHaveBeenCalled();
});

it("retains the exact discovery receipt after a lost response and blocks a replacement selection", async () => {
  const value = fixture(true);
  await choose(value);
  const retry = await screen.findByRole("button", { name: "Retry the same observation request" });
  expect((screen.getByLabelText("Runner Device") as HTMLSelectElement).disabled || Boolean(screen.getByLabelText("Runner Device").closest("fieldset[disabled]"))).toBe(true);
  fireEvent.click(retry);
  await waitFor(() => expect(value.requests).toHaveLength(2));
  expect(value.requests[1]).toEqual(value.requests[0]);
  await screen.findByRole("button", { name: "Register Fixture model…" });
  expect(value.createModel).not.toHaveBeenCalled();
});
