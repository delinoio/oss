import { feedbackObservation } from "./github-feedback-fixture";
export function reviewerObservation() {
  const feedback = feedbackObservation(), author = feedback.entries[0].author;
  return { feedback, actors: [{ author, identity_access: "available", identity: { provider_type: "Bot", kind: "bot", id: author.id, node_id: author.node_id, login: "chatgpt-codex-connector[bot]" }, permission: { access: "available", legacy_permission: "none", minimum: "NONE", maximum: "NONE" } }], applications: feedback.entries.map(entry => ({ feedback_node_id: entry.node_id, content_version: entry.content_version, access: "not-evaluated", state: "unknown" })) };
}
