// SPDX-License-Identifier: Apache-2.0
import { afterEach, beforeAll, describe, expect, it, rs } from "@rstest/core";
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { QueryClient } from "@tanstack/react-query";
import {
  AccountService,
  GetSessionResponseSchema,
  InvitationSchema,
  InvitationService,
  LifecycleState,
  DeliveryState,
  MembershipService,
  OrganizationSchema,
  OrganizationService,
  OwnershipService,
  OperatorService,
  Role,
} from "@aiprouter/client";
import { ApiProvider } from "../api";
import { AdminPage, OperatorPage } from "./index";
import { invitationStatus } from "./organization";
import { money } from "./common";

beforeAll(() => {
  if (!HTMLDialogElement.prototype.showModal)
    HTMLDialogElement.prototype.showModal = function () {
      this.setAttribute("open", "");
    };
  if (!HTMLDialogElement.prototype.close)
    HTMLDialogElement.prototype.close = function () {
      this.removeAttribute("open");
    };
});
afterEach(cleanup);
const session = create(GetSessionResponseSchema, {
  userId: "self",
  isInstanceOperator: true,
});
function fixture(
  options: { role?: Role; section?: string; operator?: boolean } = {},
) {
  let role = options.role ?? Role.OWNER;
  let operator = options.operator ?? true;
  const organization = create(OrganizationSchema, {
    id: "org-a",
    name: "Test organization",
    role,
  });
  // Mutable server state that individual tests adjust between refetches.
  const state: {
    name: string;
    transfer:
      | {
          id: string;
          toUserId: string;
          state: LifecycleState;
          expiresAt?: ReturnType<typeof timestampFromDate>;
        }
      | undefined;
    transferError: Code | undefined;
    updateError: boolean;
    updateGate: Promise<void> | undefined;
    cancelError: Code | undefined;
    memberPages:
      | { userId: string; displayName: string; email: string; role: Role }[][]
      | undefined;
  } = {
    name: organization.name,
    transfer: {
      id: "transfer",
      toUserId: "member",
      state: LifecycleState.PENDING,
      expiresAt: timestampFromDate(new Date(Date.now() - 60000)),
    },
    transferError: undefined,
    updateError: false,
    updateGate: undefined,
    cancelError: undefined,
    memberPages: undefined,
  };
  const mutations = {
    remove: rs.fn(),
    role: rs.fn(),
    invite: rs.fn(),
    resend: rs.fn(),
    delete: rs.fn(),
    settings: rs.fn(),
    nominate: rs.fn(),
    update: rs.fn(),
    cancel: rs.fn(),
  };
  const transport = createRouterTransport((router) => {
    router.service(AccountService, {
      getSession: () => ({ ...session, isInstanceOperator: operator }),
    });
    router.service(OrganizationService, {
      getOrganization: () => ({
        organization: { ...organization, name: state.name, role },
      }),
      deleteOrganization: (req) => {
        mutations.delete(req);
        return {};
      },
      updateOrganization: async (req) => {
        mutations.update(req);
        await state.updateGate;
        if (state.updateError)
          throw new ConnectError("rejected", Code.InvalidArgument);
        state.name = req.name;
        return {};
      },
    });
    router.service(MembershipService, {
      listMembers: (req) => {
        if (state.memberPages) {
          const index = Number(req.page?.pageToken || "0");
          return {
            members: state.memberPages[index],
            page:
              index + 1 < state.memberPages.length
                ? { nextPageToken: String(index + 1) }
                : undefined,
          };
        }
        return {
          members: [
            {
              userId: "owner",
              displayName: "Owner person",
              email: "owner@example.test",
              role: Role.OWNER,
            },
            {
              userId: "administrator",
              displayName: "Admin person",
              email: "admin@example.test",
              role: Role.ADMINISTRATOR,
            },
            {
              userId: "member",
              displayName: "Member person",
              email: "member@example.test",
              role: Role.MEMBER,
            },
          ],
        };
      },
      removeMember: () => {
        mutations.remove();
        return {};
      },
      setMemberRole: () => {
        mutations.role();
        return {};
      },
    });
    router.service(InvitationService, {
      listInvitations: () => ({
        invitations: [
          {
            id: "failed",
            email: "failed@example.test",
            state: LifecycleState.FAILED,
            deliveryState: DeliveryState.FAILED,
            expiresAt: timestampFromDate(new Date(Date.now() + 600000)),
          },
          {
            id: "cooldown",
            email: "wait@example.test",
            state: LifecycleState.PENDING,
            deliveryState: DeliveryState.SENT,
            resendAvailableAt: timestampFromDate(new Date(Date.now() + 600000)),
          },
        ],
      }),
      createInvitation: () => {
        mutations.invite();
        return {};
      },
      resendInvitation: () => {
        mutations.resend();
        return {};
      },
    });
    router.service(OwnershipService, {
      getOwnershipTransfer: () => {
        if (state.transferError !== undefined)
          throw new ConnectError("transfer", state.transferError);
        return { transfer: state.transfer };
      },
      nominateOwner: (req) => {
        mutations.nominate(req);
        return {};
      },
      cancelOwnershipTransfer: (req) => {
        mutations.cancel(req);
        if (state.cancelError !== undefined)
          throw new ConnectError("race", state.cancelError);
        return {};
      },
    });
    router.service(OperatorService, {
      getInstanceHealth: () => ({}),
      getInstanceUsage: () => ({}),
      getInstanceSettings: () => ({
        settings: {
          revision: "v1",
          requestLimits: { requestsPerMinute: 120, concurrentRequests: 10 },
          retention: { requestDays: 30, aggregateDays: 365, auditDays: 90 },
        },
      }),
      updateInstanceSettings: () => {
        mutations.settings();
        return {};
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const ui = (section = options.section ?? "members") => (
    <ApiProvider rpcTransport={transport} queryClient={client}>
      {options.section === "instance" ? (
        <OperatorPage session={session} />
      ) : (
        <AdminPage
          organization={organization}
          session={session}
          section={section}
        />
      )}
    </ApiProvider>
  );
  const view = render(ui());
  return {
    mutations,
    state,
    client,
    view,
    ui,
    setRole: (value: Role) => {
      role = value;
    },
    setOperator: (value: boolean) => {
      operator = value;
    },
  };
}
describe("current organization authority", () => {
  it("limits administrator actions to ordinary members", async () => {
    fixture({ role: Role.ADMINISTRATOR });
    expect(
      await screen.findByRole("button", { name: "Remove Member person" }),
    ).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: "Remove Admin person" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Make administrator" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Remove Owner person" }),
    ).toBeNull();
  });
  it("does not send a mutation after current demotion, even with an open dialog", async () => {
    const f = fixture();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Remove Member person" }),
    );
    const dialog = screen.getByRole("dialog");
    await user.type(within(dialog).getByRole("textbox"), "Member person");
    f.setRole(Role.MEMBER);
    await user.click(
      within(dialog).getByRole("button", { name: "Remove member" }),
    );
    await screen.findByText(
      "Your permissions changed. This action is no longer available.",
    );
    expect(f.mutations.remove).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).toBeNull();
  });
  it("closes owner controls when a refetch reports demotion", async () => {
    const f = fixture({ section: "settings" });
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Delete organization" }),
    );
    f.setRole(Role.ADMINISTRATOR);
    await f.client.invalidateQueries();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(
      screen.queryByRole("button", { name: "Delete organization" }),
    ).toBeNull();
  });
  it("focuses Cancel, traps focus, closes on Escape, and returns focus", async () => {
    fixture();
    const user = userEvent.setup();
    const trigger = await screen.findByRole("button", {
      name: "Remove Member person",
    });
    await user.click(trigger);
    const dialog = screen.getByRole("dialog");
    const cancel = within(dialog).getByRole("button", { name: "Cancel" });
    expect(document.activeElement).toBe(cancel);
    await user.type(within(dialog).getByRole("textbox"), "Member person");
    const submit = within(dialog).getByRole("button", {
      name: "Remove member",
    });
    submit.focus();
    await user.tab();
    expect(document.activeElement).toBe(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    );
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });
  it("requires an exact name before destructive organization deletion", async () => {
    const f = fixture({ section: "settings" });
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Delete organization" }),
    );
    const dialog = screen.getByRole("dialog");
    const button = within(dialog).getByRole("button", {
      name: "Delete organization",
    }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    await user.type(within(dialog).getByRole("textbox"), "test organization");
    expect(button.disabled).toBe(true);
    await user.clear(within(dialog).getByRole("textbox"));
    await user.type(within(dialog).getByRole("textbox"), "Test organization");
    expect(button.disabled).toBe(false);
    await user.click(button);
    await waitFor(() => expect(f.mutations.delete).toHaveBeenCalledTimes(1));
  });
  it("distinguishes expired transfers and prevents canceling an expired request", async () => {
    fixture({ section: "settings" });
    expect(await screen.findByText(/Transfer expired/)).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: "Cancel transfer" }),
    ).toBeNull();
  });
});
describe("invitation lifecycle", () => {
  it("shows failed delivery, invalid-link guidance and cooldown independently", async () => {
    fixture({ section: "invitations" });
    expect(await screen.findByText("Delivery failed")).toBeTruthy();
    expect(screen.getByText(/The link is invalid/)).toBeTruthy();
    const wait = screen
      .getByRole("heading", { name: "wait@example.test" })
      .closest("article")!;
    expect(
      (
        within(wait).getByRole("button", {
          name: "Resend",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    const failed = screen
      .getByRole("heading", { name: "failed@example.test" })
      .closest("article")!;
    expect(
      (
        within(failed).getByRole("button", {
          name: "Resend",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });
  it("focuses the critical email input and refuses a stale-authority invitation", async () => {
    const f = fixture({ section: "invitations" });
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Invite member" }),
    );
    expect(document.activeElement).toBe(
      screen.getByRole("textbox", { name: "Email address" }),
    );
    await user.type(
      screen.getByRole("textbox", { name: "Email address" }),
      "new@example.test",
    );
    f.setRole(Role.MEMBER);
    await user.click(screen.getByRole("button", { name: "Send invitation" }));
    await screen.findByText(
      "Your permissions changed. This action is no longer available.",
    );
    expect(f.mutations.invite).not.toHaveBeenCalled();
  });
  it("does not confuse sent, accepted, failed and expired", () => {
    const invite = create(InvitationSchema, {
      state: LifecycleState.PENDING,
      deliveryState: DeliveryState.SENT,
    });
    expect(invitationStatus(invite, Date.now())).toBe("Sent");
    expect(
      invitationStatus(
        { ...invite, state: LifecycleState.ACCEPTED },
        Date.now(),
      ),
    ).toBe("Accepted");
    expect(
      invitationStatus(
        { ...invite, state: LifecycleState.EXPIRED },
        Date.now(),
      ),
    ).toBe("Expired");
    expect(
      invitationStatus(
        { ...invite, deliveryState: DeliveryState.FAILED },
        Date.now(),
      ),
    ).toBe("Delivery failed");
  });
});
describe("operator boundary and unknown values", () => {
  it("requires operator authority independently from organization ownership", async () => {
    fixture({ section: "instance", operator: false });
    await screen.findByText(
      "Your permissions changed. This action is no longer available.",
    );
    expect(screen.queryByRole("button", { name: "Request limits" })).toBeNull();
  });
  it("rechecks operator identity before changing instance limits", async () => {
    const f = fixture({ section: "instance" });
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Request limits" }),
    );
    const input = await screen.findByRole("spinbutton", {
      name: "Requests per minute",
    });
    await user.clear(input);
    await user.type(input, "200");
    f.setOperator(false);
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await screen.findByText(
      "Your permissions changed. This action is no longer available.",
    );
    expect(f.mutations.settings).not.toHaveBeenCalled();
  });
  it("keeps absent cost distinct from confirmed zero", () => {
    expect(money(undefined)).toBe("Unknown");
    expect(
      money({
        $typeName: "aiprouter.v1.Money",
        currency: "USD",
        decimalAmount: "0",
      }),
    ).toBe("USD 0");
  });
});

const pendingTransfer = () => ({
  id: "transfer",
  toUserId: "member",
  state: LifecycleState.PENDING,
  expiresAt: timestampFromDate(new Date(Date.now() + 86400000)),
});
const memberFixture = (userId: string, displayName: string, role: Role) => ({
  userId,
  displayName,
  email: `${userId}@example.test`,
  role,
});
async function chooseMember(
  user: ReturnType<typeof userEvent.setup>,
  id: string,
) {
  await user.selectOptions(
    await screen.findByRole("combobox", {
      name: "Nominate an existing member",
    }),
    id,
  );
}

describe("organization settings name card", () => {
  it("renders the page hierarchy with three stacked cards for owners", async () => {
    fixture({ section: "settings" });
    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Organization settings",
      }),
    ).toBeTruthy();
    expect(screen.getByText("Manage Test organization.")).toBeTruthy();
    for (const name of [
      "Organization name",
      "Ownership transfer",
      "Delete organization",
    ])
      expect(screen.getByRole("heading", { level: 2, name })).toBeTruthy();
  });
  it("hides owner cards from administrators", async () => {
    fixture({ section: "settings", role: Role.ADMINISTRATOR });
    await screen.findByRole("heading", { name: "Organization name" });
    expect(
      screen.queryByRole("heading", { name: "Ownership transfer" }),
    ).toBeNull();
    expect(
      screen.queryByRole("heading", { name: "Delete organization" }),
    ).toBeNull();
  });
  it("focuses the name input once, disables unchanged and blank saves, keeps the draft", async () => {
    const f = fixture({ section: "settings" });
    const user = userEvent.setup();
    const input = (await screen.findByRole("textbox", {
      name: "Name",
    })) as HTMLInputElement;
    expect(document.activeElement).toBe(input);
    const save = screen.getByRole("button", {
      name: "Save name",
    }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    await user.clear(input);
    await user.type(input, "   ");
    expect(save.disabled).toBe(true);
    await user.clear(input);
    await user.type(input, "Acme Studio 2 ");
    expect(save.disabled).toBe(false);
    f.state.updateError = true;
    await user.click(save);
    // The failure is announced and associated with the field; the draft stays
    // and the save payload carries the untrimmed value.
    await waitFor(() =>
      expect(input.getAttribute("aria-invalid")).toBe("true"),
    );
    const describedBy = input.getAttribute("aria-describedby")!;
    expect(document.getElementById(describedBy)?.textContent).toContain(
      "The request failed.",
    );
    expect(f.mutations.update).toHaveBeenCalledTimes(1);
    expect(f.mutations.update.mock.calls[0][0]).toMatchObject({
      organizationId: "org-a",
      name: "Acme Studio 2 ",
    });
    // Refetch and owner dialogs do not replace the draft or steal focus.
    const other = screen.getByRole("button", { name: "Delete organization" });
    other.focus();
    await f.client.invalidateQueries();
    expect(input.value).toBe("Acme Studio 2 ");
    expect(document.activeElement).toBe(other);
    await user.click(other);
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Cancel",
      }),
    );
    expect(input.value).toBe("Acme Studio 2 ");
  });
  it("does not replace text typed while a save is in flight", async () => {
    const f = fixture({ section: "settings" });
    const user = userEvent.setup();
    let release = () => {};
    f.state.updateGate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const input = (await screen.findByRole("textbox", {
      name: "Name",
    })) as HTMLInputElement;
    await user.clear(input);
    await user.type(input, "First");
    await user.click(screen.getByRole("button", { name: "Save name" }));
    await waitFor(() => expect(f.mutations.update).toHaveBeenCalled());
    await user.type(input, " and more");
    release();
    await screen.findByText("Manage First.");
    expect(input.value).toBe("First and more");
    expect(
      (screen.getByRole("button", { name: "Save name" }) as HTMLButtonElement)
        .disabled,
    ).toBe(false);
  });
});

describe("organization settings ownership card", () => {
  it("shows a pending transfer status first with replace and cancel actions", async () => {
    const f = fixture({ section: "settings" });
    f.state.transfer = pendingTransfer();
    await f.client.invalidateQueries();
    expect(await screen.findByText("Awaiting acceptance")).toBeTruthy();
    expect(screen.getAllByText("Member person").length).toBe(2);
    expect(
      screen.getByRole("button", { name: "Cancel transfer" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Replace nomination" }),
    ).toBeTruthy();
    expect(
      screen.getByText(
        "You remain owner until the nominee accepts. They become owner and you become an administrator. A nomination expires after seven days.",
      ),
    ).toBeTruthy();
  });
  it("shows the user ID when the nominee is not on the loaded page and Unknown for unknown states", async () => {
    const f = fixture({ section: "settings" });
    f.state.transfer = {
      ...pendingTransfer(),
      toUserId: "elsewhere",
      state: LifecycleState.UNSPECIFIED,
      expiresAt: undefined,
    };
    await f.client.invalidateQueries();
    expect(await screen.findByText("elsewhere")).toBeTruthy();
    expect(screen.getAllByText("Unknown").length).toBe(2);
    expect(
      screen.queryByRole("button", { name: "Cancel transfer" }),
    ).toBeNull();
    expect(screen.getByRole("button", { name: "Nominate owner" })).toBeTruthy();
  });
  it("treats NotFound as no transfer but other failures as an error with retry", async () => {
    const f = fixture({ section: "settings" });
    f.state.transfer = undefined;
    await f.client.invalidateQueries();
    expect(await screen.findByText("No pending transfer.")).toBeTruthy();
    f.state.transferError = Code.NotFound;
    await f.client.invalidateQueries();
    expect(await screen.findByText("No pending transfer.")).toBeTruthy();
    f.state.transferError = Code.Unavailable;
    await f.client.invalidateQueries();
    await screen.findByText(/The service is unavailable/);
    expect(screen.queryByText("No pending transfer.")).toBeNull();
    const user = userEvent.setup();
    await chooseMember(user, "member");
    expect(
      (
        screen.getByRole("button", {
          name: "Nominate owner",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    f.state.transferError = undefined;
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByText("No pending transfer.");
    expect(
      (
        screen.getByRole("button", {
          name: "Nominate owner",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });
  it("keeps paging on a page without eligible members and resets selection on page change", async () => {
    const f = fixture({ section: "settings" });
    f.state.memberPages = [
      [memberFixture("owner", "Owner person", Role.OWNER)],
      [memberFixture("taylor", "Taylor", Role.MEMBER)],
    ];
    const user = userEvent.setup();
    await f.client.invalidateQueries();
    expect(
      await screen.findByText("No eligible members on this page."),
    ).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    await chooseMember(user, "taylor");
    const select = screen.getByRole("combobox", {
      name: "Nominate an existing member",
    }) as HTMLSelectElement;
    expect(select.value).toBe("taylor");
    await user.click(screen.getByRole("button", { name: "First page" }));
    await screen.findByText("No eligible members on this page.");
    expect(select.value).toBe("");
    expect(
      (
        screen.getByRole("button", {
          name: "Nominate owner",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });
});

describe("organization settings owner dialogs", () => {
  it("confirms a replacement nomination with consequences and no typed input", async () => {
    const f = fixture({ section: "settings" });
    f.state.transfer = pendingTransfer();
    f.state.memberPages = [
      [
        memberFixture("owner", "Owner person", Role.OWNER),
        memberFixture("member", "Jordan Lee", Role.MEMBER),
        memberFixture("taylor", "Taylor Chen", Role.MEMBER),
      ],
    ];
    await f.client.invalidateQueries();
    const user = userEvent.setup();
    await screen.findByText("Awaiting acceptance");
    await chooseMember(user, "taylor");
    const trigger = screen.getByRole("button", { name: "Replace nomination" });
    await user.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "Replace nomination" });
    expect(dialog.className).toContain("admin-dialog-wide");
    expect(within(dialog).queryByRole("textbox")).toBeNull();
    expect(document.activeElement).toBe(
      within(dialog).getByRole("button", { name: "Cancel" }),
    );
    expect(
      within(dialog).getByText(
        "Nominate Taylor Chen as owner of Test organization?",
      ),
    ).toBeTruthy();
    expect(
      within(dialog).getByText(
        "The previous request for Jordan Lee becomes invalid.",
      ),
    ).toBeTruthy();
    expect(
      within(dialog).getByText(
        /Ownership changes only after Taylor Chen accepts\. Taylor Chen becomes owner and you become an administrator\. You remain owner until acceptance\. This nomination expires after seven days\./,
      ),
    ).toBeTruthy();
    expect(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    ).toBeTruthy();
    await user.click(
      within(dialog).getByRole("button", { name: "Replace nomination" }),
    );
    await waitFor(() => expect(f.mutations.nominate).toHaveBeenCalledTimes(1));
    expect(f.mutations.nominate.mock.calls[0][0]).toMatchObject({
      organizationId: "org-a",
      userId: "taylor",
    });
    expect(await screen.findByText("Ownership nomination sent.")).toBeTruthy();
  });
  it("uses the generic invalidation warning when no previous nominee is loaded", async () => {
    const f = fixture({ section: "settings" });
    f.state.transfer = { ...pendingTransfer(), toUserId: "elsewhere" };
    await f.client.invalidateQueries();
    const user = userEvent.setup();
    await screen.findByText("elsewhere");
    await chooseMember(user, "administrator");
    await user.click(
      screen.getByRole("button", { name: "Replace nomination" }),
    );
    expect(
      within(screen.getByRole("dialog")).getByText(
        "Any previous request becomes invalid.",
      ),
    ).toBeTruthy();
  });
  it("keeps the transfer when dismissed and cancels the displayed transfer ID", async () => {
    const f = fixture({ section: "settings" });
    f.state.transfer = pendingTransfer();
    await f.client.invalidateQueries();
    const user = userEvent.setup();
    const open = async () => {
      await user.click(
        await screen.findByRole("button", { name: "Cancel transfer" }),
      );
      return screen.getByRole("dialog", { name: "Cancel transfer" });
    };
    let dialog = await open();
    expect(dialog.className).not.toContain("admin-dialog-wide");
    expect(document.activeElement).toBe(
      within(dialog).getByRole("button", { name: "Keep transfer" }),
    );
    expect(
      within(dialog).getByText(
        "Cancel the ownership transfer to Member person?",
      ),
    ).toBeTruthy();
    expect(
      within(dialog).getByText(
        "The nominee will no longer be able to accept this transfer. You remain the owner of Test organization.",
      ),
    ).toBeTruthy();
    await user.click(
      within(dialog).getByRole("button", { name: "Keep transfer" }),
    );
    dialog = await open();
    await user.click(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    );
    dialog = await open();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(f.mutations.cancel).not.toHaveBeenCalled();
    // A replacement made while the dialog is open must not be canceled.
    dialog = await open();
    f.state.transfer = { ...pendingTransfer(), id: "replacement" };
    await user.click(
      within(dialog).getByRole("button", { name: "Cancel transfer" }),
    );
    await waitFor(() => expect(f.mutations.cancel).toHaveBeenCalledTimes(1));
    expect(f.mutations.cancel.mock.calls[0][0]).toMatchObject({
      organizationId: "org-a",
      transferId: "transfer",
    });
    expect(
      await screen.findByText("Ownership transfer canceled."),
    ).toBeTruthy();
  });
  it("surfaces a lost cancellation race and refetches instead of reporting success", async () => {
    const f = fixture({ section: "settings" });
    f.state.transfer = pendingTransfer();
    await f.client.invalidateQueries();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Cancel transfer" }),
    );
    f.state.cancelError = Code.FailedPrecondition;
    f.state.transfer = { ...pendingTransfer(), state: LifecycleState.ACCEPTED };
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Cancel transfer",
      }),
    );
    await screen.findAllByText(/This action is no longer valid/);
    expect(screen.queryByText("Ownership transfer canceled.")).toBeNull();
    await waitFor(() => expect(screen.getByText("Accepted")).toBeTruthy());
  });
  it("focuses the deletion input and binds confirmation to the saved name", async () => {
    const f = fixture({ section: "settings" });
    const user = userEvent.setup();
    const trigger = await screen.findByRole("button", {
      name: "Delete organization",
    });
    await user.click(trigger);
    let dialog = screen.getByRole("dialog", { name: "Delete organization" });
    expect(dialog.className).toContain("admin-dialog-wide");
    expect(
      within(dialog).getByText("Permanently delete Test organization?"),
    ).toBeTruthy();
    expect(
      within(dialog).getByText(
        "This permanently deletes Test organization’s credentials, settings, temporary state, usage and organization audit records.",
      ),
    ).toBeTruthy();
    const input = within(dialog).getByRole("textbox", {
      name: "Type Test organization to confirm",
    }) as HTMLInputElement;
    expect(document.activeElement).toBe(input);
    expect(input.value).toBe("");
    expect(input.getAttribute("aria-describedby")).toBeTruthy();
    expect(
      within(dialog).getByText("The name must match exactly."),
    ).toBeTruthy();
    const submit = within(dialog).getByRole("button", {
      name: "Delete organization",
    }) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    await user.type(input, "Test organization ");
    expect(submit.disabled).toBe(true);
    await user.clear(input);
    await user.type(input, "Test organization");
    expect(submit.disabled).toBe(false);
    // A rename elsewhere invalidates the earlier typed text.
    f.state.name = "Renamed organization";
    await f.client.invalidateQueries();
    await waitFor(() => expect(submit.disabled).toBe(true));
    // Dismissal clears the typed text.
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(document.activeElement).toBe(trigger);
    await user.click(trigger);
    dialog = screen.getByRole("dialog");
    expect(
      (within(dialog).getByRole("textbox") as HTMLInputElement).value,
    ).toBe("");
    await user.type(
      within(dialog).getByRole("textbox"),
      "Renamed organization",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Delete organization" }),
    );
    await waitFor(() => expect(f.mutations.delete).toHaveBeenCalledTimes(1));
    expect(f.mutations.delete.mock.calls[0][0]).toMatchObject({
      organizationId: "org-a",
    });
  });
  it("leaves the shared Members removal dialog at its original size", async () => {
    fixture();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Remove Member person" }),
    );
    expect(screen.getByRole("dialog").className).not.toContain(
      "admin-dialog-wide",
    );
  });
});
