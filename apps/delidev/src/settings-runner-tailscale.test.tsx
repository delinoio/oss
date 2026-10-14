// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { useState } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  ProviderService,
  ResourceSchema,
  ResourceService,
  SystemCapability,
  SystemService,
  TailscaleService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { ConnectionsPage } from "./connections-page";
import { encode } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { ProductIdentityProvider } from "./product-identity-labels";
import type { RunnerTailscaleSlots } from "./runner-tailscale-slots";
import { Settings } from "./settings";
import { TailscaleConnections } from "./tailscale-connections";

// Issue 3660: the desktop-owned Tailscale controller presents access and
// devices in Runner Devices through typed slots. Every identity is synthetic.
enum Scenario {
  Supported = "supported",
  Unsupported = "unsupported",
}
const RUNNER = "Runner Devices";
const AWAY = "Browser";

const runner = create(ResourceSchema, {
  id: newRequestId(),
  kind: EntityKind.MACHINE,
  schemaVersion: 1,
  revision: 3n,
  documentJson: encode({ name: "Fixture runner" }),
});
const peer = {
  id: "peer-1",
  name: "Fixture peer",
  host: "peer.example",
  ownership: "own",
  online: true,
  shared_to: false,
};
function pending(id: string) {
  return {
    id,
    name: "Incoming device",
    server_id: "server",
    origin: "https://incoming.example",
    role: "client",
    code: "654321",
    state: "pending",
  };
}

interface Options {
  scenario?: Scenario;
  local?: boolean;
  incoming?: string;
  loseAccess?: boolean;
  loseDecision?: boolean;
  loseResume?: boolean;
  operation?: string;
  completeClient?: boolean;
}
function fixture(options: Options = {}) {
  const {
    scenario = Scenario.Supported,
    local = true,
    incoming,
    operation,
  } = options;
  let access = false;
  const calls = {
    getAccess: vi.fn(),
    discover: vi.fn(),
    check: vi.fn(),
    listConnections: vi.fn(),
    listApprovals: vi.fn(),
    setAccess: vi.fn<(request: { requestId: string }) => void>(),
    begin: vi.fn(),
    decide: vi.fn<(request: { requestId: string }) => void>(),
    resume: vi.fn<(request: { requestId: string }) => void>(),
    cancel: vi.fn(),
  };
  const lose = (flag: boolean | undefined, count: number) => {
    if (flag && count === 1) throw new ConnectError("Lost", Code.Unavailable);
  };
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {
      listResources: (request) => ({
        resources:
          request.filter?.kind === EntityKind.MACHINE ? [runner] : [],
        nextPageToken: "",
      }),
      getResource: () => ({ resource: runner }),
    });
    router.service(ProviderService, {
      listProviderInventory: () => ({ entries: [] }),
      listProviderPresets: () => ({ presetsJson: encode([]) }),
    });
    router.service(SystemService, {
      getStatus: () => ({
        capabilities:
          scenario === Scenario.Supported
            ? [SystemCapability.TAILSCALE_CONNECTIONS_V1]
            : [],
      }),
    });
    router.service(TailscaleService, {
      getTailscaleAccess: () => {
        calls.getAccess();
        return {
          documentJson: encode({
            enabled: access,
            state: access ? "ready" : "off",
            server_id: "s",
          }),
        };
      },
      listTailscaleApprovals: () => {
        calls.listApprovals();
        return { documentJson: encode(incoming ? [pending(incoming)] : []) };
      },
      discoverTailscale: () => {
        calls.discover();
        return {
          documentJson: encode({
            state: "running",
            peers: [peer],
            observed_at: "2026-01-01T00:00:00Z",
          }),
        };
      },
      checkTailscale: () => {
        calls.check();
        return {
          documentJson: encode({
            server_id: "s",
            origin: "https://peer.example",
            approval: true,
          }),
        };
      },
      listTailscaleConnections: () => {
        calls.listConnections();
        return { documentJson: encode(operation ? [pending(operation)] : []) };
      },
      setTailscaleAccess: (request) => {
        calls.setAccess(request);
        lose(options.loseAccess, calls.setAccess.mock.calls.length);
        access = request.enabled;
        return {
          documentJson: encode({
            enabled: access,
            state: access ? "ready" : "off",
            server_id: "s",
          }),
        };
      },
      beginTailscaleConnection: () => {
        calls.begin();
        return {
          documentJson: encode({
            ...pending(newRequestId()),
            state: options.completeClient ? "completed" : "pending",
            profile_id: options.completeClient ? newRequestId() : undefined,
          }),
        };
      },
      decideTailscaleApproval: (request) => {
        calls.decide(request);
        lose(options.loseDecision, calls.decide.mock.calls.length);
        return { documentJson: encode({ ...pending(request.requestId), state: "approved" }) };
      },
      resumeTailscaleConnection: (request) => {
        calls.resume(request);
        lose(options.loseResume, calls.resume.mock.calls.length);
        return { documentJson: encode({ ...pending(request.requestId), state: "denied" }) };
      },
      cancelTailscaleConnection: () => {
        calls.cancel();
        return { documentJson: encode(pending(newRequestId())) };
      },
    });
  });
  const paired = vi.fn();
  // The local window mounts the controller beside Settings and feeds it the
  // slots Settings publishes; a saved-server window has neither.
  function Owner() {
    const [slots, setSlots] = useState<RunnerTailscaleSlots>();
    return (
      <>
        <Settings
          visible
          runnerTailscale={local ? setSlots : undefined}
          connectionSettings={
            local ? <ConnectionsPage local onSlots={() => {}} /> : undefined
          }
        />
        {local ? (
          <TailscaleConnections
            current={slots?.access}
            target={slots?.devices}
            onPaired={paired}
          />
        ) : null}
      </>
    );
  }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <ProductIdentityProvider>
          <MutationIntents>
            <Owner />
          </MutationIntents>
        </ProductIdentityProvider>
      </QueryClientProvider>
    </TransportProvider>,
  );
  return { calls, paired };
}

const category = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const mutations = (calls: ReturnType<typeof fixture>["calls"]) =>
  [calls.setAccess, calls.begin, calls.decide, calls.resume, calls.cancel].map(
    (call) => call.mock.calls.length,
  );

afterEach(async () => {
  cleanup();
  document.body.replaceChildren();
  await act(async () => {
    await i18n.changeLanguage("en");
  });
});

it("presents Tailscale access then devices below the Runner inventory and nothing in Connections", async () => {
  const { calls } = fixture();
  expect(calls.discover).not.toHaveBeenCalled();
  category(RUNNER);
  const inventory = await screen.findByRole("heading", {
    name: "Fixture runner",
  });
  const access = await screen.findByRole("heading", {
    name: "Tailscale access",
  });
  const devices = await screen.findByRole("heading", {
    name: "Tailscale devices",
  });
  const before = (a: Node, b: Node) =>
    Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
  expect(before(inventory, access)).toBe(true);
  expect(before(access, devices)).toBe(true);
  expect(
    screen.getByRole("button", { name: "Enable access" }),
  ).toBeTruthy();
  expect(screen.getByRole("button", { name: "Refresh" })).toBeTruthy();
  expect(
    screen.getAllByRole("button", { name: "Set up a Worker over SSH" }).length,
  ).toBeGreaterThan(0);
  await screen.findByText("Tailscale connected");
  category("Connections");
  await screen.findByRole("heading", { name: "Current connection" });
  expect(screen.queryByRole("heading", { name: "Tailscale access" })).toBeNull();
  expect(
    screen.queryByRole("heading", { name: "Tailscale devices" }),
  ).toBeNull();
  const discoveries = calls.discover.mock.calls.length;
  category(RUNNER);
  await screen.findByRole("heading", { name: "Tailscale access" });
  await screen.findByRole("heading", { name: "Tailscale devices" });
  // Navigation never starts a mutation and discovery follows the presentation.
  expect(mutations(calls)).toEqual([0, 0, 0, 0, 0]);
  expect(calls.discover.mock.calls.length).toBeGreaterThan(discoveries);
  category(AWAY);
  const left = calls.discover.mock.calls.length;
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20));
  });
  expect(calls.discover.mock.calls.length).toBe(left);
  expect(mutations(calls)).toEqual([0, 0, 0, 0, 0]);
});

it("renders Korean headings in Runner Devices", async () => {
  fixture();
  await act(async () => {
    await i18n.changeLanguage("ko");
  });
  fireEvent.click(
    document.querySelector<HTMLElement>(
      '[data-settings-category="execution-workers"]',
    )!,
  );
  await screen.findByRole("heading", { name: "Tailscale 접근" });
  await screen.findByRole("heading", { name: "Tailscale 기기" });
});

it("keeps unsupported guidance in Runner Devices and sends no Tailscale requests", async () => {
  const { calls } = fixture({ scenario: Scenario.Unsupported });
  category(RUNNER);
  await screen.findByText(/does not support Tailscale connections/);
  expect(screen.queryByRole("heading", { name: "Tailscale access" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Enable access" })).toBeNull();
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20));
  });
  expect(calls.discover).not.toHaveBeenCalled();
  expect(calls.getAccess).not.toHaveBeenCalled();
  expect(calls.listApprovals).not.toHaveBeenCalled();
  expect(mutations(calls)).toEqual([0, 0, 0, 0, 0]);
});

it("shows no Tailscale controls and sends no Tailscale requests in a saved-server window", async () => {
  const { calls } = fixture({ local: false });
  category(RUNNER);
  await screen.findByRole("heading", { name: "Fixture runner" });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20));
  });
  expect(screen.queryByRole("heading", { name: "Tailscale access" })).toBeNull();
  expect(
    screen.queryByRole("heading", { name: "Tailscale devices" }),
  ).toBeNull();
  expect(screen.queryByRole("button", { name: "Enable access" })).toBeNull();
  for (const call of [
    calls.getAccess,
    calls.discover,
    calls.listApprovals,
    calls.listConnections,
  ])
    expect(call).not.toHaveBeenCalled();
});

it("retains a pending confirmation and an uncertain access request across category changes", async () => {
  const { calls } = fixture({ loseAccess: true });
  category(RUNNER);
  fireEvent.click(await screen.findByRole("button", { name: "Enable access" }));
  const confirm = await screen.findByRole("dialog", { name: "Enable access" });
  category(AWAY);
  category(RUNNER);
  // The owner-level confirmation survives; navigation sent nothing.
  expect(screen.getByRole("dialog", { name: "Enable access" })).toBe(confirm);
  expect(calls.setAccess).not.toHaveBeenCalled();
  fireEvent.click(within(confirm).getByRole("button", { name: "Enable access" }));
  await waitFor(() => expect(calls.setAccess).toHaveBeenCalledTimes(1));
  const original = calls.setAccess.mock.calls[0][0].requestId;
  category(AWAY);
  category(RUNNER);
  const retry = await screen.findByRole("button", {
    name: "Inspect original request",
  });
  expect(calls.setAccess).toHaveBeenCalledTimes(1);
  fireEvent.click(retry);
  await waitFor(() => expect(calls.setAccess).toHaveBeenCalledTimes(2));
  expect(calls.setAccess.mock.calls[1][0].requestId).toBe(original);
});

it("keeps an incoming approval available outside Runner Devices and retries its original decision", async () => {
  const id = newRequestId();
  const { calls } = fixture({ incoming: id, loseDecision: true });
  // No Runner Devices visit is needed: the owner-level prompt is global.
  const prompt = await screen.findByRole("dialog", {
    name: "Allow connection?",
  });
  expect(calls.decide).not.toHaveBeenCalled();
  fireEvent.click(within(prompt).getByRole("button", { name: "Allow" }));
  await waitFor(() => expect(calls.decide).toHaveBeenCalledTimes(1));
  category(RUNNER);
  fireEvent.click(
    within(screen.getByRole("dialog", { name: "Allow connection?" })).getByRole(
      "button",
      { name: "Inspect original request" },
    ),
  );
  await waitFor(() => expect(calls.decide).toHaveBeenCalledTimes(2));
  expect(calls.decide.mock.calls.map(([request]) => request.requestId)).toEqual(
    [id, id],
  );
});

it("retains an uncertain resume across leaving and returning and retries the original request", async () => {
  const id = newRequestId();
  const { calls } = fixture({ operation: id, loseResume: true });
  category(RUNNER);
  fireEvent.click(await screen.findByRole("button", { name: "Inspect original request" }));
  await waitFor(() => expect(calls.resume).toHaveBeenCalledTimes(1));
  category(AWAY);
  category(RUNNER);
  const row = await screen.findByRole("button", {
    name: "Inspect original request",
  });
  expect((row as HTMLButtonElement).disabled).toBe(true);
  const retry = await screen.findByRole("button", {
    name: "Retry the original resume request",
  });
  fireEvent.click(retry);
  await waitFor(() => expect(calls.resume).toHaveBeenCalledTimes(2));
  expect(calls.resume.mock.calls.map(([request]) => request.requestId)).toEqual(
    [id, id],
  );
  expect(calls.cancel).not.toHaveBeenCalled();
});

it("refreshes saved profiles through onPaired after a client connection completes", async () => {
  const { calls, paired } = fixture({ completeClient: true });
  category(RUNNER);
  fireEvent.click(await screen.findByRole("button", { name: "Check" }));
  fireEvent.click(await screen.findByRole("button", { name: "Connect" }));
  const task = await screen.findByRole("dialog", { name: "Connect" });
  expect(paired).not.toHaveBeenCalled();
  fireEvent.click(within(task).getByRole("button", { name: "Request approval" }));
  await waitFor(() => expect(calls.begin).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(paired).toHaveBeenCalledTimes(1));
});
