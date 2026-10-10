// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen } from "@testing-library/react";
import { newRequestId } from "@delinoio/delidev-api-client";
import { afterEach, expect, it } from "vitest";
import { canRetryExecutionStartup, executionStartupFailure, ExecutionStartupDetails, startupCorrection, startupRecoveryGuidance } from "./execution-startup";
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


it.each(["en", "ko"])("%s directs image corrections to a fresh session without changing retry ownership", async language => {
  await i18n.changeLanguage(language);
  const job = newRequestId(), execution = newRequestId();
  const failure = { state: 2, phase: 5, harness: "codex", problem_code: "unsupported", correlation_id: job, input_delivery: 1, cleanup: 1, failure_kind: 1 };
  const session: Document = { archive: "active", dispatch: "paused", recovery: "none", pending_inputs: 0, pending_input_bytes: 0, initial_execution: { id: execution, input_id: newRequestId(), configuration: { agent_id: newRequestId() }, account_id: newRequestId(), machine_id: newRequestId() }, startup: { job_id: job, execution_id: execution, failure } };
  const before = JSON.stringify(session);
  const validated = executionStartupFailure(session)!;
  expect(validated).toBe(failure);
  const guidance = startupCorrection(validated);
  if (language === "en") {
    expect(guidance).toContain("Start a new session with an Agent Worker and Runner Device that explicitly support images");
    expect(guidance).toContain("start a new text-only session");
    expect(guidance).toContain("Retrying this session keeps its original input, images and selected Agent Worker and Runner Device");
  } else {
    expect(guidance).toContain("이미지를 명시적으로 지원하는 Agent Worker와 Runner Device로 새 세션을 시작");
    expect(guidance).toContain("텍스트만 사용하는 새 세션을 시작");
    expect(guidance).toContain("이 세션을 재시도하면 원래 입력과 이미지, 선택한 Agent Worker와 Runner Device가 유지");
  }
  expect(canRetryExecutionStartup(session)).toBe(true);
  expect(canRetryExecutionStartup({ ...session, pending_inputs: 1, pending_input_bytes: 12 })).toBe(false);
  expect(JSON.stringify(session)).toBe(before);
  const generic = { ...failure, failure_kind: 0 };
  expect(executionStartupFailure({ ...session, startup: { ...session.startup as Document, failure: generic } })).toBe(generic);
  expect(startupCorrection(generic)).toBe(copy("session.startupSettings"));
  expect(startupCorrection(generic)).not.toBe(guidance);
  for (const change of [{ cleanup: 2 }, { input_delivery: 2 }, { phase: 4 }, { failure_kind: "1" }]) {
    expect(executionStartupFailure({ ...session, startup: { ...session.startup as Document, failure: { ...failure, ...change } } })).toBeUndefined();
  }
});
