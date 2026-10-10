// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, WorkerService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MachineSettingsView, useMachineSettingsController } from "./machine-settings";
import { MutationIntents } from "./mutation";

function fixture() {
  const machine = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, schemaVersion: 1, revision: 3n, documentJson: encode({ name: "Original Runner", installations: [] }) });
  const job = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.JOB, schemaVersion: 1, revision: 4n, documentJson: encode({ state: "queued" }) });
  let observation: typeof job | undefined = job, readError = false;
  const discovery = vi.fn(() => ({ machine, job }));
  const read = vi.fn((request: { id: string }) => { if (request.id === machine.id) return { resource: machine }; if (readError) throw new ConnectError("Read failed", Code.Unavailable); return { resource: observation }; });
  const transport = createRouterTransport(router => { router.service(ResourceService, { getResource: read }); router.service(WorkerService, { discoverHarnesses: discovery }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Surface() {
    const controller = useMachineSettingsController(machine, true);
    const [open, setOpen] = useState(true);
    return <><input aria-label="Schedule name" defaultValue="Keep draft" disabled={controller.locked} /><button disabled={controller.locked}>Next</button><button disabled={controller.locked}>Cancel</button>
      <button onClick={() => setOpen(value => !value)}>Toggle inspection</button>
      <button onClick={() => controller.setEdit({ revision: machine.revision, paths: { codex: "/submitted" } })}>Set submitted path</button>
      <button onClick={() => controller.setEdit({ revision: machine.revision, paths: { codex: "/later" } })}>Set later path</button>
      <button onClick={() => controller.setVerify(false)}>Change Verify</button>
      <button onClick={() => controller.setEdit(undefined)}>Discard path</button>
      <output aria-label="Path draft">{controller.edit?.paths.codex}</output>
      {open ? <MachineSettingsView controller={controller} active close={() => setOpen(false)} compact /> : null}</>;
  }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Surface /></MutationIntents></QueryClientProvider></TransportProvider>);
  return { job, machine, client, discovery, read, update: async (row?: typeof job) => { observation = row; await act(async () => { await client.invalidateQueries(); }); }, fail: async () => { readError = true; await act(async () => { await client.invalidateQueries(); }); } };
}

it.each(["succeeded", "failed", "canceled"])("settles %s inspection while retaining its original result across close", async state => {
  const f = fixture(); fireEvent.click(screen.getByText("Set submitted path"));
  fireEvent.click(screen.getByRole("button", { name: "Run optional diagnostics" }));
  await waitFor(() => expect(f.read.mock.calls.some(([request]) => request.id === f.job.id)).toBe(true));
  fireEvent.click(screen.getByText("Toggle inspection"));
  expect(screen.getByLabelText("Schedule name")).toHaveProperty("disabled", true);
  await f.update(create(ResourceSchema, { ...f.job, revision: 5n, documentJson: encode({ state }) }));
  await waitFor(() => expect(screen.getByLabelText("Schedule name")).toHaveProperty("disabled", false));
  expect(screen.getByLabelText("Schedule name")).toHaveProperty("value", "Keep draft");
  expect(screen.getByText("Next")).toHaveProperty("disabled", false);
  expect(screen.getByText("Cancel")).toHaveProperty("disabled", false);
  fireEvent.click(screen.getByText("Toggle inspection"));
  expect(await screen.findByRole("button", { name: "Finish inspection" })).toBeTruthy();
  expect(f.discovery).toHaveBeenCalledOnce(); f.client.clear();
});

it.each(["queued", "claimed", "uncertain", "malformed", "foreign", "wrong-kind", "unsupported", "regressive", "missing"])("keeps original ownership for %s observations", async state => {
  const f = fixture(); fireEvent.click(screen.getByRole("button", { name: "Run optional diagnostics" }));
  await waitFor(() => expect(f.read.mock.calls.some(([request]) => request.id === f.job.id)).toBe(true));
  const row = create(ResourceSchema, { ...f.job, revision: state === "regressive" ? 3n : 5n, id: state === "foreign" ? newRequestId() : f.job.id, kind: state === "wrong-kind" ? EntityKind.MACHINE : EntityKind.JOB, schemaVersion: state === "unsupported" ? 9 : 1, documentJson: state === "malformed" ? new TextEncoder().encode("{") : encode({ state: ["queued", "claimed", "uncertain"].includes(state) ? state : "succeeded" }) });
  await f.update(state === "missing" ? undefined : row);
  expect(screen.getByLabelText("Schedule name")).toHaveProperty("disabled", true);
  expect(f.discovery).toHaveBeenCalledOnce(); f.client.clear();
});

it("preserves later path and Verify edits and blocks failed or regressive status reads", async () => {
  const f = fixture(); fireEvent.click(screen.getByText("Set submitted path")); fireEvent.click(screen.getByRole("button", { name: "Run optional diagnostics" }));
  await waitFor(() => expect(f.read.mock.calls.some(([request]) => request.id === f.job.id)).toBe(true));
  fireEvent.click(screen.getByText("Set later path")); fireEvent.click(screen.getByText("Change Verify"));
  await f.update(create(ResourceSchema, { ...f.job, revision: 6n, documentJson: encode({ state: "succeeded" }) }));
  expect(screen.getByLabelText("Path draft").textContent).toBe("/later");
  expect(screen.getByLabelText("Schedule name")).toHaveProperty("disabled", true);
  fireEvent.click(screen.getByText("Discard path"));
  expect(screen.getByLabelText("Schedule name")).toHaveProperty("disabled", true);
  await f.update(create(ResourceSchema, { ...f.job, revision: 5n, documentJson: encode({ state: "succeeded" }) }));
  expect(screen.queryByRole("button", { name: "Finish inspection" })).toBeNull();
  await f.fail();
  expect(await screen.findByRole("button", { name: "Retry original status read" })).toBeTruthy();
  expect(f.discovery).toHaveBeenCalledOnce(); f.client.clear();
});
