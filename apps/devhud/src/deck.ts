import { GitHubErrorCode, GitHubProviderError, type GitHubDeckPullRequest, type GitHubRate } from "./github-provider.ts";
import { hasPositivePullRequestQualifier, type DeckBuilder, type DevHudSettingsV1 } from "./settings-contract.ts";

export const DeckCacheVersion = 1 as const;
export const DeckResultLimit = 100 as const;
export const DeckLimit = 25 as const;

export interface DeckCache {
  readonly version: typeof DeckCacheVersion;
  readonly deckId: string;
  readonly queryEtag: string | null;
  readonly results: readonly GitHubDeckPullRequest[];
  readonly lastSuccessfulAt: string | null;
  readonly rate: GitHubRate | null;
  readonly failures: number;
  readonly nextRefreshAt: string | null;
  readonly transitionKeys: readonly string[];
}

export type DeckFailure = "token" | "permission" | "query" | "network" | "rate-limit" | "unknown";

export function deckCacheKey(scope: string, deckId: string): string { return `devhud.deck.v${DeckCacheVersion}.${scope}.${deckId}`; }

export function readDeckCache(storage: Pick<Storage, "getItem">, scope: string, deckId: string): DeckCache | null {
  try {
    const value: unknown = JSON.parse(storage.getItem(deckCacheKey(scope, deckId)) ?? "null");
    if (value === null || typeof value !== "object" || Array.isArray(value)) return null;
    const item = value as Record<string, unknown>;
    if (item.version !== DeckCacheVersion || item.deckId !== deckId || !nullableString(item.queryEtag) || !Array.isArray(item.results) || item.results.length > DeckResultLimit || !item.results.every(isDeckPullRequest) || !nullableTimestamp(item.lastSuccessfulAt) || !nullableRate(item.rate) || !nonNegativeInteger(item.failures) || !nullableTimestamp(item.nextRefreshAt) || !Array.isArray(item.transitionKeys) || item.transitionKeys.length > DeckResultLimit * 4 || !item.transitionKeys.every((key) => typeof key === "string")) return null;
    return item as unknown as DeckCache;
  } catch { return null; }
}

export function writeDeckCache(storage: Pick<Storage, "setItem">, scope: string, cache: DeckCache): void {
  try { storage.setItem(deckCacheKey(scope, cache.deckId), JSON.stringify({ ...cache, results: cache.results.slice(0, DeckResultLimit), transitionKeys: cache.transitionKeys.slice(-DeckResultLimit * 4) })); } catch { /* cache is best effort */ }
}

export function clearDeckCache(storage: Pick<Storage, "removeItem">, scope: string, deckId: string): void { try { storage.removeItem(deckCacheKey(scope, deckId)); } catch { /* cache is best effort */ } }

export function classifyDeckFailure(error: unknown): DeckFailure {
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
  const resetAt = rate?.resetAt === null || rate?.resetAt === undefined ? 0 : Date.parse(rate.resetAt);
  return new Date(Math.max(now + exponential * 60_000, retryAt, Number.isFinite(resetAt) ? resetAt : 0)).toISOString();
}

export function deckTransitionKeys(previous: readonly GitHubDeckPullRequest[], next: readonly GitHubDeckPullRequest[]): readonly { readonly kind: "review" | "checks" | "merged" | "closed"; readonly key: string; readonly pullRequest: GitHubDeckPullRequest }[] {
  const before = new Map(previous.map((pullRequest) => [pullRequest.nodeId, pullRequest]));
  const transitions: { kind: "review" | "checks" | "merged" | "closed"; key: string; pullRequest: GitHubDeckPullRequest }[] = [];
  for (const pullRequest of next) {
    const old = before.get(pullRequest.nodeId);
    if (old === undefined) continue;
    if (old.reviewDecision !== pullRequest.reviewDecision) transitions.push({ kind: "review", key: `${pullRequest.nodeId}:review:${pullRequest.reviewDecision}`, pullRequest });
    if (checkSignature(old) !== checkSignature(pullRequest)) transitions.push({ kind: "checks", key: `${pullRequest.nodeId}:checks:${checkSignature(pullRequest)}`, pullRequest });
    if (old.state !== "merged" && pullRequest.state === "merged") transitions.push({ kind: "merged", key: `${pullRequest.nodeId}:merged`, pullRequest });
    if (old.state !== "closed" && pullRequest.state === "closed") transitions.push({ kind: "closed", key: `${pullRequest.nodeId}:closed`, pullRequest });
  }
  return transitions;
}

function checkSignature(pullRequest: GitHubDeckPullRequest): string { return JSON.stringify([pullRequest.checkRollup.state, pullRequest.checkRollup.contexts]); }

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
const ownedToken: Record<BuilderField, RegExp> = {
  repository: /(^|\s)repo:(?:"(?:\\.|[^"\\])*"|\S+)/iu,
  author: /(^|\s)author:(?:"(?:\\.|[^"\\])*"|\S+)/iu,
  review: /(^|\s)review:(?:approved|changes_requested|required)/iu,
  label: /(^|\s)label:(?:"(?:\\.|[^"\\])*"|\S+)/iu,
  state: /(^|\s)is:(?:open|closed|merged)/iu,
};

/** Applies only the chosen builder token; all unowned syntax and whitespace are retained byte-for-byte. */
export function applyDeckBuilder(query: string, field: BuilderField, value: DeckBuilder[BuilderField]): string {
  const expression = ownedToken[field];
  const found = expression.exec(query);
  if (found !== null) {
    if (value === null) return `${query.slice(0, found.index)}${found[1]}${query.slice(found.index + found[0].length)}`;
    return `${query.slice(0, found.index)}${found[1]}${qualifier[field](value as never)}${query.slice(found.index + found[0].length)}`;
  }
  if (value === null) return query;
  return `${query}${query.length === 0 || /\s$/u.test(query) ? "" : " "}${qualifier[field](value as never)}`;
}

export function parseDeckBuilder(query: string): DeckBuilder | null {
  const repository = readToken(query, ownedToken.repository, "repo:");
  const author = readToken(query, ownedToken.author, "author:");
  const label = readToken(query, ownedToken.label, "label:");
  const reviewToken = readToken(query, ownedToken.review, "review:");
  const stateToken = readToken(query, ownedToken.state, "is:");
  if ([repository, author, label, reviewToken, stateToken].every((value) => value === null)) return null;
  return { repository, author, label, review: reviewToken === "changes_requested" ? "changes-requested" : reviewToken === "approved" || reviewToken === "required" ? reviewToken : null, state: stateToken === "open" || stateToken === "closed" || stateToken === "merged" ? stateToken : null };
}

export function validateDeckQuery(query: string): boolean { return query.trim().length > 0 && hasPositivePullRequestQualifier(query); }
function readToken(query: string, expression: RegExp, prefix: string): string | null { const match = expression.exec(query); return match === null ? null : unquote(match[0].trim().slice(prefix.length)); }
function quoteQualifier(value: string): string { return /\s/u.test(value) ? `"${value.replaceAll("\\", "\\\\").replaceAll("\"", "\\\"")}"` : value; }
function unquote(value: string): string { return value.startsWith("\"") && value.endsWith("\"") ? value.slice(1, -1).replaceAll(/\\(.)/gu, "$1") : value; }
