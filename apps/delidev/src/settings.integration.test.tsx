import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, readFile, realpath, rm } from "node:fs/promises";
import { createServer, type Server } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import processInfo from "node:process";
import { promisify } from "node:util";
import { webcrypto } from "node:crypto";
import { createClient, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { BudgetState, ProviderInventoryCapability, ProviderPresetId, ProviderService, SessionService, UsageService, ConfigurationService, EntityKind, ResourceService, ScheduleService, SystemService, createDeliDevTransport, newRequestId } from "@delinoio/delidev-api-client";
import { SessionBudget } from "./session-budget";
import { Usage } from "./usage";
import { Schedules } from "./schedules";
import { NewSession } from "./new-session";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { document, encode } from "./documents";

let directory: string, transport: Transport, providerOrigin: string, binary: string, scope: string;
let process: ChildProcess | undefined, worker: ChildProcess | undefined, provider: Server | undefined;
const pause = () => new Promise((resolve) => setTimeout(resolve, 25));
afterEach(() => vi.unstubAllGlobals());
beforeAll(async () => {
  directory = await mkdtemp(join(tmpdir(), "delidev-settings-"));
  binary = join(directory, processInfo.platform === "win32" ? "delidev.exe" : "delidev");
  await promisify(execFile)("go", ["build", "-o", binary, "./cmds/delidev-cli"], { cwd: resolve(processInfo.cwd(), "../.."), timeout: 120000 });
  scope = join(directory, "server");
  process = spawn(binary, ["--data-dir", scope, "server", "run", "--listen", "127.0.0.1:0"], { stdio: "ignore" });
  const until = Date.now() + 15000;
  let ready = false;
  while (Date.now() < until) {
    if (process.exitCode !== null) throw new Error("Temporary server exited before readiness");
    try {
      const origin = JSON.parse(await readFile(join(scope, "server.json"), "utf8")).url;
      const token = JSON.parse(await readFile(join(scope, "owner.json"), "utf8")).token;
      transport = createDeliDevTransport({ origin, getToken: () => token });
      await createClient(SystemService, transport).getStatus({}, { timeoutMs: 1000 });
      ready = true;
      break;
    } catch { await pause(); }
  }
  if (!ready) throw new Error("Temporary server readiness timed out");
  // Only this owned non-inference HTTP endpoint is used. No installed model
  // server, user credential store, upstream account or harness is accessed.
  provider = createServer((request, response) => {
    if (request.method !== "GET" || request.url !== "/v1/models") { response.writeHead(404); response.end(); return; }
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(JSON.stringify({ object: "list", data: [{ id: "fixture-model", object: "model" }] }));
  });
  await new Promise<void>((resolve) => provider!.listen(0, "127.0.0.1", resolve));
  const address = provider.address();
  if (!address || typeof address === "string") throw new Error("No fixture listener");
  providerOrigin = `http://127.0.0.1:${address.port}/v1`;
}, 150000);
async function stopChild(child?: ChildProcess) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill("SIGTERM");
  const until = Date.now() + 5000;
  while (child.exitCode === null && child.signalCode === null && Date.now() < until) await pause();
  if (child.exitCode === null && child.signalCode === null) {
    const exit = new Promise<void>((resolve) => child.once("exit", () => resolve()));
    child.kill("SIGKILL"); await exit;
  }
}
function runCLI(args: string[], input?: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const child = execFile(binary, ["--data-dir", scope, ...args], { timeout: 30000, maxBuffer: 1 << 20 }, (error, stdout) => error ? reject(new Error("Owned fixture CLI failed")) : resolve(stdout));
    child.stdin!.end(input);
  });
}

it("starts with hosted presets on without accounts or models and retains identity across off/on", async () => {
  const providers = createClient(ProviderService, transport);
  const initial = await providers.listProviderInventory({ pageSize: 50 });
  expect(initial.capabilities).toEqual(expect.arrayContaining([ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER]));
  expect(initial.entries.filter((entry) => entry.presetId !== ProviderPresetId.UNSPECIFIED)).toHaveLength(9);
  expect(initial.entries.filter((entry) => [ProviderPresetId.OLLAMA, ProviderPresetId.LM_STUDIO, ProviderPresetId.VLLM].includes(entry.presetId)).every((entry) => !entry.enabled && !entry.providerId && entry.accountCountsAvailable)).toBe(true);
  expect(initial.entries.filter((entry) => ![ProviderPresetId.OLLAMA, ProviderPresetId.LM_STUDIO, ProviderPresetId.VLLM].includes(entry.presetId)).every((entry) => entry.enabled && !!entry.providerId && entry.accountCountsAvailable && entry.totalAccounts === 0n && entry.connectedAccounts === 0n)).toBe(true);

  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  const turnOff = await screen.findByRole("switch", { name: "Turn off OpenAI" });
  await waitFor(() => expect((turnOff as HTMLButtonElement).disabled).toBe(false));
  expect(screen.queryByText("Account required")).toBeNull();
  let inventory = await providers.listProviderInventory({ pageSize: 50 });
  let saved = inventory.entries.find((entry) => entry.presetId === ProviderPresetId.OPENAI)!;
  expect(saved.enabled).toBe(true);
  expect(saved.providerId).not.toBe("");
  expect(saved.totalAccounts).toBe(0n);
  expect((await createClient(ResourceService, transport).listResources({ filter: { kind: EntityKind.ACCOUNT } })).resources).toHaveLength(0);
  expect((await providers.searchModels({ pageSize: 50 })).models).toHaveLength(0);

  fireEvent.click(turnOff);
  const turnOnAgain = await screen.findByRole("switch", { name: "Turn on OpenAI" });
  await waitFor(() => expect((turnOnAgain as HTMLButtonElement).disabled).toBe(false));
  inventory = await providers.listProviderInventory({ pageSize: 50 });
  saved = inventory.entries.find((entry) => entry.presetId === ProviderPresetId.OPENAI)!;
  expect(saved.enabled).toBe(false);
  const retainedID = saved.providerId;
  fireEvent.click(turnOnAgain);
  await screen.findByRole("switch", { name: "Turn off OpenAI" });
  saved = (await providers.listProviderInventory({ pageSize: 50 })).entries.find((entry) => entry.presetId === ProviderPresetId.OPENAI)!;
  expect(saved.providerId).toBe(retainedID);
  expect(saved.enabled).toBe(true);
}, 30000);

afterAll(async () => {
  await stopChild(worker);
  if (process && process.exitCode === null) {
    try { await createClient(SystemService, transport).stopServer({ requestId: newRequestId() }, { timeoutMs: 2000 }); } catch { /* Cleanup still owns exactly this child. */ }
    const until = Date.now() + 3000;
    while (process.exitCode === null && Date.now() < until) await pause();
    if (process.exitCode === null) {
      const exit = new Promise<void>((resolve) => process!.once("exit", () => resolve()));
      process.kill("SIGKILL"); await exit;
    }
  }
  if (provider) await new Promise<void>((resolve) => provider!.close(() => resolve()));
  if (directory) await rm(directory, { recursive: true, force: true });
});

it("configures a real Go server through the settings forms and explicitly validates a private keyless provider", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  const change = (name: string, value: string) => fireEvent.change(screen.getByLabelText(name), { target: { value } });
  const create = await screen.findByRole("button", { name: "Custom provider" });
  await waitFor(() => expect((create as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(create);
  change("Name", "Owned local API"); change("API base URL", providerOrigin); change("API protocol", "openai-chat"); change("Authentication", "keyless");
  fireEvent.click(screen.getByRole("checkbox", { name: "Discover models automatically for connected accounts" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Provider" }));
  await screen.findByRole("heading", { name: "Owned local API" });
  fireEvent.click(screen.getByRole("button", { name: "API Accounts" }));
  fireEvent.click(screen.getByRole("button", { name: "Add API account" }));
  fireEvent.click(await screen.findByRole("radio", { name: "Owned local API" }));
  fireEvent.click(screen.getByRole("button", { name: "Continue to account" }));
  change("Account name", "Owned keyless account");
  fireEvent.click(screen.getByRole("button", { name: "Add and connect" }));
  await screen.findByRole("heading", { name: "Owned keyless account" });
  const manageAccount = await screen.findByRole("button", { name: "Manage account" });
  await waitFor(() => expect((manageAccount as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(manageAccount);
  await screen.findByText("Health: unverified · Credential connected");
  fireEvent.click(screen.getByRole("button", { name: "Validate account" }));
  await screen.findByText("Health: ready · Credential connected");
  fireEvent.click(screen.getByRole("button", { name: "Back to accounts" }));
  fireEvent.click(screen.getByRole("button", { name: "Models" }));
  const newModel = await screen.findByRole("button", { name: "New Model" });
  await waitFor(() => expect((newModel as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(newModel);
  await screen.findByLabelText("Native model ID");
  const option = await screen.findByRole("option", { name: "Owned local API" }) as HTMLOptionElement;
  change("Provider", (option as HTMLOptionElement).value); change("Native model ID", "fixture-model"); change("Display name", "Owned model");
  fireEvent.click(screen.getByRole("checkbox", { name: "codex" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Model" }));
  await screen.findByRole("heading", { name: "Owned model" });
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  fireEvent.click(screen.getByRole("button", { name: "New Agent Worker" }));
  change("Name", "Configured agent");
  change("Model", (await screen.findByRole("option", { name: "Owned model" }) as HTMLOptionElement).value);
  change("Add AI account", (await screen.findByRole("option", { name: "Owned keyless account · ready" }) as HTMLOptionElement).value);
  fireEvent.click(screen.getAllByRole("button", { name: "Add selected" })[0]);
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await screen.findByRole("heading", { name: "Configured agent" });
  const agents = await createClient(ResourceService, transport).listResources({ filter: { kind: EntityKind.AGENT } });
  expect(agents.resources).toHaveLength(1);
  expect(document(agents.resources[0])).toMatchObject({ name: "Configured agent", harness: "codex", accounts: [{ weight: 1 }], options: { permission: "default" } });
}, 30000);

it("inspects and saves a real owned Git checkout through a separate Go Worker before creating a project", async () => {
  const pairing = JSON.parse(await runCLI(["device", "create-pairing", "--type", "worker", "--name", "Owned Git Worker"]));
  const grant = await readFile(pairing.result.code_file, "utf8");
  const workerRoot = join(directory, "worker");
  await runCLI(["worker", "pair", "--worker-dir", workerRoot, "--name", "Owned Git Worker", "--code-stdin"], grant);
  worker = spawn(binary, ["--data-dir", scope, "worker", "start", "--worker-dir", workerRoot], { stdio: ["ignore", "pipe", "ignore"] });
  await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("Owned Worker readiness timed out")), 10000);
    let output = "";
    const fail = () => { clearTimeout(timer); reject(new Error("Owned Worker exited before readiness")); };
    worker!.once("error", fail); worker!.once("exit", fail);
    worker!.stdout!.on("data", (chunk: Buffer) => {
      output += chunk.toString();
      if (output.length > 16384) { fail(); return; }
      if (output.includes('"status":"ready"')) { clearTimeout(timer); worker!.removeListener("exit", fail); worker!.removeListener("error", fail); resolve(); }
    });
  });
  const checkout = join(directory, "checkout");
  await promisify(execFile)("git", ["init", "--quiet", checkout], { timeout: 10000 });
  const canonical = await realpath(checkout);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  const change = (name: string, value: string) => fireEvent.change(screen.getByLabelText(name), { target: { value } });
  fireEvent.click(screen.getByRole("button", { name: "Repositories" }));
  fireEvent.click(screen.getByRole("button", { name: "New Repository" }));
  change("Name", "Owned repository");
  change("Execution Worker", (await screen.findByRole("option", { name: "Owned Git Worker" }) as HTMLOptionElement).value);
  change("Absolute checkout path on this Worker", checkout);
  fireEvent.click(screen.getByRole("button", { name: "Inspect checkout" }));
  fireEvent.click(await screen.findByRole("button", { name: "Add inspected checkout" }, { timeout: 15000 }));
  fireEvent.click(screen.getByRole("button", { name: "Save Repository" }));
  await screen.findByText("Repository save accepted");
  fireEvent.click(await screen.findByRole("button", { name: "Done" }, { timeout: 15000 }));
  await screen.findByRole("heading", { name: "Owned repository" });
  const repositories = await createClient(ResourceService, transport).listResources({ filter: { kind: EntityKind.REPOSITORY } });
  expect(document(repositories.resources[0]).checkouts).toEqual([expect.objectContaining({ path: canonical })]);
  fireEvent.click(screen.getByRole("button", { name: "Projects" }));
  fireEvent.click(screen.getByRole("button", { name: "New Project" }));
  change("Name", "Owned project");
  change("Add Repository", (await screen.findByRole("option", { name: "Owned repository" }) as HTMLOptionElement).value);
  fireEvent.click(screen.getByRole("button", { name: "Add selected" }));
  change("Primary repository", repositories.resources[0].id);
  fireEvent.click(screen.getByRole("checkbox", { name: "Restrict ai accounts" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  await screen.findByRole("heading", { name: "Owned project" });
  const projects = await createClient(ResourceService, transport).listResources({ filter: { kind: EntityKind.PROJECT } });
  expect(document(projects.resources[0])).toMatchObject({ primary_repository: repositories.resources[0].id, accounts: { configured: true, ids: [] } });
  fireEvent.click(screen.getByRole("button", { name: "Execution Workers" }));
  fireEvent.click(await screen.findByRole("button", { name: "Inspect installed harnesses" }));
  fireEvent.click(screen.getByRole("button", { name: "Edit executable paths" }));
  for (const harness of ["codex", "claude-code", "opencode", "grok-build"]) change(`${harness} executable path`, join(directory, `missing-${harness}`));
  fireEvent.click(screen.getByRole("button", { name: "Check installed harnesses" }));
  fireEvent.click(await screen.findByRole("button", { name: "Finish inspection" }, { timeout: 15000 }));
  await waitFor(() => expect(screen.getAllByText("missing · Version: Unknown")).toHaveLength(4));
  // An account-less Agent is valid configuration but cannot infer readiness or
  // launch a harness. The real Worker has only explicit missing executables.
  const configurations = createClient(ConfigurationService, transport);
  const save = async (kind: EntityKind, value: Record<string, unknown>) => (await configurations.saveConfiguration({ kind, mutation: { requestId: newRequestId() }, schemaVersion: 1, documentJson: encode(value) })).resource!;
  const providerConfig = await save(EntityKind.PROVIDER, { name: "Schedule fixture provider", endpoint: providerOrigin, protocol: "openai-chat", authentication: "keyless", discovery: false });
  const model = await save(EntityKind.MODEL, { name: "Schedule fixture model", provider_id: providerConfig.id, native_id: "fixture-model", harnesses: ["codex"], manual: true, metadata_source: "unknown" });
  const accountlessAgent = await save(EntityKind.AGENT, { name: "Accountless schedule agent", harness: "codex", model_id: model.id, accounts: [], templates: [], options: { permission: "default" } });
  cleanup();
  const scheduleClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const readLocalWorker = async () => {
    const credential = JSON.parse(await readFile(join(workerRoot, "device.json"), "utf8"));
    return { machineId: credential.machine_id as string, token: credential.token as string };
  };
  let createdSessionId = "";
  render(<TransportProvider transport={transport}><QueryClientProvider client={scheduleClient}><MutationIntents><NewSession active ownsActivation activation={1} back={() => {}} openSettings={() => {}} open={(id) => { createdSessionId = id; }} created={() => {}} readLocalWorker={readLocalWorker} /></MutationIntents></QueryClientProvider></TransportProvider>);
  const newSession = within(window.document.querySelector(".new-session-page")!);
  const changeNewSession = (name: string, value: string) => fireEvent.change(newSession.getByLabelText(name), { target: { value } });
  changeNewSession("Project", (await within(newSession.getByLabelText("Project")).findByRole("option", { name: "Owned project" }) as HTMLOptionElement).value);
  fireEvent.click(newSession.getByRole("button", { name: "Options" }));
  fireEvent.click(newSession.getByRole("button", { name: "Use this computer's Local checkouts" }));
  await waitFor(() => expect((newSession.getByLabelText("Execution Worker") as HTMLSelectElement).disabled).toBe(true));
  changeNewSession("Agent Worker", (await newSession.findByRole("option", { name: "Accountless schedule agent" }) as HTMLOptionElement).value);
  changeNewSession("First message", "Local proof fixture without inference");
  fireEvent.click(newSession.getByText("Optional estimated-cost budget"));
  fireEvent.click(newSession.getByRole("checkbox", { name: "Enable estimated-cost budget" }));
  changeNewSession("Budget currency", "USD"); changeNewSession("Estimated-cost threshold", "0.000000000000001");
  fireEvent.click(newSession.getByRole("button", { name: "Create session" }));
  await waitFor(async () => {
    expect(createdSessionId).not.toBe("");
    const sessions = await createClient(ResourceService, transport).listResources({ filter: { kind: EntityKind.SESSION } });
    const created = sessions.resources.find((row) => row.id === createdSessionId);
    expect(created && document(created)).toMatchObject({ workspace: "local", name: "New session", estimated_cost_budget: { currency: "USD", threshold: "0.000000000000001" } });
  });
  cleanup();
  const budgetSession = (await createClient(ResourceService, transport).getResource({ kind: EntityKind.SESSION, id: createdSessionId })).resource!;
  expect(document(budgetSession).estimated_cost_budget).toEqual({ currency: "USD", threshold: "0.000000000000001" });
  const budgetClient = createClient(SessionService, transport);
  expect((await budgetClient.getSessionBudget({ sessionId: budgetSession.id })).view).toMatchObject({ state: BudgetState.ALLOW_INCOMPLETE, selectedCurrency: { knownAmount: "" } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={scheduleClient}><MutationIntents><SessionBudget resource={budgetSession} changed={() => {}} blocked={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  await screen.findByText(/Known lifetime subtotal: Unavailable/);
  fireEvent.click(screen.getByRole("button", { name: "Edit session budget" }));
  change("Estimated-cost threshold", "12.345678901234567");
  fireEvent.click(screen.getByRole("button", { name: "Save session budget" }));
  await screen.findByText(/Threshold: USD 12.345678901234567/);
  expect((await budgetClient.getSessionBudget({ sessionId: budgetSession.id })).view?.budget?.threshold).toBe("12.345678901234567");
  fireEvent.click(screen.getByRole("button", { name: "Edit session budget" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Enable estimated-cost budget" }));
  fireEvent.click(screen.getByRole("button", { name: "Save session budget" }));
  await screen.findByText("No estimated-cost budget is configured.");
  expect((await budgetClient.getSessionBudget({ sessionId: budgetSession.id })).view?.state).toBe(BudgetState.DISABLED);
  cleanup();
  render(<TransportProvider transport={transport}><QueryClientProvider client={scheduleClient}><MutationIntents><Schedules active open={() => {}} readLocalWorker={readLocalWorker} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "New schedule" }));
  change("Schedule name", "Owned schedule");
  change("Project", (await within(screen.getByLabelText("Project")).findByRole("option", { name: "Owned project" }) as HTMLOptionElement).value);
  change("Agent Worker", (await screen.findByRole("option", { name: "Accountless schedule agent" }) as HTMLOptionElement).value);
  change("Execution Worker", (await screen.findByRole("option", { name: "Owned Git Worker" }) as HTMLOptionElement).value);
  fireEvent.click(screen.getByRole("button", { name: "Use this computer's Local checkouts" }));
  await waitFor(() => expect((screen.getByLabelText("Execution Worker") as HTMLSelectElement).disabled).toBe(true));
  change("Scheduled prompt", "Private schedule fixture prompt"); change("Cron expression", "0 0 1 1 *"); change("IANA timezone", "Asia/Seoul");
  fireEvent.click(screen.getByRole("button", { name: "Save schedule" }));
  fireEvent.click(await screen.findByRole("button", { name: "Resume future runs" }));
  fireEvent.click(await screen.findByRole("button", { name: "Pause future runs" }));
  await screen.findByRole("button", { name: "Resume future runs" });
  fireEvent.click(screen.getByRole("button", { name: "Run now" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm Run now" }));
  await screen.findByText("Run now accepted");
  const schedules = await createClient(ScheduleService, transport).listSchedules({});
  const scheduleId = schedules.schedules[0].id;
  expect(document(schedules.schedules[0])).toMatchObject({ definition: { workspace: "local" }, local_origin: { machine_id: (await readLocalWorker()).machineId } });
  expect((await createClient(ScheduleService, transport).listScheduleOccurrences({ scheduleId })).occurrences).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Delete schedule" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm schedule deletion" }));
  await screen.findByText("Schedule configuration deleted. Retained occurrences and sessions remain.");
  expect((await createClient(ScheduleService, transport).listScheduleOccurrences({ scheduleId })).occurrences).toHaveLength(1);
  cleanup();
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Projects" }));
  fireEvent.click(await screen.findByRole("button", { name: "Delete Owned project" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm configuration deletion" }));
  await waitFor(() => expect(screen.queryByRole("heading", { name: "Delete Owned project?" })).toBeNull());
  expect((await createClient(ResourceService, transport).listResources({ filter: { kind: EntityKind.PROJECT } })).resources).toHaveLength(0);
}, 60000);

it("revokes a real paired client through settings and reads bounded server diagnostics", async () => {
  const clientRoot = join(directory, "disposable-client");
  await runCLI(["device", "pair-local", "--device-dir", clientRoot]);
  const credential = JSON.parse(await readFile(join(clientRoot, "device.json"), "utf8"));
  const paired = createDeliDevTransport({ origin: credential.endpoint as string, getToken: () => credential.token as string });
  await createClient(SystemService, paired).getStatus({});
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Paired devices" }));
  fireEvent.click(await screen.findByRole("button", { name: "Revoke DeliDev desktop" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Confirm device revocation" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Confirm device revocation" }));
  await screen.findByText("Authorization revoked for DeliDev desktop.");
  await expect(createClient(SystemService, paired).getStatus({})).rejects.toMatchObject({ code: 16 });
  const retained = await createClient(ResourceService, transport).getResource({ kind: EntityKind.DEVICE, id: credential.device_id as string });
  expect(document(retained.resource).revoked).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Return to devices" }));
  fireEvent.click(screen.getByRole("button", { name: "Diagnostics" }));
  await screen.findByText("Server owner credential loaded");
  expect(screen.getByText("Read succeeded")).toBeTruthy();
  expect(screen.getByText("Not performed")).toBeTruthy();
  expect(screen.getByRole("region", { name: "Storage diagnostics" })).toBeTruthy();
  expect(screen.getByText("Logical database size").nextElementSibling?.textContent).toMatch(/^[0-9,]+ bytes$/);
  expect(screen.getByRole("region", { name: "Worker diagnostics" })).toBeTruthy();
  expect(screen.getByRole("region", { name: "Protected credential diagnostics" })).toBeTruthy();
  expect(screen.queryByText(/legacy report/)).toBeNull();
}, 15000);

for (const kind of ["client", "worker"] as const) it(`issues a real single-use ${kind} grant from Settings and observes consumption`, async () => {
  vi.stubGlobal("crypto", webcrypto);
  const endpoint = JSON.parse(await readFile(join(scope, "server.json"), "utf8"));
  const authority = { endpoint: endpoint.url as string, serverId: (await createClient(SystemService, transport).getStatus({})).serverId };
  let delayedRead = false;
  let releaseRead!: () => void;
  const readGate = new Promise<void>((resolve) => { releaseRead = resolve; });
  // Exercise the real issuance and verification path beyond Testing Library's
  // one-second default. Shared CI load can delay issuance or its fresh read; a grant
  // must still stay private until its first fresh observation arrives.
  const grantTransport: Transport = { ...transport, async unary(method, signal, timeout, header, input, context) {
    const response = await transport.unary(method, signal, timeout, header, input, context);
    if (!delayedRead && method.parent.typeName === ResourceService.typeName && method.name === "GetResource" && "kind" in input && input.kind === EntityKind.PAIRING) {
      delayedRead = true;
      await readGate;
      await new Promise<void>((resolve) => setTimeout(resolve, 1200));
    }
    return response;
  } };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={grantTransport}><QueryClientProvider client={client}><MutationIntents><Settings pairingAuthority={authority} close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Paired devices" }));
  fireEvent.click(screen.getByRole("button", { name: "Create pairing document" }));
  fireEvent.change(screen.getByLabelText("Device name"), { target: { value: `Disposable ${kind}` } });
  fireEvent.change(screen.getByLabelText("Device type"), { target: { value: kind } });
  fireEvent.click(screen.getByRole("button", { name: "Issue single-use document" }));
  // Keep the private code hidden while a successful fresh server read is pending.
  // If that read is unavailable, exercise the explicit refresh path once.
  try {
    await waitFor(() => expect(delayedRead || screen.queryByText("Grant issued; current use status is unavailable.")).toBeTruthy(), { timeout: 5000 });
    if (delayedRead) {
      expect(screen.queryByRole("button", { name: "Reveal private document" })).toBeNull();
      expect(screen.queryByLabelText("Private pairing document")).toBeNull();
    }
  } finally { releaseRead(); }
  const reveal = screen.queryByRole("button", { name: "Reveal private document" });
  if (reveal) {
    fireEvent.click(reveal);
  } else {
    const refresh = screen.getByRole("button", { name: "Refresh pairing status" }) as HTMLButtonElement;
    await waitFor(() => expect(refresh.disabled).toBe(false), { timeout: 5000 });
    expect(screen.queryByLabelText("Private pairing document")).toBeNull();
    fireEvent.click(refresh);
    fireEvent.click(await screen.findByRole("button", { name: "Reveal private document" }, { timeout: 5000 }));
  }
  const raw = (screen.getByLabelText("Private pairing document") as HTMLTextAreaElement).value;
  const grant = JSON.parse(raw);
  const pair = (path: string) => runCLI([kind === "client" ? "device" : "worker", "pair", kind === "client" ? "--device-dir" : "--worker-dir", path, "--code-stdin", "--name", "Disposable UI grant"], raw);
  const target = join(directory, `ui-pairing-${kind}`);
  await pair(target);
  const credential = JSON.parse(await readFile(join(target, "device.json"), "utf8"));
  expect(credential.server_id).toBe(authority.serverId);
  expect(credential.pairing_id).toBe(grant.pairing_id);
  expect(credential.type).toBe(kind);
  await expect(pair(join(directory, `ui-reused-${kind}`))).rejects.toThrow("Owned fixture CLI failed");
  fireEvent.click(screen.getByRole("button", { name: "Refresh pairing status" }));
  await screen.findByText("Pairing document was used. Its private code has been cleared.", {}, { timeout: 5000 });
  expect(screen.queryByLabelText("Private pairing document")).toBeNull();
  expect(JSON.stringify(client.getQueryCache().getAll().map((query) => [query.queryKey, query.state.data]), (_key, value) => typeof value === "bigint" ? value.toString() : value)).not.toContain(grant.code);
}, 30000);

it("creates and edits singleton server preferences with the exact Go defaults", async () => {
  const defaults = JSON.parse(await runCLI(["settings", "defaults"])).result;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Server preferences" }));
  fireEvent.click(await screen.findByRole("button", { name: "New Server preferences" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Server preferences" }));
  const edit = await screen.findByRole("button", { name: "Edit Server preferences" });
  const resources = createClient(ResourceService, transport);
  const first = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
  expect(first).toHaveLength(1);
  expect(document(first[0])).toEqual(defaults);
  expect(screen.queryByRole("button", { name: "New Server preferences" })).toBeNull();
  fireEvent.click(edit);
  fireEvent.change(screen.getByLabelText("Default account routing"), { target: { value: "priority" } });
  fireEvent.click(screen.getByRole("checkbox", { name: "Allow automatic fetch before Worktree preparation" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Server preferences" }));
  await screen.findByRole("button", { name: "Edit Server preferences" });
  const latest = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
  expect(latest).toHaveLength(1);
  expect(latest[0].id).toBe(first[0].id);
  expect(latest[0].revision).toBe(first[0].revision + 1n);
  expect(document(latest[0])).toEqual({ ...defaults, default_routing: "priority", automatic_fetch: false });
}, 15000);


it("reads unavailable usage through the actual Go service without inventing cost", async () => {
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Usage active open={()=>{}} /></QueryClientProvider></TransportProvider>);
 await screen.findByText("Incomplete coverage");
 expect(screen.getByText(/No exact response usage is recorded/)).toBeTruthy();
 expect(screen.getByText("Actual API cost:").parentElement!.textContent).toContain("Unavailable");
 fireEvent.click(screen.getByRole("checkbox",{name:"General Chat only"}));
 fireEvent.click(screen.getByRole("button",{name:"Apply filters"}));
 await waitFor(()=>expect(screen.queryByText("Loading usage…")).toBeNull());
 cleanup();client.clear();
});


it("saves and inspects a real immutable model price through desktop settings and CLI", async () => {
  const configurations = createClient(ConfigurationService, transport);
  const save = async (kind: EntityKind, value: Record<string, unknown>) => (await configurations.saveConfiguration({ kind, mutation: { requestId: newRequestId() }, schemaVersion: 1, documentJson: encode(value) })).resource!;
  const provider = await save(EntityKind.PROVIDER, { name: "Pricing API", endpoint: providerOrigin, protocol: "openai-chat", authentication: "keyless", discovery: false });
  const model = await save(EntityKind.MODEL, { name: "Pricing model", provider_id: provider.id, native_id: "pricing-fixture", harnesses: ["codex"], manual: true, metadata_source: "user-declared" });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Models" }));
  const heading = await screen.findByRole("heading", { name: "Pricing model" });
  fireEvent.click(within(heading.closest("article")!).getByRole("button", { name: "Token pricing" }));
  await screen.findByText(/No pricing basis has been configured/);
  await waitFor(() => expect((screen.getByRole("button", { name: "Edit token pricing" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Edit token pricing" }));
  const change = (name: string, value: string) => fireEvent.change(screen.getByLabelText(name), { target: { value } });
  change("Currency", "USD"); change("Pricing source", "Owned explicit source"); change("As-of date", "2026-09-25"); change("Input rate per million", "0.000000001");
  fireEvent.click(screen.getByRole("button", { name: "Save pricing version" }));
  await screen.findByText(/Accepted pricing version 1/);
  const usage = createClient(UsageService, transport);
  const original = await usage.getModelPricing({ modelId: model.id });
  expect(original.modelRevision).toBe(model.revision);
  expect(original.pricing?.basis?.inputPerMillion).toBe("0.000000001");
  expect(original.pricing?.basis?.outputPerMillion).toBeUndefined();
  const cli = JSON.parse(await runCLI(["usage", "pricing", "get", "--model-id", model.id]));
  expect(cli.result.pricing.id).toBe(original.pricing?.id);
  await waitFor(() => expect((screen.getByRole("button", { name: "Edit token pricing" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Edit token pricing" }));
  change("Input rate per million", "2.5");
  fireEvent.click(screen.getByRole("button", { name: "Save pricing version" }));
  await screen.findByText(/Accepted pricing version 2/);
  const retained = await usage.getPricingVersion({ id: original.pricing!.id });
  expect(retained.pricing?.basis?.inputPerMillion).toBe("0.000000001");
  const summary = await usage.getUsageSummary({ modelId: model.id });
  expect(summary.estimates?.currencies).toEqual([]);
  expect(summary.totals?.responses).toBe(0);
}, 30000);

it("persists native Claude permission selection through the desktop and real Go configuration service", async () => {
  const configurations = createClient(ConfigurationService, transport);
  const save = async (kind: EntityKind, value: Record<string, unknown>) => (await configurations.saveConfiguration({ kind, mutation: { requestId: newRequestId() }, schemaVersion: 1, documentJson: encode(value) })).resource!;
  const provider = await save(EntityKind.PROVIDER, { name: "Claude settings API", endpoint: providerOrigin, protocol: "anthropic-messages", authentication: "keyless", discovery: false });
  const model = await save(EntityKind.MODEL, { name: "Claude settings model", provider_id: provider.id, native_id: "claude-settings-fixture", harnesses: ["claude-code"], manual: true, metadata_source: "user-declared" });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  // Model choices wait for a provider-capability read and then model search.
  // Keep both real RPCs and exercise ordinary delayed replies deterministically;
  // the component-test library's default one-second wait is not a server SLA.
  const slowTransport: Transport = {
    ...transport,
    async unary(method, signal, timeoutMs, header, input, contextValues) {
      if ((method.name === "ListProviderInventory" && (input as { pageSize?: number }).pageSize === 200) || method.name === "SearchModels") {
        await new Promise(resolve => setTimeout(resolve, 600));
      }
      return transport.unary(method, signal, timeoutMs, header, input, contextValues);
    },
  };
  render(<TransportProvider transport={slowTransport}><QueryClientProvider client={client}><MutationIntents><Settings close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  fireEvent.click(screen.getByRole("button", { name: "New Agent Worker" }));
  const change = (name: string, value: string) => fireEvent.change(screen.getByLabelText(name), { target: { value } });
  change("Name", "Native Claude settings"); change("Harness", "claude-code");
  await screen.findByRole("option", { name: "Claude settings model" }, { timeout: 5000 });
  change("Model", model.id); change("Claude permission mode", "dontAsk");
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await screen.findByRole("heading", { name: "Native Claude settings" });
  const resources = createClient(ResourceService, transport);
  const agents = await resources.listResources({ filter: { kind: EntityKind.AGENT } });
  const agent = agents.resources.find((row) => document(row).name === "Native Claude settings")!;
  expect(document(agent)).toMatchObject({ harness: "claude-code", options: { permission: "default", claude_permission: "dontAsk" } });
  const prior = document(agent);
  await expect(configurations.saveConfiguration({ kind: EntityKind.AGENT, mutation: { id: agent.id, expectedRevision: agent.revision, requestId: newRequestId() }, schemaVersion: 1, documentJson: encode({ ...prior, options: { permission: "read-only", claude_permission: "plan" } }) })).rejects.toThrow();
  expect(document((await resources.getResource({ id: agent.id, kind: EntityKind.AGENT })).resource)).toEqual(prior);
}, 15000);

it("saves and renames GitHub profiles through the real Go server and CLI", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  let saves = 0;
  // Exercise sequential save/list latency and a delayed durable-save
  // acknowledgment. Wait for refreshed profiles without replaying writes.
  const slowTransport: Transport = {
    ...transport,
    async unary(method, signal, timeoutMs, header, input, contextValues) {
      const profileList = method.name === "ListResources" && (input as { filter?: { kind?: EntityKind } }).filter?.kind === EntityKind.INTEGRATION;
      if (method.name === "SaveIntegrationProfile" || profileList) {
        await new Promise((resolve) => setTimeout(resolve, 600));
      }
      if (method.name === "SaveIntegrationProfile") {
        saves += 1;
        const response = await transport.unary(method, signal, timeoutMs, header, input, contextValues);
        // Real durable saves and their acknowledgments can exceed the default
        // one-second DOM lookup. Exercise that latency without repeating a write.
        await new Promise((resolve) => setTimeout(resolve, 1500));
        return response;
      }
      return transport.unary(method, signal, timeoutMs, header, input, contextValues);
    },
  };
  render(<TransportProvider transport={slowTransport}><QueryClientProvider client={client}><MutationIntents><Settings close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Integrations" }));
  fireEvent.click(await screen.findByRole("button", { name: "New GitHub profile" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Profile name" }), { target: { value: "Real server profile" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Resource owner" }), { target: { value: "fixture-owner" } });
  fireEvent.click(screen.getByRole("button", { name: "Save profile" }));
  // Each save crosses the real Go mutation and list refetch. Use the ordinary
  // one-second wait only if this fixture stops exercising the native server.
  fireEvent.click(await screen.findByRole("button", { name: "Rename Real server profile" }, { timeout: 15000 }));
  fireEvent.change(screen.getByRole("textbox", { name: "Profile name" }), { target: { value: "Renamed server profile" } });
  fireEvent.click(screen.getByRole("button", { name: "Save profile" }));
  await screen.findByRole("button", { name: "Manage Renamed server profile" }, { timeout: 15000 });
  const output = JSON.parse(await runCLI(["integration", "list"]));
  const row = output.result.resources.find((value: { data: { name: string } }) => value.data.name === "Renamed server profile");
  expect(row).toBeTruthy();
  expect(row.data).toEqual({ name: "Renamed server profile", provider: "github.com", token_kind: "fine-grained", resource_owner: "fixture-owner" });
  expect(row.revision).toBe(2);
  expect(saves).toBe(2);
  client.clear(); cleanup();
}, 30000);
