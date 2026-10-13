// SPDX-License-Identifier: Apache-2.0
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  SessionService,
  InboxService,
} from "@delinoio/delidev-api-client";
import { App } from "./app";
import {
  documentBytes,
  Operation,
  ProtectedState,
  type Pending,
} from "./state";
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
  const { Status } =
    await vi.importActual<typeof import("./connection")>("./connection");
  return {
    Status,
    Connection: class {
      active = false;
      status = Status.Connecting;
      cache = new QueryClient({
        defaultOptions: { queries: { retry: false } },
      });
      constructor(
        _state: unknown,
        _id: string,
        readonly changed: (status: typeof Status.Live) => void,
      ) {}
      async resume() {
        this.active = true;
        this.status = Status.Live;
        this.changed(Status.Live);
      }
      suspend() {
        this.active = false;
        void this.cache.cancelQueries();
        this.cache.clear();
      }
      canMutate() {
        return this.active;
      }
    },
  };
});
beforeAll(() => {
  // jsdom lacks modal dialog support used by the retry confirmation.
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.setAttribute("open", "");
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.removeAttribute("open");
  };
});
afterEach(cleanup);

function fixture(
  pending?: Pending,
  identityData: Record<string, unknown> = {},
) {
  const state = new ProtectedState({
    read: async () => null,
    write: async () => {},
  });
  state.state = {
    version: 1,
    profiles: [
      {
        id: "profile",
        name: "Fixture",
        origin: "https://fixture.invalid",
        serverId: "server",
        deviceId: "device",
        token: "fixture",
        pending,
      },
    ],
    selectedProfile: "profile",
    language: "en",
    theme: "system",
    notifications: false,
  };
  vi.spyOn(state, "load").mockResolvedValue(undefined);
  const session = create(ResourceSchema, {
    kind: EntityKind.SESSION,
    id: "original-session",
    revision: 7n,
    schemaVersion: 1,
    documentJson: documentBytes({
      name: "Original conversation",
      archive: "active",
      recovery: "none",
      ...identityData,
    }),
  });
  const other = create(ResourceSchema, {
    kind: EntityKind.SESSION,
    id: "other-session",
    revision: 3n,
    schemaVersion: 1,
    documentJson: documentBytes({
      name: "Other conversation",
      archive: "active",
      recovery: "none",
    }),
  });
  const sessionReads: string[] = [];
  const transport = createRouterTransport((router) => {
    router.service(SessionService, {
      listSessions: () => ({ sessions: [session, other] }),
      listQueue: () => ({ inputs: [] }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [] }),
      getResource: (request) => {
        sessionReads.push(request.id);
        return { resource: request.id === other.id ? other : session };
      },
    });
    router.service(InboxService, {
      listInbox: () => ({ entries: [] }),
      getNotificationPreferences: () => ({}),
    });
  });
  vi.spyOn(state, "transport").mockReturnValue(transport);
  const perform = vi.spyOn(state, "perform").mockResolvedValue(undefined);
  const retry = vi.spyOn(state, "retry").mockResolvedValue(undefined);
  render(<App state={state} />);
  return { state, perform, retry, sessionReads };
}
async function openConversation() {
  fireEvent.click(
    await screen.findByRole("button", { name: /Original conversation/ }),
  );
  await screen.findByRole("heading", { name: "Original conversation" });
}
function tab(name: string, keyboard = false) {
  const button = within(screen.getByRole("navigation")).getByRole("button", {
    name,
  });
  if (keyboard) {
    button.focus();
    fireEvent.keyDown(button, { key: "Enter" });
  }
  // Browsers dispatch a zero-detail click when a native button is activated by keyboard.
  fireEvent.click(button, { detail: keyboard ? 0 : 1 });
}
it.each([en.inbox, en.sessions, en.settings])(
  "leaves a conversation for %s and restores its original draft without a mutation",
  async (destination) => {
    const f = fixture();
    await openConversation();
    fireEvent.change(screen.getByLabelText(en.prompt), {
      target: { value: "Retained draft" },
    });
    fireEvent.change(screen.getByLabelText(en.mode), {
      target: { value: "plan" },
    });
    tab(destination);
    await screen.findByRole("heading", { name: destination });
    expect(
      screen.queryByRole("heading", { name: "Original conversation" }),
    ).toBeNull();
    tab(en.sessions);
    await openConversation();
    expect(
      (screen.getByLabelText(en.prompt) as HTMLTextAreaElement).value,
    ).toBe("Retained draft");
    expect((screen.getByLabelText(en.mode) as HTMLSelectElement).value).toBe(
      "plan",
    );
    expect(f.sessionReads.length).toBeGreaterThan(0);
    expect(f.sessionReads.every((id) => id === "original-session")).toBe(true);
    expect(f.perform).not.toHaveBeenCalled();
    expect(f.retry).not.toHaveBeenCalled();
  },
);
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
  const pending = {
    operation: Operation.Send,
    target: "original-session",
    request:
      '{"requestId":"original-request","sessionId":"original-session","documentJson":"original-bytes"}',
  };
  const f = fixture(pending);
  await openConversation();
  for (const destination of [en.inbox, en.settings, en.sessions]) {
    tab(destination);
    await screen.findByRole("heading", { name: destination });
    expect(f.state.profile("profile").pending).toEqual(pending);
  }
  await openConversation();
  expect(
    (
      screen
        .getByLabelText(en.prompt)
        .closest("fieldset") as HTMLFieldSetElement
    ).disabled,
  ).toBe(true);
  expect(f.state.profile("profile").pending).toEqual(pending);
  expect(f.perform).not.toHaveBeenCalled();
  expect(f.retry).not.toHaveBeenCalled();
});

it("does not carry conversation selection or drafts into a replacement profile", async () => {
  const f = fixture();
  f.state.state.profiles.push({
    ...f.state.state.profiles[0]!,
    id: "other-profile",
    name: "Other profile",
    serverId: "other-server",
  });
  vi.spyOn(f.state, "select").mockImplementation(async (id) => {
    f.state.state.selectedProfile = id;
  });
  await openConversation();
  fireEvent.change(screen.getByLabelText(en.prompt), {
    target: { value: "Original profile draft" },
  });
  tab(en.settings);
  fireEvent.click(await screen.findByRole("button", { name: "Other profile" }));
  tab(en.sessions);
  await screen.findByRole("heading", { name: en.sessions });
  await openConversation();
  expect((screen.getByLabelText(en.prompt) as HTMLTextAreaElement).value).toBe(
    "",
  );
  expect(f.perform).not.toHaveBeenCalled();
  expect(f.retry).not.toHaveBeenCalled();
});

it("presents unavailable Runner and Agent labels while preserving original conversation requests", async () => {
  const runner = "0195c9c0-7b13-7000-8000-000000000001";
  const agent = "0195c9c0-7b13-7000-8000-000000000002";
  const f = fixture(undefined, { machine_id: runner, agent_id: agent });
  await openConversation();
  expect(document.body.textContent).toContain(en.runnerUnavailable);
  expect(document.body.textContent).toContain(en.agentUnavailable);
  expect(document.body.textContent).not.toContain(runner);
  expect(document.body.textContent).not.toContain(agent);
  for (const element of document.querySelectorAll("[aria-label], [title]")) {
    expect(element.getAttribute("aria-label") ?? "").not.toContain(runner);
    expect(element.getAttribute("title") ?? "").not.toContain(agent);
  }
  expect(f.sessionReads).toContain("original-session");
  expect(f.perform).not.toHaveBeenCalled();
  expect(f.retry).not.toHaveBeenCalled();
});

function composer() {
  return screen.getByLabelText(en.prompt) as HTMLTextAreaElement;
}
function mode() {
  return screen.getByLabelText(en.mode) as HTMLSelectElement;
}
function send() {
  fireEvent.click(screen.getByRole("button", { name: en.send }));
}
function deferred() {
  let resolve!: () => void, reject!: (reason: unknown) => void;
  const promise = new Promise<undefined>((ok, fail) => {
    resolve = () => ok(undefined);
    reject = fail;
  });
  return { promise, resolve, reject };
}
function sendRequest(prompt: string) {
  // Encode the wire JSON directly; jsdom and protobuf use different Uint8Array realms.
  return JSON.stringify({
    requestId: "original-request",
    sessionId: "original-session",
    documentJson: btoa(JSON.stringify({ prompt, mode: "plan" })),
  });
}
it("clears the visible composer and retained draft after a confirmed Send and keeps the mode", async () => {
  const f = fixture();
  await openConversation();
  fireEvent.change(composer(), { target: { value: "Confirmed prompt" } });
  fireEvent.change(mode(), { target: { value: "plan" } });
  send();
  await waitFor(() => expect(composer().value).toBe(""));
  expect(f.perform).toHaveBeenCalledTimes(1);
  expect(mode().value).toBe("plan");
  tab(en.inbox);
  await screen.findByRole("heading", { name: en.inbox });
  tab(en.sessions);
  await openConversation();
  expect(composer().value).toBe("");
  expect(mode().value).toBe("plan");
  expect(f.retry).not.toHaveBeenCalled();
});
it("clears only the originating draft when confirmation lands while the conversation is unmounted", async () => {
  const f = fixture();
  const completion = deferred();
  f.perform.mockReturnValueOnce(completion.promise);
  await openConversation();
  fireEvent.change(composer(), { target: { value: "Unmounted prompt" } });
  fireEvent.change(mode(), { target: { value: "plan" } });
  send();
  await waitFor(() => expect(f.perform).toHaveBeenCalledTimes(1));
  // Settings unmounts Connected and Conversation while the original Send is in flight.
  tab(en.settings);
  await screen.findByRole("heading", { name: en.settings });
  completion.resolve();
  await act(async () => {
    await completion.promise;
  });
  tab(en.sessions);
  fireEvent.click(
    await screen.findByRole("button", { name: /Other conversation/ }),
  );
  await screen.findByRole("heading", { name: "Other conversation" });
  expect(composer().value).toBe("");
  fireEvent.change(composer(), { target: { value: "Other draft" } });
  fireEvent.click(screen.getByRole("button", { name: en.back }));
  await openConversation();
  expect(composer().value).toBe("");
  expect(mode().value).toBe("plan");
  fireEvent.click(screen.getByRole("button", { name: en.back }));
  fireEvent.click(
    await screen.findByRole("button", { name: /Other conversation/ }),
  );
  await screen.findByRole("heading", { name: "Other conversation" });
  expect(composer().value).toBe("Other draft");
});
it("keeps text edited after the Send was captured", async () => {
  const f = fixture();
  const completion = deferred();
  f.perform.mockReturnValueOnce(completion.promise);
  await openConversation();
  fireEvent.change(composer(), { target: { value: "Submitted prompt" } });
  send();
  await waitFor(() => expect(f.perform).toHaveBeenCalledTimes(1));
  fireEvent.change(composer(), { target: { value: "Newer edit" } });
  completion.resolve();
  await act(async () => {
    await completion.promise;
  });
  await waitFor(() => expect(f.perform).toHaveBeenCalledTimes(1));
  expect(composer().value).toBe("Newer edit");
  tab(en.inbox);
  await screen.findByRole("heading", { name: en.inbox });
  tab(en.sessions);
  await openConversation();
  expect(composer().value).toBe("Newer edit");
});
it("keeps the draft and pending request when the Send outcome is unconfirmed and refresh only reads", async () => {
  const pending = {
    operation: Operation.Send,
    target: "original-session",
    request: sendRequest("Uncertain prompt"),
  };
  const f = fixture();
  f.perform.mockImplementationOnce(async () => {
    f.state.state.profiles[0]!.pending = pending;
    throw new Error("lost-response");
  });
  await openConversation();
  fireEvent.change(composer(), { target: { value: "Uncertain prompt" } });
  send();
  await waitFor(() => expect(f.perform).toHaveBeenCalledTimes(1));
  await screen.findByRole("alert");
  expect(composer().value).toBe("Uncertain prompt");
  expect(f.state.profile("profile").pending).toEqual(pending);
  // The connection refresh is the first Refresh control in document order.
  fireEvent.click(screen.getAllByRole("button", { name: en.refresh })[0]!);
  tab(en.inbox);
  await screen.findByRole("heading", { name: en.inbox });
  tab(en.sessions);
  await openConversation();
  expect(composer().value).toBe("Uncertain prompt");
  expect(f.state.profile("profile").pending).toEqual(pending);
  expect(f.perform).toHaveBeenCalledTimes(1);
  expect(f.retry).not.toHaveBeenCalled();
});
it("clears the matching draft after the explicit original-request retry succeeds", async () => {
  const pending = {
    operation: Operation.Send,
    target: "original-session",
    request: sendRequest("Retried prompt"),
  };
  const f = fixture(pending);
  await openConversation();
  fireEvent.change(composer(), { target: { value: "Retried prompt" } });
  fireEvent.click(await screen.findByRole("button", { name: en.inspect }));
  await screen.findByText(en.inspected);
  fireEvent.click(screen.getByRole("button", { name: en.retry }));
  fireEvent.click(await screen.findByRole("button", { name: en.confirm }));
  await waitFor(() => expect(f.retry).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(composer().value).toBe(""));
  expect(f.perform).not.toHaveBeenCalled();
  expect(f.state.profile("profile").pending).toEqual(pending);
});
it("keeps an unrelated draft when the explicit retry belongs to another prompt", async () => {
  const pending = {
    operation: Operation.Send,
    target: "original-session",
    request: sendRequest("Retried prompt"),
  };
  const f = fixture(pending);
  await openConversation();
  fireEvent.change(composer(), { target: { value: "Different draft" } });
  fireEvent.click(await screen.findByRole("button", { name: en.inspect }));
  await screen.findByText(en.inspected);
  fireEvent.click(screen.getByRole("button", { name: en.retry }));
  fireEvent.click(await screen.findByRole("button", { name: en.confirm }));
  await waitFor(() => expect(f.retry).toHaveBeenCalledTimes(1));
  expect(composer().value).toBe("Different draft");
});
