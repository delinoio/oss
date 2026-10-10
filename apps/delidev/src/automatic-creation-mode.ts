// SPDX-License-Identifier: Apache-2.0
import { Code, ConnectError, createClient, type Transport } from "@connectrpc/connect";
import { decodeResourceDocument, EntityKind, ResourceService, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { Mode } from "./documents";

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
function unavailable(): never { throw new ConnectError("Current session defaults are unavailable.", Code.FailedPrecondition); }
function validated(row: Resource | undefined, kind: EntityKind, previous?: Resource) {
  if (!row || row.kind !== kind || !uuid.test(row.id) || row.revision < 1n || row.revision > 9223372036854775807n || !supportsResourceSchema(row) || previous?.id === row.id && row.revision < previous.revision) unavailable();
  const data = decodeResourceDocument(row);
  if (!data) unavailable();
  return data;
}

// Read the original authoritative sources directly, rather than joining a cached
// query. A failed/incomplete observation never authorizes an automatic Execute.
export async function readAutomaticCreationMode(transport: Transport, projectId: string, agentId: string, previousSettings?: Resource, previousProject?: Resource): Promise<Mode> {
  const client = createClient(ResourceService, transport);
  const [settings, project] = await Promise.all([
    client.listResources({ filter: { kind: EntityKind.SETTINGS, pageSize: 2 } }),
    projectId ? client.getResource({ kind: EntityKind.PROJECT, id: projectId }) : Promise.resolve(undefined),
  ]);
  if (settings.resources.length > 1 || settings.nextPageToken) unavailable();
  const global = settings.resources.length ? validated(settings.resources[0], EntityKind.SETTINGS, previousSettings) : {};
  if (global.plan_mode_default !== undefined && typeof global.plan_mode_default !== "boolean") unavailable();
  let override = "inherit";
  if (projectId) {
    if (project?.resource?.id !== projectId) unavailable();
    const data = validated(project.resource, EntityKind.PROJECT, previousProject);
    if (data.disabled === true || data.enabled === false) unavailable();
    if (data.settings !== undefined && (data.settings === null || typeof data.settings !== "object" || Array.isArray(data.settings))) unavailable();
    const policy = data.settings as Record<string, unknown> | undefined;
    if (policy?.plan_mode_default !== undefined) {
      if (typeof policy.plan_mode_default !== "string" || !["inherit", "enabled", "disabled"].includes(policy.plan_mode_default)) unavailable();
      override = policy.plan_mode_default;
    }
    if (data.agents !== undefined) {
      const agents = data.agents as { configured?: unknown; ids?: unknown };
      if (!agents || typeof agents !== "object" || Array.isArray(agents) || typeof agents.configured !== "boolean" || !Array.isArray(agents.ids) || !agents.ids.every(id => typeof id === "string" && uuid.test(id)) || agents.configured && !agents.ids.includes(agentId)) unavailable();
    }
  }
  return override === "enabled" || override === "inherit" && global.plan_mode_default === true ? Mode.Plan : Mode.Execute;
}
