// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, IntegrationService, SystemService, SystemCapability, EntityKind, ResourceSchema, ResourceService, WorkerService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { Settings, SettingsEntryDestination } from "./settings";
import { encode, document, type Document } from "./documents";
import { LocalWorkerState } from "./local-worker-controls";
import { validRepositoryInspection, selectedInspectionRemote } from "./repository-registration";

const metadata = { root: "/canonical/oss", name: "oss", remotes: ["origin", "upstream"], default_refs: { origin: "main" }, github_repositories: { origin: { owner: "delinoio", name: "oss" }, upstream: { owner: "another", name: "repo" } } };
function row(kind: EntityKind, value: Document): Resource { return create(ResourceSchema, { id: newRequestId(), kind, revision: 1n, schemaVersion: 1, documentJson: encode(value) }); }
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return { promise, resolve }; }
function fixture(output: Document = metadata, cloneCapabilities = false, remoteCapabilities = true, pagedProfiles = false, statusError?: ConnectError, profileOnFirstPage = false) {
  let currentStatusError = statusError;
  const machine = row(EntityKind.MACHINE, { name: "Runner", worker_capabilities: cloneCapabilities ? ["repository-clone-v1"] : [], last_seen: new Date().toISOString() });
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
  const getResource = vi.fn((request: { id: string }) => ({ resource: resources.get(request.id) }));
  const clone = vi.fn(async (request: { requestId: string; machineId: string; parentPath: string; url: string; directoryName: string; localWorkerToken: string; githubSelection?: { profileId: string; owner: string; name: string } }) => {
    const job = row(EntityKind.JOB, { type: "clone-repository", state: "queued", machine_id: request.machineId }); resources.set(job.id, job); jobs.push(job); return { job, requestId: request.requestId };
  });
  const repositories = vi.fn(async (request: { profileId: string; expectedRevision: bigint; page: number }) => ({ schemaVersion: 1, documentJson: encode({ profile_id: request.profileId, profile_revision: String(request.expectedRevision), generation_id: (document(resources.get(request.profileId)).connection as Document).generation_id, observed_at: new Date().toISOString(), page: request.page, page_size: 50, next_page: request.page === 1 ? 2 : 0, repositories: request.page === 1 ? [{ repository: { provider: "github.com", id: "123", node_id: "R_123", owner: "delinoio", name: "oss", private: true, default_branch: "main" }, archived: true, https_url: "https://github.com/delinoio/oss.git", ssh_url: "git@github.com:delinoio/oss.git" }] : [] }) }));
  const listResources = vi.fn((request: { filter?: { kind?: EntityKind; pageToken?: string } }) => {
    const matching = [...resources.values()].filter(resource => resource.kind === request.filter?.kind);
    if (pagedProfiles && request.filter?.kind === EntityKind.INTEGRATION) return request.filter.pageToken ? { resources: matching, nextPageToken: "" } : { resources: profileOnFirstPage ? matching : [], nextPageToken: "profile-next" };
    return { resources: matching };
  });
  const transport = createRouterTransport(router => {
    router.service(WorkerService, { inspectRepository: inspected, cloneRepository: clone });
    router.service(SystemService, { getStatus: () => { if (currentStatusError) throw currentStatusError; return ({ capabilities: [...(remoteCapabilities ? [SystemCapability.REMOTE_REPOSITORIES_V1] : []), ...(cloneCapabilities ? [SystemCapability.REPOSITORY_CLONE_V1, SystemCapability.GITHUB_REPOSITORY_PICKER_V1] : [])] }); } });
    router.service(IntegrationService, { listGitHubRepositories: repositories });
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(ResourceService, { listResources, getResource });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  function Workspace() {
    const [visible, setVisible] = useState(true), [target, setTarget] = useState<SettingsEntryDestination | undefined>(SettingsEntryDestination.Repositories);
    return <><button onClick={() => setVisible(false)}>Leave Settings</button><button onClick={() => { setTarget(undefined); setVisible(true); }}>Reopen settings</button><Settings visible={visible} entryDestination={target} readLocalWorker={proof} controlLocalWorker={control} chooseRepositoryFolder={choose} /></>;
  }
  const mount = () => render(<StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><Workspace /></QueryClientProvider></TransportProvider></StrictMode>);
  const add = async (local = true) => { fireEvent.click(await screen.findByRole("button", { name: "Add repository" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Git URL" }), { target: { value: "https://github.com/delinoio/oss.git" } });
    if (local) fireEvent.click(screen.getByRole("button", { name: "Connect a Local folder (optional)" }));
  };
  const chooseAndReview = async () => { await add(); fireEvent.click(screen.getByRole("button", { name: "Choose folder" })); await screen.findByRole("region", { name: "Repository detected" }); await waitFor(() => expect((within(screen.getByRole("dialog", { name: "Add repository" })).getByRole("button", { name: "Add repository" }) as HTMLButtonElement).disabled).toBe(false)); };
  return { machine, resources, jobs, clone, repositories, listResources, inspected, save, choose, proof, control, client, mount, add, chooseAndReview, getResource, clearStatusError: () => { currentStatusError = undefined; } };
}

it("retries a transient capability read without losing the registration draft", async () => {
  const f = fixture(metadata, false, true, false, new ConnectError("status unavailable", Code.Unavailable)); f.mount(); await f.add();
  const url = screen.getByRole("textbox", { name: "Git URL" }) as HTMLInputElement;
  expect(await screen.findByRole("button", { name: "Retry server capability check" })).toBeTruthy();
  f.clearStatusError(); fireEvent.click(screen.getByRole("button", { name: "Retry server capability check" }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry server capability check" })).toBeNull());
  expect(url.value).toBe("https://github.com/delinoio/oss.git");
});

it("keeps inspected-folder registration available on older servers", async () => {
  const f = fixture(metadata, false, false); f.mount();
  fireEvent.click(await screen.findByRole("button", { name: "Add repository" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Git URL" }), { target: { value: "" } });
  fireEvent.click(screen.getByRole("button", { name: "Connect a Local folder (optional)" }));
  fireEvent.click(screen.getByRole("button", { name: "Choose folder" }));
  await screen.findByRole("region", { name: "Repository detected" });
  const add = within(screen.getByRole("dialog", { name: "Add repository" })).getByRole("button", { name: "Add repository" }) as HTMLButtonElement;
  await waitFor(() => expect(add.disabled).toBe(false));
  fireEvent.click(add); await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  const saved = JSON.parse(new TextDecoder().decode(f.save.mock.calls[0][0].documentJson));
  expect(saved).toMatchObject({ name: "oss", checkouts: [{ machine_id: f.machine.id, path: "/canonical/oss" }] });
  expect(saved.remote_url).toBeUndefined();
});

it("registers the canonical checkout using folder selection and Add repository only", async () => {
  const f = fixture(); f.mount(); await f.chooseAndReview();
  expect(f.choose).toHaveBeenCalledTimes(1); expect(f.proof).toHaveBeenCalledTimes(1);
  expect(f.inspected.mock.calls[0][0]).toMatchObject({ machineId: f.machine.id, path: "/alias/repo", preferredRemote: "" });
  expect(screen.getByText("/canonical/oss")).toBeTruthy(); expect(screen.getByText("delinoio/oss")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Add inspected checkout" })).toBeNull();
  expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
  expect(screen.getByRole("button", { name: "Optional settings" }).getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(within(screen.getByRole("dialog", { name: "Add repository" })).getByRole("button", { name: "Add repository" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  const saved = JSON.parse(new TextDecoder().decode(f.save.mock.calls[0][0].documentJson));
  expect(saved).toMatchObject({ remote_url: "https://github.com/delinoio/oss.git", name: "oss", checkouts: [{ machine_id: f.machine.id, path: "/canonical/oss" }], base: {}, starting: {}, auto_fetch: true, github_owner: "delinoio", github_name: "oss" });
  expect(saved.integration_id).toBeUndefined(); expect(saved.remediation).toBeUndefined();
  expect(JSON.stringify(f.client.getQueryCache().getAll().map(query => query.queryKey))).not.toContain("A".repeat(43));
  await waitFor(() => expect(screen.queryByRole("region", { name: "Add repository" })).toBeNull());
});

it("preserves the remote source and reference options when replacing a Local folder", async () => {
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
  fireEvent.click(within(screen.getByRole("dialog", { name: "Add repository" })).getByRole("button", { name: "Add repository" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  expect(JSON.parse(new TextDecoder().decode(f.save.mock.calls[0][0].documentJson))).toMatchObject({ name: "oss", github_owner: "explicit", starting: { type: "local-branch", name: "custom" } });
});

it("does not offer atomic Clone after the repository draft is edited", async () => {
  const f = fixture(metadata, true); f.mount(); await f.chooseAndReview();
  const clone = screen.getByRole("button", { name: "Clone to this computer (optional)" }) as HTMLButtonElement;
  expect(clone.disabled).toBe(false);
  fireEvent.change(screen.getByRole("textbox", { name: "Repository name" }), { target: { value: "Edited name" } });
  expect(clone.disabled).toBe(true);
  expect(screen.getByText(/Clone mode is available before editing/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Optional settings" }));
  expect((screen.getByRole("button", { name: "Clone to this computer (optional)" }) as HTMLButtonElement).disabled).toBe(true);
});

it.each([false, true])("retains the confirmed primary checkout when additional checkout removal is %s", async removeAdditional => {
  const f = fixture();
  const other = row(EntityKind.MACHINE, { name: "Other runner", last_seen: new Date().toISOString() });
  f.resources.set(other.id, other); f.mount(); await f.chooseAndReview();
  fireEvent.click(screen.getByRole("button", { name: "Optional settings" }));
  await screen.findByRole("option", { name: "Other runner" });
  fireEvent.change(screen.getByRole("combobox", { name: "Runner Device" }), { target: { value: other.id } });
  fireEvent.change(screen.getByRole("textbox", { name: "Absolute checkout path on this Worker" }), { target: { value: "/alias/B" } });
  f.inspected.mockImplementationOnce(async request => {
    const job = row(EntityKind.JOB, { state: "succeeded", machine_id: request.machineId, output: { ...metadata, root: "/canonical/B", name: "B", github_repositories: { origin: { owner: "different", name: "B" } } } });
    f.resources.set(job.id, job); return { job };
  });
  fireEvent.click(screen.getByRole("button", { name: "Inspect checkout" }));
  fireEvent.click(await screen.findByRole("button", { name: "Add inspected checkout" }));
  const primary = within(screen.getByText(`${f.machine.id} · /canonical/oss`).closest("li")!);
  const secondary = within(screen.getByText(`${other.id} · /canonical/B`).closest("li")!);
  expect((primary.getByRole("button", { name: "Remove checkout" }) as HTMLButtonElement).disabled).toBe(true);
  expect(primary.getByText("Use Change folder to replace this checkout.")).toBeTruthy();
  fireEvent.click(primary.getByRole("button", { name: "Remove checkout" }));
  expect(screen.getByText(`${f.machine.id} · /canonical/oss`)).toBeTruthy();
  expect((secondary.getByRole("button", { name: "Remove checkout" }) as HTMLButtonElement).disabled).toBe(false);
  if (removeAdditional) fireEvent.click(secondary.getByRole("button", { name: "Remove checkout" }));
  expect(within(screen.getByRole("region", { name: "Repository detected" })).getByText("/canonical/oss")).toBeTruthy();
  fireEvent.click(within(screen.getByRole("dialog", { name: "Add repository" })).getByRole("button", { name: "Add repository" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  const saved = JSON.parse(new TextDecoder().decode(f.save.mock.calls[0][0].documentJson));
  expect(saved).toMatchObject({ name: "oss", github_owner: "delinoio", github_name: "oss" });
  expect(saved.checkouts).toEqual([{ machine_id: f.machine.id, path: "/canonical/oss" }, ...(removeAdditional ? [] : [{ machine_id: other.id, path: "/canonical/B" }])]);
});

it("keeps checkout removal available when editing an existing repository", async () => {
  const f = fixture();
  const repository = row(EntityKind.REPOSITORY, { name: "Existing", checkouts: [{ machine_id: f.machine.id, path: "/existing" }], base: {}, starting: {}, auto_fetch: true });
  f.resources.set(repository.id, repository); f.mount();
  fireEvent.click(await screen.findByRole("button", { name: "Edit Existing" }));
  const remove = screen.getByRole("button", { name: "Remove checkout" });
  expect((remove as HTMLButtonElement).disabled).toBe(false);
  expect(screen.queryByText("Use Change folder to replace this checkout.")).toBeNull();
  fireEvent.click(remove);
  expect(screen.queryByText(`${f.machine.id} · /existing`)).toBeNull();
});

it("does not replace the remote identity when a connected folder remote changes", async () => {
  const f = fixture(); f.mount(); await f.chooseAndReview(); fireEvent.click(screen.getByRole("button", { name: "Optional settings" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Preferred Git remote" }), { target: { value: "upstream" } });
  expect(screen.getByText("another/repo")).toBeTruthy();
  expect((screen.getByRole("textbox", { name: "GitHub repository owner" }) as HTMLInputElement).value).toBe("delinoio");
  expect((screen.getByRole("combobox", { name: "GitHub profile" }) as HTMLSelectElement).value).toBe("");
});

it.each([
  { root: "/unborn", name: "unborn", remotes: [], default_refs: {} },
  { root: "/legacy", name: "legacy", remotes: ["solo"], default_refs: {} },
  { root: "/ambiguous", name: "ambiguous", remotes: ["one", "two"], default_refs: {} },
])("allows missing defaults and legacy metadata for $name", async output => {
  const f = fixture(output); f.mount(); await f.chooseAndReview();
  expect(screen.getByText("Unavailable locally")).toBeTruthy();
  fireEvent.click(within(screen.getByRole("dialog", { name: "Add repository" })).getByRole("button", { name: "Add repository" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  const value = JSON.parse(new TextDecoder().decode(f.save.mock.calls[0][0].documentJson));
  expect(value.base).toEqual({}); expect(value.starting).toEqual({}); expect(value.github_owner).toBe("delinoio");
});

it("supports explicitly selected remote paths without native proof or normalization", async () => {
  const f = fixture(); f.mount(); await f.add(); fireEvent.click(screen.getByRole("button", { name: "Enter a path…" }));
  fireEvent.change(screen.getByRole("combobox", { name: "Computer" }), { target: { value: "remote" } });
  await screen.findByRole("option", { name: "Runner" });
  fireEvent.change(screen.getByRole("combobox", { name: "Runner Device" }), { target: { value: f.machine.id } });
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
  if (phase === "save") { await screen.findByRole("region", { name: "Repository detected" }); fireEvent.click(within(screen.getByRole("dialog", { name: "Add repository" })).getByRole("button", { name: "Add repository" })); await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1)); }
  fireEvent.click(screen.getByRole("button", { name: "Leave Settings" }));
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
  fireEvent.click(within(screen.getByRole("dialog", { name: "Add repository" })).getByRole("button", { name: "Add repository" }));
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

it("rejects an exit-uncertain Worker before submitting inspection", async () => {
  const f = fixture(); f.control.mockResolvedValueOnce({ machine_id: f.machine.id, state: LocalWorkerState.Uncertain, controller_active: false });
  f.mount(); await f.add(); fireEvent.click(screen.getByRole("button", { name: "Choose folder" }));
  await screen.findByText(/Worker exit is unconfirmed/);
  expect((screen.getByRole("textbox", { name: "Absolute checkout path" }) as HTMLInputElement).value).toBe("/alias/repo");
  expect(f.inspected).not.toHaveBeenCalled(); expect(f.control.mock.calls).toEqual([["status", undefined]]);
});

it("refreshes the selected Worker heartbeat while registration is active", async () => {
  const f = fixture(); f.mount(); await f.chooseAndReview();
  const readsBeforeRefresh = f.getResource.mock.calls.filter(([request]) => request.id === f.machine.id).length;
  await waitFor(() => expect(f.getResource.mock.calls.filter(([request]) => request.id === f.machine.id).length).toBeGreaterThan(readsBeforeRefresh), { timeout: 10000, interval: 100 });
}, 12000);

it.each([
  ["busy", /Another window is choosing a folder/],
  ["invalid-evidence", /selected folder cannot be used/],
  ["permission-denied", /Folder selection was denied/],
  ["unexpected", /Folder selection failed/],
])("reports picker %s before any Worker verification", async (error, guidance) => {
  const f = fixture(); f.choose.mockRejectedValueOnce(error); f.mount(); await f.add();
  fireEvent.click(screen.getByRole("button", { name: "Choose folder" }));
  await screen.findByText(guidance);
  expect(f.proof).not.toHaveBeenCalled(); expect(f.control).not.toHaveBeenCalled();
  expect(f.inspected).not.toHaveBeenCalled(); expect(f.save).not.toHaveBeenCalled();
  expect(screen.queryByText(/Worker could not be verified|selected folder is retained/)).toBeNull();
  expect(screen.queryByRole("textbox", { name: "Absolute checkout path" })).toBeNull();
  expect((screen.getByRole("button", { name: "Choose folder" }) as HTMLButtonElement).disabled).toBe(false);
});

it("preserves the current confirmation and options when a replacement picker fails", async () => {
  const f = fixture(); f.mount(); await f.chooseAndReview();
  fireEvent.click(screen.getByRole("button", { name: "Optional settings" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Retained name" } });
  f.choose.mockRejectedValueOnce("busy");
  fireEvent.click(screen.getByRole("button", { name: "Change folder" }));
  await screen.findByText(/Another window is choosing a folder/);
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("Retained name");
  expect(screen.getByText("/canonical/oss")).toBeTruthy();
  expect(f.proof).toHaveBeenCalledTimes(1); expect(f.control).toHaveBeenCalledTimes(1); expect(f.inspected).toHaveBeenCalledTimes(1);
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
  fireEvent.click(within(screen.getByRole("dialog", { name: "Add repository" })).getByRole("button", { name: "Add repository" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Leave Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Reopen settings" }));
  await screen.findByRole("heading", { level: 1, name: "AI Subscription" });
  pending.resolve({ job: row(EntityKind.JOB, { state: "succeeded" }) });
  fireEvent.click(screen.getByRole("button", { name: "Repositories" }));
  await screen.findByRole("heading", { name: "Accepted while closed" });
  expect(screen.queryByRole("region", { name: "Repository detected" })).toBeNull();
  expect(f.save).toHaveBeenCalledTimes(1);
});


it.each(["Cancel", "Close Add repository", "Escape"])("dismisses with %s, restores the opener and discards the draft", async action => {
  const f = fixture(); f.mount();
  const opener = await screen.findByRole("button", { name: "Add repository" });
  opener.focus(); await f.add();
  const dialog = screen.getByRole("dialog", { name: "Add repository" });
  expect(window.document.activeElement).toBe(within(dialog).getByRole("textbox", { name: "Git URL" }));
  expect(screen.getByRole("region", { name: "No repositories yet", hidden: true })).toBeTruthy();
  fireEvent.click(within(dialog).getByRole("button", { name: "Enter a path…" }));
  fireEvent.change(within(dialog).getByRole("textbox", { name: "Absolute checkout path" }), { target: { value: "/discard" } });
  if (action === "Escape") fireEvent(dialog, new Event("cancel", { bubbles: true, cancelable: true }));
  else fireEvent.click(within(dialog).getByRole("button", { name: action }));
  expect(screen.queryByRole("dialog", { name: "Add repository" })).toBeNull();
  await waitFor(() => expect(window.document.activeElement).toBe(opener));
  await f.add();
  expect(screen.queryByRole("textbox", { name: "Absolute checkout path" })).toBeNull();
  expect(f.inspected).not.toHaveBeenCalled(); expect(f.save).not.toHaveBeenCalled();
});

it("discards a pending inspection when the child dialog closes inside Settings", async () => {
  const f = fixture(); const pending = deferred<any>();
  f.inspected.mockReturnValueOnce(pending.promise); f.mount(); await f.add();
  fireEvent.click(screen.getByRole("button", { name: "Choose folder" }));
  await waitFor(() => expect(f.inspected).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await f.add();
  pending.resolve({ job: row(EntityKind.JOB, { machine_id: f.machine.id, state: "succeeded", output: metadata }) });
  await new Promise(resolve => setTimeout(resolve, 20));
  expect(screen.queryByRole("region", { name: "Repository detected" })).toBeNull();
  expect(screen.queryByRole("button", { name: /Retry the same/ })).toBeNull();
  expect(f.save).not.toHaveBeenCalled();
});

function cloneInputs() {
  const toggle = screen.getByRole("button", { name: "Clone to this computer (optional)" });
  if (toggle.getAttribute("aria-expanded") === "false") fireEvent.click(toggle);
  fireEvent.change(screen.getByRole("textbox", { name: "Git URL" }), { target: { value: "https://github.com/delinoio/oss.git" } });
  if (screen.getByRole("button", { name: "Clone to this computer (optional)" }).getAttribute("aria-expanded") === "false") fireEvent.click(screen.getByRole("button", { name: "Clone to this computer (optional)" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Clone to" }), { target: { value: "/parent" } });
}
function connectedProfile(f: ReturnType<typeof fixture>, pending = false) {
  const profile = row(EntityKind.INTEGRATION, { name: "Explicit profile", provider: "github.com", token_kind: "fine-grained", resource_owner: "delinoio", connection: { generation_id: newRequestId() }, ...(pending ? { pending: { operation: "replace-token" } } : {}) });
  f.resources.set(profile.id, profile); return profile;
}
it("clones with fresh local proof and no frontend registration after acceptance", async () => {
  const f = fixture(metadata, true); f.mount(); await f.add(); cloneInputs();
  expect(screen.queryByRole("textbox", { name: "Repository name" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Optional settings" })).toBeNull();
  expect((screen.getByRole("button", { name: "Add repository" }) as HTMLButtonElement).disabled).toBe(true);
  const button = screen.getByRole("button", { name: "Clone & add repository" }); await waitFor(() => expect(button.hasAttribute("disabled")).toBe(false));
  fireEvent.click(button); await screen.findByText("Repository clone accepted");
  expect(f.proof).toHaveBeenCalledTimes(1); expect(f.inspected).not.toHaveBeenCalled(); expect(f.save).not.toHaveBeenCalled();
  expect(f.clone.mock.calls[0][0]).toMatchObject({ machineId: f.machine.id, localWorkerToken: "A".repeat(43), url: "https://github.com/delinoio/oss.git", parentPath: "/parent", directoryName: "oss" });
  fireEvent.click(screen.getByRole("button", { name: "Close Add repository" }));
  f.resources.set(f.jobs[0].id, { ...f.jobs[0], revision: 2n, documentJson: encode({ type: "clone-repository", machine_id: f.machine.id, state: "succeeded" }) });
  await f.client.invalidateQueries(); expect(f.save).not.toHaveBeenCalled(); expect(screen.queryByRole("dialog", { name: "Add repository" })).toBeNull();
});
it("retains the exact clone request through an uncertain response", async () => {
  const f = fixture(metadata, true); f.clone.mockRejectedValueOnce(new ConnectError("Response lost", Code.Unavailable)); f.mount(); await f.add(); cloneInputs();
  const button = screen.getByRole("button", { name: "Clone & add repository" }); await waitFor(() => expect(button.hasAttribute("disabled")).toBe(false)); fireEvent.click(button);
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same clone request" })); await screen.findByText("Repository clone accepted");
  expect(f.clone.mock.calls[1][0]).toEqual(f.clone.mock.calls[0][0]); expect(f.proof).toHaveBeenCalledTimes(1); expect(f.save).not.toHaveBeenCalled();
});
it("rejects an older Worker before sending clone and retains local folder registration", async () => {
  const f = fixture(metadata, true); f.resources.set(f.machine.id, { ...f.machine, documentJson: encode({ name: "Older Worker", worker_capabilities: [] }) }); f.mount(); await f.add(); cloneInputs();
  const button = screen.getByRole("button", { name: "Clone & add repository" }); await waitFor(() => expect(button.hasAttribute("disabled")).toBe(false)); fireEvent.click(button);
  await screen.findByText(/Update and reconnect this computer's Worker/); expect(f.clone).not.toHaveBeenCalled(); expect(screen.getByRole("button", { name: "Choose folder" }).hasAttribute("disabled")).toBe(false);
});
it("shows only usable PAT profiles and requires explicit profile and repository selection", async () => {
  const f = fixture(metadata, true); const profile = connectedProfile(f); f.mount(); await f.add();
  fireEvent.click(await screen.findByRole("button", { name: "Choose from GitHub" })); expect(f.repositories).not.toHaveBeenCalled();
  fireEvent.change(screen.getByRole("combobox", { name: "GitHub profile" }), { target: { value: profile.id } });
  const repo = await screen.findByRole("button", { name: "delinoio/oss Private · Archived" }); fireEvent.click(repo);
  expect((screen.getByRole("textbox", { name: "Git URL" }) as HTMLInputElement).value).toBe("https://github.com/delinoio/oss.git");
  fireEvent.change(screen.getByRole("textbox", { name: "Git URL" }), { target: { value: "git@github.com:delinoio/oss.git" } });
  if (screen.getByRole("button", { name: "Clone to this computer (optional)" }).getAttribute("aria-expanded") === "false") fireEvent.click(screen.getByRole("button", { name: "Clone to this computer (optional)" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Clone to" }), { target: { value: "/parent" } }); fireEvent.click(screen.getByRole("button", { name: "Clone & add repository" }));
  await screen.findByText("Repository clone accepted"); expect(f.clone.mock.calls[0][0].githubSelection).toMatchObject({ profileId: profile.id, owner: "delinoio", name: "oss" });
});
it("keeps profile pagination explicit instead of scanning every page", async () => {
  const f = fixture(metadata, true, true, true); connectedProfile(f); f.mount(); await f.add();
  await screen.findByRole("button", { name: "Next profile page" });
  const initialProfileCalls = f.listResources.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.INTEGRATION);
  expect(initialProfileCalls.length).toBeGreaterThan(0);
  expect(initialProfileCalls.every(([request]) => !request.filter?.pageToken)).toBe(true);
  expect(screen.queryByRole("button", { name: "Choose from GitHub" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Next profile page" }));
  await screen.findByRole("button", { name: "Choose from GitHub" });
  expect(f.listResources.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.INTEGRATION && request.filter.pageToken === "profile-next").length).toBeGreaterThan(0);
});
it("clears a selected profile when outer profile paging changes page", async () => {
  const f = fixture(metadata, true, true, true, undefined, true), profile = connectedProfile(f); f.mount(); await f.add();
  fireEvent.click(await screen.findByRole("button", { name: "Choose from GitHub" }));
  fireEvent.change(screen.getByRole("combobox", { name: "GitHub profile" }), { target: { value: profile.id } });
  fireEvent.click(screen.getByRole("button", { name: "Back to Git URL" }));
  fireEvent.click(await screen.findByRole("button", { name: "Next profile page" }));
  await screen.findByRole("button", { name: "Choose from GitHub" });
  fireEvent.click(screen.getByRole("button", { name: "Choose from GitHub" }));
  expect((screen.getByRole("combobox", { name: "GitHub profile" }) as HTMLSelectElement).value).toBe("");
});
it("drops the selected profile association when the Git URL changes repositories", async () => {
  const f = fixture(metadata, true); const profile = connectedProfile(f); f.mount(); await f.add(); fireEvent.click(await screen.findByRole("button", { name: "Choose from GitHub" }));
  fireEvent.change(screen.getByRole("combobox", { name: "GitHub profile" }), { target: { value: profile.id } }); fireEvent.click(await screen.findByRole("button", { name: "delinoio/oss Private · Archived" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Git URL" }), { target: { value: "https://github.com/another/repo.git" } }); if (screen.getByRole("button", { name: "Clone to this computer (optional)" }).getAttribute("aria-expanded") === "false") fireEvent.click(screen.getByRole("button", { name: "Clone to this computer (optional)" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Clone to" }), { target: { value: "/parent" } }); fireEvent.click(screen.getByRole("button", { name: "Clone & add repository" }));
  await screen.findByText("Repository clone accepted"); expect(f.clone.mock.calls[0][0].githubSelection).toBeUndefined();
});
it("keeps repository pagination explicit including empty later pages and local filtering", async () => {
  const f = fixture(metadata, true); const profile = connectedProfile(f); f.mount(); await f.add(); fireEvent.click(await screen.findByRole("button", { name: "Choose from GitHub" }));
  fireEvent.change(screen.getByRole("combobox", { name: "GitHub profile" }), { target: { value: profile.id } }); await screen.findByRole("button", { name: "delinoio/oss Private · Archived" });
  fireEvent.change(screen.getByRole("textbox", { name: "Filter this page" }), { target: { value: "not-on-this-page" } }); await screen.findByText("No matches on this page."); expect(f.repositories).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Next" })); await screen.findByText("No repositories are accessible on this page."); expect(f.repositories.mock.calls[1][0].page).toBe(2);
  fireEvent.click(screen.getByRole("button", { name: "First" })); await screen.findByRole("button", { name: "delinoio/oss Private · Archived" });
  fireEvent.click(screen.getByRole("button", { name: "Refresh repositories" })); await waitFor(() => expect(f.repositories).toHaveBeenCalledTimes(3));
});
it("hides the GitHub choice for a pending profile change", async () => {
  const f = fixture(metadata, true); connectedProfile(f, true); f.mount(); await f.add(); await waitFor(() => expect(screen.queryByText("Checking connected GitHub profiles…")).toBeNull()); expect(screen.queryByRole("button", { name: "Choose from GitHub" })).toBeNull();
});
it("discards a late GitHub page on close without selecting or cloning", async () => {
  const f = fixture(metadata, true); const profile = connectedProfile(f), response = deferred<Awaited<ReturnType<typeof f.repositories>>>();
  f.repositories.mockImplementationOnce(() => response.promise); f.mount(); await f.add(); fireEvent.click(await screen.findByRole("button", { name: "Choose from GitHub" })); fireEvent.change(screen.getByRole("combobox", { name: "GitHub profile" }), { target: { value: profile.id } }); await waitFor(() => expect(f.repositories).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Close Add repository" })); response.resolve({ schemaVersion: 1, documentJson: encode({}) }); await Promise.resolve(); expect(screen.queryByRole("dialog", { name: "Add repository" })).toBeNull(); expect(f.clone).not.toHaveBeenCalled();
});
it("shows the published checkout when clone registration fails", async () => {
  const f = fixture(metadata, true); f.clone.mockImplementationOnce(async request => { const job = row(EntityKind.JOB, { type: "clone-repository", machine_id: request.machineId, state: "failed", output: { inspection: { root: "/parent/oss" } }, problem: { message: "Profile changed." } }); f.resources.set(job.id, job); return { job, requestId: request.requestId }; }); f.mount(); await f.add(); cloneInputs();
  const button = screen.getByRole("button", { name: "Clone & add repository" }); await waitFor(() => expect(button.hasAttribute("disabled")).toBe(false)); fireEvent.click(button); await screen.findByText(/Checkout preserved at \/parent\/oss/); expect(f.save).not.toHaveBeenCalled();
});


it("saves the URL without any local folder or Worker proof", async () => {
  const f = fixture(); f.resources.clear(); f.mount(); await f.add(false);
  fireEvent.change(screen.getByRole("textbox", { name: "Repository name" }), { target: { value: "Remote only" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Git URL" }), { target: { value: "git@example.com:team/code.git" } });
  expect((screen.getByRole("textbox", { name: "Repository name" }) as HTMLInputElement).value).toBe("Remote only");
  const add = within(screen.getByRole("dialog", { name: "Add repository" })).getByRole("button", { name: "Add repository" });
  await waitFor(() => expect(add.hasAttribute("disabled")).toBe(false)); fireEvent.click(add);
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  expect(JSON.parse(new TextDecoder().decode(f.save.mock.calls[0][0].documentJson))).toMatchObject({ remote_url: "git@example.com:team/code.git", name: "Remote only", checkouts: [] });
  for (const operation of [f.choose, f.proof, f.control, f.inspected, f.clone]) expect(operation).not.toHaveBeenCalled();
  expect(f.getResource.mock.calls.every(([request]) => f.resources.get(request.id)?.kind === EntityKind.JOB)).toBe(true);
});

it("does not send URL registration to a server without capability 37", async () => {
  const f = fixture(metadata, false, false); f.mount(); await f.add(false);
  const add = within(screen.getByRole("dialog", { name: "Add repository" })).getByRole("button", { name: "Add repository" });
  expect(add.hasAttribute("disabled")).toBe(true); fireEvent.click(add);
  await screen.findByText("Update the selected server to add repositories by URL.");
  expect(f.save).not.toHaveBeenCalled(); expect(f.proof).not.toHaveBeenCalled();
});
