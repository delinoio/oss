// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { StrictMode, type ReactNode } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  InstallationService,
  ProviderService,
  ResourceSchema,
  ResourceService,
  SystemCapability,
  SystemService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import {
  dispatchAnimation,
  reducedMotion,
  setAnimation,
  unoptedSettingsCategory,
} from "./entrance-test-support";
import {
  LocalWorkerAction,
  LocalWorkerState,
  type LocalWorkerStatus,
} from "./local-worker-controls";
import { MutationIntents } from "./mutation";
import { ProductIdentityProvider } from "./product-identity-labels";
import { Settings, SettingsEntryDestination } from "./settings";

// Issue 3213: Runner Devices (SettingsCategory.ExecutionWorkers) category and
// owned task entrance. Every device, token and status below is synthetic.
const CATEGORY = "Runner Devices";

const runner = create(ResourceSchema, {
  id: newRequestId(),
  kind: EntityKind.MACHINE,
  schemaVersion: 1,
  revision: 7n,
  documentJson: encode({
    name: "Fixture runner",
    health: "Observed health",
    harness: "Observed harness",
  }),
});

function running(): LocalWorkerStatus {
  return {
    state: LocalWorkerState.Running,
    machine_id: newRequestId(),
    generation: newRequestId(),
    controller_active: true,
  };
}

function fixture() {
  let release: (() => void) | undefined;
  let hold = false;
  const list = vi.fn((kind: EntityKind) => ({
    resources: kind === EntityKind.MACHINE ? [runner] : ([] as Resource[]),
    nextPageToken: "",
  }));
  const get = vi.fn(async (id: string) => {
    if (hold)
      await new Promise<void>((resolve) => {
        release = resolve;
      });
    return { resource: id === runner.id ? runner : undefined };
  });
  const status = vi.fn(() => ({
    capabilities: [SystemCapability.SSH_WORKER_SETUP_V1],
  }));
  const inspectHost = vi.fn(() => ({}));
  const startSetup = vi.fn(() => ({}));
  const control = vi.fn(
    async (_action: LocalWorkerAction): Promise<LocalWorkerStatus> => running(),
  );
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {
      listResources: (request) =>
        list(request.filter?.kind ?? EntityKind.UNSPECIFIED),
      getResource: (request) => get(request.id),
    });
    router.service(ProviderService, {
      listProviderInventory: () => ({ entries: [] }),
      listProviderPresets: () => ({ presetsJson: encode([]) }),
    });
    router.service(SystemService, { getStatus: status });
    router.service(InstallationService, {
      inspectSSHHost: inspectHost,
      startSSHSetup: startSetup,
    });
  });
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false, gcTime: 0 },
    },
  });
  const view = (children: ReactNode) => (
    <StrictMode>
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <ProductIdentityProvider>
            <MutationIntents>{children}</MutationIntents>
          </ProductIdentityProvider>
        </QueryClientProvider>
      </TransportProvider>
    </StrictMode>
  );
  const nativeActions = () =>
    control.mock.calls
      .map(([action]) => action)
      .filter((action) => action !== LocalWorkerAction.Status);
  return {
    list,
    get,
    status,
    inspectHost,
    startSetup,
    control,
    client,
    view,
    nativeActions,
    holdRead: () => {
      hold = true;
    },
    releaseRead: async () => {
      await act(async () => release?.());
    },
  };
}

const column = () =>
  document.querySelector<HTMLElement>(".settings-content-column > div")!;
// Runner Devices and Tailscale (issue 3689 owns the persistent Tailscale portal
// hosts) move only the static category heading (EntranceTarget.Heading).
const heading = () =>
  document.querySelector<HTMLElement>(
    ".settings-content-column > div > .settings-category-heading",
  )!;
const dialogs = () =>
  [
    ...document.querySelectorAll<HTMLDialogElement>(
      "dialog.settings-task-dialog",
    ),
  ].filter((dialog) => dialog.open);
const topDialog = () => dialogs().at(-1)!;
const taskHeader = (dialog = topDialog()) =>
  dialog.querySelector<HTMLElement>(".settings-task-header")!;
const category = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const cancel = (dialog = topDialog()) =>
  fireEvent(dialog, new Event("cancel", { cancelable: true }));
const mount = (value: ReturnType<typeof fixture>, destination?: boolean) =>
  render(
    value.view(
      <Settings
        controlLocalWorker={value.control}
        runnerTailscale={() => {}}
        entryDestination={
          destination ? SettingsEntryDestination.RunnerDevices : undefined
        }
      />,
    ),
  );

beforeEach(() => {
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("plays the Runner Devices category entrance once per explicit opening without replay", async () => {
  const value = fixture();
  mount(value);
  category(unoptedSettingsCategory);
  expect(heading().getAttribute("data-entrance")).toBeNull();
  category(CATEGORY);
  const node = heading();
  expect(node.getAttribute("data-entrance")).toBe("y4");
  expect(node.querySelectorAll("[data-entrance]")).toHaveLength(0);
  // The content column never moves, and Runner Devices no longer hosts the
  // Tailscale portal hosts (issue 3689).
  expect(column().hasAttribute("data-entrance")).toBe(false);
  expect(document.querySelector(".runner-tailscale")).toBeNull();
  dispatchAnimation(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  await screen.findByRole("heading", { name: "Fixture runner" });
  // Inventory refetch, Local Worker status reads and re-selecting the current
  // category never replay it.
  await act(async () => {
    await value.client.invalidateQueries();
  });
  fireEvent.click(screen.getByRole("button", { name: "Refresh local Worker" }));
  category(CATEGORY);
  expect(heading()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  expect(value.nativeActions()).toEqual([]);
  expect(value.inspectHost).not.toHaveBeenCalled();
  expect(value.startSetup).not.toHaveBeenCalled();
  // Leaving and returning is a new explicit activation.
  category(unoptedSettingsCategory);
  expect(heading().getAttribute("data-entrance")).toBeNull();
  category(CATEGORY);
  expect(heading().getAttribute("data-entrance")).toBe("y4");
});

it("plays the Tailscale category entrance on the heading only and never over the portal hosts", async () => {
  const value = fixture();
  mount(value);
  category(unoptedSettingsCategory);
  expect(heading().getAttribute("data-entrance")).toBeNull();
  category("Tailscale");
  const node = heading();
  expect(node.getAttribute("data-entrance")).toBe("y4");
  expect(column().hasAttribute("data-entrance")).toBe(false);
  const hosts = document.querySelector(".runner-tailscale");
  expect(hosts?.children).toHaveLength(3);
  expect(hosts?.closest("[data-entrance]")).toBeNull();
  dispatchAnimation(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  // Re-selecting the current category never replays it or reads anything.
  category("Tailscale");
  expect(heading()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  expect(value.nativeActions()).toEqual([]);
});

it("plays the entrance for a targeted Runner Devices entry", async () => {
  const value = fixture();
  mount(value, true);
  expect(column().querySelector("h1")?.textContent).toBe(CATEGORY);
  expect(heading().getAttribute("data-entrance")).toBe("y4");
  dispatchAnimation(heading());
  expect(heading().getAttribute("data-entrance")).toBeNull();
  await screen.findByRole("heading", { name: "Fixture runner" });
  expect(heading().getAttribute("data-entrance")).toBeNull();
});

it("leaves no stale Runner Devices motion when the category changes before completion", () => {
  const value = fixture();
  mount(value);
  category(CATEGORY);
  const old = heading();
  expect(old.getAttribute("data-entrance")).toBe("y4");
  category(unoptedSettingsCategory);
  expect(document.contains(old)).toBe(false);
  dispatchAnimation(old);
  expect(heading().getAttribute("data-entrance")).toBeNull();
  category(CATEGORY);
  const fresh = heading();
  expect(fresh).not.toBe(old);
  expect(fresh.getAttribute("data-entrance")).toBe("y4");
  // The stale completion of the first visit cannot settle the new one.
  dispatchAnimation(old);
  expect(fresh.getAttribute("data-entrance")).toBe("y4");
});

it("settles Runner Devices motion for Animation Off then OS reduced motion without replay", async () => {
  document.documentElement.dataset.animation = "true";
  const setReduced = reducedMotion(false);
  const value = fixture();
  mount(value);
  category(CATEGORY);
  const node = heading();
  expect(node.getAttribute("data-entrance")).toBe("y4");
  await setAnimation("false");
  expect(node.getAttribute("data-entrance")).toBeNull();
  await setAnimation("true");
  expect(node.getAttribute("data-entrance")).toBeNull();
  category(unoptedSettingsCategory);
  category(CATEGORY);
  expect(heading().getAttribute("data-entrance")).toBe("y4");
  setReduced(true);
  expect(heading().getAttribute("data-entrance")).toBeNull();
  setReduced(false);
  expect(heading().getAttribute("data-entrance")).toBeNull();
  await setAnimation("false");
  category(unoptedSettingsCategory);
  category(CATEGORY);
  expect(heading().getAttribute("data-entrance")).toBeNull();
});

// Opens the Runner settings task with a held read, dismisses it, then opens and
// dismisses the SSH setup task. The Local Worker controller stays mounted.
async function taskScenario(animation: boolean) {
  document.documentElement.dataset.animation = String(animation);
  const value = fixture();
  value.holdRead();
  mount(value, true);
  const worker = await screen.findByRole("region", {
    name: "Worker on this computer",
  });
  const inspect = await screen.findByRole("button", {
    name: "Inspect installed harnesses",
  });
  if (animation) dispatchAnimation(heading());
  inspect.focus();
  fireEvent.click(inspect);
  const machineHost = topDialog();
  const observed = {
    machineHeader: taskHeader(machineHost).getAttribute("data-entrance"),
    machineHost: machineHost.hasAttribute("data-entrance"),
    machineAttributed: machineHost.querySelectorAll("[data-entrance]").length,
    sshHeader: null as string | null,
    sshHost: false,
    sshAttributed: -1,
    workerRetained: false,
    machineFocus: false,
    sshFocus: false,
  };
  await waitFor(() => expect(value.get).toHaveBeenCalled());
  if (animation) dispatchAnimation(taskHeader(machineHost));
  // The controller region and the tooltip hosts are never inside a transformed
  // ancestor.
  expect(worker.closest("[data-entrance]")).toBeNull();
  cancel(machineHost);
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(inspect));
  observed.machineFocus = true;
  await value.releaseRead();
  const setup = screen.getByRole("button", {
    name: "Set up a Worker over SSH",
  });
  setup.focus();
  fireEvent.click(setup);
  const sshHost = topDialog();
  observed.sshHeader = taskHeader(sshHost).getAttribute("data-entrance");
  observed.sshHost = sshHost.hasAttribute("data-entrance");
  observed.sshAttributed = sshHost.querySelectorAll("[data-entrance]").length;
  if (animation) dispatchAnimation(taskHeader(sshHost));
  cancel(sshHost);
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(setup));
  observed.sshFocus = true;
  observed.workerRetained =
    screen.getByRole("region", { name: "Worker on this computer" }) === worker;
  const counts = {
    get: value.get.mock.calls.length,
    nativeActions: value.nativeActions(),
    inspectHost: value.inspectHost.mock.calls.length,
    startSetup: value.startSetup.mock.calls.length,
  };
  cleanup();
  return { observed, counts };
}
it("animates the Runner settings and SSH setup tasks once each without extra operations", async () => {
  const baseline = await taskScenario(false);
  const animated = await taskScenario(true);
  expect(baseline.observed.machineHeader).toBeNull();
  expect(baseline.observed.sshHeader).toBeNull();
  // Only the static task headers move; the native hosts and bodies never do.
  expect(animated.observed.machineHeader).toBe("y4");
  expect(animated.observed.machineHost).toBe(false);
  expect(animated.observed.machineAttributed).toBe(1);
  expect(animated.observed.sshHeader).toBe("y4");
  expect(animated.observed.sshHost).toBe(false);
  expect(animated.observed.sshAttributed).toBe(1);
  // Dismissal is immediate, focus returns to each original opener and the
  // Local Worker controller region keeps its identity.
  expect(animated.observed.machineFocus).toBe(true);
  expect(animated.observed.sshFocus).toBe(true);
  expect(animated.observed.workerRetained).toBe(true);
  // The motion adds no read, Start, Stop, SSH inspection or setup request.
  expect(animated.counts).toEqual(baseline.counts);
  expect(animated.counts.get).toBeGreaterThan(0);
  expect(animated.counts).toMatchObject({
    nativeActions: [],
    inspectHost: 0,
    startSetup: 0,
  });
});

it("does not replay the task entrance for inventory refetch or Local Worker status reads", async () => {
  const value = fixture();
  mount(value, true);
  dispatchAnimation(heading());
  const inspect = await screen.findByRole("button", {
    name: "Inspect installed harnesses",
  });
  fireEvent.click(inspect);
  const host = topDialog();
  const header = taskHeader(host);
  expect(header.getAttribute("data-entrance")).toBe("y4");
  dispatchAnimation(header);
  await act(async () => {
    await value.client.invalidateQueries();
  });
  expect(topDialog()).toBe(host);
  expect(header.getAttribute("data-entrance")).toBeNull();
  expect(value.nativeActions()).toEqual([]);
  cancel(host);
  await waitFor(() => expect(document.activeElement).toBe(inspect));
});
