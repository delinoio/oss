// SPDX-License-Identifier: Apache-2.0

// The product window renders no status for tray or desktop-notification
// failures, so these bounded structured warnings are the only renderer-side
// evidence. Records carry a stable phase and outcome and never raw error text,
// identifiers, paths or payloads.
export enum PresentationPhase {
  TrayBegin = "tray-begin",
  TrayPublish = "tray-publish",
  TrayActionRead = "tray-action-read",
  TrayActivationListener = "tray-activation-listener",
  NotificationReadiness = "notification-readiness",
  NotificationCandidateRead = "notification-candidate-read",
  NotificationScopeBegin = "notification-scope-begin",
  NotificationCandidateInvalid = "notification-candidate-invalid",
  NotificationPresent = "notification-present",
  NotificationPump = "notification-pump",
}

export enum PresentationOutcome {
  Failed = "failed",
  Recovered = "recovered",
}

const event = "delidev.presentation.outcome";

// Records one warning when a phase starts failing and one info record when it
// recovers, so a repeating failure never floods the console. One log belongs to
// one controller lifetime, so a new connection reports its own first failure.
export class PresentationFailureLog {
  private readonly failing = new Set<PresentationPhase>();
  report(phase: PresentationPhase, failed: boolean) {
    if (failed) this.failed(phase);
    else this.recovered(phase);
  }
  failed(phase: PresentationPhase) {
    if (this.failing.has(phase)) return;
    this.failing.add(phase);
    console.warn(event, { phase, outcome: PresentationOutcome.Failed });
  }
  recovered(phase: PresentationPhase) {
    if (!this.failing.delete(phase)) return;
    console.info(event, { phase, outcome: PresentationOutcome.Recovered });
  }
}
