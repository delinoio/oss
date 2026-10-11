// SPDX-License-Identifier: Apache-2.0
import { useEffect, useMemo } from "react";
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
} from "./session-lifetime-usage-model";
import { copy, useLocale } from "./localization";
import { useSessionActive } from "./session-activity";
import { Timestamp } from "./timestamp-display";

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
      { sessionLifetimeCreatedAt: createdAt?.toString() ?? "unavailable" },
    ],
    [base, createdAt],
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
          <span>{format(result.data?.input)}</span>
        </span>
        <span>
          <span className="session-info-chip">{copy("session.usageOut")}</span>{" "}
          <span>{format(result.data?.output)}</span>
        </span>
      </div>
      <p>{copy("session.usageIncompleteCoverage")}</p>
      {createdAt === undefined ? (
        <p>{copy("session.usageCreationUnavailable")}</p>
      ) : result.isFetching ? (
        <p role="status">{copy("session.usageReading")}</p>
      ) : result.error ? (
        <p role="status">
          {copy(result.data ? "session.usageStale" : "session.usageReadFailed")}
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
              {result.data?.ranges.map((range) => (
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
