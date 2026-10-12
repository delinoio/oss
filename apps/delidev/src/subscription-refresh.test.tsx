// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { isValidElement, type ComponentProps, type MouseEvent } from "react";
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
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  SubscriptionService,
  SystemCapability,
  SystemService,
  SubscriptionObservationAction,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { document as resourceDocument, encode } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import {
  ManagedSubscriptionAccount,
  SubscriptionAccounts,
} from "./subscription-accounts";
import { SettingsActionScope } from "./settings-action";
import * as settingsAction from "./settings-action";
import * as subscriptionLogin from "./subscription-login";

type Options = {
  supported?: boolean;
  data?: Record<string, unknown>;
  subscription?: Record<string, unknown>;
  list?: boolean;
};
function fixture(options: Options = {}) {
  const connection = newRequestId(),
    generation = newRequestId();
  const state = {
    generation,
    quota_observed_at: "2026-10-10T08:00:00Z",
    quota_state: "observed",
    ...options.subscription,
  };
  const data = {
    alias: "Original account",
    type: "subscription",
    subscription_service: "chatgpt",
    health: "ready",
    connection: { id: connection },
    quota: [
      {
        id: "primary",
        state: "observed",
        remaining: 12,
        observed_at: "2026-10-10T08:00:00Z",
      },
    ],
    subscription: state,
    ...options.data,
  };
  let account = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.ACCOUNT,
    schemaVersion: 2,
    revision: 9007199254740993n,
    documentJson: encode(data),
  });
  const initial = account;
  const close = vi.fn();
  let readFailure = false;
  const read = vi.fn(async (): Promise<{ resource?: Resource }> => {
    if (readFailure)
      throw new ConnectError("private-status-fixture", Code.Unavailable);
    return { resource: account };
  });
  const quota = vi.fn(async (request) => ({
    account,
    operationId: request.mutation.requestId,
  }));
  const lifecycle = vi.fn(() => ({})),
    preferences = vi.fn(() => ({}));
  const unseen = newRequestId();
  const batch = vi.fn((request) => ({
    requestId: request.requestId,
    accounts: [account.id, unseen],
  }));
  const list = vi.fn((request) => ({
    resources: request.filter?.kind === EntityKind.ACCOUNT ? [account] : [],
    nextPageToken:
      request.filter?.kind === EntityKind.ACCOUNT ? "unloaded-next-page" : "",
  }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [
          SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
          SystemCapability.SERVER_SUBSCRIPTION_LOGIN_V1,
          SystemCapability.CLAUDE_SUBSCRIPTIONS_V1,
          ...(options.supported === false
            ? []
            : [SystemCapability.SERVER_SUBSCRIPTION_QUOTA_V2]),
        ],
      }),
    });
    router.service(ResourceService, { getResource: read, listResources: list });
    router.service(SubscriptionService, {
      requestSubscriptionObservation: quota,
      requestSubscription: lifecycle,
      refreshAllSubscriptionQuotas: batch,
    });
  });
  const client = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        staleTime: Infinity,
        refetchOnWindowFocus: false,
      },
      mutations: { retry: false },
    },
  });
  function Harness() {
    return (
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <MutationIntents>
            <SettingsActionScope>
              {options.list ? (
                <SubscriptionAccounts
                  active
                  editAccount={preferences}
                  deleteAccount={preferences}
                />
              ) : (
                <ManagedSubscriptionAccount
                  initial={initial}
                  active
                  close={close}
                />
              )}
            </SettingsActionScope>
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
    );
  }
  return {
    Harness,
    client,
    initial,
    connection,
    generation,
    quota,
    read,
    close,
    lifecycle,
    batch,
    list,
    preferences,
    unseen,
    setAccount: (
      fields: Record<string, unknown>,
      revision = account.revision,
    ) => {
      account = create(ResourceSchema, {
        ...account,
        revision,
        documentJson: encode({ ...resourceDocument(account), ...fields }),
      });
    },
    failRead: () => {
      readFailure = true;
    },
  };
}
const combined = "Refresh account status and quota";
const refreshLogin = "Refresh login";
const logout = "Log out";
const statusRefresh = "Refresh account status";

function actionDisabled(name: string) {
  return (screen.getByRole("button", { name }) as HTMLButtonElement).disabled;
}
async function readyAction(name: string) {
  const button = await screen.findByRole("button", { name });
  await waitFor(() => expect(actionDisabled(name)).toBe(false));
  return button;
}

function holdRead(f: ReturnType<typeof fixture>) {
  let resolve!: (value: { resource?: typeof f.initial }) => void;
  let reject!: (error: ConnectError) => void;
  let started!: () => void;
  const ready = new Promise<void>((resolveStarted) => {
    started = resolveStarted;
  });
  f.read.mockImplementationOnce(
    () =>
      new Promise((accept, refuse) => {
        started();
        resolve = accept;
        reject = refuse;
      }),
  );
  return {
    ready,
    resolve: (resource = f.initial) => resolve({ resource }),
    missing: () => resolve({}),
    reject: () =>
      reject(new ConnectError("private-status-fixture", Code.Unavailable)),
  };
}

function captureActions() {
  const callbacks = new Map<string, () => void>();
  const Button = settingsAction.SettingsActionButton;
  vi.spyOn(settingsAction, "SettingsActionButton").mockImplementation(
    (props: ComponentProps<typeof Button>) => {
      const name =
        typeof props.children === "string"
          ? props.children
          : isValidElement<{ id?: string }>(props.children)
            ? props.children.props.id
            : undefined;
      if (name && props.onClick)
        callbacks.set(name, () =>
          props.onClick!({} as MouseEvent<HTMLButtonElement>),
        );
      return <Button {...props} />;
    },
  );
  return (name: string) => {
    const callback = callbacks.get(name);
    if (!callback) throw new Error("Missing action callback: " + name);
    return callback;
  };
}

function stubLogin() {
  const begin = vi.fn();
  vi.spyOn(subscriptionLogin, "useSubscriptionLogin").mockReturnValue({
    service: undefined,
    begin,
    body: null,
    workflow: false,
    retained: false,
    hidden: false,
    available: true,
    hide: vi.fn(),
    show: vi.fn(),
    leave: vi.fn(),
  });
  return begin;
}

it.each([Code.Aborted, Code.PermissionDenied])(
  "retains lifecycle admission after definitive quota refusal %s until the explicit read settles",
  async (code) => {
    const action = captureActions();
    const f = fixture();
    f.quota.mockRejectedValueOnce(
      new ConnectError("private-quota-fixture", code),
    );
    render(<f.Harness />);
    const refresh = await readyAction(combined);
    await waitFor(() => expect(actionDisabled(refreshLogin)).toBe(false));
    const held = holdRead(f);
    const directRefresh = action(combined);
    const directLogin = action(refreshLogin);
    const directLogout = action(logout);
    // Invoke before React commits the refreshing state to exercise the ref lock.
    act(() => {
      directRefresh();
      directLogin();
      directLogout();
    });
    await waitFor(() => expect(f.quota).toHaveBeenCalledOnce());
    const quotaResult = f.quota.mock.results[0];
    if (quotaResult.type !== "return") throw new Error("Missing quota promise");
    await act(async () => {
      await quotaResult.value.catch(() => undefined);
    });
    await waitFor(() =>
      expect(actionDisabled("Back to subscriptions")).toBe(false),
    );
    expect((refresh as HTMLButtonElement).disabled).toBe(true);
    expect(actionDisabled(refreshLogin)).toBe(true);
    expect(actionDisabled(logout)).toBe(true);
    act(() => {
      action(refreshLogin)();
      action(logout)();
    });
    expect(screen.queryByRole("button", { name: "Confirm logout" })).toBeNull();
    expect(f.lifecycle).not.toHaveBeenCalled();
    await held.ready;
    await act(async () => held.resolve());
    await waitFor(() => expect(actionDisabled(refreshLogin)).toBe(false));
    expect(actionDisabled(logout)).toBe(false);
  },
);

it("read-only explicit refresh fences an already-open logout confirmation and permits dismissal", async () => {
  const action = captureActions();
  const f = fixture({ supported: false });
  render(<f.Harness />);
  await readyAction(statusRefresh);
  await waitFor(() => expect(actionDisabled(logout)).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: logout }));
  const directConfirm = action("Confirm logout");
  const held = holdRead(f);
  act(() => {
    action(statusRefresh)();
    directConfirm();
  });
  await waitFor(() => expect(actionDisabled("Confirm logout")).toBe(true));
  act(() => action("Confirm logout")());
  expect(f.lifecycle).not.toHaveBeenCalled();
  expect(f.quota).not.toHaveBeenCalled();
  const cancel = screen.getByRole("button", { name: "Keep account connected" });
  expect((cancel as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(cancel);
  expect(screen.queryByRole("button", { name: "Confirm logout" })).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Back to subscriptions" }),
  );
  expect(f.close).toHaveBeenCalledOnce();
  await held.ready;
  await act(async () => held.resolve());
  await waitFor(() => expect(actionDisabled(refreshLogin)).toBe(false));
});

it.each([
  {
    name: "disconnected sign-in",
    data: { connection: {}, health: "disconnected" },
    label: "Sign in to ChatGPT",
    key: "subscription-accounts.signInTo_fa4edf",
  },
  {
    name: "Claude reauthentication",
    data: { subscription_service: "claude" },
    label: "Sign in again",
    key: "Sign in again",
  },
])(
  "blocks $name through the explicit read owner, including direct callbacks",
  async ({ data, label, key }) => {
    const begin = stubLogin();
    const action = captureActions();
    const f = fixture({ supported: false, data });
    render(<f.Harness />);
    const button = await screen.findByRole("button", { name: label });
    await waitFor(() =>
      expect((button as HTMLButtonElement).disabled).toBe(false),
    );
    await readyAction(statusRefresh);
    const direct = action(key);
    const held = holdRead(f);
    act(() => {
      action(statusRefresh)();
      direct();
    });
    await waitFor(() =>
      expect((button as HTMLButtonElement).disabled).toBe(true),
    );
    act(() => action(key)());
    expect(begin).not.toHaveBeenCalled();
    expect(f.lifecycle).not.toHaveBeenCalled();
    expect(f.quota).not.toHaveBeenCalled();
    await held.ready;
    await act(async () => held.resolve());
    await waitFor(() =>
      expect((button as HTMLButtonElement).disabled).toBe(false),
    );
  },
);

it.each([
  "failure",
  "missing",
  "wrong ID",
  "wrong service",
  "older revision",
  "recovery",
  "removal",
])(
  "keeps lifecycle unavailable after explicit refresh returns %s evidence",
  async (condition) => {
    const f = fixture({ supported: false });
    render(<f.Harness />);
    const refresh = await readyAction(statusRefresh);
    await waitFor(() => expect(actionDisabled(refreshLogin)).toBe(false));
    const held = holdRead(f);
    fireEvent.click(refresh);
    await waitFor(() =>
      expect((refresh as HTMLButtonElement).disabled).toBe(true),
    );
    let resource = f.initial;
    if (condition === "wrong ID")
      resource = create(ResourceSchema, {
        ...resource,
        id: newRequestId(),
      });
    if (condition === "wrong service")
      resource = create(ResourceSchema, {
        ...resource,
        documentJson: encode({
          ...resourceDocument(resource),
          subscription_service: "claude",
        }),
      });
    if (condition === "older revision")
      resource = create(ResourceSchema, {
        ...resource,
        revision: resource.revision - 1n,
      });
    if (condition === "recovery")
      resource = create(ResourceSchema, {
        ...resource,
        documentJson: encode({
          ...resourceDocument(resource),
          subscription: { recovery_required: true },
        }),
      });
    if (condition === "removal")
      resource = create(ResourceSchema, {
        ...resource,
        documentJson: encode({
          ...resourceDocument(resource),
          removal: { id: newRequestId() },
        }),
      });
    await held.ready;
    await act(async () => {
      if (condition === "failure") held.reject();
      else if (condition === "missing") held.missing();
      else held.resolve(resource);
    });
    await waitFor(() =>
      expect((refresh as HTMLButtonElement).disabled).toBe(false),
    );
    expect(actionDisabled(refreshLogin)).toBe(true);
    expect(actionDisabled(logout)).toBe(true);
    expect(f.lifecycle).not.toHaveBeenCalled();
    expect(f.quota).not.toHaveBeenCalled();
  },
);

it("a newer quota acknowledgment cannot restore lifecycle through a held older read", async () => {
  const f = fixture();
  const newer = create(ResourceSchema, {
    ...f.initial,
    revision: f.initial.revision + 1n,
  });
  f.quota.mockImplementationOnce(async (request) => ({
    account: newer,
    operationId: request.mutation.requestId,
  }));
  render(<f.Harness />);
  const refresh = await readyAction(combined);
  await waitFor(() => expect(actionDisabled(refreshLogin)).toBe(false));
  const held = holdRead(f);
  fireEvent.click(refresh);
  await waitFor(() => expect(f.quota).toHaveBeenCalledOnce());
  await held.ready;
  await act(async () => held.resolve());
  await waitFor(() =>
    expect((refresh as HTMLButtonElement).disabled).toBe(false),
  );
  expect(actionDisabled(refreshLogin)).toBe(true);
  expect(actionDisabled(logout)).toBe(true);
  f.setAccount({}, newer.revision);
  fireEvent.click(refresh);
  await waitFor(() => expect(actionDisabled(refreshLogin)).toBe(false));
  expect(f.lifecycle).not.toHaveBeenCalled();
});

it("ordinary background status reads keep their existing lifecycle readiness", async () => {
  const f = fixture({ supported: false });
  render(<f.Harness />);
  await readyAction(statusRefresh);
  await waitFor(() => expect(actionDisabled(refreshLogin)).toBe(false));
  const held = holdRead(f);
  let background!: Promise<void>;
  act(() => {
    background = f.client.invalidateQueries({ refetchType: "active" });
  });
  await waitFor(() => expect(actionDisabled(statusRefresh)).toBe(true));
  expect(actionDisabled(refreshLogin)).toBe(false);
  expect(actionDisabled(logout)).toBe(false);
  await held.ready;
  await act(async () => {
    held.resolve();
    await background;
  });
});

it("one Manage footer activation captures original bigint bindings and reads status once", async () => {
  const f = fixture();
  render(<f.Harness />);
  const button = await screen.findByRole("button", { name: combined });
  await waitFor(() =>
    expect((button as HTMLButtonElement).disabled).toBe(false),
  );
  const before = f.read.mock.calls.length;
  button.focus();
  expect(document.activeElement).toBe(button);
  expect(await screen.findByRole("tooltip")).toHaveProperty(
    "textContent",
    combined,
  );
  fireEvent.click(button);
  await waitFor(() => expect(f.quota).toHaveBeenCalledOnce());
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(before + 1));
  expect(f.quota.mock.calls[0][0]).toMatchObject({
    machineId: "",
    action: SubscriptionObservationAction.QUOTA,
    connectionId: f.connection,
    generationId: f.generation,
    mutation: { id: f.initial.id, expectedRevision: 9007199254740993n },
  });
  expect(f.quota.mock.calls[0][0]).toMatchObject({
    creditId: "",
    nextCredit: false,
    confirmed: false,
  });
  expect(f.lifecycle).not.toHaveBeenCalled();
  expect(f.preferences).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Refresh quota" })).toBeNull();
  expect(document.activeElement).toBe(button);
  fireEvent.click(
    screen.getByRole("button", { name: "Back to subscriptions" }),
  );
  expect(f.close).toHaveBeenCalledOnce();
});

it("keeps the captured original revision when the concurrent status read advances", async () => {
  const f = fixture();
  render(<f.Harness />);
  const button = await screen.findByRole("button", { name: combined });
  f.setAccount({}, f.initial.revision + 1n);
  fireEvent.click(button);
  await waitFor(() => expect(f.quota).toHaveBeenCalledOnce());
  expect(f.quota.mock.calls[0][0].mutation.expectedRevision).toBe(
    f.initial.revision,
  );
});

it.each([
  {
    name: "Execute lease without a Runner",
    subscription: { lease: { action: "execute", machine_id: newRequestId() } },
  },
  { name: "no Runner", subscription: { owner_machine_id: "" } },
])("keeps server quota eligible for $name", async (value) => {
  const f = fixture({ subscription: value.subscription });
  render(<f.Harness />);
  fireEvent.click(await screen.findByRole("button", { name: combined }));
  await waitFor(() => expect(f.quota).toHaveBeenCalledOnce());
  expect(f.quota.mock.calls[0][0].machineId).toBe("");
});

it.each([
  { name: "older server", supported: false },
  { name: "disconnected", data: { connection: {}, health: "disconnected" } },
  { name: "Claude", data: { subscription_service: "claude" } },
  { name: "recovery", subscription: { recovery_required: true } },
  { name: "removal", data: { removal: { id: newRequestId() } } },
  {
    name: "lifecycle pending",
    subscription: { pending: { id: newRequestId() } },
  },
  {
    name: "other lease",
    subscription: { lease: { action: "refresh", machine_id: newRequestId() } },
  },
  ...["queued", "sending", "uncertain"].flatMap((phase) =>
    ["observation", "server_quota", "server_credit"].map((key) => ({
      name: key + " " + phase,
      subscription: { [key]: { id: newRequestId(), phase } },
    })),
  ),
])("preserves read-only refresh without quota for $name", async (options) => {
  const f = fixture(options);
  render(<f.Harness />);
  const button = await screen.findByRole("button", {
    name: "Refresh account status",
  });
  await waitFor(() =>
    expect((button as HTMLButtonElement).disabled).toBe(false),
  );
  const before = f.read.mock.calls.length;
  fireEvent.click(button);
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(before + 1));
  expect(f.quota).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: combined })).toBeNull();
  expect(screen.queryByRole("button", { name: "Refresh quota" })).toBeNull();
});

it("locks overlap and retains the exact uncertain quota owner through changed status and locale", async () => {
  const f = fixture();
  let reject!: () => void;
  f.quota.mockImplementationOnce(
    () =>
      new Promise((_resolve, rejectPromise) => {
        reject = () =>
          rejectPromise(
            new ConnectError("private-uncertain-fixture", Code.Unavailable),
          );
      }),
  );
  render(<f.Harness />);
  const button = await screen.findByRole("button", { name: combined });
  const held = holdRead(f);
  fireEvent.click(button);
  fireEvent.click(button);
  await waitFor(() => expect(f.quota).toHaveBeenCalledOnce());
  await act(async () => reject());
  const retry = await screen.findByRole("button", {
    name: "Retry original quota or credit request",
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh account status" }),
  );
  expect(f.quota).toHaveBeenCalledOnce();
  const original = f.quota.mock.calls[0][0];
  expect(actionDisabled(refreshLogin)).toBe(true);
  expect(actionDisabled(logout)).toBe(true);
  await held.ready;
  await act(async () => held.resolve());
  expect(actionDisabled(refreshLogin)).toBe(true);
  f.setAccount({ connection: {}, health: "disconnected" });
  await act(async () => {
    await f.client.invalidateQueries({ refetchType: "active" });
    await i18n.changeLanguage("ko");
  });
  expect(f.quota).toHaveBeenCalledOnce();
  expect(screen.queryByRole("button", { name: "쿼터 새로고침" })).toBeNull();
  await act(async () => {
    await i18n.changeLanguage("en");
  });
  expect(retry.isConnected).toBe(true);
  fireEvent.click(retry);
  await waitFor(() => expect(f.quota).toHaveBeenCalledTimes(2));
  expect(f.quota.mock.calls[1][0]).toEqual(original);
  expect(f.lifecycle).not.toHaveBeenCalled();
});

it("failed status reads keep original evidence and read-error retries never submit quota", async () => {
  const f = fixture();
  render(<f.Harness />);
  await screen.findByRole("button", { name: combined });
  const observation = screen.getByText(
    /Last successful observation/,
  ).textContent;
  f.failRead();
  await act(async () => {
    await f.client.invalidateQueries({ refetchType: "active" });
  });
  expect(screen.getByText(/Last successful observation/).textContent).toBe(
    observation,
  );
  const reads = await screen.findAllByRole("button", {
    name: "Refresh account status",
  });
  fireEvent.click(reads[0]);
  await waitFor(() => expect(f.read.mock.calls.length).toBeGreaterThan(2));
  expect(f.quota).not.toHaveBeenCalled();
  expect(screen.queryByText(/private-status-fixture/)).toBeNull();
});

it("a failed quota observation retains prior success values and timestamps", async () => {
  const f = fixture();
  f.quota.mockImplementationOnce(async (request) => ({
    operationId: request.mutation.requestId,
    account: create(ResourceSchema, {
      ...f.initial,
      revision: f.initial.revision + 1n,
      documentJson: encode({
        ...resourceDocument(f.initial),
        subscription: {
          ...(resourceDocument(f.initial).subscription as object),
          quota_state: "failed",
        },
      }),
    }),
  }));
  render(<f.Harness />);
  fireEvent.click(await screen.findByRole("button", { name: combined }));
  await screen.findByText(/The latest refresh failed/);
  const result = f.quota.mock.results[0];
  expect(result.type).toBe("return");
  expect(
    screen
      .getByText(/Last successful observation/)
      .querySelector("time")
      ?.getAttribute("datetime"),
  ).toBe("2026-10-10T08:00:00Z");
  expect(f.lifecycle).not.toHaveBeenCalled();
});

it("uses one original server batch for unloaded account pages and one row quota request", async () => {
  const f = fixture({ list: true });
  render(<f.Harness />);
  await screen.findByRole("article", { name: "Original account" });
  fireEvent.click(await screen.findByRole("button", { name: "Refresh all" }));
  await waitFor(() => expect(f.batch).toHaveBeenCalledOnce());
  expect(f.quota).not.toHaveBeenCalled();
  expect(
    Object.keys(f.batch.mock.calls[0][0])
      .filter((key) => key !== "$typeName")
      .sort(),
  ).toEqual(["requestId"]);
  expect(
    f.list.mock.calls.some(
      ([r]) => r.filter?.pageToken === "unloaded-next-page",
    ),
  ).toBe(false);
  const row = await screen.findByRole("button", {
    name: /^Refresh Original account/,
  });
  await waitFor(() => expect((row as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(row);
  await waitFor(() => expect(f.quota).toHaveBeenCalledOnce());
  expect(f.quota.mock.calls[0][0]).toMatchObject({
    machineId: "",
    connectionId: f.connection,
    generationId: f.generation,
    mutation: { id: f.initial.id, expectedRevision: f.initial.revision },
  });
});

it("localizes the combined semantic icon and tooltip in Korean without a second quota button", async () => {
  await i18n.changeLanguage("ko");
  const f = fixture();
  render(<f.Harness />);
  const button = await screen.findByRole("button", {
    name: "계정 상태와 쿼터 새로고침",
  });
  button.focus();
  expect((await screen.findByRole("tooltip")).textContent).toBe(
    "계정 상태와 쿼터 새로고침",
  );
  expect(button.getAttribute("data-settings-action-presentation")).toBe("icon");
  expect(screen.queryByRole("button", { name: "쿼터 새로고침" })).toBeNull();
  fireEvent.keyDown(button, { key: "Enter" });
  fireEvent.click(button);
  await waitFor(() => expect(f.quota).toHaveBeenCalledOnce());
  expect(document.activeElement).toBe(button);
});
