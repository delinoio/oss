// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, formatNumber } from "./i18n";
import { useEffect, useState, useSyncExternalStore } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  UsageService,
  RequestOutcome,
  PriceSource,
  type UsageSummary,
  type UsageRecord,
  type Money,
} from "@aiprouter/client";
import { type PageProps, pageSize } from "./api";
import {
  PageHeader,
  Button,
  Card,
  Field,
  Dialog,
  ErrorNotice,
  Loading,
  RefreshStatus,
  EmptyState,
  Notice,
  formatDate,
  formatMoney,
  enumLabel,
  Pagination,
} from "./ui";
/** Lists every known cost as its own currency line; never converts or sums. */
export function MoneyList({
  costs,
  className = "",
}: {
  costs?: Money[];
  className?: string;
}) {
  useLocale();
  if (!costs?.length)
    return <strong className={className}>{translate("unknown")}</strong>;
  return (
    <ul className={`money-list ${className}`}>
      {costs.map((money, index) => (
        <li key={`${money.currency}:${index}`}>{formatMoney(money)}</li>
      ))}
    </ul>
  );
}
export function SummaryCards({
  summary,
  loading = false,
  updating = false,
  refreshFailed = false,
}: {
  summary?: UsageSummary;
  /** Show a labeled loading status instead of values while the summary loads. */
  loading?: boolean;
  /** Label visible data as refreshing. */
  updating?: boolean;
  /** Label visible data as stale after a failed refresh. */
  refreshFailed?: boolean;
}) {
  useLocale();
  if (loading) return <Loading label={translate("loadingUsageSummary")} />;
  return (
    <>
      <RefreshStatus updating={updating} failed={refreshFailed} />
      <div className="stats">
        <Card>
          <p>{translate("requests")}</p>
          <strong>{formatNumber(summary?.requestCount)}</strong>
        </Card>
        <Card>
          <p>{translate("knownEstimatedCost")}</p>
          <MoneyList costs={summary?.knownCosts} className="stat-value" />
          <p>
            {summary
              ? translate("requestsWithUnknownCost", {
                  value0: summary.missingCostCount,
                })
              : translate("unknownCostCoverage")}
          </p>
        </Card>
        <Card>
          <p>{translate("confirmedTokens")}</p>
          <strong>
            {translate("inOut", {
              value0: formatNumber(summary?.knownUsage?.inputTokens),
              value1: formatNumber(summary?.knownUsage?.outputTokens),
            })}
          </strong>
          <p>
            {summary
              ? translate("missingUsageMissingPrices", {
                  value0: summary.missingUsageCount,
                  value1: summary.missingPriceCount,
                })
              : translate("unknownUsageCoverage")}
          </p>
        </Card>
      </div>
    </>
  );
}
/** Viewport query at which the filters render inline instead of in a dialog. */
export const INLINE_FILTERS_QUERY = "(min-width: 1024px)";
const DAY_MS = 86400000;

/** Default Last 7 days range: today (UTC) minus six days through today (UTC). */
export function defaultUsageRange(now = Date.now()) {
  return {
    start: new Date(now - 6 * DAY_MS).toISOString().slice(0, 10),
    end: new Date(now).toISOString().slice(0, 10),
  };
}

function useMediaQuery(query: string) {
  return useSyncExternalStore(
    (notify) => {
      const list = window.matchMedia?.(query);
      list?.addEventListener?.("change", notify);
      return () => list?.removeEventListener?.("change", notify);
    },
    () => window.matchMedia?.(query).matches ?? true,
    () => true,
  );
}

/** What the "Usage filters" dialog focuses when it opens. */
enum FilterDialogFocus {
  Heading = "heading",
  InvalidDate = "invalid-date",
}

export function UsagePage({ organization, session }: PageProps) {
  useLocale();
  const [range, setRange] = useState(() => defaultUsageRange());
  const [model, setModel] = useState("");
  const [provider, setProvider] = useState("");
  const [outcome, setOutcome] = useState<RequestOutcome | -1>(-1);
  const [token, setToken] = useState("");
  const [detail, setDetail] = useState<UsageRecord>();
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [dialogFocus, setDialogFocus] = useState(FilterDialogFocus.Heading);
  const inlineFilters = useMediaQuery(INLINE_FILTERS_QUERY);
  // The dialog only exists below the inline breakpoint; drop its open state
  // when the viewport grows so it does not reappear after a later shrink.
  useEffect(() => {
    if (inlineFilters) setFiltersOpen(false);
  }, [inlineFilters]);
  const startError = range.start ? undefined : translate("enterAStartDate");
  const endError = !range.end
    ? translate("enterAnEndDate")
    : range.start > range.end
      ? translate("theEndDateMustBeOnOrAfterTheStartDate")
      : undefined;
  const valid = !startError && !endError;
  const filterCount =
    Number(!!model) + Number(!!provider) + Number(outcome !== -1);
  const input = {
    organizationId: organization.id,
    userId: session.userId,
    timeRange: valid
      ? {
          start: timestampFromDate(new Date(`${range.start}T00:00:00Z`)),
          end: timestampFromDate(new Date(`${range.end}T23:59:59.999Z`)),
        }
      : undefined,
    modelId: model.trim() || undefined,
    providerId: provider.trim() || undefined,
    outcome: outcome === -1 ? undefined : outcome,
  };
  const records = useQuery(
    UsageService.method.listUsage,
    { ...input, page: { pageSize, pageToken: token } },
    { enabled: valid },
  );
  const summary = useQuery(UsageService.method.getUsageSummary, input, {
    enabled: valid,
  });
  const reset = () => {
    setRange(defaultUsageRange());
    setModel("");
    setProvider("");
    setOutcome(-1);
    setToken("");
  };
  const filterForm = (
    <>
      <Field label={translate("startDateUtc")} error={startError}>
        <input
          type="date"
          required
          value={range.start}
          onChange={(e) => {
            setRange((v) => ({ ...v, start: e.target.value }));
            setToken("");
          }}
        />
      </Field>
      <Field label={translate("endDateUtc")} error={endError}>
        <input
          type="date"
          required
          value={range.end}
          onChange={(e) => {
            setRange((v) => ({ ...v, end: e.target.value }));
            setToken("");
          }}
        />
      </Field>
      <Field label={translate("modelId_089ef2")}>
        <input
          value={model}
          onChange={(e) => {
            setModel(e.target.value);
            setToken("");
          }}
        />
      </Field>
      <Field label={translate("providerId")}>
        <input
          value={provider}
          onChange={(e) => {
            setProvider(e.target.value);
            setToken("");
          }}
        />
      </Field>
      <Field label={translate("outcome")}>
        <select
          value={outcome}
          onChange={(e) => {
            setOutcome(Number(e.target.value) as RequestOutcome | -1);
            setToken("");
          }}
        >
          <option value={-1}>{translate("allOutcomes")}</option>
          <option value={RequestOutcome.UNSPECIFIED}>
            {translate("unknownOutcome")}
          </option>
          {[
            RequestOutcome.SUCCEEDED,
            RequestOutcome.UPSTREAM_ERROR,
            RequestOutcome.GATEWAY_ERROR,
            RequestOutcome.CANCELED,
            RequestOutcome.RATE_LIMITED,
          ].map((v) => (
            <option key={v} value={v}>
              {enumLabel(RequestOutcome[v])}
            </option>
          ))}
        </select>
      </Field>
    </>
  );
  const openFilters = () => {
    setDialogFocus(
      valid ? FilterDialogFocus.Heading : FilterDialogFocus.InvalidDate,
    );
    setFiltersOpen(true);
  };
  const visible = records.data?.records;
  return (
    <>
      <PageHeader
        title={translate("yourUsage")}
        description={translate("yourRequestsInLast7DaysBy", {
          value0: organization.name,
        })}
      />
      <Notice>{translate("costsAreEstimatesNotBillingUnknownUsage")}</Notice>
      {inlineFilters ? (
        <div
          className="usage-filters"
          role="group"
          aria-label={translate("usageFilters")}
        >
          {filterForm}
          <Button onClick={reset}>{translate("clearFilters")}</Button>
        </div>
      ) : (
        <>
          <Button className="usage-filters-trigger" onClick={openFilters}>
            {translate("filters", { value0: filterCount })}
          </Button>
          {filtersOpen && (
            <Dialog
              title={translate("usageFilters")}
              className="usage-filters-dialog"
              focusHeading={dialogFocus === FilterDialogFocus.Heading}
              initialFocus='input[aria-invalid="true"]'
              onClose={() => setFiltersOpen(false)}
            >
              <p>{translate("changesUpdateYourUsageImmediatelyDatesAre")}</p>
              {filterForm}
              <div className="actions">
                <Button onClick={reset}>{translate("clearFilters")}</Button>
                <Button variant="primary" onClick={() => setFiltersOpen(false)}>
                  {translate("done")}
                </Button>
              </div>
            </Dialog>
          )}
        </>
      )}
      {!valid && (
        <Notice tone="danger">
          {translate("chooseAValidDateRangeTheEnd")}
        </Notice>
      )}
      <ErrorNotice error={summary.error} retry={() => void summary.refetch()} />
      <SummaryCards
        summary={valid ? summary.data?.summary : undefined}
        loading={valid && summary.isPending}
        updating={
          valid && summary.isFetching && !summary.isPending && !summary.isError
        }
        refreshFailed={valid && summary.isError && summary.data !== undefined}
      />
      {valid && input.timeRange && (
        <UsageTrend
          key={`${range.start}:${range.end}:${model}:${provider}:${outcome}`}
          organizationId={organization.id}
          userId={session.userId}
          timeRange={input.timeRange}
          modelId={input.modelId}
          providerId={input.providerId}
          outcome={input.outcome}
        />
      )}
      <ErrorNotice error={records.error} retry={() => void records.refetch()} />
      {records.isPending && valid ? (
        <Loading />
      ) : valid && visible?.length === 0 ? (
        <EmptyState
          title={translate("noMatchingRequests")}
          action={<Button onClick={reset}>{translate("clearFilters")}</Button>}
        >
          {translate("requestsAppearAfterAClientUsesYour")}
        </EmptyState>
      ) : valid ? (
        <div className="records">
          {visible?.map((r) => (
            <Card key={r.id} className="record">
              <div>
                <h2>{r.modelId}</h2>
                <p>
                  {formatDate(r.occurredAt)} ·{" "}
                  {enumLabel(RequestOutcome[r.outcome])}
                </p>
                <p>
                  {translate("tokensInOut", {
                    value0: formatNumber(r.usage?.inputTokens),
                    value1: formatNumber(r.usage?.outputTokens),
                  })}
                </p>
                <p>
                  {translate("estimatedCost", {
                    value0: formatMoney(r.estimatedCost),
                  })}
                </p>
                <small>
                  {translate("requestId", {
                    value0: r.requestId || translate("unknown"),
                  })}
                </small>
              </div>
              <Button onClick={() => setDetail(r)}>
                {translate("viewEstimate")}
              </Button>
            </Card>
          ))}
        </div>
      ) : null}
      <Pagination
        next={records.data?.page?.nextPageToken}
        onNext={setToken}
        onFirst={() => setToken("")}
        hasPrevious={!!token}
        pending={!valid}
      />
      {detail && (
        <Dialog
          title={translate("requestEstimate")}
          onClose={() => setDetail(undefined)}
        >
          <p>{translate("requestId", { value0: detail.requestId })}</p>
          <p>
            {translate("estimatedCost", {
              value0: formatMoney(detail.estimatedCost),
            })}
          </p>
          <p>
            {translate("priceSource", {
              value0: enumLabel(
                detail.appliedPrice
                  ? PriceSource[detail.appliedPrice.source]
                  : undefined,
              ),
            })}
          </p>
          <p>
            {translate("priceRevision", {
              value0:
                detail.appliedPrice?.sourceRevision || translate("unknown"),
            })}
          </p>
          <p>
            {translate("applied", {
              value0: formatDate(detail.appliedPrice?.appliedAt),
            })}
          </p>
          <p>
            {translate("serviceTier", {
              value0: detail.appliedPrice?.serviceTier || translate("unknown"),
            })}
          </p>
          <Notice>{translate("theAppliedPriceAndEstimateAreFixed")}</Notice>
          <Button onClick={() => setDetail(undefined)}>
            {translate("done")}
          </Button>
        </Dialog>
      )}
    </>
  );
}

export function UsageTrend({
  organizationId,
  userId,
  timeRange,
  modelId,
  providerId,
  outcome,
}: {
  organizationId: string;
  userId: string;
  timeRange: {
    start: ReturnType<typeof timestampFromDate>;
    end: ReturnType<typeof timestampFromDate>;
  };
  modelId?: string;
  providerId?: string;
  outcome?: RequestOutcome;
}) {
  useLocale();
  const [token, setToken] = useState("");
  const query = useQuery(UsageService.method.listDailyUsage, {
    organizationId,
    userId,
    timeRange,
    modelId,
    providerId,
    outcome,
    page: { pageSize, pageToken: token },
  });
  // Only the current ListDailyUsage page is shown; absent dates are never
  // filled in and entries keep the server order.
  const days = query.data?.days ?? [];
  const max = days.reduce((m, d) => {
    const n = d.summary?.requestCount;
    return n !== undefined && n > m ? n : m;
  }, 1n);
  const hasData = query.data !== undefined;
  const failedWithoutData = query.isError && !hasData;
  return (
    <Card>
      <h2>{translate("requestsOverTime")}</h2>
      <p>{translate("dailyRecordedRequestsInUtcMissingRecords")}</p>
      <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      <RefreshStatus
        updating={hasData && query.isFetching && !query.isError}
        failed={hasData && query.isError}
      />
      {query.isPending ? (
        <Loading label={translate("loadingDailyUsage")} />
      ) : failedWithoutData ? null : days.length === 0 ? (
        <p>{translate("noDailyRecordsAvailableForThisRange")}</p>
      ) : (
        <>
          <div className="usage-bars-viewport">
            <div className="usage-bars" aria-hidden="true">
              {days.map((d, i) => {
                const count = d.summary?.requestCount;
                return (
                  <div key={`${d.date}:${d.modelId}:${i}`}>
                    <span className="bar-count">{formatNumber(count)}</span>
                    {count === undefined ? (
                      <span className="usage-gap" />
                    ) : (
                      <span
                        className={
                          count === 0n ? "usage-bar zero" : "usage-bar"
                        }
                        style={{
                          height: `${Number((count * 100n) / max)}%`,
                        }}
                      />
                    )}
                    <small>{d.date.slice(5)}</small>
                  </div>
                );
              })}
            </div>
          </div>
          <details>
            <summary>{translate("viewDailyDataTable")}</summary>
            <div
              className="table-wrap"
              role="region"
              aria-label={translate("dailyUsageTable")}
              tabIndex={0}
            >
              <table>
                <caption>{translate("dailyRecordedUsageUtc")}</caption>
                <thead>
                  <tr>
                    <th>{translate("date")}</th>
                    <th>{translate("model")}</th>
                    <th>{translate("requests")}</th>
                    <th>{translate("knownEstimatedCost")}</th>
                    <th>{translate("unknownCostCount")}</th>
                  </tr>
                </thead>
                <tbody>
                  {days.map((d, i) => (
                    <tr key={i}>
                      <th scope="row">{d.date || translate("unknown")}</th>
                      <td>{d.modelId || translate("allModels")}</td>
                      <td>{formatNumber(d.summary?.requestCount)}</td>
                      <td>
                        {d.summary?.knownCosts.length ? (
                          <MoneyList costs={d.summary.knownCosts} />
                        ) : (
                          translate("unknown")
                        )}
                      </td>
                      <td>{formatNumber(d.summary?.missingCostCount)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </details>
        </>
      )}
      <Pagination
        label={translate("dailyUsagePages")}
        next={query.data?.page?.nextPageToken}
        onNext={setToken}
        onFirst={() => setToken("")}
        hasPrevious={!!token}
        pending={query.isFetching && !hasData}
      />
    </Card>
  );
}
