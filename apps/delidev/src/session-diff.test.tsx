import { useEffect, useState, useSyncExternalStore } from "react";
import { createRouterTransport, ConnectError, Code } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  ErrorDetailSchema,
  ResourceService,
  SessionQuery,
  SessionService,
  newRequestId,
  type DeleteLocalReviewCommentRequest,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { SessionDiff } from "./session-diff";
import { Comparison } from "./session-diff-model";
import {
  SessionTabKind,
  SessionTabsStore,
  sessionTabKey,
} from "./session-tabs";
import { MutationIntents, useRetainedMutation } from "./mutation";

function fixture(worktree = true) {
  const sessionId = newRequestId(),
    primary = newRequestId(),
    other = newRequestId();
  const reply = (value: object) => ({
    documentJson: encode({
      size: "0",
      binary: false,
      truncated: false,
      ...value,
    }),
  });
  const read = vi.fn(async (request: { queryJson: Uint8Array }) => {
    const q = JSON.parse(new TextDecoder().decode(request.queryJson));
    if (q.operation === "roots")
      return reply({
        roots: [
          { repository_id: other, name: "Other", primary: false },
          { repository_id: primary, name: "Primary", primary: true },
        ],
      });
    if (q.operation === "git-diff-options")
      return reply({
        diff_options: {
          version: 1,
          repository_id: q.repository_id,
          choices: [
            {
              reference: { type: "local-branch", name: "main" },
              available: true,
              configured: false,
            },
          ],
          default: { type: "local-branch", name: "main" },
        },
      });
    return reply({
      diff: {
        comparison: q.comparison,
        repository_id: q.repository_id,
        path: q.path,
        base: "commit",
        base_object: "a".repeat(40),
        head_commit: "a".repeat(40),
        patch: "+<script>doNotRun()</script>\n",
        untracked: ["new.txt"],
        revision: "b".repeat(64),
        ...(q.comparison === "branch"
          ? {
              base_ref: q.base_ref,
              base_commit: "a".repeat(40),
              merge_base: "a".repeat(40),
            }
          : {}),
      },
    });
  });
  const readContext = vi.fn(
    async (_request: {
      queryJson: Uint8Array;
    }): Promise<{ documentJson: Uint8Array }> => {
      throw new ConnectError("unsupported context", Code.Unimplemented);
    },
  );
  const deleteLocalReviewComment = vi.fn(
    (
      _request: DeleteLocalReviewCommentRequest,
    ): Promise<{ id: string; requestId: string }> => new Promise(() => {}),
  );
  const transport = createRouterTransport((router) => {
    router.service(SessionService, {
      readSessionWorkspace: read,
      readSessionReviewContext: readContext,
      deleteLocalReviewComment,
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [] }),
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function SeedPendingDeletion() {
    const mutation = useRetainedMutation(
      `review:delete:${sessionId}:comment`,
      SessionQuery.deleteLocalReviewComment,
    );
    useEffect(() => {
      void mutation.send({
        sessionId,
        mutation: {
          id: "comment",
          expectedRevision: 1n,
          requestId: newRequestId(),
        },
      });
    }, []);
    return null;
  }
  function View({ seed = false }: { seed?: boolean } = {}) {
    const [open, setOpen] = useState(true),
      [draft, setDraft] = useState("unsent input");
    return (
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <MutationIntents>
            {seed ? <SeedPendingDeletion /> : null}
            <label>
              Draft
              <input value={draft} onChange={(e) => setDraft(e.target.value)} />
            </label>
            {open ? (
              <SessionDiff
                sessionId={sessionId}
                worktree={worktree}
                close={() => setOpen(false)}
              />
            ) : null}
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
    );
  }
  return {
    View,
    sessionId,
    transport,
    primary,
    other,
    read,
    client,
    reply,
    deleteLocalReviewComment,
    readContext,
  };
}

it("compares the selected repository and negotiated branch base with inert patch text", async () => {
  const f = fixture();
  const { container } = render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  expect(container.querySelector("script, iframe, a")).toBeNull();
  expect(
    JSON.parse(new TextDecoder().decode(f.read.mock.calls[2][0].queryJson)),
  ).toMatchObject({
    operation: "git-diff",
    repository_id: f.primary,
    comparison: "branch",
    base_ref: { type: "local-branch", name: "main" },
    path: ".",
  });
  expect(screen.getByText("new.txt")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() =>
    expect(
      JSON.parse(
        new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson),
      ).comparison,
    ).toBe("staged"),
  );
  fireEvent.click(screen.getByRole("button", { name: "Diff options" }));
  fireEvent.change(screen.getByLabelText("Relative diff path"), {
    target: { value: "src/file.ts" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Compare path" }));
  await waitFor(() =>
    expect(
      JSON.parse(
        new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson),
      ).path,
    ).toBe("src/file.ts"),
  );
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  await waitFor(() =>
    expect(
      JSON.parse(
        new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson),
      ),
    ).toMatchObject({
      repository_id: f.other,
      comparison: "branch",
      path: ".",
    }),
  );
});

it("keeps Local comparisons tied to Git and preserves drafts while discarding closed observations", async () => {
  const f = fixture(false);
  render(<f.View />);
  await screen.findByRole("region", { name: "Git comparison" });
  expect(
    screen.queryByRole("option", { name: "Since session creation" }),
  ).toBeNull();
  expect((screen.getByLabelText("Comparison") as HTMLSelectElement).value).toBe(
    "branch",
  );
  f.read.mockRejectedValueOnce(
    new ConnectError("Worker unavailable", Code.Unavailable),
  );
  fireEvent.click(screen.getByRole("button", { name: "Refresh diff" }));
  await screen.findByText(
    "Refresh failed. The previous observation is stale and shown below.",
  );
  fireEvent.keyDown(
    screen.getByRole("complementary", { name: "Session Git diff" }),
    { key: "Escape" },
  );
  expect(screen.queryByRole("complementary")).toBeNull();
  expect((screen.getByLabelText("Draft") as HTMLInputElement).value).toBe(
    "unsent input",
  );
  await waitFor(() =>
    expect(f.client.getQueryCache().getAll()).toHaveLength(0),
  );
});

it("does not request a repository diff for a projectless root", async () => {
  const f = fixture();
  f.read.mockResolvedValue(
    f.reply({ roots: [{ name: "General Chat", primary: true }] }),
  );
  render(<f.View />);
  await screen.findByText("This workspace has no prepared Git repository.");
  expect(f.read).toHaveBeenCalledTimes(1);
});

it("keeps session-scoped deletion recovery visible when the diff has no data", async () => {
  const f = fixture();
  f.read.mockImplementation(async (request) => {
    const query = JSON.parse(new TextDecoder().decode(request.queryJson));
    if (query.operation === "roots")
      return f.reply({
        roots: [{ repository_id: f.primary, name: "Primary", primary: true }],
      });
    throw new ConnectError("Worker unavailable", Code.Unavailable);
  });
  const view = render(<f.View seed />);
  await screen.findByText("Waiting for comment deletion acknowledgement…");
  expect(screen.queryByRole("region", { name: "Git comparison" })).toBeNull();
  view.unmount();
});

it("refreshes only review resources after recovered deletion", async () => {
  const f = fixture();
  f.deleteLocalReviewComment.mockRejectedValueOnce(
    new ConnectError("Response lost", Code.Unavailable),
  );
  render(<f.View seed />);
  await screen.findByRole("button", {
    name: "Retry original comment deletion",
  });
  await screen.findByText("+<script>doNotRun()</script>");
  const readsBeforeRetry = f.read.mock.calls.length;
  f.deleteLocalReviewComment.mockImplementation(
    async (request: DeleteLocalReviewCommentRequest) => ({
      id: request.mutation?.id ?? "",
      requestId: request.mutation?.requestId ?? "",
    }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Retry original comment deletion" }),
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Retry original comment deletion" }),
    ).toBeNull(),
  );
  expect(f.read).toHaveBeenCalledTimes(readsBeforeRetry);
});

it("preserves the Worker's UTF-8 filename ordering across supplementary characters", async () => {
  const f = fixture(),
    original = f.read.getMockImplementation()!;
  f.read.mockImplementation(async (request) => {
    const r = await original(request),
      value = JSON.parse(new TextDecoder().decode(r.documentJson));
    if (value.diff) value.diff.untracked = ["\uE000.txt", "\u{1F600}.txt"];
    return { documentJson: encode(value) };
  });
  render(<f.View />);
  await screen.findByText("\u{1F600}.txt");
  expect(screen.getByText("\uE000.txt")).toBeTruthy();
  expect(screen.queryByRole("alert")).toBeNull();
});

it.each([
  "foreign",
  "mixed",
  "truncated",
  "revision",
  "untracked",
  "head",
  "extra",
])("rejects malformed diff observation %s", async (change) => {
  const f = fixture();
  const original = f.read.getMockImplementation()!;
  f.read.mockImplementation(async (request) => {
    const r = await original(request),
      q = JSON.parse(new TextDecoder().decode(request.queryJson));
    if (q.operation !== "git-diff") return r;
    const value = JSON.parse(new TextDecoder().decode(r.documentJson));
    if (change === "foreign") value.diff.repository_id = newRequestId();
    if (change === "mixed") value.text = "file content";
    if (change === "truncated") value.truncated = true;
    if (change === "revision") value.diff.revision = "invalid";
    if (change === "untracked") value.diff.untracked = ["../outside"];
    if (change === "head") value.diff.head_commit = "unknown";
    if (change === "extra") value.diff.authority = true;
    return { documentJson: encode(value) };
  });
  render(<f.View />);
  await screen.findByRole("alert");
  expect(screen.queryByRole("region", { name: "Git comparison" })).toBeNull();
});

it("automatically observes the Diff entry and opens explicit base-aware descriptors", async () => {
  const f = fixture();
  const open = vi.fn();
  render(
    <TransportProvider transport={f.transport}>
      <QueryClientProvider client={f.client}>
        <MutationIntents>
          <SessionDiff
            sessionId={f.sessionId}
            worktree
            close={() => {}}
            openComparison={open}
          />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  await screen.findByText("+<script>doNotRun()</script>");
  expect(
    f.read.mock.calls.some(
      ([request]) =>
        JSON.parse(new TextDecoder().decode(request.queryJson)).operation ===
        "git-diff-options",
    ),
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Diff options" }));
  fireEvent.change(screen.getByLabelText("Relative diff path"), {
    target: { value: "src/file.ts" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Compare path" }));
  expect(open).toHaveBeenLastCalledWith({
    repository: f.primary,
    comparison: "branch",
    path: "src/file.ts",
    base_ref: { type: "local-branch", name: "main" },
  });
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  // The repository switch opens its descriptor only after that repository's
  // own base is negotiated, and always with the exact typed base.
  await waitFor(() =>
    expect(open).toHaveBeenLastCalledWith({
      repository: f.other,
      comparison: "branch",
      path: ".",
      base_ref: { type: "local-branch", name: "main" },
    }),
  );
  expect(open).toHaveBeenCalledTimes(2);
});

it("negotiates options before branch fields and preserves legacy comparisons on old peers", async () => {
  const f = fixture(),
    original = f.read.getMockImplementation()!;
  f.read.mockImplementation(async (request) => {
    const q = JSON.parse(new TextDecoder().decode(request.queryJson));
    if (q.operation === "git-diff-options")
      throw new ConnectError("old query", Code.InvalidArgument);
    return original(request);
  });
  render(<f.View />);
  await screen.findByText(/Branch comparison is unavailable/);
  expect(
    f.read.mock.calls.every(([request]) => {
      const q = JSON.parse(new TextDecoder().decode(request.queryJson));
      return !q.base_ref && q.comparison !== "branch";
    }),
  ).toBe(true);
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "working-tree" },
  });
  await screen.findByText("+<script>doNotRun()</script>");
  const q = JSON.parse(
    new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson),
  );
  expect(q.comparison).toBe("working-tree");
  expect(q.base_ref).toBeUndefined();
});

it("keeps a missing saved base unavailable until an explicit local selection", async () => {
  const f = fixture(),
    original = f.read.getMockImplementation()!;
  f.read.mockImplementation(async (request) => {
    const q = JSON.parse(new TextDecoder().decode(request.queryJson));
    if (q.operation === "git-diff-options")
      return f.reply({
        diff_options: {
          version: 1,
          repository_id: q.repository_id,
          choices: [
            {
              reference: { type: "local-branch", name: "missing" },
              configured: true,
              available: false,
            },
            {
              reference: { type: "local-branch", name: "other" },
              configured: false,
              available: true,
            },
          ],
        },
      });
    return original(request);
  });
  render(<f.View />);
  await screen.findByText(/No available default base/);
  expect(f.read.mock.calls).toHaveLength(2);
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: JSON.stringify(["local-branch", "other", undefined]) },
  });
  await screen.findByText("+<script>doNotRun()</script>");
  const q = JSON.parse(
    new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson),
  );
  expect(q.base_ref).toEqual({ type: "local-branch", name: "other" });
});

it("applies the path explicitly and closes overflow before the pane with focus return", async () => {
  const f = fixture();
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  const trigger = screen.getByRole("button", { name: "Diff options" });
  fireEvent.click(trigger);
  const reads = f.read.mock.calls.length;
  fireEvent.change(screen.getByLabelText("Relative diff path"), {
    target: { value: "src" },
  });
  expect(f.read).toHaveBeenCalledTimes(reads);
  fireEvent.keyDown(screen.getByRole("dialog", { name: "Diff options" }), {
    key: "Escape",
  });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(trigger);
  expect(screen.getByRole("complementary")).toBeTruthy();
});

it("presents the diff options as an anchored popup that reserves no space and reads nothing", async () => {
  const f = fixture();
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  const reads = f.read.mock.calls.length;
  const trigger = screen.getByRole("button", { name: "Diff options" });
  expect(screen.queryByRole("dialog", { name: "Diff options" })).toBeNull();
  fireEvent.click(trigger);
  const dialog = screen.getByRole("dialog", { name: "Diff options" });
  expect(dialog.getAttribute("popover")).toBe("manual");
  expect(dialog.classList.contains("anchored-popup")).toBe(true);
  expect(document.activeElement).toBe(
    screen.getByLabelText("Relative diff path"),
  );
  expect(f.read).toHaveBeenCalledTimes(reads);
  fireEvent.click(trigger);
  expect(screen.queryByRole("dialog", { name: "Diff options" })).toBeNull();
  expect(f.read).toHaveBeenCalledTimes(reads);
  // An outside pointer press dismisses it while the typed draft is retained.
  fireEvent.click(trigger);
  fireEvent.change(screen.getByLabelText("Relative diff path"), {
    target: { value: "src" },
  });
  fireEvent.pointerDown(document.body);
  expect(screen.queryByRole("dialog", { name: "Diff options" })).toBeNull();
  fireEvent.click(trigger);
  expect(
    (screen.getByLabelText("Relative diff path") as HTMLInputElement).value,
  ).toBe("src");
  expect(f.read).toHaveBeenCalledTimes(reads);
});

it("renders validated ordinary coordinates and keeps unsupported patches inert", async () => {
  const f = fixture(),
    original = f.read.getMockImplementation()!;
  const patch =
    "diff --git a/file.txt b/file.txt\nindex aaaaaaa..bbbbbbb 100644\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+<script>new</script>\n";
  f.read.mockImplementation(async (request) => {
    const r = await original(request),
      v = JSON.parse(new TextDecoder().decode(r.documentJson));
    if (v.diff) v.diff.patch = patch;
    return { documentJson: encode(v) };
  });
  f.readContext.mockImplementation(async (request) => {
    const r = await f.read(request),
      v = JSON.parse(new TextDecoder().decode(r.documentJson));
    return {
      documentJson: encode({
        diff: v.diff,
        files: [
          {
            path: "file.txt",
            kind: "text",
            digest: "c".repeat(64),
            lines: [
              { old: 1, text: "old", newline: true, hunk: 1 },
              { new: 1, text: "<script>new</script>", newline: true, hunk: 1 },
            ],
          },
        ],
      }),
    };
  });
  const view = render(<f.View />);
  await screen.findByRole("table", { name: "file.txt" });
  expect(view.container.querySelector("script")).toBeNull();
  expect(view.container.querySelectorAll(".diff-added")).toHaveLength(1);
  expect(view.container.querySelectorAll(".diff-removed")).toHaveLength(1);
  expect(
    [...view.container.querySelectorAll(".diff-line-number")].map(
      (node) => node.textContent,
    ),
  ).toEqual(["1", "", "", "1"]);
});

type ContextLine = {
  old?: number;
  new?: number;
  text: string;
  newline: boolean;
};

function patchFixture(
  patch: string,
  files: { kind: "text" | "non-line"; lines: ContextLine[] }[],
) {
  const f = fixture(),
    original = f.read.getMockImplementation()!;
  f.read.mockImplementation(async (request) => {
    const r = await original(request),
      v = JSON.parse(new TextDecoder().decode(r.documentJson));
    if (v.diff) v.diff.patch = patch;
    return { documentJson: encode(v) };
  });
  f.readContext.mockImplementation(async (request) => {
    const r = await f.read(request),
      v = JSON.parse(new TextDecoder().decode(r.documentJson));
    return {
      documentJson: encode({
        diff: v.diff,
        files: files.map((file) => ({
          path: "file.txt",
          kind: file.kind,
          digest: "c".repeat(64),
          lines: file.lines.map((line) => ({ ...line, hunk: 1 })),
        })),
      }),
    };
  });
  return f;
}

const textPatch = (header: string, body: string) =>
  `diff --git a/file.txt b/file.txt\n${header}index abcdef0..abcdef1\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n${body}`;
const rawPatch = (view: { container: HTMLElement }) =>
  view.container.querySelector("pre")?.textContent;
// The pending-context fallback also renders the raw patch, so wait for the
// context read to resolve and the query result to commit before asserting.
async function contextSettled(read: {
  mock: { results: { value: unknown }[] };
}) {
  await waitFor(() => expect(read.mock.results.length).toBeGreaterThan(0));
  await Promise.allSettled(read.mock.results.map((r) => r.value));
  await new Promise((resolve) => setTimeout(resolve, 50));
}
const NO_NEWLINE = "\\ No newline at end of file\n";

it.each([
  ["old mode 100644\nnew mode 100755\n"],
  ["old mode 100755\nnew mode 100644\n"],
])("keeps mode facts visible for a text edit with %j", async (header) => {
  const patch = textPatch(header, "-old\n+new\n");
  const f = patchFixture(patch, [
    {
      kind: "text",
      lines: [
        { old: 1, text: "old", newline: true },
        { new: 1, text: "new", newline: true },
      ],
    },
  ]);
  const view = render(<f.View />);
  await contextSettled(f.readContext);
  expect(rawPatch(view)).toBe(patch);
  expect(screen.queryByRole("table", { name: "file.txt" })).toBeNull();
});

it("keeps a newline-only change distinguishable in the exact patch", async () => {
  const patch = textPatch("", `-same\n+same\n${NO_NEWLINE}`);
  const f = patchFixture(patch, [
    {
      kind: "text",
      lines: [
        { old: 1, text: "same", newline: true },
        { new: 1, text: "same", newline: false },
      ],
    },
  ]);
  const view = render(<f.View />);
  await contextSettled(f.readContext);
  expect(rawPatch(view)).toBe(patch);
  expect(rawPatch(view)).toContain(NO_NEWLINE.trim());
  expect(screen.queryByRole("table", { name: "file.txt" })).toBeNull();
});

it("keeps missing final newlines on both sides and context in the exact patch", async () => {
  const bothSides = textPatch("", `-old\n${NO_NEWLINE}+new\n${NO_NEWLINE}`);
  const f = patchFixture(bothSides, [
    {
      kind: "text",
      lines: [
        { old: 1, text: "old", newline: false },
        { new: 1, text: "new", newline: false },
      ],
    },
  ]);
  const first = render(<f.View />);
  await contextSettled(f.readContext);
  expect(rawPatch(first)).toBe(bothSides);
  first.unmount();
  const context = textPatch("", ` kept\n${NO_NEWLINE}`);
  const g = patchFixture(context, [
    {
      kind: "text",
      lines: [{ old: 1, new: 1, text: "kept", newline: false }],
    },
  ]);
  const second = render(<g.View />);
  await contextSettled(g.readContext);
  expect(rawPatch(second)).toBe(context);
  expect(screen.queryByRole("table", { name: "file.txt" })).toBeNull();
});

it("keeps structured coordinates for text edits without mode changes or missing newlines", async () => {
  const patch = textPatch("new file mode 100644\n", "-old\n+new\n");
  const f = patchFixture(patch, [
    {
      kind: "text",
      lines: [
        { old: 1, text: "old", newline: true },
        { new: 1, text: "new", newline: true },
      ],
    },
  ]);
  const view = render(<f.View />);
  await screen.findByRole("table", { name: "file.txt" });
  expect(rawPatch(view)).toBeUndefined();
});

it("keeps the exact fallback for mode-only, non-line and failed context", async () => {
  const modeOnly =
    "diff --git a/file.txt b/file.txt\nold mode 100644\nnew mode 100755\n";
  const f = patchFixture(modeOnly, [{ kind: "non-line", lines: [] }]);
  const view = render(<f.View />);
  await contextSettled(f.readContext);
  expect(rawPatch(view)).toBe(modeOnly);
  view.unmount();
  f.readContext.mockClear();
  f.readContext.mockImplementation(async () => {
    throw new ConnectError("failed", Code.Unavailable);
  });
  const failed = render(<f.View />);
  await contextSettled(f.readContext);
  expect(rawPatch(failed)).toBe(modeOnly);
});

type RefChoice = { name: string; available?: boolean; configured?: boolean };

function inventoryFixture(names: RefChoice[], defaultName?: string) {
  const f = fixture(),
    original = f.read.getMockImplementation()!;
  const state = { choices: names, defaultName };
  f.read.mockImplementation(async (request) => {
    const q = JSON.parse(new TextDecoder().decode(request.queryJson));
    if (q.operation === "git-diff-options")
      return f.reply({
        diff_options: {
          version: 1,
          repository_id: q.repository_id,
          choices: state.choices.map((choice) => ({
            reference: { type: "local-branch", name: choice.name },
            available: choice.available ?? true,
            configured: choice.configured ?? false,
          })),
          ...(state.defaultName
            ? {
                default: { type: "local-branch", name: state.defaultName },
              }
            : {}),
        },
      });
    return original(request);
  });
  const operations = () =>
    f.read.mock.calls.map(([request]) => {
      const q = JSON.parse(new TextDecoder().decode(request.queryJson));
      return q.operation === "git-diff"
        ? `git-diff:${q.comparison}:${q.base_ref?.name ?? ""}`
        : (q.operation as string);
    });
  return { f, state, operations };
}

const refreshDiff = () =>
  fireEvent.click(screen.getByRole("button", { name: "Refresh diff" }));

it("reads fresh branch options before the diff on Refresh and exposes new references", async () => {
  const { f, state, operations } = inventoryFixture([{ name: "main" }], "main");
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  expect(operations()).toEqual([
    "roots",
    "git-diff-options",
    "git-diff:branch:main",
  ]);
  state.choices = [{ name: "main" }, { name: "new-base" }];
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(5));
  expect(operations().slice(3)).toEqual([
    "git-diff-options",
    "git-diff:branch:main",
  ]);
  await waitFor(() =>
    expect(
      screen.getByRole("option", { name: "new-base" }) as HTMLOptionElement,
    ).toBeTruthy(),
  );
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe(
    JSON.stringify(["local-branch", "main", null]),
  );
});

it("marks a deleted explicit base unavailable without a missing-base diff or fallback", async () => {
  const { f, state, operations } = inventoryFixture(
    [{ name: "main" }, { name: "other" }],
    "main",
  );
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: JSON.stringify(["local-branch", "other", undefined]) },
  });
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:branch:other"),
  );
  state.choices = [{ name: "main" }];
  refreshDiff();
  await screen.findByText(/No available default base/);
  const settled = operations().length;
  expect(operations().slice(-1)).toEqual(["git-diff-options"]);
  expect(screen.queryByRole("option", { name: "other" })).toBeNull();
  expect(screen.queryByRole("region", { name: "Git comparison" })).toBeNull();
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(settled + 1));
  expect(operations().slice(settled)).toEqual(["git-diff-options"]);
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: JSON.stringify(["local-branch", "main", undefined]) },
  });
  await screen.findByRole("region", { name: "Git comparison" });
  expect(operations().at(-1)).toBe("git-diff:branch:main");
});

const baseValue = (name: string) =>
  JSON.stringify(["local-branch", name, undefined]);
const comparisonRegion = () =>
  screen.queryByRole("region", { name: "Git comparison" });
const diffReads = (operations: () => string[]) =>
  operations().filter((op) => op.startsWith("git-diff:"));

// Selects `other` explicitly, then reports it as `invalid` in a fresh inventory.
async function invalidateOther(
  invalid: RefChoice[],
  setup: ReturnType<typeof inventoryFixture>,
) {
  const { f, state, operations } = setup;
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:branch:other"),
  );
  state.choices = invalid;
  refreshDiff();
  await screen.findByText(/No available default base/);
  expect(comparisonRegion()).toBeNull();
}

it.each([
  ["absent", [{ name: "main" }]],
  ["unavailable", [{ name: "main" }, { name: "other", available: false }]],
])(
  "keeps an explicit base invalidated when it is %s and later reappears until reselected",
  async (_label, invalid) => {
    const setup = inventoryFixture(
      [{ name: "main" }, { name: "other" }],
      "main",
    );
    const { f, state, operations } = setup;
    await invalidateOther(invalid, setup);
    state.choices = [{ name: "main" }, { name: "other" }];
    const settled = operations().length;
    refreshDiff();
    await waitFor(() =>
      expect(screen.getByRole("option", { name: "other" })).toBeTruthy(),
    );
    refreshDiff();
    await waitFor(() => expect(operations()).toHaveLength(settled + 2));
    expect(operations().slice(settled)).toEqual([
      "git-diff-options",
      "git-diff-options",
    ]);
    expect(comparisonRegion()).toBeNull();
    expect(screen.getByText(/No available default base/)).toBeTruthy();
    expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe("");
    // Reselecting the recreated reference itself resumes the comparison.
    fireEvent.change(screen.getByLabelText("Base"), {
      target: { value: baseValue("other") },
    });
    await screen.findByRole("region", { name: "Git comparison" });
    expect(operations().at(-1)).toBe("git-diff:branch:other");
    const q = JSON.parse(
      new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson),
    );
    expect(q.base_ref).toEqual({ type: "local-branch", name: "other" });
  },
);

it("resumes the comparison with a different available base after invalidation", async () => {
  const setup = inventoryFixture([{ name: "main" }, { name: "other" }], "main");
  const { state, operations } = setup;
  await invalidateOther([{ name: "main" }], setup);
  state.choices = [{ name: "main" }, { name: "other" }];
  refreshDiff();
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "other" })).toBeTruthy(),
  );
  expect(comparisonRegion()).toBeNull();
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("main") },
  });
  await screen.findByRole("region", { name: "Git comparison" });
  expect(operations().at(-1)).toBe("git-diff:branch:main");
});

it("does not restore trust through path submission or comparison round trips", async () => {
  const setup = inventoryFixture([{ name: "main" }, { name: "other" }], "main");
  const { state, operations } = setup;
  await invalidateOther([{ name: "main" }], setup);
  state.choices = [{ name: "main" }, { name: "other" }];
  fireEvent.click(screen.getByRole("button", { name: "Diff options" }));
  fireEvent.change(screen.getByLabelText("Relative diff path"), {
    target: { value: "src" },
  });
  // The invalidated base denies path submission instead of restoring trust.
  expect(
    (screen.getByRole("button", { name: "Compare path" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  expect(comparisonRegion()).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Diff options" }));
  refreshDiff();
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "other" })).toBeTruthy(),
  );
  expect(comparisonRegion()).toBeNull();
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() => expect(operations().at(-1)).toBe("git-diff:staged:"));
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "other" })).toBeTruthy(),
  );
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(
    diffReads(operations).filter((op) => op.startsWith("git-diff:branch")),
  ).toEqual(["git-diff:branch:main", "git-diff:branch:other"]);
  expect(comparisonRegion()).toBeNull();
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe("");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await screen.findByRole("region", { name: "Git comparison" });
  expect(operations().at(-1)).toBe("git-diff:branch:other");
});

it("does not invalidate an explicit base from a failed options read", async () => {
  const { f, operations } = inventoryFixture(
    [{ name: "main" }, { name: "other" }],
    "main",
  );
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:branch:other"),
  );
  f.read.mockRejectedValueOnce(
    new ConnectError("Worker unavailable", Code.Unavailable),
  );
  refreshDiff();
  await screen.findByText(
    "Refresh failed. The previous observation is stale and shown below.",
  );
  expect(comparisonRegion()).toBeTruthy();
  const before = operations().length;
  refreshDiff();
  await waitFor(() =>
    expect(operations().slice(before)).toEqual([
      "git-diff-options",
      "git-diff:branch:other",
    ]),
  );
});

it("does not invalidate an explicit base from an obsolete-scope options read", async () => {
  const { f, state, operations } = inventoryFixture(
    [{ name: "main" }, { name: "other" }],
    "main",
  );
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:branch:other"),
  );
  state.choices = [{ name: "main" }];
  const late = holdNextRead(f);
  const before = operations().length;
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(before + 1));
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() => expect(operations().at(-1)).toBe("git-diff:staged:"));
  late.release();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  state.choices = [{ name: "main" }, { name: "other" }];
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  await screen.findByRole("region", { name: "Git comparison" });
  expect(operations().at(-1)).toBe("git-diff:branch:other");
});

it("follows a fresh implicit default after Refresh", async () => {
  const { f, state, operations } = inventoryFixture(
    [{ name: "main" }, { name: "trunk" }],
    "main",
  );
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  state.defaultName = "trunk";
  refreshDiff();
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:branch:trunk"),
  );
  expect(
    operations().filter((op) => op === "git-diff:branch:main"),
  ).toHaveLength(1);
});

it("labels the diff stale and skips the branch diff when the options refresh fails, then recovers", async () => {
  const { f, state, operations } = inventoryFixture([{ name: "main" }], "main");
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  const before = operations().length;
  f.read.mockRejectedValueOnce(
    new ConnectError("Worker unavailable", Code.Unavailable),
  );
  refreshDiff();
  await screen.findByText(
    "Refresh failed. The previous observation is stale and shown below.",
  );
  expect(operations()).toHaveLength(before + 1);
  expect(operations().at(-1)).toBe("git-diff-options");
  expect(screen.getByRole("region", { name: "Git comparison" })).toBeTruthy();
  state.choices = [{ name: "main" }, { name: "next" }];
  refreshDiff();
  await waitFor(() =>
    expect(operations().slice(before + 1)).toEqual([
      "git-diff-options",
      "git-diff:branch:main",
    ]),
  );
  await waitFor(() =>
    expect(
      screen.queryByText(
        "Refresh failed. The previous observation is stale and shown below.",
      ),
    ).toBeNull(),
  );
  expect(screen.getByRole("option", { name: "next" })).toBeTruthy();
});

it("fences a late options response after the comparison changes", async () => {
  const { f, operations } = inventoryFixture([{ name: "main" }], "main");
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  const inventory = f.read.getMockImplementation()!;
  let release: () => void = () => {};
  f.read.mockImplementationOnce(async (request) => {
    await new Promise<void>((resolve) => (release = resolve));
    return inventory(request);
  });
  const before = operations().length;
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(before + 1));
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() => expect(operations().at(-1)).toBe("git-diff:staged:"));
  release();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  expect(operations().slice(before)).toEqual([
    "git-diff-options",
    "git-diff:staged:",
  ]);
});

type Gate = { release: () => void; signal?: AbortSignal };

// Holds the next read until released. Queue gates in request order.
function holdNextRead(f: ReturnType<typeof fixture>): Gate {
  const inner = f.read.getMockImplementation()!;
  let release: () => void = () => {};
  const opened = new Promise<void>((resolve) => (release = resolve));
  const gate: Gate = { release: () => release() };
  f.read.mockImplementationOnce(async (request, context?: object) => {
    gate.signal = (context as { signal?: AbortSignal } | undefined)?.signal;
    await opened;
    return inner(request);
  });
  return gate;
}

const addReviewComment = () =>
  screen.getByRole("button", {
    name: "Add review comment",
  }) as HTMLButtonElement;

// Raw options inventories currently held by the query cache, as default names.
function cachedOptionDefaults(client: QueryClient) {
  return client
    .getQueryCache()
    .getAll()
    .flatMap((query) => {
      const data = query.state.data as
        { documentJson?: Uint8Array } | undefined;
      if (!data?.documentJson) return [];
      const value = JSON.parse(new TextDecoder().decode(data.documentJson));
      return value.diff_options
        ? [value.diff_options.default?.name as string]
        : [];
    });
}

it("keeps Add review comment disabled through the Branch Refresh options and diff phases", async () => {
  const { f, operations } = inventoryFixture([{ name: "main" }], "main");
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  await waitFor(() => expect(addReviewComment().disabled).toBe(false));
  const options = holdNextRead(f),
    diff = holdNextRead(f);
  const before = operations().length;
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(before + 1));
  expect(addReviewComment().disabled).toBe(true);
  options.release();
  await waitFor(() => expect(operations()).toHaveLength(before + 2));
  expect(operations().at(-1)).toBe("git-diff:branch:main");
  expect(addReviewComment().disabled).toBe(true);
  diff.release();
  await waitFor(() => expect(addReviewComment().disabled).toBe(false));
});

it("keeps authoring closed until a changed implicit default has a ready comparison", async () => {
  const { f, state, operations } = inventoryFixture(
    [{ name: "main" }, { name: "trunk" }],
    "main",
  );
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  await waitFor(() => expect(addReviewComment().disabled).toBe(false));
  state.defaultName = "trunk";
  const options = holdNextRead(f),
    diff = holdNextRead(f);
  const before = operations().length;
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(before + 1));
  expect(addReviewComment().disabled).toBe(true);
  options.release();
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:branch:trunk"),
  );
  expect(
    screen.queryByRole("button", { name: "Add review comment" }) === null ||
      addReviewComment().disabled,
  ).toBe(true);
  diff.release();
  await waitFor(() => expect(addReviewComment().disabled).toBe(false));
  fireEvent.click(addReviewComment());
  expect(addReviewComment().disabled).toBe(true);
});

it("keeps authoring gated when the options refresh fails or removes the explicit base", async () => {
  const { f, state, operations } = inventoryFixture(
    [{ name: "main" }, { name: "other" }],
    "main",
  );
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: JSON.stringify(["local-branch", "other", undefined]) },
  });
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:branch:other"),
  );
  await waitFor(() => expect(addReviewComment().disabled).toBe(false));
  f.read.mockRejectedValueOnce(
    new ConnectError("Worker unavailable", Code.Unavailable),
  );
  refreshDiff();
  await screen.findByText(
    "Refresh failed. The previous observation is stale and shown below.",
  );
  expect(addReviewComment().disabled).toBe(true);
  const failed = operations().length;
  state.choices = [{ name: "main" }];
  refreshDiff();
  await screen.findByText(/No available default base/);
  expect(operations().slice(failed)).toEqual(["git-diff-options"]);
  expect(
    screen.queryByRole("button", { name: "Add review comment" }),
  ).toBeNull();
});

it("does not gate authoring on options for working-tree Refresh", async () => {
  const { f, operations } = inventoryFixture([{ name: "main" }], "main");
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "working-tree" },
  });
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:working-tree:"),
  );
  await waitFor(() => expect(addReviewComment().disabled).toBe(false));
  const diff = holdNextRead(f);
  const before = operations().length;
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(before + 1));
  expect(operations().at(-1)).toBe("git-diff:working-tree:");
  diff.release();
  await waitFor(() => expect(addReviewComment().disabled).toBe(false));
  expect(operations().slice(before)).toEqual(["git-diff:working-tree:"]);
});

it("does not publish a late options response after the comparison changes and returns", async () => {
  const { f, state, operations } = inventoryFixture(
    [{ name: "main" }, { name: "late-next" }],
    "main",
  );
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  state.defaultName = "late-next";
  const options = holdNextRead(f);
  const before = operations().length;
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(before + 1));
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() => expect(operations().at(-1)).toBe("git-diff:staged:"));
  options.release();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  expect(cachedOptionDefaults(f.client)).not.toContain("late-next");
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  await waitFor(() => expect(operations().at(-1)).toBe("git-diff:branch:main"));
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe(
    JSON.stringify(["local-branch", "main", null]),
  );
  expect(operations()).not.toContain("git-diff:branch:late-next");
  expect(cachedOptionDefaults(f.client)).not.toContain("late-next");
});

// The Diff options form is gated while a refresh is pending, so the path scope
// changes here through a selected tab descriptor, as the tabbed view does.
it("does not publish a late options response after the path changes", async () => {
  const state = { defaultName: "main" };
  const f = tabsFixture({
    options: async (repository) => ({
      documentJson: encode({
        size: "0",
        binary: false,
        truncated: false,
        diff_options: {
          version: 1,
          repository_id: repository,
          choices: ["main", "late-next"].map((name) => ({
            reference: { type: "local-branch", name },
            available: true,
            configured: false,
          })),
          default: { type: "local-branch", name: state.defaultName },
        },
      }),
    }),
  });
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  state.defaultName = "late-next";
  const optionReads = () =>
    f.read.mock.calls.filter(
      ([request]) =>
        JSON.parse(new TextDecoder().decode(request.queryJson)).operation ===
        "git-diff-options",
    ).length;
  const options = holdNextRead(f);
  const before = optionReads();
  refreshDiff();
  await waitFor(() => expect(optionReads()).toBe(before + 1));
  act(() => {
    f.store.open(f.sessionId, {
      kind: SessionTabKind.Comparison,
      repository: f.primary,
      comparison: Comparison.Branch,
      path: "src/main.ts",
      base_ref: { type: "local-branch", name: "main" },
    });
  });
  await waitFor(() =>
    expect(branchReads(f.read).at(-1)?.path).toBe("src/main.ts"),
  );
  options.release();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  expect(
    branchReads(f.read).map((q) => (q.base_ref as { name: string }).name),
  ).not.toContain("late-next");
  expect(cachedOptionDefaults(f.client)).not.toContain("late-next");
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe(
    JSON.stringify(["local-branch", "main", null]),
  );
});

it("does not publish a late options response after the repository changes", async () => {
  const { f, state, operations } = inventoryFixture(
    [{ name: "main" }, { name: "late-next" }],
    "main",
  );
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  state.defaultName = "late-next";
  const options = holdNextRead(f);
  const before = operations().length;
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(before + 1));
  state.defaultName = "main";
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  await waitFor(() => expect(operations().at(-1)).toBe("git-diff:branch:main"));
  options.release();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  expect(operations()).not.toContain("git-diff:branch:late-next");
  expect(cachedOptionDefaults(f.client)).not.toContain("late-next");
});

it("does not publish a late options response after the diff is disposed", async () => {
  const { f, state, operations } = inventoryFixture(
    [{ name: "main" }, { name: "late-next" }],
    "main",
  );
  const view = render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  state.defaultName = "late-next";
  const options = holdNextRead(f);
  const before = operations().length;
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(before + 1));
  view.unmount();
  options.release();
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(operations()).toHaveLength(before + 1);
  expect(cachedOptionDefaults(f.client)).toEqual([]);
  expect(f.client.getQueryCache().getAll()).toHaveLength(0);
});

it("accepts a fresh options response for the unchanged scope and drops a failed one", async () => {
  const { f, state, operations } = inventoryFixture(
    [{ name: "main" }, { name: "trunk" }],
    "main",
  );
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  state.defaultName = "trunk";
  refreshDiff();
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:branch:trunk"),
  );
  expect(cachedOptionDefaults(f.client)).toEqual(["trunk"]);
  const settled = operations().length;
  f.read.mockRejectedValueOnce(
    new ConnectError("Worker unavailable", Code.Unavailable),
  );
  state.defaultName = "main";
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  refreshDiff();
  await screen.findByText(
    "Refresh failed. The previous observation is stale and shown below.",
  );
  expect(operations().slice(settled)).toEqual(["git-diff-options"]);
  expect(cachedOptionDefaults(f.client)).toEqual(["trunk"]);
});

it("does not publish a late options response after returning to the original scope", async () => {
  const { f, state, operations } = inventoryFixture(
    [{ name: "main" }, { name: "late-next" }],
    "main",
  );
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  state.defaultName = "late-next";
  const options = holdNextRead(f);
  const before = operations().length;
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(before + 1));
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() => expect(operations().at(-1)).toBe("git-diff:staged:"));
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  await waitFor(() => expect(operations().at(-1)).toBe("git-diff:branch:main"));
  // The scope equals the originating scope again, but the retired request
  // must stay retired.
  options.release();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  expect(operations()).not.toContain("git-diff:branch:late-next");
  expect(cachedOptionDefaults(f.client)).not.toContain("late-next");
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe(
    JSON.stringify(["local-branch", "main", null]),
  );
});

it("keeps the stale marker when a retry is retired before it is accepted", async () => {
  const { f, operations } = inventoryFixture([{ name: "main" }], "main");
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  const stale =
    "Refresh failed. The previous observation is stale and shown below.";
  f.read.mockRejectedValueOnce(
    new ConnectError("Worker unavailable", Code.Unavailable),
  );
  refreshDiff();
  await screen.findByText(stale);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  const retry = holdNextRead(f);
  const before = operations().length;
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(before + 1));
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() => expect(operations().at(-1)).toBe("git-diff:staged:"));
  retry.release();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  await screen.findByText(stale);
  expect(addReviewComment().disabled).toBe(true);
});

it("logs only a sanitized operation and closed classification for a failed options refresh", async () => {
  const { f } = inventoryFixture([{ name: "main" }], "main");
  const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  try {
    render(<f.View />);
    await screen.findByText("+<script>doNotRun()</script>");
    f.read.mockRejectedValueOnce(
      new ConnectError("native secret /home/user/repo", Code.Unavailable),
    );
    refreshDiff();
    await screen.findByText(
      "Refresh failed. The previous observation is stale and shown below.",
    );
    const logged = warn.mock.calls.filter(
      ([event]) => event === "delidev.diff.options_refresh_failed",
    );
    expect(logged).toHaveLength(1);
    expect(JSON.stringify(logged[0])).not.toContain("native secret");
    expect(logged[0][1]).not.toHaveProperty("error");
    // Only the operation and the closed classification are allowed; resource
    // identity such as the session and repository IDs must stay out of logs.
    expect(Object.keys(logged[0][1] as object).sort()).toEqual([
      "classification",
      "operation",
    ]);
    expect(logged[0][1]).toMatchObject({
      operation: "git-diff-options",
      classification: expect.any(String),
    });
  } finally {
    warn.mockRestore();
  }
});

it("cancels the disposable options read when the view closes", async () => {
  const { f, state, operations } = inventoryFixture(
    [{ name: "main" }, { name: "late-next" }],
    "main",
  );
  const view = render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  state.defaultName = "late-next";
  const options = holdNextRead(f);
  const before = operations().length;
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(before + 1));
  expect(options.signal?.aborted).toBe(false);
  view.unmount();
  // The read is cancelled and its entry removed without waiting for the RPC.
  await waitFor(() => expect(options.signal?.aborted).toBe(true));
  expect(f.client.getQueryCache().getAll()).toHaveLength(0);
  options.release();
});

it("cancels the disposable options read when the scope changes", async () => {
  const { f, state, operations } = inventoryFixture(
    [{ name: "main" }, { name: "late-next" }],
    "main",
  );
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  state.defaultName = "late-next";
  const options = holdNextRead(f);
  const before = operations().length;
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(before + 1));
  expect(options.signal?.aborted).toBe(false);
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() => expect(options.signal?.aborted).toBe(true));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  expect(cachedOptionDefaults(f.client)).not.toContain("late-next");
  options.release();
});

it("presents a sanitized typed problem for a failed options refresh until a refresh is accepted", async () => {
  const { f } = inventoryFixture([{ name: "main" }], "main");
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  f.read.mockRejectedValueOnce(
    new ConnectError("Worker unavailable", Code.Unavailable),
  );
  refreshDiff();
  await screen.findByText(
    "Refresh failed. The previous observation is stale and shown below.",
  );
  await screen.findByText("DeliDev could not complete this request.");
  expect(screen.getByText(/This comparison is unavailable\./)).toBeTruthy();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  refreshDiff();
  await waitFor(() =>
    expect(
      screen.queryByText("DeliDev could not complete this request."),
    ).toBeNull(),
  );
  expect(
    screen.queryByText(
      "Refresh failed. The previous observation is stale and shown below.",
    ),
  ).toBeNull();
});

it("renders the correlation ID of a failed options refresh until a refresh is accepted", async () => {
  const { f } = inventoryFixture([{ name: "main" }], "main");
  const correlationId = newRequestId();
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  f.read.mockRejectedValueOnce(
    new ConnectError("Worker unavailable", Code.Unavailable, undefined, [
      {
        desc: ErrorDetailSchema,
        value: {
          code: "unavailable",
          guidance: "Retry the comparison shortly.",
          correlationId,
        },
      },
    ]),
  );
  refreshDiff();
  const reference = await screen.findByText(correlationId);
  expect(reference.parentElement?.textContent).toBe(
    `Reference: ${correlationId}`,
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  refreshDiff();
  await waitFor(() => expect(screen.queryByText(correlationId)).toBeNull());
});

it("presents the current explicit refresh failure over an older options query failure", async () => {
  const { f } = inventoryFixture([{ name: "main" }], "main");
  const current = newRequestId();
  const olderMessage =
    /Branch comparison is unavailable on this server or Worker\./;
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  // The accepted options query retains its data but enters an error state,
  // for example when a reconnect invalidation fails.
  f.read.mockRejectedValueOnce(
    new ConnectError("Unsupported", Code.InvalidArgument),
  );
  await act(async () => {
    await f.client.refetchQueries({
      predicate: (query) => {
        const data = query.state.data as
          { documentJson?: Uint8Array } | undefined;
        return (
          Boolean(data?.documentJson) &&
          new TextDecoder().decode(data!.documentJson).includes("diff_options")
        );
      },
    });
  });
  await screen.findByText(olderMessage);
  f.read.mockRejectedValueOnce(
    new ConnectError("Worker unavailable", Code.Unavailable, undefined, [
      {
        desc: ErrorDetailSchema,
        value: {
          code: "unavailable",
          guidance: "Retry the comparison shortly.",
          correlationId: current,
        },
      },
    ]),
  );
  refreshDiff();
  await screen.findByText(current);
  expect(screen.queryByText(olderMessage)).toBeNull();
  expect(screen.getByText(/This comparison is unavailable\./)).toBeTruthy();
});

it("clears the stale marker when a reconnect invalidation accepts a fresh options response after a failed refresh", async () => {
  const { f, state } = inventoryFixture([{ name: "main" }], "main");
  const stale =
    "Refresh failed. The previous observation is stale and shown below.";
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  f.read.mockRejectedValueOnce(
    new ConnectError("Worker unavailable", Code.Unavailable),
  );
  refreshDiff();
  await screen.findByText(stale);
  expect(addReviewComment().disabled).toBe(true);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  // App.tsx invalidates the active queries after a reconnect; the accepted
  // options query is refetched without any explicit Refresh.
  state.choices = [{ name: "main" }, { name: "next" }];
  await act(async () => {
    await f.client.invalidateQueries({ refetchType: "active" });
  });
  await waitFor(() => expect(screen.queryByText(stale)).toBeNull());
  expect(screen.getByRole("option", { name: "next" })).toBeTruthy();
  await waitFor(() => expect(addReviewComment().disabled).toBe(false));
});

it("retires a pending options refresh when the transport is replaced and keeps the failure marker until the replacement answers", async () => {
  const { f, operations } = inventoryFixture(
    [{ name: "main" }, { name: "trunk" }],
    "main",
  );
  const unavailable = /This comparison is unavailable\./;
  // A same-identity reconnect replaces the transport but retains the client.
  const replacement = createRouterTransport((router) => {
    router.service(SessionService, {
      readSessionWorkspace: f.read,
      readSessionReviewContext: () => {
        throw new ConnectError("unsupported context", Code.Unimplemented);
      },
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [] }),
    });
  });
  function Reconnecting() {
    const [transport, setTransport] = useState(f.transport);
    return (
      <TransportProvider transport={transport}>
        <QueryClientProvider client={f.client}>
          <MutationIntents>
            <button onClick={() => setTransport(replacement)}>Reconnect</button>
            <SessionDiff sessionId={f.sessionId} worktree close={() => {}} />
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
    );
  }
  render(<Reconnecting />);
  await screen.findByText("+<script>doNotRun()</script>");
  // An explicit repository and base keep the view and its scope unchanged
  // across the transport change.
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  await waitFor(() =>
    expect((screen.getByLabelText("Base") as HTMLSelectElement).value).not.toBe(
      "",
    ),
  );
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: JSON.stringify(["local-branch", "trunk", null]) },
  });
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:branch:trunk"),
  );
  f.read.mockRejectedValueOnce(
    new ConnectError("Worker unavailable", Code.Unavailable),
  );
  refreshDiff();
  await screen.findByText(unavailable);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  // Hold the next two options reads: the old transport's, then the
  // replacement's (other reads, such as workspace roots, pass through).
  const inner = f.read.getMockImplementation()!;
  const gates: Gate[] = [];
  const releases: (() => void)[] = [];
  f.read.mockImplementation(async (request, context?: object) => {
    const q = JSON.parse(new TextDecoder().decode(request.queryJson));
    if (q.operation === "git-diff-options" && gates.length < 2) {
      const gate: Gate = {
        release: () => {},
        signal: (context as { signal?: AbortSignal } | undefined)?.signal,
      };
      gates.push(gate);
      await new Promise<void>((resolve) => releases.push(resolve));
    }
    return inner(request);
  });
  refreshDiff();
  await waitFor(() => expect(gates).toHaveLength(1));
  fireEvent.click(screen.getByRole("button", { name: "Reconnect" }));
  await waitFor(() => expect(gates).toHaveLength(2));
  const [old] = gates;
  // The read of the old transport is cancelled and its late answer is fenced:
  // the failure marker stays until the replacement transport answers.
  await waitFor(() => expect(old.signal?.aborted).toBe(true));
  releases[0]();
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
  expect(screen.getByText(unavailable)).toBeTruthy();
  releases[1]();
  await waitFor(() => expect(screen.queryByText(unavailable)).toBeNull());
});

it("keeps working-tree Refresh as a direct diff read without options", async () => {
  const { f, operations } = inventoryFixture([{ name: "main" }], "main");
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "working-tree" },
  });
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:working-tree:"),
  );
  const before = operations().length;
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(before + 1));
  expect(operations().at(-1)).toBe("git-diff:working-tree:");
});

function tabsFixture(
  options: {
    third?: boolean;
    options?: (
      repository: string,
      original: () => Promise<{ documentJson: Uint8Array }>,
    ) => Promise<{ documentJson: Uint8Array }>;
  } = {},
) {
  const f = fixture(),
    store = new SessionTabsStore(),
    third = newRequestId(),
    original = f.read.getMockImplementation()!;
  f.read.mockImplementation(async (request) => {
    const q = JSON.parse(new TextDecoder().decode(request.queryJson));
    if (q.operation === "roots" && options.third) {
      const r = await original(request),
        value = JSON.parse(new TextDecoder().decode(r.documentJson));
      value.roots.push({ repository_id: third, name: "Third", primary: false });
      return { documentJson: encode(value) };
    }
    if (q.operation === "git-diff-options" && options.options)
      return options.options(q.repository_id, () => original(request));
    return original(request);
  });
  const comparisons = () =>
    store
      .snapshot(f.sessionId)
      .tabs.filter((tab) => tab.kind === SessionTabKind.Comparison);
  function Harness() {
    const snapshot = useSyncExternalStore(store.subscribe, () =>
      store.snapshot(f.sessionId),
    );
    const [open, setOpen] = useState(true);
    const tab = snapshot.tabs.find(
      (t) => sessionTabKey(t) === snapshot.selected,
    );
    return (
      <TransportProvider transport={f.transport}>
        <QueryClientProvider client={f.client}>
          <MutationIntents>
            <button onClick={() => setOpen(false)}>Leave Diff</button>
            <button onClick={() => setOpen(true)}>Reopen Diff</button>
            {open ? (
              <SessionDiff
                sessionId={f.sessionId}
                worktree
                close={() => setOpen(false)}
                selected={
                  tab?.kind === SessionTabKind.Comparison ? tab : undefined
                }
                openComparison={(value, options) =>
                  store.openComparison(
                    f.sessionId,
                    { kind: SessionTabKind.Comparison, ...value },
                    options?.inheritDropped,
                  )
                }
                restoreBase={(repository, reference) =>
                  store.clearDroppedBase(f.sessionId, repository, reference)
                }
              />
            ) : null}
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
    );
  }
  return { ...f, store, third, comparisons, Harness };
}

const branchReads = (read: { mock: { calls: unknown[][] } }) =>
  read.mock.calls
    .map(
      ([request]) =>
        JSON.parse(
          new TextDecoder().decode(
            (request as { queryJson: Uint8Array }).queryJson,
          ),
        ) as Record<string, unknown>,
    )
    .filter((q) => q.operation === "git-diff" && q.comparison === "branch");

it("opens the repository-switch tab with the negotiated base and reuses it when that base is selected again", async () => {
  const f = tabsFixture();
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  expect(f.comparisons()).toHaveLength(0);
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  await waitFor(() => expect(f.comparisons()).toHaveLength(1));
  const key = f.store.snapshot(f.sessionId).selected;
  expect(f.comparisons()[0]).toEqual({
    kind: SessionTabKind.Comparison,
    repository: f.other,
    comparison: "branch",
    path: ".",
    base_ref: { type: "local-branch", name: "main" },
  });
  await screen.findByRole("region", { name: "Git comparison" });
  expect(branchReads(f.read).at(-1)).toMatchObject({
    repository_id: f.other,
    base_ref: { type: "local-branch", name: "main" },
  });
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: JSON.stringify(["local-branch", "main", undefined]) },
  });
  expect(f.comparisons()).toHaveLength(1);
  expect(f.store.snapshot(f.sessionId).selected).toBe(key);
  // Switching away and back reuses the exact descriptors.
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.primary },
  });
  await waitFor(() => expect(f.comparisons()).toHaveLength(2));
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  await waitFor(() => expect(f.store.snapshot(f.sessionId).selected).toBe(key));
  expect(f.comparisons()).toHaveLength(2);
});

it("clears a completed repository selection so reselecting its originating tab keeps that tab's repository", async () => {
  const f = tabsFixture();
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  await waitFor(() => expect(f.comparisons()).toHaveLength(1));
  const otherKey = f.store.snapshot(f.sessionId).selected;
  // The next selection originates from the other-repository tab.
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.primary },
  });
  await waitFor(() => expect(f.comparisons()).toHaveLength(2));
  expect(f.store.snapshot(f.sessionId).selected).not.toBe(otherKey);
  await waitFor(() =>
    expect(
      (screen.getByLabelText("Diff repository") as HTMLSelectElement).value,
    ).toBe(f.primary),
  );
  // Reselecting the originating tab must show that tab's own repository.
  act(() => f.store.select(f.sessionId, otherKey));
  await waitFor(() =>
    expect(
      (screen.getByLabelText("Diff repository") as HTMLSelectElement).value,
    ).toBe(f.other),
  );
  expect(f.comparisons()).toHaveLength(2);
});

it("preserves the retained path and takes the base from the new repository, not the previous one", async () => {
  const f = tabsFixture({
    options: async (repository, original) => {
      const r = await original(),
        value = JSON.parse(new TextDecoder().decode(r.documentJson));
      if (repository === f0.other) {
        value.diff_options.choices = [
          {
            reference: {
              type: "remote-branch",
              name: "trunk",
              remote: "origin",
            },
            available: true,
            configured: false,
          },
        ];
        value.diff_options.default = {
          type: "remote-branch",
          name: "trunk",
          remote: "origin",
        };
      }
      return { documentJson: encode(value) };
    },
  });
  const f0 = f;
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.click(screen.getByRole("button", { name: "Diff options" }));
  fireEvent.change(screen.getByLabelText("Relative diff path"), {
    target: { value: "src" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Compare path" }));
  await waitFor(() => expect(f.comparisons()).toHaveLength(1));
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  await waitFor(() => expect(f.comparisons()).toHaveLength(2));
  expect(f.comparisons()[1]).toMatchObject({
    repository: f.other,
    path: "src",
    base_ref: { type: "remote-branch", name: "trunk", remote: "origin" },
  });
  // A different typed base for the same repository/path is a distinct tab.
  expect(f.comparisons()[0]).toMatchObject({
    repository: f.primary,
    base_ref: { type: "local-branch", name: "main" },
  });
});

it("ignores a late negotiation result for a replaced repository", async () => {
  let release: () => void = () => {},
    requested = false;
  const f = tabsFixture({
    third: true,
    options: async (repository, original) => {
      if (repository === f0.other) {
        requested = true;
        await new Promise<void>((resolve) => (release = resolve));
      }
      return original();
    },
  });
  const f0 = f;
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  await waitFor(() => expect(requested).toBe(true));
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.third },
  });
  await waitFor(() => expect(f.comparisons()).toHaveLength(1));
  expect(f.comparisons()[0]).toMatchObject({ repository: f.third });
  release();
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(f.comparisons()).toHaveLength(1);
  expect(f.comparisons().some((tab) => tab.repository === f.other)).toBe(false);
});

it("opens no tab when the Diff presentation is left before negotiation completes", async () => {
  let release: () => void = () => {},
    requested = false;
  const f = tabsFixture({
    options: async (repository, original) => {
      if (repository === f0.other) {
        requested = true;
        await new Promise<void>((resolve) => (release = resolve));
      }
      return original();
    },
  });
  const f0 = f;
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  await waitFor(() => expect(requested).toBe(true));
  fireEvent.click(screen.getByRole("button", { name: "Leave Diff" }));
  release();
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(f.comparisons()).toHaveLength(0);
});

it.each(["unsupported", "failed", "missing-default"])(
  "opens no base-less branch tab and reads no branch for %s negotiation",
  async (mode) => {
    const f = tabsFixture({
      options: async (repository, original) => {
        if (repository !== f0.other) return original();
        if (mode === "unsupported")
          throw new ConnectError("old", Code.InvalidArgument);
        if (mode === "failed") throw new ConnectError("down", Code.Unavailable);
        return f0.reply({
          diff_options: {
            version: 1,
            repository_id: repository,
            choices: [
              {
                reference: { type: "local-branch", name: "other" },
                available: true,
                configured: false,
              },
            ],
          },
        });
      },
    });
    const f0 = f;
    render(<f.Harness />);
    await screen.findByText("+<script>doNotRun()</script>");
    const reads = f.read.mock.calls.length;
    fireEvent.change(screen.getByLabelText("Diff repository"), {
      target: { value: f.other },
    });
    await waitFor(() =>
      expect(
        f.read.mock.calls
          .slice(reads)
          .some(
            ([request]) =>
              JSON.parse(new TextDecoder().decode(request.queryJson))
                .operation === "git-diff-options",
          ),
      ).toBe(true),
    );
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(f.comparisons()).toHaveLength(0);
    expect(
      branchReads(f.read)
        .slice(1)
        .filter((q) => q.repository_id === f.other),
    ).toHaveLength(0);
    if (mode === "missing-default") {
      fireEvent.change(screen.getByLabelText("Base"), {
        target: {
          value: JSON.stringify(["local-branch", "other", undefined]),
        },
      });
      await waitFor(() => expect(f.comparisons()).toHaveLength(1));
      expect(f.comparisons()[0]).toEqual({
        kind: SessionTabKind.Comparison,
        repository: f.other,
        comparison: "branch",
        path: ".",
        base_ref: { type: "local-branch", name: "other" },
      });
    }
  },
);

it("keeps working-tree, staged and creation descriptors base-free", async () => {
  const f = tabsFixture();
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  for (const comparison of ["working-tree", "staged", "creation"]) {
    fireEvent.change(screen.getByLabelText("Comparison"), {
      target: { value: comparison },
    });
    await waitFor(() =>
      expect(f.comparisons().some((tab) => tab.comparison === comparison)).toBe(
        true,
      ),
    );
  }
  expect(f.comparisons()).toHaveLength(3);
  for (const tab of f.comparisons()) expect(tab.base_ref).toBeUndefined();
});

it("keeps an invalidated explicit base unavailable across tabbed comparison switches", async () => {
  const state = { names: ["main", "other"] };
  const f = tabsFixture({
    options: async (repository) => ({
      documentJson: encode({
        size: "0",
        binary: false,
        truncated: false,
        diff_options: {
          version: 1,
          repository_id: repository,
          choices: state.names.map((name) => ({
            reference: { type: "local-branch", name },
            available: true,
            configured: false,
          })),
          default: { type: "local-branch", name: "main" },
        },
      }),
    }),
  });
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await waitFor(() =>
    expect(
      (branchReads(f.read).at(-1)?.base_ref as { name: string }).name,
    ).toBe("other"),
  );
  state.names = ["main"];
  refreshDiff();
  await screen.findByText(/No available default base/);
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() =>
    expect(f.comparisons().some((tab) => tab.comparison === "staged")).toBe(
      true,
    ),
  );
  // Returning to Branch must not fall back to the inventory default.
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "main" })).toBeTruthy(),
  );
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(
    branchReads(f.read).map((q) => (q.base_ref as { name: string }).name),
  ).toEqual(["main", "other"]);
  expect(comparisonRegion()).toBeNull();
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe("");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("main") },
  });
  await screen.findByRole("region", { name: "Git comparison" });
  expect((branchReads(f.read).at(-1)?.base_ref as { name: string }).name).toBe(
    "main",
  );
});

it("omits the raw Git reference from the base invalidation diagnostic", async () => {
  const setup = inventoryFixture(
    [{ name: "main" }, { name: "secret-ref" }],
    "main",
  );
  const debug = vi.spyOn(console, "debug").mockImplementation(() => {});
  try {
    const { f, state, operations } = setup;
    render(<f.View />);
    await screen.findByText("+<script>doNotRun()</script>");
    fireEvent.change(screen.getByLabelText("Base"), {
      target: { value: baseValue("secret-ref") },
    });
    await waitFor(() =>
      expect(operations().at(-1)).toBe("git-diff:branch:secret-ref"),
    );
    state.choices = [{ name: "main" }];
    refreshDiff();
    await screen.findByText(/No available default base/);
    const logged = debug.mock.calls.filter(
      ([event]) => event === "delidev.diff.explicit_base_invalidated",
    );
    expect(logged).toHaveLength(1);
    expect(JSON.stringify(logged[0])).not.toContain("secret-ref");
    expect(logged[0][1]).not.toHaveProperty("base");
  } finally {
    debug.mockRestore();
  }
});

function namedInventory(state: { names: string[] }) {
  return async (repository: string) => ({
    documentJson: encode({
      size: "0",
      binary: false,
      truncated: false,
      diff_options: {
        version: 1,
        repository_id: repository,
        choices: state.names.map((name) => ({
          reference: { type: "local-branch", name },
          available: true,
          configured: false,
        })),
        default: { type: "local-branch", name: "main" },
      },
    }),
  });
}

const branchBaseNames = (read: { mock: { calls: unknown[][] } }) =>
  branchReads(read).map((q) => (q.base_ref as { name: string }).name);

const refreshWhenReady = async () => {
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  refreshDiff();
};

it("retains every invalidated base across retained Branch comparison tabs until each is reselected", async () => {
  const state = { names: ["main", "alpha", "beta"] };
  const f = tabsFixture({ options: namedInventory(state) });
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  for (const name of ["alpha", "beta"]) {
    fireEvent.change(screen.getByLabelText("Base"), {
      target: { value: baseValue(name) },
    });
    await waitFor(() => expect(branchBaseNames(f.read).at(-1)).toBe(name));
  }
  const tabKey = (name: string) =>
    sessionTabKey(
      f
        .comparisons()
        .find(
          (tab) => tab.comparison === "branch" && tab.base_ref?.name === name,
        )!,
    );
  const alpha = tabKey("alpha"),
    beta = tabKey("beta");
  // Invalidate alpha while its tab is displayed.
  act(() => f.store.select(f.sessionId, alpha));
  await waitFor(() =>
    expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe(
      baseValue("alpha"),
    ),
  );
  state.names = ["main", "beta"];
  await refreshWhenReady();
  await screen.findByText(/No available default base/);
  // Then invalidate beta from its own tab; alpha must stay invalidated.
  act(() => f.store.select(f.sessionId, beta));
  await screen.findByRole("region", { name: "Git comparison" });
  state.names = ["main"];
  await refreshWhenReady();
  await screen.findByText(/No available default base/);
  state.names = ["main", "alpha", "beta"];
  await refreshWhenReady();
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "alpha" })).toBeTruthy(),
  );
  const settled = branchReads(f.read).length;
  act(() => f.store.select(f.sessionId, alpha));
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "alpha" })).toBeTruthy(),
  );
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(branchReads(f.read)).toHaveLength(settled);
  expect(comparisonRegion()).toBeNull();
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe("");
  // Reselecting alpha restores only alpha; beta is still invalidated.
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("alpha") },
  });
  await screen.findByRole("region", { name: "Git comparison" });
  expect(branchBaseNames(f.read).at(-1)).toBe("alpha");
  act(() => f.store.select(f.sessionId, beta));
  await waitFor(() =>
    expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe(""),
  );
  const afterAlpha = branchReads(f.read).length;
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(branchReads(f.read)).toHaveLength(afterAlpha);
  expect(comparisonRegion()).toBeNull();
});

it("keeps an invalidated base unavailable after the Diff presentation is left and reopened", async () => {
  const state = { names: ["main", "other"] };
  const f = tabsFixture({ options: namedInventory(state) });
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await waitFor(() => expect(branchBaseNames(f.read).at(-1)).toBe("other"));
  state.names = ["main"];
  await refreshWhenReady();
  await screen.findByText(/No available default base/);
  fireEvent.click(screen.getByRole("button", { name: "Leave Diff" }));
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "Refresh diff" })).toBeNull(),
  );
  // The reference reappears while the Diff presentation is closed.
  state.names = ["main", "other"];
  const settled = branchReads(f.read).length;
  fireEvent.click(screen.getByRole("button", { name: "Reopen Diff" }));
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "other" })).toBeTruthy(),
  );
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(branchReads(f.read)).toHaveLength(settled);
  expect(comparisonRegion()).toBeNull();
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe("");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await screen.findByRole("region", { name: "Git comparison" });
  expect(branchBaseNames(f.read).at(-1)).toBe("other");
});

it("keeps an invalidated base unavailable when it later becomes the inventory default of the generic Diff tab", async () => {
  const state = { names: ["main", "other"], default: "main" };
  const f = tabsFixture({
    options: async (repository) => ({
      documentJson: encode({
        size: "0",
        binary: false,
        truncated: false,
        diff_options: {
          version: 1,
          repository_id: repository,
          choices: state.names.map((name) => ({
            reference: { type: "local-branch", name },
            available: true,
            configured: false,
          })),
          default: { type: "local-branch", name: state.default },
        },
      }),
    }),
  });
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await waitFor(() => expect(branchBaseNames(f.read).at(-1)).toBe("other"));
  state.names = ["main"];
  await refreshWhenReady();
  await screen.findByText(/No available default base/);
  // The reference returns as the inventory default, then the generic Diff tab
  // (which carries no descriptor, so no explicit base) is reopened.
  state.names = ["main", "other"];
  state.default = "other";
  act(() => f.store.open(f.sessionId, { kind: SessionTabKind.Diff }));
  fireEvent.click(screen.getByRole("button", { name: "Leave Diff" }));
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "Refresh diff" })).toBeNull(),
  );
  const settled = branchReads(f.read).length;
  fireEvent.click(screen.getByRole("button", { name: "Reopen Diff" }));
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "other" })).toBeTruthy(),
  );
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(branchReads(f.read)).toHaveLength(settled);
  expect(comparisonRegion()).toBeNull();
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe("");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await screen.findByRole("region", { name: "Git comparison" });
  expect(branchBaseNames(f.read).at(-1)).toBe("other");
});

it("tracks a dropped invalidated base per base-free comparison descriptor", async () => {
  const state = { names: ["main", "other"] };
  const f = tabsFixture({ options: namedInventory(state) });
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  // A retained base-free Working tree tab that predates the invalidation.
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "working-tree" },
  });
  await waitFor(() =>
    expect(
      f.comparisons().some((tab) => tab.comparison === "working-tree"),
    ).toBe(true),
  );
  const workingTree = sessionTabKey(
    f.comparisons().find((tab) => tab.comparison === "working-tree")!,
  );
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  await waitFor(() =>
    expect(f.comparisons().some((tab) => tab.comparison === "branch")).toBe(
      true,
    ),
  );
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await waitFor(() => expect(branchBaseNames(f.read).at(-1)).toBe("other"));
  state.names = ["main"];
  await refreshWhenReady();
  await screen.findByText(/No available default base/);
  // The invalidated base is dropped by a different base-free descriptor.
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() =>
    expect(f.comparisons().some((tab) => tab.comparison === "staged")).toBe(
      true,
    ),
  );
  const staged = sessionTabKey(
    f.comparisons().find((tab) => tab.comparison === "staged")!,
  );
  // Changing the unrelated Working tree tab to Branch must neither consume the
  // dropped base nor receive it: it follows the inventory default.
  act(() => f.store.select(f.sessionId, workingTree));
  await waitFor(() =>
    expect(
      (screen.getByLabelText("Comparison") as HTMLSelectElement).value,
    ).toBe("working-tree"),
  );
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  await waitFor(() =>
    expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe(
      baseValue("main"),
    ),
  );
  await waitFor(() => expect(branchBaseNames(f.read).at(-1)).toBe("main"));
  // Returning from the descriptor that dropped it still republishes the
  // invalidated base itself, which stays unavailable.
  act(() => f.store.select(f.sessionId, staged));
  await waitFor(() =>
    expect(
      (screen.getByLabelText("Comparison") as HTMLSelectElement).value,
    ).toBe("staged"),
  );
  const settled = branchReads(f.read).length;
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  await waitFor(() => {
    const snapshot = f.store.snapshot(f.sessionId),
      tab = snapshot.tabs.find(
        (value) => sessionTabKey(value) === snapshot.selected,
      );
    expect(tab).toMatchObject({
      comparison: "branch",
      base_ref: { name: "other" },
    });
  });
  // The reference reappears, but its invalidation survives the round trip.
  state.names = ["main", "other"];
  await refreshWhenReady();
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "other" })).toBeTruthy(),
  );
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(branchReads(f.read)).toHaveLength(settled);
  expect(comparisonRegion()).toBeNull();
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe("");
});

it("keeps the dropped invalidated base for a retained base-free tab after it returned to Branch once", async () => {
  const state = { names: ["main", "other"] };
  const f = tabsFixture({ options: namedInventory(state) });
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await waitFor(() => expect(branchBaseNames(f.read).at(-1)).toBe("other"));
  state.names = ["main"];
  await refreshWhenReady();
  await screen.findByText(/No available default base/);
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() =>
    expect(f.comparisons().some((tab) => tab.comparison === "staged")).toBe(
      true,
    ),
  );
  const staged = sessionTabKey(
    f.comparisons().find((tab) => tab.comparison === "staged")!,
  );
  // The first return republishes the invalidated base.
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  await waitFor(() =>
    expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe(""),
  );
  // The retained Staged tab still remembers the base it dropped, so a second
  // switch to Branch cannot fall back to the inventory default.
  act(() => f.store.select(f.sessionId, staged));
  await waitFor(() =>
    expect(
      (screen.getByLabelText("Comparison") as HTMLSelectElement).value,
    ).toBe("staged"),
  );
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  await waitFor(() => {
    const snapshot = f.store.snapshot(f.sessionId),
      tab = snapshot.tabs.find(
        (value) => sessionTabKey(value) === snapshot.selected,
      );
    expect(tab).toMatchObject({
      comparison: "branch",
      base_ref: { name: "other" },
    });
  });
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(branchBaseNames(f.read)).toEqual(["main", "other"]);
  expect(comparisonRegion()).toBeNull();
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe("");
});

it("keeps the dropped invalidated base on a retained base-free tab that opens another base-free tab", async () => {
  const state = { names: ["main", "other"] };
  const f = tabsFixture({ options: namedInventory(state) });
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await waitFor(() => expect(branchBaseNames(f.read).at(-1)).toBe("other"));
  state.names = ["main"];
  await refreshWhenReady();
  await screen.findByText(/No available default base/);
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() =>
    expect(f.comparisons().some((tab) => tab.comparison === "staged")).toBe(
      true,
    ),
  );
  const staged = sessionTabKey(
    f.comparisons().find((tab) => tab.comparison === "staged")!,
  );
  // Opening another base-free tab from the retained Staged tab must neither
  // consume the Staged tab's record nor lose the base for the new tab.
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "working-tree" },
  });
  await waitFor(() =>
    expect(
      f.comparisons().some((tab) => tab.comparison === "working-tree"),
    ).toBe(true),
  );
  act(() => f.store.select(f.sessionId, staged));
  await waitFor(() =>
    expect(
      (screen.getByLabelText("Comparison") as HTMLSelectElement).value,
    ).toBe("staged"),
  );
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  await waitFor(() => {
    const snapshot = f.store.snapshot(f.sessionId),
      tab = snapshot.tabs.find(
        (value) => sessionTabKey(value) === snapshot.selected,
      );
    expect(tab).toMatchObject({
      comparison: "branch",
      base_ref: { name: "other" },
    });
  });
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(branchBaseNames(f.read)).toEqual(["main", "other"]);
  expect(comparisonRegion()).toBeNull();
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe("");
});

it("republishes the dropped invalidated base after the options cache is gone and the Diff presentation reopens", async () => {
  const state = { names: ["main", "other"] };
  const f = tabsFixture({ options: namedInventory(state) });
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await waitFor(() => expect(branchBaseNames(f.read).at(-1)).toBe("other"));
  state.names = ["main"];
  await refreshWhenReady();
  await screen.findByText(/No available default base/);
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() =>
    expect(f.comparisons().some((tab) => tab.comparison === "staged")).toBe(
      true,
    ),
  );
  // Leaving the Diff removes every cached options inventory (no cache time).
  fireEvent.click(screen.getByRole("button", { name: "Leave Diff" }));
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "Refresh diff" })).toBeNull(),
  );
  state.names = ["main", "other"];
  fireEvent.click(screen.getByRole("button", { name: "Reopen Diff" }));
  await waitFor(() =>
    expect(
      (screen.getByLabelText("Comparison") as HTMLSelectElement).value,
    ).toBe("staged"),
  );
  const settled = branchReads(f.read).length;
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  // The Branch tab is opened before any options inventory exists and still
  // carries the dropped base instead of adopting the inventory default.
  await waitFor(() => {
    const snapshot = f.store.snapshot(f.sessionId),
      tab = snapshot.tabs.find(
        (value) => sessionTabKey(value) === snapshot.selected,
      );
    expect(tab).toMatchObject({
      comparison: "branch",
      base_ref: { name: "other" },
    });
  });
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "other" })).toBeTruthy(),
  );
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(branchReads(f.read)).toHaveLength(settled);
  expect(comparisonRegion()).toBeNull();
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe("");
});

it("does not republish a dropped invalidated base for a recreated base-free tab after its tab closed", async () => {
  const state = { names: ["main", "other"] };
  const f = tabsFixture({ options: namedInventory(state) });
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await waitFor(() => expect(branchBaseNames(f.read).at(-1)).toBe("other"));
  state.names = ["main"];
  await refreshWhenReady();
  await screen.findByText(/No available default base/);
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() =>
    expect(f.comparisons().some((tab) => tab.comparison === "staged")).toBe(
      true,
    ),
  );
  const staged = f.comparisons().find((tab) => tab.comparison === "staged")!;
  expect(staged).toMatchObject({ dropped_base: { name: "other" } });
  // Closing the tab ends the record with it.
  act(() => f.store.close(f.sessionId, sessionTabKey(staged)));
  state.names = ["main", "other"];
  // An unrelated base-free tab with the same comparison and path is created.
  act(() =>
    f.store.open(f.sessionId, {
      kind: SessionTabKind.Comparison,
      repository: staged.repository,
      comparison: staged.comparison,
      path: staged.path,
    }),
  );
  await waitFor(() =>
    expect(
      (screen.getByLabelText("Comparison") as HTMLSelectElement).value,
    ).toBe("staged"),
  );
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  await waitFor(() => expect(branchBaseNames(f.read).at(-1)).toBe("main"));
  const snapshot = f.store.snapshot(f.sessionId),
    tab = snapshot.tabs.find(
      (value) => sessionTabKey(value) === snapshot.selected,
    );
  expect(tab).toMatchObject({
    comparison: "branch",
    base_ref: { name: "main" },
  });
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe(
    baseValue("main"),
  );
});

it("unmounts an open review editor when its explicit base is invalidated and never revives it on reappearance", async () => {
  const setup = inventoryFixture([{ name: "main" }, { name: "other" }], "main");
  const { f, state, operations } = setup;
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:branch:other"),
  );
  await waitFor(() => expect(addReviewComment().disabled).toBe(false));
  fireEvent.click(addReviewComment());
  const editor = () =>
    screen.queryByRole("form", { name: "New local review comment" });
  expect(editor()).not.toBeNull();
  await new Promise((resolve) => setTimeout(resolve, 50));
  const contextReads = f.readContext.mock.calls.length;
  // The base disappears: the comparison, the editor and its review context
  // read all end together, so no retained form can submit or retry.
  state.choices = [{ name: "main" }];
  refreshDiff();
  await screen.findByText(/No available default base/);
  expect(comparisonRegion()).toBeNull();
  expect(editor()).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Add review comment" }),
  ).toBeNull();
  // The reference reappears, but nothing is restored without a selection.
  state.choices = [{ name: "main" }, { name: "other" }];
  refreshDiff();
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "other" })).toBeTruthy(),
  );
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(editor()).toBeNull();
  expect(comparisonRegion()).toBeNull();
  expect(f.readContext.mock.calls.length).toBe(contextReads);
  expect(f.deleteLocalReviewComment).not.toHaveBeenCalled();
});

it("reads a fresh diff after an invalidated base is explicitly reselected", async () => {
  const setup = inventoryFixture([{ name: "main" }, { name: "other" }], "main");
  const { f, state, operations } = setup;
  // Every diff read observes a different commit for the recreated reference.
  const inner = f.read.getMockImplementation()!;
  let generation = 0;
  f.read.mockImplementation(async (request) => {
    const q = JSON.parse(new TextDecoder().decode(request.queryJson));
    if (q.operation !== "git-diff" || q.base_ref?.name !== "other")
      return inner(request);
    generation += 1;
    const response = await inner(request),
      value = JSON.parse(new TextDecoder().decode(response.documentJson));
    value.diff.revision = String(generation).repeat(64).slice(0, 64);
    value.diff.patch = `+generation-${generation}\n`;
    return { documentJson: encode(value) };
  });
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await screen.findByText("+generation-1");
  state.choices = [{ name: "main" }];
  refreshDiff();
  await screen.findByText(/No available default base/);
  expect(screen.queryByText("+generation-1")).toBeNull();
  state.choices = [{ name: "main" }, { name: "other" }];
  refreshDiff();
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "other" })).toBeTruthy(),
  );
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(screen.queryByText("+generation-1")).toBeNull();
  expect(operations().filter((op) => op === "git-diff:branch:other")).toEqual([
    "git-diff:branch:other",
  ]);
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  // Explicit reselection never shows the pre-deletion observation.
  await screen.findByText("+generation-2");
  expect(screen.queryByText("+generation-1")).toBeNull();
  expect(operations().filter((op) => op === "git-diff:branch:other")).toEqual([
    "git-diff:branch:other",
    "git-diff:branch:other",
  ]);
});

it("ends a retained base-free tab's dropped base record when the reference is explicitly reselected", async () => {
  const state = { names: ["main", "other"] };
  const f = tabsFixture({ options: namedInventory(state) });
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await waitFor(() => expect(branchBaseNames(f.read).at(-1)).toBe("other"));
  const branch = sessionTabKey(
    f
      .comparisons()
      .find(
        (tab) => tab.comparison === "branch" && tab.base_ref?.name === "other",
      )!,
  );
  state.names = ["main"];
  await refreshWhenReady();
  await screen.findByText(/No available default base/);
  // The staged tab drops the invalidated base onto its own descriptor.
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "staged" },
  });
  await waitFor(() =>
    expect(f.comparisons().some((tab) => tab.comparison === "staged")).toBe(
      true,
    ),
  );
  const staged = sessionTabKey(
    f.comparisons().find((tab) => tab.comparison === "staged")!,
  );
  const droppedOf = () =>
    f.comparisons().find((tab) => tab.comparison === "staged")?.dropped_base;
  expect(droppedOf()).toMatchObject({ name: "other" });
  // The reference reappears and is explicitly reselected on the Branch tab.
  act(() => f.store.select(f.sessionId, branch));
  state.names = ["main", "other"];
  await refreshWhenReady();
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "other" })).toBeTruthy(),
  );
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: baseValue("other") },
  });
  await screen.findByRole("region", { name: "Git comparison" });
  // Trust is restored, so no tab keeps a record of the earlier invalidation.
  expect(droppedOf()).toBeUndefined();
  // A second, independent invalidation does not bind the staged tab.
  state.names = ["main"];
  await refreshWhenReady();
  await screen.findByText(/No available default base/);
  act(() => f.store.select(f.sessionId, staged));
  await waitFor(() =>
    expect(
      (screen.getByLabelText("Comparison") as HTMLSelectElement).value,
    ).toBe("staged"),
  );
  fireEvent.change(screen.getByLabelText("Comparison"), {
    target: { value: "branch" },
  });
  await waitFor(() =>
    expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe(
      baseValue("main"),
    ),
  );
  await waitFor(() => expect(branchBaseNames(f.read).at(-1)).toBe("main"));
  const snapshot = f.store.snapshot(f.sessionId),
    selected = snapshot.tabs.find(
      (value) => sessionTabKey(value) === snapshot.selected,
    );
  expect(selected).toMatchObject({
    comparison: "branch",
    base_ref: { name: "main" },
  });
});

const submitDraftPath = (path: string) => {
  if (!screen.queryByLabelText("Relative diff path"))
    fireEvent.click(screen.getByRole("button", { name: "Diff options" }));
  fireEvent.change(screen.getByLabelText("Relative diff path"), {
    target: { value: path },
  });
  return screen.getByRole("button", {
    name: "Compare path",
  }) as HTMLButtonElement;
};

it("denies a branch path submission while the selected repository's base negotiation is pending, then admits the negotiated default", async () => {
  let release: () => void = () => {},
    requested = false;
  const f = tabsFixture({
    options: async (repository, original) => {
      if (repository === f0.other) {
        requested = true;
        await new Promise<void>((resolve) => (release = resolve));
      }
      return original();
    },
  });
  const f0 = f;
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  await waitFor(() => expect(requested).toBe(true));
  const submit = submitDraftPath("src/main.ts");
  expect(submit.disabled).toBe(true);
  fireEvent.submit(submit.closest("form")!);
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(f.comparisons()).toHaveLength(0);
  expect(
    branchReads(f.read).filter((q) => q.repository_id === f.other),
  ).toHaveLength(0);
  // The typed draft stays available while submission is denied.
  expect(
    (screen.getByLabelText("Relative diff path") as HTMLInputElement).value,
  ).toBe("src/main.ts");
  release();
  await waitFor(() => expect(f.comparisons()).toHaveLength(1));
  expect(f.comparisons()[0]).toMatchObject({
    repository: f.other,
    comparison: "branch",
    base_ref: { type: "local-branch", name: "main" },
  });
  // After negotiation, an admitted submission carries the exact typed base.
  const admitted = submitDraftPath("src/main.ts");
  await waitFor(() => expect(admitted.disabled).toBe(false));
  fireEvent.click(admitted);
  await waitFor(() => expect(f.comparisons()).toHaveLength(2));
  expect(f.comparisons()[1]).toEqual({
    kind: SessionTabKind.Comparison,
    repository: f.other,
    comparison: "branch",
    path: "src/main.ts",
    base_ref: { type: "local-branch", name: "main" },
  });
});

it.each(["unsupported", "failed", "missing-default"])(
  "denies a branch path submission for %s negotiation and admits it after an available base is selected",
  async (mode) => {
    const f = tabsFixture({
      options: async (repository, original) => {
        if (repository !== f0.other) return original();
        if (mode === "unsupported")
          throw new ConnectError("old", Code.InvalidArgument);
        if (mode === "failed") throw new ConnectError("down", Code.Unavailable);
        return f0.reply({
          diff_options: {
            version: 1,
            repository_id: repository,
            choices: [
              {
                reference: { type: "local-branch", name: "main" },
                available: true,
                configured: false,
              },
              {
                reference: { type: "local-branch", name: "other" },
                available: true,
                configured: false,
              },
            ],
          },
        });
      },
    });
    const f0 = f;
    render(<f.Harness />);
    await screen.findByText("+<script>doNotRun()</script>");
    const reads = f.read.mock.calls.length;
    fireEvent.change(screen.getByLabelText("Diff repository"), {
      target: { value: f.other },
    });
    await waitFor(() =>
      expect(
        f.read.mock.calls
          .slice(reads)
          .some(
            ([request]) =>
              JSON.parse(new TextDecoder().decode(request.queryJson))
                .operation === "git-diff-options",
          ),
      ).toBe(true),
    );
    await new Promise((resolve) => setTimeout(resolve, 50));
    const submit = submitDraftPath("src/main.ts");
    expect(submit.disabled).toBe(true);
    fireEvent.submit(submit.closest("form")!);
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(f.comparisons()).toHaveLength(0);
    expect(
      branchReads(f.read)
        .slice(1)
        .filter((q) => q.repository_id === f.other),
    ).toHaveLength(0);
    if (mode === "unsupported" || mode === "failed") return;
    fireEvent.change(screen.getByLabelText("Base"), {
      target: { value: JSON.stringify(["local-branch", "other", undefined]) },
    });
    await waitFor(() => expect(f.comparisons()).toHaveLength(1));
    const admitted = submitDraftPath("src/main.ts");
    await waitFor(() => expect(admitted.disabled).toBe(false));
    fireEvent.click(admitted);
    await waitFor(() => expect(f.comparisons()).toHaveLength(2));
    expect(f.comparisons()[1]).toEqual({
      kind: SessionTabKind.Comparison,
      repository: f.other,
      comparison: "branch",
      path: "src/main.ts",
      base_ref: { type: "local-branch", name: "other" },
    });
  },
);

it("keeps path submission immediate and base-free for working-tree, staged and creation comparisons", async () => {
  const f = tabsFixture();
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  for (const comparison of ["working-tree", "staged", "creation"]) {
    fireEvent.change(screen.getByLabelText("Comparison"), {
      target: { value: comparison },
    });
    await waitFor(() =>
      expect(f.comparisons().some((tab) => tab.comparison === comparison)).toBe(
        true,
      ),
    );
    const submit = submitDraftPath(`src/${comparison}`);
    expect(submit.disabled).toBe(false);
    fireEvent.click(submit);
    await waitFor(() =>
      expect(
        f.comparisons().some((tab) => tab.path === `src/${comparison}`),
      ).toBe(true),
    );
  }
  for (const tab of f.comparisons()) expect(tab.base_ref).toBeUndefined();
});

it("denies branch path submission while an options refresh is pending or stale", async () => {
  const { f, operations } = inventoryFixture([{ name: "main" }], "main");
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  const pathApplied = () => screen.queryByText("Relative diff path: src");
  const options = holdNextRead(f);
  const before = operations().length;
  refreshDiff();
  await waitFor(() => expect(operations()).toHaveLength(before + 1));
  // The supported base is still observed, but the inventory is being renewed.
  let submit = submitDraftPath("src");
  expect(submit.disabled).toBe(true);
  fireEvent.submit(submit.closest("form")!);
  expect(pathApplied()).toBeNull();
  expect(operations()).toHaveLength(before + 1);
  options.release();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Refresh diff" })).toHaveProperty(
      "disabled",
      false,
    ),
  );
  // A failed renewal leaves the inventory stale, so the gate stays closed.
  f.read.mockRejectedValueOnce(
    new ConnectError("Worker unavailable", Code.Unavailable),
  );
  refreshDiff();
  await screen.findByText(
    "Refresh failed. The previous observation is stale and shown below.",
  );
  const failed = operations().length;
  submit = submitDraftPath("src");
  expect(submit.disabled).toBe(true);
  fireEvent.submit(submit.closest("form")!);
  expect(pathApplied()).toBeNull();
  expect(operations()).toHaveLength(failed);
  // An accepted refresh admits the retained draft again.
  refreshDiff();
  await waitFor(() =>
    expect((submitDraftPath("src") as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(submitDraftPath("src"));
  await screen.findByText("Relative diff path: src");
});

it("denies branch path submission while a background options refetch is in flight", async () => {
  const { f, operations } = inventoryFixture([{ name: "main" }], "main");
  render(<f.View />);
  await screen.findByText("+<script>doNotRun()</script>");
  const pathApplied = () => screen.queryByText("Relative diff path: src");
  // Hold only the options read so the previous inventory stays observable.
  const inner = f.read.getMockImplementation()!;
  let release: () => void = () => {};
  const opened = new Promise<void>((resolve) => (release = resolve));
  f.read.mockImplementation(async (request) => {
    const q = JSON.parse(new TextDecoder().decode(request.queryJson));
    if (q.operation === "git-diff-options") await opened;
    return inner(request);
  });
  const before = operations().length;
  // The connection-ready handler invalidates every active query.
  void f.client.invalidateQueries({ refetchType: "active" });
  await waitFor(() => expect(operations()).toContain("git-diff-options"));
  await waitFor(() =>
    expect(
      f.client.isFetching({
        predicate: (query) =>
          (query.state.data as { documentJson?: Uint8Array } | undefined)
            ?.documentJson !== undefined &&
          query.state.fetchStatus === "fetching",
      }),
    ).toBeGreaterThan(0),
  );
  const submit = submitDraftPath("src");
  expect(submit.disabled).toBe(true);
  const during = operations().length;
  fireEvent.submit(submit.closest("form")!);
  expect(pathApplied()).toBeNull();
  expect(operations()).toHaveLength(during);
  expect(during).toBeGreaterThan(before);
  release();
  await waitFor(() =>
    expect((submitDraftPath("src") as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(submitDraftPath("src"));
  await screen.findByText("Relative diff path: src");
});

it("keeps a denied path draft after the pending repository negotiation opens its tab", async () => {
  let release: () => void = () => {},
    requested = false;
  const f = tabsFixture({
    options: async (repository, original) => {
      if (repository === f0.other) {
        requested = true;
        await new Promise<void>((resolve) => (release = resolve));
      }
      return original();
    },
  });
  const f0 = f;
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  await waitFor(() => expect(requested).toBe(true));
  const submit = submitDraftPath("src/draft.ts");
  fireEvent.submit(submit.closest("form")!);
  expect(f.comparisons()).toHaveLength(0);
  release();
  await waitFor(() => expect(f.comparisons()).toHaveLength(1));
  expect(f.comparisons()[0]).toMatchObject({ path: "." });
  await waitFor(() =>
    expect(
      (screen.getByLabelText("Relative diff path") as HTMLInputElement).value,
    ).toBe("src/draft.ts"),
  );
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(
    (screen.getByLabelText("Relative diff path") as HTMLInputElement).value,
  ).toBe("src/draft.ts");
  // The retained draft is still submittable against the negotiated base.
  const admitted = screen.getByRole("button", {
    name: "Compare path",
  }) as HTMLButtonElement;
  await waitFor(() => expect(admitted.disabled).toBe(false));
  fireEvent.click(admitted);
  await waitFor(() => expect(f.comparisons()).toHaveLength(2));
  expect(f.comparisons()[1]).toMatchObject({ path: "src/draft.ts" });
});

it("keeps a denied path draft after an explicit base is selected for a missing default", async () => {
  const f = tabsFixture({
    options: async (repository, original) => {
      if (repository !== f0.other) return original();
      return f0.reply({
        diff_options: {
          version: 1,
          repository_id: repository,
          choices: [
            {
              reference: { type: "local-branch", name: "main" },
              available: true,
              configured: false,
            },
          ],
        },
      });
    },
  });
  const f0 = f;
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  await screen.findByText(/No available default base/);
  const submit = submitDraftPath("src/draft.ts");
  fireEvent.submit(submit.closest("form")!);
  expect(f.comparisons()).toHaveLength(0);
  fireEvent.change(screen.getByLabelText("Base"), {
    target: { value: JSON.stringify(["local-branch", "main", undefined]) },
  });
  await waitFor(() => expect(f.comparisons()).toHaveLength(1));
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(
    (screen.getByLabelText("Relative diff path") as HTMLInputElement).value,
  ).toBe("src/draft.ts");
});

it("resets the path draft when another tab selects a different path", async () => {
  const f = tabsFixture();
  render(<f.Harness />);
  await screen.findByText("+<script>doNotRun()</script>");
  submitDraftPath("unsubmitted/draft");
  act(() => {
    f.store.open(f.sessionId, {
      kind: SessionTabKind.Comparison,
      repository: f.primary,
      comparison: Comparison.Branch,
      path: "src/other.ts",
      base_ref: { type: "local-branch", name: "main" },
    });
  });
  await waitFor(() =>
    expect(
      (screen.getByLabelText("Relative diff path") as HTMLInputElement).value,
    ).toBe("src/other.ts"),
  );
});
