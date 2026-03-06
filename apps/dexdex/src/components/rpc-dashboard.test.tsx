import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useQuery } from "@connectrpc/connect-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { WorkspaceEndpointSource } from "../contracts/workspace-connection";
import { getWorkspace } from "../gen/v1/dexdex-WorkspaceService_connectquery";
import {
  AgentCliType,
  AgentSessionStatus,
  PlanDecision,
  SessionAdapterFixturePreset,
} from "../gen/v1/dexdex_pb";
import { createDexDexTransport } from "../lib/connect-query-provider";
import { DashboardSectionId, RpcDashboard } from "./rpc-dashboard";

vi.mock("@connectrpc/connect-query", async () => {
  const actual = await vi.importActual<typeof import("@connectrpc/connect-query")>(
    "@connectrpc/connect-query",
  );

  return {
    ...actual,
    useQuery: vi.fn(),
  };
});

vi.mock("@connectrpc/connect", async () => {
  const actual = await vi.importActual<typeof import("@connectrpc/connect")>(
    "@connectrpc/connect",
  );

  return {
    ...actual,
    createClient: vi.fn(),
  };
});

vi.mock("../lib/connect-query-provider", async () => {
  const actual =
    await vi.importActual<typeof import("../lib/connect-query-provider")>(
      "../lib/connect-query-provider",
    );

  return {
    ...actual,
    createDexDexTransport: vi.fn(() => ({}) as never),
  };
});

const useQueryMock = vi.mocked(useQuery);
const createClientMock = vi.mocked(createClient);
const createDexDexTransportMock = vi.mocked(createDexDexTransport);
const submitPlanDecisionMock = vi.fn();
const runSubTaskSessionAdapterMock = vi.fn();
const streamWorkspaceEventsMock = vi.fn();

function createIdleResult() {
  return {
    data: undefined,
    error: null,
    isPending: false,
    isFetching: false,
  };
}

function createDeferredPromise<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("RpcDashboard", () => {
  beforeEach(() => {
    submitPlanDecisionMock.mockReset();
    runSubTaskSessionAdapterMock.mockReset();
    streamWorkspaceEventsMock.mockReset();
    createDexDexTransportMock.mockReset();
    createDexDexTransportMock.mockReturnValue({} as never);

    submitPlanDecisionMock.mockResolvedValue({
      updatedSubTask: {
        subTaskId: "sub-1",
        status: 2,
      },
    });
    runSubTaskSessionAdapterMock.mockResolvedValue({
      updatedSubTask: {
        subTaskId: "sub-1",
        status: 5,
      },
      emittedEventCount: 4n,
      sessionStatus: AgentSessionStatus.COMPLETED,
      sessionId: "session-1",
    });
    streamWorkspaceEventsMock.mockReturnValue((async function* () {
      // heartbeat event should be ignored in UI
      yield {
        sequence: 0n,
        eventType: 0,
      };
      yield {
        sequence: 1n,
        eventType: 2,
      };
    })());
    createClientMock.mockImplementation(
      () =>
        ({
          submitPlanDecision: submitPlanDecisionMock,
          runSubTaskSessionAdapter: runSubTaskSessionAdapterMock,
          streamWorkspaceEvents: streamWorkspaceEventsMock,
        }) as never,
    );

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

  it("renders the selected section only when activeSection is provided", () => {
    render(
      <RpcDashboard
        connection={{
          mode: "REMOTE",
          endpointUrl: "https://dexdex.example/rpc",
          endpointSource: WorkspaceEndpointSource.UserRemote,
          token: "token-1",
          transport: "CONNECT_RPC",
        }}
        activeSection={DashboardSectionId.Repository}
      />,
    );

    expect(
      screen.getByRole("button", { name: "Fetch Repository Group" }),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Fetch Workspace" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Submit Plan Decision" })).toBeNull();
  });

  it("submits approve plan decision and renders mutation result", async () => {
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.type(screen.getByLabelText("Plan Sub Task ID"), "sub-1");
    await user.click(screen.getByRole("button", { name: "Submit Plan Decision" }));

    await waitFor(() => {
      expect(submitPlanDecisionMock).toHaveBeenCalledWith({
        workspaceId: "workspace-1",
        subTaskId: "sub-1",
        decision: PlanDecision.APPROVE,
      });
    });

    expect(screen.getByTestId("plan-decision-result").textContent).toContain(
      '"status": 2',
    );
  });

  it("submits revise plan decision with revision note and renders created sub task", async () => {
    const user = userEvent.setup();
    submitPlanDecisionMock.mockResolvedValueOnce({
      updatedSubTask: {
        subTaskId: "sub-1",
        status: 5,
      },
      createdSubTask: {
        subTaskId: "workspace-1-subtask-2",
        status: 1,
        type: 2,
      },
    });

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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.type(screen.getByLabelText("Plan Sub Task ID"), "sub-1");
    await user.selectOptions(
      screen.getByLabelText("Plan Decision"),
      screen.getByRole("option", { name: "REVISE" }),
    );
    await user.type(
      screen.getByLabelText("Revision Note"),
      "Need clearer failure handling",
    );
    await user.click(screen.getByRole("button", { name: "Submit Plan Decision" }));

    await waitFor(() => {
      expect(submitPlanDecisionMock).toHaveBeenCalledWith({
        workspaceId: "workspace-1",
        subTaskId: "sub-1",
        decision: PlanDecision.REVISE,
        revisionNote: "Need clearer failure handling",
      });
    });

    expect(screen.getByTestId("plan-decision-result").textContent).toContain(
      "workspace-1-subtask-2",
    );
  });

  it("blocks revise plan decision without revision note", async () => {
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.type(screen.getByLabelText("Plan Sub Task ID"), "sub-1");
    await user.selectOptions(
      screen.getByLabelText("Plan Decision"),
      screen.getByRole("option", { name: "REVISE" }),
    );
    await user.click(screen.getByRole("button", { name: "Submit Plan Decision" }));

    expect(submitPlanDecisionMock).not.toHaveBeenCalled();
    expect(
      screen.getByText("Submit Plan Decision: revision note is required."),
    ).toBeTruthy();
  });

  it("resets plan decision result panel when connection changes", async () => {
    const user = userEvent.setup();

    const { rerender } = render(
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.type(screen.getByLabelText("Plan Sub Task ID"), "sub-1");
    await user.click(screen.getByRole("button", { name: "Submit Plan Decision" }));

    expect(await screen.findByTestId("plan-decision-result")).toBeTruthy();

    rerender(
      <RpcDashboard
        connection={{
          mode: "REMOTE",
          endpointUrl: "https://dexdex-other.example/rpc",
          endpointSource: WorkspaceEndpointSource.UserRemote,
          token: "token-2",
          transport: "CONNECT_RPC",
        }}
      />,
    );

    await waitFor(() => {
      expect(screen.queryByTestId("plan-decision-result")).toBeNull();
      expect(
        screen.getByText(
          "Submit plan decision to update the current sub task state.",
        ),
      ).toBeTruthy();
    });
  });

  it("resets plan decision error panel when connection changes", async () => {
    const user = userEvent.setup();
    submitPlanDecisionMock.mockRejectedValueOnce(
      new ConnectError("plan decision failed", Code.Internal),
    );

    const { rerender } = render(
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.type(screen.getByLabelText("Plan Sub Task ID"), "sub-1");
    await user.click(screen.getByRole("button", { name: "Submit Plan Decision" }));

    expect(await screen.findByText("plan decision failed")).toBeTruthy();

    rerender(
      <RpcDashboard
        connection={{
          mode: "REMOTE",
          endpointUrl: "https://dexdex-other.example/rpc",
          endpointSource: WorkspaceEndpointSource.UserRemote,
          token: "token-2",
          transport: "CONNECT_RPC",
        }}
      />,
    );

    await waitFor(() => {
      expect(screen.queryByText("plan decision failed")).toBeNull();
      expect(
        screen.getByText(
          "Submit plan decision to update the current sub task state.",
        ),
      ).toBeTruthy();
    });
  });

  it("resets plan decision pending flag on connection changes", async () => {
    const user = userEvent.setup();
    const deferred = createDeferredPromise<{
      updatedSubTask: { subTaskId: string; status: number };
      createdSubTask?: { subTaskId: string; status: number; type?: number };
    }>();

    submitPlanDecisionMock.mockReturnValueOnce(deferred.promise);

    const { rerender } = render(
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.type(screen.getByLabelText("Plan Sub Task ID"), "sub-1");
    await user.click(screen.getByRole("button", { name: "Submit Plan Decision" }));

    expect(
      screen.getByRole("button", { name: "Submitting..." }).hasAttribute("disabled"),
    ).toBe(true);

    rerender(
      <RpcDashboard
        connection={{
          mode: "REMOTE",
          endpointUrl: "https://dexdex-other.example/rpc",
          endpointSource: WorkspaceEndpointSource.UserRemote,
          token: "token-2",
          transport: "CONNECT_RPC",
        }}
      />,
    );

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "Submit Plan Decision" }),
      ).toHaveProperty("disabled", false);
    });

    deferred.resolve({
      updatedSubTask: { subTaskId: "sub-1", status: 2 },
    });
  });

  it("ignores stale plan decision responses after connection changes", async () => {
    const user = userEvent.setup();
    const deferred = createDeferredPromise<{
      updatedSubTask: { subTaskId: string; status: number };
      createdSubTask?: { subTaskId: string; status: number; type?: number };
    }>();

    submitPlanDecisionMock.mockReturnValueOnce(deferred.promise);

    const { rerender } = render(
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.type(screen.getByLabelText("Plan Sub Task ID"), "sub-1");
    await user.click(screen.getByRole("button", { name: "Submit Plan Decision" }));

    rerender(
      <RpcDashboard
        connection={{
          mode: "REMOTE",
          endpointUrl: "https://dexdex-other.example/rpc",
          endpointSource: WorkspaceEndpointSource.UserRemote,
          token: "token-2",
          transport: "CONNECT_RPC",
        }}
      />,
    );

    deferred.resolve({
      updatedSubTask: { subTaskId: "sub-1", status: 2 },
    });

    await waitFor(() => {
      expect(screen.queryByTestId("plan-decision-result")).toBeNull();
      expect(
        screen.getByText(
          "Submit plan decision to update the current sub task state.",
        ),
      ).toBeTruthy();
    });
  });

  it("reuses recent sub task history in the plan decision form", async () => {
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.type(screen.getByLabelText("Sub Task ID"), "sub-history-1");
    await user.click(screen.getByRole("button", { name: "Fetch Sub Task" }));

    const planDecisionCard = screen
      .getByRole("heading", { name: "TaskService.SubmitPlanDecision" })
      .closest("article");
    if (!planDecisionCard) {
      throw new Error("expected plan decision card");
    }

    await user.click(
      within(planDecisionCard).getByRole("button", { name: "sub-history-1" }),
    );

    expect(
      (screen.getByLabelText("Plan Sub Task ID") as HTMLInputElement).value,
    ).toBe("sub-history-1");
  });

  it("runs session adapter with fixture preset input and renders mutation result", async () => {
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.type(screen.getByLabelText("Run Unit Task ID"), "unit-1");
    await user.type(screen.getByLabelText("Run Sub Task ID"), "sub-1");
    await user.type(screen.getByLabelText("Run Session ID"), "session-1");
    await user.click(screen.getByRole("button", { name: "Run Session Adapter" }));

    await waitFor(() => {
      expect(runSubTaskSessionAdapterMock).toHaveBeenCalledWith({
        workspaceId: "workspace-1",
        unitTaskId: "unit-1",
        subTaskId: "sub-1",
        sessionId: "session-1",
        cliType: AgentCliType.CODEX_CLI,
        input: {
          case: "fixturePreset",
          value: SessionAdapterFixturePreset.CODEX_CLI_FAILURE,
        },
      });
    });

    expect(screen.getByTestId("session-adapter-result").textContent).toContain(
      '"sessionStatus": 4',
    );
  });

  it("resets session adapter result panel when connection changes", async () => {
    const user = userEvent.setup();

    const { rerender } = render(
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.type(screen.getByLabelText("Run Unit Task ID"), "unit-1");
    await user.type(screen.getByLabelText("Run Sub Task ID"), "sub-1");
    await user.type(screen.getByLabelText("Run Session ID"), "session-1");
    await user.click(screen.getByRole("button", { name: "Run Session Adapter" }));

    expect(await screen.findByTestId("session-adapter-result")).toBeTruthy();

    rerender(
      <RpcDashboard
        connection={{
          mode: "REMOTE",
          endpointUrl: "https://dexdex-other.example/rpc",
          endpointSource: WorkspaceEndpointSource.UserRemote,
          token: "token-2",
          transport: "CONNECT_RPC",
        }}
      />,
    );

    await waitFor(() => {
      expect(screen.queryByTestId("session-adapter-result")).toBeNull();
      expect(
        screen.getByText(
          "Run session adapter to execute fixture normalization.",
        ),
      ).toBeTruthy();
    });
  });

  it("resets session adapter error panel when connection changes", async () => {
    const user = userEvent.setup();
    runSubTaskSessionAdapterMock.mockRejectedValueOnce(
      new ConnectError("session adapter failed", Code.Internal),
    );

    const { rerender } = render(
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.type(screen.getByLabelText("Run Unit Task ID"), "unit-1");
    await user.type(screen.getByLabelText("Run Sub Task ID"), "sub-1");
    await user.type(screen.getByLabelText("Run Session ID"), "session-1");
    await user.click(screen.getByRole("button", { name: "Run Session Adapter" }));

    expect(await screen.findByText("session adapter failed")).toBeTruthy();

    rerender(
      <RpcDashboard
        connection={{
          mode: "REMOTE",
          endpointUrl: "https://dexdex-other.example/rpc",
          endpointSource: WorkspaceEndpointSource.UserRemote,
          token: "token-2",
          transport: "CONNECT_RPC",
        }}
      />,
    );

    await waitFor(() => {
      expect(screen.queryByText("session adapter failed")).toBeNull();
      expect(
        screen.getByText(
          "Run session adapter to execute fixture normalization.",
        ),
      ).toBeTruthy();
    });
  });

  it("resets session adapter pending flag on connection changes", async () => {
    const user = userEvent.setup();
    const deferred = createDeferredPromise<{
      updatedSubTask: { subTaskId: string; status: number };
      emittedEventCount: bigint;
      sessionStatus: AgentSessionStatus;
      sessionId: string;
    }>();

    runSubTaskSessionAdapterMock.mockReturnValueOnce(deferred.promise);

    const { rerender } = render(
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.type(screen.getByLabelText("Run Unit Task ID"), "unit-1");
    await user.type(screen.getByLabelText("Run Sub Task ID"), "sub-1");
    await user.type(screen.getByLabelText("Run Session ID"), "session-1");
    await user.click(screen.getByRole("button", { name: "Run Session Adapter" }));

    expect(
      screen.getByRole("button", { name: "Running..." }).hasAttribute("disabled"),
    ).toBe(true);

    rerender(
      <RpcDashboard
        connection={{
          mode: "REMOTE",
          endpointUrl: "https://dexdex-other.example/rpc",
          endpointSource: WorkspaceEndpointSource.UserRemote,
          token: "token-2",
          transport: "CONNECT_RPC",
        }}
      />,
    );

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "Run Session Adapter" }),
      ).toHaveProperty("disabled", false);
    });

    deferred.resolve({
      updatedSubTask: { subTaskId: "sub-1", status: 5 },
      emittedEventCount: 4n,
      sessionStatus: AgentSessionStatus.COMPLETED,
      sessionId: "session-1",
    });
  });

  it("ignores stale session adapter responses after connection changes", async () => {
    const user = userEvent.setup();
    const deferred = createDeferredPromise<{
      updatedSubTask: { subTaskId: string; status: number };
      emittedEventCount: bigint;
      sessionStatus: AgentSessionStatus;
      sessionId: string;
    }>();

    runSubTaskSessionAdapterMock.mockReturnValueOnce(deferred.promise);

    const { rerender } = render(
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.type(screen.getByLabelText("Run Unit Task ID"), "unit-1");
    await user.type(screen.getByLabelText("Run Sub Task ID"), "sub-1");
    await user.type(screen.getByLabelText("Run Session ID"), "session-1");
    await user.click(screen.getByRole("button", { name: "Run Session Adapter" }));

    rerender(
      <RpcDashboard
        connection={{
          mode: "REMOTE",
          endpointUrl: "https://dexdex-other.example/rpc",
          endpointSource: WorkspaceEndpointSource.UserRemote,
          token: "token-2",
          transport: "CONNECT_RPC",
        }}
      />,
    );

    deferred.resolve({
      updatedSubTask: { subTaskId: "sub-1", status: 5 },
      emittedEventCount: 4n,
      sessionStatus: AgentSessionStatus.COMPLETED,
      sessionId: "session-1",
    });

    await waitFor(() => {
      expect(screen.queryByTestId("session-adapter-result")).toBeNull();
      expect(
        screen.getByText(
          "Run session adapter to execute fixture normalization.",
        ),
      ).toBeTruthy();
    });
  });

  it("starts live stream and renders non-heartbeat events only", async () => {
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.clear(screen.getByLabelText("From Sequence"));
    await user.type(screen.getByLabelText("From Sequence"), "0");
    await user.click(screen.getByRole("button", { name: "Start Live Stream" }));

    await waitFor(() => {
      expect(streamWorkspaceEventsMock).toHaveBeenCalledWith(
        {
          workspaceId: "workspace-1",
          fromSequence: 0n,
        },
        expect.objectContaining({
          signal: expect.any(AbortSignal),
        }),
      );
    });

    expect(screen.queryByText("#0")).toBeNull();
    expect(screen.getByText("#1")).toBeTruthy();
  });

  it("aborts running stream when connection changes", async () => {
    const user = userEvent.setup();

    streamWorkspaceEventsMock.mockImplementation(
      (_request: unknown, options?: { signal?: AbortSignal }) =>
        (async function* () {
          const signal = options?.signal;
          await new Promise<void>((resolve) => {
            if (!signal) {
              resolve();
              return;
            }
            if (signal.aborted) {
              resolve();
              return;
            }
            signal.addEventListener("abort", () => resolve(), { once: true });
          });
        })(),
    );

    const { rerender } = render(
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.clear(screen.getByLabelText("From Sequence"));
    await user.type(screen.getByLabelText("From Sequence"), "0");
    await user.click(screen.getByRole("button", { name: "Start Live Stream" }));

    await waitFor(() => {
      expect(streamWorkspaceEventsMock).toHaveBeenCalled();
    });

    const firstCall = streamWorkspaceEventsMock.mock.calls[0];
    const firstSignal = (firstCall?.[1] as { signal?: AbortSignal } | undefined)
      ?.signal;
    if (!firstSignal) {
      throw new Error("expected stream call with abort signal");
    }

    rerender(
      <RpcDashboard
        connection={{
          mode: "REMOTE",
          endpointUrl: "https://dexdex-next.example/rpc",
          endpointSource: WorkspaceEndpointSource.UserRemote,
          token: "token-2",
          transport: "CONNECT_RPC",
        }}
      />,
    );

    await waitFor(() => {
      expect(firstSignal.aborted).toBe(true);
    });
  });

  it("resets stream panel state on connection change even after stream stops", async () => {
    const user = userEvent.setup();

    const { rerender } = render(
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
    await user.type(screen.getByLabelText("Workspace ID"), "workspace-1");
    await user.clear(screen.getByLabelText("From Sequence"));
    await user.type(screen.getByLabelText("From Sequence"), "0");
    await user.click(screen.getByRole("button", { name: "Start Live Stream" }));

    expect(await screen.findByText("#1")).toBeTruthy();
    expect(screen.getByText("Stream status:")).toBeTruthy();
    expect(screen.getByText("STOPPED")).toBeTruthy();

    rerender(
      <RpcDashboard
        connection={{
          mode: "REMOTE",
          endpointUrl: "https://dexdex-other.example/rpc",
          endpointSource: WorkspaceEndpointSource.UserRemote,
          token: "token-2",
          transport: "CONNECT_RPC",
        }}
      />,
    );

    await waitFor(() => {
      expect(screen.queryByText("#1")).toBeNull();
      expect(screen.getByText("IDLE")).toBeTruthy();
    });
  });
});
