import { GitHubErrorCode, GitHubProviderError, type GitHubDeckPullRequest, type GitHubRate } from "./github-provider.ts";
import { NativeBridgeError, NativeBridgeErrorCode } from "./native-bridge.ts";
import { deckBuilderProjection, deckBuilderToken, hasGitHubSearchQueryLimits, hasPositivePullRequestQualifier, type DeckBuilder, type DevHudSettingsV1 } from "./settings-contract.ts";

export const DeckCacheVersion = 2 as const;
export const DeckResultLimit = 100 as const;
export const DeckLimit = 25 as const;

export interface DeckPendingNotification {
  readonly key: string;
  readonly kind: "review" | "checks" | "merged" | "closed";
  readonly body: string;
}

export interface DeckCache {
  readonly version: typeof DeckCacheVersion;
  readonly deckId: string;
  readonly query: string;
  readonly queryEtag: string | null;
  readonly results: readonly GitHubDeckPullRequest[];
  readonly lastSuccessfulAt: string | null;
  readonly rate: GitHubRate | null;
  readonly failures: number;
  readonly nextRefreshAt: string | null;
  readonly transitionKeys: readonly string[];
  /** Omitted by earlier v2 caches, which decode as having no pending delivery. */
  readonly pendingNotifications?: readonly DeckPendingNotification[];
}

export type DeckFailure = "token" | "secure-storage" | "permission" | "query" | "network" | "rate-limit" | "incomplete-results" | "unknown";

export function deckCacheKey(scope: string, deckId: string): string { return `devhud.deck.v${DeckCacheVersion}.${scope}.${deckId}`; }

export function readDeckCache(storage: Pick<Storage, "getItem">, scope: string, deckId: string, query: string): DeckCache | null {
  try {
    const value: unknown = JSON.parse(storage.getItem(deckCacheKey(scope, deckId)) ?? "null");
    if (value === null || typeof value !== "object" || Array.isArray(value)) return null;
    const item = value as Record<string, unknown>;
    const pendingNotifications = item.pendingNotifications;
    if (item.version !== DeckCacheVersion || item.deckId !== deckId || item.query !== query || !nullableString(item.queryEtag) || !Array.isArray(item.results) || item.results.length > DeckResultLimit || !item.results.every(isDeckPullRequest) || !nullableTimestamp(item.lastSuccessfulAt) || !nullableRate(item.rate) || !nonNegativeInteger(item.failures) || !nullableTimestamp(item.nextRefreshAt) || !Array.isArray(item.transitionKeys) || item.transitionKeys.length > DeckResultLimit * 4 || !item.transitionKeys.every((key) => typeof key === "string") || pendingNotifications !== undefined && (!Array.isArray(pendingNotifications) || pendingNotifications.length > DeckResultLimit * 4 || !pendingNotifications.every(isDeckPendingNotification))) return null;
    return { ...item, pendingNotifications: pendingNotifications ?? [] } as unknown as DeckCache;
  } catch { return null; }
}

export function writeDeckCache(storage: Pick<Storage, "setItem">, scope: string, cache: DeckCache): void {
  try { storage.setItem(deckCacheKey(scope, cache.deckId), JSON.stringify({ ...cache, results: cache.results.slice(0, DeckResultLimit), transitionKeys: cache.transitionKeys.slice(-DeckResultLimit * 4), pendingNotifications: (cache.pendingNotifications ?? []).slice(-DeckResultLimit * 4) })); } catch { /* cache is best effort */ }
}

export function clearDeckCache(storage: Pick<Storage, "removeItem">, scope: string, deckId: string): void { try { storage.removeItem(deckCacheKey(scope, deckId)); } catch { /* cache is best effort */ } }

export function clearDeckCaches(storage: Pick<Storage, "key" | "length" | "removeItem">, scope: string): void {
  const prefix = `devhud.deck.v${DeckCacheVersion}.${scope}.`;
  try {
    const keys = Array.from({ length: storage.length }, (_, index) => storage.key(index)).filter((key): key is string => key?.startsWith(prefix) ?? false);
    for (const key of keys) storage.removeItem(key);
  } catch { /* cache is best effort */ }
}

export function classifyDeckFailure(error: unknown): DeckFailure {
  if (error instanceof NativeBridgeError && error.code === NativeBridgeErrorCode.StorageFailure) return "secure-storage";
  if (!(error instanceof GitHubProviderError)) return "unknown";
  switch (error.code) {
    case GitHubErrorCode.MissingToken:
    case GitHubErrorCode.InvalidToken: return "token";
    case GitHubErrorCode.MissingScope:
    case GitHubErrorCode.FineGrainedRepositoryRestriction:
    case GitHubErrorCode.OrganizationDenied: return "permission";
    case GitHubErrorCode.InvalidQuery: return "query";
    case GitHubErrorCode.NetworkFailure: return "network";
    case GitHubErrorCode.RateLimited: return "rate-limit";
    default: return "unknown";
  }
}

export function nextDeckRefresh(now: number, refreshMinutes: DevHudSettingsV1["decks"][number]["refreshMinutes"], failures: number, rate: GitHubRate | null): string {
  const exponential = Math.min(30, refreshMinutes * 2 ** Math.min(failures, 5));
  const retryAt = rate?.retryAfterSeconds === null || rate?.retryAfterSeconds === undefined ? 0 : now + rate.retryAfterSeconds * 1000;
  const resetAt = rate?.remaining === 0 && rate.resetAt !== null && rate.resetAt !== undefined ? Date.parse(rate.resetAt) : 0;
  return new Date(Math.max(now + exponential * 60_000, retryAt, Number.isFinite(resetAt) ? resetAt : 0)).toISOString();
}

export function deckTransitionKeys(previous: readonly GitHubDeckPullRequest[], next: readonly GitHubDeckPullRequest[]): readonly { readonly kind: "review" | "checks" | "merged" | "closed"; readonly key: string; readonly pullRequest: GitHubDeckPullRequest }[] {
  const before = new Map(previous.map((pullRequest) => [pullRequest.nodeId, pullRequest]));
  const transitions: { kind: "review" | "checks" | "merged" | "closed"; key: string; pullRequest: GitHubDeckPullRequest }[] = [];
  for (const pullRequest of next) {
    const old = before.get(pullRequest.nodeId);
    if (old === undefined) continue;
    if (old.reviewDecision !== pullRequest.reviewDecision) transitions.push({ kind: "review", key: `${pullRequest.nodeId}:review:${pullRequest.reviewDecision}:${pullRequest.updatedAt}`, pullRequest });
    if (checkSignature(old) !== checkSignature(pullRequest)) transitions.push({ kind: "checks", key: `${pullRequest.nodeId}:checks:${checkSignature(pullRequest)}:${pullRequest.updatedAt}`, pullRequest });
    if (old.state !== "merged" && pullRequest.state === "merged") transitions.push({ kind: "merged", key: `${pullRequest.nodeId}:merged`, pullRequest });
    if (old.state !== "closed" && pullRequest.state === "closed") transitions.push({ kind: "closed", key: `${pullRequest.nodeId}:closed:${pullRequest.updatedAt}`, pullRequest });
  }
  return transitions;
}

function checkSignature(pullRequest: GitHubDeckPullRequest): string { return JSON.stringify([pullRequest.checkRollup.state, pullRequest.checkRollup.contexts]); }

function isDeckPendingNotification(value: unknown): value is DeckPendingNotification {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return false;
  const notification = value as Record<string, unknown>;
  return typeof notification.key === "string" && ["review", "checks", "merged", "closed"].includes(notification.kind as string) && typeof notification.body === "string";
}

function isDeckPullRequest(value: unknown): value is GitHubDeckPullRequest {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return false;
  const pullRequest = value as Record<string, unknown>;
  if (typeof pullRequest.nodeId !== "string" || !positiveInteger(pullRequest.number) || typeof pullRequest.title !== "string" || typeof pullRequest.url !== "string" || typeof pullRequest.draft !== "boolean" || typeof pullRequest.author !== "string" || !["open", "closed", "merged"].includes(pullRequest.state as string) || ![null, "approved", "changes-requested", "required"].includes(pullRequest.reviewDecision as null | string) || !Array.isArray(pullRequest.requestedReviewers) || !pullRequest.requestedReviewers.every((reviewer) => typeof reviewer === "string") || typeof pullRequest.mergeable !== "string" || !Array.isArray(pullRequest.labels) || !pullRequest.labels.every((label) => typeof label === "string") || !timestamp(pullRequest.updatedAt)) return false;
  const repository = pullRequest.repository;
  if (repository === null || typeof repository !== "object" || Array.isArray(repository) || typeof (repository as Record<string, unknown>).owner !== "string" || typeof (repository as Record<string, unknown>).name !== "string") return false;
  const checkRollup = pullRequest.checkRollup;
  return checkRollup !== null && typeof checkRollup === "object" && !Array.isArray(checkRollup) && nullableString((checkRollup as Record<string, unknown>).state) && Array.isArray((checkRollup as Record<string, unknown>).contexts) && ((checkRollup as Record<string, unknown>).contexts as unknown[]).every((context) => context !== null && typeof context === "object" && !Array.isArray(context) && typeof (context as Record<string, unknown>).name === "string" && typeof (context as Record<string, unknown>).state === "string");
}

function positiveInteger(value: unknown): boolean { return typeof value === "number" && Number.isSafeInteger(value) && value > 0; }
function nonNegativeInteger(value: unknown): boolean { return typeof value === "number" && Number.isSafeInteger(value) && value >= 0; }
function nullableString(value: unknown): boolean { return value === null || typeof value === "string"; }
function timestamp(value: unknown): boolean { return typeof value === "string" && Number.isFinite(Date.parse(value)); }
function nullableTimestamp(value: unknown): boolean { return value === null || timestamp(value); }
function nullableRate(value: unknown): boolean {
  if (value === null) return true;
  if (typeof value !== "object" || Array.isArray(value)) return false;
  const rate = value as Record<string, unknown>;
  return nullableNumber(rate.limit) && nullableNumber(rate.remaining) && nullableNumber(rate.used) && nullableTimestamp(rate.resetAt) && nullableString(rate.resource) && nullableNumber(rate.retryAfterSeconds);
}
function nullableNumber(value: unknown): boolean { return value === null || typeof value === "number" && Number.isFinite(value); }

type BuilderField = keyof DeckBuilder;
const qualifier: Record<BuilderField, (value: NonNullable<DeckBuilder[BuilderField]>) => string> = {
  repository: (value) => `repo:${value}`,
  author: (value) => `author:${value}`,
  review: (value) => `review:${value === "changes-requested" ? "changes_requested" : value}`,
  label: (value) => `label:${quoteQualifier(value)}`,
  state: (value) => value === "merged" ? "is:merged" : `is:${value}`,
};
const ownedToken: Record<BuilderField, "repo:" | "author:" | "review:" | "label:" | "is:"> = {
  repository: "repo:",
  author: "author:",
  review: "review:",
  label: "label:",
  state: "is:",
};

/** Applies only the chosen builder token; all unowned syntax and whitespace are retained byte-for-byte. */
export function applyDeckBuilder(query: string, field: BuilderField, value: DeckBuilder[BuilderField]): string {
  const found = deckBuilderToken(query, ownedToken[field]);
  if (found !== null) {
    if (value === null) return `${query.slice(0, found.start)}${query.slice(found.end)}`;
    return `${query.slice(0, found.start)}${qualifier[field](value as never)}${query.slice(found.end)}`;
  }
  if (value === null) return query;
  return `${query}${query.length === 0 || /\s$/u.test(query) ? "" : " "}${qualifier[field](value as never)}`;
}

/** Preserves the null representation when no editable builder qualifiers remain. */
export function updateDeckBuilder(builder: DeckBuilder | null, field: BuilderField, value: DeckBuilder[BuilderField]): DeckBuilder | null {
  const next = { repository: null, author: null, review: null, label: null, state: null, ...builder, [field]: value };
  return Object.values(next).every((item) => item === null) ? null : next;
}

export function parseDeckBuilder(query: string): DeckBuilder | null {
  return deckBuilderProjection(query);
}

export function validateDeckQuery(query: string): boolean { return query.trim().length > 0 && hasPositivePullRequestQualifier(query) && hasGitHubSearchQueryLimits(query); }
function quoteQualifier(value: string): string { return /[\s"\\]/u.test(value) ? `"${value.replaceAll("\\", "\\\\").replaceAll("\"", "\\\"")}"` : value; }
