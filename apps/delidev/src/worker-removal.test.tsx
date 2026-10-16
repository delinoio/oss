// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  DeviceService,
  EntityKind,
  ErrorDetailSchema,
  ProviderService,
  RemoveWorkerRequest,
  ResourceSchema,
  ResourceService,
  SystemCapability,
  SystemService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode, document as documentOf } from "./documents";
import { ProductIdentityProvider } from "./product-identity-labels";
import { Settings } from "./settings";
import type { ReadLocalWorkerProof } from "./local-worker";

afterEach(cleanup);

const token = `${"A".repeat(42)}A`;
function machine(name: string, revision = 7n, extra: object = {}) {
  return create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.MACHINE,
    revision,
    schemaVersion: 1,
    documentJson: encode({ name, health: "Observed health", ...extra }),
  });
}
function conflict() {
  return new ConnectError("The Worker changed.", Code.Aborted, undefined, [
    {
      desc: ErrorDetailSchema,
      value: create(ErrorDetailSchema, {
        code: "conflict",
        guidance: "Review the current Runner Device.",
        correlationId: newRequestId(),
      }),
    },
  ]);
}
function unavailable() {
  return new ConnectError("The server is unavailable.", Code.Unavailable);
}

interface Options {
  capabilities?: SystemCapability[];
  local?: () => Promise<string>;
  remove?: (request: RemoveWorkerRequest) => Promise<Resource>;
}
function fixture(rows: Resource[], options: Options = {}) {
  const current = new Map(rows.map((row) => [row.id, row]));
  const removals: RemoveWorkerRequest[] = [];
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {
      listResources: () => ({
        resources: [...current.values()].filter(
          (row) => documentOf(row).retired !== true,
        ),
      }),
      getResource: (request) => ({ resource: current.get(request.id) }),
    });
    router.service(ProviderService, {
      listProviderInventory: () => ({ entries: [] }),
      listProviderPresets: () => ({ presetsJson: encode([]) }),
    });
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: options.capabilities ?? [
          SystemCapability.WORKER_REMOVAL_V1,
        ],
      }),
    });
    router.service(DeviceService, {
      removeWorker: async (request) => {
        removals.push(request);
        const resource =
          (await options.remove?.(request)) ??
          create(ResourceSchema, {
            ...current.get(request.mutation!.id)!,
            revision: current.get(request.mutation!.id)!.revision + 1n,
            documentJson: encode({
              ...documentOf(current.get(request.mutation!.id)),
              disabled: true,
              retired: true,
            }),
          });
        current.set(resource.id, resource);
        return {
          machine: resource,
          requestId: request.mutation!.requestId,
          replayed: false,
        };
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
      mutations: { retry: false, gcTime: 0 },
    },
  });
  const read: ReadLocalWorkerProof | undefined = options.local
    ? async () => ({ machineId: await options.local!(), token })
    : undefined;
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <ProductIdentityProvider>
          <Settings readLocalWorker={read} />
        </ProductIdentityProvider>
      </QueryClientProvider>
    </TransportProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Runner Devices" }));
  return { removals, current };
}
const inventory = () =>
  screen.getByRole("region", { name: "Saved runner devices" });
async function ready(dialog: HTMLElement) {
  return await waitFor(() => {
    const button = within(dialog).getByRole("button", {
      name: "Delete Worker",
    }) as HTMLButtonElement;
    expect(button.disabled).toBe(false);
    return button;
  });
}
async function deleteButton(name: string) {
  return (await screen.findByRole("button", {
    name: `Delete Worker ${name}`,
  })) as HTMLButtonElement;
}

it("protects only the exact verified local Worker and keeps same-named Workers deletable", async () => {
  const local = machine("Same name"),
    obsolete = machine("Same name");
  fixture([local, obsolete], { local: async () => local.id });
  await screen.findByText("This app's current local Worker cannot be deleted.");
  const rows = within(inventory()).getAllByRole("article");
  expect(rows).toHaveLength(2);
  const protectedRow = rows.find((row) => row.dataset.machineId === local.id)!;
  expect(
    within(protectedRow).queryByRole("button", { name: /^Delete Worker/ }),
  ).toBeNull();
  expect(
    within(protectedRow).getByRole("button", {
      name: "Inspect installed harnesses",
    }),
  ).toBeTruthy();
  expect(within(protectedRow).getByText("Managed by this app")).toBeTruthy();
  const other = rows.find((row) => row.dataset.machineId === obsolete.id)!;
  await waitFor(() =>
    expect(
      (
        within(other).getByRole("button", {
          name: /^Delete Worker/,
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
});

it("blocks fresh deletion while the native observation is pending or unavailable", async () => {
  const row = machine("Offline helper");
  let reject!: (error: Error) => void;
  const pending = new Promise<string>((_, no) => {
    reject = no;
  });
  fixture([row], { local: () => pending });
  const button = await deleteButton("Offline helper");
  await screen.findByText(/Checking which Worker belongs to this app/);
  expect(button.disabled).toBe(true);
  reject(new Error("unavailable"));
  await screen.findByText(/could not verify its current local Worker/);
  expect(button.disabled).toBe(true);
  expect(
    screen.getByRole("button", { name: "Retry verification" }),
  ).toBeTruthy();
});

it("disables deletion with guidance when the server lacks the removal capability", async () => {
  const row = machine("Old server Worker");
  fixture([row], { capabilities: [], local: async () => newRequestId() });
  expect(
    await screen.findByText(
      "This server does not support deleting Workers. Update the server to enable deletion.",
    ),
  ).toBeTruthy();
  expect((await deleteButton("Old server Worker")).disabled).toBe(true);
});

it("confirms with focus on Cancel, restores the opener on Escape and sends nothing", async () => {
  const row = machine("Offline helper");
  const value = fixture([row], { local: async () => newRequestId() });
  const opener = await deleteButton("Offline helper");
  await waitFor(() => expect(opener.disabled).toBe(false));
  opener.focus();
  fireEvent.click(opener);
  const dialog = await screen.findByRole("dialog", { name: "Delete Worker?" });
  expect(within(dialog).getByText("Offline helper")).toBeTruthy();
  expect(
    within(dialog).getByText(
      "This Worker will lose access to this server and be removed from saved runner devices.",
    ),
  ).toBeTruthy();
  expect(
    within(dialog).getByText(
      "Session history and files on the device will be kept. Running work may require recovery.",
    ),
  ).toBeTruthy();
  expect(document.activeElement).toBe(
    within(dialog).getByRole("button", { name: "Cancel" }),
  );
  fireEvent(dialog, new Event("cancel", { cancelable: true }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog", { name: "Delete Worker?" })).toBeNull(),
  );
  expect(document.activeElement).toBe(opener);
  expect(value.removals).toHaveLength(0);
});

it("sends one revision-bound request, removes the row after success and announces it", async () => {
  const first = machine("First helper", 4n),
    second = machine("Second helper", 9n);
  const value = fixture([first, second], {
    local: async () => newRequestId(),
  });
  const opener = await deleteButton("First helper");
  await waitFor(() => expect(opener.disabled).toBe(false));
  fireEvent.click(opener);
  const dialog = await screen.findByRole("dialog", { name: "Delete Worker?" });
  const confirm = await ready(dialog);
  fireEvent.click(confirm);
  fireEvent.click(confirm);
  await screen.findByText(
    "Deleted Worker First helper. Its session history and device files were kept.",
  );
  expect(value.removals).toHaveLength(1);
  expect(value.removals[0].schemaVersion).toBe(1);
  expect(value.removals[0].mutation?.id).toBe(first.id);
  expect(value.removals[0].mutation?.expectedRevision).toBe(4n);
  expect(screen.getByRole("status").textContent).toContain("First helper");
  await waitFor(() =>
    expect(
      within(inventory()).queryByText("First helper", { selector: "h3" }),
    ).toBeNull(),
  );
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() =>
    expect(
      document.activeElement
        ?.closest("[data-machine-id]")
        ?.getAttribute("data-machine-id"),
    ).toBe(second.id),
  );
});

it("rechecks the native identity on direct confirmation and never sends for the local Worker", async () => {
  const row = machine("Becomes local");
  let local = newRequestId();
  const value = fixture([row], { local: async () => local });
  const opener = await deleteButton("Becomes local");
  await waitFor(() => expect(opener.disabled).toBe(false));
  fireEvent.click(opener);
  const dialog = await screen.findByRole("dialog", { name: "Delete Worker?" });
  local = row.id;
  fireEvent.click(await ready(dialog));
  expect(
    await within(dialog).findByText(
      "This app's current local Worker cannot be deleted.",
    ),
  ).toBeTruthy();
  expect(value.removals).toHaveLength(0);
});

it("refuses a fresh request when the native identity cannot be read at confirmation", async () => {
  const row = machine("Unverified");
  let fail = false;
  const value = fixture([row], {
    local: async () => {
      if (fail) throw new Error("native unavailable");
      return newRequestId();
    },
  });
  const opener = await deleteButton("Unverified");
  await waitFor(() => expect(opener.disabled).toBe(false));
  fireEvent.click(opener);
  const dialog = await screen.findByRole("dialog", { name: "Delete Worker?" });
  fail = true;
  fireEvent.click(await ready(dialog));
  expect(
    await within(dialog).findByText(
      /could not verify its current local Worker/,
    ),
  ).toBeTruthy();
  expect(value.removals).toHaveLength(0);
});

it("requires explicit fresh confirmation after a stale revision", async () => {
  const row = machine("Stale helper", 3n);
  let attempts = 0;
  const value = fixture([row], {
    local: async () => newRequestId(),
    remove: async (request) => {
      attempts += 1;
      if (attempts === 1) throw conflict();
      return create(ResourceSchema, {
        ...row,
        revision: request.mutation!.expectedRevision + 1n,
        documentJson: encode({ name: "Stale helper", retired: true }),
      });
    },
  });
  const opener = await deleteButton("Stale helper");
  await waitFor(() => expect(opener.disabled).toBe(false));
  fireEvent.click(opener);
  const dialog = await screen.findByRole("dialog", { name: "Delete Worker?" });
  fireEvent.click(await ready(dialog));
  expect(
    await within(dialog).findByText(/changed after the confirmation opened/),
  ).toBeTruthy();
  expect(
    (
      within(dialog).getByRole("button", {
        name: "Delete Worker",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Review current Worker" }),
  );
  const confirm = await waitFor(() => {
    const button = within(dialog).getByRole("button", {
      name: "Delete Worker",
    }) as HTMLButtonElement;
    expect(button.disabled).toBe(false);
    return button;
  });
  fireEvent.click(confirm);
  await screen.findByText(/Deleted Worker Stale helper/);
  expect(value.removals).toHaveLength(2);
  expect(value.removals[1].mutation?.requestId).not.toBe(
    value.removals[0].mutation?.requestId,
  );
});

it("replays only the exact original request after an uncertain result", async () => {
  const row = machine("Lost response", 5n);
  let attempts = 0;
  const value = fixture([row], {
    local: async () => newRequestId(),
    remove: async (request) => {
      attempts += 1;
      if (attempts === 1) throw unavailable();
      return create(ResourceSchema, {
        ...row,
        revision: request.mutation!.expectedRevision + 1n,
        documentJson: encode({ name: "Lost response", retired: true }),
      });
    },
  });
  const opener = await deleteButton("Lost response");
  await waitFor(() => expect(opener.disabled).toBe(false));
  fireEvent.click(opener);
  const dialog = await screen.findByRole("dialog", { name: "Delete Worker?" });
  fireEvent.click(await ready(dialog));
  expect(
    await within(dialog).findByText(
      /The result of deleting this Worker is unknown/,
    ),
  ).toBeTruthy();
  expect(
    (
      within(dialog).getByRole("button", {
        name: "Delete Worker",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Retry original deletion" }),
  );
  await screen.findByText(/Deleted Worker Lost response/);
  expect(value.removals).toHaveLength(2);
  expect(value.removals[1].mutation?.requestId).toBe(
    value.removals[0].mutation?.requestId,
  );
  expect(value.removals[1].mutation?.expectedRevision).toBe(5n);
});
