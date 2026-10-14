// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
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
  AccountService,
  ApiAuthentication,
  ApiProtocol,
  ConfigurationService,
  EntityKind,
  configurationSchemaVersion,
  ProviderInventoryCapability,
  ProviderApiFormatSchema,
  ProviderInventoryEntrySchema,
  ProviderService,
  ResourceSchema,
  ResourceService,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode, type Document } from "./documents";
import { MutationIntents } from "./mutation";
import { Settings } from "./settings";
import { unoptedSettingsCategory } from "./entrance-test-support";

// Issue 3178: AI API Keys category and owned task entrance. Every secret value
// below is a synthetic fixture string that never leaves the test.
const syntheticSecret = "fixture-only-secret";

function dispatch(node: Element) {
  const event = new Event("animationend", { bubbles: true });
  Object.defineProperty(event, "animationName", { value: "entrance-y4" });
  act(() => {
    node.dispatchEvent(event);
  });
}
const column = () =>
  document.querySelector<HTMLElement>(".settings-content-column > div")!;
const taskHeader = () =>
  document.querySelector<HTMLElement>(".settings-task-header")!;
const category = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));

function fixture(existing = false) {
  const providerId = newRequestId();
  const provider = create(ResourceSchema, {
    kind: EntityKind.PROVIDER,
    id: providerId,
    schemaVersion: 1,
    revision: 3n,
    documentJson: encode({
      name: "Fixture provider",
      endpoint: "https://api.example.test/v1",
      protocol: "openai-chat",
      authentication: "bearer",
      enabled: true,
    }),
  });
  const accountDocument = (extra: Document): Document => ({
    alias: "Work key",
    provider_id: providerId,
    type: "api",
    api_protocol: "openai-chat",
    enabled: true,
    exclude_automatic: false,
    recovery_notifications: true,
    quota: [],
    ...extra,
  });
  const account = (value: Document, revision: bigint) =>
    create(ResourceSchema, {
      kind: EntityKind.ACCOUNT,
      id: created.id,
      schemaVersion: configurationSchemaVersion(EntityKind.ACCOUNT, value),
      revision,
      documentJson: encode(value),
    });
  const created = { id: newRequestId() };
  const createdAccount = account(
    accountDocument({ health: "disconnected" }),
    4n,
  );
  const connected = account(
    accountDocument({
      health: "unverified",
      connection: { id: newRequestId(), authentication: "api-key" },
    }),
    5n,
  );
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let saved = false;
  const connect = vi.fn(async (request) => {
    await held;
    saved = true;
    return { account: connected, requestId: request.mutation?.requestId };
  });
  const save = vi.fn(async (request) => ({
    resource: createdAccount,
    requestId: request.mutation?.requestId,
  }));
  const list = vi.fn(() => ({
    resources: saved || existing ? [connected] : [],
  }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({}) });
    router.service(ResourceService, {
      listResources: list,
      getResource: (request) => ({
        resource:
          request.id === created.id
            ? saved
              ? connected
              : createdAccount
            : provider,
      }),
    });
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(AccountService, {
      connectAccount: connect,
      getAccountStatus: () => ({ account: connected }),
    });
    router.service(ProviderService, {
      listProviderPresets: () => ({ presetsJson: encode([]) }),
      listProviderInventory: () => ({
        entries: [
          create(ProviderInventoryEntrySchema, {
            providerId,
            displayName: "Fixture provider",
            enabled: true,
            provider,
            accountCountsAvailable: true,
            apiFormats: [
              create(ProviderApiFormatSchema, {
                protocol: ApiProtocol.OPENAI_CHAT,
                endpoint: "https://api.example.test/v1",
                authentication: ApiAuthentication.BEARER,
              }),
            ],
          }),
        ],
        capabilities: [
          ProviderInventoryCapability.PROVIDER_ACTIVATION,
          ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER,
          ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
          ProviderInventoryCapability.ACCOUNT_TYPE_FILTER,
          ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1,
        ],
      }),
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <Settings />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  return { client, connect, save, list, release, created };
}

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

it("plays the AI API Keys category entrance once per explicit opening", async () => {
  fixture();
  category(unoptedSettingsCategory);
  category("AI API Keys");
  const node = column();
  expect(node.getAttribute("data-entrance")).toBe("y4");
  // The category heading is rendered by the account view inside the column and
  // never carries a second, separate motion.
  expect(node.querySelector("h1")?.textContent).toBe("AI API Keys");
  expect(node.querySelectorAll("[data-entrance]")).toHaveLength(0);
  dispatch(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  // Re-selecting the current category and background reads never replay it.
  category("AI API Keys");
  await act(async () => {
    await Promise.resolve();
  });
  expect(column()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  category(unoptedSettingsCategory);
  expect(column().getAttribute("data-entrance")).toBeNull();
  category("AI API Keys");
  expect(column().getAttribute("data-entrance")).toBe("y4");
});

it("leaves no stale motion when AI API Keys is left before completion", () => {
  fixture();
  category("AI API Keys");
  const old = column();
  category(unoptedSettingsCategory);
  expect(document.contains(old)).toBe(false);
  dispatch(old);
  expect(column().getAttribute("data-entrance")).toBeNull();
  category("AI API Keys");
  expect(column().getAttribute("data-entrance")).toBe("y4");
  expect(column()).not.toBe(old);
});

it("keeps the Add account entrance single across stage advance, drafts and a held validation reply", async () => {
  const f = fixture();
  category("AI API Keys");
  dispatch(column());
  const add = await screen.findByRole("button", { name: "Add AI API key" });
  await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(add);
  const dialog = document.querySelector("dialog.settings-task-dialog")!;
  const header = taskHeader();
  // The native task host and the credential form body never receive the
  // transform; only the static task header moves.
  expect(dialog.hasAttribute("data-entrance")).toBe(false);
  expect(header.getAttribute("data-entrance")).toBe("y4");
  expect(dialog.querySelectorAll("[data-entrance]")).toHaveLength(1);
  dispatch(header);
  const mutations: string[] = [];
  const observer = new MutationObserver((records) =>
    records.forEach((record) => mutations.push(record.attributeName ?? "")),
  );
  observer.observe(header, {
    attributes: true,
    attributeFilter: ["data-entrance"],
  });
  // Advance the original user-owned stage.
  fireEvent.click(
    await screen.findByRole("button", { name: "Fixture provider API key" }),
  );
  const name = (await screen.findByLabelText("Entry name")) as HTMLInputElement;
  const select = screen.queryByLabelText("API format");
  if (select instanceof HTMLSelectElement && select.options.length > 1)
    fireEvent.change(select, { target: { value: select.options[1].value } });
  fireEvent.change(name, { target: { value: "Work key" } });
  const key = screen.getByLabelText("API key") as HTMLInputElement;
  fireEvent.change(key, { target: { value: syntheticSecret } });
  expect(key.value).toBe(syntheticSecret);
  expect(taskHeader()).toBe(header);
  fireEvent.click(screen.getByRole("button", { name: "Add and connect" }));
  await waitFor(() => expect(f.connect).toHaveBeenCalledTimes(1));
  // The held validation reply owns the original request; nothing animates.
  expect(document.querySelector("dialog.settings-task-dialog")).toBe(dialog);
  expect(taskHeader()).toBe(header);
  expect(header.getAttribute("data-entrance")).toBeNull();
  await act(async () => {
    f.release();
    await Promise.resolve();
  });
  await waitFor(() => expect(f.list).toHaveBeenCalled());
  expect(header.getAttribute("data-entrance")).toBeNull();
  expect(f.save).toHaveBeenCalledTimes(1);
  expect(f.connect).toHaveBeenCalledTimes(1);
  expect(mutations).toEqual([]);
  expect(column().getAttribute("data-entrance")).toBeNull();
  // No credential is retained in query caches or markup.
  expect(
    JSON.stringify(
      f.client
        .getQueryCache()
        .getAll()
        .map((query) => query.queryKey),
    ),
  ).not.toContain(syntheticSecret);
  expect(document.body.innerHTML).not.toContain(syntheticSecret);
  observer.disconnect();
});

it("plays a fresh Add account entrance for each opened task window and dismisses immediately", async () => {
  fixture();
  category("AI API Keys");
  dispatch(column());
  const add = await screen.findByRole("button", { name: "Add AI API key" });
  await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(add);
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
  const first = document.querySelector("dialog.settings-task-dialog")!;
  fireEvent.click(
    await screen.findByRole("button", { name: "Fixture provider API key" }),
  );
  fireEvent.change(await screen.findByLabelText("API key"), {
    target: { value: syntheticSecret },
  });
  // Dismissal never waits for the in-flight entrance and leaves no secret behind.
  fireEvent.click(
    screen.getByRole("button", { name: /^Close Add AI API key/ }),
  );
  expect(document.querySelector("dialog.settings-task-dialog")).toBeNull();
  expect(document.body.innerHTML).not.toContain(syntheticSecret);
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Add AI API key",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  const second = document.querySelector("dialog.settings-task-dialog")!;
  expect(second).not.toBe(first);
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
  expect(
    (screen.queryByLabelText("API key") as HTMLInputElement | null)?.value ??
      "",
  ).toBe("");
});

it("settles the AI API Keys entrance for Animation Off and never replays on re-enable", async () => {
  document.documentElement.dataset.animation = "true";
  fixture();
  category("AI API Keys");
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
  expect(column()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
});

it("does not play the AI API Keys entrance when Animation is Off at activation", () => {
  document.documentElement.dataset.animation = "false";
  fixture();
  category("AI API Keys");
  expect(column().getAttribute("data-entrance")).toBeNull();
});

it("settles the AI API Keys task entrance for OS reduced motion without replay", async () => {
  const listeners = new Set<() => void>();
  const media = {
    matches: false,
    addEventListener: (_: string, listener: () => void) =>
      listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) =>
      listeners.delete(listener),
  };
  vi.stubGlobal("matchMedia", () => media);
  fixture();
  category("AI API Keys");
  dispatch(column());
  const add = await screen.findByRole("button", { name: "Add AI API key" });
  await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(add);
  const header = taskHeader();
  expect(header.getAttribute("data-entrance")).toBe("y4");
  act(() => {
    media.matches = true;
    listeners.forEach((changed) => changed());
  });
  expect(header.getAttribute("data-entrance")).toBeNull();
  act(() => {
    media.matches = false;
    listeners.forEach((changed) => changed());
  });
  expect(taskHeader()).toBe(header);
  expect(header.getAttribute("data-entrance")).toBeNull();
});

it("plays one entrance per opened manage connection and edit task window", async () => {
  const f = fixture(true);
  category(unoptedSettingsCategory);
  category("AI API Keys");
  dispatch(column());
  const row = (
    await screen.findByRole("heading", { name: "Work key" })
  ).closest("article")!;
  const manage = (await screen.findByRole("button", {
    name: "Manage connection",
  })) as HTMLButtonElement;
  await waitFor(() => expect(manage.disabled).toBe(false));
  fireEvent.click(manage);
  const first = document.querySelector("dialog.settings-task-dialog")!;
  expect(first.hasAttribute("data-entrance")).toBe(false);
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
  const header = taskHeader();
  dispatch(header);
  // A background refetch of the same account never replays the entrance.
  await act(async () => {
    await f.client.invalidateQueries({ refetchType: "active" });
  });
  expect(taskHeader()).toBe(header);
  expect(header.getAttribute("data-entrance")).toBeNull();
  // Escape dismisses immediately; there is no exit animation.
  fireEvent(first, new Event("cancel", { cancelable: true }));
  expect(document.querySelector("dialog.settings-task-dialog")).toBeNull();
  fireEvent.click(within(row).getByRole("button", { name: /More actions/ }));
  fireEvent.click(screen.getByRole("button", { name: "Edit preferences" }));
  expect(document.querySelector("dialog.settings-task-dialog")).not.toBe(first);
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
});
