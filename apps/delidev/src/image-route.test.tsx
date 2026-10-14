// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { expect, it } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  SystemCapability,
  SystemService,
  WorkerCapability,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { useImageRoute } from "./image-drafts";

const machineId = newRequestId();
const agentId = newRequestId();
const route = (modalities: string[]) => ({
  model: { provider_id: newRequestId(), native_id: "fixture-native", input_modalities: modalities },
  accounts: [{ id: newRequestId() }],
});

function wrapper(agent: Record<string, unknown>) {
  const transport = createRouterTransport(({ service }) => {
    service(SystemService, {
      getStatus: () => ({ capabilities: [SystemCapability.IMAGE_INPUTS_V1] }),
    });
    service(ResourceService, {
      getResource: (request) => ({
        resource: create(ResourceSchema, {
          id: request.id,
          kind: request.kind,
          revision: 1n,
          schemaVersion: request.kind === EntityKind.AGENT ? 4 : 1,
          documentJson: encode(
            request.kind === EntityKind.MACHINE
              ? { worker_capabilities: [WorkerCapability.IMAGE_INPUTS_V1] }
              : agent,
          ),
        }),
      }),
    });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </TransportProvider>
  );
}

async function ready(
  agent: Record<string, unknown>,
  original?: { harness: string; declared: boolean },
) {
  const { result } = renderHook(
    () =>
      useImageRoute(
        machineId,
        agentId,
        true,
        true,
        original?.harness,
        original?.declared,
      ),
    { wrapper: wrapper(agent) },
  );
  await waitFor(() => expect(result.current.machine).toBeDefined());
  await waitFor(() => expect(result.current.loading).toBe(false));
  return result.current.ready;
}

it("admits Codex agents and OpenCode agents whose every route model declares image input", async () => {
  expect(await ready({ harness: "codex", routes: [route(["text"])] })).toBe(true);
  expect(
    await ready({ harness: "opencode", routes: [route(["text", "image"])] }),
  ).toBe(true);
});

it("keeps OpenCode text-only routes and the other harnesses unsupported", async () => {
  expect(await ready({ harness: "opencode", routes: [route(["text"])] })).toBe(false);
  expect(
    await ready({
      harness: "opencode",
      routes: [route(["text", "image"]), route(["text"])],
    }),
  ).toBe(false);
  expect(await ready({ harness: "opencode", routes: [route([])] })).toBe(false);
  expect(await ready({ harness: "claude-code", routes: [route(["image"])] })).toBe(false);
  expect(await ready({ harness: "grok-build", routes: [route(["image"])] })).toBe(false);
});

it("uses the frozen execution snapshot for queued follow-ups", async () => {
  const agent = { harness: "opencode", routes: [route(["text"])] };
  expect(await ready(agent, { harness: "opencode", declared: true })).toBe(true);
  expect(await ready(agent, { harness: "opencode", declared: false })).toBe(false);
  expect(await ready(agent, { harness: "codex", declared: false })).toBe(true);
  expect(await ready(agent, { harness: "claude-code", declared: true })).toBe(false);
  expect(await ready(agent, { harness: "sidechat", declared: true })).toBe(false);
});
