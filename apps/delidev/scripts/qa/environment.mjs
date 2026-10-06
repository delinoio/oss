// SPDX-License-Identifier: Apache-2.0
import { lstat, mkdir, readFile, realpath, writeFile } from "node:fs/promises";
import { randomUUID } from "node:crypto";
import { join } from "node:path";
import { createClient } from "@connectrpc/connect";
import { Processes, QaError, until } from "./processes.mjs";
import { createHost } from "./host.mjs";

const id = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
export async function privateJson(file) {
  const stat = await lstat(file);
  if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 16_384 || process.platform !== "win32" && (stat.mode & 0o077) !== 0 || await realpath(file) !== file) throw new QaError("private-state-invalid");
  return JSON.parse(await readFile(file, "utf8"));
}
export function credential(value, endpoint, serverId, type) {
  if (value.version !== 1 || value.type !== type || value.endpoint !== endpoint || value.server_id !== serverId || !id.test(value.device_id) || !id.test(value.pairing_id) || !/^[A-Za-z0-9_-]{43}$/.test(value.token) || type === "worker" && !id.test(value.machine_id)) throw new QaError("paired-authority-invalid");
  return value;
}
export const document = resource => JSON.parse(new TextDecoder().decode(resource.documentJson));

export class Environment {
  processes = new Processes(); endpoint = ""; phase = "preparing"; closing = false;
  constructor({ root, assets, binary, api, index, changed = () => {} }) { Object.assign(this, { root, assets, binary, api, index, changed }); this.ownerNonce = randomUUID(); this.serverRoot = join(root, "server"); this.clientRoot = join(root, "client"); this.workerRoot = join(root, "worker"); }
  cli(args, options) { return this.processes.run(this.binary, ["--data-dir", this.serverRoot, ...args], { ...options, json: true }); }
  async prepare() {
    await mkdir(this.root, { mode: 0o700 }); this.rootStat = await lstat(this.root);
    await writeFile(join(this.root, "qa-owner.json"), JSON.stringify({ version: 1, nonce: this.ownerNonce }), { mode: 0o600, flag: "wx" });
    this.serverRoot = join(this.root, "server"); this.clientRoot = join(this.root, "client"); this.workerRoot = join(this.root, "worker");
    for (const path of [this.serverRoot, this.clientRoot, this.workerRoot]) await mkdir(path, { mode: 0o700 });
    this.host = await createHost(this.assets, this);
    await this.launchServer("127.0.0.1:0");
    await this.cli(["device", "pair-local", "--device-dir", this.clientRoot]);
    this.clientCredential = credential(await privateJson(join(this.clientRoot, "device.json")), this.endpoint, this.serverId, "client");
    await this.registerWorker();
    await this.startWorker();
    await this.verify(); this.phase = "ready"; this.changed();
  }
  async launchServer(listen) {
    if (this.closing) throw new QaError("environment-closing");
    this.server = this.processes.spawn(this.binary, ["--data-dir", this.serverRoot, "server", "run", "--listen", listen, "--allowed-origins", this.host.origin]);
    const ready = await until(async () => {
      if (this.closing || this.server.failed || this.server.child.exitCode !== null || this.server.child.signalCode !== null) throw new QaError("server-start-failed");
      return this.server.lines.find(value => value.version === 1 && value.result?.status === "ready")?.result;
    });
    const endpoint = new URL(ready.endpoint.url);
    if (endpoint.hostname !== "127.0.0.1" || endpoint.protocol !== "http:" || !endpoint.port || endpoint.pathname !== "/" || endpoint.search || endpoint.username || endpoint.password) throw new QaError("server-endpoint-invalid");
    if (this.endpoint && this.endpoint !== endpoint.origin) throw new QaError("server-endpoint-changed");
    this.endpoint = endpoint.origin;
    const owner = await privateJson(join(this.serverRoot, "owner.json"));
    if (!id.test(owner.server_id) || !/^[A-Za-z0-9_-]{43}$/.test(owner.token) || this.serverId && owner.server_id !== this.serverId) throw new QaError("server-identity-invalid");
    this.serverId = owner.server_id;
    this.ownerTransport = this.api.createDeliDevTransport({ origin: this.endpoint, getToken: () => owner.token });
    this.server.done.then(() => { if (!this.closing) { this.phase = "server-stopped"; this.changed(); } });
    const status = await createClient(this.api.SystemService, this.ownerTransport).getStatus({}, { timeoutMs: 5000 });
    if (status.serverId !== this.serverId || status.version !== "0.1.0" || status.protocolVersion !== 1 || status.stopping) throw new QaError("server-verification-failed");
  }
  async registerWorker() {
    // Registration is idempotent only for this run's existing credential. Never
    // replace a lost or revoked Worker with a new device behind a browser action.
    if (!this.workerCredential) {
      await this.cli(["worker", "pair-local", "--worker-dir", this.workerRoot]);
      this.workerCredential = credential(await privateJson(join(this.workerRoot, "device.json")), this.endpoint, this.serverId, "worker");
    }
    return this.workerStatus();
  }
  async workerStatus() {
    const result = await this.cli(["worker", "status", "--worker-dir", this.workerRoot]);
    const lifecycle = result.lifecycle?.version === 1 ? result.lifecycle : undefined;
    if (!this.workerCredential || lifecycle && (lifecycle.server_id !== this.serverId || lifecycle.machine_id !== this.workerCredential.machine_id || lifecycle.device_id !== this.workerCredential.device_id || lifecycle.endpoint !== this.endpoint)) throw new QaError("worker-identity-invalid");
    if (!["not-started", "starting", "running", "stopping", "exited", "uncertain"].includes(result.state) || typeof result.controller_active !== "boolean") throw new QaError("worker-status-invalid");
    if (!lifecycle && result.state !== "not-started") throw new QaError("worker-identity-invalid");
    return { state: result.state, machine_id: this.workerCredential.machine_id, generation: lifecycle?.generation, controller_active: result.controller_active };
  }
  async startWorker() {
    const status = await this.workerStatus();
    if (status.controller_active) return status;
    if (this.worker) await this.processes.stop(this.worker);
    if (this.closing) throw new QaError("environment-closing");
    this.worker = this.processes.spawn(this.binary, ["--data-dir", this.serverRoot, "worker", "start", "--worker-dir", this.workerRoot]);
    this.worker.done.then(() => { if (!this.closing) { this.phase = "worker-stopped"; this.changed(); } });
    await until(async () => {
      if (this.closing || this.worker.failed || this.worker.child.exitCode !== null || this.worker.child.signalCode !== null) throw new QaError("worker-start-failed");
      const state = await this.workerStatus(); return state.state === "running" && state.controller_active ? state : undefined;
    }, 60_000);
    await this.verifyWorker();
    if (this.phase !== "preparing") { this.phase = "ready"; this.changed(); }
    return this.workerStatus();
  }
  async verifyWorker() {
    const resource = createClient(this.api.ResourceService, this.ownerTransport);
    return until(async () => {
      const response = await resource.getResource({ kind: this.api.EntityKind.MACHINE, id: this.workerCredential.machine_id }, { timeoutMs: 5000 });
      const machine = document(response.resource);
      return !machine.disabled && machine.version === "0.1.0" && Date.parse(machine.last_seen) > Date.now() - 45_000 && machine.worker_capabilities?.length > 0;
    }, 60_000);
  }
  async verify() {
    const transport = this.api.createDeliDevTransport({ origin: this.endpoint, getToken: () => this.clientCredential.token });
    const status = await createClient(this.api.SystemService, transport).getStatus({}, { timeoutMs: 5000 });
    if (status.serverId !== this.serverId || status.protocolVersion !== 1 || status.version !== "0.1.0" || status.stopping) throw new QaError("paired-verification-failed");
    await this.verifyWorker();
  }
  async bootstrap() {
    if (!this.clientCredential || this.phase === "preparing") throw new QaError("environment-not-ready");
    // Only the paired client credential crosses this boundary, in a no-store
    // response. It must stay in the renderer's connection closure, never Query.
    return { version: 1, index: this.index, endpoint: this.endpoint, serverId: this.serverId, deviceId: this.clientCredential.device_id, token: this.clientCredential.token };
  }
  async status() {
    if (!this.ownerTransport) return { state: "checking", attempts: 0, retry_ms: 0 };
    try {
      const transport = this.api.createDeliDevTransport({ origin: this.endpoint, getToken: () => this.clientCredential.token });
      const value = await createClient(this.api.SystemService, transport).getStatus({}, { timeoutMs: 2000 });
      if (value.serverId !== this.serverId || value.version !== "0.1.0" || value.protocolVersion !== 1 || value.stopping) throw new QaError("paired-verification-failed");
      return { state: "ready", attempts: 0, retry_ms: 0 };
    } catch { return { state: this.server?.child.exitCode !== null || this.server?.child.signalCode !== null ? "stopped" : "blocked", attempts: 0, retry_ms: 0, failure: "qa-connection-unavailable" }; }
  }
  async startServer() {
    if (this.server?.child.exitCode === null && this.server?.child.signalCode === null) return this.status();
    if (this.server) await this.processes.stop(this.server);
    await this.launchServer(new URL(this.endpoint).host);
    // Workers retain their product reconnect policy. Starting a server does not
    // replace a stopped Worker generation or silently pair a revoked client.
    this.phase = "running"; this.changed(); return this.status();
  }
  async controlWorker({ action, generation }) {
    if (!this.workerCredential || this.closing) throw new QaError("environment-not-ready");
    if (action !== "stop" && generation !== undefined) throw new QaError("invalid-request");
    if (action === "status") return this.workerStatus();
    if (action === "register") return this.registerWorker();
    if (action === "start") return this.startWorker();
    if (action !== "stop" || !id.test(generation)) throw new QaError("invalid-request");
    await this.cli(["worker", "stop", "--worker-dir", this.workerRoot, "--generation", generation]);
    return this.workerStatus();
  }
  async proof() {
    const value = credential(await privateJson(join(this.workerRoot, "device.json")), this.endpoint, this.serverId, "worker");
    if (value.device_id !== this.workerCredential.device_id || value.machine_id !== this.workerCredential.machine_id) throw new QaError("worker-identity-invalid");
    return { machineId: value.machine_id, token: value.token };
  }
  async network({ machine, action, ciphertext, digest }) {
    if (machine !== this.workerCredential.machine_id || !["prepare", "status", "import"].includes(action)) throw new QaError("invalid-request");
    const args = ["worker", "network", action, "--worker-dir", this.workerRoot]; let input;
    if (action === "import") {
      if (typeof ciphertext !== "string" || ciphertext.length > 131072 || !/^[A-Za-z0-9+/]+={0,2}$/.test(ciphertext) || !/^[0-9a-f]{64}$/.test(digest)) throw new QaError("invalid-request");
      input = Buffer.from(ciphertext, "base64"); if (input.toString("base64") !== ciphertext || input.length > 96 * 1024) throw new QaError("invalid-request");
      args.push("--input", "-", "--expected-ciphertext-digest", digest);
    } else if (ciphertext !== "" || digest !== "") throw new QaError("invalid-request");
    return this.cli(args, { input });
  }
  async ownsRoot() {
    const stat = await lstat(this.root), marker = await privateJson(join(this.root, "qa-owner.json"));
    if (!stat.isDirectory() || stat.isSymbolicLink() || await realpath(this.root) !== this.root || stat.dev !== this.rootStat.dev || stat.ino !== this.rootStat.ino || marker.version !== 1 || marker.nonce !== this.ownerNonce) throw new QaError("environment-ownership-unconfirmed");
  }
  manifest() { return { worker: this.index, url: this.host?.origin, serverId: this.serverId, state: this.phase }; }
}
