// SPDX-License-Identifier: Apache-2.0
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, rs, afterEach } from "@rstest/core";
import {
  createRouterTransport,
  Code,
  ConnectError,
  type ConnectRouter,
} from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { QueryClient } from "@tanstack/react-query";
import {
  AccountService,
  OrganizationService,
  KeyService,
  OrganizationSchema,
  GetSessionResponseSchema,
  Role,
  KeyState,
} from "@tairouter/client";
import { ApiProvider } from "../src/api";
import { KeysPage } from "../src/keys";
import { App } from "../src/app";
import { Dialog, Field } from "../src/ui";
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
const DAY = 86400000;

function mount(ui: ReactNode, routes: (r: ConnectRouter) => void) {
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const transport = createRouterTransport(routes);
  return render(
    <ApiProvider queryClient={cache} rpcTransport={transport}>
      {ui}
    </ApiProvider>,
  );
}
async function openCreate(user: ReturnType<typeof userEvent.setup>) {
  await user.click(
    await screen.findByRole("button", { name: "Create API key" }),
  );
  return screen.getByRole("dialog", { name: "Create API key" });
}
function submit(dialog: HTMLElement) {
  return within(dialog).getByRole("button", {
    name: /^(Create API key|Creating…)$/,
  });
}
afterEach(() => {
  rs.restoreAllMocks();
  window.history.replaceState(null, "", "/");
});

describe("create API key dialog layout and defaults", () => {
  it("renders only Name and Expiry in the specified order with helper copy", async () => {
    const user = userEvent.setup();
    const createKey = rs.fn((_request: unknown) => ({ rawKey: "secret" }));
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => ({ keys: [] }), createKey }),
    );
    const dialog = await openCreate(user);
    expect(createKey).not.toHaveBeenCalled();
    expect(dialog.classList.contains("create-key-dialog")).toBe(true);
    const name = within(dialog).getByLabelText("Name") as HTMLInputElement;
    const expiry = within(dialog).getByLabelText("Expiry") as HTMLSelectElement;
    expect(document.activeElement).toBe(name);
    expect(name.value).toBe("");
    expect(name.required).toBe(true);
    expect(name.maxLength).toBe(100);
    expect(expiry.value).toBe("30");
    expect(
      Array.from(expiry.options).map((option) => option.textContent),
    ).toEqual(["7 days", "30 days", "90 days", "No expiry"]);
    expect(dialog.querySelectorAll("input,select,textarea")).toHaveLength(2);

    const order = [
      within(dialog).getByRole("heading", { name: "Create API key" }),
      within(dialog).getByRole("button", { name: "Close dialog" }),
      within(dialog).getByText("Use a name you can recognize later."),
      name,
      within(dialog).getByText("Up to 100 characters."),
      expiry,
      within(dialog).getByText("Choose 7, 30 or 90 days, or No expiry."),
      within(dialog).getByText("You can revoke this key from API keys."),
      within(dialog).getByRole("button", { name: "Cancel" }),
      submit(dialog),
    ];
    for (let i = 1; i < order.length; i += 1)
      expect(
        order[i - 1]!.compareDocumentPosition(order[i]!) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
    expect(name.getAttribute("aria-describedby")).toBeTruthy();
    expect(
      document.getElementById(name.getAttribute("aria-describedby")!)
        ?.textContent,
    ).toBe("Up to 100 characters.");
    expect(
      document.getElementById(expiry.getAttribute("aria-describedby")!)
        ?.textContent,
    ).toBe("Choose 7, 30 or 90 days, or No expiry.");
    expect(submit(dialog).hasAttribute("disabled")).toBe(true);
    await user.type(name, "   ");
    expect(submit(dialog).hasAttribute("disabled")).toBe(true);
    await user.selectOptions(expiry, "7");
    expect(createKey).not.toHaveBeenCalled();
  });
});

describe("create API key submission", () => {
  it.each([
    [7, 7],
    [30, 30],
    [90, 90],
  ])(
    "trims the name and computes a %i-day expiry at submission time",
    async (value, days) => {
      const user = userEvent.setup();
      const createKey = rs.fn((_request: unknown) => ({ rawKey: "secret" }));
      mount(<KeysPage {...props} />, (r) =>
        r.service(KeyService, { listKeys: () => ({ keys: [] }), createKey }),
      );
      const dialog = await openCreate(user);
      await user.type(within(dialog).getByLabelText("Name"), "  Laptop  ");
      await user.selectOptions(
        within(dialog).getByLabelText("Expiry"),
        String(value),
      );
      // Advance the clock after the dialog is open to prove it is read on submit.
      const now = 1_800_000_000_000;
      rs.spyOn(Date, "now").mockReturnValue(now);
      await user.click(submit(dialog));
      await screen.findByText("This key is shown only once.");
      expect(createKey).toHaveBeenCalledOnce();
      const request = createKey.mock.calls[0]?.[0] as {
        organizationId: string;
        name: string;
        expiresAt?: { seconds: bigint };
      };
      expect(request.organizationId).toBe("one");
      expect(request.name).toBe("Laptop");
      expect(request.expiresAt?.seconds).toBe(
        BigInt(Math.floor((now + days * DAY) / 1000)),
      );
    },
  );

  it("omits the expiry for No expiry and submits once on Enter", async () => {
    const user = userEvent.setup();
    const createKey = rs.fn((_request: unknown) => ({ rawKey: "secret" }));
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => ({ keys: [] }), createKey }),
    );
    const dialog = await openCreate(user);
    await user.selectOptions(within(dialog).getByLabelText("Expiry"), "0");
    expect(createKey).not.toHaveBeenCalled();
    await user.type(within(dialog).getByLabelText("Name"), "CI{Enter}");
    await screen.findByText("This key is shown only once.");
    expect(createKey).toHaveBeenCalledOnce();
    expect(
      (createKey.mock.calls[0]?.[0] as { expiresAt?: unknown }).expiresAt,
    ).toBeUndefined();
  });

  it("does not submit a whitespace-only name with Enter", async () => {
    const user = userEvent.setup();
    const createKey = rs.fn((_request: unknown) => ({ rawKey: "secret" }));
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => ({ keys: [] }), createKey }),
    );
    const dialog = await openCreate(user);
    await user.type(within(dialog).getByLabelText("Name"), "   {Enter}");
    expect(createKey).not.toHaveBeenCalled();
  });
});

describe("create API key pending state", () => {
  it("shows an in-dialog status, guards duplicates and hands off after a pending dismissal", async () => {
    const user = userEvent.setup();
    let release: (value: { rawKey: string }) => void = () => {};
    const createKey = rs.fn(
      (_request: unknown) =>
        new Promise<{ rawKey: string }>((resolve) => {
          release = resolve;
        }),
    );
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => ({ keys: [] }), createKey }),
    );
    const dialog = await openCreate(user);
    await user.type(within(dialog).getByLabelText("Name"), "Laptop{Enter}");
    await waitFor(() => expect(submit(dialog).textContent).toBe("Creating…"));
    expect(submit(dialog).hasAttribute("disabled")).toBe(true);
    const status = within(dialog).getByRole("status");
    expect(status.textContent).toBe(
      "Creating… Closing this dialog does not cancel a submitted change.",
    );
    fireEvent.submit(dialog.querySelector("form")!);
    await user.keyboard("{Enter}");
    await user.click(submit(dialog));
    expect(createKey).toHaveBeenCalledOnce();
    // Name and Expiry keep their values while pending.
    expect(
      (within(dialog).getByLabelText("Name") as HTMLInputElement).value,
    ).toBe("Laptop");
    expect(
      within(dialog)
        .getByRole("button", { name: "Cancel" })
        .hasAttribute("disabled"),
    ).toBe(false);
    // Dismissal does not cancel the submitted request.
    await user.click(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    );
    expect(screen.queryByRole("dialog", { name: "Create API key" })).toBeNull();
    release({ rawKey: "one-time-secret" });
    await screen.findByText("This key is shown only once.");
    expect(screen.getByLabelText("API key").getAttribute("value")).toBe(
      "one-time-secret",
    );
    expect(createKey).toHaveBeenCalledOnce();
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
    expect(window.location.href).not.toContain("one-time-secret");
  });

  it("dismisses with Cancel and Escape without sending a request", async () => {
    const user = userEvent.setup();
    const createKey = rs.fn((_request: unknown) => ({ rawKey: "secret" }));
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => ({ keys: [] }), createKey }),
    );
    let dialog = await openCreate(user);
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    dialog = await openCreate(user);
    fireEvent(
      dialog,
      new Event("cancel", { bubbles: false, cancelable: true }),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(createKey).not.toHaveBeenCalled();
  });
});

describe("create API key failure", () => {
  it("keeps the draft, announces one error, enables retry and never auto-retries", async () => {
    const user = userEvent.setup();
    const createKey = rs.fn((_request: unknown) => {
      throw new ConnectError("sensitive server detail", Code.Unavailable);
    });
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => ({ keys: [] }), createKey }),
    );
    const dialog = await openCreate(user);
    await user.type(within(dialog).getByLabelText("Name"), "Laptop");
    await user.selectOptions(within(dialog).getByLabelText("Expiry"), "90");
    await user.click(submit(dialog));
    await waitFor(() =>
      expect(within(dialog).getAllByRole("alert")).toHaveLength(1),
    );
    expect(screen.queryByText("sensitive server detail")).toBeNull();
    expect(screen.queryByText("API key created.")).toBeNull();
    expect(screen.queryByText("This key is shown only once.")).toBeNull();
    const name = within(dialog).getByLabelText("Name") as HTMLInputElement;
    expect(name.value).toBe("Laptop");
    expect(
      (within(dialog).getByLabelText("Expiry") as HTMLSelectElement).value,
    ).toBe("90");
    expect(submit(dialog).textContent).toBe("Create API key");
    expect(submit(dialog).hasAttribute("disabled")).toBe(false);
    // The error is associated with the Name control next to its helper text.
    const described = name.getAttribute("aria-describedby")!.split(" ");
    expect(described).toHaveLength(2);
    expect(
      described.map((id) => document.getElementById(id)).filter(Boolean),
    ).toHaveLength(2);
    expect(
      within(dialog).getByRole("alert").closest(`[id="${described[1]}"]`),
    ).toBeTruthy();
    expect(createKey).toHaveBeenCalledOnce();
    await user.click(submit(dialog));
    await waitFor(() => expect(createKey).toHaveBeenCalledTimes(2));
  });

  it("does not show a stale revoke failure when the create dialog opens", async () => {
    const user = userEvent.setup();
    const revokeKey = rs.fn(() => {
      throw new ConnectError("revoke failed", Code.Unavailable);
    });
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, {
        listKeys: () => ({
          keys: [
            {
              id: "key",
              name: "Old",
              state: KeyState.ACTIVE,
              prefix: "aipr_x",
            },
          ],
        }),
        revokeKey,
      }),
    );
    await user.click(await screen.findByRole("button", { name: "Revoke Old" }));
    await user.click(screen.getByRole("button", { name: "Revoke key" }));
    await waitFor(() => expect(revokeKey).toHaveBeenCalled());
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    const dialog = await openCreate(user);
    expect(within(dialog).queryByRole("alert")).toBeNull();
  });
});

describe("shared primitives", () => {
  it("keeps an existing aria-describedby when Field adds a hint", () => {
    render(
      <Field label="Value" hint="Hint text" describedBy="extra">
        <input aria-describedby="existing" />
      </Field>,
    );
    const described = screen
      .getByLabelText("Value")
      .getAttribute("aria-describedby")!
      .split(" ");
    expect(described[0]).toBe("existing");
    expect(described).toHaveLength(3);
    expect(described[2]).toBe("extra");
  });
  it("leaves other dialogs unscoped", () => {
    render(
      <Dialog title="Other" onClose={() => {}}>
        <p>Body</p>
      </Dialog>,
    );
    expect(screen.getByRole("dialog").className).toBe("");
  });
});

describe("create API key scope reset", () => {
  it("starts empty with 30 days after an organization change", async () => {
    window.history.replaceState(null, "", "/keys?organization=one");
    const user = userEvent.setup();
    const second = { id: "two", name: "Second", role: Role.MEMBER };
    mount(<App />, (r) => {
      r.service(AccountService, { getSession: () => session });
      r.service(OrganizationService, {
        listOrganizations: () => ({ organizations: [organization, second] }),
        getOrganization: (req) => ({
          organization: req.organizationId === "one" ? organization : second,
        }),
      });
      r.service(KeyService, { listKeys: () => ({ keys: [] }) });
    });
    let dialog = await openCreate(user);
    await user.type(within(dialog).getByLabelText("Name"), "Draft");
    await user.selectOptions(within(dialog).getByLabelText("Expiry"), "7");
    await user.click(
      screen.getAllByRole("button", { name: "Switch organization" })[0]!,
    );
    await user.click(screen.getByRole("button", { name: "Second Member" }));
    // The unsaved Name draft requires explicit confirmation before leaving the scope.
    await user.click(
      await screen.findByRole("button", { name: "Discard and switch" }),
    );
    await waitFor(() =>
      expect(window.location.pathname).toBe("/organizations/two/keys"),
    );
    expect(screen.queryByRole("dialog", { name: "Create API key" })).toBeNull();
    dialog = await openCreate(user);
    expect(
      (within(dialog).getByLabelText("Name") as HTMLInputElement).value,
    ).toBe("");
    expect(
      (within(dialog).getByLabelText("Expiry") as HTMLSelectElement).value,
    ).toBe("30");
  });
});
