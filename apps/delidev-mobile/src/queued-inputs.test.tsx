// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EntityKind, ResourceSchema, SessionService, type Resource, type ListQueueRequest } from "@delinoio/delidev-api-client";
import { QueuedInputs } from "./queued-inputs";
import { documentBytes } from "./state";
import { en } from "./localization";
vi.mock("./platform", () => ({ storage: { read: async () => null, write: async () => {} } }));
afterEach(cleanup);
const sessionId = "01900000-0000-7000-8000-000000000001";
const row = (n: number) => create(ResourceSchema, { kind: EntityKind.QUEUE, id: `01900000-0000-7000-8000-${String(n).padStart(12, "0")}`, sessionId, revision: BigInt(n), schemaVersion: 1, documentJson: documentBytes({ sequence: n, content_revision: 1, prompt: `Input ${n}`, mode: "execute", delivery: "queued" }) });
function fixture(read: (request: ListQueueRequest) => { inputs: Resource[]; nextPageToken?: string } | Promise<{ inputs: Resource[]; nextPageToken?: string }>) {
  const calls: ListQueueRequest[] = [], steer = vi.fn();
  const transport = createRouterTransport(router => router.service(SessionService, { listQueue: request => { calls.push(request); return read(request); } }));
  const cache = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (enabled = true, selectedId = sessionId) => <QueryClientProvider client={cache}><TransportProvider transport={transport}><QueuedInputs id={selectedId} enabled={enabled} available={enabled} execution="original-execution" turn="original-turn" labels={en} steer={steer} /></TransportProvider></QueryClientProvider>;
  return { calls, steer, view };
}
it("shows input 51 and steers its original revision after an explicit exact-token read", async () => {
  const f = fixture(r => r.pageToken ? { inputs: [row(51)] } : { inputs: Array.from({ length: 50 }, (_, n) => row(n + 1)), nextPageToken: "opaque==" });
  render(f.view()); await screen.findByText("Input 50");
  fireEvent.click(screen.getByRole("button", { name: en.more })); await screen.findByText("Input 51");
  await waitFor(() => expect((screen.getAllByRole("button", { name: en.steer })[50] as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getAllByRole("button", { name: en.steer })[50]!);
  expect(f.calls.map(r => [r.pageSize, r.pageToken])).toEqual([[50, ""], [50, "opaque=="]]);
  const sent = f.steer.mock.calls[0]![0]; expect(sent.mutation.id).toBe(row(51).id); expect(sent.mutation.expectedRevision).toBe(51n); expect(sent.expectedExecutionId).toBe("original-execution"); expect(sent.expectedTurnId).toBe("original-turn");
});
it("retains page one after an expired second cursor and requires explicit reload", async () => {
  const f = fixture(r => { if (r.pageToken) throw new ConnectError("private cursor sentinel", Code.InvalidArgument); return { inputs: [row(1)], nextPageToken: "expired" }; });
  render(f.view()); await screen.findByText("Input 1"); fireEvent.click(screen.getByRole("button", { name: en.more }));
  await screen.findByRole("alert"); expect(screen.getByText("Input 1")).toBeTruthy();
  expect((screen.getByRole("button", { name: en.steer }) as HTMLButtonElement).disabled).toBe(true); expect(screen.queryByText("private cursor sentinel")).toBeNull(); expect(f.steer).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: en.queueReload })); await waitFor(() => expect(f.calls.at(-1)?.pageToken).toBe(""));
});
it("fences delayed old reads on background, foreground return and session replacement", async () => {
  let finish!: (value: { inputs: Resource[] }) => void;
  const f = fixture(() => new Promise(resolve => { finish = resolve; }));
  const mounted = render(f.view()); await waitFor(() => expect(f.calls).toHaveLength(1));
  mounted.rerender(f.view(false)); finish({ inputs: [row(1)] });
  expect(screen.queryByText("Input 1")).toBeNull(); expect(f.steer).not.toHaveBeenCalled();
  mounted.rerender(f.view(true, "01900000-0000-7000-8000-000000000002"));
  await waitFor(() => expect(f.calls.at(-1)?.sessionId).toBe("01900000-0000-7000-8000-000000000002"));
  finish({ inputs: [row(1)] }); await screen.findByRole("alert"); expect(screen.queryByText("Input 1")).toBeNull(); expect(f.steer).not.toHaveBeenCalled();
});

it("rejects a late old-profile response after authenticated transport replacement", async () => {
  let finishOld!: (value: { inputs: Resource[] }) => void;
  const old = fixture(() => new Promise(resolve => { finishOld = resolve; }));
  const current = fixture(() => ({ inputs: [row(52)] }));
  const mounted = render(old.view()); await waitFor(() => expect(old.calls).toHaveLength(1));
  mounted.rerender(current.view()); await screen.findByText("Input 52");
  finishOld({ inputs: [row(1)] });
  await waitFor(() => expect(screen.queryByText("Input 1")).toBeNull());
  expect(old.steer).not.toHaveBeenCalled(); expect(current.steer).not.toHaveBeenCalled();
});
