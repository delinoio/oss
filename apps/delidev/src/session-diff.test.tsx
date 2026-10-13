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
  ResourceService,
  SessionQuery,
  SessionService,
  newRequestId,
  type DeleteLocalReviewCommentRequest,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { SessionDiff } from "./session-diff";
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

type Gate = { release: () => void };

// Holds the next read until released. Queue gates in request order.
function holdNextRead(f: ReturnType<typeof fixture>): Gate {
  const inner = f.read.getMockImplementation()!;
  let release: () => void = () => {};
  const opened = new Promise<void>((resolve) => (release = resolve));
  f.read.mockImplementationOnce(async (request) => {
    await opened;
    return inner(request);
  });
  return { release: () => release() };
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
        | { documentJson?: Uint8Array }
        | undefined;
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
  await waitFor(() => expect(operations().at(-1)).toBe("git-diff:branch:trunk"));
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
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:branch:main"),
  );
  expect((screen.getByLabelText("Base") as HTMLSelectElement).value).toBe(
    JSON.stringify(["local-branch", "main", null]),
  );
  expect(operations()).not.toContain("git-diff:branch:late-next");
  expect(cachedOptionDefaults(f.client)).not.toContain("late-next");
});

it("does not publish a late options response after the path changes", async () => {
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
  fireEvent.click(screen.getByRole("button", { name: "Diff options" }));
  fireEvent.change(screen.getByLabelText("Relative diff path"), {
    target: { value: "src/main.ts" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Compare path" }));
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:branch:main"),
  );
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
  await waitFor(() =>
    expect(operations().at(-1)).toBe("git-diff:branch:main"),
  );
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
            {open ? (
              <SessionDiff
                sessionId={f.sessionId}
                worktree
                close={() => setOpen(false)}
                selected={
                  tab?.kind === SessionTabKind.Comparison ? tab : undefined
                }
                openComparison={(value) =>
                  store.open(f.sessionId, {
                    kind: SessionTabKind.Comparison,
                    ...value,
                  })
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
