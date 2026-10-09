// SPDX-License-Identifier: Apache-2.0
// Closed presentation types, independent of product clients and observations.
export enum TrayDestination { Sessions = "sessions", Inbox = "inbox", Usage = "usage", Settings = "settings" }
export enum TrayQuotaState { Observed = "observed", Unknown = "unknown", Stale = "stale", Failed = "failed", Unsupported = "unsupported" }
export enum TraySubscriptionService { ChatGPT = "chatgpt", Claude = "claude", Grok = "grok" }
export interface TrayQuota { id?: string; state: TrayQuotaState; remaining_basis_points: number | null; observed_at: string | null; reset_at: string | null }
export interface TraySummary {
  overview: { observed_at: string; stale: boolean; active_sessions: string; pending_interactions: string; registered_workers: string; connected_workers: string } | null;
  usage: { known_tokens: string | null; incomplete: boolean; estimates: { currency: string; known_amount: string | null }[] } | null;
  accounts: { entries: { alias: string; alias_hidden?: boolean; subscription_service?: TraySubscriptionService; windows: TrayQuota[]; more: boolean }[]; more: boolean } | null;
}
