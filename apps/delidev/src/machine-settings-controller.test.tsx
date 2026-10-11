// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, WorkerService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { useMachineSettingsController, type MachineSettingsController } from "./machine-settings";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";

function fixture() {
  const machine = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, schemaVersion: 1, revision: 5n, documentJson: encode({ name: "Original", installations: [] }) });
  const job = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.JOB, schemaVersion: 1, revision: 8n, documentJson: encode({ state: "queued" }) });
  let observation: Resource | undefined = job, readError = false;
  let acceptedJob = job;
  const discover = vi.fn(() => ({ machine, job: acceptedJob }));
  const transport = createRouterTransport(router => { router.service(ResourceService, { getResource: request => { if (request.kind !== EntityKind.JOB) return { resource: machine }; if (readError) throw new ConnectError("Read failed", Code.Unavailable); return { resource: observation }; } }); router.service(WorkerService, { discoverHarnesses: discover }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  let controller!: MachineSettingsController;
  function Surface() { controller = useMachineSettingsController(machine, true); return null; }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Surface /></MutationIntents></QueryClientProvider></TransportProvider>);
  return { machine, job, client, discover, acceptJob: (next: Resource) => { acceptedJob = next; }, controller: () => controller,
    send: async () => { await act(() => controller.discovery.send({ mutation: { id: machine.id, expectedRevision: machine.revision, requestId: newRequestId() }, verifyProtocol: controller.verify })); },
    observe: async (value: Resource | undefined, error = false) => { observation = value; readError = error; await act(() => client.invalidateQueries()); },
  };
}
it.each(["queued", "claimed", "uncertain", "malformed", "missing", "foreign-id", "foreign-kind", "unsupported", "regressive", "invalid-json", "failed-read"])("retains the original gate for %s observations", async scenario => {
  const f = fixture(); await f.send();
  let row: Resource | undefined = create(ResourceSchema, { ...f.job, revision: 9n, documentJson: encode({ state: "succeeded" }) });
  if (["queued", "claimed", "uncertain", "malformed"].includes(scenario)) row = create(ResourceSchema, { ...row, documentJson: encode({ state: scenario }) });
  if (scenario === "missing") row = undefined;
  if (scenario === "foreign-id") row!.id = newRequestId();
  if (scenario === "foreign-kind") row!.kind = EntityKind.MACHINE;
  if (scenario === "unsupported") row!.schemaVersion = 99;
  if (scenario === "regressive") row!.revision = 7n;
  if (scenario === "invalid-json") row!.documentJson = new TextEncoder().encode("{broken");
  await f.observe(row, scenario === "failed-read");
  expect(f.controller().pending).toBe(true); expect(f.controller().locked).toBe(true); expect(f.discover).toHaveBeenCalledTimes(1); f.client.clear();
});
it("settles only its submitted executable and Verify snapshot", async () => {
  const f = fixture();
  act(() => { f.controller().setEdit({ revision: 5n, paths: { "claude-code": "/submitted" } }); f.controller().setVerify(true); });
  await f.send();
  act(() => { f.controller().setEdit({ revision: 5n, paths: { "claude-code": "/new-draft" } }); f.controller().setVerify(false); });
  await f.observe(create(ResourceSchema, { ...f.job, revision: 9n, documentJson: encode({ state: "succeeded" }) }));
  await waitFor(() => expect(f.controller().pending).toBe(false));
  expect(f.controller().edit?.paths["claude-code"]).toBe("/new-draft"); expect(f.controller().locked).toBe(true); expect(f.controller().job).toMatchObject({ id: f.job.id, revision: f.job.revision, kind: EntityKind.JOB });
  act(() => f.controller().setEdit(undefined)); expect(f.controller().locked).toBe(true); f.client.clear();
});
it("rejects a completion below the highest original job revision", async () => {
  const f = fixture(); await f.send();
  await f.observe(create(ResourceSchema, { ...f.job, revision: 10n, documentJson: encode({ state: "claimed" }) }));
  await f.observe(create(ResourceSchema, { ...f.job, revision: 9n, documentJson: encode({ state: "succeeded" }) }));
  expect(f.controller().pending).toBe(true); f.client.clear();
});

it("cannot settle a newer accepted job with the previous completion", async () => {
 const f = fixture(); await f.send();
 await f.observe(create(ResourceSchema, { ...f.job, revision: 9n, documentJson: encode({ state: "succeeded" }) }));
 await waitFor(() => expect(f.controller().pending).toBe(false));
 act(() => f.controller().setJob(undefined));
 const next = create(ResourceSchema, { ...f.job, id: newRequestId(), documentJson: encode({ state: "queued" }) });
 f.acceptJob(next); await f.send();
 await f.observe(create(ResourceSchema, { ...f.job, revision: 10n, documentJson: encode({ state: "succeeded" }) }));
 expect(f.controller().pending).toBe(true);
 await f.observe(create(ResourceSchema, { ...next, revision: 9n, documentJson: encode({ state: "succeeded" }) }));
 await waitFor(() => expect(f.controller().pending).toBe(false)); expect(f.discover).toHaveBeenCalledTimes(2); f.client.clear();
});
