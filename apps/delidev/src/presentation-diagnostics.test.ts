import { afterEach, expect, it, vi } from "vitest";
import {
  PresentationFailureLog,
  PresentationOutcome,
  PresentationPhase,
} from "./presentation-diagnostics";

afterEach(() => vi.restoreAllMocks());

it.each(Object.values(PresentationPhase))(
  "warns once per failing %s phase with only a stable phase and outcome",
  (phase) => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const info = vi.spyOn(console, "info").mockImplementation(() => {});
    const log = new PresentationFailureLog();
    log.failed(phase);
    log.failed(phase);
    log.report(phase, true);
    expect(warn).toHaveBeenCalledTimes(1);
    expect(warn).toHaveBeenCalledWith("delidev.presentation.outcome", {
      phase,
      outcome: PresentationOutcome.Failed,
    });
    log.recovered(phase);
    log.recovered(phase);
    expect(info).toHaveBeenCalledTimes(1);
    expect(info).toHaveBeenCalledWith("delidev.presentation.outcome", {
      phase,
      outcome: PresentationOutcome.Recovered,
    });
    // A later failure warns again after a recovery.
    log.report(phase, true);
    expect(warn).toHaveBeenCalledTimes(2);
  },
);

it("tracks phases independently and never records a recovery that did not fail", () => {
  const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  const info = vi.spyOn(console, "info").mockImplementation(() => {});
  const log = new PresentationFailureLog();
  log.recovered(PresentationPhase.TrayBegin);
  log.report(PresentationPhase.TrayPublish, false);
  expect(info).not.toHaveBeenCalled();
  log.failed(PresentationPhase.TrayBegin);
  log.failed(PresentationPhase.TrayPublish);
  expect(warn).toHaveBeenCalledTimes(2);
});
