import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ExecutionState, LocalService, RunSchema } from "@delinoio/async-commit-hook-api-client";
import { expect, it, vi } from "vitest";
import { Workspace } from "./App";

it.each(["first-page", "later-page", "unavailable"])("binds execution deep links to their worktree (%s)", async (placement) => {
  let release = () => {};
  const pendingRun = new Promise<void>((resolve) => { release = resolve; });
  const first = { id: "repo-a", name: "Repository A", worktrees: [{ id: "tree-a", path: "/repo-a", branch: "main-a", branchId: "branch-a", available: true }] };
  const target = { id: "repo-b", name: "Repository B", worktrees: [{ id: "tree-b", path: "/repo-b", branch: "feature-b", branchId: "branch-b", available: false }] };
  let failNext = true;
  const listRepositories = vi.fn((request: { cursor: string }) => {
    if (request.cursor) {
      if (failNext) throw new ConnectError("Page unavailable", Code.Unavailable);
      return { repositories: [target] };
    }
    return { repositories: placement === "first-page" ? [first, target] : [first], nextCursor: placement === "later-page" ? "next" : "" };
  });
  const getRun = vi.fn(async () => {
    await pendingRun;
    return { run: create(RunSchema, { id: "deep-run", repositoryId: "repo-b", worktreeId: "tree-b", commit: "bbbbbbbbbbbb", branch: "historical-b", state: ExecutionState.FAILED }) };
  });
  const listBranches = vi.fn(() => ({ branches: [] }));
  const listRuns = vi.fn(() => ({ runs: [] }));
  const acknowledge = vi.fn(() => ({}));
  const transport = createRouterTransport((router) => router.service(LocalService, {
    listRepositories, getRun, listBranches, listRuns, acknowledge, getVersion: () => ({ apiVersion: 1 }),
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { unmount } = render(<QueryClientProvider client={client}><TransportProvider transport={transport}>
    <Workspace initialRun="deep-run" onPair={() => {}} />
  </TransportProvider></QueryClientProvider>);
  try {
    const sidebar = within(screen.getByRole("complementary", { name: "Repositories" }));
    const firstButton = await sidebar.findByRole("button", { name: /\/repo-a/ });
    expect(firstButton.getAttribute("aria-current")).toBeNull();
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("Execution workspace");
    expect(screen.queryByRole("combobox", { name: "Branch" })).toBeNull();
    expect(listBranches).not.toHaveBeenCalled();
    expect(listRuns).not.toHaveBeenCalled();
    await act(async () => { release(); });
    const detail = await screen.findByRole("heading", { name: "Commit bbbbbbbbbbbb" });
    expect(document.activeElement).toBe(detail);
    expect(getRun).toHaveBeenCalledOnce();
    expect(acknowledge).not.toHaveBeenCalled();
    if (placement !== "first-page") {
      expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("Execution workspace");
      expect(screen.queryByRole("combobox", { name: "Branch" })).toBeNull();
      expect(firstButton.getAttribute("aria-current")).toBeNull();
      expect(listRepositories).toHaveBeenCalledOnce();
      expect(listBranches).not.toHaveBeenCalled();
    }
    if (placement === "unavailable") return;
    if (placement === "later-page") {
      fireEvent.click(sidebar.getByRole("button", { name: "Load more workspaces" }));
      await sidebar.findByRole("alert");
      expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("Execution workspace");
      expect(screen.getByRole("heading", { name: "Commit bbbbbbbbbbbb" })).toBe(detail);
      failNext = false;
      fireEvent.click(sidebar.getByRole("button", { name: "Try again" }));
    }
    await screen.findByRole("heading", { level: 1, name: "Repository B" });
    const targetButton = await sidebar.findByRole("button", { name: /\/repo-b/ });
    await waitFor(() => expect(targetButton.getAttribute("aria-current")).toBe("true"));
    expect(firstButton.getAttribute("aria-current")).toBeNull();
    expect((screen.getByRole("combobox", { name: "Branch" }) as HTMLSelectElement).value).toBe("branch-b");
    expect(listBranches).toHaveBeenCalledWith(expect.objectContaining({ worktreeId: "tree-b" }), expect.anything());
    expect(getRun).toHaveBeenCalledOnce();
    expect(document.activeElement).toBe(detail);
    fireEvent.click(screen.getByRole("button", { name: "← All executions" }));
    await waitFor(() => expect(listRuns).toHaveBeenCalledWith(expect.objectContaining({ repositoryId: "repo-b", worktreeId: "tree-b", branchId: "branch-b" }), expect.anything()));
    expect(acknowledge).not.toHaveBeenCalled();
  } finally { unmount(); client.clear(); }
});

it.each(["missing", "error"])("does not assign another workspace when the linked execution is %s", async (outcome) => {
  const listBranches = vi.fn(() => ({ branches: [] }));
  const transport = createRouterTransport((router) => router.service(LocalService, {
    listRepositories: () => ({ repositories: [{ id: "repo-a", name: "Repository A", worktrees: [{ id: "tree-a", path: "/repo-a", branch: "main" }] }] }),
    getVersion: () => ({ apiVersion: 1 }), listBranches,
    getRun: () => {
      if (outcome === "error") throw new ConnectError("Execution unavailable", Code.NotFound);
      return {};
    },
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { unmount } = render(<QueryClientProvider client={client}><TransportProvider transport={transport}>
    <Workspace initialRun="deep-run" onPair={() => {}} />
  </TransportProvider></QueryClientProvider>);
  try {
    await screen.findByRole("alert");
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("Execution workspace");
    expect(screen.queryByRole("combobox", { name: "Branch" })).toBeNull();
    expect(listBranches).not.toHaveBeenCalled();
  } finally { unmount(); client.clear(); }
});
