// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "../i18n";
import { pageHref } from "../routes";
import { useState } from "react";
import { flushSync } from "react-dom";
import { useMutation } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { OrganizationService, type Organization } from "@tairouter/client";
import { AccountError, AccountSurface, useMyInvitations } from "./shared";
import { useDirtyForm } from "../api";

export function OnboardingPage({
  onOrganizationCreated,
}: {
  onOrganizationCreated?: (organization: Organization) => void;
}) {
  useLocale();
  const [name, setName] = useState("");
  const [created, setCreated] = useState<Organization>();
  const cache = useQueryClient();
  const setDirty = useDirtyForm();
  const invitations = useMyInvitations();
  const create = useMutation(OrganizationService.method.createOrganization, {
    retry: false,
  });
  if (created)
    return (
      <AccountSurface
        title={translate("organizationCreated")}
        subtitle={created.name}
      >
        <p role="status">
          {translate("yourOrganizationIsReadyConnectYourPersonal")}
        </p>
        <a href={pageHref("overview", created.id)}>
          {translate("openOrganization")}
        </a>
      </AccountSurface>
    );
  return (
    <AccountSurface
      title={translate("chooseYourOrganization")}
      subtitle={translate("createAnOrganizationOrAcceptAnInvitation_ebcbcc")}
    >
      <div className="account-card">
        <h2>{translate("yourInvitations")}</h2>
        {invitations.isPending && (
          <p role="status">{translate("loadingInvitations")}</p>
        )}
        <AccountError
          error={invitations.error}
          retry={() => {
            void invitations.refetch();
          }}
        />
        {invitations.invitations.map((invitation) => (
          <p key={invitation.id}>
            <strong>{invitation.organizationName}</strong>{" "}
            <a
              href={`/invitation?invitation_id=${encodeURIComponent(invitation.id)}`}
            >
              {translate("reviewInvitation")}
            </a>
          </p>
        ))}
        {invitations.hasNextPage && (
          <button
            type="button"
            disabled={invitations.isFetching}
            onClick={() => {
              void invitations.fetchNextPage();
            }}
          >
            {translate("loadMoreInvitations")}
          </button>
        )}
        <p>{translate("openTheInvitationLinkFromYourEmail")}</p>
        <a href="/invitation">{translate("enterAnInvitation")}</a>
      </div>
      <form
        className="account-card"
        onSubmit={(event) => {
          event.preventDefault();
          if (!name.trim() || create.isPending) return;
          create.mutate(
            { name: name.trim() },
            {
              onSuccess: (response) => {
                if (!response.organization) return;
                // Navigation can run before React commits a batched state update.
                // Remove the unsaved-draft guard before handing off to that callback.
                flushSync(() => setDirty(false));
                setCreated(response.organization);
                void cache.invalidateQueries();
                onOrganizationCreated?.(response.organization);
              },
            },
          );
        }}
      >
        <h2>{translate("createOrganization")}</h2>
        <label>
          {translate("organizationName")}
          <input
            autoFocus
            required
            maxLength={200}
            value={name}
            onChange={(event) => setName(event.target.value)}
            disabled={create.isPending}
          />
        </label>
        <AccountError error={create.error} />
        <button
          className="account-primary"
          type="submit"
          disabled={!name.trim() || create.isPending}
        >
          {create.isPending
            ? translate("message_c79ed9492e")
            : translate("createOrganization")}
        </button>
      </form>
      <ol className="account-hint">
        <li>{translate("chooseOrganization")}</li>
        <li>{translate("connectPersonalProviders")}</li>
        <li>{translate("configureModelsAndRouting")}</li>
        <li>{translate("createApiKey")}</li>
        <li>{translate("connectAClient")}</li>
      </ol>
    </AccountSurface>
  );
}
