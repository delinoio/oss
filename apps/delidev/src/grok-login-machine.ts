// SPDX-License-Identifier: Apache-2.0
import type { MessageKey } from "./localization";

// The pure state machine behind the Grok browser sign-in controller. The
// transition table below is the only source of truth for what the screen
// offers, which problem it shows, which retry closure is retained and when the
// native receiver is disposed. The contract table in
// docs/cmds-delidev-grok-subscription-contract.md is generated from it and the
// unit tests exercise every phase and event pair, so a new cell cannot appear
// without a documented, tested outcome. The machine grants no login or
// mutation authority; it only describes presentation and retained state.

// Closed presentation stages. They grant no login or mutation authority.
export enum GrokLoginStage {
  Preparing = "preparing",
  Waiting = "waiting",
  Naming = "naming",
  Canceled = "canceled",
  Expired = "expired",
  Unsupported = "unsupported",
  Recovery = "recovery",
  Failed = "failed",
}

// A phase fixes the stage, the visible actions and the native binding.
export enum GrokLoginPhase {
  // The desktop has no trusted native control, so nothing can start.
  DesktopRequired = "desktop-required",
  // The account and login request are pending or unconfirmed; nothing was
  // admitted by the server yet.
  Starting = "starting",
  // The server admitted the login and reports preparing.
  Preparing = "preparing",
  // The server reports waiting but the original link is not validated yet.
  AwaitingLink = "awaiting-link",
  // The original link is validated and the native open is pending.
  Opening = "opening",
  // The native receiver confirmed that it opened the original link.
  Waiting = "waiting",
  // The native open failed; Open browser again may succeed.
  WaitingNoBrowser = "waiting-no-browser",
  // The receiver refused with Incompatible; only the copyable link remains.
  WaitingLinkOnly = "waiting-link-only",
  // A cancellation was acknowledged or observed while the server was preparing.
  PreparingCanceling = "preparing-canceling",
  // A cancellation was acknowledged or observed while the server was waiting.
  WaitingCanceling = "waiting-canceling",
  Naming = "naming",
  Canceled = "canceled",
  Expired = "expired",
  Unsupported = "unsupported",
  Recovery = "recovery",
  Failed = "failed",
  // The server definitely refused the start request; nothing was admitted.
  StartRefused = "start-refused",
  // The task was left or unmounted; nothing remains on screen.
  Disposed = "disposed",
}

// The mutation whose exact original request the controller retains for an
// explicit Retry original request after an uncertain outcome.
export enum GrokRetry {
  None = "none",
  Start = "start",
  Cancel = "cancel",
  Name = "name",
}

export enum GrokNativeBinding {
  // No native receiver was contacted for this opening.
  None = "none",
  // An open was requested; only a native Dispose retires it.
  Requested = "requested",
  Disposed = "disposed",
}

export enum GrokNameField {
  Hidden = "hidden",
  Editable = "editable",
  Locked = "locked",
}

export enum GrokHelp {
  None = "none",
  Waiting = "waiting",
  WaitingLink = "waitingLink",
  Stage = "stage",
}

export enum GrokLoginEvent {
  // Status read observations of the original operation.
  PollPreparing = "poll-preparing",
  PollPreparingCanceled = "poll-preparing-canceled",
  PollWaiting = "poll-waiting",
  PollWaitingCanceled = "poll-waiting-canceled",
  PollCanceled = "poll-canceled",
  PollExpired = "poll-expired",
  PollUnsupported = "poll-unsupported",
  PollRecovery = "poll-recovery",
  PollFailed = "poll-failed",
  PollSucceeded = "poll-succeeded",
  PollError = "poll-error",
  // The original link passed validation for the first time.
  LinkReady = "link-ready",
  // First native open of the original link.
  OpenSucceeded = "open-succeeded",
  OpenIncompatible = "open-incompatible",
  OpenFailed = "open-failed",
  // Deliberate Open browser again.
  ReopenSucceeded = "reopen-succeeded",
  ReopenIncompatible = "reopen-incompatible",
  ReopenFailed = "reopen-failed",
  // One-shot callback steps; each is attempted once and never retried.
  CallbackTakeFailed = "callback-take-failed",
  CallbackForwardFailed = "callback-forward-failed",
  // A mutation that owns a retry closure (start, cancel, name save, or an
  // explicit Retry original request) or an auxiliary action started.
  ReplayStarted = "replay-started",
  AuxiliaryStarted = "auxiliary-started",
  // Account creation plus RequestSubscription.
  StartAccepted = "start-accepted",
  StartRefused = "start-refused",
  StartUncertain = "start-uncertain",
  // CancelSubscription.
  CancelAccepted = "cancel-accepted",
  CancelRefused = "cancel-refused",
  CancelUncertain = "cancel-uncertain",
  // Account alias save.
  NameSaved = "name-saved",
  NameRefused = "name-refused",
  NameUncertain = "name-uncertain",
  // Leaving the task, closing Settings or unmounting.
  Dispose = "dispose",
}

export interface GrokLoginState {
  phase: GrokLoginPhase;
  problem?: MessageKey;
  retry: GrokRetry;
}

export enum GrokProblemRule {
  // The displayed problem is unchanged.
  Keep = "keep",
  // The displayed problem is replaced or removed, whatever it was.
  Clear = "clear",
  Set = "set",
  // Advisory rules apply only while no retry closure is retained: a problem
  // that explains a retained request is never replaced by a side observation.
  SetAdvisory = "set-advisory",
  ClearAdvisory = "clear-advisory",
  // A readable status recovers the polling notice and nothing else.
  ClearStatusOnly = "clear-status-only",
  // An unreadable status never replaces a more specific problem.
  SetStatusUnlessSpecific = "set-status-unless-specific",
}
export enum GrokRetryRule {
  Keep = "keep",
  Clear = "clear",
  Hold = "hold",
}

export const statusProblem: MessageKey =
  "grok-subscription.problem.statusUnconfirmed";

const P = GrokLoginPhase;
const E = GrokLoginEvent;

const inFlight = [
  P.Preparing,
  P.AwaitingLink,
  P.Opening,
  P.Waiting,
  P.WaitingNoBrowser,
  P.WaitingLinkOnly,
];
const winding = [P.PreparingCanceling, P.WaitingCanceling];
// Opening exists only inside one poll tick, so a status read never meets it.
const observable = [
  P.Preparing,
  P.AwaitingLink,
  P.Waiting,
  P.WaitingNoBrowser,
  P.WaitingLinkOnly,
  ...winding,
];
const settledPhases = [
  P.Naming,
  P.Canceled,
  P.Expired,
  P.Unsupported,
  P.Recovery,
  P.Failed,
  P.StartRefused,
  P.DesktopRequired,
];
const aliveEverywhere = [P.Starting, ...inFlight, ...winding, ...settledPhases];

export interface GrokLoginCell {
  event: GrokLoginEvent;
  from: GrokLoginPhase;
  to: GrokLoginPhase;
  problem: GrokProblemRule;
  problemKey?: MessageKey;
  retry: GrokRetryRule;
  hold?: GrokRetry;
}
interface Entry {
  event: GrokLoginEvent;
  from: GrokLoginPhase[];
  // Omitted: the phase is unchanged.
  to?: GrokLoginPhase | Partial<Record<GrokLoginPhase, GrokLoginPhase>>;
  problem: GrokProblemRule;
  problemKey?: MessageKey;
  retry: GrokRetryRule;
  hold?: GrokRetry;
}

const toCanceling: Partial<Record<GrokLoginPhase, GrokLoginPhase>> = {
  [P.Preparing]: P.PreparingCanceling,
  [P.Opening]: P.PreparingCanceling,
  [P.AwaitingLink]: P.WaitingCanceling,
  [P.Waiting]: P.WaitingCanceling,
  [P.WaitingNoBrowser]: P.WaitingCanceling,
  [P.WaitingLinkOnly]: P.WaitingCanceling,
};
const terminalPoll = (event: GrokLoginEvent, to: GrokLoginPhase): Entry => ({
  event,
  from: observable,
  to,
  problem: GrokProblemRule.Clear,
  retry: GrokRetryRule.Clear,
});

const entries: Entry[] = [
  {
    event: E.PollPreparing,
    from: [P.Preparing, P.PreparingCanceling],
    problem: GrokProblemRule.ClearStatusOnly,
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.PollPreparingCanceled,
    from: [P.Preparing, P.PreparingCanceling],
    to: P.PreparingCanceling,
    problem: GrokProblemRule.Set,
    problemKey: "grok-subscription.problem.canceling",
    retry: GrokRetryRule.Clear,
  },
  {
    event: E.PollWaiting,
    from: observable,
    to: {
      [P.Preparing]: P.AwaitingLink,
      [P.PreparingCanceling]: P.WaitingCanceling,
    },
    problem: GrokProblemRule.ClearStatusOnly,
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.PollWaitingCanceled,
    from: observable,
    to: P.WaitingCanceling,
    problem: GrokProblemRule.Set,
    problemKey: "grok-subscription.problem.canceling",
    retry: GrokRetryRule.Clear,
  },
  terminalPoll(E.PollCanceled, P.Canceled),
  terminalPoll(E.PollExpired, P.Expired),
  terminalPoll(E.PollUnsupported, P.Unsupported),
  terminalPoll(E.PollRecovery, P.Recovery),
  terminalPoll(E.PollFailed, P.Failed),
  terminalPoll(E.PollSucceeded, P.Naming),
  {
    event: E.PollError,
    from: observable,
    problem: GrokProblemRule.SetStatusUnlessSpecific,
    problemKey: statusProblem,
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.LinkReady,
    from: [P.AwaitingLink],
    to: P.Opening,
    problem: GrokProblemRule.Keep,
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.OpenSucceeded,
    from: [P.Opening],
    to: P.Waiting,
    problem: GrokProblemRule.ClearAdvisory,
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.OpenIncompatible,
    from: [P.Opening],
    to: P.WaitingLinkOnly,
    problem: GrokProblemRule.SetAdvisory,
    problemKey: "grok-subscription.problem.remoteUnsupported",
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.OpenFailed,
    from: [P.Opening],
    to: P.WaitingNoBrowser,
    problem: GrokProblemRule.SetAdvisory,
    problemKey: "grok-subscription.problem.browserFailed",
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.ReopenSucceeded,
    from: [P.Waiting, P.WaitingNoBrowser],
    to: P.Waiting,
    problem: GrokProblemRule.ClearAdvisory,
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.ReopenIncompatible,
    from: [P.Waiting, P.WaitingNoBrowser],
    to: P.WaitingLinkOnly,
    problem: GrokProblemRule.SetAdvisory,
    problemKey: "grok-subscription.problem.remoteUnsupported",
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.ReopenFailed,
    from: [P.Waiting, P.WaitingNoBrowser],
    problem: GrokProblemRule.SetAdvisory,
    problemKey: "grok-subscription.problem.browserFailed",
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.CallbackTakeFailed,
    from: [P.Waiting],
    problem: GrokProblemRule.SetAdvisory,
    problemKey: "grok-subscription.problem.callbackUncertain",
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.CallbackForwardFailed,
    from: [P.Waiting],
    problem: GrokProblemRule.SetAdvisory,
    problemKey: "grok-subscription.problem.forwardUncertain",
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.ReplayStarted,
    from: [P.Starting, ...inFlight, P.Naming],
    problem: GrokProblemRule.Clear,
    retry: GrokRetryRule.Clear,
  },
  {
    event: E.AuxiliaryStarted,
    from: [P.Waiting, P.WaitingNoBrowser],
    problem: GrokProblemRule.ClearAdvisory,
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.StartAccepted,
    from: [P.Starting],
    to: P.Preparing,
    problem: GrokProblemRule.Clear,
    retry: GrokRetryRule.Clear,
  },
  {
    event: E.StartRefused,
    from: [P.Starting],
    to: P.StartRefused,
    problem: GrokProblemRule.Set,
    problemKey: "grok-subscription.problem.requestFailed",
    retry: GrokRetryRule.Clear,
  },
  {
    event: E.StartUncertain,
    from: [P.Starting],
    problem: GrokProblemRule.Set,
    problemKey: "grok-subscription.problem.requestUnconfirmed",
    retry: GrokRetryRule.Hold,
    hold: GrokRetry.Start,
  },
  {
    event: E.CancelAccepted,
    from: [...inFlight, ...winding],
    to: toCanceling,
    problem: GrokProblemRule.Set,
    problemKey: "grok-subscription.problem.canceling",
    retry: GrokRetryRule.Clear,
  },
  {
    event: E.CancelRefused,
    from: inFlight,
    problem: GrokProblemRule.Set,
    problemKey: "grok-subscription.problem.cancelFailed",
    retry: GrokRetryRule.Clear,
  },
  {
    event: E.CancelUncertain,
    from: inFlight,
    problem: GrokProblemRule.Set,
    problemKey: "grok-subscription.problem.cancelUnconfirmed",
    retry: GrokRetryRule.Hold,
    hold: GrokRetry.Cancel,
  },
  {
    event: E.NameSaved,
    from: [P.Naming],
    to: P.Disposed,
    problem: GrokProblemRule.Clear,
    retry: GrokRetryRule.Clear,
  },
  {
    event: E.NameRefused,
    from: [P.Naming],
    problem: GrokProblemRule.Set,
    problemKey: "grok-subscription.problem.nameUnconfirmed",
    retry: GrokRetryRule.Clear,
  },
  {
    event: E.NameUncertain,
    from: [P.Naming],
    problem: GrokProblemRule.Set,
    problemKey: "grok-subscription.problem.nameUnconfirmed",
    retry: GrokRetryRule.Hold,
    hold: GrokRetry.Name,
  },
  {
    event: E.Dispose,
    from: aliveEverywhere,
    to: P.Disposed,
    problem: GrokProblemRule.Clear,
    retry: GrokRetryRule.Clear,
  },
];

export const grokLoginPhases = Object.values(GrokLoginPhase);
export const grokLoginEvents = Object.values(GrokLoginEvent);

const cells = new Map<string, GrokLoginCell>();
for (const entry of entries)
  for (const from of entry.from) {
    const id = `${from}|${entry.event}`;
    if (cells.has(id)) throw new Error(`Duplicate Grok login transition ${id}`);
    const to =
      typeof entry.to === "string" ? entry.to : (entry.to?.[from] ?? from);
    cells.set(id, {
      event: entry.event,
      from,
      to,
      problem: entry.problem,
      problemKey: entry.problemKey,
      retry: entry.retry,
      hold: entry.hold,
    });
  }

// The documented outcome of one cell, or undefined when the event is ignored in
// that phase (the state is returned unchanged).
export const grokLoginCell = (
  phase: GrokLoginPhase,
  event: GrokLoginEvent,
): GrokLoginCell | undefined => cells.get(`${phase}|${event}`);

export const initialGrokLoginState = (
  nativeAvailable: boolean,
): GrokLoginState =>
  nativeAvailable
    ? { phase: P.Starting, retry: GrokRetry.None }
    : {
        phase: P.DesktopRequired,
        problem: "grok-subscription.problem.desktopRequired",
        retry: GrokRetry.None,
      };

export function transition(
  state: GrokLoginState,
  event: GrokLoginEvent,
): GrokLoginState {
  const cell = grokLoginCell(state.phase, event);
  if (!cell) return state;
  const retained = state.retry !== GrokRetry.None;
  let problem = state.problem;
  switch (cell.problem) {
    case GrokProblemRule.Keep:
      break;
    case GrokProblemRule.Clear:
      problem = undefined;
      break;
    case GrokProblemRule.Set:
      problem = cell.problemKey;
      break;
    case GrokProblemRule.SetAdvisory:
      if (!retained) problem = cell.problemKey;
      break;
    case GrokProblemRule.ClearAdvisory:
      if (!retained) problem = undefined;
      break;
    case GrokProblemRule.ClearStatusOnly:
      if (problem === statusProblem) problem = undefined;
      break;
    case GrokProblemRule.SetStatusUnlessSpecific:
      if (!problem) problem = cell.problemKey;
      break;
  }
  const retry =
    cell.retry === GrokRetryRule.Hold
      ? (cell.hold ?? GrokRetry.None)
      : cell.retry === GrokRetryRule.Clear
        ? GrokRetry.None
        : state.retry;
  if (
    cell.to === state.phase &&
    problem === state.problem &&
    retry === state.retry
  )
    return state;
  return { phase: cell.to, problem, retry };
}

interface PhaseSpec {
  stage?: GrokLoginStage;
  native: GrokNativeBinding;
  openBrowser: boolean;
  copyLink: boolean;
  cancel: boolean;
  name: GrokNameField;
  help: GrokHelp;
}
const none = GrokNativeBinding.None,
  requested = GrokNativeBinding.Requested,
  disposed = GrokNativeBinding.Disposed;
const quiet = {
  openBrowser: false,
  copyLink: false,
  cancel: false,
  name: GrokNameField.Hidden,
  help: GrokHelp.None,
};
const S = GrokLoginStage;
// What each phase shows and owns. Cancel, Open browser again and Copy link are
// offered only here; Retry original request follows the retained retry kind.
export const grokPhaseSpecs: Record<GrokLoginPhase, PhaseSpec> = {
  [P.DesktopRequired]: {
    ...quiet,
    stage: S.Unsupported,
    native: none,
    help: GrokHelp.Stage,
  },
  [P.Starting]: { ...quiet, stage: S.Preparing, native: none },
  [P.Preparing]: { ...quiet, stage: S.Preparing, native: none, cancel: true },
  [P.AwaitingLink]: { ...quiet, stage: S.Waiting, native: none, cancel: true },
  [P.Opening]: {
    ...quiet,
    stage: S.Preparing,
    native: requested,
    copyLink: true,
    cancel: true,
  },
  [P.Waiting]: {
    ...quiet,
    stage: S.Waiting,
    native: requested,
    openBrowser: true,
    copyLink: true,
    cancel: true,
    help: GrokHelp.Waiting,
  },
  [P.WaitingNoBrowser]: {
    ...quiet,
    stage: S.Waiting,
    native: requested,
    openBrowser: true,
    copyLink: true,
    cancel: true,
    help: GrokHelp.WaitingLink,
  },
  [P.WaitingLinkOnly]: {
    ...quiet,
    stage: S.Waiting,
    native: requested,
    copyLink: true,
    cancel: true,
    help: GrokHelp.WaitingLink,
  },
  [P.PreparingCanceling]: { ...quiet, stage: S.Preparing, native: disposed },
  [P.WaitingCanceling]: { ...quiet, stage: S.Waiting, native: disposed },
  [P.Naming]: {
    ...quiet,
    stage: S.Naming,
    native: disposed,
    name: GrokNameField.Editable,
  },
  [P.Canceled]: {
    ...quiet,
    stage: S.Canceled,
    native: disposed,
    help: GrokHelp.Stage,
  },
  [P.Expired]: {
    ...quiet,
    stage: S.Expired,
    native: disposed,
    help: GrokHelp.Stage,
  },
  [P.Unsupported]: {
    ...quiet,
    stage: S.Unsupported,
    native: disposed,
    help: GrokHelp.Stage,
  },
  [P.Recovery]: {
    ...quiet,
    stage: S.Recovery,
    native: disposed,
    help: GrokHelp.Stage,
  },
  [P.Failed]: {
    ...quiet,
    stage: S.Failed,
    native: disposed,
    help: GrokHelp.Stage,
  },
  [P.StartRefused]: {
    ...quiet,
    stage: S.Failed,
    native: none,
    help: GrokHelp.Stage,
  },
  [P.Disposed]: { ...quiet, native: disposed },
};

export interface GrokLoginPresentation {
  stage?: GrokLoginStage;
  native: GrokNativeBinding;
  // Cancel, Open browser again and Copy link are shown; Cancel and Open
  // browser again are disabled while a retry closure is retained.
  openBrowser: boolean;
  copyLink: boolean;
  cancel: boolean;
  retry: boolean;
  name: GrokNameField;
  help: GrokHelp;
  // The login reached a state in which no further status read is needed.
  settled: boolean;
  // A cancellation was acknowledged or observed; the login is winding down.
  canceling: boolean;
}
export function grokLoginPresentation(
  state: GrokLoginState,
): GrokLoginPresentation {
  const spec = grokPhaseSpecs[state.phase];
  const retained = state.retry !== GrokRetry.None;
  return {
    stage: spec.stage,
    native: spec.native,
    openBrowser: spec.openBrowser,
    copyLink: spec.copyLink,
    cancel: spec.cancel,
    retry: retained,
    name:
      spec.name === GrokNameField.Editable && retained
        ? GrokNameField.Locked
        : spec.name,
    help: spec.help,
    settled: settledPhases.includes(state.phase) || state.phase === P.Disposed,
    canceling: winding.includes(state.phase),
  };
}

// Maps one status read of the original operation to its event.
export function grokPollEvent(
  stage: GrokLoginStage,
  canceled: boolean,
): GrokLoginEvent | undefined {
  switch (stage) {
    case GrokLoginStage.Preparing:
      return canceled ? E.PollPreparingCanceled : E.PollPreparing;
    case GrokLoginStage.Waiting:
      return canceled ? E.PollWaitingCanceled : E.PollWaiting;
    case GrokLoginStage.Naming:
      return E.PollSucceeded;
    case GrokLoginStage.Canceled:
      return E.PollCanceled;
    case GrokLoginStage.Expired:
      return E.PollExpired;
    case GrokLoginStage.Unsupported:
      return E.PollUnsupported;
    case GrokLoginStage.Recovery:
      return E.PollRecovery;
    case GrokLoginStage.Failed:
      return E.PollFailed;
  }
}

// The events that settle each retained mutation after an uncertain or definite
// failure. Retry original request replays the same closure and settles through
// the same events, so a retained request keeps its mutation-specific problem.
export const grokRetryOutcomes: Record<
  Exclude<GrokRetry, GrokRetry.None>,
  { refused: GrokLoginEvent; uncertain: GrokLoginEvent }
> = {
  [GrokRetry.Start]: { refused: E.StartRefused, uncertain: E.StartUncertain },
  [GrokRetry.Cancel]: {
    refused: E.CancelRefused,
    uncertain: E.CancelUncertain,
  },
  [GrokRetry.Name]: { refused: E.NameRefused, uncertain: E.NameUncertain },
};
