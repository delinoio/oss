// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import {
  EntityKind,
  InboxService,
  NotificationPreferencesSchema,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";
import { i18n } from "./localization";
import { sessionProblemTitle } from "./session-presentation";

enum FailureStage {
  Snapshot = "snapshot",
  Watch = "watch",
}

async function mountSession(stage: FailureStage, code: Code) {
  const id = newRequestId();
  const session = create(ResourceSchema, {
    id,
    sessionId: id,
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      name: "Stream failure session",
      workspace: "general-chat",
      outcome: "stopped",
      archive: "active",
      dispatch: "paused",
      recovery: "clear",
    }),
  });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(SessionService, {
      listSessions: () => ({ sessions: [session] }),
      listQueue: () => ({ inputs: [] }),
    });
    router.service(ResourceService, {
      getResource: () => ({ resource: session }),
      getSnapshot: () => {
        if (stage === FailureStage.Snapshot)
          throw new ConnectError("Stream rejected", code);
        return { resources: [session], cursor: newRequestId() };
      },
      listResources: () => ({ resources: [] }),
      // eslint-disable-next-line require-yield
      async *watchEvents() {
        throw new ConnectError("Stream rejected", code);
      },
    });
    router.service(InboxService, {
      listInbox: () => ({ entries: [] }),
      getNotificationPreferences: () => ({
        preferences: create(NotificationPreferencesSchema, { revision: 1n }),
      }),
    });
  });
  render(<App transport={transport} />);
  fireEvent.click(
    await screen.findByRole("button", {
      name: /General Chat Stream failure session/,
    }),
  );
}

afterEach(async () => {
  cleanup();
  await act(async () => {
    await i18n.changeLanguage("en");
  });
});

it.each([
  [FailureStage.Snapshot, Code.Unauthenticated],
  [FailureStage.Watch, Code.Unauthenticated],
])(
  "titles a typed Unauthenticated %s failure with the authentication heading",
  async (stage, code) => {
    await mountSession(stage, code);
    const composer = await screen.findByRole("textbox", { name: "Message" });
    fireEvent.change(composer, { target: { value: "Unsent draft" } });
    const region = await screen.findByRole("region", {
      name: "Session recovery",
    });
    await waitFor(() =>
      expect(region.textContent).toContain("Authentication required"),
    );
    expect(region.textContent).not.toContain("Connection unavailable");
    expect(region.textContent).toContain(
      "The DeliDev request could not complete.",
    );
    await act(async () => {
      await i18n.changeLanguage("ko");
    });
    await waitFor(() =>
      expect(region.textContent).toContain("인증이 필요합니다"),
    );
    expect(region.textContent).not.toContain("Authentication required");
    expect(
      (
        screen.getByRole("textbox", { name: "메시지" }) as
          HTMLTextAreaElement | HTMLInputElement
      ).value,
    ).toBe("Unsent draft");
  },
  15000,
);

it.each([
  [FailureStage.Snapshot, Code.Unauthenticated],
  [FailureStage.Watch, Code.Unauthenticated],
])(
  "keeps the gated connection refresh action on a %s authentication failure",
  async (stage, code) => {
    await mountSession(stage, code);
    const region = await screen.findByRole("region", {
      name: "Session recovery",
    });
    await waitFor(() =>
      expect(region.textContent).toContain("Authentication required"),
    );
    expect(
      within(region).getAllByRole("button", { name: /Refresh connection/ }),
    ).toHaveLength(1);
    expect(region.querySelectorAll('[role="alert"]').length).toBeGreaterThan(0);
  },
  15000,
);

it("titles a non-authentication Unavailable snapshot failure as a connection problem", async () => {
  await mountSession(FailureStage.Snapshot, Code.Unavailable);
  const region = await screen.findByRole("region", {
    name: "Session recovery",
  });
  await waitFor(() =>
    expect(region.textContent).toContain("Connection unavailable"),
  );
  expect(region.textContent).not.toContain("Authentication required");
}, 15000);

it("titles a terminal non-transient server failure as a connection problem", async () => {
  await mountSession(FailureStage.Watch, Code.Internal);
  const region = await screen.findByRole("region", {
    name: "Session recovery",
  });
  await waitFor(() =>
    expect(region.textContent).toContain("Connection unavailable"),
  );
  expect(region.textContent).not.toContain("Authentication required");
}, 15000);

it.each([
  [undefined, undefined, "Connection unavailable"],
  ["unauthenticated", undefined, "Authentication required"],
  ["server_unavailable", undefined, "Connection unavailable"],
  ["unavailable", undefined, "Connection unavailable"],
  ["recovery_required", undefined, "Recovery required"],
  ["budget_reached", undefined, "Session budget reached"],
  ["missing_input", "quota_exhausted", "Usage limit reached"],
  ["permission_denied", undefined, "Connection unavailable"],
])(
  "maps code %s and cause %s to %s with a connection fallback",
  (code, cause, title) => {
    expect(sessionProblemTitle(code, cause, "Connection unavailable")).toBe(
      title,
    );
  },
);
