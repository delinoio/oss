import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, readFile, realpath, rm } from "node:fs/promises";
import { createServer, type Server } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import processInfo from "node:process";
import { promisify } from "node:util";
import { createClient, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterAll, beforeAll, expect, it } from "vitest";
import { ConfigurationService, EntityKind, ResourceService, ScheduleService, SystemService, createDeliDevTransport, newRequestId } from "@delinoio/delidev-api-client";
import { CreateSession } from "./views";
import { Schedules } from "./schedules";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { document, encode } from "./documents";

let directory: string, transport: Transport, providerOrigin: string, binary: string, scope: string;
let process: ChildProcess | undefined, worker: ChildProcess | undefined, provider: Server | undefined;
const pause = () => new Promise((resolve) => setTimeout(resolve, 25));
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
  fireEvent.click(await screen.findByRole("button", { name: "New Provider" }));
  change("Name", "Owned local API"); change("API base URL", providerOrigin); change("API protocol", "openai-chat"); change("Authentication", "keyless");
  fireEvent.click(screen.getByRole("checkbox", { name: "Discover models automatically for connected accounts" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Provider" }));
  await screen.findByRole("heading", { name: "Owned local API" });
  fireEvent.click(screen.getByRole("button", { name: "AI accounts" }));
  fireEvent.click(screen.getByRole("button", { name: "New AI account" }));
  change("Account alias", "Owned keyless account");
  const option = await screen.findByRole("option", { name: "Owned local API" });
  change("Provider", (option as HTMLOptionElement).value);
  fireEvent.click(screen.getByRole("button", { name: "Save AI account" }));
  await screen.findByRole("heading", { name: "Owned keyless account" });
  fireEvent.click(screen.getByRole("button", { name: "Manage connection" }));
  await screen.findByText("Explicitly connect this keyless local endpoint on the server computer.");
  fireEvent.click(screen.getByRole("button", { name: "Connect account" }));
  await screen.findByText("Health: unverified · Credential connected");
  fireEvent.click(screen.getByRole("button", { name: "Validate account" }));
  await screen.findByText("Health: ready · Credential connected");
  fireEvent.click(screen.getByRole("button", { name: "Back to accounts" }));
  fireEvent.click(screen.getByRole("button", { name: "Models" }));
  fireEvent.click(screen.getByRole("button", { name: "New Model" }));
  await screen.findByRole("option", { name: "Owned local API" });
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
  await save(EntityKind.AGENT, { name: "Accountless schedule agent", harness: "codex", model_id: model.id, accounts: [], templates: [], options: { permission: "default" } });
  cleanup();
  const scheduleClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const readLocalWorker = async () => {
    const credential = JSON.parse(await readFile(join(workerRoot, "device.json"), "utf8"));
    return { machineId: credential.machine_id as string, token: credential.token as string };
  };
  render(<TransportProvider transport={transport}><QueryClientProvider client={scheduleClient}><MutationIntents><CreateSession visible close={() => {}} open={() => {}} readLocalWorker={readLocalWorker} /></MutationIntents></QueryClientProvider></TransportProvider>);
  change("Name", "Owned Local session");
  change("Project", (await screen.findByRole("option", { name: "Owned project" }) as HTMLOptionElement).value);
  fireEvent.click(screen.getByRole("button", { name: "Use this computer's Local checkouts" }));
  await waitFor(() => expect((screen.getByLabelText("Execution Worker") as HTMLSelectElement).disabled).toBe(true));
  change("Agent Worker", (await screen.findByRole("option", { name: "Accountless schedule agent" }) as HTMLOptionElement).value);
  change("First message", "Local proof fixture without inference");
  fireEvent.click(screen.getByRole("button", { name: "Create session" }));
  await waitFor(async () => {
    const sessions = await createClient(ResourceService, transport).listResources({ filter: { kind: EntityKind.SESSION } });
    expect(sessions.resources.some((row) => document(row).workspace === "local" && document(row).name === "Owned Local session")).toBe(true);
  });
  cleanup();
  render(<TransportProvider transport={transport}><QueryClientProvider client={scheduleClient}><MutationIntents><Schedules active open={() => {}} readLocalWorker={readLocalWorker} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "New schedule" }));
  change("Schedule name", "Owned schedule");
  change("Project", (await screen.findByRole("option", { name: "Owned project" }) as HTMLOptionElement).value);
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
  await waitFor(() => expect(screen.queryByRole("heading", { name: "Owned project" })).toBeNull());
  expect((await createClient(ResourceService, transport).listResources({ filter: { kind: EntityKind.PROJECT } })).resources).toHaveLength(0);
}, 60000);
