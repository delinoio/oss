// SPDX-License-Identifier: Apache-2.0
import { useEffect, useMemo, useRef } from "react";
import { createPortal } from "react-dom";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  UsageAccountingProfile,
  UsageQuery,
  type Resource,
} from "@delinoio/delidev-api-client";
import {
  readLifetimeUsage,
  sessionCreatedAt,
  type LifetimeMeasure,
  type LifetimeUsage,
} from "./session-lifetime-usage-model";
import { document as readDocument, object, text } from "./documents";
import { copy, useLocale } from "./localization";
import { useSessionActive } from "./session-activity";
import { Timestamp } from "./timestamp-display";

// The last complete collection is carried across response-usage generations
// only for one exact owner: the same Session identity, creation time and
// authenticated transport. Any owner change drops it so totals never cross
// Sessions or connections.
type RetainedUsage = {
  sessionId: string;
  createdAt: string | undefined;
  transport: unknown;
  data: LifetimeUsage;
};

export function SessionLifetimeUsage({
  session,
  diagnosticsTarget,
}: {
  session: Resource;
  diagnosticsTarget: HTMLElement | null;
}) {
  const locale = useLocale();
  const active = useSessionActive();
  const transport = useTransport();
  const client = useQueryClient();
  const createdAt = sessionCreatedAt(session);
  // The millisecond bound above floors fractional seconds, so ownership and
  // the query identity use the exact validated RFC 3339 creation timestamp.
  const createdAtExact = createdAt === undefined ? undefined : session.createdAt;
  const responseUsageId = text(
    object(readDocument(session).execution).latest_response_usage_id,
  );
  const request = useMemo(
    () => ({
      sessionId: session.id,
      accountingProfile: UsageAccountingProfile.UNSPECIFIED,
    }),
    [session.id],
  );
  const base = useMemo(
    () =>
      createQueryOptions(UsageQuery.getUsageSummary, request, { transport }),
    [request, transport],
  );
  // Retain the generated RPC prefix so original usage invalidation and the
  // authenticated reconnect refresh this collection as one server-state read.
  const key = useMemo(
    () => [
      ...base.queryKey,
      { sessionLifetimeCreatedAt: createdAtExact ?? "unavailable" },
      { latestResponseUsageId: responseUsageId },
    ],
    [base, createdAtExact, responseUsageId],
  );
  const result = useQuery({
    queryKey: key,
    queryFn: (context) =>
      readLifetimeUsage(
        createdAt!,
        async (range) => {
          const options = createQueryOptions(
            UsageQuery.getUsageSummary,
            { ...request, ...range },
            { transport },
          );
          return options.queryFn!({ ...context, queryKey: options.queryKey });
        },
        context.signal,
      ),
    enabled: active && createdAt !== undefined,
    retry: false,
    gcTime: 0,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: "always",
  });
  const retained = useRef<RetainedUsage | undefined>(undefined);
  const ownedByCurrent = (value: RetainedUsage | undefined) =>
    value !== undefined &&
    value.sessionId === session.id &&
    value.createdAt === createdAtExact &&
    value.transport === transport;
  // Publish only a complete result of the current generation. A replacement
  // read replaces the whole collection at once, and a delayed result from an
  // older generation has no observer here, so it can never write.
  useEffect(() => {
    if (!ownedByCurrent(retained.current)) retained.current = undefined;
    if (result.data)
      retained.current = {
        sessionId: session.id,
        createdAt: createdAtExact,
        transport,
        data: result.data,
      };
  }, [result.data, session.id, createdAtExact, transport]);
  const displayed =
    result.data ??
    (ownedByCurrent(retained.current) ? retained.current?.data : undefined);
  useEffect(() => {
    if (!active) void client.cancelQueries({ queryKey: key, exact: true });
    return () => {
      void client.cancelQueries({ queryKey: key, exact: true });
    };
  }, [active, client, key]);
  const format = (value: LifetimeMeasure | undefined) =>
    value?.known === undefined
      ? copy("session.usageUnavailable")
      : value.known.toLocaleString(locale);
  return (
    <>
      <div
        className="session-lifetime-tokens"
        aria-label={copy("session.lifetimeUsage")}
      >
        <span>
          <span className="session-info-chip">{copy("session.usageIn")}</span>{" "}
          <span>{format(displayed?.input)}</span>
        </span>
        <span>
          <span className="session-info-chip">{copy("session.usageOut")}</span>{" "}
          <span>{format(displayed?.output)}</span>
        </span>
      </div>
      <p>{copy("session.usageIncompleteCoverage")}</p>
      {createdAt === undefined ? (
        <p>{copy("session.usageCreationUnavailable")}</p>
      ) : result.isFetching ? (
        <p role="status">{copy("session.usageReading")}</p>
      ) : result.error ? (
        <p role="status">
          {copy(displayed ? "session.usageStale" : "session.usageReadFailed")}
        </p>
      ) : null}
      {createdAt !== undefined && result.error ? (
        <button
          disabled={result.isFetching}
          onClick={() => void result.refetch()}
        >
          {copy("session-name.retryRead")}
        </button>
      ) : null}
      {diagnosticsTarget
        ? createPortal(
            <section>
              <h3>{copy("session.lifetimeUsage")}</h3>
              <p>{copy("session.usageIndependentRanges")}</p>
              {result.error ? <p>{copy("session.usageReadFailed")}</p> : null}
              {displayed?.ranges.map((range) => (
                <div key={range.fromUnixMs.toString()}>
                  <p>
                    <Timestamp
                      value={new Date(Number(range.fromUnixMs)).toISOString()}
                    />{" "}
                    —{" "}
                    <Timestamp
                      value={new Date(Number(range.untilUnixMs)).toISOString()}
                    />
                  </p>
                  <p>
                    {copy("session.usageRangeEvidence", {
                      input: format(range.input),
                      output: format(range.output),
                      inputMeasured:
                        range.input.measured.toLocaleString(locale),
                      inputUnavailable:
                        range.input.unavailable.toLocaleString(locale),
                      outputMeasured:
                        range.output.measured.toLocaleString(locale),
                      outputUnavailable:
                        range.output.unavailable.toLocaleString(locale),
                      executions:
                        range.missingExecutions.toLocaleString(locale),
                      compactions:
                        range.missingCompactions.toLocaleString(locale),
                    })}
                  </p>
                </div>
              ))}
              {createdAt === undefined ? (
                <p>{copy("session.usageCreationUnavailable")}</p>
              ) : null}
            </section>,
            diagnosticsTarget,
          )
        : null}
    </>
  );
}
