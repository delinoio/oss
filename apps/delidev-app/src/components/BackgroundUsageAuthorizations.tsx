import {
  useInfiniteQuery,
  useMutation,
  useQuery,
} from "@connectrpc/connect-query";
import { Code, type Transport } from "@connectrpc/connect";
import {
  BackgroundUsageAuthorizationStatus,
  BackgroundUsagePeriod,
  BackgroundUsagePurpose,
  BillingService,
  ErrorReason,
  type BackgroundUsageAuthorization,
  type BackgroundUsageAuthorizationView,
} from "@delinoio/delibase-connect";
import { useEffect, useRef, useState } from "react";

import { describeDelibaseError, getDelibaseError } from "../api/errors";
import { useOnline } from "../hooks/useOnline";
import { createIdempotencyKey } from "../utils/format";
import { Dialog } from "./Dialog";
import {
  EmptyState,
  ErrorState,
  LoadingState,
  OfflineActionHint,
} from "./States";

type AuthorizationScope =
  | {
      kind: "account";
    }
  | {
      kind: "organization";
      organizationId: string;
      organizationName: string;
      showOrganizationWide: boolean;
    };

function uuid(value: string | undefined) {
  return value ? { value } : undefined;
}

function safeIdentifier(value: string | undefined): string {
  if (!value) return "Unavailable";
  return value.length > 12 ? `…${value.slice(-8)}` : value;
}

function formatUnits(value: bigint | undefined): string {
  return value === undefined ? "Unavailable" : value.toLocaleString("en-US");
}

function currentUnits(view: BackgroundUsageAuthorizationView): bigint | undefined {
  const usage = view.currentPeriodUsage;
  if (!usage?.heldUnits || !usage.committedUnits) return undefined;
  return usage.heldUnits.value + usage.committedUnits.value;
}

function statusLabel(status: BackgroundUsageAuthorizationStatus): string {
  switch (status) {
    case BackgroundUsageAuthorizationStatus.ACTIVE:
      return "Active";
    case BackgroundUsageAuthorizationStatus.REVOKED:
      return "Revoked";
    case BackgroundUsageAuthorizationStatus.ACCESS_LOST:
      return "Access lost";
    case BackgroundUsageAuthorizationStatus.RESOURCE_DELETED:
      return "Resource deleted";
    case BackgroundUsageAuthorizationStatus.OWNER_DELETED:
      return "Owner deleted";
    default:
      return "Unavailable";
  }
}

function purposeLabel(purpose: BackgroundUsagePurpose): string {
  return purpose === BackgroundUsagePurpose.REALQA_STORAGE
    ? "RealQA storage"
    : "Unavailable";
}

function periodLabel(period: BackgroundUsagePeriod): string {
  return period === BackgroundUsagePeriod.UTC_DAY
    ? "UTC day"
    : "Unavailable";
}

export function backgroundAuthorizationRecovery(
  status: BackgroundUsageAuthorizationStatus,
): string {
  switch (status) {
    case BackgroundUsageAuthorizationStatus.ACTIVE:
      return "No authorization failure is reported. If billing cannot settle, restore available credit or permitted overage, then let the bound service retry safely.";
    case BackgroundUsageAuthorizationStatus.REVOKED:
      return "This grant cannot be restored. Rebind the feature resource with a new authorization if background usage should resume.";
    case BackgroundUsageAuthorizationStatus.ACCESS_LOST:
      return "Restore the authorizer’s organization and team access, then rebind the feature resource with a new authorization.";
    case BackgroundUsageAuthorizationStatus.RESOURCE_DELETED:
      return "The bound resource was deleted. Create or select a new resource before authorizing background usage again.";
    case BackgroundUsageAuthorizationStatus.OWNER_DELETED:
      return "The personal or organization owner no longer exists. Choose an active owner and rebind with a new authorization.";
    default:
      return "Refresh the authorization. If its state remains unavailable, contact support before retrying background usage.";
  }
}

function ownerLabel(authorization: BackgroundUsageAuthorization): string {
  const owner = authorization.owner?.owner;
  if (owner?.case === "personalAccountId") {
    return `Personal account ${safeIdentifier(owner.value.value)}`;
  }
  if (owner?.case === "organizationId") {
    return `Organization ${safeIdentifier(owner.value.value)}`;
  }
  return "Unavailable";
}

function billingFailureLabel(
  status: BackgroundUsageAuthorizationStatus,
): string {
  return status === BackgroundUsageAuthorizationStatus.ACTIVE
    ? "None reported"
    : "Authorization inactive";
}

function AuthorizationFacts({
  view,
}: {
  view: BackgroundUsageAuthorizationView;
}) {
  const authorization = view.authorization;
  if (!authorization) {
    return (
      <p className="inline-error" role="alert">
        Authorization details are unavailable.
      </p>
    );
  }
  const usage = view.currentPeriodUsage;
  return (
    <dl className="authorization-facts">
      <div>
        <dt>Owner</dt>
        <dd>{ownerLabel(authorization)}</dd>
      </div>
      <div>
        <dt>Payer</dt>
        <dd>
          Organization {safeIdentifier(authorization.organizationId?.value)}
        </dd>
      </div>
      <div>
        <dt>Team</dt>
        <dd>{safeIdentifier(authorization.teamId?.value)}</dd>
      </div>
      <div>
        <dt>Purpose</dt>
        <dd>{purposeLabel(authorization.purpose)}</dd>
      </div>
      <div>
        <dt>Resource identifier</dt>
        <dd>
          <code>{safeIdentifier(authorization.featureResourceId?.value)}</code>
        </dd>
      </div>
      <div>
        <dt>Status</dt>
        <dd>{statusLabel(authorization.status)}</dd>
      </div>
      <div>
        <dt>Current-period units</dt>
        <dd>{formatUnits(currentUnits(view))}</dd>
      </div>
      <div>
        <dt>Maximum units per {periodLabel(authorization.period)}</dt>
        <dd>
          {formatUnits(
            usage?.maximumUnits?.value ?? authorization.maximumUnits?.value,
          )}
        </dd>
      </div>
      <div>
        <dt>Billing failure</dt>
        <dd>{billingFailureLabel(authorization.status)}</dd>
      </div>
      <div>
        <dt>Revision</dt>
        <dd>{authorization.revision.toString()}</dd>
      </div>
      <div className="authorization-guidance">
        <dt>Recovery guidance</dt>
        <dd>{backgroundAuthorizationRecovery(authorization.status)}</dd>
      </div>
    </dl>
  );
}

function AuthorizationDialog({
  authorizationId,
  initialView,
  onClose,
  onRevoked,
  transport,
}: {
  authorizationId: string;
  initialView: BackgroundUsageAuthorizationView;
  onClose: () => void;
  onRevoked: () => Promise<void>;
  transport: Transport;
}) {
  const online = useOnline();
  const [confirming, setConfirming] = useState(false);
  const [resultMessage, setResultMessage] = useState("");
  const [revokeError, setRevokeError] = useState("");
  const confirmationFocusRef = useRef<HTMLButtonElement>(null);
  const revokeKey = useRef<{ key: string } | undefined>(undefined);
  const detail = useQuery(
    BillingService.method.getBackgroundUsageAuthorization,
    { authorizationId: uuid(authorizationId) },
    {
      gcTime: 0,
      retry: false,
      staleTime: 0,
      transport,
    },
  );
  const revoke = useMutation(
    BillingService.method.revokeBackgroundUsageAuthorization,
    { gcTime: 0, transport },
  );
  const view = detail.data?.authorization ?? initialView;
  const authorization = view.authorization;

  useEffect(() => {
    if (confirming) confirmationFocusRef.current?.focus();
  }, [confirming]);

  const close = () => {
    if (revoke.isPending) return;
    if (confirming) {
      setConfirming(false);
      setRevokeError("");
      return;
    }
    revokeKey.current = undefined;
    onClose();
  };

  const revokeAuthorization = async () => {
    if (!authorization || !online) return;
    setRevokeError("");
    setResultMessage("");
    revokeKey.current ??= createIdempotencyKey();
    try {
      const response = await revoke.mutateAsync({
        authorizationId: uuid(authorizationId),
        expectedRevision: authorization.revision,
        idempotency: revokeKey.current,
      });
      revokeKey.current = undefined;
      setConfirming(false);
      setResultMessage(
        response.idempotency?.replayed
          ? "Revocation confirmed from the original safe retry."
          : "Authorization revoked.",
      );
      await Promise.all([
        detail.refetch({ throwOnError: true }),
        onRevoked(),
      ]).catch(() => undefined);
    } catch (error) {
      const typed = getDelibaseError(error);
      setRevokeError(describeDelibaseError(error));
      if (
        typed.code === Code.PermissionDenied ||
        typed.reason ===
          ErrorReason.BACKGROUND_USAGE_AUTHORIZATION_ACCESS_LOST ||
        typed.reason ===
          ErrorReason.BACKGROUND_USAGE_AUTHORIZATION_STATUS_INVALID ||
        typed.reason === ErrorReason.RESOURCE_CONFLICT ||
        typed.reason === ErrorReason.IDEMPOTENCY_CONFLICT ||
        typed.reason === ErrorReason.BACKGROUND_USAGE_REPLAY_CONFLICT
      ) {
        await Promise.all([
          detail.refetch(),
          onRevoked(),
        ]).catch(() => undefined);
      }
    }
  };

  return (
    <Dialog
      descriptionId="background-authorization-description"
      onClose={close}
      titleId="background-authorization-title"
    >
      <h2 id="background-authorization-title">
        {confirming
          ? "Revoke background usage authorization?"
          : "Background usage authorization"}
      </h2>
      <p id="background-authorization-description">
        {confirming
          ? "Revocation is immediate and cannot be undone. In-flight safe retries may return the original revocation result."
          : "This view contains bounded identifiers and summarized usage only. Service credentials and provider data are never exposed."}
      </p>
      {detail.isPending && !detail.data ? (
        <LoadingState label="Refreshing authorization details" />
      ) : null}
      {detail.isError && !detail.data ? (
        <ErrorState
          error={detail.error}
          onRetry={() => void detail.refetch()}
          title="Authorization details unavailable"
        />
      ) : null}
      {view ? <AuthorizationFacts view={view} /> : null}
      {revokeError ? (
        <p className="inline-error" role="alert">
          {revokeError}
        </p>
      ) : null}
      {resultMessage ? (
        <p className="inline-success" role="status">
          {resultMessage}
        </p>
      ) : null}
      {confirming && !online ? <OfflineActionHint /> : null}
      <div className="dialog-actions">
        <button
          className="button secondary"
          disabled={revoke.isPending}
          onClick={close}
          ref={confirming ? confirmationFocusRef : undefined}
          type="button"
        >
          {confirming ? "Keep authorization" : "Close"}
        </button>
        {!detail.isError &&
        authorization?.status ===
          BackgroundUsageAuthorizationStatus.ACTIVE ? (
          confirming ? (
            <button
              className="button danger"
              disabled={!online || revoke.isPending}
              onClick={() => void revokeAuthorization()}
              type="button"
            >
              {revoke.isPending ? "Revoking…" : "Revoke authorization"}
            </button>
          ) : (
            <button
              className="button danger"
              disabled={!online}
              onClick={() => {
                setResultMessage("");
                setConfirming(true);
              }}
              type="button"
            >
              Review revocation
            </button>
          )
        ) : null}
      </div>
    </Dialog>
  );
}

export function BackgroundUsageAuthorizations({
  scope,
  transport,
}: {
  scope: AuthorizationScope;
  transport: Transport;
}) {
  const online = useOnline();
  const [selected, setSelected] = useState<
    BackgroundUsageAuthorizationView | undefined
  >(undefined);
  const input =
    scope.kind === "account"
      ? {
          page: { cursor: "", pageSize: 25 },
        }
      : {
          organizationId: { value: scope.organizationId },
          page: { cursor: "", pageSize: 25 },
        };
  const authorizations = useInfiniteQuery(
    BillingService.method.listBackgroundUsageAuthorizations,
    input,
    {
      enabled: online,
      gcTime: 0,
      getNextPageParam: (lastPage) => {
        const cursor = lastPage.page?.nextCursor;
        return cursor ? { cursor, pageSize: 25 } : undefined;
      },
      pageParamKey: "page",
      retry: false,
      staleTime: 0,
      transport,
    },
  );
  const rows =
    authorizations.data?.pages.flatMap((page) => page.authorizations) ?? [];
  const scopeDescription =
    scope.kind === "account"
      ? "The server returns grants you created plus organization-wide grants where you are an Owner or Admin."
      : scope.showOrganizationWide
        ? `As an Owner or Admin, you can review and revoke every grant paid by ${scope.organizationName}.`
        : `Only grants you created and their summarized usage are returned for ${scope.organizationName}.`;

  return (
    <section
      aria-labelledby={`background-authorizations-${scope.kind}-title`}
      className="content-card background-authorizations"
    >
      <div>
        <span className="eyebrow">Billing and usage</span>
        <h2 id={`background-authorizations-${scope.kind}-title`}>
          Background usage authorizations
        </h2>
        <p>{scopeDescription}</p>
      </div>
      {!online && !authorizations.data ? (
        <p className="outage-state" role="status">
          Reconnect to load network-only authorization and usage data.
        </p>
      ) : null}
      {authorizations.isPending && online ? (
        <LoadingState label="Loading background authorizations" />
      ) : null}
      {authorizations.isError && !authorizations.data ? (
        <ErrorState
          error={authorizations.error}
          onRetry={() => void authorizations.refetch()}
          title="Background authorizations unavailable"
        />
      ) : null}
      {authorizations.data && rows.length === 0 ? (
        <EmptyState
          description="No visible background usage grants were found."
          title="No background authorizations"
        />
      ) : null}
      {rows.length ? (
        <>
          <div className="table-card">
            <table>
              <caption className="sr-only">
                Visible background usage authorizations
              </caption>
              <thead>
                <tr>
                  <th scope="col">Purpose</th>
                  <th scope="col">Resource</th>
                  <th scope="col">Status</th>
                  <th scope="col">Current units</th>
                  <th scope="col">Maximum units</th>
                  <th scope="col">Revision</th>
                  <th scope="col">Actions</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((view) => {
                  const authorization = view.authorization;
                  if (!authorization) return null;
                  return (
                    <tr key={authorization.authorizationId?.value}>
                      <td>{purposeLabel(authorization.purpose)}</td>
                      <td>
                        <code>
                          {safeIdentifier(
                            authorization.featureResourceId?.value,
                          )}
                        </code>
                      </td>
                      <td>{statusLabel(authorization.status)}</td>
                      <td>{formatUnits(currentUnits(view))}</td>
                      <td>
                        {formatUnits(
                          view.currentPeriodUsage?.maximumUnits?.value ??
                            authorization.maximumUnits?.value,
                        )}
                      </td>
                      <td>{authorization.revision.toString()}</td>
                      <td>
                        <button
                          className="button secondary compact-button"
                          onClick={() => setSelected(view)}
                          type="button"
                        >
                          View details
                          <span className="sr-only">
                            {" "}
                            for resource{" "}
                            {safeIdentifier(
                              authorization.featureResourceId?.value,
                            )}
                          </span>
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          {authorizations.isFetchNextPageError ? (
            <p className="inline-error" role="alert">
              {describeDelibaseError(authorizations.error)}
            </p>
          ) : null}
          {authorizations.hasNextPage ? (
            <div className="pagination-actions">
              <button
                className="button secondary"
                disabled={authorizations.isFetchingNextPage || !online}
                onClick={() => void authorizations.fetchNextPage()}
                type="button"
              >
                {authorizations.isFetchingNextPage
                  ? "Loading more…"
                  : "Load more authorizations"}
              </button>
            </div>
          ) : null}
        </>
      ) : null}
      {selected?.authorization?.authorizationId?.value ? (
        <AuthorizationDialog
          authorizationId={selected.authorization.authorizationId.value}
          initialView={selected}
          onClose={() => setSelected(undefined)}
          onRevoked={async () => {
            await authorizations.refetch({ throwOnError: true });
          }}
          transport={transport}
        />
      ) : null}
    </section>
  );
}
