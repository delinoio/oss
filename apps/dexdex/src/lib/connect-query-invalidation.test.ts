import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { RepositoryService } from "../gen/v1/dexdex_pb";
import { listRepositories } from "../gen/v1/dexdex-RepositoryService_connectquery";
import {
  createConnectQueryServiceKey,
  invalidateConnectQueryServiceQueries,
} from "./connect-query-invalidation";

describe("connect-query-invalidation", () => {
  it("builds identical keys from service descriptor and service name", () => {
    expect(createConnectQueryServiceKey(RepositoryService)).toEqual(
      createConnectQueryServiceKey("dexdex.v1.RepositoryService"),
    );
  });

  it("uses a service key compatible with connect-query method keys", () => {
    const serviceKey = createConnectQueryServiceKey(RepositoryService);
    const queryKey = createConnectQueryKey({
      schema: listRepositories,
      input: { workspaceId: "ws-default" },
    });

    expect(queryKey[0]).toBe(serviceKey[0]);
    expect((queryKey[1] as { serviceName: string }).serviceName).toBe(
      serviceKey[1].serviceName,
    );
  });

  it("invalidates queries using a service descriptor", async () => {
    const queryClient = new QueryClient();
    const queryKey = createConnectQueryKey({
      schema: listRepositories,
      input: { workspaceId: "ws-default" },
    });
    queryClient.setQueryData(queryKey, { repositories: [] });

    await invalidateConnectQueryServiceQueries(queryClient, RepositoryService);

    const query = queryClient.getQueryCache().find({ queryKey });
    expect(query).toBeDefined();
    expect(query?.state.isInvalidated).toBe(true);
  });
});
