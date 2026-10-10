// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef } from "react";
import { FailureCause, FailureCode } from "@delinoio/delidev-api-client";
import { JobState } from "./jobs";
import { type Document } from "./documents";

// Only the verified original terminal job can release its retained name draft.
export function ConfigurationNameFailure({ state, verified, problem, confirmed }: { state: string; verified: boolean; problem: Document; confirmed: () => void }) {
  const callback = useRef(confirmed), settled = useRef(false);
  callback.current = confirmed;
  useEffect(() => {
    if (settled.current || !verified || state !== JobState.Failed || problem.code !== FailureCode.Conflict || problem.cause !== FailureCause.ConfigurationNameConflict) return;
    settled.current = true; callback.current();
  }, [state, verified, problem.code, problem.cause]);
  return null;
}
