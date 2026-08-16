// @vitest-environment jsdom

import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider, useMutation, useQuery } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { createElement, type PropsWithChildren } from "react";
import { describe, expect, it, vi } from "vitest";

import {
  getSettings as getSettingsQuery,
  replaceSettings as replaceSettingsMutation,
} from "../src/gen/devhud/v1/settings-SettingsService_connectquery.js";
import {
  GetSettingsResponseSchema,
  type ReplaceSettingsRequest,
  ReplaceSettingsResponseSchema,
  SettingsService,
} from "../src/gen/devhud/v1/settings_pb.js";

describe("Connect Query integration", () => {
  it("executes generated query and mutation descriptors through React Query", async () => {
    const getSettingsHandler = vi.fn(() =>
      create(GetSettingsResponseSchema, {
        snapshot: {
          schemaVersion: 1,
          revision: 1n,
          canonicalJson: new TextEncoder().encode("{}"),
        },
      }),
    );
    const replaceSettingsHandler = vi.fn((request: ReplaceSettingsRequest) =>
      create(ReplaceSettingsResponseSchema, {
        snapshot: {
          schemaVersion: request.schemaVersion,
          revision: request.expectedRevision + 1n,
          canonicalJson: request.canonicalJson,
        },
      }),
    );
    const transport = createRouterTransport(({ service }) => {
      service(SettingsService, {
        getSettings: getSettingsHandler,
        replaceSettings: replaceSettingsHandler,
      });
    });
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });
    const wrapper = ({ children }: PropsWithChildren) =>
      createElement(
        TransportProvider,
        { transport },
        createElement(QueryClientProvider, { client: queryClient }, children),
      );

    const query = renderHook(() => useQuery(getSettingsQuery), { wrapper });
    await waitFor(() => expect(query.result.current.isSuccess).toBe(true));
    expect(query.result.current.data?.snapshot?.revision).toBe(1n);
    expect(getSettingsHandler).toHaveBeenCalledOnce();
    query.unmount();

    const mutation = renderHook(() => useMutation(replaceSettingsMutation), { wrapper });
    act(() => {
      mutation.result.current.mutate({
        schemaVersion: 1,
        canonicalJson: new TextEncoder().encode('{"theme":"dark"}'),
        expectedRevision: 1n,
      });
    });
    await waitFor(() => expect(mutation.result.current.isSuccess).toBe(true));
    expect(mutation.result.current.data?.snapshot?.revision).toBe(2n);
    expect(replaceSettingsHandler).toHaveBeenCalledOnce();
    expect(replaceSettingsHandler.mock.calls[0]?.[0].expectedRevision).toBe(1n);

    mutation.unmount();
    queryClient.clear();
  });
});
