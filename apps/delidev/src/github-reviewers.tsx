import { object, text, type Document } from "./documents";
import { actorValid, bounded, positive } from "./github-query-model";
import { PRFeedback, validPRFeedback } from "./github-feedback";

const accessStates = new Set(["available", "invalid-token", "restricted", "not-found-or-inaccessible", "sso-required", "rate-limited", "unavailable", "not-evaluated"]);
function permissionInterval(legacy: string, role: string): [string, string] | undefined {
  const standard = new Map<string, [string, string]>([["none", ["none", "NONE"]], ["read", ["read", "READ"]], ["triage", ["read", "TRIAGE"]], ["write", ["write", "WRITE"]], ["maintain", ["write", "MAINTAIN"]], ["admin", ["admin", "ADMIN"]]]);
  const known = standard.get(role);
  if (known) return known[0] === legacy ? [known[1], known[1]] : undefined;
  if (legacy === "none") return role === "" ? ["NONE", "NONE"] : undefined;
  if (legacy === "read") return ["READ", "TRIAGE"];
  if (legacy === "write") return ["WRITE", "MAINTAIN"];
  if (legacy === "admin") return ["ADMIN", "ADMIN"];
}
function validPermission(value: Document): boolean {
  if (!accessStates.has(text(value.access))) return false;
  if (value.access !== "available") return value.legacy_permission == null && value.role_name == null && value.minimum == null && value.maximum == null;
  if (!bounded(value.legacy_permission, 16) || (value.role_name != null && (!bounded(value.role_name, 256, false) || /[\r\n]/.test(value.role_name)))) return false;
  const interval = permissionInterval(value.legacy_permission, text(value.role_name));
  return interval != null && value.minimum === interval[0] && value.maximum === interval[1];
}
export function validPRReviewers(raw: unknown, item: Document): boolean {
  const value = object(raw), feedback = object(value.feedback);
  if (!validPRFeedback(value.feedback, item) || !Array.isArray(value.actors) || !Array.isArray(value.applications)) return false;
  const entries = new Map<string, Document>(), authors = new Map<string, Document>();
  for (const entry of feedback.entries as Document[]) {
    entries.set(text(entry.node_id), entry);
    if (entry.author == null) continue;
    const author = object(entry.author), previous = authors.get(text(author.node_id));
    if (previous && ["native_type", "kind", "id", "node_id", "login"].some(key => previous[key] !== author[key])) return false;
    authors.set(text(author.node_id), author);
  }
  if (value.actors.length !== authors.size || value.applications.length !== entries.size) return false;
  const seen = new Set<string>();
  for (const rawActor of value.actors) {
    const actor = object(rawActor), author = object(actor.author), original = authors.get(text(author.node_id)), identity = object(actor.identity), permission = object(actor.permission);
    if (!original || seen.has(text(author.node_id)) || ["native_type", "kind", "id", "node_id", "login"].some(key => original[key] !== author[key]) || !accessStates.has(text(actor.identity_access)) || !validPermission(permission)) return false;
    seen.add(text(author.node_id));
    if (actor.identity_access !== "available") {
      if (actor.identity != null || permission.access !== "not-evaluated") return false;
    } else if (!actorValid(actor.identity) || !["user", "bot"].includes(text(identity.kind)) || identity.id !== author.id || identity.node_id !== author.node_id || identity.kind !== author.kind) return false;
  }
  seen.clear();
  for (const rawAttribution of value.applications) {
    const attribution = object(rawAttribution), entry = entries.get(text(attribution.feedback_node_id)), app = object(attribution.application);
    if (!entry || seen.has(text(entry.node_id)) || attribution.content_version !== entry.content_version || !accessStates.has(text(attribution.access))) return false;
    seen.add(text(entry.node_id));
    if (entry.kind !== "conversation-comment" && (attribution.access !== "not-evaluated" || attribution.state !== "unknown")) return false;
    switch (attribution.state) {
      case "unknown": if (attribution.application != null) return false; break;
      case "none": if (attribution.access !== "available" || attribution.application != null) return false; break;
      case "attributed": if (attribution.access !== "available" || !positive(app.id) || !bounded(app.node_id, 256) || !bounded(app.slug, 100) || /[\r\n]/.test(app.slug)) return false; break;
      default: return false;
    }
  }
  return true;
}

export function PRReviewers({ value }: { value: Document }) {
  const actors = value.actors as Document[], applications = value.applications as Document[];
  return <section aria-label="Feedback author verification">
    <p>Identities and repository permissions were checked for this observation. Automatic handling must verify them again before execution. Public repository visibility is not a collaborator permission.</p>
    {actors.length ? <table><caption>Current feedback author identities and permissions</caption><thead><tr><th scope="col">Author</th><th scope="col">Identity</th><th scope="col">Repository permission</th></tr></thead><tbody>{actors.map(actor => {
      const author = object(actor.author), identity = object(actor.identity), permission = object(actor.permission);
      return <tr key={text(author.node_id)}><th scope="row">{text(identity.login) || text(author.login)}<small> · {text(author.native_type)} · {text(author.id) || "Numeric ID unavailable"}</small></th><td>{actor.identity_access === "available" ? "Verified identity" : text(actor.identity_access)}<small> · {text(author.node_id)}</small></td><td>{permission.access === "available" ? permission.minimum === "NONE" ? "No collaborator grant" : permission.minimum === permission.maximum ? text(permission.minimum) : `${text(permission.minimum)} verified; ${text(permission.maximum)} uncertain` : text(permission.access)}{permission.role_name ? <small> · Role: {text(permission.role_name)}</small> : null}</td></tr>;
    })}</tbody></table> : <p>No identifiable feedback authors were returned.</p>}
    {applications.length ? <details><summary>GitHub App attribution for each feedback entry</summary><p>Only GitHub's explicit attribution identifies an App. A bot name or another comment's App cannot supply missing attribution.</p><table><caption>Original feedback App attribution</caption><thead><tr><th scope="col">Feedback</th><th scope="col">App attribution</th></tr></thead><tbody>{applications.map(attribution => { const app = object(attribution.application); return <tr key={text(attribution.feedback_node_id)}><th scope="row"><code>{text(attribution.feedback_node_id)}</code></th><td>{attribution.state === "attributed" ? `${text(app.slug)} · App ${text(app.id)} · ${text(app.node_id)}` : attribution.state === "none" ? "GitHub reports no App attribution" : `App not verified · ${text(attribution.access)}`}</td></tr>; })}</tbody></table></details> : null}
    <PRFeedback value={object(value.feedback)} />
  </section>;
}
