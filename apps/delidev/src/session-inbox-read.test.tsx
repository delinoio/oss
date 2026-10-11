// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, waitFor } from "@testing-library/react";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
import {
  InboxService,
  SystemService,
  SystemCapability,
  newRequestId,
  type MarkSessionInboxReadRequest,
} from "@delinoio/delidev-api-client";
import {
  MutationIntents,
  useRetainedMutationIntents,
  sessionInboxReadPrefix,
} from "./mutation";
import { InboxBadgePresentation } from "./inbox-badge";
import {
  SessionInboxReadBoundary,
  useSessionInboxRead,
} from "./session-inbox-read";
const native = vi.hoisted(() => ({ invoke: vi.fn(), desktop: true }));
vi.mock("@tauri-apps/api/core", () => ({
  isTauri: () => native.desktop,
  invoke: native.invoke,
}));
vi.mock("@tauri-apps/api/event", () => ({ listen: async () => () => {} }));
let focused = true,
  hidden = false;
beforeEach(() => {
  focused = true;
  hidden = false;
  native.desktop = true;
  vi.spyOn(document, "hasFocus").mockImplementation(() => focused);
  vi.spyOn(document, "visibilityState", "get").mockImplementation(() =>
    hidden ? "hidden" : "visible",
  );
  native.invoke.mockReset();
  native.invoke.mockImplementation(async (command: string) =>
    command === "begin_inbox_badge"
      ? "scope"
      : command === "read_inbox_badge_selection"
        ? { scope: "scope", generation: 1 }
        : undefined,
  );
});
afterEach(() => vi.restoreAllMocks());
function Visit({
  id,
  active,
  loaded,
}: {
  id: string;
  active: boolean;
  loaded: boolean;
}) {
  useSessionInboxRead(id, active, loaded);
  return null;
}
function Pending() {
  const attempts = useRetainedMutationIntents(sessionInboxReadPrefix);
  return <p>{attempts.length ? "Original retained" : "No pending request"}</p>;
}
function fixture(
  mark: (request: MarkSessionInboxReadRequest) => Promise<object>,
  supported = true,
) {
  const requests: MarkSessionInboxReadRequest[] = [];
  let count = 3n;
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: supported ? [SystemCapability.SESSION_INBOX_READ_V1] : [],
      }),
    });
    router.service(InboxService, {
      markSessionInboxRead: async (request) => {
        requests.push(request);
        return await mark(request);
      },
      getUnreadInboxCount: () => ({
        unreadCount: count,
        observedAt: "2026-10-11T00:00:00Z",
      }),
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const id = newRequestId();
  const view = ({
    active = true,
    loaded = true,
    ready = true,
    sender = true,
  } = {}) => (
    <StrictMode>
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <MutationIntents>
            <InboxBadgePresentation ready supported />
            <Pending />
            <SessionInboxReadBoundary ready={ready}>
              {sender ? (
                <Visit id={id} active={active} loaded={loaded} />
              ) : null}
            </SessionInboxReadBoundary>
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
    </StrictMode>
  );
  return {
    requests,
    client,
    invalidate,
    id,
    view,
    setCount: (next: bigint) => {
      count = next;
    },
  };
}
const receipt = (request: MarkSessionInboxReadRequest) => ({
  requestId: request.requestId,
  sessionId: request.sessionId,
  markedCount: 2n,
  observedAt: "2026-10-11T00:00:00Z",
});
it("admits once after successful foreground loading, including StrictMode, and ignores polling/readiness changes", async () => {
  const f = fixture(async (request) => receipt(request));
  const mounted = render(f.view({ loaded: false }));
  await waitFor(() => expect(native.invoke).toHaveBeenCalled());
  expect(f.requests).toHaveLength(0);
  mounted.rerender(f.view());
  await waitFor(() => expect(f.requests).toHaveLength(1));
  mounted.rerender(f.view({ loaded: false }));
  mounted.rerender(f.view());
  await act(async () => {
    await f.client.invalidateQueries();
  });
  expect(f.requests).toHaveLength(1);
  focused = false;
  fireEvent(window, new Event("blur"));
  focused = true;
  fireEvent(window, new Event("focus"));
  await waitFor(() => expect(f.requests).toHaveLength(2));
  expect(f.requests[1].requestId).not.toBe(f.requests[0].requestId);
});
it.each([
  "hidden",
  "unfocused",
  "inactive",
  "not-ready",
  "incomplete",
  "unsupported",
  "non-desktop",
])("does not acknowledge %s admission", async (condition) => {
  hidden = condition === "hidden";
  focused = condition !== "unfocused";
  native.desktop = condition !== "non-desktop";
  const f = fixture(
    async (request) => receipt(request),
    condition !== "unsupported",
  );
  render(
    f.view({
      active: condition !== "inactive",
      ready: condition !== "not-ready",
      loaded: condition !== "incomplete",
    }),
  );
  await act(async () => {
    await f.client.invalidateQueries();
  });
  expect(f.requests).toHaveLength(0);
});
it.each([Code.Unavailable, Code.PermissionDenied])(
  "retains exact failed bytes across navigation and retries only on a later activation (%s)",
  async (code) => {
    const f = fixture(async (request) => {
      if (f.requests.length === 1)
        throw new ConnectError("fixture uncertain", code);
      return receipt(request);
    });
    const mounted = render(f.view());
    await waitFor(() => expect(f.requests).toHaveLength(1));
    await waitFor(() =>
      expect(document.body.textContent).toContain("Original retained"),
    );
    mounted.rerender(f.view({ active: false }));
    mounted.rerender(f.view());
    await waitFor(() => expect(f.requests).toHaveLength(2));
    expect(f.requests[1]).toEqual(f.requests[0]);
    await waitFor(() =>
      expect(document.body.textContent).toContain("No pending request"),
    );
  },
);
it("refreshes original Inbox/count queries after sender disposal and publishes authoritative 3 to 1 to 0", async () => {
  let finish!: () => void;
  const f = fixture(async (request) => {
    await new Promise<void>((resolve) => {
      finish = resolve;
    });
    return receipt(request);
  });
  const mounted = render(f.view());
  await waitFor(() => expect(f.requests).toHaveLength(1));
  await waitFor(() =>
    expect(native.invoke).toHaveBeenCalledWith(
      "publish_inbox_badge",
      expect.objectContaining({ count: "3" }),
    ),
  );
  mounted.rerender(f.view({ sender: false }));
  f.setCount(1n);
  f.invalidate.mockClear();
  await act(async () => finish());
  await waitFor(() => expect(f.invalidate).toHaveBeenCalledTimes(3));
  await waitFor(() =>
    expect(native.invoke).toHaveBeenCalledWith(
      "publish_inbox_badge",
      expect.objectContaining({ count: "1" }),
    ),
  );
  f.setCount(0n);
  await act(async () => {
    await f.client.invalidateQueries();
  });
  await waitFor(() =>
    expect(native.invoke).toHaveBeenCalledWith(
      "publish_inbox_badge",
      expect.objectContaining({ count: "0" }),
    ),
  );
});

it("cannot settle an original connection request into its successor", async () => {
  let finish!: () => void;
  const original = fixture(async (request) => {
    await new Promise<void>((resolve) => {
      finish = resolve;
    });
    return receipt(request);
  });
  const mounted = render(original.view());
  await waitFor(() => expect(original.requests).toHaveLength(1));
  mounted.unmount();
  const successor = fixture(async (request) => receipt(request));
  render(successor.view({ sender: false }));
  successor.invalidate.mockClear();
  await act(async () => finish());
  expect(successor.invalidate).not.toHaveBeenCalled();
  expect(successor.requests).toHaveLength(0);
});
