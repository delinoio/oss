import { StrictMode, useState } from "react";
// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
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
  TerminalService,
  TerminalAction,
  SystemService,
  SystemCapability,
  TerminalCreationMode,
  TerminalQuery,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents, useRetainedMutation } from "./mutation";
import {
  SessionTabKind,
  SessionTabsProvider,
  sessionTabKey,
  useSessionTabs,
  useSessionTabsStore,
  type SessionTabsStore,
} from "./session-tabs";
import { SessionTerminals } from "./session-terminals";
import { ShortcutProvider } from "./shortcut-provider";

// Component tests use a text fixture; real Ghostty parser/CSP acceptance runs in
// the external Chrome fixture. This adapter exercises input ownership only.
vi.mock("./terminal-emulator", () => ({
  openTerminalScreen: (
    host: HTMLElement,
    input: (bytes: Uint8Array) => void,
    _resized: unknown,
    _unavailable: unknown,
    tabShortcut?: (event: KeyboardEvent) => boolean,
  ) => {
    const output = document.createElement("pre"),
      field = document.createElement("textarea");
    field.setAttribute("aria-label", "Terminal input");
    field.disabled = true;
    // Mirror the real adapter: the shortcut owner runs before PTY delivery.
    host.addEventListener(
      "keydown",
      (event) => {
        if (tabShortcut?.(event)) {
          event.preventDefault();
          event.stopImmediatePropagation();
        }
      },
      true,
    );
    field.addEventListener("keydown", (event) => {
      if (!field.disabled && event.key === "Enter")
        input(new TextEncoder().encode(field.value + "\r"));
      // An unconsumed modifier chord would reach the PTY as bytes.
      else if (!field.disabled && (event.ctrlKey || event.metaKey))
        input(new TextEncoder().encode(event.key));
    });
    host.append(output, field);
    let decoder = new TextDecoder();
    return {
      write: async (bytes: Uint8Array, gap: boolean) => {
        if (gap) {
          decoder = new TextDecoder();
          output.textContent = "";
        }
        output.textContent += decoder.decode(bytes, { stream: true });
      },
      enabled: (value: boolean) => {
        field.disabled = !value;
      },
      focus: () => field.focus(),
      dispose: () => host.replaceChildren(),
    };
  },
}));

it("retries the exact creation request and reattaches without another shell", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const terminal = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.TERMINAL,
    sessionId: session.id,
    schemaVersion: 1,
    revision: 4n,
    documentJson: encode({
      state: "running",
      machine_id: newRequestId(),
      shell: "/bin/sh",
      cwd: "/fixture",
    }),
  });
  const createTerminal = vi.fn(async (_request: unknown) => ({ terminal }));
  createTerminal.mockRejectedValueOnce(
    new ConnectError("acknowledgment lost", Code.Unavailable),
  );
  const calls: { epoch: string; afterSequence: bigint }[] = [];
  const epoch = newRequestId();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [terminal] }),
    });
    router.service(TerminalService, {
      createTerminal,
      watchTerminalOutput: async function* (request) {
        calls.push({
          epoch: request.epoch,
          afterSequence: request.afterSequence,
        });
        if (calls.length === 1) {
          yield {
            epoch,
            sequence: 1n,
            data: new Uint8Array([
              ...new TextEncoder().encode("original shell output"),
              0xe2,
              0x82,
            ]),
            terminal,
          };
          throw new ConnectError("disconnected", Code.Unavailable);
        }
        if (calls.length === 2) {
          yield { epoch, sequence: 1n, heartbeat: true, terminal };
          return;
        }
        const exited = create(ResourceSchema, {
          ...terminal,
          revision: 5n,
          documentJson: encode({ state: "exited", cleanup_verified: true }),
        });
        yield {
          epoch,
          sequence: 2n,
          data: new Uint8Array([0xac]),
          terminal: exited,
        };
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionTerminals session={session} close={() => {}} />
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Create terminal",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("button", { name: "Create terminal" }));
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Retry the same terminal creation",
    }),
  );
  await screen.findByText("original shell output");
  fireEvent.click(
    await screen.findByRole("button", { name: "Reattach original terminal" }),
  );
  await waitFor(() => expect(calls).toHaveLength(2));
  expect(calls[1]).toEqual({ epoch, afterSequence: 1n });
  fireEvent.click(
    await screen.findByRole("button", { name: "Reattach original terminal" }),
  );
  await screen.findByText("original shell output€");
  expect(calls[2]).toEqual({ epoch, afterSequence: 1n });
  expect(createTerminal).toHaveBeenCalledTimes(2);
  expect(createTerminal.mock.calls[0]![0]).toEqual(
    createTerminal.mock.calls[1]![0],
  );
  view.unmount();
  client.clear();
});

it("focuses the attached terminal input and sends exact UTF-8 and native resize controls", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const terminal = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.TERMINAL,
    sessionId: session.id,
    schemaVersion: 1,
    revision: 4n,
    documentJson: encode({ state: "running", machine_id: newRequestId() }),
  });
  const controlTerminal = vi.fn(async (_request: unknown) => ({ terminal }));
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [terminal] }),
    });
    router.service(TerminalService, {
      controlTerminal,
      watchTerminalOutput: async function* () {
        yield {
          epoch: newRequestId(),
          sequence: 0n,
          heartbeat: true,
          terminal,
        };
        await held;
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionTerminals session={session} close={() => {}} />
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
    const input = await screen.findByRole("textbox", {
      name: "Terminal input",
    });
    await waitFor(() => expect(document.activeElement).toBe(input));
    fireEvent.change(input, { target: { value: "€" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(controlTerminal).toHaveBeenCalledTimes(1));
    const sent = controlTerminal.mock.calls[0]![0] as { input: Uint8Array };
    expect(sent).toMatchObject({
      mutation: { id: terminal.id, expectedRevision: 4n },
      action: TerminalAction.INPUT,
    });
    expect(Array.from(sent.input)).toEqual([0xe2, 0x82, 0xac, 13]);
    /* Automatic dimensions are tested by the dedicated queue/browser fixture. */
  } finally {
    view.unmount();
    release();
    client.clear();
  }
});

it("attaches to the accepted creation beyond the first full history page", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const history = Array.from({ length: 50 }, () =>
    create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.TERMINAL,
      sessionId: session.id,
      schemaVersion: 1,
      revision: 2n,
      documentJson: encode({ state: "closed", cleanup_verified: true }),
    }),
  );
  const terminal = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.TERMINAL,
    sessionId: session.id,
    schemaVersion: 1,
    revision: 4n,
    documentJson: encode({ state: "running" }),
  });
  const createTerminal = vi.fn(() => ({ terminal }));
  const controlTerminal = vi.fn((_request: unknown) => ({ terminal }));
  const watched: string[] = [];
  const listResources = vi.fn(() => ({
    resources: history,
    nextPageToken: "older-page",
  }));
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, { listResources });
    router.service(TerminalService, {
      createTerminal,
      controlTerminal,
      watchTerminalOutput: async function* (request) {
        watched.push(request.terminalId);
        const epoch = newRequestId();
        yield {
          epoch,
          sequence: 1n,
          data: new TextEncoder().encode("new original terminal"),
          terminal,
        };
        await held;
        yield {
          epoch,
          sequence: 1n,
          heartbeat: true,
          terminal: create(ResourceSchema, {
            ...terminal,
            revision: 5n,
            documentJson: encode({ state: "exited", cleanup_verified: true }),
          }),
        };
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionTerminals session={session} close={() => {}} />
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  try {
    await screen.findByRole("tab", { name: /Terminal 50/ });
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "Create terminal",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByRole("button", { name: "Create terminal" }));
    await screen.findByText("new original terminal");
    expect(watched).toEqual([terminal.id]);
    expect(createTerminal).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(listResources.mock.calls.length).toBeGreaterThan(1),
    );
    const input = screen.getByRole("textbox", { name: "Terminal input" });
    fireEvent.change(input, { target: { value: "original" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(controlTerminal).toHaveBeenCalledTimes(1));
    expect(controlTerminal.mock.calls[0]![0]).toMatchObject({
      mutation: { id: terminal.id, expectedRevision: 4n },
      action: TerminalAction.INPUT,
    });
    expect(screen.getByText("new original terminal")).toBeTruthy();
    await act(async () => release());
    await waitFor(() =>
      expect(document.getElementById(`terminal-tab-${terminal.id}`)).toBeNull(),
    );
    expect(screen.getAllByRole("tab")).toHaveLength(50);
    expect(createTerminal).toHaveBeenCalledTimes(1);
    expect(controlTerminal).toHaveBeenCalledTimes(1);
  } finally {
    view.unmount();
    release();
    client.clear();
  }
});

it("gates initial reads and polling on advertised terminal support", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  let capabilities: SystemCapability[] = [];
  let statusPending = true;
  let release = (_value: { capabilities: SystemCapability[] }) => {};
  const pending = new Promise<{ capabilities: SystemCapability[] }>(
    (resolve) => {
      release = resolve;
    },
  );
  const getStatus = vi.fn(async () =>
    statusPending ? await pending : { capabilities },
  );
  const listResources = vi.fn(() => ({ resources: [] }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus });
    router.service(ResourceService, { listResources });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionTerminals session={session} close={() => {}} />
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  const waitForPoll = () =>
    act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 1100));
    });
  try {
    await waitFor(() => expect(getStatus).toHaveBeenCalledTimes(1));
    expect(
      screen.queryByRole("button", { name: "Refresh terminals" }),
    ).toBeNull();
    await waitForPoll();
    expect(listResources).not.toHaveBeenCalled();
    statusPending = false;
    await act(async () => release({ capabilities: [] }));
    await waitForPoll();
    expect(listResources).not.toHaveBeenCalled();
    expect(
      screen.getByText("Waiting for a server that supports session terminals."),
    ).toBeTruthy();
    capabilities = [SystemCapability.SESSION_TERMINALS_V1];
    await act(async () => {
      await client.invalidateQueries();
    });
    // Invalidating everything also reaches the refresh marker while the first
    // read is active; that is queued as one coalesced refresh, so the read
    // count is at least one and settles below.
    await waitFor(() => expect(listResources).toHaveBeenCalled());
    capabilities = [];
    // Refresh only status to avoid requesting one more supported history page.
    await act(async () => {
      await client.invalidateQueries({
        predicate: (query) =>
          query.queryKey.some(
            (part) =>
              typeof part === "object" &&
              part !== null &&
              "methodName" in part &&
              part.methodName === "GetStatus",
          ),
      });
    });
    await waitFor(() =>
      expect(
        screen.getByText(
          "Waiting for a server that supports session terminals.",
        ),
      ).toBeTruthy(),
    );
    const calls = listResources.mock.calls.length;
    await waitForPoll();
    expect(listResources).toHaveBeenCalledTimes(calls);
  } finally {
    release({ capabilities: [] });
    view.unmount();
    client.clear();
  }
});

it.each([
  { pending: undefined, dispatched: true },
  { pending: {}, dispatched: false },
])(
  "dispatches input for a running terminal only without present pending metadata (%j)",
  async ({ pending, dispatched }) => {
    const session = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.SESSION,
      schemaVersion: 1,
      revision: 7n,
      documentJson: encode({ archive: "active" }),
    });
    const terminal = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.TERMINAL,
      sessionId: session.id,
      schemaVersion: 1,
      revision: 4n,
      documentJson: encode({ state: "running", pending }),
    });
    const controlTerminal = vi.fn(async () => ({ terminal }));
    let releaseStream = () => {};
    const held = new Promise<void>((resolve) => {
      releaseStream = resolve;
    });
    const transport = createRouterTransport((router) => {
      router.service(SystemService, {
        getStatus: () => ({
          capabilities: [SystemCapability.SESSION_TERMINALS_V1],
        }),
      });
      router.service(ResourceService, {
        listResources: () => ({ resources: [terminal] }),
      });
      router.service(TerminalService, {
        controlTerminal,
        watchTerminalOutput: async function* () {
          yield {
            epoch: newRequestId(),
            sequence: 0n,
            heartbeat: true,
            terminal,
          };
          await held;
        },
      });
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const view = render(
      <QueryClientProvider client={client}>
        <TransportProvider transport={transport}>
          <MutationIntents>
            <SessionTerminals session={session} close={() => {}} />
          </MutationIntents>
        </TransportProvider>
      </QueryClientProvider>,
    );
    try {
      fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
      const input = (await screen.findByRole("textbox", {
        name: "Terminal input",
      })) as HTMLTextAreaElement;
      await waitFor(() => expect(document.activeElement).toBe(input));
      fireEvent.change(input, { target: { value: "fixture line" } });
      fireEvent.keyDown(input, { key: "Enter" });
      if (dispatched)
        await waitFor(() => expect(controlTerminal).toHaveBeenCalled());
      else {
        await new Promise((resolve) => setTimeout(resolve, 100));
        expect(controlTerminal).not.toHaveBeenCalled();
      }
    } finally {
      view.unmount();
      releaseStream();
      client.clear();
    }
  },
);

it("keeps terminal input enabled while one control is pending and never steals focus on acknowledgment", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const terminal = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.TERMINAL,
    sessionId: session.id,
    schemaVersion: 1,
    revision: 4n,
    documentJson: encode({ state: "running" }),
  });
  let acknowledge = () => {};
  const pending = new Promise<void>((resolve) => {
    acknowledge = resolve;
  });
  const controlTerminal = vi.fn(async () => {
    await pending;
    return { terminal };
  });
  let releaseStream = () => {};
  const held = new Promise<void>((resolve) => {
    releaseStream = resolve;
  });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [terminal] }),
    });
    router.service(TerminalService, {
      controlTerminal,
      watchTerminalOutput: async function* () {
        yield {
          epoch: newRequestId(),
          sequence: 0n,
          heartbeat: true,
          terminal,
        };
        await held;
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionTerminals session={session} close={() => {}} />
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
    const input = (await screen.findByRole("textbox", {
      name: "Terminal input",
    })) as HTMLTextAreaElement;
    await waitFor(() => expect(document.activeElement).toBe(input));
    fireEvent.change(input, { target: { value: "fixture line" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(controlTerminal).toHaveBeenCalledTimes(1));
    expect(input.disabled).toBe(false);
    // jsdom retains disabled focus; model the browser's focus loss explicitly.
    const columns = screen.getByRole("button", { name: "Create terminal" });
    columns.focus();
    expect(document.activeElement).not.toBe(input);
    await act(async () => acknowledge());
    await waitFor(() => expect(input.disabled).toBe(false));
    expect(document.activeElement).toBe(columns);
    expect(controlTerminal).toHaveBeenCalledTimes(1);
    // A metadata poll must not steal focus while controls remain available.
    columns.focus();
    await act(async () => {
      await client.invalidateQueries();
    });
    expect(document.activeElement).toBe(columns);
  } finally {
    acknowledge();
    view.unmount();
    releaseStream();
    client.clear();
  }
});

it("preserves the explicitly attached terminal and its draft after its history payload is evicted", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    revision: 1n,
    documentJson: encode({ archive: "active" }),
  });
  const history = Array.from({ length: 4 }, () =>
    create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.TERMINAL,
      schemaVersion: 1,
      sessionId: session.id,
      revision: 1n,
      documentJson: encode({ state: "running" }),
    }),
  );
  const watch = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: (request) => {
        const index = Number(request.filter?.pageToken || 0);
        return {
          resources: [history[index]],
          nextPageToken: index < 3 ? String(index + 1) : "",
        };
      },
    });
    router.service(TerminalService, {
      watchTerminalOutput: async function* (request, context) {
        watch(request);
        yield {
          epoch: newRequestId(),
          sequence: 1n,
          data: new TextEncoder().encode("Original attached output"),
          terminal: history[0],
        };
        await new Promise<void>((resolve) =>
          context.signal.addEventListener("abort", () => resolve(), {
            once: true,
          }),
        );
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <SessionTerminals session={session} close={() => {}} />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
  await screen.findByText("Original attached output");
  const input = screen.getByRole("textbox", { name: "Terminal input" });
  fireEvent.change(input, { target: { value: "Retained terminal draft" } });
  for (let index = 2; index <= 4; index++) {
    fireEvent.click(
      screen.getByRole("button", { name: "Load more Terminal history pages" }),
    );
    await screen.findByRole("tab", {
      name: new RegExp(`Terminal ${index}`),
    });
  }
  expect(screen.getByRole("tab", { name: /Terminal 1/ })).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "Terminal input" })).toBe(input);
  expect(input).toHaveProperty("value", "Retained terminal draft");
  expect(watch).toHaveBeenCalledTimes(1);
});

it("keeps failed terminal capability reads distinct from missing support", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const status = vi
    .fn()
    .mockRejectedValueOnce(
      new ConnectError("private-native-status", Code.PermissionDenied),
    )
    .mockResolvedValue({ capabilities: [] });
  const createTerminal = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: status });
    router.service(TerminalService, { createTerminal });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionTerminals session={session} close={() => {}} />
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  const retry = await screen.findByRole("button", {
    name: "Retry terminal capability read",
  });
  expect(screen.queryByText(/Waiting for a server that supports/)).toBeNull();
  expect(createTerminal).not.toHaveBeenCalled();
  fireEvent.click(retry);
  await waitFor(() => expect(status).toHaveBeenCalledTimes(2));
  expect(createTerminal).not.toHaveBeenCalled();
});

it("retains exact uncertain input across dock hiding and never closes or creates a shell", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const terminal = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.TERMINAL,
    sessionId: session.id,
    schemaVersion: 1,
    revision: 4n,
    documentJson: encode({ state: "running" }),
  });
  const controls = vi.fn(async (_request: unknown) => ({ terminal }));
  controls.mockRejectedValueOnce(new ConnectError("lost", Code.Unavailable));
  const createTerminal = vi.fn(),
    watched = vi.fn(),
    aborted = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [terminal] }),
    });
    router.service(TerminalService, {
      createTerminal,
      controlTerminal: controls,
      watchTerminalOutput: async function* (_request, context) {
        watched();
        yield {
          epoch: "original-epoch",
          sequence: 0n,
          heartbeat: true,
          terminal,
        };
        await new Promise<void>((resolve) =>
          context.signal.addEventListener(
            "abort",
            () => {
              aborted();
              resolve();
            },
            { once: true },
          ),
        );
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const fixture = (active: boolean) => (
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionTerminals
            session={session}
            active={active}
            close={() => {}}
          />
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>
  );
  const view = render(fixture(true));
  fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
  const input = await screen.findByRole("textbox", { name: "Terminal input" });
  await waitFor(() => expect(input).toHaveProperty("disabled", false));
  fireEvent.change(input, { target: { value: "€original" } });
  fireEvent.keyDown(input, { key: "Enter" });
  await screen.findByRole("button", {
    name: "Retry the same terminal operation",
  });
  expect(controls).toHaveBeenCalledTimes(1);
  view.rerender(fixture(false));
  await waitFor(() => expect(aborted).toHaveBeenCalledTimes(1));
  view.rerender(fixture(true));
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Retry the same terminal operation",
    }),
  );
  await waitFor(() => expect(controls).toHaveBeenCalledTimes(2));
  expect(controls.mock.calls[1]![0]).toEqual(controls.mock.calls[0]![0]);
  expect(createTerminal).not.toHaveBeenCalled();
  expect(
    (controls.mock.calls[0]![0] as { action: TerminalAction }).action,
  ).toBe(TerminalAction.INPUT);
  view.unmount();
  client.clear();
});

it("keeps every existing terminal picker keyboard reachable in the single-pane workspace", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({ archive: "active" }),
  });
  const terminals = [1, 2].map(() =>
    create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.TERMINAL,
      sessionId: session.id,
      schemaVersion: 1,
      revision: 1n,
      documentJson: encode({ state: "running" }),
    }),
  );
  const open = vi.fn(),
    transport = createRouterTransport((router) => {
      router.service(SystemService, {
        getStatus: () => ({
          capabilities: [SystemCapability.SESSION_TERMINALS_V1],
        }),
      });
      router.service(ResourceService, {
        listResources: () => ({ resources: terminals }),
      });
    });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionTerminals
            session={session}
            tabbed
            close={() => {}}
            openTerminal={open}
          />
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  const first = await screen.findByRole("button", { name: /Terminal 1/ }),
    second = screen.getByRole("button", { name: /Terminal 2/ });
  expect(first.tabIndex).toBe(0);
  expect(second.tabIndex).toBe(0);
  // The shared tab presentation never converts the tabbed group into ARIA tabs.
  const strip = first.parentElement!;
  expect(strip.getAttribute("role")).toBe("group");
  expect(strip.classList.contains("tab-strip")).toBe(true);
  expect(
    [first, second].every(
      (button) =>
        button.classList.contains("tab-item") &&
        button.classList.contains("tab-label") &&
        button.getAttribute("role") === null &&
        button.getAttribute("aria-selected") === null,
    ),
  ).toBe(true);
  first.focus();
  expect(fireEvent.keyDown(first, { key: "ArrowRight" })).toBe(true);
  expect(open).not.toHaveBeenCalled();
  second.focus();
  expect(document.activeElement).toBe(second);
  view.unmount();
  client.clear();
});

it.each(["empty", "later-page", "uncertain", "read-error", "empty-pending"])(
  "resolves explicit opening once under Strict Mode (%s)",
  async (scenario) => {
    const session = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.SESSION,
      schemaVersion: 1,
      revision: 7n,
      documentJson: encode({ archive: "active" }),
    });
    const row = (state: string, cleanup = false, pending?: object) =>
      create(ResourceSchema, {
        id: newRequestId(),
        sessionId: session.id,
        kind: EntityKind.TERMINAL,
        schemaVersion: 1,
        revision: 2n,
        documentJson: encode({ state, cleanup_verified: cleanup, pending }),
      });
    const terminal = row("running"),
      history = Array.from({ length: 50 }, () => row("closed", true));
    const opened = vi.fn(),
      createTerminal = vi.fn((_request: unknown) => ({ terminal }));
    const pages: string[] = [];
    const transport = createRouterTransport((router) => {
      router.service(SystemService, {
        getStatus: () => ({
          capabilities: [SystemCapability.SESSION_TERMINALS_V1],
        }),
      });
      router.service(ResourceService, {
        listResources: (request) => {
          pages.push(request.filter?.pageToken ?? "");
          if (scenario === "read-error")
            throw new ConnectError("Fixture read failure", Code.Unavailable);
          if (scenario === "later-page")
            return request.filter?.pageToken
              ? { resources: [terminal] }
              : { resources: history, nextPageToken: "next" };
          return {
            resources:
              scenario === "uncertain"
                ? [row("uncertain")]
                : scenario === "empty-pending"
                  ? // Verified closed, but present-yet-empty pending metadata is
                    // unsettled original-operation evidence.
                    [row("closed", true, {})]
                  : [],
          };
        },
      });
      router.service(TerminalService, { createTerminal });
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    function View() {
      const [intent, setIntent] = useState<{
        requestId: string;
        revision: bigint;
      }>();
      return (
        <>
          <button
            onClick={() =>
              setIntent(
                (value) =>
                  value ?? {
                    requestId: newRequestId(),
                    revision: session.revision,
                  },
              )
            }
          >
            Open fixture
          </button>
          <SessionTerminals
            session={session}
            selectedId=""
            tabbed
            openIntent={intent}
            finishOpenIntent={() => setIntent(undefined)}
            openTerminal={opened}
            close={() => {}}
          />
        </>
      );
    }
    const view = render(
      <StrictMode>
        <QueryClientProvider client={client}>
          <TransportProvider transport={transport}>
            <MutationIntents>
              <View />
            </MutationIntents>
          </TransportProvider>
        </QueryClientProvider>
      </StrictMode>,
    );
    try {
      await screen.findByRole("button", { name: "Create terminal" });
      expect(createTerminal).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole("button", { name: "Open fixture" }));
      fireEvent.click(screen.getByRole("button", { name: "Open fixture" }));
      if (scenario === "empty") {
        await waitFor(() => expect(createTerminal).toHaveBeenCalledOnce());
        expect(createTerminal.mock.calls[0]?.[0]).toMatchObject({
          mutation: { id: session.id, expectedRevision: 7n },
          rows: 24,
          columns: 80,
          shellOverride: "",
          creationMode: TerminalCreationMode.REUSE_OR_CREATE,
        });
        await waitFor(() => expect(opened).toHaveBeenCalledWith(terminal.id));
      } else if (scenario === "later-page") {
        await waitFor(() => expect(opened).toHaveBeenCalledWith(terminal.id));
        expect(pages).toContain("next");
        expect(createTerminal).toHaveBeenCalledOnce();
        expect(createTerminal.mock.calls[0]?.[0]).toMatchObject({
          creationMode: TerminalCreationMode.REUSE_OR_CREATE,
          preferredTerminalId: terminal.id,
        });
      } else {
        await screen.findByRole("button", {
          name: "Retry terminal inventory read",
        });
        expect(createTerminal).not.toHaveBeenCalled();
        fireEvent.click(
          screen.getByRole("button", { name: "Retry terminal inventory read" }),
        );
        expect(createTerminal).not.toHaveBeenCalled();
        expect(opened).not.toHaveBeenCalled();
      }
    } finally {
      view.unmount();
      client.clear();
    }
  },
);

it.each([{}, [], 12, true, "invalid-id"])(
  "rejects malformed close-request metadata before reusing or creating a terminal (%j)",
  async (closeRequest) => {
    const session = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.SESSION,
      revision: 7n,
      schemaVersion: 1,
      documentJson: encode({ archive: "active" }),
    });
    const terminal = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.TERMINAL,
      sessionId: session.id,
      revision: 1n,
      schemaVersion: 1,
      documentJson: encode({
        state: "running",
        close_request_id: closeRequest,
      }),
    });
    const opened = vi.fn(),
      createTerminal = vi.fn(() => ({ terminal }));
    const transport = createRouterTransport((router) => {
      router.service(SystemService, {
        getStatus: () => ({
          capabilities: [SystemCapability.SESSION_TERMINALS_V1],
        }),
      });
      router.service(ResourceService, {
        listResources: () => ({ resources: [terminal] }),
      });
      router.service(TerminalService, { createTerminal });
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    function View() {
      const [intent, setIntent] = useState<{
        requestId: string;
        revision: bigint;
      }>();
      return (
        <>
          <button
            onClick={() =>
              setIntent({
                requestId: newRequestId(),
                revision: session.revision,
              })
            }
          >
            Open fixture
          </button>
          <SessionTerminals
            session={session}
            selectedId=""
            tabbed
            openIntent={intent}
            finishOpenIntent={() => setIntent(undefined)}
            openTerminal={opened}
            close={() => {}}
          />
        </>
      );
    }
    const view = render(
      <QueryClientProvider client={client}>
        <TransportProvider transport={transport}>
          <MutationIntents>
            <View />
          </MutationIntents>
        </TransportProvider>
      </QueryClientProvider>,
    );
    try {
      await screen.findByRole("button", { name: "Create terminal" });
      fireEvent.click(screen.getByRole("button", { name: "Open fixture" }));
      await screen.findByRole("button", {
        name: "Retry terminal inventory read",
      });
      expect(opened).not.toHaveBeenCalled();
      expect(createTerminal).not.toHaveBeenCalled();
    } finally {
      view.unmount();
      client.clear();
    }
  },
);

it("reopens the last content-tab selection instead of the first inventory terminal", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const terminals = [1, 2].map(() =>
    create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.TERMINAL,
      sessionId: session.id,
      revision: 1n,
      schemaVersion: 1,
      documentJson: encode({ state: "running" }),
    }),
  );
  const opened = vi.fn(),
    createTerminal = vi.fn(() => ({ terminal: terminals[1] }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: terminals }),
    });
    router.service(TerminalService, { createTerminal });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function View() {
    const [selected, select] = useState(terminals[0]!.id),
      [intent, setIntent] = useState<{ requestId: string; revision: bigint }>();
    return (
      <>
        <button onClick={() => select(terminals[1]!.id)}>
          Select second content tab
        </button>
        <button
          onClick={() => {
            select("");
            setIntent({
              requestId: newRequestId(),
              revision: session.revision,
            });
          }}
        >
          Open fixture
        </button>
        <SessionTerminals
          session={session}
          selectedId={selected}
          tabbed
          openIntent={intent}
          finishOpenIntent={() => setIntent(undefined)}
          openTerminal={opened}
          close={() => {}}
        />
      </>
    );
  }
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <View />
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  try {
    await screen.findByRole("button", { name: /Terminal 2/ });
    fireEvent.click(
      screen.getByRole("button", { name: "Select second content tab" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Open fixture" }));
    await waitFor(() => expect(opened).toHaveBeenCalledWith(terminals[1]!.id));
    expect(createTerminal).toHaveBeenCalledOnce();
  } finally {
    view.unmount();
    client.clear();
  }
});

it.each([TerminalAction.INPUT, TerminalAction.RESIZE, TerminalAction.CLOSE])(
  "reopens non-close uncertain controls while retaining exact ownership (action %s)",
  async (action) => {
    const session = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.SESSION,
      schemaVersion: 1,
      revision: 7n,
      documentJson: encode({ archive: "active" }),
    });
    const terminal = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.TERMINAL,
      sessionId: session.id,
      revision: 1n,
      schemaVersion: 1,
      documentJson: encode({ state: "running" }),
    });
    const opened = vi.fn(),
      createTerminal = vi.fn(() => ({ terminal })),
      controlTerminal = vi.fn(() => {
        throw new ConnectError(
          "Original acknowledgment lost",
          Code.Unavailable,
        );
      });
    const transport = createRouterTransport((router) => {
      router.service(SystemService, {
        getStatus: () => ({
          capabilities: [SystemCapability.SESSION_TERMINALS_V1],
        }),
      });
      router.service(ResourceService, {
        listResources: () => ({ resources: [terminal] }),
      });
      router.service(TerminalService, { createTerminal, controlTerminal });
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    function View() {
      const control = useRetainedMutation(
        `terminal-control:${terminal.id}`,
        TerminalQuery.controlTerminal,
      );
      const [intent, setIntent] = useState<{
        requestId: string;
        revision: bigint;
      }>();
      return (
        <>
          <button
            onClick={() =>
              void control.send({
                mutation: {
                  id: terminal.id,
                  expectedRevision: 1n,
                  requestId: newRequestId(),
                },
                action,
                input:
                  action === TerminalAction.INPUT
                    ? new Uint8Array([13])
                    : undefined,
                rows: action === TerminalAction.RESIZE ? 24 : undefined,
                columns: action === TerminalAction.RESIZE ? 80 : undefined,
              })
            }
          >
            Control fixture
          </button>
          {control.uncertain ? <p>Retained control</p> : null}
          <button
            onClick={() =>
              setIntent({
                requestId: newRequestId(),
                revision: session.revision,
              })
            }
          >
            Open fixture
          </button>
          <SessionTerminals
            session={session}
            selectedId=""
            tabbed
            openIntent={intent}
            finishOpenIntent={() => setIntent(undefined)}
            openTerminal={opened}
            close={() => {}}
          />
        </>
      );
    }
    const view = render(
      <QueryClientProvider client={client}>
        <TransportProvider transport={transport}>
          <MutationIntents>
            <View />
          </MutationIntents>
        </TransportProvider>
      </QueryClientProvider>,
    );
    try {
      fireEvent.click(screen.getByRole("button", { name: "Control fixture" }));
      await screen.findByText("Retained control");
      fireEvent.click(screen.getByRole("button", { name: "Open fixture" }));
      if (action === TerminalAction.CLOSE) {
        await screen.findByRole("button", {
          name: "Retry terminal inventory read",
        });
        expect(opened).not.toHaveBeenCalled();
      } else
        await waitFor(() => expect(opened).toHaveBeenCalledWith(terminal.id));
      expect(createTerminal).toHaveBeenCalledTimes(
        action === TerminalAction.CLOSE ? 0 : 1,
      );
      expect(controlTerminal).toHaveBeenCalledOnce();
      expect(screen.getByText("Retained control")).toBeTruthy();
    } finally {
      view.unmount();
      client.clear();
    }
  },
);

it("rejects empty nonterminal inventory pages before another gesture-owned request", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const opened = vi.fn(),
    createTerminal = vi.fn(),
    reads = vi.fn(() => ({ resources: [], nextPageToken: newRequestId() }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, { listResources: reads });
    router.service(TerminalService, { createTerminal });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function View() {
    const [intent, setIntent] = useState<{
      requestId: string;
      revision: bigint;
    }>();
    return (
      <>
        <button
          onClick={() =>
            setIntent({ requestId: newRequestId(), revision: session.revision })
          }
        >
          Open fixture
        </button>
        <SessionTerminals
          session={session}
          selectedId=""
          tabbed
          openIntent={intent}
          finishOpenIntent={() => setIntent(undefined)}
          openTerminal={opened}
          close={() => {}}
        />
      </>
    );
  }
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <View />
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  try {
    await screen.findByRole("button", { name: "Create terminal" });
    await waitFor(() => expect(reads).toHaveBeenCalledOnce());
    fireEvent.click(screen.getByRole("button", { name: "Open fixture" }));
    await screen.findByRole("button", {
      name: "Retry terminal inventory read",
    });
    expect(reads).toHaveBeenCalledTimes(2);
    expect(opened).not.toHaveBeenCalled();
    expect(createTerminal).not.toHaveBeenCalled();
  } finally {
    view.unmount();
    client.clear();
  }
});

it.each([false, true])(
  "does not revive a hidden terminal from stale running inventory (replacement %s)",
  async (otherEligible) => {
    const session = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.SESSION,
      schemaVersion: 1,
      revision: 7n,
      documentJson: encode({ archive: "active" }),
    });
    const stale = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.TERMINAL,
      sessionId: session.id,
      revision: 1n,
      schemaVersion: 1,
      documentJson: encode({ state: "running" }),
    });
    const exited = create(ResourceSchema, {
      ...stale,
      revision: 2n,
      documentJson: encode({ state: "exited", cleanup_verified: true }),
    });
    const replacement = create(ResourceSchema, {
      ...stale,
      id: newRequestId(),
    });
    const opened = vi.fn(),
      createTerminal = vi.fn(() => ({ terminal: replacement }));
    const transport = createRouterTransport((router) => {
      router.service(SystemService, {
        getStatus: () => ({
          capabilities: [SystemCapability.SESSION_TERMINALS_V1],
        }),
      });
      router.service(ResourceService, {
        listResources: () => ({
          resources: otherEligible ? [stale, replacement] : [stale],
        }),
      });
      router.service(TerminalService, { createTerminal });
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    function View() {
      const store = useSessionTabsStore();
      store.terminalPresentation(session.id).observe(session.id, exited);
      const [intent, setIntent] = useState<{
        requestId: string;
        revision: bigint;
      }>();
      return (
        <>
          <button
            onClick={() =>
              setIntent({
                requestId: newRequestId(),
                revision: session.revision,
              })
            }
          >
            Open fixture
          </button>
          <SessionTerminals
            session={session}
            selectedId=""
            tabbed
            openIntent={intent}
            finishOpenIntent={() => setIntent(undefined)}
            openTerminal={opened}
            close={() => {}}
          />
        </>
      );
    }
    const view = render(
      <QueryClientProvider client={client}>
        <TransportProvider transport={transport}>
          <MutationIntents>
            <SessionTabsProvider>
              <View />
            </SessionTabsProvider>
          </MutationIntents>
        </TransportProvider>
      </QueryClientProvider>,
    );
    try {
      await screen.findByRole("button", { name: "Create terminal" });
      fireEvent.click(screen.getByRole("button", { name: "Open fixture" }));
      await waitFor(() => expect(opened).toHaveBeenCalledWith(replacement.id));
      expect(opened).not.toHaveBeenCalledWith(stale.id);
      expect(createTerminal).toHaveBeenCalledOnce();
      if (!otherEligible) expect(createTerminal.mock.calls[0]).toBeTruthy();
    } finally {
      view.unmount();
      client.clear();
    }
  },
);

it("selects only the atomically admitted replacement after a listed terminal retires", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const stale = create(ResourceSchema, {
    id: newRequestId(),
    sessionId: session.id,
    kind: EntityKind.TERMINAL,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({ state: "running" }),
  });
  const replacement = create(ResourceSchema, { ...stale, id: newRequestId() });
  const opened = vi.fn(),
    createTerminal = vi.fn((_request: unknown) => ({ terminal: replacement }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [stale] }),
    });
    router.service(TerminalService, { createTerminal });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function View() {
    const [intent, setIntent] = useState<{
      requestId: string;
      revision: bigint;
    }>();
    return (
      <>
        <button
          onClick={() =>
            setIntent({ requestId: newRequestId(), revision: session.revision })
          }
        >
          Open fixture
        </button>
        <SessionTerminals
          session={session}
          selectedId=""
          tabbed
          openIntent={intent}
          finishOpenIntent={() => setIntent(undefined)}
          openTerminal={opened}
          close={() => {}}
        />
      </>
    );
  }
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <View />
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  try {
    await screen.findByRole("button", { name: /Terminal 1/ });
    fireEvent.click(screen.getByRole("button", { name: "Open fixture" }));
    await waitFor(() => expect(opened).toHaveBeenCalledWith(replacement.id));
    expect(opened).not.toHaveBeenCalledWith(stale.id);
    expect(createTerminal).toHaveBeenCalledOnce();
    expect(createTerminal.mock.calls[0]?.[0]).toMatchObject({
      creationMode: TerminalCreationMode.REUSE_OR_CREATE,
      preferredTerminalId: stale.id,
    });
  } finally {
    view.unmount();
    client.clear();
  }
});

function creationFixture(archive = "active") {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive }),
  });
  const terminal = (state = "running") =>
    create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.TERMINAL,
      sessionId: session.id,
      schemaVersion: 1,
      revision: 1n,
      documentJson: encode({
        state,
        machine_id: newRequestId(),
        shell: "/private/shell",
        cwd: "/private/cwd",
      }),
    });
  const listed = terminal(),
    created = [terminal(), terminal()];
  const known = new Map([listed, ...created].map((row) => [row.id, row]));
  let gate: Promise<void> | undefined;
  const createTerminal = vi.fn(async (_request: unknown) => {
    await gate;
    return { terminal: created[createTerminal.mock.calls.length - 1] };
  });
  const controlTerminal = vi.fn(async (_request: unknown) => ({
    terminal: listed,
  }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [listed] }),
    });
    router.service(TerminalService, {
      createTerminal,
      controlTerminal,
      watchTerminalOutput: async function* (request, context) {
        yield {
          epoch: newRequestId(),
          sequence: 0n,
          heartbeat: true,
          terminal: known.get(request.terminalId),
        };
        await new Promise<void>((resolve) =>
          context.signal.addEventListener("abort", () => resolve(), {
            once: true,
          }),
        );
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionTabsProvider>
            <ShortcutProvider>
              <SessionTerminals session={session} close={() => {}} />
            </ShortcutProvider>
          </SessionTabsProvider>
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  return {
    session,
    listed,
    created,
    createTerminal,
    controlTerminal,
    hold(value: Promise<void>) {
      gate = value;
    },
    cleanup() {
      view.unmount();
      client.clear();
    },
  };
}
const primaryT = { key: "t", code: "KeyT", ctrlKey: true } as const;

it("keeps only the Terminals heading and + button in the terminal panel", async () => {
  const f = creationFixture();
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /^Terminal 1/ }));
    await screen.findByRole("textbox", { name: "Terminal input" });
    const header = document.querySelector(".terminal-dock-header")!;
    // The standalone dock also owns its resize control; the tabbed Session
    // workspace renders exactly the heading and +.
    expect(
      [...header.children].map((node) => node.textContent?.trim()),
    ).toEqual(["Terminals", "+", "Restore terminal dock"]);
    for (const name of [
      "Details",
      "Hide terminals",
      "Refresh terminals",
      "Close terminal",
    ])
      expect(screen.queryByRole("button", { name })).toBeNull();
    expect(screen.queryByLabelText("Worker shell override")).toBeNull();
    expect(screen.queryByText(/Worker\b/)).toBeNull();
    expect(screen.queryByText(/\/private\/(shell|cwd)/)).toBeNull();
    expect(screen.queryByText(/Process cleanup/)).toBeNull();
    expect(f.createTerminal).not.toHaveBeenCalled();
  } finally {
    f.cleanup();
  }
});

it("shares default-shell additional creation between + and terminal-focused primary+T", async () => {
  const f = creationFixture();
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /^Terminal 1/ }));
    const first = await screen.findByRole("textbox", {
      name: "Terminal input",
    });
    await waitFor(() => expect(document.activeElement).toBe(first));
    // The + button selects and focuses the returned terminal.
    fireEvent.click(screen.getByRole("button", { name: "Create terminal" }));
    await waitFor(() => expect(f.createTerminal).toHaveBeenCalledTimes(1));
    const second = await waitFor(() => {
      const field = screen.getByRole("textbox", { name: "Terminal input" });
      expect(field).not.toBe(first);
      return field;
    });
    await waitFor(() => expect(document.activeElement).toBe(second));
    expect(
      screen
        .getByRole("tab", { name: /^Terminal 2/ })
        .getAttribute("aria-selected"),
    ).toBe("true");
    // The accepted chord creates through the same callback and sends no PTY bytes.
    expect(fireEvent.keyDown(second, primaryT)).toBe(false);
    await waitFor(() => expect(f.createTerminal).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(
        screen
          .getByRole("tab", { name: /^Terminal 3/ })
          .getAttribute("aria-selected"),
      ).toBe("true"),
    );
    expect(f.controlTerminal).not.toHaveBeenCalled();
    const requests = f.createTerminal.mock.calls.map(
      ([request]) =>
        request as {
          shellOverride: string;
          rows: number;
          columns: number;
          mutation: { requestId: string; id: string; expectedRevision: bigint };
        },
    );
    for (const request of requests)
      expect(request).toMatchObject({
        shellOverride: "",
        rows: 24,
        columns: 80,
        mutation: { id: f.session.id, expectedRevision: 7n },
      });
    expect(requests[0]!.mutation.requestId).not.toBe(
      requests[1]!.mutation.requestId,
    );
  } finally {
    f.cleanup();
  }
});

it("annotates the terminal host and input with the effective primary+T binding", async () => {
  const f = creationFixture();
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /^Terminal 1/ }));
    const input = await screen.findByRole("textbox", {
      name: "Terminal input",
    });
    const host = input.parentElement!;
    expect(host.classList.contains("terminal-screen")).toBe(true);
    // The test platform resolves primary to Control, as the dispatch test does.
    await waitFor(() => {
      expect(input.getAttribute("aria-keyshortcuts")).toBe("Control+T");
      expect(host.getAttribute("aria-keyshortcuts")).toBe("Control+T");
    });
  } finally {
    f.cleanup();
  }
});

it("creates from primary+T only in terminal input and consumes it while creation is gated", async () => {
  let release = () => {};
  const f = creationFixture();
  f.hold(
    new Promise<void>((resolve) => {
      release = resolve;
    }),
  );
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /^Terminal 1/ }));
    const input = await screen.findByRole("textbox", {
      name: "Terminal input",
    });
    // Another control in the same panel is not terminal input.
    const plus = screen.getByRole("button", { name: "Create terminal" });
    plus.focus();
    fireEvent.keyDown(plus, primaryT);
    expect(f.createTerminal).not.toHaveBeenCalled();
    // Wrong modifiers and repeated events never create.
    await waitFor(() => expect(document.activeElement).toBe(input));
    fireEvent.keyDown(input, { ...primaryT, shiftKey: true });
    fireEvent.keyDown(input, { ...primaryT, altKey: true });
    fireEvent.keyDown(input, { ...primaryT, repeat: true });
    fireEvent.keyDown(input, { key: "t", code: "KeyT" });
    expect(f.createTerminal).not.toHaveBeenCalled();
    fireEvent.keyDown(input, primaryT);
    await waitFor(() => expect(f.createTerminal).toHaveBeenCalledTimes(1));
    // While admission is pending, the matching chord is consumed without a second request.
    await waitFor(() => expect(plus).toHaveProperty("disabled", true));
    expect(fireEvent.keyDown(input, primaryT)).toBe(false);
    expect(f.createTerminal).toHaveBeenCalledTimes(1);
  } finally {
    release();
    f.cleanup();
  }
});

it("consumes primary+T without creation for an archived session", async () => {
  const f = creationFixture("archived");
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /^Terminal 1/ }));
    const input = await screen.findByRole("textbox", {
      name: "Terminal input",
    });
    await waitFor(() => expect(document.activeElement).toBe(input));
    expect(fireEvent.keyDown(input, primaryT)).toBe(false);
    expect(f.createTerminal).not.toHaveBeenCalled();
    expect(f.controlTerminal).not.toHaveBeenCalled();
  } finally {
    f.cleanup();
  }
});

// Shared harness for explicit reuse-or-create inventory scenarios. The Session
// owner is modeled with the real tab store; the terminal pane stays unselected.
function explicitInventory(
  listResources: (request: {
    filter?: { pageToken?: string };
  }) => { resources: Resource[]; nextPageToken?: string } | Promise<never>,
  createTerminal: (request: unknown) => { terminal: Resource },
) {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: (request) => listResources(request),
    });
    router.service(TerminalService, { createTerminal });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const opened = vi.fn();
  let store: SessionTabsStore | undefined;
  function Probe() {
    store = useSessionTabsStore();
    return null;
  }
  function View() {
    const [intent, setIntent] = useState<{
      requestId: string;
      revision: bigint;
    }>();
    return (
      <>
        <Probe />
        <button
          onClick={() =>
            setIntent({ requestId: newRequestId(), revision: session.revision })
          }
        >
          Open fixture
        </button>
        <SessionTerminals
          session={session}
          selectedId=""
          tabbed
          active={false}
          sessionActive
          openIntent={intent}
          finishOpenIntent={() => setIntent(undefined)}
          openTerminal={(id) => {
            opened(id);
            store!.open(session.id, { kind: SessionTabKind.Terminal, id });
          }}
          close={() => {}}
        />
      </>
    );
  }
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionTabsProvider>
            <View />
          </SessionTabsProvider>
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  return {
    session,
    opened,
    presentation: () => store!.terminalPresentation(session.id),
    open: () =>
      fireEvent.click(screen.getByRole("button", { name: "Open fixture" })),
    cleanup() {
      view.unmount();
      client.clear();
    },
  };
}

function inventoryRow(
  sessionId: string,
  state: string,
  cleanup = false,
  extra: Record<string, unknown> = {},
) {
  return create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.TERMINAL,
    sessionId,
    schemaVersion: 1,
    revision: 2n,
    documentJson: encode({ state, cleanup_verified: cleanup, ...extra }),
  });
}

it("numbers the explicit inventory in encounter order before the admitted terminal opens", async () => {
  let a!: Resource, b!: Resource;
  const createTerminal = vi.fn((_request: unknown) => ({ terminal: b }));
  const f = explicitInventory(() => ({ resources: [a, b] }), createTerminal);
  a = inventoryRow(f.session.id, "exited", true);
  b = inventoryRow(f.session.id, "running");
  try {
    f.open();
    await waitFor(() => expect(createTerminal).toHaveBeenCalledOnce());
    expect(createTerminal.mock.calls[0]![0]).toMatchObject({
      creationMode: TerminalCreationMode.REUSE_OR_CREATE,
      preferredTerminalId: b.id,
    });
    // B is queried first: it is Terminal 2 only because A was registered first.
    expect(f.presentation().number(b.id)).toBe(2);
    expect(f.presentation().number(a.id)).toBe(1);
    // Numbering grants no lifecycle authority: the exited-verified A stays visible.
    expect(f.presentation().hidden(a.id)).toBe(false);
    await waitFor(() => expect(f.opened).toHaveBeenCalledWith(b.id));
    expect(f.presentation().number(b.id)).toBe(2);
  } finally {
    f.cleanup();
  }
});

it("keeps the inventory number of a terminal admitted after the preferred one is rejected", async () => {
  let a!: Resource, b!: Resource;
  const createTerminal = vi.fn((_request: unknown) => ({ terminal: b }));
  const f = explicitInventory(() => ({ resources: [a, b] }), createTerminal);
  a = inventoryRow(f.session.id, "running");
  b = inventoryRow(f.session.id, "running");
  try {
    f.open();
    await waitFor(() => expect(createTerminal).toHaveBeenCalledOnce());
    expect(createTerminal.mock.calls[0]![0]).toMatchObject({
      preferredTerminalId: a.id,
    });
    await waitFor(() => expect(f.opened).toHaveBeenCalledWith(b.id));
    expect(f.presentation().number(a.id)).toBe(1);
    expect(f.presentation().number(b.id)).toBe(2);
  } finally {
    f.cleanup();
  }
});

it("numbers a multi-page inventory in authenticated page order and keeps repeated reads stable", async () => {
  let a!: Resource, b!: Resource, c!: Resource;
  let reorder = false;
  const createTerminal = vi.fn((_request: unknown) => ({ terminal: c }));
  const f = explicitInventory((request) => {
    const first = !request.filter?.pageToken;
    if (reorder)
      return first
        ? { resources: [c], nextPageToken: "next" }
        : { resources: [b, a] };
    return first
      ? { resources: [a, b], nextPageToken: "next" }
      : { resources: [c] };
  }, createTerminal);
  a = inventoryRow(f.session.id, "closed", true);
  b = inventoryRow(f.session.id, "exited", true);
  c = inventoryRow(f.session.id, "running");
  try {
    f.open();
    await waitFor(() => expect(createTerminal).toHaveBeenCalledOnce());
    expect(f.presentation().number(c.id)).toBe(3);
    expect(f.presentation().number(b.id)).toBe(2);
    expect(f.presentation().number(a.id)).toBe(1);
    // A repeated, reordered read never renumbers an existing terminal.
    reorder = true;
    await waitFor(() => expect(f.opened).toHaveBeenCalledWith(c.id));
    f.open();
    await waitFor(() => expect(createTerminal).toHaveBeenCalledTimes(2));
    expect(f.presentation().number(a.id)).toBe(1);
    expect(f.presentation().number(b.id)).toBe(2);
    expect(f.presentation().number(c.id)).toBe(3);
  } finally {
    f.cleanup();
  }
});

it("still numbers a running-only inventory as Terminal 1", async () => {
  let a!: Resource;
  const createTerminal = vi.fn((_request: unknown) => ({ terminal: a }));
  const f = explicitInventory(() => ({ resources: [a] }), createTerminal);
  a = inventoryRow(f.session.id, "running");
  try {
    f.open();
    await waitFor(() => expect(f.opened).toHaveBeenCalledWith(a.id));
    expect(f.presentation().number(a.id)).toBe(1);
  } finally {
    f.cleanup();
  }
});

it.each(["malformed", "foreign", "duplicate", "bad-state", "bad-pending"])(
  "allocates no presentation number for an inventory rejected as %s",
  async (scenario) => {
    let valid!: Resource;
    let bad!: Resource;
    const createTerminal = vi.fn((_request: unknown) => ({ terminal: valid }));
    const f = explicitInventory(
      (request) =>
        request.filter?.pageToken
          ? { resources: [bad] }
          : { resources: [valid], nextPageToken: "next" },
      createTerminal,
    );
    valid = inventoryRow(f.session.id, "running");
    bad = {
      malformed: inventoryRow(f.session.id, "running", false, {
        close_request_id: 12,
      }),
      foreign: inventoryRow(newRequestId(), "running"),
      duplicate: create(ResourceSchema, valid),
      "bad-state": inventoryRow(f.session.id, "frozen"),
      "bad-pending": inventoryRow(f.session.id, "running", false, {
        pending: { action: "explode" },
      }),
    }[scenario]!;
    try {
      f.open();
      await screen.findByRole("button", {
        name: "Retry terminal inventory read",
        hidden: true,
      });
      expect(createTerminal).not.toHaveBeenCalled();
      expect(f.opened).not.toHaveBeenCalled();
      // The earlier valid page is not numbered: the next terminal gets 1.
      expect(f.presentation().number(newRequestId())).toBe(1);
      expect(f.presentation().hidden(valid.id)).toBe(false);
    } finally {
      f.cleanup();
    }
  },
);

// Read Retry harness: the terminal pane is never selected unless the test asks
// for it, and every listResources call is recorded to observe polling.
function retryFixture() {
  // Installed before mounting so every interval timer runs on the fake clock;
  // the clock still follows real time so Testing Library polling keeps working.
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const terminal = inventoryRow(session.id, "running");
  const behavior: {
    next: (call: number) => Promise<{ resources: Resource[] }>;
  } = { next: async () => ({ resources: [terminal] }) };
  const listResources = vi.fn(async (_request: unknown) =>
    behavior.next(listResources.mock.calls.length),
  );
  const createTerminal = vi.fn((_request: unknown) => ({ terminal }));
  const controlTerminal = vi.fn(async (_request: unknown) => ({ terminal }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, { listResources });
    router.service(TerminalService, {
      createTerminal,
      controlTerminal,
      watchTerminalOutput: async function* (_request, context) {
        yield {
          epoch: newRequestId(),
          sequence: 0n,
          heartbeat: true,
          terminal,
        };
        await new Promise<void>((resolve) =>
          context.signal.addEventListener("abort", () => resolve(), {
            once: true,
          }),
        );
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function View() {
    const [intent, setIntent] = useState<{
      requestId: string;
      revision: bigint;
    }>();
    const [pane, setPane] = useState(false);
    const [departed, setDeparted] = useState(false);
    return (
      <>
        <button
          onClick={() =>
            setIntent({ requestId: newRequestId(), revision: session.revision })
          }
        >
          Open fixture
        </button>
        <button onClick={() => setPane(true)}>Select terminal pane</button>
        <button onClick={() => setPane(false)}>Select Conversation</button>
        <button onClick={() => setDeparted(true)}>Depart session</button>
        <button onClick={() => setDeparted(false)}>Return to session</button>
        <SessionTerminals
          session={session}
          selectedId={pane ? terminal.id : ""}
          tabbed
          active={pane && !departed}
          sessionActive={!departed}
          openIntent={intent}
          finishOpenIntent={() => setIntent(undefined)}
          openTerminal={() => {}}
          close={() => {}}
        />
      </>
    );
  }
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionTabsProvider>
            <View />
          </SessionTabsProvider>
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  const retryButton = () =>
    screen.findByRole("button", {
      name: "Retry terminal inventory read",
      hidden: true,
    });
  const click = (name: string) =>
    fireEvent.click(screen.getByRole("button", { name, hidden: true }));
  // Failing explicit-open read, which leaves the Retry button visible.
  const failOpening = async () => {
    behavior.next = async () => {
      throw new ConnectError("Fixture read failure", Code.Unavailable);
    };
    click("Open fixture");
    await retryButton();
    expect(listResources).toHaveBeenCalledTimes(1);
  };
  const settle = (milliseconds: number) =>
    act(async () => {
      await vi.advanceTimersByTimeAsync(milliseconds);
    });
  return {
    terminal,
    listResources,
    createTerminal,
    controlTerminal,
    behavior,
    click,
    retryButton,
    failOpening,
    settle,
    cleanup() {
      view.unmount();
      client.clear();
      vi.useRealTimers();
    },
  };
}

it("settles one successful Retry inventory read without selecting a terminal pane or polling", async () => {
  const f = retryFixture();
  try {
    await f.failOpening();
    f.behavior.next = async () => ({ resources: [f.terminal] });
    f.click("Retry terminal inventory read");
    await waitFor(() => expect(f.listResources).toHaveBeenCalledTimes(2));
    await f.settle(5000);
    expect(f.listResources).toHaveBeenCalledTimes(2);
    expect(f.createTerminal).not.toHaveBeenCalled();
    expect(f.controlTerminal).not.toHaveBeenCalled();
  } finally {
    f.cleanup();
  }
});

it("keeps usable retry feedback after a failed Retry and starts one bounded attempt per later Retry", async () => {
  const f = retryFixture();
  try {
    await f.failOpening();
    f.click("Retry terminal inventory read");
    await waitFor(() => expect(f.listResources).toHaveBeenCalledTimes(2));
    // The failed attempt keeps the Problem and a usable Retry button.
    const again = await f.retryButton();
    await f.settle(5000);
    expect(f.listResources).toHaveBeenCalledTimes(2);
    f.behavior.next = async () => ({ resources: [f.terminal] });
    fireEvent.click(again);
    await waitFor(() => expect(f.listResources).toHaveBeenCalledTimes(3));
    await f.settle(5000);
    expect(f.listResources).toHaveBeenCalledTimes(3);
    expect(f.createTerminal).not.toHaveBeenCalled();
    expect(f.controlTerminal).not.toHaveBeenCalled();
  } finally {
    f.cleanup();
  }
});

it("keeps the periodic refresh for a selected terminal pane and stops it again on return to Conversation", async () => {
  const f = retryFixture();
  try {
    await f.failOpening();
    f.behavior.next = async () => ({ resources: [f.terminal] });
    f.click("Retry terminal inventory read");
    await waitFor(() => expect(f.listResources).toHaveBeenCalledTimes(2));
    await f.settle(3000);
    expect(f.listResources).toHaveBeenCalledTimes(2);
    f.click("Select terminal pane");
    await f.settle(3500);
    const polled = f.listResources.mock.calls.length;
    expect(polled).toBeGreaterThanOrEqual(5);
    f.click("Select Conversation");
    await f.settle(500);
    const stopped = f.listResources.mock.calls.length;
    await f.settle(5000);
    // The retry flag cannot keep polling alive after returning to Conversation.
    expect(f.listResources).toHaveBeenCalledTimes(stopped);
    expect(f.createTerminal).not.toHaveBeenCalled();
  } finally {
    f.cleanup();
  }
});

it("does not poll when a late Retry resolves after Session departure", async () => {
  const f = retryFixture();
  try {
    await f.failOpening();
    let release!: () => void;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    f.behavior.next = async () => {
      await held;
      return { resources: [f.terminal] };
    };
    f.click("Retry terminal inventory read");
    await waitFor(() => expect(f.listResources).toHaveBeenCalledTimes(2));
    f.click("Depart session");
    await act(async () => release());
    await f.settle(5000);
    expect(f.listResources).toHaveBeenCalledTimes(2);
    f.click("Return to session");
    await f.settle(5000);
    expect(f.listResources).toHaveBeenCalledTimes(2);
    expect(f.createTerminal).not.toHaveBeenCalled();
    expect(f.controlTerminal).not.toHaveBeenCalled();
  } finally {
    f.cleanup();
  }
});

// Additional-creation harness driven by the real Session tab store, so pane
// navigation before the CreateTerminal acknowledgment can be simulated.
function navigationFixture(options: { rejectFirst?: boolean } = {}) {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const origin = inventoryRow(session.id, "running");
  const other = inventoryRow(session.id, "running");
  const added = inventoryRow(session.id, "running");
  const known = new Map([origin, other, added].map((row) => [row.id, row]));
  let gate: Promise<void> | undefined;
  const createTerminal = vi.fn(async (_request: unknown) => {
    await gate;
    if (options.rejectFirst && createTerminal.mock.calls.length === 1)
      throw new ConnectError("acknowledgment lost", Code.Unavailable);
    return { terminal: added };
  });
  const controlTerminal = vi.fn(async (_request: unknown) => ({
    terminal: origin,
  }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [origin, other] }),
    });
    router.service(TerminalService, {
      createTerminal,
      controlTerminal,
      watchTerminalOutput: async function* (request, context) {
        yield {
          epoch: newRequestId(),
          sequence: 0n,
          heartbeat: true,
          terminal: known.get(request.terminalId),
        };
        await new Promise<void>((resolve) =>
          context.signal.addEventListener("abort", () => resolve(), {
            once: true,
          }),
        );
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  let store!: SessionTabsStore;
  const key = (id: string) =>
    sessionTabKey({ kind: SessionTabKind.Terminal, id });
  function View() {
    store = useSessionTabsStore();
    const tabs = useSessionTabs(session.id);
    const [departed, setDeparted] = useState(false);
    const [intent, setIntent] = useState<{
      requestId: string;
      revision: bigint;
    }>();
    return (
      <>
        <button onClick={() => setDeparted(true)}>Depart session</button>
        <button onClick={() => setDeparted(false)}>Return to session</button>
        <button
          onClick={() =>
            setIntent({ requestId: newRequestId(), revision: session.revision })
          }
        >
          Open fixture
        </button>
        <SessionTerminals
          session={session}
          close={() => {}}
          tabbed
          sessionActive={!departed}
          openIntent={intent}
          finishOpenIntent={() => setIntent(undefined)}
          active={!departed && tabs.tab.kind === SessionTabKind.Terminal}
          selectedId={
            tabs.tab.kind === SessionTabKind.Terminal ? tabs.tab.id : ""
          }
          openTerminal={(id) =>
            tabs.store.open(session.id, { kind: SessionTabKind.Terminal, id })
          }
        />
      </>
    );
  }
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionTabsProvider>
            <ShortcutProvider>
              <View />
            </ShortcutProvider>
          </SessionTabsProvider>
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  return {
    session,
    origin,
    other,
    added,
    createTerminal,
    controlTerminal,
    selected: () => store.snapshot(session.id).selected,
    keyOf: key,
    navigate(action: (store: SessionTabsStore, id: string) => void) {
      act(() => action(store, session.id));
    },
    openOrigin() {
      act(() =>
        store.open(session.id, {
          kind: SessionTabKind.Terminal,
          id: origin.id,
        }),
      );
    },
    depart() {
      fireEvent.click(screen.getByRole("button", { name: "Depart session" }));
    },
    returnToSession() {
      fireEvent.click(
        screen.getByRole("button", { name: "Return to session" }),
      );
    },
    hold() {
      let release = () => {};
      gate = new Promise<void>((resolve) => {
        release = resolve;
      });
      return () => {
        gate = undefined;
        release();
      };
    },
    addedTab: () => document.getElementById(`terminal-tab-${added.id}`),
    createButton: () =>
      screen.getByRole("button", {
        name: "Create terminal",
        hidden: true,
      }) as HTMLButtonElement,
    cleanup() {
      view.unmount();
      client.clear();
    },
  };
}

it("selects and focuses the additional terminal when no newer navigation happened", async () => {
  const f = navigationFixture();
  try {
    f.openOrigin();
    const first = await screen.findByRole("textbox", {
      name: "Terminal input",
    });
    await waitFor(() => expect(f.createButton().disabled).toBe(false));
    fireEvent.click(f.createButton());
    await waitFor(() => expect(f.selected()).toBe(f.keyOf(f.added.id)));
    const second = await waitFor(() => {
      const field = screen.getByRole("textbox", { name: "Terminal input" });
      expect(field).not.toBe(first);
      return field;
    });
    await waitFor(() => expect(document.activeElement).toBe(second));
    expect(f.createTerminal).toHaveBeenCalledTimes(1);
  } finally {
    f.cleanup();
  }
});

it.each([
  [
    "Files",
    (store: SessionTabsStore, id: string) => {
      store.open(id, { kind: SessionTabKind.Files });
    },
  ],
  [
    "Conversation",
    (store: SessionTabsStore, id: string) => {
      store.select(id, SessionTabKind.Conversation);
    },
  ],
] as const)(
  "keeps %s selected when it was chosen before the additional creation returns",
  async (_name, navigate) => {
    const f = navigationFixture();
    try {
      f.openOrigin();
      await screen.findByRole("textbox", { name: "Terminal input" });
      await waitFor(() => expect(f.createButton().disabled).toBe(false));
      const release = f.hold();
      fireEvent.click(f.createButton());
      await waitFor(() => expect(f.createTerminal).toHaveBeenCalledOnce());
      f.navigate(navigate);
      const chosen = f.selected();
      await act(async () => release());
      // The acknowledged terminal is retained and listed for later opening.
      await waitFor(() => expect(f.addedTab()).not.toBeNull());
      expect(f.selected()).toBe(chosen);
      expect(f.createTerminal).toHaveBeenCalledOnce();
      expect(f.controlTerminal).not.toHaveBeenCalled();
      expect(f.addedTab()!.textContent).toMatch(/^Terminal 3/);
    } finally {
      f.cleanup();
    }
  },
);

it("keeps another terminal selected when it was chosen before the creation returns", async () => {
  const f = navigationFixture();
  try {
    f.openOrigin();
    await screen.findByRole("textbox", { name: "Terminal input" });
    await waitFor(() => expect(f.createButton().disabled).toBe(false));
    const release = f.hold();
    fireEvent.click(f.createButton());
    await waitFor(() => expect(f.createTerminal).toHaveBeenCalledOnce());
    f.navigate((store, id) =>
      store.open(id, { kind: SessionTabKind.Terminal, id: f.other.id }),
    );
    await act(async () => release());
    await waitFor(() => expect(f.addedTab()).not.toBeNull());
    expect(f.selected()).toBe(f.keyOf(f.other.id));
  } finally {
    f.cleanup();
  }
});

it("preserves a later choice after navigating away and back to the origin terminal", async () => {
  const f = navigationFixture();
  try {
    f.openOrigin();
    await screen.findByRole("textbox", { name: "Terminal input" });
    await waitFor(() => expect(f.createButton().disabled).toBe(false));
    const release = f.hold();
    fireEvent.click(f.createButton());
    await waitFor(() => expect(f.createTerminal).toHaveBeenCalledOnce());
    f.navigate((store, id) => store.open(id, { kind: SessionTabKind.Files }));
    f.navigate((store, id) =>
      store.open(id, { kind: SessionTabKind.Terminal, id: f.origin.id }),
    );
    expect(f.selected()).toBe(f.keyOf(f.origin.id));
    await act(async () => release());
    await waitFor(() => expect(f.addedTab()).not.toBeNull());
    expect(f.selected()).toBe(f.keyOf(f.origin.id));
  } finally {
    f.cleanup();
  }
});

it("keeps the later choice for primary+T without a second creation or PTY bytes", async () => {
  const f = navigationFixture();
  try {
    f.openOrigin();
    const input = await screen.findByRole("textbox", {
      name: "Terminal input",
    });
    await waitFor(() => expect(document.activeElement).toBe(input));
    const release = f.hold();
    expect(fireEvent.keyDown(input, primaryT)).toBe(false);
    await waitFor(() => expect(f.createTerminal).toHaveBeenCalledOnce());
    f.navigate((store, id) => store.open(id, { kind: SessionTabKind.Files }));
    await act(async () => release());
    await waitFor(() => expect(f.addedTab()).not.toBeNull());
    expect(f.selected()).toBe(SessionTabKind.Files);
    expect(f.createTerminal).toHaveBeenCalledOnce();
    expect(f.controlTerminal).not.toHaveBeenCalled();
  } finally {
    f.cleanup();
  }
});

it("does not select an exact retry acknowledged after the user navigated away", async () => {
  const f = navigationFixture({ rejectFirst: true });
  try {
    f.openOrigin();
    await screen.findByRole("textbox", { name: "Terminal input" });
    await waitFor(() => expect(f.createButton().disabled).toBe(false));
    fireEvent.click(f.createButton());
    const retry = await screen.findByRole("button", {
      name: "Retry the same terminal creation",
      hidden: true,
    });
    f.navigate((store, id) => store.open(id, { kind: SessionTabKind.Files }));
    fireEvent.click(retry);
    await waitFor(() => expect(f.createTerminal).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(f.addedTab()).not.toBeNull());
    expect(f.selected()).toBe(SessionTabKind.Files);
    expect(f.controlTerminal).not.toHaveBeenCalled();
  } finally {
    f.cleanup();
  }
});

it("selects an exact retry acknowledged with no newer navigation", async () => {
  const f = navigationFixture({ rejectFirst: true });
  try {
    f.openOrigin();
    await screen.findByRole("textbox", { name: "Terminal input" });
    await waitFor(() => expect(f.createButton().disabled).toBe(false));
    fireEvent.click(f.createButton());
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Retry the same terminal creation",
        hidden: true,
      }),
    );
    await waitFor(() => expect(f.selected()).toBe(f.keyOf(f.added.id)));
    expect(f.createTerminal).toHaveBeenCalledTimes(2);
  } finally {
    f.cleanup();
  }
});

it("still selects the terminal admitted by the toolbar reuse-or-create gesture from Conversation", async () => {
  const f = navigationFixture();
  try {
    expect(f.selected()).toBe(SessionTabKind.Conversation);
    fireEvent.click(
      await screen.findByRole("button", { name: "Open fixture" }),
    );
    await waitFor(() => expect(f.createTerminal).toHaveBeenCalledOnce());
    expect(f.createTerminal.mock.calls[0]![0]).toMatchObject({
      creationMode: TerminalCreationMode.REUSE_OR_CREATE,
    });
    await waitFor(() => expect(f.selected()).toBe(f.keyOf(f.added.id)));
  } finally {
    f.cleanup();
  }
});

it.each(["button", "primary+T"] as const)(
  "preserves the origin terminal after Session departure and return before %s creation acknowledgment",
  async (trigger) => {
    const f = navigationFixture();
    try {
      f.openOrigin();
      const input = await screen.findByRole("textbox", {
        name: "Terminal input",
      });
      await waitFor(() => expect(f.createButton().disabled).toBe(false));
      const release = f.hold();
      if (trigger === "button") fireEvent.click(f.createButton());
      else expect(fireEvent.keyDown(input, primaryT)).toBe(false);
      await waitFor(() => expect(f.createTerminal).toHaveBeenCalledOnce());
      f.depart();
      f.returnToSession();
      expect(f.selected()).toBe(f.keyOf(f.origin.id));
      const returnedInput = await screen.findByRole("textbox", {
        name: "Terminal input",
      });
      await waitFor(() => expect(document.activeElement).toBe(returnedInput));
      await act(async () => release());
      await waitFor(() => expect(f.addedTab()).not.toBeNull());
      expect(f.addedTab()!.textContent).toMatch(/^Terminal 3/);
      expect(f.selected()).toBe(f.keyOf(f.origin.id));
      expect(document.activeElement).toBe(returnedInput);
      expect(f.createTerminal).toHaveBeenCalledOnce();
      expect(f.controlTerminal).not.toHaveBeenCalled();
    } finally {
      f.cleanup();
    }
  },
);

it("retains a terminal acknowledged while the Session is absent for explicit opening", async () => {
  const f = navigationFixture();
  try {
    f.openOrigin();
    await screen.findByRole("textbox", { name: "Terminal input" });
    await waitFor(() => expect(f.createButton().disabled).toBe(false));
    const release = f.hold();
    fireEvent.click(f.createButton());
    await waitFor(() => expect(f.createTerminal).toHaveBeenCalledOnce());
    f.depart();
    await act(async () => release());
    f.returnToSession();
    await waitFor(() => expect(f.addedTab()).not.toBeNull());
    expect(f.addedTab()!.textContent).toMatch(/^Terminal 3/);
    expect(f.selected()).toBe(f.keyOf(f.origin.id));
    f.navigate((store, id) =>
      store.open(id, { kind: SessionTabKind.Terminal, id: f.added.id }),
    );
    expect(f.selected()).toBe(f.keyOf(f.added.id));
    expect(f.createTerminal).toHaveBeenCalledOnce();
    expect(f.controlTerminal).not.toHaveBeenCalled();
  } finally {
    f.cleanup();
  }
});

it("keeps exact additional-creation retry bytes after Session departure and return", async () => {
  const f = navigationFixture({ rejectFirst: true });
  try {
    f.openOrigin();
    await screen.findByRole("textbox", { name: "Terminal input" });
    await waitFor(() => expect(f.createButton().disabled).toBe(false));
    fireEvent.click(f.createButton());
    const retry = await screen.findByRole("button", {
      name: "Retry the same terminal creation",
      hidden: true,
    });
    const originalRequest = f.createTerminal.mock.calls[0]![0];
    f.depart();
    f.returnToSession();
    fireEvent.click(retry);
    await waitFor(() => expect(f.createTerminal).toHaveBeenCalledTimes(2));
    expect(f.createTerminal.mock.calls[1]![0]).toEqual(originalRequest);
    await waitFor(() => expect(f.addedTab()).not.toBeNull());
    expect(f.selected()).toBe(f.keyOf(f.origin.id));
    expect(f.controlTerminal).not.toHaveBeenCalled();
  } finally {
    f.cleanup();
  }
});

it("discards an unsent open intent when workspace readiness is lost during inventory reads and never replays it", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const opened = vi.fn(),
    createTerminal = vi.fn((_request: unknown) => ({
      terminal: create(ResourceSchema, {
        id: newRequestId(),
        kind: EntityKind.TERMINAL,
        sessionId: session.id,
        schemaVersion: 1,
        revision: 1n,
        documentJson: encode({ state: "running" }),
      }),
    }));
  let hold = false;
  const reads = vi.fn(async () => {
    if (hold) await held;
    return { resources: [] as Resource[] };
  });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, { listResources: reads });
    router.service(TerminalService, { createTerminal });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function View() {
    const [ready, setReady] = useState(true);
    const [intent, setIntent] = useState<{
      requestId: string;
      revision: bigint;
    }>();
    return (
      <>
        <button
          onClick={() =>
            setIntent({ requestId: newRequestId(), revision: session.revision })
          }
        >
          Open fixture
        </button>
        <button onClick={() => setReady((value) => !value)}>
          Toggle readiness
        </button>
        <SessionTerminals
          session={session}
          selectedId=""
          tabbed
          workspaceReady={ready}
          openIntent={intent}
          finishOpenIntent={() => setIntent(undefined)}
          openTerminal={opened}
          close={() => {}}
        />
      </>
    );
  }
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <View />
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  try {
    const plus = await screen.findByRole("button", { name: "Create terminal" });
    await waitFor(() => expect(reads).toHaveBeenCalled());
    await waitFor(() => expect(plus).toHaveProperty("disabled", false));
    // Hold the next inventory read behind the gesture, then lose readiness.
    hold = true;
    const before = reads.mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "Open fixture" }));
    await waitFor(() => expect(reads.mock.calls.length).toBeGreaterThan(before));
    fireEvent.click(screen.getByRole("button", { name: "Toggle readiness" }));
    expect(plus).toHaveProperty("disabled", true);
    await act(async () => release());
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(createTerminal).not.toHaveBeenCalled();
    // Readiness returning neither replays the discarded intent nor opens.
    hold = false;
    fireEvent.click(screen.getByRole("button", { name: "Toggle readiness" }));
    await waitFor(() => expect(plus).toHaveProperty("disabled", false));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(createTerminal).not.toHaveBeenCalled();
    expect(opened).not.toHaveBeenCalled();
    // A fresh gesture follows the original reuse-or-create flow once.
    fireEvent.click(screen.getByRole("button", { name: "Open fixture" }));
    await waitFor(() => expect(createTerminal).toHaveBeenCalledOnce());
    expect(createTerminal.mock.calls[0]?.[0]).toMatchObject({
      creationMode: TerminalCreationMode.REUSE_OR_CREATE,
    });
  } finally {
    release();
    view.unmount();
    client.clear();
  }
});

it("blocks an open intent and additional creation while the workspace is not ready", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const createTerminal = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [] }),
    });
    router.service(TerminalService, { createTerminal });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const finish = vi.fn();
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionTerminals
            session={session}
            selectedId=""
            tabbed
            workspaceReady={false}
            openIntent={{ requestId: newRequestId(), revision: 7n }}
            finishOpenIntent={finish}
            close={() => {}}
          />
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  try {
    const plus = await screen.findByRole("button", { name: "Create terminal" });
    expect(plus).toHaveProperty("disabled", true);
    await waitFor(() => expect(finish).toHaveBeenCalled());
    fireEvent.click(plus);
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(createTerminal).not.toHaveBeenCalled();
  } finally {
    view.unmount();
    client.clear();
  }
});
