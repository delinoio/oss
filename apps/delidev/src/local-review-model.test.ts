import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { expect, it } from "vitest";
import { encode, Mode } from "./documents";
import { Comparison, type Diff } from "./session-diff-model";
import { AnchorKind, ReviewSide, readAnchor, readComment, readSubmission, readReviewContext, selectedContext, freshness, type Anchor, type ReviewFile } from "./local-review-model";

const session = newRequestId();
const diff: Diff = { repository_id: newRequestId(), comparison: Comparison.Creation, path: ".", base: "commit", base_object: "a".repeat(40), head_commit: "b".repeat(40), revision: "c".repeat(64), patch: "", untracked: [] };
const anchor: Anchor = { repository_id: diff.repository_id, comparison: diff.comparison, query_path: ".", diff_revision: diff.revision, file_digest: "d".repeat(64), context: "new\nlast", selection: { path: "file.txt", kind: AnchorKind.Lines, side: ReviewSide.New, start: 1, end: 2 } };
const file: ReviewFile = { path: "file.txt", kind: "text", digest: anchor.file_digest, lines: [{ old: 1, text: "old", newline: true, hunk: 1 }, { new: 1, text: "new", newline: true, hunk: 1 }, { old: 2, new: 2, text: "last", newline: false, hunk: 1 }] };
const resource = (value: object) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REVIEW, sessionId: session, revision: 1n, schemaVersion: 1, documentJson: encode(value) });

it("preserves original sides, EOF and comparison freshness without relocating anchors", () => {
  expect(readAnchor(anchor)).toEqual(anchor);
  expect(readReviewContext(encode({ diff, files: [file] }), diff).files).toEqual([file]);
  expect(selectedContext(file, anchor.selection)).toBe("new\nlast");
  expect(selectedContext(file, { ...anchor.selection, side: ReviewSide.Old })).toBe("old\nlast");
  expect(freshness(anchor, diff)).toBe("Current for this comparison");
  expect(freshness(anchor, { ...diff, revision: "e".repeat(64) })).toContain("Stale");
  expect(freshness(anchor, { ...diff, repository_id: newRequestId() })).toContain("Not checked");
  expect(() => readReviewContext(encode({ diff: { ...diff, revision: "e".repeat(64) }, files: [file] }), diff)).toThrow("diff changed");
});

it("rejects missing, cross-file, cross-hunk, duplicate, binary and oversized text locations", () => {
  for (const changed of [
    { ...file, path: "other.txt" }, { ...file, kind: "non-line" as const, lines: [] },
    { ...file, lines: file.lines.slice(0, 2) }, { ...file, lines: [...file.lines, file.lines[2]] },
    { ...file, lines: file.lines.map((v, i) => ({ ...v, hunk: i + 1 })) },
    { ...file, lines: file.lines.map((v) => ({ ...v, text: "💬".repeat(2049) })) },
  ]) expect(selectedContext(changed, anchor.selection)).toBeUndefined();
  expect(selectedContext({ ...file, kind: "non-line", lines: [] }, { kind: AnchorKind.File, path: file.path })).toBe("");
});

it.each(["foreign", "duplicate", "mixed", "binary", "number", "surrogate", "scope"])("rejects malformed context %s", (kind) => {
  const value = structuredClone({ diff, files: [file] });
  if (kind === "foreign") value.diff.repository_id = newRequestId();
  if (kind === "duplicate") value.files.push(value.files[0]);
  if (kind === "mixed") Object.assign(value.files[0], { authority: true });
  if (kind === "binary") value.files[0].kind = "non-line";
  if (kind === "number") value.files[0].lines[0].old = 1.5;
  if (kind === "surrogate") value.files[0].lines[0].text = "\ud800";
  if (kind === "scope") value.files[0].path = "../other.txt";
  expect(() => readReviewContext(encode(value), diff)).toThrow();
});

it("keeps immutable submission copies independent of later comment content and exact large revisions", () => {
  const comment = { anchor, body: "<script>inert</script> 💬", content_revision: "9007199254740993", last_submission_id: newRequestId(), last_submitted_content_revision: "9007199254740992" };
  expect(readComment(resource({ version: 1, type: "comment", comment }), session)).toEqual(comment);
  const submission = { input_id: newRequestId(), mode: Mode.Plan, comments: [{ id: newRequestId(), anchor, body: "original text", content_revision: "9007199254740992", freshness: "stale" }] };
  expect(readSubmission(resource({ version: 1, type: "submission", submission }), session)).toEqual(submission);
  expect(readSubmission(resource({ version: 1, type: "submission", submission: { ...submission, comments: [...submission.comments, submission.comments[0]] } }), session)).toBeUndefined();
  for (const changed of [{ ...comment, content_revision: 9007199254740992 }, { ...comment, content_revision: "9223372036854775808" }, { ...comment, content_revision: "1" }, { ...comment, last_submission_id: undefined }, { ...comment, body: "💬".repeat(2049) }]) expect(readComment(resource({ version: 1, type: "comment", comment: changed }), session)).toBeUndefined();
  expect(readComment(resource({ version: 1, type: "comment", comment, submission }), session)).toBeUndefined();
  expect(readComment(resource({ version: 1, type: "comment", comment }), newRequestId())).toBeUndefined();
});
