// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen } from "@testing-library/react";
import { newRequestId } from "@delinoio/delidev-api-client";
import { afterEach, expect, it } from "vitest";
import { canRetryExecutionStartup, executionStartupFailure, ExecutionStartupDetails, startupRecoveryGuidance } from "./execution-startup";
import { copy, i18n } from "./localization";
import type { Document } from "./documents";

afterEach(() => { void i18n.changeLanguage("en"); });
for (const language of ["en", "ko"]) for (const delivery of [1, 2, 3, 4]) for (const cleanup of [1, 2]) {
 it(`${language} preserves recovery and independent delivery ${delivery}/cleanup ${cleanup}`, async () => {
  await i18n.changeLanguage(language);
  const job = newRequestId(), execution = newRequestId();
  const failure = { state: 3, phase: 6, harness: "codex", problem_code: "unsupported", native_version: "0.151.0", correlation_id: job, input_delivery: delivery, cleanup };
  const session: Document = { archive: "active", dispatch: "paused", recovery: "required", pending_inputs: 0, pending_input_bytes: 0, initial_execution: { id: execution }, startup: { job_id: job, execution_id: execution, failure } };
  const before = JSON.stringify(session);
  expect(executionStartupFailure(session)).toBe(failure);
  expect(canRetryExecutionStartup(session)).toBe(false);
  // Even otherwise settled session metadata cannot promote an uncertain outcome.
  expect(canRetryExecutionStartup({ ...session, recovery: "none" })).toBe(false);
  render(<ExecutionStartupDetails failure={failure} />);
  fireEvent.click(screen.getByText(copy("session.startupDetails")));
  const guidance = startupRecoveryGuidance(failure);
  expect(screen.getByText(guidance)).toBeTruthy();
  expect(guidance.includes(copy("session.startupDeliveryUncertain"))).toBe(delivery === 2 || delivery === 4);
  expect(guidance.includes(copy("session.startupCleanupUncertain"))).toBe(cleanup === 2);
  expect(guidance).toContain(copy("session.startupRecoverOriginal"));
  if (delivery === 3 && cleanup === 1) expect(guidance).toContain(copy("session.startupSettings"));
  expect(screen.queryByRole("button", { name: /retry|send|재시도|보내기/i })).toBeNull();
  expect(JSON.stringify(session)).toBe(before);
 });
}
