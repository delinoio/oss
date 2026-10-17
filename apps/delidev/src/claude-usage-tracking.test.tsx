// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  configure,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  ClaudeUsageIdentityReviewSchema,
  ClaudeUsageLoginState,
  EntityKind,
  NativeSubscriptionDiagnosticSchema,
  ResourceSchema,
  ResourceService,
  SubscriptionObservationAction,
  SubscriptionService,
  SystemCapability,
  SystemService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { OAuthNativeAction, OAuthNativeProvider } from "./account-oauth";
import { ManagedSubscriptionAccount } from "./subscription-accounts";
import {
  claudeUsageAuthorizationURL,
  claudeUsageRowIdentity,
} from "./claude-usage-tracking";
import {
  ClaudeUsageCapability,
  ClaudeUsageView,
  claudeUsageSection,
  claudeUsageView,
} from "./claude-usage-state";
import { MutationIntents } from "./mutation";
import { SettingsLifetime } from "./settings-lifetime";
import { SettingsDialogSize, SettingsTaskDialog } from "./settings-task";
import { encode } from "./documents";
import { pressEnter } from "./test-form-enter";

// The two-second account read and the polls are slower than the default wait.
configure({ asyncUtilTimeout: 5000 });
afterEach(() => vi.useRealTimers());

const manual = "https://platform.claude.com/oauth/code/callback";
function authorization(redirect = manual) {
  const url = new URL("https://claude.com/cai/oauth/authorize");
  for (const [key, value] of Object.entries({
    code: "true",
    client_id: "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
    response_type: "code",
    redirect_uri: redirect,
    scope: "user:profile user:inference user:sessions:claude_code",
    code_challenge: "a".repeat(43),
    code_challenge_method: "S256",
    state: "b".repeat(43),
  }))
    url.searchParams.set(key, value);
  return url.href;
}

const generation = newRequestId();
const operationId = newRequestId();
const commitment = "ab".repeat(32);
const minutes = (value: number) =>
  new Date(Date.now() + value * 60_000).toISOString();
const identity = {
  email: "alex@example.com",
  organization: "Example Org",
  plan: "Max plan",
  commitment,
};
const operation = (state: string, extra: Record<string, unknown> = {}) => ({
  id: operationId,
  state,
  started_at: minutes(-1),
  expires_at: minutes(10),
  ...extra,
});
const windows = (observed = minutes(-1)) => [
  {
    id: "five_hour",
    remaining: 0.75,
    observed_at: observed,
    reset_at: minutes(120),
  },
  {
    id: "seven_day",
    remaining: 0.5,
    observed_at: observed,
    reset_at: minutes(60 * 24 * 4),
  },
];
const linked = (extra: Record<string, unknown> = {}) => ({
  connection: "linked",
  generation,
  identity,
  published: true,
  linked_at: minutes(-30),
  last_success_at: minutes(-1),
  windows: windows(),
  ...extra,
});

let revision = 3n;
function accountOf(
  usage: Record<string, unknown> | undefined,
  connected = true,
  observation?: Record<string, unknown>,
): Resource {
  return create(ResourceSchema, {
    id: accountId,
    kind: EntityKind.ACCOUNT,
    revision,
    schemaVersion: 2,
    documentJson: encode({
      alias: "Server Claude",
      type: "subscription",
      subscription_service: "claude",
      enabled: true,
      health: connected ? "ready" : "disconnected",
      ...(connected ? { connection: { id: newRequestId() } } : {}),
      subscription: {
        claude_credential: "server-token",
        generation: newRequestId(),
        ...(usage ? { claude_usage: usage } : {}),
        ...(observation ? { observation } : {}),
      },
    }),
  });
}
const accountId = newRequestId();

interface Options {
  usage?: Record<string, unknown>;
  connected?: boolean;
  /** An existing quota observation owner of the account document. */
  observation?: Record<string, unknown>;
  capabilities?: SystemCapability[] | "loading" | "error";
  progress?: () => Record<string, unknown>;
  dialog?: (close: () => void) => void;
  native?: boolean;
}
const available = [
  SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
  SystemCapability.CLAUDE_SERVER_LOGIN_V1,
  SystemCapability.CLAUDE_SERVER_USAGE_V1,
];

function mount(options: Options = {}) {
  let current = accountOf(
    options.usage,
    options.connected ?? true,
    options.observation,
  );
  const set = (usage: Record<string, unknown> | undefined) => {
    revision += 1n;
    current = accountOf(usage, options.connected ?? true, options.observation);
    return current;
  };
  const respond = (usage?: Record<string, unknown>) => ({
    account: usage === undefined ? current : set(usage),
  });
  const capabilities = options.capabilities ?? available;
  const status = vi.fn(async () => {
    if (capabilities === "loading") return new Promise<never>(() => undefined);
    if (capabilities === "error")
      throw new ConnectError("status unavailable", Code.Unavailable);
    return { capabilities };
  });
  const start = vi.fn((request) => ({
    ...respond(),
    operationId,
    usageGeneration: generation,
    replayed: false,
    request,
  }));
  const progress = vi.fn(
    options.progress ??
      (() => ({
        state: ClaudeUsageLoginState.PREPARING,
        operationId,
        usageGeneration: generation,
      })),
  );
  const submit = vi.fn(async (request: { code: Uint8Array }) => ({
    accepted: true,
    replayed: false,
    length: request.code.length,
  }));
  const confirm = vi.fn((_request: unknown) => respond());
  const cancel = vi.fn((_request: unknown) => respond());
  const disconnect = vi.fn((_request: unknown) => respond());
  const observe = vi.fn((request) => ({
    account: current,
    operationId: request.mutation.requestId,
  }));
  const browserGeneration = newRequestId();
  const native = vi.fn(
    async (
      _opening: string,
      _action: OAuthNativeAction,
      _generation: string,
      _operation: string,
      _url: string,
    ) => ({ generation: browserGeneration }),
  );
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: status });
    router.service(ResourceService, {
      getResource: () => ({ resource: current }),
    });
    router.service(SubscriptionService, {
      startClaudeUsageLogin: start,
      getClaudeUsageLoginProgress: progress as never,
      submitClaudeUsageApprovalCode: submit,
      confirmClaudeUsageLink: confirm,
      cancelClaudeUsageLogin: cancel,
      disconnectClaudeUsage: disconnect,
      requestSubscriptionObservation: observe,
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const close = vi.fn();
  let isActive = true;
  const body = () => (
    <ManagedSubscriptionAccount
      initial={current}
      active={isActive}
      close={close}
    />
  );
  const tree = () => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <OAuthNativeProvider control={native}>
          <SettingsLifetime>
            {() => (
              <MutationIntents>
                {options.dialog ? (
                  <SettingsTaskDialog
                    title="Manage subscription"
                    size={SettingsDialogSize.Wide}
                    close={close}
                  >
                    {body()}
                  </SettingsTaskDialog>
                ) : (
                  body()
                )}
              </MutationIntents>
            )}
          </SettingsLifetime>
        </OAuthNativeProvider>
      </QueryClientProvider>
    </TransportProvider>
  );
  const view = render(tree());
  return {
    ...view,
    client,
    /** Hides or shows the Manage task the way an overlay does. */
    setActive: (next: boolean) => {
      isActive = next;
      view.rerender(tree());
    },
    set,
    close,
    status,
    start,
    progress,
    submit,
    confirm,
    cancel,
    disconnect,
    observe,
    native,
    section: () =>
      view.container.ownerDocument.querySelector<HTMLElement>(".claude-usage"),
  };
}
const usageSection = async () => {
  const heading = await screen.findByRole("heading", {
    name: "Usage tracking",
  });
  return heading.closest<HTMLElement>("section.claude-usage")!;
};
const state = async (value: ClaudeUsageView) => {
  await waitFor(async () =>
    expect((await usageSection()).getAttribute("data-state")).toBe(value),
  );
  return usageSection();
};
const waitingProgress =
  (extra: Record<string, unknown> = {}) =>
  () => ({
    state: ClaudeUsageLoginState.WAITING,
    operationId,
    usageGeneration: generation,
    url: authorization(),
    codeRequested: false,
    ...extra,
  });
const confirmingProgress = () => ({
  state: ClaudeUsageLoginState.AWAITING_CONFIRMATION,
  operationId,
  usageGeneration: generation,
  identity: create(ClaudeUsageIdentityReviewSchema, {
    email: identity.email,
    organization: identity.organization,
    plan: identity.plan,
    identityCommitment: commitment,
  }),
});

it("shows the loading state while the capability read is pending and sends nothing", async () => {
  const value = mount({ capabilities: "loading" });
  const section = await state(ClaudeUsageView.Loading);
  expect(within(section).getByRole("status").textContent).toMatch(
    /Checking whether this server supports usage tracking/,
  );
  expect(value.start).not.toHaveBeenCalled();
  expect(
    within(section).queryByRole("button", { name: "Connect usage account" }),
  ).toBeNull();
});

it("shows the unavailable state when support cannot be verified", async () => {
  const value = mount({ capabilities: "error" });
  const section = await state(ClaudeUsageView.Unavailable);
  expect(within(section).getByRole("alert").textContent).toMatch(
    /could not be verified/,
  );
  expect(value.start).not.toHaveBeenCalled();
});

it("shows update guidance without any action when the server lacks the capability", async () => {
  const value = mount({
    capabilities: [
      SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
      SystemCapability.CLAUDE_SERVER_LOGIN_V1,
    ],
    usage: linked(),
  });
  const section = await state(ClaudeUsageView.UpdateRequired);
  expect(section.textContent).toMatch(/needs a server update/);
  expect(section.textContent).toMatch(/execution sign-in stays unchanged/);
  expect(within(section).queryAllByRole("button")).toHaveLength(0);
  // Older peers answer Unimplemented, so nothing is submitted or polled.
  await new Promise((resolve) => setTimeout(resolve, 100));
  expect(value.start).not.toHaveBeenCalled();
  expect(value.progress).not.toHaveBeenCalled();
  expect(value.observe).not.toHaveBeenCalled();
});

it("starts one usage login from the not-connected state and opens the browser through the native binding", async () => {
  const value = mount({ progress: waitingProgress() });
  const section = await state(ClaudeUsageView.NotConnected);
  expect(section.textContent).toMatch(
    /Connect a Claude account to fetch usage limits/,
  );
  const connect = within(section).getByRole("button", {
    name: "Connect usage account",
  });
  await waitFor(() =>
    expect((connect as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(connect);
  fireEvent.click(connect);
  await waitFor(() => expect(value.start).toHaveBeenCalledTimes(1));
  const request = value.start.mock.calls[0][0];
  expect(request.mutation.id).toBe(accountId);
  expect(request.mutation.expectedRevision).toBe(revision);
  expect(request.mutation.requestId).toMatch(/^[0-9a-f-]{36}$/);
  // The server allocates the operation; the document carries it afterwards.
  value.set({
    connection: "connecting",
    generation,
    operation: operation("waiting"),
  });
  await state(ClaudeUsageView.Waiting);
  await waitFor(() => expect(value.native).toHaveBeenCalledTimes(1));
  const [, action, , attempt, url] = value.native.mock.calls[0];
  expect(action).toBe(OAuthNativeAction.ClaudeSubscriptionOpen);
  expect(attempt).toBe(operationId);
  expect(url).toBe(authorization());
});

it("never opens a client localhost callback or a foreign address", async () => {
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: waitingProgress({
      url: authorization("http://localhost:4567/callback"),
    }),
  });
  const section = await state(ClaudeUsageView.Waiting);
  await waitFor(() =>
    expect(within(section).getAllByRole("alert")[0].textContent).toMatch(
      /address was not recognized/,
    ),
  );
  expect(
    (
      within(section).getByRole("button", {
        name: "Open browser again",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(value.native).not.toHaveBeenCalled();
  expect(claudeUsageAuthorizationURL(authorization())).toBe(true);
  expect(
    claudeUsageAuthorizationURL(
      authorization("http://localhost:4567/callback"),
    ),
  ).toBe(false);
  expect(
    claudeUsageAuthorizationURL(
      authorization().replace("claude.com", "attacker.invalid"),
    ),
  ).toBe(false);
});

it("does not open the browser by itself in a reopened dialog but offers Open browser again", async () => {
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: waitingProgress(),
  });
  const section = await state(ClaudeUsageView.Waiting);
  const open = within(section).getByRole("button", {
    name: "Open browser again",
  });
  await waitFor(() => expect((open as HTMLButtonElement).disabled).toBe(false));
  expect(value.native).not.toHaveBeenCalled();
  fireEvent.click(open);
  await waitFor(() => expect(value.native).toHaveBeenCalledTimes(1));
  expect(value.native.mock.calls[0][1]).toBe(
    OAuthNativeAction.ClaudeSubscriptionOpen,
  );
  await new Promise((resolve) => setTimeout(resolve, 20));
  fireEvent.click(open);
  await waitFor(() => expect(value.native).toHaveBeenCalledTimes(2));
  expect(value.native.mock.calls[1][1]).toBe(
    OAuthNativeAction.ClaudeSubscriptionReopen,
  );
  expect(value.native.mock.calls[1][0]).toBe(value.native.mock.calls[0][0]);
  expect(value.start).not.toHaveBeenCalled();
});

it("rotates the opening identity after a failed browser open and records the failure", async () => {
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: waitingProgress(),
  });
  const section = await state(ClaudeUsageView.Waiting);
  const open = within(section).getByRole("button", {
    name: "Open browser again",
  });
  await waitFor(() => expect((open as HTMLButtonElement).disabled).toBe(false));
  value.native.mockRejectedValueOnce(new Error("blocked"));
  fireEvent.click(open);
  await screen.findByText(/browser could not be opened/);
  expect(value.native.mock.calls.at(-1)?.[1]).toBe(OAuthNativeAction.Dispose);
  fireEvent.click(open);
  await waitFor(() => expect(value.native).toHaveBeenCalledTimes(3));
  expect(value.native.mock.calls[2][1]).toBe(
    OAuthNativeAction.ClaudeSubscriptionOpen,
  );
  expect(value.native.mock.calls[2][0]).not.toBe(value.native.mock.calls[0][0]);
});

it("focuses the single write-only approval-code input and submits the code exactly once", async () => {
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: waitingProgress({ codeRequested: true }),
  });
  const input = (await screen.findByLabelText(
    "Approval code",
  )) as HTMLInputElement;
  expect(input.type).toBe("password");
  await waitFor(() => expect(document.activeElement).toBe(input));
  expect(screen.getAllByLabelText("Approval code")).toHaveLength(1);
  fireEvent.change(input, { target: { value: "code-123" } });
  fireEvent.click(screen.getByRole("button", { name: "Submit code" }));
  await waitFor(() => expect(value.submit).toHaveBeenCalledTimes(1));
  const request = value.submit.mock.calls[0][0] as unknown as {
    operationId: string;
    usageGeneration: string;
    mutation: { requestId: string };
  };
  expect(request.operationId).toBe(operationId);
  expect(request.usageGeneration).toBe(generation);
  // The field is cleared immediately and cannot submit a second code.
  expect(input.value).toBe("");
  await waitFor(() => expect(input.disabled).toBe(true));
  fireEvent.click(screen.getByRole("button", { name: "Submit code" }));
  expect(value.submit).toHaveBeenCalledTimes(1);
});

it("pauses login progress polling while the capability status refetches and keeps the last reply", async () => {
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: waitingProgress({ codeRequested: true }),
  });
  const input = (await screen.findByLabelText(
    "Approval code",
  )) as HTMLInputElement;
  await waitFor(() => expect(value.progress).toHaveBeenCalled());
  let release: () => void = () => undefined;
  value.status.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        release = () => resolve({ capabilities: available });
      }) as never,
  );
  const before = value.status.mock.calls.length;
  void value.client.invalidateQueries();
  await waitFor(() => expect(value.status.mock.calls.length).toBe(before + 1));
  await new Promise((resolve) => setTimeout(resolve, 100));
  const calls = value.progress.mock.calls.length;
  // A retained successful status must not keep polling while it refetches.
  await new Promise((resolve) => setTimeout(resolve, 1400));
  expect(value.progress.mock.calls.length).toBe(calls);
  expect(screen.getByLabelText("Approval code")).toBe(input);
  release();
  await waitFor(() =>
    expect(value.progress.mock.calls.length).toBeGreaterThan(calls),
  );
});

it("submits the approval code with plain Enter exactly once and ignores held or composing Enter", async () => {
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: waitingProgress({ codeRequested: true }),
  });
  const input = (await screen.findByLabelText(
    "Approval code",
  )) as HTMLInputElement;
  await waitFor(() => expect(input.disabled).toBe(false));
  fireEvent.change(input, { target: { value: "code-789" } });
  for (const press of [
    { composing: true },
    { keyCode: 229 },
    { repeat: true },
    { shiftKey: true },
    { ctrlKey: true },
    { altKey: true },
    { metaKey: true },
  ])
    expect(pressEnter(input, press)).toBe(0);
  expect(value.submit).not.toHaveBeenCalled();
  expect(input.value).toBe("code-789");
  expect(pressEnter(input)).toBe(1);
  await waitFor(() => expect(value.submit).toHaveBeenCalledTimes(1));
  expect(input.value).toBe("");
});

it("does not send a blank approval code with Enter", async () => {
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: waitingProgress({ codeRequested: true }),
  });
  const input = (await screen.findByLabelText(
    "Approval code",
  )) as HTMLInputElement;
  await waitFor(() => expect(input.disabled).toBe(false));
  pressEnter(input);
  expect(value.submit).not.toHaveBeenCalled();
});

it("rejects an invalid approval code locally and keeps the input", async () => {
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: waitingProgress({ codeRequested: true }),
  });
  const input = (await screen.findByLabelText(
    "Approval code",
  )) as HTMLInputElement;
  await waitFor(() => expect(input.disabled).toBe(false));
  fireEvent.change(input, { target: { value: "bad code" } });
  fireEvent.click(screen.getByRole("button", { name: "Submit code" }));
  await screen.findByText(/code was not sent/);
  expect(value.submit).not.toHaveBeenCalled();
});

it("replays the exact original approval-code request after an uncertain result", async () => {
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: waitingProgress({ codeRequested: true }),
  });
  value.submit.mockRejectedValueOnce(
    new ConnectError("lost", Code.Unavailable),
  );
  const input = (await screen.findByLabelText(
    "Approval code",
  )) as HTMLInputElement;
  await waitFor(() => expect(input.disabled).toBe(false));
  fireEvent.change(input, { target: { value: "code-456" } });
  fireEvent.click(screen.getByRole("button", { name: "Submit code" }));
  const retry = await screen.findByRole("button", {
    name: "Retry original request",
  });
  fireEvent.click(retry);
  await waitFor(() => expect(value.submit).toHaveBeenCalledTimes(2));
  const [first, second] = value.submit.mock.calls.map(
    (call) => call[0] as unknown as { mutation: { requestId: string } },
  );
  expect(second.mutation.requestId).toBe(first.mutation.requestId);
});

it("cancels only through the explicit Cancel sign-in action", async () => {
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: waitingProgress(),
  });
  const section = await state(ClaudeUsageView.Waiting);
  const cancel = within(section).getByRole("button", {
    name: "Cancel sign-in",
  });
  await waitFor(() =>
    expect((cancel as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(cancel);
  await waitFor(() => expect(value.cancel).toHaveBeenCalledTimes(1));
  const request = value.cancel.mock.calls[0][0] as unknown as {
    operationId: string;
    usageGeneration: string;
  };
  expect(request.operationId).toBe(operationId);
  expect(request.usageGeneration).toBe(generation);
});

it("hides without cancelling on Escape, disposes the browser binding and keeps the server login", async () => {
  const value = mount({
    dialog: () => undefined,
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: waitingProgress({ codeRequested: true }),
  });
  const input = (await screen.findByLabelText(
    "Approval code",
  )) as HTMLInputElement;
  await waitFor(() => expect(input.disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Open browser again" }));
  await waitFor(() => expect(value.native).toHaveBeenCalledTimes(1));
  fireEvent.change(input, { target: { value: "pending-code" } });
  const dialog = screen.getByRole("dialog");
  fireEvent(dialog, new Event("cancel", { cancelable: true }));
  await waitFor(() => expect(value.close).toHaveBeenCalled());
  value.unmount();
  expect(value.cancel).not.toHaveBeenCalled();
  expect(value.disconnect).not.toHaveBeenCalled();
  expect(value.native.mock.calls.at(-1)?.[1]).toBe(OAuthNativeAction.Dispose);
});

it("disposes the browser binding and never opens one automatically while Manage is hidden", async () => {
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: waitingProgress(),
  });
  const section = await state(ClaudeUsageView.Waiting);
  const open = within(section).getByRole("button", {
    name: "Open browser again",
  });
  await waitFor(() => expect((open as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(open);
  await waitFor(() => expect(value.native).toHaveBeenCalledTimes(1));
  value.setActive(false);
  await waitFor(() =>
    expect(value.native.mock.calls.at(-1)?.[1]).toBe(OAuthNativeAction.Dispose),
  );
  const calls = value.native.mock.calls.length;
  value.setActive(true);
  await new Promise((resolve) => setTimeout(resolve, 100));
  expect(value.native).toHaveBeenCalledTimes(calls);
});

it("keeps the login progress, address and identity out of the shared query cache", async () => {
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: waitingProgress(),
  });
  const section = await state(ClaudeUsageView.Waiting);
  await waitFor(() => expect(value.progress).toHaveBeenCalled());
  await waitFor(() =>
    expect(
      (
        within(section).getByRole("button", {
          name: "Open browser again",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  const keys = value.client
    .getQueryCache()
    .getAll()
    .map((query) =>
      JSON.stringify(query.queryKey, (_key, item) =>
        typeof item === "bigint" ? item.toString() : item,
      ).toLowerCase(),
    );
  expect(keys.some((key) => key.includes("progress"))).toBe(false);
  value.unmount();
});

it("keeps terminal actions disabled until the account document reflects the progress result", async () => {
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: () => ({
      state: ClaudeUsageLoginState.FAILED,
      operationId,
      usageGeneration: generation,
    }),
  });
  const section = await state(ClaudeUsageView.LoginFailed);
  const again = () =>
    within(section).getByRole("button", {
      name: "Try again",
    }) as HTMLButtonElement;
  await new Promise((resolve) => setTimeout(resolve, 300));
  expect(again().disabled).toBe(true);
  fireEvent.click(again());
  expect(value.start).not.toHaveBeenCalled();
  // The revised document arrives through the account read.
  value.set({
    connection: "connecting",
    generation,
    operation: operation("failed"),
  });
  await waitFor(() => expect(again().disabled).toBe(false));
  fireEvent.click(again());
  await waitFor(() => expect(value.start).toHaveBeenCalledTimes(1));
  expect(value.start.mock.calls[0][0].mutation.expectedRevision).toBe(revision);
});

it("shows the reviewed identity, goes back by discarding, and links only the reviewed commitment", async () => {
  const value = mount({
    usage: {
      connection: "confirming",
      generation,
      operation: operation("succeeded"),
      identity,
    },
    progress: confirmingProgress,
  });
  const section = await state(ClaudeUsageView.Confirmation);
  expect(section.textContent).toMatch(/Confirm usage account/);
  expect(section.textContent).toMatch(/alex@example.com/);
  expect(section.textContent).toMatch(/Max plan/);
  expect(section.textContent).toMatch(/Example Org/);
  expect(section.textContent).toMatch(/Going back discards this sign-in/);
  const link = within(section).getByRole("button", {
    name: "Link usage account",
  });
  await waitFor(() => expect((link as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(link);
  await waitFor(() => expect(value.confirm).toHaveBeenCalledTimes(1));
  const request = value.confirm.mock.calls[0][0] as unknown as {
    identityCommitment: string;
    operationId: string;
    usageGeneration: string;
  };
  expect(request.identityCommitment).toBe(commitment);
  expect(request.operationId).toBe(operationId);
  expect(request.usageGeneration).toBe(generation);
  expect(value.cancel).not.toHaveBeenCalled();
});

it("Back discards the unconfirmed sign-in through the original cancel operation", async () => {
  const value = mount({
    usage: {
      connection: "confirming",
      generation,
      operation: operation("succeeded"),
      identity,
    },
    progress: confirmingProgress,
  });
  const section = await state(ClaudeUsageView.Confirmation);
  const back = within(section).getByRole("button", { name: "Back" });
  await waitFor(() => expect((back as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(back);
  await waitFor(() => expect(value.cancel).toHaveBeenCalledTimes(1));
  expect(value.confirm).not.toHaveBeenCalled();
});

it("disables linking when the reviewed commitment differs from the staged identity", async () => {
  const value = mount({
    usage: {
      connection: "confirming",
      generation,
      operation: operation("succeeded"),
      identity: { ...identity, commitment: "cd".repeat(32) },
    },
    progress: confirmingProgress,
  });
  const section = await state(ClaudeUsageView.Confirmation);
  await screen.findByText(/signed-in account changed after it was reviewed/);
  const link = within(section).getByRole("button", {
    name: "Link usage account",
  });
  expect((link as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(link);
  expect(value.confirm).not.toHaveBeenCalled();
});

it("shows a failed login with its closed diagnostic and offers a fresh attempt", async () => {
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("failed"),
    },
    progress: () => ({
      state: ClaudeUsageLoginState.FAILED,
      operationId,
      usageGeneration: generation,
      diagnostic: create(NativeSubscriptionDiagnosticSchema, {
        detectedVersion: "2.1.287",
        requiredVersion: "2.1.236",
        phase: 4,
        code: "unsupported",
        correlationId: newRequestId(),
      }),
    }),
  });
  const section = await state(ClaudeUsageView.LoginFailed);
  expect(within(section).getAllByRole("alert")[0].textContent).toMatch(
    /usage sign-in failed/,
  );
  await waitFor(() => expect(section.textContent).toMatch(/2\.1\.287/));
  const again = within(section).getByRole("button", { name: "Try again" });
  await waitFor(() =>
    expect((again as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(again);
  await waitFor(() => expect(value.start).toHaveBeenCalledTimes(1));
});

it("keeps a recovery-required login visible with only an explicit cancel", async () => {
  mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("recovery-required"),
    },
    progress: () => ({
      state: ClaudeUsageLoginState.RECOVERY_REQUIRED,
      operationId,
      usageGeneration: generation,
    }),
  });
  const section = await state(ClaudeUsageView.Recovery);
  expect(section.textContent).toMatch(/needs recovery on the server/);
  expect(
    within(section).getByRole("button", { name: "Cancel sign-in" }),
  ).toBeTruthy();
  expect(
    within(section).queryByRole("button", { name: "Try again" }),
  ).toBeNull();
});

it("shows the linked identity, remaining quota with reset times and the observation time", async () => {
  mount({ usage: linked() });
  const section = await state(ClaudeUsageView.Linked);
  expect(section.textContent).toMatch(/Connected/);
  expect(section.textContent).toMatch(/Usage account/);
  expect(section.textContent).toMatch(/alex@example.com/);
  expect(section.textContent).toMatch(/Linked manually/);
  expect(section.textContent).toMatch(/5-hour limit/);
  expect(section.textContent).toMatch(/7-day limit/);
  expect(section.textContent).toMatch(/75% remaining/);
  expect(section.textContent).toMatch(/50% remaining/);
  expect(section.textContent).toMatch(/Updated/);
  expect(
    within(section).getByRole("button", { name: "Disconnect usage" }),
  ).toBeTruthy();
  // There is no second refresh action inside the section.
  expect(within(section).queryByRole("button", { name: /Refresh/ })).toBeNull();
});

it("disconnects usage only after a confirmation bound to the usage generation", async () => {
  const value = mount({ usage: linked(), dialog: () => undefined });
  const section = await state(ClaudeUsageView.Linked);
  const open = within(section).getByRole("button", {
    name: "Disconnect usage",
  });
  await waitFor(() => expect((open as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(open);
  const keep = await screen.findByRole("button", {
    name: "Keep tracking usage",
  });
  fireEvent.click(keep);
  expect(value.disconnect).not.toHaveBeenCalled();
  fireEvent.click(open);
  fireEvent.click(
    await screen.findByRole("button", { name: "Confirm disconnect" }),
  );
  await waitFor(() => expect(value.disconnect).toHaveBeenCalledTimes(1));
  const request = value.disconnect.mock.calls[0][0] as unknown as {
    usageGeneration: string;
    mutation: { id: string };
  };
  expect(request.usageGeneration).toBe(generation);
  expect(request.mutation.id).toBe(accountId);
});

it("blocks usage actions while a quota observation owns the account", async () => {
  const value = mount({
    usage: linked(),
    observation: { phase: "queued" },
  });
  const section = await state(ClaudeUsageView.Linked);
  await new Promise((resolve) => setTimeout(resolve, 100));
  const disconnect = within(section).getByRole("button", {
    name: "Disconnect usage",
  }) as HTMLButtonElement;
  expect(disconnect.disabled).toBe(true);
  fireEvent.click(disconnect);
  expect(value.disconnect).not.toHaveBeenCalled();
});

it("closes a disconnect confirmation when another client replaced the usage generation", async () => {
  const value = mount({ usage: linked(), dialog: () => undefined });
  const section = await state(ClaudeUsageView.Linked);
  const open = within(section).getByRole("button", {
    name: "Disconnect usage",
  });
  await waitFor(() => expect((open as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(open);
  await screen.findByRole("button", { name: "Confirm disconnect" });
  value.set(linked({ generation: newRequestId() }));
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Confirm disconnect" }),
    ).toBeNull(),
  );
  expect(value.disconnect).not.toHaveBeenCalled();
});

it("marks an observation older than five minutes as stale", async () => {
  mount({
    usage: linked({
      last_success_at: minutes(-12),
      windows: windows(minutes(-12)),
    }),
  });
  const section = await state(ClaudeUsageView.Stale);
  expect(section.textContent).toMatch(/Out of date/);
  expect(section.textContent).toMatch(/more than 5 minutes old/);
  expect(section.textContent).toMatch(/75%/);
});

it("pauses reads during backoff, keeps evidence and never sends an explicit refresh", async () => {
  const value = mount({
    usage: linked({
      failure: "rate-limited",
      backoff_until: minutes(20),
    }),
  });
  const section = await state(ClaudeUsageView.Backoff);
  expect(section.textContent).toMatch(/Reads paused/);
  expect(section.textContent).toMatch(/asked DeliDev to wait/);
  expect(section.textContent).toMatch(/rate limited/);
  expect(section.textContent).toMatch(/75% remaining/);
  const refresh = await screen.findByRole("button", {
    name: "Refresh account status and quota",
  });
  await waitFor(() =>
    expect((refresh as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(refresh);
  await new Promise((resolve) => setTimeout(resolve, 100));
  expect(value.observe).not.toHaveBeenCalled();
});

it("shows a closed failure for a rejected read and keeps the prior evidence", async () => {
  mount({ usage: linked({ failure: "malformed" }) });
  const section = await state(ClaudeUsageView.RefreshFailed);
  expect(within(section).getAllByRole("alert")[0].textContent).toMatch(
    /Latest refresh failed/,
  );
  expect(section.textContent).toMatch(/response was not recognized/);
  expect(section.textContent).toMatch(/75% remaining/);
});

it("requires an explicit reconnect, keeps stale evidence and never reconnects by itself", async () => {
  const value = mount({
    usage: {
      connection: "reconnect-required",
      generation,
      identity,
      failure: "unauthorized",
      last_success_at: minutes(-2),
      windows: windows(minutes(-2)),
    },
  });
  const section = await state(ClaudeUsageView.ReconnectRequired);
  expect(section.textContent).toMatch(/needs to be connected again/);
  expect(section.textContent).toMatch(/Stale/);
  await new Promise((resolve) => setTimeout(resolve, 100));
  expect(value.start).not.toHaveBeenCalled();
  const reconnect = within(section).getByRole("button", {
    name: "Reconnect usage account",
  });
  await waitFor(() =>
    expect((reconnect as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(reconnect);
  await waitFor(() => expect(value.start).toHaveBeenCalledTimes(1));
  // A refresh is not offered from a reconnect-required connection.
  expect(within(section).queryByRole("button", { name: /Refresh/ })).toBeNull();
  const footer = await screen.findByRole("button", {
    name: "Refresh account status",
  });
  fireEvent.click(footer);
  await new Promise((resolve) => setTimeout(resolve, 100));
  expect(value.observe).not.toHaveBeenCalled();
});

it("uses the single Manage Refresh footer to request a usage observation for a linked connection", async () => {
  const value = mount({ usage: linked() });
  await state(ClaudeUsageView.Linked);
  const refresh = await screen.findByRole("button", {
    name: "Refresh account status and quota",
  });
  await waitFor(() =>
    expect((refresh as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(refresh);
  await waitFor(() => expect(value.observe).toHaveBeenCalledTimes(1));
  const request = value.observe.mock.calls[0][0] as unknown as {
    action: SubscriptionObservationAction;
  };
  expect(request.action).toBe(SubscriptionObservationAction.QUOTA);
  expect(
    screen.getAllByRole("button", { name: /^Refresh account status/ }),
  ).toHaveLength(1);
});

it("offers no connect action for a disconnected execution account and explains why", async () => {
  const value = mount({ connected: false });
  const section = await state(ClaudeUsageView.NotConnected);
  expect(section.textContent).toMatch(/Connect this Claude account before/);
  const connect = within(section).getByRole("button", {
    name: "Connect usage account",
  });
  expect((connect as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(connect);
  expect(value.start).not.toHaveBeenCalled();
});

it("treats a malformed usage section as unreadable instead of guessing a connection", async () => {
  mount({ usage: { connection: "linked", generation: "not-an-id" } });
  const section = await state(ClaudeUsageView.Unreadable);
  expect(within(section).getByRole("alert").textContent).toMatch(
    /state is not recognized/,
  );
  expect(within(section).queryAllByRole("button")).toHaveLength(0);
});

it("rejects a connecting or confirming section that omits its login operation", () => {
  for (const connection of ["connecting", "confirming"])
    expect(
      claudeUsageSection({ claude_usage: { connection, generation } }).kind,
    ).toBe("unreadable");
  expect(
    claudeUsageSection({
      claude_usage: {
        connection: "connecting",
        generation,
        operation: operation("preparing"),
      },
    }).kind,
  ).toBe("present");
});

it("rejects a linked section that omits its identity, publication marker or link time", () => {
  for (const missing of [
    { identity: undefined },
    { identity: { organization: "Acme", plan: "max" } },
    { published: false },
    { published: undefined },
    { linked_at: undefined },
    { linked_at: "not-a-time" },
  ])
    expect(claudeUsageSection({ claude_usage: linked(missing) }).kind).toBe(
      "unreadable",
    );
  expect(claudeUsageSection({ claude_usage: linked() }).kind).toBe("present");
});

it("projects only closed fields and identifies a manually linked row only with observations", () => {
  expect(claudeUsageSection({}).kind).toBe("absent");
  expect(
    claudeUsageSection({ claude_usage: { connection: "bogus" } }).kind,
  ).toBe("unreadable");
  expect(
    claudeUsageSection({
      claude_usage: {
        ...linked(),
        credential_id: newRequestId(),
        retired_credentials: [newRequestId()],
      },
    }),
  ).toMatchObject({ kind: "present" });
  expect(claudeUsageRowIdentity({ claude_usage: linked() })).toBe(
    "alex@example.com",
  );
  expect(
    claudeUsageRowIdentity({
      claude_usage: linked({ windows: [], last_success_at: undefined }),
    }),
  ).toBeUndefined();
  expect(
    claudeUsageRowIdentity({
      claude_usage: {
        connection: "reconnect-required",
        generation,
        identity,
        windows: windows(),
        last_success_at: minutes(-1),
      },
    }),
  ).toBeUndefined();
});

it("derives every distinct view from capability, lifecycle and clock", () => {
  const now = Date.now();
  const present = (usage: Record<string, unknown>) =>
    claudeUsageSection({ claude_usage: usage });
  const view = (
    usage: Record<string, unknown> | undefined,
    capability = ClaudeUsageCapability.Available,
    progress?: ClaudeUsageLoginState,
  ) =>
    claudeUsageView({
      capability,
      section: usage ? present(usage) : { kind: "absent" },
      progress,
      now,
    });
  expect(view(undefined, ClaudeUsageCapability.Loading)).toBe(
    ClaudeUsageView.Loading,
  );
  expect(view(undefined, ClaudeUsageCapability.Unavailable)).toBe(
    ClaudeUsageView.Unavailable,
  );
  expect(view(linked(), ClaudeUsageCapability.UpdateRequired)).toBe(
    ClaudeUsageView.UpdateRequired,
  );
  expect(view(undefined)).toBe(ClaudeUsageView.NotConnected);
  expect(
    view({
      connection: "connecting",
      generation,
      operation: operation("preparing"),
    }),
  ).toBe(ClaudeUsageView.Preparing);
  expect(
    view(
      {
        connection: "connecting",
        generation,
        operation: operation("preparing"),
      },
      ClaudeUsageCapability.Available,
      ClaudeUsageLoginState.WAITING,
    ),
  ).toBe(ClaudeUsageView.Waiting);
  expect(
    view({
      connection: "confirming",
      generation,
      operation: operation("succeeded"),
      identity,
    }),
  ).toBe(ClaudeUsageView.Confirmation);
  expect(linked().last_success_at).toBeTruthy();
  expect(view(linked())).toBe(ClaudeUsageView.Linked);
  expect(
    view(linked({ backoff_until: minutes(5), failure: "forbidden" })),
  ).toBe(ClaudeUsageView.Backoff);
  // An expired deadline stops being a backoff but keeps the closed failure.
  expect(
    view(linked({ backoff_until: minutes(-5), failure: "forbidden" })),
  ).toBe(ClaudeUsageView.RefreshFailed);
  expect(view(linked({ last_success_at: minutes(-10) }))).toBe(
    ClaudeUsageView.Stale,
  );
});

it("returns keyboard focus to the section heading when the focused control disappears", async () => {
  let canceled = false;
  const value = mount({
    usage: {
      connection: "connecting",
      generation,
      operation: operation("waiting"),
    },
    progress: () =>
      canceled
        ? {
            state: ClaudeUsageLoginState.CANCELED,
            operationId,
            usageGeneration: generation,
          }
        : waitingProgress()(),
  });
  const section = await state(ClaudeUsageView.Waiting);
  const cancel = within(section).getByRole("button", {
    name: "Cancel sign-in",
  });
  await waitFor(() =>
    expect((cancel as HTMLButtonElement).disabled).toBe(false),
  );
  cancel.focus();
  expect(document.activeElement).toBe(cancel);
  fireEvent.click(cancel);
  await waitFor(() => expect(value.cancel).toHaveBeenCalledTimes(1));
  canceled = true;
  value.set({
    connection: "connecting",
    generation,
    operation: operation("canceled"),
  });
  await state(ClaudeUsageView.LoginFailed);
  const heading = screen.getByRole("heading", { name: "Usage tracking" });
  await waitFor(() => expect(document.activeElement).toBe(heading));
}, 15000);
