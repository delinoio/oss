import {
  assertUuidV7,
  canonicalizeSettingsJson,
  encodeCanonicalSettingsJson,
  validateCanonicalSettingsJson,
} from "@delinoio/devhud-api-client";
import { ShortcutContractError, defaultDesktopShortcutBindings, parseDesktopShortcutBindings, type DesktopShortcutBindings } from "./shortcuts";

export const LegacySettingsSchemaVersion = 1 as const;
export const GitHubProfilesSettingsSchemaVersion = 2 as const;
export const SettingsSchemaVersion = 3 as const;

const Theme = ["system", "light", "dark"] as const;
const Language = ["system", "en", "ko"] as const;
const AgentKind = ["codex", "claude-code", "opencode"] as const;
const AgentMode = ["draft", "direct"] as const;
const UploadProvider = ["official", "r2"] as const;
const NotificationKind = ["review", "checks", "merged", "closed"] as const;
const Platform = ["desktop", "ios", "android"] as const;
export const GitHubCredentialKind = ["fine-grained", "classic"] as const;

type Theme = (typeof Theme)[number];
type Language = (typeof Language)[number];
type AgentKind = (typeof AgentKind)[number];
type AgentMode = (typeof AgentMode)[number];
type UploadProvider = (typeof UploadProvider)[number];
type NotificationKind = (typeof NotificationKind)[number];
type Platform = (typeof Platform)[number];
export type GitHubCredentialKind = (typeof GitHubCredentialKind)[number];

export interface DevHudSettings {
  readonly schemaVersion: typeof SettingsSchemaVersion;
  readonly appearance: { readonly theme: Theme; readonly language: Language };
  readonly decks: readonly {
    readonly id: string;
    readonly title: string;
    readonly query: string;
    readonly repository: string | null;
    readonly profileRef: string | null;
    readonly display: { readonly groupBy: "none" | "repository" | "author"; readonly showDrafts: boolean };
    readonly refreshMinutes: 1 | 5 | 15 | 30;
    readonly notifications: readonly NotificationKind[];
  }[];
  readonly github: {
    readonly profiles: readonly { readonly id: string; readonly name: string; readonly kind: GitHubCredentialKind }[];
    readonly pendingPatRemovals: readonly string[];
    readonly repositories: readonly { readonly owner: string; readonly name: string; readonly profileRef: string | null }[];
    readonly issueTracker: { readonly owner: string; readonly repository: string; readonly labels: readonly string[]; readonly profileRef: string | null } | null;
  };
  readonly urlMappings: readonly { readonly sourcePrefix: string; readonly destinationPrefix: string }[];
  readonly shortcuts: Readonly<{ readonly desktop: DesktopShortcutBindings; readonly ios: Readonly<Record<string, never>>; readonly android: Readonly<Record<string, never>> }>;
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

/** Compatibility alias for callers written before the synchronized schema v3 migration. */
export type DevHudSettingsV1 = DevHudSettings;

export const defaultDevHudSettings: DevHudSettingsV1 = Object.freeze<DevHudSettingsV1>({
  schemaVersion: SettingsSchemaVersion,
  appearance: { theme: "system", language: "system" },
  decks: [],
  github: { profiles: [], pendingPatRemovals: [], repositories: [], issueTracker: null },
  urlMappings: [],
  shortcuts: { desktop: defaultDesktopShortcutBindings, ios: {}, android: {} },
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
const prototypeSensitiveKeys = new Set(["__proto__", "constructor", "prototype"]);

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
  const schemaVersion = integer(root.schemaVersion, "$.schemaVersion", LegacySettingsSchemaVersion, SettingsSchemaVersion);
  const legacy = schemaVersion === LegacySettingsSchemaVersion;
  const legacyShortcuts = schemaVersion < SettingsSchemaVersion;

  const appearance = object(root.appearance, "$.appearance", ["theme", "language"]);
  const decks = array(root.decks, "$.decks");
  if (decks.length > 25) throw new SettingsContractError("$.decks", "must contain at most 25 entries");
  const github = object(root.github, "$.github", legacy ? ["repositories", "issueTracker"] : ["profiles", "pendingPatRemovals", "repositories", "issueTracker"]);
  const githubProfiles = legacy ? [] : array(github.profiles, "$.github.profiles").map((entry, index) => parseGitHubProfile(entry, `$.github.profiles[${index}]`));
  if (githubProfiles.length > 25) throw new SettingsContractError("$.github.profiles", "must contain at most 25 entries");
  if (new Set(githubProfiles.map((profile) => profile.id)).size !== githubProfiles.length) throw new SettingsContractError("$.github.profiles", "must contain unique IDs");
  const pendingPatRemovals = legacy ? [] : array(github.pendingPatRemovals, "$.github.pendingPatRemovals").map((entry, index) => parseGitHubProfileId(entry, `$.github.pendingPatRemovals[${index}]`));
  if (pendingPatRemovals.length > 25) throw new SettingsContractError("$.github.pendingPatRemovals", "must contain at most 25 entries");
  if (new Set(pendingPatRemovals).size !== pendingPatRemovals.length) throw new SettingsContractError("$.github.pendingPatRemovals", "must contain unique IDs");
  const githubProfileIds = new Set(githubProfiles.map((profile) => profile.id));
  if (pendingPatRemovals.some((profileId) => githubProfileIds.has(profileId))) throw new SettingsContractError("$.github.pendingPatRemovals", "must not reference an active GitHub profile");
  const shortcuts = object(root.shortcuts, "$.shortcuts", [...Platform]);
  const uploads = object(root.uploads, "$.uploads", ["provider", "r2"]);

  const parsed: DevHudSettingsV1 = {
    schemaVersion: SettingsSchemaVersion,
    appearance: {
      theme: enumeration(appearance.theme, "$.appearance.theme", Theme),
      language: enumeration(appearance.language, "$.appearance.language", Language),
    },
    decks: decks.map((entry, index) => parseDeck(entry, `$.decks[${index}]`, legacy)),
    github: {
      profiles: githubProfiles,
      pendingPatRemovals,
      repositories: array(github.repositories, "$.github.repositories").map((entry, index) => {
        const path = `$.github.repositories[${index}]`;
        const repository = object(entry, path, legacy ? ["owner", "name"] : ["owner", "name", "profileRef"]);
        return { owner: text(repository.owner, `${path}.owner`), name: text(repository.name, `${path}.name`), profileRef: legacy ? null : parseProfileRef(repository.profileRef, `${path}.profileRef`) };
      }),
      issueTracker: github.issueTracker === null ? null : parseIssueTracker(github.issueTracker, legacy),
    },
    urlMappings: array(root.urlMappings, "$.urlMappings").map((entry, index) => {
      const path = `$.urlMappings[${index}]`;
      const mapping = object(entry, path, ["sourcePrefix", "destinationPrefix"]);
      return {
        sourcePrefix: url(mapping.sourcePrefix, `${path}.sourcePrefix`),
        destinationPrefix: url(mapping.destinationPrefix, `${path}.destinationPrefix`),
      };
    }),
    shortcuts: {
      desktop: desktopShortcutMap(shortcuts.desktop, legacyShortcuts),
      ios: legacyShortcuts ? legacyShortcutMap(shortcuts.ios, "$.shortcuts.ios") : emptyShortcutMap(shortcuts.ios, "$.shortcuts.ios"),
      android: legacyShortcuts ? legacyShortcutMap(shortcuts.android, "$.shortcuts.android") : emptyShortcutMap(shortcuts.android, "$.shortcuts.android"),
    },
    agents: array(root.agents, "$.agents").map((entry, index) => parseAgent(entry, `$.agents[${index}]`)),
    uploads: {
      provider: enumeration(uploads.provider, "$.uploads.provider", UploadProvider),
      r2: uploads.r2 === null ? null : parseR2(uploads.r2),
    },
  };
  for (const [index, repository] of parsed.github.repositories.entries()) validateGitHubProfileRef(repository.profileRef, `$.github.repositories[${index}].profileRef`, githubProfileIds);
  validateGitHubProfileRef(parsed.github.issueTracker?.profileRef ?? null, "$.github.issueTracker.profileRef", githubProfileIds);
  for (const [index, deck] of parsed.decks.entries()) validateGitHubProfileRef(deck.profileRef, `$.decks[${index}].profileRef`, githubProfileIds);
  return parsed;
}

export function encodeDevHudSettings(value: unknown): Uint8Array {
  return encodeCanonicalSettingsJson(parseDevHudSettings(value));
}

export function decodeDevHudSettings(value: Uint8Array): DevHudSettingsV1 {
  return parseDevHudSettings(validateCanonicalSettingsJson(value));
}

export function decodeVersionedDevHudSettings(value: Uint8Array, envelopeSchemaVersion: number): DevHudSettingsV1 {
  const decoded = validateCanonicalSettingsJson(value);
  if (decoded !== null && typeof decoded === "object" && !Array.isArray(decoded) && (decoded as Record<string, unknown>).schemaVersion !== envelopeSchemaVersion) {
    throw new SettingsContractError("$.schemaVersion", "must match the snapshot envelope schema version");
  }
  return parseDevHudSettings(decoded);
}

export function canonicalDevHudSettings(value: unknown): string {
  return canonicalizeSettingsJson(parseDevHudSettings(value));
}

function parseDeck(value: unknown, path: string, legacy: boolean): DevHudSettingsV1["decks"][number] {
  const deck = object(value, path, legacy ? ["id", "title", "query", "repository", "display", "refreshMinutes", "notifications"] : ["id", "title", "query", "repository", "profileRef", "display", "refreshMinutes", "notifications"]);
  const display = object(deck.display, `${path}.display`, ["groupBy", "showDrafts"]);
  const refresh = integer(deck.refreshMinutes, `${path}.refreshMinutes`, 1, 30);
  if (![1, 5, 15, 30].includes(refresh)) throw new SettingsContractError(`${path}.refreshMinutes`, "must be 1, 5, 15, or 30");
  const id = text(deck.id, `${path}.id`);
  try {
    assertUuidV7(id);
  } catch {
    throw new SettingsContractError(`${path}.id`, "must be a canonical lowercase RFC 9562 UUID v7");
  }
  const repository = deck.repository === null ? null : text(deck.repository, `${path}.repository`);
  const profileRef = legacy ? null : parseProfileRef(deck.profileRef, `${path}.profileRef`);
  if (repository === null && profileRef !== null) throw new SettingsContractError(`${path}.profileRef`, "must be null when repository is null");
  return {
    id,
    title: text(deck.title, `${path}.title`),
    query: text(deck.query, `${path}.query`, true),
    repository,
    profileRef,
    display: {
      groupBy: enumeration(display.groupBy, `${path}.display.groupBy`, ["none", "repository", "author"] as const),
      showDrafts: boolean(display.showDrafts, `${path}.display.showDrafts`),
    },
    refreshMinutes: refresh as 1 | 5 | 15 | 30,
    notifications: array(deck.notifications, `${path}.notifications`).map((item, index) => enumeration(item, `${path}.notifications[${index}]`, NotificationKind)),
  };
}

function parseIssueTracker(value: unknown, legacy: boolean): NonNullable<DevHudSettingsV1["github"]["issueTracker"]> {
  const path = "$.github.issueTracker";
  const tracker = object(value, path, legacy ? ["owner", "repository", "labels"] : ["owner", "repository", "labels", "profileRef"]);
  return {
    owner: text(tracker.owner, `${path}.owner`),
    repository: text(tracker.repository, `${path}.repository`),
    labels: array(tracker.labels, `${path}.labels`).map((item, index) => text(item, `${path}.labels[${index}]`)),
    profileRef: legacy ? null : parseProfileRef(tracker.profileRef, `${path}.profileRef`),
  };
}

function parseGitHubProfile(value: unknown, path: string): DevHudSettingsV1["github"]["profiles"][number] {
  const profile = object(value, path, ["id", "name", "kind"]);
  const id = parseGitHubProfileId(profile.id, `${path}.id`);
  const name = text(profile.name, `${path}.name`);
  if (name.trim() !== name || name.length > 80) throw new SettingsContractError(`${path}.name`, "must be a trimmed string of at most 80 characters");
  return { id, name, kind: enumeration(profile.kind, `${path}.kind`, GitHubCredentialKind) };
}

function parseGitHubProfileId(value: unknown, path: string): string {
  const id = text(value, path);
  try {
    assertUuidV7(id);
  } catch {
    throw new SettingsContractError(path, "must be a canonical lowercase RFC 9562 UUID v7");
  }
  return id;
}

function parseProfileRef(value: unknown, path: string): string | null {
  if (value === null) return null;
  const profileRef = text(value, path);
  if (!profileRefPattern.test(profileRef)) throw new SettingsContractError(path, "is invalid");
  return profileRef;
}

function validateGitHubProfileRef(value: string | null, path: string, profileIds: ReadonlySet<string>): void {
  if (value !== null && !profileIds.has(value)) throw new SettingsContractError(path, "must reference a configured GitHub profile");
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

function legacyShortcutMap(value: unknown, path: string): Readonly<Record<string, never>> {
  validateLegacyShortcutMap(value, path);
  return {};
}

function emptyShortcutMap(value: unknown, path: string): Readonly<Record<string, never>> {
  validateLegacyShortcutMap(value, path);
  if (Object.keys(value as Record<string, unknown>).length !== 0) throw new SettingsContractError(path, "must be empty");
  return {};
}

function validateLegacyShortcutMap(value: unknown, path: string): void {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new SettingsContractError(path, "must be an object");
  for (const [key, item] of Object.entries(value)) {
    if (!safeDynamicKeyPattern.test(key) || sensitiveKeyPattern.test(key) || prototypeSensitiveKeys.has(key)) throw new SettingsContractError(`${path}.${key}`, "is not an allowed shortcut action");
    text(item, `${path}.${key}`);
  }
}

function desktopShortcutMap(value: unknown, legacy: boolean): DesktopShortcutBindings {
  if (!legacy && value !== null && typeof value === "object" && !Array.isArray(value) && Object.keys(value).length === 0) {
    throw new SettingsContractError("$.shortcuts.desktop", "malformed");
  }
  try {
    return parseDesktopShortcutBindings(value);
  } catch (error) {
    if (!legacy) {
      const detail = error instanceof ShortcutContractError ? error.code : "malformed";
      throw new SettingsContractError("$.shortcuts.desktop", detail);
    }
    try {
      // Version-1 and version-2 snapshots accepted arbitrary string maps. Their raw display
      // chords cannot enter the structured contract, so preserve the snapshot
      // while upgrading its shortcut portion to safe documented defaults.
      validateLegacyShortcutMap(value, "$.shortcuts.desktop");
      return defaultDesktopShortcutBindings;
    } catch {
      const detail = error instanceof ShortcutContractError ? error.code : "malformed";
      throw new SettingsContractError("$.shortcuts.desktop", detail);
    }
  }
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
    const isContractedShortcutAction = path === "$.shortcuts.desktop" && [
      "shell.command-palette",
      "realqa.capture.display",
      "realqa.capture.active-window",
      "realqa.capture.all-displays",
      "realqa.capture.selection",
      "realqa.capture.toolbar",
    ].includes(key);
    if (!isContractedShortcutAction && sensitiveKeyPattern.test(key)) throw new SettingsContractError(`${path}.${key}`, "device-local or secret field is forbidden");
    rejectSensitiveContent(item, Array.isArray(value) ? `${path}[${key}]` : `${path}.${key}`, seen);
  }
  seen.delete(value);
}
