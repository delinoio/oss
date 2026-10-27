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
import { createRouterTransport } from "@connectrpc/connect";
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
  const mutations = {
    remove: rs.fn(),
    role: rs.fn(),
    invite: rs.fn(),
    resend: rs.fn(),
    delete: rs.fn(),
    settings: rs.fn(),
    nominate: rs.fn(),
  };
  const transport = createRouterTransport((router) => {
    router.service(AccountService, {
      getSession: () => ({ ...session, isInstanceOperator: operator }),
    });
    router.service(OrganizationService, {
      getOrganization: () => ({ organization: { ...organization, role } }),
      deleteOrganization: () => {
        mutations.delete();
        return {};
      },
    });
    router.service(MembershipService, {
      listMembers: () => ({
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
      }),
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
      getOwnershipTransfer: () => ({
        transfer: {
          id: "transfer",
          toUserId: "member",
          state: LifecycleState.PENDING,
          expiresAt: timestampFromDate(new Date(Date.now() - 60000)),
        },
      }),
      nominateOwner: () => {
        mutations.nominate();
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
    await user.click(
      screen.getByRole("button", { name: "Review and save changes" }),
    );
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
