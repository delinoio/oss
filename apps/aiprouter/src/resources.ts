// SPDX-License-Identifier: Apache-2.0
import { useQuery } from "@tanstack/react-query";
import { useApi, pageSize } from "./api";
import type { ProviderConnection, Pool, Route } from "@aiprouter/client";
// Editors must include dependencies beyond the first list page. Query cancellation
// stops enumeration when the organization boundary unmounts.
export function usePersonalResources(organizationId: string) {
  const api = useApi();
  const connections = useQuery({
    queryKey: ["all-connections", organizationId],
    queryFn: async ({ signal }) => {
      const result: ProviderConnection[] = [];
      let pageToken = "";
      do {
        const r = await api.provider.listProviderConnections(
          { organizationId, page: { pageSize, pageToken } },
          { signal },
        );
        result.push(...r.connections);
        pageToken = r.page?.nextPageToken ?? "";
      } while (pageToken);
      return result;
    },
  });
  const pools = useQuery({
    queryKey: ["all-pools", organizationId],
    queryFn: async ({ signal }) => {
      const result: Pool[] = [];
      let pageToken = "";
      do {
        const r = await api.pool.listPools(
          { organizationId, page: { pageSize, pageToken } },
          { signal },
        );
        result.push(...r.pools);
        pageToken = r.page?.nextPageToken ?? "";
      } while (pageToken);
      return result;
    },
  });
  const routes = useQuery({
    queryKey: ["all-routes", organizationId],
    queryFn: async ({ signal }) => {
      const result: Route[] = [];
      let pageToken = "";
      do {
        const r = await api.routing.listRoutes(
          { organizationId, page: { pageSize, pageToken } },
          { signal },
        );
        result.push(...r.routes);
        pageToken = r.page?.nextPageToken ?? "";
      } while (pageToken);
      return result;
    },
  });
  return { connections, pools, routes };
}
