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
/** Claims never establish read state. Replayed claims cannot cause OS submission. */
export async function presentForeground(
  state: ProtectedState,
  id: string,
  active: () => boolean,
  platform: NotificationPlatform,
): Promise<void> {
  const profile = state.profile(id);
  if (
    !state.state.notifications ||
    state.state.selectedProfile !== id ||
    profile.pending ||
    !active()
  )
    return;
  const client = createClient(InboxService, state.transport(id));
  const candidates = await client.listNotificationCandidates({ limit: 1 });
  const candidate = candidates.candidates[0];
  if (!candidate || !active() || state.state.selectedProfile !== id) return;
  const claim = (await state.perform(
    id,
    Operation.Claim,
    create(ClaimNotificationRequestSchema, {
      inboxId: candidate.inboxId,
      requestId: uuid(),
    }),
    candidate.inboxId,
  )) as ClaimNotificationResponse;
  if (!claim.delivery || !claim.mayPresent) return;
  // Retain an uncertain report before asking the OS. A crash or profile switch
  // leaves this exact report recoverable and cannot authorize another alert.
  const report = create(ReportNotificationRequestSchema, {
    inboxId: candidate.inboxId,
    claimId: claim.delivery.claimId,
    requestId: uuid(),
    state: NotificationState.UNCERTAIN,
  });
  await state.prepare(id, Operation.Report, report, candidate.inboxId);
  let outcome = NotificationState.UNCERTAIN;
  if (active() && state.state.selectedProfile === id)
    try {
      outcome = (await platform.notify(
        id,
        candidate.inboxId,
        state.state.language,
      ))
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
  await state.retry(id);
}
