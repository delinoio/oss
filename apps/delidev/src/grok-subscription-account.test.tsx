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
import { copy } from "./localization";

configure({ asyncUtilTimeout: 4000 });

function grokAccount(connected: boolean) {
  return create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.ACCOUNT,
    revision: 3n,
    schemaVersion: 2,
    documentJson: encode({
      alias: "Grok",
      type: "subscription",
      subscription_service: "grok",
      enabled: true,
      health: connected ? "ready" : "disconnected",
      ...(connected ? { connection: { id: newRequestId() } } : {}),
      subscription: connected
        ? {
            generation: newRequestId(),
            identity_commitment: "ab".repeat(32),
          }
        : {},
    }),
  });
}
function mount(capabilities: SystemCapability[], account = grokAccount(true)) {
  const lifecycle = vi.fn((request) => ({
    account,
    operationId: request.mutation.requestId,
  }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities }) });
    router.service(ResourceService, {
      getResource: () => ({ resource: account }),
    });
    router.service(SubscriptionService, { requestSubscription: lifecycle });
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
  return { lifecycle, account };
}
const refreshLogin = () =>
  screen.getByRole("button", {
    name: copy("subscription-accounts.refreshLogin_85188a"),
  }) as HTMLButtonElement;
const logOut = () =>
  screen.getByRole("button", {
    name: copy("subscription-accounts.logOut_496161"),
  }) as HTMLButtonElement;

it("enables Refresh login and Log out for a connected Grok account under capability 39", async () => {
  const value = mount([
    SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
    SystemCapability.GROK_SUBSCRIPTION_LOGIN_V1,
  ]);
  await waitFor(() => expect(refreshLogin().disabled).toBe(false));
  expect(logOut().disabled).toBe(false);
  // Neither the generic unsupported-service remediation nor the Grok guidance
  // appears for a negotiated lifecycle.
  expect(
    screen.queryByText(
      copy("account-connection.inline.subscriptionUnsupported"),
    ),
  ).toBeNull();
  expect(
    screen.queryByText(copy("grok-subscription.lifecycle.unavailable")),
  ).toBeNull();
  fireEvent.click(refreshLogin());
  await waitFor(() => expect(value.lifecycle).toHaveBeenCalledTimes(1));
  expect(value.lifecycle.mock.calls[0][0].action).toBe(
    SubscriptionAction.REFRESH,
  );
  expect(value.lifecycle.mock.calls[0][0].machineId).toBe("");
  await waitFor(() => expect(logOut().disabled).toBe(false));
  fireEvent.click(logOut());
  fireEvent.click(
    await screen.findByRole("button", {
      name: copy("subscription-accounts.confirmLogout_2e9d08"),
    }),
  );
  await waitFor(() => expect(value.lifecycle).toHaveBeenCalledTimes(2));
  const request = value.lifecycle.mock.calls[1][0];
  expect(request.action).toBe(SubscriptionAction.LOGOUT);
  expect(request.machineId).toBe("");
});

it("keeps ChatGPT and Claude capabilities from enabling a Grok lifecycle and names the Grok guidance", async () => {
  const value = mount([
    SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
    SystemCapability.SERVER_SUBSCRIPTION_LOGIN_V1,
    SystemCapability.CLAUDE_SUBSCRIPTIONS_V1,
    SystemCapability.CLAUDE_SERVER_LOGIN_V1,
  ]);
  await screen.findByText(copy("grok-subscription.lifecycle.unavailable"));
  expect(refreshLogin().disabled).toBe(true);
  expect(logOut().disabled).toBe(true);
  // The Grok guidance replaces the generic unsupported-service remediation.
  expect(
    screen.queryByText(
      copy("account-connection.inline.subscriptionUnsupported"),
    ),
  ).toBeNull();
  expect(
    screen.getByRole("button", {
      name: copy("account-connection.inline.supportRecheck"),
    }),
  ).toBeTruthy();
  fireEvent.click(refreshLogin());
  fireEvent.click(logOut());
  expect(value.lifecycle).not.toHaveBeenCalled();
});

it("offers no sign-in action on an existing disconnected Grok account", async () => {
  mount(
    [
      SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
      SystemCapability.GROK_SUBSCRIPTION_LOGIN_V1,
    ],
    grokAccount(false),
  );
  await screen.findByText(/Disconnected/);
  await waitFor(() =>
    expect(
      screen.queryByText(copy("grok-subscription.lifecycle.unavailable")),
    ).toBeNull(),
  );
  expect(screen.queryByRole("button", { name: /^Sign in to/ })).toBeNull();
  expect(
    screen.queryByRole("button", {
      name: copy("subscription-accounts.refreshLogin_85188a"),
    }),
  ).toBeNull();
});
