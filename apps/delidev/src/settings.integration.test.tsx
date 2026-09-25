import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer, type Server } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import processInfo from "node:process";
import { promisify } from "node:util";
import { createClient, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterAll, beforeAll, expect, it } from "vitest";
import { EntityKind, ResourceService, SystemService, createDeliDevTransport, newRequestId } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { document } from "./documents";

let directory: string, transport: Transport, providerOrigin: string;
let process: ChildProcess | undefined, provider: Server | undefined;
const pause = () => new Promise((resolve) => setTimeout(resolve, 25));
beforeAll(async () => {
  directory = await mkdtemp(join(tmpdir(), "delidev-settings-"));
  const binary = join(directory, processInfo.platform === "win32" ? "delidev.exe" : "delidev");
  await promisify(execFile)("go", ["build", "-o", binary, "./cmds/delidev-cli"], { cwd: resolve(processInfo.cwd(), "../.."), timeout: 120000 });
  const scope = join(directory, "server");
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
afterAll(async () => {
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
