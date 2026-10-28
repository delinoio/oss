// SPDX-License-Identifier: Apache-2.0
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, rs } from "@rstest/core";
import {
  createRouterTransport,
  Code,
  ConnectError,
  type ConnectRouter,
} from "@connectrpc/connect";
import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { QueryClient } from "@tanstack/react-query";
import {
  KeyService,
  OrganizationSchema,
  GetSessionResponseSchema,
  Role,
  KeyState,
  KeyKind,
  type InferenceKeySchema,
} from "@tairouter/client";
import { ApiProvider } from "../src/api";
import { KeysPage } from "../src/keys";
import {
  RevokeFailureKind,
  classifyRevokeFailure,
} from "../src/revoke-key-dialog";
import type { ReactNode } from "react";

const organization = create(OrganizationSchema, {
  id: "org-a",
  name: "Acme Studio",
  role: Role.MEMBER,
});
const session = create(GetSessionResponseSchema, {
  userId: "person",
  displayName: "Alex",
});
const props = { organization, session };
const uncertainCopy =
  "Revocation could not be confirmed. Refresh API keys to check its status before trying again.";
const pendingCopy =
  "Revoking key. Closing this dialog does not cancel the submitted change.";

type KeyInit = MessageInitShape<typeof InferenceKeySchema> & { id: string };
function key(overrides: KeyInit): KeyInit {
  return {
    name: overrides.id,
    prefix: `aipr_${overrides.id}`,
    kind: KeyKind.PERSONAL,
    state: KeyState.ACTIVE,
    createdAt: timestampFromDate(new Date("2026-10-20T09:00:00Z")),
    ...overrides,
  };
}
function mount(ui: ReactNode, routes: (r: ConnectRouter) => void) {
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const transport = createRouterTransport(routes);
  const wrap = (children: ReactNode) => (
    <ApiProvider queryClient={cache} rpcTransport={transport}>
      {children}
    </ApiProvider>
  );
  const result = render(wrap(ui));
  return {
    ...result,
    rerenderUi: (next: ReactNode) => result.rerender(wrap(next)),
  };
}
/** A revokeKey handler whose resolution the test controls. */
function deferred() {
  let resolve!: () => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<Record<string, never>>((res, rej) => {
    resolve = () => res({});
    reject = rej;
  });
  const handler = rs.fn((_request: unknown) => promise);
  return { handler, resolve, reject };
}
async function open(
  routes: (r: ConnectRouter) => void,
  name = "Production client",
) {
  const user = userEvent.setup();
  const view = mount(<KeysPage {...props} />, routes);
  const trigger = await screen.findByRole("button", {
    name: `Revoke ${name}`,
  });
  await user.click(trigger);
  return { user, trigger, view };
}
const production = key({
  id: "key-a",
  name: "Production client",
  state: KeyState.ACTIVE,
});

describe("revoke API key dialog content and dismissal", () => {
  it("shows the exact identity and copy, focuses Cancel and sends nothing on open", async () => {
    const revokeKey = rs.fn((_request: unknown) => ({}));
    await open((r) =>
      r.service(KeyService, {
        listKeys: () => ({ keys: [production] }),
        revokeKey,
      }),
    );
    const dialog = screen.getByRole("dialog");
    expect(
      within(dialog).getByRole("heading", { name: "Revoke API key?" }),
    ).toBeTruthy();
    expect(dialog.getAttribute("aria-labelledby")).toBeTruthy();
    expect(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    ).toBeTruthy();
    const lead = within(dialog).getByText(
      "This key will stop accepting new requests.",
    );
    const rows = Array.from(dialog.querySelectorAll("dl > div")).map((row) => [
      row.querySelector("dt")?.textContent,
      row.querySelector("dd")?.textContent,
    ]);
    expect(rows).toEqual([
      ["API key", "Production client"],
      ["Organization", "Acme Studio"],
    ]);
    const consequence = within(dialog).getByText(
      "New requests stop immediately. Responses already sent to a provider may finish.",
    );
    const irreversible = within(dialog).getByText(
      "This cannot be undone. Revoked keys cannot be reactivated.",
    );
    const describedBy = (dialog.getAttribute("aria-describedby") ?? "").split(
      " ",
    );
    for (const part of [
      lead.id,
      dialog.querySelector("dl")!.id,
      consequence.id,
      irreversible.id,
    ])
      expect(describedBy).toContain(part);
    const buttons = Array.from(
      dialog.querySelectorAll(".revoke-key-actions button"),
    ).map((button) => button.textContent);
    expect(buttons).toEqual(["Cancel", "Revoke key"]);
    expect(document.activeElement?.textContent).toBe("Cancel");
    expect(dialog.className).toContain("revoke-key-dialog");
    expect(revokeKey).not.toHaveBeenCalled();
  });

  it.each([
    [
      "Cancel",
      async (user: ReturnType<typeof userEvent.setup>) =>
        user.click(screen.getByRole("button", { name: "Cancel" })),
    ],
    [
      "Close",
      async (user: ReturnType<typeof userEvent.setup>) =>
        user.click(screen.getByRole("button", { name: "Close dialog" })),
    ],
    [
      "Escape",
      async () => {
        fireEvent(
          screen.getByRole("dialog"),
          new Event("cancel", { cancelable: true }),
        );
      },
    ],
    [
      "Enter on Cancel",
      async (user: ReturnType<typeof userEvent.setup>) =>
        user.keyboard("{Enter}"),
    ],
  ])(
    "%s dismisses without a request and restores focus to the Revoke button",
    async (_name, dismiss) => {
      const revokeKey = rs.fn((_request: unknown) => ({}));
      const { user, trigger } = await open((r) =>
        r.service(KeyService, {
          listKeys: () => ({ keys: [production] }),
          revokeKey,
        }),
      );
      await dismiss(user);
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(document.activeElement).toBe(trigger);
      expect(revokeKey).not.toHaveBeenCalled();
      expect(screen.queryByText("Key revoked.")).toBeNull();
    },
  );

  it("wraps Tab and Shift+Tab inside the dialog", async () => {
    const { user } = await open((r) =>
      r.service(KeyService, {
        listKeys: () => ({ keys: [production] }),
        revokeKey: () => ({}),
      }),
    );
    expect(document.activeElement?.textContent).toBe("Cancel");
    await user.tab();
    expect(document.activeElement?.textContent).toBe("Revoke key");
    await user.tab();
    expect(document.activeElement?.getAttribute("aria-label")).toBe(
      "Close dialog",
    );
    await user.tab({ shift: true });
    expect(document.activeElement?.textContent).toBe("Revoke key");
  });

  it("shows a CLI key's exact identity with the same semantics", async () => {
    const revokeKey = rs.fn((_request: unknown) => ({}));
    const { user } = await open(
      (r) =>
        r.service(KeyService, {
          listKeys: () => ({
            keys: [key({ id: "cli-1", name: "Laptop CLI", kind: KeyKind.CLI })],
          }),
          revokeKey,
        }),
      "Laptop CLI",
    );
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText("Laptop CLI")).toBeTruthy();
    expect(document.activeElement?.textContent).toBe("Cancel");
    await user.click(
      within(dialog).getByRole("button", { name: "Revoke key" }),
    );
    await waitFor(() => expect(revokeKey).toHaveBeenCalledOnce());
    expect(revokeKey.mock.calls[0]?.[0]).toMatchObject({
      organizationId: "org-a",
      keyId: "cli-1",
    });
  });

  it("renders long unbroken and markup-like names as literal text", async () => {
    const longName = `<img src=x onerror=alert(1)>${"K".repeat(200)}`;
    await open(
      (r) =>
        r.service(KeyService, {
          listKeys: () => ({ keys: [key({ id: "long", name: longName })] }),
          revokeKey: () => ({}),
        }),
      longName,
    );
    const dialog = screen.getByRole("dialog");
    expect(dialog.querySelector("img")).toBeNull();
    expect(dialog.querySelector(".revoke-key-name")?.textContent).toBe(
      longName,
    );
    expect(
      within(dialog).getByRole("button", { name: "Revoke key" }),
    ).toBeTruthy();
  });

  it("offers no Revoke entry for revoked or expired records", async () => {
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, {
        listKeys: () => ({
          keys: [
            key({ id: "r", name: "Old", state: KeyState.REVOKED }),
            key({ id: "e", name: "Stale", state: KeyState.EXPIRED }),
          ],
        }),
      }),
    );
    await screen.findByText("Old");
    expect(screen.queryAllByRole("button", { name: /^Revoke / })).toHaveLength(
      0,
    );
  });
});

describe("revoke API key dialog submission", () => {
  it("sends one request for repeated confirmation, shows pending state and closes on success", async () => {
    const pending = deferred();
    const listKeys = rs.fn(() => ({ keys: [production] }));
    const { user } = await open((r) =>
      r.service(KeyService, { listKeys, revokeKey: pending.handler }),
    );
    const confirm = screen.getByRole("button", { name: "Revoke key" });
    await user.click(confirm);
    await user.click(confirm);
    await waitFor(() =>
      expect(
        (
          screen
            .getByRole("dialog")
            .querySelector(
              ".revoke-key-actions button:last-child",
            ) as HTMLButtonElement
        ).textContent,
      ).toBe("Revoking…"),
    );
    const dialog = screen.getByRole("dialog");
    const submit = within(dialog).getByRole("button", {
      name: "Revoking…",
    }) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    expect(within(dialog).getByText(pendingCopy).getAttribute("role")).toBe(
      "status",
    );
    expect(pending.handler).toHaveBeenCalledOnce();
    expect(pending.handler.mock.calls[0]?.[0]).toMatchObject({
      organizationId: "org-a",
      keyId: "key-a",
    });
    expect(screen.queryByText("Key revoked.")).toBeNull();
    const before = listKeys.mock.calls.length;
    pending.resolve();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await screen.findByText("Key revoked.")).toBeTruthy();
    await waitFor(() =>
      expect(listKeys.mock.calls.length).toBeGreaterThan(before),
    );
  });

  it("dismisses while pending without cancelling, duplicating or implying rollback", async () => {
    const pending = deferred();
    const { user } = await open((r) =>
      r.service(KeyService, {
        listKeys: () => ({ keys: [production] }),
        revokeKey: pending.handler,
      }),
    );
    await user.click(screen.getByRole("button", { name: "Revoke key" }));
    await waitFor(() => expect(pending.handler).toHaveBeenCalledOnce());
    fireEvent(
      screen.getByRole("dialog"),
      new Event("cancel", { cancelable: true }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.queryByText("Key revoked.")).toBeNull();
    // A reopened dialog never shows the earlier submission and cannot submit again.
    await user.click(
      screen.getByRole("button", { name: "Revoke Production client" }),
    );
    const dialog = screen.getByRole("dialog");
    const confirm = within(dialog).getByRole("button", {
      name: "Revoke key",
    }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    expect(within(dialog).queryByText(pendingCopy)).toBeNull();
    await user.click(confirm);
    expect(pending.handler).toHaveBeenCalledOnce();
    pending.resolve();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await screen.findByText("Key revoked.")).toBeTruthy();
    expect(pending.handler).toHaveBeenCalledOnce();
  });

  it("does not close another key's dialog when an earlier dismissed request succeeds", async () => {
    const pending = deferred();
    const { user } = await open((r) =>
      r.service(KeyService, {
        listKeys: () => ({
          keys: [production, key({ id: "key-b", name: "Second key" })],
        }),
        revokeKey: pending.handler,
      }),
    );
    await user.click(screen.getByRole("button", { name: "Revoke key" }));
    await waitFor(() => expect(pending.handler).toHaveBeenCalledOnce());
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await user.click(screen.getByRole("button", { name: "Revoke Second key" }));
    pending.resolve();
    await screen.findByText("Key revoked.");
    expect(
      within(screen.getByRole("dialog")).getByText("Second key"),
    ).toBeTruthy();
  });
});

describe("revoke API key dialog failures", () => {
  it("keeps the target with a sanitized error, then re-enables an explicit retry", async () => {
    let fail = true;
    const revokeKey = rs.fn((_request: unknown) => {
      if (fail)
        throw new ConnectError("secret backend detail", Code.Unavailable);
      return {};
    });
    const { user } = await open((r) =>
      r.service(KeyService, {
        listKeys: () => ({ keys: [production] }),
        revokeKey,
      }),
    );
    await user.click(screen.getByRole("button", { name: "Revoke key" }));
    const dialog = screen.getByRole("dialog");
    await waitFor(() =>
      expect(within(dialog).getAllByRole("alert").length).toBeGreaterThan(0),
    );
    expect(within(dialog).getByText("Production client")).toBeTruthy();
    expect(
      within(dialog).getByText(
        "The server is unavailable. Try again when the connection is restored.",
      ),
    ).toBeTruthy();
    expect(within(dialog).queryByText(uncertainCopy)).toBeNull();
    expect(screen.queryByText(/secret backend detail/)).toBeNull();
    expect(screen.queryByText(/Request ID/)).toBeNull();
    expect(screen.queryByText("Key revoked.")).toBeNull();
    const confirm = within(dialog).getByRole("button", {
      name: "Revoke key",
    }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(false);
    expect(revokeKey).toHaveBeenCalledOnce();
    fail = false;
    await user.click(confirm);
    await waitFor(() => expect(revokeKey).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it.each([
    ["DeadlineExceeded", Code.DeadlineExceeded],
    ["Canceled", Code.Canceled],
    ["Unknown", Code.Unknown],
  ])(
    "shows the unconfirmed outcome copy for %s and never retries automatically",
    async (_name, code) => {
      const revokeKey = rs.fn((_request: unknown) => {
        throw new ConnectError("raw transport detail", code);
      });
      const { user } = await open((r) =>
        r.service(KeyService, {
          listKeys: () => ({ keys: [production] }),
          revokeKey,
        }),
      );
      await user.click(screen.getByRole("button", { name: "Revoke key" }));
      const dialog = screen.getByRole("dialog");
      expect(await within(dialog).findByText(uncertainCopy)).toBeTruthy();
      expect(screen.queryByText(/raw transport detail/)).toBeNull();
      expect(screen.queryByText("Key revoked.")).toBeNull();
      // The dialog itself never falls back to the generic failure copy.
      expect(
        within(dialog).queryByText(
          "The action could not be completed. Try again.",
        ),
      ).toBeNull();
      await new Promise((resolve) => setTimeout(resolve, 20));
      expect(revokeKey).toHaveBeenCalledOnce();
    },
  );

  it.each([
    ["PermissionDenied", Code.PermissionDenied],
    ["Unauthenticated", Code.Unauthenticated],
  ])(
    "reports %s through the authority-changed flow without retrying",
    async (_name, code) => {
      const events: unknown[] = [];
      const listener = (event: Event) =>
        events.push((event as CustomEvent).detail);
      window.addEventListener("authority-changed", listener);
      try {
        const revokeKey = rs.fn((_request: unknown) => {
          throw new ConnectError("denied detail", code);
        });
        const { user } = await open((r) =>
          r.service(KeyService, {
            listKeys: () => ({ keys: [production] }),
            revokeKey,
          }),
        );
        await user.click(screen.getByRole("button", { name: "Revoke key" }));
        await waitFor(() => expect(events).toEqual([code]));
        expect(screen.queryByText(/denied detail/)).toBeNull();
        expect(screen.queryByText("Key revoked.")).toBeNull();
        expect(revokeKey).toHaveBeenCalledOnce();
      } finally {
        window.removeEventListener("authority-changed", listener);
      }
    },
  );

  it("does not show an earlier failure when another revoke dialog opens", async () => {
    const revokeKey = rs.fn((_request: unknown) => {
      throw new ConnectError("detail", Code.Unavailable);
    });
    const { user } = await open((r) =>
      r.service(KeyService, {
        listKeys: () => ({ keys: [production] }),
        revokeKey,
      }),
    );
    await user.click(screen.getByRole("button", { name: "Revoke key" }));
    await waitFor(() =>
      expect(
        within(screen.getByRole("dialog")).getAllByRole("alert").length,
      ).toBeGreaterThan(0),
    );
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await user.click(
      screen.getByRole("button", { name: "Revoke Production client" }),
    );
    expect(
      within(screen.getByRole("dialog")).queryAllByRole("alert"),
    ).toHaveLength(0);
  });

  it("classifies only outcome-less failures as unconfirmed", () => {
    expect(classifyRevokeFailure(new Error("network down"))).toBe(
      RevokeFailureKind.Uncertain,
    );
    expect(classifyRevokeFailure(new ConnectError("x", Code.Unavailable))).toBe(
      RevokeFailureKind.Definitive,
    );
    expect(classifyRevokeFailure(new ConnectError("x", Code.NotFound))).toBe(
      RevokeFailureKind.Definitive,
    );
  });
});

describe("revoke API key dialog scope", () => {
  it("closes on an organization change and never replays the mutation", async () => {
    const revokeKey = rs.fn((_request: unknown) => ({}));
    const { view } = await open((r) =>
      r.service(KeyService, {
        listKeys: () => ({ keys: [production] }),
        revokeKey,
      }),
    );
    const other = create(OrganizationSchema, {
      id: "org-b",
      name: "Other Org",
      role: Role.MEMBER,
    });
    view.rerenderUi(<KeysPage organization={other} session={session} />);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.queryByText("Production client")).toBeNull();
    expect(revokeKey).not.toHaveBeenCalled();
  });
});
