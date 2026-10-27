// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, formatNumber } from "./i18n";
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  UsageService,
  RequestOutcome,
  PriceSource,
  type UsageSummary,
  type UsageRecord,
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
  EmptyState,
  Notice,
  formatDate,
  formatMoney,
  enumLabel,
  Pagination,
} from "./ui";
export function SummaryCards({ summary }: { summary?: UsageSummary }) {
  useLocale();
  return (
    <div className="stats">
      <Card>
        <p>{translate("requests")}</p>
        <strong>{formatNumber(summary?.requestCount)}</strong>
      </Card>
      <Card>
        <p>{translate("knownEstimatedCost")}</p>
        <strong>
          {summary?.knownCosts.length
            ? summary.knownCosts.map(formatMoney).join(" · ")
            : translate("unknown")}
        </strong>
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
  );
}
export function UsagePage({ organization, session }: PageProps) {
  useLocale();
  const [range, setRange] = useState(() => ({
    start: new Date(Date.now() - 6 * 86400000).toISOString().slice(0, 10),
    end: new Date().toISOString().slice(0, 10),
  }));
  const [model, setModel] = useState("");
  const [provider, setProvider] = useState("");
  const [outcome, setOutcome] = useState<RequestOutcome | -1>(-1);
  const [token, setToken] = useState("");
  const [detail, setDetail] = useState<UsageRecord>();
  const [filters, setFilters] = useState(false);
  const valid = !!range.start && !!range.end && range.start <= range.end;
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
    setRange({
      start: new Date(Date.now() - 6 * 86400000).toISOString().slice(0, 10),
      end: new Date().toISOString().slice(0, 10),
    });
    setModel("");
    setProvider("");
    setOutcome(-1);
    setToken("");
  };
  const filterForm = (
    <>
      <Field label={translate("startDateUtc")}>
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
      <Field label={translate("endDateUtc")}>
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
      <Button onClick={reset}>{translate("clearFilters")}</Button>
    </>
  );
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
      <div className="desktop-filters filters">{filterForm}</div>
      <Button className="mobile-filters" onClick={() => setFilters(true)}>
        {translate("filters", {
          value0: Number(!!model) + Number(outcome !== -1) + Number(!!provider),
        })}
      </Button>
      {filters && (
        <Dialog
          title={translate("usageFilters")}
          onClose={() => setFilters(false)}
        >
          {filterForm}
          <Button variant="primary" onClick={() => setFilters(false)}>
            {translate("done")}
          </Button>
        </Dialog>
      )}
      {!valid && (
        <Notice tone="danger">
          {translate("chooseAValidDateRangeTheEnd")}
        </Notice>
      )}
      <ErrorNotice error={summary.error} retry={() => void summary.refetch()} />
      <SummaryCards summary={valid ? summary.data?.summary : undefined} />
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
      ) : visible?.length === 0 ? (
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
  const days = query.data?.days ?? [];
  const max = days.reduce((m, d) => {
    const n = d.summary?.requestCount;
    return n !== undefined && n > m ? n : m;
  }, 1n);
  return (
    <Card>
      <h2>{translate("requestsOverTime")}</h2>
      <p>{translate("dailyRecordedRequestsInUtcMissingRecords")}</p>
      <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <Loading />
      ) : days.length === 0 ? (
        <p>{translate("noDailyRecordsAvailableForThisRange")}</p>
      ) : (
        <>
          <div className="usage-bars" aria-hidden="true">
            {days.map((d, i) => (
              <div key={`${d.date}:${d.modelId}:${i}`}>
                <span className="bar-count">
                  {formatNumber(d.summary?.requestCount)}
                </span>
                <span
                  className="usage-bar"
                  style={{
                    height: d.summary
                      ? `${Number((d.summary.requestCount * 100n) / max)}%`
                      : undefined,
                  }}
                />
                <small>{d.date.slice(5)}</small>
              </div>
            ))}
          </div>
          <details>
            <summary>{translate("viewDailyDataTable")}</summary>
            <div className="table-wrap">
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
                      <th scope="row">{d.date}</th>
                      <td>{d.modelId || translate("allModels")}</td>
                      <td>{formatNumber(d.summary?.requestCount)}</td>
                      <td>
                        {d.summary?.knownCosts.length
                          ? d.summary.knownCosts.map(formatMoney).join(" · ")
                          : translate("unknown")}
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
        next={query.data?.page?.nextPageToken}
        onNext={setToken}
        onFirst={() => setToken("")}
        hasPrevious={!!token}
      />
    </Card>
  );
}
