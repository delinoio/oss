import { Code, ConnectError } from "@connectrpc/connect";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useQuery } from "@connectrpc/connect-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { WorkspaceEndpointSource } from "../contracts/workspace-connection";
import { getWorkspace } from "../gen/v1/dexdex-WorkspaceService_connectquery";
import { RpcDashboard } from "./rpc-dashboard";

vi.mock("@connectrpc/connect-query", async () => {
  const actual = await vi.importActual<typeof import("@connectrpc/connect-query")>(
    "@connectrpc/connect-query",
  );

  return {
    ...actual,
    useQuery: vi.fn(),
  };
});

const useQueryMock = vi.mocked(useQuery);

function createIdleResult() {
  return {
    data: undefined,
    error: null,
    isPending: false,
    isFetching: false,
  };
}

describe("RpcDashboard", () => {
  beforeEach(() => {
    useQueryMock.mockImplementation((schema, input) => {
      if (!input) {
        return createIdleResult() as never;
      }

      if (schema === getWorkspace) {
        const workspaceId = (input as { workspaceId: string }).workspaceId;
        if (workspaceId === "workspace-found") {
          return {
            ...createIdleResult(),
            data: {
              workspace: {
                workspaceId: "workspace-found",
              },
            },
          } as never;
        }
        if (workspaceId === "workspace-missing") {
          return {
            ...createIdleResult(),
            error: new ConnectError("workspace not found", Code.NotFound),
          } as never;
        }
      }

      return createIdleResult() as never;
    });
  });

  it("submits workspace lookup and renders successful result", async () => {
    const user = userEvent.setup();

    render(
      <RpcDashboard
        connection={{
          mode: "REMOTE",
          endpointUrl: "https://dexdex.example/rpc",
          endpointSource: WorkspaceEndpointSource.UserRemote,
          token: "token-1",
          transport: "CONNECT_RPC",
        }}
      />,
    );

    await user.clear(screen.getByLabelText("Workspace ID"));
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-found");
    await user.click(screen.getByRole("button", { name: "Fetch Workspace" }));

    await waitFor(() => {
      expect(useQueryMock).toHaveBeenCalledWith(
        getWorkspace,
        { workspaceId: "workspace-found" },
        expect.objectContaining({ enabled: true }),
      );
    });

    expect(screen.getByTestId("workspace-result").textContent).toContain(
      "workspace-found",
    );
  });

  it("renders not-found error message for missing workspace lookup", async () => {
    const user = userEvent.setup();

    render(
      <RpcDashboard
        connection={{
          mode: "REMOTE",
          endpointUrl: "https://dexdex.example/rpc",
          endpointSource: WorkspaceEndpointSource.UserRemote,
          token: "token-1",
          transport: "CONNECT_RPC",
        }}
      />,
    );

    await user.clear(screen.getByLabelText("Workspace ID"));
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-missing");
    await user.click(screen.getByRole("button", { name: "Fetch Workspace" }));

    expect(
      await screen.findByText("No workspace found for this workspace id."),
    ).toBeTruthy();
  });

  it("keeps workspace lookup history deduped, recency-ordered, and capped to five", async () => {
    const user = userEvent.setup();

    render(
      <RpcDashboard
        connection={{
          mode: "REMOTE",
          endpointUrl: "https://dexdex.example/rpc",
          endpointSource: WorkspaceEndpointSource.UserRemote,
          token: "token-1",
          transport: "CONNECT_RPC",
        }}
      />,
    );

    for (const workspaceId of ["a", "b", "a", "c", "d", "e", "f"]) {
      await user.clear(screen.getByLabelText("Workspace ID"));
      await user.type(screen.getByLabelText("Workspace ID"), workspaceId);
      await user.click(screen.getByRole("button", { name: "Fetch Workspace" }));
    }

    for (const expected of ["f", "e", "d", "c", "a"]) {
      expect(screen.getByRole("button", { name: expected })).toBeTruthy();
    }
    expect(screen.queryByRole("button", { name: "b" })).toBeNull();
    expect(screen.getAllByRole("button", { name: "a" })).toHaveLength(1);
  });
});
