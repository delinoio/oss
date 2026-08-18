import {
  assertUuidV7,
  canonicalizeSettingsJson,
  encodeCanonicalSettingsJson,
  validateCanonicalSettingsJson,
} from "@delinoio/devhud-api-client";

export const LegacySettingsSchemaVersion = 1 as const;
export const PreviousSettingsSchemaVersion = 2 as const;
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
export type DeckReviewFilter = "approved" | "changes-requested" | "required";
export type DeckPullRequestState = "open" | "closed" | "merged";

export interface DeckBuilder {
  readonly repository: string | null;
  readonly author: string | null;
  readonly review: DeckReviewFilter | null;
  readonly label: string | null;
  readonly state: DeckPullRequestState | null;
}

export interface DevHudSettings {
  readonly schemaVersion: typeof SettingsSchemaVersion;
  readonly appearance: { readonly theme: Theme; readonly language: Language };
  readonly decks: readonly {
    readonly id: string;
    readonly name: string;
    readonly query: string;
    readonly builder: DeckBuilder | null;
    readonly profileRef: string;
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

/** Compatibility alias for callers written before the synchronized schema v3 migration. */
export type DevHudSettingsV1 = DevHudSettings;

export const defaultDevHudSettings: DevHudSettingsV1 = Object.freeze<DevHudSettingsV1>({
  schemaVersion: SettingsSchemaVersion,
  appearance: { theme: "system", language: "system" },
  decks: [],
  github: { profiles: [], pendingPatRemovals: [], repositories: [], issueTracker: null },
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
  const previous = schemaVersion === PreviousSettingsSchemaVersion;

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
  const parsedDecks = decks.flatMap((entry, index) => parseDeck(entry, `$.decks[${index}]`, legacy, previous));
  if (new Set(parsedDecks.map((deck) => deck.id)).size !== parsedDecks.length) throw new SettingsContractError("$.decks", "must contain unique IDs");

  const parsed: DevHudSettingsV1 = {
    schemaVersion: SettingsSchemaVersion,
    appearance: {
      theme: enumeration(appearance.theme, "$.appearance.theme", Theme),
      language: enumeration(appearance.language, "$.appearance.language", Language),
    },
    decks: parsedDecks,
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
    shortcuts: Object.fromEntries(Platform.map((platform) => [platform, stringMap(shortcuts[platform], `$.shortcuts.${platform}`)])) as DevHudSettingsV1["shortcuts"],
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
  const embeddedSchemaVersion = decoded !== null && typeof decoded === "object" && !Array.isArray(decoded) ? (decoded as Record<string, unknown>).schemaVersion : undefined;
  // A v2 service can return a v3 canonical body during a rolling client/server upgrade.
  // The body is still fully validated as v3 and the next replacement uses envelope v3.
  const rollingUpgrade = envelopeSchemaVersion === PreviousSettingsSchemaVersion && embeddedSchemaVersion === SettingsSchemaVersion;
  if (embeddedSchemaVersion !== undefined && embeddedSchemaVersion !== envelopeSchemaVersion && !rollingUpgrade) {
    throw new SettingsContractError("$.schemaVersion", "must match the snapshot envelope schema version");
  }
  return parseDevHudSettings(decoded);
}

export function canonicalDevHudSettings(value: unknown): string {
  return canonicalizeSettingsJson(parseDevHudSettings(value));
}

function parseDeck(value: unknown, path: string, legacy: boolean, previous: boolean): readonly DevHudSettingsV1["decks"][number][] {
  const deck = object(value, path, legacy ? ["id", "title", "query", "repository", "display", "refreshMinutes", "notifications"] : previous ? ["id", "title", "query", "repository", "profileRef", "display", "refreshMinutes", "notifications"] : ["id", "name", "query", "builder", "profileRef", "display", "refreshMinutes", "notifications"]);
  const display = object(deck.display, `${path}.display`, ["groupBy", "showDrafts"]);
  const refresh = integer(deck.refreshMinutes, `${path}.refreshMinutes`, 1, 30);
  if (![1, 5, 15, 30].includes(refresh)) throw new SettingsContractError(`${path}.refreshMinutes`, "must be 1, 5, 15, or 30");
  const id = text(deck.id, `${path}.id`);
  try {
    assertUuidV7(id);
  } catch {
    throw new SettingsContractError(`${path}.id`, "must be a canonical lowercase RFC 9562 UUID v7");
  }
  const profileRef = legacy ? null : parseProfileRef(deck.profileRef, `${path}.profileRef`);
  // v1/v2 entries without a local credential are deliberately removed during migration.
  if (profileRef === null) {
    if (legacy || previous) return [];
    throw new SettingsContractError(`${path}.profileRef`, "must select a local GitHub credential profile");
  }
  const rawQuery = text(deck.query, `${path}.query`, true);
  const legacyRepository = previous && deck.repository !== null ? text(deck.repository, `${path}.repository`) : null;
  let query = hasPositivePullRequestQualifier(rawQuery) ? rawQuery : appendDeckQualifier(rawQuery, "is:pr");
  if (legacyRepository !== null && !hasExactRepositoryQualifier(query, legacyRepository)) query = appendDeckQualifier(query, `repo:${legacyRepository}`);
  if (!hasPositivePullRequestQualifier(query)) throw new SettingsContractError(`${path}.query`, "must contain a standalone positive is:pr qualifier");
  if (!legacy && !previous && !hasRepositoryQualifier(query)) throw new SettingsContractError(`${path}.query`, "must contain a repository qualifier when a credential profile is selected");
  const builder = legacy ? null : previous ? legacyDeckBuilder(legacyRepository, `${path}.repository`) : parseDeckBuilder(deck.builder, `${path}.builder`);
  if (!legacy && !previous && builder !== null && !sameDeckBuilder(builder, deckBuilderProjection(query))) {
    throw new SettingsContractError(`${path}.builder`, "must be the lossless projection of the query");
  }
  const notificationValues = array(deck.notifications, `${path}.notifications`).map((item, index) => enumeration(item, `${path}.notifications[${index}]`, NotificationKind));
  if (!legacy && !previous && new Set(notificationValues).size !== notificationValues.length) throw new SettingsContractError(`${path}.notifications`, "must contain unique values");
  const notifications = legacy || previous ? [...new Set(notificationValues)] : notificationValues;
  return [{
    id,
    name: previous || legacy ? text(deck.title, `${path}.title`) : text(deck.name, `${path}.name`),
    query,
    builder,
    profileRef,
    display: {
      groupBy: enumeration(display.groupBy, `${path}.display.groupBy`, ["none", "repository", "author"] as const),
      showDrafts: boolean(display.showDrafts, `${path}.display.showDrafts`),
    },
    refreshMinutes: refresh as 1 | 5 | 15 | 30,
    notifications,
  }];
}

function legacyDeckBuilder(value: unknown, path: string): DeckBuilder | null {
  if (value === null) return null;
  return { repository: text(value, path), author: null, review: null, label: null, state: null };
}

function parseDeckBuilder(value: unknown, path: string): DeckBuilder | null {
  if (value === null) return null;
  const builder = object(value, path, ["repository", "author", "review", "label", "state"]);
  return {
    repository: nullableTrimmedText(builder.repository, `${path}.repository`),
    author: nullableTrimmedText(builder.author, `${path}.author`),
    review: builder.review === null ? null : enumeration(builder.review, `${path}.review`, ["approved", "changes-requested", "required"] as const),
    label: nullableTrimmedText(builder.label, `${path}.label`),
    state: builder.state === null ? null : enumeration(builder.state, `${path}.state`, ["open", "closed", "merged"] as const),
  };
}

function sameDeckBuilder(left: DeckBuilder, right: DeckBuilder | null): boolean {
  return right !== null && left.repository === right.repository && left.author === right.author && left.review === right.review && left.label === right.label && left.state === right.state;
}

function nullableTrimmedText(value: unknown, path: string): string | null {
  if (value === null) return null;
  const result = text(value, path);
  if (result.trim() !== result) throw new SettingsContractError(path, "must be trimmed");
  return result;
}

export function hasPositivePullRequestQualifier(query: string): boolean {
  return deckQueryTokens(query).some((token) => token.value.toLowerCase() === "is:pr");
}

export function hasRepositoryQualifier(query: string): boolean {
  const repositories = deckRepositories(query);
  return repositories !== null && repositories.length > 0;
}

function hasExactRepositoryQualifier(query: string, repository: string): boolean {
  const repositories = deckRepositories(query);
  return repositories !== null && repositories.some((item) => `${item.owner}/${item.name}`.toLowerCase() === repository.toLowerCase());
}

export interface DeckRepositoryRef { readonly owner: string; readonly name: string }

/** Returns null when a repository qualifier cannot name one GitHub repository. */
export function deckRepositories(query: string): readonly DeckRepositoryRef[] | null {
  const repositories = new Map<string, DeckRepositoryRef>();
  for (const token of deckQueryTokens(query)) {
    if (token.value.slice(0, "repo:".length).toLowerCase() !== "repo:") continue;
    const value = token.value.slice("repo:".length);
    const separator = value.indexOf("/");
    if (separator < 1 || separator !== value.lastIndexOf("/") || separator === value.length - 1 || /[\s"]/u.test(value)) return null;
    const repository = { owner: value.slice(0, separator), name: value.slice(separator + 1) };
    const key = `${repository.owner}/${repository.name}`.toLowerCase();
    repositories.set(key, repository);
  }
  return [...repositories.values()];
}

export interface DeckQueryToken { readonly value: string; readonly start: number; readonly end: number; }

/** Returns the editable builder fields only when the query contains builder-owned qualifiers. */
export function deckBuilderProjection(query: string): DeckBuilder | null {
  const tokens = deckQueryTokens(query);
  const repository = deckBuilderValue(tokens, "repo:");
  const author = deckBuilderValue(tokens, "author:");
  const label = deckBuilderValue(tokens, "label:");
  const review = deckBuilderValue(tokens, "review:");
  const state = deckBuilderValue(tokens, "is:");
  const normalizedReview: DeckReviewFilter | null = review?.toLowerCase() === "changes_requested" ? "changes-requested" : review?.toLowerCase() === "approved" || review?.toLowerCase() === "required" ? review.toLowerCase() as DeckReviewFilter : null;
  const normalizedState: DeckPullRequestState | null = state?.toLowerCase() === "open" || state?.toLowerCase() === "closed" || state?.toLowerCase() === "merged" ? state.toLowerCase() as DeckPullRequestState : null;
  if ([repository, author, label, normalizedReview, normalizedState].every((value) => value === null)) return null;
  return { repository, author, label, review: normalizedReview, state: normalizedState };
}

export function deckBuilderToken(query: string, prefix: "repo:" | "author:" | "review:" | "label:" | "is:"): DeckQueryToken | null {
  return deckQueryTokens(query).find((token) => token.value.slice(0, prefix.length).toLowerCase() === prefix && builderTokenIsSupported(token.value, prefix)) ?? null;
}

function deckBuilderValue(tokens: readonly DeckQueryToken[], prefix: "repo:" | "author:" | "review:" | "label:" | "is:"): string | null {
  const token = tokens.find((item) => item.value.slice(0, prefix.length).toLowerCase() === prefix && builderTokenIsSupported(item.value, prefix));
  return token === undefined ? null : unquoteDeckQualifier(token.value.slice(prefix.length));
}

function builderTokenIsSupported(value: string, prefix: "repo:" | "author:" | "review:" | "label:" | "is:"): boolean {
  const token = value.slice(prefix.length);
  const normalized = token.toLowerCase();
  return prefix === "review:" ? normalized === "approved" || normalized === "changes_requested" || normalized === "required" : prefix === "is:" ? normalized === "open" || normalized === "closed" || normalized === "merged" : token.length > 0;
}

function unquoteDeckQualifier(value: string): string { return value.startsWith("\"") && value.endsWith("\"") ? value.slice(1, -1).replaceAll(/\\(.)/gu, "$1") : value; }

function appendDeckQualifier(query: string, qualifier: string): string {
  return `${query}${query.length === 0 || /\s$/u.test(query) ? "" : " "}${qualifier}`;
}

/** Splits GitHub search syntax without treating quoted phrases as qualifiers. */
function deckQueryTokens(query: string): readonly DeckQueryToken[] {
  const tokens: DeckQueryToken[] = [];
  let token = "";
  let start = 0;
  let quoted = false;
  let escaped = false;
  const flush = () => {
    if (token.length > 0) tokens.push({ value: token, start, end: start + token.length });
    token = "";
  };
  for (let index = 0; index < query.length; index += 1) {
    const character = query[index] as string;
    if (token.length === 0 && !quoted && !/\s/u.test(character)) start = index;
    if (escaped) {
      token += character;
      escaped = false;
      continue;
    }
    if (quoted) {
      token += character;
      if (character === "\\") escaped = true;
      else if (character === "\"") quoted = false;
      continue;
    }
    if (character === "\"") {
      token += character;
      quoted = true;
    } else if (/\s/u.test(character)) {
      flush();
    } else {
      token += character;
    }
  }
  flush();
  return tokens;
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

function stringMap(value: unknown, path: string): Readonly<Record<string, string>> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new SettingsContractError(path, "must be an object");
  const result: Record<string, string> = {};
  for (const [key, item] of Object.entries(value)) {
    if (!safeDynamicKeyPattern.test(key) || sensitiveKeyPattern.test(key) || prototypeSensitiveKeys.has(key)) throw new SettingsContractError(`${path}.${key}`, "is not an allowed shortcut action");
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
