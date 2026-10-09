// SPDX-License-Identifier: Apache-2.0
import { copy, useLocale } from "./localization";
import { object, text } from "./documents";
import type { MessageKey } from "./localization";
const statuses: Record<string, MessageKey> = { inProgress: "native-auto-review.running", approved: "native-auto-review.approved", denied: "native-auto-review.denied", timedOut: "native-auto-review.timedOut", aborted: "native-auto-review.aborted" };
export function NativeAutoReview({ progress, state }: { progress: unknown; state: string }) {
  useLocale();
  const p = object(progress), review = object(p.auto_review), status = text(review.status);
  const valid = state === "complete" && p.kind === "codex-auto-review" && Object.keys(p).every(key => key === "kind" || key === "auto_review") && Object.keys(review).every(key => ["review_id", "target_item_id", "status", "started_at_ms", "completed_at_ms"].includes(key)) && typeof review.review_id === "string" && review.review_id.length > 0 && review.review_id.length <= 1024 && (review.target_item_id === null || typeof review.target_item_id === "string" && review.target_item_id.length > 0 && review.target_item_id.length <= 1024) && Object.hasOwn(statuses, status) && Number.isSafeInteger(review.started_at_ms) && Number(review.started_at_ms) >= 0 && (status === "inProgress" ? review.completed_at_ms === null : Number.isSafeInteger(review.completed_at_ms) && Number(review.completed_at_ms) >= Number(review.started_at_ms));
  return <div className="native-auto-review"><strong>{copy("native-auto-review.title")}</strong><p role="status">{copy(valid ? statuses[status] : "native-auto-review.unavailable")}</p></div>;
}
