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
  ConfigurationService,
  EntityKind,
  ResourceSchema,
  ResourceService,
  SubscriptionLoginMethod,
  SubscriptionLoginState,
  SubscriptionService,
  SystemCapability,
  SystemService,
  WorkerService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { OAuthNativeAction, OAuthNativeProvider } from "./account-oauth";
import { document as accountDocument, encode } from "./documents";
import { MutationIntents } from "./mutation";
import { Settings } from "./settings";

// Issue 3209: AI Subscription category and owned task entrance. Every account,
// machine and URL below is a synthetic fixture value.
const authorization = new URL("https://claude.com/cai/oauth/authorize");
for (const [key, value] of Object.entries({
  client_id: "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
  response_type: "code",
  redirect_uri: "https://platform.claude.com/oauth/code/callback",
  scope: "user:profile user:inference user:sessions:claude_code",
  code_challenge: "a".repeat(43),
  code_challenge_method: "S256",
  state: "b".repeat(32),
  code: "true",
}))
  authorization.searchParams.set(key, value);
const originalURL = authorization.href;

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
const taskDialog = () =>
  document.querySelector<HTMLDialogElement>("dialog.settings-task-dialog");
const category = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
// A native dialog cancel event is the Escape dismissal the task window handles.
const escape = (dialog: Element) =>
  fireEvent(dialog, new Event("cancel", { cancelable: true }));

function fixture(options: { holdLogin?: boolean; connected?: boolean } = {}) {
  const machine = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.MACHINE,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      name: "Remote Linux",
      disabled: false,
      worker_capabilities: ["native-claude-subscriptions-v1"],
      installations: [
        {
          harness: "claude-code",
          version: "2.1.236",
          state: "detected",
          protocol_verified: true,
          protocol: { state: "verified" },
        },
      ],
    }),
  });
  const row = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.ACCOUNT,
    schemaVersion: 2,
    revision: 1n,
    documentJson: encode({
      alias: "Retained subscription",
      type: "subscription",
      subscription_service: "chatgpt",
      enabled: true,
      health: options.connected ? "connected" : "disconnected",
      quota: [],
      ...(options.connected
        ? {
            connection: { id: newRequestId() },
            subscription: { owner_machine_id: machine.id },
          }
        : {}),
    }),
  });
  let current = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.ACCOUNT,
    schemaVersion: 2,
    revision: 1n,
    documentJson: encode({
      alias: "Claude",
      type: "subscription",
      subscription_service: "claude",
    }),
  });
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const generation = newRequestId(),
    profile = newRequestId(),
    browser = newRequestId();
  const save = vi.fn((request) => {
    current = create(ResourceSchema, {
      ...current,
      revision: current.revision + 1n,
      documentJson: request.documentJson,
    });
    return { requestId: request.mutation.requestId, resource: current };
  });
  const login = vi.fn(async (request) => {
    if (options.holdLogin) await held;
    const operation = request.mutation.requestId;
    current = create(ResourceSchema, {
      ...current,
      revision: current.revision + 1n,
      documentJson: encode({
        ...accountDocument(current),
        subscription: {
          owner_machine_id: machine.id,
          native_profile_id: profile,
          native_operation: { id: operation },
          pending: { id: operation },
        },
      }),
    });
    return { account: current, operationId: operation };
  });
  const progress = vi.fn(() => ({
    state: SubscriptionLoginState.WAITING,
    url: originalURL,
    loginMethod: SubscriptionLoginMethod.BROWSER_CODE,
    generation,
  }));
  const cancel = vi.fn(() => ({ account: current }));
  const native = vi.fn(
    async (
      _opening: string,
      _action: OAuthNativeAction,
      _generation: string,
      _operation: string,
      _url: string,
    ) => ({ generation: browser }),
  );
  const accounts = vi.fn(
    (request: { filter?: { kind?: EntityKind } }) => ({
      resources:
        request.filter?.kind === EntityKind.MACHINE
          ? [machine]
          : request.filter?.kind === EntityKind.ACCOUNT
            ? [row]
            : [],
      nextPageToken: "",
    }),
  );
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [
          SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
          SystemCapability.CLAUDE_SUBSCRIPTIONS_V1,
          SystemCapability.SERVER_SUBSCRIPTION_LOGIN_V1,
        ],
      }),
    });
    router.service(WorkerService, {
      discoverHarnesses: async () => ({}),
    });
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(ResourceService, {
      listResources: accounts,
      getResource: (request) => ({
        resource:
          request.id === machine.id
            ? machine
            : request.id === row.id
              ? row
              : current,
      }),
    });
    router.service(SubscriptionService, {
      requestSubscription: login,
      getSubscriptionProgress: progress,
      cancelSubscription: cancel,
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <OAuthNativeProvider control={native}>
            <Settings />
          </OAuthNativeProvider>
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  return { client, save, login, cancel, native, release, row, accounts };
}

async function openClaudeLogin() {
  category("Instructions");
  category("AI Subscription");
  dispatch(column());
  const add = await screen.findByRole("button", {
    name: "Claude · Add account",
  });
  await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(add);
  await waitFor(() => expect(taskDialog()).not.toBeNull());
}
async function chooseRunner() {
  const runner = await screen.findByRole("combobox", { name: "Runner Device" });
  fireEvent.click(runner);
  await screen.findByRole("option", { name: "Remote Linux" });
  fireEvent.click(screen.getByRole("option", { name: "Remote Linux" }));
  const start = screen.getByRole("button", { name: "Start sign-in" });
  await waitFor(() =>
    expect((start as HTMLButtonElement).disabled).toBe(false),
  );
  return start;
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

it("plays the AI Subscription category entrance once per explicit opening", async () => {
  fixture();
  category("Instructions");
  expect(column().getAttribute("data-entrance")).toBeNull();
  category("AI Subscription");
  const node = column();
  expect(node.getAttribute("data-entrance")).toBe("y4");
  // The standard Settings heading lives inside the moved content column.
  expect(node.querySelector(".settings-category-heading h1")?.textContent).toBe(
    "AI Subscription",
  );
  expect(node.querySelectorAll("[data-entrance]")).toHaveLength(0);
  dispatch(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  // Re-selecting the category and the inventory refetch never replay it.
  category("AI Subscription");
  await screen.findByRole("article", { name: "Retained subscription" });
  expect(column()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  category("Instructions");
  expect(column().getAttribute("data-entrance")).toBeNull();
  category("AI Subscription");
  expect(column().getAttribute("data-entrance")).toBe("y4");
});

it("leaves no stale motion when AI Subscription is left before completion", () => {
  fixture();
  category("AI Subscription");
  const old = column();
  category("Instructions");
  expect(document.contains(old)).toBe(false);
  dispatch(old);
  expect(column().getAttribute("data-entrance")).toBeNull();
  category("AI Subscription");
  expect(column().getAttribute("data-entrance")).toBe("y4");
  expect(column()).not.toBe(old);
});

it("settles the AI Subscription entrance for Animation Off and OS reduced motion without replay", async () => {
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
  fixture();
  category("Instructions");
  category("AI Subscription");
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
  act(() => {
    media.matches = true;
    listeners.forEach((changed) => changed());
    media.matches = false;
    listeners.forEach((changed) => changed());
  });
  expect(column()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
});

it("does not play the AI Subscription entrance when Animation is Off at activation", () => {
  document.documentElement.dataset.animation = "false";
  fixture();
  category("Instructions");
  category("AI Subscription");
  expect(column().getAttribute("data-entrance")).toBeNull();
});

it("keeps one login entrance across a user step and a held login response, and dismisses with Escape", async () => {
  const f = fixture({ holdLogin: true });
  await openClaudeLogin();
  const dialog = taskDialog()!;
  const header = taskHeader();
  expect(dialog.hasAttribute("data-entrance")).toBe(false);
  expect(header.getAttribute("data-entrance")).toBe("y4");
  dispatch(header);
  const mutations: string[] = [];
  const observer = new MutationObserver((records) =>
    records.forEach((record) => mutations.push(record.attributeName ?? "")),
  );
  observer.observe(header, {
    attributes: true,
    attributeFilter: ["data-entrance"],
  });
  // Advance the original user-owned stage and hold the login response.
  const start = await chooseRunner();
  expect(taskDialog()).toBe(dialog);
  fireEvent.click(start);
  await waitFor(() => expect(f.login).toHaveBeenCalledTimes(1));
  expect(taskHeader()).toBe(header);
  expect(header.getAttribute("data-entrance")).toBeNull();
  // Escape hides the retained original operation immediately: no replay, no
  // cancellation, and the held reply cannot reopen the task.
  escape(dialog);
  expect(taskDialog()).toBeNull();
  const reopen = await screen.findByRole("button", {
    name: "View original operation",
  });
  const trigger = document.activeElement;
  await act(async () => {
    f.release();
    await Promise.resolve();
  });
  await waitFor(() => expect(f.login).toHaveBeenCalledTimes(1));
  expect(taskDialog()).toBeNull();
  expect(f.cancel).not.toHaveBeenCalled();
  expect(document.activeElement).toBe(trigger);
  expect(mutations).toEqual([]);
  expect(column().getAttribute("data-entrance")).toBeNull();
  // Only an explicit show of the retained operation mounts a new window.
  fireEvent.click(reopen);
  await waitFor(() => expect(taskDialog()).not.toBeNull());
  expect(taskHeader()).not.toBe(header);
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
  observer.disconnect();
  expect(f.login).toHaveBeenCalledTimes(1);
  expect(f.save).toHaveBeenCalledTimes(1);
});

it("disposes the native OAuth owner on dismissal before any login starts", async () => {
  const f = fixture();
  await openClaudeLogin();
  // Choosing the Runner is a user step inside the same window: no new entrance.
  const header = taskHeader();
  expect(header.getAttribute("data-entrance")).toBe("y4");
  await chooseRunner();
  expect(taskHeader()).toBe(header);
  expect(header.getAttribute("data-entrance")).toBe("y4");
  const disposed = f.native.mock.calls.filter(
    (call) => call[1] === OAuthNativeAction.Dispose,
  ).length;
  escape(taskDialog()!);
  expect(taskDialog()).toBeNull();
  await waitFor(() =>
    expect(
      f.native.mock.calls.filter(
        (call) => call[1] === OAuthNativeAction.Dispose,
      ),
    ).toHaveLength(disposed + 1),
  );
  expect(f.login).not.toHaveBeenCalled();
  expect(f.save).not.toHaveBeenCalled();
  // A fresh explicit Add account plays a fresh entrance.
  const add = screen.getByRole("button", { name: "Claude · Add account" });
  await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(add);
  await waitFor(() => expect(taskDialog()).not.toBeNull());
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
});

it("plays one manage window entrance and ignores refetches", async () => {
  const f = fixture();
  category("Instructions");
  category("AI Subscription");
  dispatch(column());
  const row = await screen.findByRole("article", {
    name: "Retained subscription",
  });
  const manage = within(row).getByRole("button", {
    name: "Manage Retained subscription",
  }) as HTMLButtonElement;
  await waitFor(() => expect(manage.disabled).toBe(false));
  fireEvent.click(manage);
  await waitFor(() => expect(taskDialog()).not.toBeNull());
  const dialog = taskDialog()!;
  const header = taskHeader();
  expect(dialog.hasAttribute("data-entrance")).toBe(false);
  expect(header.getAttribute("data-entrance")).toBe("y4");
  dispatch(header);
  await act(async () => {
    await f.client.invalidateQueries({ refetchType: "active" });
  });
  expect(taskDialog()).toBe(dialog);
  expect(header.getAttribute("data-entrance")).toBeNull();
  escape(dialog);
  expect(taskDialog()).toBeNull();
});

it("keeps one manage window entrance when the logout confirmation step opens and closes", async () => {
  const f = fixture({ connected: true });
  category("Instructions");
  category("AI Subscription");
  dispatch(column());
  const row = await screen.findByRole("article", {
    name: "Retained subscription",
  });
  const manage = within(row).getByRole("button", {
    name: "Manage Retained subscription",
  }) as HTMLButtonElement;
  await waitFor(() => expect(manage.disabled).toBe(false));
  fireEvent.click(manage);
  await waitFor(() => expect(taskDialog()).not.toBeNull());
  const dialog = taskDialog()!;
  const header = taskHeader();
  dispatch(header);
  const logout = await screen.findByRole("button", { name: "Log out" });
  await waitFor(() => expect((logout as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(logout);
  await screen.findByRole("button", { name: "Confirm logout" });
  // The confirmation is a step of the same window: no second entrance.
  expect(document.querySelectorAll("dialog.settings-task-dialog")).toHaveLength(1);
  expect(taskDialog()).toBe(dialog);
  expect(taskHeader()).toBe(header);
  expect(header.getAttribute("data-entrance")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Keep account connected" }));
  expect(taskDialog()).toBe(dialog);
  expect(header.getAttribute("data-entrance")).toBeNull();
  expect(f.cancel).not.toHaveBeenCalled();
});
