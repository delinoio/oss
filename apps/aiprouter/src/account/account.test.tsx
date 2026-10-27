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
} from "@aiprouter/client";
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
        screen.getByRole("heading", { level: 1, name: "Sign in to AIPRouter" }),
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
      expect(link.getAttribute("href")).toBe("/auth/login?return_to=%2F");
      expect(container.querySelectorAll("a")).toHaveLength(1);
      expect(container.querySelectorAll("input, select, button")).toHaveLength(
        0,
      );
      expect(container.querySelector(".sign-in-wordmark")?.textContent).toBe(
        "AIPRouter",
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
      [undefined, "", "%2F"],
      ["/device?code=fictional", "", "%2Fdevice%3Fcode%3Dfictional"],
      [
        undefined,
        "?return_to=/device?code=fictional",
        "%2Fdevice%3Fcode%3Dfictional",
      ],
      ["/device", "?return_to=/other", "%2Fdevice"],
      ["//external.example", "", "%2F"],
      ["https://external.example", "", "%2F"],
      ["/a\\b", "", "%2F"],
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
