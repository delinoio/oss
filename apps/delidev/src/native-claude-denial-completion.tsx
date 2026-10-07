import { copy, useLocale } from "./localization";
import { object, type Document } from "./documents";

const nativeID = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const id = (v: unknown) => nativeID(v) && v[14] === "7";
const exact = (v: Document, fields: string[]) => Object.keys(v).length === fields.length && fields.every((key) => Object.hasOwn(v, key));

function valid(progress: Document) {
  const v = object(progress.claude_denial), prior = object(progress.claude_interruption);
  const references = [v.input_id, v.interaction_id, v.arrival_id, v.context_id, v.result_id, progress.execution_id, progress.native_thread_id];
  const envelopes = [v.command_native_id, v.idle_native_id, progress.native_turn_id];
  return exact(v, ["input_id", "interaction_id", "arrival_id", "context_id", "result_id", "command_native_id", "idle_native_id", "native_input_id", "cleanup_verified"]) && exact(prior, ["interaction_id", "context_id", "result_id"]) && references.every(id) && envelopes.every(nativeID) && new Set([...references, ...envelopes]).size === references.length + envelopes.length && v.input_id === progress.input_id && v.interaction_id === prior.interaction_id && v.context_id === prior.context_id && v.result_id === prior.result_id && v.native_input_id === null && typeof v.cleanup_verified === "boolean" && progress.outcome === "stopped" && progress.claude_terminal == null && progress.claude_stop == null && (progress.cleanup_verified === undefined || typeof progress.cleanup_verified === "boolean");
}

export function NativeClaudeDenialCompletion({ progress }: { progress: Document }) {
  useLocale();
  if (progress.claude_denial == null) return null;
  if (!valid(progress)) return <p>{copy("native-claude-denial-completion.theRetainedClaudeDenialCleanupIs_81b06b")}</p>;
  return <section aria-label={copy("native-claude-denial-completion.claudeOriginalDenialCleanup_b0c03b")}>
    <h4>{copy("native-claude-denial-completion.claudeStoppedAfterDenial_2ef315")}</h4>
    <p>{copy("native-claude-denial-completion.theOriginalDenialWasProcessedAnd_2d5a72")}</p>
    <dl><dt>{copy("native-claude-denial-completion.nativeLoop_fec242")}</dt><dd>{copy("native-claude-denial-completion.idleObserved_34b00d")}</dd><dt>{copy("native-claude-denial-completion.ownedNativeProcessCleanup_05bea7")}</dt><dd>{object(progress.claude_denial).cleanup_verified === true ? copy("native-claude-denial-completion.confirmed_fe00b6") : copy("native-claude-denial-completion.notConfirmed_bc1c29")}</dd>
      <dt>{copy("native-claude-denial-completion.workerWorkspaceCleanupReport_e52033")}</dt><dd>{progress.cleanup_verified === true ? copy("native-claude-denial-completion.confirmed_fe00b6") : copy("native-claude-denial-completion.notConfirmed_bc1c29")}</dd></dl>
    <p>{copy("native-claude-denial-completion.historyReconciliationIsStillRequiredBefore_5f5801")}</p>
  </section>;
}
