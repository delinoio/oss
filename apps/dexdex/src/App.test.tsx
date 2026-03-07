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

    expect(
      screen.getByRole("heading", { level: 1, name: "Workspace Picker" }),
    ).toBeTruthy();
    expect(window.location.pathname).toBe("/");
    expect(screen.queryByTestId("rpc-dashboard")).toBeNull();
  });

  it("opens LOCAL workspace and navigates to /projects", async () => {
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
    await user.click(screen.getByRole("button", { name: "Open Workspace" }));

    await waitFor(() => {
      expect(resolver).toHaveBeenCalledTimes(1);
      expect(window.location.pathname).toBe("/projects");
      expect(screen.getByTestId("rpc-dashboard")).toBeTruthy();
    });

    const firstArg = resolver.mock.calls[0][0] as ResolveWorkspaceConnectionInput;
    expect(firstArg).toMatchObject({
      mode: WorkspaceMode.Local,
    });

    expect(screen.getByText(/Workspace ID:/).textContent).toContain("workspace-1");
  });

  it("guards desktop routes and redirects to picker without active session", async () => {
    window.history.replaceState({}, "", "/projects");

    render(<App resolver={vi.fn()} />);

    await waitFor(() => {
      expect(window.location.pathname).toBe("/");
      expect(
        screen.getByRole("heading", { level: 1, name: "Workspace Picker" }),
      ).toBeTruthy();
    });
  });

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
    await user.click(screen.getByRole("button", { name: "Open Workspace" }));

    await waitFor(() => {
      expect(window.location.pathname).toBe("/projects");
      expect(screen.getByTestId("rpc-dashboard")).toBeTruthy();
    });

    await user.click(screen.getByRole("button", { name: "Switch Workspace" }));

    await waitFor(() => {
      expect(window.location.pathname).toBe("/");
      expect(
        screen.getByRole("heading", { level: 1, name: "Workspace Picker" }),
      ).toBeTruthy();
      expect(screen.queryByTestId("rpc-dashboard")).toBeNull();
    });
  });
});
