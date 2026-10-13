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
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  BudgetState,
  EntityKind,
  ResourceSchema,
  ResourceService,
  SessionBudgetViewSchema,
  SessionService,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionNameEditorProvider } from "./session-name-editor";
import { SessionTabsProvider } from "./session-tabs";
import { SessionView } from "./session";

function fixture() {
  const id = newRequestId();
  const session = create(ResourceSchema, {
    id,
    sessionId: id,
    kind: EntityKind.SESSION,
    revision: 7n,
    schemaVersion: 1,
    documentJson: encode({
      name: "Original session",
      workspace: "general-chat",
      outcome: "stopped",
      archive: "active",
      dispatch: "blocked",
      recovery: "none",
    }),
  });
  const control = vi.fn(async () => ({ change: { session } }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getOverview: () => {
        throw new ConnectError("Unavailable", Code.Unimplemented);
      },
      getStatus: () => ({ capabilities: [] }),
    });
    router.service(SessionService, {
      listQueue: () => ({ inputs: [], nextPageToken: "" }),
      getSessionBudget: () => ({
        view: create(SessionBudgetViewSchema, {
          session,
          state: BudgetState.THRESHOLD_REACHED,
          budget: { currency: "USD", threshold: "1" },
        }),
      }),
      controlSession: control,
    });
    router.service(ResourceService, {
      getSnapshot: () => ({ resources: [session], cursor: "snapshot" }),
      getResource: () => ({ resource: session }),
      listResources: () => ({ resources: [], nextPageToken: "" }),
      async *watchEvents(_request, context) {
        await new Promise<void>((resolve) =>
          context.signal.addEventListener("abort", () => resolve(), {
            once: true,
          }),
        );
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const setDraft = vi.fn();
  const view = (draft = "Original draft", active = true, embedded = false) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <SessionNameEditorProvider>
            <SessionTabsProvider>
              <SessionView
                id={id}
                draft={draft}
                setDraft={setDraft}
                active={active}
                embedded={embedded}
              />
            </SessionTabsProvider>
          </SessionNameEditorProvider>
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  return { view, control, client };
}

function dispatch(node: Element, name: string, type = "animationend") {
  const event = new Event(type, { bubbles: true });
  Object.defineProperty(event, "animationName", { value: name });
  act(() => {
    node.dispatchEvent(event);
  });
}
function reducedMotion(initial: boolean) {
  const listeners = new Set<() => void>();
  const media = {
    matches: initial,
    addEventListener: (_: string, listener: () => void) =>
      listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) =>
      listeners.delete(listener),
  };
  vi.stubGlobal("matchMedia", () => media);
  return (next: boolean) =>
    act(() => {
      media.matches = next;
      listeners.forEach((changed) => changed());
    });
}
const header = () =>
  document.querySelector<HTMLElement>("header.session-header")!;
const info = () =>
  document.querySelector<HTMLElement>(".session-information > header")!;

beforeEach(() => {
  reducedMotion(false);
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("animates the session header once per explicit activation and never the body", async () => {
  const f = fixture();
  const mounted = render(f.view());
  const composer = await screen.findByRole("textbox", { name: "Message" });
  expect(header().getAttribute("data-entrance")).toBe("y2");
  // Info is persistent: it does not animate until it is explicitly opened.
  expect(info().getAttribute("data-entrance")).toBeNull();
  // Only the header carries motion; transcript, composer and Info body do not.
  expect(document.querySelectorAll("[data-entrance]").length).toBe(1);
  const node = header();
  fireEvent.change(composer, { target: { value: "typing during entrance" } });
  mounted.rerender(f.view("Refetched draft"));
  expect(header()).toBe(node);
  expect(composer.isConnected).toBe(true);
  expect(header().getAttribute("data-entrance")).toBe("y2");
  dispatch(node, "entrance-y2");
  expect(header().getAttribute("data-entrance")).toBeNull();
  mounted.rerender(f.view("Later draft"));
  expect(header().getAttribute("data-entrance")).toBeNull();
  expect(f.control).not.toHaveBeenCalled();
});

it("replays only when a retained session is explicitly activated again", async () => {
  const f = fixture();
  const mounted = render(f.view());
  const composer = await screen.findByRole("textbox", { name: "Message" });
  dispatch(header(), "entrance-y2");
  expect(header().getAttribute("data-entrance")).toBeNull();
  mounted.rerender(f.view("Original draft", false));
  expect(header().getAttribute("data-entrance")).toBeNull();
  mounted.rerender(f.view("Original draft", true));
  expect(header().getAttribute("data-entrance")).toBe("y2");
  // The retained controllers keep their identity through the activation.
  expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
  // Leaving before completion cannot leave stale motion on re-entry.
  mounted.rerender(f.view("Original draft", false));
  mounted.rerender(f.view("Original draft", true));
  expect(header().getAttribute("data-entrance")).toBe("y2");
  dispatch(header(), "entrance-y2");
  expect(header().getAttribute("data-entrance")).toBeNull();
});

it("does not animate an embedded Sidechat header or an inactive session", async () => {
  const embedded = render(fixture().view("", true, true));
  await screen.findByRole("textbox", { name: "Message" });
  expect(header().getAttribute("data-entrance")).toBeNull();
  embedded.unmount();
  render(fixture().view("", false));
  await waitFor(() => expect(header()).not.toBeNull());
  expect(header().getAttribute("data-entrance")).toBeNull();
});

it("plays the Info heading entrance only for an explicit opening", async () => {
  const f = fixture();
  const mounted = render(f.view());
  await screen.findByRole("textbox", { name: "Message" });
  dispatch(header(), "entrance-y2");
  fireEvent.click(await screen.findByRole("button", { name: "Show details" }));
  expect(info().getAttribute("data-entrance")).toBe("x4");
  expect(header().getAttribute("data-entrance")).toBeNull();
  const node = info();
  mounted.rerender(f.view("Streaming answer refresh"));
  expect(info()).toBe(node);
  expect(info().getAttribute("data-entrance")).toBe("x4");
  // An animation of a descendant or another primitive does not settle it.
  dispatch(node, "entrance-y2");
  expect(info().getAttribute("data-entrance")).toBe("x4");
  dispatch(node, "entrance-x4");
  expect(info().getAttribute("data-entrance")).toBeNull();
  mounted.rerender(f.view("Another refresh"));
  expect(info().getAttribute("data-entrance")).toBeNull();
  // A later explicit opening is a new activation.
  fireEvent.click(screen.getByRole("button", { name: "Show details" }));
  expect(info().getAttribute("data-entrance")).toBe("x4");
});

it("settles mid-flight for Animation Off and reduced motion without replay", async () => {
  const change = reducedMotion(false);
  const f = fixture();
  render(f.view());
  await screen.findByRole("textbox", { name: "Message" });
  expect(header().getAttribute("data-entrance")).toBe("y2");
  change(true);
  expect(header().getAttribute("data-entrance")).toBeNull();
  change(false);
  expect(header().getAttribute("data-entrance")).toBeNull();
  fireEvent.click(await screen.findByRole("button", { name: "Show details" }));
  expect(info().getAttribute("data-entrance")).toBe("x4");
  await act(async () => {
    document.documentElement.dataset.animation = "false";
    await Promise.resolve();
  });
  expect(info().getAttribute("data-entrance")).toBeNull();
  await act(async () => {
    document.documentElement.dataset.animation = "true";
    await Promise.resolve();
  });
  expect(info().getAttribute("data-entrance")).toBeNull();
});

it("does not start motion while Animation is Off", async () => {
  document.documentElement.dataset.animation = "false";
  const f = fixture();
  render(f.view());
  await screen.findByRole("textbox", { name: "Message" });
  expect(header().getAttribute("data-entrance")).toBeNull();
  fireEvent.click(await screen.findByRole("button", { name: "Show details" }));
  expect(info().getAttribute("data-entrance")).toBeNull();
});
