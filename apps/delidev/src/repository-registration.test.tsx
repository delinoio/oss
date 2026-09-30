// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, ResourceSchema, ResourceService, WorkerService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { Settings, SettingsEntryDestination } from "./settings";
import { encode, document, type Document } from "./documents";
import { LocalWorkerState } from "./local-worker-controls";
import { validRepositoryInspection, selectedInspectionRemote } from "./repository-registration";

const metadata = { root: "/canonical/oss", name: "oss", remotes: ["origin", "upstream"], default_refs: { origin: "main" }, github_repositories: { origin: { owner: "delinoio", name: "oss" }, upstream: { owner: "another", name: "repo" } } };
function row(kind: EntityKind, value: Document): Resource { return create(ResourceSchema, { id: newRequestId(), kind, revision: 1n, schemaVersion: 1, documentJson: encode(value) }); }
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return { promise, resolve }; }
function fixture(output: Document = metadata) {
  const machine = row(EntityKind.MACHINE, { name: "Runner", last_seen: new Date().toISOString() });
  const resources = new Map<string, Resource>([[machine.id, machine]]);
  const jobs: Resource[] = [];
  const inspected = vi.fn(async (request: { machineId: string; path: string; preferredRemote: string; requestId: string }) => {
    const job = row(EntityKind.JOB, { state: "succeeded", machine_id: request.machineId, output }); resources.set(job.id, job); jobs.push(job); return { job };
  });
  const save = vi.fn(async (_request: { documentJson: Uint8Array }) => {
    const job = row(EntityKind.JOB, { state: "succeeded" }); resources.set(job.id, job); return { job };
  });
  const choose = vi.fn(async (): Promise<string | null> => "/alias/repo");
  const proof = vi.fn(async () => ({ machineId: machine.id, token: "A".repeat(43) }));
  const control = vi.fn(async () => ({ machine_id: machine.id, state: LocalWorkerState.Running, controller_active: true }));
  const transport = createRouterTransport(router => {
    router.service(WorkerService, { inspectRepository: inspected });
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(ResourceService, { listResources: request => ({ resources: [...resources.values()].filter(resource => resource.kind === request.filter?.kind) }), getResource: request => ({ resource: resources.get(request.id) }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  function Workspace() {
    const [visible, setVisible] = useState(true), [target, setTarget] = useState<SettingsEntryDestination | undefined>(SettingsEntryDestination.Repositories);
    return <><button onClick={() => { setTarget(undefined); setVisible(true); }}>Reopen settings</button><Settings visible={visible} close={() => setVisible(false)} entryDestination={target} readLocalWorker={proof} controlLocalWorker={control} chooseRepositoryFolder={choose} /></>;
  }
  const mount = () => render(<StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><Workspace /></QueryClientProvider></TransportProvider></StrictMode>);
  const add = async () => { fireEvent.click(await screen.findByRole("button", { name: "Add repository" })); };
  const chooseAndReview = async () => { await add(); fireEvent.click(screen.getByRole("button", { name: "Choose folder" })); await screen.findByRole("region", { name: "Repository detected" }); };
  return { machine, resources, jobs, inspected, save, choose, proof, control, client, mount, add, chooseAndReview };
}

it("registers the canonical checkout using folder selection and Add repository only", async () => {
  const f = fixture(); f.mount(); await f.chooseAndReview();
  expect(f.choose).toHaveBeenCalledTimes(1); expect(f.proof).toHaveBeenCalledTimes(1);
  expect(f.inspected.mock.calls[0][0]).toMatchObject({ machineId: f.machine.id, path: "/alias/repo", preferredRemote: "" });
  expect(screen.getByText("/canonical/oss")).toBeTruthy(); expect(screen.getByText("delinoio/oss")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Add inspected checkout" })).toBeNull();
  expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
  expect(screen.getByRole("button", { name: "Optional settings" }).getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(screen.getByRole("button", { name: "Add repository" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  const saved = JSON.parse(new TextDecoder().decode(f.save.mock.calls[0][0].documentJson));
  expect(saved).toMatchObject({ name: "oss", checkouts: [{ machine_id: f.machine.id, path: "/canonical/oss" }], base: {}, starting: {}, auto_fetch: true, github_owner: "delinoio", github_name: "oss" });
  expect(saved.integration_id).toBeUndefined(); expect(saved.remediation).toBeUndefined();
  expect(JSON.stringify(f.client.getQueryCache().getAll().map(query => query.queryKey))).not.toContain("A".repeat(43));
  await waitFor(() => expect(screen.queryByRole("region", { name: "Add repository" })).toBeNull());
});

it("preserves cancellation and resets repository-bound options on a successful replacement", async () => {
  const f = fixture(); f.mount(); await f.chooseAndReview();
  fireEvent.click(screen.getByRole("button", { name: "Optional settings" }));
  fireEvent.change(screen.getByRole("textbox", { name: "GitHub repository owner" }), { target: { value: "explicit" } });
  fireEvent.change(screen.getByRole("combobox", { name: "Starting reference type" }), { target: { value: "local-branch" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Starting reference name" }), { target: { value: "custom" } });
  f.choose.mockResolvedValueOnce(null);
  fireEvent.click(screen.getByRole("button", { name: "Change folder" }));
  await waitFor(() => expect(f.choose).toHaveBeenCalledTimes(2));
  expect((screen.getByRole("textbox", { name: "GitHub repository owner" }) as HTMLInputElement).value).toBe("explicit");
  expect(f.inspected).toHaveBeenCalledTimes(1);
  f.inspected.mockImplementationOnce(async request => { const job = row(EntityKind.JOB, { state: "succeeded", machine_id: request.machineId, output: { ...metadata, root: "/canonical/B", name: "B" } }); f.resources.set(job.id, job); return { job }; });
  fireEvent.click(screen.getByRole("button", { name: "Change folder" }));
  await screen.findByText("/canonical/B");
  fireEvent.click(screen.getByRole("button", { name: "Add repository" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  expect(JSON.parse(new TextDecoder().decode(f.save.mock.calls[0][0].documentJson))).toMatchObject({ name: "B", github_owner: "delinoio", starting: {} });
});

it("uses the selected remote's metadata without selecting an authentication profile", async () => {
  const f = fixture(); f.mount(); await f.chooseAndReview(); fireEvent.click(screen.getByRole("button", { name: "Optional settings" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Preferred Git remote" }), { target: { value: "upstream" } });
  expect(screen.getByText("another/repo")).toBeTruthy();
  expect((screen.getByRole("textbox", { name: "GitHub repository owner" }) as HTMLInputElement).value).toBe("another");
  expect((screen.getByRole("combobox", { name: "GitHub profile" }) as HTMLSelectElement).value).toBe("");
});

it.each([
  { root: "/unborn", name: "unborn", remotes: [], default_refs: {} },
  { root: "/legacy", name: "legacy", remotes: ["solo"], default_refs: {} },
  { root: "/ambiguous", name: "ambiguous", remotes: ["one", "two"], default_refs: {} },
])("allows missing defaults and legacy metadata for $name", async output => {
  const f = fixture(output); f.mount(); await f.chooseAndReview();
  expect(screen.getByText("Unavailable locally")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Add repository" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  const value = JSON.parse(new TextDecoder().decode(f.save.mock.calls[0][0].documentJson));
  expect(value.base).toEqual({}); expect(value.starting).toEqual({}); expect(value.github_owner).toBe("");
});

it("supports explicitly selected remote paths without native proof or normalization", async () => {
  const f = fixture(); f.mount(); await f.add(); fireEvent.click(screen.getByRole("button", { name: "Enter a path…" }));
  fireEvent.change(screen.getByRole("combobox", { name: "Computer" }), { target: { value: "remote" } });
  await screen.findByRole("option", { name: "Runner" });
  fireEvent.change(screen.getByRole("combobox", { name: "Execution Worker" }), { target: { value: f.machine.id } });
  const path = "C:\\Work Space\\repo";
  fireEvent.change(screen.getByRole("textbox", { name: "Absolute checkout path" }), { target: { value: path } });
  fireEvent.click(screen.getByRole("button", { name: "Inspect folder" }));
  await screen.findByRole("region", { name: "Repository detected" });
  expect(f.inspected.mock.calls[0][0].path).toBe(path); expect(f.proof).not.toHaveBeenCalled(); expect(f.choose).not.toHaveBeenCalled();
});

it.each(["picker", "proof", "inspection", "save"])("discards a late %s result after close without another request", async phase => {
  const f = fixture(); const pending = deferred<any>();
  if (phase === "picker") f.choose.mockReturnValueOnce(pending.promise);
  if (phase === "proof") f.proof.mockReturnValueOnce(pending.promise);
  if (phase === "inspection") f.inspected.mockReturnValueOnce(pending.promise);
  if (phase === "save") f.save.mockReturnValueOnce(pending.promise);
  f.mount(); await f.add(); fireEvent.click(screen.getByRole("button", { name: "Choose folder" }));
  if (phase !== "picker") await waitFor(() => expect(f.proof).toHaveBeenCalledTimes(1));
  if (phase === "inspection") await waitFor(() => expect(f.inspected).toHaveBeenCalledTimes(1));
  if (phase === "save") { await screen.findByRole("region", { name: "Repository detected" }); fireEvent.click(screen.getByRole("button", { name: "Add repository" })); await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1)); }
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Reopen settings" }));
  expect(await screen.findByRole("heading", { level: 1, name: "AI Subscription" })).toBeTruthy();
  const focus = window.document.activeElement;
  if (phase === "picker") pending.resolve("/old");
  if (phase === "proof") pending.resolve({ machineId: f.machine.id, token: "A".repeat(43) });
  if (phase === "inspection" || phase === "save") pending.resolve({ job: row(EntityKind.JOB, { machine_id: f.machine.id, state: "succeeded", output: metadata }) });
  await new Promise(resolve => setTimeout(resolve, 20));
  expect(window.document.activeElement).toBe(focus);
  if (phase === "picker" || phase === "proof") expect(f.inspected).not.toHaveBeenCalled();
  if (phase !== "save") expect(f.save).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Repositories" })); await f.add();
  expect(screen.queryByRole("region", { name: "Repository detected" })).toBeNull();
  expect(screen.queryByRole("button", { name: /Retry the same/ })).toBeNull();
});

it("retries uncertain inspection and save with the original exact requests", async () => {
  const f = fixture(); f.inspected.mockRejectedValueOnce(new ConnectError("lost", Code.Unavailable)); f.save.mockRejectedValueOnce(new ConnectError("lost", Code.Unavailable));
  f.mount(); await f.add(); fireEvent.click(screen.getByRole("button", { name: "Choose folder" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same inspection" }));
  await screen.findByRole("region", { name: "Repository detected" }); expect(f.inspected.mock.calls[0][0]).toEqual(f.inspected.mock.calls[1][0]);
  fireEvent.click(screen.getByRole("button", { name: "Add repository" }));
  const retry = await screen.findByRole("button", { name: "Retry the same repository save" });
  expect((screen.getByRole("button", { name: "Change folder" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(retry); await waitFor(() => expect(f.save).toHaveBeenCalledTimes(2)); expect(f.save.mock.calls[0][0]).toEqual(f.save.mock.calls[1][0]);
});

it.each([LocalWorkerState.NotStarted, LocalWorkerState.Exited])("retains the folder and reports known %s status without starting a Worker", async state => {
  const f = fixture(); f.control.mockResolvedValueOnce({ machine_id: f.machine.id, state, controller_active: false });
  f.mount(); await f.add(); fireEvent.click(screen.getByRole("button", { name: "Choose folder" }));
  await screen.findByText(/Worker (is stopped|exited)/);
  expect((screen.getByRole("textbox", { name: "Absolute checkout path" }) as HTMLInputElement).value).toBe("/alias/repo");
  expect(f.inspected).not.toHaveBeenCalled(); expect(f.control.mock.calls).toEqual([["status", undefined]]);
});

it("does not label unreadable proof as an absent registration", async () => {
  const f = fixture(); f.proof.mockRejectedValueOnce("permission-denied"); f.mount(); await f.add();
  fireEvent.click(screen.getByRole("button", { name: "Choose folder" })); await screen.findByText(/Access.*was denied/);
  expect(f.inspected).not.toHaveBeenCalled(); expect(screen.queryByText(/unregistered/i)).toBeNull();
});

it("validates all metadata before inferring a registration", () => {
  expect(validRepositoryInspection(metadata)).toBe(true);
  expect(validRepositoryInspection({ ...metadata, github_repositories: { foreign: { owner: "owner", name: "repo" } } })).toBe(false);
  expect(validRepositoryInspection({ ...metadata, github_repositories: { origin: { owner: "owner", name: "repo", url: "private" } } })).toBe(false);
  expect(selectedInspectionRemote({ remotes: ["one", "two"] }, "")).toBe("");
});

it("observes an accepted save after close without restoring or replaying the wizard", async () => {
  const f = fixture(); const pending = deferred<{ job?: Resource }>();
  const accepted = row(EntityKind.REPOSITORY, { name: "Accepted while closed", checkouts: [{ machine_id: f.machine.id, path: "/canonical/oss" }] });
  f.save.mockImplementationOnce(async () => { f.resources.set(accepted.id, accepted); return pending.promise as Promise<{ job: Resource }>; });
  f.mount(); await f.chooseAndReview();
  fireEvent.click(screen.getByRole("button", { name: "Add repository" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Reopen settings" }));
  await screen.findByRole("heading", { level: 1, name: "AI Subscription" });
  pending.resolve({ job: row(EntityKind.JOB, { state: "succeeded" }) });
  fireEvent.click(screen.getByRole("button", { name: "Repositories" }));
  await screen.findByRole("heading", { name: "Accepted while closed" });
  expect(screen.queryByRole("region", { name: "Repository detected" })).toBeNull();
  expect(f.save).toHaveBeenCalledTimes(1);
});
