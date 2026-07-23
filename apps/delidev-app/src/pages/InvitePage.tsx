import { createClient } from "@connectrpc/connect";
import {
  OrganizationRole,
  OrganizationService,
  TeamRole,
  type GetOrganizationInvitationResponse,
} from "@delinoio/delibase-connect";
import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { useAuthSession } from "../auth/AuthSession";
import { ErrorState, LoadingState, OfflineActionHint } from "../components/States";
import { useDocumentMetadata } from "../hooks/useDocumentMetadata";
import { useOnline } from "../hooks/useOnline";
import { createIdempotencyKey, formatEnumLabel } from "../utils/format";

type InvitationRequestState =
  | {
      attempt: number;
      status: "pending";
      token: string;
    }
  | {
      attempt: number;
      data: GetOrganizationInvitationResponse;
      status: "success";
      token: string;
    }
  | {
      attempt: number;
      error: unknown;
      status: "error";
      token: string;
    };

type AcceptanceState =
  | { status: "idle" }
  | { status: "pending" }
  | { error: unknown; status: "error" };

function errorMessage(error: unknown): string {
  return error instanceof Error
    ? error.message
    : "The request could not be completed. Please try again.";
}

export function InvitePage() {
  useDocumentMetadata(
    "Organization invitation",
    "Review and accept a private DeliDev organization invitation.",
  );
  const { token = "" } = useParams();
  const { transport } = useAuthSession();
  const online = useOnline();
  const navigate = useNavigate();
  const client = useMemo(
    () => (transport ? createClient(OrganizationService, transport) : undefined),
    [transport],
  );
  const [loadAttempt, setLoadAttempt] = useState(0);
  const [invitation, setInvitation] = useState<InvitationRequestState>({
    attempt: 0,
    status: "pending",
    token,
  });
  const [acceptance, setAcceptance] = useState<AcceptanceState>({
    status: "idle",
  });
  const acceptanceKey = useRef<
    { idempotency: { key: string }; token: string } | undefined
  >(undefined);
  const currentInvitation =
    invitation.token === token && invitation.attempt === loadAttempt
      ? invitation
      : ({ attempt: loadAttempt, status: "pending", token } as const);

  useEffect(() => {
    if (!client || !online || !token) return;
    const controller = new AbortController();
    const attempt = loadAttempt;

    void client
      .getOrganizationInvitation(
        { bearerToken: { token } },
        { signal: controller.signal },
      )
      .then((data) => {
        if (!controller.signal.aborted) {
          setInvitation({ attempt, data, status: "success", token });
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setInvitation({ attempt, error, status: "error", token });
        }
      });

    return () => controller.abort();
  }, [client, loadAttempt, online, token]);

  const acceptInvitation = async () => {
    if (!client) return;
    const pendingAcceptance =
      acceptanceKey.current?.token === token
        ? acceptanceKey.current
        : {
            idempotency: createIdempotencyKey(),
            token,
          };
    acceptanceKey.current = pendingAcceptance;
    setAcceptance({ status: "pending" });
    try {
      const response = await client.acceptOrganizationInvitation({
        bearerToken: { token },
        idempotency: pendingAcceptance.idempotency,
      });
      acceptanceKey.current = undefined;
      navigate(`/o/${response.organization?.slug ?? ""}/apps`, {
        replace: true,
      });
    } catch (error) {
      setAcceptance({ error, status: "error" });
    }
  };

  const dependencyError =
    !token
      ? new Error("This invitation link is missing its bearer token.")
      : !client
        ? new Error("An authenticated connection is required.")
        : undefined;

  if (dependencyError) {
    return (
      <div className="page narrow">
        <ErrorState error={dependencyError} title="Invitation unavailable" />
      </div>
    );
  }

  if (!online) {
    return (
      <div className="page narrow">
        <ErrorState
          error={new Error(
            "Invitation tokens and invitation details are never stored offline.",
          )}
          title="Reconnect to review this invitation"
        />
      </div>
    );
  }
  if (currentInvitation.status === "pending") {
    return (
      <div className="page narrow">
        <LoadingState label="Checking invitation" />
      </div>
    );
  }
  if (currentInvitation.status === "error") {
    return (
      <div className="page narrow">
        <ErrorState
          error={currentInvitation.error}
          onRetry={() => setLoadAttempt((attempt) => attempt + 1)}
          title="This invitation isn’t available"
        />
      </div>
    );
  }
  const invitationDetails = currentInvitation.data.invitation;
  const organizationRole = formatEnumLabel(
    OrganizationRole[
      invitationDetails?.organizationRole ?? OrganizationRole.MEMBER
    ] ?? "member",
  );
  const hasTeamAssignment = Boolean(invitationDetails?.teamId?.value);
  const teamRole = formatEnumLabel(
    TeamRole[invitationDetails?.teamRole ?? TeamRole.MEMBER] ?? "member",
  );

  return (
    <div className="page narrow">
      <section className="invite-card">
        <span className="eyebrow">You’re invited</span>
        <h1>Join {currentInvitation.data.organizationName}</h1>
        <p>Your organization role will be {organizationRole}.</p>
        {hasTeamAssignment ? (
          <p>
            You’ll join the{" "}
            <strong>{currentInvitation.data.teamName || "assigned"}</strong>{" "}
            team as {teamRole}.
          </p>
        ) : null}
        {acceptance.status === "error" ? (
          <p className="inline-error" role="alert">
            {errorMessage(acceptance.error)}
          </p>
        ) : null}
        <button
          className="button primary full"
          disabled={!online || acceptance.status === "pending"}
          type="button"
          onClick={() => void acceptInvitation()}
        >
          {acceptance.status === "pending"
            ? "Joining…"
            : "Accept invitation"}
        </button>
        {!online ? <OfflineActionHint /> : null}
      </section>
    </div>
  );
}
