// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it, vi } from "vitest";
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
} from "@testing-library/react";
import {
  EntityKind,
  MCPService,
  MCPMutationState,
  ResourceService,
  ResourceSchema,
  SystemService,
  SystemCapability,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { MutationIntents } from "./mutation";
import { MCPSettings, decodeMCPDefinitions } from "./mcp-settings";
import { encode } from "./documents";
import { SettingsCategory } from "./settings-category";
import { settingsCategories } from "./settings";
import { english, korean } from "./locales/resources";
const definition = {
  id: "fixture",
  worker_device_id: "worker-a",
  revision: "1",
  name: "Fixture MCP",
  enabled: false,
  transport: "stdio",
  authentication: "none",
  support: [],
};
describe("MCP Settings safe projection", () => {
  it("rejects cross-Worker and malformed projections", () => {
    expect(decodeMCPDefinitions(encode([definition]), "worker-a")).toHaveLength(
      1,
    );
    expect(() =>
      decodeMCPDefinitions(encode([definition]), "worker-b"),
    ).toThrow();
    expect(() =>
      decodeMCPDefinitions(encode([definition, definition]), "worker-a"),
    ).toThrow();
    expect(() =>
      decodeMCPDefinitions(
        encode([{ ...definition, revision: "0" }]),
        "worker-a",
      ),
    ).toThrow();
  });
  it("keeps management separate from native support and supplies both languages", () => {
    expect(decodeMCPDefinitions(encode([definition]))[0].support).toEqual([]);
    expect(settingsCategories[SettingsCategory.MCP].label).toBe(
      english["mcp.title"],
    );
    for (const key of Object.keys(english).filter((k) =>
      k.startsWith("mcp."),
    )) {
      expect(korean[key as keyof typeof english]).toBeTruthy();
    }
  });
});

// These use an in-memory Connect router. They provide no native/account proof.
function managementFixture() {
  const first = newRequestId(),
    second = newRequestId(),
    workerA = newRequestId(),
    workerB = newRequestId();
  const rowA = {
    ...definition,
    id: newRequestId(),
    worker_device_id: workerA,
    name: "First MCP",
    enabled: true,
  };
  const rowB = {
    ...definition,
    id: newRequestId(),
    worker_device_id: workerB,
    name: "Second MCP",
    enabled: true,
  };
  const inventory = vi.fn(async (request: { machineId: string }) => ({
    workerDeviceId: request.machineId === first ? workerA : workerB,
    definitionsJson: encode([request.machineId === first ? rowA : rowB]),
  }));
  let original = "";
  const mutate = vi.fn(async (request: { requestId: string }) => {
    original = request.requestId;
    throw new ConnectError("Synthetic response loss", Code.Unavailable);
  });
  const inspect = vi.fn(async (request: { requestId: string }) => ({
    requestId: request.requestId,
    state: MCPMutationState.MCP_MUTATION_STATE_SUCCEEDED,
    resultJson: encode({
      worker_device_id: workerA,
      request_id: request.requestId,
      state: "succeeded",
      definitions: [{ ...rowA, enabled: false, revision: "2" }],
    }),
  }));
  const resources = vi.fn((request: { filter?: { kind: EntityKind } }) => {
    expect(request.filter?.kind).toBe(EntityKind.MACHINE);
    return {
      resources: [first, second].map((id, i) =>
        create(ResourceSchema, {
          id,
          kind: EntityKind.MACHINE,
          schemaVersion: 1,
          revision: 1n,
          documentJson: encode({
            name: i === 0 ? "First Runner" : "Second Runner",
          }),
        }),
      ),
    };
  });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({ capabilities: [SystemCapability.MANAGED_MCP_V1] }),
    });
    router.service(ResourceService, { listResources: resources });
    router.service(MCPService, {
      listMCPDefinitions: inventory,
      mutateMCPDefinition: mutate,
      getMCPMutation: inspect,
      authenticateMCP: () => {
        throw new Error("Settings must not authenticate implicitly");
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  function View() {
    return (
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <MutationIntents>
            <MCPSettings active />
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
    );
  }
  return {
    first,
    second,
    rowA,
    rowB,
    inventory,
    mutate,
    inspect,
    View,
    get original() {
      return original;
    },
  };
}
it("keeps Runner rows separate, supports search and restores modal focus on Esc", async () => {
  const f = managementFixture();
  render(<f.View />);
  await screen.findByRole("option", { name: "First Runner" });
  expect(f.mutate).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Runner Device"), {
    target: { value: f.first },
  });
  await screen.findByText("First MCP");
  const add = screen.getByRole("button", { name: "Add MCP" });
  add.focus();
  fireEvent.click(add);
  const dialog = screen.getByRole("dialog");
  await waitFor(() =>
    expect(document.activeElement).toBe(screen.getByLabelText("Name")),
  );
  fireEvent(dialog, new Event("cancel", { cancelable: true }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(document.activeElement).toBe(add);
  fireEvent.change(screen.getByLabelText("Search MCP"), {
    target: { value: "missing" },
  });
  expect(screen.queryByText("First MCP")).toBeNull();
  fireEvent.change(screen.getByLabelText("Search MCP"), {
    target: { value: "" },
  });
  fireEvent.change(screen.getByLabelText("Runner Device"), {
    target: { value: f.second },
  });
  await screen.findByText("Second MCP");
  expect(screen.queryByText("First MCP")).toBeNull();
  expect(f.mutate).not.toHaveBeenCalled();
});
it("recovers a lost original acknowledgement by inspection without replay", async () => {
  const f = managementFixture();
  render(<f.View />);
  await screen.findByRole("option", { name: "First Runner" });
  fireEvent.change(screen.getByLabelText("Runner Device"), {
    target: { value: f.first },
  });
  await screen.findByText("First MCP");
  fireEvent.click(screen.getByRole("button", { name: "Disable" }));
  await screen.findByText(english["mcp.uncertain"]);
  expect(f.mutate).toHaveBeenCalledTimes(1);
  expect(
    (screen.getByRole("button", { name: "Disable" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  fireEvent.click(
    screen.getByRole("button", { name: "Inspect original request" }),
  );
  await waitFor(() =>
    expect(screen.queryByText(english["mcp.uncertain"])).toBeNull(),
  );
  expect(f.inspect.mock.calls[0][0].requestId).toBe(f.original);
  expect(f.mutate).toHaveBeenCalledTimes(1);
});
it("does not publish a late first-Runner observation into the second Runner", async () => {
  const f = managementFixture();
  let resolve!: (value: {
    workerDeviceId: string;
    definitionsJson: Uint8Array;
  }) => void;
  f.inventory.mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  render(<f.View />);
  await screen.findByRole("option", { name: "First Runner" });
  fireEvent.change(screen.getByLabelText("Runner Device"), {
    target: { value: f.first },
  });
  await waitFor(() => expect(f.inventory).toHaveBeenCalledTimes(1));
  fireEvent.change(screen.getByLabelText("Runner Device"), {
    target: { value: f.second },
  });
  await screen.findByText("Second MCP");
  await act(async () =>
    resolve({
      workerDeviceId: f.rowA.worker_device_id,
      definitionsJson: encode([f.rowA]),
    }),
  );
  expect(screen.queryByText("First MCP")).toBeNull();
  expect(screen.getByText("Second MCP")).toBeTruthy();
});
