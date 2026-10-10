// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { EntityKind, ResourceSchema, ResourceService, SessionService, InboxService } from "@delinoio/delidev-api-client";
import { App } from "./app";
import { documentBytes, Operation, ProtectedState, type Pending } from "./state";
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

function fixture(pending?: Pending) {
  const state = new ProtectedState({ read: async () => null, write: async () => {} });
  state.state = {
    version: 1, profiles: [{ id: "profile", name: "Fixture", origin: "https://fixture.invalid", serverId: "server", deviceId: "device", token: "fixture", pending }],
    selectedProfile: "profile", language: "en", theme: "system", notifications: false,
  };
  vi.spyOn(state, "load").mockResolvedValue(undefined);
  const session = create(ResourceSchema, { kind: EntityKind.SESSION, id: "original-session", revision: 7n, schemaVersion: 1,
    documentJson: documentBytes({ name: "Original conversation", archive: "active", recovery: "none" }) });
  const sessionReads: string[] = [];
  const transport = createRouterTransport(router => {
    router.service(SessionService, { listSessions: () => ({ sessions: [session] }), listQueue: () => ({ inputs: [] }) });
    router.service(ResourceService, {
      listResources: () => ({ resources: [] }),
      getResource: request => { sessionReads.push(request.id); return { resource: session }; },
    });
    router.service(InboxService, { listInbox: () => ({ entries: [] }), getNotificationPreferences: () => ({}) });
  });
  vi.spyOn(state, "transport").mockReturnValue(transport);
  const perform = vi.spyOn(state, "perform").mockResolvedValue(undefined);
  const retry = vi.spyOn(state, "retry").mockResolvedValue(undefined);
  render(<App state={state} />);
  return { state, perform, retry, sessionReads };
}
async function openConversation() {
  fireEvent.click(await screen.findByRole("button", { name: /Original conversation/ }));
  await screen.findByRole("heading", { name: "Original conversation" });
}
function tab(name: string, keyboard = false) {
  const button = within(screen.getByRole("navigation")).getByRole("button", { name });
  if (keyboard) { button.focus(); fireEvent.keyDown(button, { key: "Enter" }); }
  // Browsers dispatch a zero-detail click when a native button is activated by keyboard.
  fireEvent.click(button, { detail: keyboard ? 0 : 1 });
}
it.each([en.inbox, en.sessions, en.settings])("leaves a conversation for %s and restores its original draft without a mutation", async destination => {
  const f = fixture();
  await openConversation();
  fireEvent.change(screen.getByLabelText(en.prompt), { target: { value: "Retained draft" } });
  fireEvent.change(screen.getByLabelText(en.mode), { target: { value: "plan" } });
  tab(destination);
  await screen.findByRole("heading", { name: destination });
  expect(screen.queryByRole("heading", { name: "Original conversation" })).toBeNull();
  tab(en.sessions);
  await openConversation();
  expect((screen.getByLabelText(en.prompt) as HTMLTextAreaElement).value).toBe("Retained draft");
  expect((screen.getByLabelText(en.mode) as HTMLSelectElement).value).toBe("plan");
  expect(f.sessionReads.length).toBeGreaterThan(0);
  expect(f.sessionReads.every(id => id === "original-session")).toBe(true);
  expect(f.perform).not.toHaveBeenCalled();
  expect(f.retry).not.toHaveBeenCalled();
});
it("honors a keyboard-origin Inbox activation and Back returns to the opening tab", async () => {
  const f = fixture();
  await openConversation();
  tab(en.inbox, true);
  await screen.findByRole("heading", { name: en.inbox });
  tab(en.sessions);
  await openConversation();
  fireEvent.click(screen.getByRole("button", { name: en.back }));
  await screen.findByRole("heading", { name: en.sessions });
  expect(f.perform).not.toHaveBeenCalled();
});
it("retains exact protected uncertain bytes across tabs and return without resend or Stop", async () => {
  const pending = { operation: Operation.Send, target: "original-session", request: '{"requestId":"original-request","sessionId":"original-session","documentJson":"original-bytes"}' };
  const f = fixture(pending);
  await openConversation();
  for (const destination of [en.inbox, en.settings, en.sessions]) {
    tab(destination);
    await screen.findByRole("heading", { name: destination });
    expect(f.state.profile("profile").pending).toEqual(pending);
  }
  await openConversation();
  expect((screen.getByLabelText(en.prompt).closest("fieldset") as HTMLFieldSetElement).disabled).toBe(true);
  expect(f.state.profile("profile").pending).toEqual(pending);
  expect(f.perform).not.toHaveBeenCalled();
  expect(f.retry).not.toHaveBeenCalled();
});

it("does not carry conversation selection or drafts into a replacement profile", async () => {
  const f = fixture();
  f.state.state.profiles.push({ ...f.state.state.profiles[0]!, id: "other-profile", name: "Other profile", serverId: "other-server" });
  vi.spyOn(f.state, "select").mockImplementation(async id => { f.state.state.selectedProfile = id; });
  await openConversation();
  fireEvent.change(screen.getByLabelText(en.prompt), { target: { value: "Original profile draft" } });
  tab(en.settings);
  fireEvent.click(await screen.findByRole("button", { name: /^Other profile · / }));
  tab(en.sessions);
  await screen.findByRole("heading", { name: en.sessions });
  await openConversation();
  expect((screen.getByLabelText(en.prompt) as HTMLTextAreaElement).value).toBe("");
  expect(f.perform).not.toHaveBeenCalled();
  expect(f.retry).not.toHaveBeenCalled();
});
