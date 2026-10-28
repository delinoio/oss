// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, formatNumber } from "../i18n";
import {
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
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
  type DailyUsage,
  type UsageSummary,
  type UsageRecord,
} from "@aiprouter/client";
import { Card, PageHeader } from "../ui";
import {
  AdminAuthority,
  Dialog,
  DialogSize,
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
function SummaryBody({ summary }: { summary?: UsageSummary }) {
  return (
    <>
      {summary ? (
        <>
          <div className="admin-metrics admin-usage-metrics">
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
    </>
  );
}
export function Summary({ summary }: { summary?: UsageSummary }) {
  useLocale();
  return (
    <Card>
      <h2>{translate("usageSummary")}</h2>
      <SummaryBody summary={summary} />
    </Card>
  );
}
type RegionQuery<T> = {
  data?: T;
  error: unknown;
  isFetching: boolean;
  fetchStatus: string;
  refetch: () => unknown;
};
// One region of the organization usage report. Each region keeps its own
// loading, error, refreshing and stale states so sibling regions are never
// blocked, and pending or failed data is never rendered as zeros.
function UsageRegion<T>({
  title,
  loadingLabel,
  retryLabel,
  query,
  className = "",
  headingRef,
  children,
}: {
  title: string;
  loadingLabel: string;
  retryLabel: string;
  query: RegionQuery<T>;
  className?: string;
  // Makes the heading programmatically focusable so a dialog can return focus to it.
  headingRef?: RefObject<HTMLHeadingElement | null>;
  children: (data: T) => ReactNode;
}) {
  useLocale();
  const headingId = useId();
  const retry = () => void query.refetch();
  const staleDataText = translate("usageDataStale");
  const offline = query.fetchStatus === "paused";
  let content;
  if (!query.data) {
    content = query.error ? (
      <ErrorNotice error={query.error} retry={retry} retryLabel={retryLabel} />
    ) : offline ? (
      <div role="status" className="admin-stale">
        <p>{staleDataText}</p>
        <button aria-label={retryLabel} onClick={retry}>
          {translate("tryAgain")}
        </button>
      </div>
    ) : (
      <Loading label={loadingLabel} />
    );
  } else {
    const stale = !!query.error || offline;
    content = (
      <>
        {query.error && (
          <ErrorNotice
            error={query.error}
            retry={retry}
            retryLabel={retryLabel}
          />
        )}
        {stale && (
          <p role="status" className="admin-stale">
            {staleDataText}
          </p>
        )}
        {!stale && query.isFetching && (
          <p role="status" className="admin-stale">
            {translate("refreshing")}
          </p>
        )}
        {children(query.data)}
      </>
    );
  }
  return (
    <Card aria-labelledby={headingId} className={className}>
      <h2
        id={headingId}
        ref={headingRef}
        tabIndex={headingRef ? -1 : undefined}
        className={headingRef ? "admin-focus-target" : undefined}
      >
        {title}
      </h2>
      {content}
    </Card>
  );
}
const usageTableQuery = "(min-width: 1024px)";
function costText(summary?: UsageSummary) {
  return summary?.knownCosts.length
    ? summary.knownCosts.map(money).join(" · ")
    : translate("unknown");
}
function dailyCoverage(summary?: UsageSummary) {
  return translate("unknownCostMissingUsageMissingPrice", {
    value0: formatNumber(summary?.missingCostCount),
    value1: formatNumber(summary?.missingUsageCount),
    value2: formatNumber(summary?.missingPriceCount),
  });
}
function outcomeText(outcome: RequestOutcome) {
  return outcome === RequestOutcome.UNSPECIFIED
    ? translate("unknownOutcome")
    : label(RequestOutcome[outcome]);
}
function tokenText(record: UsageRecord) {
  return translate("inputOutput", {
    value0: formatNumber(record.usage?.inputTokens),
    value1: formatNumber(record.usage?.outputTokens),
  });
}
// Rows are kept exactly as returned: no regrouping, gap days or client filtering.
function dailyKey(day: DailyUsage, index: number) {
  return `${day.date}:${day.userId}:${day.modelId}:${index}`;
}
function DailyUsageTable({ days }: { days: DailyUsage[] }) {
  useLocale();
  return (
    <table
      className="admin-audit-table admin-usage-table"
      aria-label={translate("dailyUsage")}
    >
      <thead>
        <tr>
          <th scope="col">{translate("usageDateUtc")}</th>
          <th scope="col">{translate("memberId")}</th>
          <th scope="col">{translate("model")}</th>
          <th scope="col">{translate("requests")}</th>
          <th scope="col">{translate("knownEstimatedCost")}</th>
          <th scope="col">{translate("usageCoverage")}</th>
        </tr>
      </thead>
      <tbody>
        {days.map((day, index) => (
          <tr key={dailyKey(day, index)}>
            <td>{day.date}</td>
            <td>{day.userId || translate("message_152bc3aa4e")}</td>
            <td>{day.modelId || translate("allModels")}</td>
            <td>{formatNumber(day.summary?.requestCount)}</td>
            <td>{costText(day.summary)}</td>
            <td>{dailyCoverage(day.summary)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
function DailyUsageCard({ day }: { day: DailyUsage }) {
  useLocale();
  const headingId = useId();
  return (
    <article className="admin-audit-card" aria-labelledby={headingId}>
      <h3 id={headingId}>
        {translate("utc", {
          value0: day.date,
          value1: day.modelId || translate("allModels"),
        })}
      </h3>
      <dl>
        <div>
          <dt>{translate("memberId")}</dt>
          <dd>{day.userId || translate("message_152bc3aa4e")}</dd>
        </div>
        <div>
          <dt>{translate("requests")}</dt>
          <dd>{formatNumber(day.summary?.requestCount)}</dd>
        </div>
        <div>
          <dt>{translate("knownEstimatedCost")}</dt>
          <dd>{costText(day.summary)}</dd>
        </div>
        <div>
          <dt>{translate("usageCoverage")}</dt>
          <dd>{dailyCoverage(day.summary)}</dd>
        </div>
      </dl>
    </article>
  );
}
function RequestTable({
  records,
  onDetail,
}: {
  records: UsageRecord[];
  onDetail: (record: UsageRecord) => void;
}) {
  useLocale();
  return (
    <table
      className="admin-audit-table admin-usage-table"
      aria-label={translate("requestMetadata")}
    >
      <thead>
        <tr>
          <th scope="col">{translate("model")}</th>
          <th scope="col">{translate("usageTimeLocal")}</th>
          <th scope="col">{translate("memberId")}</th>
          <th scope="col">{translate("outcome")}</th>
          <th scope="col">{translate("usageInputOutputColumn")}</th>
          <th scope="col">{translate("estimatedCost_9ccba2")}</th>
          <th scope="col">{translate("requestId_d561f5")}</th>
          <th scope="col">
            <span className="admin-visually-hidden">
              {translate("usageDetailsColumn")}
            </span>
          </th>
        </tr>
      </thead>
      <tbody>
        {records.map((record) => (
          <tr key={record.id}>
            <td>{record.modelId || translate("unknown")}</td>
            <td>{date(record.occurredAt)}</td>
            <td>{record.userId || translate("message_152bc3aa4e")}</td>
            <td>{outcomeText(record.outcome)}</td>
            <td>{tokenText(record)}</td>
            <td>{money(record.estimatedCost)}</td>
            <td>{record.requestId || translate("unknown")}</td>
            <td>
              <button type="button" onClick={() => onDetail(record)}>
                {translate("viewEstimateDetails")}
              </button>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
function RequestCard({
  record,
  onDetail,
}: {
  record: UsageRecord;
  onDetail: (record: UsageRecord) => void;
}) {
  useLocale();
  const headingId = useId();
  return (
    <article className="admin-audit-card" aria-labelledby={headingId}>
      <h3 id={headingId}>{record.modelId || translate("unknown")}</h3>
      <p className="admin-audit-card-meta">
        <span>{date(record.occurredAt)}</span>
      </p>
      <dl>
        <div>
          <dt>{translate("memberId")}</dt>
          <dd>{record.userId || translate("message_152bc3aa4e")}</dd>
        </div>
        <div>
          <dt>{translate("outcome")}</dt>
          <dd>{outcomeText(record.outcome)}</dd>
        </div>
        <div>
          <dt>{translate("usageInputOutputColumn")}</dt>
          <dd>{tokenText(record)}</dd>
        </div>
        <div>
          <dt>{translate("estimatedCost_9ccba2")}</dt>
          <dd>{money(record.estimatedCost)}</dd>
        </div>
        <div>
          <dt>{translate("requestId_d561f5")}</dt>
          <dd>{record.requestId || translate("unknown")}</dd>
        </div>
      </dl>
      <button
        type="button"
        className="admin-usage-card-action"
        onClick={() => onDetail(record)}
      >
        {translate("viewEstimateDetails")}
      </button>
    </article>
  );
}
// Below this width the filters collapse into a Filters button and a modal sheet.
const usageSheetQuery = "(max-width: 767.98px)";
interface UsageFilterState {
  start: string;
  end: string;
  userId: string;
  modelId: string;
  providerId: string;
  outcome: string;
}
// Member ID, Model, Provider and Outcome count individually (an explicit Unknown
// outcome counts); the date pair counts once when either date differs from the
// default pair the page was mounted or last cleared with, including invalid dates.
function usageActiveFilterCount(
  filters: UsageFilterState,
  defaults: { start: string; end: string },
) {
  return (
    (filters.userId ? 1 : 0) +
    (filters.modelId ? 1 : 0) +
    (filters.providerId ? 1 : 0) +
    (filters.outcome !== "" ? 1 : 0) +
    (filters.start !== defaults.start || filters.end !== defaults.end ? 1 : 0)
  );
}
function UsageFilterFields({
  filters,
  onChange,
  dateProps,
}: {
  filters: UsageFilterState;
  onChange: (patch: Partial<UsageFilterState>) => void;
  dateProps: { "aria-invalid"?: boolean; "aria-describedby"?: string };
}) {
  useLocale();
  return (
    <>
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
      <label>
        {translate("memberId")}
        <input
          value={filters.userId}
          onChange={(e) => onChange({ userId: e.target.value })}
          placeholder={translate("allMembers")}
        />
      </label>
      <label>
        {translate("model")}
        <input
          value={filters.modelId}
          onChange={(e) => onChange({ modelId: e.target.value })}
          placeholder={translate("allModels")}
        />
      </label>
      <label>
        {translate("provider")}
        <input
          value={filters.providerId}
          placeholder={translate("allProviders")}
          onChange={(e) => onChange({ providerId: e.target.value })}
        />
      </label>
      <label>
        {translate("outcome")}
        <select
          value={filters.outcome}
          onChange={(e) => onChange({ outcome: e.target.value })}
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
    </>
  );
}
// Read-only detail of one returned usage record. It opens from the selected
// record without another request, so it can never reprice a historical estimate.
function RequestEstimateDialog({
  record,
  onClose,
  returnFocusFallback,
}: {
  record: UsageRecord;
  onClose: () => void;
  returnFocusFallback: RefObject<HTMLElement | null>;
}) {
  useLocale();
  const descriptionId = useId();
  const price = record.appliedPrice;
  return (
    <Dialog
      title={translate("requestEstimate")}
      onClose={onClose}
      initial="cancel"
      size={DialogSize.Wide}
      describedBy={descriptionId}
      returnFocusFallback={returnFocusFallback}
    >
      <div className="admin-estimate-cost">
        <span>{translate("estimatedCost_9ccba2")}</span>
        <strong>{money(record.estimatedCost)}</strong>
      </div>
      <dl className="admin-estimate-details">
        <div>
          <dt>{translate("requestId_d561f5")}</dt>
          <dd>{record.requestId || translate("unknown")}</dd>
        </div>
        <div>
          <dt>{translate("estimatePriceProvenance")}</dt>
          <dd>
            {price ? label(PriceSource[price.source]) : translate("unknown")}
          </dd>
        </div>
        <div>
          <dt>{translate("estimateRevision")}</dt>
          <dd>{price?.sourceRevision || translate("unknown")}</dd>
        </div>
        <div>
          <dt>{translate("serviceTier_e9cf60")}</dt>
          <dd>{price?.serviceTier || translate("unknown")}</dd>
        </div>
        <div>
          <dt>{translate("estimateErrorCode")}</dt>
          <dd>{record.errorCode || translate("message_ffcae7fc61")}</dd>
        </div>
      </dl>
      <p id={descriptionId} className="admin-estimate-note">
        {translate("thisEstimateWasFixedWhenTheRequest")}
      </p>
      <div className="admin-actions">
        <button data-cancel onClick={onClose}>
          {translate("close")}
        </button>
      </div>
    </Dialog>
  );
}
export function OrganizationUsage() {
  useLocale();
  const { organizationId } = useContext(AdminAuthority);
  const rangeErrorId = useId();
  const sheetErrorId = useId();
  // The default pair is the reference for the date part of the active-filter
  // count; Clear filters supplies a fresh pair so clearing returns the count to zero.
  const [defaults, setDefaults] = useState(initialDates);
  const [dates, setDates] = useState(defaults);
  const [userId, setUserId] = useState("");
  const [modelId, setModelId] = useState("");
  const [providerId, setProviderId] = useState("");
  const [dailyToken, setDailyToken] = useState("");
  const [outcome, setOutcome] = useState("");
  const [token, setToken] = useState("");
  const [detail, setDetail] = useState<UsageRecord>();
  const requestHeadingRef = useRef<HTMLHeadingElement>(null);
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
  const table = useMediaQuery(usageTableQuery);
  const compact = useMediaQuery(usageSheetQuery);
  const filterSummaryRef = useRef<HTMLElement>(null);
  const refocusFilters = useRef(false);
  // After the viewport grows, the closed sheet hands focus to the inline Filters disclosure.
  useEffect(() => {
    if (!compact && refocusFilters.current) {
      refocusFilters.current = false;
      filterSummaryRef.current?.focus();
    }
  }, [compact]);
  const filterState: UsageFilterState = {
    ...dates,
    userId,
    modelId,
    providerId,
    outcome,
  };
  const activeCount = usageActiveFilterCount(filterState, defaults);
  const filtersActive = !!userId || !!modelId || !!providerId || outcome !== "";
  // A successful report with no data at all is an expected first-use state.
  const reportEmpty =
    !filtersActive &&
    !token &&
    !dailyToken &&
    summary.data?.summary?.requestCount === 0n &&
    daily.data?.days.length === 0 &&
    query.data?.records.length === 0;
  // Both date inputs share the validation message so assistive technology
  // announces it from either field.
  const dateProps = {
    "aria-invalid": valid ? undefined : true,
    "aria-describedby": valid ? undefined : rangeErrorId,
  };
  function resetCursors() {
    setToken("");
    setDailyToken("");
  }
  function changeFilters(patch: Partial<UsageFilterState>) {
    if (patch.start !== undefined || patch.end !== undefined)
      setDates((current) => ({
        start: patch.start ?? current.start,
        end: patch.end ?? current.end,
      }));
    if (patch.userId !== undefined) setUserId(patch.userId);
    if (patch.modelId !== undefined) setModelId(patch.modelId);
    if (patch.providerId !== undefined) setProviderId(patch.providerId);
    if (patch.outcome !== undefined) setOutcome(patch.outcome);
    resetCursors();
  }
  function reset() {
    const fresh = initialDates();
    setDefaults(fresh);
    setDates(fresh);
    setUserId("");
    setModelId("");
    setProviderId("");
    setOutcome("");
    resetCursors();
  }
  return (
    <>
      <PageHeader
        title={translate("organizationUsage")}
        description={translate("perMemberRequestMetadataNoCredentialsPrompts")}
      />
      {compact ? (
        <>
          <FilterSheet
            activeCount={activeCount}
            dialogTitle={translate("usageFilters")}
            closeLabel={translate("closeUsageFilters")}
            summary={
              dates.start && dates.end
                ? [
                    translate("usageRangeUtc", {
                      value0: dates.start,
                      value1: dates.end,
                    }),
                  ]
                : []
            }
            description={translate("usageSheetDescription")}
            onClear={reset}
            onUnmountWhileOpen={() => {
              refocusFilters.current = true;
            }}
            notice={
              !valid && (
                <p role="alert" id={sheetErrorId} className="admin-danger-text">
                  {translate("chooseAnEndDateOnOrAfter")}
                </p>
              )
            }
          >
            <UsageFilterFields
              filters={filterState}
              onChange={changeFilters}
              dateProps={
                valid
                  ? {}
                  : { "aria-invalid": true, "aria-describedby": sheetErrorId }
              }
            />
          </FilterSheet>
          {!valid && (
            <p className="admin-danger-text">
              {translate("chooseAnEndDateOnOrAfter")}
            </p>
          )}
        </>
      ) : (
        <>
          {dates.start && dates.end && (
            <p className="admin-usage-range">
              {translate("usageRangeUtc", {
                value0: dates.start,
                value1: dates.end,
              })}
            </p>
          )}
          <Card className="admin-usage-filters">
            <details className="admin-filters" open>
              <summary ref={filterSummaryRef}>
                {translate("filters_546ebb")}
              </summary>
              <div className="admin-usage-filter-grid">
                <UsageFilterFields
                  filters={filterState}
                  onChange={changeFilters}
                  dateProps={dateProps}
                />
              </div>
              <div className="admin-actions">
                <button type="button" onClick={reset}>
                  {translate("clearFilters")}
                </button>
              </div>
            </details>
            {!valid && (
              <p role="alert" id={rangeErrorId} className="admin-danger-text">
                {translate("chooseAnEndDateOnOrAfter")}
              </p>
            )}
          </Card>
        </>
      )}
      {valid && (
        <>
          <UsageRegion
            title={translate("usageSummary")}
            loadingLabel={translate("loadingUsageSummary")}
            retryLabel={translate("tryAgainLoadingUsageSummary")}
            query={summary}
            className="admin-usage-summary"
          >
            {(data) => <SummaryBody summary={data.summary} />}
          </UsageRegion>
          {reportEmpty && (
            <Empty>{translate("usageNoneRecordedForDates")}</Empty>
          )}
          <UsageRegion
            title={translate("dailyUsage")}
            loadingLabel={translate("loadingDailyUsage")}
            retryLabel={translate("tryAgainLoadingDailyUsage")}
            query={daily}
          >
            {(data) => (
              <>
                {data.days.length > 0 &&
                  (table ? (
                    <DailyUsageTable days={data.days} />
                  ) : (
                    <div className="admin-audit-cards">
                      {data.days.map((day, index) => (
                        <DailyUsageCard key={dailyKey(day, index)} day={day} />
                      ))}
                    </div>
                  ))}
                {!data.days.length && !reportEmpty && (
                  <Empty>{translate("noDailyUsageMatchesTheseFilters")}</Empty>
                )}
                <Pager
                  label={translate("dailyUsagePages")}
                  nextLabel={translate("usageNextDaily")}
                  token={dailyToken}
                  next={data.page?.nextPageToken}
                  onChange={setDailyToken}
                />
              </>
            )}
          </UsageRegion>
          <UsageRegion
            title={translate("requestMetadata")}
            headingRef={requestHeadingRef}
            loadingLabel={translate("loadingRequests")}
            retryLabel={translate("tryAgainLoadingRequests")}
            query={query}
          >
            {(data) => (
              <>
                {data.records.length > 0 &&
                  (table ? (
                    <RequestTable records={data.records} onDetail={setDetail} />
                  ) : (
                    <div className="admin-audit-cards">
                      {data.records.map((record) => (
                        <RequestCard
                          key={record.id}
                          record={record}
                          onDetail={setDetail}
                        />
                      ))}
                    </div>
                  ))}
                {!data.records.length && !reportEmpty && (
                  <Empty>
                    {translate("noRequestsMatchTheseFiltersClearFilters")}
                    {filtersActive && (
                      <>
                        <br />
                        <button type="button" onClick={reset}>
                          {translate("clearFilters")}
                        </button>
                      </>
                    )}
                  </Empty>
                )}
                <Pager
                  label={translate("usageRequestPages")}
                  nextLabel={translate("usageNextRequests")}
                  token={token}
                  next={data.page?.nextPageToken}
                  onChange={setToken}
                />
              </>
            )}
          </UsageRegion>
        </>
      )}
      {detail && (
        <RequestEstimateDialog
          record={detail}
          onClose={() => setDetail(undefined)}
          returnFocusFallback={requestHeadingRef}
        />
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
