// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  configure,
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
  SubscriptionAction,
  SubscriptionService,
  SystemCapability,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { OAuthNativeProvider } from "./account-oauth";
import { ManagedSubscriptionAccount } from "./subscription-accounts";
import { MutationIntents } from "./mutation";
import { SettingsLifetime } from "./settings-lifetime";
import { encode } from "./documents";

// The one-second poll cadence is slower than the default async-utility wait.
configure({ asyncUtilTimeout: 4000 });

function connectedAccount() {
  return create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.ACCOUNT,
    revision: 3n,
    schemaVersion: 2,
    documentJson: encode({
      alias: "Server Claude",
      type: "subscription",
      subscription_service: "claude",
      enabled: true,
      health: "ready",
      connection: { id: newRequestId() },
      subscription: {
        claude_credential: "server-token",
        generation: newRequestId(),
        identity_commitment: "ab".repeat(32),
      },
    }),
  });
}
function disconnectedAccount(subscription: Record<string, unknown>) {
  return create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.ACCOUNT,
    revision: 4n,
    schemaVersion: 2,
    documentJson: encode({
      alias: "Server Claude",
      type: "subscription",
      subscription_service: "claude",
      enabled: true,
      health: "disconnected",
      subscription: { claude_credential: "server-token", ...subscription },
    }),
  });
}
function mount(capabilities: SystemCapability[], account = connectedAccount()) {
  const logout = vi.fn((request) => ({
    account,
    operationId: request.mutation.requestId,
  }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities }) });
    router.service(ResourceService, {
      getResource: () => ({ resource: account }),
    });
    router.service(SubscriptionService, { requestSubscription: logout });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <OAuthNativeProvider control={vi.fn(async () => ({}) as never)}>
          <SettingsLifetime>
            {() => (
              <MutationIntents>
                <ManagedSubscriptionAccount
                  initial={account}
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
  return { logout, account };
}

it("states token limits, offers no reauthentication and logs out without a Runner Device", async () => {
  const value = mount([
    SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
    SystemCapability.CLAUDE_SERVER_LOGIN_V1,
  ]);
  await screen.findByText(/The token expires, cannot use Remote Control/);
  expect(screen.queryByRole("button", { name: "Sign in again" })).toBeNull();
  expect(screen.queryByText(/Owner Runner Device/)).toBeNull();
  const out = screen.getByRole("button", { name: "Log out" });
  await waitFor(() => expect((out as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(out);
  fireEvent.click(
    await screen.findByRole("button", { name: "Confirm logout" }),
  );
  await waitFor(() => expect(value.logout).toHaveBeenCalled());
  const request = value.logout.mock.calls[0][0];
  expect(request.action).toBe(SubscriptionAction.LOGOUT);
  expect(request.machineId).toBe("");
});

it("shows update guidance when the server lacks the managed Claude capability", async () => {
  mount([SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1]);
  await screen.findByText(/The token expires, cannot use Remote Control/);
  await screen.findByRole("button", { name: /Check support again|Recheck/i });
  expect(screen.queryByRole("button", { name: "Sign in again" })).toBeNull();
});

it("connects a new account instead of offering Sign in on a logged-out server-token account", async () => {
  const loggedOut = disconnectedAccount({
    server_operation: {
      id: newRequestId(),
      action: "logout",
      state: "succeeded",
    },
  });
  const value = mount(
    [
      SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
      SystemCapability.CLAUDE_SERVER_LOGIN_V1,
    ],
    loggedOut,
  );
  await screen.findByText(/The token expires, cannot use Remote Control/);
  expect(screen.queryByRole("button", { name: /^Sign in to/ })).toBeNull();
  const connect = await screen.findByRole("button", {
    name: "Connect new account",
  });
  await waitFor(() =>
    expect((connect as HTMLButtonElement).disabled).toBe(false),
  );
  // Selecting the action never submits a login for the logged-out account.
  fireEvent.click(connect);
  expect(value.logout).not.toHaveBeenCalled();
});

it("keeps Sign in for a server-token account whose first login did not connect", async () => {
  mount(
    [
      SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
      SystemCapability.CLAUDE_SERVER_LOGIN_V1,
    ],
    disconnectedAccount({
      server_operation: {
        id: newRequestId(),
        action: "login",
        state: "failed",
      },
    }),
  );
  await screen.findByText(/The token expires, cannot use Remote Control/);
  await screen.findByRole("button", { name: /^Sign in to/ });
  expect(
    screen.queryByRole("button", { name: "Connect new account" }),
  ).toBeNull();
});

function deviceOwnedAccount(connected: boolean) {
  return create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.ACCOUNT,
    revision: 4n,
    schemaVersion: 2,
    documentJson: encode({
      alias: "Device Claude",
      type: "subscription",
      subscription_service: "claude",
      enabled: true,
      health: connected ? "ready" : "disconnected",
      ...(connected ? { connection: { id: newRequestId() } } : {}),
      subscription: {
        owner_machine_id: newRequestId(),
        native_profile_id: newRequestId(),
        generation: newRequestId(),
        native_operation: {
          id: newRequestId(),
          action: connected ? "login" : "logout",
          state: "succeeded",
        },
      },
    }),
  });
}

it("connects a new server-owned account for a logged-out device-owned account without a Runner wizard", async () => {
  // A device-owned logout clears the profile and owner but keeps the native
  // operation, so the server refuses to convert it into a server-token account.
  const value = mount(
    [
      SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
      SystemCapability.CLAUDE_SUBSCRIPTIONS_V1,
      SystemCapability.CLAUDE_SERVER_LOGIN_V1,
    ],
    deviceOwnedAccount(false),
  );
  const connect = await screen.findByRole("button", {
    name: "Connect new account",
  });
  await waitFor(() =>
    expect((connect as HTMLButtonElement).disabled).toBe(false),
  );
  expect(screen.queryByRole("button", { name: /^Sign in to/ })).toBeNull();
  expect(screen.queryByRole("button", { name: "Sign in again" })).toBeNull();
  expect(screen.queryByText("Select a Runner Device")).toBeNull();
  expect(value.logout).not.toHaveBeenCalled();
});

it("disables reconnection with server-update guidance when capability 67 is absent", async () => {
  const value = mount(
    [
      SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
      SystemCapability.CLAUDE_SUBSCRIPTIONS_V1,
    ],
    deviceOwnedAccount(false),
  );
  const connect = await screen.findByRole("button", {
    name: "Connect new account",
  });
  expect((connect as HTMLButtonElement).disabled).toBe(true);
  await screen.findByText(/Claude sign-in needs a server update/);
  expect(screen.queryByRole("button", { name: /^Sign in to/ })).toBeNull();
  expect(screen.queryByText("Select a Runner Device")).toBeNull();
  expect(value.logout).not.toHaveBeenCalled();
});

it("keeps a connected device-owned account manageable without Runner reauthentication", async () => {
  const value = mount(
    [
      SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
      SystemCapability.CLAUDE_SUBSCRIPTIONS_V1,
      SystemCapability.CLAUDE_SERVER_LOGIN_V1,
    ],
    deviceOwnedAccount(true),
  );
  const out = await screen.findByRole("button", { name: "Log out" });
  await waitFor(() => expect((out as HTMLButtonElement).disabled).toBe(false));
  expect(screen.queryByRole("button", { name: "Sign in again" })).toBeNull();
  expect(
    screen.queryByRole("button", { name: /Refresh login|Reauthenticate/i }),
  ).toBeNull();
  expect(screen.queryByRole("button", { name: /^Sign in to/ })).toBeNull();
  // Logout keeps its original Runner binding.
  fireEvent.click(out);
  fireEvent.click(
    await screen.findByRole("button", { name: "Confirm logout" }),
  );
  await waitFor(() => expect(value.logout).toHaveBeenCalled());
  const request = value.logout.mock.calls[0][0];
  expect(request.action).toBe(SubscriptionAction.LOGOUT);
  expect(request.machineId).not.toBe("");
});

it("starts the server flow for a never-used Claude account", async () => {
  const fresh = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.ACCOUNT,
    revision: 1n,
    schemaVersion: 2,
    documentJson: encode({
      alias: "Fresh Claude",
      type: "subscription",
      subscription_service: "claude",
      enabled: true,
      health: "disconnected",
    }),
  });
  const value = mount(
    [
      SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
      SystemCapability.CLAUDE_SUBSCRIPTIONS_V1,
      SystemCapability.CLAUDE_SERVER_LOGIN_V1,
    ],
    fresh,
  );
  const signIn = await screen.findByRole("button", { name: /^Sign in to/ });
  await waitFor(() =>
    expect((signIn as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(signIn);
  await waitFor(() => expect(value.logout).toHaveBeenCalled());
  const request = value.logout.mock.calls[0][0];
  expect(request.action).toBe(SubscriptionAction.LOGIN);
  expect(request.machineId).toBe("");
});
