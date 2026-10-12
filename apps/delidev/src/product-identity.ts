// SPDX-License-Identifier: Apache-2.0
/** Presentation kinds never replace RPC, storage, selection or recovery identity. */
export enum PresentationKind {
  Resource = "resource",
  Project = "project",
  Repository = "repository",
  Session = "session",
  Agent = "agent",
  Template = "template",
  Account = "account",
  Provider = "provider",
  Model = "model",
  Runner = "runner",
  Device = "device",
  Server = "server",
  Connection = "connection",
  Backup = "backup",
  Execution = "execution",
  Input = "input",
  Request = "request",
  Observation = "observation",
  Terminal = "terminal",
  Schedule = "schedule",
  Occurrence = "occurrence",
  Review = "review",
  Comment = "comment",
  Pricing = "pricing",
  Integration = "integration",
  NetworkProfile = "network-profile",
  Storage = "storage",
  Settings = "settings",
  Theme = "theme",
  Attempt = "attempt",
}
interface IdentityPresentation {
  readonly number: number;
  name?: string;
}
export type IdentityLabel = (
  kind: PresentationKind,
  id: string | undefined,
  name?: string,
) => string;

/** One owner retains numbers and historical names, including removed targets. */
export class ProductIdentities {
  private readonly entries = new Map<
    PresentationKind,
    Map<string, IdentityPresentation>
  >();
  private readonly names = new Map<
    PresentationKind,
    Map<string, Set<string>>
  >();
  observe(
    kind: PresentationKind,
    id: string,
    name?: string,
  ): IdentityPresentation {
    let entries = this.entries.get(kind);
    if (!entries) this.entries.set(kind, (entries = new Map()));
    let entry = entries.get(id);
    if (!entry) entries.set(id, (entry = { number: entries.size + 1 }));
    // Empty and unavailable observations cannot erase an already observed name.
    // Original names, including UUID-looking names, remain byte-for-byte intact.
    if (name && name !== entry.name) {
      let names = this.names.get(kind);
      if (!names) this.names.set(kind, (names = new Map()));
      if (entry.name) names.get(entry.name)?.delete(id);
      let identities = names.get(name);
      if (!identities) names.set(name, (identities = new Set()));
      identities.add(id);
      entry.name = name;
    }
    return entry;
  }
  label(
    kind: PresentationKind,
    id: string | undefined,
    fallback: string,
    numbered: (name: string, number: number) => string,
    name?: string,
  ): string {
    if (!id) return fallback;
    if (
      kind === PresentationKind.Provider &&
      !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
        id,
      )
    )
      return name || id;
    const entry = this.observe(kind, id, name);
    if (!entry.name) return numbered(fallback, entry.number);
    const duplicate = (this.names.get(kind)?.get(entry.name)?.size ?? 0) > 1;
    return duplicate ? numbered(entry.name, entry.number) : entry.name;
  }
}

const sourceContent =
  /^(?:native(?:_|$)|raw(?:_|$)|(?:.*_)?(?:content|text|prompt|response|output|metadata|name|alias|path)$)/;
const uuid =
  /\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi;
/** Call only for DeliDev-generated diagnostic prose, never user/native output. */
export function productDiagnostic(value: unknown, replacement: string): string {
  return typeof value === "string" ? value.replace(uuid, replacement) : "";
}

/** Clone only an owner's declared internal fields; original evidence stays intact. */
export function productMetadata(
  value: unknown,
  fields: Readonly<Record<string, PresentationKind>>,
  label: IdentityLabel,
): unknown {
  if (Array.isArray(value))
    return value.map((entry) => productMetadata(entry, fields, label));
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(
    Object.entries(value).map(([key, entry]) => [
      key,
      fields[key] && typeof entry === "string"
        ? label(fields[key], entry)
        : fields[key] && Array.isArray(entry)
          ? entry.map((id) =>
              typeof id === "string" ? label(fields[key], id) : id,
            )
          : // Native/user artifacts are source content, not generated identity rows.
            sourceContent.test(key)
            ? entry
            : productMetadata(entry, fields, label),
    ]),
  );
}

/** Project declared JSON fields without parsing or rounding numeric tokens.
 * The lexer keeps source/native subtrees and every other byte intact. */
export function productMetadataJSON(
  raw: string,
  fields: Readonly<Record<string, PresentationKind>>,
  label: IdentityLabel,
): string {
  interface Frame {
    object: boolean;
    key?: string;
    expectsKey: boolean;
    protected: boolean;
    kind?: PresentationKind;
  }
  const stack: Frame[] = [];
  const tokens =
    /"(?:\\.|[^"\\])*"|[{}\[\]:,]|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|true|false|null/g;
  return raw.replace(tokens, (token) => {
    const parent = stack[stack.length - 1];
    const protectedValue = Boolean(
      parent?.protected ||
      (parent?.object && parent.key && sourceContent.test(parent.key)),
    );
    const kind = parent?.object ? fields[parent.key ?? ""] : parent?.kind;
    if (token === "{" || token === "[") {
      stack.push({
        object: token === "{",
        expectsKey: token === "{",
        protected: protectedValue,
        kind,
      });
      return token;
    }
    if (token === "}" || token === "]") {
      stack.pop();
      return token;
    }
    if (token === ",") {
      if (parent?.object) {
        parent.expectsKey = true;
        parent.key = undefined;
      }
      return token;
    }
    if (token === ":") {
      if (parent) parent.expectsKey = false;
      return token;
    }
    if (!token.startsWith('"')) return token;
    const value = JSON.parse(token) as string;
    if (parent?.object && parent.expectsKey) {
      parent.key = value;
      return token;
    }
    return kind && !protectedValue ? JSON.stringify(label(kind, value)) : token;
  });
}
export const generatedIdentityFields: Readonly<
  Record<string, PresentationKind>
> = {
  id: PresentationKind.Resource,
  server_id: PresentationKind.Server,
  session_id: PresentationKind.Session,
  repository_id: PresentationKind.Repository,
  project_id: PresentationKind.Project,
  agent_id: PresentationKind.Agent,
  template_id: PresentationKind.Template,
  machine_id: PresentationKind.Runner,
  device_id: PresentationKind.Device,
  account_id: PresentationKind.Account,
  provider_id: PresentationKind.Provider,
  model_id: PresentationKind.Model,
  model_key: PresentationKind.Model,
  connection_id: PresentationKind.Connection,
  execution_id: PresentationKind.Execution,
  input_id: PresentationKind.Input,
  request_id: PresentationKind.Request,
  correlation_id: PresentationKind.Request,
  attempt_id: PresentationKind.Attempt,
  operation_id: PresentationKind.Request,
  publication_request_id: PresentationKind.Request,
  backup_id: PresentationKind.Backup,
  integration_id: PresentationKind.Integration,
  network_profile_id: PresentationKind.NetworkProfile,
  snapshot_id: PresentationKind.Storage,
  checkpoint_id: PresentationKind.Storage,
  occurrence_id: PresentationKind.Occurrence,
  schedule_id: PresentationKind.Schedule,
  primary_repository: PresentationKind.Repository,
  repositories: PresentationKind.Repository,
};
