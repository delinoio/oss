// SPDX-License-Identifier: Apache-2.0
import { readFileSync } from "node:fs";
import { render, screen, waitFor, within } from "@testing-library/react";
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
import type { ReactNode } from "react";

const organization = create(OrganizationSchema, {
  id: "one",
  name: "Studio",
  role: Role.MEMBER,
});
const session = create(GetSessionResponseSchema, {
  userId: "person",
  displayName: "Alex",
});
const props = { organization, session };

function at(iso: string) {
  return timestampFromDate(new Date(iso));
}
type KeyInit = MessageInitShape<typeof InferenceKeySchema> & { id: string };
function key(overrides: KeyInit): KeyInit {
  return {
    name: overrides.id,
    prefix: `aipr_${overrides.id}`,
    kind: KeyKind.PERSONAL,
    state: KeyState.ACTIVE,
    createdAt: at("2026-10-20T09:00:00Z"),
    ...overrides,
  };
}
function client() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
}
function mount(ui: ReactNode, routes: (r: ConnectRouter) => void) {
  const cache = client();
  const transport = createRouterTransport(routes);
  const wrap = (children: ReactNode) => (
    <ApiProvider queryClient={cache} rpcTransport={transport}>
      {children}
    </ApiProvider>
  );
  const result = render(wrap(ui));
  return {
    ...result,
    cache,
    rerenderUi: (next: ReactNode) => result.rerender(wrap(next)),
  };
}
const fourKeys = [
  key({
    id: "demo1",
    name: "Production app",
    expiresAt: at("2026-11-19T09:00:00Z"),
  }),
  key({
    id: "demo2",
    name: "Work laptop",
    kind: KeyKind.CLI,
    createdAt: at("2026-10-18T08:30:00Z"),
  }),
  key({
    id: "demo3",
    name: "Old integration",
    state: KeyState.EXPIRED,
    createdAt: at("2026-09-15T10:00:00Z"),
    expiresAt: at("2026-10-15T10:00:00Z"),
  }),
  key({
    id: "demo4",
    name: "Retired laptop",
    kind: KeyKind.CLI,
    state: KeyState.REVOKED,
    createdAt: at("2026-08-12T14:00:00Z"),
    expiresAt: at("2026-12-01T00:00:00Z"),
  }),
];
function rowFor(name: string) {
  return screen.getByText(name).closest("tr") as HTMLElement;
}

describe("API keys list layout", () => {
  it("keeps the header, once-only strip, feedback and errors above the collection", async () => {
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => ({ keys: fourKeys }) }),
    );
    await screen.findByRole("table");
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    expect(
      screen.getByRole("heading", { level: 1, name: "API keys" }),
    ).toBeTruthy();
    expect(screen.getByText("Your keys in Studio.")).toBeTruthy();
    expect(
      screen.getAllByRole("button", { name: "Create API key" }),
    ).toHaveLength(1);
    const strip = screen.getByText(
      "New keys are shown only once. Store them securely.",
    );
    const table = screen.getByRole("table");
    expect(
      strip.compareDocumentPosition(table) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    const group = screen.getByRole("group", { name: "API key pages" });
    expect(
      table.compareDocumentPosition(group) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("renders a semantic table with scoped column headings and a hidden Actions heading", async () => {
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => ({ keys: fourKeys }) }),
    );
    const table = await screen.findByRole("table", { name: "Your API keys" });
    const headers = within(table).getAllByRole("columnheader");
    expect(headers.map((h) => h.textContent)).toEqual([
      "Name",
      "Type",
      "Created",
      "Expires",
      "Status",
      "Actions",
    ]);
    expect(headers.every((h) => h.getAttribute("scope") === "col")).toBe(true);
    expect(within(headers[5]!).getByText("Actions").className).toContain(
      "visually-hidden",
    );
    const body = within(table).getAllByRole("row").slice(1);
    expect(body).toHaveLength(4);
    // Server order is preserved.
    expect(body.map((row) => row.querySelector("strong")?.textContent)).toEqual(
      ["Production app", "Work laptop", "Old integration", "Retired laptop"],
    );
  });

  it("shows masked identity, type, UTC dates, No expiry and text status per row", async () => {
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => ({ keys: fourKeys }) }),
    );
    await screen.findByRole("table");
    const production = within(rowFor("Production app"));
    expect(production.getByText("aipr_demo1••••")).toBeTruthy();
    expect(production.getByText("Personal")).toBeTruthy();
    expect(production.getByText("Active")).toBeTruthy();
    expect(
      production.getByText(/Oct 20, 2026.*UTC/).getAttribute("datetime"),
    ).toBe("2026-10-20T09:00:00.000Z");
    expect(production.getByText(/Nov 19, 2026.*UTC/)).toBeTruthy();
    const laptop = within(rowFor("Work laptop"));
    expect(laptop.getByText("CLI")).toBeTruthy();
    expect(laptop.getByText("No expiry")).toBeTruthy();
    expect(within(rowFor("Old integration")).getByText("Expired")).toBeTruthy();
    const retired = within(rowFor("Retired laptop"));
    expect(retired.getByText("Revoked")).toBeTruthy();
    // A present expiry keeps its UTC timestamp on a revoked row.
    expect(retired.getByText(/Dec 1, 2026.*UTC/)).toBeTruthy();
  });

  it("offers Revoke only for Active rows with record-specific names", async () => {
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => ({ keys: fourKeys }) }),
    );
    await screen.findByRole("table");
    const revokes = screen.getAllByRole("button", { name: /^Revoke / });
    expect(revokes.map((b) => b.getAttribute("aria-label"))).toEqual([
      "Revoke Production app",
      "Revoke Work laptop",
    ]);
    expect(revokes.every((b) => b.textContent === "Revoke")).toBe(true);
    // The displayed identifier distinguishes repeated names for assistive technology.
    const description = revokes[0]!
      .getAttribute("aria-describedby")
      ?.split(" ")
      .map((id) => document.getElementById(id)?.textContent);
    expect(description).toEqual(["aipr_demo1••••"]);
    expect(within(rowFor("Old integration")).queryByRole("button")).toBeNull();
    expect(within(rowFor("Retired laptop")).queryByRole("button")).toBeNull();
    for (const name of [
      /search/i,
      /sort/i,
      /export/i,
      /reactivate/i,
      /select/i,
    ]) {
      expect(screen.queryByRole("button", { name })).toBeNull();
      expect(screen.queryByRole("searchbox")).toBeNull();
    }
  });

  it("never relabels or offers actions for unspecified, unknown or fallback records", async () => {
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, {
        listKeys: () => ({
          keys: [
            key({
              id: "unspecified",
              state: KeyState.UNSPECIFIED,
              kind: KeyKind.UNSPECIFIED,
              prefix: "",
              createdAt: undefined,
            }),
            key({ id: "unknown", state: 99 as KeyState }),
            key({
              id: "future",
              state: KeyState.EXPIRED,
              expiresAt: at("2099-01-01T00:00:00Z"),
            }),
          ],
        }),
      }),
    );
    await screen.findByRole("table");
    const unspecified = within(rowFor("unspecified"));
    expect(unspecified.getByText("Unspecified")).toBeTruthy();
    expect(unspecified.getByText("Personal")).toBeTruthy();
    expect(unspecified.getByText("Unknown")).toBeTruthy();
    expect(unspecified.getByText("Identifier unavailable")).toBeTruthy();
    expect(unspecified.queryByText(/••••/)).toBeNull();
    expect(within(rowFor("unknown")).getByText("Unknown")).toBeTruthy();
    // A future expiry with a server Expired state is not relabeled by the browser clock.
    expect(within(rowFor("future")).getByText("Expired")).toBeTruthy();
    expect(screen.queryAllByRole("button", { name: /^Revoke / })).toHaveLength(
      0,
    );
  });

  it("keeps one accessible representation with labeled cells and a container-width reflow", async () => {
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => ({ keys: fourKeys }) }),
    );
    await screen.findByRole("table");
    expect(screen.getAllByRole("table")).toHaveLength(1);
    expect(screen.getAllByRole("button", { name: /^Revoke / })).toHaveLength(2);
    const labels = Array.from(
      rowFor("Production app").querySelectorAll("td"),
    ).map((cell) => cell.getAttribute("data-label"));
    expect(labels).toEqual([
      "Name",
      "Type",
      "Created",
      "Expires",
      "Status",
      null,
    ]);
    const css = readFileSync(
      new URL("../src/styles.css", import.meta.url),
      "utf8",
    );
    expect(css).toContain("@container keys-collection (max-width: 1099px)");
    expect(css).toContain("container: keys-collection / inline-size");
  });
});

describe("API keys list paging", () => {
  it("passes pageSize 100 and the exact returned cursor, and First page resets it", async () => {
    const user = userEvent.setup();
    const listKeys = rs.fn((request: { page?: { pageToken?: string } }) =>
      request.page?.pageToken === "cursor-2"
        ? { keys: [key({ id: "second" })], page: { nextPageToken: "" } }
        : { keys: [key({ id: "first" })], page: { nextPageToken: "cursor-2" } },
    );
    mount(<KeysPage {...props} />, (r) => r.service(KeyService, { listKeys }));
    await screen.findByText("first");
    const first = screen.getByRole("button", { name: "First page" });
    const next = screen.getByRole("button", { name: "Next page" });
    expect(first.hasAttribute("disabled")).toBe(true);
    expect(next.hasAttribute("disabled")).toBe(false);
    expect(listKeys.mock.calls[0]?.[0]).toMatchObject({
      organizationId: "one",
      page: { pageSize: 100, pageToken: "" },
    });
    await user.click(next);
    await screen.findByText("second");
    expect(listKeys.mock.calls.at(-1)?.[0]).toMatchObject({
      page: { pageSize: 100, pageToken: "cursor-2" },
    });
    expect(
      screen
        .getByRole("button", { name: "Next page" })
        .hasAttribute("disabled"),
    ).toBe(true);
    await user.click(screen.getByRole("button", { name: "First page" }));
    await screen.findByText("first");
    expect(listKeys.mock.calls.at(-1)?.[0]).toMatchObject({
      page: { pageToken: "" },
    });
  });

  it("keeps First page usable on a zero-key later page", async () => {
    const user = userEvent.setup();
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, {
        listKeys: (request) =>
          request.page?.pageToken
            ? { keys: [] }
            : {
                keys: [key({ id: "first" })],
                page: { nextPageToken: "later" },
              },
      }),
    );
    await user.click(await screen.findByRole("button", { name: "Next page" }));
    await screen.findByText("Create your first API key");
    expect(
      screen
        .getByRole("button", { name: "First page" })
        .hasAttribute("disabled"),
    ).toBe(false);
  });
});

describe("API keys list states", () => {
  it("shows an accessible Loading status, never invented rows, while loading", async () => {
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => new Promise(() => {}) }),
    );
    const loading = (await screen.findByText("Loading…")).closest(
      '[role="status"]',
    );
    expect(loading?.getAttribute("aria-busy")).toBe("true");
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.queryByText("Create your first API key")).toBeNull();
  });

  it("shows the empty state without a duplicate primary action", async () => {
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => ({ keys: [] }) }),
    );
    await screen.findByText("Create your first API key");
    expect(
      screen.getByText("Keys connect your clients to your personal routing."),
    ).toBeTruthy();
    expect(
      screen.getAllByRole("button", { name: "Create API key" }),
    ).toHaveLength(1);
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("shows a retryable error instead of the empty state", async () => {
    const user = userEvent.setup();
    let fail = true;
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, {
        listKeys: () => {
          if (fail) throw new ConnectError("down", Code.Unavailable);
          return { keys: [key({ id: "back" })] };
        },
      }),
    );
    await screen.findByRole("alert");
    expect(screen.queryByText("Create your first API key")).toBeNull();
    fail = false;
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByText("back");
  });

  it("keeps rows with a stale notice when a same-scope refresh fails", async () => {
    let fail = false;
    const view = mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, {
        listKeys: () => {
          if (fail) throw new ConnectError("down", Code.Unavailable);
          return { keys: [key({ id: "kept" })] };
        },
      }),
    );
    await screen.findByText("kept");
    fail = true;
    await view.cache.invalidateQueries();
    await screen.findByText(
      "Showing the last loaded API keys. The latest refresh failed.",
    );
    expect(screen.getByText("kept")).toBeTruthy();
    expect(screen.getByRole("alert")).toBeTruthy();
  });

  it("removes rows and shows no retry when access is lost", async () => {
    let denied = false;
    const view = mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, {
        listKeys: () => {
          if (denied) throw new ConnectError("no", Code.PermissionDenied);
          return { keys: [key({ id: "gone" })] };
        },
      }),
    );
    await screen.findByText("gone");
    denied = true;
    await view.cache.invalidateQueries();
    await screen.findByText(/You no longer have access to these API keys/);
    expect(screen.queryByText("gone")).toBeNull();
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
  });
});

describe("API keys list revoke handoff", () => {
  it("opens the confirmation without a request, then moves focus to the collection heading", async () => {
    const user = userEvent.setup();
    let revoked = false;
    const revokeKey = rs.fn((_request: unknown) => {
      revoked = true;
      return {};
    });
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, {
        listKeys: () => ({
          keys: [
            key({
              id: "a",
              name: "Same name",
              state: revoked ? KeyState.REVOKED : KeyState.ACTIVE,
            }),
            key({ id: "b", name: "Same name", state: KeyState.REVOKED }),
          ],
        }),
        revokeKey,
      }),
    );
    await user.click(
      await screen.findByRole("button", { name: "Revoke Same name" }),
    );
    expect(revokeKey).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Revoke key" }));
    await waitFor(() => expect(revokeKey).toHaveBeenCalledOnce());
    expect(revokeKey.mock.calls[0]?.[0]).toMatchObject({
      organizationId: "one",
      keyId: "a",
    });
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "Revoke Same name" }),
      ).toBeNull(),
    );
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole("heading", { name: "Your API keys" }),
      ),
    );
    expect(screen.getAllByText("Revoked")).toHaveLength(2);
  });

  it("clears the cursor, dialogs and rows when the organization changes", async () => {
    const user = userEvent.setup();
    const listKeys = rs.fn((request: { organizationId: string }) => ({
      keys: [
        key({
          id: request.organizationId,
          name: `Key ${request.organizationId}`,
        }),
      ],
      page: { nextPageToken: "more" },
    }));
    const view = mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys }),
    );
    await user.click(await screen.findByRole("button", { name: "Next page" }));
    await user.click(
      await screen.findByRole("button", { name: "Revoke Key one" }),
    );
    expect(screen.getByRole("dialog")).toBeTruthy();
    view.rerenderUi(
      <KeysPage
        session={session}
        organization={create(OrganizationSchema, {
          id: "two",
          name: "Second",
          role: Role.MEMBER,
        })}
      />,
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByText("Key one")).toBeNull();
    await screen.findByText("Key two");
    expect(listKeys.mock.calls.at(-1)?.[0]).toMatchObject({
      organizationId: "two",
      page: { pageToken: "" },
    });
  });

  it("never renders a raw key in the list, URL or storage after the result closes", async () => {
    const user = userEvent.setup();
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, {
        listKeys: () => ({ keys: fourKeys }),
        createKey: () => ({ rawKey: "aipr_raw-secret-value" }),
      }),
    );
    await user.click(
      await screen.findByRole("button", { name: "Create API key" }),
    );
    await user.type(screen.getByLabelText("Name"), "New");
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Create API key",
      }),
    );
    await screen.findByText("This key is shown only once.");
    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(document.body.innerHTML).not.toContain("raw-secret-value");
    expect(window.location.href).not.toContain("raw-secret-value");
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });
});
