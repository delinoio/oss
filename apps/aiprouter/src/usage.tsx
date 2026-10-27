// SPDX-License-Identifier: Apache-2.0
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
  return (
    <div className="stats">
      <Card>
        <p>Requests</p>
        <strong>{summary?.requestCount.toString() ?? "Unknown"}</strong>
      </Card>
      <Card>
        <p>Known estimated cost</p>
        <strong>
          {summary?.knownCosts.length
            ? summary.knownCosts.map(formatMoney).join(" · ")
            : "Unknown"}
        </strong>
        <p>
          {summary
            ? `${summary.missingCostCount} requests with unknown cost`
            : "Unknown cost coverage"}
        </p>
      </Card>
      <Card>
        <p>Confirmed tokens</p>
        <strong>
          {summary?.knownUsage?.inputTokens?.toString() ?? "Unknown"} in /{" "}
          {summary?.knownUsage?.outputTokens?.toString() ?? "Unknown"} out
        </strong>
        <p>
          {summary
            ? `${summary.missingUsageCount} missing usage · ${summary.missingPriceCount} missing prices`
            : "Unknown usage coverage"}
        </p>
      </Card>
    </div>
  );
}
export function UsagePage({ organization, session }: PageProps) {
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
      <Field label="Start date (UTC)">
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
      <Field label="End date (UTC)">
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
      <Field label="Model ID">
        <input
          value={model}
          onChange={(e) => {
            setModel(e.target.value);
            setToken("");
          }}
        />
      </Field>
      <Field label="Provider ID">
        <input
          value={provider}
          onChange={(e) => {
            setProvider(e.target.value);
            setToken("");
          }}
        />
      </Field>
      <Field label="Outcome">
        <select
          value={outcome}
          onChange={(e) => {
            setOutcome(Number(e.target.value) as RequestOutcome | -1);
            setToken("");
          }}
        >
          <option value={-1}>All outcomes</option>
          <option value={RequestOutcome.UNSPECIFIED}>Unknown outcome</option>
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
      <Button onClick={reset}>Clear filters</Button>
    </>
  );
  const visible = records.data?.records;
  return (
    <>
      <PageHeader
        title="Your usage"
        description={`Your requests in ${organization.name}. Last 7 days by default. Times shown in UTC.`}
      />
      <Notice>
        Costs are estimates, not billing. Unknown usage and prices are never
        counted as zero. Recording failures can leave totals incomplete.
      </Notice>
      <div className="desktop-filters filters">{filterForm}</div>
      <Button className="mobile-filters" onClick={() => setFilters(true)}>
        Filters ({Number(!!model) + Number(outcome !== -1) + Number(!!provider)}
        )
      </Button>
      {filters && (
        <Dialog title="Usage filters" onClose={() => setFilters(false)}>
          {filterForm}
          <Button variant="primary" onClick={() => setFilters(false)}>
            Done
          </Button>
        </Dialog>
      )}
      {!valid && (
        <Notice tone="danger">
          Choose a valid date range. The end date must be on or after the start
          date.
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
          title="No matching requests"
          action={<Button onClick={reset}>Clear filters</Button>}
        >
          Requests appear after a client uses your routing.
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
                  Tokens: {r.usage?.inputTokens?.toString() ?? "Unknown"} in /{" "}
                  {r.usage?.outputTokens?.toString() ?? "Unknown"} out
                </p>
                <p>Estimated cost: {formatMoney(r.estimatedCost)}</p>
                <small>Request ID: {r.requestId || "Unknown"}</small>
              </div>
              <Button onClick={() => setDetail(r)}>View estimate</Button>
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
        <Dialog title="Request estimate" onClose={() => setDetail(undefined)}>
          <p>Request ID: {detail.requestId}</p>
          <p>Estimated cost: {formatMoney(detail.estimatedCost)}</p>
          <p>
            Price source:{" "}
            {enumLabel(
              detail.appliedPrice
                ? PriceSource[detail.appliedPrice.source]
                : undefined,
            )}
          </p>
          <p>
            Price revision: {detail.appliedPrice?.sourceRevision || "Unknown"}
          </p>
          <p>Applied: {formatDate(detail.appliedPrice?.appliedAt)}</p>
          <p>Service tier: {detail.appliedPrice?.serviceTier || "Unknown"}</p>
          <Notice>
            The applied price and estimate are fixed for this request. Later
            price changes do not change this estimate.
          </Notice>
          <Button onClick={() => setDetail(undefined)}>Done</Button>
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
      <h2>Requests over time</h2>
      <p>
        Daily recorded requests in UTC. Missing records remain unknown.
        Estimates include only known costs.
      </p>
      <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <Loading />
      ) : days.length === 0 ? (
        <p>No daily records available for this range.</p>
      ) : (
        <>
          <div className="usage-bars" aria-hidden="true">
            {days.map((d, i) => (
              <div key={`${d.date}:${d.modelId}:${i}`}>
                <span className="bar-count">
                  {d.summary?.requestCount.toString() ?? "Unknown"}
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
            <summary>View daily data table</summary>
            <div className="table-wrap">
              <table>
                <caption>Daily recorded usage (UTC)</caption>
                <thead>
                  <tr>
                    <th>Date</th>
                    <th>Model</th>
                    <th>Requests</th>
                    <th>Known estimated cost</th>
                    <th>Unknown cost count</th>
                  </tr>
                </thead>
                <tbody>
                  {days.map((d, i) => (
                    <tr key={i}>
                      <th scope="row">{d.date}</th>
                      <td>{d.modelId || "All models"}</td>
                      <td>{d.summary?.requestCount.toString() ?? "Unknown"}</td>
                      <td>
                        {d.summary?.knownCosts.length
                          ? d.summary.knownCosts.map(formatMoney).join(" · ")
                          : "Unknown"}
                      </td>
                      <td>
                        {d.summary?.missingCostCount.toString() ?? "Unknown"}
                      </td>
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
