// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ForkWorkspace, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionForkAction, SessionForkProvider } from "./session-fork";

it("retains exact fork retry and accepted job while navigation changes, publishing only the verified child", async () => {
 const turn = newRequestId();
 const source = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1, documentJson: encode({ name: "Original", workspace: "general-chat", archive: "active", recovery: "none", outcome: "succeeded", initial_execution: { configuration: { harness: "codex" } }, execution: { native_turn_id: turn, cleanup_verified: true } }) });
 const job = create(ResourceSchema, { kind: EntityKind.JOB, id: newRequestId(), revision: 1n, schemaVersion: 1, documentJson: encode({ state: "claimed", input: { source_session_id: source.id } }) });
 const child = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Child", fork: { source_session_id: source.id } }) });
 const sent: unknown[] = [];
 const fork = vi.fn(async (request) => { sent.push(request); if (sent.length === 1) throw new ConnectError("lost reply", Code.Unavailable); return { job }; });
 let completed = false;
 const observe = vi.fn(async () => ({ job: completed ? { ...job, documentJson: encode({ state: "succeeded" }) } : job, session: completed ? child : undefined }));
 const open = vi.fn();
 const transport = createRouterTransport((router) => { router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.CODEX_SESSION_FORK_V1] }) }); router.service(SessionService, { forkSession: fork, getSessionFork: observe }); router.service(ResourceService, { getResource: () => ({ resource: source }) }); });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
 const view = (active: boolean) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={open}>{active ? <SessionForkAction source={source} /> : <p>Other conversation</p>}</SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>;
 const rendered = render(view(true));
 fireEvent.click(await screen.findByRole("button", { name: "Fork session" }));
 expect((screen.getByRole("textbox", { name: "Fork name" }) as HTMLInputElement).value).toBe("Original fork");
 fireEvent.click(screen.getByRole("button", { name: "Create fork" }));
 await screen.findByRole("button", { name: "Retry the same fork request" });
 fireEvent.click(screen.getByRole("button", { name: "Close Fork session" }));
 rendered.rerender(view(false));
 fireEvent.click(screen.getByRole("button", { name: "Return to retained fork operation" }));
 fireEvent.click(screen.getByRole("button", { name: "Retry the same fork request" }));
 await screen.findByRole("region", { name: "Fork operation" });
 expect(sent[1]).toEqual(sent[0]);
 expect(sent[0]).toMatchObject({ mutation: { id: source.id, expectedRevision: 8n }, expectedTurnId: turn, workspace: ForkWorkspace.UNSPECIFIED });
 expect(screen.queryByRole("button", { name: "Open forked session" })).toBeNull();
 completed = true;
	await waitFor(() => expect((screen.getByRole("button", { name: "Refresh fork operation" }) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button", { name: "Refresh fork operation" }));
 fireEvent.click(await screen.findByRole("button", { name: "Open forked session" }));
 await waitFor(() => expect(open).toHaveBeenCalledWith(child.id));
 expect(fork).toHaveBeenCalledTimes(2);
});


it("offers a completed root but refuses a completed forked child", async () => {
  const completed = { name: "Completed", workspace: "general-chat", archive: "active", recovery: "none", outcome: "succeeded", initial_execution: { configuration: { harness: "codex" } }, execution: { native_turn_id: newRequestId(), cleanup_verified: true } };
  const root = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1, documentJson: encode(completed) });
  const child = create(ResourceSchema, { ...root, id: newRequestId(), documentJson: encode({ ...completed, fork: { source_session_id: root.id, native_thread_id: newRequestId() } }) });
  const fork = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.CODEX_SESSION_FORK_V1] }) });
    router.service(SessionService, { forkSession: fork });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = (source: typeof root) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={vi.fn()}><SessionForkAction source={source} /></SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>;
  const rendered = render(view(root));
  await screen.findByRole("button", { name: "Fork session" });
  rendered.rerender(view(child));
  expect(screen.queryByRole("button", { name: "Fork session" })).toBeNull();
  expect(fork).not.toHaveBeenCalled();
});
