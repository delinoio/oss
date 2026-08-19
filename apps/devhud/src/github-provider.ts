import { SecureSettingKind, type NativeBridgeV1 } from "./native-bridge.ts";
import { hasPositivePullRequestQualifier, type DevHudSettingsV1, type GitHubCredentialKind } from "./settings-contract.ts";
export { ClassicPatCreationUrl, FineGrainedPatCreationUrl } from "./github-links.ts";

export const GitHubProviderId = "github.com" as const;
export const InternalProviderRegistryVersion = 1 as const;
export const InternalProviderRegistryV1 = Object.freeze({ issueTrackers: [GitHubProviderId] as const, pullRequests: [GitHubProviderId] as const });
export const GitHubApiOrigin = "https://api.github.com" as const;
export const GitHubApiVersion = "2026-03-10" as const;

export const GitHubOperation = {
  ValidateCredential: "validate-credential",
  ValidateRepository: "validate-repository",
  ListLabels: "list-labels",
  SearchIssueMarker: "search-issue-marker",
  CreateIssue: "create-issue",
  SearchPullRequests: "search-pull-requests",
  EnrichPullRequests: "enrich-pull-requests",
  GetPullRequest: "get-pull-request",
  OwnsCanonicalUrl: "owns-canonical-url",
} as const;
export type GitHubOperation = (typeof GitHubOperation)[keyof typeof GitHubOperation];

export const GitHubErrorCode = {
  MissingToken: "missing-token",
  InvalidToken: "invalid-token",
  MissingScope: "missing-scope",
  FineGrainedRepositoryRestriction: "fine-grained-repository-restriction",
  OrganizationDenied: "organization-denied",
  RateLimited: "rate-limited",
  NetworkFailure: "network-failure",
  InvalidResponse: "invalid-response",
  AmbiguousWrite: "ambiguous-write",
  InvalidQuery: "invalid-query",
} as const;
export type GitHubErrorCode = (typeof GitHubErrorCode)[keyof typeof GitHubErrorCode];

export interface GitHubRepositoryRef { readonly owner: string; readonly name: string }
export interface GitHubCredential { readonly profileId: string; readonly kind: GitHubCredentialKind; readonly token: string }
export interface GitHubRate { readonly limit: number | null; readonly remaining: number | null; readonly used: number | null; readonly resetAt: string | null; readonly resource: string | null; readonly retryAfterSeconds: number | null }
export interface GitHubResponseMetadata { readonly etag: string | null; readonly rate: GitHubRate }
export interface GitHubPage<T> { readonly items: readonly T[]; readonly nextPage: number | null; readonly notModified: boolean; readonly metadata: GitHubResponseMetadata }
export interface GitHubLabel { readonly name: string; readonly color: string; readonly description: string | null }
export interface GitHubIssue { readonly number: number; readonly title: string; readonly url: string; readonly marker: string; readonly reconciled: boolean }
export interface GitHubPullRequestSummary { readonly nodeId: string; readonly number: number; readonly title: string; readonly url: string; readonly draft: boolean; readonly repository: GitHubRepositoryRef }
export interface GitHubPullRequestDetail extends GitHubPullRequestSummary { readonly body: string | null; readonly author: string; readonly state: string; readonly headSha: string; readonly labels: readonly string[] }
export interface GitHubDeckPullRequest extends GitHubPullRequestSummary {
  readonly author: string;
  readonly state: "open" | "closed" | "merged";
  readonly reviewDecision: "approved" | "changes-requested" | "required" | null;
  readonly requestedReviewers: readonly string[];
  readonly checkRollup: { readonly state: string | null; readonly contexts: readonly { readonly name: string; readonly state: string }[] };
  readonly mergeable: string;
  readonly labels: readonly string[];
  readonly updatedAt: string;
}
export interface GitHubValidation { readonly repository: GitHubRepositoryRef; readonly private: boolean; readonly permissions: { readonly metadata: boolean; readonly pullRequests: boolean; readonly issues: boolean; readonly contents: boolean }; readonly metadata: GitHubResponseMetadata }
export interface GitHubDiagnostic { readonly provider: typeof GitHubProviderId; readonly operation: GitHubOperation; readonly code: GitHubErrorCode; readonly status: number | null; readonly rate: GitHubRate | null }

export class GitHubProviderError extends Error {
  readonly code: GitHubErrorCode;
  readonly operation: GitHubOperation;
  readonly status: number | null;
  readonly rate: GitHubRate | null;

  constructor(code: GitHubErrorCode, operation: GitHubOperation, status: number | null = null, rate: GitHubRate | null = null) {
    super(`${GitHubProviderId}:${operation}:${code}`);
    this.name = "GitHubProviderError";
    this.code = code;
    this.operation = operation;
    this.status = status;
    this.rate = rate;
  }
}

export function githubDiagnostic(error: GitHubProviderError): GitHubDiagnostic {
  return Object.freeze({ provider: GitHubProviderId, operation: error.operation, code: error.code, status: error.status, rate: error.rate });
}

export interface GitHubProvider {
  readonly id: typeof GitHubProviderId;
  validateCredential(credential: GitHubCredential): Promise<GitHubResponseMetadata>;
  validateRepository(credential: GitHubCredential, repository: GitHubRepositoryRef): Promise<GitHubValidation>;
  listLabels(credential: GitHubCredential, repository: GitHubRepositoryRef, options?: { readonly page?: number; readonly etag?: string }): Promise<GitHubPage<GitHubLabel>>;
  searchIssueMarker(credential: GitHubCredential, repository: GitHubRepositoryRef, marker: string): Promise<{ readonly issue: GitHubIssue | null; readonly metadata: GitHubResponseMetadata }>;
  createIssue(credential: GitHubCredential, repository: GitHubRepositoryRef, input: { readonly title: string; readonly body: string; readonly labels: readonly string[]; readonly submissionId: string }): Promise<{ readonly issue: GitHubIssue; readonly metadata: GitHubResponseMetadata }>;
  searchPullRequests(credential: GitHubCredential, query: string, options?: { readonly page?: number; readonly etag?: string }): Promise<GitHubPage<GitHubPullRequestSummary>>;
  enrichPullRequests(credential: GitHubCredential, nodeIds: readonly string[]): Promise<{ readonly items: readonly GitHubDeckPullRequest[]; readonly metadata: GitHubResponseMetadata }>;
  getPullRequest(credential: GitHubCredential, repository: GitHubRepositoryRef, number: number, etag?: string): Promise<{ readonly pullRequest: GitHubPullRequestDetail | null; readonly notModified: boolean; readonly metadata: GitHubResponseMetadata }>;
  ownsCanonicalUrl(url: string): { readonly kind: "issue" | "pull-request"; readonly repository: GitHubRepositoryRef; readonly number: number } | null;
}

interface RequestResult { readonly response: Response; readonly json: unknown; readonly metadata: GitHubResponseMetadata }
interface ProviderOptions { readonly fetch: typeof globalThis.fetch }

export async function readGitHubCredential(bridge: NativeBridgeV1, profile: DevHudSettingsV1["github"]["profiles"][number], scopeId: string): Promise<GitHubCredential> {
  const response = await bridge.request({ operation: "secure.read", setting: { kind: SecureSettingKind.GithubPat, profileId: profile.id, scopeId } });
  if (response.kind !== "secure-value") throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, GitHubOperation.ValidateCredential);
  if (response.value === null || response.value.trim() === "") throw new GitHubProviderError(GitHubErrorCode.MissingToken, GitHubOperation.ValidateCredential);
  return { profileId: profile.id, kind: profile.kind, token: response.value };
}

export function createGitHubProvider({ fetch: fetchImpl }: ProviderOptions): GitHubProvider {
  async function request(operation: GitHubOperation, credential: GitHubCredential, path: string, init: RequestInit = {}, acceptedStatuses: readonly number[] = []): Promise<RequestResult> {
    let response: Response;
    try {
      response = await fetchImpl(`${GitHubApiOrigin}${path}`, {
        ...init,
        headers: {
          Accept: "application/vnd.github+json",
          Authorization: `Bearer ${credential.token}`,
          "X-GitHub-Api-Version": GitHubApiVersion,
          ...init.headers,
        },
      });
    } catch {
      throw new GitHubProviderError(GitHubErrorCode.NetworkFailure, operation);
    }
    const metadata = responseMetadata(response.headers);
    let json: unknown = null;
    if (response.status !== 304 && response.status !== 204) {
      try { json = await response.json(); } catch { if (response.ok) throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, operation, response.status, metadata.rate); }
    }
    if (!response.ok && response.status !== 304 && !acceptedStatuses.includes(response.status)) throw classifyFailure(operation, credential.kind, response, json, metadata.rate);
    return { response, json, metadata };
  }

  async function validateCredential(credential: GitHubCredential): Promise<GitHubResponseMetadata> {
    const result = await request(GitHubOperation.ValidateCredential, credential, "/user");
    validateClassicRepoScope(credential, result, GitHubOperation.ValidateCredential);
    return result.metadata;
  }

  async function listRecentIssueMarker(credential: GitHubCredential, repository: GitHubRepositoryRef, marker: string): Promise<{ readonly issue: GitHubIssue | null; readonly metadata: GitHubResponseMetadata }> {
    const result = await request(GitHubOperation.SearchIssueMarker, credential, `${repositoryPath(repository)}/issues?state=all&sort=created&direction=desc&per_page=100`);
    for (const candidate of array(result.json, GitHubOperation.SearchIssueMarker)) {
      const issue = record(candidate, GitHubOperation.SearchIssueMarker);
      if ("pull_request" in issue || typeof issue.body !== "string" || !issue.body.includes(marker)) continue;
      return { issue: issueValue(issue, marker, true), metadata: result.metadata };
    }
    return { issue: null, metadata: result.metadata };
  }

  return {
    id: GitHubProviderId,
    validateCredential,
    async validateRepository(credential, repository) {
      const result = await request(GitHubOperation.ValidateRepository, credential, repositoryPath(repository));
      validateClassicRepoScope(credential, result, GitHubOperation.ValidateRepository);
      const value = record(result.json, GitHubOperation.ValidateRepository);
      const validations = await Promise.all([
        request(GitHubOperation.ValidateRepository, credential, `${repositoryPath(repository)}/pulls?state=open&per_page=1`),
        request(GitHubOperation.ValidateRepository, credential, `${repositoryPath(repository)}/issues?state=open&per_page=1`),
        request(GitHubOperation.ValidateRepository, credential, `${repositoryPath(repository)}/contents`, {}, [404]),
        ...(credential.kind === "fine-grained" ? [request(GitHubOperation.ValidateRepository, credential, `${repositoryPath(repository)}/issues`, { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" }, [422])] : []),
      ]);
      const contents = validations[2];
      if (contents?.response.status === 404 && value.pushed_at !== null) throw classifyFailure(GitHubOperation.ValidateRepository, credential.kind, contents.response, contents.json, contents.metadata.rate);
      if (credential.kind === "fine-grained" && validations[3]?.response.status !== 422) throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, GitHubOperation.ValidateRepository, validations[3]?.response.status ?? null, validations[3]?.metadata.rate ?? null);
      return {
        repository,
        private: value.private === true,
        permissions: { metadata: true, pullRequests: true, issues: true, contents: true },
        metadata: result.metadata,
      };
    },
    async listLabels(credential, repository, options = {}) {
      const page = positiveInteger(options.page ?? 1);
      const result = await request(GitHubOperation.ListLabels, credential, `${repositoryPath(repository)}/labels?per_page=100&page=${page}`, options.etag ? { headers: { "If-None-Match": options.etag } } : {});
      if (result.response.status === 304) return { items: [], nextPage: null, notModified: true, metadata: result.metadata };
      const items = array(result.json, GitHubOperation.ListLabels).map((item) => {
        const label = record(item, GitHubOperation.ListLabels);
        return { name: string(label.name, GitHubOperation.ListLabels), color: string(label.color, GitHubOperation.ListLabels), description: nullableString(label.description, GitHubOperation.ListLabels) };
      });
      return { items, nextPage: nextPage(result.response.headers.get("link")), notModified: false, metadata: result.metadata };
    },
    async searchIssueMarker(credential, repository, marker) {
      validateMarker(marker);
      const query = encodeURIComponent(`repo:${repository.owner}/${repository.name} is:issue in:body \"${marker}\"`);
      const result = await request(GitHubOperation.SearchIssueMarker, credential, `/search/issues?q=${query}&per_page=10`);
      const root = record(result.json, GitHubOperation.SearchIssueMarker);
      for (const candidate of array(root.items, GitHubOperation.SearchIssueMarker)) {
        const issue = record(candidate, GitHubOperation.SearchIssueMarker);
        if (typeof issue.body === "string" && issue.body.includes(marker)) return { issue: issueValue(issue, marker, true), metadata: result.metadata };
      }
      return { issue: null, metadata: result.metadata };
    },
    async createIssue(credential, repository, input) {
      const marker = issueMarker(input.submissionId);
      const existing = await this.searchIssueMarker(credential, repository, marker);
      if (existing.issue !== null) return { issue: existing.issue, metadata: existing.metadata };
      const recent = await listRecentIssueMarker(credential, repository, marker);
      if (recent.issue !== null) return { issue: recent.issue, metadata: recent.metadata };
      let result: RequestResult;
      try {
        result = await request(GitHubOperation.CreateIssue, credential, `${repositoryPath(repository)}/issues`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ title: input.title, body: `${input.body}\n\n${marker}`, labels: input.labels }),
        });
      } catch (error) {
        const ambiguous = error instanceof GitHubProviderError && (error.code === GitHubErrorCode.NetworkFailure || (error.code === GitHubErrorCode.InvalidResponse && error.status !== null && (error.status >= 500 || (error.status >= 200 && error.status < 300))));
        if (!ambiguous) throw error;
        const reconciled = await listRecentIssueMarker(credential, repository, marker);
        if (reconciled.issue !== null) return { issue: reconciled.issue, metadata: reconciled.metadata };
        throw new GitHubProviderError(GitHubErrorCode.AmbiguousWrite, GitHubOperation.CreateIssue);
      }
      return { issue: issueValue(record(result.json, GitHubOperation.CreateIssue), marker, false), metadata: result.metadata };
    },
    async searchPullRequests(credential, query, options = {}) {
      const page = positiveInteger(options.page ?? 1);
      const normalized = hasPositivePullRequestQualifier(query) ? query : `${query} is:pr`;
      const result = await request(GitHubOperation.SearchPullRequests, credential, `/search/issues?q=${encodeURIComponent(normalized)}&per_page=100&page=${page}`, options.etag ? { headers: { "If-None-Match": options.etag } } : {});
      if (result.response.status === 304) return { items: [], nextPage: null, notModified: true, metadata: result.metadata };
      const items = array(record(result.json, GitHubOperation.SearchPullRequests).items, GitHubOperation.SearchPullRequests).map((item) => pullSummary(record(item, GitHubOperation.SearchPullRequests)));
      return { items, nextPage: nextPage(result.response.headers.get("link")), notModified: false, metadata: result.metadata };
    },
    async enrichPullRequests(credential, nodeIds) {
      if (nodeIds.length === 0) return { items: [], metadata: { etag: null, rate: emptyRate() } };
      if (nodeIds.length > 100 || new Set(nodeIds).size !== nodeIds.length || nodeIds.some((id) => id.length === 0 || id.length > 256)) throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, GitHubOperation.EnrichPullRequests);
      const result = await request(GitHubOperation.EnrichPullRequests, credential, "/graphql", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ query: DeckPullRequestQuery, variables: { ids: nodeIds } }),
      });
      const root = record(result.json, GitHubOperation.EnrichPullRequests);
      const errors = root.errors;
      if (Array.isArray(errors) && errors.length > 0) throw graphQlFailure(result.response.status, result.metadata.rate, errors);
      const data = record(root.data, GitHubOperation.EnrichPullRequests);
      const items = array(data.nodes, GitHubOperation.EnrichPullRequests).flatMap((node) => node === null ? [] : [deckPullRequest(record(node, GitHubOperation.EnrichPullRequests))]);
      return { items, metadata: result.metadata };
    },
    async getPullRequest(credential, repository, number, etag) {
      const result = await request(GitHubOperation.GetPullRequest, credential, `${repositoryPath(repository)}/pulls/${positiveInteger(number)}`, etag ? { headers: { "If-None-Match": etag } } : {});
      if (result.response.status === 304) return { pullRequest: null, notModified: true, metadata: result.metadata };
      const pull = record(result.json, GitHubOperation.GetPullRequest);
      const summary = pullSummary(pull, repository);
      const user = record(pull.user, GitHubOperation.GetPullRequest);
      const head = record(pull.head, GitHubOperation.GetPullRequest);
      return { pullRequest: { ...summary, body: nullableString(pull.body, GitHubOperation.GetPullRequest), author: string(user.login, GitHubOperation.GetPullRequest), state: string(pull.state, GitHubOperation.GetPullRequest), headSha: string(head.sha, GitHubOperation.GetPullRequest), labels: array(pull.labels, GitHubOperation.GetPullRequest).map((label) => string(record(label, GitHubOperation.GetPullRequest).name, GitHubOperation.GetPullRequest)) }, notModified: false, metadata: result.metadata };
    },
    ownsCanonicalUrl,
  };
}

export function issueMarker(submissionId: string): string {
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(submissionId)) throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, GitHubOperation.CreateIssue);
  return `<!-- devhud-submission:${submissionId} -->`;
}

export function ownsCanonicalUrl(value: string): { readonly kind: "issue" | "pull-request"; readonly repository: GitHubRepositoryRef; readonly number: number } | null {
  try {
    const url = new URL(value);
    if (url.protocol !== "https:" || url.hostname !== "github.com" || url.username || url.password || url.search || url.hash) return null;
    const match = /^\/([A-Za-z0-9](?:[A-Za-z0-9-]{0,38}))\/([A-Za-z0-9_.-]+)\/(issues|pull)\/([1-9]\d*)\/?$/u.exec(url.pathname);
    if (match === null) return null;
    return { kind: match[3] === "issues" ? "issue" : "pull-request", repository: { owner: match[1], name: match[2] }, number: Number(match[4]) };
  } catch { return null; }
}

function repositoryPath(repository: GitHubRepositoryRef): string { return `/repos/${encodeURIComponent(repository.owner)}/${encodeURIComponent(repository.name)}`; }
function validateClassicRepoScope(credential: GitHubCredential, result: RequestResult, operation: GitHubOperation): void { if (credential.kind !== "classic") return; const scopes = new Set((result.response.headers.get("x-oauth-scopes") ?? "").split(",").map((scope) => scope.trim()).filter(Boolean)); if (!scopes.has("repo")) throw new GitHubProviderError(GitHubErrorCode.MissingScope, operation, result.response.status, result.metadata.rate); }
function validateMarker(marker: string): void { if (!/^<!-- devhud-submission:[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12} -->$/u.test(marker)) throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, GitHubOperation.SearchIssueMarker); }
function responseMetadata(headers: Headers): GitHubResponseMetadata {
  const reset = numberHeader(headers, "x-ratelimit-reset");
  return { etag: headers.get("etag"), rate: { limit: numberHeader(headers, "x-ratelimit-limit"), remaining: numberHeader(headers, "x-ratelimit-remaining"), used: numberHeader(headers, "x-ratelimit-used"), resetAt: reset === null || reset > 8_640_000_000 ? null : new Date(reset * 1000).toISOString(), resource: headers.get("x-ratelimit-resource"), retryAfterSeconds: numberHeader(headers, "retry-after") } };
}
function numberHeader(headers: Headers, name: string): number | null { const value = headers.get(name); if (value === null || !/^\d+$/u.test(value)) return null; const parsed = Number(value); return Number.isSafeInteger(parsed) ? parsed : null; }
function classifyFailure(operation: GitHubOperation, kind: GitHubCredentialKind, response: Response, json: unknown, rate: GitHubRate): GitHubProviderError {
  const message = typeof json === "object" && json !== null && typeof (json as Record<string, unknown>).message === "string" ? ((json as Record<string, unknown>).message as string).toLowerCase() : "";
  if (operation === GitHubOperation.SearchPullRequests && response.status === 422) return new GitHubProviderError(GitHubErrorCode.InvalidQuery, operation, response.status, rate);
  if ((response.status === 403 || response.status === 429) && (rate.remaining === 0 || response.status === 429 || message.includes("rate limit"))) return new GitHubProviderError(GitHubErrorCode.RateLimited, operation, response.status, rate);
  if (response.status === 401) return new GitHubProviderError(GitHubErrorCode.InvalidToken, operation, response.status, rate);
  if (response.status === 403 && (response.headers.has("x-github-sso") || message.includes("organization") || message.includes("approval") || message.includes("policy"))) return new GitHubProviderError(GitHubErrorCode.OrganizationDenied, operation, response.status, rate);
  if (kind === "fine-grained" && response.status === 404) return new GitHubProviderError(GitHubErrorCode.FineGrainedRepositoryRestriction, operation, response.status, rate);
  if (response.status === 403 || response.status === 404) return new GitHubProviderError(GitHubErrorCode.MissingScope, operation, response.status, rate);
  return new GitHubProviderError(GitHubErrorCode.InvalidResponse, operation, response.status, rate);
}
function record(value: unknown, operation: GitHubOperation): Record<string, unknown> { if (value === null || typeof value !== "object" || Array.isArray(value)) throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, operation); return value as Record<string, unknown>; }
function array(value: unknown, operation: GitHubOperation): readonly unknown[] { if (!Array.isArray(value)) throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, operation); return value; }
function string(value: unknown, operation: GitHubOperation): string { if (typeof value !== "string") throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, operation); return value; }
function nullableString(value: unknown, operation: GitHubOperation): string | null { if (value === null || value === undefined) return null; return string(value, operation); }
function positiveInteger(value: number, operation: GitHubOperation = GitHubOperation.GetPullRequest): number { if (!Number.isSafeInteger(value) || value < 1) throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, operation); return value; }
function nextPage(link: string | null): number | null { if (link === null) return null; for (const part of link.split(",")) { if (!/;\s*rel="next"/u.test(part)) continue; const match = /[?&]page=(\d+)/u.exec(part); if (match !== null) return Number(match[1]); } return null; }
function issueValue(value: Record<string, unknown>, marker: string, reconciled: boolean): GitHubIssue { return { number: positiveInteger(Number(value.number), GitHubOperation.CreateIssue), title: string(value.title, GitHubOperation.CreateIssue), url: canonicalApiHtmlUrl(value.html_url, "issue"), marker, reconciled }; }
function pullSummary(value: Record<string, unknown>, fallback?: GitHubRepositoryRef): GitHubPullRequestSummary {
  const url = canonicalApiHtmlUrl(value.html_url, "pull-request");
  const owned = ownsCanonicalUrl(url);
  if (owned === null || owned.kind !== "pull-request") throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, GitHubOperation.SearchPullRequests);
  return { nodeId: typeof value.node_id === "string" ? value.node_id : url, number: positiveInteger(Number(value.number), GitHubOperation.GetPullRequest), title: string(value.title, GitHubOperation.GetPullRequest), url, draft: value.draft === true, repository: fallback ?? owned.repository };
}

const DeckPullRequestQuery = `query DeckPullRequests($ids: [ID!]!) { nodes(ids: $ids) { ... on PullRequest { id number title url isDraft state merged mergedAt updatedAt mergeable reviewDecision repository { owner { login } name } author { login } labels(first: 100) { nodes { name } } reviewRequests(first: 100) { nodes { requestedReviewer { ... on User { login } ... on Team { slug } } } } commits(last: 1) { nodes { commit { statusCheckRollup { state contexts(first: 100) { nodes { __typename ... on CheckRun { name conclusion status } ... on StatusContext { context state } } } } } } } } } }`;

function deckPullRequest(value: Record<string, unknown>): GitHubDeckPullRequest {
  const operation = GitHubOperation.EnrichPullRequests;
  const repository = record(value.repository, operation);
  const owner = record(repository.owner, operation);
  const stateValue = string(value.state, operation);
  const merged = value.merged === true || value.mergedAt !== null && value.mergedAt !== undefined;
  const state: GitHubDeckPullRequest["state"] = merged ? "merged" : stateValue === "OPEN" ? "open" : stateValue === "CLOSED" ? "closed" : invalidDeckState();
  const review = nullableString(value.reviewDecision, operation);
  const reviewDecision = review === null ? null : review === "APPROVED" ? "approved" : review === "CHANGES_REQUESTED" ? "changes-requested" : review === "REVIEW_REQUIRED" ? "required" : null;
  const labels = array(record(value.labels, operation).nodes, operation).map((item) => string(record(item, operation).name, operation));
  const requestedReviewers = array(record(value.reviewRequests, operation).nodes, operation).flatMap((item) => {
    const requested = record(item, operation).requestedReviewer;
    if (requested === null) return [];
    const reviewer = record(requested, operation);
    if (typeof reviewer.login === "string") return [reviewer.login];
    if (typeof reviewer.slug === "string") return [reviewer.slug];
    return [];
  });
  const commitNodes = array(record(value.commits, operation).nodes, operation);
  const rollup = commitNodes.length === 0 ? null : record(record(commitNodes[0], operation).commit, operation).statusCheckRollup;
  const contexts = rollup === null || rollup === undefined ? [] : array(record(record(rollup, operation).contexts, operation).nodes, operation).map((context) => {
    const item = record(context, operation);
    return { name: typeof item.name === "string" ? item.name : string(item.context, operation), state: typeof item.conclusion === "string" && item.conclusion !== "null" ? item.conclusion : string(item.state ?? item.status, operation) };
  });
  return { nodeId: string(value.id, operation), number: positiveInteger(Number(value.number), operation), title: string(value.title, operation), url: canonicalApiHtmlUrl(value.url, "pull-request"), draft: value.isDraft === true, repository: { owner: string(owner.login, operation), name: string(repository.name, operation) }, author: value.author === null ? "ghost" : string(record(value.author, operation).login, operation), state, reviewDecision, requestedReviewers, checkRollup: { state: rollup === null || rollup === undefined ? null : nullableString(record(rollup, operation).state, operation), contexts }, mergeable: string(value.mergeable, operation), labels, updatedAt: string(value.updatedAt, operation) };
}
function invalidDeckState(): never { throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, GitHubOperation.EnrichPullRequests); }
function emptyRate(): GitHubRate { return { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null }; }
function graphQlFailure(status: number, rate: GitHubRate, errors: readonly unknown[]): GitHubProviderError {
  const messages = errors.map((error) => typeof error === "object" && error !== null ? (error as Record<string, unknown>).message : "").filter((message): message is string => typeof message === "string").join(" ").toLowerCase();
  if (rate.remaining === 0 || messages.includes("rate limit")) return new GitHubProviderError(GitHubErrorCode.RateLimited, GitHubOperation.EnrichPullRequests, status, rate);
  if (messages.includes("could not resolve") || messages.includes("syntax") || messages.includes("search")) return new GitHubProviderError(GitHubErrorCode.InvalidQuery, GitHubOperation.EnrichPullRequests, status, rate);
  if (messages.includes("resource not accessible") || messages.includes("permission")) return new GitHubProviderError(GitHubErrorCode.MissingScope, GitHubOperation.EnrichPullRequests, status, rate);
  return new GitHubProviderError(GitHubErrorCode.InvalidResponse, GitHubOperation.EnrichPullRequests, status, rate);
}
function canonicalApiHtmlUrl(value: unknown, expected: "issue" | "pull-request"): string { const operation = expected === "issue" ? GitHubOperation.CreateIssue : GitHubOperation.GetPullRequest; const url = string(value, operation); const owned = ownsCanonicalUrl(url); if (owned === null || owned.kind !== expected) throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, operation); return url; }
