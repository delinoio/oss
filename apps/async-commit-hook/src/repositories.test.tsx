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
    <Workspace initialRun="" onPair={() => {}} />
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
    <Workspace initialRun="" onPair={() => {}} />
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
