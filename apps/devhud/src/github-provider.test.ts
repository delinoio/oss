import { describe, expect, it, vi } from "vitest";
import fixture from "../fixtures/github-provider.json";
import { ClassicPatCreationUrl, createGitHubProvider, FineGrainedPatCreationUrl, githubDiagnostic, GitHubApiOrigin, GitHubErrorCode, GitHubProviderError, InternalProviderRegistryV1, InternalProviderRegistryVersion, issueMarker, ownsCanonicalUrl, readGitHubCredential, type GitHubCredential, type GitHubRepositoryRef } from "./github-provider.ts";
import { referencedRepositories, validateGitHubProfile } from "./github-settings-ui.tsx";
import { NativeBridgeError, NativeBridgeErrorCode, type NativeBridgeV1 } from "./native-bridge.ts";
import { canonicalDevHudSettings, defaultDevHudSettings, parseDevHudSettings } from "./settings-contract.ts";

const fine: GitHubCredential = { profileId: fixture.profiles.fine.id, kind: "fine-grained", token: fixture.profiles.fine.token };
const classic: GitHubCredential = { profileId: fixture.profiles.classic.id, kind: "classic", token: fixture.profiles.classic.token };
const restricted: GitHubCredential = { profileId: fixture.profiles.restricted.id, kind: "fine-grained", token: fixture.profiles.restricted.token };
const publicRepository = { owner: fixture.repositories.public.owner, name: fixture.repositories.public.name };
const privateRepository = { owner: fixture.repositories.private.owner, name: fixture.repositories.private.name };

function json(value: unknown, status = 200, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(value), { status, headers: { "content-type": "application/json", ...headers } });
}

function router() {
  let markerSearches = 0;
  return vi.fn<typeof fetch>(async (input, init) => {
    const url = new URL(String(input));
    const authorization = new Headers(init?.headers).get("authorization");
    const token = authorization?.replace("Bearer ", "") ?? "";
    if (token === "network-failure") throw new TypeError("fixture private network detail");
    if (url.pathname === "/user") {
      if (token === "invalid") return json({ message: "Bad credentials and private data" }, 401);
      if (token === "rate") return json({ message: "API rate limit exceeded" }, 429, { "x-ratelimit-remaining": "0", "x-ratelimit-limit": "5000", "x-ratelimit-reset": "2000000000", "retry-after": "60" });
      return json({ login: "fixture" }, 200, token.includes("classic") ? { "x-oauth-scopes": token.includes("no-scope") ? "read:user" : "repo, read:user" } : {});
    }
    if (token === fixture.profiles.restricted.token) return json({ message: "Not Found" }, 404);
    if (token === "org-denied") return json({ message: "organization policy denied" }, 403, { "x-github-sso": "required" });
    if (/^\/repos\/[^/]+\/[^/]+$/u.test(url.pathname)) return json({ private: url.pathname.includes("octo-private") }, 200, { etag: '"repository"', "x-ratelimit-limit": "5000", "x-ratelimit-remaining": "4999", "x-ratelimit-used": "1", "x-ratelimit-reset": "2000000000", "x-ratelimit-resource": "core", ...(token.includes("classic") ? { "x-oauth-scopes": "repo, read:user" } : {}) });
    if (url.pathname.endsWith("/issues") && init?.method === "POST") {
      if (token === "fine-no-issues") return json({ message: "Resource not accessible by personal access token" }, 403);
      if (init.body === "{}") return json({ message: "Validation Failed" }, 422);
      return json(fixture.issue, 201);
    }
    if (/^\/repos\/[^/]+\/[^/]+\/(pulls|issues|contents)$/u.test(url.pathname)) return json([]);
    if (url.pathname.endsWith("/labels")) {
      if (new Headers(init?.headers).get("if-none-match") === '"labels"') return new Response(null, { status: 304, headers: { etag: '"labels"' } });
      const page = url.searchParams.get("page");
      return json(page === "1" ? [fixture.labels[0]] : [fixture.labels[1]], 200, page === "1" ? { etag: '"labels"', link: `<${GitHubApiOrigin}${url.pathname}?per_page=100&page=2>; rel="next"` } : {});
    }
    if (url.pathname === "/search/issues" && url.searchParams.get("q")?.includes("devhud-submission")) {
      markerSearches += 1;
      const marker = issueMarker(fixture.submissionId);
      return json({ items: markerSearches === 1 ? [{ ...fixture.issue, body: `Body\n${marker}` }] : [] }, 200, { etag: '"search"' });
    }
    if (url.pathname === "/search/issues") return json({ items: [fixture.pullRequest] }, 200, { link: `<${GitHubApiOrigin}/search/issues?page=2>; rel="next"` });
    if (url.pathname.endsWith("/pulls/9")) return json(fixture.pullRequest, 200, { etag: '"pull"' });
    return json({ message: "fixture route missing" }, 500);
  });
}

describe("GitHub.com provider", () => {
  it("registers GitHub.com as the only v1 issue and pull-request provider", () => {
    expect(InternalProviderRegistryVersion).toBe(1);
    expect(InternalProviderRegistryV1).toEqual({ issueTrackers: ["github.com"], pullRequests: ["github.com"] });
  });
  it("validates public/private repositories and both PAT kinds with direct requests", async () => {
    const fetch = router();
    const provider = createGitHubProvider({ fetch });
    await expect(provider.validateRepository(fine, publicRepository)).resolves.toMatchObject({ private: false, permissions: { metadata: true, pullRequests: true, issues: true, contents: true } });
    await expect(provider.validateRepository(classic, privateRepository)).resolves.toMatchObject({ private: true });
    const issueWriteProbes = fetch.mock.calls.filter(([input, init]) => new URL(String(input)).pathname.endsWith("/issues") && init?.method === "POST" && init.body === "{}");
    expect(issueWriteProbes).toHaveLength(1);
    expect(fetch).toHaveBeenCalled();
    for (const [input] of fetch.mock.calls) expect(String(input).startsWith(GitHubApiOrigin)).toBe(true);
  });

  it("rejects a fine-grained PAT without Issues write permission", async () => {
    const provider = createGitHubProvider({ fetch: router() });
    await expect(provider.validateRepository({ ...fine, token: "fine-no-issues" }, publicRepository)).rejects.toMatchObject({ code: GitHubErrorCode.MissingScope, operation: "validate-repository", status: 403 });
  });

  it.each([
    ["invalid token", { ...fine, token: "invalid" }, GitHubErrorCode.InvalidToken],
    ["classic missing repo scope", { ...classic, token: "fixture-classic-no-scope" }, GitHubErrorCode.MissingScope],
    ["fine-grained repository restriction", restricted, GitHubErrorCode.FineGrainedRepositoryRestriction],
    ["organization denial", { ...fine, token: "org-denied" }, GitHubErrorCode.OrganizationDenied],
    ["rate limit", { ...fine, token: "rate" }, GitHubErrorCode.RateLimited],
    ["network failure", { ...fine, token: "network-failure" }, GitHubErrorCode.NetworkFailure],
  ])("classifies %s without private response data", async (_name, credential, code) => {
    const provider = createGitHubProvider({ fetch: router() });
    const promise = code === GitHubErrorCode.FineGrainedRepositoryRestriction || code === GitHubErrorCode.OrganizationDenied ? provider.validateRepository(credential, privateRepository) : provider.validateCredential(credential);
    const error = await promise.catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(GitHubProviderError);
    if (!(error instanceof GitHubProviderError)) throw error;
    expect(error.code).toBe(code);
    expect(error.message).not.toMatch(/private data|authorization|org-denied|fixture-fine|fixture-classic/iu);
    expect(JSON.stringify(githubDiagnostic(error))).not.toMatch(/private data|authorization|org-denied|fixture-fine|fixture-classic|body|headers|https?:/iu);
  });

  it("returns ETag/rate metadata, label pagination, and 304 results", async () => {
    const provider = createGitHubProvider({ fetch: router() });
    const first = await provider.listLabels(fine, privateRepository);
    expect(first).toMatchObject({ items: [fixture.labels[0]], nextPage: 2, notModified: false, metadata: { etag: '"labels"' } });
    const second = await provider.listLabels(fine, privateRepository, { page: 2 });
    expect(second.items).toEqual([fixture.labels[1]]);
    await expect(provider.listLabels(fine, privateRepository, { etag: '"labels"' })).resolves.toMatchObject({ items: [], notModified: true });
  });

  it("searches the idempotency marker before creating and returns an existing issue", async () => {
    const fetch = router();
    const provider = createGitHubProvider({ fetch });
    const result = await provider.createIssue(fine, privateRepository, { title: "never posted", body: "private body", labels: ["bug"], submissionId: fixture.submissionId });
    expect(result.issue).toMatchObject({ number: 41, reconciled: true, marker: issueMarker(fixture.submissionId) });
    expect(fetch.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
  });

  it("reconciles an ambiguous issue write from the recent issue list without a second POST", async () => {
    let searches = 0;
    let lists = 0;
    let posts = 0;
    const marker = issueMarker(fixture.submissionId);
    const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
      const url = new URL(String(input));
      if (url.pathname === "/search/issues") {
        searches += 1;
        return json({ items: [] });
      }
      if (url.pathname.endsWith("/issues") && init?.method !== "POST") {
        lists += 1;
        return json(posts === 0 ? [] : [{ ...fixture.issue, body: `Body\n${marker}` }]);
      }
      if (init?.method === "POST") { posts += 1; throw new TypeError("connection ended after write"); }
      return json({ message: "unexpected" }, 500);
    });
    const issue = await createGitHubProvider({ fetch }).createIssue(fine, privateRepository, { title: "Issue", body: "Body", labels: ["bug"], submissionId: fixture.submissionId });
    expect(issue.issue).toMatchObject({ number: 41, reconciled: true });
    expect({ searches, lists, posts }).toEqual({ searches: 1, lists: 2, posts: 1 });
  });

  it("does not repost while search indexing lags after an ambiguous write", async () => {
    let searches = 0;
    let lists = 0;
    let posts = 0;
    const marker = issueMarker(fixture.submissionId);
    const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
      const url = new URL(String(input));
      if (url.pathname === "/search/issues") { searches += 1; return json({ items: [] }); }
      if (url.pathname.endsWith("/issues") && init?.method !== "POST") {
        lists += 1;
        return json(lists < 3 ? [] : [{ ...fixture.issue, body: `Body\n${marker}` }]);
      }
      if (init?.method === "POST") { posts += 1; throw new TypeError("connection ended after write"); }
      return json({ message: "unexpected" }, 500);
    });
    const provider = createGitHubProvider({ fetch });
    const input = { title: "Issue", body: "Body", labels: ["bug"], submissionId: fixture.submissionId };
    await expect(provider.createIssue(fine, privateRepository, input)).rejects.toMatchObject({ code: GitHubErrorCode.AmbiguousWrite });
    await expect(provider.createIssue(fine, privateRepository, input)).resolves.toMatchObject({ issue: { number: 41, reconciled: true } });
    expect({ searches, lists, posts }).toEqual({ searches: 2, lists: 3, posts: 1 });
  });

  it("searches and enriches pull requests with pagination", async () => {
    const fetch = router();
    const provider = createGitHubProvider({ fetch });
    await expect(provider.searchPullRequests(fine, "repo:octo-private/controls")).resolves.toMatchObject({ nextPage: 2, items: [{ number: 9, repository: privateRepository }] });
    const search = fetch.mock.calls.find(([input]) => new URL(String(input)).pathname === "/search/issues");
    expect(new URL(String(search?.[0])).searchParams.get("q")).toBe("repo:octo-private/controls is:pr");
    await expect(provider.getPullRequest(fine, privateRepository, 9)).resolves.toMatchObject({ pullRequest: { author: "octocat", headSha: "0123456789abcdef", labels: ["needs-review"] }, metadata: { etag: '"pull"' } });
  });

  it.each([
    ["repo:octo-private/controls is:pr", "repo:octo-private/controls is:pr"],
    ["repo:octo-private/controls IS:PR", "repo:octo-private/controls IS:PR"],
    ["repo:octo-private/controls is:private", "repo:octo-private/controls is:private is:pr"],
    ["repo:octo-private/controls -is:pr", "repo:octo-private/controls -is:pr is:pr"],
  ])("requires a standalone positive pull-request qualifier in %s", async (query, expected) => {
    const fetch = router();
    await createGitHubProvider({ fetch }).searchPullRequests(fine, query);
    const search = fetch.mock.calls.find(([input]) => new URL(String(input)).pathname === "/search/issues");
    expect(new URL(String(search?.[0])).searchParams.get("q")).toBe(expected);
  });

  it("owns only canonical GitHub.com issue and pull request URLs", () => {
    expect(ownsCanonicalUrl("https://github.com/octo-private/controls/issues/41")).toEqual({ kind: "issue", repository: privateRepository, number: 41 });
    expect(ownsCanonicalUrl("https://github.com/octo-private/controls/pull/9")).toEqual({ kind: "pull-request", repository: privateRepository, number: 9 });
    expect(ownsCanonicalUrl("https://github.com/octo-private/controls/pull/9?token=secret")).toBeNull();
    expect(ownsCanonicalUrl("https://github.example/octo-private/controls/pull/9")).toBeNull();
  });

  it("generates contracted PAT links without hiding owner/repository selection", () => {
    expect(FineGrainedPatCreationUrl).toBe("https://github.com/settings/personal-access-tokens/new?contents=read&issues=write&metadata=read&pull_requests=read");
    expect(FineGrainedPatCreationUrl).not.toContain("target_name");
    expect(ClassicPatCreationUrl).toBe("https://github.com/settings/tokens/new?scopes=repo");
  });
});

describe("GitHub profile and server isolation", () => {
  const settings = parseDevHudSettings({ ...defaultDevHudSettings, github: { profiles: [{ id: fine.profileId, name: "Fine", kind: fine.kind }, { id: classic.profileId, name: "Classic", kind: classic.kind }], pendingPatRemovals: [], repositories: [{ ...publicRepository, profileRef: fine.profileId }, { ...privateRepository, profileRef: fine.profileId }], issueTracker: { owner: privateRepository.owner, repository: privateRepository.name, labels: ["bug"], profileRef: fine.profileId } } });

  it("distinguishes secure-store absence", async () => {
    const bridge = bridgeWithValue(null);
    const error = await readGitHubCredential(bridge, settings.github.profiles[0]).catch((reason: unknown) => reason);
    expect(error).toMatchObject({ code: GitHubErrorCode.MissingToken });
  });

  it("preserves typed secure-store failures", async () => {
    const failure = new NativeBridgeError(NativeBridgeErrorCode.StorageFailure);
    const bridge = { request: vi.fn(async () => { throw failure; }), listen: vi.fn(async () => () => undefined) } as NativeBridgeV1;
    await expect(readGitHubCredential(bridge, settings.github.profiles[0])).rejects.toBe(failure);
  });

  it("does not classify an invalid secure-store response as a missing PAT", async () => {
    const bridge = { request: vi.fn(async () => ({ kind: "ok" as const })), listen: vi.fn(async () => () => undefined) } as NativeBridgeV1;
    await expect(readGitHubCredential(bridge, settings.github.profiles[0])).rejects.toMatchObject({ code: GitHubErrorCode.InvalidResponse });
  });

  it("validates every referenced repository with only the explicitly selected profile", async () => {
    const validateRepository = vi.fn(async (_credential: GitHubCredential, _repository: GitHubRepositoryRef) => ({ repository: publicRepository, private: false, permissions: { metadata: true, pullRequests: true, issues: true, contents: true }, metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } }));
    const provider = { ...createGitHubProvider({ fetch: router() }), validateCredential: vi.fn(async () => ({ etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } })), validateRepository };
    await validateGitHubProfile(settings, fine.profileId, bridgeWithValue(fine.token), provider);
    expect(referencedRepositories(settings, fine.profileId)).toEqual([publicRepository, privateRepository]);
    expect(validateRepository).toHaveBeenCalledTimes(2);
    expect(validateRepository.mock.calls.every(([credential]) => credential.profileId === fine.profileId)).toBe(true);
  });

  it("synchronizes only stable IDs and sends PATs/GitHub requests to no server", async () => {
    const server = vi.fn();
    const fetch = router();
    await createGitHubProvider({ fetch }).validateCredential(fine);
    const synchronized = canonicalDevHudSettings(settings);
    expect(synchronized).toContain(fine.profileId);
    expect(synchronized).not.toContain(fine.token);
    expect(server).not.toHaveBeenCalled();
    expect(fetch.mock.calls.every(([input]) => String(input).startsWith(GitHubApiOrigin))).toBe(true);
  });
});

function bridgeWithValue(value: string | null): NativeBridgeV1 {
  return { request: vi.fn(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value } : { kind: "ok" }), listen: vi.fn(async () => () => undefined) } as NativeBridgeV1;
}
