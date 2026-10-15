import { afterEach, expect, it, vi } from "vitest";
import {
  PresentationEventKind,
  PresentationFailureLog,
  PresentationOutcome,
  PresentationPhase,
  initialPresentationState,
  reducePresentation,
  type PresentationEvent,
  type PresentationRecord,
  type PresentationState,
} from "./presentation-diagnostics";

afterEach(() => vi.restoreAllMocks());

const phases = Object.values(PresentationPhase);
const [first, second] = [
  PresentationPhase.TrayBegin,
  PresentationPhase.TrayPublish,
];
const fail = (phase: PresentationPhase): PresentationEvent => ({
  kind: PresentationEventKind.Failure,
  phase,
});
const succeed = (phase: PresentationPhase): PresentationEvent => ({
  kind: PresentationEventKind.Success,
  phase,
});
const dispose: PresentationEvent = { kind: PresentationEventKind.Disposal };
const failed = (phase: PresentationPhase): PresentationRecord => ({
  phase,
  outcome: PresentationOutcome.Failed,
});
const recovered = (phase: PresentationPhase): PresentationRecord => ({
  phase,
  outcome: PresentationOutcome.Recovered,
});

function run(events: readonly PresentationEvent[]) {
  let state: PresentationState = initialPresentationState;
  const records: PresentationRecord[] = [];
  for (const event of events) {
    const step = reducePresentation(state, event);
    state = step.state;
    if (step.record) records.push(step.record);
  }
  return { state, records };
}

// Every row is one cell of the reducer table in the desktop contract: the
// events applied to a fresh log, the records they produce and the phases that
// remain failing.
const cells: ReadonlyArray<{
  name: string;
  events: (p: PresentationPhase, q: PresentationPhase) => PresentationEvent[];
  records: (p: PresentationPhase, q: PresentationPhase) => PresentationRecord[];
  failing: (p: PresentationPhase, q: PresentationPhase) => PresentationPhase[];
}> = [
  {
    name: "healthy phase, success: no record",
    events: (p) => [succeed(p)],
    records: () => [],
    failing: () => [],
  },
  {
    name: "healthy phase, failure: one warning and the phase is failing",
    events: (p) => [fail(p)],
    records: (p) => [failed(p)],
    failing: (p) => [p],
  },
  {
    name: "failing phase, repeated failure: no further record",
    events: (p) => [fail(p), fail(p), fail(p)],
    records: (p) => [failed(p)],
    failing: (p) => [p],
  },
  {
    name: "failing phase, success: one recovery and the phase is healthy",
    events: (p) => [fail(p), succeed(p)],
    records: (p) => [failed(p), recovered(p)],
    failing: () => [],
  },
  {
    name: "recovered phase, repeated success: no further record",
    events: (p) => [fail(p), succeed(p), succeed(p)],
    records: (p) => [failed(p), recovered(p)],
    failing: () => [],
  },
  {
    name: "recovered phase, failure: warns again",
    events: (p) => [fail(p), succeed(p), fail(p)],
    records: (p) => [failed(p), recovered(p), failed(p)],
    failing: (p) => [p],
  },
  {
    name: "two phases failing in one batch: each records its own warning",
    events: (p, q) => [fail(p), fail(q)],
    records: (p, q) => [failed(p), failed(q)],
    failing: (p, q) => [p, q],
  },
  {
    name: "success of another phase never recovers a failing phase",
    events: (p, q) => [fail(p), succeed(q)],
    records: (p) => [failed(p)],
    failing: (p) => [p],
  },
  {
    name: "recovering one of two failing phases keeps the other failing",
    events: (p, q) => [fail(p), fail(q), succeed(p)],
    records: (p, q) => [failed(p), failed(q), recovered(p)],
    failing: (_, q) => [q],
  },
  {
    name: "healthy phase, disposal: later events record nothing",
    events: (p) => [dispose, fail(p), succeed(p)],
    records: () => [],
    failing: () => [],
  },
  {
    name: "failing phase, disposal: no recovery record and later events record nothing",
    events: (p) => [fail(p), dispose, succeed(p), fail(p)],
    records: (p) => [failed(p)],
    failing: () => [],
  },
  {
    name: "repeated disposal: no record",
    events: (p) => [fail(p), dispose, dispose],
    records: (p) => [failed(p)],
    failing: () => [],
  },
];

for (const phase of phases)
  it.each(cells)(`$name (${phase})`, ({ events, records, failing }) => {
    const other = phases.find((value) => value !== phase) ?? first;
    const result = run(events(phase, other));
    expect(result.records).toEqual(records(phase, other));
    expect(result.state.failing).toEqual(failing(phase, other));
  });

it("replacement: a new log starts healthy and reports its own first failure", () => {
  const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  const old = new PresentationFailureLog();
  old.failed(first);
  old.dispose();
  const replacement = new PresentationFailureLog();
  replacement.failed(first);
  replacement.failed(first);
  expect(warn).toHaveBeenCalledTimes(2);
});

it("reducer events never mutate the previous state", () => {
  const before = run([fail(first)]).state;
  const snapshot = JSON.stringify(before);
  reducePresentation(before, fail(second));
  reducePresentation(before, succeed(first));
  reducePresentation(before, dispose);
  expect(JSON.stringify(before)).toBe(snapshot);
});

it.each(phases)(
  "the log writes %s records with only a stable phase and outcome",
  (phase) => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const info = vi.spyOn(console, "info").mockImplementation(() => {});
    const log = new PresentationFailureLog();
    log.report(phase, true);
    log.report(phase, true);
    expect(warn).toHaveBeenCalledTimes(1);
    expect(warn).toHaveBeenCalledWith("delidev.presentation.outcome", {
      phase,
      outcome: PresentationOutcome.Failed,
    });
    log.report(phase, false);
    log.recovered(phase);
    expect(info).toHaveBeenCalledTimes(1);
    expect(info).toHaveBeenCalledWith("delidev.presentation.outcome", {
      phase,
      outcome: PresentationOutcome.Recovered,
    });
    log.dispose();
    log.failed(phase);
    expect(warn).toHaveBeenCalledTimes(1);
  },
);
