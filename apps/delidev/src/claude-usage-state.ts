// SPDX-License-Identifier: Apache-2.0
// Pure projection of the server-owned Claude usage section. The renderer never
// writes this state: it only reads the closed fields below and treats any
// malformed section as unreadable instead of guessing a connection.
import {
  ClaudeUsageLoginState,
  SystemCapability,
  isEntityId,
} from "@delinoio/delidev-api-client";
import { copy, type MessageKey } from "./localization";
import { object, text, items } from "./documents";
import {
  QuotaObservationState,
  type SubscriptionQuotaWindow,
} from "./subscription-settings";

/** Closed server lifecycle of the optional usage connection. */
export enum ClaudeUsageConnection {
  Connecting = "connecting",
  Confirming = "confirming",
  Linked = "linked",
  ReconnectRequired = "reconnect-required",
}
export enum ClaudeUsageRefreshPhase {
  Idle = "",
  Reading = "reading",
  Renewing = "renewing",
  RotationUncertain = "rotation-uncertain",
}
/** Closed reason of the latest failed read. It never carries provider text. */
export enum ClaudeUsageFailure {
  None = "",
  RateLimited = "rate-limited",
  Forbidden = "forbidden",
  Unauthorized = "unauthorized",
  Unavailable = "unavailable",
  Malformed = "malformed",
  Empty = "empty",
  IdentityChanged = "identity-changed",
  Unsupported = "unsupported",
}
/** Login operation states shared with the setup-token lifecycle. */
export enum ClaudeUsageOperationState {
  Preparing = "preparing",
  Waiting = "waiting",
  Succeeded = "succeeded",
  Canceled = "canceled",
  Expired = "expired",
  Unsupported = "unsupported",
  RecoveryRequired = "recovery-required",
  Failed = "failed",
}
export enum ClaudeUsageWindowId {
  FiveHour = "five_hour",
  SevenDay = "seven_day",
}
/** Closed outcome of the System capability negotiation for this presentation. */
export enum ClaudeUsageCapability {
  Loading = "loading",
  Unavailable = "unavailable",
  UpdateRequired = "update-required",
  Available = "available",
}
/** Distinct states of the Usage tracking section. */
export enum ClaudeUsageView {
  Loading = "loading",
  Unavailable = "unavailable",
  Unreadable = "unreadable",
  UpdateRequired = "update-required",
  NotConnected = "not-connected",
  Preparing = "preparing",
  Waiting = "waiting",
  Confirmation = "confirmation",
  LoginFailed = "login-failed",
  Recovery = "recovery",
  Linked = "linked",
  Stale = "stale",
  Backoff = "backoff",
  RefreshFailed = "refresh-failed",
  ReconnectRequired = "reconnect-required",
}

/** The server marks usage observations older than this as stale. */
export const claudeUsageFreshnessMilliseconds = 5 * 60 * 1000;

export interface ClaudeUsageIdentity {
  email: string;
  organization: string;
  plan: string;
  commitment: string;
}
export interface ClaudeUsageOperation {
  id: string;
  state: ClaudeUsageOperationState;
  codeSubmitted: boolean;
}
export interface ClaudeUsageWindow {
  id: ClaudeUsageWindowId;
  remaining: number;
  observedAt: string;
  resetAt?: string;
}
export interface ClaudeUsageProjection {
  connection: ClaudeUsageConnection;
  generation: string;
  operation?: ClaudeUsageOperation;
  identity?: ClaudeUsageIdentity;
  published: boolean;
  refresh: ClaudeUsageRefreshPhase;
  failure: ClaudeUsageFailure;
  lastSuccessAt?: string;
  backoffUntil?: string;
  windows: readonly ClaudeUsageWindow[];
}
export type ClaudeUsageSection =
  | { kind: "absent" }
  | { kind: "unreadable" }
  | { kind: "present"; usage: ClaudeUsageProjection };

const values = <T extends string>(enumeration: Record<string, T>) =>
  Object.values(enumeration);
const member = <T extends string>(
  enumeration: Record<string, T>,
  value: unknown,
): T | undefined =>
  values(enumeration).find((candidate) => candidate === value);
const instant = (value: unknown): string | undefined => {
  const raw = text(value);
  return raw && Number.isFinite(Date.parse(raw)) ? raw : undefined;
};

function identityOf(value: unknown): ClaudeUsageIdentity | undefined {
  const raw = object(value),
    email = text(raw.email);
  return email
    ? {
        email,
        organization: text(raw.organization),
        plan: text(raw.plan),
        commitment: text(raw.commitment),
      }
    : undefined;
}

/**
 * Reads `subscription.claude_usage` of a server-token Claude account. Only the
 * closed presentation fields are copied; protected references (credential,
 * actor, epoch, retired references) are never read.
 */
export function claudeUsageSection(
  subscription: Record<string, unknown>,
): ClaudeUsageSection {
  if (
    subscription.claude_usage === undefined ||
    subscription.claude_usage === null
  )
    return { kind: "absent" };
  const raw = object(subscription.claude_usage);
  const connection = member(ClaudeUsageConnection, raw.connection),
    generation = text(raw.generation),
    refresh = member(ClaudeUsageRefreshPhase, text(raw.refresh)),
    failure = member(ClaudeUsageFailure, text(raw.failure));
  if (
    !connection ||
    !isEntityId(generation) ||
    refresh === undefined ||
    failure === undefined
  )
    return { kind: "unreadable" };
  let operation: ClaudeUsageOperation | undefined;
  if (raw.operation !== undefined && raw.operation !== null) {
    const op = object(raw.operation),
      state = member(ClaudeUsageOperationState, op.state);
    if (!isEntityId(text(op.id)) || !state) return { kind: "unreadable" };
    operation = {
      id: text(op.id),
      state,
      codeSubmitted: text(op.code_submission_id) !== "",
    };
  }
  // The connecting and confirming lifecycles are always owned by one login
  // operation; a section that omits it cannot be acted on and is unreadable.
  if (
    (connection === ClaudeUsageConnection.Connecting ||
      connection === ClaudeUsageConnection.Confirming) &&
    !operation
  )
    return { kind: "unreadable" };
  // A linked connection always carries its reviewed identity, the publication
  // marker and the link time. A section that omits any of them is malformed: it
  // would present as connected with no identity block or Disconnect action, so
  // it is unreadable instead.
  const identity = identityOf(raw.identity);
  if (
    connection === ClaudeUsageConnection.Linked &&
    (!identity || raw.published !== true || !instant(raw.linked_at))
  )
    return { kind: "unreadable" };
  const lastSuccessAt = raw.last_success_at
      ? instant(raw.last_success_at)
      : undefined,
    backoffUntil = raw.backoff_until ? instant(raw.backoff_until) : undefined;
  if (
    (raw.last_success_at && !lastSuccessAt) ||
    (raw.backoff_until && !backoffUntil)
  )
    return { kind: "unreadable" };
  const windows: ClaudeUsageWindow[] = [];
  for (const entry of items(raw.windows)) {
    const window = object(entry),
      id = member(ClaudeUsageWindowId, window.id),
      observedAt = instant(window.observed_at);
    if (
      !id ||
      !observedAt ||
      typeof window.remaining !== "number" ||
      !(window.remaining >= 0 && window.remaining <= 1) ||
      (window.reset_at && !instant(window.reset_at)) ||
      windows.some((existing) => existing.id === id)
    )
      return { kind: "unreadable" };
    windows.push({
      id,
      remaining: window.remaining,
      observedAt,
      ...(window.reset_at ? { resetAt: text(window.reset_at) } : {}),
    });
  }
  windows.sort(
    (a, b) =>
      values(ClaudeUsageWindowId).indexOf(a.id) -
      values(ClaudeUsageWindowId).indexOf(b.id),
  );
  return {
    kind: "present",
    usage: {
      connection,
      generation,
      operation,
      identity,
      published: raw.published === true,
      refresh,
      failure,
      lastSuccessAt,
      backoffUntil,
      windows,
    },
  };
}

/** Maps a negotiated status to the presentation capability. */
export function claudeUsageCapability(status: {
  data?: { capabilities: readonly SystemCapability[] };
  error?: unknown;
}): ClaudeUsageCapability {
  if (status.error) return ClaudeUsageCapability.Unavailable;
  if (!status.data) return ClaudeUsageCapability.Loading;
  return status.data.capabilities.includes(
    SystemCapability.CLAUDE_SERVER_USAGE_V1,
  )
    ? ClaudeUsageCapability.Available
    : ClaudeUsageCapability.UpdateRequired;
}

const loginStateOf = (
  state: ClaudeUsageLoginState | undefined,
): ClaudeUsageOperationState | "linked" | undefined => {
  switch (state) {
    case ClaudeUsageLoginState.PREPARING:
      return ClaudeUsageOperationState.Preparing;
    case ClaudeUsageLoginState.WAITING:
      return ClaudeUsageOperationState.Waiting;
    case ClaudeUsageLoginState.AWAITING_CONFIRMATION:
      return ClaudeUsageOperationState.Succeeded;
    case ClaudeUsageLoginState.LINKED:
      return "linked";
    case ClaudeUsageLoginState.CANCELED:
      return ClaudeUsageOperationState.Canceled;
    case ClaudeUsageLoginState.EXPIRED:
      return ClaudeUsageOperationState.Expired;
    case ClaudeUsageLoginState.UNSUPPORTED:
      return ClaudeUsageOperationState.Unsupported;
    case ClaudeUsageLoginState.RECOVERY_REQUIRED:
      return ClaudeUsageOperationState.RecoveryRequired;
    case ClaudeUsageLoginState.FAILED:
      return ClaudeUsageOperationState.Failed;
    default:
      return undefined;
  }
};

/** The login operation state a progress result corresponds to. */
export const claudeUsageLoginOperationState = loginStateOf;

/** Resolves the one distinct state the section presents. */
export function claudeUsageView(input: {
  capability: ClaudeUsageCapability;
  section: ClaudeUsageSection;
  progress?: ClaudeUsageLoginState;
  now: number;
}): ClaudeUsageView {
  switch (input.capability) {
    case ClaudeUsageCapability.UpdateRequired:
      return ClaudeUsageView.UpdateRequired;
    case ClaudeUsageCapability.Loading:
      return ClaudeUsageView.Loading;
    case ClaudeUsageCapability.Unavailable:
      return ClaudeUsageView.Unavailable;
  }
  if (input.section.kind === "unreadable") return ClaudeUsageView.Unreadable;
  if (input.section.kind === "absent") return ClaudeUsageView.NotConnected;
  const usage = input.section.usage;
  const reported = loginStateOf(input.progress);
  switch (usage.connection) {
    case ClaudeUsageConnection.ReconnectRequired:
      return ClaudeUsageView.ReconnectRequired;
    case ClaudeUsageConnection.Connecting:
    case ClaudeUsageConnection.Confirming: {
      // The server document is authoritative for the lifecycle; the progress
      // read refines the operation phase only within that lifecycle.
      const state =
        reported && reported !== "linked"
          ? reported
          : (usage.operation?.state ?? ClaudeUsageOperationState.Preparing);
      if (
        usage.connection === ClaudeUsageConnection.Confirming &&
        state === ClaudeUsageOperationState.Succeeded
      )
        return ClaudeUsageView.Confirmation;
      switch (state) {
        case ClaudeUsageOperationState.Preparing:
          return ClaudeUsageView.Preparing;
        case ClaudeUsageOperationState.Waiting:
          return ClaudeUsageView.Waiting;
        case ClaudeUsageOperationState.RecoveryRequired:
          return ClaudeUsageView.Recovery;
        case ClaudeUsageOperationState.Succeeded:
          return usage.connection === ClaudeUsageConnection.Confirming
            ? ClaudeUsageView.Confirmation
            : ClaudeUsageView.Preparing;
        default:
          return ClaudeUsageView.LoginFailed;
      }
    }
    case ClaudeUsageConnection.Linked: {
      const backoff = usage.backoffUntil
        ? Date.parse(usage.backoffUntil)
        : undefined;
      if (backoff !== undefined && backoff > input.now)
        return ClaudeUsageView.Backoff;
      if (usage.failure !== ClaudeUsageFailure.None)
        return ClaudeUsageView.RefreshFailed;
      if (
        usage.lastSuccessAt &&
        input.now - Date.parse(usage.lastSuccessAt) >
          claudeUsageFreshnessMilliseconds
      )
        return ClaudeUsageView.Stale;
      return ClaudeUsageView.Linked;
    }
  }
}

const windowLabels: Record<ClaudeUsageWindowId, MessageKey> = {
  [ClaudeUsageWindowId.FiveHour]: "claude-usage.window.fiveHour",
  [ClaudeUsageWindowId.SevenDay]: "claude-usage.window.sevenDay",
};
/** Friendly label of a recognized window; null for any other quota window. */
export function claudeUsageWindowLabel(id: string): string | undefined {
  const window = member(ClaudeUsageWindowId, id);
  return window ? copy(windowLabels[window]) : undefined;
}

/** Retained evidence as quota rows. A retired connection shows it as stale. */
export function claudeUsageQuotaWindows(
  usage: ClaudeUsageProjection,
): SubscriptionQuotaWindow[] {
  const retired = usage.connection !== ClaudeUsageConnection.Linked;
  return usage.windows.map((window) => ({
    id: window.id,
    label: claudeUsageWindowLabel(window.id),
    remaining: window.remaining,
    observedAt: window.observedAt,
    resetAt: window.resetAt,
    state: retired
      ? QuotaObservationState.Stale
      : QuotaObservationState.Observed,
  }));
}

export const claudeUsageFailureMessages: Record<
  Exclude<ClaudeUsageFailure, ClaudeUsageFailure.None>,
  MessageKey
> = {
  [ClaudeUsageFailure.RateLimited]: "claude-usage.failure.rateLimited",
  [ClaudeUsageFailure.Forbidden]: "claude-usage.failure.forbidden",
  [ClaudeUsageFailure.Unauthorized]: "claude-usage.failure.unauthorized",
  [ClaudeUsageFailure.Unavailable]: "claude-usage.failure.unavailable",
  [ClaudeUsageFailure.Malformed]: "claude-usage.failure.malformed",
  [ClaudeUsageFailure.Empty]: "claude-usage.failure.empty",
  [ClaudeUsageFailure.IdentityChanged]: "claude-usage.failure.identityChanged",
  [ClaudeUsageFailure.Unsupported]: "claude-usage.failure.unsupported",
};

export const claudeUsageLoginMessages: Partial<
  Record<ClaudeUsageOperationState, MessageKey>
> = {
  [ClaudeUsageOperationState.Canceled]: "claude-usage.login.canceled",
  [ClaudeUsageOperationState.Expired]: "claude-usage.login.expired",
  [ClaudeUsageOperationState.Unsupported]: "claude-usage.login.unsupported",
  [ClaudeUsageOperationState.RecoveryRequired]: "claude-usage.login.recovery",
  [ClaudeUsageOperationState.Failed]: "claude-usage.login.failed",
};

/**
 * Account rows identify a manually linked usage account only when observations
 * are present. The email is the reviewed OAuth identity, not proof of identity
 * equality with the execution setup token.
 */
export function claudeUsageRowIdentity(
  subscription: Record<string, unknown>,
): string | undefined {
  const section = claudeUsageSection(subscription);
  return section.kind === "present" &&
    section.usage.connection === ClaudeUsageConnection.Linked &&
    section.usage.published &&
    section.usage.identity &&
    section.usage.windows.length > 0
    ? section.usage.identity.email
    : undefined;
}
