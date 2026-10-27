// SPDX-License-Identifier: Apache-2.0
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, rs, afterEach } from "@rstest/core";
import {
  createRouterTransport,
  Code,
  ConnectError,
  type ConnectRouter,
} from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { QueryClient } from "@tanstack/react-query";
import {
  AccountService,
  OrganizationService,
  ProviderService,
  PoolService,
  RoutingService,
  OrganizationSchema,
  GetSessionResponseSchema,
  Role,
  ProviderAuthKind,
  ProviderOAuthMethod,
  OAuthState,
} from "@aiprouter/client";
import { ApiProvider } from "../src/api";
import { ProvidersPage } from "../src/providers";
import { App } from "../src/app";
import type { ReactNode } from "react";

const organization = create(OrganizationSchema, {
  id: "one",
  name: "Studio",
  role: Role.MEMBER,
});
const session = create(GetSessionResponseSchema, {
  userId: "person",
  displayName: "Alex",
});
const props = { organization, session };
const expiresAt = { seconds: BigInt(Date.UTC(2026, 9, 27, 14, 10) / 1000) };

type Authorization = Record<string, unknown>;

function deferred<T = void>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function mount(ui: ReactNode, routes: (r: ConnectRouter) => void) {
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const transport = createRouterTransport(routes);
  return render(
    <ApiProvider queryClient={cache} rpcTransport={transport}>
      {ui}
    </ApiProvider>,
  );
}

interface Options {
  method?: ProviderOAuthMethod;
  authorization?: Authorization;
  current?: (() => { authorization: Authorization }) | undefined;
  complete?: (request: unknown) => Promise<object> | object;
  cancel?: (request: unknown) => Promise<object> | object;
}

// Opens the real OAuth setup flow so the dialog is mounted by ProvidersPage.
async function openDialog(options: Options = {}) {
  const user = userEvent.setup();
  const authorization = {
    id: "auth",
    state: OAuthState.PENDING,
    authorizationUrl: "https://provider.example.test/login",
    callbackState: "safe-state",
    expiresAt,
    ...options.authorization,
  };
  const complete = rs.fn(options.complete ?? (() => ({})));
  const cancel = rs.fn(options.cancel ?? (() => ({})));
  const getProviderOAuth = rs.fn(
    options.current ?? (() => ({ authorization })),
  );
  mount(<ProvidersPage {...props} />, (r) => {
    r.service(ProviderService, {
      listProviderConnections: () => ({ connections: [] }),
      listProviderCatalog: () => ({
        providers: [
          {
            id: "catalog-provider",
            displayName: "Catalog provider",
            authKinds: [ProviderAuthKind.OAUTH],
            oauthMethods: options.method ? [options.method] : [],
          },
        ],
      }),
      beginProviderOAuth: () => ({ authorization }),
      getProviderOAuth,
      completeProviderOAuth: (request) => complete(request),
      cancelProviderOAuth: (request) => cancel(request),
    });
    r.service(PoolService, { listPools: () => ({ pools: [] }) });
    r.service(RoutingService, { listRoutes: () => ({ routes: [] }) });
  });
  await user.click(screen.getByRole("button", { name: "Connect provider" }));
  await user.selectOptions(
    screen.getByLabelText("Provider"),
    "catalog-provider",
  );
  await user.type(screen.getByLabelText("Account label"), "Personal login");
  await user.click(screen.getByRole("button", { name: "Continue with OAuth" }));
  await user.click(screen.getByRole("button", { name: "Start authorization" }));
  const dialog = await screen.findByRole("dialog", {
    name: "Provider authorization",
  });
  return { user, dialog, complete, cancel, getProviderOAuth };
}

const CODE_LABEL = "Authorization code, token, or callback URL";
afterEach(() => window.history.replaceState(null, "", "/"));

describe("OAuth dialog header and manual variant", () => {
  it("shows the named heading, text state, UTC expiry and the persistent close notice", async () => {
    const { dialog } = await openDialog({ method: ProviderOAuthMethod.PKCE });
    expect(
      within(dialog).getByRole("heading", { name: "Provider authorization" }),
    ).toBeTruthy();
    expect(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    ).toBeTruthy();
    expect(within(dialog).getByText("Pending")).toBeTruthy();
    expect(within(dialog).getByText(/Expires Oct 27, 2026.*UTC$/)).toBeTruthy();
    expect(
      within(dialog).getByText(
        "Closing this dialog does not cancel authorization.",
      ),
    ).toBeTruthy();
  });

  it("renders the two manual steps with a safe link, a focused masked input and no state field", async () => {
    const { dialog } = await openDialog({ method: ProviderOAuthMethod.PKCE });
    expect(
      within(dialog).getByRole("heading", {
        name: "Sign in with your provider",
      }),
    ).toBeTruthy();
    const link = within(dialog).getByRole("link", {
      name: "Open provider sign-in",
    });
    expect(link.getAttribute("href")).toBe(
      "https://provider.example.test/login",
    );
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
    expect(
      within(dialog).getByRole("heading", { name: "Finish authorization" }),
    ).toBeTruthy();
    const input = within(dialog).getByLabelText(CODE_LABEL);
    expect(input.getAttribute("type")).toBe("password");
    expect(input.getAttribute("autocomplete")).toBe("off");
    expect(document.activeElement).toBe(input);
    expect(within(dialog).queryByLabelText("Authorization state")).toBeNull();
    expect(
      within(dialog).getByRole("button", { name: "Complete authorization" }),
    ).toHaveProperty("disabled", true);
    expect(
      within(dialog).getByText(
        "Cancel authorization stops this attempt. Close leaves it pending.",
      ),
    ).toBeTruthy();
  });

  it("asks for the state only when the server sent none and gates completion on both values", async () => {
    const { user, dialog } = await openDialog({
      method: ProviderOAuthMethod.NATIVE_CALLBACK,
      authorization: { callbackState: undefined },
    });
    const complete = within(dialog).getByRole("button", {
      name: "Complete authorization",
    });
    await user.type(within(dialog).getByLabelText(CODE_LABEL), "code");
    expect(complete).toHaveProperty("disabled", true);
    await user.type(within(dialog).getByLabelText("Authorization state"), "s");
    expect(complete).toHaveProperty("disabled", false);
  });

  it.each([
    ["javascript:alert(1)"],
    ["data:text/html,hello"],
    ["http://provider.example.test/login"],
    ["not a url"],
  ])(
    "renders no sign-in link for %s and explains why",
    async (authorizationUrl) => {
      const { dialog } = await openDialog({
        method: ProviderOAuthMethod.PKCE,
        authorization: { authorizationUrl },
      });
      expect(within(dialog).queryByRole("link")).toBeNull();
      expect(
        within(dialog).getByText(/sign-in address is missing or not approved/),
      ).toBeTruthy();
    },
  );

  it.each([["http://localhost:8080/cb"], ["http://127.0.0.1:9/cb"]])(
    "keeps loopback HTTP link %s",
    async (authorizationUrl) => {
      const { dialog } = await openDialog({
        method: ProviderOAuthMethod.PKCE,
        authorization: { authorizationUrl },
      });
      expect(
        within(dialog)
          .getByRole("link", { name: "Open provider sign-in" })
          .getAttribute("href"),
      ).toBe(authorizationUrl);
    },
  );

  it("submits the existing parser output once and shows the success on the page", async () => {
    const pending = deferred<object>();
    const { user, dialog, complete } = await openDialog({
      method: ProviderOAuthMethod.PKCE,
      complete: () => pending.promise,
    });
    await user.type(
      within(dialog).getByLabelText(CODE_LABEL),
      "http://127.0.0.1:9999/callback?code=one-time&state=safe-state",
    );
    const button = within(dialog).getByRole("button", {
      name: "Complete authorization",
    });
    await user.click(button);
    await user.click(button);
    expect(complete).toHaveBeenCalledOnce();
    expect(complete.mock.calls[0][0]).toMatchObject({
      organizationId: "one",
      authorizationId: "auth",
      code: "one-time",
      state: "safe-state",
    });
    expect(screen.queryByText("Provider connected.")).toBeNull();
    pending.resolve({});
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await screen.findByText("Provider connected.")).toBeTruthy();
  });

  it("rejects a mismatched callback before any RPC and keeps the typed value", async () => {
    const { user, dialog, complete } = await openDialog({
      method: ProviderOAuthMethod.PKCE,
    });
    const input = within(dialog).getByLabelText(CODE_LABEL);
    await user.type(input, "https://cb.example.test/?code=x&state=wrong");
    await user.click(
      within(dialog).getByRole("button", { name: "Complete authorization" }),
    );
    expect(complete).not.toHaveBeenCalled();
    expect(
      await within(dialog).findByText(
        /could not be completed|check the entered values/i,
      ),
    ).toBeTruthy();
    expect(screen.getByRole("dialog")).toBe(dialog);
    expect((input as HTMLInputElement).value).toContain("state=wrong");
    expect(input.getAttribute("aria-describedby")).toContain("-error");
  });

  it("accepts a bare code and preserves a trimmed native payload", async () => {
    const native = "zed://login?token=private-token";
    const { user, dialog, complete } = await openDialog({
      method: ProviderOAuthMethod.NATIVE_CALLBACK,
    });
    await user.type(within(dialog).getByLabelText(CODE_LABEL), `  ${native}  `);
    await user.click(
      within(dialog).getByRole("button", { name: "Complete authorization" }),
    );
    await waitFor(() => expect(complete).toHaveBeenCalledOnce());
    expect(complete.mock.calls[0][0]).toMatchObject({
      code: native,
      state: "safe-state",
    });
  });

  it("keeps the dialog and typed code after a rejected completion without replaying it", async () => {
    const { user, dialog, complete } = await openDialog({
      method: ProviderOAuthMethod.PKCE,
      complete: () => {
        throw new ConnectError("denied", Code.Unavailable);
      },
    });
    const input = within(dialog).getByLabelText(CODE_LABEL);
    await user.type(input, "bare-code");
    await user.click(
      within(dialog).getByRole("button", { name: "Complete authorization" }),
    );
    expect(await within(dialog).findByRole("alert")).toBeTruthy();
    expect(complete).toHaveBeenCalledOnce();
    expect((input as HTMLInputElement).value).toBe("bare-code");
    expect(screen.queryByText("Provider connected.")).toBeNull();
  });

  it("reports the final outcome on the page when the dialog was closed mid-request", async () => {
    const pending = deferred<object>();
    const { user, dialog } = await openDialog({
      method: ProviderOAuthMethod.PKCE,
      complete: () => pending.promise,
    });
    await user.type(within(dialog).getByLabelText(CODE_LABEL), "bare-code");
    await user.click(
      within(dialog).getByRole("button", { name: "Complete authorization" }),
    );
    await user.click(within(dialog).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(
      screen.getByText(
        "Saving… Closing this dialog does not cancel a submitted change.",
      ),
    ).toBeTruthy();
    pending.resolve({});
    expect(await screen.findByText("Provider connected.")).toBeTruthy();
  });

  it("shows a closed-mid-request failure on the page", async () => {
    const pending = deferred<object>();
    const { user, dialog } = await openDialog({
      method: ProviderOAuthMethod.PKCE,
      complete: () => pending.promise,
    });
    await user.type(within(dialog).getByLabelText(CODE_LABEL), "bare-code");
    await user.click(
      within(dialog).getByRole("button", { name: "Complete authorization" }),
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    );
    pending.reject(new ConnectError("no", Code.Unavailable));
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText("Provider connected.")).toBeNull();
  });
});

describe("OAuth dialog polling variant", () => {
  it.each([
    ["DEVICE method", { method: ProviderOAuthMethod.DEVICE }, {}],
    ["BROWSER_POLL method", { method: ProviderOAuthMethod.BROWSER_POLL }, {}],
    [
      "advertised interval",
      { method: ProviderOAuthMethod.PKCE },
      { pollIntervalSeconds: 7 },
    ],
  ])(
    "shows the waiting status without manual controls for %s",
    async (_n, base, extra) => {
      const { dialog } = await openDialog({
        ...base,
        authorization: { userCode: "ABCD-1234", ...extra },
      });
      expect(
        within(dialog).getByText(
          "Waiting for provider approval. This page checks the provider at its current polling interval.",
        ),
      ).toBeTruthy();
      expect(within(dialog).getByText("ABCD-1234")).toBeTruthy();
      expect(
        within(dialog).getByRole("link", { name: "Open provider sign-in" }),
      ).toBeTruthy();
      expect(within(dialog).queryByLabelText(CODE_LABEL)).toBeNull();
      expect(
        within(dialog).queryByRole("button", {
          name: "Complete authorization",
        }),
      ).toBeNull();
      expect(within(dialog).queryByRole("progressbar")).toBeNull();
      expect(within(dialog).queryByText(/%/)).toBeNull();
      expect(
        within(dialog).getByRole("button", { name: "Cancel authorization" }),
      ).toBeTruthy();
    },
  );

  it("omits the user code when none is returned", async () => {
    const { dialog } = await openDialog({ method: ProviderOAuthMethod.DEVICE });
    expect(within(dialog).queryByText(/Enter this code/)).toBeNull();
  });

  it("stops claiming healthy polling after a query error and retries on request", async () => {
    let calls = 0;
    const { user, dialog, getProviderOAuth } = await openDialog({
      method: ProviderOAuthMethod.DEVICE,
      current: () => {
        calls += 1;
        if (calls === 1) throw new ConnectError("down", Code.Unavailable);
        return {
          authorization: {
            id: "auth",
            state: OAuthState.PENDING,
            expiresAt,
          },
        };
      },
    });
    expect(
      await within(dialog).findByText(/Automatic status checks have stopped/),
    ).toBeTruthy();
    expect(
      within(dialog).queryByText(/Waiting for provider approval/),
    ).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "Try again" }));
    await waitFor(() =>
      expect(
        within(dialog).getByText(/Waiting for provider approval/),
      ).toBeTruthy(),
    );
    expect(getProviderOAuth).toHaveBeenCalledTimes(2);
  });
});

describe("OAuth dialog dismissal and cancellation", () => {
  it("never cancels on Close, the close icon or Escape", async () => {
    for (const dismiss of [
      (d: HTMLElement) => within(d).getByRole("button", { name: "Close" }),
      (d: HTMLElement) =>
        within(d).getByRole("button", { name: "Close dialog" }),
    ]) {
      const { user, dialog, cancel } = await openDialog({
        method: ProviderOAuthMethod.PKCE,
      });
      await user.click(dismiss(dialog));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(cancel).not.toHaveBeenCalled();
      document.body.innerHTML = "";
    }
    const { dialog, cancel } = await openDialog({
      method: ProviderOAuthMethod.PKCE,
    });
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(cancel).not.toHaveBeenCalled();
  });

  it("calls cancel once, reports only after confirmation and closes", async () => {
    const pending = deferred<object>();
    const { user, dialog, cancel } = await openDialog({
      method: ProviderOAuthMethod.PKCE,
      cancel: () => pending.promise,
    });
    const button = within(dialog).getByRole("button", {
      name: "Cancel authorization",
    });
    await user.click(button);
    await user.click(button);
    expect(cancel).toHaveBeenCalledOnce();
    expect(cancel.mock.calls[0][0]).toMatchObject({
      organizationId: "one",
      authorizationId: "auth",
    });
    expect(button).toHaveProperty("disabled", true);
    expect(screen.queryByText("Authorization canceled.")).toBeNull();
    pending.resolve({});
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await screen.findByText("Authorization canceled.")).toBeTruthy();
  });

  it("keeps the dialog and typed code when cancellation is rejected", async () => {
    const { user, dialog } = await openDialog({
      method: ProviderOAuthMethod.PKCE,
      cancel: () => {
        throw new ConnectError("no", Code.Unavailable);
      },
    });
    const input = within(dialog).getByLabelText(CODE_LABEL);
    await user.type(input, "keep-me");
    await user.click(
      within(dialog).getByRole("button", { name: "Cancel authorization" }),
    );
    expect(await within(dialog).findByRole("alert")).toBeTruthy();
    expect(screen.queryByText("Authorization canceled.")).toBeNull();
    expect((input as HTMLInputElement).value).toBe("keep-me");
  });
});

describe("OAuth dialog terminal states", () => {
  it.each([
    [
      "completed with a connection",
      { state: OAuthState.COMPLETED, connectionId: "c" },
      "Completed",
      "Provider connected. Your connection is ready in this organization.",
    ],
    [
      "completed without a connection",
      { state: OAuthState.COMPLETED },
      "Completed",
      /no connection was reported/,
    ],
    [
      "canceled",
      { state: OAuthState.CANCELED },
      "Canceled",
      /authorization was canceled/,
    ],
    [
      "expired",
      { state: OAuthState.EXPIRED },
      "Expired",
      /expired before it finished/,
    ],
    [
      "failed",
      { state: OAuthState.FAILED },
      "Failed",
      /could not complete this authorization/,
    ],
    [
      "unspecified",
      { state: OAuthState.UNSPECIFIED },
      "Unknown status",
      /state this console does not recognize/,
    ],
    [
      "unknown future value",
      { state: 99 },
      "Unknown status",
      /state this console does not recognize/,
    ],
  ])(
    "shows distinct safe text for %s with only Done",
    async (_n, authorization, badge, text) => {
      const { dialog } = await openDialog({
        method: ProviderOAuthMethod.PKCE,
        authorization,
      });
      expect(within(dialog).getByText(badge)).toBeTruthy();
      expect(within(dialog).getByText(text)).toBeTruthy();
      if (!(authorization as { connectionId?: string }).connectionId)
        expect(within(dialog).queryByText(/connection is ready/)).toBeNull();
      for (const name of [
        "Complete authorization",
        "Cancel authorization",
        "Close",
      ])
        expect(within(dialog).queryByRole("button", { name })).toBeNull();
      expect(within(dialog).queryByLabelText(CODE_LABEL)).toBeNull();
      expect(
        within(dialog).queryByText(/does not cancel authorization/),
      ).toBeNull();
      expect(within(dialog).getByRole("button", { name: "Done" })).toBeTruthy();
    },
  );

  it("refreshes and closes on Done", async () => {
    const { user, dialog, getProviderOAuth } = await openDialog({
      method: ProviderOAuthMethod.PKCE,
      authorization: { state: OAuthState.CANCELED },
    });
    await user.click(within(dialog).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(getProviderOAuth).toHaveBeenCalled();
  });

  it("moves from pending to a terminal state using the server record", async () => {
    const { dialog } = await openDialog({
      method: ProviderOAuthMethod.DEVICE,
      current: () => ({
        authorization: {
          id: "auth",
          state: OAuthState.EXPIRED,
          expiresAt,
        },
      }),
    });
    expect(await within(dialog).findByText("Expired")).toBeTruthy();
    expect(
      within(dialog).queryByRole("button", { name: "Cancel authorization" }),
    ).toBeNull();
  });
});

describe("OAuth dialog scope loss", () => {
  it("discards the dialog and typed secret when the organization changes", async () => {
    window.history.replaceState(null, "", "/providers?organization=one");
    const user = userEvent.setup();
    const second = { id: "two", name: "Second", role: Role.MEMBER };
    const authorization = {
      id: "auth",
      state: OAuthState.PENDING,
      authorizationUrl: "https://provider.example.test/login",
      callbackState: "safe-state",
      expiresAt,
    };
    mount(<App />, (r) => {
      r.service(AccountService, { getSession: () => session });
      r.service(OrganizationService, {
        listOrganizations: () => ({ organizations: [organization, second] }),
        getOrganization: (req) => ({
          organization: req.organizationId === "one" ? organization : second,
        }),
      });
      r.service(ProviderService, {
        listProviderConnections: () => ({ connections: [] }),
        listProviderCatalog: () => ({
          providers: [
            {
              id: "catalog-provider",
              displayName: "Catalog provider",
              authKinds: [ProviderAuthKind.OAUTH],
            },
          ],
        }),
        beginProviderOAuth: () => ({ authorization }),
        getProviderOAuth: () => ({ authorization }),
      });
      r.service(PoolService, { listPools: () => ({ pools: [] }) });
      r.service(RoutingService, { listRoutes: () => ({ routes: [] }) });
    });
    await user.click(
      await screen.findByRole("button", { name: "Connect provider" }),
    );
    await user.selectOptions(
      screen.getByLabelText("Provider"),
      "catalog-provider",
    );
    await user.type(screen.getByLabelText("Account label"), "Login");
    await user.click(
      screen.getByRole("button", { name: "Continue with OAuth" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Start authorization" }),
    );
    await user.type(await screen.findByLabelText(CODE_LABEL), "secret-code");
    await user.click(
      screen.getAllByRole("button", { name: "Switch organization" })[0]!,
    );
    await user.click(screen.getByRole("button", { name: "Second Member" }));
    const discard = screen.queryByRole("button", {
      name: "Discard and switch",
    });
    if (discard) await user.click(discard);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.queryByDisplayValue("secret-code")).toBeNull();
    expect(document.body.textContent).not.toContain("secret-code");
  });
});
