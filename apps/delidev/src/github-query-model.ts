import { object, text } from "./documents";

export enum ItemKind { PullRequest = "pull-request", Issue = "issue" }
export enum QueryOperation { List = "list", Search = "search", Detail = "detail", Diff = "diff", Checks = "checks", Statuses = "statuses", Rules = "rules", CI = "ci", Feedback = "feedback", Reviewers = "reviewers" }
export enum ItemState { Open = "open", Closed = "closed", All = "all" }
export type GitHubQuery = { kind: ItemKind; operation: QueryOperation; state?: ItemState; search?: string; number?: string; page?: number; page_size?: number };
export const positive = (value: unknown): value is string => typeof value === "string" && /^[1-9][0-9]{0,19}$/.test(value) && BigInt(value) <= 18446744073709551615n;
export const uuid = (value: unknown) => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
export const bounded = (value: unknown, limit: number, required = true): value is string => typeof value === "string" && (!required || value.trim().length > 0) && new TextEncoder().encode(value).length <= limit && !value.includes("\0");
export const date = (value: unknown) => bounded(value, 64) && Number.isFinite(Date.parse(value));
export const sha = (value: unknown) => typeof value === "string" && /^[a-f0-9]{40}$/.test(value);
export function actorValid(raw: unknown, identity = false) {
  const actor = object(raw), kind = text(actor.kind), native = text(actor.provider_type), login = kind === "bot" ? text(actor.login).replace(/\[bot\]$/, "") : text(actor.login);
  if (!positive(actor.id) || !bounded(actor.node_id, 256)) return false;
  if (identity) return /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(login);
  if (!bounded(native, 64) || !bounded(actor.login, 100) || /[\r\n]/.test(native + text(actor.login))) return false;
  if (kind === "unknown") return !["User", "Bot", "Organization"].includes(native);
  return ({ user: "User", bot: "Bot", organization: "Organization" } as Record<string, string>)[kind] === native && /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(login);
}
