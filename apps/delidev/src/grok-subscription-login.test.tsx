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
import {
  grokDeviceVerification,
  grokLoginURL,
} from "./grok-subscription-login";
import { copy, i18n, SupportedLanguage } from "./localization";
import { SettingsLifetime } from "./settings-lifetime";
import { SubscriptionAccounts } from "./subscription-accounts";
import { useSubscriptionLogin } from "./subscription-login";
import { MutationIntents } from "./mutation";
import english from "./locales/en/grok-subscription.json";
import korean from "./locales/ko/grok-subscription.json";

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

function fixture(url = loginURL) {
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
  const progress = vi.fn(() => ({
    state,
    url: state === State.WAITING ? url : "",
    userCode: state === State.WAITING ? userCode : "",
    suggestedName: suggested,
    generation,
  }));
  const cancel = vi.fn(() => ({ account: current }));
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
  function Body() {
    const flow = useSubscriptionLogin(true, changed);
    return (
      <>
        {flow.body ?? (
          <>
            <button onClick={() => flow.begin(SubscriptionServiceId.Grok)}>
              Start Grok
            </button>
            <button
              onClick={() =>
                flow.begin(
                  SubscriptionServiceId.Grok,
                  undefined,
                  false,
                  false,
                  true,
                )
              }
            >
              Start Grok device
            </button>
          </>
        )}
      </>
    );
  }
  const view = () => (
    <StrictMode>
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <OAuthNativeProvider control={native}>
            <SettingsLifetime>{() => <Body />}</SettingsLifetime>
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
    current: () => current,
    setState: (value: State) => {
      state = value;
    },
    setUserCode: (value: string) => {
      userCode = value;
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
  fireEvent.click(
    screen.getByRole("button", { name: copy("grok-subscription.openBrowser") }),
  );
  await waitFor(() =>
    expect(actions(f, OAuthNativeAction.GrokSubscriptionReopen)).toHaveLength(
      1,
    ),
  );
  expect(actions(f, OAuthNativeAction.GrokSubscriptionOpen)).toHaveLength(1);
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
  expect(
    screen.getByText(copy("grok-subscription.problem.requestUnconfirmed")),
  ).toBeTruthy();
  expect(
    screen.queryByRole("button", {
      name: copy("grok-subscription.retryOriginal"),
    }),
  ).toBeNull();
  expect(f.login).toHaveBeenCalledTimes(1);
  expect(f.progress).not.toHaveBeenCalled();
});

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
    () => expect(f.progress.mock.calls.length).toBeGreaterThanOrEqual(before + 2),
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
    "grok-subscription.device.codeAria",
    "grok-subscription.device.codeCopied",
    "grok-subscription.device.codeCopyFailed",
    "grok-subscription.device.codeLabel",
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
