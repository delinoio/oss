// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, ResourceService, ProviderService, WorkerService, newRequestId } from "@delinoio/delidev-api-client";
import { MutationIntents } from "./mutation";
import { RepositoryFields } from "./configuration-fields";
import { revealRepositoryInvalidControl } from "./repository-editor";
import { encode, type Document } from "./documents";

function fixture(data: Document, registration = false) {
  const machineId = newRequestId();
  const machine = create(ResourceSchema, { id: machineId, kind: EntityKind.MACHINE, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Build machine" }) });
  const inspect = vi.fn(async (_request: unknown) => ({ job: create(ResourceSchema, { id: newRequestId(), kind: EntityKind.JOB, schemaVersion: 1, revision: 1n, documentJson: encode({ machine_id: machineId, state: "succeeded", output: { root: "/canonical/checkout", remotes: [], default_refs: {} } }) }) }));
  const transport = createRouterTransport(router => {
    router.service(ResourceService, { listResources: request => ({ resources: request.filter?.kind === EntityKind.MACHINE ? [machine] : [] }), getResource: () => ({ resource: machine }) });
    router.service(ProviderService, { listProviderInventory: () => ({ entries: [] }) });
    router.service(WorkerService, { inspectRepository: inspect });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  let latest = data;
  function Editor() { const [draft, setDraft] = useState(data); return <form onInvalidCapture={revealRepositoryInvalidControl}><RepositoryFields data={draft} change={value => { latest = value; setDraft(value); }} active existing={!registration} registration={registration} /><button>Save fixture</button></form>; }
  const view = render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Editor /></MutationIntents></QueryClientProvider></TransportProvider>);
  return { ...view, inspect, machineId, draft: () => latest };
}
const base = { name: "oss", remote_url: "https://github.com/delinoio/oss.git", checkouts: [], base: {}, starting: {}, auto_fetch: true };
it("shows approved section order and mounted collapsed controls only for editing", () => {
  const value = fixture(base);
  expect([...value.container.querySelectorAll('section h4')].map(node => node.textContent)).toEqual(["Repository", "GitHub", "Checkouts"]);
  expect(screen.getByText("No connected checkouts")).toBeTruthy();
  expect([...value.container.querySelectorAll('details')].map(node => node.open)).toEqual([false, false]);
  expect(value.container.querySelector('input[value="oss"]')).toBeTruthy();
  expect(value.container.querySelectorAll('input')[0].value).toBe("oss");
  value.unmount();
  const registration = fixture(base, true);
  expect(registration.container.querySelector('.repository-edit-sections')).toBeNull();
  expect(screen.getByRole("button", { name: "Inspect checkout" })).toBeTruthy();
});
it("preserves every policy and draft through disclosure changes", () => {
  const value = fixture({ ...base, preferred_remote: "origin", base: { type: "local-branch", name: "main" }, starting: { type: "remote-branch", name: "release", remote: "origin" } });
  const advanced = screen.getByText("Advanced settings").closest('details')!;
  fireEvent.click(screen.getByText("Advanced settings"));
  fireEvent.change(screen.getByLabelText("Preferred Git remote"), { target: { value: "upstream" } });
  fireEvent.change(screen.getByLabelText("Starting reference name"), { target: { value: "next" } });
  fireEvent.click(screen.getByText("Advanced settings"));
  expect(advanced.open).toBe(false);
  expect(value.draft()).toMatchObject({ preferred_remote: "upstream", base: { type: "local-branch", name: "main" }, starting: { type: "remote-branch", name: "next", remote: "origin" }, auto_fetch: true });
  fireEvent.click(screen.getByText("Advanced settings"));
  expect((screen.getByLabelText("Starting reference name") as HTMLInputElement).value).toBe("next");
});
it("reveals and focuses the first invalid hidden reference before submission", async () => {
  const value = fixture({ ...base, base: { type: "local-branch", name: "" } });
  const input = value.container.querySelector('input[value=""]')!;
  // Target the exact invalid reference rather than a valid empty optional field.
  const invalid = screen.getByLabelText("Base reference name");
  fireEvent.invalid(invalid);
  expect(screen.getByText("Advanced settings").closest('details')!.open).toBe(true);
  await waitFor(() => expect(document.activeElement).toBe(invalid));
  expect(input).toBeTruthy();
});
it("retains checkout selection and path through collapse without inspection", async () => {
  const value = fixture(base);
  fireEvent.click(value.container.querySelector("summary")!);
  await screen.findByRole("option", { name: "Build machine" });
  fireEvent.change(screen.getByRole("combobox", { name: "Runner Device" }), { target: { value: value.machineId } });
  fireEvent.change(screen.getByLabelText("Absolute checkout path on this Worker"), { target: { value: "/alias/checkout" } });
  fireEvent.click(value.container.querySelector("summary")!);
  fireEvent.click(value.container.querySelector("summary")!);
  expect((screen.getByLabelText("Absolute checkout path on this Worker") as HTMLInputElement).value).toBe("/alias/checkout");
  expect((screen.getByRole("combobox", { name: "Runner Device" }) as HTMLSelectElement).value).toBe(value.machineId);
  expect(value.inspect).not.toHaveBeenCalled();
});

it("adds only the canonical inspected root and preserves the original inspection owner", async () => {
  const value = fixture(base);
  const summary = value.container.querySelector('summary')!;
  fireEvent.click(summary);
  await screen.findByRole("option", { name: "Build machine" });
  fireEvent.change(screen.getByRole("combobox", { name: "Runner Device" }), { target: { value: value.machineId } });
  fireEvent.change(screen.getByLabelText("Absolute checkout path on this Worker"), { target: { value: "/alias/checkout" } });
  fireEvent.click(screen.getByRole("button", { name: "Inspect checkout" }));
  await screen.findByRole("button", { name: "Add inspected checkout" });
  fireEvent.click(summary); fireEvent.click(summary);
  expect(value.inspect).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Add inspected checkout" }));
  expect(value.draft().checkouts).toEqual([{ machine_id: value.machineId, path: "/canonical/checkout" }]);
});
it("reveals an uncertain inspection and retries only its identical retained request", async () => {
  const value = fixture(base);
  value.inspect.mockRejectedValueOnce(new ConnectError("Unavailable", Code.Unavailable));
  const summary = value.container.querySelector('summary')!;
  fireEvent.click(summary);
  await screen.findByRole("option", { name: "Build machine" });
  fireEvent.change(screen.getByRole("combobox", { name: "Runner Device" }), { target: { value: value.machineId } });
  fireEvent.change(screen.getByLabelText("Absolute checkout path on this Worker"), { target: { value: "/alias/checkout" } });
  fireEvent.click(screen.getByRole("button", { name: "Inspect checkout" }));
  await screen.findByRole("button", { name: "Retry the same inspection" });
  expect(summary.closest('details')!.open).toBe(true);
  fireEvent.click(summary); fireEvent.click(summary);
  expect(value.inspect).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Retry the same inspection" }));
  await screen.findByRole("button", { name: "Add inspected checkout" });
  expect(value.inspect).toHaveBeenCalledTimes(2);
  expect(value.inspect.mock.calls[1][0]).toEqual(value.inspect.mock.calls[0][0]);
});
