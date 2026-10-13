// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
        <OAuthNativeProvider control={vi.fn()}>
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
  fireEvent.click(await screen.findByRole("button", { name: "Confirm logout" }));
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
