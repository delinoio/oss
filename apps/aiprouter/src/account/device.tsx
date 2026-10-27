// SPDX-License-Identifier: Apache-2.0
import { pageHref } from "../routes";
import { useRef, useState } from "react";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  DeviceApprovalService,
  DeviceAuthorizationState as State,
} from "@aiprouter/client";
import {
  AccountError,
  AccountSurface,
  dateLabel,
  timestampMs,
  useNow,
  useOrganizations,
} from "./shared";

enum Expiry {
  ThirtyDays = "30",
  NinetyDays = "90",
  None = "none",
}

export function DevicePage({ initialCode }: { initialCode?: string }) {
  const [input, setInput] = useState(
    initialCode ??
      new URLSearchParams(window.location.search).get("user_code") ??
      "",
  );
  const [code, setCode] = useState(input.trim());
  return (
    <AccountSurface
      title="Connect this device?"
      subtitle="Verify the details below and approve only a connection you started."
    >
      {code ? (
        <DeviceReview key={code} code={code} onReset={() => setCode("")} />
      ) : (
        <form
          className="account-card"
          onSubmit={(event) => {
            event.preventDefault();
            if (input.trim()) setCode(input.trim());
          }}
        >
          <label>
            Connection code
            <input
              autoFocus
              autoComplete="off"
              autoCapitalize="characters"
              spellCheck={false}
              value={input}
              required
              maxLength={64}
              onChange={(event) => setInput(event.target.value)}
            />
          </label>
          <p className="account-hint">
            Enter the one-time code shown by your CLI.
          </p>
          <button className="account-primary" type="submit">
            Review connection
          </button>
        </form>
      )}
    </AccountSurface>
  );
}

function DeviceReview({
  code,
  onReset,
}: {
  code: string;
  onReset: () => void;
}) {
  const now = useNow();
  const [organizationId, setOrganizationId] = useState("");
  const [expiry, setExpiry] = useState(Expiry.ThirtyDays);
  const [decision, setDecision] = useState<State>();
  const originalDeadline = useRef<number | undefined>(undefined);
  const inspected = useQuery(
    DeviceApprovalService.method.getDeviceAuthorization,
    { userCode: code },
    {
      retry: false,
      refetchInterval: (query) => {
        const state = query.state.data?.authorization?.state;
        return state === State.PENDING || state === State.APPROVED
          ? 5000
          : false;
      },
    },
  );
  const organizations = useOrganizations();
  const approve = useMutation(
    DeviceApprovalService.method.approveDeviceAuthorization,
    { retry: false },
  );
  const deny = useMutation(
    DeviceApprovalService.method.denyDeviceAuthorization,
    { retry: false },
  );
  const authorization =
    approve.data?.authorization ?? inspected.data?.authorization;
  const inspectedState = inspected.data?.authorization?.state;
  // A retry must never create a fresh ten-minute window. Keep the earliest server deadline.
  const receivedDeadline = timestampMs(authorization?.expiresAt);
  if (receivedDeadline !== undefined)
    originalDeadline.current = Math.min(
      originalDeadline.current ?? receivedDeadline,
      receivedDeadline,
    );
  const deadline = originalDeadline.current;
  const remaining =
    deadline === undefined
      ? undefined
      : Math.max(0, Math.ceil((deadline - now) / 1000));
  const terminal =
    inspectedState !== undefined &&
    [State.DELIVERED, State.REVOKED, State.EXPIRED, State.DENIED].includes(
      inspectedState,
    );
  const serverState = terminal ? inspectedState : (decision ?? inspectedState);
  const state =
    remaining === 0 &&
    (serverState === State.PENDING || serverState === State.APPROVED)
      ? State.EXPIRED
      : serverState;
  const busy = approve.isPending || deny.isPending;
  const orgs = organizations.organizations;
  const selectedOrg = orgs.find((org) => org.id === organizationId);
  const error = approve.error ?? deny.error ?? inspected.error;
  const actionable =
    state === State.PENDING &&
    !busy &&
    !inspected.isError &&
    remaining !== undefined &&
    remaining > 0;

  return (
    <div className="account-card">
      {inspected.isPending && <p role="status">Checking connection code…</p>}
      <AccountError
        error={error}
        retry={() => {
          approve.reset();
          deny.reset();
          void inspected.refetch();
        }}
      />
      {authorization && (
        <>
          <dl className="account-details">
            <dt>Server</dt>
            <dd>{authorization.serverAddress}</dd>
            <dt>Device</dt>
            <dd>{authorization.deviceName}</dd>
            {state !== State.PENDING && authorization.organizationId && (
              <>
                <dt>Organization</dt>
                <dd>
                  {orgs.find((org) => org.id === authorization.organizationId)
                    ?.name ?? authorization.organizationId}
                </dd>
                <dt>Key expiry</dt>
                <dd>
                  {authorization.keyExpiresAt
                    ? dateLabel(authorization.keyExpiresAt)
                    : "No expiry"}
                </dd>
              </>
            )}
          </dl>
          <p>Connection code</p>
          <strong className="account-code">{code}</strong>
          <p>
            Confirm this code matches your CLI. Approve only a connection you
            started.
          </p>
          <p>
            Original deadline:{" "}
            {deadline === undefined
              ? "Not available"
              : dateLabel(timestampFromDate(new Date(deadline)))}
          </p>
          {remaining !== undefined && (
            <p aria-live="off">
              Code expires in {Math.floor(remaining / 60)}:
              {String(remaining % 60).padStart(2, "0")}
            </p>
          )}
          {state === State.PENDING && (
            <form
              onSubmit={(event) => {
                event.preventDefault();
                if (
                  !actionable ||
                  !selectedOrg ||
                  organizations.isError ||
                  deadline === undefined ||
                  Date.now() >= deadline
                )
                  return;
                approve.mutate(
                  {
                    userCode: code,
                    organizationId: selectedOrg.id,
                    keyExpiresAt:
                      expiry === Expiry.None
                        ? undefined
                        : timestampFromDate(
                            new Date(Date.now() + Number(expiry) * 86400000),
                          ),
                  },
                  {
                    onSuccess: (response) => {
                      setDecision(
                        response.authorization?.state ?? State.APPROVED,
                      );
                      void inspected.refetch();
                    },
                  },
                );
              }}
            >
              <AccountError
                error={organizations.error}
                retry={() => {
                  void organizations.refetch();
                }}
              />
              <label>
                Organization
                <select
                  required
                  value={organizationId}
                  disabled={busy || organizations.isError}
                  onChange={(event) => setOrganizationId(event.target.value)}
                >
                  <option value="">Choose an organization</option>
                  {orgs.map((org) => (
                    <option key={org.id} value={org.id}>
                      {org.name}
                    </option>
                  ))}
                </select>
              </label>
              {!organizations.isPending &&
                !organizations.isError &&
                orgs.length === 0 && (
                  <p>
                    <a href="/overview">
                      Create an organization or accept an invitation
                    </a>
                    , then return to this code before its deadline.
                  </p>
                )}
              {organizations.hasNextPage && (
                <button
                  type="button"
                  disabled={organizations.isFetching}
                  onClick={() => {
                    void organizations.fetchNextPage();
                  }}
                >
                  Load more organizations
                </button>
              )}
              <label>
                Key expiry
                <select
                  value={expiry}
                  disabled={busy}
                  onChange={(event) => setExpiry(event.target.value as Expiry)}
                >
                  <option value={Expiry.ThirtyDays}>30 days</option>
                  <option value={Expiry.NinetyDays}>90 days</option>
                  <option value={Expiry.None}>No expiry</option>
                </select>
              </label>
              <div className="account-actions">
                <button
                  type="submit"
                  className="account-primary"
                  disabled={
                    !actionable || !selectedOrg || organizations.isError
                  }
                >
                  {approve.isPending ? "Approving…" : "Approve connection"}
                </button>
                <button
                  type="button"
                  disabled={!actionable}
                  onClick={() =>
                    deny.mutate(
                      { userCode: code },
                      {
                        onSuccess: () => {
                          setDecision(State.DENIED);
                          void inspected.refetch();
                        },
                      },
                    )
                  }
                >
                  {deny.isPending ? "Denying…" : "Deny"}
                </button>
              </div>
            </form>
          )}
          <div role="status">
            {state === State.APPROVED && (
              <>
                <h2>Approved — waiting for CLI storage</h2>
                <p>
                  Return to your CLI to finish. Approval does not confirm that
                  your CLI stored the key.
                </p>
                <p>
                  If local storage fails, the CLI attempts to revoke the key. If
                  it reports incomplete revocation, revoke the CLI key in{" "}
                  <a href={pageHref("keys", authorization.organizationId)}>
                    API keys
                  </a>
                  .
                </p>
              </>
            )}
            {state === State.DELIVERED && (
              <>
                <h2>Connected</h2>
                <p>
                  The CLI confirmed that it stored the key. Continue in your CLI
                  to review client settings.
                </p>
              </>
            )}
            {state === State.DENIED && (
              <>
                <h2>Connection denied</h2>
                <p>
                  This request was denied. Start a new connection from your CLI
                  if needed.
                </p>
              </>
            )}
            {state === State.EXPIRED && (
              <>
                <h2>Connection expired</h2>
                <p>
                  The original deadline has passed. An unclaimed key is revoked
                  by the server. Start a new connection from your CLI.
                </p>
              </>
            )}
            {state === State.REVOKED && (
              <>
                <h2>Connection not completed</h2>
                <p>
                  The connection key was revoked. Check the CLI for storage or
                  delivery errors, then start a new connection.
                </p>
              </>
            )}
            {state === State.UNSPECIFIED && (
              <p>
                Connection status is unavailable. Refresh before taking any
                action.
              </p>
            )}
          </div>
        </>
      )}
      <div className="account-actions">
        <button type="button" disabled={busy} onClick={onReset}>
          Enter another code
        </button>
      </div>
    </div>
  );
}
