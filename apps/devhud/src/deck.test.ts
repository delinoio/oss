import { describe, expect, it } from "vitest";
import { applyDeckBuilder, classifyDeckFailure, deckTransitionKeys, nextDeckRefresh, parseDeckBuilder, readDeckCache, updateDeckBuilder, validateDeckQuery } from "./deck.ts";
import { GitHubErrorCode, GitHubProviderError } from "./github-provider.ts";
import { NativeBridgeError, NativeBridgeErrorCode } from "./native-bridge.ts";
import { deckRepositories } from "./settings-contract.ts";

describe("Deck query and local transitions", () => {
  it("round-trips builder fields without rewriting raw syntax", () => {
    const raw = 'repo:octo/widgets "keep this exact text" is:pr label:"needs review"';
    expect(parseDeckBuilder(raw)).toMatchObject({ repository: "octo/widgets", label: "needs review" });
    expect(applyDeckBuilder(raw, "author", "octocat")).toBe(`${raw} author:octocat`);
    expect(applyDeckBuilder(raw, "repository", "octo/next")).toBe('repo:octo/next "keep this exact text" is:pr label:"needs review"');
    expect(validateDeckQuery(raw)).toBe(true);
    expect(validateDeckQuery("-is:pr repo:octo/widgets")).toBe(false);
    expect(validateDeckQuery('"find is:pr here" repo:octo/widgets')).toBe(false);
    expect(validateDeckQuery("repo:octo/widgets IS:PR")).toBe(true);
  });
  it("ignores builder-looking qualifiers inside quoted phrases", () => {
    const raw = '"find repo:foo/bar" repo:real/project is:pr';
    expect(parseDeckBuilder(raw)).toMatchObject({ repository: "real/project" });
    expect(applyDeckBuilder(raw, "repository", "next/project")).toBe('"find repo:foo/bar" repo:next/project is:pr');
  });
  it("normalizes an emptied builder back to null", () => {
    expect(updateDeckBuilder({ repository: null, author: null, review: null, label: "needs review", state: null }, "label", null)).toBeNull();
  });
  it("round-trips labels containing query syntax characters", () => {
    const quoted = applyDeckBuilder("is:pr", "label", '"foo"');
    const escaped = applyDeckBuilder("is:pr", "label", "path\\name");
    expect(quoted).toBe('is:pr label:"\\"foo\\""');
    expect(escaped).toBe('is:pr label:"path\\\\name"');
    expect(parseDeckBuilder(quoted)).toMatchObject({ label: '"foo"' });
    expect(parseDeckBuilder(escaped)).toMatchObject({ label: "path\\name" });
  });
  it("uses reset only for an exhausted quota and always honors retry-after", () => {
    expect(Date.parse(nextDeckRefresh(0, 5, 2, { limit: 1, remaining: 0, used: 1, resetAt: "1970-01-01T00:30:00.000Z", resource: "core", retryAfterSeconds: null }))).toBe(1_800_000);
    expect(Date.parse(nextDeckRefresh(0, 5, 0, { limit: 1, remaining: 1, used: 0, resetAt: "1970-01-01T00:30:00.000Z", resource: "core", retryAfterSeconds: 900 }))).toBe(900_000);
  });
  it("parses every distinct repository qualifier and rejects malformed ones", () => {
    expect(deckRepositories("repo:octo/widgets repo:octo/widgets repo:delinoio/oss is:pr")).toEqual([{ owner: "octo", name: "widgets" }, { owner: "delinoio", name: "oss" }]);
    expect(deckRepositories("repo:octo is:pr")).toBeNull();
  });
  it("discards malformed nested cache entries", () => {
    const cache = { version: 2, deckId: "deck", query: "repo:octo/widgets is:pr", queryEtag: null, results: [null], lastSuccessfulAt: null, rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] };
    expect(readDeckCache({ getItem: () => JSON.stringify(cache) }, "origin", "deck", "repo:octo/widgets is:pr")).toBeNull();
  });
  it("accepts legacy caches without pending notifications and rejects malformed pending notifications", () => {
    const cache = { version: 2, deckId: "deck", query: "repo:octo/widgets is:pr", queryEtag: null, results: [], lastSuccessfulAt: null, rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] };
    expect(readDeckCache({ getItem: () => JSON.stringify(cache) }, "origin", "deck", "repo:octo/widgets is:pr")?.pendingNotifications).toEqual([]);
    expect(readDeckCache({ getItem: () => JSON.stringify({ ...cache, pendingNotifications: [{ key: "event", kind: "unexpected", body: "PR" }] }) }, "origin", "deck", "repo:octo/widgets is:pr")).toBeNull();
  });
  it("notifies only changed existing pull requests", () => {
    const base = { nodeId: "pr", number: 1, title: "PR", url: "https://github.com/o/r/pull/1", draft: false, repository: { owner: "o", name: "r" }, author: "a", state: "open" as const, reviewDecision: null, requestedReviewers: [], checkRollup: { state: "PENDING", contexts: [] }, mergeable: "MERGEABLE", labels: [], updatedAt: "2026-01-01T00:00:00Z" };
    expect(deckTransitionKeys([base], [{ ...base, state: "merged", reviewDecision: "approved", checkRollup: { state: "SUCCESS", contexts: [] } }]).map((item) => item.kind)).toEqual(["review", "checks", "merged"]);
  });
  it("gives repeated review and check transitions distinct notification keys", () => {
    const base = { nodeId: "pr", number: 1, title: "PR", url: "https://github.com/o/r/pull/1", draft: false, repository: { owner: "o", name: "r" }, author: "a", state: "open" as const, reviewDecision: null, requestedReviewers: [], checkRollup: { state: "PENDING", contexts: [] }, mergeable: "MERGEABLE", labels: [], updatedAt: "2026-01-01T00:00:00Z" };
    const failed = { ...base, checkRollup: { state: "FAILURE", contexts: [] }, updatedAt: "2026-01-01T00:01:00Z" };
    const rerun = { ...base, checkRollup: { state: "PENDING", contexts: [] }, updatedAt: "2026-01-01T00:02:00Z" };
    const failedAgain = { ...base, checkRollup: { state: "FAILURE", contexts: [] }, updatedAt: "2026-01-01T00:03:00Z" };
    expect(deckTransitionKeys([base], [failed])[0]?.key).not.toBe(deckTransitionKeys([rerun], [failedAgain])[0]?.key);
  });
  it("gives repeated close transitions distinct notification keys", () => {
    const base = { nodeId: "pr", number: 1, title: "PR", url: "https://github.com/o/r/pull/1", draft: false, repository: { owner: "o", name: "r" }, author: "a", state: "open" as const, reviewDecision: null, requestedReviewers: [], checkRollup: { state: "PENDING", contexts: [] }, mergeable: "MERGEABLE", labels: [], updatedAt: "2026-01-01T00:00:00Z" };
    const closed = { ...base, state: "closed" as const, updatedAt: "2026-01-01T00:01:00Z" };
    const reopened = { ...base, updatedAt: "2026-01-01T00:02:00Z" };
    const closedAgain = { ...closed, updatedAt: "2026-01-01T00:03:00Z" };
    expect(deckTransitionKeys([base], [closed])[0]?.key).not.toBe(deckTransitionKeys([reopened], [closedAgain])[0]?.key);
  });
  it("does not collapse credential, secure-storage, permission, query, network, and rate failures", () => {
    expect(classifyDeckFailure(new GitHubProviderError(GitHubErrorCode.MissingToken, "validate-credential"))).toBe("token");
    expect(classifyDeckFailure(new NativeBridgeError(NativeBridgeErrorCode.StorageFailure))).toBe("secure-storage");
    expect(classifyDeckFailure(new GitHubProviderError(GitHubErrorCode.MissingScope, "enrich-pull-requests"))).toBe("permission");
    expect(classifyDeckFailure(new GitHubProviderError(GitHubErrorCode.InvalidQuery, "enrich-pull-requests"))).toBe("query");
    expect(classifyDeckFailure(new GitHubProviderError(GitHubErrorCode.NetworkFailure, "search-pull-requests"))).toBe("network");
    expect(classifyDeckFailure(new GitHubProviderError(GitHubErrorCode.RateLimited, "search-pull-requests"))).toBe("rate-limit");
  });
});
