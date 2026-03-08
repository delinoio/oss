import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  WorkspaceEndpointSource,
  type ResolveWorkspaceConnectionInput,
} from "./contracts/workspace-connection";
import { WorkspaceMode } from "./contracts/workspace-mode";
import { App } from "./App";

function createMemoryStorage(): Storage {
  const values = new Map<string, string>();

  return {
    get length() {
      return values.size;
    },
    clear() {
      values.clear();
    },
    getItem(key) {
      return values.has(key) ? values.get(key) ?? null : null;
    },
    key(index) {
      return Array.from(values.keys())[index] ?? null;
    },
    removeItem(key) {
      values.delete(key);
    },
    setItem(key, value) {
      values.set(key, value);
    },
  };
}

const guardedRoutes = [
  "/projects",
  "/threads",
  "/review",
  "/automations",
  "/worktrees",
  "/local-environments",
  "/settings",
];

describe("App", () => {
  beforeEach(() => {
    Object.defineProperty(window, "localStorage", {
      value: createMemoryStorage(),
      configurable: true,
    });
    window.localStorage.clear();
  });

  it("renders workspace picker at root", () => {
    window.history.replaceState({}, "", "/");

    render(<App resolver={vi.fn()} />);

    expect(screen.getByText("DexDex")).toBeTruthy();
    expect(screen.getByText("Select a workspace to get started.")).toBeTruthy();
    expect(window.location.pathname).toBe("/");
  });

  it("opens LOCAL workspace and navigates to default page", async () => {
    window.history.replaceState({}, "", "/");

    const resolver = vi.fn().mockResolvedValue({
      mode: WorkspaceMode.Local,
      endpointUrl: "http://127.0.0.1:7878/",
      endpointSource: WorkspaceEndpointSource.ManagedLoopback,
      transport: "CONNECT_RPC",
    });

    const user = userEvent.setup();
    render(<App resolver={resolver} />);

    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.click(screen.getByRole("button", { name: "Connect" }));

    await waitFor(() => {
      expect(resolver).toHaveBeenCalledTimes(1);
      expect(window.location.pathname).toBe("/threads");
    });

    const firstArg = resolver.mock.calls[0][0] as ResolveWorkspaceConnectionInput;
    expect(firstArg).toMatchObject({
      mode: WorkspaceMode.Local,
    });

    expect(screen.getAllByText("workspace-1").length).toBeGreaterThanOrEqual(1);
  });

  it.each(guardedRoutes)(
    "guards desktop route %s and redirects to picker without active session",
    async (route) => {
      window.history.replaceState({}, "", route);

      render(<App resolver={vi.fn()} />);

      await waitFor(() => {
        expect(window.location.pathname).toBe("/");
        expect(screen.getByText("Select a workspace to get started.")).toBeTruthy();
      });
    },
  );

  it("switches back to workspace picker from desktop shell", async () => {
    window.history.replaceState({}, "", "/");

    const resolver = vi.fn().mockResolvedValue({
      mode: WorkspaceMode.Local,
      endpointUrl: "http://127.0.0.1:7878/",
      endpointSource: WorkspaceEndpointSource.ManagedLoopback,
      transport: "CONNECT_RPC",
    });

    const user = userEvent.setup();
    render(<App resolver={resolver} />);

    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.click(screen.getByRole("button", { name: "Connect" }));

    await waitFor(() => {
      expect(window.location.pathname).toBe("/threads");
    });

    await user.click(screen.getByRole("button", { name: "Switch" }));

    await waitFor(() => {
      expect(window.location.pathname).toBe("/");
      expect(screen.getByText("Select a workspace to get started.")).toBeTruthy();
    });
  });
});
