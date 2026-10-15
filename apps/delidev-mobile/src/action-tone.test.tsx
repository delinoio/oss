// SPDX-License-Identifier: Apache-2.0
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import {
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
import { ActionTone } from "./action-tone";
import { App } from "./app";
import {
  documentBytes,
  Operation,
  PendingPhase,
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
// Keep the fixture read-only and independent of network synchronization.
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
  // jsdom lacks the modal dialog support used by confirmations.
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.setAttribute("open", "");
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.removeAttribute("open");
  };
});
afterEach(cleanup);

const tone = (button: HTMLElement) => button.getAttribute("data-action-tone");
function fixture(pending?: Pending, paused = false) {
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
      ...(paused ? { dispatch: "paused" } : {}),
    }),
  });
  const transport = createRouterTransport((router) => {
    router.service(SessionService, {
      listSessions: () => ({ sessions: [session] }),
      listQueue: () => ({ inputs: [] }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [] }),
      getResource: () => ({ resource: session }),
    });
    router.service(InboxService, {
      listInbox: () => ({ entries: [] }),
      getNotificationPreferences: () => ({}),
    });
  });
  vi.spyOn(state, "transport").mockReturnValue(transport);
  const perform = vi.spyOn(state, "perform").mockResolvedValue(undefined);
  const retry = vi.spyOn(state, "retry").mockResolvedValue(undefined);
  const forget = vi.spyOn(state, "forget").mockResolvedValue(undefined);
  render(<App state={state} />);
  return { state, perform, retry, forget };
}
function tab(name: string) {
  fireEvent.click(
    within(screen.getByRole("navigation")).getByRole("button", { name }),
  );
}
const dialog = () => within(screen.getByRole("dialog"));
const uncertain: Pending = {
  operation: Operation.Send,
  phase: PendingPhase.Uncertain,
  target: "session",
  request: '{"requestId":"original-request","sessionId":"session"}',
};

it("exports the same closed tone vocabulary as the desktop marker", () => {
  expect(Object.values(ActionTone)).toEqual(["default", "danger"]);
});
it("marks Forget and its confirmation Danger while Add and pairing retry stay untoned", async () => {
  const f = fixture();
  tab(en.settings);
  await screen.findByRole("heading", { name: en.settings });
  expect(tone(screen.getByRole("button", { name: en.add }))).toBeNull();
  const forget = screen.getByRole("button", { name: en.forget });
  expect(tone(forget)).toBe("danger");
  fireEvent.click(forget);
  expect(f.forget).not.toHaveBeenCalled();
  expect(tone(dialog().getByRole("button", { name: en.confirm }))).toBe(
    "danger",
  );
  // Closing the confirmation never forgets the profile.
  expect(f.forget).not.toHaveBeenCalled();
});
it("marks Revoke and its confirmation Danger", async () => {
  const f = fixture();
  const revoke = await screen.findByRole("button", { name: en.revoke });
  await waitFor(() =>
    expect((revoke as HTMLButtonElement).disabled).toBe(false),
  );
  expect(tone(revoke)).toBe("danger");
  fireEvent.click(revoke);
  expect(screen.getByRole("heading", { name: en.confirmRevoke })).toBeTruthy();
  expect(tone(dialog().getByRole("button", { name: en.confirm }))).toBe(
    "danger",
  );
  expect(f.perform).not.toHaveBeenCalled();
});
it("keeps the original-request retry confirmation neutral and Inspect and Retry untoned", async () => {
  const f = fixture(uncertain);
  const inspect = await screen.findByRole("button", { name: en.inspect });
  expect(tone(inspect)).toBeNull();
  await waitFor(() =>
    expect((inspect as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(inspect);
  await screen.findByText(en.inspected);
  const retry = screen.getByRole("button", { name: en.retry });
  expect(tone(retry)).toBeNull();
  fireEvent.click(retry);
  expect(screen.getByRole("heading", { name: en.confirmRetry })).toBeTruthy();
  expect(tone(dialog().getByRole("button", { name: en.confirm }))).toBe(
    "default",
  );
  expect(f.retry).not.toHaveBeenCalled();
});
it("keeps the revoke confirmation Danger only while the revoke branch is open", async () => {
  fixture(uncertain);
  const inspect = await screen.findByRole("button", { name: en.inspect });
  await waitFor(() =>
    expect((inspect as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(inspect);
  await screen.findByText(en.inspected);
  fireEvent.click(screen.getByRole("button", { name: en.retry }));
  expect(tone(dialog().getByRole("button", { name: en.confirm }))).toBe(
    "default",
  );
  expect(screen.queryByRole("heading", { name: en.confirmRevoke })).toBeNull();
});
it("leaves Stop, Resume and their confirmation untoned", async () => {
  fixture(undefined, true);
  fireEvent.click(
    await screen.findByRole("button", { name: /Original conversation/ }),
  );
  await screen.findByRole("heading", { name: "Original conversation" });
  const stop = screen.getByRole("button", { name: en.stop });
  const resume = screen.getByRole("button", { name: en.resume });
  expect(tone(stop)).toBeNull();
  expect(tone(resume)).toBeNull();
  await waitFor(() => expect((stop as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(stop);
  expect(tone(dialog().getByRole("button", { name: en.confirm }))).toBeNull();
});
