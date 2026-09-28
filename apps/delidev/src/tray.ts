import { EntityKind, UsageCoverage, type GetOverviewResponse, type GetUsageSummaryResponse, type ListResourcesResponse } from "@delinoio/delidev-api-client";
import { document, items, object, text } from "./documents";

export enum TrayDestination { Sessions = "sessions", Inbox = "inbox", Usage = "usage", Settings = "settings" }
export enum TrayQuotaState { Observed = "observed", Unknown = "unknown", Stale = "stale", Failed = "failed", Unsupported = "unsupported" }
interface TrayQuota { state: TrayQuotaState; remaining_basis_points: number | null; observed_at: string | null; reset_at: string | null }
export interface TraySummary {
  overview: { observed_at: string; stale: boolean; active_sessions: string; pending_interactions: string; registered_workers: string; connected_workers: string } | null;
  usage: { known_tokens: string | null; incomplete: boolean } | null;
  accounts: { entries: { alias: string; windows: TrayQuota[]; more: boolean }[]; more: boolean } | null;
}
export const unavailableTray = (): TraySummary => ({ overview: null, usage: null, accounts: null });
const decimal = /^(0|[1-9][0-9]{0,79})$/;
function timestamp(value: unknown): string | null {
  if (typeof value !== "string" || value.length > 40 || !Number.isFinite(Date.parse(value))) return null;
  return new Date(value).toISOString();
}
export function traySummary(overview: GetOverviewResponse | undefined, overviewFailed: boolean, usage: GetUsageSummaryResponse | undefined, accounts: ListResourcesResponse | undefined): TraySummary {
  const summary = unavailableTray();
  const observed = timestamp(overview?.observedAt);
  if (overview && observed && overview.connectedWorkers <= overview.registeredWorkers) {
    summary.overview = { observed_at: observed, stale: overviewFailed, active_sessions: overview.activeSessions.toString(), pending_interactions: overview.pendingInteractions.toString(), registered_workers: overview.registeredWorkers.toString(), connected_workers: overview.connectedWorkers.toString() };
  }
  if (overview && usage && usage.fromUnixMs === overview.todayFromUnixMs && usage.untilUnixMs === overview.todayUntilUnixMs && usage.coverage === UsageCoverage.OBSERVED_ROOT_RESPONSES) {
    const total = usage.totals?.total;
    // Exact observed root responses remain incomplete telemetry even when all
    // recorded counters are known. This is neither billing nor pooled quota.
    summary.usage = { known_tokens: total && total.measuredResponses > 0 && decimal.test(total.knownTotal) ? total.knownTotal : null, incomplete: true };
  }
  if (accounts && accounts.resources.length <= 20 && observed) {
    const now = Date.parse(observed);
    const entries: NonNullable<TraySummary["accounts"]>["entries"] = [];
    for (const resource of accounts.resources) {
      const value = document(resource);
      const alias = text(value.alias);
      if (resource.kind !== EntityKind.ACCOUNT || !alias || new TextEncoder().encode(alias).length > 256 || !Array.isArray(value.quota)) return summary;
      const quotas = items(value.quota);
      const windows = quotas.slice(0, 8).map((raw): TrayQuota => {
        const quota = object(raw);
        let state = Object.values(TrayQuotaState).includes(quota.state as TrayQuotaState) ? quota.state as TrayQuotaState : TrayQuotaState.Unknown;
        const observed_at = timestamp(quota.observed_at), reset_at = timestamp(quota.reset_at);
        const remaining = typeof quota.remaining === "number" && Number.isFinite(quota.remaining) && quota.remaining >= 0 && quota.remaining <= 1 ? Math.round(quota.remaining * 10000) : null;
        if (state === TrayQuotaState.Observed) {
          if (!observed_at || remaining === null) state = TrayQuotaState.Unknown;
          else if (overviewFailed || now < Date.parse(observed_at) || now - Date.parse(observed_at) > 300000 || (reset_at !== null && Date.parse(reset_at) <= now)) state = TrayQuotaState.Stale;
        }
        return { state, remaining_basis_points: remaining, observed_at, reset_at };
      });
      // Do not send an email-shaped user alias to native presentation at all.
      entries.push({ alias: alias.includes("@") ? "Account alias hidden" : alias, windows, more: quotas.length > 8 });
    }
    summary.accounts = { entries, more: Boolean(accounts.nextPageToken) };
  }
  return summary;
}

export interface TrayBridge {
  begin(): Promise<string>;
  publish(scope: string, revision: number, summary: TraySummary): Promise<void>;
}
// One in-flight native publication and one latest replacement keep refreshes
// bounded. Scope and monotonic revision prevent late work from another mount
// replacing current window data, including React Strict Mode effect replay.
export class TrayPublisher {
  private pending?: TraySummary;
  private running = false;
  private closed = false;
  private scope?: Promise<string>;
  private revision = 0;
  constructor(private readonly bridge: TrayBridge, private readonly problem: (failed: boolean) => void) {}
  update(summary: TraySummary) { if (!this.closed) { this.pending = summary; void this.drain(); } }
  close() { this.closed = true; this.pending = unavailableTray(); if (this.scope) void this.drain(); }
  private async drain() {
    if (this.running) return;
    this.running = true;
    try {
      this.scope ??= this.bridge.begin();
      const scope = await this.scope;
      while (this.pending) {
        const next = this.pending; this.pending = undefined;
        await this.bridge.publish(scope, ++this.revision, next);
        if (!this.closed) this.problem(false);
      }
    } catch { if (!this.closed) { this.scope = undefined; this.problem(true); } }
    finally { this.running = false; }
  }
}
