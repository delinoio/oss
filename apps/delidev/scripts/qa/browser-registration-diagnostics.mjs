// SPDX-License-Identifier: Apache-2.0
import { AssertionError } from "node:assert";

export const RegistrationStep = Object.freeze({
  GitFixture: "git-fixture",
  OpenRepositories: "open-repositories",
  OpenDialog: "open-dialog",
  FillURL: "fill-url",
  OpenLocalFolder: "open-local-folder",
  NativePickerDisabled: "native-picker-disabled",
  OpenManualPath: "open-manual-path",
  FillPath: "fill-path",
  Inspect: "inspect",
  ObserveInspection: "observe-inspection",
  Save: "save",
  ObserveResource: "observe-resource",
  ObserveRow: "observe-row",
});
const steps = new Set(Object.values(RegistrationStep));

// Playwright errors can embed selectors, private paths and renderer contents.
// Retain only the closed action and a known constructor class. Rethrow the
// original error so this evidence never turns a failed assertion into success.
export function registrationDiagnostics(TimeoutError) {
  const failures = new Map();
  return {
    async step(environment, substage, operation) {
      if (![1, 2].includes(environment) || !steps.has(substage)) throw new TypeError("Invalid registration diagnostic boundary");
      try { return await operation(); }
      catch (error) {
        if (!failures.has(environment)) failures.set(environment, {
          environment, substage,
          errorClass: typeof TimeoutError === "function" && error instanceof TimeoutError ? "timeout" : error instanceof AssertionError ? "assertion" : "unknown",
        });
        throw error;
      }
    },
    records: () => [...failures.values()].sort((left, right) => left.environment - right.environment).map(value => ({ ...value })),
  };
}
