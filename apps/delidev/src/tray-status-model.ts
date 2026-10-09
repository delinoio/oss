// SPDX-License-Identifier: Apache-2.0
import { DateFormatPreference } from "./timestamp-format";
import { SupportedLanguage } from "./localization";
import { TrayQuotaState, TraySubscriptionService, type TraySummary } from "./tray-types";

export enum TrayPanelAction { Show = "show", Sessions = "sessions", Inbox = "inbox", Usage = "usage", Settings = "settings", Quit = "quit", Recovery = "recovery" }
export interface TrayPanelTarget { label: string; instance: string; scope: string; revision: number }
export interface TrayPanelWindow { target: TrayPanelTarget; name: string; summary: TraySummary | null; stale: boolean; observed_age_ms: number }
export interface TrayPanelSnapshot { instance: string; windows: TrayPanelWindow[]; more: boolean; recent: string | null; theme: "system" | "light" | "dark"; language: SupportedLanguage; date_format: DateFormatPreference }
const id = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const integer = (value: unknown, maximum = Number.MAX_SAFE_INTEGER): value is number => typeof value === "number" && Number.isSafeInteger(value) && value >= 0 && value <= maximum;
const bounded = (value: unknown, maximum: number): value is string => typeof value === "string" && new TextEncoder().encode(value).length <= maximum;
const timestamp = (value: unknown) => value === null || (bounded(value, 40) && Number.isFinite(Date.parse(value)));
const counter = (value: unknown) => typeof value === "string" && /^(0|[1-9][0-9]{0,79})$/.test(value);
const fields = (value: object, names: string[]) => Object.keys(value).every(key => names.includes(key));
function summary(value: TraySummary | null): boolean {
  if (value === null) return true;
  if (!value || !fields(value, ["overview", "usage", "accounts"]) || !["overview", "usage", "accounts"].every(key => Object.hasOwn(value, key))) return false;
  const { overview, usage, accounts } = value;
  if (overview && (!fields(overview, ["observed_at", "stale", "active_sessions", "pending_interactions", "registered_workers", "connected_workers"]) || !timestamp(overview.observed_at) || typeof overview.stale !== "boolean" || ![overview.active_sessions, overview.pending_interactions, overview.registered_workers, overview.connected_workers].every(counter) || BigInt(overview.connected_workers) > BigInt(overview.registered_workers))) return false;
  if (usage && (!fields(usage, ["known_tokens", "incomplete", "estimates"]) || usage.incomplete !== true || (usage.known_tokens !== null && !counter(usage.known_tokens)) || !Array.isArray(usage.estimates) || usage.estimates.length > 32 || new Set(usage.estimates.map(v => v.currency)).size !== usage.estimates.length || usage.estimates.some(v => !fields(v, ["currency", "known_amount"]) || !/^[A-Z]{3}$/.test(v.currency) || (v.known_amount !== null && !/^(0|[1-9][0-9]{0,79})(\.[0-9]{1,9})?$/.test(v.known_amount))))) return false;
  if (accounts && (!fields(accounts, ["entries", "more"]) || typeof accounts.more !== "boolean" || !Array.isArray(accounts.entries) || accounts.entries.length > 20 || accounts.entries.some(account =>
    !fields(account, ["alias", "alias_hidden", "subscription_service", "windows", "more"]) || !bounded(account.alias, 256) || !account.alias || typeof account.more !== "boolean" || (account.alias_hidden !== undefined && typeof account.alias_hidden !== "boolean") || (account.subscription_service !== undefined && !Object.values(TraySubscriptionService).includes(account.subscription_service)) || !Array.isArray(account.windows) || account.windows.length > 8 || account.windows.some(quota =>
      !fields(quota, ["id", "state", "remaining_basis_points", "observed_at", "reset_at"]) || (quota.id !== undefined && !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/.test(quota.id)) || !Object.values(TrayQuotaState).includes(quota.state) || (quota.remaining_basis_points !== null && !integer(quota.remaining_basis_points, 10000)) || !timestamp(quota.observed_at) || !timestamp(quota.reset_at))))) return false;
  return true;
}
/** A closed snapshot cannot smuggle endpoints, credentials or product commands. */
export function parseTrayPanel(value: unknown, instance: string): TrayPanelSnapshot {
  const data = value as TrayPanelSnapshot;
  try {
    if (!data || !fields(data, ["instance", "windows", "more", "recent", "theme", "language", "date_format"]) || data.instance !== instance || !id.test(data.instance) || !Array.isArray(data.windows) || data.windows.length > 32 || typeof data.more !== "boolean" || (data.recent !== null && !id.test(data.recent)) || !["system", "light", "dark"].includes(data.theme) || !Object.values(SupportedLanguage).includes(data.language) || !Object.values(DateFormatPreference).includes(data.date_format) || data.windows.some(row =>
      !fields(row, ["target", "name", "summary", "stale", "observed_age_ms"]) || !row.target || !fields(row.target, ["label", "instance", "scope", "revision"]) || !bounded(row.target.label, 160) || !id.test(row.target.instance) || (row.target.scope !== "" && !id.test(row.target.scope)) || !integer(row.target.revision, 0xffffffff) || !bounded(row.name, 512) || !row.name || typeof row.stale !== "boolean" || !integer(row.observed_age_ms) || !summary(row.summary)) || new Set(data.windows.map(v => v.target.instance)).size !== data.windows.length) throw new Error();
    return data;
  } catch { throw new Error("Invalid retained tray snapshot"); }
}
