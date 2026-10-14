// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  IntegrationService,
  ResourceSchema,
  SystemCapability,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { PRAvatarCache } from "./pr-avatar-cache";
import { AvatarContext, PRWorkspaceRows } from "./pr-workspace";
import {
  StandalonePullRequestResults,
  type PullRequestNavigation,
} from "./github-items";
import { ItemKind, ItemState, QueryOperation } from "./github-query-model";
import { i18n } from "./localization";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

it("retains list/scroll while lazily reading all four tabs with independent commit counts", async () => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(Date.parse("2026-10-11T00:18:12Z"));
  const profile = newRequestId(),
    generation = newRequestId(),
    selected = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.REPOSITORY,
      revision: 1n,
      schemaVersion: 1,
      documentJson: encode({
        integration_id: profile,
        github_owner: "fixture-owner",
        github_name: "repo",
      }),
    });
  const remote = {
    provider: "github.com",
    id: "37",
    node_id: "R_37",
    owner: "fixture-owner",
    name: "repo",
    private: true,
  };
  const scope = {
    repository_id: selected.id,
    repository_revision: "1",
    profile_id: profile,
    generation_id: generation,
    observed_at: "2026-10-11T00:00:00Z",
    repository: remote,
  };
  const author = {
    provider_type: "User",
    kind: "user",
    id: "19",
    node_id: "U_19",
    login: "fixture-author",
  };
  const item = (number: string, detail: boolean) => ({
    provider: "github.com",
    kind: "pull-request",
    identity_source: "pull-request-api",
    id: String(Number(number) + 1000),
    node_id: `PR_${number}`,
    number,
    title: `Complete original title ${number}`,
    state: "open",
    created_at: "2026-10-10T00:00:00Z",
    updated_at: "2026-10-11T00:00:00Z",
    url: `https://github.com/fixture-owner/repo/pull/${number}`,
    author,
    draft: false,
    ...(detail
      ? {
          body: "# Readable heading\n\nOriginal **strong** body\n\n![inert](https://forbidden.invalid/image.png)\n<script>inert source</script>",
          merged: false,
          base_ref: number === "102" ? "s1" : number === "103" ? "s2" : "main",
          head_ref:
            number === "101"
              ? "s1"
              : number === "102"
                ? "s2"
                : number === "103"
                  ? "s3"
                  : "independent",
          base_sha: "a".repeat(40),
          head_sha: "b".repeat(40),
          head_repository: {
            state: "available",
            repository: { ...remote, default_branch: "main" },
          },
        }
      : {}),
  });
  // Workspace graph nodes carry every Detail field except the description.
  const nodeItem = (number: string) => {
    const { body: _body, ...rest } = item(number, true);
    return rest;
  };
  const legacy = vi.fn(async (request: { queryJson: Uint8Array }) => {
    const query = JSON.parse(new TextDecoder().decode(request.queryJson));
    const detail = query.operation !== "list";
    return {
      schemaVersion: 1,
      documentJson: encode({
        ...scope,
        identity: { id: "17", node_id: "U_17", login: "fixture-user" },
        query,
        items: detail
          ? [item(query.number, true)]
          : [item("102", false), item("104", false)],
        ...(query.operation === "diff"
          ? {
              diff: {
                patch: "diff --git a/file b/file\n@@ -1 +1 @@\n-old\n+new\n",
                base_sha: "a".repeat(40),
                head_sha: "b".repeat(40),
                digest: "c".repeat(64),
              },
            }
          : {}),
        ...(query.operation === "feedback"
          ? {
              feedback: {
                base_sha: "a".repeat(40),
                head_sha: "b".repeat(40),
                entries: [],
                threads: [],
                excluded_draft_reviews: 0,
              },
            }
          : {}),
      }),
    };
  });
  const enrich = vi.fn(async (request: { seedNumbers: string[] }) => ({
    schemaVersion: 1,
    documentJson: encode({
      ...scope,
      state: "complete",
      reasons: [],
      seeds: request.seedNumbers,
      nodes: ["101", "102", "103", "104"].map((number) => ({
        item: nodeItem(number),
        seed: request.seedNumbers.includes(number),
        counts: { additions: "24", deletions: "8" },
      })),
      edges: [
        { parent: "101", child: "102" },
        { parent: "102", child: "103" },
      ],
    }),
  }));
  const commits = vi.fn(
    async (request: {
      number: string;
      pullRequestId: string;
      pageToken: string;
      baseSha: string;
      headSha: string;
    }) => ({
      schemaVersion: 1,
      documentJson: encode({
        ...scope,
        pull_request_id: request.pullRequestId,
        number: request.number,
        base_sha: request.baseSha,
        head_sha: request.headSha,
        page_token: request.pageToken,
        next_page_token: "",
        commits: [
          {
            sha: "c".repeat(40),
            message: "Original complete commit\n\nFull body",
            author: { name: "Original author", date: "2026-10-11T00:00:00Z" },
            committer: {
              name: "Original committer",
              date: "2026-10-11T00:00:00Z",
            },
            parents: [request.headSha],
            counts: { additions: "8", deletions: "3" },
          },
        ],
      }),
    }),
  );
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.PULL_REQUEST_WORKSPACE_V1],
      }),
    });
    router.service(IntegrationService, {
      queryRepositoryIntegration: legacy,
      getPullRequestWorkspace: enrich,
      listPullRequestCommits: commits,
    });
  });
  let resize: ((entries: unknown[]) => void) | undefined;
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(private callback: (entries: unknown[]) => void) {}
      observe(node: Element) {
        if (node.classList.contains("pr-workspace")) resize = this.callback;
        this.callback([{ contentRect: { width: 1000 } }]);
      }
      disconnect() {}
    },
  );
  function Fixture() {
    const [navigation, setNavigation] = useState<PullRequestNavigation>({
      scopeKey: "fixture",
      query: {
        kind: ItemKind.PullRequest,
        operation: QueryOperation.List,
        state: ItemState.Open,
        page: 1,
        page_size: 20,
      },
      previous: [],
    });
    return (
      <StandalonePullRequestResults
        selected={selected}
        navigation={navigation}
        changeNavigation={setNavigation}
        active
      />
    );
  }
  const view = render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={new QueryClient()}>
        <Fixture />
      </QueryClientProvider>
    </TransportProvider>,
  );
  await waitFor(() =>
    expect(view.container.querySelectorAll(".pr-workspace-row")).toHaveLength(
      4,
    ),
  );
  expect(screen.getAllByText("Stack context")).toHaveLength(2);
  expect(legacy).toHaveBeenCalledTimes(1);
  const list = view.container.querySelector(
    ".pr-workspace-list",
  ) as HTMLElement;
  list.scrollTop = 80;
  fireEvent.click(
    screen.getByRole("button", { name: /#102 Complete original title 102/ }),
  );
  await screen.findByRole("heading", { name: "Readable heading" });
  // Detail presentation: toolbar actions, status line, relative Updated label.
  const header = view.container.querySelector(".pr-detail-header")!;
  const toolbar = view.container.querySelector(".pr-detail-toolbar")!;
  expect(toolbar.firstElementChild).toBe(
    screen.getByRole("button", { name: "Back to results" }),
  );
  const toolbarEnd = toolbar.querySelector(".pr-detail-toolbar-end")!;
  expect(
    [...toolbarEnd.querySelectorAll("button")].map((b) =>
      b.getAttribute("aria-label"),
    ),
  ).toEqual(["Refresh GitHub results", "Open on GitHub"]);
  const openAction = screen.getByRole("button", { name: "Open on GitHub" });
  expect((openAction as HTMLButtonElement).disabled).toBe(true);
  const notice = screen.getByText(/Browser opening is available/);
  expect(
    toolbar.compareDocumentPosition(notice) &
      Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
  expect(header.querySelector(".pr-detail-status")?.textContent).toMatch(
    /^Open.*#102$/,
  );
  expect(header.querySelector("h3")?.textContent).toBe(
    "Complete original title 102",
  );
  expect(header.querySelector(".pr-detail-branches code")?.textContent).toMatch(
    / → /,
  );
  const updated = header.querySelector(".pr-detail-meta time")!;
  expect(updated.textContent).toBe("18 minutes ago");
  expect(header.querySelector(".pr-detail-meta")?.textContent).toContain(
    "Updated 18 minutes ago",
  );
  expect(updated.getAttribute("datetime")).toBe("2026-10-11T00:00:00Z");
  expect(updated.getAttribute("title")).toBe("2026-10-11T00:00:00Z");
  expect(updated.getAttribute("aria-description")).toBe("2026-10-11T00:00:00Z");
  await act(() => i18n.changeLanguage("ko"));
  expect(header.querySelector(".pr-detail-meta")?.textContent).toContain(
    "18분 전 업데이트",
  );
  await act(() => i18n.changeLanguage("en"));
  // A clock tick recomputes the label without another GitHub read.
  const readsBeforeTick = legacy.mock.calls.length;
  act(() => vi.setSystemTime(Date.parse("2026-10-11T00:19:12Z")));
  fireEvent.focus(window);
  expect(updated.textContent).toBe("19 minutes ago");
  expect(legacy.mock.calls.length).toBe(readsBeforeTick);
  expect(view.container.querySelector("script")).toBeNull();
  expect(view.container.querySelector('img[src^="https:"]')).toBeNull();
  const description = screen.getByRole("tab", { name: "Description" });
  description.focus();
  fireEvent.keyDown(description, { key: "ArrowRight" });
  expect(document.activeElement).toBe(
    screen.getByRole("tab", { name: "Diff" }),
  );
  expect(screen.getByRole("tab", { name: "Diff" }).tabIndex).toBe(0);
  expect(description.tabIndex).toBe(-1);
  expect(legacy).toHaveBeenCalledTimes(2);
  fireEvent.click(screen.getByRole("tab", { name: "Diff" }));
  await screen.findByText("Snapshot details");
  fireEvent.click(screen.getByRole("tab", { name: "Reviews" }));
  await screen.findByRole("region", { name: "Published PR feedback" });
  fireEvent.click(screen.getByRole("tab", { name: "Commits" }));
  await screen.findByRole("heading", { name: "Original complete commit" });
  const detail = view.container.querySelector(".pr-workspace-detail")!;
  expect(
    within(detail as HTMLElement).getAllByText("+24").length,
  ).toBeGreaterThan(0);
  expect(within(detail as HTMLElement).getByText("+8")).toBeTruthy();
  expect(commits).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("tab", { name: "Description" }));
  fireEvent.click(screen.getByRole("tab", { name: "Commits" }));
  expect(commits).toHaveBeenCalledTimes(1);
  expect(legacy).toHaveBeenCalledTimes(4);
  expect(list.scrollTop).toBe(80);
  expect(view.container.querySelectorAll(".pr-workspace-row")).toHaveLength(4);
  await act(async () => resize?.([{ contentRect: { width: 600 } }]));
  expect(list.hidden).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Back to results" }));
  await waitFor(() =>
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: /#102 Complete original title 102/ }),
    ),
  );
  expect(list.hidden).toBe(false);
  expect(list.scrollTop).toBe(80);
  expect(commits).toHaveBeenCalledTimes(1);
  expect(
    legacy.mock.calls.map(
      ([q]) => JSON.parse(new TextDecoder().decode(q.queryJson)).operation,
    ),
  ).toEqual(["list", "detail", "diff", "feedback"]);
  vi.unstubAllGlobals();
});

// The server keeps the original workspace RPC pending while a displaced read is
// resumed. The desktop must show neither a cancellation warning nor Retry on that
// pending path, publish the final observation without another RPC, and still
// expose Retry for a real terminal failure.
function pendingWorkspaceFixture(
  enrich: (request: { seedNumbers: string[] }) => Promise<unknown>,
) {
  const profile = newRequestId(),
    generation = newRequestId(),
    selected = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.REPOSITORY,
      revision: 1n,
      schemaVersion: 1,
      documentJson: encode({
        integration_id: profile,
        github_owner: "fixture-owner",
        github_name: "repo",
      }),
    });
  const remote = {
    provider: "github.com",
    id: "37",
    node_id: "R_37",
    owner: "fixture-owner",
    name: "repo",
    private: true,
  };
  const scope = {
    repository_id: selected.id,
    repository_revision: "1",
    profile_id: profile,
    generation_id: generation,
    observed_at: "2026-10-11T00:00:00Z",
    repository: remote,
  };
  const author = {
    provider_type: "User",
    kind: "user",
    id: "19",
    node_id: "U_19",
    login: "fixture-author",
  };
  const item = (number: string, detail: boolean) => ({
    provider: "github.com",
    kind: "pull-request",
    identity_source: "pull-request-api",
    id: String(Number(number) + 1000),
    node_id: `PR_${number}`,
    number,
    title: `Complete original title ${number}`,
    state: "open",
    created_at: "2026-10-10T00:00:00Z",
    updated_at: "2026-10-11T00:00:00Z",
    url: `https://github.com/fixture-owner/repo/pull/${number}`,
    author,
    draft: false,
    ...(detail
      ? {
          body: "Pending path body",
          merged: false,
          base_ref: number === "102" ? "s1" : number === "103" ? "s2" : "main",
          head_ref:
            number === "101"
              ? "s1"
              : number === "102"
                ? "s2"
                : number === "103"
                  ? "s3"
                  : "independent",
          base_sha: "a".repeat(40),
          head_sha: "b".repeat(40),
          head_repository: {
            state: "available",
            repository: { ...remote, default_branch: "main" },
          },
        }
      : {}),
  });
  // Workspace graph nodes carry every Detail field except the description.
  const nodeItem = (number: string) => {
    const { body: _body, ...rest } = item(number, true);
    return rest;
  };
  const legacy = vi.fn(async (request: { queryJson: Uint8Array }) => {
    const query = JSON.parse(new TextDecoder().decode(request.queryJson));
    const detail = query.operation !== "list";
    return {
      schemaVersion: 1,
      documentJson: encode({
        ...scope,
        identity: { id: "17", node_id: "U_17", login: "fixture-user" },
        query,
        items: detail
          ? [item(query.number, true)]
          : [item("102", false), item("104", false)],
      }),
    };
  });
  const workspace = vi.fn(enrich);
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.PULL_REQUEST_WORKSPACE_V1],
      }),
    });
    router.service(IntegrationService, {
      queryRepositoryIntegration: legacy,
      getPullRequestWorkspace: workspace as never,
    });
  });
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(private callback: (entries: unknown[]) => void) {}
      observe() {
        this.callback([{ contentRect: { width: 1000 } }]);
      }
      disconnect() {}
    },
  );
  function Fixture() {
    const [navigation, setNavigation] = useState<PullRequestNavigation>({
      scopeKey: "fixture",
      query: {
        kind: ItemKind.PullRequest,
        operation: QueryOperation.List,
        state: ItemState.Open,
        page: 1,
        page_size: 20,
      },
      previous: [],
    });
    return (
      <StandalonePullRequestResults
        selected={selected}
        navigation={navigation}
        changeNavigation={setNavigation}
        active
      />
    );
  }
  const view = render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={new QueryClient()}>
        <Fixture />
      </QueryClientProvider>
    </TransportProvider>,
  );
  const workspaceDocument = (seeds: string[], withBodies = false) =>
    encode({
      ...scope,
      state: "complete",
      reasons: [],
      seeds,
      nodes: ["101", "102", "103", "104"].map((number) => ({
        item: withBodies ? item(number, true) : nodeItem(number),
        seed: seeds.includes(number),
        counts: { additions: "24", deletions: "8" },
      })),
      edges: [
        { parent: "101", child: "102" },
        { parent: "102", child: "103" },
      ],
    });
  return { view, legacy, workspace, workspaceDocument };
}

it("keeps the stack read pending without warning or Retry until the final observation", async () => {
  let resolveWorkspace: (value: unknown) => void = () => {};
  const { view, workspace, workspaceDocument } = pendingWorkspaceFixture(
    (request) =>
      new Promise((resolve) => {
        resolveWorkspace = () =>
          resolve({
            schemaVersion: 1,
            documentJson: workspaceDocument(request.seedNumbers),
          });
      }),
  );
  await waitFor(() =>
    expect(view.container.querySelectorAll(".pr-workspace-row")).toHaveLength(
      2,
    ),
  );
  fireEvent.click(
    screen.getByRole("button", { name: /#102 Complete original title 102/ }),
  );
  await screen.findAllByText("Pending path body");
  expect(workspace).toHaveBeenCalledTimes(1);
  expect(screen.queryByText(/Stack context is incomplete/)).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Retry stack context" }),
  ).toBeNull();
  expect(view.container.querySelectorAll(".pr-workspace-row")).toHaveLength(2);
  await act(async () => resolveWorkspace(undefined));
  await waitFor(() =>
    expect(view.container.querySelectorAll(".pr-workspace-row")).toHaveLength(
      4,
    ),
  );
  expect(workspace).toHaveBeenCalledTimes(1);
  expect(screen.queryByText(/Stack context is incomplete/)).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Retry stack context" }),
  ).toBeNull();
  view.unmount();
});

it("still exposes Retry stack context after a terminal workspace failure", async () => {
  const { view, workspace } = pendingWorkspaceFixture(async () => {
    throw new ConnectError("The provider failed.", Code.Unavailable);
  });
  await waitFor(() =>
    expect(view.container.querySelectorAll(".pr-workspace-row")).toHaveLength(
      2,
    ),
  );
  const retry = await screen.findByRole("button", {
    name: "Retry stack context",
  });
  expect(screen.getByText(/Stack context is incomplete/)).toBeTruthy();
  expect(workspace).toHaveBeenCalledTimes(1);
  fireEvent.click(retry);
  await waitFor(() => expect(workspace).toHaveBeenCalledTimes(2));
  view.unmount();
});

it("rejects workspace nodes that carry a description body", async () => {
  const { view, workspace, workspaceDocument } = pendingWorkspaceFixture(
    async (request) => ({
      schemaVersion: 1,
      documentJson: workspaceDocument(request.seedNumbers, true),
    }),
  );
  await waitFor(() =>
    expect(view.container.querySelectorAll(".pr-workspace-row")).toHaveLength(
      2,
    ),
  );
  await screen.findByRole("button", { name: "Retry stack context" });
  expect(workspace).toHaveBeenCalledTimes(1);
  expect(view.container.querySelectorAll(".pr-workspace-row")).toHaveLength(2);
  view.unmount();
});

it("accepts body-free workspace nodes and keeps the selected description from its own read", async () => {
  const { view, legacy, workspace, workspaceDocument } =
    pendingWorkspaceFixture(async (request) => ({
      schemaVersion: 1,
      documentJson: workspaceDocument(request.seedNumbers),
    }));
  await waitFor(() =>
    expect(view.container.querySelectorAll(".pr-workspace-row")).toHaveLength(
      4,
    ),
  );
  expect(workspace).toHaveBeenCalledTimes(1);
  expect(
    screen.queryByRole("button", { name: "Retry stack context" }),
  ).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: /#102 Complete original title 102/ }),
  );
  await screen.findAllByText("Pending path body");
  expect(
    legacy.mock.calls.map(
      ([q]) => JSON.parse(new TextDecoder().decode(q.queryJson)).operation,
    ),
  ).toEqual(["list", "detail"]);
  view.unmount();
});

// A selection and the avatar cache are bound to the profile generation and
// numeric repository that produced them. A reconnect that re-reads the retained
// list under another identity must dispose both before new observations render,
// while a same-scope refresh keeps the selection.
enum ScopeChange {
  None = "none",
  Generation = "generation",
  NumericRepository = "numeric-repository",
  NodeId = "node-id",
}
it.each([
  [ScopeChange.None, true],
  [ScopeChange.Generation, false],
  [ScopeChange.NumericRepository, false],
  [ScopeChange.NodeId, true],
])(
  "handles a reconnect scope change of %s (selection kept: %s)",
  async (change, kept) => {
    const profile = newRequestId(),
      avatarReference = newRequestId(),
      selected = create(ResourceSchema, {
        id: newRequestId(),
        kind: EntityKind.REPOSITORY,
        revision: 1n,
        schemaVersion: 1,
        documentJson: encode({
          integration_id: profile,
          github_owner: "fixture-owner",
          github_name: "repo",
        }),
      });
    let generation = newRequestId(),
      repositoryId = "37",
      nodeId = "R_37";
    const scope = () => ({
      repository_id: selected.id,
      repository_revision: "1",
      profile_id: profile,
      generation_id: generation,
      observed_at: "2026-10-11T00:00:00Z",
      repository: {
        provider: "github.com",
        id: repositoryId,
        node_id: nodeId,
        owner: "fixture-owner",
        name: "repo",
        private: true,
      },
    });
    const author = {
      provider_type: "User",
      kind: "user",
      id: "19",
      node_id: "U_19",
      login: "fixture-author",
    };
    const item = (number: string, detail: boolean) => ({
      provider: "github.com",
      kind: "pull-request",
      identity_source: "pull-request-api",
      id: String(Number(number) + 1000),
      node_id: `PR_${number}`,
      number,
      title: `Complete original title ${number}`,
      state: "open",
      created_at: "2026-10-10T00:00:00Z",
      updated_at: "2026-10-11T00:00:00Z",
      url: `https://github.com/fixture-owner/repo/pull/${number}`,
      author,
      draft: false,
      ...(detail
        ? {
            body: "Scoped body",
            merged: false,
            base_ref: "main",
            head_ref: "independent",
            base_sha: "a".repeat(40),
            head_sha: "b".repeat(40),
            head_repository: {
              state: "available",
              repository: { ...scope().repository, default_branch: "main" },
            },
          }
        : {}),
    });
    // Workspace graph nodes carry every Detail field except the description.
    const nodeItem = (number: string) => {
      const { body: _body, ...rest } = item(number, true);
      return rest;
    };
    const legacy = vi.fn(async (request: { queryJson: Uint8Array }) => {
      const query = JSON.parse(new TextDecoder().decode(request.queryJson));
      return {
        schemaVersion: 1,
        documentJson: encode({
          ...scope(),
          identity: { id: "17", node_id: "U_17", login: "fixture-user" },
          query,
          items:
            query.operation === "list"
              ? [item("102", false), item("104", false)]
              : [item(query.number, true)],
        }),
      };
    });
    const workspace = vi.fn(async (request: { seedNumbers: string[] }) => ({
      schemaVersion: 1,
      documentJson: encode({
        ...scope(),
        state: "complete",
        reasons: [],
        seeds: request.seedNumbers,
        nodes: ["102", "104"].map((number) => ({
          item: nodeItem(number),
          seed: request.seedNumbers.includes(number),
          avatar_reference: avatarReference,
          counts: { additions: "24", deletions: "8" },
        })),
        edges: [],
      }),
    }));
    const avatar = vi.fn(async () => ({ png: new Uint8Array([1, 2, 3]) }));
    const transport = createRouterTransport((router) => {
      router.service(SystemService, {
        getStatus: () => ({
          capabilities: [SystemCapability.PULL_REQUEST_WORKSPACE_V1],
        }),
      });
      router.service(IntegrationService, {
        queryRepositoryIntegration: legacy,
        getPullRequestWorkspace: workspace,
        readPullRequestAvatar: avatar as never,
      });
    });
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(private callback: (entries: unknown[]) => void) {}
        observe() {
          this.callback([{ contentRect: { width: 1000 } }]);
        }
        disconnect() {}
      },
    );
    const originalCreate = URL.createObjectURL,
      originalRevoke = URL.revokeObjectURL;
    const create_ = vi.fn(() => "blob:fixture-avatar"),
      revoke = vi.fn();
    URL.createObjectURL = create_;
    URL.revokeObjectURL = revoke;
    try {
      const client = new QueryClient();
      function Fixture() {
        const [navigation, setNavigation] = useState<PullRequestNavigation>({
          scopeKey: "fixture",
          query: {
            kind: ItemKind.PullRequest,
            operation: QueryOperation.List,
            state: ItemState.Open,
            page: 1,
            page_size: 20,
          },
          previous: [],
        });
        return (
          <StandalonePullRequestResults
            selected={selected}
            navigation={navigation}
            changeNavigation={setNavigation}
            active
          />
        );
      }
      const view = render(
        <TransportProvider transport={transport}>
          <QueryClientProvider client={client}>
            <Fixture />
          </QueryClientProvider>
        </TransportProvider>,
      );
      await waitFor(() => expect(create_).toHaveBeenCalled());
      fireEvent.click(
        screen.getByRole("button", {
          name: /#102 Complete original title 102/,
        }),
      );
      await screen.findAllByText("Scoped body");
      expect(revoke).not.toHaveBeenCalled();
      const listCalls = legacy.mock.calls.length;
      if (change === ScopeChange.Generation) generation = newRequestId();
      if (change === ScopeChange.NumericRepository) repositoryId = "38";
      if (change === ScopeChange.NodeId) nodeId = "R_37_reencoded";
      await act(async () => {
        await client.invalidateQueries();
      });
      await waitFor(() =>
        expect(legacy.mock.calls.length).toBeGreaterThan(listCalls),
      );
      if (kept) {
        await new Promise((resolve) => setTimeout(resolve, 50));
        expect(screen.queryByText(/Select a pull request/)).toBeNull();
        expect(screen.getAllByText("Scoped body").length).toBeGreaterThan(0);
        expect(revoke).not.toHaveBeenCalled();
      } else {
        await screen.findByText(/Select a pull request/);
        expect(screen.queryByText("Scoped body")).toBeNull();
        expect(revoke).toHaveBeenCalledWith("blob:fixture-avatar");
      }
      view.unmount();
    } finally {
      URL.createObjectURL = originalCreate;
      URL.revokeObjectURL = originalRevoke;
    }
  },
);

// Each resident payload page renders its own PRWorkspaceRows instance, so a
// component with seeds on two resident pages must be owned by exactly one.
it("renders a component with seeds on two resident pages exactly once", () => {
  const selected = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.REPOSITORY,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({
      integration_id: newRequestId(),
      github_owner: "fixture-owner",
      github_name: "repo",
    }),
  });
  const item = (number: string) => ({
    provider: "github.com",
    kind: "pull-request",
    identity_source: "pull-request-api",
    id: String(Number(number) + 1000),
    number,
    title: `Resident title ${number}`,
    state: "open",
    created_at: "2026-10-10T00:00:00Z",
    updated_at: "2026-10-11T00:00:00Z",
    url: `https://github.com/fixture-owner/repo/pull/${number}`,
    author: { login: "fixture-author" },
    draft: false,
  });
  const page = (number: number, seed: string) => ({
    query: {
      kind: ItemKind.PullRequest,
      operation: QueryOperation.List,
      state: ItemState.Open,
      page: number,
      page_size: 1,
    },
    data: {
      observed_at: "2026-10-11T00:00:00Z",
      items: [item(seed)],
    },
  });
  const observations = new Map([
    [
      "scope",
      {
        state: "complete",
        nodes: ["101", "102", "103"].map((number) => ({ item: item(number) })),
        edges: [
          { parent: "101", child: "102" },
          { parent: "102", child: "103" },
        ],
      },
    ],
  ]);
  const transport = createRouterTransport(() => {});
  const avatars = new PRAvatarCache();
  const client = new QueryClient();
  const allSeeds = ["101", "103"];
  const rows = (residentSeeds?: string[]) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <AvatarContext.Provider value={avatars}>
          {[page(1, "101"), page(2, "103")].map((value) => (
            <PRWorkspaceRows
              key={value.query.page}
              pages={[value]}
              selected={selected}
              enabled={false}
              choose={() => {}}
              observations={observations}
              observe={() => {}}
              allSeeds={allSeeds}
              residentSeeds={residentSeeds}
            />
          ))}
        </AvatarContext.Provider>
      </QueryClientProvider>
    </TransportProvider>
  );
  const view = render(rows(allSeeds));
  const numbers = () =>
    [...view.container.querySelectorAll("[data-pr-number]")].map((node) =>
      node.getAttribute("data-pr-number"),
    );
  expect(numbers()).toEqual(["101", "102", "103"]);
  // Evicting the earlier page moves ownership to the earliest resident seed.
  view.rerender(rows(["103"]));
  expect(numbers()).toEqual(["101", "102", "103"]);
  view.unmount();
});
