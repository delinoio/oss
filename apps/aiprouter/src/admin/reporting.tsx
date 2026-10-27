// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, formatNumber } from "../i18n";
import { useContext, useEffect, useId, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  AuditAction,
  AuditOutcome,
  AuditService,
  OperatorService,
  UsageService,
  RequestOutcome,
  PriceSource,
  type AuditRecord,
  type UsageSummary,
  type UsageRecord,
} from "@aiprouter/client";
import { Card, PageHeader } from "../ui";
import {
  AdminAuthority,
  Dialog,
  Empty,
  ErrorNotice,
  FilterSheet,
  Loading,
  Pager,
  date,
  label,
  money,
  useMediaQuery,
} from "./common";
function initialDates() {
  const end = new Date();
  const start = new Date(end.getTime() - 7 * 86400000);
  return {
    start: start.toISOString().slice(0, 10),
    end: end.toISOString().slice(0, 10),
  };
}
export function timeRange(start: string, end: string) {
  return {
    start: timestampFromDate(new Date(`${start}T00:00:00Z`)),
    end: timestampFromDate(new Date(`${end}T23:59:59.999Z`)),
  };
}
export function Summary({ summary }: { summary?: UsageSummary }) {
  useLocale();
  return (
    <Card>
      <h2>{translate("usageSummary")}</h2>
      {summary ? (
        <>
          <div className="admin-metrics">
            <div>
              <span>{translate("requests")}</span>
              <strong>{String(summary.requestCount)}</strong>
            </div>
            <div>
              <span>{translate("knownEstimatedCost")}</span>
              <strong>
                {summary.knownCosts.length
                  ? summary.knownCosts.map(money).join(" · ")
                  : translate("unknown")}
              </strong>
            </div>
            <div>
              <span>{translate("confirmedInputTokens")}</span>
              <strong>{formatNumber(summary.knownUsage?.inputTokens)}</strong>
            </div>
            <div>
              <span>{translate("confirmedOutputTokens")}</span>
              <strong>{formatNumber(summary.knownUsage?.outputTokens)}</strong>
            </div>
          </div>
          <p>
            {translate("requestsWithUnknownCostMissingUsageMissing", {
              value0: String(summary.missingCostCount),
              value1: String(summary.missingUsageCount),
              value2: String(summary.missingPriceCount),
            })}
          </p>
          <p>
            {translate("failedRequestsRecordedMetadataMayBeIncomplete", {
              value0: String(summary.failedRequestCount),
            })}
          </p>
        </>
      ) : (
        <Empty>{translate("usageSummaryIsUnavailable")}</Empty>
      )}
      <p>{translate("estimatesNotBillingCurrencyIsShownWith")}</p>
    </Card>
  );
}
export function OrganizationUsage() {
  useLocale();
  const { organizationId } = useContext(AdminAuthority);
  const [dates, setDates] = useState(initialDates);
  const [userId, setUserId] = useState("");
  const [modelId, setModelId] = useState("");
  const [providerId, setProviderId] = useState("");
  const [dailyToken, setDailyToken] = useState("");
  const [outcome, setOutcome] = useState("");
  const [token, setToken] = useState("");
  const [detail, setDetail] = useState<UsageRecord>();
  const valid = !!dates.start && !!dates.end && dates.start <= dates.end;
  const filters = {
    organizationId,
    userId,
    modelId: modelId || undefined,
    providerId: providerId || undefined,
    outcome: outcome === "" ? undefined : (Number(outcome) as RequestOutcome),
    timeRange: valid ? timeRange(dates.start, dates.end) : undefined,
  };
  const query = useQuery(
    UsageService.method.listUsage,
    { ...filters, page: { pageSize: 100, pageToken: token } },
    { enabled: valid },
  );
  const summary = useQuery(UsageService.method.getUsageSummary, filters, {
    enabled: valid,
  });
  const daily = useQuery(
    UsageService.method.listDailyUsage,
    {
      ...filters,
      page: { pageSize: 100, pageToken: dailyToken },
    },
    { enabled: valid },
  );
  const records = query.data?.records ?? [];
  function reset() {
    setDates(initialDates());
    setUserId("");
    setModelId("");
    setProviderId("");
    setOutcome("");
    setToken("");
    setDailyToken("");
  }
  return (
    <>
      <PageHeader
        title={translate("organizationUsage")}
        description={translate("perMemberRequestMetadataNoCredentialsPrompts")}
      />
      <Card>
        <details className="admin-filters" open>
          <summary>{translate("filters_546ebb")}</summary>
          <div className="admin-form-grid">
            <label>
              {translate("fromUtc")}
              <input
                type="date"
                value={dates.start}
                onChange={(e) => {
                  setDates({ ...dates, start: e.target.value });
                  setToken("");
                  setDailyToken("");
                }}
              />
            </label>
            <label>
              {translate("throughUtc")}
              <input
                type="date"
                value={dates.end}
                onChange={(e) => {
                  setDates({ ...dates, end: e.target.value });
                  setToken("");
                  setDailyToken("");
                }}
              />
            </label>
            <label>
              {translate("memberId")}
              <input
                value={userId}
                onChange={(e) => {
                  setUserId(e.target.value);
                  setToken("");
                  setDailyToken("");
                }}
                placeholder={translate("allMembers")}
              />
            </label>
            <label>
              {translate("model")}
              <input
                value={modelId}
                onChange={(e) => {
                  setModelId(e.target.value);
                  setToken("");
                  setDailyToken("");
                }}
                placeholder={translate("allModels")}
              />
            </label>
            <label>
              {translate("provider")}
              <input
                value={providerId}
                placeholder={translate("allProviders")}
                onChange={(e) => {
                  setProviderId(e.target.value);
                  setToken("");
                  setDailyToken("");
                }}
              />
            </label>
            <label>
              {translate("outcome")}
              <select
                value={outcome}
                onChange={(e) => {
                  setOutcome(e.target.value);
                  setToken("");
                  setDailyToken("");
                }}
              >
                <option value="">{translate("allOutcomes")}</option>
                {Object.values(RequestOutcome)
                  .filter((v) => typeof v === "number")
                  .map((v) => (
                    <option value={v} key={v}>
                      {v === RequestOutcome.UNSPECIFIED
                        ? translate("unknown")
                        : label(RequestOutcome[v as number])}
                    </option>
                  ))}
              </select>
            </label>
          </div>
          <button onClick={reset}>{translate("clearFilters")}</button>
        </details>
        {!valid && <p role="alert">{translate("chooseAnEndDateOnOrAfter")}</p>}
      </Card>
      {valid && (
        <>
          {summary.isPending ? (
            <Loading />
          ) : summary.error ? (
            <ErrorNotice
              error={summary.error}
              retry={() => void summary.refetch()}
            />
          ) : (
            <Summary summary={summary.data?.summary} />
          )}
          <Card>
            <h2>{translate("dailyUsage")}</h2>
            {daily.isPending ? (
              <Loading />
            ) : daily.error ? (
              <ErrorNotice
                error={daily.error}
                retry={() => void daily.refetch()}
              />
            ) : (
              <>
                <div className="admin-records">
                  {daily.data?.days.map((day, index) => (
                    <article
                      className="admin-record"
                      key={`${day.date}:${day.userId}:${day.modelId}:${index}`}
                    >
                      <div>
                        <h3>
                          {translate("utc", {
                            value0: day.date,
                            value1: day.modelId || translate("allModels"),
                          })}
                        </h3>
                        <p>
                          {translate("member_0975eb", {
                            value0:
                              day.userId || translate("message_152bc3aa4e"),
                          })}
                        </p>
                        <p>
                          {translate("requests_f598de", {
                            value0: formatNumber(day.summary?.requestCount),
                          })}
                        </p>
                        <p>
                          {translate("knownEstimatedCost_ad4501", {
                            value0: day.summary?.knownCosts.length
                              ? day.summary.knownCosts.map(money).join(" · ")
                              : translate("unknown"),
                          })}
                        </p>
                        <p>
                          {translate("unknownCostMissingUsageMissingPrice", {
                            value0: formatNumber(day.summary?.missingCostCount),
                            value1: formatNumber(
                              day.summary?.missingUsageCount,
                            ),
                            value2: formatNumber(
                              day.summary?.missingPriceCount,
                            ),
                          })}
                        </p>
                      </div>
                    </article>
                  ))}
                </div>
                {!daily.data?.days.length && (
                  <Empty>{translate("noDailyUsageMatchesTheseFilters")}</Empty>
                )}
                <Pager
                  token={dailyToken}
                  next={daily.data?.page?.nextPageToken}
                  onChange={setDailyToken}
                />
              </>
            )}
          </Card>
          <Card>
            <h2>{translate("requestMetadata")}</h2>
            {query.isPending ? (
              <Loading />
            ) : query.error ? (
              <ErrorNotice
                error={query.error}
                retry={() => void query.refetch()}
              />
            ) : (
              <>
                <div className="admin-records">
                  {records.map((record) => (
                    <article className="admin-record" key={record.id}>
                      <div>
                        <h3>{record.modelId}</h3>
                        <p>{date(record.occurredAt)}</p>
                        <p>
                          {translate("member_827fcf", {
                            value0:
                              record.userId || translate("message_152bc3aa4e"),
                            value1:
                              record.outcome === RequestOutcome.UNSPECIFIED
                                ? translate("unknownOutcome")
                                : label(RequestOutcome[record.outcome]),
                          })}
                        </p>
                        <p>
                          {translate("inputOutput", {
                            value0: formatNumber(record.usage?.inputTokens),
                            value1: formatNumber(record.usage?.outputTokens),
                          })}
                        </p>
                        <p>
                          {translate("estimatedCost", {
                            value0: money(record.estimatedCost),
                          })}
                        </p>
                        <p>
                          {translate("requestId", {
                            value0: record.requestId || translate("unknown"),
                          })}
                        </p>
                      </div>
                      <button onClick={() => setDetail(record)}>
                        {translate("viewEstimateDetails")}
                      </button>
                    </article>
                  ))}
                </div>
                {!records.length && (
                  <Empty>
                    {translate("noRequestsMatchTheseFiltersClearFilters")}
                  </Empty>
                )}
                <Pager
                  token={token}
                  next={query.data?.page?.nextPageToken}
                  onChange={setToken}
                />
              </>
            )}
          </Card>
        </>
      )}
      {detail && (
        <Dialog
          title={translate("requestEstimate")}
          onClose={() => setDetail(undefined)}
          initial="cancel"
        >
          <p>{translate("requestId", { value0: detail.requestId })}</p>
          <p>
            {translate("estimatedCost", {
              value0: money(detail.estimatedCost),
            })}
          </p>
          <p>
            {translate("priceProvenance", {
              value0: detail.appliedPrice
                ? label(PriceSource[detail.appliedPrice.source])
                : translate("unknown"),
            })}
          </p>
          <p>
            {translate("revision", {
              value0:
                detail.appliedPrice?.sourceRevision || translate("unknown"),
            })}
          </p>
          <p>
            {translate("serviceTier", {
              value0: detail.appliedPrice?.serviceTier || translate("unknown"),
            })}
          </p>
          <p>{translate("thisEstimateWasFixedWhenTheRequest")}</p>
          <p>
            {translate("errorCode", {
              value0: detail.errorCode || translate("message_ffcae7fc61"),
            })}
          </p>
          <button data-cancel onClick={() => setDetail(undefined)}>
            {translate("close")}
          </button>
        </Dialog>
      )}
    </>
  );
}
export function Audit({ operator = false }: { operator?: boolean }) {
  return operator ? <InstanceAudit /> : <OrganizationAudit />;
}
function InstanceAudit() {
  useLocale();
  const [token, setToken] = useState("");
  const [actor, setActor] = useState("");
  const [action, setAction] = useState("");
  const [outcome, setOutcome] = useState("");
  const [dates, setDates] = useState({ start: "", end: "" });
  const valid =
    (!dates.start && !dates.end) ||
    (!!dates.start && !!dates.end && dates.start <= dates.end);
  const range =
    dates.start && valid ? timeRange(dates.start, dates.end) : undefined;
  const filters = {
    page: { pageSize: 100, pageToken: token },
    timeRange: range,
    action: action === "" ? undefined : (Number(action) as AuditAction),
    actorUserId: actor || undefined,
    outcome: outcome === "" ? undefined : (Number(outcome) as AuditOutcome),
  };
  const query = useQuery(OperatorService.method.listInstanceAudit, filters, {
    enabled: valid,
  });
  const records = query.data?.records ?? [];
  return (
    <>
      <PageHeader
        title={translate("instanceAudit")}
        description={translate(
          "permissionLifecycleAndConfigurationEventsSafeMetadata",
        )}
      />
      <Card>
        <details open className="admin-filters">
          <summary>{translate("filters_546ebb")}</summary>
          <div className="admin-form-grid">
            <label>
              {translate("actorId")}
              <input
                value={actor}
                onChange={(e) => {
                  setActor(e.target.value);
                  setToken("");
                }}
              />
            </label>
            <label>
              {translate("action")}
              <select
                value={action}
                onChange={(e) => {
                  setAction(e.target.value);
                  setToken("");
                }}
              >
                <option value="">{translate("allActions")}</option>
                {Object.values(AuditAction)
                  .filter((v) => typeof v === "number")
                  .map((v) => (
                    <option key={v} value={v}>
                      {label(AuditAction[v as number])}
                    </option>
                  ))}
              </select>
            </label>
            <label>
              {translate("outcome")}
              <select
                value={outcome}
                onChange={(e) => {
                  setOutcome(e.target.value);
                  setToken("");
                }}
              >
                <option value="">{translate("allOutcomes")}</option>
                {Object.values(AuditOutcome)
                  .filter((v) => typeof v === "number")
                  .map((value) => (
                    <option key={value} value={value}>
                      {value === AuditOutcome.UNSPECIFIED
                        ? translate("unknown")
                        : label(AuditOutcome[value as number])}
                    </option>
                  ))}
              </select>
            </label>
            <label>
              {translate("fromUtc")}
              <input
                type="date"
                value={dates.start}
                onChange={(e) => {
                  setDates({ ...dates, start: e.target.value });
                  setToken("");
                }}
              />
            </label>
            <label>
              {translate("throughUtc")}
              <input
                type="date"
                value={dates.end}
                onChange={(e) => {
                  setDates({ ...dates, end: e.target.value });
                  setToken("");
                }}
              />
            </label>
          </div>
          <button
            onClick={() => {
              setActor("");
              setAction("");
              setOutcome("");
              setDates({ start: "", end: "" });
              setToken("");
            }}
          >
            {translate("clearFilters")}
          </button>
        </details>
        {!valid ? (
          <p role="alert">{translate("chooseBothDatesWithTheEndOn")}</p>
        ) : query.isPending ? (
          <Loading />
        ) : query.error ? (
          <ErrorNotice error={query.error} retry={() => void query.refetch()} />
        ) : (
          <>
            <div className="admin-records">
              {records.map((record) => (
                <AuditRow key={record.id} record={record} />
              ))}
            </div>
            {!records.length && (
              <Empty>{translate("noAuditRecordsMatchClearFiltersOr")}</Empty>
            )}
            <Pager
              token={token}
              next={query.data?.page?.nextPageToken}
              onChange={setToken}
            />
          </>
        )}
      </Card>
    </>
  );
}
function AuditRow({ record }: { record: AuditRecord }) {
  useLocale();
  return (
    <article className="admin-record">
      <div>
        <h2>
          {record.action === AuditAction.UNSPECIFIED
            ? translate("unknownAction")
            : label(AuditAction[record.action])}
        </h2>
        <p>{date(record.occurredAt)}</p>
        <p>
          {translate("actor", {
            value0: record.actorUserId || translate("message_152bc3aa4e"),
          })}
        </p>
        <p>
          {translate("target", {
            value0: record.resourceId || translate("unknown"),
          })}
        </p>
        <p>
          {translate("outcome_3bacad", {
            value0:
              record.outcome === AuditOutcome.UNSPECIFIED
                ? translate("unknown")
                : label(AuditOutcome[record.outcome]),
          })}
        </p>
        {record.requestId && (
          <p>{translate("requestId", { value0: record.requestId })}</p>
        )}
      </div>
    </article>
  );
}

// Organization audit presentation. Request semantics match the previous page: the
// server filters and orders records, any filter change resets the cursor, and the
// client never re-filters the returned page.
const compactViewportQuery = "(max-width: 639.98px)";
const tableViewportQuery = "(min-width: 1024px)";

enum AuditTone {
  Neutral = "neutral",
  Success = "success",
  Danger = "danger",
}
interface AuditFilterState {
  actor: string;
  action: string;
  outcome: string;
  start: string;
  end: string;
}

function auditActionLabel(action: AuditAction) {
  return action === AuditAction.UNSPECIFIED
    ? translate("unknownAction")
    : label(AuditAction[action]);
}
function auditOutcome(outcome: AuditOutcome): {
  text: string;
  tone: AuditTone;
} {
  switch (outcome) {
    case AuditOutcome.SUCCEEDED:
      return { text: label(AuditOutcome[outcome]), tone: AuditTone.Success };
    case AuditOutcome.FAILED:
    case AuditOutcome.DENIED:
      return { text: label(AuditOutcome[outcome]), tone: AuditTone.Danger };
    default:
      return { text: translate("unknown"), tone: AuditTone.Neutral };
  }
}
function OutcomeBadge({ outcome }: { outcome: AuditOutcome }) {
  useLocale();
  const { text, tone } = auditOutcome(outcome);
  return <span className={`admin-outcome admin-outcome-${tone}`}>{text}</span>;
}
function filterSummary(filters: AuditFilterState) {
  const parts: string[] = [];
  if (filters.actor) parts.push(translate("actor", { value0: filters.actor }));
  if (filters.action !== "")
    parts.push(
      translate("auditFilterAction", {
        value0: label(AuditAction[Number(filters.action)]),
      }),
    );
  if (filters.outcome !== "")
    parts.push(
      translate("outcome_3bacad", {
        value0:
          Number(filters.outcome) === AuditOutcome.UNSPECIFIED
            ? translate("unknown")
            : label(AuditOutcome[Number(filters.outcome)]),
      }),
    );
  if (filters.start && filters.end)
    parts.push(
      translate("auditFilterDates", {
        value0: filters.start,
        value1: filters.end,
      }),
    );
  else if (filters.start)
    parts.push(translate("auditFilterFrom", { value0: filters.start }));
  else if (filters.end)
    parts.push(translate("auditFilterThrough", { value0: filters.end }));
  return parts;
}
// Actor, Action, Outcome and the date range are four categories; an incomplete
// date pair counts once and explicit zero enum choices count as active.
function activeFilterCount(filters: AuditFilterState) {
  return (
    (filters.actor ? 1 : 0) +
    (filters.action !== "" ? 1 : 0) +
    (filters.outcome !== "" ? 1 : 0) +
    (filters.start || filters.end ? 1 : 0)
  );
}

function AuditFilterFields({
  filters,
  onChange,
  invalid,
  describedBy,
}: {
  filters: AuditFilterState;
  onChange: (patch: Partial<AuditFilterState>) => void;
  invalid: boolean;
  describedBy?: string;
}) {
  useLocale();
  const dateProps = {
    "aria-invalid": invalid || undefined,
    "aria-describedby": invalid ? describedBy : undefined,
  };
  return (
    <>
      <label className="admin-audit-actor">
        {translate("actorId")}
        <input
          placeholder={translate("auditActorPlaceholder")}
          value={filters.actor}
          onChange={(e) => onChange({ actor: e.target.value })}
        />
      </label>
      <label>
        {translate("action")}
        <select
          value={filters.action}
          onChange={(e) => onChange({ action: e.target.value })}
        >
          <option value="">{translate("allActions")}</option>
          {Object.values(AuditAction)
            .filter((v) => typeof v === "number")
            .map((v) => (
              <option key={v} value={v}>
                {label(AuditAction[v as number])}
              </option>
            ))}
        </select>
      </label>
      <label>
        {translate("outcome")}
        <select
          value={filters.outcome}
          onChange={(e) => onChange({ outcome: e.target.value })}
        >
          <option value="">{translate("allOutcomes")}</option>
          {Object.values(AuditOutcome)
            .filter((v) => typeof v === "number")
            .map((value) => (
              <option key={value} value={value}>
                {value === AuditOutcome.UNSPECIFIED
                  ? translate("unknown")
                  : label(AuditOutcome[value as number])}
              </option>
            ))}
        </select>
      </label>
      <label>
        {translate("fromUtc")}
        <input
          type="date"
          value={filters.start}
          onChange={(e) => onChange({ start: e.target.value })}
          {...dateProps}
        />
      </label>
      <label>
        {translate("throughUtc")}
        <input
          type="date"
          value={filters.end}
          onChange={(e) => onChange({ end: e.target.value })}
          {...dateProps}
        />
      </label>
    </>
  );
}

function OrganizationAudit() {
  useLocale();
  const { organizationId } = useContext(AdminAuthority);
  const ids = useId();
  const [token, setToken] = useState("");
  const [filters, setFilters] = useState<AuditFilterState>({
    actor: "",
    action: "",
    outcome: "",
    start: "",
    end: "",
  });
  const compact = useMediaQuery(compactViewportQuery);
  const table = useMediaQuery(tableViewportQuery);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const refocusHeading = useRef(false);
  // After the viewport grows, the closed sheet hands focus to the inline Filters heading.
  useEffect(() => {
    if (!compact && refocusHeading.current) {
      refocusHeading.current = false;
      headingRef.current?.focus();
    }
  }, [compact]);
  const { actor, action, outcome, start, end } = filters;
  const valid = (!start && !end) || (!!start && !!end && start <= end);
  const range = start && valid ? timeRange(start, end) : undefined;
  const query = useQuery(
    AuditService.method.listOrganizationAudit,
    {
      organizationId,
      page: { pageSize: 100, pageToken: token },
      timeRange: range,
      action: action === "" ? undefined : (Number(action) as AuditAction),
      actorUserId: actor || undefined,
      outcome: outcome === "" ? undefined : (Number(outcome) as AuditOutcome),
    },
    { enabled: valid },
  );
  const change = (patch: Partial<AuditFilterState>) => {
    setFilters((current) => ({ ...current, ...patch }));
    setToken("");
  };
  const clear = () => {
    setFilters({ actor: "", action: "", outcome: "", start: "", end: "" });
    setToken("");
  };
  const activeCount = activeFilterCount(filters);
  const rangeErrorId = `${ids}-range-error`;
  const rangeHelpId = `${ids}-range-help`;
  const sheetErrorId = `${ids}-sheet-error`;
  const timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const records = query.data?.records ?? [];
  const next = query.data?.page?.nextPageToken;
  const offline = query.fetchStatus === "paused";

  function results() {
    if (!valid)
      return (
        <div className="admin-range-pause">
          <p role="alert" id={rangeErrorId} className="admin-danger-text">
            {translate("chooseBothDatesWithTheEndOn")}
          </p>
          <p id={rangeHelpId}>{translate("auditRangePaused")}</p>
        </div>
      );
    if (!query.data) {
      if (query.error)
        return (
          <ErrorNotice error={query.error} retry={() => void query.refetch()} />
        );
      if (offline)
        return (
          <div role="status" className="admin-error">
            <p>{translate("auditEventsStale")}</p>
            <button onClick={() => void query.refetch()}>
              {translate("tryAgain")}
            </button>
          </div>
        );
      return <Loading label={translate("auditEventsLoading")} />;
    }
    const stale = !!query.error || offline;
    const unfilteredFirstPage = activeCount === 0 && token === "";
    return (
      <>
        {query.error && (
          <ErrorNotice error={query.error} retry={() => void query.refetch()} />
        )}
        {stale && (
          <div role="status" className="admin-stale">
            <p>{translate("auditEventsStale")}</p>
            {!query.error && (
              <button onClick={() => void query.refetch()}>
                {translate("tryAgain")}
              </button>
            )}
          </div>
        )}
        {records.length > 0 ? (
          <>
            <p className="admin-audit-timezone">
              {translate("auditTimesShown", { value0: timeZone })}
            </p>
            {table ? (
              <AuditTable records={records} />
            ) : (
              <div className="admin-audit-cards">
                {records.map((record) => (
                  <AuditCard key={record.id} record={record} />
                ))}
              </div>
            )}
          </>
        ) : unfilteredFirstPage ? (
          <Empty>
            <strong>{translate("auditNoEventsTitle")}</strong>
            <span className="admin-empty-detail">
              {translate("auditNoEventsDetail")}
            </span>
          </Empty>
        ) : (
          <Empty>
            {activeCount > 0
              ? translate("noAuditRecordsMatchClearFiltersOr")
              : translate("auditNoEventsOnPage")}
            {compact && activeCount > 0 && (
              <>
                <br />
                <button onClick={clear}>{translate("clearFilters")}</button>
              </>
            )}
          </Empty>
        )}
        {!(records.length === 0 && unfilteredFirstPage) && (
          <Pager token={token} next={next} onChange={setToken} />
        )}
      </>
    );
  }

  const fields = (describedBy: string) => (
    <AuditFilterFields
      filters={filters}
      onChange={change}
      invalid={!valid}
      describedBy={describedBy}
    />
  );
  return (
    <>
      <PageHeader
        title={translate("organizationAudit")}
        description={translate(
          "permissionLifecycleAndConfigurationEventsSafeMetadata",
        )}
      />
      {compact ? (
        <FilterSheet
          activeCount={activeCount}
          summary={filterSummary(filters)}
          description={translate("auditSheetDescription")}
          onClear={clear}
          onUnmountWhileOpen={() => {
            refocusHeading.current = true;
          }}
          notice={
            !valid && (
              <>
                <p id={sheetErrorId} className="admin-danger-text">
                  {translate("chooseBothDatesWithTheEndOn")}
                </p>
                <p className="admin-audit-helper">
                  {translate("auditRangePaused")}
                </p>
              </>
            )
          }
        >
          {fields(sheetErrorId)}
        </FilterSheet>
      ) : (
        <Card
          className="admin-audit-filters"
          aria-labelledby={`${ids}-filters`}
        >
          <h2 id={`${ids}-filters`} ref={headingRef} tabIndex={-1}>
            {translate("filters_546ebb")}
          </h2>
          <p className="admin-audit-helper">
            {translate("auditFiltersHelper")}
          </p>
          <div className="admin-audit-filter-grid">
            {fields(`${rangeErrorId} ${rangeHelpId}`)}
            <div className="admin-audit-clear">
              <button type="button" onClick={clear}>
                {translate("clearFilters")}
              </button>
            </div>
          </div>
        </Card>
      )}
      <Card aria-labelledby={`${ids}-events`}>
        <h2 id={`${ids}-events`}>{translate("auditEvents")}</h2>
        {results()}
      </Card>
    </>
  );
}

function AuditTable({ records }: { records: AuditRecord[] }) {
  useLocale();
  return (
    <table className="admin-audit-table">
      <thead>
        <tr>
          <th scope="col">{translate("auditTimeColumn")}</th>
          <th scope="col">{translate("auditActorColumn")}</th>
          <th scope="col">{translate("action")}</th>
          <th scope="col">{translate("auditSafeTarget")}</th>
          <th scope="col">{translate("outcome")}</th>
          <th scope="col">{translate("requestId_d561f5")}</th>
        </tr>
      </thead>
      <tbody>
        {records.map((record) => (
          <tr key={record.id}>
            <td>{date(record.occurredAt)}</td>
            <td>{record.actorUserId || translate("message_152bc3aa4e")}</td>
            <td>{auditActionLabel(record.action)}</td>
            <td>{record.resourceId || translate("unknown")}</td>
            <td>
              <OutcomeBadge outcome={record.outcome} />
            </td>
            <td>
              {record.requestId || (
                <span className="admin-visually-hidden">
                  {translate("notProvided")}
                </span>
              )}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
function AuditCard({ record }: { record: AuditRecord }) {
  useLocale();
  const headingId = useId();
  return (
    <article className="admin-audit-card" aria-labelledby={headingId}>
      <h3 id={headingId}>{auditActionLabel(record.action)}</h3>
      <p className="admin-audit-card-meta">
        <span>{date(record.occurredAt)}</span>
        <OutcomeBadge outcome={record.outcome} />
      </p>
      <dl>
        <div>
          <dt>{translate("auditActorColumn")}</dt>
          <dd>{record.actorUserId || translate("message_152bc3aa4e")}</dd>
        </div>
        <div>
          <dt>{translate("auditSafeTarget")}</dt>
          <dd>{record.resourceId || translate("unknown")}</dd>
        </div>
        {record.requestId && (
          <div>
            <dt>{translate("requestId_d561f5")}</dt>
            <dd>{record.requestId}</dd>
          </div>
        )}
      </dl>
    </article>
  );
}
