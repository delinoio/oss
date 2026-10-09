// SPDX-License-Identifier: Apache-2.0
import { copy } from "./localization";

// Native failure identifiers are closed; private sidecar text is never rendered.
export function localStartupProblem(failure?: string): string | undefined {
  switch (failure) {
    case "ownership-conflict": return copy("desktop.startupOwnershipConflict");
    case "startup-conflict": return copy("desktop.startupConflict");
    case "busy": return copy("desktop.aLocalConnectionAttemptIsAlready_2efbe3");
    case "incompatible": return copy("desktop.theRunningServerUsesADifferent_327644");
    default: return undefined;
  }
}
