// SPDX-License-Identifier: Apache-2.0
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import {
  GrokHelp,
  GrokLoginEvent,
  GrokLoginPhase,
  GrokLoginStage,
  GrokNameField,
  GrokNativeBinding,
  GrokProblemRule,
  GrokRetry,
  GrokRetryRule,
  grokLoginCell,
  grokLoginEvents,
  grokLoginPhases,
  grokLoginPresentation,
  grokPhaseSpecs,
  grokPollEvent,
  grokRetryOutcomes,
  initialGrokLoginState,
  statusProblem,
  transition,
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

// A representative state of every phase, with and without a retained request.
function statesOf(phase: GrokLoginPhase): GrokLoginState[] {
  const states: GrokLoginState[] = [{ phase, retry: GrokRetry.None }];
  for (const kind of retryKinds)
    if (retryHomes[kind].phases.includes(phase))
      states.push({
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
const nativeRank = {
  [GrokNativeBinding.None]: 0,
  [GrokNativeBinding.Requested]: 1,
  [GrokNativeBinding.Disposed]: 2,
};

const pairs = grokLoginPhases.flatMap((phase) =>
  grokLoginEvents.map((event) => [phase, event] as const),
);

describe("every phase and event cell", () => {
  it("covers every phase and event pair", () => {
    expect(pairs).toHaveLength(grokLoginPhases.length * grokLoginEvents.length);
    expect(new Set(grokLoginPhases).size).toBe(grokLoginPhases.length);
  });
  it.each(pairs)("%s x %s", (phase, event) => {
    const cell = grokLoginCell(phase, event);
    for (const state of statesOf(phase)) {
      const next = transition(state, event);
      if (!cell) {
        // An ignored event never changes anything, including a late result.
        expect(next).toBe(state);
        continue;
      }
      expect(cell.from).toBe(phase);
      expect(cell.event).toBe(event);
      expect(next.phase).toBe(cell.to);
      expect(next.problem).toBe(
        expectedProblem(state, cell.problem, cell.problemKey),
      );
      expect(next.retry).toBe(expectedRetry(state, cell.retry, cell.hold));
      // The native binding only moves forward and Disposed is absorbing.
      expect(
        nativeRank[grokPhaseSpecs[next.phase].native],
      ).toBeGreaterThanOrEqual(nativeRank[grokPhaseSpecs[phase].native]);
    }
  });
});

describe("table invariants", () => {
  // Every state reachable from either start state through any event sequence.
  const reachable = (() => {
    const seen = new Map<string, GrokLoginState>();
    const id = (s: GrokLoginState) => `${s.phase}|${s.problem}|${s.retry}`;
    const queue = [initialGrokLoginState(true), initialGrokLoginState(false)];
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
  it.each(reachable.map((s) => [s.phase, s.problem ?? "-", s.retry, s]))(
    "reachable %s / %s / %s is consistent",
    (_phase, _problem, _retry, state) => {
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
        expect([ui.cancel, ui.openBrowser, ui.copyLink]).toEqual([
          false,
          false,
          false,
        ]);
        expect(ui.native).not.toBe(GrokNativeBinding.Requested);
      }
      if (ui.canceling)
        expect(s.problem).toBe("grok-subscription.problem.canceling");
      // Open browser again requires a browser the receiver can still open.
      if (ui.openBrowser) expect(ui.copyLink).toBe(true);
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
  it("treats a provisional refusal as a status read that settles nothing", () => {
    for (const phase of grokLoginPhases) {
      const settled = grokLoginPresentation({ phase, retry: GrokRetry.None });
      const observed = grokLoginCell(phase, E.PollRefusalProvisional);
      if (!observed) {
        // Settled, unobservable and not yet started phases ignore it.
        expect(
          transition({ phase, retry: GrokRetry.None }, E.PollRefusalProvisional)
            .phase,
        ).toBe(phase);
        continue;
      }
      expect(settled.settled).toBe(false);
      // The phase, the native binding and any retained request stay as they
      // were; only the polling notice recovers.
      expect(observed.to).toBe(phase);
      expect(observed.retry).toBe(GrokRetryRule.Keep);
      expect(observed.problem).toBe(GrokProblemRule.ClearStatusOnly);
      for (const kind of retryKinds) {
        const state: GrokLoginState = {
          phase,
          retry: kind,
          problem: retryHomes[kind].problem as GrokLoginState["problem"],
        };
        if (retryHomes[kind].phases.includes(phase))
          expect(transition(state, E.PollRefusalProvisional)).toEqual(state);
      }
    }
    // The refusal keeps the original sign-in cancelable and adopts the next
    // response like any other poll.
    const preparing: GrokLoginState = { phase: P.Preparing, retry: GrokRetry.None };
    const refused = transition(preparing, E.PollRefusalProvisional);
    expect(refused).toEqual(preparing);
    expect(grokLoginPresentation(refused).cancel).toBe(true);
    expect(transition(refused, E.PollFailed).phase).toBe(P.Failed);
    expect(transition(refused, E.PollSucceeded).phase).toBe(P.Naming);
    expect(transition(refused, E.PollUnsupported).phase).toBe(P.Unsupported);
  });
  it("marks Disposed as absorbing", () => {
    for (const event of grokLoginEvents)
      expect(
        transition({ phase: P.Disposed, retry: GrokRetry.None }, event).phase,
      ).toBe(P.Disposed);
  });
  it("settles each retained mutation through its own notice", () => {
    for (const kind of retryKinds) {
      const outcomes = grokRetryOutcomes[kind];
      const home = retryHomes[kind].phases[0];
      const uncertain = transition(
        { phase: home, retry: GrokRetry.None },
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

function renderTables(): string {
  const out: string[] = [begin, ""];
  out.push(
    "#### Phases",
    "",
    "| Phase | Stage | Native binding | Cancel | Open browser again | Copy link | Name field | Waiting guidance |",
    "| ----- | ----- | -------------- | ------ | ------------------ | --------- | ---------- | ---------------- |",
  );
  for (const phase of grokLoginPhases) {
    const spec = grokPhaseSpecs[phase];
    out.push(
      `| \`${phase}\` | ${spec.stage ? `\`${spec.stage}\`` : "-"} | \`${spec.native}\` | ${yes(spec.cancel)} | ${yes(spec.openBrowser)} | ${yes(spec.copyLink)} | \`${spec.name}\` | \`${spec.help}\` |`,
    );
  }
  out.push(
    "",
    "Retry original request is shown exactly while a retry kind other than `none` is retained. While it is retained, Cancel and Open browser again are disabled and the naming field is locked (`locked` replaces `editable`).",
    "",
    "#### Events",
    "",
    "| Event | Trigger |",
    "| ----- | ------- |",
  );
  for (const event of grokLoginEvents)
    out.push(`| \`${event}\` | ${eventTriggers[event]} |`);
  out.push(
    "",
    "#### Transitions",
    "",
    "Each table lists the events that change something in that phase. Every other event is ignored in that phase: the state, the retained request and the native binding stay unchanged (this covers late native results after a cancellation and any outcome that arrives after the login settled). The `opening` phase exists only inside one status-read tick, so a status read never meets it. `Native` shows the next phase's native binding; `disposed (issued)` marks the transition that sends the native Dispose.",
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
      const before = grokPhaseSpecs[cell.from].native;
      const after = grokPhaseSpecs[cell.to].native;
      out.push(
        `| \`${cell.event}\` | \`${cell.to}\` | ${ruleText(cell.problem, cell.problemKey)} | ${retryText(cell.retry, cell.hold)} | \`${after}\`${before !== GrokNativeBinding.Disposed && after === GrokNativeBinding.Disposed ? " (issued)" : ""} |`,
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
