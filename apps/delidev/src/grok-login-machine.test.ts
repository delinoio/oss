// SPDX-License-Identifier: Apache-2.0
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import {
  GrokCopyAttemptRelation,
  GrokCopyEvent,
  GrokCopyPhase,
  GrokCopyTarget,
  GrokHelp,
  GrokLoginEvent,
  GrokLoginMethod,
  GrokLoginPhase,
  GrokLoginStage,
  GrokNameField,
  GrokNativeBinding,
  GrokProblemRule,
  GrokRetry,
  GrokRetryRule,
  grokCopyAfterPhase,
  grokCopyCells,
  grokCopyEvents,
  grokCopyOffered,
  grokCopyTransition,
  grokLoginCell,
  grokLoginEvents,
  grokLoginPhases,
  grokLoginPresentation,
  grokNativeBinding,
  grokPhaseSpecs,
  grokPollEvent,
  grokRetryOutcomes,
  initialGrokCopyState,
  initialGrokLoginState,
  statusProblem,
  transition,
  type GrokCopyState,
  type GrokLoginState,
} from "./grok-login-machine";
import english from "./locales/en/grok-subscription.json";
import korean from "./locales/ko/grok-subscription.json";

// Every phase x event pair is generated from the transition table, so a cell
// cannot exist without a documented, tested outcome. The contract tables in
// docs/cmds-delidev-grok-subscription-contract.md are rendered from the same
// table: run `UPDATE_GROK_LOGIN_DOCS=1 pnpm exec vitest run
// src/grok-login-machine.test.ts` from apps/delidev to regenerate them.

const P = GrokLoginPhase;
const E = GrokLoginEvent;
// Vitest runs from apps/delidev.
const docsPath = resolve(
  process.cwd(),
  "../../docs/cmds-delidev-grok-subscription-contract.md",
);
const begin = "<!-- grok-login-machine:begin -->";
const end = "<!-- grok-login-machine:end -->";

const problemPrefix = "grok-subscription.problem.";
const shortKey = (key: string) => key.slice(problemPrefix.length);

// Where each retry kind is legal and which notice always explains it.
const retryHomes: Record<
  Exclude<GrokRetry, GrokRetry.None>,
  { phases: GrokLoginPhase[]; problem: string }
> = {
  [GrokRetry.Start]: {
    phases: [P.Starting],
    problem: "grok-subscription.problem.requestUnconfirmed",
  },
  [GrokRetry.Cancel]: {
    phases: [
      P.Preparing,
      P.AwaitingLink,
      P.Opening,
      P.Waiting,
      P.WaitingNoBrowser,
      P.WaitingLinkOnly,
      P.DeviceWaiting,
      P.DeviceOpened,
      P.DeviceAddress,
    ],
    problem: "grok-subscription.problem.cancelUnconfirmed",
  },
  [GrokRetry.Name]: {
    phases: [P.Naming],
    problem: "grok-subscription.problem.nameUnconfirmed",
  },
};
const retryKinds = Object.keys(retryHomes) as Array<
  Exclude<GrokRetry, GrokRetry.None>
>;

const methods = Object.values(GrokLoginMethod);
// A representative state of every phase, with and without a retained request.
function statesOf(
  phase: GrokLoginPhase,
  method: GrokLoginMethod,
): GrokLoginState[] {
  const states: GrokLoginState[] = [{ method, phase, retry: GrokRetry.None }];
  for (const kind of retryKinds)
    if (retryHomes[kind].phases.includes(phase))
      states.push({
        method,
        phase,
        retry: kind,
        problem: retryHomes[kind].problem as GrokLoginState["problem"],
      });
  return states;
}

function expectedProblem(
  state: GrokLoginState,
  rule: GrokProblemRule,
  key: GrokLoginState["problem"],
) {
  const retained = state.retry !== GrokRetry.None;
  switch (rule) {
    case GrokProblemRule.Keep:
      return state.problem;
    case GrokProblemRule.Clear:
      return undefined;
    case GrokProblemRule.Set:
      return key;
    case GrokProblemRule.SetAdvisory:
      return retained ? state.problem : key;
    case GrokProblemRule.ClearAdvisory:
      return retained ? state.problem : undefined;
    case GrokProblemRule.ClearStatusOnly:
      return state.problem === statusProblem ? undefined : state.problem;
    case GrokProblemRule.SetStatusUnlessSpecific:
      return state.problem ?? key;
  }
}
function expectedRetry(
  state: GrokLoginState,
  rule: GrokRetryRule,
  hold?: GrokRetry,
) {
  if (rule === GrokRetryRule.Hold) return hold;
  return rule === GrokRetryRule.Clear ? GrokRetry.None : state.retry;
}

const pairs = grokLoginPhases.flatMap((phase) =>
  grokLoginEvents.flatMap((event) =>
    methods.map((method) => [phase, event, method] as const),
  ),
);

describe("every phase and event cell", () => {
  it("covers every phase and event pair", () => {
    expect(pairs).toHaveLength(
      grokLoginPhases.length * grokLoginEvents.length * methods.length,
    );
    expect(new Set(grokLoginPhases).size).toBe(grokLoginPhases.length);
  });
  it.each(pairs)("%s x %s (%s)", (phase, event, method) => {
    const cell = grokLoginCell(phase, event);
    for (const state of statesOf(phase, method)) {
      const next = transition(state, event);
      if (!cell || (cell.method && cell.method !== method)) {
        // An ignored event never changes anything, including a late result.
        expect(next).toBe(state);
        continue;
      }
      expect(cell.from).toBe(phase);
      expect(cell.event).toBe(event);
      expect(next.method).toBe(method);
      expect(next.phase).toBe(cell.to);
      expect(next.problem).toBe(
        expectedProblem(state, cell.problem, cell.problemKey),
      );
      expect(next.retry).toBe(expectedRetry(state, cell.retry, cell.hold));
      // Once the native binding was disposed it stays disposed.
      if (grokNativeBinding(phase, method) === GrokNativeBinding.Disposed)
        expect(grokNativeBinding(next.phase, method)).toBe(
          GrokNativeBinding.Disposed,
        );
    }
  });
});

describe("table invariants", () => {
  // Every state reachable from either start state through any event sequence.
  const reachable = (() => {
    const seen = new Map<string, GrokLoginState>();
    const id = (s: GrokLoginState) =>
      `${s.method}|${s.phase}|${s.problem}|${s.retry}`;
    const queue = methods.flatMap((method) => [
      initialGrokLoginState(true, method),
      initialGrokLoginState(false, method),
    ]);
    while (queue.length) {
      const state = queue.pop()!;
      if (seen.has(id(state))) continue;
      seen.set(id(state), state);
      for (const event of grokLoginEvents) queue.push(transition(state, event));
    }
    return [...seen.values()];
  })();

  it("reaches every phase", () => {
    expect(new Set(reachable.map((s) => s.phase))).toEqual(
      new Set(grokLoginPhases),
    );
  });
  it("keeps each sign-in method in its own phases", () => {
    const browserOnly = [
      P.Opening,
      P.Waiting,
      P.WaitingNoBrowser,
      P.WaitingLinkOnly,
    ];
    const deviceOnly = [P.DeviceWaiting, P.DeviceOpened, P.DeviceAddress];
    for (const s of reachable) {
      if (s.method === GrokLoginMethod.Browser)
        expect(deviceOnly).not.toContain(s.phase);
      else expect(browserOnly).not.toContain(s.phase);
    }
  });
  it.each(
    reachable.map((s) => [s.method, s.phase, s.problem ?? "-", s.retry, s]),
  )(
    "reachable %s / %s / %s / %s is consistent",
    (_method, _phase, _problem, _retry, state) => {
      const s = state as GrokLoginState;
      const ui = grokLoginPresentation(s);
      // A retained request lives only where its mutation can run and is always
      // explained by its own mutation-specific notice.
      if (s.retry !== GrokRetry.None) {
        expect(retryHomes[s.retry].phases).toContain(s.phase);
        expect(s.problem).toBe(retryHomes[s.retry].problem);
        expect(ui.retry).toBe(true);
      } else expect(ui.retry).toBe(false);
      // A notice that tells the user to retry exists only with a retry button.
      if (s.problem === retryHomes[GrokRetry.Start].problem)
        expect(s.retry).toBe(GrokRetry.Start);
      if (s.problem === retryHomes[GrokRetry.Cancel].problem)
        expect(s.retry).toBe(GrokRetry.Cancel);
      if (s.problem === "grok-subscription.problem.requestFailed")
        expect([s.phase, s.retry]).toEqual([P.StartRefused, GrokRetry.None]);
      if (s.problem === "grok-subscription.problem.cancelFailed")
        expect(s.retry).toBe(GrokRetry.None);
      // The name field locks exactly while the naming save is retained.
      expect(ui.name === GrokNameField.Locked).toBe(s.retry === GrokRetry.Name);
      // Winding down and settled logins offer no login action and never keep a
      // native binding.
      if (ui.canceling || ui.settled) {
        expect([
          ui.cancel,
          ui.openBrowser,
          ui.copyLink,
          ui.openVerification,
          ui.copyCode,
          ui.copyAddress,
        ]).toEqual([false, false, false, false, false, false]);
        expect(ui.native).not.toBe(GrokNativeBinding.Owed);
      }
      if (ui.canceling)
        expect(s.problem).toBe("grok-subscription.problem.canceling");
      // Open browser again requires a browser the receiver can still open.
      if (ui.openBrowser) expect(ui.copyLink).toBe(true);
      // The verification address is only a fallback for the opener.
      if (ui.copyAddress) expect(ui.openVerification).toBe(true);
      if (ui.openVerification) expect(ui.copyCode).toBe(true);
      // Browser controls belong to the browser method and device controls to
      // the device method.
      if (s.method === GrokLoginMethod.DeviceCode)
        expect([ui.openBrowser, ui.copyLink]).toEqual([false, false]);
      else
        expect([ui.openVerification, ui.copyCode, ui.copyAddress]).toEqual([
          false,
          false,
          false,
        ]);
      // A device login owns native state only after an open was requested.
      if (
        s.method === GrokLoginMethod.DeviceCode &&
        [P.Starting, P.Preparing, P.AwaitingLink, P.DeviceWaiting].includes(
          s.phase,
        )
      )
        expect(ui.native).toBe(GrokNativeBinding.None);
      // Terminal and naming screens carry no stale problem or help.
      if (
        [
          P.Canceled,
          P.Expired,
          P.Unsupported,
          P.Recovery,
          P.Failed,
          P.Disposed,
        ].includes(s.phase)
      )
        expect(s.problem).toBeUndefined();
      if (s.phase === P.StartRefused)
        expect(s.problem).toBe("grok-subscription.problem.requestFailed");
      if (s.phase === P.DesktopRequired)
        expect(s.problem).toBe("grok-subscription.problem.desktopRequired");
      if (ui.stage === GrokLoginStage.Naming)
        expect(ui.name).not.toBe(GrokNameField.Hidden);
      else expect(ui.name).toBe(GrokNameField.Hidden);
    },
  );
  it("keeps the retained mutation's notice across every polling observation", () => {
    for (const kind of retryKinds)
      for (const phase of retryHomes[kind].phases)
        for (const event of grokLoginEvents) {
          const state: GrokLoginState = {
            method: GrokLoginMethod.Browser,
            phase,
            retry: kind,
            problem: retryHomes[kind].problem as GrokLoginState["problem"],
          };
          const next = transition(state, event);
          // Only an event that settles the retained mutation or the whole
          // login may change its notice.
          if (next.retry === kind && next.phase === phase)
            expect(next.problem).toBe(state.problem);
        }
  });
  it("offers each copy value exactly as its phase column says", () => {
    for (const s of reachable) {
      const ui = grokLoginPresentation(s);
      expect(grokCopyOffered(ui, GrokCopyTarget.Link)).toBe(ui.copyLink);
      expect(grokCopyOffered(ui, GrokCopyTarget.Code)).toBe(ui.copyCode);
      expect(grokCopyOffered(ui, GrokCopyTarget.Address)).toBe(ui.copyAddress);
    }
  });
  it("retires a copy notice with its own value, not with the phase's other copy actions", () => {
    // A failed verification open reveals the address beside the code; a later
    // successful open hides the address while the code stays offered.
    const failed = transition(
      transition(
        transition(
          { ...initialGrokLoginState(true, GrokLoginMethod.DeviceCode) },
          E.StartAccepted,
        ),
        E.PollWaiting,
      ),
      E.CodeReady,
    );
    const open = transition(failed, E.VerificationOpenStarted);
    const address = transition(open, E.VerificationOpenFailed);
    const reopened = transition(address, E.VerificationOpenStarted);
    expect(address.phase).toBe(P.DeviceAddress);
    expect(reopened.phase).toBe(P.DeviceOpened);
    const before = grokLoginPresentation(address),
      after = grokLoginPresentation(reopened);
    expect(grokCopyOffered(before, GrokCopyTarget.Address)).toBe(true);
    expect(grokCopyOffered(after, GrokCopyTarget.Address)).toBe(false);
    expect(grokCopyOffered(after, GrokCopyTarget.Code)).toBe(true);
    // A cancellation retires every copy value together.
    const canceling = transition(address, E.CancelAccepted);
    for (const target of Object.values(GrokCopyTarget))
      expect(grokCopyOffered(grokLoginPresentation(canceling), target)).toBe(
        false,
      );
  });
  it("treats a provisional refusal as a status read that settles nothing", () => {
    for (const method of methods)
      for (const phase of grokLoginPhases) {
        const cell = grokLoginCell(phase, E.PollRefusalProvisional);
        const base: GrokLoginState = { method, phase, retry: GrokRetry.None };
        if (!cell || (cell.method && cell.method !== method)) {
          // Settled, unobservable and not yet started phases ignore it.
          expect(transition(base, E.PollRefusalProvisional)).toEqual(base);
          continue;
        }
        expect(grokLoginPresentation(base).settled).toBe(false);
        // The phase, the native binding and any retained request stay as they
        // were; only the polling notice recovers.
        expect(cell.to).toBe(phase);
        expect(cell.retry).toBe(GrokRetryRule.Keep);
        expect(cell.problem).toBe(GrokProblemRule.ClearStatusOnly);
        for (const kind of retryKinds) {
          const state: GrokLoginState = {
            ...base,
            retry: kind,
            problem: retryHomes[kind].problem as GrokLoginState["problem"],
          };
          if (retryHomes[kind].phases.includes(phase))
            expect(transition(state, E.PollRefusalProvisional)).toEqual(state);
        }
      }
    // The refusal keeps the original sign-in cancelable and adopts the next
    // response like any other poll.
    for (const method of methods) {
      const preparing = initialGrokLoginState(true, method);
      const queued = transition(preparing, E.StartAccepted);
      expect(queued.phase).toBe(P.Preparing);
      const refused = transition(queued, E.PollRefusalProvisional);
      expect(refused).toEqual(queued);
      expect(grokLoginPresentation(refused).cancel).toBe(true);
      expect(transition(refused, E.PollFailed).phase).toBe(P.Failed);
      expect(transition(refused, E.PollSucceeded).phase).toBe(P.Naming);
      expect(transition(refused, E.PollUnsupported).phase).toBe(P.Unsupported);
    }
  });
  it("marks Disposed as absorbing", () => {
    for (const event of grokLoginEvents)
      expect(
        transition(
          {
            method: GrokLoginMethod.Browser,
            phase: P.Disposed,
            retry: GrokRetry.None,
          },
          event,
        ).phase,
      ).toBe(P.Disposed);
  });
  it("settles each retained mutation through its own notice", () => {
    for (const kind of retryKinds) {
      const outcomes = grokRetryOutcomes[kind];
      const home = retryHomes[kind].phases[0];
      const uncertain = transition(
        {
          method: GrokLoginMethod.Browser,
          phase: home,
          retry: GrokRetry.None,
        },
        outcomes.uncertain,
      );
      expect(uncertain.retry).toBe(kind);
      expect(uncertain.problem).toBe(retryHomes[kind].problem);
      // A second uncertain outcome of the replayed request keeps both.
      const replayed = transition(uncertain, E.ReplayStarted);
      expect(replayed.retry).toBe(GrokRetry.None);
      expect(replayed.problem).toBeUndefined();
      expect(transition(replayed, outcomes.uncertain)).toEqual(uncertain);
      // A definite refusal retains nothing and never claims a pending retry.
      const refused = transition(replayed, outcomes.refused);
      expect(refused.retry).toBe(GrokRetry.None);
      if (kind !== GrokRetry.Name)
        expect(refused.problem).not.toBe(retryHomes[kind].problem);
    }
  });
  it("maps every server stage to one poll event", () => {
    for (const stage of Object.values(GrokLoginStage)) {
      const event = grokPollEvent(stage, false);
      expect(event).toBeDefined();
      expect(grokLoginEvents).toContain(event);
    }
    expect(grokPollEvent(GrokLoginStage.Waiting, true)).toBe(
      E.PollWaitingCanceled,
    );
    expect(grokPollEvent(GrokLoginStage.Preparing, true)).toBe(
      E.PollPreparingCanceled,
    );
    // Only an unsupported response can be a provisional refusal.
    expect(grokPollEvent(GrokLoginStage.Unsupported, false, true)).toBe(
      E.PollRefusalProvisional,
    );
    expect(grokPollEvent(GrokLoginStage.Unsupported, false, false)).toBe(
      E.PollUnsupported,
    );
    for (const stage of Object.values(GrokLoginStage))
      if (stage !== GrokLoginStage.Unsupported)
        expect(grokPollEvent(stage, false, true)).toBe(
          grokPollEvent(stage, false),
        );
  });
});

describe("problem keys", () => {
  const used = new Set<string>([
    statusProblem,
    "grok-subscription.problem.desktopRequired",
  ]);
  for (const phase of grokLoginPhases)
    for (const event of grokLoginEvents) {
      const key = grokLoginCell(phase, event)?.problemKey;
      if (key) used.add(key);
    }
  const catalogKeys = (catalog: Record<string, string>) =>
    Object.keys(catalog).filter((key) => key.startsWith(problemPrefix));
  it("uses exactly the problem notices the catalogs define", () => {
    expect([...used].sort()).toEqual(catalogKeys(english).sort());
    expect(catalogKeys(korean).sort()).toEqual(catalogKeys(english).sort());
  });
  it("gives each notice distinct English and Korean text", () => {
    for (const catalog of [english, korean]) {
      const texts = catalogKeys(catalog).map(
        (key) => (catalog as Record<string, string>)[key],
      );
      expect(new Set(texts).size).toBe(texts.length);
    }
  });
});

const eventTriggers: Record<GrokLoginEvent, string> = {
  [E.PollPreparing]: "A status read reports preparing",
  [E.PollPreparingCanceled]:
    "A status read reports preparing with the canceled flag",
  [E.PollWaiting]: "A status read reports waiting",
  [E.PollWaitingCanceled]:
    "A status read reports waiting with the canceled flag (another client or a lost cancel response)",
  [E.PollCanceled]: "A status read reports the canceled state",
  [E.PollExpired]: "A status read reports the expired state",
  [E.PollUnsupported]:
    "A status read reports the unsupported state without a Grok diagnostic",
  [E.PollRefusalProvisional]:
    "A status read reports the unsupported state together with a Grok diagnostic: a refused Take the server keeps queued",
  [E.PollRecovery]: "A status read reports recovery required",
  [E.PollFailed]: "A status read reports the failed state",
  [E.PollSucceeded]:
    "A status read reports success and the fresh account verifies generation and operation",
  [E.PollError]: "A status read fails or its result cannot be verified",
  [E.LinkReady]:
    "The original authorization link passes validation for the first time",
  [E.CodeReady]:
    "The validated device verification address and user code pass validation for the first time",
  [E.VerificationOpenStarted]:
    "Open verification page is requested (the opener may be admitted before it finishes)",
  [E.VerificationOpenSucceeded]:
    "The native opener opened the verification page",
  [E.VerificationOpenFailed]:
    "The native opener failed to open the verification page",
  [E.OpenSucceeded]: "The native receiver confirms the first open",
  [E.OpenIncompatible]: "The first open meets the Incompatible refusal",
  [E.OpenFailed]: "The first open fails for any other reason",
  [E.ReopenSucceeded]: "Open browser again succeeds",
  [E.ReopenIncompatible]: "Open browser again meets the Incompatible refusal",
  [E.ReopenFailed]: "Open browser again fails",
  [E.CallbackTakeFailed]: "Taking the native callback fails (never retried)",
  [E.CallbackForwardFailed]:
    "Forwarding the taken callback fails (never retried)",
  [E.ReplayStarted]:
    "A start, cancel or name save request (first run or Retry original request) begins",
  [E.AuxiliaryStarted]: "Open browser again begins",
  [E.StartAccepted]: "Account creation and RequestSubscription succeed",
  [E.StartRefused]:
    "Account creation or RequestSubscription is refused definitively, or its receipt fails the local account, alias or operation checks",
  [E.StartUncertain]:
    "Account creation or RequestSubscription has an uncertain outcome",
  [E.CancelAccepted]: "CancelSubscription is acknowledged",
  [E.CancelRefused]:
    "CancelSubscription is refused definitively, or its receipt fails the local account checks",
  [E.CancelUncertain]: "CancelSubscription has an uncertain outcome",
  [E.NameSaved]: "The account alias save is verified",
  [E.NameRefused]:
    "The account alias save is refused definitively, or its receipt fails the local alias or account checks",
  [E.NameUncertain]: "The account alias save has an uncertain outcome",
  [E.Dispose]: "The task is left, Settings closes or the controller unmounts",
};
const yes = (value: boolean) => (value ? "yes" : "-");
const ruleText = (rule: GrokProblemRule, key?: string): string => {
  const name = key ? `\`${shortKey(key)}\`` : "";
  return {
    [GrokProblemRule.Keep]: "keep",
    [GrokProblemRule.Clear]: "clear",
    [GrokProblemRule.Set]: `set ${name}`,
    [GrokProblemRule.SetAdvisory]: `set ${name} unless a request is retained`,
    [GrokProblemRule.ClearAdvisory]: "clear unless a request is retained",
    [GrokProblemRule.ClearStatusOnly]: "clear only `statusUnconfirmed`",
    [GrokProblemRule.SetStatusUnlessSpecific]: `set ${name} unless another problem shows`,
  }[rule];
};
const retryText = (rule: GrokRetryRule, hold?: GrokRetry) =>
  rule === GrokRetryRule.Hold
    ? `hold the original ${hold} request`
    : rule === GrokRetryRule.Clear
      ? "drop the retained request"
      : "keep";

const browserOnlyPhases = [
  P.Opening,
  P.Waiting,
  P.WaitingNoBrowser,
  P.WaitingLinkOnly,
];
const devicePhases = [P.DeviceWaiting, P.DeviceOpened, P.DeviceAddress];
const methodOf = (phase: GrokLoginPhase) =>
  browserOnlyPhases.includes(phase)
    ? "browser"
    : devicePhases.includes(phase)
      ? "device code"
      : "both";
// The native binding of a phase, per method when the methods differ.
function nativeText(
  phase: GrokLoginPhase,
  allowed: GrokLoginMethod[] = methods,
) {
  const values = allowed.map((m) => grokNativeBinding(phase, m));
  return new Set(values).size === 1
    ? `\`${values[0]}\``
    : allowed
        .map(
          (m, index) =>
            `${m === GrokLoginMethod.Browser ? "browser" : "device"} \`${values[index]}\``,
        )
        .join(", ");
}
function nextNativeText(cell: NonNullable<ReturnType<typeof grokLoginCell>>) {
  const allowed = cell.method ? [cell.method] : methods;
  const issued = allowed.filter(
    (m) =>
      grokNativeBinding(cell.from, m) === GrokNativeBinding.Owed &&
      grokNativeBinding(cell.to, m) === GrokNativeBinding.Disposed,
  );
  const text = nativeText(cell.to, allowed);
  return issued.length
    ? `${text} (Dispose issued${issued.length < allowed.length ? ` for ${issued.map((m) => (m === GrokLoginMethod.Browser ? "browser" : "device")).join(", ")}` : ""})`
    : text;
}

function renderTables(): string {
  const out: string[] = [begin, ""];
  out.push(
    "#### Phases",
    "",
    "| Phase | Method | Stage | Native binding | Cancel | Open browser again | Copy link | Open verification page | Copy code | Copy address | Name field | Waiting guidance |",
    "| ----- | ------ | ----- | -------------- | ------ | ------------------ | --------- | ---------------------- | --------- | ------------ | ---------- | ---------------- |",
  );
  for (const phase of grokLoginPhases) {
    const spec = grokPhaseSpecs[phase];
    out.push(
      `| \`${phase}\` | ${methodOf(phase)} | ${spec.stage ? `\`${spec.stage}\`` : "-"} | ${nativeText(phase)} | ${yes(spec.cancel)} | ${yes(spec.openBrowser)} | ${yes(spec.copyLink)} | ${yes(spec.openVerification)} | ${yes(spec.copyCode)} | ${yes(spec.copyAddress)} | \`${spec.name}\` | \`${spec.help}\` |`,
    );
  }
  out.push(
    "",
    "Retry original request is shown exactly while a retry kind other than `none` is retained. While it is retained, Cancel, Open browser again and Open verification page are disabled and the naming field is locked (`locked` replaces `editable`). `owed` means a native Dispose is owed when the opening is left: a browser login owns its receiver for the whole opening, a device login only once Open verification page was requested. The device code and address are shown only in the phases that offer Copy code and Copy address and are cleared with them. A copy notice belongs to the value it copied (link, code or address): it is kept exactly while the current phase still offers that value and is cleared the moment it does not, even when the phase still offers another copy action.",
    "",
    "#### Events",
    "",
    "| Event | Method | Trigger |",
    "| ----- | ------ | ------- |",
  );
  for (const event of grokLoginEvents) {
    const method = new Set(
      grokLoginPhases.flatMap((phase) => {
        const cell = grokLoginCell(phase, event);
        return cell ? [cell.method ?? "both"] : [];
      }),
    );
    out.push(
      `| \`${event}\` | ${[...method].map((m) => (m === GrokLoginMethod.DeviceCode ? "device code" : m)).join(", ")} | ${eventTriggers[event]} |`,
    );
  }
  out.push(
    "",
    "#### Transitions",
    "",
    "Each table lists the events that change something in that phase. Every other event is ignored in that phase: the state, the retained request and the native binding stay unchanged (this covers late native results after a cancellation and any outcome that arrives after the login settled). The `opening` phase exists only inside one status-read tick, so a status read never meets it. `Native` shows the next phase's native binding; `Dispose issued` marks the transition that sends the native Dispose.",
    "",
  );
  for (const phase of grokLoginPhases) {
    const rows = grokLoginEvents.flatMap((event) => {
      const cell = grokLoginCell(phase, event);
      return cell ? [cell] : [];
    });
    out.push(`##### \`${phase}\``, "");
    if (!rows.length) {
      out.push("No event changes this phase.", "");
      continue;
    }
    out.push(
      "| Event | Next phase | Problem | Retained request | Native |",
      "| ----- | ---------- | ------- | ---------------- | ------ |",
    );
    for (const cell of rows) {
      out.push(
        `| \`${cell.event}\` | \`${cell.to}\` | ${ruleText(cell.problem, cell.problemKey)} | ${retryText(cell.retry, cell.hold)} | ${nextNativeText(cell)} |`,
      );
    }
    out.push("");
  }
  out.push(
    "#### Problem notices",
    "",
    "| Notice | Set by | Retry button |",
    "| ------ | ------ | ------------ |",
  );
  const setBy = new Map<string, Set<string>>();
  for (const phase of grokLoginPhases)
    for (const event of grokLoginEvents) {
      const cell = grokLoginCell(phase, event);
      if (!cell?.problemKey) continue;
      setBy.set(
        cell.problemKey,
        (setBy.get(cell.problemKey) ?? new Set()).add(event),
      );
    }
  setBy.set(
    "grok-subscription.problem.desktopRequired",
    new Set(["start without the native control"]),
  );
  // The events that retain a request while setting the notice.
  const retains = (key: string) =>
    [...(setBy.get(key) ?? [])].filter((event) =>
      grokLoginPhases.some(
        (phase) =>
          grokLoginCell(phase, event as GrokLoginEvent)?.retry ===
            GrokRetryRule.Hold &&
          grokLoginCell(phase, event as GrokLoginEvent)?.problemKey === key,
      ),
    );
  for (const key of [...setBy.keys()].sort()) {
    const holders = retains(key);
    out.push(
      `| \`${shortKey(key)}\` | ${[...setBy.get(key)!].map((e) => `\`${e}\``).join(", ")} | ${holders.length ? `only after ${holders.map((e) => `\`${e}\``).join(", ")}` : "never"} |`,
    );
  }
  out.push(
    "",
    "#### Copy attempts",
    "",
    "Copy link, Copy code and Copy address start an attempt with a new identity, and only the latest attempt may show a notice. A completion or notice timer names the attempt it belongs to; one that names an earlier attempt is ignored. An attempt ends (`retired`) the moment the current phase stops offering its value or the task is disposed, so a value that was hidden and offered again never receives the completion of the earlier attempt, and two writes that settle out of order show only the latest. Starting an attempt removes the previous notice. Events not listed for a copy state leave it unchanged.",
    "",
    "| Copy state | Event | Attempt named | Next copy state |",
    "| ---------- | ----- | ------------- | --------------- |",
  );
  for (const cell of grokCopyCells)
    out.push(
      `| \`${cell.from}\` | \`${cell.event}\` | ${cell.relation} | \`${cell.to}\` |`,
    );
  out.push("", end);
  return out.join("\n");
}

// The repository formatter pads Markdown tables, so compare cell contents only.
const squeeze = (text: string) =>
  text
    .split("\n")
    .map((line) =>
      line.startsWith("|")
        ? line
            .split("|")
            .map((cell) => cell.trim())
            .map((cell) => (/^:?-+:?$/.test(cell) ? "---" : cell))
            .join("|")
        : line,
    )
    .join("\n");

describe("copy attempts", () => {
  const phases = Object.values(GrokCopyPhase);
  const targets = Object.values(GrokCopyTarget);
  const stateOf = (phase: GrokCopyPhase, attempt = 3): GrokCopyState => ({
    phase,
    target: phase === GrokCopyPhase.Idle ? undefined : GrokCopyTarget.Code,
    attempt,
  });
  it("lists every copy state, event and attempt relation exactly once", () => {
    const relations = (event: GrokCopyEvent) =>
      [
        GrokCopyEvent.Succeeded,
        GrokCopyEvent.Rejected,
        GrokCopyEvent.NoticeExpired,
      ].includes(event)
        ? 2
        : 1;
    expect(grokCopyCells).toHaveLength(
      phases.length *
        grokCopyEvents.reduce((sum, event) => sum + relations(event), 0),
    );
    expect(
      new Set(grokCopyCells.map((c) => `${c.from}|${c.event}|${c.relation}`))
        .size,
    ).toBe(grokCopyCells.length);
  });
  // One test per table cell, derived from the table.
  for (const cell of grokCopyCells)
    it(`${cell.from} + ${cell.event} (${cell.relation}) -> ${cell.to}`, () => {
      const state = stateOf(cell.from);
      const attempt =
        cell.relation === GrokCopyAttemptRelation.Unnamed
          ? undefined
          : cell.relation === GrokCopyAttemptRelation.Latest
            ? state.attempt
            : state.attempt - 1;
      const next = grokCopyTransition(state, cell.event, {
        attempt,
        target: GrokCopyTarget.Address,
      });
      expect(next.phase).toBe(cell.to);
      if (cell.event === GrokCopyEvent.Started) {
        // A new identity supersedes every earlier attempt.
        expect(next.attempt).toBe(state.attempt + 1);
        expect(next.target).toBe(GrokCopyTarget.Address);
      } else {
        expect(next.attempt).toBe(state.attempt);
        // An ignored event returns the very same state.
        if (cell.to === cell.from) expect(next).toBe(state);
        expect(next.target).toBe(
          cell.to === GrokCopyPhase.Idle ? undefined : state.target,
        );
      }
      // Only a latest, still pending attempt may show a notice.
      const shows = [GrokCopyPhase.Copied, GrokCopyPhase.Failed];
      if (shows.includes(cell.to) && !shows.includes(cell.from)) {
        expect(cell.from).toBe(GrokCopyPhase.Pending);
        expect(cell.relation).toBe(GrokCopyAttemptRelation.Latest);
      }
    });
  it("retires the latest attempt exactly when its own value stops being offered", () => {
    for (const method of methods)
      for (const phase of grokLoginPhases)
        for (const target of targets)
          for (const copyPhase of phases) {
            const state: GrokCopyState = {
              ...stateOf(copyPhase),
              target: copyPhase === GrokCopyPhase.Idle ? undefined : target,
            };
            const ui = grokLoginPresentation({
              method,
              phase,
              retry: GrokRetry.None,
            });
            const after = grokCopyAfterPhase(state, ui);
            if (copyPhase === GrokCopyPhase.Idle || grokCopyOffered(ui, target))
              expect(after).toBe(state);
            else expect(after.phase).toBe(GrokCopyPhase.Idle);
          }
  });
  it("ignores a completion that outlives its value hidden and offered again", () => {
    // Waiting for the address after a failed open; the device code stays.
    const waiting = {
      method: GrokLoginMethod.DeviceCode,
      retry: GrokRetry.None,
    };
    const address = grokLoginPresentation({
      ...waiting,
      phase: P.DeviceAddress,
    });
    const opened = grokLoginPresentation({ ...waiting, phase: P.DeviceOpened });
    let copy = grokCopyTransition(initialGrokCopyState, GrokCopyEvent.Started, {
      target: GrokCopyTarget.Address,
    });
    const first = copy.attempt;
    copy = grokCopyAfterPhase(copy, opened);
    expect(copy.phase).toBe(GrokCopyPhase.Idle);
    copy = grokCopyAfterPhase(copy, address);
    expect(
      grokCopyTransition(copy, GrokCopyEvent.Succeeded, { attempt: first }),
    ).toBe(copy);
    expect(
      grokCopyTransition(copy, GrokCopyEvent.Rejected, { attempt: first }),
    ).toBe(copy);
    // A new attempt after the value is offered again works normally.
    copy = grokCopyTransition(copy, GrokCopyEvent.Started, {
      target: GrokCopyTarget.Address,
    });
    expect(
      grokCopyTransition(copy, GrokCopyEvent.Succeeded, { attempt: first }),
    ).toBe(copy);
    expect(
      grokCopyTransition(copy, GrokCopyEvent.Succeeded, {
        attempt: copy.attempt,
      }).phase,
    ).toBe(GrokCopyPhase.Copied);
  });
  it("shows only the latest of two attempts that settle out of order", () => {
    for (const order of [
      [1, 2],
      [2, 1],
    ]) {
      let copy = grokCopyTransition(
        initialGrokCopyState,
        GrokCopyEvent.Started,
        { target: GrokCopyTarget.Link },
      );
      copy = grokCopyTransition(copy, GrokCopyEvent.Started, {
        target: GrokCopyTarget.Code,
      });
      for (const attempt of order)
        copy = grokCopyTransition(copy, GrokCopyEvent.Succeeded, { attempt });
      expect(copy.phase).toBe(GrokCopyPhase.Copied);
      expect(copy.target).toBe(GrokCopyTarget.Code);
      expect(copy.attempt).toBe(2);
    }
  });
  it("lets a replaced notice's timer neither clear nor outlive the newer notice", () => {
    let copy = grokCopyTransition(initialGrokCopyState, GrokCopyEvent.Started, {
      target: GrokCopyTarget.Link,
    });
    copy = grokCopyTransition(copy, GrokCopyEvent.Succeeded, { attempt: 1 });
    copy = grokCopyTransition(copy, GrokCopyEvent.Started, {
      target: GrokCopyTarget.Code,
    });
    copy = grokCopyTransition(copy, GrokCopyEvent.Rejected, { attempt: 2 });
    expect(
      grokCopyTransition(copy, GrokCopyEvent.NoticeExpired, { attempt: 1 }),
    ).toBe(copy);
    expect(
      grokCopyTransition(copy, GrokCopyEvent.NoticeExpired, { attempt: 2 })
        .phase,
    ).toBe(GrokCopyPhase.Idle);
  });
});

describe("contract document", () => {
  it("renders the transition tables of the Grok subscription contract", () => {
    const document = readFileSync(docsPath, "utf8");
    const rendered = renderTables();
    if (process.env.UPDATE_GROK_LOGIN_DOCS === "1") {
      const start = document.indexOf(begin),
        stop = document.indexOf(end);
      if (start < 0 || stop < start) throw new Error("Missing table markers");
      writeFileSync(
        docsPath,
        document.slice(0, start) + rendered + document.slice(stop + end.length),
      );
      return;
    }
    const start = document.indexOf(begin),
      stop = document.indexOf(end);
    expect(start).toBeGreaterThanOrEqual(0);
    expect(squeeze(document.slice(start, stop + end.length))).toBe(
      squeeze(rendered),
    );
  });
  it("lists the help kinds the phases use", () => {
    expect(new Set(Object.values(grokPhaseSpecs).map((s) => s.help))).toEqual(
      new Set(Object.values(GrokHelp)),
    );
  });
});
