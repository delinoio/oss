import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import {
  WorkspaceEndpointSource,
  type ResolveWorkspaceConnectionInput,
} from "./contracts/workspace-connection";
import { WorkspaceMode } from "./contracts/workspace-mode";
import { App } from "./App";

describe("App", () => {
  it("resolves LOCAL mode and renders normalized connection summary", async () => {
    const resolver = vi.fn().mockResolvedValue({
      mode: WorkspaceMode.Local,
      endpointUrl: "http://127.0.0.1:7878/",
      endpointSource: WorkspaceEndpointSource.ManagedLoopback,
      transport: "CONNECT_RPC",
    });

    const user = userEvent.setup();
    render(<App resolver={resolver} />);

    await user.click(screen.getByRole("button", { name: "Resolve Workspace" }));

    await waitFor(() => expect(resolver).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId("connection-summary").textContent).toContain(
      "CONNECT_RPC",
    );
    expect(screen.getByTestId("connection-summary").textContent).toContain(
      "MANAGED_LOOPBACK",
    );
    expect(screen.getByTestId("rpc-dashboard")).toBeTruthy();
  });

  it("resolves REMOTE mode through the same summary flow", async () => {
    const resolver = vi.fn().mockResolvedValue({
      mode: WorkspaceMode.Remote,
      endpointUrl: "https://dexdex.example/rpc",
      endpointSource: WorkspaceEndpointSource.UserRemote,
      token: "token-1",
      transport: "CONNECT_RPC",
    });

    const user = userEvent.setup();
    render(<App resolver={resolver} />);

    await user.selectOptions(
      screen.getByRole("combobox", { name: "Workspace Mode" }),
      WorkspaceMode.Remote,
    );
    await user.clear(
      screen.getByRole("textbox", { name: "Remote Endpoint URL" }),
    );
    await user.type(
      screen.getByRole("textbox", { name: "Remote Endpoint URL" }),
      "https://dexdex.example/rpc",
    );
    await user.type(
      screen.getByLabelText("Remote Token (optional)"),
      "token-1",
    );
    await user.click(screen.getByRole("button", { name: "Resolve Workspace" }));

    await waitFor(() => expect(resolver).toHaveBeenCalledTimes(1));

    const firstArg = resolver.mock.calls[0][0] as ResolveWorkspaceConnectionInput;
    expect(firstArg).toMatchObject({
      mode: WorkspaceMode.Remote,
      remoteEndpointUrl: "https://dexdex.example/rpc",
      remoteToken: "token-1",
    });

    expect(screen.getByTestId("connection-summary").textContent).toContain(
      "USER_REMOTE",
    );
    expect(screen.getByTestId("connection-summary").textContent).toContain(
      "CONNECT_RPC",
    );
    expect(screen.getByTestId("rpc-dashboard")).toBeTruthy();
  });

  it("shows actionable error state on resolve failure", async () => {
    const resolver = vi
      .fn()
      .mockRejectedValue(new Error("remoteEndpointUrl must be a valid absolute URL."));

    const user = userEvent.setup();
    render(<App resolver={resolver} />);

    await user.selectOptions(
      screen.getByRole("combobox", { name: "Workspace Mode" }),
      WorkspaceMode.Remote,
    );
    await user.click(screen.getByRole("button", { name: "Resolve Workspace" }));

    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain("valid absolute URL");
  });
});
