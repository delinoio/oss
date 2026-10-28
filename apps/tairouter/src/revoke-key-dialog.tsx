// SPDX-License-Identifier: Apache-2.0
import { useId, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { t as translate, useLocale } from "./i18n";
import { Button, Dialog, ErrorNotice, Notice } from "./ui";

/**
 * How a failed revocation request is presented. A definitive failure is a
 * server answer; an uncertain one means the request may have reached the server
 * but its result is unknown, so the console must not claim either outcome.
 */
export enum RevokeFailureKind {
  Definitive = "definitive",
  Uncertain = "uncertain",
}

/**
 * Classifies a failed RevokeKey call. Timeouts, cancellation and failures that
 * carry no server verdict (including non-Connect transport errors, which
 * ConnectError.from maps to Unknown) leave the outcome unconfirmed. Unavailable
 * keeps its fixed "server is unavailable" message because the shared error
 * contract treats it as a definitive refusal to serve the request.
 */
export function classifyRevokeFailure(error: unknown): RevokeFailureKind {
  const code = ConnectError.from(error).code;
  return code === Code.DeadlineExceeded ||
    code === Code.Canceled ||
    code === Code.Unknown
    ? RevokeFailureKind.Uncertain
    : RevokeFailureKind.Definitive;
}

/**
 * Confirmation for revoking one API key. Key and organization names are
 * rendered as literal text only. The shared action state belongs to the whole
 * page, so pending and error feedback is scoped to a submission made from this
 * dialog instance; a dialog opened later never shows an earlier outcome.
 */
export function RevokeKeyDialog({
  keyName,
  organizationName,
  busy,
  error,
  onClose,
  onConfirm,
}: {
  keyName: string;
  organizationName: string;
  /** True while any page action is outstanding; keeps confirmation disabled. */
  busy: boolean;
  /** Latest page action error; shown only after this dialog submitted. */
  error: unknown;
  onClose: () => void;
  onConfirm: () => void;
}) {
  useLocale();
  const id = useId();
  const leadId = `${id}-lead`;
  const identityId = `${id}-identity`;
  const consequenceId = `${id}-consequence`;
  const irreversibleId = `${id}-irreversible`;
  const [submitted, setSubmitted] = useState(false);
  const pending = submitted && busy;
  const visibleError = submitted && !busy ? error : undefined;
  const uncertain =
    visibleError !== undefined &&
    classifyRevokeFailure(visibleError) === RevokeFailureKind.Uncertain;
  return (
    <Dialog
      title={translate("revokeApiKeyTitle")}
      onClose={onClose}
      initialFocus="[data-cancel]"
      className="revoke-key-dialog"
      describedBy={`${leadId} ${identityId} ${consequenceId} ${irreversibleId}`}
    >
      <p className="revoke-key-lead" id={leadId}>
        {translate("thisKeyWillStopAcceptingNewRequests")}
      </p>
      <dl className="revoke-key-identity" id={identityId}>
        <div>
          <dt>{translate("apiKey")}</dt>
          <dd className="revoke-key-name">{keyName}</dd>
        </div>
        <div>
          <dt>{translate("organization")}</dt>
          <dd>{organizationName}</dd>
        </div>
      </dl>
      <p className="revoke-key-consequence" id={consequenceId}>
        {translate("revokeKeyConsequence")}
      </p>
      <p className="revoke-key-irreversible" id={irreversibleId}>
        {translate("revokeKeyIrreversible")}
      </p>
      {uncertain ? (
        <Notice tone="danger">
          {translate("revocationCouldNotBeConfirmed")}
        </Notice>
      ) : (
        <ErrorNotice error={visibleError} />
      )}
      <p className="revoke-key-status" role="status">
        {pending ? translate("revokingKeyClosingDoesNotCancel") : ""}
      </p>
      <div className="actions revoke-key-actions">
        <Button data-cancel onClick={onClose}>
          {translate("cancel")}
        </Button>
        <Button
          variant="danger"
          disabled={busy}
          onClick={() => {
            if (busy) return;
            setSubmitted(true);
            onConfirm();
          }}
        >
          {pending ? translate("revoking") : translate("revokeKey")}
        </Button>
      </div>
    </Dialog>
  );
}
