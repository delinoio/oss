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
  EntityKind,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionNameEditorProvider } from "./session-name-editor";
import {
  SessionTabKind,
  SessionTabsProvider,
  sessionTabKey,
  useSessionTabsStore,
  type SessionTabsStore,
} from "./session-tabs";
import { SessionView } from "./session";

// Retained published Sidechats keep their own embedded SessionView. These
// tests cover how its header entrance follows tab selection, pending
// promotion and a held late reply, with synthetic resources only.
function resource(id: string, name: string) {
  return create(ResourceSchema, {
    id,
    sessionId: id,
    kind: EntityKind.SESSION,
    revision: 3n,
    schemaVersion: 1,
    documentJson: encode({
      name,
      workspace: "general-chat",
      outcome: "stopped",
      archive: "active",
      dispatch: "blocked",
      recovery: "none",
    }),
  });
}

function setup() {
  const parentId = newRequestId(),
    childId = newRequestId();
  const parent = resource(parentId, "Original session"),
    child = resource(childId, "Sidechat session");
  const control = vi.fn(async () => ({ change: { session: parent } }));
  const reads = vi.fn();
  // Holds the child's late original reply until the test releases it.
  let release: () => void = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getOverview: () => {
        throw new ConnectError("Unavailable", Code.Unimplemented);
      },
      getStatus: () => ({ capabilities: [] }),
    });
    router.service(SessionService, {
      listQueue: () => ({ inputs: [], nextPageToken: "" }),
      controlSession: control,
    });
    router.service(ResourceService, {
      getSnapshot: () => ({ resources: [parent, child], cursor: "snapshot" }),
      getResource: async (request) => {
        reads(request.id);
        if (request.id === childId) await held;
        return { resource: request.id === childId ? child : parent };
      },
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
  const stores: SessionTabsStore[] = [];
  function Capture() {
    stores.push(useSessionTabsStore());
    return null;
  }
  const view = () => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <SessionNameEditorProvider>
            <SessionTabsProvider>
              <Capture />
              <SessionView id={parentId} draft="" setDraft={() => {}} active />
            </SessionTabsProvider>
          </SessionNameEditorProvider>
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  return {
    parentId,
    childId,
    control,
    reads,
    release,
    view,
    store: () => stores[stores.length - 1]!,
  };
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
function dispatch(node: Element, name: string) {
  const event = new Event("animationend", { bubbles: true });
  Object.defineProperty(event, "animationName", { value: name });
  act(() => {
    node.dispatchEvent(event);
  });
}
const parentHeader = () =>
  document.querySelector<HTMLElement>(
    ".session-tabbed > header.session-header > .session-header-entrance",
  )!;
const childHeader = () =>
  document.querySelector<HTMLElement>(
    ".session-sidechat-pane header.session-header > .session-header-entrance",
  )!;
const entrances = () => document.querySelectorAll("[data-entrance]");

beforeEach(() => {
  reducedMotion(false);
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("plays one entrance per explicit Sidechat selection and none for promotion or late replies", async () => {
  const f = setup();
  render(f.view());
  await screen.findByRole("textbox", { name: "Message" });
  dispatch(parentHeader(), "entrance-y2");
  const parentTab = f.store().snapshot(f.parentId).tabs[0]!;
  const pending = {
    kind: SessionTabKind.PendingSidechat,
    requestId: "late-original-request",
    name: "Pending",
  } as const;
  // A pending original Sidechat is inspected: it has no heading to animate.
  act(() => f.store().open(f.parentId, pending));
  expect(f.store().snapshot(f.parentId).selected).toBe(sessionTabKey(pending));
  expect(entrances().length).toBe(0);

  // Pending-to-accepted promotion moves the selection to the child tab but is
  // not an explicit activation, and the draft is carried over unchanged.
  const child = {
    kind: SessionTabKind.Sidechat,
    id: f.childId,
    name: "Sidechat",
  } as const;
  act(() => {
    expect(
      f.store().publishSidechat(f.parentId, pending.requestId, child, {
        text: "Retained draft",
        start: 3,
        end: 3,
        focus: false,
      }),
    ).toBe(true);
  });
  expect(f.store().snapshot(f.parentId).selected).toBe(sessionTabKey(child));
  await waitFor(() => expect(childHeader()).not.toBeNull());
  const composer = document.getElementById(
    `prompt-${f.childId}`,
  ) as HTMLTextAreaElement;
  expect(composer.value).toBe("Retained draft");
  expect(childHeader().getAttribute("data-entrance")).toBeNull();
  expect(entrances().length).toBe(0);

  // Switching away does not animate the Session header (the Session itself
  // stays active); switching back is a new explicit activation: one entrance.
  act(() => f.store().select(f.parentId, sessionTabKey(parentTab)));
  expect(entrances().length).toBe(0);
  act(() => f.store().select(f.parentId, sessionTabKey(child)));
  expect(childHeader().getAttribute("data-entrance")).toBe("y2");
  expect(parentHeader().getAttribute("data-entrance")).toBeNull();
  // Only the header wrapper moves; composer and transcript never do.
  expect(entrances().length).toBe(1);
  expect(childHeader().contains(composer)).toBe(false);
  expect(document.getElementById(`prompt-${f.childId}`)).toBe(composer);
  const node = childHeader();

  // The late original reply and typing do not restart or add an entrance.
  fireEvent.change(composer, { target: { value: "Edited draft" } });
  f.release();
  await act(async () => {
    await Promise.resolve();
  });
  expect(childHeader()).toBe(node);
  expect(childHeader().getAttribute("data-entrance")).toBe("y2");
  expect(entrances().length).toBe(1);
  dispatch(node, "entrance-y2");
  expect(entrances().length).toBe(0);
  expect(composer.value).toBe("Edited draft");
  expect(document.getElementById(`prompt-${f.childId}`)).toBe(composer);
  // Motion never creates, sends or cancels anything.
  expect(f.control).not.toHaveBeenCalled();
});

it("does not play a Sidechat entrance published in the background", async () => {
  const f = setup();
  render(f.view());
  await screen.findByRole("textbox", { name: "Message" });
  dispatch(parentHeader(), "entrance-y2");
  const parentTab = f.store().snapshot(f.parentId).tabs[0]!;
  const pending = {
    kind: SessionTabKind.PendingSidechat,
    requestId: "background-request",
    name: "Pending",
  } as const;
  act(() => f.store().open(f.parentId, pending));
  // The user navigates elsewhere before the original request is accepted.
  act(() => f.store().select(f.parentId, sessionTabKey(parentTab)));
  const child = {
    kind: SessionTabKind.Sidechat,
    id: f.childId,
    name: "Sidechat",
  } as const;
  act(() => {
    f.store().publishSidechat(f.parentId, pending.requestId, child, {
      text: "",
      start: 0,
      end: 0,
      focus: false,
    });
  });
  expect(f.store().snapshot(f.parentId).selected).toBe(
    sessionTabKey(parentTab),
  );
  await waitFor(() => expect(childHeader()).not.toBeNull());
  expect(entrances().length).toBe(0);
  // The first explicit selection plays once.
  act(() => f.store().select(f.parentId, sessionTabKey(child)));
  expect(childHeader().getAttribute("data-entrance")).toBe("y2");
  dispatch(childHeader(), "entrance-y2");
  expect(entrances().length).toBe(0);
  expect(f.control).not.toHaveBeenCalled();
});

it("plays one entrance when the user opens a Sidechat that mounts selected", async () => {
  const f = setup();
  render(f.view());
  await screen.findByRole("textbox", { name: "Message" });
  dispatch(parentHeader(), "entrance-y2");
  const child = {
    kind: SessionTabKind.Sidechat,
    id: f.childId,
    name: "Sidechat",
  } as const;
  // An explicit open (for example from a Search result) mounts the retained
  // pane selected, and that is an explicit activation.
  act(() => f.store().open(f.parentId, child));
  expect(f.store().snapshot(f.parentId).selected).toBe(sessionTabKey(child));
  await waitFor(() => expect(childHeader()).not.toBeNull());
  expect(childHeader().getAttribute("data-entrance")).toBe("y2");
  expect(parentHeader().getAttribute("data-entrance")).toBeNull();
  expect(entrances().length).toBe(1);
  dispatch(childHeader(), "entrance-y2");
  expect(entrances().length).toBe(0);
  expect(f.control).not.toHaveBeenCalled();
});

it("settles a Sidechat entrance for reduced motion and Animation Off without replay", async () => {
  const change = reducedMotion(false);
  const f = setup();
  render(f.view());
  await screen.findByRole("textbox", { name: "Message" });
  dispatch(parentHeader(), "entrance-y2");
  const parentTab = f.store().snapshot(f.parentId).tabs[0]!;
  const child = {
    kind: SessionTabKind.Sidechat,
    id: f.childId,
    name: "Sidechat",
  } as const;
  act(() => f.store().open(f.parentId, child));
  await waitFor(() => expect(childHeader()).not.toBeNull());
  // The explicit open already played once; settle it, then select away and
  // back to make another explicit activation.
  dispatch(childHeader(), "entrance-y2");
  expect(entrances().length).toBe(0);
  act(() => f.store().select(f.parentId, sessionTabKey(parentTab)));
  act(() => f.store().select(f.parentId, sessionTabKey(child)));
  expect(childHeader().getAttribute("data-entrance")).toBe("y2");
  change(true);
  expect(childHeader().getAttribute("data-entrance")).toBeNull();
  change(false);
  expect(childHeader().getAttribute("data-entrance")).toBeNull();
  // Rapid leave and re-entry never leaves a stale attribute.
  act(() => f.store().select(f.parentId, sessionTabKey(parentTab)));
  document.documentElement.dataset.animation = "false";
  act(() => f.store().select(f.parentId, sessionTabKey(child)));
  expect(childHeader().getAttribute("data-entrance")).toBeNull();
  expect(f.control).not.toHaveBeenCalled();
});
