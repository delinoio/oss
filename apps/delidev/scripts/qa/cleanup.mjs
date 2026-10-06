// SPDX-License-Identifier: Apache-2.0
import { lstat, readdir, realpath, rm } from "node:fs/promises";
import { join } from "node:path";
import { createClient } from "@connectrpc/connect";
import { document, privateJson } from "./environment.mjs";
import { QaError, until } from "./processes.mjs";

export async function list(environment, kind) {
  const client = createClient(environment.api.ResourceService, environment.ownerTransport);
  const resources = [], seen = new Set(); let pageToken = "";
  do {
    const page = await client.listResources({ filter: { kind, pageSize: 200, pageToken } }, { timeoutMs: 5000 });
    resources.push(...page.resources); pageToken = page.nextPageToken;
    if (resources.length > 10_000 || pageToken && seen.has(pageToken)) throw new QaError("cleanup-inventory-invalid");
    seen.add(pageToken);
  } while (pageToken);
  return resources;
}
const mutation = (api, row) => ({ id: row.id, expectedRevision: row.revision, requestId: api.newRequestId() });

async function paged(read, key) {
  const rows = [], seen = new Set(); let pageToken = "";
  do {
    const page = await read({ pageSize: 100, pageToken }); rows.push(...page[key]); pageToken = page.nextPageToken;
    if (rows.length > 10_000 || pageToken && seen.has(pageToken)) throw new QaError("cleanup-inventory-invalid");
    seen.add(pageToken);
  } while (pageToken);
  return rows;
}
async function deletionIds(root) {
  try {
    const stat = await lstat(root);
    if (!stat.isDirectory() || stat.isSymbolicLink() || await realpath(root) !== root) throw new QaError("deletion-inventory-unconfirmed");
    const entries = await readdir(root, { withFileTypes: true });
    if (entries.length > 4096 || entries.some(entry => !entry.isFile() || !/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.json$/.test(entry.name))) throw new QaError("deletion-inventory-unconfirmed");
    return entries.map(entry => entry.name.slice(0, -5));
  } catch (error) { if (error.code === "ENOENT") return []; throw error; }
}

// Check only metadata in scopes created by this run. Do not enumerate the user's
// native credential store or guess whether a protected reference is dispensable.
export async function auditVault(root, serverId) {
  let entries;
  try {
    const stat = await lstat(root);
    if (!stat.isDirectory() || stat.isSymbolicLink() || await realpath(root) !== root) throw new QaError("protected-reference-unconfirmed");
    entries = await readdir(root, { withFileTypes: true });
  } catch (error) { if (error.code === "ENOENT") return; throw error; }
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
  for (const entry of entries) {
    if (entry.isSymbolicLink()) throw new QaError("protected-reference-unconfirmed");
    if (entry.isFile() && entry.name === "scope.json") { const pin = await privateJson(join(root, entry.name)); if (Object.keys(pin).join() !== "scope" || pin.scope !== serverId) throw new QaError("protected-reference-unconfirmed"); continue; }
    if (entry.isFile() && ["vault.lock", "store.lock"].includes(entry.name)) continue;
    if (!entry.isDirectory() || !uuid.test(entry.name)) throw new QaError("protected-reference-unconfirmed");
    for (const record of await readdir(join(root, entry.name), { withFileTypes: true })) {
      if (!record.isFile() || !record.name.endsWith(".json") || !uuid.test(record.name.slice(0, -5))) throw new QaError("protected-reference-unconfirmed");
      const value = await privateJson(join(root, entry.name, record.name));
      if (value.version !== 1 || value.scope !== serverId || value.state !== "deleted") throw new QaError("protected-reference-unconfirmed");
      const reference = value.reference;
      const vault = reference?.owner === entry.name && reference.id === record.name.slice(0, -5) && ["account-api", "account-login", "network-proxy", "worker-ssh", "worker-network-key", "worker-network-config"].includes(reference.purpose) && Object.keys(reference).length === 3;
      const pat = reference?.profile_id === entry.name && reference.generation_id === record.name.slice(0, -5) && Object.keys(reference).length === 2;
      if (!(vault || pat) || Object.keys(value).some(key => !["version", "scope", "reference", "state", "commitment"].includes(key))) throw new QaError("protected-reference-unconfirmed");
    }
  }
}

async function productCleanup(environment) {
  const { api } = environment, options = { timeoutMs: 5000 };
  const resources = createClient(api.ResourceService, environment.ownerTransport);
  if (environment.clientCredential) {
    const { resource } = await resources.getResource({ kind: api.EntityKind.DEVICE, id: environment.clientCredential.device_id }, options);
    if (!document(resource).revoked) await createClient(api.DeviceService, environment.ownerTransport).revokeDevice({ mutation: mutation(api, resource) }, options);
  }
  // Timers cannot create fresh work after the cleanup inventory is collected.
  const schedules = createClient(api.ScheduleService, environment.ownerTransport);
  for (const row of await list(environment, api.EntityKind.SCHEDULE)) await schedules.deleteSchedule({ mutation: mutation(api, row) }, options);
  const sessions = createClient(api.SessionService, environment.ownerTransport);
  const pending = [];
  for (let row of await list(environment, api.EntityKind.SESSION)) {
    if (document(row).archive !== "archived") {
      await sessions.controlSession({ mutation: mutation(api, row), action: api.SessionAction.ARCHIVE }, options);
      row = (await until(async () => {
        const result = await resources.getResource({ kind: api.EntityKind.SESSION, id: row.id }, options);
        return document(result.resource).archive === "archived" ? result : undefined;
      })).resource;
    }
    const response = await sessions.deleteSession({ mutation: mutation(api, row) }, options);
    pending.push(response.job.sessionId);
  }
  for (const sessionId of pending) await until(async () => {
    const { job } = await sessions.getSessionDeletion({ sessionId }, options);
    return job?.state === api.SessionDeletionState.SUCCEEDED && job.workersPending === 0 && job.databaseRemoved && job.backupsRemoved;
  });
  // Previously accepted deletions can outlive their visible Session resource.
  // Filenames supply original IDs only; the product API supplies completion.
  for (const sessionId of await deletionIds(join(environment.serverRoot, "session-deletions"))) await until(async () => {
    const { job } = await sessions.getSessionDeletion({ sessionId }, options);
    return job?.state === api.SessionDeletionState.SUCCEEDED && job.workersPending === 0 && job.databaseRemoved && job.backupsRemoved;
  });
  const system = createClient(api.SystemService, environment.ownerTransport);
  for (const job of await paged(input => system.listBackupCreations(input, options), "jobs")) {
    if (job.state === api.BackupCreationState.PENDING) await until(async () => (await system.getBackupCreation({ id: job.id }, options)).job?.state !== api.BackupCreationState.PENDING);
  }
  for (const backup of await paged(input => system.listBackups(input, options), "backups")) {
    const inspected = await system.inspectBackup({ id: backup.id }, options);
    const deleted = await system.deleteBackup({ requestId: api.newRequestId(), backup: inspected.backup, sha256: inspected.sha256 }, options);
    await until(async () => {
      const { job } = await system.getBackupDeletion({ id: deleted.job.id }, options);
      return job?.state === api.BackupDeletionState.SUCCEEDED && job.removalObserved;
    });
  }
  for (const job of await paged(input => system.listBackupDeletions(input, options), "jobs")) if (job.state !== api.BackupDeletionState.SUCCEEDED || !job.removalObserved) throw new QaError("backup-cleanup-unconfirmed");
  const accounts = createClient(api.AccountService, environment.ownerTransport);
  for (const row of await list(environment, api.EntityKind.ACCOUNT)) {
    const value = document(row);
    if (value.type === "subscription" && value.connection) {
      const response = await createClient(api.SubscriptionService, environment.ownerTransport).requestSubscription({ mutation: mutation(api, row), machineId: value.subscription?.machine_id ?? "", action: api.SubscriptionAction.LOGOUT }, options);
      await until(async () => {
        const result = await resources.getResource({ kind: api.EntityKind.ACCOUNT, id: response.account.id }, options);
        const state = document(result.resource);
        return !state.connection && !state.removal && !state.subscription?.pending && !state.subscription?.lease && !state.subscription?.recovery_required;
      });
    } else if (value.connection || value.removal) {
      const response = await accounts.disconnectAccount({ mutation: mutation(api, row) }, options);
      if (response.cleanupProblemJson.length || document(response.account).removal) throw new QaError("account-cleanup-unconfirmed");
    }
  }
  for (const row of await list(environment, api.EntityKind.INTEGRATION)) {
    const response = await createClient(api.IntegrationService, environment.ownerTransport).deleteIntegrationProfile({ mutation: mutation(api, row) }, options);
    if (!response.deleted || response.problemJson.length) throw new QaError("integration-cleanup-unconfirmed");
  }
  const network = createClient(api.NetworkService, environment.ownerTransport);
  const routes = await list(environment, api.EntityKind.NETWORK_ROUTE);
  for (const row of routes) {
    const value = document(row);
    if (value.profile_id) await network.selectNetworkProfile({ mutation: mutation(api, row), machineId: value.machine_id ?? "", profileId: "", profileRevision: 0n }, options);
  }
  for (const row of await list(environment, api.EntityKind.NETWORK_PROFILE)) await network.deleteNetworkProfile({ mutation: mutation(api, row) }, options);
  for (const row of await list(environment, api.EntityKind.JOB)) {
    if (!["succeeded", "failed", "canceled"].includes(document(row).state)) throw new QaError("original-job-unsettled");
  }
  for (const row of await list(environment, api.EntityKind.ACCOUNT)) {
    const value = document(row);
    if (value.connection || value.removal || value.subscription?.pending || value.subscription?.lease || value.subscription?.recovery_required) throw new QaError("account-cleanup-unconfirmed");
  }
  for (const row of await list(environment, api.EntityKind.DEVICE)) if (document(row).browser_profiles?.length) throw new QaError("native-browser-cleanup-unconfirmed");
  if ((await list(environment, api.EntityKind.SSH_SETUP)).length) throw new QaError("external-setup-ownership-retained");
  if ((await list(environment, api.EntityKind.UPDATE)).length) throw new QaError("replacement-worker-ownership-retained");
}

export async function cleanup(environment) {
  environment.closing = true; environment.phase = "cleaning"; environment.changed();
  const reasons = [];
  try { await environment.host?.close(); } catch { reasons.push("host-drain-unconfirmed"); }
  try {
    await environment.ownsRoot();
    if (environment.ownerTransport) {
      // A stopped/crashed server can be restarted only by the runner for cleanup,
      // with its original endpoint and identity. No browser admission is reopened.
      if (environment.server.child.exitCode !== null || environment.server.child.signalCode !== null) {
        environment.closing = false;
        try { await environment.launchServer(new URL(environment.endpoint).host); } finally { environment.closing = true; }
      }
      await productCleanup(environment);
    } else if (environment.server) throw new QaError("server-cleanup-authority-unavailable");
  } catch (error) { reasons.push(error instanceof QaError ? error.code : "product-cleanup-unconfirmed"); }
  try {
    if (environment.workerCredential) {
      const status = await environment.workerStatus();
      if (status.generation && status.controller_active) await environment.cli(["worker", "stop", "--worker-dir", environment.workerRoot, "--generation", status.generation]);
      if (environment.worker) await environment.processes.stop(environment.worker);
    }
    if (environment.ownerTransport) await createClient(environment.api.SystemService, environment.ownerTransport).stopServer({ requestId: environment.api.newRequestId() }, { timeoutMs: 5000 });
  } catch { reasons.push("product-stop-unconfirmed"); }
  try { await environment.processes.close(); } catch { reasons.push("process-exit-unconfirmed"); }
  try {
    await environment.ownsRoot();
    for (const path of [join(environment.serverRoot, "secrets"), join(environment.serverRoot, "github-pats"), join(environment.workerRoot, "network-vault")]) await auditVault(path, environment.serverId);
    // Unknown OAuth/SSH journals or native Worker scopes retain original recovery
    // authority even after their process exits. Product deletion must settle them.
    for (const path of [join(environment.serverRoot, "oauth"), join(environment.serverRoot, "ssh-setup-claims"), join(environment.serverRoot, "subscription-runtime"), join(environment.workerRoot, "managed-auth")]) {
      try { if ((await readdir(path)).length) throw new QaError("protected-operation-retained"); } catch (error) { if (error.code !== "ENOENT") throw error; }
    }
    if (reasons.length === 0) { await rm(environment.root, { recursive: true }); environment.phase = "deleted"; }
  } catch (error) { reasons.push(error instanceof QaError ? error.code : "cleanup-audit-unconfirmed"); }
  if (reasons.length) environment.phase = "preserved";
  environment.changed();
  return { worker: environment.index, state: environment.phase, ...(reasons.length ? { path: environment.root, serverId: environment.serverId, endpoint: environment.endpoint, frontendOrigin: environment.host?.origin, reasons: [...new Set(reasons)] } : {}) };
}
