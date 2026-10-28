// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, enumLabel } from "./i18n";
import { useId, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
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
  Card,
  Field,
  Dialog,
  ConfirmDialog,
  ErrorNotice,
  Loading,
  EmptyState,
  Badge,
  CopyButton,
  Notice,
  formatDate,
  Pagination,
} from "./ui";
export function KeysPage({ organization }: PageProps) {
  useLocale();
  const api = useApi();
  const action = useAction();
  const [token, setToken] = useState("");
  const query = useQuery(KeyService.method.listKeys, {
    organizationId: organization.id,
    page: { pageSize, pageToken: token },
  });
  const [create, setCreate] = useState(false);
  const [revoke, setRevoke] = useState<InferenceKey>();
  const [raw, setRaw] = useState("");
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
      <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <Loading />
      ) : query.data?.keys.length === 0 ? (
        <EmptyState title={translate("createYourFirstApiKey")}>
          {translate("keysConnectYourClientsToYourPersonal")}
        </EmptyState>
      ) : (
        <div className="records">
          {query.data?.keys.map((key) => (
            <Card key={key.id} className="record">
              <div>
                <h2>{key.name}</h2>
                <code>{key.prefix}••••</code>
                <p>
                  {translate("created", {
                    value0:
                      key.kind === KeyKind.CLI ? "CLI" : translate("personal"),
                    value1: formatDate(key.createdAt),
                  })}
                </p>
                <p>
                  {translate("expires", {
                    value0: key.expiresAt
                      ? formatDate(key.expiresAt)
                      : translate("noExpiry"),
                  })}
                </p>
              </div>
              <div className="actions">
                <Badge
                  tone={key.state === KeyState.ACTIVE ? "success" : "neutral"}
                >
                  {enumLabel(KeyState[key.state])}
                </Badge>
                {key.state === KeyState.ACTIVE && (
                  <Button variant="danger" onClick={() => setRevoke(key)}>
                    {translate("revoke")}
                  </Button>
                )}
              </div>
            </Card>
          ))}
        </div>
      )}
      <Pagination
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
              () => setRevoke(undefined),
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
