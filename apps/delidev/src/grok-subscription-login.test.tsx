// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
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
import { grokLoginURL } from "./grok-subscription-login";
import { copy, i18n, SupportedLanguage } from "./localization";
import { SettingsLifetime } from "./settings-lifetime";
import { SubscriptionAccounts } from "./subscription-accounts";
import { useSubscriptionLogin } from "./subscription-login";
import { MutationIntents } from "./mutation";
import english from "./locales/en/grok-subscription.json";
import korean from "./locales/ko/grok-subscription.json";
import englishClaude from "./locales/en/claude-subscription.json";
import koreanClaude from "./locales/ko/claude-subscription.json";

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
    suggestedName: suggested,
    generation,
  }));
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
  function Body() {
    const flow = useSubscriptionLogin(true, changed);
    return (
      <>
        {flow.body ?? (
          <button onClick={() => flow.begin(SubscriptionServiceId.Grok)}>
            Start Grok
          </button>
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
    "grok-subscription.copyLink",
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
        expect(
          actions(f, OAuthNativeAction.GrokSubscriptionOpen),
        ).toHaveLength(1),
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
    ][] = [
      [
        "claude+grok+native",
        [claude, grok],
        true,
        "claude-subscription.availabilityGrokAvailable",
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
    for (const [name, extra, withNative, key] of cells) {
      const transport = createRouterTransport((router) => {
        router.service(SystemService, {
          getStatus: () => ({
            capabilities: [
              SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
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
