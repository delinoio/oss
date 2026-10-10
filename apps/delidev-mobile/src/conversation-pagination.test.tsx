// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within, waitFor } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { EntityKind, ResourceSchema, ResourceService, SessionService, InboxService } from "@delinoio/delidev-api-client";
import { App } from "./app";
import { documentBytes, Operation, ProtectedState } from "./state";
import { en } from "./localization";

vi.mock("./platform", () => ({
  storage: { read: async () => null, write: async () => {} },
  observeForeground: async () => () => {},
  permission: async () => false,
  notify: async () => false,
  tls: async () => "ok",
}));
// Keep navigation fixtures read-only and independent of network synchronization.
vi.mock("./connection", async () => {
  const { QueryClient } = await import("@tanstack/react-query");
  const { Status } = await vi.importActual<typeof import("./connection")>("./connection");
  return {
    Status,
    Connection: class {
      active = false;
      status = Status.Connecting;
      cache = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      constructor(_state: unknown, _id: string, readonly changed: (status: typeof Status.Live) => void) {}
      async resume() { this.active = true; this.status = Status.Live; this.changed(Status.Live); }
      suspend() { this.active = false; void this.cache.cancelQueries(); this.cache.clear(); }
      canMutate() { return this.active; }
    },
  };
});
afterEach(cleanup);


function fixture(question = false, invalidFresh = "") {
  const state = new ProtectedState({ read: async () => null, write: async () => {} });
  state.state = { version: 1, profiles: [{ id: "profile", name: "Fixture", origin: "https://fixture.invalid", serverId: "server", deviceId: "device", token: "fixture" }], selectedProfile: "profile", language: "en", theme: "system", notifications: false };
  vi.spyOn(state, "load").mockResolvedValue(undefined);
  const resource = (kind: EntityKind, id: string, document: unknown) => create(ResourceSchema, { kind, id, sessionId: "session", revision: 13n, schemaVersion: 1, documentJson: documentBytes(document) });
  const session = resource(EntityKind.SESSION, "session", { name: "Original conversation", archive: "active", recovery: "none", outcome: "running", active_execution_id: "original-execution", execution: { native_turn_id: "original-turn" } });
  const interaction = resource(EntityKind.INTERACTION, "original-request", question ? { closure: "open", type: "user-question", questions: { questions: [{ id: "original-question", text: "Choose", options: [{ label: "Answer" }], custom: false }] } } : { closure: "open", type: "approval-request", opencode: { permission: "fixture" } });
  const reads: string[] = [];
  const transport = createRouterTransport(router => {
    router.service(SessionService, { listSessions: () => ({ sessions: [session] }), listQueue: request => ({ inputs: request.pageToken ? [resource(EntityKind.QUEUE, "original-input", { prompt: "Input fifty-one" })] : Array.from({ length: 50 }, (_, index) => resource(EntityKind.QUEUE, `queue-${index}`, { prompt: `Input ${index}` })), nextPageToken: request.pageToken ? "" : "queue-exact" }) });
    router.service(ResourceService, {
      listResources: request => request.filter?.kind === EntityKind.INTERACTION ? { resources: request.filter.pageToken ? [interaction] : Array.from({ length: 50 }, (_, index) => resource(EntityKind.INTERACTION, `closed-${index}`, { closure: "closed" })), nextPageToken: request.filter.pageToken ? "" : "interaction-exact" } : { resources: [] },
      getResource: request => { reads.push(request.id); return { resource: request.kind === EntityKind.INTERACTION ? { ...interaction, id: invalidFresh === "identity" ? "replacement" : interaction.id, revision: invalidFresh === "revision" ? 14n : interaction.revision, schemaVersion: invalidFresh === "schema" ? 99 : interaction.schemaVersion } : session }; },
    });
    router.service(InboxService, { listInbox: () => ({ entries: [] }), getNotificationPreferences: () => ({}) });
  });
  vi.spyOn(state, "transport").mockReturnValue(transport);
  const perform = vi.spyOn(state, "perform").mockResolvedValue(undefined);
  render(<App state={state} />);
  return { perform, reads };
}
async function openConversation() {
  fireEvent.click(await screen.findByRole("button", { name: /Original conversation/ }));
  await screen.findByRole("heading", { name: "Original conversation" });
}
it("Steers the original 51st queued input with its exact revision/execution/turn", async () => {
  const f = fixture(); await openConversation();
  const queue = screen.getByRole("region", { name: en.queue });
  fireEvent.click(await within(queue).findByRole("button", { name: en.more }));
  const article = (await within(queue).findByText("Input fifty-one")).closest("article")!;
  fireEvent.click(within(article).getByRole("button", { name: en.steer }));
  await waitFor(() => expect(f.perform).toHaveBeenCalled());
  expect(screen.getByText(`${en.closedRequests}: 50`)).toBeTruthy();
  expect(screen.queryByRole("heading", { name: en.approval })).toBeNull();
  expect(f.perform.mock.calls[0]).toMatchObject(["profile", Operation.Steer, { mutation: { id: "original-input", expectedRevision: 13n }, sessionId: "session", expectedExecutionId: "original-execution", expectedTurnId: "original-turn" }, "session"]);
});
it.each([false, true])("answers the original second-page approval/question (%s) after an exact revision read", async question => {
  const f = fixture(question); await openConversation();
  const requests = screen.getByRole("region", { name: en.interactions });
  expect(await within(requests).findByText(`${en.closedRequests}: 50`)).toBeTruthy();
  expect(within(requests).queryByRole("heading", { name: en.answer })).toBeNull();
  fireEvent.click(within(requests).getByRole("button", { name: en.more }));
  await within(requests).findByRole("heading", { name: question ? en.answer : en.approval });
  if (question) fireEvent.click(within(requests).getByLabelText("Answer"));
  else fireEvent.change(within(requests).getByRole("combobox"), { target: { value: "once" } });
  fireEvent.click(within(requests).getByRole("button", { name: en.submit }));
  await waitFor(() => expect(f.perform).toHaveBeenCalled());
  expect(f.reads).toContain("original-request");
  expect(f.perform.mock.calls[0]).toMatchObject(["profile", question ? Operation.Question : Operation.Approval, { mutation: { id: "original-request", expectedRevision: 13n } }, "original-request"]);
});

it.each(["identity", "revision", "schema"])("refuses a second-page response after the fresh original request changes %s", async invalidFresh => {
  const f = fixture(false, invalidFresh); await openConversation();
  const requests = screen.getByRole("region", { name: en.interactions });
  fireEvent.click(await within(requests).findByRole("button", { name: en.more }));
  await within(requests).findByRole("heading", { name: en.approval });
  fireEvent.change(within(requests).getByRole("combobox"), { target: { value: "once" } });
  fireEvent.click(within(requests).getByRole("button", { name: en.submit }));
  await within(requests).findByRole("alert");
  expect(f.perform).not.toHaveBeenCalled();
});
