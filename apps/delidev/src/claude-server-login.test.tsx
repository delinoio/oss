// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  configure,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  ConfigurationService,
  EntityKind,
  NativeSubscriptionDiagnosticSchema,
  ResourceSchema,
  ResourceService,
  SubscriptionLoginMethod,
  SubscriptionLoginState,
  SubscriptionService,
  SubscriptionServiceId,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { OAuthNativeAction, OAuthNativeProvider } from "./account-oauth";
import {
  claudeLoginURL,
  claudeSetupTokenURL,
} from "./claude-subscription-login";
import { useSubscriptionLogin } from "./subscription-login";
import { SettingsLifetime } from "./settings-lifetime";
import { SettingsDialogSize, SettingsTaskDialog } from "./settings-task";
import { document, encode } from "./documents";
import { RunnerRemediationProvider } from "./runner-remediation";

// The one-second poll cadence is slower than the default async-utility wait.
configure({ asyncUtilTimeout: 4000 });

const authorization = new URL("https://claude.com/cai/oauth/authorize");
for (const [key, value] of Object.entries({
  code: "true",
  client_id: "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
  response_type: "code",
  redirect_uri: "https://platform.claude.com/oauth/code/callback",
  scope: "user:inference",
  code_challenge: "a".repeat(43),
  code_challenge_method: "S256",
  state: "b".repeat(43),
}))
  authorization.searchParams.set(key, value);
const setupURL = authorization.href;

// The server advertises managed-Claude login and no Runner Device exists.
function fixture(dialog = false) {
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
  let state = SubscriptionLoginState.WAITING,
    operation = "";
  let progressURL = setupURL;
  const generation = newRequestId(),
    browser = newRequestId();
  const save = vi.fn((request) => {
    current = create(ResourceSchema, {
      ...current,
      revision: current.revision + 1n,
      documentJson: request.documentJson,
    });
    return { requestId: request.mutation.requestId, resource: current };
  });
  const login = vi.fn((request) => {
    operation = request.mutation.requestId;
    current = create(ResourceSchema, {
      ...current,
      revision: current.revision + 1n,
      documentJson: encode({
        ...document(current),
        subscription: {
          claude_credential: "server-token",
          server_operation: { id: operation },
          pending: { id: operation },
        },
      }),
    });
    return { account: current, operationId: operation };
  });
  let diagnostic:
    | ReturnType<typeof create<typeof NativeSubscriptionDiagnosticSchema>>
    | undefined;
  const progress = vi.fn(() => ({
    state,
    url: state === SubscriptionLoginState.WAITING ? progressURL : "",
    loginMethod: SubscriptionLoginMethod.BROWSER_CODE,
    generation,
    nativeDiagnostic: diagnostic,
  }));
  const submit = vi.fn(async (request) => {
    request.code.fill(0);
    return { accepted: true };
  });
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
  const list = vi.fn(() => ({ resources: [], nextPageToken: "" }));
  const transport = createRouterTransport((router) => {
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(ResourceService, {
      listResources: list,
      getResource: () => ({ resource: current }),
    });
    router.service(SubscriptionService, {
      requestSubscription: login,
      getSubscriptionProgress: progress,
      submitSubscriptionLoginCode: submit,
      cancelSubscription: cancel,
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  function Body() {
    const flow = useSubscriptionLogin(true, () => undefined);
    return (
      <>
        {flow.body ? (
          dialog ? (
            <SettingsTaskDialog
              title="Connect Claude account"
              size={SettingsDialogSize.Wide}
              retained={flow.retained}
              close={flow.hide}
            >
              {flow.body}
            </SettingsTaskDialog>
          ) : (
            flow.body
          )
        ) : flow.hidden ? (
          <button onClick={flow.show}>View original operation</button>
        ) : (
          <button onClick={() => flow.begin(SubscriptionServiceId.Claude)}>
            Add Claude
          </button>
        )}
      </>
    );
  }
  const view = () => (
    <StrictMode>
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <RunnerRemediationProvider active>
            <OAuthNativeProvider control={native}>
              <SettingsLifetime>{() => <Body />}</SettingsLifetime>
            </OAuthNativeProvider>
          </RunnerRemediationProvider>
        </QueryClientProvider>
      </TransportProvider>
    </StrictMode>
  );
  return {
    view,
    client,
    list,
    save,
    login,
    progress,
    submit,
    cancel,
    native,
    unsupported: (version: string) => {
      state = SubscriptionLoginState.UNSUPPORTED;
      diagnostic = create(NativeSubscriptionDiagnosticSchema, {
        detectedVersion: version,
        requiredVersion: "2.1.236",
        phase: 4,
        code: "unsupported",
        correlationId: newRequestId(),
      });
    },
    setURL: (value: string) => {
      progressURL = value;
    },
    current: () => current,
    success: (overrides: Record<string, unknown> = {}) => {
      state = SubscriptionLoginState.SUCCEEDED;
      current = create(ResourceSchema, {
        ...current,
        revision: current.revision + 1n,
        documentJson: encode({
          ...document(current),
          connection: { id: newRequestId() },
          subscription: {
            claude_credential: "server-token",
            generation,
            server_operation: { id: operation },
            ...overrides,
          },
        }),
      });
    },
  };
}
async function start(f: ReturnType<typeof fixture>) {
  render(f.view());
  const add = screen.getByRole("button", { name: "Add Claude" });
  fireEvent.click(add);
  fireEvent.click(add);
  await screen.findByLabelText("Approval code", {}, { timeout: 4000 });
}

it("starts one sign-in from a single click with no Runner Device and opens the original address", async () => {
  const f = fixture();
  await start(f);
  expect(screen.queryByRole("combobox", { name: "Runner Device" })).toBeNull();
  expect(screen.queryByText(/capability 20/)).toBeNull();
  expect(screen.queryByText("Runner Device")).toBeNull();
  expect(screen.getByText("Sign in")).toBeTruthy();
  expect(screen.getByText("Account name")).toBeTruthy();
  expect(f.list).not.toHaveBeenCalled();
  expect(f.save).toHaveBeenCalledTimes(1);
  expect(f.login).toHaveBeenCalledTimes(1);
  expect(f.login.mock.calls[0][0].machineId).toBe("");
  await waitFor(() =>
    expect(
      f.native.mock.calls.filter(
        (c) => c[1] === OAuthNativeAction.ClaudeSubscriptionOpen,
      ),
    ).toHaveLength(1),
  );
  expect(
    f.native.mock.calls.find(
      (c) => c[1] === OAuthNativeAction.ClaudeSubscriptionOpen,
    )?.[4],
  ).toBe(setupURL);
  expect(screen.getByText("Complete sign-in in your browser")).toBeTruthy();
  for (const name of ["Open browser again", "Back", "Cancel sign-in"])
    expect(screen.getByRole("button", { name })).toBeTruthy();
  // Polling, reopening and duplicate clicks never start another operation.
  fireEvent.click(screen.getByRole("button", { name: "Open browser again" }));
  await waitFor(() =>
    expect(
      f.native.mock.calls.filter(
        (c) => c[1] === OAuthNativeAction.ClaudeSubscriptionReopen,
      ),
    ).toHaveLength(1),
  );
  expect(f.save).toHaveBeenCalledTimes(1);
  expect(f.login).toHaveBeenCalledTimes(1);
  expect(
    JSON.stringify(
      f.client
        .getQueryCache()
        .getAll()
        .map((q) => q.state.data),
      (_key, value) => (typeof value === "bigint" ? value.toString() : value),
    ),
  ).not.toContain(setupURL);
});

it("submits the write-only approval code once and advances once to the focused name", async () => {
  const f = fixture();
  await start(f);
  const code = screen.getByLabelText("Approval code") as HTMLInputElement;
  expect(code.type).toBe("password");
  fireEvent.change(code, { target: { value: "fixture-approval#state" } });
  fireEvent.click(screen.getByRole("button", { name: "Submit code" }));
  await waitFor(() => expect(f.submit).toHaveBeenCalledTimes(1));
  expect(code.value).toBe("");
  expect(code.disabled).toBe(true);
  f.success();
  const name = await screen.findByLabelText(
    "Account name",
    {},
    { timeout: 4000 },
  );
  expect((name as HTMLInputElement).value).toBe("Claude");
  await waitFor(() => expect(window.document.activeElement).toBe(name));
  fireEvent.click(screen.getByRole("button", { name: "Later" }));
  expect(document(f.current()).connection).toBeTruthy();
  expect(f.submit).toHaveBeenCalledTimes(1);
  expect(f.login).toHaveBeenCalledTimes(1);
  expect(f.cancel).not.toHaveBeenCalled();
});

it("hides the task without canceling, clears the code, and only explicit Cancel waits for cleanup", async () => {
  const f = fixture(true);
  await start(f);
  const code = screen.getByLabelText("Approval code") as HTMLInputElement;
  fireEvent.change(code, { target: { value: "unsubmitted-fixture-code" } });
  fireEvent.click(
    screen.getByRole("button", { name: "Close Connect Claude account" }),
  );
  await screen.findByRole("button", { name: "View original operation" });
  expect(code.value).toBe("");
  expect(f.cancel).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "View original operation" }),
  );
  await screen.findByLabelText("Approval code");
  expect(f.login).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Cancel sign-in" }));
  await waitFor(() => expect(f.cancel).toHaveBeenCalledTimes(1), {
    timeout: 4000,
  });
  expect(
    await screen.findByText(
      "Cancellation requested. Waiting for original native cleanup.",
      {},
      { timeout: 4000 },
    ),
  ).toBeTruthy();
});

it("rejects a substituted authorization profile and never opens it", async () => {
  const f = fixture();
  f.setURL(
    setupURL.replace(
      "scope=user%3Ainference",
      "scope=user%3Ainference+user%3Aprofile",
    ),
  );
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Add Claude" }));
  await screen.findByText(
    "The original sign-in status could not be verified. No new sign-in will start.",
    {},
    { timeout: 4000 },
  );
  expect(
    f.native.mock.calls.filter(
      (c) => c[1] === OAuthNativeAction.ClaudeSubscriptionOpen,
    ),
  ).toHaveLength(0);
});

it("does not advance on success evidence from a device-owned or foreign operation", async () => {
  const f = fixture();
  await start(f);
  f.success({
    claude_credential: undefined,
    native_profile_id: newRequestId(),
  });
  await waitFor(
    () =>
      expect(
        screen.getByText(
          "The original sign-in status could not be verified. No new sign-in will start.",
        ),
      ).toBeTruthy(),
    { timeout: 4000 },
  );
  expect(screen.queryByLabelText("Account name")).toBeNull();
});

it("uses the setup-token profile separately from the auth-login profile", () => {
  expect(claudeSetupTokenURL(setupURL)).toBe(true);
  expect(claudeLoginURL(setupURL)).toBe(false);
  const login = new URL(setupURL);
  login.searchParams.set("scope", "user:profile user:inference");
  expect(claudeLoginURL(login.href)).toBe(true);
  expect(claudeSetupTokenURL(login.href)).toBe(false);
  const callback = new URL(setupURL);
  callback.searchParams.set("redirect_uri", "http://localhost:4000/callback");
  expect(claudeSetupTokenURL(callback.href)).toBe(false);
  const noCode = new URL(setupURL);
  noCode.searchParams.delete("code");
  expect(claudeSetupTokenURL(noCode.href)).toBe(false);
  expect(
    claudeSetupTokenURL(setupURL.replace("claude.com", "other.invalid")),
  ).toBe(false);
});

it("does not retransmit approval input after an uncertain result", async () => {
  const f = fixture();
  f.submit.mockImplementation(async (request) => {
    request.code.fill(0);
    throw new ConnectError("Fixture lost reply", Code.Unavailable);
  });
  await start(f);
  const input = screen.getByLabelText("Approval code") as HTMLInputElement;
  fireEvent.change(input, {
    target: { value: "fixture-approval#original-state" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Submit code" }));
  await waitFor(() => expect(f.submit).toHaveBeenCalledTimes(1));
  expect(input.value).toBe("");
  expect(input.disabled).toBe(true);
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 1100));
  });
  expect(f.submit).toHaveBeenCalledTimes(1);
  expect(
    screen.queryByRole("button", { name: "Retry original request" }),
  ).toBeNull();
  expect(
    JSON.stringify(
      f.client
        .getQueryCache()
        .getAll()
        .map((q) => q.state.data),
      (_key, value) => (typeof value === "bigint" ? value.toString() : value),
    ),
  ).not.toContain("fixture-approval");
});

it("shows only observed validation details for a newer prerelease Claude Code", async () => {
  const f = fixture();
  f.unsupported("2.1.237-rc.1");
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Add Claude" }));
  await screen.findByText("Claude Code validation did not complete.");
  expect(f.login).toHaveBeenCalledTimes(1);
  expect(screen.getByText("2.1.237-rc.1")).toBeTruthy();
  expect(screen.getByText("Native launch")).toBeTruthy();
  expect(screen.getByText("unsupported")).toBeTruthy();
  expect(screen.queryByText("Required version")).toBeNull();
  expect(screen.queryByText(/2\.1\.236/)).toBeNull();
  expect(screen.getByRole("button", { name: "Back" })).toBeTruthy();
});
