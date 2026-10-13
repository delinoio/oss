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
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  ConfigurationService,
  EntityKind,
  ResourceSchema,
  ResourceService,
  SystemCapability,
  SystemService,
  WorkerService,
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
import { MutationIntents } from "./mutation";
import { ProductIdentityProvider } from "./product-identity-labels";
import { Settings, SettingsEntryDestination } from "./settings";

// Issue 3211: Repositories category and owned task entrance. Every value below
// is a synthetic fixture.
const CATEGORY = "Repositories";

const repository = create(ResourceSchema, {
  id: newRequestId(),
  kind: EntityKind.REPOSITORY,
  schemaVersion: 1,
  revision: 2n,
  documentJson: encode({
    name: "Fixture repo",
    remote_url: "https://github.com/fixture/repo.git",
    checkouts: [],
    base: {},
    starting: {},
    auto_fetch: true,
  }),
});

function fixture() {
  const rows: Resource[] = [repository];
  const list = vi.fn((kind: EntityKind) => ({
    resources: rows.filter((row) => row.kind === kind),
  }));
  const get = vi.fn((id: string) => ({
    resource: rows.find((row) => row.id === id),
  }));
  const save = vi.fn(() => ({ resource: repository }));
  const remove = vi.fn(() => ({}));
  const inspect = vi.fn(() => ({}));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.REMOTE_REPOSITORIES_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: (request) =>
        list(request.filter?.kind ?? EntityKind.UNSPECIFIED),
      getResource: (request) => get(request.id),
    });
    router.service(ConfigurationService, {
      saveConfiguration: save,
      deleteConfiguration: remove,
    });
    router.service(WorkerService, { inspectRepository: inspect });
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
  const operations = () => [
    list.mock.calls.length,
    get.mock.calls.length,
    save.mock.calls.length,
    remove.mock.calls.length,
    inspect.mock.calls.length,
  ];
  return { list, get, save, remove, inspect, client, view, operations };
}

const column = () =>
  document.querySelector<HTMLElement>(".settings-content-column > div")!;
const dialog = () =>
  document.querySelector<HTMLDialogElement>("dialog.settings-task-dialog")!;
const taskHeader = () =>
  document.querySelector<HTMLElement>(".settings-task-header")!;
const category = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const settle = () =>
  act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 300));
  });

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

it("plays the Repositories category entrance once per explicit opening without replay", async () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(unoptedSettingsCategory);
  expect(column().getAttribute("data-entrance")).toBeNull();
  category(CATEGORY);
  const node = column();
  expect(node.getAttribute("data-entrance")).toBe("y4");
  expect(node.querySelectorAll("[data-entrance]")).toHaveLength(0);
  dispatchAnimation(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  await screen.findByText("Fixture repo");
  const reads = value.operations();
  // Refetch and re-selecting the current category never replay it.
  await act(async () => {
    await value.client.invalidateQueries();
  });
  category(CATEGORY);
  expect(column()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  // The motion itself reads and writes nothing.
  expect(value.save).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
  expect(value.inspect).not.toHaveBeenCalled();
  expect(value.operations().slice(2)).toEqual(reads.slice(2));
  // Leaving and returning is a new explicit activation.
  category(unoptedSettingsCategory);
  expect(column().getAttribute("data-entrance")).toBeNull();
  category(CATEGORY);
  expect(column().getAttribute("data-entrance")).toBe("y4");
});

it("plays the entrance for a targeted Repositories entry and a fresh Settings visit", () => {
  const value = fixture();
  const view = render(
    value.view(
      <Settings entryDestination={SettingsEntryDestination.Repositories} />,
    ),
  );
  expect(column().querySelector("h1")?.textContent).toBe(CATEGORY);
  expect(column().getAttribute("data-entrance")).toBe("y4");
  dispatchAnimation(column());
  expect(column().getAttribute("data-entrance")).toBeNull();
  // A hidden Settings visit that is shown again is a fresh visit.
  view.rerender(
    value.view(
      <Settings
        visible={false}
        entryDestination={SettingsEntryDestination.Repositories}
      />,
    ),
  );
  view.rerender(
    value.view(
      <Settings
        visible
        entryDestination={SettingsEntryDestination.Repositories}
      />,
    ),
  );
  expect(column().getAttribute("data-entrance")).toBe("y4");
});

it("leaves no stale Repositories motion when the category changes before completion", () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(CATEGORY);
  const old = column();
  expect(old.getAttribute("data-entrance")).toBe("y4");
  category(unoptedSettingsCategory);
  expect(document.contains(old)).toBe(false);
  dispatchAnimation(old);
  expect(column().getAttribute("data-entrance")).toBeNull();
  category(CATEGORY);
  const fresh = column();
  expect(fresh).not.toBe(old);
  expect(fresh.getAttribute("data-entrance")).toBe("y4");
  // The stale completion of the first visit cannot settle the new one.
  dispatchAnimation(old);
  expect(fresh.getAttribute("data-entrance")).toBe("y4");
});

it("settles Repositories motion for Animation Off then OS reduced motion without replay", async () => {
  document.documentElement.dataset.animation = "true";
  const setReduced = reducedMotion(false);
  const value = fixture();
  render(value.view(<Settings />));
  category(CATEGORY);
  const node = column();
  expect(node.getAttribute("data-entrance")).toBe("y4");
  await setAnimation("false");
  expect(node.getAttribute("data-entrance")).toBeNull();
  await setAnimation("true");
  expect(node.getAttribute("data-entrance")).toBeNull();
  // OS reduced motion on a fresh activation settles it the same way.
  category(unoptedSettingsCategory);
  category(CATEGORY);
  expect(column().getAttribute("data-entrance")).toBe("y4");
  setReduced(true);
  expect(column().getAttribute("data-entrance")).toBeNull();
  setReduced(false);
  expect(column().getAttribute("data-entrance")).toBeNull();
  // Animation Off at activation never sets the attribute.
  await setAnimation("false");
  category(unoptedSettingsCategory);
  category(CATEGORY);
  expect(column().getAttribute("data-entrance")).toBeNull();
});

it("animates the edit task header once and disposes it immediately with opener focus restored", async () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(CATEGORY);
  dispatchAnimation(column());
  const opener = await screen.findByRole("button", {
    name: "Edit Fixture repo",
  });
  opener.focus();
  fireEvent.click(opener);
  const host = dialog();
  const header = taskHeader();
  expect(header.getAttribute("data-entrance")).toBe("y4");
  // Only the static header moves, never the native host or the form body.
  expect(host.hasAttribute("data-entrance")).toBe(false);
  expect(host.querySelectorAll("[data-entrance]")).toHaveLength(1);
  const reads = value.operations();
  dispatchAnimation(header);
  expect(header.getAttribute("data-entrance")).toBeNull();
  // Reads, refetches and typing never replay it.
  await act(async () => {
    await value.client.invalidateQueries();
  });
  const name = await screen.findByLabelText("Name");
  fireEvent.change(name, { target: { value: "Edited name" } });
  expect(header.getAttribute("data-entrance")).toBeNull();
  expect((name as HTMLInputElement).value).toBe("Edited name");
  expect(value.save).not.toHaveBeenCalled();
  expect(value.operations()[2]).toBe(reads[2]);
  // Escape disposes the task without waiting for any animation.
  fireEvent(host, new Event("cancel", { cancelable: true }));
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(opener));
  expect(value.save).not.toHaveBeenCalled();
});

it("animates the edit task header once while its entrance is in flight and Escape disposes it", async () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(CATEGORY);
  dispatchAnimation(column());
  const opener = await screen.findByRole("button", {
    name: "Edit Fixture repo",
  });
  opener.focus();
  fireEvent.click(opener);
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
  fireEvent(dialog(), new Event("cancel", { cancelable: true }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.querySelector("[data-entrance]")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(opener));
  // Reopening is a new window with its own single entrance.
  fireEvent.click(opener);
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
});

it("animates the delete confirmation once without deleting on completion", async () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(CATEGORY);
  dispatchAnimation(column());
  const opener = await screen.findByRole("button", {
    name: "Delete Fixture repo",
  });
  opener.focus();
  fireEvent.click(opener);
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
  expect(dialog().hasAttribute("data-entrance")).toBe(false);
  dispatchAnimation(taskHeader());
  expect(taskHeader().getAttribute("data-entrance")).toBeNull();
  await settle();
  expect(value.remove).not.toHaveBeenCalled();
  fireEvent(dialog(), new Event("cancel", { cancelable: true }));
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(opener));
  expect(value.remove).not.toHaveBeenCalled();
});
