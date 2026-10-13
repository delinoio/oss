// SPDX-License-Identifier: Apache-2.0
import { create, toJsonString } from "@bufbuild/protobuf";
import { createClient } from "@connectrpc/connect";
import {
  ClaimNotificationRequestSchema,
  ReportNotificationRequestSchema,
  InboxService,
  NotificationState,
  type ClaimNotificationResponse,
} from "@delinoio/delidev-api-client";
import { Operation, ProtectedState, uuid } from "./state";
export interface NotificationPlatform {
  notify(profile: string, inbox: string, language: string): Promise<boolean>;
}
/**
 * Identity of one connection generation. The owner object stays stable while
 * `epoch` changes on every suspend and resume, so a stale drain cannot keep
 * running in a newer generation.
 */
export interface DrainOwner {
  readonly epoch: number;
}
/** Closed reasons a foreground drain stops. None of them schedule a retry. */
export enum DrainStop {
  Drained = "drained",
  Lifecycle = "lifecycle",
  Pending = "pending",
  NoProgress = "no-progress",
  TransportFailure = "transport-failure",
}
/** Server maximum for ListNotificationCandidates. */
export const NOTIFICATION_BATCH_LIMIT = 50;
interface Drain {
  epoch: number;
  rerun: boolean;
  promise: Promise<void>;
}
const drains = new WeakMap<DrainOwner, Drain>();
const yieldMacrotask = () =>
  new Promise<void>((resolve) => setTimeout(resolve, 0));
/**
 * Drains pending notification candidates for the selected profile. At most one
 * drain runs per connection generation; later triggers join it and request one
 * more pass after it drains. Claims never establish read state, and replayed
 * claims cannot cause OS submission.
 */
export function presentForeground(
  state: ProtectedState,
  id: string,
  active: () => boolean,
  platform: NotificationPlatform,
  owner: DrainOwner,
): Promise<void> {
  const epoch = owner.epoch;
  const existing = drains.get(owner);
  if (existing && existing.epoch === epoch) {
    existing.rerun = true;
    return existing.promise;
  }
  const drain: Drain = { epoch, rerun: false, promise: Promise.resolve() };
  drain.promise = drainNotifications(
    state,
    id,
    active,
    platform,
    owner,
    drain,
  ).finally(() => {
    if (drains.get(owner) === drain) drains.delete(owner);
  });
  drains.set(owner, drain);
  return drain.promise;
}
async function drainNotifications(
  state: ProtectedState,
  id: string,
  active: () => boolean,
  platform: NotificationPlatform,
  owner: DrainOwner,
  drain: Drain,
): Promise<void> {
  const epoch = drain.epoch;
  const owns = () => {
    try {
      return (
        owner.epoch === epoch &&
        active() &&
        state.state.notifications &&
        state.state.selectedProfile === id
      );
    } catch {
      return false;
    }
  };
  const ready = () => {
    if (!owns()) return DrainStop.Lifecycle;
    try {
      return state.profile(id).pending ? DrainStop.Pending : undefined;
    } catch {
      return DrainStop.Lifecycle;
    }
  };
  const client = () => createClient(InboxService, state.transport(id));
  let batches = 0,
    presented = 0,
    stop = DrainStop.Drained;
  try {
    do {
      drain.rerun = false;
      stop = DrainStop.Drained;
      for (;;) {
        const blocked = ready();
        if (blocked) {
          stop = blocked;
          break;
        }
        const response = await client().listNotificationCandidates({
          limit: NOTIFICATION_BATCH_LIMIT,
        });
        batches++;
        let fresh = 0,
          halted: DrainStop | undefined;
        for (const candidate of response.candidates) {
          halted = ready();
          if (halted) break;
          const step = await presentCandidate(
            state,
            id,
            candidate.inboxId,
            platform,
            owns,
          );
          if (step.fresh) {
            fresh++;
            presented++;
          }
          if (step.halt) {
            halted = ready() ?? DrainStop.Lifecycle;
            break;
          }
        }
        if (halted) {
          stop = halted;
          break;
        }
        if (!response.more) break;
        if (fresh === 0) {
          // Replayed claims are never counted as progress; stop instead of looping.
          stop = DrainStop.NoProgress;
          break;
        }
        await yieldMacrotask();
      }
    } while (drain.rerun && stop === DrainStop.Drained);
  } catch (error) {
    stop = DrainStop.TransportFailure;
    throw error;
  } finally {
    console.info("mobile_notification_drain", {
      profile: id,
      stop,
      batches,
      presented,
    });
  }
}
/** Claim, protect the report, submit to the OS at most once, then report. */
async function presentCandidate(
  state: ProtectedState,
  id: string,
  inboxId: string,
  platform: NotificationPlatform,
  owns: () => boolean,
): Promise<{ fresh: boolean; halt: boolean }> {
  const claim = (await state.perform(
    id,
    Operation.Claim,
    create(ClaimNotificationRequestSchema, { inboxId, requestId: uuid() }),
    inboxId,
  )) as ClaimNotificationResponse;
  if (!claim.delivery || !claim.mayPresent) return { fresh: false, halt: false };
  // Retain an uncertain report before asking the OS. A crash or profile switch
  // leaves this exact report recoverable and cannot authorize another alert.
  const report = create(ReportNotificationRequestSchema, {
    inboxId,
    claimId: claim.delivery.claimId,
    requestId: uuid(),
    state: NotificationState.UNCERTAIN,
  });
  await state.prepare(id, Operation.Report, report, inboxId);
  let outcome = NotificationState.UNCERTAIN;
  if (owns())
    try {
      outcome = (await platform.notify(id, inboxId, state.state.language))
        ? NotificationState.SUBMITTED
        : NotificationState.DENIED;
    } catch {
      outcome = NotificationState.FAILED;
    }
  // This is the first report send. The outcome becomes immutable once stored.
  report.state = outcome;
  await state.update((s) => {
    const p = s.profiles.find((p) => p.id === id);
    if (!p?.pending || p.pending.operation !== Operation.Report)
      throw new Error("report-scope-changed");
    p.pending.request = toJsonString(ReportNotificationRequestSchema, report);
  });
  // A lifecycle change before the report send leaves the exact stored report
  // pending for explicit recovery; it is never resent implicitly from here.
  if (!owns()) return { fresh: true, halt: true };
  await state.retry(id);
  return { fresh: true, halt: false };
}
