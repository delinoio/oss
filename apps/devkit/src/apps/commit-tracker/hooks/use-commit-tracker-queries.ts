"use client";

import { useQuery } from "@connectrpc/connect-query";

import {
  listMetricSeries,
  getPullRequestComparison,
} from "@/gen/committracker/v1/commit_tracker-MetricQueryService_connectquery";
import { GitProviderKind } from "@/gen/committracker/v1/commit_tracker_pb";

export interface MetricSeriesParams {
  provider: GitProviderKind;
  repository: string;
  branch: string;
  environment: string;
  metricKey: string;
  limit?: number;
}

export function useListMetricSeries(params: MetricSeriesParams | undefined) {
  return useQuery(
    listMetricSeries,
    params
      ? {
          provider: params.provider,
          repository: params.repository,
          branch: params.branch,
          environment: params.environment,
          metricKey: params.metricKey,
          limit: params.limit ?? 50,
        }
      : undefined,
    { enabled: !!params },
  );
}

export interface PrComparisonParams {
  provider: GitProviderKind;
  repository: string;
  baseCommitSha: string;
  headCommitSha: string;
  environment: string;
}

export function useGetPullRequestComparison(params: PrComparisonParams | undefined) {
  return useQuery(
    getPullRequestComparison,
    params
      ? {
          provider: params.provider,
          repository: params.repository,
          baseCommitSha: params.baseCommitSha,
          headCommitSha: params.headCommitSha,
          environment: params.environment,
          metricKeys: [],
        }
      : undefined,
    { enabled: !!params },
  );
}
