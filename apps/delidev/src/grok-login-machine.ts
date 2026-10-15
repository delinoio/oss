// SPDX-License-Identifier: Apache-2.0
import type { MessageKey } from "./localization";

// The pure state machine behind the Grok browser and device-code sign-in controller. The
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

// The explicit sign-in method chosen by the user. It never changes mid-login.
export enum GrokLoginMethod {
  Browser = "browser",
  DeviceCode = "device-code",
}

// A phase fixes the stage, the visible actions and (with the method) the
// native binding.
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
  // Device code: the validated verification address and user code are pinned
  // and shown; no open was requested yet.
  DeviceWaiting = "device-waiting",
  // Device code: an open of the verification page was requested (pending or
  // confirmed); only a native Dispose retires an admitted dispatch.
  DeviceOpened = "device-opened",
  // Device code: the native opener failed, so the validated address is shown
  // for manual use in any browser.
  DeviceAddress = "device-address",
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
  // The opening owns no native state, so leaving it needs no native call.
  None = "none",
  // A native Dispose is owed: a browser login owns its receiver for the whole
  // opening, and a device login owns native state once an open was requested.
  Owed = "owed",
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
  DeviceWaiting = "deviceWaiting",
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
  // The validated device verification address and user code are pinned.
  CodeReady = "code-ready",
  // Open verification page: requested, confirmed, or failed.
  VerificationOpenStarted = "verification-open-started",
  VerificationOpenSucceeded = "verification-open-succeeded",
  VerificationOpenFailed = "verification-open-failed",
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
  method: GrokLoginMethod;
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

const devicePhases = [P.DeviceWaiting, P.DeviceOpened, P.DeviceAddress];
const inFlight = [
  P.Preparing,
  P.AwaitingLink,
  P.Opening,
  P.Waiting,
  P.WaitingNoBrowser,
  P.WaitingLinkOnly,
  ...devicePhases,
];
const winding = [P.PreparingCanceling, P.WaitingCanceling];
// Opening exists only inside one poll tick, so a status read never meets it.
const observable = [
  P.Preparing,
  P.AwaitingLink,
  P.Waiting,
  P.WaitingNoBrowser,
  P.WaitingLinkOnly,
  ...devicePhases,
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
  // The only method the event belongs to; omitted: both.
  method?: GrokLoginMethod;
  from: GrokLoginPhase;
  to: GrokLoginPhase;
  problem: GrokProblemRule;
  problemKey?: MessageKey;
  retry: GrokRetryRule;
  hold?: GrokRetry;
}
interface Entry {
  event: GrokLoginEvent;
  method?: GrokLoginMethod;
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
  [P.DeviceWaiting]: P.WaitingCanceling,
  [P.DeviceOpened]: P.WaitingCanceling,
  [P.DeviceAddress]: P.WaitingCanceling,
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
    // Server stages only advance, so a preparing read after the login waited
    // keeps the phase; every readable status still recovers the polling notice.
    event: E.PollPreparing,
    from: observable,
    problem: GrokProblemRule.ClearStatusOnly,
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.PollPreparingCanceled,
    from: observable,
    to: {
      [P.Preparing]: P.PreparingCanceling,
      [P.AwaitingLink]: P.WaitingCanceling,
      [P.Waiting]: P.WaitingCanceling,
      [P.WaitingNoBrowser]: P.WaitingCanceling,
      [P.WaitingLinkOnly]: P.WaitingCanceling,
      [P.DeviceWaiting]: P.WaitingCanceling,
      [P.DeviceOpened]: P.WaitingCanceling,
      [P.DeviceAddress]: P.WaitingCanceling,
    },
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
    method: GrokLoginMethod.Browser,
    from: [P.AwaitingLink],
    to: P.Opening,
    problem: GrokProblemRule.Keep,
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.OpenSucceeded,
    method: GrokLoginMethod.Browser,
    from: [P.Opening],
    to: P.Waiting,
    problem: GrokProblemRule.ClearAdvisory,
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.OpenIncompatible,
    method: GrokLoginMethod.Browser,
    from: [P.Opening],
    to: P.WaitingLinkOnly,
    problem: GrokProblemRule.SetAdvisory,
    problemKey: "grok-subscription.problem.remoteUnsupported",
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.OpenFailed,
    method: GrokLoginMethod.Browser,
    from: [P.Opening],
    to: P.WaitingNoBrowser,
    problem: GrokProblemRule.SetAdvisory,
    problemKey: "grok-subscription.problem.browserFailed",
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.ReopenSucceeded,
    method: GrokLoginMethod.Browser,
    from: [P.Waiting, P.WaitingNoBrowser],
    to: P.Waiting,
    problem: GrokProblemRule.ClearAdvisory,
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.ReopenIncompatible,
    method: GrokLoginMethod.Browser,
    from: [P.Waiting, P.WaitingNoBrowser],
    to: P.WaitingLinkOnly,
    problem: GrokProblemRule.SetAdvisory,
    problemKey: "grok-subscription.problem.remoteUnsupported",
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.ReopenFailed,
    method: GrokLoginMethod.Browser,
    from: [P.Waiting, P.WaitingNoBrowser],
    problem: GrokProblemRule.SetAdvisory,
    problemKey: "grok-subscription.problem.browserFailed",
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.CallbackTakeFailed,
    method: GrokLoginMethod.Browser,
    from: [P.Waiting],
    problem: GrokProblemRule.SetAdvisory,
    problemKey: "grok-subscription.problem.callbackUncertain",
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.CallbackForwardFailed,
    method: GrokLoginMethod.Browser,
    from: [P.Waiting],
    problem: GrokProblemRule.SetAdvisory,
    problemKey: "grok-subscription.problem.forwardUncertain",
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.CodeReady,
    method: GrokLoginMethod.DeviceCode,
    from: [P.AwaitingLink],
    to: P.DeviceWaiting,
    problem: GrokProblemRule.Keep,
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.VerificationOpenStarted,
    method: GrokLoginMethod.DeviceCode,
    from: devicePhases,
    to: P.DeviceOpened,
    problem: GrokProblemRule.ClearAdvisory,
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.VerificationOpenSucceeded,
    method: GrokLoginMethod.DeviceCode,
    from: [P.DeviceOpened],
    problem: GrokProblemRule.ClearAdvisory,
    retry: GrokRetryRule.Keep,
  },
  {
    event: E.VerificationOpenFailed,
    method: GrokLoginMethod.DeviceCode,
    from: [P.DeviceOpened],
    to: P.DeviceAddress,
    problem: GrokProblemRule.SetAdvisory,
    problemKey: "grok-subscription.problem.verificationOpenFailed",
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
    method: GrokLoginMethod.Browser,
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
      method: entry.method,
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
  method: GrokLoginMethod = GrokLoginMethod.Browser,
): GrokLoginState =>
  nativeAvailable
    ? { method, phase: P.Starting, retry: GrokRetry.None }
    : {
        method,
        phase: P.DesktopRequired,
        problem: "grok-subscription.problem.desktopRequired",
        retry: GrokRetry.None,
      };

export function transition(
  state: GrokLoginState,
  event: GrokLoginEvent,
): GrokLoginState {
  const cell = grokLoginCell(state.phase, event);
  // An event of the other sign-in method is never applied.
  if (!cell || (cell.method && cell.method !== state.method)) return state;
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
  return { method: state.method, phase: cell.to, problem, retry };
}

interface PhaseSpec {
  stage?: GrokLoginStage;
  openBrowser: boolean;
  copyLink: boolean;
  // Device code: Open verification page, and the pinned user code with Copy
  // code, and the validated address with Copy address.
  openVerification: boolean;
  copyCode: boolean;
  copyAddress: boolean;
  cancel: boolean;
  name: GrokNameField;
  help: GrokHelp;
}
const quiet = {
  openBrowser: false,
  copyLink: false,
  openVerification: false,
  copyCode: false,
  copyAddress: false,
  cancel: false,
  name: GrokNameField.Hidden,
  help: GrokHelp.None,
};
const S = GrokLoginStage;
const device = {
  ...quiet,
  stage: S.Waiting,
  openVerification: true,
  copyCode: true,
  cancel: true,
  help: GrokHelp.DeviceWaiting,
};
// What each phase shows. Cancel, Open browser again, Copy link and the device
// controls are offered only here; Retry original request follows the retained
// retry kind.
export const grokPhaseSpecs: Record<GrokLoginPhase, PhaseSpec> = {
  [P.DesktopRequired]: { ...quiet, stage: S.Unsupported, help: GrokHelp.Stage },
  [P.Starting]: { ...quiet, stage: S.Preparing },
  [P.Preparing]: { ...quiet, stage: S.Preparing, cancel: true },
  [P.AwaitingLink]: { ...quiet, stage: S.Waiting, cancel: true },
  [P.Opening]: {
    ...quiet,
    stage: S.Preparing,
    copyLink: true,
    cancel: true,
  },
  [P.Waiting]: {
    ...quiet,
    stage: S.Waiting,
    openBrowser: true,
    copyLink: true,
    cancel: true,
    help: GrokHelp.Waiting,
  },
  [P.WaitingNoBrowser]: {
    ...quiet,
    stage: S.Waiting,
    openBrowser: true,
    copyLink: true,
    cancel: true,
    help: GrokHelp.WaitingLink,
  },
  [P.WaitingLinkOnly]: {
    ...quiet,
    stage: S.Waiting,
    copyLink: true,
    cancel: true,
    help: GrokHelp.WaitingLink,
  },
  [P.DeviceWaiting]: device,
  [P.DeviceOpened]: device,
  [P.DeviceAddress]: { ...device, copyAddress: true },
  [P.PreparingCanceling]: { ...quiet, stage: S.Preparing },
  [P.WaitingCanceling]: { ...quiet, stage: S.Waiting },
  [P.Naming]: { ...quiet, stage: S.Naming, name: GrokNameField.Editable },
  [P.Canceled]: { ...quiet, stage: S.Canceled, help: GrokHelp.Stage },
  [P.Expired]: { ...quiet, stage: S.Expired, help: GrokHelp.Stage },
  [P.Unsupported]: { ...quiet, stage: S.Unsupported, help: GrokHelp.Stage },
  [P.Recovery]: { ...quiet, stage: S.Recovery, help: GrokHelp.Stage },
  [P.Failed]: { ...quiet, stage: S.Failed, help: GrokHelp.Stage },
  [P.StartRefused]: { ...quiet, stage: S.Failed, help: GrokHelp.Stage },
  [P.Disposed]: { ...quiet },
};

// The native state an opening owns in a phase. A browser login owns its
// receiver from the start; a device login owns native state only once the user
// asked to open the verification page, so leaving a never-opened device login
// makes no native call.
export function grokNativeBinding(
  phase: GrokLoginPhase,
  method: GrokLoginMethod,
): GrokNativeBinding {
  switch (phase) {
    case P.DesktopRequired:
      return GrokNativeBinding.None;
    case P.Starting:
    case P.Preparing:
    case P.AwaitingLink:
    case P.DeviceWaiting:
      return method === GrokLoginMethod.Browser
        ? GrokNativeBinding.Owed
        : GrokNativeBinding.None;
    case P.Opening:
    case P.Waiting:
    case P.WaitingNoBrowser:
    case P.WaitingLinkOnly:
    case P.DeviceOpened:
    case P.DeviceAddress:
      return GrokNativeBinding.Owed;
    default:
      return GrokNativeBinding.Disposed;
  }
}

// What a copy action copies and which copy notice belongs to it. Every value
// is display-only.
export enum GrokCopyTarget {
  Link = "link",
  Code = "code",
  Address = "address",
}
// A copy notice belongs to the value it copied and survives exactly while the
// current phase still offers that value (the Copy link, Copy code and Copy
// address columns of the phase table), whatever else it offers.
export function grokCopyOffered(
  offered: Pick<GrokLoginPresentation, "copyLink" | "copyCode" | "copyAddress">,
  target: GrokCopyTarget,
): boolean {
  switch (target) {
    case GrokCopyTarget.Link:
      return offered.copyLink;
    case GrokCopyTarget.Code:
      return offered.copyCode;
    case GrokCopyTarget.Address:
      return offered.copyAddress;
  }
}

// Copy attempts. A clipboard write settles later than the click that started
// it, so every attempt carries an identity and only the latest attempt may show
// a notice. An attempt is retired when the phase stops offering its value or
// the task is disposed, and a newer attempt supersedes it, so a completion that
// outlives its attempt (a value hidden and revealed again, two writes settling
// out of order, a notice timer of a replaced notice) never changes anything.
export enum GrokCopyPhase {
  // Nothing is pending and no notice is shown.
  Idle = "idle",
  // The latest attempt waits for the clipboard; no notice is shown.
  Pending = "pending",
  // The latest attempt copied its value; the copied notice is shown.
  Copied = "copied",
  // The latest attempt could not copy; the failure notice is shown.
  Failed = "failed",
}
export enum GrokCopyEvent {
  // Copy link, Copy code or Copy address was activated for a value the current
  // phase offers.
  Started = "started",
  // The clipboard settled the attempt the event names.
  Succeeded = "succeeded",
  Rejected = "rejected",
  // The current phase stopped offering the value of the latest attempt, or the
  // task was disposed.
  Retired = "retired",
  // The notice timer of the attempt the event names fired.
  NoticeExpired = "notice-expired",
}
export enum GrokCopyAttemptRelation {
  // The event does not name an attempt.
  Unnamed = "unnamed",
  // The event names the latest attempt (it may already be retired).
  Latest = "latest",
  // The event names an earlier attempt that a newer one superseded.
  Superseded = "superseded",
}
export interface GrokCopyState {
  phase: GrokCopyPhase;
  // The value of the latest attempt; absent while idle.
  target?: GrokCopyTarget;
  // The identity of the latest attempt; it only grows.
  attempt: number;
}
export const initialGrokCopyState: GrokCopyState = {
  phase: GrokCopyPhase.Idle,
  attempt: 0,
};
export interface GrokCopyCell {
  from: GrokCopyPhase;
  event: GrokCopyEvent;
  relation: GrokCopyAttemptRelation;
  to: GrokCopyPhase;
}
const C = GrokCopyPhase;
const CE = GrokCopyEvent;
const R = GrokCopyAttemptRelation;
const copyPhases = Object.values(GrokCopyPhase);
export const grokCopyEvents = Object.values(GrokCopyEvent);
const namedEvents: GrokCopyEvent[] = [
  CE.Succeeded,
  CE.Rejected,
  CE.NoticeExpired,
];
// Every cell is listed so the contract table shows ignored completions too.
const copyCells = new Map<string, GrokCopyCell>();
for (const from of copyPhases)
  for (const event of grokCopyEvents)
    for (const relation of namedEvents.includes(event)
      ? [R.Latest, R.Superseded]
      : [R.Unnamed]) {
      let to = from;
      if (event === CE.Started) to = C.Pending;
      else if (event === CE.Retired) to = C.Idle;
      else if (relation === R.Latest) {
        if (from === C.Pending && event === CE.Succeeded) to = C.Copied;
        if (from === C.Pending && event === CE.Rejected) to = C.Failed;
        if (event === CE.NoticeExpired && from !== C.Pending) to = C.Idle;
      }
      copyCells.set(`${from}|${event}|${relation}`, {
        from,
        event,
        relation,
        to,
      });
    }
export const grokCopyCells = [...copyCells.values()];
export const grokCopyRelation = (
  state: GrokCopyState,
  event: GrokCopyEvent,
  attempt?: number,
): GrokCopyAttemptRelation =>
  !namedEvents.includes(event)
    ? R.Unnamed
    : attempt === state.attempt
      ? R.Latest
      : R.Superseded;
export function grokCopyTransition(
  state: GrokCopyState,
  event: GrokCopyEvent,
  detail: { attempt?: number; target?: GrokCopyTarget } = {},
): GrokCopyState {
  const cell = copyCells.get(
    `${state.phase}|${event}|${grokCopyRelation(state, event, detail.attempt)}`,
  );
  if (!cell) return state;
  if (event === CE.Started)
    return {
      phase: cell.to,
      target: detail.target,
      attempt: state.attempt + 1,
    };
  if (cell.to === state.phase) return state;
  return {
    phase: cell.to,
    target: cell.to === C.Idle ? undefined : state.target,
    attempt: state.attempt,
  };
}
// The retirement that follows a phase change: the latest attempt ends exactly
// when the phase stops offering its value, whatever else it still offers.
export function grokCopyAfterPhase(
  state: GrokCopyState,
  offered: Pick<GrokLoginPresentation, "copyLink" | "copyCode" | "copyAddress">,
): GrokCopyState {
  return state.target && !grokCopyOffered(offered, state.target)
    ? grokCopyTransition(state, CE.Retired)
    : state;
}

export interface GrokLoginPresentation {
  stage?: GrokLoginStage;
  native: GrokNativeBinding;
  // Cancel, Open browser again and Copy link are shown; Cancel and Open
  // browser again are disabled while a retry closure is retained.
  openBrowser: boolean;
  copyLink: boolean;
  openVerification: boolean;
  copyCode: boolean;
  copyAddress: boolean;
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
    native: grokNativeBinding(state.phase, state.method),
    openBrowser: spec.openBrowser,
    copyLink: spec.copyLink,
    openVerification: spec.openVerification,
    copyCode: spec.copyCode,
    copyAddress: spec.copyAddress,
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
