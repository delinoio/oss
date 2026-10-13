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
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  TerminalQuery,
  TerminalService,
  SystemService,
  SystemCapability,
  newRequestId,
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
vi.mock("./terminal-emulator", () => ({
  openTerminalScreen: (
    host: HTMLElement,
    input: (bytes: Uint8Array) => void,
  ) => {
    const field = document.createElement("textarea");
    field.setAttribute("aria-label", "Terminal input");
    field.addEventListener("keydown", (event) => {
      if (event.key === "Enter" && !field.disabled)
        input(new Uint8Array([120]));
    });
    host.append(field);
    return {
      write: async () => {},
      enabled: (value: boolean) => {
        field.disabled = !value;
      },
      focus: () => field.focus(),
      dispose: () => host.replaceChildren(),
    };
  },
}));
interface FixtureOptions {
  /** Wire Session-owned tab store callbacks like the real tabbed Session. */
  wired?: boolean;
  /** Extra siblings rendered inside the same connection mutation registry. */
  extra?: ReactNode;
  /** Report inventory rows in reverse resource order. */
  reverse?: boolean;
}
function fixture(
  count = 1,
  initialExited = false,
  offPage = false,
  options: FixtureOptions = {},
) {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const rows = Array.from({ length: count }, () =>
    create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.TERMINAL,
      sessionId: session.id,
      schemaVersion: 1,
      revision: 4n,
      documentJson: encode({
        state: initialExited ? "exited" : "running",
        cleanup_verified: initialExited,
      }),
    }),
  );
  let inventory = offPage ? [] : options.reverse ? [...rows].reverse() : rows,
    release = () => {},
    event: (typeof rows)[number] | undefined,
    fail = false;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const open = vi.fn(),
    close = vi.fn(),
    control = vi.fn(),
    createTerminal = vi.fn(),
    getResource = vi.fn(() => ({ resource: rows[0] }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, {
      getResource,
      listResources: () => ({ resources: inventory }),
    });
    router.service(TerminalService, {
      controlTerminal: control,
      createTerminal,
      watchTerminalOutput: async function* (request, context) {
        const terminal = rows.find((value) => value.id === request.terminalId)!;
        const epoch = newRequestId();
        yield { epoch, sequence: 0n, terminal, heartbeat: true };
        await held;
        if (context.signal.aborted) return;
        if (fail)
          throw new ConnectError("fixture disconnect", Code.Unavailable);
        if (event)
          yield { epoch, sequence: 0n, terminal: event, heartbeat: true };
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const hide = vi.fn();
  let store: SessionTabsStore | undefined;
  // Mirrors the tabbed Session wiring: the store owns content-tab order and
  // selection while SessionTerminals reports verified exits.
  function Wired() {
    const tabs = useSessionTabs(session.id);
    store = useSessionTabsStore();
    return (
      <SessionTerminals
        session={session}
        close={close}
        tabbed
        selectedId={
          tabs.tab.kind === SessionTabKind.Terminal ? tabs.tab.id : ""
        }
        openTerminal={(id) =>
          tabs.store.open(session.id, { kind: SessionTabKind.Terminal, id })
        }
        dismissTerminal={(id, fallback) => {
          tabs.store.dismissTerminal(session.id, id, fallback);
        }}
        hideEmpty={() => {
          hide();
          if (tabs.store.liveTerminals(session.id).length) return;
          for (const tab of tabs.store.snapshot(session.id).tabs)
            if (tab.kind === SessionTabKind.Terminal)
              tabs.store.close(session.id, sessionTabKey(tab));
        }}
      />
    );
  }
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionTabsProvider>
            {options.wired ? (
              <Wired />
            ) : (
              <SessionTerminals
                session={session}
                close={close}
                selectedId={offPage ? rows[0]!.id : undefined}
                openTerminal={open}
              />
            )}
            {options.extra}
          </SessionTabsProvider>
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  return {
    rows,
    session,
    hide,
    tabs: () => store!,
    omit(...at: number[]) {
      inventory = rows.filter((_, index) => !at.includes(index));
    },
    open,
    close,
    control,
    createTerminal,
    getResource,
    exit(at = 0, change: object = {}) {
      event = create(ResourceSchema, {
        ...rows[at]!,
        revision: 5n,
        documentJson: encode({ state: "exited", cleanup_verified: true }),
        ...change,
      });
      release();
    },
    end(error = false) {
      fail = error;
      release();
    },
    async inventoryExit(...exited: number[]) {
      const next = rows.map((row, index) =>
        exited.includes(index)
          ? create(ResourceSchema, {
              ...row,
              revision: 5n,
              documentJson: encode({ state: "exited", cleanup_verified: true }),
            })
          : row,
      );
      inventory = options.reverse ? next.reverse() : next;
      // The panel has no Refresh control: invalidate the retained inventory read.
      await client.invalidateQueries();
    },
    cleanup() {
      view.unmount();
      release();
      client.clear();
    },
  };
}
it("dismisses matching newer watch metadata and hides the final dock exactly once without process operations", async () => {
  const test = fixture();
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
    await screen.findByRole("textbox", { name: "Terminal input" });
    test.exit();
    await waitFor(() => expect(screen.queryByRole("tab")).toBeNull());
    expect(test.close).toHaveBeenCalledTimes(1);
    expect(test.control).not.toHaveBeenCalled();
    expect(test.createTerminal).not.toHaveBeenCalled();
    await test.inventoryExit(0);
    await waitFor(() => expect(screen.queryByRole("tab")).toBeNull());
    expect(test.close).toHaveBeenCalledTimes(1);
  } finally {
    test.cleanup();
  }
});
it.each([0, 1])(
  "selected exit %i selects the nearest remaining terminal",
  async (at) => {
    const test = fixture(3);
    try {
      fireEvent.click(
        await screen.findByRole("tab", {
          name: new RegExp(`Terminal ${at + 1}`),
        }),
      );
      await screen.findByRole("textbox", { name: "Terminal input" });
      test.exit(at);
      await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(2));
      const selected = screen
        .getAllByRole("tab")
        .find((tab) => tab.getAttribute("aria-selected") === "true");
      expect(selected?.id).toBe(
        `terminal-tab-${test.rows[at === 0 ? 1 : 0]!.id}`,
      );
      expect(test.close).not.toHaveBeenCalled();
    } finally {
      test.cleanup();
    }
  },
);
it("inactive inventory exit preserves selection and focus", async () => {
  const test = fixture(3);
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /Terminal 2/ }));
    const input = await screen.findByRole("textbox", {
      name: "Terminal input",
    });
    await waitFor(() => expect(document.activeElement).toBe(input));
    await test.inventoryExit(0);
    input.focus();
    await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(2));
    expect(document.activeElement).toBe(input);
    expect(screen.getByRole("tab", { selected: true }).id).toBe(
      `terminal-tab-${test.rows[1]!.id}`,
    );
    expect(test.close).not.toHaveBeenCalled();
  } finally {
    test.cleanup();
  }
});
it("an explicitly opened exited-only inventory keeps creation available without hiding", async () => {
  const test = fixture(2, true);
  try {
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Create terminal" }),
      ).toHaveProperty("disabled", false),
    );
    expect(screen.queryAllByRole("tab")).toHaveLength(0);
    expect(test.close).not.toHaveBeenCalled();
  } finally {
    test.cleanup();
  }
});
it.each([false, true])(
  "stream completion or failure %s retains the terminal and reattachment",
  async (error) => {
    const test = fixture();
    try {
      fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
      await screen.findByRole("textbox", { name: "Terminal input" });
      test.end(error);
      await screen.findByRole("button", { name: "Reattach original terminal" });
      expect(screen.getAllByRole("tab")).toHaveLength(1);
      expect(test.close).not.toHaveBeenCalled();
    } finally {
      test.cleanup();
    }
  },
);
it.each([
  { sessionId: "foreign" },
  { schemaVersion: 2 },
  { documentJson: encode({ state: "exited", cleanup_verified: "true" }) },
  { revision: 3n },
  { documentJson: new TextEncoder().encode("invalid") },
])(
  "untrusted or incomplete watch evidence retains presentation",
  async (change) => {
    const test = fixture();
    try {
      fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
      await screen.findByRole("textbox", { name: "Terminal input" });
      test.exit(0, change);
      await waitFor(() =>
        expect(
          screen.getByRole("button", { name: "Reattach original terminal" }),
        ).toBeTruthy(),
      );
      expect(screen.getAllByRole("tab")).toHaveLength(1);
      expect(test.close).not.toHaveBeenCalled();
    } finally {
      test.cleanup();
    }
  },
);

it("verified exit retains exact uncertain input recovery until the original receipt resolves", async () => {
  const test = fixture();
  test.control.mockRejectedValueOnce(
    new ConnectError("fixture acknowledgment lost", Code.Unavailable),
  );
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
    const input = await screen.findByRole("textbox", {
      name: "Terminal input",
    });
    await waitFor(() => expect(input).toHaveProperty("disabled", false));
    fireEvent.keyDown(input, { key: "Enter" });
    await screen.findByRole("button", {
      name: "Retry the same terminal operation",
    });
    test.exit();
    await waitFor(() =>
      expect(
        screen.getByRole("button", {
          name: "Retry the same terminal operation",
        }),
      ).toBeTruthy(),
    );
    await new Promise((resolve) => setTimeout(resolve, 30));
    expect(screen.getAllByRole("tab")).toHaveLength(1);
    expect(test.close).not.toHaveBeenCalled();
    test.control.mockResolvedValueOnce({
      terminal: create(ResourceSchema, {
        ...test.rows[0]!,
        revision: 5n,
        documentJson: encode({ state: "exited", cleanup_verified: true }),
      }),
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Retry the same terminal operation" }),
    );
    await waitFor(() => expect(test.close).toHaveBeenCalledTimes(1));
    expect(test.control.mock.calls[1]![0]).toEqual(
      test.control.mock.calls[0]![0],
    );
  } finally {
    test.cleanup();
  }
});

it("an explicit ID outside the inventory dismisses only after its own exit without extra reads", async () => {
  const test = fixture(1, false, true);
  try {
    await screen.findByRole("textbox", { name: "Terminal input" });
    await screen.findByRole("tab", { name: /Terminal 1/ });
    expect(test.getResource).toHaveBeenCalledTimes(1);
    expect(test.close).not.toHaveBeenCalled();
    test.exit();
    await waitFor(() => expect(test.close).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByRole("tab")).toBeNull());
    expect(test.getResource).toHaveBeenCalledTimes(1);
    expect(test.control).not.toHaveBeenCalled();
    expect(test.createTerminal).not.toHaveBeenCalled();
  } finally {
    test.cleanup();
  }
});

it("a late original creation acknowledgment cannot replace presentation after verified exit", async () => {
  const test = fixture();
  let accept = () => {};
  const receipt = new Promise<{ terminal: (typeof test.rows)[number] }>(
    (resolve) => {
      accept = () => resolve({ terminal: test.rows[0]! });
    },
  );
  test.createTerminal.mockImplementation(() => receipt);
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
    await screen.findByRole("textbox", { name: "Terminal input" });
    fireEvent.click(screen.getByRole("button", { name: "Create terminal" }));
    await waitFor(() => expect(test.createTerminal).toHaveBeenCalledTimes(1));
    test.exit();
    // Tombstoning is immediate; the final surface hide waits for the receipt.
    await waitFor(() => expect(screen.queryByRole("tab")).toBeNull());
    await new Promise((resolve) => setTimeout(resolve, 30));
    expect(test.close).not.toHaveBeenCalled();
    accept();
    await waitFor(() => expect(test.close).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Create terminal" }),
      ).toHaveProperty("disabled", false),
    );
    expect(test.close).toHaveBeenCalledTimes(1);
    expect(test.open).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("tab")).toBeNull();
    expect(test.control).not.toHaveBeenCalled();
  } finally {
    accept();
    test.cleanup();
  }
});

it("verified exit keeps the final surface while an uncertain creation is unresolved and hides once after the same retry", async () => {
  const test = fixture();
  test.createTerminal.mockRejectedValueOnce(
    new ConnectError("fixture acknowledgment lost", Code.Unavailable),
  );
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
    await screen.findByRole("textbox", { name: "Terminal input" });
    fireEvent.click(screen.getByRole("button", { name: "Create terminal" }));
    const retry = await screen.findByRole("button", {
      name: "Retry the same terminal creation",
    });
    test.exit();
    await waitFor(() => expect(screen.queryByRole("tab")).toBeNull());
    await new Promise((resolve) => setTimeout(resolve, 30));
    // The final hide is fenced and nothing retries automatically.
    expect(test.close).not.toHaveBeenCalled();
    expect(test.createTerminal).toHaveBeenCalledTimes(1);
    expect(
      screen.getByRole("button", { name: "Retry the same terminal creation" }),
    ).toBe(retry);
    test.createTerminal.mockResolvedValueOnce({ terminal: test.rows[0]! });
    fireEvent.click(retry);
    await waitFor(() => expect(test.close).toHaveBeenCalledTimes(1));
    expect(test.createTerminal).toHaveBeenCalledTimes(2);
    expect(test.createTerminal.mock.calls[1]![0]).toEqual(
      test.createTerminal.mock.calls[0]![0],
    );
    // The late receipt names the dismissed terminal and cannot reopen it.
    expect(screen.queryByRole("tab")).toBeNull();
    await new Promise((resolve) => setTimeout(resolve, 30));
    expect(test.close).toHaveBeenCalledTimes(1);
    expect(test.control).not.toHaveBeenCalled();
  } finally {
    test.cleanup();
  }
});

it("creation busy in an unrelated session does not fence the final hide", async () => {
  function Other() {
    const other = useRetainedMutation(
      "terminal-create:another-session",
      TerminalQuery.createTerminal,
    );
    return (
      <button
        onClick={() =>
          void other.send({
            mutation: {
              requestId: newRequestId(),
              id: "another-session",
              expectedRevision: 1n,
            },
            shellOverride: "",
            rows: 24,
            columns: 80,
          })
        }
      >
        Create in other session
      </button>
    );
  }
  const test = fixture(1, false, false, { extra: <Other /> });
  test.createTerminal.mockImplementationOnce(() => new Promise(() => {}));
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
    await screen.findByRole("textbox", { name: "Terminal input" });
    fireEvent.click(
      screen.getByRole("button", { name: "Create in other session" }),
    );
    await waitFor(() => expect(test.createTerminal).toHaveBeenCalledTimes(1));
    test.exit();
    await waitFor(() => expect(test.close).toHaveBeenCalledTimes(1));
    expect(test.control).not.toHaveBeenCalled();
  } finally {
    test.cleanup();
  }
});

it("a verified exit keeps an opened terminal absent from the replacement inventory chain", async () => {
  const test = fixture(2, false, false, { wired: true });
  try {
    const session = test.session.id;
    await screen.findByRole("button", { name: /Terminal 2/ });
    act(() => {
      test
        .tabs()
        .open(session, { kind: SessionTabKind.Terminal, id: test.rows[0]!.id });
      test
        .tabs()
        .open(session, { kind: SessionTabKind.Terminal, id: test.rows[1]!.id });
    });
    await screen.findByRole("textbox", { name: "Terminal input" });
    // Cursor-expiry Reload replaced the chain with B only; A stays opened.
    test.omit(0);
    await act(async () => {
      await test.inventoryExit(1);
    });
    await waitFor(() =>
      expect(test.tabs().liveTerminals(session)).toEqual([test.rows[0]!.id]),
    );
    expect(test.tabs().snapshot(session).selected).toBe(
      sessionTabKey({ kind: SessionTabKind.Terminal, id: test.rows[0]!.id }),
    );
    expect(test.hide).not.toHaveBeenCalled();
    expect(test.close).not.toHaveBeenCalled();
    expect(test.control).not.toHaveBeenCalled();
    expect(test.createTerminal).not.toHaveBeenCalled();
  } finally {
    test.cleanup();
  }
});

it("a genuine final exit hides exactly once through the Session-owned path", async () => {
  const test = fixture(1, false, false, { wired: true });
  try {
    const session = test.session.id;
    await screen.findByRole("button", { name: /Terminal 1/ });
    act(() => {
      test
        .tabs()
        .open(session, { kind: SessionTabKind.Terminal, id: test.rows[0]!.id });
    });
    await screen.findByRole("textbox", { name: "Terminal input" });
    test.exit();
    await waitFor(() => expect(test.hide).toHaveBeenCalledTimes(1));
    expect(test.tabs().liveTerminals(session)).toEqual([]);
    expect(
      test
        .tabs()
        .snapshot(session)
        .tabs.some((tab) => tab.kind === SessionTabKind.Terminal),
    ).toBe(false);
    expect(test.tabs().snapshot(session).selected).toBe(
      SessionTabKind.Conversation,
    );
    await test.inventoryExit(0);
    await new Promise((resolve) => setTimeout(resolve, 30));
    expect(test.hide).toHaveBeenCalledTimes(1);
    expect(test.close).not.toHaveBeenCalled();
    expect(test.control).not.toHaveBeenCalled();
    expect(test.createTerminal).not.toHaveBeenCalled();
  } finally {
    test.cleanup();
  }
});

it.each([false, true])(
  "one refresh reporting exits of the selected A and B selects live C in order (reverse %s)",
  async (reverse) => {
    const test = fixture(3, false, false, { wired: true, reverse });
    try {
      const session = test.session.id;
      await screen.findByRole("button", { name: /Terminal 3/ });
      act(() => {
        test.tabs().open(session, {
          kind: SessionTabKind.Terminal,
          id: test.rows[0]!.id,
        });
      });
      await screen.findByRole("textbox", { name: "Terminal input" });
      await act(async () => {
        await test.inventoryExit(0, 1);
      });
      const survivor = test.rows[2]!.id;
      await waitFor(() =>
        expect(test.tabs().liveTerminals(session)).toEqual([survivor]),
      );
      expect(test.tabs().snapshot(session).selected).toBe(
        sessionTabKey({ kind: SessionTabKind.Terminal, id: survivor }),
      );
      expect(test.hide).not.toHaveBeenCalled();
      expect(test.control).not.toHaveBeenCalled();
      expect(test.createTerminal).not.toHaveBeenCalled();
      // The surviving selected terminal is attached.
      await screen.findByRole("textbox", { name: "Terminal input" });
    } finally {
      test.cleanup();
    }
  },
);

it("one refresh reporting every terminal exited takes the final dismissal path once", async () => {
  const test = fixture(3, false, false, { wired: true });
  try {
    const session = test.session.id;
    await screen.findByRole("button", { name: /Terminal 3/ });
    act(() => {
      test
        .tabs()
        .open(session, { kind: SessionTabKind.Terminal, id: test.rows[0]!.id });
    });
    await screen.findByRole("textbox", { name: "Terminal input" });
    await act(async () => {
      await test.inventoryExit(0, 1, 2);
    });
    await waitFor(() => expect(test.hide).toHaveBeenCalledTimes(1));
    expect(test.tabs().liveTerminals(session)).toEqual([]);
    expect(test.tabs().snapshot(session).selected).toBe(
      SessionTabKind.Conversation,
    );
    await new Promise((resolve) => setTimeout(resolve, 30));
    expect(test.hide).toHaveBeenCalledTimes(1);
  } finally {
    test.cleanup();
  }
});

it("inactive batch exits leave the selected terminal unchanged", async () => {
  const test = fixture(3, false, false, { wired: true });
  try {
    const session = test.session.id;
    await screen.findByRole("button", { name: /Terminal 3/ });
    act(() => {
      test
        .tabs()
        .open(session, { kind: SessionTabKind.Terminal, id: test.rows[0]!.id });
      test
        .tabs()
        .open(session, { kind: SessionTabKind.Terminal, id: test.rows[2]!.id });
    });
    await screen.findByRole("textbox", { name: "Terminal input" });
    await act(async () => {
      await test.inventoryExit(0, 1);
    });
    await waitFor(() =>
      expect(test.tabs().liveTerminals(session)).toEqual([test.rows[2]!.id]),
    );
    expect(test.tabs().snapshot(session).selected).toBe(
      sessionTabKey({ kind: SessionTabKind.Terminal, id: test.rows[2]!.id }),
    );
    expect(test.hide).not.toHaveBeenCalled();
  } finally {
    test.cleanup();
  }
});

it("the standalone dock selects and focuses live C after A and B exit in one refresh", async () => {
  const test = fixture(3);
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
    await screen.findByRole("textbox", { name: "Terminal input" });
    await test.inventoryExit(0, 1);
    await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(1));
    const survivor = `terminal-tab-${test.rows[2]!.id}`;
    expect(screen.getByRole("tab", { selected: true }).id).toBe(survivor);
    await waitFor(() => {
      const tab = document.getElementById(survivor);
      expect(
        document.activeElement === tab ||
          !!document
            .querySelector(".terminal-view")
            ?.contains(document.activeElement),
      ).toBe(true);
    });
    // One final selection opens exactly C after the click opened A: no intermediate B.
    expect(test.open.mock.calls).toEqual([
      [test.rows[0]!.id],
      [test.rows[2]!.id],
    ]);
    expect(test.close).not.toHaveBeenCalled();
    expect(test.control).not.toHaveBeenCalled();
    expect(test.createTerminal).not.toHaveBeenCalled();
  } finally {
    test.cleanup();
  }
});

it("a user selection made before the deferred batch focus suppresses it", async () => {
  const frames: FrameRequestCallback[] = [];
  const request = vi
    .spyOn(window, "requestAnimationFrame")
    .mockImplementation((callback) => frames.push(callback));
  const test = fixture(4);
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
    await screen.findByRole("textbox", { name: "Terminal input" });
    await test.inventoryExit(0, 1);
    await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(2));
    // The user picks D before the queued focus for C runs.
    fireEvent.click(screen.getByRole("tab", { name: /Terminal 4/ }));
    await waitFor(() =>
      expect(screen.getByRole("tab", { selected: true }).id).toBe(
        `terminal-tab-${test.rows[3]!.id}`,
      ),
    );
    const survivor = document.getElementById(
      `terminal-tab-${test.rows[2]!.id}`,
    );
    for (const frame of frames.splice(0)) frame(0);
    expect(document.activeElement).not.toBe(survivor);
  } finally {
    request.mockRestore();
    test.cleanup();
  }
});
