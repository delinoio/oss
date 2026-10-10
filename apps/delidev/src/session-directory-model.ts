// SPDX-License-Identifier: Apache-2.0
import { EntityKind, type Resource, type SessionDirectoryOperation, type ChangeSessionDirectoryRequest } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import type { Root } from "./session-files-observation";

export const directoryUUID = (value: string) => /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
export function canonicalDirectory(value: string): boolean {
 return new TextEncoder().encode(value).byteLength <= 4096 && value.length > 0 && !/[\\:\x00-\x1f\x7f]/.test(value) && (value === "." || value.split("/").every(part => part !== "" && part !== "." && part !== ".."));
}
export function safeDirectoryRoots(roots: Root[]): Root[] | undefined {
 if (!roots.length || roots.length > 100 || new Set(roots.map(root => root.repository_id)).size !== roots.length || roots.some(root => !root.name || new TextEncoder().encode(root.name).byteLength > 4096 || root.repository_id && !directoryUUID(root.repository_id)) || roots.some(root => !root.repository_id) && (roots.length !== 1 || roots[0].repository_id !== "")) return;
 return roots.map((root,index) => ({...root,name:/^(?:[/\\]|[a-zA-Z]:)/.test(root.name) || /[\x00-\x1f\x7f]/.test(root.name) ? `… (${index+1})` : directoryDisplayPath(root.name)}));
}

export function directoryEligible(session: Resource | undefined, supported: boolean): boolean {
 const s=document(session),e=object(s.execution),p=object(s.preparation);
 return Boolean(supported && session?.schemaVersion === 1 && session.documentJson.byteLength <= (1<<20) && directoryUUID(session.id) && session.revision>0n && session.kind===EntityKind.SESSION && object(object(s.initial_execution).configuration).harness==="codex" && (s.fork===undefined || s.fork===null || typeof s.fork==="object" && !Array.isArray(s.fork)) && !object(s.fork).sidechat_parent_snapshot && p.state==="ready" && s.archive==="active" && s.recovery==="none" && s.dispatch==="ready" && s.outcome==="succeeded" && !s.active_execution_id && !s.directory_job_id && !s.compaction_job_id && !s.execution_recovery_job_id && !s.pending_steer_id && !s.pending_inputs && !s.pending_input_bytes && directoryUUID(text(e.execution_id)) && directoryUUID(text(e.job_id)) && e.cleanup_verified===true && !e.unconfirmed_responses && !Object.values(object(e.waiting)).some(Boolean) && !Object.keys(object(e.subagents)).length && Object.values(object(e.native_compactions)).every(stage=>stage==="completed") && Object.values(object(e.auto_reviews)).every(review=>["approved","denied","timedOut","aborted"].includes(text(object(review).status))) && !["queued","running","uncertain"].includes(text(s.title_state)));
}
export type DirectorySource = {execution:string;previous:string;sourceJob:string};
export function directorySource(session: Resource): DirectorySource {
 const s=document(session),e=object(s.execution);
 return { execution:text(e.execution_id),previous:text(object(s.directory).generation_id),sourceJob:text(e.job_id) };
}
export function verifiedDirectoryOperation(op: SessionDirectoryOperation | undefined, request: ChangeSessionDirectoryRequest, source: DirectorySource, originalJob?: string): boolean {
 const m=request.mutation,j=op?.job,d=document(j),g=op?.generation;
 if (!m || !directoryUUID(source.execution) || !directoryUUID(source.sourceJob) || source.previous && !directoryUUID(source.previous) || !op || op.sessionId!==m.id || op.requestId!==m.requestId || !j || j.kind!==EntityKind.JOB || j.schemaVersion!==1 || j.documentJson.byteLength>(1<<20) || j.revision<=0n || j.sessionId!==m.id || !directoryUUID(j.id) || originalJob && j.id!==originalJob || d.type!=="change-session-directory" || d.input!==undefined || d.output!==undefined || !["queued","claimed","uncertain","failed","canceled","succeeded"].includes(text(d.state)) || d.parent_id!==source.sourceJob) return false;
 if (d.state!=="succeeded") return !g;
 if (!g || g.jobId!==j.id || g.requestId!==m.requestId || g.sourceExecutionId!==source.execution || g.previousGenerationId!==source.previous || g.repositoryId!==request.repositoryId || g.relativePath!==request.relativePath || !canonicalDirectory(g.relativePath)) return false;
 const ids=[g.generationId,g.jobId,g.requestId,g.sourceExecutionId];
 return ids.every(directoryUUID) && new Set(ids).size===ids.length && g.generationId!==g.previousGenerationId;
}
export const directorySettled = (op: SessionDirectoryOperation | undefined) => ["succeeded","failed","canceled"].includes(text(document(op?.job).state));

export const directoryDisplayPath = (value: string) => value.replace(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi, "…");
