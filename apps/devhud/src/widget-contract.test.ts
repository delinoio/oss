import { describe, expect, it } from "vitest";
import type { GitHubDeckPullRequest } from "./github-provider.ts";
import { WidgetRefreshState, widgetDeckCounts, widgetPullRequests, widgetRefreshState, widgetSnapshotIsStale } from "./widget-contract.ts";

function pull(nodeId: string, state: GitHubDeckPullRequest["state"], draft = false): GitHubDeckPullRequest {
  return {
    nodeId, number: Number(nodeId), title: `PR ${nodeId}`, url: `https://github.com/private/widgets/pull/${nodeId}`,
    draft, repository: { owner: "private", name: "widgets" }, author: "octocat", state,
    reviewDecision: null, requestedReviewers: [], checkRollup: { state: null, contexts: [] }, mergeable: "UNKNOWN", labels: [], updatedAt: "2026-08-20T00:00:00.000Z",
  };
}

describe("widget contract", () => {
  it("counts mutually exclusive states over the bounded result set", () => {
    const results = [pull("1", "open"), pull("2", "open", true), pull("3", "merged"), pull("4", "closed")];
    expect(widgetDeckCounts(120, results)).toEqual({ total: 120, open: 1, draft: 1, merged: 1, closed: 1, bounded: true });
  });

  it("retains GitHub query order and private titles", () => {
    const results = [pull("3", "open"), pull("1", "open"), pull("2", "open")];
    expect(widgetPullRequests(results).map(({ nodeId, title, repository }) => ({ nodeId, title, repository }))).toEqual([
      { nodeId: "3", title: "PR 3", repository: "private/widgets" },
      { nodeId: "1", title: "PR 1", repository: "private/widgets" },
      { nodeId: "2", title: "PR 2", repository: "private/widgets" },
    ]);
  });

  it("marks a successful snapshot stale after sixty minutes", () => {
    const snapshot = { version: 1, deckId: "deck", query: "is:pr", counts: { total: 0, open: 0, draft: 0, merged: 0, closed: 0, bounded: false }, results: [], state: "fresh", lastSuccessfulAt: "2026-08-20T00:00:00.000Z", lastAttemptedAt: "2026-08-20T00:00:00.000Z", rate: null } as const;
    expect(widgetSnapshotIsStale(snapshot, Date.parse("2026-08-20T00:59:59.999Z"))).toBe(false);
    expect(widgetSnapshotIsStale(snapshot, Date.parse("2026-08-20T01:00:00.000Z"))).toBe(true);
  });

  it("keeps offline/stale, missing, revoked, rate-limit, and background failure states explicit", () => {
    const now = Date.parse("2026-08-20T02:00:00.000Z");
    expect(widgetRefreshState("2026-08-20T01:30:00.000Z", null, now)).toBe(WidgetRefreshState.Fresh);
    expect(widgetRefreshState("2026-08-20T00:30:00.000Z", null, now)).toBe(WidgetRefreshState.Stale);
    expect(widgetRefreshState(null, "missing-token", now)).toBe(WidgetRefreshState.MissingToken);
    expect(widgetRefreshState("2026-08-20T00:30:00.000Z", "rate-limit", now)).toBe(WidgetRefreshState.RateLimit);
    expect(widgetRefreshState("2026-08-20T00:30:00.000Z", "error", now)).toBe(WidgetRefreshState.Error);
  });
});
