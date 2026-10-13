// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
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
  EntityKind,
  IntegrationService,
  ResourceSchema,
  ResourceService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode, type Document } from "./documents";
import {
  PRReadLimiter,
  SessionPRState,
  SessionPRStatus,
  sessionPRState,
} from "./session-pr-state";
import { SessionActivityProvider } from "./session-activity";

function fixture() {
  const profile = newRequestId();
  const repository = create(ResourceSchema, {
    id: newRequestId(),
    projectId: newRequestId(),
    kind: EntityKind.REPOSITORY,
    schemaVersion: 1,
    revision: 9007199254740993n,
    documentJson: encode({
      integration_id: profile,
      github_owner: "owner",
      github_name: "repo",
    }),
  });
  const association = {
    repository_id: repository.id,
    remote_repository_id: "37",
    repository_node_id: "R_37",
    pull_request_id: "9007199254740993",
    pull_request_node_id: "PR_17",
    owner: "owner",
    name: "repo",
    number: "17",
  };
  const row = create(ResourceSchema, {
    id: newRequestId(),
    sessionId: newRequestId(),
    projectId: repository.projectId,
    kind: EntityKind.PULL_REQUEST,
    revision: 1n,
    schemaVersion: 1,
  });
  const item = {
    provider: "github.com",
    kind: "pull-request",
    identity_source: "pull-request-api",
    id: association.pull_request_id,
    node_id: association.pull_request_node_id,
    number: "17",
    title: "Retained title",
    state: "open",
    merged: false,
    draft: false,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-10T00:00:00Z",
    url: "https://github.com/owner/repo/pull/17",
    body: "",
    base_ref: "main",
    head_ref: "feature",
    base_sha: "a".repeat(40),
    head_sha: "b".repeat(40),
  };
  const detail: Document = {
    repository_id: repository.id,
    repository_revision: repository.revision.toString(),
    profile_id: profile,
    generation_id: newRequestId(),
    observed_at: "2026-10-11T00:00:00Z",
    identity: { id: "19", node_id: "U_19", login: "owner" },
    query: { kind: "pull-request", operation: "detail", number: "17" },
    repository: {
      provider: "github.com",
      id: "37",
      node_id: "R_37",
      owner: "owner",
      name: "repo",
      private: true,
    },
    items: [item],
  };
  return { repository, association, row, item, detail };
}
it("joins exact remote PR identity, gives merged precedence and rejects contradictory state", () => {
  const f = fixture();
  expect(sessionPRState(f.detail, f.association)).toBe(SessionPRState.Open);
  f.item.state = "closed";
  expect(sessionPRState(f.detail, f.association)).toBe(SessionPRState.Closed);
  f.item.merged = true;
  expect(sessionPRState(f.detail, f.association)).toBe(SessionPRState.Merged);
  f.item.state = "open";
  expect(sessionPRState(f.detail, f.association)).toBe(SessionPRState.Neutral);
  f.item.state = "closed";
  for (const key of [
    "remote_repository_id",
    "repository_node_id",
    "pull_request_id",
    "pull_request_node_id",
    "number",
    "owner",
    "name",
  ]) {
    expect(
      sessionPRState(f.detail, { ...f.association, [key]: "different" }),
    ).toBe(SessionPRState.Neutral);
  }
});

it.each([
  ["open", false, "Pull request open", "open"],
  ["closed", true, "Pull request merged", "merged"],
  ["closed", false, "Pull request closed without merge", "closed"],
] as const)(
  "renders the accessible %s/%s dot without a text badge",
  async (state, merged, label, dot) => {
    const f = fixture();
    f.item.state = state;
    f.item.merged = merged;
    const transport = createRouterTransport((router) => {
      router.service(ResourceService, {
        getResource: () => ({ resource: f.repository }),
      });
      router.service(IntegrationService, {
        queryRepositoryIntegration: () => ({
          schemaVersion: 1,
          documentJson: encode(f.detail),
        }),
      });
    });
    render(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={new QueryClient()}>
          <SessionPRStatus
            row={f.row}
            association={f.association}
            limiter={new PRReadLimiter()}
            active
          />
        </QueryClientProvider>
      </TransportProvider>,
    );
    const icon = await screen.findByRole("img", { name: label });
    expect(icon.querySelector(`.session-pr-dot-${dot}`)).toBeTruthy();
    expect(icon.getAttribute("title")).toBe(label);
    expect(icon.textContent).toBe("");
  },
);

it("reads PR state from a global repository for a project-owned association", async () => {
  const f = fixture();
  f.repository.projectId = "";
  f.row.projectId = newRequestId();
  f.item.state = "open";
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {
      getResource: () => ({ resource: f.repository }),
    });
    router.service(IntegrationService, {
      queryRepositoryIntegration: () => ({
        schemaVersion: 1,
        documentJson: encode(f.detail),
      }),
    });
  });
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={new QueryClient()}>
        <SessionPRStatus
          row={f.row}
          association={f.association}
          limiter={new PRReadLimiter()}
          active
        />
      </QueryClientProvider>
    </TransportProvider>,
  );
  expect(
    await screen.findByRole("img", { name: "Pull request open" }),
  ).toBeTruthy();
});

it("fences a late detail from the replaced original association owner", async () => {
  const f = fixture();
  let release!: () => void;
  const old = encode(f.detail);
  f.item.state = "closed";
  const current = encode(f.detail);
  let calls = 0;
  const detail = vi.fn(async () => {
    if (++calls === 1) {
      await new Promise<void>((resolve) => {
        release = resolve;
      });
      return { schemaVersion: 1, documentJson: old };
    }
    return { schemaVersion: 1, documentJson: current };
  });
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {
      getResource: () => ({ resource: f.repository }),
    });
    router.service(IntegrationService, { queryRepositoryIntegration: detail });
  });
  const client = new QueryClient();
  const limiter = new PRReadLimiter();
  const view = (row: typeof f.row) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <SessionPRStatus
          row={row}
          association={f.association}
          limiter={limiter}
          active
        />
      </QueryClientProvider>
    </TransportProvider>
  );
  const mounted = render(view(f.row));
  await waitFor(() => expect(detail).toHaveBeenCalledTimes(1));
  mounted.rerender(
    view({ ...f.row, id: newRequestId(), sessionId: newRequestId() }),
  );
  await screen.findByRole("img", { name: "Pull request closed without merge" });
  await act(async () => {
    release();
  });
  expect(screen.queryByRole("img", { name: "Pull request open" })).toBeNull();
  expect(detail).toHaveBeenCalledTimes(2);
});
it("caps concurrent visible detail reads at four and drops canceled queued rows", async () => {
  const limiter = new PRReadLimiter();
  const controllers = Array.from({ length: 6 }, () => new AbortController());
  const releases: Array<() => void> = [];
  let running = 0,
    peak = 0;
  const read = vi.fn(async () => {
    peak = Math.max(peak, ++running);
    await new Promise<void>((resolve) => releases.push(resolve));
    running--;
  });
  const reads = controllers.map((controller) =>
    limiter.read(controller.signal, read),
  );
  await Promise.resolve();
  expect(read).toHaveBeenCalledTimes(4);
  const canceled = expect(reads[4]).rejects.toThrow();
  controllers[4]!.abort();
  await canceled;
  releases.shift()!();
  await reads[0];
  await Promise.resolve();
  expect(read).toHaveBeenCalledTimes(5);
  for (const release of releases) release();
  await Promise.all([reads[1], reads[2], reads[3], reads[5]]);
  expect(peak).toBe(4);
});
it("keeps current-state reads inactive when hidden and neutral after failed refresh with original Retry", async () => {
  const f = fixture();
  const detail = vi.fn(
    async (request: { repositoryId: string; queryJson: Uint8Array }) => {
      expect(request.repositoryId).toBe(f.repository.id);
      expect(JSON.parse(new TextDecoder().decode(request.queryJson))).toEqual(
        f.detail.query,
      );
      return { schemaVersion: 1, documentJson: encode(f.detail) };
    },
  );
  const get = vi.fn(async () => ({ resource: f.repository }));
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, { getResource: get });
    router.service(IntegrationService, { queryRepositoryIntegration: detail });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const target = document.createElement("div");
  document.body.append(target);
  const limiter = new PRReadLimiter();
  const view = (active: boolean) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <SessionActivityProvider active={active}>
          <SessionPRStatus
            row={f.row}
            association={f.association}
            limiter={limiter}
            active={active}
            diagnosticsTarget={target}
          />
        </SessionActivityProvider>
      </QueryClientProvider>
    </TransportProvider>
  );
  const mounted = render(view(false));
  expect(detail).not.toHaveBeenCalled();
  mounted.rerender(view(true));
  await screen.findByRole("img", { name: "Pull request open" });
  detail.mockRejectedValueOnce(new Error("fixture detail refresh unavailable"));
  await act(async () => {
    await client.invalidateQueries();
  });
  expect(
    await screen.findByRole("img", {
      name: "Pull request state unavailable or stale",
    }),
  ).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Retry PR #17 read" }));
  await screen.findByRole("img", { name: "Pull request open" });
  expect(detail).toHaveBeenCalledTimes(3);
  mounted.unmount();
  target.remove();
});
it.each(["repository", "profile", "remote", "pr", "contradiction", "schema"])(
  "never colors a mismatched %s observation",
  async (fault) => {
    const f = fixture();
    if (fault === "repository") f.detail.repository_id = newRequestId();
    if (fault === "profile") f.detail.profile_id = newRequestId();
    if (fault === "remote") (f.detail.repository as Document).id = "38";
    if (fault === "pr") f.item.id = "9007199254740994";
    if (fault === "contradiction") f.item.merged = true;
    const detail = vi.fn(async () => ({
      schemaVersion: fault === "schema" ? 2 : 1,
      documentJson: encode(f.detail),
    }));
    const transport = createRouterTransport((router) => {
      router.service(ResourceService, {
        getResource: () => ({ resource: f.repository }),
      });
      router.service(IntegrationService, {
        queryRepositoryIntegration: detail,
      });
    });
    render(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={new QueryClient()}>
          <SessionPRStatus
            row={f.row}
            association={f.association}
            limiter={new PRReadLimiter()}
            active
          />
        </QueryClientProvider>
      </TransportProvider>,
    );
    await waitFor(() => expect(detail).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(screen.getByRole("img")).toHaveProperty(
        "title",
        "Pull request state unavailable or stale",
      ),
    );
  },
);

function readCounters(f: ReturnType<typeof fixture>) {
  const get = vi.fn(async () => ({ resource: f.repository }));
  const detail = vi.fn(async () => ({
    schemaVersion: 1,
    documentJson: encode(f.detail),
  }));
  const makeTransport = () =>
    createRouterTransport((router) => {
      router.service(ResourceService, { getResource: get });
      router.service(IntegrationService, {
        queryRepositoryIntegration: detail,
      });
    });
  return { get, detail, makeTransport };
}
function refreshView(
  f: ReturnType<typeof fixture>,
  client: QueryClient,
  limiter: PRReadLimiter,
  transport: ReturnType<typeof createRouterTransport>,
  options: { row?: typeof f.row; sessionActive?: boolean; visible?: boolean },
) {
  return (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <SessionActivityProvider active={options.sessionActive ?? true}>
          <SessionPRStatus
            row={options.row ?? f.row}
            association={f.association}
            limiter={limiter}
            active={options.visible ?? true}
          />
        </SessionActivityProvider>
      </QueryClientProvider>
    </TransportProvider>
  );
}

it("reuses the retained observation across viewport re-entry without new reads", async () => {
  const f = fixture();
  const { get, detail, makeTransport } = readCounters(f);
  const transport = makeTransport();
  const client = new QueryClient();
  const limiter = new PRReadLimiter();
  const mounted = render(refreshView(f, client, limiter, transport, {}));
  await screen.findByRole("img", { name: "Pull request open" });
  expect(get).toHaveBeenCalledTimes(1);
  expect(detail).toHaveBeenCalledTimes(1);
  for (let pass = 0; pass < 3; pass++) {
    mounted.rerender(
      refreshView(f, client, limiter, transport, { visible: false }),
    );
    mounted.rerender(
      refreshView(f, client, limiter, transport, { visible: true }),
    );
    await screen.findByRole("img", { name: "Pull request open" });
  }
  await act(async () => {});
  expect(get).toHaveBeenCalledTimes(1);
  expect(detail).toHaveBeenCalledTimes(1);
});

it("refreshes exactly once on association invalidation", async () => {
  const f = fixture();
  const { get, detail, makeTransport } = readCounters(f);
  const transport = makeTransport();
  const client = new QueryClient();
  const limiter = new PRReadLimiter();
  const mounted = render(refreshView(f, client, limiter, transport, {}));
  await screen.findByRole("img", { name: "Pull request open" });
  mounted.rerender(
    refreshView(f, client, limiter, transport, {
      row: { ...f.row, revision: 2n },
    }),
  );
  await waitFor(() => expect(detail).toHaveBeenCalledTimes(2));
  await screen.findByRole("img", { name: "Pull request open" });
  await act(async () => {});
  expect(get).toHaveBeenCalledTimes(2);
  expect(detail).toHaveBeenCalledTimes(2);
});

it("refreshes exactly once when the authenticated transport is replaced", async () => {
  const f = fixture();
  const { get, detail, makeTransport } = readCounters(f);
  const client = new QueryClient();
  const limiter = new PRReadLimiter();
  const mounted = render(
    refreshView(f, client, limiter, makeTransport(), {}),
  );
  await screen.findByRole("img", { name: "Pull request open" });
  mounted.rerender(refreshView(f, client, limiter, makeTransport(), {}));
  await waitFor(() => expect(detail).toHaveBeenCalledTimes(2));
  await screen.findByRole("img", { name: "Pull request open" });
  await act(async () => {});
  expect(get).toHaveBeenCalledTimes(2);
  expect(detail).toHaveBeenCalledTimes(2);
});

it("refreshes exactly once when the session becomes active again", async () => {
  const f = fixture();
  const { get, detail, makeTransport } = readCounters(f);
  const transport = makeTransport();
  const client = new QueryClient();
  const limiter = new PRReadLimiter();
  const mounted = render(refreshView(f, client, limiter, transport, {}));
  await screen.findByRole("img", { name: "Pull request open" });
  mounted.rerender(
    refreshView(f, client, limiter, transport, { sessionActive: false }),
  );
  await act(async () => {});
  expect(detail).toHaveBeenCalledTimes(1);
  mounted.rerender(
    refreshView(f, client, limiter, transport, { sessionActive: true }),
  );
  await waitFor(() => expect(detail).toHaveBeenCalledTimes(2));
  await screen.findByRole("img", { name: "Pull request open" });
  await act(async () => {});
  expect(get).toHaveBeenCalledTimes(2);
  expect(detail).toHaveBeenCalledTimes(2);
});
