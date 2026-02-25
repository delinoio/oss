import {
  ComparisonQuery,
  ListMetricSeriesResponse,
  PublishPullRequestReportResponse,
  PublishReportRequest,
  PullRequestComparisonResponse,
  SeriesQuery,
} from "@/apps/commit-tracker/contracts";

async function parseJsonResponse<T>(response: Response): Promise<T> {
  if (!response.ok) {
    const details = await response.text();
    throw new Error(`Request failed (${response.status}): ${details}`);
  }
  return (await response.json()) as T;
}

function withSearchParams(pathname: string, params: URLSearchParams): string {
  return `${pathname}?${params.toString()}`;
}

function toSeriesParams(query: SeriesQuery): URLSearchParams {
  const params = new URLSearchParams({
    provider: query.provider,
    repository: query.repository,
    branch: query.branch,
    environment: query.environment,
  });

  if (query.metricKey) {
    params.set("metricKey", query.metricKey);
  }
  if (query.fromTime) {
    params.set("fromTime", query.fromTime);
  }
  if (query.toTime) {
    params.set("toTime", query.toTime);
  }
  if (query.limit && query.limit > 0) {
    params.set("limit", String(query.limit));
  }

  return params;
}

function toComparisonParams(query: ComparisonQuery): URLSearchParams {
  const params = new URLSearchParams({
    provider: query.provider,
    repository: query.repository,
    baseCommitSha: query.baseCommitSha,
    headCommitSha: query.headCommitSha,
    environment: query.environment,
  });

  for (const metricKey of query.metricKeys ?? []) {
    if (metricKey.trim().length > 0) {
      params.append("metricKey", metricKey.trim());
    }
  }

  return params;
}

export async function listMetricSeries(
  query: SeriesQuery,
): Promise<ListMetricSeriesResponse> {
  const response = await fetch(
    withSearchParams("/api/commit-tracker/series", toSeriesParams(query)),
    {
      cache: "no-store",
    },
  );
  return parseJsonResponse<ListMetricSeriesResponse>(response);
}

export async function getPullRequestComparison(
  query: ComparisonQuery,
): Promise<PullRequestComparisonResponse> {
  const response = await fetch(
    withSearchParams(
      "/api/commit-tracker/comparison",
      toComparisonParams(query),
    ),
    {
      cache: "no-store",
    },
  );
  return parseJsonResponse<PullRequestComparisonResponse>(response);
}

export async function publishPullRequestReport(
  request: PublishReportRequest,
): Promise<PublishPullRequestReportResponse> {
  const response = await fetch("/api/commit-tracker/report", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
  });
  return parseJsonResponse<PublishPullRequestReportResponse>(response);
}
