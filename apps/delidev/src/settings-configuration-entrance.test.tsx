// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { StrictMode, type ReactNode } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  ConfigurationService,
  EntityKind,
  ProviderInventoryCapability,
  ProviderService,
  ResourceSchema,
  ResourceService,
  SystemService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode, type Document } from "./documents";
import { MutationIntents } from "./mutation";
import { ProductIdentityProvider } from "./product-identity-labels";
import { Settings } from "./settings";
import { unoptedSettingsCategory } from "./entrance-test-support";

// Issues 3180, 3184, 3185 and 3210: API Providers, Instructions, Project
// defaults and Projects opt in to the shared 160ms Y4 category entrance, and
// every task they own plays one header entrance per opened window.

function resource(kind: EntityKind, data: Document, revision = 3n): Resource {
  return create(ResourceSchema, {
    id: newRequestId(),
    kind,
    revision,
    schemaVersion: 1,
    documentJson: encode(data),
  });
}
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
const instruction = resource(EntityKind.TEMPLATE, {
  name: "Release notes",
  contents: "Summarize every change.",
});
const project = resource(EntityKind.PROJECT, {
  name: "Fixture project",
  repositories: [],
  primary_repository: "",
  agents: { configured: false, ids: [] },
  accounts: { configured: false, ids: [] },
});
const repository = resource(EntityKind.REPOSITORY, { name: "Fixture repo" });
const policy = resource(EntityKind.SETTINGS, {
  default_routing: "priority",
  automatic_fetch: false,
  notifications: false,
  remediation: {
    ci_failure: true,
    review_feedback: false,
    merge_conflict: true,
    conflict_strategy: "rebase",
    session_strategy: "dedicated",
    attempt_limit: 9,
    agent_id: newRequestId(),
    machine_id: newRequestId(),
  },
});

function fixture() {
  const rows = [instruction, project, repository, policy];
  const provider = resource(EntityKind.PROVIDER, {
    name: "Fixture Provider",
    enabled: true,
    protocol: "openai-responses",
    authentication: "keyless",
    endpoint: "http://127.0.0.1:1234/v1",
  });
  const second = resource(EntityKind.PROVIDER, {
    name: "Second Provider",
    enabled: true,
    protocol: "openai-responses",
    authentication: "keyless",
    endpoint: "http://127.0.0.1:1235/v1",
  });
  const gates: ReturnType<typeof deferred>[] = [];
  let holdInventory = false;
  const inventory = vi.fn(async () => {
    if (holdInventory && inventory.mock.calls.length > 1) {
      const gate = deferred();
      gates.push(gate);
      await gate.promise;
    }
    return {
      entries: [provider, second].map((row) => ({
        provider: row,
        providerId: row.id,
        displayName: row === provider ? "Fixture Provider" : "Second Provider",
        enabled: true,
      })),
      capabilities: [
        ProviderInventoryCapability.PROVIDER_ACTIVATION,
        ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER,
        ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
        ProviderInventoryCapability.ACCOUNT_TYPE_FILTER,
      ],
    };
  });
  const save = vi.fn((request: { documentJson: Uint8Array }) => ({
    resource: create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.TEMPLATE,
      revision: 4n,
      schemaVersion: 1,
      documentJson: request.documentJson,
    }),
  }));
  const remove = vi.fn(() => ({}));
  const list = vi.fn((kind: EntityKind) => ({
    resources: rows.filter((row) => row.kind === kind),
  }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(ResourceService, {
      listResources: (request) =>
        list(request.filter?.kind ?? EntityKind.UNSPECIFIED),
      getResource: (request) => ({
        resource: [...rows, provider, second].find(
          (row) => row.id === request.id,
        ),
      }),
    });
    router.service(ConfigurationService, {
      saveConfiguration: save,
      deleteConfiguration: remove,
    });
    router.service(ProviderService, {
      listProviderInventory: inventory,
      listProviderPresets: () => ({ presetsJson: encode([]) }),
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
  return {
    inventory,
    save,
    remove,
    list,
    gates,
    view,
    hold: () => {
      holdInventory = true;
    },
  };
}

function dispatch(node: Element, type = "animationend") {
  const event = new Event(type, { bubbles: true });
  Object.defineProperty(event, "animationName", { value: "entrance-y4" });
  act(() => {
    node.dispatchEvent(event);
  });
}
const column = () =>
  document.querySelector<HTMLElement>(".settings-content-column > div")!;
const heading = () =>
  document.querySelector<HTMLElement>(".settings-category-heading");
const dialog = () =>
  document.querySelector<HTMLDialogElement>("dialog.settings-task-dialog")!;
const taskHeader = () =>
  document.querySelector<HTMLElement>(".settings-task-header")!;
const category = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));

enum Opted {
  Providers = "API Providers",
  Instructions = "Instructions",
  ProjectDefaults = "Project defaults",
  Projects = "Projects",
}
const optedCategories = Object.values(Opted);

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

it.each(optedCategories)(
  "plays the %s category entrance once per explicit opening",
  async (name) => {
    const value = fixture();
    render(value.view(<Settings />));
    // The default landing category (AI Subscription) opted in too, so leave it first.
    category(unoptedSettingsCategory);
    expect(column().getAttribute("data-entrance")).toBeNull();
    category(name);
    const node = column();
    expect(node.getAttribute("data-entrance")).toBe("y4");
    // Surface categories never give the static heading a second motion.
    expect(heading()?.getAttribute("data-entrance") ?? null).toBeNull();
    dispatch(node);
    expect(node.getAttribute("data-entrance")).toBeNull();
    // Reads, refetches and re-selecting the current category never replay.
    category(name);
    await act(async () => {});
    expect(column()).toBe(node);
    expect(column().getAttribute("data-entrance")).toBeNull();
    // Leaving and returning is a new explicit activation.
    category(unoptedSettingsCategory);
    expect(column().getAttribute("data-entrance")).toBeNull();
    category(name);
    expect(column().getAttribute("data-entrance")).toBe("y4");
  },
);

it.each(optedCategories)(
  "leaves no stale %s motion when the category changes before completion",
  (name) => {
    const value = fixture();
    render(value.view(<Settings />));
    category(name);
    const old = column();
    expect(old.getAttribute("data-entrance")).toBe("y4");
    category(unoptedSettingsCategory);
    expect(document.contains(old)).toBe(false);
    dispatch(old);
    expect(column().getAttribute("data-entrance")).toBeNull();
    category(name);
    const fresh = column();
    expect(fresh).not.toBe(old);
    expect(fresh.getAttribute("data-entrance")).toBe("y4");
    // The stale completion of the first visit cannot settle the new one.
    dispatch(old);
    expect(fresh.getAttribute("data-entrance")).toBe("y4");
    dispatch(fresh);
    expect(fresh.getAttribute("data-entrance")).toBeNull();
  },
);

it.each(optedCategories)(
  "settles %s motion for Animation Off then OS reduced motion without replay",
  async (name) => {
    document.documentElement.dataset.animation = "true";
    const listeners = new Set<() => void>();
    const media = {
      matches: false,
      addEventListener: (_: string, listener: () => void) =>
        listeners.add(listener),
      removeEventListener: (_: string, listener: () => void) =>
        listeners.delete(listener),
    };
    vi.stubGlobal("matchMedia", () => media);
    const value = fixture();
    render(value.view(<Settings />));
    category(name);
    const node = column();
    expect(node.getAttribute("data-entrance")).toBe("y4");
    await act(async () => {
      document.documentElement.dataset.animation = "false";
      await Promise.resolve();
    });
    expect(node.getAttribute("data-entrance")).toBeNull();
    await act(async () => {
      document.documentElement.dataset.animation = "true";
      await Promise.resolve();
    });
    expect(node.getAttribute("data-entrance")).toBeNull();
    category(name);
    expect(column().getAttribute("data-entrance")).toBeNull();
    // OS reduced motion on a fresh activation settles it the same way.
    category(unoptedSettingsCategory);
    category(name);
    expect(column().getAttribute("data-entrance")).toBe("y4");
    act(() => {
      media.matches = true;
      listeners.forEach((changed) => changed());
    });
    expect(column().getAttribute("data-entrance")).toBeNull();
    act(() => {
      media.matches = false;
      listeners.forEach((changed) => changed());
    });
    expect(column().getAttribute("data-entrance")).toBeNull();
  },
);

it.each(optedCategories)(
  "does not play the %s entrance when Animation is Off at activation",
  (name) => {
    document.documentElement.dataset.animation = "false";
    const value = fixture();
    render(value.view(<Settings />));
    category(name);
    expect(column().getAttribute("data-entrance")).toBeNull();
    expect(heading()?.getAttribute("data-entrance") ?? null).toBeNull();
  },
);

it("keeps provider rows, refresh count and focus return through a held identical refresh while the editor opens and closes", async () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(Opted.Providers);
  const rowNames = () =>
    screen
      .queryAllByRole("heading")
      .map((node) => node.textContent ?? "")
      .filter((text) => /Fixture Provider|Second Provider/.test(text));
  await screen.findByRole("heading", { name: "Second Provider" });
  const node = column();
  expect(node.getAttribute("data-entrance")).toBe("y4");
  const before = rowNames();
  expect(before).toHaveLength(2);
  dispatch(node);
  value.hold();
  const refresh = screen.getByRole("button", { name: "Refresh providers" });
  const calls = value.inventory.mock.calls.length;
  fireEvent.click(refresh);
  await waitFor(() => expect(value.gates).toHaveLength(1));
  expect(value.inventory.mock.calls.length).toBe(calls + 1);
  // While the identical refresh is held, the editor opens and closes.
  const create = screen.getByRole("button", { name: "Custom provider" });
  await waitFor(() =>
    expect((create as HTMLButtonElement).disabled).toBe(false),
  );
  create.focus();
  fireEvent.click(create);
  expect(dialog().hasAttribute("data-entrance")).toBe(false);
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
  expect(column().getAttribute("data-entrance")).toBeNull();
  const header = taskHeader();
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "Typed while entering" },
  });
  expect(taskHeader()).toBe(header);
  expect(header.getAttribute("data-entrance")).toBe("y4");
  dispatch(header);
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "Typed afterwards" },
  });
  expect(header.getAttribute("data-entrance")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: /^Close/ }));
  expect(document.querySelector("dialog.settings-task-dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(create));
  // The held refresh completes after the editor closed and never replays.
  const closedCalls = value.inventory.mock.calls.length;
  await act(async () => {
    value.gates.forEach((gate) => gate.resolve());
    await new Promise((resolve) => setTimeout(resolve, 300));
  });
  expect(column()).toBe(node);
  expect(column().getAttribute("data-entrance")).toBeNull();
  expect(rowNames()).toEqual(before);
  // Completing the refresh and the settling fallback add no request.
  expect(value.inventory.mock.calls.length).toBe(closedCalls);
  expect(value.save).not.toHaveBeenCalled();
  // Opening the editor again is a new owned-task window.
  fireEvent.click(create);
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
});

it("animates the instruction editor and deletion confirmation once per window without touching drafts", async () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(Opted.Instructions);
  await screen.findByText("Release notes");
  dispatch(column());
  const edit = screen.getByRole("button", { name: /^Edit Release notes/ });
  edit.focus();
  fireEvent.click(edit);
  expect(dialog().hasAttribute("data-entrance")).toBe(false);
  const header = taskHeader();
  expect(header.getAttribute("data-entrance")).toBe("y4");
  const contents = screen.getByRole("textbox", { name: "Instructions" });
  fireEvent.change(contents, { target: { value: "Draft one" } });
  fireEvent.change(contents, { target: { value: "Draft two" } });
  expect(taskHeader()).toBe(header);
  expect(header.getAttribute("data-entrance")).toBe("y4");
  dispatch(header);
  fireEvent.change(contents, { target: { value: "Draft three" } });
  expect(header.getAttribute("data-entrance")).toBeNull();
  expect((contents as HTMLTextAreaElement).value).toBe("Draft three");
  fireEvent.click(screen.getByRole("button", { name: /^Close/ }));
  expect(document.querySelector("dialog.settings-task-dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(edit));
  // The deletion confirmation is its own owned task window.
  const del = screen.getByRole("button", { name: /^Delete Release notes/ });
  fireEvent.click(del);
  expect(dialog().hasAttribute("data-entrance")).toBe(false);
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
  dispatch(taskHeader());
  expect(taskHeader().getAttribute("data-entrance")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: /^Close/ }));
  expect(document.querySelector("dialog.settings-task-dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(del));
  // Neither the motion nor its settling fallback reads, saves or deletes.
  const reads = value.list.mock.calls.length;
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 300));
  });
  expect(value.list.mock.calls.length).toBe(reads);
  expect(value.remove).not.toHaveBeenCalled();
  expect(value.save).not.toHaveBeenCalled();
});

it("plays the Project defaults entrance only for explicit opening, not for drafts or disclosures", async () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(Opted.ProjectDefaults);
  await screen.findByRole("form", { name: "Project defaults form" });
  const node = column();
  expect(node.getAttribute("data-entrance")).toBe("y4");
  dispatch(node);
  const form = screen.getByRole("form", { name: "Project defaults form" });
  const fetchBox = screen.getByLabelText(
    "Allow automatic fetch before Worktree preparation",
  ) as HTMLInputElement;
  fireEvent.click(fetchBox);
  const details = screen.getByText("Remediation details").closest("details")!;
  details.open = true;
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await act(async () => {});
  expect(column()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  expect(screen.getByRole("form", { name: "Project defaults form" })).toBe(
    form,
  );
  expect(fetchBox.checked).toBe(true);
  // The original lifecycle discards the draft on departure; a return plays again.
  category(unoptedSettingsCategory);
  category(Opted.ProjectDefaults);
  await screen.findByRole("form", { name: "Project defaults form" });
  expect(column().getAttribute("data-entrance")).toBe("y4");
  expect(
    (
      screen.getByLabelText(
        "Allow automatic fetch before Worktree preparation",
      ) as HTMLInputElement
    ).checked,
  ).toBe(false);
  expect(value.save).not.toHaveBeenCalled();
});

it("animates New project and edit windows once and keeps drafts, tabs and operation counts", async () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(Opted.Projects);
  await screen.findByText("Fixture project");
  dispatch(column());
  const edit = screen.getByRole("button", { name: /^Edit Fixture project/ });
  edit.focus();
  fireEvent.click(edit);
  expect(dialog().hasAttribute("data-entrance")).toBe(false);
  const header = taskHeader();
  expect(header.getAttribute("data-entrance")).toBe("y4");
  dispatch(header);
  const tabs = screen.getAllByRole("tab");
  expect(tabs.length).toBeGreaterThan(1);
  const nameField = () =>
    within(screen.getByRole("group", { name: "Name" })).getByRole(
      "textbox",
    ) as HTMLInputElement;
  fireEvent.change(nameField(), { target: { value: "Renamed draft" } });
  fireEvent.click(tabs[1]!);
  expect(taskHeader()).toBe(header);
  expect(header.getAttribute("data-entrance")).toBeNull();
  fireEvent.click(screen.getByRole("tab", { name: "General" }));
  expect(taskHeader()).toBe(header);
  expect(header.getAttribute("data-entrance")).toBeNull();
  expect(nameField().value).toBe("Renamed draft");
  fireEvent.click(screen.getByRole("button", { name: /^Close/ }));
  expect(document.querySelector("dialog.settings-task-dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(edit));
  // New project is a separate owned window.
  fireEvent.click(screen.getByRole("button", { name: "New Project" }));
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
  expect(dialog().hasAttribute("data-entrance")).toBe(false);
  expect(value.save).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 300));
  });
  // Settling the entrance neither creates nor deletes a project, and the
  // creation window keeps its single header entrance.
  expect(value.save).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
});

it("animates the project deletion confirmation once without deleting on completion", async () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(Opted.Projects);
  await screen.findByText("Fixture project");
  fireEvent.click(
    screen.getByRole("button", { name: /^Delete Fixture project/ }),
  );
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
  expect(dialog().hasAttribute("data-entrance")).toBe(false);
  dispatch(taskHeader());
  expect(taskHeader().getAttribute("data-entrance")).toBeNull();
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 300));
  });
  expect(value.remove).not.toHaveBeenCalled();
});
