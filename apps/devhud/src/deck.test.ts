import { describe, expect, it } from "vitest";
import { applyDeckBuilder, classifyDeckFailure, deckTransitionKeys, nextDeckRefresh, parseDeckBuilder, readDeckCache, validateDeckQuery } from "./deck.ts";
import { GitHubErrorCode, GitHubProviderError } from "./github-provider.ts";
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
  it("uses rate reset and exponential backoff", () => {
    expect(Date.parse(nextDeckRefresh(0, 5, 2, { limit: 1, remaining: 0, used: 1, resetAt: "1970-01-01T00:30:00.000Z", resource: "core", retryAfterSeconds: null }))).toBe(1_800_000);
  });
  it("parses every distinct repository qualifier and rejects malformed ones", () => {
    expect(deckRepositories("repo:octo/widgets repo:octo/widgets repo:delinoio/oss is:pr")).toEqual([{ owner: "octo", name: "widgets" }, { owner: "delinoio", name: "oss" }]);
    expect(deckRepositories("repo:octo is:pr")).toBeNull();
  });
  it("discards malformed nested cache entries", () => {
    const cache = { version: 1, deckId: "deck", queryEtag: null, results: [null], lastSuccessfulAt: null, rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] };
    expect(readDeckCache({ getItem: () => JSON.stringify(cache) }, "profile", "deck")).toBeNull();
  });
  it("notifies only changed existing pull requests", () => {
    const base = { nodeId: "pr", number: 1, title: "PR", url: "https://github.com/o/r/pull/1", draft: false, repository: { owner: "o", name: "r" }, author: "a", state: "open" as const, reviewDecision: null, requestedReviewers: [], checkRollup: { state: "PENDING", contexts: [] }, mergeable: "MERGEABLE", labels: [], updatedAt: "2026-01-01T00:00:00Z" };
    expect(deckTransitionKeys([base], [{ ...base, state: "merged", reviewDecision: "approved", checkRollup: { state: "SUCCESS", contexts: [] } }]).map((item) => item.kind)).toEqual(["review", "checks", "merged"]);
  });
  it("does not collapse token, permission, query, network, and rate failures", () => {
    expect(classifyDeckFailure(new GitHubProviderError(GitHubErrorCode.MissingToken, "validate-credential"))).toBe("token");
    expect(classifyDeckFailure(new GitHubProviderError(GitHubErrorCode.MissingScope, "enrich-pull-requests"))).toBe("permission");
    expect(classifyDeckFailure(new GitHubProviderError(GitHubErrorCode.InvalidQuery, "enrich-pull-requests"))).toBe("query");
    expect(classifyDeckFailure(new GitHubProviderError(GitHubErrorCode.NetworkFailure, "search-pull-requests"))).toBe("network");
    expect(classifyDeckFailure(new GitHubProviderError(GitHubErrorCode.RateLimited, "search-pull-requests"))).toBe("rate-limit");
  });
});
