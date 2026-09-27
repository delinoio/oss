import { useState } from "react";
import { createRouterTransport, ConnectError, Code } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SessionService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { SessionDiff } from "./session-diff";

function fixture(worktree = true) {
  const sessionId = newRequestId(), primary = newRequestId(), other = newRequestId();
  const reply = (value: object) => ({ documentJson: encode({ size: "0", binary: false, truncated: false, ...value }) });
  const read = vi.fn(async (request: { queryJson: Uint8Array }) => {
    const q = JSON.parse(new TextDecoder().decode(request.queryJson));
    if (q.operation === "roots") return reply({ roots: [{ repository_id: other, name: "Other", primary: false }, { repository_id: primary, name: "Primary", primary: true }] });
    return reply({ diff: { comparison: q.comparison, repository_id: q.repository_id, path: q.path, base: "commit", base_object: "a".repeat(40), head_commit: "a".repeat(40), patch: "+<script>doNotRun()</script>\n", untracked: ["new.txt"], revision: "b".repeat(64) } });
  });
  const transport = createRouterTransport((router) => router.service(SessionService, { readSessionWorkspace: read }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function View() {
    const [open, setOpen] = useState(true), [draft, setDraft] = useState("unsent input");
    return <TransportProvider transport={transport}><QueryClientProvider client={client}><label>Draft<input value={draft} onChange={(e) => setDraft(e.target.value)} /></label>{open ? <SessionDiff sessionId={sessionId} worktree={worktree} close={() => setOpen(false)} /> : null}</QueryClientProvider></TransportProvider>;
  }
  return { View, primary, other, read, client, reply };
}

it("compares the selected repository and creation commit with inert patch text", async () => {
  const f = fixture(); const { container } = render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  expect(container.querySelector("script, iframe, a")).toBeNull();
  expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls[1][0].queryJson))).toMatchObject({ operation: "git-diff", repository_id: f.primary, comparison: "creation", path: "." });
  expect(screen.getByText("new.txt")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Comparison"), { target: { value: "staged" } });
  await waitFor(() => expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson)).comparison).toBe("staged"));
  fireEvent.change(screen.getByLabelText("Relative diff path"), { target: { value: "src/file.ts" } });
  fireEvent.click(screen.getByRole("button", { name: "Compare path" }));
  await waitFor(() => expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson)).path).toBe("src/file.ts"));
  fireEvent.change(screen.getByLabelText("Diff repository"), { target: { value: f.other } });
  await waitFor(() => expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson))).toMatchObject({ repository_id: f.other, comparison: "creation", path: "." }));
});

it("keeps Local comparisons tied to Git and preserves drafts while discarding closed observations", async () => {
  const f = fixture(false); render(<f.View />);
  await screen.findByRole("region", { name: "Git comparison" });
  expect(screen.queryByRole("option", { name: "Working tree against creation commit" })).toBeNull();
  expect((screen.getByLabelText("Comparison") as HTMLSelectElement).value).toBe("working-tree");
  f.read.mockRejectedValueOnce(new ConnectError("Worker unavailable", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Refresh diff" }));
  await screen.findByText("Refresh failed. The previous comparison is shown below.");
  fireEvent.keyDown(screen.getByRole("complementary", { name: "Session Git diff" }), { key: "Escape" });
  expect(screen.queryByRole("complementary")).toBeNull();
  expect((screen.getByLabelText("Draft") as HTMLInputElement).value).toBe("unsent input");
  await waitFor(() => expect(f.client.getQueryCache().getAll()).toHaveLength(0));
});

it("does not request a repository diff for a projectless root", async () => {
  const f = fixture(); f.read.mockResolvedValue(f.reply({ roots: [{ name: "General Chat", primary: true }] })); render(<f.View />);
  await screen.findByText("This workspace has no prepared Git repository.");
  expect(f.read).toHaveBeenCalledTimes(1);
});

it("preserves the Worker's UTF-8 filename ordering across supplementary characters", async () => {
  const f = fixture(), original = f.read.getMockImplementation()!;
  f.read.mockImplementation(async (request) => {
    const r = await original(request), value = JSON.parse(new TextDecoder().decode(r.documentJson));
    if (value.diff) value.diff.untracked = ["\uE000.txt", "\u{1F600}.txt"];
    return { documentJson: encode(value) };
  });
  render(<f.View />);
  await screen.findByText("\u{1F600}.txt");
  expect(screen.getByText("\uE000.txt")).toBeTruthy();
  expect(screen.queryByRole("alert")).toBeNull();
});

it.each(["foreign", "mixed", "truncated", "revision", "untracked", "head", "extra"])("rejects malformed diff observation %s", async (change) => {
  const f = fixture(); const original = f.read.getMockImplementation()!;
  f.read.mockImplementation(async (request) => {
    const r = await original(request), q = JSON.parse(new TextDecoder().decode(request.queryJson));
    if(q.operation === "roots") return r;
    const value = JSON.parse(new TextDecoder().decode(r.documentJson));
    if(change === "foreign") value.diff.repository_id = newRequestId();
    if(change === "mixed") value.text = "file content";
    if(change === "truncated") value.truncated = true;
    if(change === "revision") value.diff.revision = "invalid";
    if(change === "untracked") value.diff.untracked = ["../outside"];
    if(change === "head") value.diff.head_commit = "unknown";
    if(change === "extra") value.diff.authority = true;
    return { documentJson: encode(value) };
  });
  render(<f.View />);
  await screen.findByRole("alert");
  expect(screen.queryByRole("region", { name: "Git comparison" })).toBeNull();
});
