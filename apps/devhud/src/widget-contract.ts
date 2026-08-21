import { SettingsTextLimit } from "./contract-limits.ts";
import type { GitHubDeckPullRequest, GitHubRate } from "./github-provider.ts";
import type { DeckRepositoryRef, GitHubCredentialKind } from "./settings-contract.ts";

export const WidgetContractVersion = 1 as const;
export const WidgetRepositoryLimit = 10 as const;
export const WidgetResultLimit = 100 as const;
export const WidgetPreviewLimit = 3 as const;
export const WidgetQueryLimit = SettingsTextLimit;
export const WidgetStaleAfterMilliseconds = 60 * 60 * 1000;

export const WidgetRefreshState = {
  Fresh: "fresh",
  Stale: "stale",
  MissingToken: "missing-token",
  RateLimit: "rate-limit",
  Permission: "permission",
  Error: "error",
} as const;
export type WidgetRefreshState = (typeof WidgetRefreshState)[keyof typeof WidgetRefreshState];

export interface WidgetDeckConfiguration {
  readonly version: typeof WidgetContractVersion;
  readonly deckId: string;
  readonly name: string;
  readonly query: string;
  readonly repositories: readonly DeckRepositoryRef[];
  readonly profileId: string;
  readonly profileKind: GitHubCredentialKind;
  readonly scopeId: string;
  readonly language: "en" | "ko";
}

export interface WidgetDeckCounts {
  readonly total: number;
  readonly open: number;
  readonly draft: number;
  readonly merged: number;
  readonly closed: number;
  readonly bounded: boolean;
}

export interface WidgetPullRequest {
  readonly nodeId: string;
  readonly number: number;
  readonly title: string;
  readonly repository: string;
  readonly state: GitHubDeckPullRequest["state"];
  readonly draft: boolean;
}

export interface WidgetDeckSnapshot {
  readonly version: typeof WidgetContractVersion;
  readonly deckId: string;
  readonly query: string;
  readonly counts: WidgetDeckCounts;
  readonly results: readonly WidgetPullRequest[];
  readonly state: WidgetRefreshState;
  readonly lastSuccessfulAt: string | null;
  readonly lastAttemptedAt: string;
  readonly rate: GitHubRate | null;
}

/** Counts are mutually exclusive: draft takes precedence, followed by merged, closed, and open. */
export function widgetDeckCounts(total: number, results: readonly GitHubDeckPullRequest[]): WidgetDeckCounts {
  const counts = { open: 0, draft: 0, merged: 0, closed: 0 };
  for (const pullRequest of results.slice(0, WidgetResultLimit)) {
    if (pullRequest.draft) counts.draft += 1;
    else if (pullRequest.state === "merged") counts.merged += 1;
    else if (pullRequest.state === "closed") counts.closed += 1;
    else counts.open += 1;
  }
  return { total, ...counts, bounded: total > WidgetResultLimit };
}

export function widgetPullRequests(results: readonly GitHubDeckPullRequest[]): readonly WidgetPullRequest[] {
  return results.slice(0, WidgetResultLimit).map((pullRequest) => ({
    nodeId: pullRequest.nodeId,
    number: pullRequest.number,
    title: pullRequest.title,
    repository: `${pullRequest.repository.owner}/${pullRequest.repository.name}`,
    state: pullRequest.state,
    draft: pullRequest.draft,
  }));
}

export function widgetSnapshotIsStale(snapshot: WidgetDeckSnapshot, now = Date.now()): boolean {
  return snapshot.lastSuccessfulAt === null || now - Date.parse(snapshot.lastSuccessfulAt) >= WidgetStaleAfterMilliseconds;
}

export function widgetRefreshState(lastSuccessfulAt: string | null, failure: "missing-token" | "rate-limit" | "permission" | "error" | null, now = Date.now()): WidgetRefreshState {
  if (failure === "missing-token") return WidgetRefreshState.MissingToken;
  if (failure === "rate-limit") return WidgetRefreshState.RateLimit;
  if (failure === "permission") return WidgetRefreshState.Permission;
  if (failure === "error") return WidgetRefreshState.Error;
  return lastSuccessfulAt === null || now - Date.parse(lastSuccessfulAt) >= WidgetStaleAfterMilliseconds ? WidgetRefreshState.Stale : WidgetRefreshState.Fresh;
}
