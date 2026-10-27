// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, formatNumber } from "../i18n";
import { useId, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  HealthState,
  OperatorService,
  type UsageSummary,
} from "@aiprouter/client";
import { Badge, Card, PageHeader } from "../ui";
import { ErrorNotice, date, label, money } from "./common";
import { timeRange } from "./reporting";

// Instance health polls on the existing 30 second cadence. Usage aggregates never poll.
export const HEALTH_REFETCH_INTERVAL_MS = 30000;
const DAY_MS = 86400000;
const INITIAL_RANGE_DAYS = 7;

enum RefreshPhase {
  Idle = "idle",
  Refreshing = "refreshing",
  Refreshed = "refreshed",
}

// Only the three server-defined states are displayed. UNSPECIFIED, absent and
// unrecognized wire values are Unknown and never receive success styling.
function knownHealth(state?: HealthState) {
  return state === HealthState.HEALTHY ||
    state === HealthState.DEGRADED ||
    state === HealthState.UNAVAILABLE
    ? state
    : undefined;
}

function StatusBadge({ state }: { state?: HealthState }) {
  useLocale();
  const known = knownHealth(state);
  const tone =
    known === HealthState.HEALTHY
      ? "success"
      : known === HealthState.DEGRADED
        ? "warning"
        : known === HealthState.UNAVAILABLE
          ? "danger"
          : "neutral";
  return (
    <Badge tone={tone}>
      {known === undefined ? translate("unknown") : label(HealthState[known])}
    </Badge>
  );
}

function initialDates() {
  return {
    start: new Date(Date.now() - INITIAL_RANGE_DAYS * DAY_MS)
      .toISOString()
      .slice(0, 10),
    end: new Date().toISOString().slice(0, 10),
  };
}

function Skeleton({ text }: { text: string }) {
  return (
    <div className="admin-health-skeleton" role="status" aria-busy="true">
      {text}
    </div>
  );
}

// Instance-specific presentation of the shared UsageSummary values. The shared
// Summary used by organization usage stays unchanged.
function InstanceMetrics({ summary }: { summary?: UsageSummary }) {
  useLocale();
  if (!summary)
    return (
      <p className="admin-empty">{translate("usageSummaryIsUnavailable")}</p>
    );
  return (
    <>
      <dl className="admin-health-metrics">
        <div>
          <dt>{translate("requests")}</dt>
          <dd>{formatNumber(summary.requestCount)}</dd>
        </div>
        <div>
          <dt>{translate("failedRequests")}</dt>
          <dd>{formatNumber(summary.failedRequestCount)}</dd>
        </div>
        <div>
          <dt>{translate("confirmedInputTokens")}</dt>
          <dd>{formatNumber(summary.knownUsage?.inputTokens)}</dd>
        </div>
        <div>
          <dt>{translate("confirmedOutputTokens")}</dt>
          <dd>{formatNumber(summary.knownUsage?.outputTokens)}</dd>
        </div>
        <div>
          <dt>{translate("knownEstimatedCost")}</dt>
          <dd>
            {summary.knownCosts.length ? (
              <ul className="admin-health-costs">
                {summary.knownCosts.map((cost, index) => (
                  <li key={`${cost.currency}:${index}`}>{money(cost)}</li>
                ))}
              </ul>
            ) : (
              translate("unknown")
            )}
          </dd>
        </div>
      </dl>
      <ul className="admin-health-missing">
        <li>
          {translate("requestsWithUnknownCost", {
            value0: summary.missingCostCount,
          })}
        </li>
        <li>
          {translate("requestsMissingUsage", {
            value0: summary.missingUsageCount,
          })}
        </li>
        <li>
          {translate("requestsMissingPrice", {
            value0: summary.missingPriceCount,
          })}
        </li>
      </ul>
      {summary.requestCount === 0n && (
        <p>{translate("noRecordedRequestsInThisPeriod")}</p>
      )}
    </>
  );
}

export function Health() {
  useLocale();
  const ids = useId();
  const hintId = `${ids}-hint`;
  const helperId = `${ids}-helper`;
  const errorId = `${ids}-error`;
  const health = useQuery(
    OperatorService.method.getInstanceHealth,
    {},
    { refetchInterval: HEALTH_REFETCH_INTERVAL_MS },
  );
  const [phase, setPhase] = useState(RefreshPhase.Idle);
  const [dates, setDates] = useState(initialDates);
  const valid = !!dates.start && !!dates.end && dates.start <= dates.end;
  const usage = useQuery(
    OperatorService.method.getInstanceUsage,
    { timeRange: valid ? timeRange(dates.start, dates.end) : undefined },
    { enabled: valid },
  );
  const info = health.data?.health;
  const loaded = !health.isPending && !health.error;
  const recording = knownHealth(info?.usageRecording);
  // A Healthy recording state does not certify earlier periods, so the caution
  // below always stays and no totals are ever labeled complete.
  const incomplete =
    !!health.error || (loaded && recording !== HealthState.HEALTHY);
  const recordingUnknown = loaded && recording === undefined;
  const updating = health.isFetching && !health.isPending;
  function refresh() {
    if (health.isFetching) return;
    setPhase(RefreshPhase.Refreshing);
    void health
      .refetch()
      .then((result) =>
        setPhase(result.isError ? RefreshPhase.Idle : RefreshPhase.Refreshed),
      );
  }
  return (
    <>
      <PageHeader
        title={translate("instanceHealth")}
        description={translate("wholeInstanceAvailabilityAndRequestAggregates")}
      />
      <p className="admin-scope">
        {translate("wholeInstanceHealthAndAggregatesOnly")}
      </p>
      <Card className="admin-health-card" aria-busy={health.isFetching}>
        <div className="admin-health-heading">
          <h2>{translate("availability")}</h2>
          <button
            type="button"
            // aria-disabled keeps keyboard focus on the button while a refresh runs.
            aria-disabled={health.isFetching}
            aria-describedby={hintId}
            onClick={refresh}
          >
            {health.isFetching && phase === RefreshPhase.Refreshing
              ? translate("refreshing")
              : translate("refreshHealth")}
          </button>
        </div>
        <p id={hintId} className="admin-scope">
          {translate("healthRefreshesEvery30Seconds")}
        </p>
        <p role="status" className="admin-health-refresh-status">
          {phase === RefreshPhase.Refreshed && !health.isFetching
            ? translate("healthRefreshed")
            : ""}
        </p>
        {health.isPending ? (
          <Skeleton text={translate("loadingHealth")} />
        ) : health.error ? (
          <ErrorNotice
            error={health.error}
            retry={() => void health.refetch()}
          />
        ) : (
          <div
            className={updating ? "admin-health-updating" : undefined}
            data-updating={updating || undefined}
          >
            <dl className="admin-health-status">
              <div>
                <dt>{translate("service")}</dt>
                <dd>
                  <StatusBadge state={info?.service} />
                </dd>
              </div>
              <div>
                <dt>{translate("database")}</dt>
                <dd>
                  <StatusBadge state={info?.database} />
                </dd>
              </div>
              <div>
                <dt>{translate("usageRecording")}</dt>
                <dd>
                  <StatusBadge state={info?.usageRecording} />
                  <p className="admin-scope">
                    {translate(
                      "recordingFailuresCanLeaveRequestTotalsIncomplete",
                    )}
                  </p>
                </dd>
              </div>
            </dl>
            <hr />
            <dl className="admin-health-cleanup">
              <div>
                <dt>{translate("lastSuccessfulCleanup")}</dt>
                <dd>{date(info?.lastCleanupSuccessAt)}</dd>
              </div>
              <div>
                <dt>{translate("consecutiveCleanupFailures")}</dt>
                <dd>
                  {info?.lastCleanupSuccessAt
                    ? formatNumber(info.consecutiveCleanupFailures)
                    : translate("unknown")}
                </dd>
              </div>
            </dl>
          </div>
        )}
      </Card>
      <Card className="admin-health-card">
        <h2>{translate("instanceUsageAggregates")}</h2>
        <div className="admin-form-grid">
          <label>
            {translate("fromUtc")}
            <input
              type="date"
              value={dates.start}
              aria-invalid={!valid || undefined}
              aria-describedby={`${helperId}${valid ? "" : ` ${errorId}`}`}
              onChange={(e) => setDates({ ...dates, start: e.target.value })}
            />
          </label>
          <label>
            {translate("throughUtc")}
            <input
              type="date"
              value={dates.end}
              aria-invalid={!valid || undefined}
              aria-describedby={`${helperId}${valid ? "" : ` ${errorId}`}`}
              onChange={(e) => setDates({ ...dates, end: e.target.value })}
            />
          </label>
        </div>
        <p id={helperId} className="admin-scope">
          {translate("datesIncludeTheFullSelectedDaysInUtc")}
        </p>
        {!valid && (
          <p id={errorId} role="alert" className="admin-error">
            {translate("chooseAValidStartAndEndDate")}
          </p>
        )}
        <hr />
        {incomplete && (
          <div className="admin-health-callout" role="note">
            <strong>{translate("requestTotalsMayBeIncomplete")}</strong>
            {recordingUnknown && (
              <p>
                {translate(
                  "unknownRecordingHealthDoesNotConfirmCompleteTotals",
                )}
              </p>
            )}
          </div>
        )}
        <p>
          {translate("recordedMetadataMayBeIncompleteIfUsageRecordingFailed")}
        </p>
        {valid &&
          (usage.isPending ? (
            <Skeleton text={translate("loadingUsageAggregates")} />
          ) : usage.error ? (
            <ErrorNotice
              error={usage.error}
              retry={() => void usage.refetch()}
            />
          ) : (
            <div
              className={usage.isFetching ? "admin-health-updating" : undefined}
              aria-busy={usage.isFetching}
            >
              <InstanceMetrics summary={usage.data?.summary} />
            </div>
          ))}
        <p>{translate("estimatesNotBillingCurrencyIsShownWith")}</p>
      </Card>
    </>
  );
}
