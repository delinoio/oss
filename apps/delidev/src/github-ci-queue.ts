import { object, text, type Document } from "./documents";
import { bounded, positive, sha } from "./github-query-model";

function validEntry(row: Document): boolean {
  return bounded(row.node_id, 256) && bounded(row.pull_request_node_id, 256) && positive(row.pull_request_number) && typeof row.position === "string" && /^(0|[1-9][0-9]*)$/.test(row.position) && row.position.length <= 10 && BigInt(row.position) <= 2147483647n && bounded(row.state, 64) && (row.base_sha == null || sha(row.base_sha)) && (row.head_sha == null || sha(row.head_sha));
}
export function validCIQueue(queue: Document, item: Document): boolean {
  const entry = object(queue.entry);
  if (!bounded(queue.node_id, 256) || !bounded(queue.repository_node_id, 256) || (item.repository_node_id != null && queue.repository_node_id !== item.repository_node_id) || !["ALLGREEN", "HEADGREEN", "unknown"].includes(text(queue.strategy)) || !validEntry(entry) || entry.pull_request_node_id !== item.node_id || entry.pull_request_number !== item.number || !Array.isArray(queue.entries) || queue.entries.length > 500 || queue.total_count !== String(queue.entries.length)) return false;
  const nodes = new Set<string>(), prs = new Set<string>();
  let found = false;
  let previous = -1n;
  for (const raw of queue.entries) {
    const row = object(raw);
    if (!validEntry(row) || BigInt(text(row.position)) <= previous || nodes.has(text(row.node_id)) || prs.has(text(row.pull_request_node_id))) return false;
    previous = BigInt(text(row.position));
    nodes.add(text(row.node_id)); prs.add(text(row.pull_request_node_id));
    if (row.node_id === entry.node_id) {
      if (["pull_request_node_id", "pull_request_number", "position", "base_sha", "head_sha", "state"].some(key => row[key] !== entry[key])) return false;
      found = true;
    }
  }
  return found;
}
export function evaluableCIQueue(queue: Document, item: Document): boolean {
  const entry = object(queue.entry);
  return validCIQueue(queue, item) && queue.strategy === "ALLGREEN" && sha(entry.base_sha) && sha(entry.head_sha) && entry.head_sha !== item.head_sha && entry.head_sha !== item.base_sha && entry.head_sha !== entry.base_sha && queue.rollup != null;
}
export function selectedCIRollup(value: Document): Document {
  const source = object(value.result).source;
  return source === "merge-queue" ? object(object(value.merge_queue).rollup) : source === "test-merge" ? object(value.test_merge) : object(value.head);
}
export function queueResultProvenance(row: Document): boolean {
  if (object(row.application).id !== "15368") return true;
  const evidence = object(row.evidence), workflow = object(evidence.workflow);
  return row.workflow_event === "merge_group" && workflow.suite_node_id === evidence.suite_node_id && workflow.commit_sha === row.commit_sha;
}
