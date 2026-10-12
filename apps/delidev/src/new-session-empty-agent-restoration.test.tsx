// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemService,
  SystemCapability,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { scrollChoiceValue } from "./test-scroll-picker";
import { NewSession, NewSessionKind } from "./new-session";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";

const CREATE = { name: "Create agent worker" };

function agentResource(extra = {}): Resource {
  return create(ResourceSchema, {
    kind: EntityKind.AGENT,
    id: newRequestId(),
    revision: 1n,
    schemaVersion: 4,
    documentJson: encode({
      name: "Retained agent",
      harness: "codex",
      routes: [
        {
          model: {
            provider_id: newRequestId(),
            native_id: "fixture-native-model",
            input_modalities: ["text"],
            metadata_source: "unknown",
          },
          accounts: [{ id: newRequestId(), weight: 1 }],
        },
      ],
      templates: [],
      options: { permission: "default" },
      ...extra,
    }),
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

// Agent inventory is always a complete empty page, so only creation preference
// and retained-Agent restoration state decide whether creation may be offered.
function fixture(kind: NewSessionKind) {
  const agent = agentResource();
  const machine = create(ResourceSchema, {
    kind: EntityKind.MACHINE,
    id: newRequestId(),
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({ name: "Runner", disabled: false }),
  });
  const scope = { server_id: newRequestId(), device_id: newRequestId() };
  const read = deferred<unknown>();
  const exact = deferred<{ resource?: Resource }>();
  const bridge = {
    read: vi.fn(() => read.promise),
    update: vi.fn(),
  };
  const getResource = vi.fn(
    (request: { kind: EntityKind; id: string }): Promise<{
      resource?: Resource;
    }> | { resource?: Resource } => {
      if (request.kind === EntityKind.AGENT && request.id === agent.id)
        return exact.promise;
      if (request.kind === EntityKind.MACHINE && request.id === machine.id)
        return { resource: machine };
      throw new ConnectError("missing", Code.NotFound);
    },
  );
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {
      getResource,
      listResources: () => ({ resources: [] }),
    });
    router.service(SystemService, {
      getStatus: () => ({ capabilities: [SystemCapability.AUTOMATIC_TITLES_V1] }),
    });
    router.service(SessionService, {});
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const view = (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <NewSession
            kind={kind}
            active
            ownsActivation
            activation={1}
            back={() => {}}
            openSettings={() => {}}
            open={() => {}}
            created={() => {}}
            preferenceBridge={bridge}
            preferenceScope={scope}
          />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  const snapshot = (pair: { agent_id: string; machine_id: string } | null) => ({
    revision: 1,
    scope,
    pair,
    problem: null,
  });
  return { agent, machine, read, exact, bridge, getResource, view, snapshot };
}

const kinds = [NewSessionKind.Session, NewSessionKind.GeneralChat];

async function inventorySettled() {
  // The complete empty first page has loaded once the loading status is gone.
  await waitFor(() =>
    expect(screen.queryByText("Loading Agent Worker choices…")).not.toBeNull(),
  );
}

it.each(kinds)(
  "withholds Create agent worker until the preference read settles, then offers it without a retained pair (%s)",
  async (kind) => {
    const f = fixture(kind);
    render(f.view);
    await waitFor(() => expect(f.bridge.read).toHaveBeenCalled());
    await inventorySettled();
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });
    expect(screen.queryByRole("button", CREATE)).toBeNull();
    expect(screen.getByText("Loading Agent Worker choices…")).toBeTruthy();
    await act(async () => f.read.resolve(f.snapshot(null)));
    expect(await screen.findByRole("button", CREATE)).toBeTruthy();
    expect(f.getResource).not.toHaveBeenCalled();
  },
);

it.each(kinds)(
  "keeps creation hidden while the retained Agent exact read is pending and restores it once eligible (%s)",
  async (kind) => {
    const f = fixture(kind);
    render(f.view);
    await act(async () =>
      f.read.resolve(
        f.snapshot({ agent_id: f.agent.id, machine_id: f.machine.id }),
      ),
    );
    await waitFor(() =>
      expect(f.getResource).toHaveBeenCalledWith(
        expect.objectContaining({ id: f.agent.id }),
        expect.anything(),
      ),
    );
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });
    expect(screen.queryByRole("button", CREATE)).toBeNull();
    await act(async () => f.exact.resolve({ resource: f.agent }));
    await waitFor(() =>
      expect(
        scrollChoiceValue(
          screen.getByRole("combobox", { name: "Agent Worker" }),
        ),
      ).toBe(f.agent.id),
    );
    expect(screen.queryByRole("button", CREATE)).toBeNull();
  },
);

it.each(kinds)(
  "offers creation after an ineligible retained Agent settles (%s)",
  async (kind) => {
    const f = fixture(kind);
    render(f.view);
    await act(async () =>
      f.read.resolve(
        f.snapshot({ agent_id: f.agent.id, machine_id: f.machine.id }),
      ),
    );
    await waitFor(() => expect(f.getResource).toHaveBeenCalled());
    expect(screen.queryByRole("button", CREATE)).toBeNull();
    const disabled = agentResource({ disabled: true });
    disabled.id = f.agent.id;
    await act(async () => f.exact.resolve({ resource: disabled }));
    expect(await screen.findByRole("button", CREATE)).toBeTruthy();
  },
);

it.each(kinds)(
  "offers creation after the retained Agent read fails (%s)",
  async (kind) => {
    const f = fixture(kind);
    render(f.view);
    await act(async () =>
      f.read.resolve(
        f.snapshot({ agent_id: f.agent.id, machine_id: f.machine.id }),
      ),
    );
    await waitFor(() => expect(f.getResource).toHaveBeenCalled());
    expect(screen.queryByRole("button", CREATE)).toBeNull();
    await act(async () =>
      f.exact.reject(new ConnectError("offline", Code.Unavailable)),
    );
    expect(await screen.findByRole("button", CREATE)).toBeTruthy();
  },
);

it.each(kinds)(
  "offers creation after the preference read itself fails (%s)",
  async (kind) => {
    const f = fixture(kind);
    render(f.view);
    expect(screen.queryByRole("button", CREATE)).toBeNull();
    await act(async () => f.read.reject(new Error("native read failed")));
    expect(await screen.findByRole("button", CREATE)).toBeTruthy();
  },
);
