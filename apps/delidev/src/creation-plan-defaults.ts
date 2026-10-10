// SPDX-License-Identifier: Apache-2.0
import { Code, ConnectError } from "@connectrpc/connect";
import { decodeResourceDocument, EntityKind, isEntityId, type Resource } from "@delinoio/delidev-api-client";
import { Mode } from "./documents";

type DefaultsReader = {
  listResources: (request: { filter: { kind: EntityKind; pageSize: number } }) => Promise<{ resources: Resource[]; nextPageToken?: string }>;
  getResource: (request: { kind: EntityKind; id: string }) => Promise<{ resource?: Resource }>;
};

function invalidDefaults(): never {
  throw new ConnectError("Current Plan defaults could not be verified. Retry the defaults read or explicitly choose a mode.", Code.FailedPrecondition);
}

function verifiedDocument(resource: Resource | undefined, kind: EntityKind, known?: Resource) {
  if (!resource || resource.kind !== kind || !isEntityId(resource.id) || resource.revision <= 0n || known && (resource.id !== known.id || resource.revision < known.revision)) invalidDefaults();
  if (known && resource.revision === known.revision && (resource.schemaVersion !== known.schemaVersion || resource.documentJson.length !== known.documentJson.length || resource.documentJson.some((byte, index) => byte !== known.documentJson[index]))) invalidDefaults();
  const data = decodeResourceDocument(resource);
  if (!data) invalidDefaults();
  return data;
}

// Fresh source reads authorize only a new automatic choice. Retained mutation
// retries must keep their original request and must never call this resolver.
export async function readAutomaticCreationMode(reader: DefaultsReader, projectId: string, knownSettings?: Resource, knownProject?: Resource): Promise<Mode> {
  const [settings, project] = await Promise.all([
    reader.listResources({ filter: { kind: EntityKind.SETTINGS, pageSize: 2 } }),
    projectId ? reader.getResource({ kind: EntityKind.PROJECT, id: projectId }) : Promise.resolve(undefined),
  ]);
  if (settings.nextPageToken || settings.resources.length > 1 || knownSettings && settings.resources.length === 0) invalidDefaults();
  const global: Record<string, unknown> = settings.resources[0] ? verifiedDocument(settings.resources[0], EntityKind.SETTINGS, knownSettings) : {};
  if (global.plan_mode_default !== undefined && typeof global.plan_mode_default !== "boolean") invalidDefaults();
  let override: unknown = "inherit";
  if (projectId) {
    if (project?.resource?.id !== projectId) invalidDefaults();
    const data = verifiedDocument(project?.resource, EntityKind.PROJECT, knownProject);
    if (data.settings !== undefined && (data.settings === null || typeof data.settings !== "object" || Array.isArray(data.settings))) invalidDefaults();
    override = (data.settings as Record<string, unknown> | undefined)?.plan_mode_default ?? "inherit";
    if (!["inherit", "enabled", "disabled"].includes(override as string)) invalidDefaults();
  }
  return override === "enabled" || override === "inherit" && global.plan_mode_default === true ? Mode.Plan : Mode.Execute;
}
