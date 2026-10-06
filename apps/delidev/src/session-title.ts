import { copy } from "./localization";
import { object, text, type Document } from "./documents";

export enum SessionTitleState {
  Waiting = "waiting",
  Queued = "queued",
  Running = "running",
  Succeeded = "succeeded",
  Skipped = "skipped",
  Failed = "failed",
  Unsupported = "unsupported",
  Uncertain = "uncertain",
}

const labels: Record<SessionTitleState, string> = {
  get [SessionTitleState.Waiting]() { return copy("session-title.titleWaitsForTheFirstCompleted_11677c"); },
  get [SessionTitleState.Queued]() { return copy("session-title.titleQueued_b44555"); },
  get [SessionTitleState.Running]() { return copy("session-title.generatingTitle_d25441"); },
  get [SessionTitleState.Succeeded]() { return copy("session-title.titleGenerated_1e4690"); },
  get [SessionTitleState.Skipped]() { return copy("session-title.titleSkipped_958887"); },
  get [SessionTitleState.Failed]() { return copy("session-title.titleGenerationFailed_ffe5d8"); },
  get [SessionTitleState.Unsupported]() { return copy("session-title.titleGenerationUnsupported_419b31"); },
  get [SessionTitleState.Uncertain]() { return copy("session-title.titleOutcomeUncertain_3cc9af"); },
};

const reasons: Record<string, string> = {
  "unsupported-agent-profile": "The original Agent profile does not support title generation.",
  "worker-capability-absent": "The original Worker did not prove the required title capability.",
  "budget-reached": "The session budget did not allow another request.",
  canceled: "Title generation was canceled with the session operation.",
  "authority-lost": "The original account or session permission is no longer available.",
  "invalid-output": "The title result did not pass validation.",
  "inference-failed": "The title request failed; the conversation remains available.",
  "cleanup-uncertain": "Worker cleanup is uncertain; inspect the retained operation.",
  "manual-rename": "A manual rename now owns this title.",
};

const shortReasons: Record<string, string> = {
  "unsupported-agent-profile": "Agent profile",
  "worker-capability-absent": "Worker capability",
  "budget-reached": "Budget reached",
  canceled: "Canceled",
  "authority-lost": "Authority lost",
  "invalid-output": "Invalid output",
  "inference-failed": "Inference failed",
  "cleanup-uncertain": "Cleanup uncertain",
  "manual-rename": "Manual rename",
};

export function sessionTitlePresentation(value: unknown): { label: string; detail?: string; shortDetail?: string } | undefined {
  const data = object(value);
  if (data.name_mode !== "automatic") return undefined;
  const state = text(data.title_state);
  if (!Object.values(SessionTitleState).includes(state as SessionTitleState)) {
    return { label: state ? `Unknown title state (${state})` : "Title state unavailable" };
  }
  const reason = text(data.title_reason);
  return {
    label: labels[state as SessionTitleState],
    detail: reasons[reason] ?? (reason ? `Unknown title reason (${reason})` : undefined),
    shortDetail: reason ? shortReasons[reason] ?? "Other reason" : undefined,
  };
}

export function sessionTitleLabel(value: Document): string | undefined {
  return sessionTitlePresentation(value)?.label;
}
