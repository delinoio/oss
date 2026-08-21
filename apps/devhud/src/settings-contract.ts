import {
  assertUuidV7,
  canonicalizeSettingsJson,
  encodeCanonicalSettingsJson,
  validateCanonicalSettingsJson,
} from "@delinoio/devhud-api-client";
import { SettingsTextLimit } from "./contract-limits.ts";
import { ShortcutContractError, defaultDesktopShortcutBindings, parseDesktopShortcutBindings, type DesktopShortcutBindings } from "./shortcuts";
import { configuredChromeOrigins, mappingAcceptsOrigin, parseUrlPattern, type UrlRepositoryMapping } from "./url-mapping";

export const LegacySettingsSchemaVersion = 1 as const;
export const PreviousSettingsSchemaVersion = 2 as const;
/** Version 3 was released independently by the Deck and shortcut branches. */
export const CollidingSettingsSchemaVersion = 3 as const;
export const SettingsSchemaVersion = 4 as const;
export const MaximumUrlRepositoryMappings = 100;

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
  readonly urlMappings: readonly UrlRepositoryMapping[];
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

/** Compatibility alias for callers written before the synchronized schema v4 migration. */
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
const MaximumNativeMessagingJsonBytes = 256 * 1024;
const NativeMessagingEnvelopeRequestId = "01900000-0000-7000-8000-000000000000";

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
  const sourceSchemaVersion = integer(root.schemaVersion, "$.schemaVersion", LegacySettingsSchemaVersion, SettingsSchemaVersion);
  const legacy = sourceSchemaVersion === LegacySettingsSchemaVersion;

  const appearance = object(root.appearance, "$.appearance", ["theme", "language"]);
  const decks = array(root.decks, "$.decks");
  if (decks.length > 25) throw new SettingsContractError("$.decks", "must contain at most 25 entries");
  const previous = sourceSchemaVersion === PreviousSettingsSchemaVersion || sourceSchemaVersion === CollidingSettingsSchemaVersion && !hasCurrentDeckShape(decks[0]);
  const legacyShortcuts = sourceSchemaVersion < SettingsSchemaVersion;
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
  const structuredMappings = sourceSchemaVersion === SettingsSchemaVersion || sourceSchemaVersion === CollidingSettingsSchemaVersion && hasStructuredURLMappingShape(root.urlMappings);
  const urlMappings = structuredMappings ? parseStructuredMappings(root.urlMappings) : parseLegacyMappings(root.urlMappings);
  if (urlMappings.length > MaximumUrlRepositoryMappings) throw new SettingsContractError("$.urlMappings", `must contain at most ${MaximumUrlRepositoryMappings} entries`);
  if (new Set(urlMappings.map((mapping) => mapping.id)).size !== urlMappings.length) throw new SettingsContractError("$.urlMappings", "must not contain duplicate mapping IDs");

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
    // Prefix mappings cannot identify a repository or credential. They are deliberately
    // discarded rather than guessed during the approved v1/v2 -> v3 migration.
    urlMappings,
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
  for (const [index, mapping] of parsed.urlMappings.entries()) validateGitHubProfileRef(mapping.credentialProfileRef, `$.urlMappings[${index}].credentialProfileRef`, githubProfileIds);
  if (!nativeMessagingConfigurationEnvelopesFit(parsed.urlMappings)) {
    throw new SettingsContractError("$.urlMappings", "projected Native Messaging configuration must fit the 256 KiB response envelope");
  }
  return parsed;
}

function nativeMessagingConfigurationEnvelopesFit(mappings: readonly UrlRepositoryMapping[]): boolean {
  const configuration = { origins: configuredChromeOrigins(mappings), language: "en" };
  const envelopes = [
    { version: 1, schema_version: 1, request_id: NativeMessagingEnvelopeRequestId, accepted: true, error: null, payload: configuration },
    { version: 1, schema_version: 1, request_id: NativeMessagingEnvelopeRequestId, ok: true, state: "accepted", payload: configuration },
  ];
  return envelopes.every((envelope) => new TextEncoder().encode(JSON.stringify(envelope)).byteLength <= MaximumNativeMessagingJsonBytes);
}

function parseLegacyMappings(value: unknown): readonly [] {
  for (const [index, entry] of array(value, "$.urlMappings").entries()) {
    const path = `$.urlMappings[${index}]`;
    const mapping = object(entry, path, ["sourcePrefix", "destinationPrefix"]);
    url(mapping.sourcePrefix, `${path}.sourcePrefix`);
    url(mapping.destinationPrefix, `${path}.destinationPrefix`);
  }
  return [];
}

function parseStructuredMappings(value: unknown): readonly UrlRepositoryMapping[] {
  return array(value, "$.urlMappings").map(parseUrlMapping);
}

function parseUrlMapping(value: unknown, index: number): UrlRepositoryMapping {
  const path = `$.urlMappings[${index}]`;
  const mapping = object(value, path, ["id", "pattern", "repository", "credentialProfileRef", "priority", "chromeOrigin", "updatedAt"]);
  const id = text(mapping.id, `${path}.id`);
  try { assertUuidV7(id); } catch { throw new SettingsContractError(`${path}.id`, "must be a canonical lowercase RFC 9562 UUID v7"); }
  const pattern = text(mapping.pattern, `${path}.pattern`);
  try { parseUrlPattern(pattern); } catch (error) { throw new SettingsContractError(`${path}.pattern`, error instanceof Error ? error.message : "is invalid"); }
  const repository = object(mapping.repository, `${path}.repository`, ["owner", "name"]);
  const credentialProfileRef = text(mapping.credentialProfileRef, `${path}.credentialProfileRef`);
  if (!profileRefPattern.test(credentialProfileRef)) throw new SettingsContractError(`${path}.credentialProfileRef`, "is invalid");
  const updatedAt = text(mapping.updatedAt, `${path}.updatedAt`);
  if (!Number.isFinite(Date.parse(updatedAt)) || new Date(updatedAt).toISOString() !== updatedAt) throw new SettingsContractError(`${path}.updatedAt`, "must be a canonical UTC timestamp");
  const normalizedChromeOrigin = mapping.chromeOrigin === null ? null : chromeOrigin(mapping.chromeOrigin, `${path}.chromeOrigin`);
  if (normalizedChromeOrigin !== null && !mappingAcceptsOrigin({ pattern }, normalizedChromeOrigin)) {
    throw new SettingsContractError(`${path}.chromeOrigin`, "must be covered by the mapping pattern scheme, host, and port");
  }
  return {
    id,
    pattern,
    repository: { owner: text(repository.owner, `${path}.repository.owner`), name: text(repository.name, `${path}.repository.name`) },
    credentialProfileRef,
    priority: integer(mapping.priority, `${path}.priority`, -1_000_000, 1_000_000),
    chromeOrigin: normalizedChromeOrigin,
    updatedAt,
  };
}

export function encodeDevHudSettings(value: unknown): Uint8Array {
  return encodeCanonicalSettingsJson(parseDevHudSettings(value));
}

export function decodeDevHudSettings(value: Uint8Array): DevHudSettingsV1 {
  return decodeDevHudSettingsSnapshot(value).settings;
}

export function decodeDevHudSettingsSnapshot(value: Uint8Array): { readonly sourceSchemaVersion: 1 | 2 | 3 | 4; readonly settings: DevHudSettingsV1 } {
  const canonical = validateCanonicalSettingsJson(value);
  const settings = parseDevHudSettings(canonical);
  const sourceSchemaVersion = (canonical as { readonly schemaVersion: unknown }).schemaVersion;
  if (sourceSchemaVersion !== LegacySettingsSchemaVersion && sourceSchemaVersion !== PreviousSettingsSchemaVersion && sourceSchemaVersion !== CollidingSettingsSchemaVersion && sourceSchemaVersion !== SettingsSchemaVersion) throw new SettingsContractError("$.schemaVersion", "is unsupported");
  return { sourceSchemaVersion, settings };
}

export function decodeVersionedDevHudSettings(value: Uint8Array, envelopeSchemaVersion: number): DevHudSettingsV1 {
  const decoded = validateCanonicalSettingsJson(value);
  const embeddedSchemaVersion = decoded !== null && typeof decoded === "object" && !Array.isArray(decoded) ? (decoded as Record<string, unknown>).schemaVersion : undefined;
  if (embeddedSchemaVersion !== undefined && embeddedSchemaVersion !== envelopeSchemaVersion) {
    throw new SettingsContractError("$.schemaVersion", "must match the snapshot envelope schema version");
  }
  return parseDevHudSettings(decoded);
}

export function canonicalDevHudSettings(value: unknown): string {
  return canonicalizeSettingsJson(parseDevHudSettings(value));
}

function hasCurrentDeckShape(value: unknown): boolean {
  return value !== null && typeof value === "object" && !Array.isArray(value) && "name" in value;
}

function hasStructuredURLMappingShape(value: unknown): boolean {
  const mappings = array(value, "$.urlMappings");
  return mappings.some((mapping) => mapping !== null && typeof mapping === "object" && !Array.isArray(mapping) && "id" in mapping);
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
  if (previous && deck.repository === null) throw new SettingsContractError(`${path}.repository`, "must be selected when a credential profile is selected");
  const rawQuery = text(deck.query, `${path}.query`, true);
  const legacyRepository = previous && deck.repository !== null ? text(deck.repository, `${path}.repository`) : null;
  let query = hasPositivePullRequestQualifier(rawQuery) ? rawQuery : appendDeckQualifier(rawQuery, "is:pr");
  if (legacyRepository !== null && !hasExactRepositoryQualifier(query, legacyRepository)) query = appendDeckQualifier(query, `repo:${legacyRepository}`);
  if (!legacy && !hasRepositoryQualifier(query)) throw new SettingsContractError(`${path}.query`, "must contain a repository qualifier when a credential profile is selected");
  if (!hasPositivePullRequestQualifier(query)) throw new SettingsContractError(`${path}.query`, "must contain a standalone positive is:pr qualifier");
  const builder = legacy ? null : previous ? deckBuilderProjection(query) : parseDeckBuilder(deck.builder, `${path}.builder`);
  if (!legacy && !previous && builder !== null && !sameDeckBuilder(builder, deckBuilderProjection(query))) {
    throw new SettingsContractError(`${path}.builder`, "must be the lossless projection of the query");
  }
  const notificationValues = array(deck.notifications, `${path}.notifications`).map((item, index) => enumeration(item, `${path}.notifications[${index}]`, NotificationKind));
  if (!legacy && !previous && new Set(notificationValues).size !== notificationValues.length) throw new SettingsContractError(`${path}.notifications`, "must contain unique values");
  const notifications = legacy || previous ? [...new Set(notificationValues)] : notificationValues;
  const name = previous || legacy ? text(deck.title, `${path}.title`).trim() : text(deck.name, `${path}.name`);
  if (name.trim() !== name || name.length === 0) throw new SettingsContractError(`${path}.name`, "must be a trimmed nonblank string");
  return [{
    id,
    name,
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
  const branches = deckQueryBranches(query);
  return branches !== null && branches.every((branch) => branch.hasPullRequestQualifier);
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

export const DeckRepositoryLimit = 10 as const;
export const GitHubSearchQueryTextLimit = 256 as const;
export const GitHubSearchQueryOperatorLimit = 5 as const;
const DeckQueryBranchLimit = 100 as const;
const githubOwnerIdentifier = /^[A-Za-z0-9-]{1,39}$/u;
const githubRepositoryIdentifier = /^[A-Za-z0-9._-]{1,100}$/u;

function isGitHubOwnerIdentifier(value: string): boolean {
  return githubOwnerIdentifier.test(value) && !value.startsWith("-") && !value.endsWith("-") && !value.includes("--");
}

/** Returns null unless every Boolean branch is scoped to valid GitHub repositories. */
export function deckRepositories(query: string): readonly DeckRepositoryRef[] | null {
  const branches = deckQueryBranches(query);
  if (branches === null || branches.some((branch) => branch.repositories.size === 0)) return null;
  const repositories = new Map<string, DeckRepositoryRef>();
  for (const branch of branches) {
    for (const [key, repository] of branch.repositories) {
      repositories.set(key, repository);
      if (repositories.size > DeckRepositoryLimit) return null;
    }
  }
  return [...repositories.values()];
}

export interface DeckQueryToken { readonly value: string; readonly start: number; readonly end: number; }

/** Returns the editable builder fields only when the query contains builder-owned qualifiers. */
export function deckBuilderProjection(query: string): DeckBuilder | null {
  if (hasDeckBooleanQuerySyntax(query)) return null;
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
  const source = hasDeckBooleanQuerySyntax(query) ? `(${query})` : query;
  return `${source}${source.length === 0 || /\s$/u.test(source) ? "" : " "}${qualifier}`;
}

interface DeckQueryBranch {
  readonly repositories: ReadonlyMap<string, DeckRepositoryRef>;
  readonly hasPullRequestQualifier: boolean;
}

type DeckBooleanToken = { readonly kind: "term"; readonly value: string } | { readonly kind: "open" | "close" | "not" };
const deckQueryWhitespace = /[\u0009-\u000D\u0020\u00A0\u1680\u2000-\u200A\u2028\u2029\u202F\u205F\u3000\uFEFF]/u;

export function hasDeckBooleanQuerySyntax(query: string): boolean {
  const tokens = deckBooleanTokens(query);
  return tokens !== null && tokens.some((token) => token.kind !== "term" || token.value.toLowerCase() === "and" || token.value.toLowerCase() === "or");
}

/** Matches GitHub Search's text and Boolean-operator limits without charging qualifiers. */
export function hasGitHubSearchQueryLimits(query: string): boolean {
  let operators = 0;
  let excludedLength = 0;
  for (const token of deckQueryTokens(query)) {
    const normalized = token.value.toLowerCase();
    if (normalized === "and" || normalized === "or" || normalized === "not") {
      operators += 1;
      excludedLength += token.end - token.start;
    } else if (isGitHubSearchQualifier(token.value)) {
      excludedLength += token.end - token.start;
    }
  }
  return operators <= GitHubSearchQueryOperatorLimit && query.length - excludedLength <= GitHubSearchQueryTextLimit;
}

function isGitHubSearchQualifier(value: string): boolean {
  const source = value.startsWith("-") ? value.slice(1) : value;
  const separator = source.indexOf(":");
  return separator > 0 && /^[A-Za-z][A-Za-z0-9-]*$/u.test(source.slice(0, separator));
}

/** Parses GitHub's Boolean search grammar to prove every reachable branch is repository-scoped. */
function deckQueryBranches(query: string): readonly DeckQueryBranch[] | null {
  const tokens = deckBooleanTokens(query);
  if (tokens === null || tokens.length === 0) return null;
  let index = 0;
  const peek = () => tokens[index];
  const isOperator = (token: DeckBooleanToken | undefined, value: "and" | "or") => token?.kind === "term" && token.value.toLowerCase() === value;
  const isPrimary = (token: DeckBooleanToken | undefined) => token?.kind === "open" || token?.kind === "not" || token?.kind === "term" && !isOperator(token, "and") && !isOperator(token, "or");
  const combineAnd = (left: readonly DeckQueryBranch[], right: readonly DeckQueryBranch[]): readonly DeckQueryBranch[] | null => {
    const combined: DeckQueryBranch[] = [];
    for (const leftBranch of left) for (const rightBranch of right) {
      const repositories = new Map(leftBranch.repositories);
      for (const [key, repository] of rightBranch.repositories) repositories.set(key, repository);
      combined.push({ repositories, hasPullRequestQualifier: leftBranch.hasPullRequestQualifier || rightBranch.hasPullRequestQualifier });
      if (combined.length > DeckQueryBranchLimit) return null;
    }
    return combined;
  };
  const parsePrimary = (): readonly DeckQueryBranch[] | null => {
    const token = peek();
    if (token?.kind === "not") {
      index += 1;
      if (parsePrimary() === null) return null;
      return [{ repositories: new Map(), hasPullRequestQualifier: false }];
    }
    if (token?.kind === "open") {
      index += 1;
      const nested = parseOr();
      if (nested === null || peek()?.kind !== "close") return null;
      index += 1;
      return nested;
    }
    if (token?.kind !== "term" || isOperator(token, "and") || isOperator(token, "or")) return null;
    index += 1;
    const repository = deckRepositoryQualifier(token.value);
    if (repository === undefined) return null;
    const repositories = new Map<string, DeckRepositoryRef>();
    if (repository !== null) repositories.set(`${repository.owner}/${repository.name}`.toLowerCase(), repository);
    return [{ repositories, hasPullRequestQualifier: token.value.toLowerCase() === "is:pr" }];
  };
  const parseAnd = (): readonly DeckQueryBranch[] | null => {
    let result = parsePrimary();
    if (result === null) return null;
    while (true) {
      if (isOperator(peek(), "and")) index += 1;
      else if (!isPrimary(peek())) break;
      const next = parsePrimary();
      if (next === null) return null;
      result = combineAnd(result, next);
      if (result === null) return null;
    }
    return result;
  };
  const parseOr = (): readonly DeckQueryBranch[] | null => {
    let result = parseAnd();
    if (result === null) return null;
    while (isOperator(peek(), "or")) {
      index += 1;
      const next = parseAnd();
      if (next === null || result.length + next.length > DeckQueryBranchLimit) return null;
      result = [...result, ...next];
    }
    return result;
  };
  const branches = parseOr();
  return branches === null || index !== tokens.length ? null : branches;
}

/** Returns undefined for invalid positive repo qualifiers and null for non-repository terms. */
function deckRepositoryQualifier(value: string): DeckRepositoryRef | null | undefined {
  if (value.slice(0, "repo:".length).toLowerCase() !== "repo:") return null;
  const repositoryValue = value.slice("repo:".length);
  const separator = repositoryValue.indexOf("/");
  if (separator < 1 || separator !== repositoryValue.lastIndexOf("/") || separator === repositoryValue.length - 1) return undefined;
  const repository = { owner: repositoryValue.slice(0, separator), name: repositoryValue.slice(separator + 1) };
  return isGitHubOwnerIdentifier(repository.owner) && githubRepositoryIdentifier.test(repository.name) ? repository : undefined;
}

function deckBooleanTokens(query: string): readonly DeckBooleanToken[] | null {
  const tokens: DeckBooleanToken[] = [];
  let index = 0;
  while (index < query.length) {
    const character = query[index] as string;
    if (deckQueryWhitespace.test(character)) { index += 1; continue; }
    if (character === "(") { tokens.push({ kind: "open" }); index += 1; continue; }
    if (character === ")") { tokens.push({ kind: "close" }); index += 1; continue; }
    let value = "";
    let quoted = false;
    let escaped = false;
    while (index < query.length) {
      const next = query[index] as string;
      if (escaped) { value += next; escaped = false; index += 1; continue; }
      if (quoted) {
        value += next;
        if (next === "\\") escaped = true;
        else if (next === "\"") quoted = false;
        index += 1;
        continue;
      }
      if (next === "\"") { value += next; quoted = true; index += 1; continue; }
      if (deckQueryWhitespace.test(next) || next === "(" || next === ")") break;
      value += next;
      index += 1;
    }
    if (quoted || value.length === 0) return null;
    tokens.push(value.toLowerCase() === "not" ? { kind: "not" } : { kind: "term", value });
  }
  return tokens;
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
    if (token.length === 0 && !quoted && !deckQueryWhitespace.test(character)) start = index;
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
    } else if (deckQueryWhitespace.test(character)) {
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
  if (typeof value !== "string" || (!allowEmpty && value.length === 0) || value.length > SettingsTextLimit) throw new SettingsContractError(path, "must be a bounded string");
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
  if (!legacy && value !== null && typeof value === "object" && !Array.isArray(value) && Object.keys(value).length === 0) throw new SettingsContractError("$.shortcuts.desktop", "malformed");
  try {
    return parseDesktopShortcutBindings(value);
  } catch (error) {
    if (!legacy) {
      const detail = error instanceof ShortcutContractError ? error.code : "malformed";
      throw new SettingsContractError("$.shortcuts.desktop", detail);
    }
    try {
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

function chromeOrigin(value: unknown, path: string): string {
  const parsed = text(value, path);
  try {
    const candidate = new URL(parsed);
    if (candidate.protocol !== "http:" && candidate.protocol !== "https:") throw new Error();
    if (candidate.username || candidate.password || candidate.search || candidate.hash || candidate.pathname !== "/" || candidate.hostname.includes("*")) throw new Error();
    return candidate.origin;
  } catch { throw new SettingsContractError(path, "must be a concrete HTTP(S) origin without credentials, path, query, or fragment"); }
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
    const isContractedShortcutAction = path === "$.shortcuts.desktop" && ["shell.command-palette", "realqa.capture.display", "realqa.capture.active-window", "realqa.capture.all-displays", "realqa.capture.selection", "realqa.capture.toolbar"].includes(key);
    if (!isContractedShortcutAction && sensitiveKeyPattern.test(key)) throw new SettingsContractError(`${path}.${key}`, "device-local or secret field is forbidden");
    rejectSensitiveContent(item, Array.isArray(value) ? `${path}[${key}]` : `${path}.${key}`, seen);
  }
  seen.delete(value);
}
