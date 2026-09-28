import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";

const uuid = (value: unknown) => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
const digest = (value: unknown) => typeof value === "string" && /^[0-9a-f]{64}$/.test(value);
const exact = (value: Document, keys: string[]) => Object.keys(value).length === keys.length && keys.every((key) => Object.hasOwn(value, key));
// Keep nanoseconds when comparing Go RFC3339Nano timestamps. Date.parse alone
// rounds them to milliseconds and also normalizes invalid calendar dates.
function timestamp(value: unknown): string | undefined {
  if (typeof value !== "string") return;
  const match = /^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?Z$/.exec(value);
  if (!match) return;
  const whole = `${match[1]}Z`, parsed = Date.parse(whole);
  if (!Number.isFinite(parsed) || new Date(parsed).toISOString() !== `${match[1]}.000Z`) return;
  return `${match[1]}.${(match[2] ?? "").padEnd(9, "0")}Z`;
}
const revision = (value: unknown) => typeof value === "string" && /^[1-9][0-9]{0,19}$/.test(value) && BigInt(value) <= 18446744073709551615n;
const reasons: Record<string, string> = {
  conflict: "The PR or prepared workspace changed.", missing_input: "Required PR or Git input was unavailable.",
  unavailable: "The Worker could not complete the Git check.", canceled: "Startup was canceled.",
  resource_exhausted: "The startup check reached a resource limit.", invalid_argument: "The startup check rejected an input.",
};

export function startupRejection(resource?: Resource): Document | undefined {
  const session = document(resource), value = object(session.startup_rejection), proof = object(value.workspace), initial = object(session.initial_execution);
  if (!resource || resource.kind !== EntityKind.SESSION || resource.sessionId !== resource.id || !uuid(resource.id) || session.workspace !== "worktree" || session.outcome !== "not-started" || session.dispatch !== "paused" || session.recovery !== "none" || session.execution != null || session.current_execution != null || session.active_execution_id != null && session.active_execution_id !== "" || session.next_execution_intent != null && session.next_execution_intent !== "") return;
  if (!exact(value, ["version", "type", "server_id", "device_id", "instance_id", "machine_id", "input_id", "account_id", "connection_id", "assignment_revision", "assignment_digest", "assignment_input_digest", "configuration_digest", "workspace"]) || value.version !== 1 || value.type !== "pr-startup-rejected" || !revision(value.assignment_revision)) return;
  if (!["server_id", "device_id", "instance_id", "machine_id", "input_id", "account_id", "connection_id"].every((key) => uuid(value[key])) || !["assignment_digest", "assignment_input_digest", "configuration_digest"].every((key) => digest(value[key]))) return;
  if (!exact(proof, ["job_id", "execution_id", "session_id", "preparation_digest", "manifest_digest", "target_digest", "journal_digest", "reason", "started_at", "finished_at"]) || !["job_id", "execution_id", "session_id"].every((key) => uuid(proof[key])) || new Set([proof.job_id, proof.execution_id, proof.session_id]).size !== 3 || !["preparation_digest", "manifest_digest", "target_digest", "journal_digest"].every((key) => digest(proof[key])) || !Object.hasOwn(reasons, text(proof.reason)) || !timestamp(proof.started_at) || !timestamp(proof.finished_at) || timestamp(proof.finished_at)! < timestamp(proof.started_at)!) return;
  if (proof.session_id !== resource.id || initial.id !== proof.execution_id || initial.input_id !== value.input_id || initial.initial_account_id !== value.account_id || initial.connection_id !== value.connection_id || initial.configuration_digest !== value.configuration_digest || session.machine_id !== value.machine_id) return;
  return value;
}

export function StartupRejection({ session }: { session: Resource }) {
  if (!Object.hasOwn(document(session), "startup_rejection")) return null;
  const value = startupRejection(session);
  if (!value) return <p className="notice" role="status">Startup rejection details are unavailable. Keep this input paused while its original state is checked.</p>;
  const proof = object(value.workspace);
  return <section className="notice" aria-label="Agent startup rejection"><strong>Agent did not start</strong><p>{reasons[text(proof.reason)]}</p><p>The original input remains in history. Review the PR and Worker Git access before starting a new fix.</p><small>Recorded {text(proof.finished_at)} · This session remains paused.</small></section>;
}

export function RejectedInput({ resource, session }: { resource: Resource; session?: Resource }) {
  const input = document(resource), value = startupRejection(session), proof = object(value?.workspace);
  if (!value || resource.kind !== EntityKind.QUEUE || resource.sessionId !== session?.id || resource.id !== value.input_id || input.execution_id !== proof.execution_id || input.delivery !== "rejected-before-start") return <p className="notice">The original startup rejection could not be verified from the retained session.</p>;
  return <p className="notice">Rejected before agent startup. This input is retained in history and will not be sent again.</p>;
}
