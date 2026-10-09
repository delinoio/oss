// SPDX-License-Identifier: Apache-2.0
import { object } from "./documents";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy } from "./localization";
import { statusLabel } from "./product-status";

const uuid = (value: unknown) => typeof value === "string" && /^[a-f0-9]{8}-[a-f0-9]{4}-7[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(value);
function reference(snapshot: unknown) {
 const s = object(snapshot), v = object(s.image_view);
 if (Object.keys(s).some(key => !["kind", "status", "image_view", "changes"].includes(key)) || s.kind !== "image-view" || !["running", "completed"].includes(String(s.status)) || ["command", "changes", "read", "shell", "todo", "builtin"].some(key => s[key] != null) || Object.keys(v).some(key => !["reference_id", "machine_id", "repository_id", "manifest_digest", "location"].includes(key))) return;
 const location = v.location;
 if (!uuid(v.reference_id) || !uuid(v.machine_id) || (v.repository_id != null && !uuid(v.repository_id)) || typeof v.manifest_digest !== "string" || !/^[a-f0-9]{64}$/.test(v.manifest_digest) || typeof location !== "string" || !location || new TextEncoder().encode(location).length > 4096 || /[\\:\u0000-\u001f\u007f\uD800-\uDFFF]/u.test(location) || location.startsWith("/") || location.split("/").some(part => !part || part === "." || part === "..")) return;
 return { id: v.reference_id, machine: v.machine_id, repository: v.repository_id, digest: v.manifest_digest, location };
}

/** Native metadata is inert: a path is neither byte nor live-file authority. */
export function NativeImageView({ tool, state }: { tool: unknown; state: string }) {
 const t = object(tool), first = reference(t.started), last = t.completed == null ? undefined : reference(t.completed);
 const valid = first && object(t.started).status === "running" && t.output == null && t.inputs == null && t.patches == null && t.states == null && (t.completed == null ? state === "streaming" : state === "complete" && object(t.completed).status === "completed" && last && JSON.stringify(first) === JSON.stringify(last));
 if (!valid) return <p>{copy("session.imageViewUnsupported")}</p>;
 return <Disclosure><DisclosureSummary>{copy("session.imageView")} · {statusLabel(t.completed == null ? "running" : "completed")}</DisclosureSummary><pre>{first.location}</pre><p>{copy("session.imageViewPreviewUnavailable")}</p></Disclosure>;
}
