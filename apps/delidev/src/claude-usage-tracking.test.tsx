// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  configure,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, expect, it, vi } from "vitest";
import {
  ClaudeUsageLoginState,
  EntityKind,
  ResourceSchema,
  ResourceService,
  SubscriptionObservationAction,
  SubscriptionService,
  SystemCapability,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { OAuthNativeAction, OAuthNativeProvider } from "./account-oauth";
import {
  ClaudeUsageHealth,
  ClaudeUsageLoginPhase,
  claudeUsageHealth,
  claudeUsageProjection,
} from "./claude-usage-tracking";
import { encode } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { SettingsLifetime } from "./settings-lifetime";
import { ManagedSubscriptionAccount } from "./subscription-accounts";

configure({ asyncUtilTimeout: 4000 });
afterEach(() => cleanup());

const authorization = (redirect: string) =>
  "https://claude.com/cai/oauth/authorize?" +
  new URLSearchParams({
    code: "true",
    client_id: "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
    response_type: "code",
    redirect_uri: redirect,
    scope: "user:profile user:inference user:sessions:claude_code",
    code_challenge: "a".repeat(43),
    code_challenge_method: "S256",
    state: "b".repeat(32),
  }).toString();
const MANUAL = "https://platform.claude.com/oauth/code/callback";
const iso = (offset: number) => new Date(Date.now() + offset).toISOString();

interface Server {
  usage?: Record<string, unknown>;
  quota: unknown[];
  connection: string;
  revision: bigint;
}
function document(server: Server, id: string) {
  return create(ResourceSchema, {
    id,
    kind: EntityKind.ACCOUNT,
    revision: server.revision,
    schemaVersion: 2,
    documentJson: encode({
      alias: "Server Claude",
      type: "subscription",
      subscription_service: "claude",
      enabled: true,
      health: "ready",
      connection: { id: server.connection },
      quota: server.quota,
      subscription: {
        claude_credential: "server-token",
        generation: newRequestId(),
        identity_commitment: "ab".repeat(32),
        ...(server.usage ? { claude_usage: server.usage } : {}),
      },
    }),
  });
}
const linkedUsage = (extra: Record<string, unknown> = {}) => ({
  generation: newRequestId(),
  linked_at: iso(-60_000),
  credential_id: newRequestId(),
  identity: {
    email: "alex@example.com",
    organization: "Example Org",
    plan: "max",
    commitment: "cd".repeat(32),
  },
  read: {},
  ...extra,
});
const windows = (observed: number) => [
  {
    id: "five_hour",
    comparison_group: "claude",
    blocking: false,
    remaining: 0.75,
    reset_at: iso(2 * 3600_000),
    observed_at: new Date(observed).toISOString(),
    state: "observed",
  },
  {
    id: "seven_day",
    comparison_group: "claude",
    blocking: false,
    remaining: 0.5,
    reset_at: iso(4 * 24 * 3600_000),
    observed_at: new Date(observed).toISOString(),
    state: "observed",
  },
];

function mount(
  server: Server,
  capabilities: SystemCapability[] = [
    SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
    SystemCapability.CLAUDE_SERVER_LOGIN_V1,
    SystemCapability.CLAUDE_SERVER_USAGE_V1,
  ],
  progress: () => Record<string, unknown> = () => ({}),
) {
  const id = newRequestId();
  const calls = {
    start: vi.fn((request) => {
      server.revision += 1n;
      server.usage = {
        ...(server.usage ?? {}),
        operation: {
          id: request.mutation.requestId,
          generation: request.mutation.requestId,
          state: "preparing",
        },
      };
      return {
        operationId: request.mutation.requestId,
        usageGeneration: request.mutation.requestId,
      };
    }),
    progress: vi.fn(() => progress()),
    submit: vi.fn(
      (_request: {
        code: Uint8Array;
        operationId: string;
        usageGeneration: string;
      }) => ({
        accepted: true,
      }),
    ),
    confirm: vi.fn((_request: { operationId: string }) => ({})),
    cancel: vi.fn((_request: { operationId: string }) => ({})),
    disconnect: vi.fn((_request: { usageGeneration: string }) => ({})),
    observe: vi.fn((request) => ({
      operationId: request.mutation.requestId,
      account: document(server, id),
    })),
  };
  const native = vi.fn(async (_opening: string, action: string) => ({
    generation: action === OAuthNativeAction.Dispose ? "" : newRequestId(),
  }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities }) });
    router.service(ResourceService, {
      getResource: () => ({ resource: document(server, id) }),
    });
    router.service(SubscriptionService, {
      startClaudeUsage: calls.start,
      getClaudeUsageProgress: calls.progress,
      submitClaudeUsageCode: calls.submit,
      confirmClaudeUsageLink: calls.confirm,
      cancelClaudeUsage: calls.cancel,
      disconnectClaudeUsage: calls.disconnect,
      requestSubscriptionObservation: calls.observe,
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <OAuthNativeProvider control={native as never}>
          <SettingsLifetime>
            {() => (
              <MutationIntents>
                <ManagedSubscriptionAccount
                  initial={document(server, id)}
                  active
                  close={vi.fn()}
                />
              </MutationIntents>
            )}
          </SettingsLifetime>
        </OAuthNativeProvider>
      </QueryClientProvider>
    </TransportProvider>,
  );
  return { calls, native };
}

it("offers Connect usage account for an unlinked account without touching execution", async () => {
  const server: Server = {
    quota: [],
    connection: newRequestId(),
    revision: 3n,
  };
  const { calls } = mount(server);
  await screen.findByRole("heading", { name: "Usage tracking" });
  expect(
    await screen.findByText("Connect a Claude account to fetch usage limits."),
  ).toBeTruthy();
  expect(
    screen.getByText("Your execution sign-in stays unchanged."),
  ).toBeTruthy();
  const connect = await screen.findByRole("button", {
    name: "Connect usage account",
  });
  await waitFor(() =>
    expect((connect as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(connect);
  await waitFor(() => expect(calls.start).toHaveBeenCalledTimes(1));
  const request = calls.start.mock.calls[0][0];
  expect(request.observedUsageGeneration).toBe("");
  expect(request.mutation.expectedRevision).toBeGreaterThan(0n);
  expect(calls.confirm).not.toHaveBeenCalled();
});

it("shows server-update guidance and submits nothing when the capability is missing", async () => {
  const server: Server = {
    quota: [],
    connection: newRequestId(),
    revision: 3n,
  };
  const { calls } = mount(server, [
    SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
    SystemCapability.CLAUDE_SERVER_LOGIN_V1,
  ]);
  await screen.findByText(/does not support Claude usage tracking/);
  expect(
    screen.queryByRole("button", { name: "Connect usage account" }),
  ).toBeNull();
  expect(calls.start).not.toHaveBeenCalled();
});

it("opens the original address through the trusted binding, takes write-only code focus, hides on Escape and cancels explicitly", async () => {
  const operation = newRequestId();
  const server: Server = {
    quota: [],
    connection: newRequestId(),
    revision: 3n,
    usage: {
      operation: { id: operation, generation: operation, state: "waiting" },
    },
  };
  const { calls, native } = mount(server, undefined, () => ({
    state: ClaudeUsageLoginState.WAITING,
    url: authorization(MANUAL),
    codeRequested: true,
  }));
  // The login is hidden until the user views it: it was not started here.
  fireEvent.click(
    await screen.findByRole("button", { name: "View usage sign-in" }),
  );
  const dialog = await screen.findByRole("dialog");
  const input = (await screen.findByLabelText(
    "Approval code",
  )) as HTMLInputElement;
  await waitFor(() => expect(document_activeElement()).toBe(input));
  expect(input.type).toBe("password");
  await waitFor(() =>
    expect(native).toHaveBeenCalledWith(
      expect.any(String),
      OAuthNativeAction.ClaudeSubscriptionOpen,
      "",
      operation,
      authorization(MANUAL),
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "Open browser again" }));
  await waitFor(() =>
    expect(native).toHaveBeenCalledWith(
      expect.any(String),
      OAuthNativeAction.ClaudeSubscriptionReopen,
      expect.any(String),
      operation,
      authorization(MANUAL),
    ),
  );
  fireEvent.change(input, { target: { value: "approval#code" } });
  fireEvent.click(screen.getByRole("button", { name: "Submit code" }));
  await waitFor(() => expect(calls.submit).toHaveBeenCalledTimes(1));
  const submitted = calls.submit.mock.calls[0][0];
  expect(new TextDecoder().decode(submitted.code)).toBe("approval#code");
  expect(submitted.operationId).toBe(operation);
  expect(submitted.usageGeneration).toBe(operation);
  expect(input.value).toBe("");
  // Escape hides; it never cancels.
  fireEvent.keyDown(dialog, { key: "Escape" });
  fireEvent(dialog, new Event("cancel", { cancelable: true }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(calls.cancel).not.toHaveBeenCalled();
  fireEvent.click(
    await screen.findByRole("button", { name: "View usage sign-in" }),
  );
  fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(calls.cancel).toHaveBeenCalledTimes(1));
  expect(calls.cancel.mock.calls[0][0].operationId).toBe(operation);
});

it("refuses a substituted or localhost-callback address instead of opening it", async () => {
  const operation = newRequestId();
  const server: Server = {
    quota: [],
    connection: newRequestId(),
    revision: 3n,
    usage: {
      operation: { id: operation, generation: operation, state: "waiting" },
    },
  };
  const { native } = mount(server, undefined, () => ({
    state: ClaudeUsageLoginState.WAITING,
    url: authorization("http://localhost:4567/callback"),
    codeRequested: true,
  }));
  fireEvent.click(
    await screen.findByRole("button", { name: "View usage sign-in" }),
  );
  await screen.findByRole("dialog");
  await new Promise((resolve) => setTimeout(resolve, 1300));
  expect(
    native.mock.calls.some(
      (call) => call[1] === OAuthNativeAction.ClaudeSubscriptionOpen,
    ),
  ).toBe(false);
  expect(screen.queryByLabelText("Approval code")).toBeNull();
});

it("reviews the validated identity with Back and Link usage account", async () => {
  const operation = newRequestId();
  const server: Server = {
    quota: [],
    connection: newRequestId(),
    revision: 3n,
    usage: {
      operation: {
        id: operation,
        generation: operation,
        state: "confirming",
        credential_id: newRequestId(),
        identity: {
          email: "alex@example.com",
          organization: "Example Org",
          plan: "max",
          commitment: "cd".repeat(32),
        },
      },
    },
  };
  const { calls } = mount(server);
  fireEvent.click(
    await screen.findByRole("button", { name: "View usage sign-in" }),
  );
  await screen.findByRole("heading", { name: "Confirm usage account" });
  expect(screen.getAllByText("alex@example.com").length).toBeGreaterThan(0);
  expect(screen.getByText("max plan")).toBeTruthy();
  expect(screen.getByText("Organization: Example Org")).toBeTruthy();
  expect(
    screen.getAllByText(/Confirm it is the account you want to track/).length,
  ).toBeGreaterThan(0);
  fireEvent.click(screen.getByRole("button", { name: "Link usage account" }));
  await waitFor(() => expect(calls.confirm).toHaveBeenCalledTimes(1));
  expect(calls.confirm.mock.calls[0][0].operationId).toBe(operation);
  expect(calls.cancel).not.toHaveBeenCalled();
  fireEvent.click(
    await screen.findByRole("button", { name: "View usage sign-in" }),
  );
  fireEvent.click(await screen.findByRole("button", { name: "Back" }));
  await waitFor(() => expect(calls.cancel).toHaveBeenCalledTimes(1));
});

it("shows the linked identity, the 5-hour and 7-day limits and disconnects with a confirmation", async () => {
  const usage = linkedUsage({ last_success_at: iso(-30_000) });
  const server: Server = {
    quota: windows(Date.now() - 30_000),
    connection: newRequestId(),
    revision: 3n,
    usage,
  };
  const { calls } = mount(server);
  await screen.findByText("Linked manually · max plan");
  expect(screen.getByText("alex@example.com")).toBeTruthy();
  expect(screen.getByText("5-hour limit")).toBeTruthy();
  expect(screen.getByText("7-day limit")).toBeTruthy();
  expect(screen.getByText("75% remaining")).toBeTruthy();
  expect(screen.getByText("50% remaining")).toBeTruthy();
  expect(screen.getByText("Connected")).toBeTruthy();
  expect(screen.getAllByRole("button", { name: /Refresh/ }).length).toBe(1);
  fireEvent.click(screen.getByRole("button", { name: "Disconnect usage" }));
  await screen.findByRole("heading", { name: "Disconnect usage?" });
  fireEvent.click(screen.getByRole("button", { name: "Keep connected" }));
  expect(calls.disconnect).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Disconnect usage" }));
  await screen.findByRole("heading", { name: "Disconnect usage?" });
  fireEvent.click(
    screen.getAllByRole("button", { name: "Disconnect usage" }).at(-1)!,
  );
  await waitFor(() => expect(calls.disconnect).toHaveBeenCalledTimes(1));
  expect(calls.disconnect.mock.calls[0][0].usageGeneration).toBe(
    usage.generation,
  );
});

it("reuses the single Manage Refresh footer for the usage read", async () => {
  const usage = linkedUsage({ last_success_at: iso(-30_000) });
  const server: Server = {
    quota: windows(Date.now() - 30_000),
    connection: newRequestId(),
    revision: 3n,
    usage,
  };
  const { calls } = mount(server);
  const refresh = await screen.findByRole("button", {
    name: "Refresh account status and quota",
  });
  await waitFor(() =>
    expect((refresh as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(refresh);
  await waitFor(() => expect(calls.observe).toHaveBeenCalledTimes(1));
  const request = calls.observe.mock.calls[0][0];
  expect(request.action).toBe(SubscriptionObservationAction.QUOTA);
  expect(request.generationId).toBe(usage.generation);
  expect(request.machineId).toBe("");
});

it("keeps loading, waiting, stale, failed, backoff, cleanup and reconnect states distinct", async () => {
  const cases: Array<[string, Record<string, unknown>, RegExp]> = [
    ["waiting", linkedUsage(), /Waiting for the first usage reading/],
    [
      "stale",
      linkedUsage({ last_success_at: iso(-10 * 60_000) }),
      /Usage values are out of date/,
    ],
    [
      "failed",
      linkedUsage({ last_success_at: iso(-30_000), failure: "timeout" }),
      /The last reading failed/,
    ],
    [
      "backoff",
      linkedUsage({
        last_success_at: iso(-30_000),
        failure: "rate_limited",
        backoff_until: iso(10 * 60_000),
      }),
      /asked to pause usage reads/,
    ],
    [
      "cleanup",
      linkedUsage({ cleanup_pending: true }),
      /Removing the previous usage sign-in/,
    ],
    [
      "reconnect",
      linkedUsage({ reconnect_required: true, failure: "refresh_rejected" }),
      /expired or was rejected/,
    ],
  ];
  for (const [name, usage, message] of cases) {
    const server: Server = {
      quota: usage.last_success_at
        ? windows(Date.parse(String(usage.last_success_at)))
        : [],
      connection: newRequestId(),
      revision: 3n,
      usage,
    };
    const { calls } = mount(server);
    await screen.findByText(message);
    // Failures never change the execution account's presentation.
    expect(screen.getByText(/· Connected/)).toBeTruthy();
    if (name === "backoff") {
      const footer = await screen.findByRole("button", {
        name: /Refresh account status/,
      });
      expect((footer as HTMLButtonElement).disabled).toBe(true);
    }
    if (name === "reconnect")
      expect(
        screen.getByRole("button", { name: "Reconnect usage account" }),
      ).toBeTruthy();
    if (name === "reconnect" || name === "cleanup") {
      // The server rejects usage reads for an unreadable connection, so the
      // shared footer must not advertise or send a usage refresh.
      const footer = await screen.findByRole("button", {
        name: /Refresh account status/,
      });
      await waitFor(() =>
        expect((footer as HTMLButtonElement).disabled).toBe(false),
      );
      fireEvent.click(footer);
      await new Promise((resolve) => setTimeout(resolve, 100));
      expect(calls.observe).not.toHaveBeenCalled();
    }
    cleanup();
  }
});

it("localizes the section in Korean", async () => {
  await act(() => i18n.changeLanguage("ko"));
  try {
    const server: Server = {
      quota: [],
      connection: newRequestId(),
      revision: 3n,
    };
    mount(server);
    await screen.findByRole("heading", { name: "사용량 추적" });
    expect(
      await screen.findByRole("button", { name: "사용량 계정 연결" }),
    ).toBeTruthy();
    expect(screen.getByText("실행용 로그인은 그대로 유지됩니다.")).toBeTruthy();
  } finally {
    await act(() => i18n.changeLanguage("en"));
  }
});

it("projects the stored state into closed phases and health", () => {
  const projected = claudeUsageProjection({
    subscription: {
      claude_usage: linkedUsage({
        operation: {
          id: newRequestId(),
          generation: newRequestId(),
          state: "recovery-required",
          failure: "cleanup_uncertain",
        },
      }),
    },
  });
  expect(projected.linked).toBe(true);
  expect(projected.operation?.phase).toBe(ClaudeUsageLoginPhase.Recovery);
  const now = Date.now();
  expect(claudeUsageHealth({ ...projected, cleanupPending: true }, now)).toBe(
    ClaudeUsageHealth.Cleanup,
  );
  expect(
    claudeUsageHealth({ ...projected, backoffUntil: iso(60_000) }, now),
  ).toBe(ClaudeUsageHealth.Backoff);
  expect(
    claudeUsageHealth({ ...projected, lastSuccessAt: iso(-1000) }, now),
  ).toBe(ClaudeUsageHealth.Fresh);
  expect(claudeUsageProjection({}).linked).toBe(false);
});

it("styles with semantic tokens, 8px corners, wrapping and a stacked narrow layout", () => {
  const css = readFileSync(
    join(process.cwd(), "src", "claude-usage-tracking.css"),
    "utf8",
  );
  expect(css).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
  expect(css).toContain("border-radius: 8px");
  expect(css).toContain("min-height: 40px");
  expect(css).toContain("overflow-wrap: anywhere");
  expect(css).toMatch(
    /@media \(max-width: 480px\)[\s\S]*flex-direction: column/,
  );
  expect(css).toContain("var(--surface)");
  expect(css).toContain("var(--warning-border)");
});

it("shows the confirmation for a reconnect even though the old connection stays linked", async () => {
  const operation = newRequestId();
  const server: Server = {
    quota: [],
    connection: newRequestId(),
    revision: 3n,
    usage: linkedUsage({
      operation: {
        id: operation,
        generation: operation,
        state: "confirming",
        credential_id: newRequestId(),
        identity: {
          email: "new@example.com",
          organization: "Example Org",
          plan: "max",
          commitment: "ef".repeat(32),
        },
      },
    }),
  };
  const { calls } = mount(server);
  fireEvent.click(
    await screen.findByRole("button", { name: "View usage sign-in" }),
  );
  await screen.findByRole("heading", { name: "Confirm usage account" });
  fireEvent.click(screen.getByRole("button", { name: "Link usage account" }));
  await waitFor(() => expect(calls.confirm).toHaveBeenCalledTimes(1));
  expect(calls.confirm.mock.calls[0][0].operationId).toBe(operation);
});

it("opens a later login under a fresh native opening after the earlier one is disposed", async () => {
  const operation = newRequestId();
  const server: Server = {
    quota: [],
    connection: newRequestId(),
    revision: 3n,
    usage: {
      operation: { id: operation, generation: operation, state: "waiting" },
    },
  };
  const { calls, native } = mount(server, undefined, () => ({
    state: ClaudeUsageLoginState.WAITING,
    url: authorization(MANUAL),
    codeRequested: true,
  }));
  calls.cancel.mockImplementation(() => {
    server.revision += 1n;
    server.usage = {
      operation: { id: operation, generation: operation, state: "canceled" },
    };
    return {};
  });
  const opens = () =>
    native.mock.calls.filter(
      (call) => call[1] === OAuthNativeAction.ClaudeSubscriptionOpen,
    );
  fireEvent.click(
    await screen.findByRole("button", { name: "View usage sign-in" }),
  );
  await waitFor(() => expect(opens().length).toBe(1));
  const first = opens()[0][0];
  fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
  await waitFor(() =>
    expect(
      native.mock.calls.some(
        (call) => call[1] === OAuthNativeAction.Dispose && call[0] === first,
      ),
    ).toBe(true),
  );
  const connect = await screen.findByRole("button", {
    name: "Connect usage account",
  });
  await waitFor(() =>
    expect((connect as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(connect);
  await waitFor(() => expect(opens().length).toBe(2));
  expect(opens()[1][0]).not.toBe(first);
});

it("reports a rejected first browser open", async () => {
  const operation = newRequestId();
  const server: Server = {
    quota: [],
    connection: newRequestId(),
    revision: 3n,
    usage: {
      operation: { id: operation, generation: operation, state: "waiting" },
    },
  };
  const { native } = mount(server, undefined, () => ({
    state: ClaudeUsageLoginState.WAITING,
    url: authorization(MANUAL),
    codeRequested: true,
  }));
  native.mockImplementation(async (_opening: string, action: string) => {
    if (action === OAuthNativeAction.ClaudeSubscriptionOpen)
      throw new Error("browser unavailable");
    return { generation: "" };
  });
  fireEvent.click(
    await screen.findByRole("button", { name: "View usage sign-in" }),
  );
  expect(
    await screen.findByText(
      "The browser could not be opened. Try Open browser again.",
    ),
  ).toBeTruthy();
});

it("does not offer to retry an approval-code submission whose bytes were erased", async () => {
  const operation = newRequestId();
  const server: Server = {
    quota: [],
    connection: newRequestId(),
    revision: 3n,
    usage: {
      operation: { id: operation, generation: operation, state: "waiting" },
    },
  };
  const { calls } = mount(server, undefined, () => ({
    state: ClaudeUsageLoginState.WAITING,
    url: authorization(MANUAL),
    codeRequested: true,
  }));
  calls.submit.mockImplementationOnce(() => {
    throw new ConnectError("offline", Code.Unavailable);
  });
  fireEvent.click(
    await screen.findByRole("button", { name: "View usage sign-in" }),
  );
  const input = (await screen.findByLabelText(
    "Approval code",
  )) as HTMLInputElement;
  fireEvent.change(input, { target: { value: "approval#code" } });
  fireEvent.click(screen.getByRole("button", { name: "Submit code" }));
  await waitFor(() => expect(calls.submit).toHaveBeenCalledTimes(1));
  await screen.findAllByText(/last approval-code submission is unknown/);
  expect(
    screen.queryByRole("button", { name: "Retry original request" }),
  ).toBeNull();
});

it("retries a disconnect that needs credential cleanup with the original request", async () => {
  const usage = linkedUsage({ last_success_at: iso(-30_000) });
  const server: Server = {
    quota: windows(Date.now() - 30_000),
    connection: newRequestId(),
    revision: 3n,
    usage,
  };
  const { calls } = mount(server);
  calls.disconnect.mockImplementationOnce(() => {
    throw new ConnectError("cleanup pending", Code.FailedPrecondition);
  });
  fireEvent.click(
    await screen.findByRole("button", { name: "Disconnect usage" }),
  );
  await screen.findByRole("heading", { name: "Disconnect usage?" });
  fireEvent.click(
    screen.getAllByRole("button", { name: "Disconnect usage" }).at(-1)!,
  );
  await waitFor(() => expect(calls.disconnect).toHaveBeenCalledTimes(1));
  fireEvent.click(
    await screen.findByRole("button", { name: "Retry original request" }),
  );
  await waitFor(() => expect(calls.disconnect).toHaveBeenCalledTimes(2));
  const [first, second] = calls.disconnect.mock.calls.map(
    (call) =>
      (call[0] as unknown as { mutation: { requestId: string } }).mutation
        .requestId,
  );
  expect(second).toBe(first);
});

it("clears a recovered progress-poll error", async () => {
  const operation = newRequestId();
  const server: Server = {
    quota: [],
    connection: newRequestId(),
    revision: 3n,
    usage: {
      operation: { id: operation, generation: operation, state: "waiting" },
    },
  };
  let polls = 0;
  mount(server, undefined, () => {
    polls += 1;
    if (polls === 1) throw new ConnectError("offline", Code.Unavailable);
    return {
      state: ClaudeUsageLoginState.WAITING,
      url: authorization(MANUAL),
      codeRequested: true,
    };
  });
  fireEvent.click(
    await screen.findByRole("button", { name: "View usage sign-in" }),
  );
  await screen.findAllByText(/result of the last request is unknown/);
  await waitFor(() =>
    expect(
      screen.queryAllByText(/result of the last request is unknown/),
    ).toEqual([]),
  );
  await screen.findByLabelText("Approval code");
});

it("never replays an earlier action's retry after a different action fails", async () => {
  const usage = linkedUsage({
    last_success_at: iso(-30_000),
    reconnect_required: true,
    failure: "refresh_rejected",
  });
  const server: Server = {
    quota: windows(Date.now() - 30_000),
    connection: newRequestId(),
    revision: 3n,
    usage,
  };
  const { calls } = mount(server);
  calls.disconnect.mockImplementationOnce(() => {
    throw new ConnectError("offline", Code.Unavailable);
  });
  calls.start.mockImplementationOnce(() => {
    throw new ConnectError("changed", Code.Aborted);
  });
  fireEvent.click(
    await screen.findByRole("button", { name: "Disconnect usage" }),
  );
  await screen.findByRole("heading", { name: "Disconnect usage?" });
  fireEvent.click(
    screen.getAllByRole("button", { name: "Disconnect usage" }).at(-1)!,
  );
  await screen.findAllByRole("button", { name: "Retry original request" });
  fireEvent.click(
    screen.getByRole("button", { name: "Reconnect usage account" }),
  );
  await waitFor(() => expect(calls.start).toHaveBeenCalledTimes(1));
  await waitFor(() =>
    expect(
      screen.queryAllByRole("button", { name: "Retry original request" }),
    ).toEqual([]),
  );
  expect(calls.disconnect).toHaveBeenCalledTimes(1);
});

function document_activeElement() {
  return globalThis.document.activeElement;
}
