// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
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
import { afterEach, expect, it, vi } from "vitest";
import {
  ConfigurationService,
  EntityKind,
  ErrorDetailSchema,
  FailureCode,
  GrokDiagnosticPhase,
  GrokDiagnosticSchema,
  ResourceSchema,
  ResourceService,
  SubscriptionLoginState as State,
  SubscriptionService,
  SubscriptionServiceId,
  SystemCapability,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { OAuthNativeAction, OAuthNativeProvider } from "./account-oauth";
import { document, encode } from "./documents";
import { safeGrokDiagnostic } from "./grok-diagnostic";
import { GrokLifetimeTrigger, grokLifetimeCells } from "./grok-login-machine";
import {
  useGrokSubscriptionLogin,
  GrokLoginMethod,
  grokDeviceVerification,
  grokLoginURL,
} from "./grok-subscription-login";
import urlValidationFixtures from "./grok-url-validation-fixtures.json";
import { copy, i18n, SupportedLanguage } from "./localization";
import { SettingsLifetime } from "./settings-lifetime";
import { SubscriptionAccounts } from "./subscription-accounts";
import { useSubscriptionLogin } from "./subscription-login";
import { MutationIntents } from "./mutation";
import english from "./locales/en/grok-subscription.json";
import korean from "./locales/ko/grok-subscription.json";
import englishClaude from "./locales/en/claude-subscription.json";
import koreanClaude from "./locales/ko/claude-subscription.json";

const externalOpen = vi.hoisted(() => vi.fn(async (_url: string) => undefined));
vi.mock("./external-links", async (original) => ({
  ...(await original<typeof import("./external-links")>()),
  externalLinksAvailable: () => true,
  openExternalUrl: externalOpen,
}));

const DEVICE_CODE = "ABCD-1234";
const DEVICE_URL = `https://accounts.x.ai/oauth2/device?user_code=${DEVICE_CODE}`;
const CHALLENGE = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM";
function authorization(edit: (url: URL) => void = () => {}) {
  const url = new URL("https://auth.x.ai/oauth2/authorize");
  url.searchParams.set("response_type", "code");
  url.searchParams.set("client_id", "b1a00492-073a-47ea-816f-4c329264a828");
  url.searchParams.set("redirect_uri", "http://127.0.0.1:43817/callback");
  url.searchParams.set("scope", "openid profile email offline_access");
  url.searchParams.set("code_challenge", CHALLENGE);
  url.searchParams.set("code_challenge_method", "S256");
  url.searchParams.set("state", "fixture-grok-state_123");
  url.searchParams.set("nonce", "fixture-grok-nonce");
  url.searchParams.set("referrer", "grok-build");
  edit(url);
  return url.toString();
}
const loginURL = authorization();
function typedFailure(code: Code, failure: FailureCode) {
  return new ConnectError("Fixture request failure", code, undefined, [
    {
      desc: ErrorDetailSchema,
      value: create(ErrorDetailSchema, { code: failure }),
    },
  ]);
}

function refusal() {
  return create(GrokDiagnosticSchema, {
    detectedVersion: "1.0.43",
    supportedVersion: "1.0.46",
    phase: GrokDiagnosticPhase.VERSION,
    code: "unsupported",
    message: "Grok Build 1.0.43 did not complete version validation.",
    correlationId: newRequestId(),
  });
}

// The screen rebuilds the explanation from the closed phase and code, so the
// fixture's own message text never reaches it.
const refusalMessage = () => safeGrokDiagnostic(refusal())!.message;

function fixture(url = loginURL, productEntry = false) {
  let current = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.ACCOUNT,
    schemaVersion: 2,
    revision: 1n,
    documentJson: encode({
      alias: "Grok",
      type: "subscription",
      subscription_service: "grok",
    }),
  });
  let state = State.WAITING,
    userCode = "",
    canceled = false,
    diagnostic: ReturnType<typeof refusal> | undefined,
    operation = "",
    suggested = "grok.fixture@example.invalid",
    successOperation = "";
  const generation = newRequestId(),
    nativeGeneration = newRequestId();
  const save = vi.fn(async (request) => {
    current = create(ResourceSchema, {
      ...current,
      revision: current.revision + 1n,
      documentJson: request.documentJson,
    });
    return { requestId: request.mutation.requestId, resource: current };
  });
  const login = vi.fn((request) => {
    operation = request.mutation.requestId;
    successOperation = operation;
    current = create(ResourceSchema, {
      ...current,
      revision: current.revision + 1n,
      documentJson: encode({
        ...document(current),
        subscription: {
          server_operation: { id: operation },
          pending: { id: operation },
        },
      }),
    });
    return { account: current, operationId: operation };
  });
  const progress = vi.fn(
    (_request: {
      operationId: string;
    }): {
      state: State;
      url: string;
      userCode: string;
      suggestedName: string;
      generation: string;
      canceled: boolean;
      grokDiagnostic?: ReturnType<typeof refusal>;
    } => ({
      state,
      url: state === State.WAITING ? url : "",
      userCode: state === State.WAITING ? userCode : "",
      suggestedName: suggested,
      generation,
      canceled,
      grokDiagnostic: diagnostic,
    }),
  );
  const cancel = vi.fn((_request: { mutation?: { requestId: string } }) => ({
    account: current,
  }));
  const forward = vi.fn((_request: { operationId: string }) => ({
    accepted: true,
  }));
  const read = vi.fn(() => ({ resource: current }));
  const native = vi.fn(
    async (
      _scope: string,
      _action: OAuthNativeAction,
      _generation: string,
      _attempt: string,
      _url: string,
    ) =>
      ({ generation: nativeGeneration }) as {
        generation: string;
        code?: number[];
      },
  );
  const transport = createRouterTransport((router) => {
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(ResourceService, { getResource: read });
    router.service(SubscriptionService, {
      requestSubscription: login,
      getSubscriptionProgress: progress,
      cancelSubscription: cancel,
      forwardSubscriptionCallback: forward,
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const changed = vi.fn();
  let active = true;
  let settings: { dispose: (client: QueryClient) => void } | undefined;
  function Body() {
    const product = useSubscriptionLogin(active, changed);
    const controller = useGrokSubscriptionLogin(active, changed);
    const flow = productEntry
      ? product
      : {
          ...controller,
          begin: (
            _service: SubscriptionServiceId,
            _initial?: unknown,
            device = false,
          ) =>
            controller.begin(
              device ? GrokLoginMethod.DeviceCode : GrokLoginMethod.Browser,
            ),
        };
    return (
      <>
        {flow.body ?? (
          <>
            <button onClick={() => flow.begin(SubscriptionServiceId.Grok)}>
              Start Grok
            </button>
            <button
              onClick={() =>
                flow.begin(SubscriptionServiceId.Grok, undefined, true)
              }
            >
              Start Grok device
            </button>
          </>
        )}
      </>
    );
  }
  const view = (control: typeof native = native) => (
    <StrictMode>
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <OAuthNativeProvider control={control}>
            <SettingsLifetime>
              {(opening) => {
                settings = opening;
                return <Body />;
              }}
            </SettingsLifetime>
          </OAuthNativeProvider>
        </QueryClientProvider>
      </TransportProvider>
    </StrictMode>
  );
  return {
    view,
    save,
    login,
    progress,
    cancel,
    forward,
    read,
    native,
    client,
    changed,
    generation,
    setActive: (value: boolean) => {
      active = value;
    },
    closeSettings: () => settings?.dispose(client),
    current: () => current,
    setState: (value: State, grok?: ReturnType<typeof refusal>) => {
      state = value;
      diagnostic = grok;
    },
    setUserCode: (value: string) => {
      userCode = value;
    },
    // The server reports the login as canceled while it still says waiting.
    observeCanceled: (value = true) => {
      canceled = value;
    },
    suggestion: (name: string) => {
      suggested = name;
    },
    success: (overrides: { operation?: string; generation?: string } = {}) => {
      state = State.SUCCEEDED;
      current = create(ResourceSchema, {
        ...current,
        revision: current.revision + 1n,
        documentJson: encode({
          ...document(current),
          connection: { id: newRequestId() },
          subscription: {
            generation: overrides.generation ?? generation,
            server_operation: { id: overrides.operation ?? successOperation },
          },
        }),
      });
    },
  };
}
const actions = (f: ReturnType<typeof fixture>, action: OAuthNativeAction) =>
  f.native.mock.calls.filter((call) => call[1] === action);
async function start(f: ReturnType<typeof fixture>) {
  const rendered = render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
  await screen.findByRole(
    "button",
    { name: copy("grok-subscription.openBrowser") },
    { timeout: 3000 },
  );
  return rendered;
}
afterEach(async () => {
  externalOpen.mockClear();
  await i18n.changeLanguage(SupportedLanguage.English);
  vi.restoreAllMocks();
});

it("accepts only the fixed Grok authorization profile", () => {
  expect(grokLoginURL(loginURL)).toBe(true);
  expect(
    grokLoginURL(authorization((u) => u.searchParams.delete("scope"))),
  ).toBe(true);
  const rejected: Record<string, (url: URL) => void> = {
    host: (u) => {
      u.host = "auth.example.com";
    },
    path: (u) => {
      u.pathname = "/oauth2/token";
    },
    scheme: (u) => {
      u.protocol = "http:";
    },
    fragment: (u) => {
      u.hash = "x";
    },
    credentials: (u) => {
      u.username = "user";
    },
    client: (u) => u.searchParams.set("client_id", "other"),
    responseType: (u) => u.searchParams.set("response_type", "token"),
    method: (u) => u.searchParams.set("code_challenge_method", "plain"),
    challenge: (u) => u.searchParams.set("code_challenge", "short"),
    missingState: (u) => u.searchParams.delete("state"),
    badState: (u) => u.searchParams.set("state", "bad state!"),
    missingNonce: (u) => u.searchParams.delete("nonce"),
    redirectHost: (u) =>
      u.searchParams.set("redirect_uri", "http://localhost:43817/callback"),
    redirectPath: (u) =>
      u.searchParams.set(
        "redirect_uri",
        "http://127.0.0.1:43817/auth/callback",
      ),
    redirectPort: (u) =>
      u.searchParams.set("redirect_uri", "http://127.0.0.1:80/callback"),
    redirectLeadingZero: (u) =>
      u.searchParams.set("redirect_uri", "http://127.0.0.1:043817/callback"),
    redirectSecure: (u) =>
      u.searchParams.set("redirect_uri", "https://127.0.0.1:43817/callback"),
    referrer: (u) => u.searchParams.set("referrer", "other"),
    extra: (u) => u.searchParams.set("extra", "1"),
    duplicate: (u) => u.searchParams.append("state", "second-state-value"),
  };
  for (const [name, edit] of Object.entries(rejected))
    expect(grokLoginURL(authorization(edit)), name).toBe(false);
  expect(grokLoginURL("not a url")).toBe(false);
  expect(grokLoginURL(`${loginURL}${"a".repeat(5000)}`)).toBe(false);
  // The native receiver rejects any fragment delimiter, even an empty one.
  expect(grokLoginURL(`${loginURL}#`)).toBe(false);
  expect(grokLoginURL(`${loginURL}#fragment`)).toBe(false);
});

it("creates one account and one browser login despite double click, Strict Mode and polling", async () => {
  const f = fixture();
  render(f.view());
  const add = screen.getByRole("button", { name: "Start Grok" });
  fireEvent.click(add);
  fireEvent.click(add);
  await screen.findByRole(
    "button",
    { name: copy("grok-subscription.openBrowser") },
    { timeout: 3000 },
  );
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 2300));
  });
  expect(f.save).toHaveBeenCalledTimes(1);
  expect(
    JSON.parse(new TextDecoder().decode(f.save.mock.calls[0][0].documentJson)),
  ).toMatchObject({
    subscription_service: SubscriptionServiceId.Grok,
    type: "subscription",
    alias: "Grok",
  });
  expect(f.login).toHaveBeenCalledTimes(1);
  expect(f.login.mock.calls[0][0]).toMatchObject({
    action: 1,
    deviceCode: false,
    machineId: "",
  });
  expect(f.progress.mock.calls.length).toBeGreaterThan(1);
  expect(actions(f, OAuthNativeAction.GrokSubscriptionOpen)).toHaveLength(1);
  expect(actions(f, OAuthNativeAction.GrokSubscriptionOpen)[0][4]).toBe(
    loginURL,
  );
  expect(actions(f, OAuthNativeAction.SubscriptionOpen)).toHaveLength(0);
  expect(actions(f, OAuthNativeAction.Take).length).toBeGreaterThan(0);
  // The browser opened: the guidance may say so.
  expect(screen.getByText(copy("grok-subscription.help.waiting"))).toBeTruthy();
  expect(
    screen.queryByText(copy("grok-subscription.help.waitingLink")),
  ).toBeNull();
  // Progress holds the sign-in link: it never reaches the shared query cache.
  expect(
    f.client
      .getQueryCache()
      .getAll()
      .filter((query) =>
        JSON.stringify(query.queryKey).includes("SubscriptionService"),
      ),
  ).toHaveLength(0);
  expect(window.document.body.textContent).not.toContain("auth.x.ai");
  expect(window.document.body.textContent).not.toContain("fixture-grok-state");
});

it("reopens the original browser binding, copies the link and cancels through the server", async () => {
  const f = fixture();
  const writeText = vi.fn(async (_value: string) => undefined);
  Object.defineProperty(window.navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  await start(f);
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.openBrowser") }),
  );
  await waitFor(() =>
    expect(actions(f, OAuthNativeAction.GrokSubscriptionReopen)).toHaveLength(
      1,
    ),
  );
  const open = actions(f, OAuthNativeAction.GrokSubscriptionOpen)[0],
    reopen = actions(f, OAuthNativeAction.GrokSubscriptionReopen)[0];
  expect(reopen[0]).toBe(open[0]);
  expect(reopen[3]).toBe(open[3]);
  expect(reopen[4]).toBe(loginURL);
  expect(actions(f, OAuthNativeAction.GrokSubscriptionOpen)).toHaveLength(1);

  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.copyLink") }),
  );
  await screen.findByText(copy("grok-subscription.linkCopied"));
  expect(writeText).toHaveBeenCalledWith(loginURL);
  expect(window.document.body.textContent).not.toContain("auth.x.ai");

  f.setState(State.CANCELED);
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  await waitFor(() => expect(f.cancel).toHaveBeenCalledTimes(1));
  await screen.findByText(
    copy("grok-subscription.status.canceled"),
    {},
    {
      timeout: 3000,
    },
  );
  expect(actions(f, OAuthNativeAction.Dispose).length).toBeGreaterThan(0);
  expect(
    screen.queryByRole("button", { name: copy("grok-subscription.cancel") }),
  ).toBeNull();
});

it("shows a copy failure without exposing the link", async () => {
  const f = fixture();
  Object.defineProperty(window.navigator, "clipboard", {
    configurable: true,
    value: {
      writeText: vi.fn(async () => {
        throw new Error("blocked");
      }),
    },
  });
  await start(f);
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.copyLink") }),
  );
  await screen.findByText(copy("grok-subscription.copyFailed"));
  expect(window.document.body.textContent).not.toContain("auth.x.ai");
});

it("leaving disposes native authority without canceling the accepted operation", async () => {
  const f = fixture();
  const rendered = await start(f);
  rendered.unmount();
  await waitFor(() =>
    expect(actions(f, OAuthNativeAction.Dispose).length).toBeGreaterThan(0),
  );
  expect(f.cancel).not.toHaveBeenCalled();
  expect(f.login).toHaveBeenCalledTimes(1);
});

it("Back keeps the accepted operation and disposes the native binding", async () => {
  const f = fixture();
  await start(f);
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.back") }),
  );
  await screen.findByRole("button", { name: "Start Grok" });
  expect(actions(f, OAuthNativeAction.Dispose).length).toBeGreaterThan(0);
  expect(f.cancel).not.toHaveBeenCalled();
  expect(f.changed).toHaveBeenCalled();
});

// Controller lifetime cells (trigger x outcome). Every trigger disposes the
// native binding, leaves the accepted operation alone and removes the screen;
// only the user leaving tells the parent.
//
//   trigger            screen   native Dispose   cancel   parent notified
//   leave              cleared  yes              no       yes
//   inactive           cleared  yes              no       no
//   settings-closed    cleared  yes              no       no
//   authority-changed  cleared  yes              no       no
it.each([
  [GrokLifetimeTrigger.Leave, true],
  [GrokLifetimeTrigger.Inactive, false],
  [GrokLifetimeTrigger.SettingsClosed, false],
  [GrokLifetimeTrigger.AuthorityChanged, false],
] as const)(
  "clears the Grok view when the lifetime ends by %s",
  async (trigger, notified) => {
    expect(grokLifetimeCells[trigger].notifyParent).toBe(notified);
    const f = fixture();
    const other = vi.fn(f.native.getMockImplementation()!);
    const rendered = await start(f);
    const disposed = () => actions(f, OAuthNativeAction.Dispose).length;
    const before = disposed();
    f.changed.mockClear();
    if (trigger === GrokLifetimeTrigger.Leave)
      fireEvent.click(
        screen.getByRole("button", { name: copy("grok-subscription.back") }),
      );
    else if (trigger === GrokLifetimeTrigger.Inactive) {
      f.setActive(false);
      rendered.rerender(f.view());
    } else if (trigger === GrokLifetimeTrigger.SettingsClosed)
      act(() => f.closeSettings());
    else rendered.rerender(f.view(other));
    // The stale screen is gone: its actions never remain inert on screen.
    await screen.findByRole("button", { name: "Start Grok" });
    expect(
      screen.queryByRole("button", {
        name: copy("grok-subscription.openBrowser"),
      }),
    ).toBeNull();
    expect(disposed()).toBeGreaterThan(before);
    // The old control received the Dispose; the replacement was never used.
    expect(other).not.toHaveBeenCalled();
    expect(f.cancel).not.toHaveBeenCalled();
    expect(f.changed).toHaveBeenCalledTimes(notified ? 1 : 0);
  },
  20000,
);

it("starts a new login under a replaced native control after the authority changed", async () => {
  const f = fixture();
  const other = vi.fn(f.native.getMockImplementation()!);
  const rendered = await start(f);
  rendered.rerender(f.view(other));
  fireEvent.click(await screen.findByRole("button", { name: "Start Grok" }));
  await screen.findByRole(
    "button",
    { name: copy("grok-subscription.openBrowser") },
    { timeout: 3000 },
  );
  expect(actions(f, OAuthNativeAction.GrokSubscriptionOpen)).toHaveLength(1);
  expect(
    other.mock.calls.filter(
      (call) => call[1] === OAuthNativeAction.GrokSubscriptionOpen,
    ),
  ).toHaveLength(1);
}, 20000);

it("advances to naming only after verified success, prefilling and focusing the name", async () => {
  const f = fixture();
  await start(f);
  f.success();
  const input = await screen.findByLabelText(
    copy("grok-subscription.accountName"),
    {},
    { timeout: 3000 },
  );
  expect((input as HTMLInputElement).value).toBe(
    "grok.fixture@example.invalid",
  );
  await waitFor(() => expect(window.document.activeElement).toBe(input));
  expect(
    screen
      .getByText(copy("grok-subscription.status.success"))
      .closest("[role=status]"),
  ).toBeTruthy();
  expect(actions(f, OAuthNativeAction.Dispose).length).toBeGreaterThan(0);
  fireEvent.change(input, { target: { value: "Edited Grok" } });
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.saveName") }),
  );
  await screen.findByRole("button", { name: "Start Grok" });
  expect(document(f.current())).toMatchObject({ alias: "Edited Grok" });
  expect(f.save).toHaveBeenCalledTimes(2);
  expect(f.login).toHaveBeenCalledTimes(1);
  expect(f.cancel).not.toHaveBeenCalled();
});

// Naming cells: the form is editable only while no uncertain save is retained.
//
//   save outcome        name input   Save account name   Retry original request
//   none yet            enabled      enabled             hidden
//   in flight           disabled     disabled            hidden
//   uncertain (kept)    disabled     disabled            enabled; replays the same request
//   definite refusal    enabled      enabled             hidden
it("locks the name to the retained request after an uncertain save and replays it unchanged", async () => {
  const f = fixture();
  await start(f);
  f.success();
  const input = (await screen.findByLabelText(
    copy("grok-subscription.accountName"),
    {},
    { timeout: 3000 },
  )) as HTMLInputElement;
  const saveButton = () =>
    screen.getByRole("button", {
      name: copy("grok-subscription.saveName"),
    }) as HTMLButtonElement;
  fireEvent.change(input, { target: { value: "Retained Grok" } });
  f.save.mockImplementationOnce(() => {
    throw typedFailure(Code.Internal, FailureCode.Internal);
  });
  fireEvent.click(saveButton());
  const retry = await screen.findByRole("button", {
    name: copy("grok-subscription.retryOriginal"),
  });
  expect(f.save).toHaveBeenCalledTimes(2);
  expect(input.disabled).toBe(true);
  expect(saveButton().disabled).toBe(true);
  // Neither another submission nor an edit can start a different request.
  fireEvent.submit(input.closest("form")!);
  fireEvent.change(input, { target: { value: "Different Grok" } });
  expect(f.save).toHaveBeenCalledTimes(2);
  expect(input.value).toBe("Retained Grok");
  fireEvent.click(retry);
  await screen.findByRole("button", { name: "Start Grok" });
  expect(f.save).toHaveBeenCalledTimes(3);
  expect(f.save.mock.calls[2][0].mutation.requestId).toBe(
    f.save.mock.calls[1][0].mutation.requestId,
  );
  expect(document(f.current())).toMatchObject({ alias: "Retained Grok" });
});

it("keeps the name editable after a definite naming refusal", async () => {
  const f = fixture();
  await start(f);
  f.success();
  const input = (await screen.findByLabelText(
    copy("grok-subscription.accountName"),
    {},
    { timeout: 3000 },
  )) as HTMLInputElement;
  f.save.mockImplementationOnce(() => {
    throw typedFailure(Code.PermissionDenied, FailureCode.PermissionDenied);
  });
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.saveName") }),
  );
  await screen.findByText(copy("grok-subscription.problem.nameUnconfirmed"));
  expect(input.disabled).toBe(false);
  expect(
    (
      screen.getByRole("button", {
        name: copy("grok-subscription.saveName"),
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(false);
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeNull();
});

it("falls back to the default name when no valid suggestion exists", async () => {
  const f = fixture();
  f.suggestion("");
  await start(f);
  f.success();
  const input = await screen.findByLabelText(
    copy("grok-subscription.accountName"),
    {},
    { timeout: 3000 },
  );
  expect((input as HTMLInputElement).value).toBe("Grok");
  expect(screen.getByText(copy("grok-subscription.nameChoose"))).toBeTruthy();
});

it.each([
  ["operation", { operation: newRequestId() }],
  ["generation", { generation: newRequestId() }],
])(
  "does not name an account whose %s cannot be verified",
  async (_name, change) => {
    const f = fixture();
    await start(f);
    f.success(change);
    await screen.findByText(
      copy("grok-subscription.problem.statusUnconfirmed"),
      {},
      { timeout: 3000 },
    );
    expect(
      screen.queryByLabelText(copy("grok-subscription.accountName")),
    ).toBeNull();
    expect(f.save).toHaveBeenCalledTimes(1);
  },
);

it.each([
  [State.CANCELED, "canceled", "canceled"],
  [State.EXPIRED, "expired", "expired"],
  [State.FAILED, "failed", "failed"],
  [State.RECOVERY_REQUIRED, "recovery", "recovery"],
  [State.UNSUPPORTED, "unsupported", "unsupported"],
] as const)(
  "renders the distinct %s state with its own status and guidance",
  async (state, status, help) => {
    const f = fixture();
    await start(f);
    f.setState(state);
    await screen.findByText(
      copy(`grok-subscription.status.${status}` as never),
      {},
      { timeout: 3000 },
    );
    expect(
      screen.getByText(copy(`grok-subscription.help.${help}` as never)),
    ).toBeTruthy();
    expect(
      screen.queryByRole("button", {
        name: copy("grok-subscription.openBrowser"),
      }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", {
        name: copy("grok-subscription.copyLink"),
      }),
    ).toBeNull();
    expect(actions(f, OAuthNativeAction.Dispose).length).toBeGreaterThan(0);
    // A terminal state never starts a replacement login.
    expect(f.login).toHaveBeenCalledTimes(1);
    expect(f.save).toHaveBeenCalledTimes(1);
  },
);

it("has distinct English and Korean text for every state", async () => {
  for (const catalog of [english, korean]) {
    const values = Object.entries(catalog)
      .filter(([key]) => key.startsWith("grok-subscription.status."))
      .map(([, value]) => value);
    expect(values).toHaveLength(8);
    expect(new Set(values).size).toBe(8);
  }
  const f = fixture();
  await i18n.changeLanguage(SupportedLanguage.Korean);
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
  await screen.findByRole(
    "button",
    { name: korean["grok-subscription.openBrowser"] },
    { timeout: 3000 },
  );
  expect(screen.getByRole("status").textContent).toContain(
    korean["grok-subscription.status.waiting"],
  );
});

it("refuses a malformed original authorization without opening a browser", async () => {
  const f = fixture(authorization((u) => u.searchParams.set("client_id", "x")));
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
  await screen.findByText(
    copy("grok-subscription.problem.statusUnconfirmed"),
    {},
    { timeout: 3000 },
  );
  expect(actions(f, OAuthNativeAction.GrokSubscriptionOpen)).toHaveLength(0);
  expect(
    screen.queryByRole("button", { name: copy("grok-subscription.copyLink") }),
  ).toBeNull();
  // Nothing opened and no link is offered, so no waiting guidance claims either.
  expect(screen.queryByText(copy("grok-subscription.help.waiting"))).toBeNull();
  expect(
    screen.queryByText(copy("grok-subscription.help.waitingLink")),
  ).toBeNull();
});

it("presents the receiver's Incompatible refusal for remote desktops and keeps the link copyable", async () => {
  const f = fixture();
  f.native.mockImplementation(async (_scope, action) => {
    if (action === OAuthNativeAction.GrokSubscriptionOpen) throw "incompatible";
    return { generation: "" };
  });
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
  await screen.findByText(
    copy("grok-subscription.problem.remoteUnsupported"),
    {},
    { timeout: 3000 },
  );
  expect(
    screen.getByRole("button", { name: copy("grok-subscription.copyLink") }),
  ).toBeTruthy();
  expect(actions(f, OAuthNativeAction.GrokSubscriptionOpen)).toHaveLength(1);
  // The closed refusal leaves no native binding, so reopening is never offered,
  // not even after later polls observe the same waiting login. The guidance
  // does not claim a browser opened; it points at the copyable link.
  expect(
    screen.getByText(copy("grok-subscription.help.waitingLink")),
  ).toBeTruthy();
  expect(screen.queryByText(copy("grok-subscription.help.waiting"))).toBeNull();
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.openBrowser"),
    }),
  ).toBeNull();
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 1300));
  });
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.openBrowser"),
    }),
  ).toBeNull();
  expect(
    screen.getByRole("button", { name: copy("grok-subscription.copyLink") }),
  ).toBeTruthy();
  expect(actions(f, OAuthNativeAction.GrokSubscriptionOpen)).toHaveLength(1);
  expect(actions(f, OAuthNativeAction.GrokSubscriptionReopen)).toHaveLength(0);
  expect(actions(f, OAuthNativeAction.Take)).toHaveLength(0);
});

it("stops offering Open browser again when a reopen meets the Incompatible refusal", async () => {
  const f = fixture();
  f.native.mockImplementation(async (_scope, action) => {
    if (action === OAuthNativeAction.GrokSubscriptionOpen)
      throw "browser-launch-failed";
    if (action === OAuthNativeAction.GrokSubscriptionReopen)
      throw "incompatible";
    return { generation: "" };
  });
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
  await screen.findByText(
    copy("grok-subscription.problem.browserFailed"),
    {},
    { timeout: 3000 },
  );
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.openBrowser") }),
  );
  await screen.findByText(copy("grok-subscription.problem.remoteUnsupported"));
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.openBrowser"),
    }),
  ).toBeNull();
  expect(
    screen.getByRole("button", { name: copy("grok-subscription.copyLink") }),
  ).toBeTruthy();
  expect(actions(f, OAuthNativeAction.GrokSubscriptionReopen)).toHaveLength(1);
});

it("sends one cancel mutation, keeps polling and hides every login action once the server acknowledged it", async () => {
  const f = fixture();
  await start(f);
  const progressCalls = f.progress.mock.calls.length;
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  await screen.findByText(copy("grok-subscription.problem.canceling"));
  expect(f.cancel).toHaveBeenCalledTimes(1);
  // Nothing is offered once the cancellation was acknowledged, the link included.
  const hidden = () => {
    for (const name of [
      "grok-subscription.cancel",
      "grok-subscription.openBrowser",
      "grok-subscription.copyLink",
      "grok-subscription.retryOriginal",
    ] as const)
      expect(
        screen.queryByRole("button", { name: copy(name) }),
        name,
      ).toBeNull();
  };
  hidden();
  // The server still reports the login as waiting: polling continues but a
  // poll never re-offers Cancel.
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 2300));
  });
  expect(f.progress.mock.calls.length).toBeGreaterThan(progressCalls);
  hidden();
  expect(f.cancel).toHaveBeenCalledTimes(1);
  // The terminal state replaces the notice and still offers nothing.
  f.setState(State.CANCELED);
  await screen.findByText(
    copy("grok-subscription.status.canceled"),
    {},
    { timeout: 3000 },
  );
  hidden();
  expect(f.cancel).toHaveBeenCalledTimes(1);
  expect(f.login).toHaveBeenCalledTimes(1);
});

it("never opens a browser or takes a callback for a login whose cancellation was acknowledged first", async () => {
  const f = fixture();
  f.setState(State.PREPARING);
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
  fireEvent.click(
    await screen.findByRole(
      "button",
      { name: copy("grok-subscription.cancel") },
      { timeout: 3000 },
    ),
  );
  await screen.findByText(copy("grok-subscription.problem.canceling"));
  // The server now reports the login as waiting before it reports cancellation.
  f.setState(State.WAITING);
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 2300));
  });
  expect(actions(f, OAuthNativeAction.GrokSubscriptionOpen)).toHaveLength(0);
  expect(actions(f, OAuthNativeAction.Take)).toHaveLength(0);
  expect(f.cancel).toHaveBeenCalledTimes(1);
  for (const name of [
    "grok-subscription.cancel",
    "grok-subscription.openBrowser",
  ] as const)
    expect(screen.queryByRole("button", { name: copy(name) }), name).toBeNull();
});

// Native completion after an acknowledged cancellation. The acknowledgement
// disposes the native binding at once; a native step that was already pending
// is discarded when it settles.
//
//   pending native step   settles with   result
//   GrokSubscriptionOpen  generation     discarded: no Take, notice kept
//   GrokSubscriptionOpen  failure        discarded: notice kept
//   Take                  callback       zeroed, never forwarded
//   Take                  failure        discarded: notice kept
it.each([
  ["a generation", "resolve"],
  ["a failure", "reject"],
] as const)(
  "discards a pending native open that settles with %s after the cancellation was acknowledged",
  async (_name, outcome) => {
    const f = fixture();
    let settle: () => void = () => undefined;
    f.native.mockImplementation(async (_scope, action, generation) => {
      if (action !== OAuthNativeAction.GrokSubscriptionOpen)
        return { generation: generation || newRequestId() };
      return new Promise((resolve, reject) => {
        settle = () =>
          outcome === "resolve"
            ? resolve({ generation: newRequestId() })
            : reject("browser-launch-failed");
      });
    });
    render(f.view());
    fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
    await waitFor(
      () =>
        expect(actions(f, OAuthNativeAction.GrokSubscriptionOpen)).toHaveLength(
          1,
        ),
      { timeout: 3000 },
    );
    fireEvent.click(
      await screen.findByRole("button", {
        name: copy("grok-subscription.cancel"),
      }),
    );
    await screen.findByText(copy("grok-subscription.problem.canceling"));
    // The binding is retired at the acknowledgement, before the open settles.
    expect(actions(f, OAuthNativeAction.Dispose).length).toBeGreaterThan(0);
    await act(async () => {
      settle();
      await new Promise((resolve) => setTimeout(resolve, 1300));
    });
    expect(actions(f, OAuthNativeAction.Take)).toHaveLength(0);
    expect(f.forward).not.toHaveBeenCalled();
    expect(
      screen.getByText(copy("grok-subscription.problem.canceling")),
    ).toBeTruthy();
    expect(
      screen.queryByText(copy("grok-subscription.problem.browserFailed")),
    ).toBeNull();
    expect(
      screen.queryByRole("button", {
        name: copy("grok-subscription.openBrowser"),
      }),
    ).toBeNull();
    expect(f.cancel).toHaveBeenCalledTimes(1);
  },
  20000,
);

it.each([
  ["a callback", "callback"],
  ["a failure", "failure"],
] as const)(
  "discards a pending native take that settles with %s after the cancellation was acknowledged",
  async (_name, outcome) => {
    const f = fixture();
    const code = [99, 111, 100, 101];
    let settle: () => void = () => undefined;
    f.native.mockImplementation(async (_scope, action, generation) => {
      if (action !== OAuthNativeAction.Take)
        return { generation: generation || newRequestId() };
      return new Promise((resolve, reject) => {
        settle = () =>
          outcome === "callback"
            ? resolve({ generation, code })
            : reject("take-failed");
      });
    });
    render(f.view());
    fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
    await waitFor(
      () => expect(actions(f, OAuthNativeAction.Take)).toHaveLength(1),
      { timeout: 3000 },
    );
    fireEvent.click(
      await screen.findByRole("button", {
        name: copy("grok-subscription.cancel"),
      }),
    );
    await screen.findByText(copy("grok-subscription.problem.canceling"));
    await act(async () => {
      settle();
      await new Promise((resolve) => setTimeout(resolve, 1300));
    });
    // The callback is zeroed and never forwarded to the server.
    if (outcome === "callback") expect(code).toEqual([0, 0, 0, 0]);
    expect(f.forward).not.toHaveBeenCalled();
    expect(
      screen.getByText(copy("grok-subscription.problem.canceling")),
    ).toBeTruthy();
    expect(
      screen.queryByText(copy("grok-subscription.problem.callbackUncertain")),
    ).toBeNull();
  },
  20000,
);

it("replays an uncertain cancel only through Retry original request and never as a second cancel", async () => {
  const f = fixture();
  await start(f);
  f.cancel.mockImplementationOnce(() => {
    throw typedFailure(Code.Internal, FailureCode.Internal);
  });
  const cancel = screen.getByRole("button", {
    name: copy("grok-subscription.cancel"),
  });
  fireEvent.click(cancel);
  const retry = await screen.findByRole("button", {
    name: copy("grok-subscription.retryOriginal"),
  });
  expect(
    screen.getByText(copy("grok-subscription.problem.cancelUnconfirmed")),
  ).toBeTruthy();
  // Cancel stays visible but disabled while the original request is unresolved.
  expect(
    (
      screen.getByRole("button", {
        name: copy("grok-subscription.cancel"),
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  expect(f.cancel).toHaveBeenCalledTimes(1);
  fireEvent.click(retry);
  await screen.findByText(copy("grok-subscription.problem.canceling"));
  expect(f.cancel).toHaveBeenCalledTimes(2);
  expect(f.cancel.mock.calls[1][0].mutation?.requestId).toBe(
    f.cancel.mock.calls[0][0].mutation?.requestId,
  );
  expect(
    screen.queryByRole("button", { name: copy("grok-subscription.cancel") }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeNull();
});

it("shows a generic browser failure and recovers through Open browser again", async () => {
  const f = fixture();
  f.native.mockImplementationOnce(async () => {
    throw "browser-launch-failed";
  });
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
  await screen.findByText(
    copy("grok-subscription.problem.browserFailed"),
    {},
    { timeout: 3000 },
  );
  // The open failed: the guidance points at the link instead of claiming it opened.
  expect(
    screen.getByText(copy("grok-subscription.help.waitingLink")),
  ).toBeTruthy();
  expect(screen.queryByText(copy("grok-subscription.help.waiting"))).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.openBrowser") }),
  );
  await waitFor(() =>
    expect(actions(f, OAuthNativeAction.GrokSubscriptionReopen)).toHaveLength(
      1,
    ),
  );
  expect(actions(f, OAuthNativeAction.GrokSubscriptionOpen)).toHaveLength(1);
  // A successful reopen is the first time the browser opened.
  await screen.findByText(copy("grok-subscription.help.waiting"));
  expect(
    screen.queryByText(copy("grok-subscription.help.waitingLink")),
  ).toBeNull();
});

it("forwards one native callback and never sends it again", async () => {
  const f = fixture();
  let delivered = false;
  f.native.mockImplementation(async (_scope, action, generation) => {
    if (action === OAuthNativeAction.Take && !delivered) {
      delivered = true;
      return { generation, code: [99, 111, 100, 101] };
    }
    return { generation: generation || "019a0000-0000-7000-8000-000000000001" };
  });
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
  await waitFor(() => expect(f.forward).toHaveBeenCalledTimes(1), {
    timeout: 3000,
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 1300));
  });
  expect(f.forward).toHaveBeenCalledTimes(1);
  expect(f.forward.mock.calls[0][0]).toMatchObject({
    operationId: f.login.mock.calls[0][0].mutation.requestId,
  });
});

it("replays the exact original request after an uncertain failure without a second account", async () => {
  const f = fixture(),
    admit = f.login.getMockImplementation()!;
  let receipt: ReturnType<typeof admit>;
  f.login.mockImplementationOnce((request) => {
    receipt = admit(request);
    throw typedFailure(Code.Internal, FailureCode.Internal);
  });
  f.login.mockImplementationOnce(() => receipt);
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
  const retry = await screen.findByRole("button", {
    name: copy("grok-subscription.retryOriginal"),
  });
  fireEvent.click(retry);
  await screen.findByRole(
    "button",
    { name: copy("grok-subscription.openBrowser") },
    { timeout: 3000 },
  );
  expect(f.save).toHaveBeenCalledTimes(1);
  expect(f.login).toHaveBeenCalledTimes(2);
  expect(f.login.mock.calls[1][0].mutation.requestId).toBe(
    f.login.mock.calls[0][0].mutation.requestId,
  );
});

it("ends a definitively refused request without offering a replacement login", async () => {
  const f = fixture();
  f.login.mockImplementationOnce(() => {
    throw typedFailure(Code.PermissionDenied, FailureCode.PermissionDenied);
  });
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
  await screen.findByText(copy("grok-subscription.status.failed"));
  // A definite refusal retains no request, so it never tells the user to retry
  // one and names the refusal instead.
  expect(
    screen.getByText(copy("grok-subscription.problem.requestFailed")),
  ).toBeTruthy();
  expect(
    screen.queryByText(copy("grok-subscription.problem.requestUnconfirmed")),
  ).toBeNull();
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeNull();
  expect(f.login).toHaveBeenCalledTimes(1);
  expect(f.progress).not.toHaveBeenCalled();
});

it("rejects a created account whose alias differs from the requested name before any login starts", async () => {
  const f = fixture();
  f.save.mockImplementationOnce(async (request) => ({
    requestId: request.mutation.requestId,
    resource: create(ResourceSchema, {
      ...f.current(),
      documentJson: encode({ ...document(f.current()), alias: "Renamed" }),
    }),
  }));
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
  // The receipt is rejected locally. Replaying the same request would return
  // the same receipt, so the attempt ends with the definite-failure notice and
  // retains nothing: no Retry button, no login request and no adoption.
  await screen.findByText(copy("grok-subscription.problem.requestFailed"));
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeNull();
  expect(
    screen.queryByText(copy("grok-subscription.problem.requestUnconfirmed")),
  ).toBeNull();
  expect(f.save).toHaveBeenCalledTimes(1);
  expect(f.login).not.toHaveBeenCalled();
  expect(f.progress).not.toHaveBeenCalled();
  expect(f.changed).not.toHaveBeenCalled();
});

it("rejects a login receipt for another operation as a definite failure without a retained request", async () => {
  const f = fixture(),
    admit = f.login.getMockImplementation()!;
  f.login.mockImplementationOnce((request) => ({
    ...admit(request),
    operationId: newRequestId(),
  }));
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
  await screen.findByText(copy("grok-subscription.problem.requestFailed"));
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeNull();
  expect(
    screen.queryByText(copy("grok-subscription.problem.requestUnconfirmed")),
  ).toBeNull();
  // Nothing replays the rejected receipt and no operation is polled.
  expect(f.save).toHaveBeenCalledTimes(1);
  expect(f.login).toHaveBeenCalledTimes(1);
  expect(f.progress).not.toHaveBeenCalled();
});

it("rejects a cancellation receipt for another account as a definite failure and keeps Cancel available", async () => {
  const f = fixture();
  await start(f);
  f.cancel.mockImplementationOnce(() => ({
    account: create(ResourceSchema, {
      ...f.current(),
      id: newRequestId(),
    }),
  }));
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  await screen.findByText(copy("grok-subscription.problem.cancelFailed"));
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeNull();
  expect(
    screen.queryByText(copy("grok-subscription.problem.cancelUnconfirmed")),
  ).toBeNull();
  // The rejected receipt is not replayed; a deliberate Cancel is a new request.
  const again = screen.getByRole("button", {
    name: copy("grok-subscription.cancel"),
  }) as HTMLButtonElement;
  expect(again.disabled).toBe(false);
  expect(f.cancel).toHaveBeenCalledTimes(1);
  fireEvent.click(again);
  await screen.findByText(copy("grok-subscription.problem.canceling"));
  expect(f.cancel).toHaveBeenCalledTimes(2);
  expect(f.cancel.mock.calls[1][0].mutation?.requestId).not.toBe(
    f.cancel.mock.calls[0][0].mutation?.requestId,
  );
});

it("rejects a saved name whose receipt carries another alias as a definite failure and keeps the name editable", async () => {
  const f = fixture();
  await start(f);
  f.success();
  const input = (await screen.findByLabelText(
    copy("grok-subscription.accountName"),
    {},
    { timeout: 3000 },
  )) as HTMLInputElement;
  f.save.mockImplementationOnce(async (request) => ({
    requestId: request.mutation.requestId,
    resource: create(ResourceSchema, {
      ...f.current(),
      revision: f.current().revision + 1n,
      documentJson: encode({ ...document(f.current()), alias: "Other" }),
    }),
  }));
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.saveName") }),
  );
  await screen.findByText(copy("grok-subscription.problem.nameUnconfirmed"));
  expect(input.disabled).toBe(false);
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeNull();
  expect(f.save).toHaveBeenCalledTimes(2);
});

it("replaces the unconfirmed request notice when an uncertain start replay is refused definitively", async () => {
  const f = fixture();
  f.login.mockImplementationOnce(() => {
    throw typedFailure(Code.Internal, FailureCode.Internal);
  });
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
  const retry = await screen.findByRole("button", {
    name: copy("grok-subscription.retryOriginal"),
  });
  expect(
    screen.getByText(copy("grok-subscription.problem.requestUnconfirmed")),
  ).toBeTruthy();
  // The replay is refused definitively: the retained request is gone, the
  // unconfirmed notice with it.
  f.login.mockImplementationOnce(() => {
    throw typedFailure(Code.PermissionDenied, FailureCode.PermissionDenied);
  });
  fireEvent.click(retry);
  await screen.findByText(copy("grok-subscription.problem.requestFailed"));
  expect(
    screen.queryByText(copy("grok-subscription.problem.requestUnconfirmed")),
  ).toBeNull();
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeNull();
  expect(f.save).toHaveBeenCalledTimes(1);
});

it("treats an observed cancellation like an acknowledged one and retires every login action", async () => {
  const f = fixture();
  await start(f);
  expect(actions(f, OAuthNativeAction.Dispose)).toHaveLength(0);
  // Another client (or a lost cancel response) canceled the login while the
  // server still reports waiting.
  f.observeCanceled();
  await screen.findByText(
    copy("grok-subscription.problem.canceling"),
    {},
    { timeout: 3000 },
  );
  for (const name of [
    "grok-subscription.cancel",
    "grok-subscription.openBrowser",
    "grok-subscription.copyLink",
    "grok-subscription.retryOriginal",
  ] as const)
    expect(screen.queryByRole("button", { name: copy(name) }), name).toBeNull();
  // The native binding is disposed once and nothing is opened or taken again.
  await waitFor(() =>
    expect(actions(f, OAuthNativeAction.Dispose).length).toBeGreaterThan(0),
  );
  const disposed = actions(f, OAuthNativeAction.Dispose).length;
  const taken = actions(f, OAuthNativeAction.Take).length;
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 2300));
  });
  expect(actions(f, OAuthNativeAction.Dispose)).toHaveLength(disposed);
  expect(actions(f, OAuthNativeAction.Take)).toHaveLength(taken);
  expect(actions(f, OAuthNativeAction.GrokSubscriptionOpen)).toHaveLength(1);
  expect(f.cancel).not.toHaveBeenCalled();
  f.setState(State.CANCELED);
  await screen.findByText(
    copy("grok-subscription.status.canceled"),
    {},
    { timeout: 3000 },
  );
}, 20000);

it("keeps the cancellation problem across a repeated uncertain replay and names a definite refusal", async () => {
  const f = fixture();
  await start(f);
  f.cancel.mockImplementationOnce(() => {
    throw typedFailure(Code.Internal, FailureCode.Internal);
  });
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  const retry = () =>
    screen.findByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    });
  await retry();
  expect(
    screen.getByText(copy("grok-subscription.problem.cancelUnconfirmed")),
  ).toBeTruthy();
  // The replay is uncertain again: the retained request keeps its own notice.
  f.cancel.mockImplementationOnce(() => {
    throw typedFailure(Code.Internal, FailureCode.Internal);
  });
  fireEvent.click(await retry());
  await waitFor(() => expect(f.cancel).toHaveBeenCalledTimes(2));
  await retry();
  expect(
    screen.getByText(copy("grok-subscription.problem.cancelUnconfirmed")),
  ).toBeTruthy();
  expect(
    screen.queryByText(copy("grok-subscription.problem.requestUnconfirmed")),
  ).toBeNull();
  // A status failure never hides the retained request's notice.
  f.progress.mockImplementationOnce(() => {
    throw new Error("status unavailable");
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 1300));
  });
  expect(
    screen.getByText(copy("grok-subscription.problem.cancelUnconfirmed")),
  ).toBeTruthy();
  // A definite refusal on the third replay ends the retained request and offers
  // a fresh Cancel instead of an unconfirmed notice.
  f.cancel.mockImplementationOnce(() => {
    throw typedFailure(Code.PermissionDenied, FailureCode.PermissionDenied);
  });
  fireEvent.click(await retry());
  await screen.findByText(copy("grok-subscription.problem.cancelFailed"));
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeNull();
  expect(
    (
      screen.getByRole("button", {
        name: copy("grok-subscription.cancel"),
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(false);
}, 20000);

it("keeps the naming problem across a repeated uncertain save replay", async () => {
  const f = fixture();
  await start(f);
  f.success();
  const input = (await screen.findByLabelText(
    copy("grok-subscription.accountName"),
    {},
    { timeout: 3000 },
  )) as HTMLInputElement;
  fireEvent.change(input, { target: { value: "Retained Grok" } });
  f.save.mockImplementationOnce(() => {
    throw typedFailure(Code.Internal, FailureCode.Internal);
  });
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.saveName") }),
  );
  const retry = () =>
    screen.findByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    });
  await retry();
  f.save.mockImplementationOnce(() => {
    throw typedFailure(Code.Internal, FailureCode.Internal);
  });
  fireEvent.click(await retry());
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(3));
  await retry();
  expect(
    screen.getByText(copy("grok-subscription.problem.nameUnconfirmed")),
  ).toBeTruthy();
  expect(
    screen.queryByText(copy("grok-subscription.problem.requestUnconfirmed")),
  ).toBeNull();
  expect(input.disabled).toBe(true);
});

it("drops a retained cancel request once the login succeeded so the name can be saved", async () => {
  const f = fixture();
  await start(f);
  f.cancel.mockImplementationOnce(() => {
    throw typedFailure(Code.Internal, FailureCode.Internal);
  });
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  await screen.findByRole("button", {
    name: copy("grok-subscription.retryOriginal"),
  });
  f.success();
  const input = (await screen.findByLabelText(
    copy("grok-subscription.accountName"),
    {},
    { timeout: 3000 },
  )) as HTMLInputElement;
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeNull();
  expect(
    screen.queryByText(copy("grok-subscription.problem.cancelUnconfirmed")),
  ).toBeNull();
  expect(input.disabled).toBe(false);
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.saveName") }),
  );
  await screen.findByRole("button", { name: "Start Grok" });
  expect(f.save).toHaveBeenCalledTimes(2);
});

const stops = async (f: ReturnType<typeof fixture>) => {
  const before = f.progress.mock.calls.length;
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 2200));
  });
  expect(f.progress.mock.calls.length).toBe(before);
};
it("keeps polling a provisional refusal, keeps Cancel and adopts the same operation's success", async () => {
  const f = fixture();
  await start(f);
  const operation = f.login.mock.calls[0][0].mutation.requestId;
  f.setState(State.UNSUPPORTED, refusal());
  await screen.findByText(refusalMessage(), {}, { timeout: 3000 });
  // Nothing about the sign-in is disposed or ended by the refusal.
  expect(actions(f, OAuthNativeAction.Dispose)).toHaveLength(0);
  expect(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  ).toBeTruthy();
  const refused = f.progress.mock.calls.length;
  await waitFor(
    () => expect(f.progress.mock.calls.length).toBeGreaterThan(refused + 1),
    { timeout: 4000 },
  );
  f.setState(State.PREPARING);
  await waitFor(() => expect(screen.queryByText(refusalMessage())).toBeNull(), {
    timeout: 3000,
  });
  f.success();
  const input = await screen.findByLabelText(
    copy("grok-subscription.accountName"),
    {},
    { timeout: 3000 },
  );
  expect((input as HTMLInputElement).value).toBe(
    "grok.fixture@example.invalid",
  );
  expect(screen.queryByText(refusalMessage())).toBeNull();
  for (const [request] of f.progress.mock.calls)
    expect(request).toMatchObject({ operationId: operation });
  expect(f.login).toHaveBeenCalledTimes(1);
  expect(f.cancel).not.toHaveBeenCalled();
  expect(f.forward).not.toHaveBeenCalled();
}, 20000);
it("settles a provisional refusal into a terminal failure with its diagnostic and stops polling", async () => {
  const f = fixture();
  await start(f);
  f.setState(State.UNSUPPORTED, refusal());
  await screen.findByText(refusalMessage(), {}, { timeout: 3000 });
  f.setState(State.FAILED, refusal());
  await screen.findByText(
    copy("grok-subscription.status.failed"),
    {},
    { timeout: 3000 },
  );
  expect(screen.getByText(refusalMessage())).toBeTruthy();
  await waitFor(
    () => expect(actions(f, OAuthNativeAction.Dispose)).toHaveLength(1),
    { timeout: 3000 },
  );
  await stops(f);
}, 20000);
it("ends a provisional refusal that a cancellation superseded without a diagnostic", async () => {
  const f = fixture();
  await start(f);
  f.setState(State.UNSUPPORTED, refusal());
  await screen.findByText(refusalMessage(), {}, { timeout: 3000 });
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  await waitFor(() => expect(f.cancel).toHaveBeenCalledTimes(1));
  f.setState(State.CANCELED);
  await screen.findByText(
    copy("grok-subscription.status.canceled"),
    {},
    { timeout: 3000 },
  );
  expect(screen.queryByText(refusalMessage())).toBeNull();
  await waitFor(
    () => expect(actions(f, OAuthNativeAction.Dispose)).toHaveLength(1),
    { timeout: 3000 },
  );
  await stops(f);
}, 20000);
it("treats an unsupported response without a Grok diagnostic as final and shows no notice", async () => {
  const f = fixture();
  await start(f);
  f.setState(State.UNSUPPORTED);
  await screen.findByText(
    copy("grok-subscription.status.unsupported"),
    {},
    { timeout: 3000 },
  );
  expect(screen.queryByText(refusalMessage())).toBeNull();
  await waitFor(
    () => expect(actions(f, OAuthNativeAction.Dispose)).toHaveLength(1),
    { timeout: 3000 },
  );
  await stops(f);
}, 20000);

it("keeps the Grok row unchanged without capability 39 and enables Add account with it", async () => {
  async function row(capabilities: SystemCapability[]) {
    const transport = createRouterTransport((router) => {
      router.service(SystemService, { getStatus: () => ({ capabilities }) });
      router.service(ResourceService, {
        listResources: () => ({ resources: [] }),
      });
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const native = vi.fn(async () => ({ generation: newRequestId() }));
    const view = render(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <OAuthNativeProvider control={native}>
            <SettingsLifetime>
              {() => (
                <MutationIntents>
                  <SubscriptionAccounts
                    active
                    editAccount={vi.fn()}
                    deleteAccount={vi.fn()}
                  />
                </MutationIntents>
              )}
            </SettingsLifetime>
          </OAuthNativeProvider>
        </QueryClientProvider>
      </TransportProvider>,
    );
    return { view, client };
  }
  const label = (state: string) =>
    copy("subscription-settings.message_78735b", { v0: "Grok", v1: state });
  const without = await row([
    SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
  ]);
  const disabled = await screen.findByRole("button", {
    name: label(copy("subscription-settings.comingSoon_4f7d64")),
  });
  expect((disabled as HTMLButtonElement).disabled).toBe(true);
  without.view.unmount();
  without.client.clear();
  const present = await row([
    SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
    SystemCapability.GROK_SUBSCRIPTION_LOGIN_V1,
  ]);
  await waitFor(async () => {
    const button = await screen.findByRole("button", {
      name: label(copy("subscription-settings.addAccount_ee7ee5")),
    });
    expect((button as HTMLButtonElement).disabled).toBe(false);
  });
  present.view.unmount();
  present.client.clear();
});

function deviceFixture(url = DEVICE_URL, code = DEVICE_CODE) {
  const f = fixture(url);
  f.setUserCode(code);
  return f;
}
async function startDevice(f: ReturnType<typeof fixture>) {
  const rendered = render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok device" }));
  await screen.findByText(DEVICE_CODE, {}, { timeout: 3000 });
  return rendered;
}
const nativeActions = (f: ReturnType<typeof fixture>) =>
  f.native.mock.calls.map((call) => call[1]);

it("accepts only the fixed accounts.x.ai device verification page and matching code", () => {
  expect(grokDeviceVerification(DEVICE_URL, DEVICE_CODE)).toBe(true);
  expect(
    grokDeviceVerification("https://accounts.x.ai/oauth2/device", DEVICE_CODE),
  ).toBe(true);
  const rejected: Array<[string, string, string]> = [
    ["host", "https://auth.x.ai/oauth2/device", DEVICE_CODE],
    ["lookalike", "https://accounts.x.ai.evil.test/oauth2/device", DEVICE_CODE],
    ["scheme", "http://accounts.x.ai/oauth2/device", DEVICE_CODE],
    ["path", "https://accounts.x.ai/oauth2/token", DEVICE_CODE],
    ["port", "https://accounts.x.ai:8443/oauth2/device", DEVICE_CODE],
    ["credentials", "https://u@accounts.x.ai/oauth2/device", DEVICE_CODE],
    ["fragment", `${DEVICE_URL}#x`, DEVICE_CODE],
    ["extra query", `${DEVICE_URL}&next=1`, DEVICE_CODE],
    ["foreign query", "https://accounts.x.ai/oauth2/device?x=1", DEVICE_CODE],
    ["mismatched code", DEVICE_URL, "WXYZ-9876"],
    ["lowercase code", DEVICE_URL, "abcd-1234"],
    ["short code", "https://accounts.x.ai/oauth2/device", "AB"],
    ["long code", "https://accounts.x.ai/oauth2/device", "A".repeat(33)],
    ["empty code", "https://accounts.x.ai/oauth2/device", ""],
    ["not a url", "not a url", DEVICE_CODE],
  ];
  for (const [name, url, code] of rejected)
    expect(grokDeviceVerification(url, code), name).toBe(false);
});

interface DeviceVerificationCase {
  name: string;
  url: string;
  userCode: string;
  native: boolean;
  renderer: boolean;
}
interface AuthorizationCase {
  name: string;
  url: string;
  valid: boolean;
}
const urlFixtures = urlValidationFixtures as {
  deviceVerification: DeviceVerificationCase[];
  authorization: AuthorizationCase[];
};

// The same table drives the native Rust tests in src-tauri/src/oauth.rs, so the
// renderer predicate and the native validator cannot drift apart.
it("matches the shared device verification URL table", () => {
  expect(urlFixtures.deviceVerification.length).toBeGreaterThan(20);
  for (const row of urlFixtures.deviceVerification)
    expect(grokDeviceVerification(row.url, row.userCode), row.name).toBe(
      row.renderer,
    );
  // Every row the native host rejects is also rejected by the renderer.
  for (const row of urlFixtures.deviceVerification)
    if (!row.native) expect(row.renderer, row.name).toBe(false);
});

it("matches the shared Grok authorization URL table", () => {
  expect(urlFixtures.authorization.length).toBeGreaterThan(20);
  for (const row of urlFixtures.authorization)
    expect(grokLoginURL(row.url), row.name).toBe(row.valid);
});

it("sends one deviceCode login despite double click, Strict Mode and polling and shows the transient code", async () => {
  const f = deviceFixture();
  const logs = (["log", "info", "warn", "error", "debug"] as const).map(
    (level) => vi.spyOn(console, level).mockImplementation(() => undefined),
  );
  render(f.view());
  const add = screen.getByRole("button", { name: "Start Grok device" });
  fireEvent.click(add);
  fireEvent.click(add);
  const code = await screen.findByText(DEVICE_CODE, {}, { timeout: 3000 });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 2300));
  });
  expect(f.save).toHaveBeenCalledTimes(1);
  expect(f.login).toHaveBeenCalledTimes(1);
  expect(f.login.mock.calls[0][0]).toMatchObject({
    action: 1,
    deviceCode: true,
  });
  expect(f.progress.mock.calls.length).toBeGreaterThan(1);
  // Device sign-in binds no native receiver and never takes a callback.
  expect(nativeActions(f)).toEqual([]);
  expect(f.forward).not.toHaveBeenCalled();
  expect(code.tagName).toBe("OUTPUT");
  expect(code.className).toContain("grok-device-user-code");
  expect(screen.getByLabelText(copy("grok-subscription.device.codeAria"))).toBe(
    code,
  );
  expect(code.tabIndex).toBe(0);
  expect(
    screen.getByRole("textbox", {
      name: copy("grok-subscription.device.codeAria"),
    }),
  ).toBe(code);
  expect(screen.getByRole("status").textContent).toContain(
    copy("grok-subscription.status.waiting"),
  );
  expect(
    screen.getByText(copy("grok-subscription.device.help.waiting")),
  ).toBeTruthy();
  // The code and URL never reach the shared query cache, title, location or logs.
  expect(
    f.client
      .getQueryCache()
      .getAll()
      .filter((query) =>
        JSON.stringify(query.queryKey).includes("SubscriptionService"),
      ),
  ).toHaveLength(0);
  expect(window.document.title).not.toContain(DEVICE_CODE);
  expect(window.location.href).not.toContain(DEVICE_CODE);
  expect(window.document.body.textContent).not.toContain("accounts.x.ai");
  for (const log of logs)
    expect(JSON.stringify(log.mock.calls)).not.toContain(DEVICE_CODE);
});

it("copies the code, opens the verification page once and reports failures", async () => {
  const f = deviceFixture();
  const writeText = vi.fn(async (_value: string) => undefined);
  Object.defineProperty(window.navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  externalOpen.mockClear();
  await startDevice(f);
  fireEvent.click(
    screen.getByRole("button", {
      name: copy("grok-subscription.device.copyCode"),
    }),
  );
  await screen.findByText(copy("grok-subscription.device.codeCopied"));
  expect(writeText).toHaveBeenCalledWith(DEVICE_CODE);
  expect(writeText).toHaveBeenCalledTimes(1);

  fireEvent.click(
    screen.getByRole("button", {
      name: copy("grok-subscription.device.openVerification"),
    }),
  );
  await waitFor(() =>
    expect(
      actions(f, OAuthNativeAction.GrokDeviceAuthorizationOpen),
    ).toHaveLength(1),
  );
  // The authorization page goes through the closed OAuth native action with
  // the original operation, never through the General/Local link opener.
  expect(f.native).toHaveBeenCalledWith(
    expect.any(String),
    OAuthNativeAction.GrokDeviceAuthorizationOpen,
    "",
    f.login.mock.calls[0][0].mutation.requestId,
    DEVICE_URL,
  );
  expect(externalOpen).not.toHaveBeenCalled();

  f.native.mockRejectedValueOnce("browser-launch-failed");
  fireEvent.click(
    screen.getByRole("button", {
      name: copy("grok-subscription.device.openVerification"),
    }),
  );
  await screen.findByText(
    copy("grok-subscription.problem.verificationOpenFailed"),
  );
  // The code stays visible and copyable after a failed open.
  expect(screen.getByText(DEVICE_CODE)).toBeTruthy();
  expect(externalOpen).not.toHaveBeenCalled();

  writeText.mockRejectedValueOnce(new Error("denied"));
  fireEvent.click(
    screen.getByRole("button", {
      name: copy("grok-subscription.device.copyCode"),
    }),
  );
  await screen.findByText(copy("grok-subscription.device.codeCopyFailed"));
  expect(screen.getByText(DEVICE_CODE)).toBeTruthy();
});

it("keeps the device code keyboard-reachable and selectable for manual copy", async () => {
  Object.defineProperty(window.navigator, "clipboard", {
    configurable: true,
    value: { writeText: vi.fn().mockRejectedValue(new Error("denied")) },
  });
  const f = deviceFixture();
  await startDevice(f);
  const code = screen.getByRole("textbox", {
    name: copy("grok-subscription.device.codeAria"),
  });
  expect(code.tabIndex).toBe(0);
  expect(code.getAttribute("aria-readonly")).toBe("true");
  // The documented fallback after a clipboard failure is selecting the code.
  fireEvent.click(
    screen.getByRole("button", {
      name: copy("grok-subscription.device.copyCode"),
    }),
  );
  await screen.findByText(copy("grok-subscription.device.codeCopyFailed"));
  // The code is a tab stop, and focusing it selects exactly the code.
  act(() => code.focus());
  expect(window.document.activeElement).toBe(code);
  expect(window.getSelection()?.toString()).toBe(DEVICE_CODE);
  // Ctrl+A re-selects only the code instead of the whole page.
  window.getSelection()?.removeAllRanges();
  const handled = fireEvent.keyDown(code, { key: "a", ctrlKey: true });
  expect(handled).toBe(false);
  expect(window.getSelection()?.toString()).toBe(DEVICE_CODE);
  // Other keys never edit the code.
  fireEvent.keyDown(code, { key: "z" });
  expect(code.textContent).toBe(DEVICE_CODE);
});

it.each([
  [State.EXPIRED, "grok-subscription.status.expired", "device.help.expired"],
  [State.FAILED, "grok-subscription.status.failed", "device.help.failed"],
  [State.CANCELED, "grok-subscription.status.canceled", "device.help.canceled"],
  [
    State.RECOVERY_REQUIRED,
    "grok-subscription.status.recovery",
    "help.recovery",
  ],
] as const)(
  "clears the code on terminal state %s and returns to the picker without a connected account",
  async (state, status, help) => {
    const f = deviceFixture();
    await startDevice(f);
    f.setState(state);
    await screen.findByText(copy(status as never), {}, { timeout: 3000 });
    expect(screen.queryByText(DEVICE_CODE)).toBeNull();
    expect(
      screen.queryByRole("button", {
        name: copy("grok-subscription.device.copyCode"),
      }),
    ).toBeNull();
    expect(
      screen.getByText(copy(`grok-subscription.${help}` as never)),
    ).toBeTruthy();
    expect(f.save).toHaveBeenCalledTimes(1);
    fireEvent.click(
      screen.getByRole("button", { name: copy("grok-subscription.back") }),
    );
    await screen.findByRole("button", { name: "Start Grok device" });
    expect(window.document.body.textContent).not.toContain(DEVICE_CODE);
    // Never an automatic second login or fallback to the browser method.
    expect(f.login).toHaveBeenCalledTimes(1);
    expect(nativeActions(f)).not.toContain(
      OAuthNativeAction.GrokSubscriptionOpen,
    );
  },
);

it("cancels through the server and clears the code when the screen closes", async () => {
  const f = deviceFixture();
  const view = await startDevice(f);
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  await waitFor(() => expect(f.cancel).toHaveBeenCalledTimes(1));
  f.setState(State.CANCELED);
  await screen.findByText(
    copy("grok-subscription.status.canceled"),
    {},
    {
      timeout: 3000,
    },
  );
  expect(screen.queryByText(DEVICE_CODE)).toBeNull();
  view.unmount();
  expect(window.document.body.textContent).not.toContain(DEVICE_CODE);
});

it("clears the code on unmount without canceling the accepted operation", async () => {
  const f = deviceFixture();
  const view = await startDevice(f);
  const polls = f.progress.mock.calls.length;
  view.unmount();
  expect(window.document.body.textContent).not.toContain(DEVICE_CODE);
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 1300));
  });
  expect(f.progress.mock.calls.length).toBeLessThanOrEqual(polls + 1);
  expect(f.cancel).not.toHaveBeenCalled();
});

it.each([
  ["foreign host", "https://auth.x.ai/oauth2/device", DEVICE_CODE],
  ["mismatched query code", DEVICE_URL, "WXYZ-9876"],
  ["malformed code", "https://accounts.x.ai/oauth2/device", "ab"],
  ["browser URL", loginURL, DEVICE_CODE],
])("refuses an invalid device authorization (%s)", async (_name, url, code) => {
  const f = deviceFixture(url, code);
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok device" }));
  await screen.findByText(
    copy("grok-subscription.problem.statusUnconfirmed"),
    {},
    { timeout: 3000 },
  );
  expect(screen.queryByText(code)).toBeNull();
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.device.copyCode"),
    }),
  ).toBeNull();
  expect(externalOpen).not.toHaveBeenCalled();
  expect(
    actions(f, OAuthNativeAction.GrokDeviceAuthorizationOpen),
  ).toHaveLength(0);
});

it("refuses a user code on the browser method and a changed code mid-login", async () => {
  const browser = fixture();
  browser.setUserCode(DEVICE_CODE);
  render(browser.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok" }));
  await screen.findByText(
    copy("grok-subscription.problem.statusUnconfirmed"),
    {},
    { timeout: 3000 },
  );
  expect(screen.queryByText(DEVICE_CODE)).toBeNull();
  expect(actions(browser, OAuthNativeAction.GrokSubscriptionOpen)).toHaveLength(
    0,
  );
  cleanup();

  const device = deviceFixture();
  await startDevice(device);
  device.setUserCode("WXYZ-9876");
  await screen.findByText(
    copy("grok-subscription.problem.statusUnconfirmed"),
    {},
    { timeout: 3000 },
  );
  // The originally pinned code stays; the replacement is never displayed.
  expect(screen.queryByText("WXYZ-9876")).toBeNull();
});

it("keeps the same device screen while the server slows polling", async () => {
  const f = deviceFixture();
  await startDevice(f);
  // Slow-down is server side: repeated identical Waiting progress changes nothing.
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 2300));
  });
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.getByText(DEVICE_CODE)).toBeTruthy();
  expect(f.login).toHaveBeenCalledTimes(1);
});

const openVerification = () =>
  fireEvent.click(
    screen.getByRole("button", {
      name: copy("grok-subscription.device.openVerification"),
    }),
  );
async function pollTwice(f: ReturnType<typeof fixture>) {
  const before = f.progress.mock.calls.length;
  await waitFor(
    () =>
      expect(f.progress.mock.calls.length).toBeGreaterThanOrEqual(before + 2),
    { timeout: 4000 },
  );
}

it("keeps a verification-open failure visible across later waiting polls until another open succeeds", async () => {
  const f = deviceFixture();
  await startDevice(f);
  f.native.mockRejectedValueOnce("browser-launch-failed");
  openVerification();
  const failure = copy("grok-subscription.problem.verificationOpenFailed");
  await screen.findByText(failure);
  // Normal one-second WAITING polls neither clear nor replace the failure.
  await pollTwice(f);
  expect(screen.getByText(failure)).toBeTruthy();
  expect(screen.getByText(DEVICE_CODE)).toBeTruthy();
  // A failing status read keeps the more specific opener failure too.
  f.progress.mockImplementationOnce(() => {
    throw new ConnectError("Fixture status failure", Code.Unavailable);
  });
  await pollTwice(f);
  expect(screen.getByText(failure)).toBeTruthy();
  expect(
    screen.queryByText(copy("grok-subscription.problem.statusUnconfirmed")),
  ).toBeNull();
  // The next deliberate open attempt succeeds and the failure goes away.
  openVerification();
  await waitFor(() => expect(screen.queryByText(failure)).toBeNull());
  await pollTwice(f);
  expect(screen.queryByRole("alert")).toBeNull();
  expect(
    actions(f, OAuthNativeAction.GrokDeviceAuthorizationOpen),
  ).toHaveLength(2);
  expect(externalOpen).not.toHaveBeenCalled();
}, 20000);

it("recovers only the polling notice after a later successful status read", async () => {
  const f = deviceFixture();
  await startDevice(f);
  f.progress.mockImplementationOnce(() => {
    throw new ConnectError("Fixture status failure", Code.Unavailable);
  });
  await screen.findByText(
    copy("grok-subscription.problem.statusUnconfirmed"),
    {},
    { timeout: 3000 },
  );
  await waitFor(
    () =>
      expect(
        screen.queryByText(copy("grok-subscription.problem.statusUnconfirmed")),
      ).toBeNull(),
    { timeout: 3000 },
  );
  expect(screen.getByText(DEVICE_CODE)).toBeTruthy();
});

it("keeps a failed cancellation and its retry visible across later waiting polls", async () => {
  const f = deviceFixture();
  await startDevice(f);
  f.cancel.mockImplementationOnce(() => {
    throw new ConnectError("Fixture cancel failure", Code.Unavailable);
  });
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  const failure = copy("grok-subscription.problem.cancelUnconfirmed");
  await screen.findByText(failure);
  await pollTwice(f);
  expect(screen.getByText(failure)).toBeTruthy();
  expect(
    screen.getByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeTruthy();
});

it("advances to naming after verified device success and drops the code", async () => {
  const f = deviceFixture();
  await startDevice(f);
  f.success();
  const input = await screen.findByLabelText(
    copy("grok-subscription.accountName"),
    {},
    { timeout: 3000 },
  );
  expect((input as HTMLInputElement).value).toBe(
    "grok.fixture@example.invalid",
  );
  expect(screen.queryByText(DEVICE_CODE)).toBeNull();
});

it("renders English and Korean device text and keeps the locale key sets aligned", async () => {
  const deviceKeys = [
    "grok-subscription.device.action",
    "grok-subscription.device.addressAria",
    "grok-subscription.device.addressCopied",
    "grok-subscription.device.addressCopyFailed",
    "grok-subscription.device.addressLabel",
    "grok-subscription.device.codeAria",
    "grok-subscription.device.codeCopied",
    "grok-subscription.device.codeCopyFailed",
    "grok-subscription.device.codeLabel",
    "grok-subscription.device.copyAddress",
    "grok-subscription.device.copyCode",
    "grok-subscription.device.help.canceled",
    "grok-subscription.device.help.expired",
    "grok-subscription.device.help.failed",
    "grok-subscription.device.help.unsupported",
    "grok-subscription.device.help.waiting",
    "grok-subscription.device.openVerification",
    "grok-subscription.problem.verificationOpenFailed",
  ];
  for (const catalog of [english, korean])
    expect(
      Object.keys(catalog)
        .filter(
          (key) =>
            key.startsWith("grok-subscription.device.") ||
            key === "grok-subscription.problem.verificationOpenFailed",
        )
        .sort(),
    ).toEqual(deviceKeys);
  expect(Object.keys(korean).sort()).toEqual(Object.keys(english).sort());
  for (const key of deviceKeys)
    expect((korean as Record<string, string>)[key]).not.toBe(
      (english as Record<string, string>)[key],
    );
  await i18n.changeLanguage(SupportedLanguage.Korean);
  const f = deviceFixture();
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok device" }));
  await screen.findByText(DEVICE_CODE, {}, { timeout: 3000 });
  expect(
    screen.getByRole("button", {
      name: korean["grok-subscription.device.openVerification"],
    }),
  ).toBeTruthy();
  expect(
    screen.getByRole("button", {
      name: korean["grok-subscription.device.copyCode"],
    }),
  ).toBeTruthy();
});

it("offers Use a device code beside Add account only with capability 39 and starts one deviceCode login", async () => {
  async function render39(capabilities: SystemCapability[]) {
    const f = deviceFixture();
    const transport = createRouterTransport((router) => {
      router.service(SystemService, { getStatus: () => ({ capabilities }) });
      router.service(ResourceService, {
        listResources: () => ({ resources: [] }),
        getResource: f.read,
      });
      router.service(ConfigurationService, { saveConfiguration: f.save });
      router.service(SubscriptionService, {
        requestSubscription: f.login,
        getSubscriptionProgress: f.progress,
        cancelSubscription: f.cancel,
        forwardSubscriptionCallback: f.forward,
      });
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const rendered = render(
      <StrictMode>
        <TransportProvider transport={transport}>
          <QueryClientProvider client={client}>
            <OAuthNativeProvider control={f.native}>
              <SettingsLifetime>
                {() => (
                  <MutationIntents>
                    <SubscriptionAccounts
                      active
                      editAccount={vi.fn()}
                      deleteAccount={vi.fn()}
                    />
                  </MutationIntents>
                )}
              </SettingsLifetime>
            </OAuthNativeProvider>
          </QueryClientProvider>
        </TransportProvider>
      </StrictMode>,
    );
    return { f, rendered, client };
  }
  const deviceLabel = copy("subscription-settings.message_78735b", {
    v0: "Grok",
    v1: copy("grok-subscription.device.action"),
  });
  const without = await render39([
    SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
  ]);
  await screen.findByRole("button", {
    name: copy("subscription-settings.message_78735b", {
      v0: "Grok",
      v1: copy("subscription-settings.comingSoon_4f7d64"),
    }),
  });
  expect(screen.queryByRole("button", { name: deviceLabel })).toBeNull();
  without.rendered.unmount();
  without.client.clear();

  // Inventory capability 17 is required too: capability 39 alone offers no
  // device action, exactly like Add account.
  const noInventory = await render39([
    SystemCapability.GROK_SUBSCRIPTION_LOGIN_V1,
  ]);
  await waitFor(() => expect(noInventory.f.progress).not.toHaveBeenCalled());
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 300));
  });
  expect(screen.queryByRole("button", { name: deviceLabel })).toBeNull();
  expect(noInventory.f.save).not.toHaveBeenCalled();
  noInventory.rendered.unmount();
  noInventory.client.clear();

  const present = await render39([
    SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
    SystemCapability.GROK_SUBSCRIPTION_LOGIN_V1,
  ]);
  const device = await screen.findByRole("button", { name: deviceLabel });
  const add = screen.getByRole("button", {
    name: copy("subscription-settings.message_78735b", {
      v0: "Grok",
      v1: copy("subscription-settings.addAccount_ee7ee5"),
    }),
  });
  expect(device.parentElement).toBe(add.parentElement);
  expect((device as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(device);
  fireEvent.click(device);
  await screen.findByText(DEVICE_CODE, {}, { timeout: 3000 });
  expect(present.f.login).toHaveBeenCalledTimes(1);
  expect(present.f.login.mock.calls[0][0]).toMatchObject({ deviceCode: true });
  expect(present.f.save).toHaveBeenCalledTimes(1);
  present.rendered.unmount();
  present.client.clear();
});

// Device login state table (renderer half), abridged from the generated state
// machine in grok-login-machine.ts (the source of truth, tested for every phase
// and event pair). Rows are the server stage, columns the pending work and the
// way the screen can leave; every cell has a test here too.
//
//   stage      retry closure   verification open   leave (Back/unmount/abort) | terminal
//   PREPARING  none            hidden              no native call             | n/a
//   WAITING    none            enabled             Dispose only if opened     | Dispose only if opened
//   WAITING    held (cancel)   disabled            Dispose only if opened     | retry dropped
//   WAITING    canceling       hidden              Dispose only if opened     | Dispose only if opened
//   WAITING    open pending    busy/disabled       Dispose retires the open   | Dispose retires the open
//   terminal   none            hidden              Dispose only if opened     | settled
//
// A status-read notice recovers on any later healthy read (any stage); every
// other notice stays until the user acts again.
const openName = () => copy("grok-subscription.device.openVerification");
const nativeOpen = (f: ReturnType<typeof fixture>) =>
  actions(f, OAuthNativeAction.GrokDeviceAuthorizationOpen);
function holdNextNativeOpen(f: ReturnType<typeof fixture>) {
  let settle: (outcome: "resolve" | "reject") => void = () => undefined;
  f.native.mockImplementationOnce(
    () =>
      new Promise((resolve, reject) => {
        settle = (outcome) =>
          outcome === "resolve"
            ? resolve({ generation: "" })
            : reject("browser-launch-failed");
      }),
  );
  return (outcome: "resolve" | "reject" = "resolve") => settle(outcome);
}
const cancelRequests = (f: ReturnType<typeof fixture>) =>
  f.cancel.mock.calls as unknown as Array<
    [{ mutation: { requestId: string } }]
  >;
const failCancelOnce = (f: ReturnType<typeof fixture>) =>
  f.cancel.mockImplementationOnce(() => {
    throw new ConnectError("Fixture cancel failure", Code.Unavailable);
  });

it("keeps the held cancellation retry when the verification page cannot be opened", async () => {
  const f = deviceFixture();
  await startDevice(f);
  failCancelOnce(f);
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  await screen.findByText(copy("grok-subscription.problem.cancelUnconfirmed"));
  const retry = screen.getByRole("button", {
    name: copy("grok-subscription.retryOriginal"),
  });
  // Only the retry settles the uncertain cancellation: the opener and a second
  // cancel are unavailable, so neither can replace the original request.
  const opener = screen.getByRole("button", { name: openName() });
  expect((opener as HTMLButtonElement).disabled).toBe(true);
  expect(
    (
      screen.getByRole("button", {
        name: copy("grok-subscription.cancel"),
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(opener);
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  await pollTwice(f);
  expect(nativeOpen(f)).toHaveLength(0);
  expect(f.cancel).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: retry.textContent! })).toBeTruthy();
  fireEvent.click(retry);
  await waitFor(() => expect(f.cancel).toHaveBeenCalledTimes(2));
  // The retry replays the exact original request.
  expect(cancelRequests(f)[1][0]).toEqual(cancelRequests(f)[0][0]);
  expect(cancelRequests(f)[1][0].mutation.requestId).toBe(
    cancelRequests(f)[0][0].mutation.requestId,
  );
  await screen.findByText(copy("grok-subscription.problem.canceling"));
  // Once the cancellation is accepted the verification page is not offered.
  expect(screen.queryByRole("button", { name: openName() })).toBeNull();
  expect(
    screen.queryByRole("button", { name: copy("grok-subscription.cancel") }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeNull();
  expect(nativeOpen(f)).toHaveLength(0);
}, 20000);

it("keeps the held cancellation retry when the browser login cannot be reopened", async () => {
  const f = fixture();
  await start(f);
  failCancelOnce(f);
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  await screen.findByText(copy("grok-subscription.problem.cancelUnconfirmed"));
  const reopen = screen.getByRole("button", {
    name: copy("grok-subscription.openBrowser"),
  });
  expect((reopen as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(reopen);
  await pollTwice(f);
  expect(actions(f, OAuthNativeAction.GrokSubscriptionReopen)).toHaveLength(0);
  fireEvent.click(
    screen.getByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  );
  await waitFor(() => expect(f.cancel).toHaveBeenCalledTimes(2));
  expect(cancelRequests(f)[1][0]).toEqual(cancelRequests(f)[0][0]);
}, 20000);

it("drops the held retry and every opener when a terminal state arrives", async () => {
  const f = deviceFixture();
  await startDevice(f);
  failCancelOnce(f);
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  await screen.findByText(copy("grok-subscription.problem.cancelUnconfirmed"));
  f.setState(State.EXPIRED);
  await screen.findByText(
    copy("grok-subscription.status.expired"),
    {},
    { timeout: 3000 },
  );
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeNull();
  expect(screen.queryByRole("button", { name: openName() })).toBeNull();
  expect(screen.queryByText(DEVICE_CODE)).toBeNull();
  expect(f.cancel).toHaveBeenCalledTimes(1);
  // No opener was ever requested, so no native state exists to dispose.
  expect(nativeActions(f)).toEqual([]);
}, 20000);

it("recovers the polling notice on a healthy preparing read and keeps other notices", async () => {
  const f = deviceFixture();
  await startDevice(f);
  f.setState(State.PREPARING);
  f.progress.mockImplementationOnce(() => {
    throw new ConnectError("Fixture status failure", Code.Unavailable);
  });
  const notice = copy("grok-subscription.problem.statusUnconfirmed");
  await screen.findByText(notice, {}, { timeout: 3000 });
  // Later successful reads still report PREPARING, which has no early path to
  // a presentation update, yet they must recover the notice.
  await waitFor(() => expect(screen.queryByText(notice)).toBeNull(), {
    timeout: 4000,
  });
  // Server stages only advance, so the screen keeps the waiting phase.
  expect(screen.getByRole("button", { name: openName() })).toBeTruthy();

  // A one-shot opener failure is not a polling notice and survives healthy
  // preparing reads.
  f.setState(State.WAITING);
  await screen.findByRole("button", { name: openName() }, { timeout: 3000 });
  f.native.mockRejectedValueOnce("browser-launch-failed");
  openVerification();
  const failure = copy("grok-subscription.problem.verificationOpenFailed");
  await screen.findByText(failure);
  f.setState(State.PREPARING);
  await pollTwice(f);
  expect(screen.getByText(failure)).toBeTruthy();
}, 30000);

it("recovers the polling notice on a healthy read that reports an accepted cancellation", async () => {
  const f = deviceFixture();
  await startDevice(f);
  f.progress.mockImplementationOnce(() => {
    throw new ConnectError("Fixture status failure", Code.Unavailable);
  });
  const notice = copy("grok-subscription.problem.statusUnconfirmed");
  await screen.findByText(notice, {}, { timeout: 3000 });
  f.progress.mockImplementation(() => ({
    state: State.WAITING,
    url: DEVICE_URL,
    userCode: DEVICE_CODE,
    suggestedName: "",
    generation: f.generation,
    canceled: true,
  }));
  await waitFor(() => expect(screen.queryByText(notice)).toBeNull(), {
    timeout: 4000,
  });
  // The server already canceled: the page is not offered any more.
  expect(screen.queryByRole("button", { name: openName() })).toBeNull();
  expect(nativeOpen(f)).toHaveLength(0);
}, 20000);

it("does not touch native state when a never-opened device login is left", async () => {
  const f = deviceFixture();
  const view = await startDevice(f);
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.back") }),
  );
  await screen.findByRole("button", { name: "Start Grok device" });
  expect(nativeActions(f)).toEqual([]);
  view.unmount();
  expect(nativeActions(f)).toEqual([]);
});

it("disposes the opening after a completed open when the screen is left", async () => {
  const f = deviceFixture();
  await startDevice(f);
  openVerification();
  await waitFor(() => expect(nativeOpen(f)).toHaveLength(1));
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.back") }),
  );
  await screen.findByRole("button", { name: "Start Grok device" });
  const disposals = actions(f, OAuthNativeAction.Dispose);
  expect(disposals.length).toBeGreaterThanOrEqual(1);
  // The same opening scope retires the dispatch the host admitted.
  for (const call of disposals) expect(call[0]).toBe(nativeOpen(f)[0][0]);
});

it.each([
  ["Back", "back"],
  ["unmount", "unmount"],
  ["terminal state", "terminal"],
  ["observed cancellation", "observed"],
  ["verified success", "success"],
] as const)(
  "retires a pending verification open through the native host on %s",
  async (_name, how) => {
    const f = deviceFixture();
    const view = await startDevice(f);
    const settle = holdNextNativeOpen(f);
    openVerification();
    await waitFor(() => expect(nativeOpen(f)).toHaveLength(1));
    // While the native call is pending the opener is busy, never duplicated.
    expect(
      (screen.getByRole("button", { name: openName() }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: openName() }));
    expect(nativeOpen(f)).toHaveLength(1);
    const before = actions(f, OAuthNativeAction.Dispose).length;
    if (how === "back")
      fireEvent.click(
        screen.getByRole("button", { name: copy("grok-subscription.back") }),
      );
    else if (how === "unmount") view.unmount();
    else if (how === "terminal") {
      f.setState(State.EXPIRED);
      await screen.findByText(
        copy("grok-subscription.status.expired"),
        {},
        { timeout: 3000 },
      );
    } else if (how === "observed") {
      // A WAITING status read carries the canceled flag while the native open
      // is still pending: the pending dispatch is retired without any local
      // cancel mutation, so the browser cannot open after the server canceled.
      f.observeCanceled();
      await screen.findByText(
        copy("grok-subscription.problem.canceling"),
        {},
        { timeout: 3000 },
      );
      expect(f.cancel).not.toHaveBeenCalled();
    } else {
      f.success();
      await screen.findByLabelText(
        copy("grok-subscription.accountName"),
        {},
        { timeout: 3000 },
      );
    }
    await waitFor(() =>
      expect(actions(f, OAuthNativeAction.Dispose).length).toBeGreaterThan(
        before,
      ),
    );
    for (const call of actions(f, OAuthNativeAction.Dispose))
      expect(call[0]).toBe(nativeOpen(f)[0][0]);
    // The late native settlement neither throws nor resurrects any state.
    await act(async () => {
      settle("reject");
      await new Promise((resolve) => setTimeout(resolve, 50));
    });
    expect(screen.queryByText(DEVICE_CODE)).toBeNull();
    expect(
      screen.queryByText(
        copy("grok-subscription.problem.verificationOpenFailed"),
      ),
    ).toBeNull();
    expect(nativeOpen(f)).toHaveLength(1);
    expect(externalOpen).not.toHaveBeenCalled();
  },
  20000,
);

it("hides the verification page when the server accepted the cancellation", async () => {
  const f = deviceFixture();
  await startDevice(f);
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  await screen.findByText(copy("grok-subscription.problem.canceling"));
  expect(screen.queryByRole("button", { name: openName() })).toBeNull();
  // Nothing is offered once the cancellation was accepted: the pinned code and
  // its copy action are released with the opener.
  expect(screen.queryByText(DEVICE_CODE)).toBeNull();
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.device.copyCode"),
    }),
  ).toBeNull();
  expect(nativeOpen(f)).toHaveLength(0);
  expect(f.cancel).toHaveBeenCalledTimes(1);
}, 20000);

// Verification address cells (device flow). The address is a display-only copy
// of the validated URL shown after the native opener failed.
//
//   situation                         address shown   Copy address   code
//   waiting, nothing requested        no              no             shown
//   native open failed                yes             yes            shown
//   native open failed, then opened   no              no             shown
//   open failed, cancel accepted      no              no             cleared
//   open failed, terminal state       no              no             cleared
const addressName = () => copy("grok-subscription.device.addressAria");
const copyAddressName = () => copy("grok-subscription.device.copyAddress");
async function failVerificationOpen(f: ReturnType<typeof fixture>) {
  f.native.mockRejectedValueOnce("browser-launch-failed");
  fireEvent.click(screen.getByRole("button", { name: openName() }));
  await screen.findByText(
    copy("grok-subscription.problem.verificationOpenFailed"),
  );
}
const addressHidden = () => {
  expect(screen.queryByRole("textbox", { name: addressName() })).toBeNull();
  expect(screen.queryByRole("button", { name: copyAddressName() })).toBeNull();
};

it.each([
  ["with the embedded code", DEVICE_URL],
  ["without an embedded code", "https://accounts.x.ai/oauth2/device"],
])(
  "shows a selectable, keyboard-reachable and copyable verification address after the native open failed (%s)",
  async (_name, url) => {
    const writeText = vi.fn(async (_value: string) => undefined);
    Object.defineProperty(window.navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    externalOpen.mockClear();
    const f = deviceFixture(url);
    await startDevice(f);
    addressHidden();
    await failVerificationOpen(f);
    const address = screen.getByRole("textbox", { name: addressName() });
    expect(address.tagName).toBe("OUTPUT");
    expect(address.textContent).toBe(url);
    expect(address.tabIndex).toBe(0);
    expect(address.getAttribute("aria-readonly")).toBe("true");
    // The code and the closed native opener are unchanged.
    expect(screen.getByText(DEVICE_CODE)).toBeTruthy();
    expect(nativeOpen(f)).toHaveLength(1);
    expect(externalOpen).not.toHaveBeenCalled();
    // Focus and Ctrl/Cmd+A select exactly the address, never the page.
    act(() => address.focus());
    expect(window.document.activeElement).toBe(address);
    expect(window.getSelection()?.toString()).toBe(url);
    window.getSelection()?.removeAllRanges();
    expect(fireEvent.keyDown(address, { key: "a", metaKey: true })).toBe(false);
    expect(window.getSelection()?.toString()).toBe(url);
    fireEvent.keyDown(address, { key: "z" });
    expect(address.textContent).toBe(url);
    // Its own copy control copies the address, not the code or the link.
    fireEvent.click(screen.getByRole("button", { name: copyAddressName() }));
    await screen.findByText(copy("grok-subscription.device.addressCopied"));
    expect(writeText).toHaveBeenCalledWith(url);
    writeText.mockRejectedValueOnce(new Error("denied"));
    fireEvent.click(screen.getByRole("button", { name: copyAddressName() }));
    await screen.findByText(copy("grok-subscription.device.addressCopyFailed"));
    expect(screen.getByText(DEVICE_CODE)).toBeTruthy();
    expect(address.textContent).toBe(url);
  },
  20000,
);

it("hides the verification address after a later open succeeds", async () => {
  const f = deviceFixture();
  await startDevice(f);
  await failVerificationOpen(f);
  expect(screen.getByRole("textbox", { name: addressName() })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: openName() }));
  await waitFor(() => expect(nativeOpen(f)).toHaveLength(2));
  await waitFor(addressHidden);
  expect(
    screen.queryByText(
      copy("grok-subscription.problem.verificationOpenFailed"),
    ),
  ).toBeNull();
  expect(screen.getByText(DEVICE_CODE)).toBeTruthy();
}, 20000);

// Copy notice cells: a notice belongs to the value it copied and is kept exactly
// while the phase still offers that value.
//
//   copied     then                                 notice
//   address    a later open succeeds (code stays)   cleared with the address
//   code       open fails (address appears)         kept (code still offered)
//   code       a later open succeeds                kept (code still offered)
//   address    cancellation accepted or observed    cleared with everything
//   address    clipboard write settles after the    none (no stale notice)
//              address was hidden
function stubClipboard(writeText: (value: string) => Promise<void>) {
  Object.defineProperty(window.navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
}
const copyCodeName = () => copy("grok-subscription.device.copyCode");
const addressNotice = () => copy("grok-subscription.device.addressCopied");
const codeNotice = () => copy("grok-subscription.device.codeCopied");

it("clears the address copy notice when a later open hides the address although the code stays copyable", async () => {
  stubClipboard(async () => undefined);
  const f = deviceFixture();
  await startDevice(f);
  await failVerificationOpen(f);
  fireEvent.click(screen.getByRole("button", { name: copyAddressName() }));
  await screen.findByText(addressNotice());
  fireEvent.click(screen.getByRole("button", { name: openName() }));
  await waitFor(() => expect(nativeOpen(f)).toHaveLength(2));
  await waitFor(addressHidden);
  // The code is still copyable, but the notice belonged to the address.
  expect(screen.getByRole("button", { name: copyCodeName() })).toBeTruthy();
  expect(screen.queryByText(addressNotice())).toBeNull();
  expect(
    screen.queryByText(copy("grok-subscription.device.addressCopyFailed")),
  ).toBeNull();
}, 20000);

it("keeps the code copy notice when the address appears and disappears around it", async () => {
  stubClipboard(async () => undefined);
  const f = deviceFixture();
  await startDevice(f);
  fireEvent.click(screen.getByRole("button", { name: copyCodeName() }));
  await screen.findByText(codeNotice());
  await failVerificationOpen(f);
  expect(screen.getByText(codeNotice())).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: openName() }));
  await waitFor(() => expect(nativeOpen(f)).toHaveLength(2));
  await waitFor(addressHidden);
  expect(screen.getByText(codeNotice())).toBeTruthy();
}, 20000);

it("shows no address copy notice when the address was hidden before the clipboard write settled", async () => {
  let finish: () => void = () => undefined;
  stubClipboard(
    () =>
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
  );
  const f = deviceFixture();
  await startDevice(f);
  await failVerificationOpen(f);
  fireEvent.click(screen.getByRole("button", { name: copyAddressName() }));
  fireEvent.click(screen.getByRole("button", { name: openName() }));
  await waitFor(() => expect(nativeOpen(f)).toHaveLength(2));
  await waitFor(addressHidden);
  await act(async () => {
    finish();
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
  expect(screen.queryByText(addressNotice())).toBeNull();
}, 20000);

// Copy attempt cells (the copy attempt table of grok-login-machine.ts): a
// completion that outlives its attempt shows nothing.
//
//   situation when the clipboard settles                         notice
//   address hidden, then offered again (earlier attempt)         none
//   address hidden, offered again, a new attempt settled first   the new one
//   two attempts settle out of order, earlier one last           the latest
//   two attempts settle out of order, earlier one first          the latest
const deferredClipboard = () => {
  const pending: Array<{ value: string; settle: (ok: boolean) => void }> = [];
  stubClipboard(
    (value) =>
      new Promise<void>((resolve, reject) => {
        pending.push({
          value,
          settle: (ok) => (ok ? resolve() : reject(new Error("denied"))),
        });
      }),
  );
  return pending;
};
const settleAll = async (run: () => void) => {
  await act(async () => {
    run();
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
};

it("ignores an address copy that settles after the address was hidden and offered again", async () => {
  const pending = deferredClipboard();
  const f = deviceFixture();
  await startDevice(f);
  await failVerificationOpen(f);
  fireEvent.click(screen.getByRole("button", { name: copyAddressName() }));
  expect(pending).toHaveLength(1);
  // A successful open hides the address; a later failed open reveals it again.
  fireEvent.click(screen.getByRole("button", { name: openName() }));
  await waitFor(() => expect(nativeOpen(f)).toHaveLength(2));
  await waitFor(addressHidden);
  f.native.mockRejectedValueOnce("browser-launch-failed");
  fireEvent.click(screen.getByRole("button", { name: openName() }));
  await screen.findByRole("textbox", { name: addressName() });
  await settleAll(() => pending[0].settle(true));
  expect(screen.queryByText(addressNotice())).toBeNull();
  // The address is offered again, so a new attempt shows its own notice.
  fireEvent.click(screen.getByRole("button", { name: copyAddressName() }));
  expect(pending).toHaveLength(2);
  await settleAll(() => pending[1].settle(true));
  expect(screen.getByText(addressNotice())).toBeTruthy();
}, 20000);

it("ignores a failed address copy that settles after the address was hidden and offered again", async () => {
  const pending = deferredClipboard();
  const f = deviceFixture();
  await startDevice(f);
  await failVerificationOpen(f);
  fireEvent.click(screen.getByRole("button", { name: copyAddressName() }));
  fireEvent.click(screen.getByRole("button", { name: openName() }));
  await waitFor(() => expect(nativeOpen(f)).toHaveLength(2));
  await waitFor(addressHidden);
  f.native.mockRejectedValueOnce("browser-launch-failed");
  fireEvent.click(screen.getByRole("button", { name: openName() }));
  await screen.findByRole("textbox", { name: addressName() });
  await settleAll(() => pending[0].settle(false));
  expect(
    screen.queryByText(copy("grok-subscription.device.addressCopyFailed")),
  ).toBeNull();
}, 20000);

it.each([
  ["the earlier copy settling last", [1, 0]],
  ["the earlier copy settling first", [0, 1]],
] as const)(
  "shows only the latest copy notice with %s",
  async (_name, order) => {
    const pending = deferredClipboard();
    const f = deviceFixture();
    await startDevice(f);
    await failVerificationOpen(f);
    // The code and the address are both offered after a failed open.
    fireEvent.click(screen.getByRole("button", { name: copyCodeName() }));
    fireEvent.click(screen.getByRole("button", { name: copyAddressName() }));
    expect(pending.map((entry) => entry.value)).toEqual([
      DEVICE_CODE,
      DEVICE_URL,
    ]);
    for (const index of order) {
      await settleAll(() => pending[index].settle(true));
      expect(screen.queryByText(codeNotice())).toBeNull();
    }
    expect(screen.getByText(addressNotice())).toBeTruthy();
  },
  20000,
);

it("hides the verification address once the server accepted the cancellation", async () => {
  const f = deviceFixture();
  await startDevice(f);
  await failVerificationOpen(f);
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.cancel") }),
  );
  await screen.findByText(copy("grok-subscription.problem.canceling"));
  addressHidden();
  expect(screen.queryByRole("button", { name: openName() })).toBeNull();
  // Cancellation releases the code together with the address.
  expect(screen.queryByText(DEVICE_CODE)).toBeNull();
  expect(screen.queryByText(DEVICE_URL)).toBeNull();
}, 20000);

it("releases the code, address and opener when a status read reports the cancellation", async () => {
  const f = deviceFixture();
  await startDevice(f);
  await failVerificationOpen(f);
  expect(nativeOpen(f)).toHaveLength(1);
  const disposed = actions(f, OAuthNativeAction.Dispose).length;
  // Another client (or a lost cancel response) canceled the login while the
  // server still reports waiting: handled exactly like an acknowledged cancel.
  f.observeCanceled();
  await screen.findByText(
    copy("grok-subscription.problem.canceling"),
    {},
    { timeout: 3000 },
  );
  addressHidden();
  expect(screen.queryByRole("button", { name: openName() })).toBeNull();
  expect(screen.queryByText(DEVICE_CODE)).toBeNull();
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.device.copyCode"),
    }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", { name: copy("grok-subscription.cancel") }),
  ).toBeNull();
  // The opener was requested, so the opening is disposed exactly once.
  await waitFor(() =>
    expect(actions(f, OAuthNativeAction.Dispose)).toHaveLength(disposed + 1),
  );
  expect(f.cancel).not.toHaveBeenCalled();
}, 20000);

it("ends a definitively refused device request without a retry notice and without native calls", async () => {
  const f = deviceFixture();
  f.login.mockImplementationOnce(() => {
    throw typedFailure(Code.PermissionDenied, FailureCode.PermissionDenied);
  });
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Start Grok device" }));
  await screen.findByText(copy("grok-subscription.status.failed"));
  expect(
    screen.getByText(copy("grok-subscription.problem.requestFailed")),
  ).toBeTruthy();
  expect(
    screen.queryByText(copy("grok-subscription.problem.requestUnconfirmed")),
  ).toBeNull();
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeNull();
  expect(nativeActions(f)).toEqual([]);
});

it.each([
  [State.EXPIRED, "grok-subscription.status.expired"],
  [State.FAILED, "grok-subscription.status.failed"],
  [State.CANCELED, "grok-subscription.status.canceled"],
] as const)(
  "clears the verification address on terminal state %s",
  async (state, status) => {
    const f = deviceFixture();
    await startDevice(f);
    await failVerificationOpen(f);
    f.setState(state);
    await screen.findByText(copy(status), {}, { timeout: 3000 });
    addressHidden();
    expect(screen.queryByText(DEVICE_URL)).toBeNull();
    expect(screen.queryByText(DEVICE_CODE)).toBeNull();
  },
  20000,
);

it("keeps the verification address out of the browser flow and localized in English and Korean", async () => {
  for (const catalog of [english, korean]) {
    for (const key of [
      "addressLabel",
      "addressAria",
      "copyAddress",
      "addressCopied",
      "addressCopyFailed",
    ] as const)
      expect(catalog[`grok-subscription.device.${key}`], key).toBeTruthy();
  }
  expect(korean["grok-subscription.device.copyAddress"]).not.toBe(
    english["grok-subscription.device.copyAddress"],
  );
  // The failure notice points at the address that is now on screen.
  expect(english["grok-subscription.problem.verificationOpenFailed"]).toMatch(
    /address/,
  );
  expect(korean["grok-subscription.problem.verificationOpenFailed"]).toMatch(
    /주소/,
  );
  const f = fixture();
  await start(f);
  expect(screen.queryByRole("textbox", { name: addressName() })).toBeNull();
  expect(window.document.body.textContent).not.toContain("accounts.x.ai");
});

it("replays the exact original naming request after an uncertain failure on a settled login", async () => {
  const f = deviceFixture();
  await startDevice(f);
  f.success();
  const input = await screen.findByLabelText(
    copy("grok-subscription.accountName"),
    {},
    { timeout: 3000 },
  );
  f.save.mockImplementationOnce(() => {
    throw typedFailure(Code.Internal, FailureCode.Internal);
  });
  fireEvent.change(input, { target: { value: "Edited Grok" } });
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.saveName") }),
  );
  // The settled (terminal) login still owns the retry for its naming request.
  fireEvent.click(
    await screen.findByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  );
  await screen.findByRole("button", { name: "Start Grok device" });
  const saves = f.save.mock.calls as unknown as Array<
    [{ mutation: { requestId: string } }]
  >;
  expect(saves).toHaveLength(3);
  expect(saves[2][0].mutation.requestId).toBe(saves[1][0].mutation.requestId);
  expect(document(f.current())).toMatchObject({ alias: "Edited Grok" });
}, 20000);

// Grok Add account availability cells (capability 39 x status read state).
//
//   status read              Add account   starts a login
//   settled, advertised      enabled       yes
//   settled, not advertised  Coming soon   no
//   refetching (stale data)  disabled      no
//   refetch failed           disabled      no
it.each([
  ["while a status refetch is in flight", "hang"],
  ["after a failed status refetch", "fail"],
] as const)(
  "disables Grok Add account from stale capability 39 %s",
  async (_name, mode) => {
    let next: "ok" | "hang" | "fail" = "ok";
    const save = vi.fn((request) => ({
      requestId: request.mutation.requestId,
      resource: create(ResourceSchema, { id: newRequestId() }),
    }));
    const transport = createRouterTransport((router) => {
      router.service(ConfigurationService, { saveConfiguration: save });
      router.service(SystemService, {
        getStatus: () => {
          if (next === "hang") return new Promise(() => undefined);
          if (next === "fail")
            throw typedFailure(Code.Unavailable, FailureCode.Unavailable);
          return {
            capabilities: [
              SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
              SystemCapability.GROK_SUBSCRIPTION_LOGIN_V1,
            ],
          };
        },
      });
      router.service(ResourceService, {
        listResources: () => ({ resources: [] }),
      });
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const native = vi.fn(async () => ({ generation: newRequestId() }));
    const view = render(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <OAuthNativeProvider control={native}>
            <SettingsLifetime>
              {() => (
                <MutationIntents>
                  <SubscriptionAccounts
                    active
                    editAccount={vi.fn()}
                    deleteAccount={vi.fn()}
                  />
                </MutationIntents>
              )}
            </SettingsLifetime>
          </OAuthNativeProvider>
        </QueryClientProvider>
      </TransportProvider>,
    );
    const label = (state: string) =>
      copy("subscription-settings.message_78735b", { v0: "Grok", v1: state });
    const add = label(copy("subscription-settings.addAccount_ee7ee5"));
    await waitFor(async () => {
      const button = await screen.findByRole("button", { name: add });
      expect((button as HTMLButtonElement).disabled).toBe(false);
    });
    next = mode;
    void client.invalidateQueries();
    await waitFor(() => {
      const button = screen.queryByRole("button", { name: add });
      // The enabled Add account action is gone while the capability is stale.
      expect(button === null || (button as HTMLButtonElement).disabled).toBe(
        true,
      );
    });
    const stale = screen.queryByRole("button", { name: add });
    if (stale) fireEvent.click(stale);
    // No account was written and no native browser step started.
    expect(save).not.toHaveBeenCalled();
    expect(native).not.toHaveBeenCalled();
    view.unmount();
    client.clear();
  },
  20000,
);

it.each([[SupportedLanguage.English], [SupportedLanguage.Korean]] as const)(
  "names the real Claude and Grok sign-in state in the connect notice (%s)",
  async (language) => {
    await i18n.changeLanguage(language);
    const claude = SystemCapability.CLAUDE_SERVER_LOGIN_V1,
      grok = SystemCapability.GROK_SUBSCRIPTION_LOGIN_V1;
    // Every cell of Claude capability x Grok capability x native control.
    const cells: [
      string,
      SystemCapability[],
      boolean,
      (
        | "claude-subscription.availability"
        | "claude-subscription.availabilityGrokAvailable"
        | "claude-subscription.serverUpdateRequired"
        | "claude-subscription.serverUpdateRequiredGrokAvailable"
      ),
      boolean?,
    ][] = [
      [
        "claude+grok+native",
        [claude, grok],
        true,
        "claude-subscription.availabilityGrokAvailable",
      ],
      // Grok Add account also needs inventory capability 17, so the notice
      // never claims a sign-in that the disabled row cannot start.
      [
        "claude+grok+native, no inventory",
        [claude, grok],
        true,
        "claude-subscription.availability",
        false,
      ],
      [
        "grok only, no inventory",
        [grok],
        true,
        "claude-subscription.serverUpdateRequired",
        false,
      ],
      [
        "claude+grok, no native",
        [claude, grok],
        false,
        "claude-subscription.availability",
      ],
      ["claude only", [claude], true, "claude-subscription.availability"],
      [
        "grok only",
        [grok],
        true,
        "claude-subscription.serverUpdateRequiredGrokAvailable",
      ],
      [
        "grok only, no native",
        [grok],
        false,
        "claude-subscription.serverUpdateRequired",
      ],
      ["neither", [], true, "claude-subscription.serverUpdateRequired"],
    ];
    for (const [name, extra, withNative, key, withInventory = true] of cells) {
      const transport = createRouterTransport((router) => {
        router.service(SystemService, {
          getStatus: () => ({
            capabilities: [
              ...(withInventory
                ? [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1]
                : []),
              ...extra,
            ],
          }),
        });
        router.service(ResourceService, {
          listResources: () => ({ resources: [] }),
        });
      });
      const client = new QueryClient({
        defaultOptions: { queries: { retry: false } },
      });
      const body = (
        <SettingsLifetime>
          {() => (
            <MutationIntents>
              <SubscriptionAccounts
                active
                editAccount={vi.fn()}
                deleteAccount={vi.fn()}
              />
            </MutationIntents>
          )}
        </SettingsLifetime>
      );
      const view = render(
        <TransportProvider transport={transport}>
          <QueryClientProvider client={client}>
            {withNative ? (
              <OAuthNativeProvider
                control={vi.fn(async () => ({ generation: newRequestId() }))}
              >
                {body}
              </OAuthNativeProvider>
            ) : (
              body
            )}
          </QueryClientProvider>
        </TransportProvider>,
      );
      const notice = await screen.findByText(copy(key), {}, { timeout: 3000 });
      expect(notice.className, name).toBe("subscription-unavailable");
      const grokReady = key.endsWith("GrokAvailable");
      expect(
        copy(key).includes(
          language === SupportedLanguage.English
            ? "not available yet"
            : "아직 지원하지",
        ),
        name,
      ).toBe(!grokReady);
      view.unmount();
      client.clear();
    }
  },
);

it("keeps the Claude-only update notice free of Grok state in English and Korean", () => {
  for (const catalog of [englishClaude, koreanClaude]) {
    expect(
      catalog["claude-subscription.serverUpdateRequiredClaudeOnly"],
    ).not.toMatch(/Grok/);
    // The two Grok-aware notices extend the Claude-only sentence.
    expect(
      catalog["claude-subscription.serverUpdateRequiredGrokAvailable"],
    ).toContain(catalog["claude-subscription.serverUpdateRequiredClaudeOnly"]);
    expect(catalog["claude-subscription.serverUpdateRequired"]).toContain(
      catalog["claude-subscription.serverUpdateRequiredClaudeOnly"],
    );
  }
});

it("starts Grok through the server CLI device flow from the default account action", async () => {
  const f = fixture("https://accounts.x.ai/oauth2/device", true);
  f.setUserCode("ABCD-1234");
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: /^Start Grok$/ }));
  await waitFor(() => expect(f.login).toHaveBeenCalledTimes(1));
  expect(f.login.mock.calls[0][0]).toMatchObject({
    deviceCode: true,
    machineId: "",
  });
  await waitFor(() => expect(screen.getByText("ABCD-1234")).toBeTruthy());
  expect(f.forward).not.toHaveBeenCalled();
});
