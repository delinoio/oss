// SPDX-License-Identifier: Apache-2.0
import { Timestamp } from "./timestamp-display";
import { LocalizedText, copy, useLocale } from "./localization";
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { utcTimestamp as timestamp } from "./timestamp";

const uuid = (value: unknown) => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
const digest = (value: unknown) => typeof value === "string" && /^[0-9a-f]{64}$/.test(value);
const exact = (value: Document, keys: string[]) => Object.keys(value).length === keys.length && keys.every((key) => Object.hasOwn(value, key));
const revision = (value: unknown) => typeof value === "string" && /^[1-9][0-9]{0,19}$/.test(value) && BigInt(value) <= 18446744073709551615n;
const reasons: Record<string, string> = {
  get conflict() { return copy("startup-rejection.extra.0ab4527c94e0"); }, get missing_input() { return copy("startup-rejection.extra.2bdb660fc027"); },
  get unavailable() { return copy("startup-rejection.extra.319c00c76acb"); }, get canceled() { return copy("startup-rejection.extra.a78e7fc39866"); },
  get resource_exhausted() { return copy("startup-rejection.extra.933576bc6c6f"); }, get invalid_argument() { return copy("startup-rejection.extra.bfc1004381ca"); },
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
  useLocale();
  if (!Object.hasOwn(document(session), "startup_rejection")) return null;
  const value = startupRejection(session);
  if (!value) return <p className="notice" role="status">{copy("startup-rejection.startupRejectionDetailsAreUnavailableKeep_780ffd")}</p>;
  const proof = object(value.workspace);
  return <section className="notice" aria-label={copy("startup-rejection.agentStartupRejection_27a3f8")}><strong>{copy("startup-rejection.agentDidNotStart_32a1e1")}</strong><p>{reasons[text(proof.reason)]}</p><p>{copy("startup-rejection.theOriginalInputRemainsInHistory_e7a561")}</p><small><LocalizedText id="startup-rejection.recordedThisSessionRemainsPaused_ed54d8" components={{ s0: <><Timestamp value={text(proof.finished_at)} /></> }} /></small></section>;
}

export function RejectedInput({ resource, session }: { resource: Resource; session?: Resource }) {
  useLocale();
  const input = document(resource), value = startupRejection(session), proof = object(value?.workspace);
  if (!value || resource.kind !== EntityKind.QUEUE || resource.sessionId !== session?.id || resource.id !== value.input_id || input.execution_id !== proof.execution_id || input.delivery !== "rejected-before-start") return <p className="notice">{copy("startup-rejection.theOriginalStartupRejectionCouldNot_fff807")}</p>;
  return <p className="notice">{copy("startup-rejection.rejectedBeforeAgentStartupThisInput_9b8fc2")}</p>;
}
