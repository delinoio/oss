import { object, text, type Document } from "./documents";
import { actorValid, bounded, date, positive } from "./github-query-model";

const publishedReview = (value: unknown) => typeof value === "string" && ["APPROVED", "CHANGES_REQUESTED", "COMMENTED", "DISMISSED"].includes(value);
const count = (value: unknown): value is number => Number.isInteger(value) && Number(value) >= 0 && Number(value) <= 500;
const authorValid = (raw: unknown) => {
  if (raw == null) return true;
  const author = object(raw);
  if (!bounded(author.native_type, 64) || !bounded(author.node_id, 256) || !bounded(author.login, 100) || /[\r\n]/.test(`${author.native_type}${author.login}`)) return false;
  if (["User", "Bot", "Organization"].includes(author.native_type)) return actorValid({ ...author, provider_type: author.native_type });
  return author.kind === "unknown" && author.id == null;
};

export function validPRFeedback(raw: unknown, item: Document): boolean {
  const value = object(raw);
  if (raw == null || value.base_sha !== item.base_sha || value.head_sha !== item.head_sha || !Array.isArray(value.entries) || !Array.isArray(value.threads) || value.threads.length > 500 || !count(value.excluded_draft_reviews) || value.entries.length + value.excluded_draft_reviews > 500) return false;
  const entries = new Map<string, Document>(), numeric = new Set<string>();
  let bytes = 0, total = value.entries.length + value.excluded_draft_reviews;
  for (const rawEntry of value.entries) {
    const entry = object(rawEntry);
    if (!positive(entry.id) || !bounded(entry.node_id, 256) || entries.has(entry.node_id) || numeric.has(`${entry.kind}:${entry.id}`) || !bounded(entry.body, 64 << 10, false) || !date(entry.published_at) || (entry.last_edited_at != null && !date(entry.last_edited_at)) || !authorValid(entry.author) || !/^[a-f0-9]{64}$/.test(text(entry.content_version))) return false;
    let fragment: string;
    switch (entry.kind) {
      case "review":
        fragment = "pullrequestreview-";
        if (!publishedReview(entry.native_state) || !date(entry.review_submitted_at) || entry.review_state != null || entry.review_node_id != null || entry.thread_node_id != null || entry.code != null) return false;
        break;
      case "conversation-comment":
        fragment = "issuecomment-";
        if (entry.native_state != null || entry.review_state != null || entry.review_submitted_at != null || entry.review_node_id != null || entry.thread_node_id != null || entry.code != null) return false;
        break;
      case "review-comment": {
        fragment = "discussion_r";
        const code = object(entry.code);
        if (entry.native_state !== "SUBMITTED" || !publishedReview(entry.review_state) || !date(entry.review_submitted_at) || !bounded(entry.review_node_id, 256) || !bounded(entry.thread_node_id, 256) || !bounded(code.path, 4096) || !bounded(code.diff_hunk, 64 << 10, false)) return false;
        for (const line of [code.line, code.original_line]) if (line != null && (!Number.isInteger(line) || Number(line) < 1 || Number(line) > 2147483647)) return false;
        bytes += new TextEncoder().encode(text(code.path) + text(code.diff_hunk)).length;
        break;
      }
      default: return false;
    }
    if (entry.url !== `${text(item.url)}#${fragment}${entry.id}`) return false;
    entries.set(entry.node_id, entry); numeric.add(`${entry.kind}:${entry.id}`);
    bytes += new TextEncoder().encode(entry.body).length;
  }
  if (bytes > 512 << 10) return false;
  const threads = new Set<string>(), comments = new Set<string>();
  for (const rawThread of value.threads) {
    const thread = object(rawThread);
    if (!bounded(thread.node_id, 256) || threads.has(thread.node_id) || entries.has(thread.node_id) || typeof thread.resolved !== "boolean" || typeof thread.outdated !== "boolean" || !Array.isArray(thread.comment_nodes) || !count(thread.excluded_drafts)) return false;
    threads.add(thread.node_id); total += thread.excluded_drafts;
    for (const id of thread.comment_nodes) {
      if (typeof id !== "string") return false;
      const entry = entries.get(id), review = entry ? entries.get(text(entry.review_node_id)) : undefined;
      if (!entry || comments.has(id) || entry.kind !== "review-comment" || entry.thread_node_id !== thread.node_id || review?.kind !== "review" || review.native_state !== entry.review_state || review.review_submitted_at !== entry.review_submitted_at) return false;
      comments.add(id);
    }
  }
  return total <= 500 && [...entries.values()].every(entry => entry.kind !== "review-comment" || comments.has(text(entry.node_id)));
}

export function PRFeedback({ value }: { value: Document }) {
  const threads = new Map((value.threads as Document[]).map(thread => [text(thread.node_id), thread]));
  const entries = value.entries as Document[];
  const excluded = Number(value.excluded_draft_reviews) + [...threads.values()].reduce((sum, thread) => sum + Number(thread.excluded_drafts), 0);
  return <section aria-label="Published PR feedback">
    <p>{entries.length} published feedback entries · {threads.size} review threads{excluded ? ` · ${excluded} unsubmitted drafts excluded` : ""}</p>
    <p>Approved and dismissed reviews remain visible. GitHub review dismissal is separate from handling feedback in DeliDev. Author identity alone does not verify a GitHub App or current repository permission.</p>
    {entries.length === 0 ? <p>No published feedback was returned.</p> : entries.map(entry => {
      const author = object(entry.author), thread = threads.get(text(entry.thread_node_id)), code = object(entry.code);
      const kind = entry.kind === "review" ? "Review body" : entry.kind === "review-comment" ? "Code review comment" : "PR conversation comment";
      return <article className="result" key={text(entry.node_id)}>
        <h5>{kind} · {text(author.login) || "Author unavailable"}</h5>
        {entry.author != null ? <p>{text(author.native_type)} · {text(author.id) || "Numeric identity unavailable"} · <code>{text(author.node_id)}</code></p> : null}
        <p>Published {text(entry.published_at)}{entry.last_edited_at ? ` · Edited ${text(entry.last_edited_at)}` : ""}{entry.native_state ? ` · ${text(entry.native_state)}` : ""}{entry.review_state ? ` · Review ${text(entry.review_state)}` : ""}</p>
        {thread ? <p>{thread.resolved ? "Thread resolved on GitHub" : "Thread unresolved on GitHub"}{thread.outdated ? " · Outdated code position" : ""}</p> : null}
        <pre>{text(entry.body) || "Empty review body."}</pre>
        {entry.code != null ? <details><summary>Code context · {text(code.path)}</summary><p>Current line: {code.line == null ? "Unavailable" : String(code.line)} · Original line: {code.original_line == null ? "Unavailable" : String(code.original_line)}</p><pre>{text(code.diff_hunk)}</pre></details> : null}
        <details><summary>Feedback identity and content version</summary><p>GitHub address: <code>{text(entry.url)}</code></p><p>Original ID: {text(entry.id)} · <code>{text(entry.node_id)}</code></p><p>Content version: <code>{text(entry.content_version)}</code></p></details>
      </article>;
    })}
  </section>;
}
