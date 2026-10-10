// SPDX-License-Identifier: Apache-2.0
import { createPortal } from "react-dom";
import { useEffect } from "react";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { UsageQuery, UsageAccountingProfile, UsageCoverage, type GetUsageSummaryResponse, type Resource, type UsageMeasure } from "@delinoio/delidev-api-client";
import { useSessionActive } from "./session-activity";
import { copy, displayLocale, useLocale } from "./localization";
import { Problem } from "./ui";

export const usageRangeLimit = 366n * 86_400_000n;
export interface LifetimeMeasure { known?: bigint; measured: bigint; unavailable: bigint; missing: boolean }
export interface LifetimeUsage { input: LifetimeMeasure; output: LifetimeMeasure; ranges: GetUsageSummaryResponse[]; incomplete: boolean }
const count = (value: number) => Number.isInteger(value) && value >= 0 && value <= 0xffffffff;
const emptyMeasure = (): LifetimeMeasure => ({ measured: 0n, unavailable: 0n, missing: false });
function addMeasure(sum: LifetimeMeasure, value: UsageMeasure | undefined, responses: number) {
  if (!value) { sum.missing = true; return; }
  if (!count(value.measuredResponses) || !count(value.unavailableResponses) || value.measuredResponses + value.unavailableResponses !== responses || (value.measuredResponses === 0 ? value.knownTotal !== "" : !/^(0|[1-9][0-9]{0,127})$/.test(value.knownTotal))) throw new Error("Invalid response subtotal evidence");
  if (value.measuredResponses) sum.known = (sum.known ?? 0n) + BigInt(value.knownTotal);
  sum.measured += BigInt(value.measuredResponses); sum.unavailable += BigInt(value.unavailableResponses);
}
export function sessionCreationMs(resource: Resource): bigint {
  const value = resource.createdAt;
  const parts = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?Z$/.exec(value);
  const milliseconds = parts ? Date.parse(`${parts[1]}.${(parts[2] ?? "").padEnd(3,"0").slice(0,3)}Z`) : NaN;
  if (!Number.isSafeInteger(milliseconds) || milliseconds <= 0 || new Date(milliseconds).toISOString().slice(0,19) !== parts?.[1]) throw new Error("Invalid session creation metadata");
  return BigInt(milliseconds);
}
/** Sequential disjoint server retention ranges; never an atomic database snapshot.
 * Publish only the completed result. Native/child/cache/reasoning amounts and
 * range-local missing-execution counts are not lifetime additive evidence. */
export async function readLifetimeUsage(created: bigint, read: (from: bigint, until: bigint) => Promise<GetUsageSummaryResponse>, signal?: AbortSignal): Promise<LifetimeUsage> {
  const result: LifetimeUsage = { input: emptyMeasure(), output: emptyMeasure(), ranges: [], incomplete: false };
  let from = 0n, until = 0n;
  do {
    if (signal?.aborted) throw new DOMException("Read canceled", "AbortError");
    const reply = await read(from, until);
    if (signal?.aborted) throw new DOMException("Read canceled", "AbortError");
    if (reply.accountingProfile !== UsageAccountingProfile.UNSPECIFIED || reply.coverage !== UsageCoverage.OBSERVED_ROOT_RESPONSES || reply.fromUnixMs <= 0n || reply.untilUnixMs <= reply.fromUnixMs || reply.untilUnixMs - reply.fromUnixMs > usageRangeLimit || created <= 0n || created > reply.untilUnixMs && result.ranges.length === 0 || (until !== 0n && (reply.fromUnixMs !== from || reply.untilUnixMs !== until)) || !reply.totals || !count(reply.totals.responses) || !count(reply.acceptedExecutionsWithoutResponse) || !count(reply.acceptedCompactionsWithoutResponse)) throw new Error("Invalid session response summary boundary");
    addMeasure(result.input, reply.totals.input, reply.totals.responses);
    addMeasure(result.output, reply.totals.output, reply.totals.responses);
    result.incomplete ||= reply.acceptedExecutionsWithoutResponse > 0 || reply.acceptedCompactionsWithoutResponse > 0;
    result.ranges.push(reply);
    until = reply.fromUnixMs;
    from = until - created > usageRangeLimit ? until - usageRangeLimit : created;
  } while (until > created);
  result.incomplete ||= result.input.missing || result.output.missing || result.input.unavailable > 0n || result.output.unavailable > 0n || result.input.known === undefined || result.output.known === undefined;
  return result;
}
export function SessionLifetimeUsage({ session, diagnosticsTarget }: { session: Resource; diagnosticsTarget?: HTMLElement | null }) {
  useLocale();
  const active = useSessionActive(), transport = useTransport(), client = useQueryClient();
  const base = createQueryOptions(UsageQuery.getUsageSummary, { sessionId: session.id, accountingProfile: UsageAccountingProfile.UNSPECIFIED }, { transport });
  const queryKey = [...base.queryKey, { lifetimeSessionUsage: session.createdAt }];
  const result = useQuery({ queryKey, enabled: active, retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false,
    queryFn: async context => readLifetimeUsage(sessionCreationMs(session), async (fromUnixMs, untilUnixMs) => {
      const options = createQueryOptions(UsageQuery.getUsageSummary, { sessionId: session.id, accountingProfile: UsageAccountingProfile.UNSPECIFIED, fromUnixMs, untilUnixMs }, { transport });
      return options.queryFn({ ...context, queryKey: options.queryKey });
    }, context.signal) });
  useEffect(() => () => { void client.cancelQueries({ queryKey, exact: true }); }, [client, transport, session.id, session.createdAt, active]);
  const value = (metric?: LifetimeMeasure) => metric?.known === undefined ? copy("usage.extra.ca1844969742") : metric.known.toLocaleString(displayLocale());
  return <div className="session-lifetime-usage">
    <div className="session-token-row" aria-label={copy("session.lifetimeTokens")}><span><span className="session-token-label">{copy("session.tokensIn")}</span> <span>{value(result.data?.input)}</span></span><span><span className="session-token-label">{copy("session.tokensOut")}</span> <span>{value(result.data?.output)}</span></span></div>
    <small>{copy("session.responseSubtotals")}</small>
    {result.data?.incomplete ? <p className="notice">{copy("session.incompleteUsage")}</p> : null}
    {result.error ? <><Problem error={result.error}/>{result.data ? <p>{copy("session.staleUsage")}</p> : null}<button disabled={result.isFetching} onClick={() => void result.refetch()}>{copy("session-name.retryRead")}</button></> : null}
    {diagnosticsTarget && result.data ? createPortal(<section aria-label={copy("session.usageRangeEvidence")}><h3>{copy("session.usageRangeEvidence")}</h3><p>{copy("session.usageRangeLimits")}</p><ol>{result.data.ranges.map(range => <li key={range.fromUnixMs.toString()}><p>{range.fromUnixMs.toString()} – {range.untilUnixMs.toString()}</p><dl><dt>{copy("session.tokensIn")}</dt><dd>{range.totals?.input?.knownTotal || copy("usage.extra.ca1844969742")} · {range.totals?.input?.measuredResponses ?? copy("usage.extra.ca1844969742")} / {range.totals?.input?.unavailableResponses ?? copy("usage.extra.ca1844969742")}</dd><dt>{copy("session.tokensOut")}</dt><dd>{range.totals?.output?.knownTotal || copy("usage.extra.ca1844969742")} · {range.totals?.output?.measuredResponses ?? copy("usage.extra.ca1844969742")} / {range.totals?.output?.unavailableResponses ?? copy("usage.extra.ca1844969742")}</dd><dt>{copy("session.rangeMissingExecutions")}</dt><dd>{range.acceptedExecutionsWithoutResponse}</dd><dt>{copy("session.rangeMissingCompactions")}</dt><dd>{range.acceptedCompactionsWithoutResponse}</dd></dl></li>)}</ol></section>,diagnosticsTarget) : null}
  </div>;
}
