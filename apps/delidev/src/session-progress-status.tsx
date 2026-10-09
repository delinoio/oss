// SPDX-License-Identifier: Apache-2.0
import { copy, useLocale, type MessageKey } from "./localization";
import { SessionProgressPhase } from "./session-progress";
import "./session-progress.css";
const labels: Record<SessionProgressPhase, MessageKey> = {
  [SessionProgressPhase.Preparing]: "session-progress.preparing",
  [SessionProgressPhase.Queued]: "session-progress.queued",
  [SessionProgressPhase.Starting]: "session-progress.starting",
  [SessionProgressPhase.Response]: "session-progress.response",
};
export function SessionProgressStatus({ phase, compact }: { phase: SessionProgressPhase; compact: boolean }) {
  useLocale();
  return <div className={`session-progress${compact ? " is-compact" : ""}`} role="status" aria-live="polite" aria-atomic="true"><span className="session-progress-spinner" aria-hidden="true" /><span>{copy(labels[phase])}</span></div>;
}
