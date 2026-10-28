// SPDX-License-Identifier: Apache-2.0
import { useId } from "react";
import type { Route } from "@tairouter/client";
import { t as translate, useLocale } from "./i18n";
import { Button, Dialog, ErrorNotice } from "./ui";

/**
 * Route deletion confirmation. Local to the routing page so the shared ConfirmDialog consumers keep their layout.
 * The alias and organization name render as escaped text and are never truncated or substituted.
 */
export function RouteDeleteDialog({
  route,
  organizationName,
  pending,
  error,
  onClose,
  onConfirm,
}: {
  route: Route;
  organizationName: string;
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onConfirm: () => void;
}) {
  useLocale();
  const descriptionId = useId();
  return (
    <Dialog
      title={translate("deleteRoutePrompt")}
      subtitle={translate("onlyYoursIn", { value0: organizationName })}
      onClose={onClose}
      initialFocus="[data-cancel]"
      className="route-delete-dialog"
      describedBy={descriptionId}
    >
      <div id={descriptionId} className="route-delete-content">
        <dl className="route-delete-alias">
          <dt>{translate("routeAlias")}</dt>
          <dd>{route.modelId}</dd>
        </dl>
        <p>{translate("newRequestsForThisAliasCanNo")}</p>
        <p className="route-delete-retained">
          {translate("providerConnectionsAndPoolsRemainAvailable")}
        </p>
      </div>
      <ErrorNotice error={error} />
      <div className="actions route-delete-footer">
        <Button data-cancel onClick={onClose}>
          {translate("cancel")}
        </Button>
        <Button variant="danger" disabled={pending} onClick={onConfirm}>
          {pending
            ? translate("message_23e39291d6")
            : translate("deleteRoute_032ad8")}
        </Button>
      </div>
    </Dialog>
  );
}
