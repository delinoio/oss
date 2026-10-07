// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useMemo, useRef } from "react";
import { createConnectQueryKey, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { FailureCode, SystemQuery, isEntityId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { copy, formatTimestamp, useLocale, type MessageKey } from "./localization";
import { Problem } from "./ui";
import "./account-storage.css";

enum AccountStorageState {
  Observed = "observed", Unavailable = "unavailable", Unconfigured = "unconfigured",
  NotApplicable = "not-applicable", Failed = "failed", Superseded = "superseded",
}
enum ReportState { Ready, Missing, Legacy, Unsupported, Invalid }
interface StorageRecord { accountId: string; connectionId: string; state: AccountStorageState; code?: FailureCode; guidance?: string }
interface StorageReport { state: ReportState; serverId?: string; observedAt?: string; records?: Map<string, StorageRecord>; more?: boolean }
const labels: Record<AccountStorageState, MessageKey> = {
  [AccountStorageState.Observed]: "account-storage.readable",
  [AccountStorageState.Unavailable]: "account-storage.unavailable",
  [AccountStorageState.Unconfigured]: "account-storage.unconfigured",
  [AccountStorageState.NotApplicable]: "account-storage.keyless",
  [AccountStorageState.Failed]: "account-storage.failed",
  [AccountStorageState.Superseded]: "account-storage.superseded",
};
const unavailableCodes = new Set([FailureCode.Unavailable, FailureCode.ServerUnavailable, FailureCode.Unsupported, FailureCode.Canceled]);
function bounded(value: unknown, maximum: number): value is string {
  return typeof value === "string" && value.length <= maximum && !value.includes("\0");
}
function validTimestamp(value: unknown): value is string {
  if (!bounded(value, 64) || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?Z$/.test(value)) return false;
  const instant = new Date(value);
  return Number.isFinite(instant.getTime()) && instant.toISOString().slice(0, 19) === value.slice(0, 19);
}
/** Validate only the account observation projection; unrelated diagnostics do not grant account health. */
function accountStorageReport(bytes?: Uint8Array): StorageReport {
  if (!bytes) return { state: ReportState.Missing };
  const invalid = { state: ReportState.Invalid };
  if (bytes.byteLength > 1 << 20) return invalid;
  try {
    const report = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes)));
    if (!bounded(report.version, 256) || !report.version) return invalid;
    if (report.schema_version === undefined) return { state: ReportState.Legacy };
    if (report.schema_version !== 2) return { state: ReportState.Unsupported };
    if (!isEntityId(text(report.server_id)) || !validTimestamp(report.observed_at) || !Array.isArray(report.credentials) || report.credentials.length > 50 || (report.more_credentials !== undefined && typeof report.more_credentials !== "boolean")) return invalid;
    const records = new Map<string, StorageRecord>();
    for (const value of report.credentials) {
      const entry = object(value), result = object(entry.result);
      const accountId = text(entry.account_id), connectionId = text(entry.connection_id), state = result.state as AccountStorageState;
      if (!isEntityId(accountId) || records.has(accountId) || (entry.connection_id !== undefined && !isEntityId(connectionId)) || !Object.values(AccountStorageState).includes(state) || (result.guidance !== undefined && !bounded(result.guidance, 4096))) return invalid;
      const code = result.code as FailureCode | undefined;
      const ordinary = [AccountStorageState.Observed, AccountStorageState.Unconfigured, AccountStorageState.NotApplicable].includes(state);
      if (ordinary ? code !== undefined : !Object.values(FailureCode).includes(code!)) return invalid;
      if (!ordinary && state !== AccountStorageState.Superseded && (state === AccountStorageState.Unavailable) !== unavailableCodes.has(code!)) return invalid;
      if (state === AccountStorageState.Superseded && code !== FailureCode.Conflict) return invalid;
      if (state === AccountStorageState.Unconfigured && connectionId) return invalid;
      if ([AccountStorageState.Observed, AccountStorageState.NotApplicable, AccountStorageState.Failed].includes(state) && !connectionId) return invalid;
      records.set(accountId, { accountId, connectionId, state, code, guidance: result.guidance as string | undefined });
    }
    return { state: ReportState.Ready, serverId: text(report.server_id), observedAt: report.observed_at, records, more: report.more_credentials as boolean | undefined };
  } catch { return invalid; }
}

export function useAccountStorage(accounts: readonly Resource[], active: boolean, listStale = false) {
  const upstream = useTransport(), client = useQueryClient();
  const signature = JSON.stringify(accounts.map(row => [row.id, row.revision.toString(), text(object(document(row).connection).id)]).sort((a, b) => a[0].localeCompare(b[0])));
  // A new account/page generation needs a fresh observation. The wrapper has
  // its own Connect Query identity without changing any RPC input or wire shape.
  const transport = useMemo(() => ({ unary: upstream.unary.bind(upstream), stream: upstream.stream.bind(upstream) }), [upstream, signature]);
  const queryKey = useMemo(() => createConnectQueryKey({ schema: SystemQuery.getDoctor, input: {}, transport, cardinality: "finite" }), [transport]);
  const result = useQuery(SystemQuery.getDoctor, {}, { transport, enabled: active && accounts.length > 0, gcTime: 0 });
  useEffect(() => () => {
    // These generation-specific queries also dispose outside Settings fixtures.
    // Cancel only this reader; never remove sibling or native-operation state.
    void client.cancelQueries({ queryKey, exact: true });
    client.removeQueries({ queryKey, exact: true });
  }, [client, queryKey]);
  const report = useMemo(() => accountStorageReport(result.data?.reportJson), [result.data]);
  const stale = Boolean(result.error || listStale || result.isFetching && result.data);
  return {
    header: <AccountStorageHeader active={active && accounts.length > 0} result={result} report={report} listStale={listStale} />,
    forAccount: (row: Resource) => <AccountStorageNotice key={JSON.stringify([report.serverId, row.id, text(object(document(row).connection).id), row.revision.toString()])} row={row} report={report} stale={stale} loading={result.isFetching && !result.data} />,
  };
}
function AccountStorageHeader({ active, result, report, listStale }: {
  active: boolean; result: ReturnType<typeof useQuery<typeof SystemQuery.getDoctor.input, typeof SystemQuery.getDoctor.output>>;
  report: StorageReport; listStale: boolean;
}) {
  useLocale();
  return <section className="account-storage-header" aria-label={copy("account-storage.heading")}>
    <div className="account-storage-toolbar"><span>{copy("account-storage.heading")}</span><button type="button" disabled={!active || result.isFetching || listStale} onClick={() => void result.refetch()}>{copy("account-storage.refresh")}</button></div>
    <p>{copy("account-storage.scope")}</p>
    <Problem error={result.error} />
    {result.isFetching ? <p role="status">{copy("account-storage.loading")}</p> : null}
    {result.data && (result.error || result.isFetching || listStale) ? <p role="status">{copy("account-storage.previous")}</p> : null}
    {report.state === ReportState.Legacy || report.state === ReportState.Unsupported || report.state === ReportState.Invalid ? <p role="status">{copy(report.state === ReportState.Legacy ? "account-storage.legacy" : report.state === ReportState.Unsupported ? "account-storage.unsupported" : "account-storage.invalid")}</p> : null}
    {report.more === true ? <p>{copy("account-storage.partial")}</p> : report.state === ReportState.Ready && report.more === undefined ? <p>{copy("account-storage.completeness")}</p> : null}
  </section>;
}
function AccountStorageNotice({ row, report, stale, loading }: { row: Resource; report: StorageReport; stale: boolean; loading: boolean }) {
  useLocale();
  const disclosure = useRef<HTMLDetailsElement | null>(null), disclosureOpen = useRef(false);
  // The keyed account owner survives a hidden successful observation. Retain
  // the native open value even when its toggle event has not fired yet.
  const retainDisclosure = useCallback((node: HTMLDetailsElement | null) => {
    if (disclosure.current) disclosureOpen.current = disclosure.current.open;
    disclosure.current = node;
    if (node) node.open = disclosureOpen.current;
  }, []);
  const value = document(row), connectionId = text(object(value.connection).id), record = report.records?.get(row.id);
  if (!record) return <div className="account-storage-notice"><p>{copy(loading ? "account-storage.loading" : "account-storage.missing")}</p></div>;
  if (record.connectionId !== connectionId) return <div className="account-storage-notice"><p>{copy("account-storage.superseded")}</p></div>;
  const chatGPT = value.type === "subscription" && value.subscription_service === "chatgpt";
  const deferredSubscription = chatGPT && record.state === AccountStorageState.Unavailable && record.code !== FailureCode.Unsupported;
  if (record.state === AccountStorageState.Observed) return null;
  const failed = record.state === AccountStorageState.Failed;
  const subscription = value.type === "subscription" && record.state === AccountStorageState.Unavailable && record.code === FailureCode.Unsupported;
  return <div className="account-storage-notice" data-failed={failed || undefined}>
    <div className="account-storage-summary" role={failed ? "alert" : "status"}>
      {failed ? <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3 2 21h20L12 3Z" /><path d="M12 9v5m0 3h.01" /></svg> : null}
      <div><p><strong>{copy(subscription ? "account-storage.subscription" : deferredSubscription ? "account-storage.chatGPTUnavailable" : labels[record.state])}</strong></p>
        {failed ? <p>{copy(chatGPT ? "account-storage.chatGPTRecovery" : "account-storage.recovery")}</p> : null}
        {stale ? <p>{copy("account-storage.previous")}</p> : null}
      </div>
    </div>
    <details ref={retainDisclosure}><summary>{copy("ui.technicalDetails")}</summary>
      <dl><dt>{copy("account-storage.classification")}</dt><dd>{record.state}</dd><dt>{copy("account-storage.observed")}</dt><dd>{formatTimestamp(report.observedAt!)}</dd>{record.code ? <><dt>{copy("account-storage.code")}</dt><dd>{record.code}</dd></> : null}{record.connectionId ? <><dt>{copy("account-storage.connection")}</dt><dd>{record.connectionId}</dd></> : null}</dl>
      {record.guidance ? <p>{record.guidance}</p> : null}
    </details>
  </div>;
}
