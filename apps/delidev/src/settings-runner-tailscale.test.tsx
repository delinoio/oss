// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { StrictMode, useState } from "react";
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
import { Settings, type SettingsNavigationEntry } from "./settings";
import { SettingsCategory } from "./settings-category";
import { SettingsSearchTarget } from "./settings-search";
import { TailscaleConnections } from "./tailscale-connections";

// Issue 3689: the desktop-owned Tailscale controller presents This computer,
// Devices on your tailnet and Connection requests in the dedicated Tailscale
// category through typed slots. Every identity is synthetic.
enum Scenario {
  Supported = "supported",
  Unsupported = "unsupported",
}
const RUNNER = "Runner Devices";
const TAILSCALE = "Tailscale";
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
const second = {
  ...peer,
  id: "peer-2",
  name: "Second peer",
  host: "second.example",
};
const offline = {
  ...peer,
  id: "peer-off",
  name: "Offline peer",
  host: "off.example",
  online: false,
};
const sharedTo = {
  ...peer,
  id: "peer-shared",
  name: "Shared peer",
  host: "shared.example",
  shared_to: true,
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
  initialAccess?: { enabled: boolean; state: string };
  readFails?: boolean;
  peerAccepts?: boolean;
  operations?: { id: string; state: string }[];
  entry?: SettingsNavigationEntry;
  peers?: (typeof peer)[];
  // Hold every check until the test releases it by peer ID.
  holdChecks?: boolean;
  // Peers whose check fails with an ordinary unavailable error.
  failChecks?: string[];
  strict?: boolean;
}
function fixture(options: Options = {}) {
  const {
    scenario = Scenario.Supported,
    local = true,
    incoming,
    operation,
  } = options;
  let access = options.initialAccess?.enabled ?? false;
  let accessState = options.initialAccess?.state;
  const calls = {
    getAccess: vi.fn(),
    discover: vi.fn(),
    check: vi.fn<(request: { peerId: string }) => void>(),
    listConnections: vi.fn(),
    listApprovals: vi.fn(),
    setAccess: vi.fn<(request: { requestId: string }) => void>(),
    begin: vi.fn(),
    decide: vi.fn<(request: { requestId: string }) => void>(),
    resume: vi.fn<(request: { requestId: string }) => void>(),
    cancel: vi.fn(),
  };
  const holds = new Map<string, () => void>();
  const checkState = { active: 0, maxActive: 0 };
  const release = (peerId: string) => {
    holds.get(peerId)?.();
    holds.delete(peerId);
  };
  const lose = (flag: boolean | undefined, count: number) => {
    if (flag && count === 1) throw new ConnectError("Lost", Code.Unavailable);
  };
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {
      listResources: (request) => ({
        resources: request.filter?.kind === EntityKind.MACHINE ? [runner] : [],
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
        if (options.readFails)
          throw new ConnectError("Access unavailable", Code.Unavailable);
        return {
          documentJson: encode({
            enabled: access,
            state: accessState ?? (access ? "ready" : "off"),
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
            peers: options.peers ?? [peer],
            observed_at: "2026-01-01T00:00:00Z",
          }),
        };
      },
      checkTailscale: async (request) => {
        calls.check(request);
        checkState.active += 1;
        checkState.maxActive = Math.max(
          checkState.maxActive,
          checkState.active,
        );
        try {
          if (options.holdChecks)
            await new Promise<void>((resolve) =>
              holds.set(request.peerId, resolve),
            );
        } finally {
          checkState.active -= 1;
        }
        if (options.failChecks?.includes(request.peerId))
          throw new ConnectError("Peer unavailable", Code.Unavailable);
        return {
          documentJson: encode({
            server_id: "s",
            origin: "https://peer.example",
            approval: options.peerAccepts ?? true,
          }),
        };
      },
      listTailscaleConnections: () => {
        calls.listConnections();
        return {
          documentJson: encode(
            options.operations
              ? options.operations.map(({ id, state }) => ({
                  ...pending(id),
                  state,
                  code: state === "pending" ? "654321" : "",
                }))
              : operation
                ? [pending(operation)]
                : [],
          ),
        };
      },
      setTailscaleAccess: (request) => {
        calls.setAccess(request);
        lose(options.loseAccess, calls.setAccess.mock.calls.length);
        access = request.enabled;
        accessState = undefined;
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
        return {
          documentJson: encode({
            ...pending(request.requestId),
            state: "approved",
          }),
        };
      },
      resumeTailscaleConnection: (request) => {
        calls.resume(request);
        lose(options.loseResume, calls.resume.mock.calls.length);
        return {
          documentJson: encode({
            ...pending(request.requestId),
            state: "denied",
          }),
        };
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
          entryDestination={options.entry}
          runnerTailscale={local ? setSlots : undefined}
          connectionSettings={
            local ? <ConnectionsPage local onSlots={() => {}} /> : undefined
          }
        />
        {local ? (
          <TailscaleConnections
            current={slots?.access}
            target={slots?.devices}
            requests={slots?.requests}
            onPaired={paired}
          />
        ) : null}
      </>
    );
  }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const Wrapper = options.strict
    ? StrictMode
    : ({ children }: { children: React.ReactNode }) => <>{children}</>;
  render(
    <Wrapper>
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <ProductIdentityProvider>
            <MutationIntents>
              <Owner />
            </MutationIntents>
          </ProductIdentityProvider>
        </QueryClientProvider>
      </TransportProvider>
    </Wrapper>,
  );
  return { calls, paired, release, checkState };
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

const beforeNode = (a: Node, b: Node) =>
  Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
const idle = () =>
  act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20));
  });
const quiet = [0, 0, 0, 0, 0];

it("lists Tailscale after Paired devices in Device management and presents the three sections with a title and purpose line", async () => {
  const { calls } = fixture();
  const names = [
    ...document.querySelectorAll<HTMLElement>("[data-settings-category]"),
  ].map((node) => node.dataset.settingsCategory);
  expect(
    names.slice(
      names.indexOf("execution-workers"),
      names.indexOf("execution-workers") + 3,
    ),
  ).toEqual(["execution-workers", "paired-devices", "tailscale"]);
  // Opening Settings never reads or writes Tailscale state.
  expect(calls.discover).not.toHaveBeenCalled();
  category(TAILSCALE);
  expect(
    await screen.findByRole("heading", { level: 1, name: "Tailscale" }),
  ).toBeTruthy();
  expect(
    screen.getByText(
      "Check this computer's Tailscale access, find devices on your tailnet and review connection requests.",
    ),
  ).toBeTruthy();
  const computer = await screen.findByRole("heading", {
    name: "This computer",
  });
  const devices = await screen.findByRole("heading", {
    name: "Devices on your tailnet",
  });
  const requests = await screen.findByRole("heading", {
    name: "Connection requests",
  });
  expect(beforeNode(computer, devices)).toBe(true);
  expect(beforeNode(devices, requests)).toBe(true);
  expect(screen.getByRole("button", { name: "Enable access" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Refresh" })).toBeTruthy();
  await screen.findByText("Tailscale connected");
  expect(await screen.findByText("No connection requests.")).toBeTruthy();
  expect(mutations(calls)).toEqual(quiet);
});

it("renders no Tailscale controls in Runner Devices or Connections", async () => {
  const { calls } = fixture();
  category(RUNNER);
  await screen.findByRole("heading", { name: "Fixture runner" });
  await idle();
  for (const name of [
    "This computer",
    "Devices on your tailnet",
    "Connection requests",
    "Tailscale access",
    "Tailscale devices",
  ])
    expect(screen.queryByRole("heading", { name })).toBeNull();
  expect(screen.queryByRole("button", { name: "Enable access" })).toBeNull();
  expect(document.querySelector(".runner-tailscale")).toBeNull();
  category("Connections");
  await screen.findByRole("heading", { name: "Current connection" });
  expect(screen.queryByRole("heading", { name: "This computer" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Enable access" })).toBeNull();
  expect(calls.discover).not.toHaveBeenCalled();
  expect(calls.check).not.toHaveBeenCalled();
  expect(mutations(calls)).toEqual(quiet);
});

it("starts discovery only while the Tailscale category is mounted and never mutates on navigation", async () => {
  const { calls } = fixture();
  category(TAILSCALE);
  await screen.findByRole("heading", { name: "Devices on your tailnet" });
  await waitFor(() => expect(calls.discover).toHaveBeenCalled());
  category(AWAY);
  const left = calls.discover.mock.calls.length;
  await idle();
  expect(calls.discover.mock.calls.length).toBe(left);
  category(TAILSCALE);
  await screen.findByRole("heading", { name: "Devices on your tailnet" });
  expect(mutations(calls)).toEqual(quiet);
});

it("renders Korean headings in the Tailscale category and keeps the locale switch free of mutations", async () => {
  const { calls } = fixture();
  category(TAILSCALE);
  await screen.findByRole("heading", { name: "This computer" });
  await act(async () => {
    await i18n.changeLanguage("ko");
  });
  await screen.findByRole("heading", { name: "이 컴퓨터" });
  await screen.findByRole("heading", { name: "테일넷의 기기" });
  await screen.findByRole("heading", { name: "연결 요청" });
  expect(mutations(calls)).toEqual(quiet);
});

it("shows unsupported guidance without discovery or mutation requests", async () => {
  const { calls } = fixture({ scenario: Scenario.Unsupported });
  category(TAILSCALE);
  await screen.findByText(/does not support Tailscale connections/);
  expect(screen.queryByRole("button", { name: "Enable access" })).toBeNull();
  expect(
    screen.queryByRole("heading", { name: "Devices on your tailnet" }),
  ).toBeNull();
  await idle();
  expect(calls.discover).not.toHaveBeenCalled();
  expect(calls.getAccess).not.toHaveBeenCalled();
  expect(calls.listApprovals).not.toHaveBeenCalled();
  expect(mutations(calls)).toEqual(quiet);
});

it("lists no Tailscale category and sends no Tailscale requests in a saved-server window", async () => {
  const { calls } = fixture({ local: false });
  expect(screen.queryByRole("button", { name: TAILSCALE })).toBeNull();
  expect(
    document.querySelector('[data-settings-category="tailscale"]'),
  ).toBeNull();
  category(RUNNER);
  await screen.findByRole("heading", { name: "Fixture runner" });
  await idle();
  expect(screen.queryByRole("heading", { name: "This computer" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Enable access" })).toBeNull();
  for (const call of [
    calls.getAccess,
    calls.discover,
    calls.listApprovals,
    calls.listConnections,
  ])
    expect(call).not.toHaveBeenCalled();
});

it("opens Runner Devices instead of an empty page when a saved-server window receives a Tailscale destination", async () => {
  const { calls } = fixture({
    local: false,
    entry: {
      category: SettingsCategory.Tailscale,
      target: SettingsSearchTarget.TailscaleDevices,
      generation: "stale-tailscale",
    },
  });
  await screen.findByRole("heading", { level: 1, name: RUNNER });
  expect(
    screen.queryByRole("heading", { level: 1, name: TAILSCALE }),
  ).toBeNull();
  await idle();
  for (const call of [
    calls.getAccess,
    calls.discover,
    calls.listApprovals,
    calls.listConnections,
  ])
    expect(call).not.toHaveBeenCalled();
});

it("separates enabled intent from Ready: setup-required shows Action needed with guidance and no readiness", async () => {
  const { calls } = fixture({
    initialAccess: { enabled: true, state: "setup-required" },
  });
  category(TAILSCALE);
  const status = await screen.findByText("Action needed");
  expect(status.getAttribute("role")).toBe("status");
  expect(screen.getByText("Enabled")).toBeTruthy();
  expect(
    screen.getByText(
      "Check Tailscale sign-in and HTTPS setup, then refresh devices.",
    ),
  ).toBeTruthy();
  expect(screen.queryByText("Ready")).toBeNull();
  expect(screen.getByRole("button", { name: "Disable access" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Enable access" })).toBeNull();
  // No uncertain request exists, so no original-request inspection is offered.
  expect(
    screen.queryByRole("button", { name: "Inspect original request" }),
  ).toBeNull();
  expect(mutations(calls)).toEqual(quiet);
});

it("treats cleanup-required with enabled intent as action needed and Worker registration stays unavailable", async () => {
  fixture({ initialAccess: { enabled: true, state: "cleanup-required" } });
  category(TAILSCALE);
  await screen.findByText("Action needed");
  expect(screen.queryByText("Ready")).toBeNull();
  await screen.findByText(/Accepting requests/);
  expect(screen.getByRole("button", { name: "Connect" })).toBeTruthy();
  expect(
    (
      screen.getByRole("button", {
        name: "Register Worker",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
});

it("presents Ready with Disable and Off with Enable, and a read failure without readiness", async () => {
  fixture({ initialAccess: { enabled: true, state: "ready" } });
  category(TAILSCALE);
  await screen.findByText("Ready");
  expect(screen.queryByText("Action needed")).toBeNull();
  expect(screen.getByRole("button", { name: "Disable access" })).toBeTruthy();
  cleanup();
  fixture();
  category(TAILSCALE);
  const off = await screen.findByText("Off");
  expect(off.getAttribute("role")).toBe("status");
  expect(screen.getByText("Disabled")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Enable access" })).toBeTruthy();
  cleanup();
  const { calls } = fixture({ readFails: true });
  category(TAILSCALE);
  await waitFor(() => expect(calls.getAccess).toHaveBeenCalled());
  await screen.findByText("Unavailable or blocked", { selector: "dd" });
  expect(screen.queryByText("Ready")).toBeNull();
  expect(screen.queryByText("Action needed")).toBeNull();
  expect(mutations(calls)).toEqual(quiet);
});

it("never enables a connection from online status alone and follows the automatic check result", async () => {
  fixture({ peerAccepts: false });
  category(TAILSCALE);
  const row = (await screen.findByText("Fixture peer")).closest("li")!;
  expect(within(row).getByText(/Online/)).toBeTruthy();
  expect(within(row).queryByRole("button", { name: "Connect" })).toBeNull();
  expect(
    within(row).queryByRole("button", { name: "Register Worker" }),
  ).toBeNull();
  await within(row).findByText(/Unavailable or blocked/);
  expect(within(row).queryByRole("button", { name: "Connect" })).toBeNull();
  expect(
    within(row).getAllByRole("button", { name: "Set up a Worker over SSH" })
      .length,
  ).toBeGreaterThan(0);
});

it("enables Connect and Register Worker only after a successful check when access is Ready", async () => {
  fixture({ initialAccess: { enabled: true, state: "ready" } });
  category(TAILSCALE);
  const row = (await screen.findByText("Fixture peer")).closest("li")!;
  await within(row).findByText(/Accepting requests/);
  expect(
    (within(row).getByRole("button", { name: "Connect" }) as HTMLButtonElement)
      .disabled,
  ).toBe(false);
  expect(
    (
      within(row).getByRole("button", {
        name: "Register Worker",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(false);
});

it("lists retained requests in every state and offers resume and cancel only where they apply", async () => {
  const ids = [
    "pending",
    "approved",
    "denied",
    "expired",
    "completed",
    "uncertain",
  ].map((state) => ({ id: newRequestId(), state }));
  const { calls } = fixture({ operations: ids });
  category(TAILSCALE);
  await screen.findByText("Approval pending");
  for (const label of [
    "Approved; completion requires inspection",
    "Denied",
    "Expired",
    "Completed",
    "Outcome unconfirmed",
  ])
    expect(screen.getByText(label)).toBeTruthy();
  // Resume: pending, approved, uncertain. Cancel: pending only.
  expect(
    screen.getAllByRole("button", { name: "Inspect original request" }),
  ).toHaveLength(3);
  expect(
    screen.getAllByRole("button", { name: "Cancel original request" }),
  ).toHaveLength(1);
  fireEvent.click(
    screen.getAllByRole("button", { name: "Cancel original request" })[0],
  );
  await waitFor(() => expect(calls.cancel).toHaveBeenCalledTimes(1));
  expect(calls.setAccess).not.toHaveBeenCalled();
});

it("retains a pending confirmation and an uncertain access request across category changes", async () => {
  const { calls } = fixture({ loseAccess: true });
  category(TAILSCALE);
  fireEvent.click(await screen.findByRole("button", { name: "Enable access" }));
  const confirm = await screen.findByRole("dialog", { name: "Enable access" });
  category(AWAY);
  category(TAILSCALE);
  // The owner-level confirmation survives; navigation sent nothing.
  expect(screen.getByRole("dialog", { name: "Enable access" })).toBe(confirm);
  expect(calls.setAccess).not.toHaveBeenCalled();
  fireEvent.click(
    within(confirm).getByRole("button", { name: "Enable access" }),
  );
  await waitFor(() => expect(calls.setAccess).toHaveBeenCalledTimes(1));
  const original = calls.setAccess.mock.calls[0][0].requestId;
  category(AWAY);
  category(TAILSCALE);
  const retry = await screen.findByRole("button", {
    name: "Inspect original request",
  });
  expect(calls.setAccess).toHaveBeenCalledTimes(1);
  fireEvent.click(retry);
  await waitFor(() => expect(calls.setAccess).toHaveBeenCalledTimes(2));
  expect(calls.setAccess.mock.calls[1][0].requestId).toBe(original);
});

it("keeps an incoming approval available outside the Tailscale category and retries its original decision", async () => {
  const id = newRequestId();
  const { calls } = fixture({ incoming: id, loseDecision: true });
  // No Tailscale visit is needed: the owner-level prompt is global.
  const prompt = await screen.findByRole("dialog", {
    name: "Allow connection?",
  });
  expect(calls.decide).not.toHaveBeenCalled();
  fireEvent.click(within(prompt).getByRole("button", { name: "Allow" }));
  await waitFor(() => expect(calls.decide).toHaveBeenCalledTimes(1));
  category(TAILSCALE);
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
  category(TAILSCALE);
  fireEvent.click(
    await screen.findByRole("button", { name: "Inspect original request" }),
  );
  await waitFor(() => expect(calls.resume).toHaveBeenCalledTimes(1));
  category(AWAY);
  category(TAILSCALE);
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
  category(TAILSCALE);
  fireEvent.click(await screen.findByRole("button", { name: "Connect" }));
  const task = await screen.findByRole("dialog", { name: "Connect" });
  expect(paired).not.toHaveBeenCalled();
  fireEvent.click(
    within(task).getByRole("button", { name: "Request approval" }),
  );
  await waitFor(() => expect(calls.begin).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(paired).toHaveBeenCalledTimes(1));
});

it("finds the Tailscale category and its sections through Settings search", async () => {
  fixture();
  const input = screen.getByRole("searchbox");
  fireEvent.change(input, { target: { value: "tailnet" } });
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Tailscale › Devices on your tailnet",
    }),
  );
  await screen.findByRole("heading", { name: "Devices on your tailnet" });
});

const rowOf = async (name: string) =>
  (await screen.findByText(name)).closest("li")!;
const checkedIds = (calls: ReturnType<typeof fixture>["calls"]) =>
  calls.check.mock.calls.map(([request]) => request.peerId);

it("checks eligible peers automatically one at a time with an announced checking state", async () => {
  const { calls, release, checkState } = fixture({
    peers: [peer, second],
    holdChecks: true,
  });
  category(TAILSCALE);
  const first = await rowOf("Fixture peer");
  const other = await rowOf("Second peer");
  await waitFor(() => expect(checkedIds(calls)).toEqual(["peer-1"]));
  const status = within(first).getByText(/DeliDev: Checking…/);
  expect(status.getAttribute("role")).toBe("status");
  expect(first.querySelector(".tailscale-progress")).toBeTruthy();
  expect(
    (within(first).getByRole("button", { name: "Check" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  expect(within(other).getByText(/Not checked/)).toBeTruthy();
  await idle();
  expect(checkedIds(calls)).toEqual(["peer-1"]);
  release("peer-1");
  await within(first).findByText(/Accepting requests/);
  await waitFor(() => expect(checkedIds(calls)).toEqual(["peer-1", "peer-2"]));
  expect(within(first).queryByText(/Checking…/)).toBeNull();
  expect(within(other).getByText(/Checking…/)).toBeTruthy();
  release("peer-2");
  await within(other).findByText(/Accepting requests/);
  expect(
    (within(first).getByRole("button", { name: "Check" }) as HTMLButtonElement)
      .disabled,
  ).toBe(false);
  expect(checkState.maxActive).toBe(1);
  expect(checkedIds(calls)).toEqual(["peer-1", "peer-2"]);
  expect(mutations(calls)).toEqual(quiet);
});

it("checks only online peers that are not shared to this tailnet user", async () => {
  const { calls } = fixture({ peers: [offline, sharedTo, peer, second] });
  category(TAILSCALE);
  const row = await rowOf("Second peer");
  await within(row).findByText(/Accepting requests/);
  await idle();
  expect(checkedIds(calls)).toEqual(["peer-1", "peer-2"]);
  expect(
    within(await rowOf("Offline peer")).getByText(/Not checked/),
  ).toBeTruthy();
  expect(
    within(await rowOf("Shared peer")).getByText(/Not checked/),
  ).toBeTruthy();
  expect(mutations(calls)).toEqual(quiet);
});

it("keeps Check as a single-peer manual retry after an automatic failure or unapproved result", async () => {
  const { calls } = fixture({
    peers: [peer, second],
    failChecks: ["peer-1"],
    initialAccess: { enabled: true, state: "ready" },
  });
  category(TAILSCALE);
  const failed = await rowOf("Fixture peer");
  await within(failed).findByText(/Unavailable or blocked/);
  await waitFor(() => expect(checkedIds(calls)).toEqual(["peer-1", "peer-2"]));
  const accepted = await rowOf("Second peer");
  await within(accepted).findByText(/Accepting requests/);
  expect(within(failed).getByRole("alert")).toBeTruthy();
  expect(within(accepted).queryByRole("alert")).toBeNull();
  expect(within(failed).queryByRole("button", { name: "Connect" })).toBeNull();
  expect(
    within(failed).queryByRole("button", { name: "Register Worker" }),
  ).toBeNull();
  expect(
    within(accepted).getByRole("button", { name: "Connect" }),
  ).toBeTruthy();
  fireEvent.click(within(failed).getByRole("button", { name: "Check" }));
  await waitFor(() =>
    expect(checkedIds(calls)).toEqual(["peer-1", "peer-2", "peer-1"]),
  );
  await idle();
  expect(checkedIds(calls)).toEqual(["peer-1", "peer-2", "peer-1"]);
  // A manual re-check of a settled accepted peer sends exactly one more read.
  fireEvent.click(within(accepted).getByRole("button", { name: "Check" }));
  await waitFor(() => expect(checkedIds(calls).at(-1)).toBe("peer-2"));
  expect(checkedIds(calls)).toHaveLength(4);
  expect(mutations(calls)).toEqual(quiet);
});

it("drops the queue when the category is left and starts a fresh visit on reopening", async () => {
  const { calls, release } = fixture({
    peers: [peer, second],
    holdChecks: true,
  });
  category(TAILSCALE);
  await waitFor(() => expect(checkedIds(calls)).toEqual(["peer-1"]));
  category(AWAY);
  await waitFor(() =>
    expect(
      screen.queryByRole("heading", { name: "Devices on your tailnet" }),
    ).toBeNull(),
  );
  release("peer-1");
  await idle();
  expect(checkedIds(calls)).toEqual(["peer-1"]);
  category(TAILSCALE);
  await waitFor(() => expect(checkedIds(calls)).toEqual(["peer-1", "peer-1"]));
  const first = await rowOf("Fixture peer");
  expect(within(first).getByText(/Checking…/)).toBeTruthy();
  release("peer-1");
  await waitFor(() =>
    expect(checkedIds(calls)).toEqual(["peer-1", "peer-1", "peer-2"]),
  );
  release("peer-2");
  await within(await rowOf("Second peer")).findByText(/Accepting requests/);
  await idle();
  expect(checkedIds(calls)).toHaveLength(3);
  expect(mutations(calls)).toEqual(quiet);
});

it("does not repeat or restart automatic checks on Strict Mode replay, focus, reconnect or Refresh", async () => {
  const options = { peers: [peer], strict: true };
  const { calls } = fixture(options);
  category(TAILSCALE);
  const first = await rowOf("Fixture peer");
  await within(first).findByText(/Accepting requests/);
  expect(checkedIds(calls)).toEqual(["peer-1"]);
  act(() => {
    document.dispatchEvent(new Event("visibilitychange"));
    window.dispatchEvent(new Event("focus"));
    window.dispatchEvent(new Event("online"));
  });
  await idle();
  const discovered = calls.discover.mock.calls.length;
  options.peers = [peer, second];
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  const added = await rowOf("Second peer");
  await waitFor(() =>
    expect(calls.discover.mock.calls.length).toBeGreaterThan(discovered),
  );
  await idle();
  // A peer first seen after Refresh is only checked manually in this visit.
  expect(checkedIds(calls)).toEqual(["peer-1"]);
  expect(within(added).getByText(/Not checked/)).toBeTruthy();
  fireEvent.click(within(added).getByRole("button", { name: "Check" }));
  await within(added).findByText(/Accepting requests/);
  expect(checkedIds(calls)).toEqual(["peer-1", "peer-2"]);
  category(AWAY);
  await idle();
  category(TAILSCALE);
  await waitFor(() =>
    expect(checkedIds(calls)).toEqual(["peer-1", "peer-2", "peer-1", "peer-2"]),
  );
  await idle();
  expect(checkedIds(calls)).toHaveLength(4);
  expect(mutations(calls)).toEqual(quiet);
});
