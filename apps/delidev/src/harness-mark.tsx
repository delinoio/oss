// SPDX-License-Identifier: Apache-2.0
import { Harness } from "./configuration-fields";
import "./harness-mark.css";

// Only the saved top-level enum identifies a mark. Names, routes and account
// metadata cannot establish harness identity or execution eligibility.
export function knownHarness(value: unknown): Harness | undefined {
  return Object.values(Harness).find(harness => harness === value);
}

export function HarnessMark({ harness }: { harness?: Harness }) {
  return harness ? <span className={`worker-harness-mark worker-harness-mark-${harness}`} data-harness={harness} aria-hidden="true" /> : null;
}
