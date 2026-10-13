// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  SystemCapability,
  SystemService,
  TerminalService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionTabsProvider } from "./session-tabs";
import { SessionTerminals } from "./session-terminals";
import { ShortcutProvider } from "./shortcut-provider";
import {
  dispatchAnimation,
  setAnimation,
  stubReducedMotion,
} from "./session-tool-entrance-helpers";

// Component tests use a text fixture; real Ghostty parser/CSP acceptance runs in
// the external Chrome fixture. This adapter exercises input ownership only.
vi.mock("./terminal-emulator", () => ({
  openTerminalScreen: (
    host: HTMLElement,
    input: (bytes: Uint8Array) => void,
  ) => {
    const output = document.createElement("pre"),
      field = document.createElement("textarea");
    field.setAttribute("aria-label", "Terminal input");
    field.disabled = true;
    field.addEventListener("keydown", (event) => {
      if (!field.disabled && event.key === "Enter")
        input(new TextEncoder().encode(field.value + "\r"));
    });
    host.append(output, field);
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

function terminalRow(sessionId: string) {
  return create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.TERMINAL,
    sessionId,
    schemaVersion: 1,
    revision: 4n,
    documentJson: encode({ state: "running", machine_id: newRequestId() }),
  });
}

function fixture() {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 7n,
    documentJson: encode({ archive: "active" }),
  });
  const first = terminalRow(session.id),
    second = terminalRow(session.id);
  const rows = new Map([first, second].map((row) => [row.id, row]));
  const controlTerminal = vi.fn(async (_request: unknown) => ({
    terminal: first,
  }));
  const attached = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_TERMINALS_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [first, second] }),
    });
    router.service(TerminalService, {
      controlTerminal,
      watchTerminalOutput: async function* (request, context) {
        attached(request.terminalId);
        yield {
          epoch: newRequestId(),
          sequence: 0n,
          heartbeat: true,
          terminal: rows.get(request.terminalId),
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
  let setActive!: (value: boolean) => void;
  let setSelected!: (value: string) => void;
  function View() {
    const [active, setActiveState] = useState(true);
    const [selected, setSelectedState] = useState(first.id);
    setActive = setActiveState;
    setSelected = setSelectedState;
    return (
      <SessionTerminals
        session={session}
        close={() => {}}
        tabbed
        sessionActive={active}
        active={active}
        selectedId={selected}
        openTerminal={setSelectedState}
      />
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
    first,
    second,
    controlTerminal,
    attached,
    setActive: (value: boolean) => act(() => setActive(value)),
    select: (value: string) => act(() => setSelected(value)),
    cleanup() {
      view.unmount();
      client.clear();
    },
  };
}

const header = () =>
  document.querySelector<HTMLElement>("header.terminal-dock-header")!;
const entranceNodes = () => document.querySelectorAll("[data-entrance]");

beforeEach(() => {
  stubReducedMotion(false);
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("animates only the dock header once per Terminal activation and keeps input and attach counts", async () => {
  const f = fixture();
  try {
    expect(header().getAttribute("data-entrance")).toBe("y2");
    // The aside, emulator canvas, tabs and size owners never carry the attribute.
    expect(entranceNodes().length).toBe(1);
    expect(header().closest("[data-entrance]")).toBe(header());
    dispatchAnimation(header());
    const input = await screen.findByRole("textbox", {
      name: "Terminal input",
    });
    await waitFor(() => expect(document.activeElement).toBe(input));
    expect(input.closest("[data-entrance]")).toBeNull();
    expect(
      document.querySelector(".terminal-screen")!.closest("[data-entrance]"),
    ).toBeNull();
    fireEvent.change(input, { target: { value: "ls" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(f.controlTerminal).toHaveBeenCalledTimes(1));
    const attachments = f.attached.mock.calls.length;
    // Switching terminals keeps the Terminal tool active: no replay.
    f.select(f.second.id);
    await waitFor(() =>
      expect(f.attached.mock.calls.length).toBeGreaterThan(attachments),
    );
    expect(entranceNodes().length).toBe(0);
    expect(f.controlTerminal).toHaveBeenCalledTimes(1);
    // Leaving and returning is one new explicit activation.
    f.setActive(false);
    f.setActive(true);
    expect(header().getAttribute("data-entrance")).toBe("y2");
    expect(entranceNodes().length).toBe(1);
    expect(f.controlTerminal).toHaveBeenCalledTimes(1);
  } finally {
    f.cleanup();
  }
});

it("settles an in-flight entrance on departure and leaves no stale attribute after rapid re-entry", async () => {
  const f = fixture();
  try {
    expect(header().getAttribute("data-entrance")).toBe("y2");
    f.setActive(false);
    expect(entranceNodes().length).toBe(0);
    f.setActive(true);
    expect(entranceNodes().length).toBe(1);
    dispatchAnimation(header());
    expect(entranceNodes().length).toBe(0);
  } finally {
    f.cleanup();
  }
});

it("is suppressed by Animation Off and reduced motion without replay on re-enable", async () => {
  const change = stubReducedMotion(false);
  const f = fixture();
  try {
    expect(header().getAttribute("data-entrance")).toBe("y2");
    await setAnimation("false");
    expect(entranceNodes().length).toBe(0);
    await setAnimation(undefined);
    expect(entranceNodes().length).toBe(0);
    f.setActive(false);
    f.setActive(true);
    expect(header().getAttribute("data-entrance")).toBe("y2");
    change(true);
    await waitFor(() => expect(entranceNodes().length).toBe(0));
    change(false);
    expect(entranceNodes().length).toBe(0);
  } finally {
    f.cleanup();
  }
});
