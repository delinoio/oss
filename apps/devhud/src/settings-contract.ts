import {
  canonicalizeSettingsJson,
  encodeCanonicalSettingsJson,
  validateCanonicalSettingsJson,
} from "@delinoio/devhud-api-client";

export const SettingsSchemaVersion = 1 as const;

const Theme = ["system", "light", "dark"] as const;
const Language = ["system", "en", "ko"] as const;
const AgentKind = ["codex", "claude-code", "opencode"] as const;
const AgentMode = ["draft", "direct"] as const;
const UploadProvider = ["official", "r2"] as const;
const NotificationKind = ["review", "checks", "merged", "closed"] as const;
const Platform = ["desktop", "ios", "android"] as const;

type Theme = (typeof Theme)[number];
type Language = (typeof Language)[number];
type AgentKind = (typeof AgentKind)[number];
type AgentMode = (typeof AgentMode)[number];
type UploadProvider = (typeof UploadProvider)[number];
type NotificationKind = (typeof NotificationKind)[number];
type Platform = (typeof Platform)[number];

export interface DevHudSettingsV1 {
  readonly schemaVersion: typeof SettingsSchemaVersion;
  readonly appearance: { readonly theme: Theme; readonly language: Language };
  readonly decks: readonly {
    readonly id: string;
    readonly title: string;
    readonly query: string;
    readonly repository: string | null;
    readonly display: { readonly groupBy: "none" | "repository" | "author"; readonly showDrafts: boolean };
    readonly refreshMinutes: 1 | 5 | 15 | 30;
    readonly notifications: readonly NotificationKind[];
  }[];
  readonly github: {
    readonly repositories: readonly { readonly owner: string; readonly name: string }[];
    readonly issueTracker: { readonly owner: string; readonly repository: string; readonly labels: readonly string[] } | null;
  };
  readonly urlMappings: readonly { readonly sourcePrefix: string; readonly destinationPrefix: string }[];
  readonly shortcuts: Readonly<Record<Platform, Readonly<Record<string, string>>>>;
  readonly agents: readonly {
    readonly id: string;
    readonly enabled: boolean;
    readonly kind: AgentKind;
    readonly mode: AgentMode;
    readonly repositoryPrompts: boolean;
    readonly profileRef: string | null;
  }[];
  readonly uploads: {
    readonly provider: UploadProvider;
    readonly r2: {
      readonly profileRef: string;
      readonly bucket: string;
      readonly endpoint: string;
      readonly region: string;
      readonly publicBaseUrl: string | null;
    } | null;
  };
}

export const defaultDevHudSettings: DevHudSettingsV1 = Object.freeze<DevHudSettingsV1>({
  schemaVersion: SettingsSchemaVersion,
  appearance: { theme: "system", language: "system" },
  decks: [],
  github: { repositories: [], issueTracker: null },
  urlMappings: [],
  shortcuts: { desktop: {}, ios: {}, android: {} },
  agents: [],
  uploads: { provider: "official", r2: null },
});

const sensitiveKeyPattern = /(?:^|[-_.])(?:api[-_.]?url|token|password|passwd|pwd|secret|pat|access[-_.]?key(?:[-_.]?id)?|private[-_.]?key|authorization|cookie|agent[-_.]?(?:path|version)|autostart|window|widget|draft|cache|pairing|permission)(?:$|[-_.])/iu;
const sensitiveValuePatterns = [
  /\b(?:ghp|github_pat)_[A-Za-z0-9_]+\b/u,
  /\bAKIA[0-9A-Z]{16}\b/u,
  /\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b/u,
  /-----BEGIN [A-Z ]*PRIVATE KEY-----/u,
] as const;
const safeDynamicKeyPattern = /^[a-zA-Z0-9._:-]{1,128}$/u;
const profileRefPattern = /^[a-zA-Z0-9._-]{1,128}$/u;

export class SettingsContractError extends TypeError {
  readonly path: string;

  constructor(path: string, message: string) {
    super(`${path}: ${message}`);
    this.name = "SettingsContractError";
    this.path = path;
  }
}

export function parseDevHudSettings(value: unknown): DevHudSettingsV1 {
  rejectSensitiveContent(value, "$", new WeakSet());
  const root = object(value, "$", ["schemaVersion", "appearance", "decks", "github", "urlMappings", "shortcuts", "agents", "uploads"]);
  integer(root.schemaVersion, "$.schemaVersion", 1, 1);

  const appearance = object(root.appearance, "$.appearance", ["theme", "language"]);
  const decks = array(root.decks, "$.decks");
  if (decks.length > 25) throw new SettingsContractError("$.decks", "must contain at most 25 entries");
  const github = object(root.github, "$.github", ["repositories", "issueTracker"]);
  const shortcuts = object(root.shortcuts, "$.shortcuts", [...Platform]);
  const uploads = object(root.uploads, "$.uploads", ["provider", "r2"]);

  return {
    schemaVersion: SettingsSchemaVersion,
    appearance: {
      theme: enumeration(appearance.theme, "$.appearance.theme", Theme),
      language: enumeration(appearance.language, "$.appearance.language", Language),
    },
    decks: decks.map((entry, index) => parseDeck(entry, `$.decks[${index}]`)),
    github: {
      repositories: array(github.repositories, "$.github.repositories").map((entry, index) => {
        const path = `$.github.repositories[${index}]`;
        const repository = object(entry, path, ["owner", "name"]);
        return { owner: text(repository.owner, `${path}.owner`), name: text(repository.name, `${path}.name`) };
      }),
      issueTracker: github.issueTracker === null ? null : parseIssueTracker(github.issueTracker),
    },
    urlMappings: array(root.urlMappings, "$.urlMappings").map((entry, index) => {
      const path = `$.urlMappings[${index}]`;
      const mapping = object(entry, path, ["sourcePrefix", "destinationPrefix"]);
      return {
        sourcePrefix: url(mapping.sourcePrefix, `${path}.sourcePrefix`),
        destinationPrefix: url(mapping.destinationPrefix, `${path}.destinationPrefix`),
      };
    }),
    shortcuts: Object.fromEntries(Platform.map((platform) => [platform, stringMap(shortcuts[platform], `$.shortcuts.${platform}`)])) as DevHudSettingsV1["shortcuts"],
    agents: array(root.agents, "$.agents").map((entry, index) => parseAgent(entry, `$.agents[${index}]`)),
    uploads: {
      provider: enumeration(uploads.provider, "$.uploads.provider", UploadProvider),
      r2: uploads.r2 === null ? null : parseR2(uploads.r2),
    },
  };
}

export function encodeDevHudSettings(value: unknown): Uint8Array {
  return encodeCanonicalSettingsJson(parseDevHudSettings(value));
}

export function decodeDevHudSettings(value: Uint8Array): DevHudSettingsV1 {
  return parseDevHudSettings(validateCanonicalSettingsJson(value));
}

export function canonicalDevHudSettings(value: unknown): string {
  return canonicalizeSettingsJson(parseDevHudSettings(value));
}

function parseDeck(value: unknown, path: string): DevHudSettingsV1["decks"][number] {
  const deck = object(value, path, ["id", "title", "query", "repository", "display", "refreshMinutes", "notifications"]);
  const display = object(deck.display, `${path}.display`, ["groupBy", "showDrafts"]);
  const refresh = integer(deck.refreshMinutes, `${path}.refreshMinutes`, 1, 30);
  if (![1, 5, 15, 30].includes(refresh)) throw new SettingsContractError(`${path}.refreshMinutes`, "must be 1, 5, 15, or 30");
  return {
    id: identifier(deck.id, `${path}.id`),
    title: text(deck.title, `${path}.title`),
    query: text(deck.query, `${path}.query`, true),
    repository: deck.repository === null ? null : text(deck.repository, `${path}.repository`),
    display: {
      groupBy: enumeration(display.groupBy, `${path}.display.groupBy`, ["none", "repository", "author"] as const),
      showDrafts: boolean(display.showDrafts, `${path}.display.showDrafts`),
    },
    refreshMinutes: refresh as 1 | 5 | 15 | 30,
    notifications: array(deck.notifications, `${path}.notifications`).map((item, index) => enumeration(item, `${path}.notifications[${index}]`, NotificationKind)),
  };
}

function parseIssueTracker(value: unknown): NonNullable<DevHudSettingsV1["github"]["issueTracker"]> {
  const path = "$.github.issueTracker";
  const tracker = object(value, path, ["owner", "repository", "labels"]);
  return {
    owner: text(tracker.owner, `${path}.owner`),
    repository: text(tracker.repository, `${path}.repository`),
    labels: array(tracker.labels, `${path}.labels`).map((item, index) => text(item, `${path}.labels[${index}]`)),
  };
}

function parseAgent(value: unknown, path: string): DevHudSettingsV1["agents"][number] {
  const agent = object(value, path, ["id", "enabled", "kind", "mode", "repositoryPrompts", "profileRef"]);
  const profileRef = agent.profileRef === null ? null : text(agent.profileRef, `${path}.profileRef`);
  if (profileRef !== null && !profileRefPattern.test(profileRef)) throw new SettingsContractError(`${path}.profileRef`, "is invalid");
  return {
    id: identifier(agent.id, `${path}.id`),
    enabled: boolean(agent.enabled, `${path}.enabled`),
    kind: enumeration(agent.kind, `${path}.kind`, AgentKind),
    mode: enumeration(agent.mode, `${path}.mode`, AgentMode),
    repositoryPrompts: boolean(agent.repositoryPrompts, `${path}.repositoryPrompts`),
    profileRef,
  };
}

function parseR2(value: unknown): NonNullable<DevHudSettingsV1["uploads"]["r2"]> {
  const path = "$.uploads.r2";
  const r2 = object(value, path, ["profileRef", "bucket", "endpoint", "region", "publicBaseUrl"]);
  const profileRef = text(r2.profileRef, `${path}.profileRef`);
  if (!profileRefPattern.test(profileRef)) throw new SettingsContractError(`${path}.profileRef`, "is invalid");
  return {
    profileRef,
    bucket: text(r2.bucket, `${path}.bucket`),
    endpoint: url(r2.endpoint, `${path}.endpoint`, true),
    region: text(r2.region, `${path}.region`),
    publicBaseUrl: r2.publicBaseUrl === null ? null : url(r2.publicBaseUrl, `${path}.publicBaseUrl`, true),
  };
}

function object(value: unknown, path: string, allowed: readonly string[]): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new SettingsContractError(path, "must be an object");
  const record = value as Record<string, unknown>;
  const allowedSet = new Set(allowed);
  for (const key of Object.keys(record)) if (!allowedSet.has(key)) throw new SettingsContractError(`${path}.${key}`, "unknown field");
  for (const key of allowed) if (!(key in record)) throw new SettingsContractError(`${path}.${key}`, "is required");
  return record;
}

function array(value: unknown, path: string): readonly unknown[] {
  if (!Array.isArray(value)) throw new SettingsContractError(path, "must be an array");
  return value;
}

function text(value: unknown, path: string, allowEmpty = false): string {
  if (typeof value !== "string" || (!allowEmpty && value.length === 0) || value.length > 4096) throw new SettingsContractError(path, "must be a bounded string");
  return value;
}

function identifier(value: unknown, path: string): string {
  const parsed = text(value, path);
  if (!safeDynamicKeyPattern.test(parsed)) throw new SettingsContractError(path, "is invalid");
  return parsed;
}

function integer(value: unknown, path: string, minimum: number, maximum: number): number {
  if (!Number.isInteger(value) || (value as number) < minimum || (value as number) > maximum) throw new SettingsContractError(path, `must be an integer from ${minimum} through ${maximum}`);
  return value as number;
}

function boolean(value: unknown, path: string): boolean {
  if (typeof value !== "boolean") throw new SettingsContractError(path, "must be a boolean");
  return value;
}

function enumeration<const Values extends readonly string[]>(value: unknown, path: string, values: Values): Values[number] {
  if (typeof value !== "string" || !values.includes(value)) throw new SettingsContractError(path, `must be one of ${values.join(", ")}`);
  return value as Values[number];
}

function stringMap(value: unknown, path: string): Readonly<Record<string, string>> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new SettingsContractError(path, "must be an object");
  const result: Record<string, string> = {};
  for (const [key, item] of Object.entries(value)) {
    if (!safeDynamicKeyPattern.test(key) || sensitiveKeyPattern.test(key)) throw new SettingsContractError(`${path}.${key}`, "is not an allowed shortcut action");
    result[key] = text(item, `${path}.${key}`);
  }
  return result;
}

function url(value: unknown, path: string, httpsOnly = false): string {
  const parsed = text(value, path);
  try {
    const candidate = new URL(parsed);
    if (candidate.username || candidate.password || candidate.href.includes("?") || candidate.href.includes("#") || (httpsOnly && candidate.protocol !== "https:")) throw new Error();
    return candidate.toString();
  } catch {
    throw new SettingsContractError(path, httpsOnly ? "must be an HTTPS URL without credentials, query, or fragment" : "must be a URL without credentials, query, or fragment");
  }
}

function rejectSensitiveContent(value: unknown, path: string, seen: WeakSet<object>): void {
  if (typeof value === "string") {
    if (sensitiveValuePatterns.some((pattern) => pattern.test(value))) throw new SettingsContractError(path, "contains secret material");
    return;
  }
  if (value === null || typeof value !== "object") return;
  if (seen.has(value)) throw new SettingsContractError(path, "must not contain cycles");
  seen.add(value);
  for (const [key, item] of Object.entries(value)) {
    if (sensitiveKeyPattern.test(key)) throw new SettingsContractError(`${path}.${key}`, "device-local or secret field is forbidden");
    rejectSensitiveContent(item, Array.isArray(value) ? `${path}[${key}]` : `${path}.${key}`, seen);
  }
  seen.delete(value);
}
