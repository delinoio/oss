// SPDX-License-Identifier: Apache-2.0
import { useId } from "react";
import type { Pool } from "@tairouter/client";
import { t as translate, useLocale } from "./i18n";
import { pageHref } from "./routes";
import { Button, Dialog, ErrorNotice } from "./ui";

/**
 * Pool deletion confirmation. Local to the Account pools page so the shared ConfirmDialog consumers keep their layout.
 * The pool and organization names render as escaped text and are never truncated or substituted.
 * Only the explicit confirm action deletes; the Review routing link is a plain same-tab anchor.
 */
export function PoolDeleteDialog({
  pool,
  organizationId,
  organizationName,
  pending,
  error,
  onClose,
  onConfirm,
}: {
  pool: Pool;
  organizationId: string;
  organizationName: string;
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onConfirm: () => void;
}) {
  useLocale();
  const consequenceId = useId();
  return (
    <Dialog
      title={translate("delete_3d83de", { value0: pool.name })}
      subtitle={translate("onlyYoursIn", { value0: organizationName })}
      onClose={onClose}
      initialFocus="[data-cancel]"
      className="pool-delete-dialog"
      describedBy={consequenceId}
    >
      <div className="pool-delete-content">
        <dl className="pool-delete-context">
          <div>
            <dt>{translate("poolLabel")}</dt>
            <dd>{pool.name}</dd>
          </div>
          <div>
            <dt>{translate("organization")}</dt>
            <dd>{organizationName}</dd>
          </div>
        </dl>
        <div id={consequenceId} className="pool-delete-consequences">
          <p>{translate("connectionsRemainAvailable")}</p>
          <p>{translate("routesUsingThisPoolMustBeRepaired")}</p>
        </div>
        <a
          className="pool-delete-link"
          href={pageHref("routing", organizationId)}
        >
          {translate("reviewRouting")}
        </a>
      </div>
      <ErrorNotice error={error} />
      {/* Polite pending status for the modal; the page-level status is inert behind the dialog. */}
      <p className="visually-hidden" role="status">
        {pending ? translate("message_23e39291d6") : ""}
      </p>
      <div className="actions pool-delete-footer">
        <Button data-cancel onClick={onClose}>
          {translate("cancel")}
        </Button>
        <Button variant="danger" disabled={pending} onClick={onConfirm}>
          {pending ? translate("message_23e39291d6") : translate("deletePool")}
        </Button>
      </div>
    </Dialog>
  );
}
