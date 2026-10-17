// SPDX-License-Identifier: Apache-2.0
import { useQueryClient } from "@tanstack/react-query";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import {
  AccountTypeFilter,
  EntityKind,
  ResourceQuery,
} from "@delinoio/delidev-api-client";

/** Saved subscription-account list input shared by every inventory owner (the
 * rail, and the invalidation published after a local login change). Keeping
 * one builder keeps the generated query keys identical across owners. */
export function subscriptionInventoryRequest(token: string) {
  return {
    filter: { kind: EntityKind.ACCOUNT, pageSize: 50, pageToken: token },
    accountType: AccountTypeFilter.SUBSCRIPTION,
    providerId: "",
  };
}

/** Returns a publisher that asks every mounted saved subscription-account
 * inventory (Settings and the rail) to reread its already accepted ranges.
 *
 * The key deliberately omits the transport: Settings reads through a
 * per-opening wrapper transport while the rail reads through the application
 * transport, so a transport-bound key would never reach the rail. The query
 * client is owned by one authenticated connection, which keeps the
 * notification inside that connection. Each owner keeps its own authentication,
 * visibility, capability and stopping gates, and unseen pages are never fetched
 * by the notification. */
export function useSubscriptionInventoryInvalidation(): () => void {
  const client = useQueryClient();
  return () => {
    console.info("delidev.subscription.inventory_invalidated");
    void client.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: ResourceQuery.listResources,
        input: subscriptionInventoryRequest(""),
        cardinality: "finite",
      }),
      refetchType: "active",
    });
  };
}
