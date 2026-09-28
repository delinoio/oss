import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport, ConnectError, Code } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SessionService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { Comparison, type Diff } from "./session-diff-model";
import { MutationIntents } from "./mutation";
import { LocalReviews } from "./local-reviews";

function fixture() {
 const sessionId = newRequestId(), inputId = newRequestId();
 const diff: Diff = { repository_id: newRequestId(), comparison: Comparison.Creation, path: ".", base: "commit", base_object: "a".repeat(40), head_commit: "a".repeat(40), patch: "", untracked: [], revision: "b".repeat(64) };
 const anchor = { repository_id: diff.repository_id, comparison: diff.comparison, query_path: ".", diff_revision: diff.revision, file_digest: "c".repeat(64), context: "original line\n", selection: { path: "file.txt", kind: "lines", side: "new", start: 1, end: 1 } };
 const comment = { anchor, body: "<script>original feedback</script>", content_revision: "1" };
 let rows = [create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REVIEW, sessionId, revision: 9007199254740993n, schemaVersion: 1, documentJson: encode({ version: 1, type: "comment", comment }) })];
 const read = vi.fn(async () => ({ documentJson: encode({ diff, files: [{ path: "file.txt", kind: "text", digest: anchor.file_digest, lines: [{ new: 1, text: "original line", newline: true, hunk: 1 }] }] }) }));
 const save = vi.fn(async (_r: { requestId: string; documentJson: Uint8Array }) => ({ comment: rows[0] }));
 const edit = vi.fn(async (_r: { body: string; mutation?: { expectedRevision: bigint } }) => ({ comment: rows[0] }));
 const remove = vi.fn(async () => ({ id: rows[0].id }));
 const submit = vi.fn(async (_r: { requestId: string; documentJson: Uint8Array }) => ({ change: { input: { id: inputId } } }));
 const transport = createRouterTransport((router) => {
  router.service(ResourceService, { listResources: () => ({ resources: rows }) });
  router.service(SessionService, { readSessionReviewContext: read, createLocalReviewComment: save, editLocalReviewComment: edit, deleteLocalReviewComment: remove, submitLocalReview: submit });
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 function View() { const [open, setOpen] = useState(true); return <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><button onClick={() => setOpen(!open)}>Toggle review</button>{open ? <LocalReviews sessionId={sessionId} diff={diff} reading={false} /> : null}</MutationIntents></QueryClientProvider></TransportProvider>; }
 return { View, read, save, edit, remove, submit, diff, inputId, replaceComment: (body: string) => { rows = [{ ...rows[0], revision: rows[0].revision + 1n, documentJson: encode({ version: 1, type: "comment", comment: { ...comment, body, content_revision: "2" } }) }]; } };
}
const decoded = (v: Uint8Array) => JSON.parse(new TextDecoder().decode(v));

it("authors exact visible context and renders feedback as inert text", async () => {
 const f = fixture(); const { container } = render(<f.View />);
 await screen.findByText("<script>original feedback</script>"); expect(container.querySelector("script")).toBeNull();
 fireEvent.click(screen.getByRole("button", { name: "Add review comment" }));
 await screen.findByRole("option", { name: "file.txt" });
 fireEvent.change(screen.getByLabelText("Comment location"), { target: { value: "lines" } });
 expect(screen.getByLabelText("Selected review context").textContent).toBe("original line\n");
 fireEvent.change(screen.getByLabelText("Review comment"), { target: { value: "Please change this line" } });
 fireEvent.click(screen.getByRole("button", { name: "Save comment" }));
 await screen.findByText("Comment saved.");
 expect(decoded(f.save.mock.calls[0][0].documentJson)).toMatchObject({ query: { repository_id: f.diff.repository_id, comparison: "creation" }, diff_revision: f.diff.revision, selection: { path: "file.txt", kind: "lines", side: "new", start: 1, end: 1 }, body: "Please change this line" });
});

it("requires a new location after the comparison changes before editing", async () => {
 const f = fixture(); f.read.mockResolvedValue({ documentJson: encode({ diff: { ...f.diff, revision: "d".repeat(64) }, files: [] }) }); render(<f.View />);
 fireEvent.click(screen.getByRole("button", { name: "Add review comment" }));
 await screen.findByText(/diff changed before comment editing/);
 expect((screen.getByRole("button", { name: "Save comment" }) as HTMLButtonElement).disabled).toBe(true);
 expect(f.save).not.toHaveBeenCalled();
});

it("retries the original grouped submission after navigation without rounding revisions", async () => {
 const f = fixture(); f.submit.mockRejectedValueOnce(new ConnectError("Response lost", Code.Unavailable)); render(<f.View />);
 fireEvent.click(await screen.findByRole("checkbox", { name: "Select comment on file.txt" }));
 fireEvent.change(screen.getByLabelText("Review request mode"), { target: { value: "plan" } });
 fireEvent.click(screen.getByRole("checkbox", { name: /Include stale comments/ }));
 fireEvent.click(screen.getByRole("button", { name: "Request changes" }));
 await screen.findByRole("button", { name: "Retry original review submission" });
 const original = f.submit.mock.calls[0][0];
 expect(decoded(original.documentJson)).toMatchObject({ comments: [{ revision: "9007199254740993" }], mode: "plan", allow_stale: true });
 expect((screen.getByRole("button", { name: "Delete comment" }) as HTMLButtonElement).disabled).toBe(true);
 fireEvent.click(screen.getByRole("button", { name: "Toggle review" })); fireEvent.click(screen.getByRole("button", { name: "Toggle review" }));
 fireEvent.click(await screen.findByRole("button", { name: "Retry original review submission" }));
 await screen.findByText(`Request changes queued as input ${f.inputId}.`);
 expect(f.submit).toHaveBeenCalledTimes(2); expect(f.submit.mock.calls[1][0]).toEqual(original);
});

it("retains selected and edited versions when another client changes the comment", async () => {
 const f = fixture(); render(<f.View />);
 fireEvent.click(await screen.findByRole("checkbox", { name: "Select comment on file.txt" }));
 fireEvent.click(screen.getByRole("button", { name: "Edit comment" }));
 fireEvent.change(screen.getByLabelText("Edit review comment"), { target: { value: "My retained edit" } });
 f.replaceComment("Concurrent change"); fireEvent.click(screen.getByRole("button", { name: "Refresh reviews" }));
 await screen.findByText("The comment changed. Your edit is retained.");
 expect((screen.getByRole("button", { name: "Save comment edit" }) as HTMLButtonElement).disabled).toBe(true);
 expect((screen.getByLabelText("Edit review comment") as HTMLTextAreaElement).value).toBe("My retained edit");
 expect(screen.getByText(/An earlier version is selected/)).toBeTruthy();
 fireEvent.click(screen.getByRole("button", { name: "Use latest comment revision with this edit" }));
 fireEvent.click(screen.getByRole("button", { name: "Save comment edit" }));
 await waitFor(() => expect(f.edit).toHaveBeenCalledTimes(1));
 expect(f.edit.mock.calls[0][0]).toMatchObject({ body: "My retained edit", mutation: { expectedRevision: 9007199254740994n } });
});
