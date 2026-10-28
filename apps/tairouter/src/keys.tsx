// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, enumLabel } from "./i18n";
import { useEffect, useId, useRef, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import { timestampFromDate, type Timestamp } from "@bufbuild/protobuf/wkt";
import {
  KeyService,
  KeyState,
  KeyKind,
  type InferenceKey,
} from "@tairouter/client";
import { useApi, type PageProps, pageSize } from "./api";
import { useAction } from "./actions";
import {
  PageHeader,
  Button,
  Field,
  Dialog,
  ConfirmDialog,
  ErrorNotice,
  Loading,
  EmptyState,
  Badge,
  CopyButton,
  Notice,
  RefreshStatus,
  useOnline,
  formatDate,
  Pagination,
} from "./ui";
/** Presentation tone for the server-authoritative key state. */
function keyStateTone(state: KeyState): "success" | "neutral" {
  return state === KeyState.ACTIVE ? "success" : "neutral";
}
/** Machine-readable UTC value for a returned timestamp, when it is representable. */
function utcIso(value: Timestamp): string | undefined {
  const date = new Date(Number(value.seconds) * 1000);
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString();
}
/** A UTC date-time with a machine-readable value; a missing value reads Unknown. */
function KeyTime({ value }: { value?: Timestamp }) {
  useLocale();
  const iso = value ? utcIso(value) : undefined;
  return iso ? (
    <time dateTime={iso}>{formatDate(value)}</time>
  ) : (
    <>{formatDate(value)}</>
  );
}
// The key list remounts per organization, so rows, dialogs, the raw secret,
// the page cursor and action state never carry over into another scope.
export function KeysPage(props: PageProps) {
  return <KeysScope key={props.organization.id} {...props} />;
}
function KeysScope({ organization }: PageProps) {
  useLocale();
  const api = useApi();
  const action = useAction();
  const headingId = useId();
  const [token, setToken] = useState("");
  const query = useQuery(KeyService.method.listKeys, {
    organizationId: organization.id,
    page: { pageSize, pageToken: token },
  });
  const [create, setCreate] = useState(false);
  const [revoke, setRevoke] = useState<InferenceKey>();
  const [raw, setRaw] = useState("");
  // Timestamp of a confirmed revoke whose Revoke button may disappear on refetch.
  const [restoreFocus, setRestoreFocus] = useState<number>();
  const collectionHeading = useRef<HTMLHeadingElement>(null);
  // The dialog returns focus to the Revoke button that opened it; once the
  // refetch removes that button, move focus to the surviving collection heading.
  useEffect(() => {
    if (restoreFocus === undefined) return;
    const active = document.activeElement;
    if (!active || active === document.body || !active.isConnected) {
      collectionHeading.current?.focus();
      setRestoreFocus(undefined);
    } else if (
      query.fetchStatus === "idle" &&
      query.dataUpdatedAt >= restoreFocus
    ) {
      setRestoreFocus(undefined);
    }
  }, [restoreFocus, query.data, query.fetchStatus, query.dataUpdatedAt]);
  const online = useOnline();
  const listCode = query.error
    ? ConnectError.from(query.error).code
    : undefined;
  const accessDenied =
    listCode === Code.PermissionDenied || listCode === Code.Unauthenticated;
  const keys = accessDenied ? undefined : query.data?.keys;
  return (
    <>
      <PageHeader
        title={translate("apiKeys")}
        description={translate("yourKeysIn", { value0: organization.name })}
        action={
          <Button variant="primary" onClick={() => setCreate(true)}>
            {translate("createApiKey")}
          </Button>
        }
      />
      <Notice>{translate("newKeysAreShownOnlyOnceStore")}</Notice>
      {action.feedback}
      {accessDenied ? (
        <Notice tone="warning">
          {translate("youNoLongerHaveAccessToTheseApiKeys")}
        </Notice>
      ) : (
        <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      )}
      {query.error && query.data && !accessDenied && (
        <Notice tone="warning">
          {translate("showingTheLastLoadedApiKeysTheLatest")}
        </Notice>
      )}
      {!online && query.data && !accessDenied && (
        <Notice tone="warning">
          {translate("youAreOfflineTheseApiKeysMayBeOut")}
        </Notice>
      )}
      <RefreshStatus updating={!!keys && query.isFetching && !query.isError} />
      <div className="keys-collection">
        <h2
          id={headingId}
          ref={collectionHeading}
          tabIndex={-1}
          className="visually-hidden"
        >
          {translate("yourApiKeys")}
        </h2>
        {query.isPending ? (
          <Loading />
        ) : keys?.length === 0 ? (
          <EmptyState title={translate("createYourFirstApiKey")}>
            {translate("keysConnectYourClientsToYourPersonal")}
          </EmptyState>
        ) : keys ? (
          <KeysTable keys={keys} headingId={headingId} onRevoke={setRevoke} />
        ) : null}
      </div>
      <Pagination
        label={translate("apiKeyPages")}
        next={query.data?.page?.nextPageToken}
        onNext={setToken}
        onFirst={() => setToken("")}
        hasPrevious={!!token}
      />
      {create && (
        <CreateKey
          onClose={() => setCreate(false)}
          pending={action.pending}
          error={action.error}
          onSave={(name, days) => {
            void action.run(
              () =>
                api.key.createKey({
                  organizationId: organization.id,
                  name,
                  expiresAt: days
                    ? timestampFromDate(new Date(Date.now() + days * 86400000))
                    : undefined,
                }),
              (result) => {
                setCreate(false);
                setRaw(result.rawKey);
              },
              translate("apiKeyCreated"),
            );
          }}
        />
      )}
      {raw && (
        <ApiKeyResult
          organizationName={organization.name}
          rawKey={raw}
          onClose={() => setRaw("")}
        />
      )}
      {revoke && (
        <ConfirmDialog
          title={translate("revoke_c771c3", { value0: revoke.name })}
          confirmLabel={translate("revokeKey")}
          onClose={() => setRevoke(undefined)}
          pending={action.pending}
          error={action.error}
          onConfirm={() => {
            void action.run(
              () =>
                api.key.revokeKey({
                  organizationId: organization.id,
                  keyId: revoke.id,
                }),
              () => {
                setRevoke(undefined);
                setRestoreFocus(Date.now());
              },
              translate("keyRevoked"),
            );
          }}
        >
          {translate("revokeThisKeyInNewRequestsStop", {
            value0: organization.name,
          })}
        </ConfirmDialog>
      )}
    </>
  );
}
/**
 * One semantic table at every width. Below the collection's container query the
 * stylesheet reflows each row into a labeled card, so only one set of focusable
 * controls exists; ARIA roles keep the table semantics when display changes.
 */
function KeysTable({
  keys,
  headingId,
  onRevoke,
}: {
  keys: InferenceKey[];
  headingId: string;
  onRevoke: (key: InferenceKey) => void;
}) {
  useLocale();
  const idPrefix = useId();
  const columns = {
    name: translate("name"),
    type: translate("type"),
    created: translate("createdColumn"),
    expires: translate("expires_f6725f"),
    status: translate("status"),
  };
  return (
    <table className="keys-table" role="table" aria-labelledby={headingId}>
      <thead role="rowgroup">
        <tr role="row">
          <th scope="col" role="columnheader">
            {columns.name}
          </th>
          <th scope="col" role="columnheader">
            {columns.type}
          </th>
          <th scope="col" role="columnheader">
            {columns.created}
          </th>
          <th scope="col" role="columnheader">
            {columns.expires}
          </th>
          <th scope="col" role="columnheader">
            {columns.status}
          </th>
          <th scope="col" role="columnheader">
            <span className="visually-hidden">{translate("actions")}</span>
          </th>
        </tr>
      </thead>
      <tbody role="rowgroup">
        {keys.map((key) => {
          const identityId = `${idPrefix}-${key.id}-identity`;
          return (
            <tr key={key.id} role="row">
              <td role="cell" data-label={columns.name} className="key-name">
                <strong>{key.name}</strong>
                {key.prefix ? (
                  <code id={identityId}>{key.prefix}••••</code>
                ) : (
                  <span id={identityId} className="key-identifier-missing">
                    {translate("identifierUnavailable")}
                  </span>
                )}
              </td>
              <td role="cell" data-label={columns.type}>
                {key.kind === KeyKind.CLI ? "CLI" : translate("personal")}
              </td>
              <td role="cell" data-label={columns.created}>
                <KeyTime value={key.createdAt} />
              </td>
              <td role="cell" data-label={columns.expires}>
                {key.expiresAt ? (
                  <KeyTime value={key.expiresAt} />
                ) : (
                  translate("noExpiry")
                )}
              </td>
              <td role="cell" data-label={columns.status}>
                <Badge tone={keyStateTone(key.state)}>
                  {enumLabel(KeyState[key.state])}
                </Badge>
              </td>
              <td role="cell" className="key-actions">
                {key.state === KeyState.ACTIVE && (
                  <Button
                    variant="danger"
                    aria-label={translate("revokeNamedKey", {
                      value0: key.name,
                    })}
                    aria-describedby={identityId}
                    onClick={() => onRevoke(key)}
                  >
                    {translate("revoke")}
                  </Button>
                )}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
// One-time result of ordinary key issuance. The raw value lives only in the
// owning page state and is never rendered into a live region.
function ApiKeyResult({
  organizationName,
  rawKey,
  onClose,
}: {
  organizationName: string;
  rawKey: string;
  onClose: () => void;
}) {
  useLocale();
  const id = useId();
  const warningId = `${id}-warning`;
  const helperId = `${id}-helper`;
  const statusId = `${id}-status`;
  return (
    <Dialog
      title={translate("yourNewApiKey")}
      onClose={onClose}
      className="key-result"
    >
      <p className="key-result-context">
        {translate("yourKeyIn", { value0: organizationName })}
      </p>
      <div className="key-result-warning" id={warningId}>
        <span aria-hidden="true" className="key-result-icon">
          !
        </span>
        <p>
          <strong>{translate("thisKeyIsShownOnlyOnce")}</strong>{" "}
          {translate("storeItSecurelyBeforeClosingThisDialog")}
        </p>
      </div>
      <Field label={translate("apiKey")}>
        <input
          type="text"
          readOnly
          autoFocus
          spellCheck={false}
          autoComplete="off"
          className="key-result-input"
          value={rawKey}
          aria-describedby={`${warningId} ${helperId}`}
        />
      </Field>
      <div className="key-result-copy">
        <CopyButton
          value={rawKey}
          label={translate("copyApiKey")}
          statusId={statusId}
        />
      </div>
      <p className="key-result-helper" id={helperId}>
        {translate("youCannotViewThisKeyAgainAfterClosing")}
      </p>
      <div className="actions key-result-footer">
        <Button variant="primary" onClick={onClose}>
          {translate("done")}
        </Button>
      </div>
    </Dialog>
  );
}
function CreateKey({
  onClose,
  onSave,
  pending,
  error,
}: {
  onClose: () => void;
  onSave: (name: string, days: number) => void;
  pending: boolean;
  error: unknown;
}) {
  useLocale();
  const errorId = useId();
  const [name, setName] = useState("");
  const [days, setDays] = useState(30);
  // The shared action state can hold a pending or failed revoke from before this dialog opened.
  // Only surface state that belongs to a submission made from this form instance.
  const [submitted, setSubmitted] = useState(false);
  const submitting = submitted && pending;
  const visibleError = submitted && !pending ? error : undefined;
  return (
    <Dialog
      title={translate("createApiKey")}
      onClose={onClose}
      className="create-key-dialog"
    >
      <p className="create-key-guidance">
        {translate("createKeyNameGuidance")}
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const trimmed = name.trim();
          if (pending || !trimmed) return;
          setSubmitted(true);
          onSave(trimmed, days);
        }}
      >
        <Field
          label={translate("name")}
          hint={translate("createKeyNameHelper")}
          describedBy={visibleError ? errorId : undefined}
        >
          <input
            autoFocus
            required
            maxLength={100}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field
          label={translate("expiry")}
          hint={translate("createKeyExpiryHelper")}
        >
          <select
            value={days}
            onChange={(e) => setDays(Number(e.target.value))}
          >
            <option value={7}>{translate("message7Days")}</option>
            <option value={30}>{translate("message30Days")}</option>
            <option value={90}>{translate("message90Days")}</option>
            <option value={0}>{translate("noExpiry")}</option>
          </select>
        </Field>
        <p className="create-key-guidance">
          {translate("createKeyRevokeGuidance")}
        </p>
        <div id={errorId} className="create-key-error">
          <ErrorNotice error={visibleError} />
        </div>
        <p className="create-key-status" role="status">
          {submitting ? translate("createKeyPendingStatus") : ""}
        </p>
        <div className="actions create-key-actions">
          <Button onClick={onClose}>{translate("cancel")}</Button>
          <Button
            type="submit"
            variant="primary"
            disabled={pending || !name.trim()}
          >
            {submitting
              ? translate("message_c79ed9492e")
              : translate("createApiKey")}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
