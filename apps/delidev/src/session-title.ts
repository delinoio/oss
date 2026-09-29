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
  [SessionTitleState.Waiting]: "Title waits for the first completed turn",
  [SessionTitleState.Queued]: "Title queued",
  [SessionTitleState.Running]: "Generating title",
  [SessionTitleState.Succeeded]: "Title generated",
  [SessionTitleState.Skipped]: "Title skipped",
  [SessionTitleState.Failed]: "Title generation failed",
  [SessionTitleState.Unsupported]: "Title generation unsupported",
  [SessionTitleState.Uncertain]: "Title outcome uncertain",
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

export function sessionTitlePresentation(value: unknown): { label: string; detail?: string } | undefined {
  const data = object(value);
  if (data.name_mode !== "automatic") return undefined;
  const state = text(data.title_state);
  if (!Object.values(SessionTitleState).includes(state as SessionTitleState)) {
    return { label: state ? `Unknown title state (${state})` : "Title state unavailable" };
  }
  const reason = text(data.title_reason);
  return { label: labels[state as SessionTitleState], detail: reasons[reason] ?? (reason ? `Unknown title reason (${reason})` : undefined) };
}

export function sessionTitleLabel(value: Document): string | undefined {
  return sessionTitlePresentation(value)?.label;
}
