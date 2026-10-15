// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { expect, it } from "vitest";
import {
  GrokDiagnosticPhase,
  GrokDiagnosticSchema,
  SubscriptionLoginState as State,
  SubscriptionServiceId,
} from "@delinoio/delidev-api-client";
import {
  ProgressDisposition as Disposition,
  drivesBrowserLogin,
  progressDisposition,
} from "./subscription-progress";

const diagnostic = create(GrokDiagnosticSchema, {
  detectedVersion: "1.0.43",
  supportedVersion: "1.0.46",
  phase: GrokDiagnosticPhase.VERSION,
  code: "unsupported",
});

// One row per progress state and diagnostic presence. The same rows appear in
// "Client classification of a progress response" in
// docs/cmds-delidev-grok-subscription-contract.md; the Go server rows are
// asserted by TestGrokProgressViewClassifiesEveryOperationState
// in cmds/delidev-cli/internal/server.
it.each([
  ["preparing", State.PREPARING, false, Disposition.Active],
  ["waiting", State.WAITING, false, Disposition.Active],
  ["succeeded", State.SUCCEEDED, false, Disposition.Success],
  ["canceled", State.CANCELED, false, Disposition.Terminal],
  ["expired", State.EXPIRED, false, Disposition.Terminal],
  ["recovery required", State.RECOVERY_REQUIRED, false, Disposition.Terminal],
  ["failed", State.FAILED, false, Disposition.Terminal],
  ["failed with a Grok diagnostic", State.FAILED, true, Disposition.Terminal],
  ["unsupported", State.UNSUPPORTED, false, Disposition.Terminal],
  [
    "unsupported with a Grok diagnostic",
    State.UNSUPPORTED,
    true,
    Disposition.Provisional,
  ],
] as const)("classifies a %s response", (_name, state, grok, want) => {
  expect(progressDisposition(state, grok ? diagnostic : undefined)).toBe(want);
});

it("rejects a state outside the closed set", () => {
  expect(progressDisposition(State.UNSPECIFIED)).toBeUndefined();
  expect(progressDisposition(99 as State, diagnostic)).toBeUndefined();
});

it.each([
  [SubscriptionServiceId.ChatGPT, true],
  [SubscriptionServiceId.Claude, false],
  [SubscriptionServiceId.Grok, false],
] as const)("service %s drives the browser sign-in: %s", (service, want) => {
  expect(drivesBrowserLogin(service)).toBe(want);
});
