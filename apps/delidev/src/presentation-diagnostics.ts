// SPDX-License-Identifier: Apache-2.0

// The product window renders no status for sidebar-preference, tray or
// desktop-notification failures, so these bounded structured warnings are the
// only renderer-side evidence. Records carry a stable phase and outcome and
// never raw error text, identifiers, paths or payloads.
export enum PresentationPhase {
  SidebarRead = "sidebar-read",
  SidebarSave = "sidebar-save",
  TrayBegin = "tray-begin",
  TrayPublish = "tray-publish",
  TrayActionRead = "tray-action-read",
  TrayActivationListener = "tray-activation-listener",
  NotificationReadiness = "notification-readiness",
  NotificationCandidateRead = "notification-candidate-read",
  NotificationScopeBegin = "notification-scope-begin",
  NotificationCandidateInvalid = "notification-candidate-invalid",
  NotificationPump = "notification-pump",
  NotificationPresent = "notification-present",
  NotificationReport = "notification-report",
}

export enum PresentationOutcome {
  Failed = "failed",
  Recovered = "recovered",
}

export enum PresentationEventKind {
  Failure = "failure",
  Success = "success",
  Disposal = "disposal",
}

export type PresentationEvent =
  | {
      kind: PresentationEventKind.Failure | PresentationEventKind.Success;
      phase: PresentationPhase;
    }
  | { kind: PresentationEventKind.Disposal };

export interface PresentationRecord {
  phase: PresentationPhase;
  outcome: PresentationOutcome;
}

export interface PresentationState {
  // Phases whose latest observed operation failed, in first-failure order.
  readonly failing: readonly PresentationPhase[];
  // A disposed log belongs to a finished lifetime and ignores every event.
  readonly disposed: boolean;
}

export interface PresentationStep {
  state: PresentationState;
  record?: PresentationRecord;
}

export const initialPresentationState: PresentationState = {
  failing: [],
  disposed: false,
};

// The only transition function for every presentation reporter. A phase is one
// operation: its failure records once on the healthy-to-failing transition, its
// next success records one recovery, and an operation that was not attempted
// changes nothing. Phases never affect each other, so two phases that fail in
// one batch each record their own transition.
export function reducePresentation(
  state: PresentationState,
  event: PresentationEvent,
): PresentationStep {
  if (state.disposed) return { state };
  if (event.kind === PresentationEventKind.Disposal)
    return { state: { failing: [], disposed: true } };
  const failing = state.failing.includes(event.phase);
  if (event.kind === PresentationEventKind.Failure) {
    if (failing) return { state };
    return {
      state: { ...state, failing: [...state.failing, event.phase] },
      record: {
        phase: event.phase,
        outcome: PresentationOutcome.Failed,
      },
    };
  }
  if (!failing) return { state };
  return {
    state: {
      ...state,
      failing: state.failing.filter((phase) => phase !== event.phase),
    },
    record: { phase: event.phase, outcome: PresentationOutcome.Recovered },
  };
}

const eventName = "delidev.presentation.outcome";

// Applies the reducer to one lifetime and writes its records: one warning when
// a phase starts failing and one info record when it recovers, so a repeating
// failure never floods the console. One log belongs to one controller lifetime
// (tray lifetime, notification connection or sidebar provider), so a new
// lifetime reports its own first failure.
export class PresentationFailureLog {
  private state = initialPresentationState;
  report(phase: PresentationPhase, failed: boolean) {
    if (failed) this.failed(phase);
    else this.recovered(phase);
  }
  failed(phase: PresentationPhase) {
    this.apply({ kind: PresentationEventKind.Failure, phase });
  }
  recovered(phase: PresentationPhase) {
    this.apply({ kind: PresentationEventKind.Success, phase });
  }
  dispose() {
    this.apply({ kind: PresentationEventKind.Disposal });
  }
  private apply(event: PresentationEvent) {
    const step = reducePresentation(this.state, event);
    this.state = step.state;
    if (!step.record) return;
    if (step.record.outcome === PresentationOutcome.Failed)
      console.warn(eventName, step.record);
    else console.info(eventName, step.record);
  }
}
