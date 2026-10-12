import { captureSharedTabLayout } from "./test-shared-tabs-layout";
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
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents, useRetainedMutation } from "./mutation";
import { SessionTabsProvider, useSessionTabsStore } from "./session-tabs";
import { SessionTabBar } from "./session-tab-bar";
import { ShortcutProvider } from "./shortcut-provider";
import { useSessionTabs, SessionTabKind } from "./session-tabs";
import { SessionTerminals } from "./session-terminals";

// Component tests use a text fixture; real Ghostty parser/CSP acceptance runs in
// the external Chrome fixture. This adapter exercises input ownership only.
vi.mock("./terminal-emulator", () => ({
  openTerminalScreen: (
    host: HTMLElement,
    input: (bytes: Uint8Array) => void,
    _resize: unknown,
    _unavailable: unknown,
    shortcut?: (event: KeyboardEvent) => boolean,
  ) => {
    const output = document.createElement("pre"),
      field = document.createElement("textarea");
    const shortcutListener = (event: KeyboardEvent) => {
      if (shortcut?.(event)) {
        event.preventDefault();
        event.stopImmediatePropagation();
      }
    };
    host.addEventListener("keydown", shortcutListener, true);
    field.setAttribute("aria-label", "Terminal input");
    field.disabled = true;
    field.addEventListener("keydown", (event) => {
      if (
        !field.disabled &&
        event.key.toLowerCase() === "t" &&
        (event.ctrlKey || event.metaKey)
      )
        input(new Uint8Array([20]));
      if (!field.disabled && event.key === "Enter")
        input(new TextEncoder().encode(field.value + "\r"));
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
      dispose: () => {
        host.removeEventListener("keydown", shortcutListener, true);
        host.replaceChildren();
      },
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
    fireEvent.click(await screen.findByRole("tab", { name: /Terminal · 1/ }));
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
  const createTerminal = vi.fn((_request: unknown) => ({ terminal }));
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
    await screen.findByRole("tab", { name: /Terminal · 50/ });
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
    await captureSharedTabLayout("terminal");
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

it("gates initial reads and polling on advertised terminal support without removed refresh UI", async () => {
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
    expect(screen.queryByRole("button", { name: "Details" })).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Refresh terminals" }),
    ).toBeNull();
    const refresh = screen.getByRole("button", {
      name: "Create terminal",
    }) as HTMLButtonElement;
    expect(refresh.disabled).toBe(true);
    fireEvent.click(refresh);
    await waitForPoll();
    expect(listResources).not.toHaveBeenCalled();
    statusPending = false;
    await act(async () => release({ capabilities: [] }));
    await waitForPoll();
    expect(refresh.disabled).toBe(true);
    fireEvent.click(refresh);
    expect(listResources).not.toHaveBeenCalled();
    expect(
      screen.getByText("Waiting for a server that supports session terminals."),
    ).toBeTruthy();
    capabilities = [SystemCapability.SESSION_TERMINALS_V1];
    await act(async () => {
      await client.invalidateQueries();
    });
    await waitFor(() => expect(listResources).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(refresh.disabled).toBe(false));
    await act(async () => {
      await client.invalidateQueries();
    });
    await waitFor(() =>
      expect(listResources.mock.calls.length).toBeGreaterThan(1),
    );
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
    await waitFor(() => expect(refresh.disabled).toBe(true));
    const calls = listResources.mock.calls.length;
    await waitForPoll();
    fireEvent.click(refresh);
    expect(listResources).toHaveBeenCalledTimes(calls);
  } finally {
    release({ capabilities: [] });
    view.unmount();
    client.clear();
  }
});

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
    fireEvent.click(await screen.findByRole("tab", { name: /Terminal · 1/ }));
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
  fireEvent.click(await screen.findByRole("tab", { name: /Terminal · 1/ }));
  await screen.findByText("Original attached output");
  const input = screen.getByRole("textbox", { name: "Terminal input" });
  fireEvent.change(input, { target: { value: "Retained terminal draft" } });
  for (let index = 2; index <= 4; index++) {
    fireEvent.click(
      screen.getByRole("button", { name: "Load more Terminal history pages" }),
    );
    await screen.findByRole("tab", {
      name: new RegExp(`Terminal · ${index}`),
    });
  }
  expect(screen.getByRole("tab", { name: /Terminal · 1/ })).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "Terminal input" })).toBe(input);
  expect(input).toHaveProperty("value", "Retained terminal draft");
  expect(watch).toHaveBeenCalledTimes(1);
});

it("keeps failed terminal capability reads distinct from missing support without shell override UI", async () => {
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
  expect(screen.queryByRole("textbox")).toBeNull();
  const retry = await screen.findByRole("button", {
    name: "Retry terminal capability read",
  });
  expect(screen.queryByText(/Waiting for a server that supports/)).toBeNull();
  expect(createTerminal).not.toHaveBeenCalled();
  fireEvent.click(retry);
  await waitFor(() => expect(status).toHaveBeenCalledTimes(2));
  expect(screen.queryByRole("textbox")).toBeNull();
  expect(
    screen.getByRole("button", { name: "Create terminal" }),
  ).toHaveProperty("disabled", true);
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
  fireEvent.click(await screen.findByRole("tab", { name: /Terminal · 1/ }));
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

it("delegates terminal selection to the shared tab list without another picker", async () => {
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
  await screen.findByRole("button", { name: "Create terminal" });
  expect(screen.queryByRole("group", { name: "Terminals" })).toBeNull();
  expect(screen.queryByRole("tablist")).toBeNull();
  expect(screen.queryByRole("button", { name: /Terminal [12]/ })).toBeNull();
  expect(open).not.toHaveBeenCalled();
  view.unmount();
  client.clear();
});

it.each(["empty", "later-page", "uncertain", "read-error"])(
  "resolves explicit opening once under Strict Mode (%s)",
  async (scenario) => {
    const session = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.SESSION,
      schemaVersion: 1,
      revision: 7n,
      documentJson: encode({ archive: "active" }),
    });
    const row = (state: string, cleanup = false) =>
      create(ResourceSchema, {
        id: newRequestId(),
        sessionId: session.id,
        kind: EntityKind.TERMINAL,
        schemaVersion: 1,
        revision: 2n,
        documentJson: encode({ state, cleanup_verified: cleanup }),
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
            resources: scenario === "uncertain" ? [row("uncertain")] : [],
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
      createTerminal = vi.fn((_request: unknown) => ({ terminal }));
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
    await screen.findByRole("button", { name: "Create terminal" });
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
      createTerminal = vi.fn((_request: unknown) => ({ terminal })),
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
    await screen.findByRole("button", { name: "Create terminal" });
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

const terminalFixture = () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const terminal = create(ResourceSchema, {
    id: newRequestId(),
    sessionId: session.id,
    kind: EntityKind.TERMINAL,
    schemaVersion: 1,
    revision: 2n,
    documentJson: encode({ state: "running" }),
  });
  return { session, terminal };
};

function DirectOpeningFixture({
  session,
}: {
  session: ReturnType<typeof terminalFixture>["session"];
}) {
  const tabs = useSessionTabs(session.id);
  const [target, setTarget] = useState<HTMLDivElement | null>(null);
  const [intent, setIntent] = useState<{
    requestId: string;
    revision: bigint;
  }>();
  return (
    <>
      <SessionTabBar
        id="fixture"
        tabs={tabs.tabs}
        selected={tabs.selected}
        terminalNumber={(id) =>
          tabs.store.terminalPresentation(session.id).number(id)
        }
        select={(key) => {
          setIntent(undefined);
          tabs.store.select(session.id, key);
        }}
        close={(key) => tabs.store.close(session.id, key)}
      />
      <div ref={setTarget} data-testid="opening-strip" />
      <button
        onClick={() =>
          setIntent(
            (current) =>
              current ?? {
                requestId: newRequestId(),
                revision: session.revision,
              },
          )
        }
      >
        Open original terminal
      </button>
      <button onClick={() => setIntent(undefined)}>Leave opening</button>
      <p>Original conversation draft</p>
      <SessionTerminals
        session={session}
        close={() => {}}
        tabbed
        active={tabs.tab.kind === SessionTabKind.Terminal}
        resolveActive
        selectedId={
          tabs.tab.kind === SessionTabKind.Terminal ? tabs.tab.id : ""
        }
        recoveryTarget={target}
        openIntent={intent}
        resumeOpenIntent={setIntent}
        finishOpenIntent={() => setIntent(undefined)}
        openTerminal={(id) =>
          tabs.store.open(session.id, { kind: SessionTabKind.Terminal, id })
        }
      />
    </>
  );
}

it.each([
  "admitted",
  "uncertain",
  "read-error",
  "unsupported",
  "depart-before-send",
])(
  "keeps Conversation and visible original recovery while terminal attachment is hidden (%s)",
  async (scenario) => {
    const { session, terminal } = terminalFixture();
    let releaseRead = () => {};
    const read = new Promise<void>((resolve) => {
      releaseRead = resolve;
    });
    let releaseStream = () => {};
    const held = new Promise<void>((resolve) => {
      releaseStream = resolve;
    });
    const createTerminal = vi.fn(async (_request: unknown) => ({ terminal }));
    if (scenario === "uncertain")
      createTerminal.mockRejectedValueOnce(
        new ConnectError("receipt unavailable", Code.Unavailable),
      );
    const listResources = vi.fn(async () => {
      await read;
      if (scenario === "read-error")
        throw new ConnectError("read unavailable", Code.Unavailable);
      return { resources: [] };
    });
    const watch = vi.fn(async function* () {
      yield { epoch: newRequestId(), sequence: 0n, heartbeat: true, terminal };
      await held;
    });
    const transport = createRouterTransport((router) => {
      router.service(SystemService, {
        getStatus: () => ({
          capabilities:
            scenario === "unsupported"
              ? []
              : [SystemCapability.SESSION_TERMINALS_V1],
        }),
      });
      router.service(ResourceService, { listResources });
      router.service(TerminalService, {
        createTerminal,
        watchTerminalOutput: watch,
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
              <DirectOpeningFixture session={session} />
            </SessionTabsProvider>
          </MutationIntents>
        </TransportProvider>
      </QueryClientProvider>,
    );
    try {
      fireEvent.click(
        screen.getByRole("button", { name: "Open original terminal" }),
      );
      fireEvent.click(
        screen.getByRole("button", { name: "Open original terminal" }),
      );
      expect(
        screen
          .getByRole("tab", { name: "Conversation" })
          .getAttribute("aria-selected"),
      ).toBe("true");
      expect(screen.getAllByRole("tab")).toHaveLength(1);
      expect(screen.getByText("Original conversation draft")).toBeTruthy();
      expect(watch).not.toHaveBeenCalled();
      if (scenario === "unsupported") {
        expect(
          await screen.findByText(
            "Waiting for a server that supports session terminals.",
          ),
        ).toBeTruthy();
        fireEvent.click(
          screen.getByRole("button", {
            name: "Retry terminal capability read",
          }),
        );
        expect(createTerminal).not.toHaveBeenCalled();
        expect(listResources).not.toHaveBeenCalled();
        return;
      }
      await waitFor(() => expect(listResources).toHaveBeenCalledOnce());
      expect(screen.getByTestId("opening-strip").textContent).toContain(
        "Opening terminal",
      );
      if (scenario === "depart-before-send")
        fireEvent.click(screen.getByRole("button", { name: "Leave opening" }));
      await act(async () => releaseRead());
      if (scenario === "depart-before-send") {
        expect(createTerminal).not.toHaveBeenCalled();
        expect(screen.getAllByRole("tab")).toHaveLength(1);
      } else if (scenario === "read-error") {
        const retry = await screen.findByRole("button", {
          name: "Retry terminal inventory read",
        });
        expect(screen.getByTestId("opening-strip").contains(retry)).toBe(true);
        fireEvent.click(retry);
        await waitFor(() =>
          expect(listResources.mock.calls.length).toBeGreaterThan(1),
        );
        expect(createTerminal).not.toHaveBeenCalled();
        expect(screen.getAllByRole("tab")).toHaveLength(1);
      } else {
        if (scenario === "uncertain") {
          const retry = await screen.findByRole("button", {
            name: "Retry the same terminal creation",
          });
          expect(screen.getByTestId("opening-strip").contains(retry)).toBe(
            true,
          );
          expect(screen.getAllByRole("tab")).toHaveLength(1);
          const original = createTerminal.mock.calls[0];
          // Repeated opening is still the original unresolved operation.
          fireEvent.click(
            screen.getByRole("button", { name: "Open original terminal" }),
          );
          expect(createTerminal).toHaveBeenCalledOnce();
          fireEvent.click(retry);
          await waitFor(() => expect(createTerminal).toHaveBeenCalledTimes(2));
          expect(createTerminal.mock.calls[1]?.[0]).toEqual(original?.[0]);
        }
        const tab = await screen.findByRole("tab", { name: "Terminal 1" });
        expect(tab.getAttribute("aria-selected")).toBe("true");
        expect(createTerminal).toHaveBeenCalledTimes(
          scenario === "uncertain" ? 2 : 1,
        );
        expect(screen.queryByRole("tab", { name: "Terminals" })).toBeNull();
        expect(tab.title).toBe("Terminal 1");
        const input = await screen.findByRole("textbox", {
          name: "Terminal input",
        });
        await waitFor(() => expect(document.activeElement).toBe(input));
        fireEvent.click(
          screen.getByRole("button", { name: "Close Terminal 1 tab" }),
        );
        expect(
          screen
            .getByRole("tab", { name: "Conversation" })
            .getAttribute("aria-selected"),
        ).toBe("true");
        expect(createTerminal).toHaveBeenCalledTimes(
          scenario === "uncertain" ? 2 : 1,
        );
      }
    } finally {
      releaseRead();
      view.unmount();
      releaseStream();
      client.clear();
    }
  },
);

it("shares default-shell additional creation between + and pre-PTY terminal input primary+T", async () => {
  const { session, terminal } = terminalFixture();
  let releaseStream = () => {};
  const held = new Promise<void>((resolve) => {
    releaseStream = resolve;
  });
  const createTerminal = vi.fn((_request: unknown) => ({ terminal }));
  const controlTerminal = vi.fn((_request: unknown) => ({ terminal }));
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
          <ShortcutProvider>
            <SessionTerminals session={session} close={() => {}} />
          </ShortcutProvider>
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
    expect(screen.queryByRole("button", { name: "Details" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Hide terminals" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Close terminal" })).toBeNull();
    expect(
      view.container.querySelector(".terminal-resource-details"),
    ).toBeNull();
    const button = screen.getByRole("button", { name: "Create terminal" });
    fireEvent.click(button);
    await waitFor(() => expect(createTerminal).toHaveBeenCalledOnce());
    await waitFor(() => expect(button).toHaveProperty("disabled", false));
    input.focus();
    fireEvent.keyDown(input, { key: "t", code: "KeyT", ctrlKey: true });
    await waitFor(() => expect(createTerminal).toHaveBeenCalledTimes(2));
    const first = createTerminal.mock.calls[0]![0] as unknown as {
      mutation: { requestId: string };
    };
    const second = createTerminal.mock.calls[1]![0] as unknown as {
      mutation: { requestId: string };
    };
    expect(first).toMatchObject({
      mutation: { id: session.id, expectedRevision: 7n },
      shellOverride: "",
      rows: 24,
      columns: 80,
    });
    expect(second).toMatchObject({
      mutation: { id: session.id, expectedRevision: 7n },
      shellOverride: "",
      rows: 24,
      columns: 80,
    });
    expect(second.mutation.requestId).not.toBe(first.mutation.requestId);
    expect(controlTerminal).not.toHaveBeenCalled();
    button.focus();
    fireEvent.keyDown(button, { key: "t", code: "KeyT", ctrlKey: true });
    expect(createTerminal).toHaveBeenCalledTimes(2);
    // An archived terminal can remain attached; its matching chord is consumed.
    view.rerender(
      <QueryClientProvider client={client}>
        <TransportProvider transport={transport}>
          <MutationIntents>
            <ShortcutProvider>
              <SessionTerminals
                session={create(ResourceSchema, {
                  ...session,
                  documentJson: encode({ archive: "archived" }),
                })}
                close={() => {}}
              />
            </ShortcutProvider>
          </MutationIntents>
        </TransportProvider>
      </QueryClientProvider>,
    );
    input.focus();
    fireEvent.keyDown(input, { key: "t", code: "KeyT", ctrlKey: true });
    expect(createTerminal).toHaveBeenCalledTimes(2);
    expect(controlTerminal).not.toHaveBeenCalled();
  } finally {
    view.unmount();
    releaseStream();
    client.clear();
  }
});

it("recovers a hidden terminal control with its exact original bytes and retains verified-exit tombstones", async () => {
  const { session, terminal } = terminalFixture();
  const exited = create(ResourceSchema, {
    ...terminal,
    revision: 3n,
    documentJson: encode({ state: "exited", cleanup_verified: true }),
  });
  const controlTerminal = vi.fn(async (_request: unknown) => ({
    terminal: exited,
  }));
  controlTerminal.mockRejectedValueOnce(
    new ConnectError("receipt lost", Code.Unavailable),
  );
  const transport = createRouterTransport((router) => {
    router.service(TerminalService, { controlTerminal });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  let presentation: ReturnType<
    ReturnType<typeof useSessionTabsStore>["terminalPresentation"]
  >;
  function View() {
    const store = useSessionTabsStore();
    presentation = store.terminalPresentation(session.id);
    presentation.observe(session.id, terminal);
    const [target, setTarget] = useState<HTMLDivElement | null>(null);
    const control = useRetainedMutation(
      `terminal-control:${terminal.id}`,
      TerminalQuery.controlTerminal,
    );
    return (
      <>
        <div ref={setTarget} data-testid="original-recovery" />
        <button
          onClick={() =>
            void control.send({
              mutation: {
                id: terminal.id,
                requestId: newRequestId(),
                expectedRevision: terminal.revision,
              },
              action: TerminalAction.INPUT,
              input: new Uint8Array([0xe2, 0x82, 0xac, 13]),
            })
          }
        >
          Send original control
        </button>
        <SessionTerminals
          session={session}
          close={() => {}}
          active={false}
          resolveActive
          recoveryTarget={target}
          tabbed
          selectedId=""
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
    fireEvent.click(
      screen.getByRole("button", { name: "Send original control" }),
    );
    const retry = await screen.findByRole("button", {
      name: "Retry the same terminal operation",
    });
    expect(screen.getByTestId("original-recovery").contains(retry)).toBe(true);
    const original = controlTerminal.mock.calls[0]?.[0];
    fireEvent.click(retry);
    await waitFor(() => expect(controlTerminal).toHaveBeenCalledTimes(2));
    expect(controlTerminal.mock.calls[1]?.[0]).toEqual(original);
    await waitFor(() => expect(presentation!.hidden(terminal.id)).toBe(true));
    expect(presentation!.number(terminal.id)).toBe(1);
    presentation!.observe(session.id, terminal);
    expect(presentation!.hidden(terminal.id)).toBe(true);
    expect(
      screen.queryByRole("textbox", { name: "Terminal input" }),
    ).toBeNull();
  } finally {
    view.unmount();
    client.clear();
  }
});

it("keeps capability recovery outside an already selected terminal pane", async () => {
  const { session, terminal } = terminalFixture();
  const list = vi.fn(),
    createTerminal = vi.fn(),
    watch = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(ResourceService, { listResources: list });
    router.service(TerminalService, {
      createTerminal,
      watchTerminalOutput: watch,
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function Selected() {
    const [target, setTarget] = useState<HTMLDivElement | null>(null);
    return (
      <>
        <div ref={setTarget} data-testid="selected-recovery" />
        <SessionTerminals
          session={session}
          selectedId={terminal.id}
          tabbed
          recoveryTarget={target}
          close={() => {}}
        />
      </>
    );
  }
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <Selected />
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  try {
    const retry = await screen.findByRole("button", {
      name: "Retry terminal capability read",
    });
    expect(retry.closest('[data-testid="selected-recovery"]')).not.toBeNull();
    expect(
      screen.getAllByText(
        "Waiting for a server that supports session terminals.",
      ),
    ).toHaveLength(1);
    expect(list).not.toHaveBeenCalled();
    expect(createTerminal).not.toHaveBeenCalled();
    expect(watch).not.toHaveBeenCalled();
  } finally {
    view.unmount();
    client.clear();
  }
});
