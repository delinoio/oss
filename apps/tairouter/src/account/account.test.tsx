// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, rs } from "@rstest/core";
import {
  createRouterTransport,
  ConnectError,
  Code,
  type ConnectRouter,
} from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { QueryClient } from "@tanstack/react-query";
import {
  AccountService,
  DeviceApprovalService,
  DeviceAuthorizationState as State,
  ErrorDetailSchema,
  ErrorReason,
  InvitationService,
  LifecycleState,
  OrganizationService,
  OwnershipService,
  Role,
} from "@tairouter/client";
import { useEffect, useState, type ReactNode } from "react";
import { ApiProvider, DirtyContext } from "../api";
import {
  AccountPage,
  DevicePage,
  InvitationPage,
  OnboardingPage,
  OwnershipAcceptancePage,
  SignInPage,
} from "./index";

function mount(ui: ReactNode, routes: (router: ConnectRouter) => void) {
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const view = render(
    <ApiProvider
      rpcTransport={createRouterTransport(routes)}
      queryClient={cache}
    >
      {ui}
    </ApiProvider>,
  );
  return { ...view, cache };
}
const org = { id: "org-one", name: "Studio", role: Role.MEMBER };
const session = { userId: "user-one", email: "person@example.test" };
const future = () => timestampFromDate(new Date(Date.now() + 600000));
const device = () => ({
  authorizationId: "device-one",
  deviceName: "SSH workstation",
  serverAddress: "https://router.example.test",
  state: State.PENDING,
  expiresAt: future(),
});

describe("browser device authorization", () => {
  it("requires code review and explicit selected-organization approval; no CLI key or storage success is invented", async () => {
    const user = userEvent.setup();
    const authorization = device();
    const approve = rs.fn((request) => ({
      authorization: {
        ...authorization,
        organizationId: request.organizationId,
        state: State.APPROVED,
      },
    }));
    mount(<DevicePage />, (router) => {
      router.service(DeviceApprovalService, {
        getDeviceAuthorization: () => ({ authorization }),
        approveDeviceAuthorization: approve,
      });
      router.service(OrganizationService, {
        listOrganizations: () => ({ organizations: [org] }),
      });
    });
    expect(document.activeElement).toBe(
      screen.getByLabelText("Connection code"),
    );
    await user.type(screen.getByLabelText("Connection code"), "ABCD-EFGH");
    await user.click(screen.getByRole("button", { name: "Review connection" }));
    await screen.findByText("SSH workstation");
    expect(approve).not.toHaveBeenCalled();
    expect(
      (
        screen.getByRole("button", {
          name: "Approve connection",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    await user.selectOptions(screen.getByLabelText("Organization"), org.id);
    await user.selectOptions(screen.getByLabelText("Key expiry"), "none");
    await user.click(
      screen.getByRole("button", { name: "Approve connection" }),
    );
    await screen.findByText("Approved — waiting for CLI storage");
    expect(approve).toHaveBeenCalledTimes(1);
    expect(approve.mock.calls[0][0]).toMatchObject({
      userCode: "ABCD-EFGH",
      organizationId: org.id,
    });
    expect(approve.mock.calls[0][0].keyExpiresAt).toBeUndefined();
    expect(screen.queryByRole("heading", { name: "Connected" })).toBeNull();
    expect(screen.queryByText(/raw.key/i)).toBeNull();
  });

  it("denies without approval and keeps denial distinct", async () => {
    const user = userEvent.setup();
    const approve = rs.fn();
    const deny = rs.fn(() => ({}));
    mount(<DevicePage initialCode="CODE" />, (router) => {
      router.service(DeviceApprovalService, {
        getDeviceAuthorization: () => ({ authorization: device() }),
        approveDeviceAuthorization: approve,
        denyDeviceAuthorization: deny,
      });
      router.service(OrganizationService, {
        listOrganizations: () => ({ organizations: [org] }),
      });
    });
    await user.click(await screen.findByRole("button", { name: "Deny" }));
    await screen.findByText("Connection denied");
    expect(deny).toHaveBeenCalledTimes(1);
    expect(approve).not.toHaveBeenCalled();
  });

  it("does not restart the original deadline on mount", async () => {
    mount(<DevicePage initialCode="OLD" />, (router) => {
      router.service(DeviceApprovalService, {
        getDeviceAuthorization: () => ({
          authorization: {
            ...device(),
            expiresAt: timestampFromDate(new Date(Date.now() - 1)),
          },
        }),
      });
      router.service(OrganizationService, {
        listOrganizations: () => ({ organizations: [org] }),
      });
    });
    await screen.findByText("Connection expired");
    expect(
      screen.queryByRole("button", { name: "Approve connection" }),
    ).toBeNull();
  });

  it.each([
    [State.DELIVERED, "Connected"],
    [State.REVOKED, "Connection not completed"],
    [State.DENIED, "Connection denied"],
  ])("renders confirmed terminal state %s", async (state, heading) => {
    mount(<DevicePage initialCode="CODE" />, (router) => {
      router.service(DeviceApprovalService, {
        getDeviceAuthorization: () => ({
          authorization: { ...device(), state },
        }),
      });
      router.service(OrganizationService, {
        listOrganizations: () => ({ organizations: [org] }),
      });
    });
    await screen.findByRole("heading", { name: heading });
    expect(
      screen.queryByRole("button", { name: "Approve connection" }),
    ).toBeNull();
  });

  it("reports unavailable approval without displaying success or retrying the mutation", async () => {
    const user = userEvent.setup();
    const approve = rs.fn(() => {
      throw new ConnectError("private diagnostic", Code.Unavailable);
    });
    mount(<DevicePage initialCode="CODE" />, (router) => {
      router.service(DeviceApprovalService, {
        getDeviceAuthorization: () => ({ authorization: device() }),
        approveDeviceAuthorization: approve,
      });
      router.service(OrganizationService, {
        listOrganizations: () => ({ organizations: [org] }),
      });
    });
    await screen.findByText("SSH workstation");
    await user.selectOptions(screen.getByLabelText("Organization"), org.id);
    await user.click(
      screen.getByRole("button", { name: "Approve connection" }),
    );
    await screen.findByText(
      "The service is unavailable. No change has been confirmed.",
    );
    expect(approve).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("private diagnostic")).toBeNull();
    expect(screen.queryByText("Approved — waiting for CLI storage")).toBeNull();
  });
});

describe("account and onboarding", () => {
  it("blocks self-deletion when any owned organization exists", async () => {
    const deletion = rs.fn();
    mount(<AccountPage />, (router) => {
      router.service(AccountService, {
        getSession: () => session,
        deleteAccount: deletion,
      });
      router.service(OrganizationService, {
        listOrganizations: () => ({
          organizations: [{ ...org, role: Role.OWNER }],
        }),
      });
    });
    await screen.findByText("These organizations block account deletion:");
    expect(
      (
        screen.getByRole("button", {
          name: "Delete my account",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(deletion).not.toHaveBeenCalled();
  });

  it("returns focus after Escape and does not delete merely by opening the dialog", async () => {
    const user = userEvent.setup();
    const deletion = rs.fn(() => ({}));
    mount(<AccountPage />, (router) => {
      router.service(AccountService, {
        getSession: () => session,
        deleteAccount: deletion,
      });
      router.service(OrganizationService, {
        listOrganizations: () => ({ organizations: [] }),
      });
    });
    await screen.findByText("person@example.test");
    const trigger = screen.getByRole("button", { name: "Delete my account" });
    await waitFor(() =>
      expect((trigger as HTMLButtonElement).disabled).toBe(false),
    );
    await user.click(trigger);
    const dialog = screen.getByRole("dialog");
    expect(deletion).not.toHaveBeenCalled();
    screen.getByRole("button", { name: "Cancel" }).focus();
    await user.tab({ shift: true });
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Close Delete your account?" }),
    );
    await user.tab();
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Cancel" }),
    );
    fireEvent(dialog, new Event("cancel", { bubbles: true, cancelable: true }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("does not create an organization until explicitly submitted and uses generated Connect input", async () => {
    const user = userEvent.setup();
    const createOrg = rs.fn((request) => ({
      organization: { ...org, name: request.name },
    }));
    mount(<OnboardingPage />, (router) => {
      router.service(OrganizationService, { createOrganization: createOrg });
      router.service(InvitationService, {
        listMyInvitations: () => ({ invitations: [] }),
      });
    });
    expect(document.activeElement).toBe(
      screen.getByLabelText("Organization name"),
    );
    expect(createOrg).not.toHaveBeenCalled();
    await user.type(screen.getByLabelText("Organization name"), "New team");
    await user.click(
      screen.getByRole("button", { name: "Create organization" }),
    );
    await screen.findByText("Organization created");
    expect(createOrg.mock.calls[0][0]).toMatchObject({ name: "New team" });
  });

  it("renders OIDC blocking errors and restricts redirect destinations", () => {
    render(
      <SignInPage error="unverified_email" returnTo="//external.example" />,
    );
    expect(screen.getByRole("alert").textContent).toContain(
      "Verify your email",
    );
    expect(screen.getByRole("link").getAttribute("href")).toBe(
      "/auth/login?return_to=%2Foverview",
    );
  });

  describe("sign-in card", () => {
    const fallback = "Sign-in could not be completed. Start a new sign-in.";
    const setQuery = (query: string) =>
      window.history.replaceState(null, "", `/sign-in${query}`);
    afterEach(() => window.history.replaceState(null, "", "/"));

    it("renders the default state with one heading, one link and no alert", () => {
      setQuery("");
      const { container } = render(<SignInPage />);
      expect(screen.getAllByRole("heading")).toHaveLength(1);
      expect(
        screen.getByRole("heading", { level: 1, name: "Sign in to TaiRouter" }),
      ).toBeTruthy();
      expect(
        screen.getByText(
          "Use your organization's identity provider to continue.",
        ),
      ).toBeTruthy();
      expect(
        screen.getByText(
          "Your identity provider must supply a verified email address.",
        ),
      ).toBeTruthy();
      expect(screen.queryByRole("alert")).toBeNull();
      const link = screen.getByRole("link", {
        name: "Continue with your identity provider",
      });
      expect(link.getAttribute("href")).toBe(
        "/auth/login?return_to=%2Foverview",
      );
      expect(container.querySelectorAll("a")).toHaveLength(1);
      expect(container.querySelectorAll("input, select, button")).toHaveLength(
        0,
      );
      expect(container.querySelector(".sign-in-wordmark")?.textContent).toBe(
        "TaiRouter",
      );
      expect(container.querySelector("a.sign-in-wordmark")).toBeNull();
    });

    it.each([
      [
        "missing_email",
        "Your identity provider did not provide an email address. Add an email there, then try again.",
      ],
      [
        "unverified_email",
        "Verify your email address with your identity provider, then try again.",
      ],
      [
        "verified_email_required",
        "Your identity provider must provide a verified email address. Correct this at your identity provider, then try again.",
      ],
      [
        "login_denied",
        "Sign-in was canceled or denied. Try again when you are ready.",
      ],
      ["login_expired", "This sign-in attempt expired. Start a new sign-in."],
      [
        "authentication_unavailable",
        "Sign-in is temporarily unavailable. Try again later.",
      ],
      ["private_diagnostic_123", fallback],
    ])("maps reason %s to its safe copy", (reason, message) => {
      setQuery("");
      const { container } = render(<SignInPage error={reason} />);
      const alert = screen.getByRole("alert");
      expect(alert.textContent).toBe(message);
      expect(alert.textContent).not.toContain("private_diagnostic");
      expect(container.querySelectorAll('[role="alert"]')).toHaveLength(1);
      expect(alert.querySelector("svg")?.getAttribute("aria-hidden")).toBe(
        "true",
      );
    });

    it("omits the alert for an empty reason", () => {
      setQuery("");
      render(<SignInPage error="" />);
      expect(screen.queryByRole("alert")).toBeNull();
    });

    it("reads the query error and lets the error prop win", () => {
      setQuery("?error=login_expired");
      const first = render(<SignInPage />);
      expect(screen.getByRole("alert").textContent).toBe(
        "This sign-in attempt expired. Start a new sign-in.",
      );
      first.unmount();
      render(<SignInPage error="login_denied" />);
      expect(screen.getByRole("alert").textContent).toContain(
        "canceled or denied",
      );
    });

    it("treats an empty error prop as authoritative over the query", () => {
      setQuery("?error=login_expired");
      render(<SignInPage error="" />);
      expect(screen.queryByRole("alert")).toBeNull();
    });

    it("does not recreate the alert on an unchanged rerender", () => {
      setQuery("");
      const view = render(<SignInPage error="login_denied" />);
      const alert = screen.getByRole("alert");
      view.rerender(<SignInPage error="login_denied" />);
      expect(screen.getByRole("alert")).toBe(alert);
      expect(screen.getAllByRole("alert")).toHaveLength(1);
    });

    it.each([
      [undefined, "", "%2Foverview"],
      ["/device?code=fictional", "", "%2Fdevice%3Fcode%3Dfictional"],
      [
        undefined,
        "?return_to=/device?code=fictional",
        "%2Fdevice%3Fcode%3Dfictional",
      ],
      ["/device", "?return_to=/other", "%2Fdevice"],
      ["//external.example", "", "%2Foverview"],
      ["https://external.example", "", "%2Foverview"],
      ["/a\\b", "", "%2Foverview"],
    ])(
      "builds the href for returnTo %s and query %s",
      (returnTo, query, encoded) => {
        setQuery(query);
        render(<SignInPage returnTo={returnTo} />);
        expect(screen.getByRole("link").getAttribute("href")).toBe(
          `/auth/login?return_to=${encoded}`,
        );
      },
    );

    it("focuses the heading on first render and never the link", () => {
      setQuery("");
      render(<SignInPage />);
      const heading = screen.getByRole("heading", { level: 1 });
      expect(heading.getAttribute("tabindex")).toBe("-1");
      expect(document.activeElement).toBe(heading);
      expect(document.activeElement).not.toBe(screen.getByRole("link"));
    });

    it("leaves other AccountSurface consumers without heading focus", () => {
      mount(<OnboardingPage />, () => {});
      const heading = screen.getByRole("heading", { level: 1 });
      expect(heading.hasAttribute("tabindex")).toBe(false);
      expect(document.activeElement).not.toBe(heading);
      expect(document.querySelector(".sign-in-card")).toBeNull();
    });
  });
});

describe("public acceptance", () => {
  it.each([
    [
      LifecycleState.EXPIRED,
      "This invitation has expired. Ask an administrator for a new invitation.",
    ],
    [
      LifecycleState.SUPERSEDED,
      "This invitation was replaced. Open the newest invitation email.",
    ],
    [LifecycleState.CANCELED, "This invitation was canceled."],
    [LifecycleState.ACCEPTED, "This invitation has already been used."],
  ])("does not accept terminal invitation state %s", async (state, message) => {
    const accept = rs.fn();
    mount(<InvitationPage token="invitation-token" />, (router) => {
      router.service(AccountService, { getSession: () => session });
      router.service(InvitationService, {
        getInvitation: () => ({
          invitation: {
            id: "invite",
            organizationName: "Studio",
            email: session.email,
            state,
            expiresAt: future(),
            role: Role.MEMBER,
          },
        }),
        acceptInvitation: accept,
      });
    });
    await screen.findByText(message);
    expect(
      (
        screen.getByRole("button", {
          name: "Accept invitation",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(accept).not.toHaveBeenCalled();
  });

  it("explains wrong-email invitations without revealing metadata", async () => {
    mount(<InvitationPage token="invitation-token" />, (router) => {
      router.service(AccountService, { getSession: () => session });
      router.service(InvitationService, {
        getInvitation: () => {
          throw new ConnectError(
            "secret recipient",
            Code.PermissionDenied,
            undefined,
            [
              {
                desc: ErrorDetailSchema,
                value: create(ErrorDetailSchema, {
                  reason: ErrorReason.INVITATION_EMAIL_MISMATCH,
                }),
              },
            ],
          );
        },
      });
    });
    await screen.findByText("Sign in with the invited email address.");
    expect(screen.queryByText("secret recipient")).toBeNull();
    expect(
      (
        screen.getByRole("button", {
          name: "Accept invitation",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });

  it("requires explicit acceptance by the nominated owner", async () => {
    const user = userEvent.setup();
    const accept = rs.fn(
      (_request: { organizationId: string; transferId: string }) => ({
        organization: { ...org, role: Role.OWNER },
      }),
    );
    mount(<OwnershipAcceptancePage organizationId={org.id} />, (router) => {
      router.service(AccountService, { getSession: () => session });
      router.service(OrganizationService, {
        getOrganization: () => ({ organization: org }),
      });
      router.service(OwnershipService, {
        getOwnershipTransfer: () => ({
          transfer: {
            id: "transfer",
            toUserId: session.userId,
            state: LifecycleState.PENDING,
            expiresAt: future(),
          },
        }),
        acceptOwnership: accept,
      });
    });
    const button = await screen.findByRole("button", {
      name: "Accept ownership",
    });
    await waitFor(() =>
      expect((button as HTMLButtonElement).disabled).toBe(false),
    );
    expect(accept).not.toHaveBeenCalled();
    await user.click(button);
    await screen.findByText("Ownership accepted. You are now the owner.");
    expect(accept.mock.calls[0][0]).toMatchObject({
      organizationId: org.id,
      transferId: "transfer",
    });
  });
});

describe("invitation acceptance review", () => {
  afterEach(() => window.history.replaceState(null, "", "/"));
  const pendingInvitation = (
    overrides: Record<string, unknown> = {},
  ): Record<string, unknown> => ({
    id: "invite-one",
    organizationName: "Acme Studio",
    email: session.email,
    state: LifecycleState.PENDING,
    expiresAt: future(),
    role: Role.MEMBER,
    ...overrides,
  });
  const rpcError = (code: Code, reason?: ErrorReason, requestId?: string) =>
    new ConnectError("secret recipient", code, undefined, [
      {
        desc: ErrorDetailSchema,
        value: create(ErrorDetailSchema, { reason, requestId }),
      },
    ]);
  const acceptButton = () =>
    screen.getByRole("button", {
      name: "Accept invitation",
    }) as HTMLButtonElement;

  it("renders the manual entry form with a focused labeled input and review-only submit", async () => {
    const user = userEvent.setup();
    const get = rs.fn((_request: { token: string }) => ({
      invitation: pendingInvitation(),
    }));
    const accept = rs.fn();
    mount(<InvitationPage />, (router) => {
      router.service(AccountService, { getSession: () => session });
      router.service(InvitationService, {
        getInvitation: get,
        acceptInvitation: accept,
      });
    });
    expect(
      screen.getByRole("heading", { level: 1, name: "Join an organization" }),
    ).toBeTruthy();
    expect(
      screen.getByText(
        "Use the account with the invited, verified email address.",
      ),
    ).toBeTruthy();
    const field = screen.getByLabelText(
      "Invitation link or code",
    ) as HTMLInputElement;
    expect(document.activeElement).toBe(field);
    expect(field.getAttribute("autocomplete")).toBe("off");
    expect(field.getAttribute("spellcheck")).toBe("false");
    expect(
      screen.getByText("Paste the invitation link or code from your email."),
    ).toBeTruthy();
    await screen.findByText(`Signed in as ${session.email}`);
    await user.type(
      field,
      "https://router.example/invitation?token=fixture-code{Enter}",
    );
    await screen.findByRole("heading", { level: 2, name: "Acme Studio" });
    expect(get.mock.calls[0][0]).toMatchObject({ token: "fixture-code" });
    expect(accept).not.toHaveBeenCalled();
    expect(window.location.search).toBe("");
    expect(document.activeElement).not.toBe(acceptButton());
  });

  it("rejects empty and whitespace-only manual input without any request", async () => {
    const user = userEvent.setup();
    const get = rs.fn();
    const accept = rs.fn();
    mount(<InvitationPage />, (router) => {
      router.service(AccountService, { getSession: () => session });
      router.service(InvitationService, {
        getInvitation: get,
        acceptInvitation: accept,
      });
    });
    const field = screen.getByLabelText("Invitation link or code");
    await user.click(screen.getByRole("button", { name: "Review invitation" }));
    await screen.findByText(
      "Enter the invitation link or code from your email.",
    );
    expect(field.getAttribute("aria-invalid")).toBe("true");
    await user.type(field, "   {Enter}");
    expect(get).not.toHaveBeenCalled();
    expect(accept).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(field);
  });

  it("uses the raw code when a pasted value is not a link", async () => {
    const user = userEvent.setup();
    const get = rs.fn((_request: { token: string }) => ({
      invitation: pendingInvitation(),
    }));
    mount(<InvitationPage />, (router) => {
      router.service(AccountService, { getSession: () => session });
      router.service(InvitationService, { getInvitation: get });
    });
    await user.type(
      screen.getByLabelText("Invitation link or code"),
      "fixture-code{Enter}",
    );
    await screen.findByRole("heading", { level: 2, name: "Acme Studio" });
    expect(get.mock.calls[0][0]).toMatchObject({ token: "fixture-code" });
  });

  it("renders the pending review card in order and accepts only on explicit click", async () => {
    const user = userEvent.setup();
    const accept = rs.fn(
      (_request: { token?: string; invitationId?: string }) => ({
        organization: { id: "org-new", name: "Acme Studio", role: Role.MEMBER },
      }),
    );
    const accepted = rs.fn();
    mount(
      <InvitationPage token="invitation-token" onAccepted={accepted} />,
      (router) => {
        router.service(AccountService, { getSession: () => session });
        router.service(InvitationService, {
          getInvitation: () => ({ invitation: pendingInvitation() }),
          acceptInvitation: accept,
        });
      },
    );
    expect(
      screen.getByRole("heading", { level: 1, name: "Join an organization" }),
    ).toBe(document.activeElement);
    await screen.findByRole("heading", { level: 2, name: "Acme Studio" });
    await waitFor(() => expect(acceptButton().disabled).toBe(false));
    expect(document.activeElement).not.toBe(acceptButton());
    expect(screen.getByText("Pending")).toBeTruthy();
    expect(
      screen.getByText("Review your invitation before joining."),
    ).toBeTruthy();
    expect(
      Array.from(document.querySelectorAll("dt")).map((n) => n.textContent),
    ).toEqual(["Invited email", "Role", "Expires"]);
    expect(
      Array.from(document.querySelectorAll("dd")).map((n) => n.textContent)[1],
    ).toBe("Member");
    expect(
      screen.getByText(
        "Joining does not copy provider credentials or settings from other organizations.",
      ),
    ).toBeTruthy();
    expect(screen.getByText("You join only after you accept.")).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: /decline|resend/i }),
    ).toBeNull();
    expect(accept).not.toHaveBeenCalled();
    await user.click(acceptButton());
    await screen.findByRole("heading", {
      level: 1,
      name: "Invitation accepted",
    });
    expect(accept).toHaveBeenCalledTimes(1);
    expect(accept.mock.calls[0][0]).toMatchObject({
      token: "invitation-token",
    });
    expect(accept.mock.calls[0][0].invitationId ?? "").toBe("");
    expect(screen.getByRole("status").textContent).toBe(
      "You joined Acme Studio.",
    );
    expect(
      screen
        .getByRole("link", { name: "Open organization" })
        .getAttribute("href"),
    ).toBe("/organizations/org-new/overview");
    expect(accepted).toHaveBeenCalledTimes(1);
    expect(document.querySelector(".invitation-identity")).toBeNull();
  });

  it("does not treat a response without an organization as success", async () => {
    const user = userEvent.setup();
    const accept = rs.fn(() => ({}));
    const accepted = rs.fn();
    mount(
      <InvitationPage token="invitation-token" onAccepted={accepted} />,
      (router) => {
        router.service(AccountService, { getSession: () => session });
        router.service(InvitationService, {
          getInvitation: () => ({ invitation: pendingInvitation() }),
          acceptInvitation: accept,
        });
      },
    );
    await waitFor(async () => {
      await screen.findByRole("heading", { level: 2, name: "Acme Studio" });
      expect(acceptButton().disabled).toBe(false);
    });
    await user.click(acceptButton());
    await waitFor(() => expect(accept).toHaveBeenCalledTimes(1));
    expect(
      screen.getByRole("heading", { level: 1, name: "Join an organization" }),
    ).toBeTruthy();
    expect(screen.queryByText(/You joined/)).toBeNull();
    expect(
      screen.queryByRole("link", { name: "Open organization" }),
    ).toBeNull();
    expect(accepted).not.toHaveBeenCalled();
  });

  it("omits the identity strip without session data and never shows a placeholder", async () => {
    mount(<InvitationPage token="invitation-token" />, (router) => {
      router.service(AccountService, {
        getSession: () => {
          throw rpcError(Code.Unavailable);
        },
      });
      router.service(InvitationService, {
        getInvitation: () => ({ invitation: pendingInvitation() }),
      });
    });
    await screen.findByText(
      "The service is unavailable. No change has been confirmed.",
    );
    expect(screen.queryByText(/Signed in as/)).toBeNull();
    expect(screen.queryByText("…")).toBeNull();
    expect(screen.queryByRole("heading", { level: 2 })).toBeNull();
    expect(acceptButton().disabled).toBe(true);
  });

  it("shows Not available for a missing expiry and keeps acceptance blocked", async () => {
    const accept = rs.fn();
    mount(<InvitationPage token="invitation-token" />, (router) => {
      router.service(AccountService, { getSession: () => session });
      router.service(InvitationService, {
        getInvitation: () => ({
          invitation: pendingInvitation({ expiresAt: undefined }),
        }),
        acceptInvitation: accept,
      });
    });
    await screen.findByText("Not available");
    expect(acceptButton().disabled).toBe(true);
    expect(accept).not.toHaveBeenCalled();
  });

  it("maps unknown roles and statuses without inventing authority", async () => {
    mount(<InvitationPage token="invitation-token" />, (router) => {
      router.service(AccountService, { getSession: () => session });
      router.service(InvitationService, {
        getInvitation: () => ({
          invitation: pendingInvitation({
            role: Role.OWNER,
            state: LifecycleState.UNSPECIFIED,
          }),
        }),
      });
    });
    await screen.findByText("Invitation status is unavailable.");
    expect(screen.getByText("Unknown")).toBeTruthy();
    expect(screen.queryByText("Pending")).toBeNull();
    expect(acceptButton().disabled).toBe(true);
  });

  it("renders the failed terminal state and a pending invitation past its deadline as polite status", async () => {
    mount(<InvitationPage token="invitation-token" />, (router) => {
      router.service(AccountService, { getSession: () => session });
      router.service(InvitationService, {
        getInvitation: () => ({
          invitation: pendingInvitation({
            state: LifecycleState.FAILED,
          }),
        }),
      });
    });
    const status = await screen.findByText(
      "Invitation delivery failed and this link is invalid. Ask an administrator to resend it.",
    );
    expect(status.getAttribute("role")).toBe("status");
    expect(acceptButton().disabled).toBe(true);
    expect(screen.queryByText("You join only after you accept.")).toBeNull();
  });

  it("treats a pending invitation whose deadline has passed as expired", async () => {
    mount(<InvitationPage token="invitation-token" />, (router) => {
      router.service(AccountService, { getSession: () => session });
      router.service(InvitationService, {
        getInvitation: () => ({
          invitation: pendingInvitation({
            expiresAt: timestampFromDate(new Date(Date.now() - 1000)),
          }),
        }),
      });
    });
    const status = await screen.findByText(
      "This invitation has expired. Ask an administrator for a new invitation.",
    );
    expect(status.getAttribute("role")).toBe("status");
    expect(screen.queryByText("Pending")).toBeNull();
    expect(acceptButton().disabled).toBe(true);
  });

  it("shows a reserved skeleton and one polite loading message", async () => {
    mount(<InvitationPage token="invitation-token" />, (router) => {
      router.service(AccountService, { getSession: () => session });
      router.service(InvitationService, {
        getInvitation: () => ({ invitation: pendingInvitation() }),
      });
    });
    const status = await screen.findByText("Loading invitation…");
    expect(status.getAttribute("role")).toBe("status");
    expect(
      document
        .querySelector(".invitation-skeleton")
        ?.getAttribute("aria-hidden"),
    ).toBe("true");
    expect(acceptButton().disabled).toBe(true);
    await screen.findByRole("heading", { level: 2, name: "Acme Studio" });
    expect(screen.queryByText("Loading invitation…")).toBeNull();
    expect(document.querySelector(".invitation-skeleton")).toBeNull();
  });

  describe("invitation_id lookup", () => {
    const listing =
      (
        pages: Array<{ invitations: Record<string, unknown>[]; next?: string }>,
        calls: Array<{ pageSize?: number; pageToken?: string }> = [],
      ) =>
      (request: { page?: { pageSize: number; pageToken: string } }) => {
        calls.push({
          pageSize: request.page?.pageSize,
          pageToken: request.page?.pageToken,
        });
        const index = request.page?.pageToken
          ? pages.findIndex(
              (_, at) =>
                at > 0 && pages[at - 1].next === request.page?.pageToken,
            )
          : 0;
        const page = pages[index];
        return {
          invitations: page.invitations,
          page: { nextPageToken: page.next ?? "" },
        };
      };

    it("reports not found so far, pages with the opaque token and accepts by invitation id only", async () => {
      window.history.replaceState(
        null,
        "",
        "/invitation?invitation_id=invite-two",
      );
      const user = userEvent.setup();
      const calls: Array<{ pageSize?: number; pageToken?: string }> = [];
      const accept = rs.fn(
        (_request: { token?: string; invitationId?: string }) => ({
          organization: {
            id: "org-new",
            name: "Acme Studio",
            role: Role.MEMBER,
          },
        }),
      );
      mount(<InvitationPage />, (router) => {
        router.service(AccountService, { getSession: () => session });
        router.service(InvitationService, {
          listMyInvitations: listing(
            [
              { invitations: [], next: "page-two" },
              { invitations: [pendingInvitation({ id: "invite-two" })] },
            ],
            calls,
          ),
          acceptInvitation: accept,
        });
      });
      await screen.findByText(
        "This invitation has not been found in the invitations checked so far.",
      );
      expect(
        screen.queryByText(/no longer pending or is unavailable/),
      ).toBeNull();
      const more = screen.getByRole("button", {
        name: "Check more invitations",
      });
      expect(document.activeElement).not.toBe(more);
      await user.click(more);
      await screen.findByRole("heading", { level: 2, name: "Acme Studio" });
      expect(calls).toEqual([
        { pageSize: 100, pageToken: "" },
        { pageSize: 100, pageToken: "page-two" },
      ]);
      expect(
        screen.queryByRole("button", { name: "Check more invitations" }),
      ).toBeNull();
      await waitFor(() => expect(acceptButton().disabled).toBe(false));
      await user.click(acceptButton());
      await screen.findByRole("heading", {
        level: 1,
        name: "Invitation accepted",
      });
      expect(accept.mock.calls[0][0]).toMatchObject({
        invitationId: "invite-two",
      });
      expect(accept.mock.calls[0][0].token ?? "").toBe("");
    });

    it("announces paging progress and disables the control while fetching", async () => {
      window.history.replaceState(
        null,
        "",
        "/invitation?invitation_id=invite-two",
      );
      const user = userEvent.setup();
      let release: () => void = () => undefined;
      const gate = new Promise<void>((resolve) => {
        release = resolve;
      });
      mount(<InvitationPage />, (router) => {
        router.service(AccountService, { getSession: () => session });
        router.service(InvitationService, {
          listMyInvitations: async (request: {
            page?: { pageToken: string };
          }) => {
            if (!request.page?.pageToken)
              return { invitations: [], page: { nextPageToken: "page-two" } };
            await gate;
            return {
              invitations: [],
              page: { nextPageToken: "page-three" },
            };
          },
        });
      });
      const more = await screen.findByRole("button", {
        name: "Check more invitations",
      });
      more.focus();
      await user.click(more);
      const status = await screen.findByText("Checking more invitations…");
      expect(status.getAttribute("role")).toBe("status");
      expect((more as HTMLButtonElement).disabled).toBe(true);
      release();
      await waitFor(() =>
        expect((more as HTMLButtonElement).disabled).toBe(false),
      );
      await waitFor(() => expect(document.activeElement).toBe(more));
      expect(screen.queryByText("Checking more invitations…")).toBeNull();
    });

    it("claims exhaustion only after the last page and never invents a state", async () => {
      window.history.replaceState(
        null,
        "",
        "/invitation?invitation_id=missing",
      );
      const accept = rs.fn();
      mount(<InvitationPage />, (router) => {
        router.service(AccountService, { getSession: () => session });
        router.service(InvitationService, {
          listMyInvitations: listing([
            { invitations: [pendingInvitation({ id: "other" })] },
          ]),
          acceptInvitation: accept,
        });
      });
      const alert = await screen.findByText(
        "This invitation is no longer pending or is unavailable to your account. Open the original email link for its current status.",
      );
      expect(alert.getAttribute("role")).toBe("alert");
      expect(
        screen.queryByText(/not been found in the invitations checked so far/),
      ).toBeNull();
      expect(
        screen.queryByRole("button", { name: "Check more invitations" }),
      ).toBeNull();
      expect(screen.queryByRole("heading", { level: 2 })).toBeNull();
      expect(acceptButton().disabled).toBe(true);
      expect(accept).not.toHaveBeenCalled();
    });

    it("keeps a retry path on a failed page and never claims exhaustion", async () => {
      window.history.replaceState(
        null,
        "",
        "/invitation?invitation_id=invite-two",
      );
      const user = userEvent.setup();
      let failing = true;
      mount(<InvitationPage />, (router) => {
        router.service(AccountService, { getSession: () => session });
        router.service(InvitationService, {
          listMyInvitations: (request: { page?: { pageToken: string } }) => {
            if (!request.page?.pageToken)
              return { invitations: [], page: { nextPageToken: "page-two" } };
            if (failing)
              throw rpcError(Code.Unavailable, undefined, "req-page");
            return { invitations: [pendingInvitation({ id: "invite-two" })] };
          },
        });
      });
      await user.click(
        await screen.findByRole("button", { name: "Check more invitations" }),
      );
      await screen.findByText("Request ID: req-page");
      expect(
        screen.queryByText(/no longer pending or is unavailable/),
      ).toBeNull();
      expect(acceptButton().disabled).toBe(true);
      failing = false;
      await user.click(screen.getByRole("button", { name: "Try again" }));
      await screen.findByRole("heading", { level: 2, name: "Acme Studio" });
    });

    it("lets a non-empty token win over invitation_id", async () => {
      window.history.replaceState(
        null,
        "",
        "/invitation?invitation_id=invite-two",
      );
      const user = userEvent.setup();
      const list = rs.fn();
      const accept = rs.fn(
        (_request: { token?: string; invitationId?: string }) => ({
          organization: {
            id: "org-new",
            name: "Acme Studio",
            role: Role.MEMBER,
          },
        }),
      );
      mount(<InvitationPage token="invitation-token" />, (router) => {
        router.service(AccountService, { getSession: () => session });
        router.service(InvitationService, {
          getInvitation: () => ({ invitation: pendingInvitation() }),
          listMyInvitations: list,
          acceptInvitation: accept,
        });
      });
      await screen.findByRole("heading", { level: 2, name: "Acme Studio" });
      await waitFor(() => expect(acceptButton().disabled).toBe(false));
      await user.click(acceptButton());
      await screen.findByRole("heading", {
        level: 1,
        name: "Invitation accepted",
      });
      expect(list).not.toHaveBeenCalled();
      expect(accept.mock.calls[0][0].token).toBe("invitation-token");
      expect(accept.mock.calls[0][0].invitationId ?? "").toBe("");
    });
  });

  it("drops authorized detail when a later read reports an email mismatch", async () => {
    const user = userEvent.setup();
    let mismatch = false;
    mount(<InvitationPage token="invitation-token" />, (router) => {
      router.service(AccountService, { getSession: () => session });
      router.service(InvitationService, {
        getInvitation: () => {
          if (mismatch)
            throw rpcError(
              Code.PermissionDenied,
              ErrorReason.INVITATION_EMAIL_MISMATCH,
              "req-mismatch",
            );
          return { invitation: pendingInvitation() };
        },
        acceptInvitation: () => {
          throw rpcError(Code.Unavailable);
        },
      });
    });
    await screen.findByRole("heading", { level: 2, name: "Acme Studio" });
    await waitFor(() => expect(acceptButton().disabled).toBe(false));
    mismatch = true;
    await user.click(acceptButton());
    await screen.findByText("Sign in with the invited email address.");
    await screen.findByText("Request ID: req-mismatch");
    expect(screen.queryByRole("heading", { level: 2 })).toBeNull();
    expect(screen.queryByText("Acme Studio")).toBeNull();
    expect(screen.queryByText("secret recipient")).toBeNull();
    expect(screen.queryByText("Member")).toBeNull();
    expect(
      screen.getByRole("link", { name: "Sign in again" }).getAttribute("href"),
    ).toContain("/auth/login?return_to=");
    expect(acceptButton().disabled).toBe(true);
  });

  it.each([
    [
      Code.Unauthenticated,
      "Your session has expired. Sign in again, then review and submit the action.",
    ],
    [
      Code.PermissionDenied,
      "Your permissions changed or this action is not available to your account.",
    ],
    [Code.NotFound, "This link or code is invalid or no longer available."],
    [Code.InvalidArgument, "Check the entered value and try again."],
    [
      Code.Unavailable,
      "The service is unavailable. No change has been confirmed.",
    ],
  ])(
    "shows safe copy and only the typed request id for code %s",
    async (code, message) => {
      const accept = rs.fn();
      mount(<InvitationPage token="invitation-token" />, (router) => {
        router.service(AccountService, { getSession: () => session });
        router.service(InvitationService, {
          getInvitation: () => {
            throw rpcError(code, undefined, "req-safe");
          },
          acceptInvitation: accept,
        });
      });
      await screen.findByText(message);
      expect(screen.getByText("Request ID: req-safe")).toBeTruthy();
      expect(screen.queryByText("secret recipient")).toBeNull();
      expect(acceptButton().disabled).toBe(true);
      expect(accept).not.toHaveBeenCalled();
    },
  );

  it("clears detail when the session is lost and never replays the mutation", async () => {
    const user = userEvent.setup();
    let signedIn = true;
    const accept = rs.fn(() => {
      throw rpcError(Code.Unavailable);
    });
    const { cache } = mount(
      <InvitationPage token="invitation-token" />,
      (router) => {
        router.service(AccountService, {
          getSession: () => {
            if (!signedIn) throw rpcError(Code.Unauthenticated);
            return session;
          },
        });
        router.service(InvitationService, {
          getInvitation: () => ({ invitation: pendingInvitation() }),
          acceptInvitation: accept,
        });
      },
    );
    await screen.findByRole("heading", { level: 2, name: "Acme Studio" });
    await waitFor(() => expect(acceptButton().disabled).toBe(false));
    await user.click(acceptButton());
    await waitFor(() => expect(accept).toHaveBeenCalledTimes(1));
    signedIn = false;
    await cache.refetchQueries();
    await screen.findByText(/Your session has expired/);
    expect(screen.queryByRole("heading", { level: 2 })).toBeNull();
    expect(screen.queryByText(/Signed in as/)).toBeNull();
    expect(acceptButton().disabled).toBe(true);
    expect(accept).toHaveBeenCalledTimes(1);
  });

  it("drops detail loaded for the previous account when the session email changes", async () => {
    let email = session.email;
    const { cache } = mount(
      <InvitationPage token="invitation-token" />,
      (router) => {
        router.service(AccountService, {
          getSession: () => ({ ...session, email }),
        });
        router.service(InvitationService, {
          getInvitation: () => {
            if (email !== session.email)
              throw rpcError(
                Code.PermissionDenied,
                ErrorReason.INVITATION_EMAIL_MISMATCH,
              );
            return { invitation: pendingInvitation() };
          },
        });
      },
    );
    await screen.findByRole("heading", { level: 2, name: "Acme Studio" });
    email = "other@example.test";
    await cache.refetchQueries({
      queryKey: createConnectQueryKey({
        schema: AccountService.method.getSession,
        cardinality: undefined,
      }),
    });
    await screen.findByText("Sign in with the invited email address.");
    expect(screen.queryByText("Acme Studio")).toBeNull();
    expect(screen.getByText("Signed in as other@example.test")).toBeTruthy();
  });

  it("scopes the invitation styles away from other account screens", async () => {
    const { container } = mount(
      <OwnershipAcceptancePage organizationId={org.id} />,
      (router) => {
        router.service(AccountService, { getSession: () => session });
        router.service(OrganizationService, {
          getOrganization: () => ({ organization: org }),
        });
        router.service(OwnershipService, {
          getOwnershipTransfer: () => ({}),
        });
      },
    );
    await screen.findByRole("heading", { level: 1 });
    expect(container.querySelector(".invitation-surface")).toBeNull();
    expect(container.querySelector(".invitation-card")).toBeNull();
  });
});

describe("ownership acceptance review", () => {
  afterEach(() => window.history.replaceState(null, "", "/"));
  const acme = { id: "org-a", name: "Acme Studio", role: Role.MEMBER };
  const nominated = (overrides: Record<string, unknown> = {}) => ({
    id: "t1",
    toUserId: session.userId,
    state: LifecycleState.PENDING,
    expiresAt: future(),
    ...overrides,
  });
  type Handlers = {
    transfer?: () => unknown;
    accept?: (request: {
      organizationId: string;
      transferId: string;
    }) => unknown;
    session?: () => unknown;
    organization?: () => unknown;
  };
  function mountOwnership(
    handlers: Handlers,
    props: { onAccepted?: (organization: unknown) => void } = {},
  ) {
    return mount(
      <OwnershipAcceptancePage organizationId={acme.id} {...props} />,
      (router) => {
        router.service(AccountService, {
          getSession: (handlers.session ?? (() => session)) as never,
        });
        router.service(OrganizationService, {
          getOrganization: (handlers.organization ??
            (() => ({ organization: acme }))) as never,
        });
        router.service(OwnershipService, {
          getOwnershipTransfer: (handlers.transfer ??
            (() => ({ transfer: nominated() }))) as never,
          acceptOwnership: (handlers.accept ?? (() => ({}))) as never,
        });
      },
    );
  }
  const acceptButton = () =>
    screen.getByRole("button", {
      name: "Accept ownership",
    }) as HTMLButtonElement;
  const rpcError = (code: Code, reason?: ErrorReason, requestId?: string) =>
    new ConnectError("private detail", code, undefined, [
      {
        desc: ErrorDetailSchema,
        value: create(ErrorDetailSchema, { reason, requestId }),
      },
    ]);

  it("renders the review card in the specified order with the heading focused", async () => {
    const accept = rs.fn();
    mountOwnership({ accept });
    expect(document.activeElement).toBe(
      screen.getByRole("heading", {
        level: 1,
        name: "Accept organization ownership",
      }),
    );
    await screen.findByRole("heading", { level: 2, name: "Acme Studio" });
    await waitFor(() => expect(acceptButton().disabled).toBe(false));
    expect(document.activeElement).not.toBe(acceptButton());
    expect(
      screen.getByText("Review the change before accepting."),
    ).toBeTruthy();
    expect(screen.getByText("Organization")).toBeTruthy();
    expect(screen.getByText("Pending acceptance")).toBeTruthy();
    expect(
      screen.getByRole("heading", { level: 3, name: "What will change" }),
    ).toBeTruthy();
    const terms = Array.from(document.querySelectorAll("dt")).map(
      (node) => node.textContent,
    );
    const values = Array.from(document.querySelectorAll("dd")).map(
      (node) => node.textContent,
    );
    expect(terms).toEqual(["You", "Previous owner", "Expires"]);
    expect(values.slice(0, 2)).toEqual([
      "Become owner",
      "Become administrator",
    ]);
    expect(values[2]).toMatch(/\d{4}|\d{1,2}\/\d{1,2}/);
    expect(
      screen.getByText("Transfer requests are valid for 7 days."),
    ).toBeTruthy();
    expect(
      screen.getByText("Ownership does not change until you accept."),
    ).toBeTruthy();
    expect(
      screen.getByText("Leaving this page does not cancel the transfer."),
    ).toBeTruthy();
    expect(
      screen
        .getByRole("link", { name: "Back to organization" })
        .getAttribute("href"),
    ).toBe("/organizations/org-a/overview");
    expect(accept).not.toHaveBeenCalled();
  });

  it("shows a hidden skeleton and one polite loading message before data arrives", async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const { container } = mountOwnership({
      transfer: async () => {
        await gate;
        return { transfer: nominated() };
      },
    });
    expect(screen.getByText("Loading ownership transfer…")).toBeTruthy();
    expect(
      screen.getByText("Loading ownership transfer…").getAttribute("role"),
    ).toBe("status");
    expect(
      container
        .querySelector(".ownership-skeleton")
        ?.getAttribute("aria-hidden"),
    ).toBe("true");
    expect(screen.queryByText("Acme Studio")).toBeNull();
    expect(screen.queryByText("Become owner")).toBeNull();
    release();
    await screen.findByText("Acme Studio");
    expect(container.querySelector(".ownership-skeleton")).toBeNull();
    expect(screen.queryByText("Loading ownership transfer…")).toBeNull();
  });

  it("sends exactly the reviewed ids once on explicit click and keeps success after empty polls", async () => {
    const user = userEvent.setup();
    let confirmed = false;
    const accept = rs.fn(
      (_request: { organizationId: string; transferId: string }) => {
        confirmed = true;
        return { organization: { ...acme, role: Role.OWNER } };
      },
    );
    const onAccepted = rs.fn();
    const { cache } = mountOwnership(
      {
        accept,
        transfer: () => (confirmed ? {} : { transfer: nominated() }),
      },
      { onAccepted },
    );
    await screen.findByText("Acme Studio");
    await waitFor(() => expect(acceptButton().disabled).toBe(false));
    await user.click(acceptButton());
    await screen.findByText("Ownership accepted. You are now the owner.");
    expect(accept).toHaveBeenCalledTimes(1);
    expect(accept.mock.calls[0][0]).toMatchObject({
      organizationId: "org-a",
      transferId: "t1",
    });
    expect(onAccepted).toHaveBeenCalledTimes(1);
    expect(
      screen
        .getByText("Ownership accepted. You are now the owner.")
        .getAttribute("role"),
    ).toBe("status");
    await cache.refetchQueries();
    await waitFor(() => expect(cache.isFetching()).toBe(0));
    expect(
      screen.getByText("Ownership accepted. You are now the owner."),
    ).toBeTruthy();
    expect(
      screen.queryByText("No pending ownership transfer is available."),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Accept ownership" }),
    ).toBeNull();
    expect(
      screen.getByRole("link", { name: "Back to organization" }),
    ).toBeTruthy();
    expect(accept).toHaveBeenCalledTimes(1);
  });

  it("keeps the confirmed result when later polls fail with NotFound", async () => {
    const user = userEvent.setup();
    let confirmed = false;
    const { cache } = mountOwnership({
      accept: () => {
        confirmed = true;
        return { organization: { ...acme, role: Role.OWNER } };
      },
      transfer: () => {
        if (confirmed) throw rpcError(Code.NotFound);
        return { transfer: nominated() };
      },
    });
    await screen.findByText("Acme Studio");
    await waitFor(() => expect(acceptButton().disabled).toBe(false));
    await user.click(acceptButton());
    await screen.findByText("Ownership accepted. You are now the owner.");
    await cache.refetchQueries();
    await waitFor(() => expect(cache.isFetching()).toBe(0));
    expect(
      screen.getByText("Ownership accepted. You are now the owner."),
    ).toBeTruthy();
    expect(
      screen.queryByText(
        "This link or code is invalid or no longer available.",
      ),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Accept ownership" }),
    ).toBeNull();
  });

  it("blocks a mismatching transfer_id without accepting the newer nomination", async () => {
    window.history.replaceState(null, "", "/ownership?transfer_id=old");
    const accept = rs.fn();
    mountOwnership({
      transfer: () => ({ transfer: nominated({ id: "t2" }) }),
      accept,
    });
    await screen.findByText(
      "This request was replaced by a newer ownership transfer. Ask the owner for the current link.",
    );
    expect(acceptButton().disabled).toBe(true);
    expect(screen.queryByText("Become owner")).toBeNull();
    expect(screen.queryByText("Expires")).toBeNull();
    expect(accept).not.toHaveBeenCalled();
  });

  it("accepts the current nomination when transfer_id is absent or matches", async () => {
    window.history.replaceState(null, "", "/ownership?transfer_id=t1");
    const user = userEvent.setup();
    const accept = rs.fn(() => ({ organization: acme }));
    mountOwnership({ accept });
    await screen.findByText("Acme Studio");
    await waitFor(() => expect(acceptButton().disabled).toBe(false));
    await user.click(acceptButton());
    await waitFor(() => expect(accept).toHaveBeenCalledTimes(1));
  });

  it("disables acceptance at the local deadline and announces expiry once", async () => {
    const accept = rs.fn();
    mountOwnership({
      transfer: () => ({
        transfer: nominated({
          expiresAt: timestampFromDate(new Date(Date.now() + 1500)),
        }),
      }),
      accept,
    });
    await screen.findByText("Acme Studio");
    await waitFor(() => expect(acceptButton().disabled).toBe(false));
    await waitFor(() => expect(acceptButton().disabled).toBe(true), {
      timeout: 4000,
    });
    expect(
      screen.getAllByText("This ownership transfer has expired."),
    ).toHaveLength(1);
    expect(
      screen
        .getByText("This ownership transfer has expired.")
        .getAttribute("role"),
    ).toBe("status");
    expect(screen.queryByText("Pending acceptance")).toBeNull();
    expect(accept).not.toHaveBeenCalled();
  });

  it("treats a missing deadline as not available and not acceptable", async () => {
    mountOwnership({
      transfer: () => ({ transfer: nominated({ expiresAt: undefined }) }),
    });
    await screen.findByText("Acme Studio");
    expect(screen.getByText("Not available")).toBeTruthy();
    expect(acceptButton().disabled).toBe(true);
    expect(screen.queryByText("Pending acceptance")).toBeNull();
  });

  it("explains a wrong nominee without enabling acceptance", async () => {
    const accept = rs.fn();
    mountOwnership({
      transfer: () => ({ transfer: nominated({ toUserId: "someone-else" }) }),
      accept,
    });
    await screen.findByText(
      "Only the nominated member can accept this transfer. Sign in with the nominated account.",
    );
    expect(acceptButton().disabled).toBe(true);
    expect(screen.queryByText("Pending acceptance")).toBeNull();
    expect(accept).not.toHaveBeenCalled();
  });

  it.each([
    [
      LifecycleState.ACCEPTED,
      "This ownership transfer has already been accepted.",
    ],
    [LifecycleState.CANCELED, "This ownership transfer was canceled."],
    [LifecycleState.EXPIRED, "This ownership transfer has expired."],
    [
      LifecycleState.SUPERSEDED,
      "This ownership transfer was replaced by a newer request.",
    ],
    [LifecycleState.FAILED, "This ownership transfer is no longer available."],
    [LifecycleState.UNSPECIFIED, "Ownership transfer status is unavailable."],
  ])(
    "renders returned state %s as its own status without an action",
    async (state, message) => {
      const accept = rs.fn();
      mountOwnership({
        transfer: () => ({ transfer: nominated({ state }) }),
        accept,
      });
      const status = await screen.findByText(message);
      expect(status.getAttribute("role")).toBe("status");
      expect(
        screen.queryByRole("button", { name: "Accept ownership" }),
      ).toBeNull();
      expect(screen.queryByText("Become owner")).toBeNull();
      expect(screen.queryByText("Pending acceptance")).toBeNull();
      expect(accept).not.toHaveBeenCalled();
    },
  );

  it("renders a distinct empty lookup and a distinct NotFound without history", async () => {
    const empty = mountOwnership({ transfer: () => ({}) });
    await screen.findByText("No pending ownership transfer is available.");
    expect(
      screen.queryByRole("button", { name: "Accept ownership" }),
    ).toBeNull();
    empty.unmount();
    mountOwnership({
      transfer: () => {
        throw rpcError(Code.NotFound);
      },
    });
    await screen.findByText(
      "This link or code is invalid or no longer available.",
    );
    expect(screen.queryByText(/canceled|expired|accepted/i)).toBeNull();
    expect(
      screen.queryByText("No pending ownership transfer is available."),
    ).toBeNull();
  });

  it("blocks a link without an organization and never queries or accepts", () => {
    const accept = rs.fn();
    const transfer = rs.fn();
    mount(<OwnershipAcceptancePage />, (router) => {
      router.service(AccountService, { getSession: () => session });
      router.service(OwnershipService, {
        getOwnershipTransfer: transfer,
        acceptOwnership: accept,
      });
    });
    expect(
      screen
        .getByText(
          "This link is missing its organization. Ask the current owner for a new link.",
        )
        .getAttribute("role"),
    ).toBe("alert");
    expect(
      screen.queryByRole("link", { name: "Back to organization" }),
    ).toBeNull();
    expect(screen.queryByText("Loading ownership transfer…")).toBeNull();
    expect(transfer).not.toHaveBeenCalled();
    expect(accept).not.toHaveBeenCalled();
  });

  it("shows a safe request id on query errors and retries queries without accepting", async () => {
    const user = userEvent.setup();
    const accept = rs.fn();
    let failures = 1;
    const transfer = rs.fn(() => {
      if (failures-- > 0)
        throw rpcError(Code.Unavailable, undefined, "req-123");
      return { transfer: nominated() };
    });
    mountOwnership({ transfer, accept });
    await screen.findByText(
      "The service is unavailable. No change has been confirmed.",
    );
    expect(screen.getByText("Request ID: req-123")).toBeTruthy();
    expect(screen.queryByText("private detail")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByText("Acme Studio");
    expect(transfer).toHaveBeenCalledTimes(2);
    expect(accept).not.toHaveBeenCalled();
  });

  it("fails closed when a poll loses authorization and offers sign-in", async () => {
    let calls = 0;
    const { cache } = mountOwnership({
      transfer: () => {
        if (calls++ > 0) throw rpcError(Code.Unauthenticated);
        return { transfer: nominated() };
      },
    });
    await screen.findByText("Acme Studio");
    await cache.refetchQueries();
    await screen.findByText(
      "Your session has expired. Sign in again, then review and submit the action.",
    );
    expect(screen.queryByText("Acme Studio")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Accept ownership" }),
    ).toBeNull();
    expect(screen.getByRole("link", { name: "Sign in again" })).toBeTruthy();
  });

  it("reports a failed acceptance without success, refetches and never replays", async () => {
    const user = userEvent.setup();
    const accept = rs.fn(() => {
      throw rpcError(Code.PermissionDenied);
    });
    const transfer = rs.fn(() => ({ transfer: nominated() }));
    mountOwnership({ accept, transfer });
    await screen.findByText("Acme Studio");
    await waitFor(() => expect(acceptButton().disabled).toBe(false));
    await user.click(acceptButton());
    await screen.findByText(
      "Your permissions changed or this action is not available to your account.",
    );
    await waitFor(() => expect(transfer.mock.calls.length).toBeGreaterThan(1));
    expect(accept).toHaveBeenCalledTimes(1);
    expect(
      screen.queryByText("Ownership accepted. You are now the owner."),
    ).toBeNull();
  });

  it("does not refocus or re-announce on transfer polls", async () => {
    const { cache } = mountOwnership({});
    const heading = screen.getByRole("heading", { level: 1 });
    await screen.findByText("Acme Studio");
    await waitFor(() => expect(acceptButton().disabled).toBe(false));
    const status = document.querySelector('[role="status"]');
    const before = status?.textContent;
    await cache.refetchQueries();
    await waitFor(() => expect(cache.isFetching()).toBe(0));
    expect(document.activeElement).toBe(heading);
    expect(document.querySelector('[role="status"]')).toBe(status);
    expect(status?.textContent).toBe(before);
  });

  it("refetches every 15 seconds and a replacement nomination cannot be accepted", async () => {
    window.history.replaceState(null, "", "/ownership?transfer_id=t1");
    rs.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date"] });
    try {
      const accept = rs.fn();
      let current = "t1";
      const transfer = rs.fn(() => ({
        transfer: nominated({ id: current }),
      }));
      mountOwnership({ transfer, accept });
      await rs.advanceTimersByTimeAsync(100);
      await screen.findByText("Acme Studio");
      expect(acceptButton().disabled).toBe(false);
      expect(transfer).toHaveBeenCalledTimes(1);
      current = "t2";
      await rs.advanceTimersByTimeAsync(14000);
      expect(transfer).toHaveBeenCalledTimes(1);
      await rs.advanceTimersByTimeAsync(2000);
      expect(transfer.mock.calls.length).toBeGreaterThan(1);
      await screen.findByText(
        "This request was replaced by a newer ownership transfer. Ask the owner for the current link.",
      );
      expect(acceptButton().disabled).toBe(true);
      expect(accept).not.toHaveBeenCalled();
    } finally {
      rs.useRealTimers();
    }
  });

  it("scopes the review styles away from invitation acceptance", async () => {
    const { container } = mount(
      <InvitationPage token="invitation-token" />,
      (router) => {
        router.service(AccountService, { getSession: () => session });
        router.service(InvitationService, {
          getInvitation: () => ({
            invitation: {
              id: "invite",
              organizationName: "Studio",
              email: session.email,
              state: LifecycleState.PENDING,
              expiresAt: future(),
              role: Role.MEMBER,
            },
          }),
        });
      },
    );
    await screen.findByText("Studio");
    expect(container.querySelector(".ownership-surface")).toBeNull();
    expect(container.querySelector(".ownership-card")).toBeNull();
    expect(container.querySelector(".account-card")).not.toBeNull();
  });
});

describe("account lifecycle races", () => {
  it("blocks deletion until all organization pages have been inspected", async () => {
    const user = userEvent.setup();
    mount(<AccountPage />, (router) => {
      router.service(AccountService, { getSession: () => session });
      router.service(OrganizationService, {
        listOrganizations: (request) =>
          request.page?.pageToken
            ? { organizations: [{ ...org, role: Role.OWNER }] }
            : { organizations: [], page: { nextPageToken: "next" } },
      });
    });
    await screen.findByText(
      "The organization list is incomplete. Account deletion is blocked until all ownership can be checked.",
    );
    expect(
      (
        screen.getByRole("button", {
          name: "Delete my account",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    await user.click(
      screen.getByRole("button", { name: "Check more organizations" }),
    );
    await screen.findByText("These organizations block account deletion:");
    expect(
      (
        screen.getByRole("button", {
          name: "Delete my account",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });

  it("keeps failed logout visible and clears private cache only after server confirmation", async () => {
    const user = userEvent.setup();
    let fail = true;
    const logout = rs.fn(() => {
      if (fail) throw new ConnectError("outage", Code.Unavailable);
      return {};
    });
    const { cache } = mount(<AccountPage />, (router) => {
      router.service(AccountService, { getSession: () => session, logout });
      router.service(OrganizationService, {
        listOrganizations: () => ({ organizations: [] }),
      });
    });
    cache.setQueryData(["private-fixture"], "private");
    await user.click(await screen.findByRole("button", { name: "Sign out" }));
    await screen.findByText(
      "The service is unavailable. No change has been confirmed.",
    );
    expect(screen.queryByRole("heading", { name: "Signed out" })).toBeNull();
    expect(cache.getQueryData(["private-fixture"])).toBe("private");
    fail = false;
    await user.click(screen.getByRole("button", { name: "Sign out" }));
    await screen.findByRole("heading", { name: "Signed out" });
    await waitFor(() =>
      expect(cache.getQueryData(["private-fixture"])).toBeUndefined(),
    );
  });

  it("allows dismissal during deletion and reports the final result on the page", async () => {
    const user = userEvent.setup();
    let finish!: () => void;
    const deletion = rs.fn(
      () =>
        new Promise<Record<string, never>>((resolve) => {
          finish = () => resolve({});
        }),
    );
    mount(<AccountPage />, (router) => {
      router.service(AccountService, {
        getSession: () => session,
        deleteAccount: deletion,
      });
      router.service(OrganizationService, {
        listOrganizations: () => ({ organizations: [] }),
      });
    });
    await screen.findByText("person@example.test");
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "Delete my account",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    await user.click(screen.getByRole("button", { name: "Delete my account" }));
    const dialog = screen.getByRole("dialog");
    await user.click(dialog.querySelector("button.account-danger")!);
    await waitFor(() => expect(deletion).toHaveBeenCalledTimes(1));
    fireEvent(dialog, new Event("cancel", { bubbles: true, cancelable: true }));
    expect(screen.queryByRole("dialog")).toBeNull();
    finish();
    await screen.findByRole("heading", { name: "Account deleted" });
  });
});

describe("onboarding navigation guards", () => {
  function DraftGuard({
    onCreated,
  }: {
    onCreated: (organization: typeof org) => void;
  }) {
    const [dirty, setDirty] = useState(false);
    useEffect(() => {
      const guard = (event: Event) => {
        if (dirty) event.preventDefault();
      };
      window.addEventListener("beforeunload", guard);
      return () => window.removeEventListener("beforeunload", guard);
    }, [dirty]);
    return (
      <DirtyContext value={setDirty}>
        <div onInputCapture={() => setDirty(true)}>
          <OnboardingPage onOrganizationCreated={onCreated} />
        </div>
      </DirtyContext>
    );
  }

  function unloadIsBlocked() {
    const event = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(event);
    return event.defaultPrevented;
  }

  it("removes the discard guard before the success callback can navigate", async () => {
    const user = userEvent.setup();
    const navigation = rs.fn(() => unloadIsBlocked());
    mount(<DraftGuard onCreated={navigation} />, (router) => {
      router.service(OrganizationService, {
        createOrganization: () => ({ organization: org }),
      });
      router.service(InvitationService, {
        listMyInvitations: () => ({ invitations: [] }),
      });
    });
    await user.type(
      screen.getByLabelText("Organization name"),
      "Browser QA Organization",
    );
    expect(unloadIsBlocked()).toBe(true);
    await user.click(
      screen.getByRole("button", { name: "Create organization" }),
    );
    await waitFor(() => expect(navigation).toHaveBeenCalledTimes(1));
    expect(navigation.mock.results[0].value).toBe(false);
    expect(unloadIsBlocked()).toBe(false);
  });

  it("keeps the draft and discard guard when creation fails", async () => {
    const user = userEvent.setup();
    const navigation = rs.fn();
    mount(<DraftGuard onCreated={navigation} />, (router) => {
      router.service(OrganizationService, {
        createOrganization: () => {
          throw new ConnectError("offline", Code.Unavailable);
        },
      });
      router.service(InvitationService, {
        listMyInvitations: () => ({ invitations: [] }),
      });
    });
    await user.type(
      screen.getByLabelText("Organization name"),
      "Keep this draft",
    );
    await user.click(
      screen.getByRole("button", { name: "Create organization" }),
    );
    await screen.findByText(
      "The service is unavailable. No change has been confirmed.",
    );
    expect(
      (screen.getByLabelText("Organization name") as HTMLInputElement).value,
    ).toBe("Keep this draft");
    expect(unloadIsBlocked()).toBe(true);
    expect(navigation).not.toHaveBeenCalled();
  });
});
