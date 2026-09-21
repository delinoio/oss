import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { LocalService } from "@delinoio/async-commit-hook-api-client";
import { expect, it, vi } from "vitest";
import { Workspace } from "./App";

it("loads split repositories on demand and retains selection through a page error", async () => {
  let failNext = true;
  let releaseNext = () => {};
  const nextRead = new Promise<void>((resolve) => { releaseNext = resolve; });
  const listRepositories = vi.fn(async (request: { cursor: string; limit: number }) => {
    if (!request.cursor) return { repositories: [{ id: "repo", name: "Repository", worktrees: [{ id: "one", path: "/first", branch: "main" }] }], nextCursor: "page-two" };
    expect(request.cursor).toBe("page-two");
    expect(request.limit).toBe(50);
    if (failNext) {
      await nextRead;
      throw new ConnectError("Temporary page failure", Code.Unavailable);
    }
    return { repositories: [{ id: "repo", name: "Repository", worktrees: [{ id: "two", path: "/second", branch: "feature" }] }] };
  });
  const listRuns = vi.fn(() => ({ runs: [] }));
  const transport = createRouterTransport((router) => router.service(LocalService, {
    listRepositories, getVersion: () => ({ apiVersion: 1 }), listBranches: () => ({ branches: [] }), listRuns,
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { unmount } = render(<QueryClientProvider client={client}><TransportProvider transport={transport}>
    <Workspace initialRun="" />
  </TransportProvider></QueryClientProvider>);
  try {
    const sidebar = within(screen.getByRole("complementary", { name: "Repositories" }));
    const first = await sidebar.findByRole("button", { name: /\/first/ });
    await waitFor(() => expect(first.getAttribute("aria-current")).toBe("true"));
    expect(listRepositories).toHaveBeenCalledOnce();
    const load = sidebar.getByRole("button", { name: "Load more workspaces" });
    load.focus();
    fireEvent.click(load);
    await sidebar.findByRole("button", { name: "Loading workspaces…" });
    expect(document.activeElement).toBe(load);
    expect(load.getAttribute("aria-disabled")).toBe("true");
    fireEvent.click(load);
    expect(listRepositories).toHaveBeenCalledTimes(2);
    await act(async () => { releaseNext(); });
    await sidebar.findByRole("alert");
    expect(document.activeElement).toBe(load);
    expect(first.getAttribute("aria-current")).toBe("true");
    failNext = false;
    fireEvent.click(sidebar.getByRole("button", { name: "Try again" }));
    const second = await sidebar.findByRole("button", { name: /\/second/ });
    expect(sidebar.getAllByRole("heading", { name: "Repository" })).toHaveLength(1);
    expect(first.getAttribute("aria-current")).toBe("true");
    expect(sidebar.getByRole("button", { name: "All workspaces loaded" }).getAttribute("aria-disabled")).toBe("true");
    fireEvent.click(second);
    await waitFor(() => expect(second.getAttribute("aria-current")).toBe("true"));
    expect(listRepositories).toHaveBeenCalledTimes(3);
    expect(listRuns.mock.calls.length).toBeGreaterThan(1);
  } finally { unmount(); client.clear(); }
});

it("pages branches without losing an unloaded selection or loaded options on errors", async () => {
  let fail = true;
  const listBranches = vi.fn((request: { cursor: string; limit: number }) => {
    expect(request.limit).toBe(50);
    if (!request.cursor) return { branches: [{ name: "alpha", commit: "one" }], nextCursor: "next" };
    expect(request.cursor).toBe("next");
    if (fail) throw new ConnectError("Temporary branch failure", Code.Unavailable);
    return { branches: [{ name: "zulu", commit: "two" }] };
  });
  const transport = createRouterTransport((router) => router.service(LocalService, {
    listRepositories: () => ({ repositories: [{ id: "repo", name: "Repository", worktrees: [{ id: "tree", path: "/repo", branch: "zulu" }] }] }),
    getVersion: () => ({ apiVersion: 1 }), listBranches, listRuns: () => ({ runs: [] }),
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { unmount } = render(<QueryClientProvider client={client}><TransportProvider transport={transport}>
    <Workspace initialRun="" />
  </TransportProvider></QueryClientProvider>);
  try {
    const select = await screen.findByRole("combobox", { name: "Branch" });
    await screen.findByRole("option", { name: "alpha" });
    expect((select as HTMLSelectElement).value).toBe("zulu");
    expect(listBranches).toHaveBeenCalledOnce();
    const load = screen.getByRole("button", { name: "Load more branches" });
    load.focus(); fireEvent.click(load);
    await screen.findByRole("alert");
    expect(document.activeElement).toBe(load);
    expect((select as HTMLSelectElement).value).toBe("zulu");
    expect(screen.getByRole("option", { name: "alpha" })).toBeDefined();
    fail = false;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByRole("button", { name: "All branches loaded" });
    expect(screen.getAllByRole("option", { name: "zulu" })).toHaveLength(1);
    fireEvent.change(select, { target: { value: "alpha" } });
    expect((select as HTMLSelectElement).value).toBe("alpha");
    expect(listBranches).toHaveBeenCalledTimes(3);
  } finally { unmount(); client.clear(); }
});

it("keeps colliding branch labels separate and sends opaque identities in every view", async () => {
  const listRuns = vi.fn(() => ({ runs: [] }));
  const getChanges = vi.fn(() => ({ diff: "selected branch", head: "two" }));
  const listCommits = vi.fn(() => ({ commits: [] }));
  const transport = createRouterTransport((router) => router.service(LocalService, {
    listRepositories: () => ({ repositories: [{ id: "repo", name: "Repository", worktrees: [{ id: "tree", path: "/repo", branch: "bad-�", branchId: "raw-one" }] }] }),
    getVersion: () => ({ apiVersion: 1 }),
    listBranches: () => ({ branches: [{ id: "raw-one", name: "bad-�", commit: "one" }, { id: "raw-two", name: "bad-�", commit: "two" }] }),
    listRuns, getChanges, listCommits,
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { unmount } = render(<QueryClientProvider client={client}><TransportProvider transport={transport}>
    <Workspace initialRun="" />
  </TransportProvider></QueryClientProvider>);
  try {
    const select = await screen.findByRole("combobox", { name: "Branch" });
    await waitFor(() => expect(screen.getAllByRole("option", { name: "bad-�" })).toHaveLength(2));
    expect((select as HTMLSelectElement).value).toBe("raw-one");
    fireEvent.change(select, { target: { value: "raw-two" } });
    await waitFor(() => expect(listRuns).toHaveBeenLastCalledWith(expect.objectContaining({ branchId: "raw-two", branch: "", detached: false }), expect.anything()));
    fireEvent.click(screen.getByRole("button", { name: "Changes" }));
    await waitFor(() => expect(getChanges).toHaveBeenCalledWith(expect.objectContaining({ branchId: "raw-two", ref: "" }), expect.anything()));
    fireEvent.click(screen.getByRole("button", { name: "Commits" }));
    await waitFor(() => expect(listCommits).toHaveBeenCalledWith(expect.objectContaining({ branchId: "raw-two", ref: "" }), expect.anything()));
    fireEvent.click(screen.getByRole("button", { name: "Inbox" }));
    await waitFor(() => expect(listRuns).toHaveBeenLastCalledWith(expect.objectContaining({ branchId: "", branch: "", detached: false, inbox: true }), expect.anything()));
  } finally { unmount(); client.clear(); }
});

it("renders the local diff-base diagnostic and accepts an explicit base", async () => {
  const getChanges = vi.fn((request: { base: string }) => {
    if (!request.base)
      throw new ConnectError(
        "diff-base-required: select a local diff base; no remote fetch is performed",
        Code.InvalidArgument,
      );
    return {
      base: request.base,
      head: "HEAD",
      mergeBase: "merge-base",
      diff: "selected diff",
      truncated: false,
    };
  });
  const transport = createRouterTransport((router) => router.service(LocalService, {
    listRepositories: () => ({ repositories: [{ id: "repo", name: "Repository", worktrees: [{ id: "tree", path: "/repo", branch: "main" }] }] }),
    getVersion: () => ({ apiVersion: 1 }),
    listBranches: () => ({ branches: [] }),
    listRuns: () => ({ runs: [] }),
    getChanges,
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { unmount } = render(<QueryClientProvider client={client}><TransportProvider transport={transport}>
    <Workspace initialRun="" />
  </TransportProvider></QueryClientProvider>);
  try {
    await screen.findByRole("button", { name: /\/repo/ });
    fireEvent.click(screen.getByRole("button", { name: "Changes" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("select a local diff base; no remote fetch is performed");
    expect(alert.textContent).not.toContain("Cannot reach ach");

    const base = screen.getByRole("textbox", { name: "Diff base" });
    fireEvent.change(base, { target: { value: "HEAD" } });
    fireEvent.submit(base.closest("form")!);
    await waitFor(() => expect(getChanges).toHaveBeenLastCalledWith(
      expect.objectContaining({ base: "HEAD" }),
      expect.anything(),
    ));
    await screen.findByText("selected diff");
    expect(screen.getByText("merge-base")).toBeTruthy();
  } finally { unmount(); client.clear(); }
});

for (const outcome of ["selected", "empty", "error"] as const) {
  it(`does not request unfiltered runs while workspace discovery is ${outcome}`, async () => {
    let release = () => {};
    const pending = new Promise<void>((resolve) => { release = resolve; });
    const listRuns = vi.fn(() => ({ runs: [] }));
    const transport = createRouterTransport((router) => router.service(LocalService, {
      listRepositories: async () => {
        await pending;
        if (outcome === "error") throw new ConnectError("Discovery unavailable", Code.Unavailable);
        return { repositories: outcome === "empty" ? [] : [{ id: "repo", name: "Repository", worktrees: [{ id: "tree", path: "/repo", branch: "main" }] }] };
      },
      getVersion: () => ({ apiVersion: 1 }), listBranches: () => ({ branches: [] }), listRuns,
    }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { unmount } = render(<QueryClientProvider client={client}><TransportProvider transport={transport}>
      <Workspace initialRun="" />
    </TransportProvider></QueryClientProvider>);
    try {
      await screen.findByText("Loading repositories…");
      expect(listRuns).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole("button", { name: "Inbox" }));
      await screen.findByText("Select a registered worktree to view its results.");
      expect(listRuns).not.toHaveBeenCalled();
      await act(async () => { release(); });
      if (outcome === "selected") {
        await waitFor(() => expect(listRuns).toHaveBeenCalledOnce());
        expect(listRuns).toHaveBeenCalledWith(expect.objectContaining({ repositoryId: "repo", worktreeId: "tree", inbox: true }), expect.anything());
      } else {
        await waitFor(() => expect(screen.queryByText("Loading repositories…")).toBeNull());
        fireEvent.click(screen.getByRole("button", { name: "Checks" }));
        expect(listRuns).not.toHaveBeenCalled();
      }
    } finally { unmount(); client.clear(); }
  });
}
