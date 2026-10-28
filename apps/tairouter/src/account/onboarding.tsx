// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "../i18n";
import { pageHref } from "../routes";
import { useEffect, useId, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { useMutation } from "@connectrpc/connect-query";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQueryClient } from "@tanstack/react-query";
import { OrganizationService, type Organization } from "@tairouter/client";
import { AccountError, AccountSurface, useMyInvitations } from "./shared";
import { useDirtyForm } from "../api";

const ORGANIZATION_NAME_MAX_LENGTH = 200;

/** Existing five-step setup guide; purely informational on this page. */
const setupSteps = () =>
  [
    translate("chooseOrganization"),
    translate("connectPersonalProviders"),
    translate("configureModelsAndRouting"),
    translate("createApiKey"),
    translate("connectAClient"),
  ] as const;

function invitationsAdded(count: number) {
  return count === 1
    ? translate("oneMoreInvitationLoaded")
    : translate("moreInvitationsLoaded", { value0: count });
}

function InvitationsCard() {
  useLocale();
  const headingId = useId();
  const invitations = useMyInvitations();
  const [announcement, setAnnouncement] = useState("");
  const nextPageFailed = invitations.isFetchNextPageError;
  const firstQueryFailed = invitations.isError && !nextPageFailed;
  const loadNextPage = () => {
    if (invitations.isFetching) return;
    const before = invitations.invitations.length;
    setAnnouncement("");
    void invitations.fetchNextPage().then((result) => {
      if (result.isError) return;
      const after =
        result.data?.pages.reduce(
          (total, page) => total + page.invitations.length,
          0,
        ) ?? before;
      if (after > before) setAnnouncement(invitationsAdded(after - before));
    });
  };
  return (
    <section
      className="account-card onboarding-card"
      aria-labelledby={headingId}
    >
      <h2 id={headingId}>{translate("yourInvitations")}</h2>
      {/* Always mounted so loading and added results are announced politely. */}
      <p role="status" className="onboarding-status">
        {invitations.isPending ? translate("loadingInvitations") : announcement}
      </p>
      {invitations.isPending && (
        <div className="onboarding-skeleton" aria-hidden="true">
          <span className="onboarding-skeleton-line" />
          <span className="onboarding-skeleton-line" />
        </div>
      )}
      <AccountError
        error={invitations.error}
        retry={() => {
          if (invitations.isFetching) return;
          if (nextPageFailed) void invitations.fetchNextPage();
          else void invitations.refetch();
        }}
      />
      {invitations.invitations.length > 0 && (
        <ul className="onboarding-invitations">
          {invitations.invitations.map((invitation) => (
            <li key={invitation.id}>
              <strong>{invitation.organizationName}</strong>
              <a
                className="onboarding-review"
                aria-label={translate("reviewInvitationFor", {
                  value0: invitation.organizationName,
                })}
                href={`/invitation?invitation_id=${encodeURIComponent(invitation.id)}`}
              >
                {translate("reviewInvitation")}
              </a>
            </li>
          ))}
        </ul>
      )}
      {invitations.isSuccess && invitations.invitations.length === 0 && (
        <p className="onboarding-empty">{translate("noPendingInvitations")}</p>
      )}
      {invitations.hasNextPage && !firstQueryFailed && !nextPageFailed && (
        <button
          type="button"
          className="onboarding-more"
          disabled={invitations.isFetching}
          onClick={loadNextPage}
        >
          {invitations.isFetching
            ? translate("loadingMoreInvitations")
            : translate("loadMoreInvitations")}
        </button>
      )}
      <div className="onboarding-manual">
        <p>{translate("openTheInvitationLinkFromYourEmail")}</p>
        <a className="onboarding-manual-link" href="/invitation">
          {translate("enterAnInvitation")}
        </a>
      </div>
    </section>
  );
}

function CreateOrganizationCard({
  onOrganizationCreated,
  onCreated,
}: {
  onOrganizationCreated?: (organization: Organization) => void;
  onCreated: (organization: Organization) => void;
}) {
  useLocale();
  const [name, setName] = useState("");
  const [unconfirmed, setUnconfirmed] = useState(false);
  const cache = useQueryClient();
  const setDirty = useDirtyForm();
  const create = useMutation(OrganizationService.method.createOrganization, {
    retry: false,
  });
  const headingId = useId();
  const inputId = useId();
  const helperId = useId();
  const errorId = useId();
  const input = useRef<HTMLInputElement>(null);
  const inFlight = useRef(false);
  // Focus the name field once when the form first appears; later rerenders
  // (invitation refetches, creation errors) never move focus.
  useEffect(() => {
    input.current?.focus();
  }, []);
  const invalidName =
    Boolean(create.error) &&
    ConnectError.from(create.error).code === Code.InvalidArgument;
  const describedBy = [helperId, invalidName ? errorId : undefined]
    .filter(Boolean)
    .join(" ");
  return (
    <form
      className="account-card onboarding-card"
      aria-labelledby={headingId}
      onSubmit={(event) => {
        event.preventDefault();
        if (!name.trim() || create.isPending || inFlight.current) return;
        inFlight.current = true;
        setUnconfirmed(false);
        create.mutate(
          { name: name.trim() },
          {
            onSuccess: (response) => {
              if (!response.organization) {
                // A response without an organization is not a confirmed creation.
                setUnconfirmed(true);
                return;
              }
              // Navigation can run before React commits a batched state update.
              // Remove the unsaved-draft guard before handing off to that callback.
              flushSync(() => setDirty(false));
              onCreated(response.organization);
              void cache.invalidateQueries();
              onOrganizationCreated?.(response.organization);
            },
            onSettled: () => {
              inFlight.current = false;
            },
          },
        );
      }}
    >
      <h2 id={headingId}>{translate("createOrganization")}</h2>
      <div className="onboarding-field">
        <label htmlFor={inputId}>{translate("organizationName")}</label>
        <input
          id={inputId}
          ref={input}
          required
          maxLength={ORGANIZATION_NAME_MAX_LENGTH}
          value={name}
          onChange={(event) => setName(event.target.value)}
          disabled={create.isPending}
          aria-describedby={describedBy}
          aria-invalid={invalidName || undefined}
        />
        <p id={helperId} className="onboarding-helper">
          {translate("upToCharacters", {
            value0: ORGANIZATION_NAME_MAX_LENGTH,
          })}
        </p>
      </div>
      {Boolean(create.error) && (
        <div id={errorId}>
          <AccountError error={create.error} />
        </div>
      )}
      {unconfirmed && (
        <div className="account-error" role="alert">
          <p>{translate("noOrganizationWasCreatedTryAgain")}</p>
        </div>
      )}
      <button
        className="account-primary onboarding-submit"
        type="submit"
        disabled={!name.trim() || create.isPending}
      >
        {create.isPending
          ? translate("message_c79ed9492e")
          : translate("createOrganization")}
      </button>
      <p className="onboarding-note">
        {translate("anOrganizationIsCreatedOnlyWhen")}
      </p>
    </form>
  );
}

function SetupGuide() {
  useLocale();
  const headingId = useId();
  return (
    <section className="onboarding-guide" aria-labelledby={headingId}>
      <h2 id={headingId}>{translate("yourSetupGuide")}</h2>
      <ol className="onboarding-steps">
        {setupSteps().map((step, index) => (
          <li key={step} aria-current={index === 0 ? "step" : undefined}>
            <span>{step}</span>
            {index === 0 && (
              <span className="onboarding-current">
                {translate("currentStep")}
              </span>
            )}
          </li>
        ))}
      </ol>
    </section>
  );
}

export function OnboardingPage({
  onOrganizationCreated,
}: {
  onOrganizationCreated?: (organization: Organization) => void;
}) {
  useLocale();
  const [created, setCreated] = useState<Organization>();
  if (created)
    return (
      <AccountSurface
        title={translate("organizationCreated")}
        subtitle={created.name}
        className="onboarding-surface onboarding-created"
        focusHeading
      >
        <p role="status">
          {translate("yourOrganizationIsReadyConnectYourPersonal")}
        </p>
        <a
          className="account-primary onboarding-open"
          href={pageHref("overview", created.id)}
        >
          {translate("openOrganization")}
        </a>
      </AccountSurface>
    );
  return (
    <AccountSurface
      title={translate("chooseYourOrganization")}
      subtitle={translate("createAnOrganizationOrAcceptAnInvitation_ebcbcc")}
      className="onboarding-surface"
    >
      <div className="onboarding-grid">
        <InvitationsCard />
        <CreateOrganizationCard
          onOrganizationCreated={onOrganizationCreated}
          onCreated={setCreated}
        />
      </div>
      <SetupGuide />
    </AccountSurface>
  );
}
