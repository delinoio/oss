import { useMutation, useQuery } from "@connectrpc/connect-query";
import { OrganizationService } from "@delinoio/delibase-connect";
import { useNavigate, useParams } from "react-router-dom";

import { useAuthSession } from "../auth/AuthSession";
import { ErrorState, LoadingState, OfflineActionHint } from "../components/States";
import { useDocumentMetadata } from "../hooks/useDocumentMetadata";
import { useOnline } from "../hooks/useOnline";
import { createIdempotencyKey, formatEnumLabel } from "../utils/format";

export function InvitePage() {
  useDocumentMetadata(
    "Organization invitation",
    "Review and accept a private DeliDev organization invitation.",
  );
  const { token = "" } = useParams();
  const { transport } = useAuthSession();
  const online = useOnline();
  const navigate = useNavigate();
  const invitation = useQuery(
    OrganizationService.method.getOrganizationInvitation,
    { bearerToken: { token } },
    {
      enabled: online && Boolean(token),
      gcTime: 0,
      retry: false,
      staleTime: 0,
      transport,
    },
  );
  const accept = useMutation(
    OrganizationService.method.acceptOrganizationInvitation,
    { transport },
  );

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
  if (invitation.isPending) {
    return (
      <div className="page narrow">
        <LoadingState label="Checking invitation" />
      </div>
    );
  }
  if (invitation.isError) {
    return (
      <div className="page narrow">
        <ErrorState
          error={invitation.error}
          onRetry={() => void invitation.refetch()}
          title="This invitation isn’t available"
        />
      </div>
    );
  }

  return (
    <div className="page narrow">
      <section className="invite-card">
        <span className="eyebrow">You’re invited</span>
        <h1>Join {invitation.data.organizationName}</h1>
        <p>
          You’ll join the <strong>{invitation.data.teamName}</strong> team as{" "}
          {formatEnumLabel(
            invitation.data.invitation?.organizationRole ?? "member",
          )}
          .
        </p>
        {accept.error ? (
          <p className="inline-error" role="alert">
            {accept.error.message}
          </p>
        ) : null}
        <button
          className="button primary full"
          disabled={!online || accept.isPending}
          type="button"
          onClick={() =>
            accept.mutate(
              {
                bearerToken: { token },
                idempotency: createIdempotencyKey(),
              },
              {
                onSuccess: (response) =>
                  navigate(`/o/${response.organization?.slug ?? ""}/apps`, {
                    replace: true,
                  }),
              },
            )
          }
        >
          {accept.isPending ? "Joining…" : "Accept invitation"}
        </button>
        {!online ? <OfflineActionHint /> : null}
      </section>
    </div>
  );
}
