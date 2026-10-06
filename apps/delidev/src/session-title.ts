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
  get "unsupported-agent-profile"() { return copy("session-title.extra.b1cfcf6f1ba4"); },
  get "worker-capability-absent"() { return copy("session-title.extra.adb334a75949"); },
  get "budget-reached"() { return copy("session-title.extra.0a98a1052ba2"); },
  get canceled() { return copy("session-title.extra.473157bf8b8e"); },
  get "authority-lost"() { return copy("session-title.extra.550d15708cce"); },
  get "invalid-output"() { return copy("session-title.extra.fae8644abb79"); },
  get "inference-failed"() { return copy("session-title.extra.547cca3850e6"); },
  get "cleanup-uncertain"() { return copy("session-title.extra.a4dcd69e159f"); },
  get "manual-rename"() { return copy("session-title.extra.24ecf17871e5"); },
};

const shortReasons: Record<string, string> = {
  get "unsupported-agent-profile"() { return copy("session-title.extra.c00881f82856"); },
  get "worker-capability-absent"() { return copy("session-title.extra.ce8353ca4914"); },
  get "budget-reached"() { return copy("session-title.extra.691fb5a6027e"); },
  get canceled() { return copy("session-title.extra.13ca2ee24993"); },
  get "authority-lost"() { return copy("session-title.extra.45d7462719c5"); },
  get "invalid-output"() { return copy("session-title.extra.3f7bbf047330"); },
  get "inference-failed"() { return copy("session-title.extra.d7dec563b46e"); },
  get "cleanup-uncertain"() { return copy("session-title.extra.4e49f30cdabe"); },
  get "manual-rename"() { return copy("session-title.extra.a37a649ad48c"); },
};

export function sessionTitlePresentation(value: unknown): { label: string; detail?: string; shortDetail?: string } | undefined {
  const data = object(value);
  if (data.name_mode !== "automatic") return undefined;
  const state = text(data.title_state);
  if (!Object.values(SessionTitleState).includes(state as SessionTitleState)) {
    return { get label() { return state ? copy("session-title.sentence.adc6a2c6a5eb", { v0: state }) : copy("session-title.extra.17b90626003c"); } };
  }
  const reason = text(data.title_reason);
  return {
    get label() { return labels[state as SessionTitleState]; },
    get detail() { return reasons[reason] ?? (reason ? copy("session-title.sentence.0e3944f9aca4", { v0: reason }) : undefined); },
    get shortDetail() { return reason ? shortReasons[reason] ?? copy("session-title.extra.14a71f1899d6") : undefined; },
  };
}

export function sessionTitleLabel(value: Document): string | undefined {
  return sessionTitlePresentation(value)?.label;
}
