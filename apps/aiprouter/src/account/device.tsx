// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "../i18n";
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
  useLocale();
  const [input, setInput] = useState(
    initialCode ??
      new URLSearchParams(window.location.search).get("user_code") ??
      "",
  );
  const [code, setCode] = useState(input.trim());
  return (
    <AccountSurface
      title={translate("connectThisDevice")}
      subtitle={translate("verifyTheDetailsBelowAndApproveOnly")}
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
            {translate("connectionCode")}
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
            {translate("enterTheOneTimeCodeShownBy")}
          </p>
          <button className="account-primary" type="submit">
            {translate("reviewConnection")}
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
  useLocale();
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
      {inspected.isPending && (
        <p role="status">{translate("checkingConnectionCode")}</p>
      )}
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
            <dt>{translate("server_aef7de")}</dt>
            <dd>{authorization.serverAddress}</dd>
            <dt>{translate("device")}</dt>
            <dd>{authorization.deviceName}</dd>
            {state !== State.PENDING && authorization.organizationId && (
              <>
                <dt>{translate("organization")}</dt>
                <dd>
                  {orgs.find((org) => org.id === authorization.organizationId)
                    ?.name ?? authorization.organizationId}
                </dd>
                <dt>{translate("keyExpiry")}</dt>
                <dd>
                  {authorization.keyExpiresAt
                    ? dateLabel(authorization.keyExpiresAt)
                    : translate("noExpiry")}
                </dd>
              </>
            )}
          </dl>
          <p>{translate("connectionCode")}</p>
          <strong className="account-code">{code}</strong>
          <p>{translate("confirmThisCodeMatchesYourCliApprove")}</p>
          <p>
            {translate("originalDeadline", {
              value0:
                deadline === undefined
                  ? translate("notAvailable")
                  : dateLabel(timestampFromDate(new Date(deadline))),
            })}
          </p>
          {remaining !== undefined && (
            <p aria-live="off">
              {translate("codeExpiresIn", {
                value0: Math.floor(remaining / 60),
                value1: String(remaining % 60).padStart(2, "0"),
              })}
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
                {translate("organization")}
                <select
                  required
                  value={organizationId}
                  disabled={busy || organizations.isError}
                  onChange={(event) => setOrganizationId(event.target.value)}
                >
                  <option value="">{translate("chooseAnOrganization")}</option>
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
                      {translate("createAnOrganizationOrAcceptAnInvitation")}
                    </a>
                    {translate("thenReturnToThisCodeBeforeIts")}
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
                  {translate("loadMoreOrganizations")}
                </button>
              )}
              <label>
                {translate("keyExpiry")}
                <select
                  value={expiry}
                  disabled={busy}
                  onChange={(event) => setExpiry(event.target.value as Expiry)}
                >
                  <option value={Expiry.ThirtyDays}>
                    {translate("message30Days")}
                  </option>
                  <option value={Expiry.NinetyDays}>
                    {translate("message90Days")}
                  </option>
                  <option value={Expiry.None}>{translate("noExpiry")}</option>
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
                  {approve.isPending
                    ? translate("message_e99dcb0a97")
                    : translate("approveConnection")}
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
                  {deny.isPending
                    ? translate("message_a3830dd8c9")
                    : translate("message_05a2d7332e")}
                </button>
              </div>
            </form>
          )}
          <div role="status">
            {state === State.APPROVED && (
              <>
                <h2>{translate("approvedWaitingForCliStorage")}</h2>
                <p>{translate("returnToYourCliToFinishApproval")}</p>
                <p>
                  {translate("ifLocalStorageFailsTheCliAttempts")}
                  <a href={pageHref("keys", authorization.organizationId)}>
                    {translate("apiKeys")}
                  </a>
                  .
                </p>
              </>
            )}
            {state === State.DELIVERED && (
              <>
                <h2>{translate("connected")}</h2>
                <p>{translate("theCliConfirmedThatItStoredThe")}</p>
              </>
            )}
            {state === State.DENIED && (
              <>
                <h2>{translate("connectionDenied")}</h2>
                <p>{translate("thisRequestWasDeniedStartANew")}</p>
              </>
            )}
            {state === State.EXPIRED && (
              <>
                <h2>{translate("connectionExpired")}</h2>
                <p>{translate("theOriginalDeadlineHasPassedAnUnclaimed")}</p>
              </>
            )}
            {state === State.REVOKED && (
              <>
                <h2>{translate("connectionNotCompleted")}</h2>
                <p>{translate("theConnectionKeyWasRevokedCheckThe")}</p>
              </>
            )}
            {state === State.UNSPECIFIED && (
              <p>
                {translate("connectionStatusIsUnavailableRefreshBeforeTaking")}
              </p>
            )}
          </div>
        </>
      )}
      <div className="account-actions">
        <button type="button" disabled={busy} onClick={onReset}>
          {translate("enterAnotherCode")}
        </button>
      </div>
    </div>
  );
}
