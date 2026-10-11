import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { InboxService, newRequestId } from "@delinoio/delidev-api-client";
import { MutationIntents } from "./mutation";
import { useForegroundSessionInboxRead } from "./session-inbox-read";

const session = newRequestId();
let focused = true;
beforeEach(() => {
  focused = true;
  vi.spyOn(document, "hasFocus").mockImplementation(() => focused);
});
afterEach(() => vi.restoreAllMocks());

function Probe({ id = session, eligible = true, loaded = true }: { id?: string; eligible?: boolean; loaded?: boolean }) {
  useForegroundSessionInboxRead({ sessionId: id, eligible, loaded });
  return null;
}
function mount(mark: (request: { requestId: string; sessionId: string }) => Promise<unknown>, props: Parameters<typeof Probe>[0] = {}) {
  const transport = createRouterTransport(router => router.service(InboxService, {
    markSessionInboxRead: request => mark(request).then(() => ({ requestId: request.requestId, sessionId: request.sessionId, markedCount: 2n, observedAt: "2026-10-11T00:00:00Z" })) as never,
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (next: Parameters<typeof Probe>[0]) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Probe {...next} /></MutationIntents></QueryClientProvider></TransportProvider>;
  const rendered = render(view(props));
  return { rerender: (next: Parameters<typeof Probe>[0]) => rendered.rerender(view(next)) };
}

it("acknowledges once after successful foreground loading and ignores re-renders", async () => {
  const mark = vi.fn(async (_: { requestId: string; sessionId: string }) => undefined);
  const view = mount(mark, { loaded: false });
  await act(async () => {});
  expect(mark).not.toHaveBeenCalled();
  view.rerender({ loaded: true });
  await waitFor(() => expect(mark).toHaveBeenCalledTimes(1));
  expect(mark.mock.calls[0][0].sessionId).toBe(session);
  view.rerender({ loaded: true });
  view.rerender({ loaded: false });
  view.rerender({ loaded: true });
  await act(async () => {});
  expect(mark).toHaveBeenCalledTimes(1);
});

it("submits nothing while unfocused, hidden or ineligible and again after returning to the foreground", async () => {
  const mark = vi.fn(async (_: { requestId: string; sessionId: string }) => undefined);
  focused = false;
  const view = mount(mark);
  await act(async () => {});
  expect(mark).not.toHaveBeenCalled();
  view.rerender({ eligible: false });
  await act(async () => { focused = true; window.dispatchEvent(new Event("focus")); });
  expect(mark).not.toHaveBeenCalled();
  view.rerender({ eligible: true });
  await waitFor(() => expect(mark).toHaveBeenCalledTimes(1));
  await act(async () => { focused = false; window.dispatchEvent(new Event("blur")); });
  await act(async () => { focused = true; window.dispatchEvent(new Event("focus")); });
  await waitFor(() => expect(mark).toHaveBeenCalledTimes(2));
  expect(mark.mock.calls[1][0].requestId).not.toBe(mark.mock.calls[0][0].requestId);
});

it("acknowledges each newly selected chat once", async () => {
  const mark = vi.fn(async (_: { requestId: string; sessionId: string }) => undefined);
  const other = newRequestId();
  const view = mount(mark);
  await waitFor(() => expect(mark).toHaveBeenCalledTimes(1));
  view.rerender({ id: other });
  await waitFor(() => expect(mark).toHaveBeenCalledTimes(2));
  expect(mark.mock.calls[1][0].sessionId).toBe(other);
});
