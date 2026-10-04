import { Code, ConnectError, type Client } from "@connectrpc/connect";
import { InboxService, NotificationKind, NotificationState, isEntityId, newRequestId, type NotificationCandidate } from "@delinoio/delidev-api-client";

export enum NativeNotificationPermission { NotDetermined = "not-determined", Granted = "granted", ServiceAvailable = "service-available", Denied = "denied", Unavailable = "unavailable" }
export enum NativeNotificationProblem { None = "none", BundleRequired = "bundle-required", ActionsUnavailable = "actions-unavailable", OsUnavailable = "os-unavailable", Capacity = "capacity" }
export enum NativeNotificationKind { Request = "request", Succeeded = "succeeded", Failed = "failed", Stopped = "stopped", SubscriptionRecovery = "subscription-recovery" }
export enum NativeNotificationResult { Submitted = "submitted", Denied = "denied", Failed = "failed", Uncertain = "uncertain" }
export interface NotificationReadiness { permission: NativeNotificationPermission; problem: NativeNotificationProblem }
export interface NativeNotice { claim_id: string; inbox_id: string; kind: NativeNotificationKind }
export interface NotificationBridge { begin(): Promise<string>; end(scope: string): Promise<unknown>; present(scope: string, notice: NativeNotice): Promise<NativeNotificationResult> }
export function notificationReady(value?: NotificationReadiness): boolean { return value?.permission === NativeNotificationPermission.Granted || value?.permission === NativeNotificationPermission.ServiceAvailable; }
export function notificationReadiness(value: unknown): NotificationReadiness {
  const raw = value as Partial<NotificationReadiness> | null;
  if (!raw || !Object.values(NativeNotificationPermission).includes(raw.permission!) || !Object.values(NativeNotificationProblem).includes(raw.problem!)) throw new Error("Native notification status is unavailable.");
  return { permission: raw.permission!, problem: raw.problem! };
}
const nativeKinds: Partial<Record<NotificationKind, NativeNotificationKind>> = { [NotificationKind.SUBSCRIPTION_RECOVERY]: NativeNotificationKind.SubscriptionRecovery, [NotificationKind.REQUEST]: NativeNotificationKind.Request, [NotificationKind.SUCCEEDED]: NativeNotificationKind.Succeeded, [NotificationKind.FAILED]: NativeNotificationKind.Failed, [NotificationKind.STOPPED]: NativeNotificationKind.Stopped };
const reportedStates: Record<NativeNotificationResult, NotificationState> = { [NativeNotificationResult.Submitted]: NotificationState.SUBMITTED, [NativeNotificationResult.Denied]: NotificationState.DENIED, [NativeNotificationResult.Failed]: NotificationState.FAILED, [NativeNotificationResult.Uncertain]: NotificationState.UNCERTAIN };
type Service = Pick<Client<typeof InboxService>, "claimNotification" | "reportNotification">;

// One serial batch and one latest replacement bound memory and side effects.
// Reconnection starts from server candidates; no local queue replays a display.
export class NotificationPump {
  private pending?: readonly NotificationCandidate[];
  private readonly abort = new AbortController();
  private running = false;
  private closed = false;
  private scope?: Promise<string>;
  constructor(private readonly service: Service, private readonly bridge: NotificationBridge, private readonly problem: (failed: boolean) => void, private readonly changed: () => void) {}
  update(values: readonly NotificationCandidate[]) {
    if (this.closed) return;
    if (values.length > 50 || values.some((v) => !isEntityId(v.inboxId) || (v.kind===NotificationKind.SUBSCRIPTION_RECOVERY ? !isEntityId(v.accountId) || Boolean(v.sessionId) : !isEntityId(v.sessionId) || Boolean(v.accountId)) || !nativeKinds[v.kind])) { this.problem(true); return; }
    this.pending = values.map((v) => ({ ...v })); void this.drain();
  }
  close() { this.closed = true; this.pending = undefined; this.abort.abort(); void this.scope?.then((scope) => this.bridge.end(scope)).catch(() => {}); }
  private async drain() {
    if (this.running) return;
    this.running = true;
    try {
      this.scope ??= this.bridge.begin().catch((error: unknown) => { this.scope = undefined; throw error; });
      const scope = await this.scope;
      if (!isEntityId(scope)) throw new Error("Native presentation scope is unavailable.");
      while (!this.closed && this.pending) {
        const batch = this.pending; this.pending = undefined;
        let failed = false, accepted = false;
        for (const candidate of batch) {
          if (this.closed) return;
          const requestId = newRequestId();
          let claim;
          try { claim = await this.service.claimNotification({ inboxId: candidate.inboxId, requestId }, { signal: this.abort.signal, timeoutMs: 10000 }); }
          catch (error) { if (error instanceof ConnectError && [Code.Aborted, Code.NotFound].includes(error.code)) { accepted = true; continue; } throw error; }
          accepted = true;
          if (this.closed) return;
          if (!claim.mayPresent) continue;
          const current = claim.delivery?.candidate;
          if (claim.replayed || claim.requestId !== requestId || claim.delivery?.claimId !== requestId || claim.delivery.state !== NotificationState.CLAIMED || current?.inboxId !== candidate.inboxId || current.sessionId !== candidate.sessionId || current.kind !== candidate.kind || current.accountId!==candidate.accountId) throw new Error("Original notification claim could not be verified.");
          let result: NativeNotificationResult;
          try { result = await this.bridge.present(scope, { claim_id: requestId, inbox_id: candidate.inboxId, kind: nativeKinds[candidate.kind]! }); }
          catch { result = NativeNotificationResult.Uncertain; }
          if (this.closed) return;
          if (!Object.values(NativeNotificationResult).includes(result)) result = NativeNotificationResult.Uncertain;
          if (result !== NativeNotificationResult.Submitted) failed = true;
          // Reporting is metadata only. Failure never retries native display;
          // the durable server claim retains uncertainty after this view exits.
          await this.service.reportNotification({ inboxId: candidate.inboxId, claimId: requestId, requestId: newRequestId(), state: reportedStates[result] }, { signal: this.abort.signal, timeoutMs: 10000 });
          if (failed) break;
        }
        if (!this.closed) { this.problem(failed); if (accepted && !failed) this.changed(); }
      }
    } catch { if (!this.closed) { this.pending = undefined; this.problem(true); } }
    finally { this.running = false; }
  }
}
