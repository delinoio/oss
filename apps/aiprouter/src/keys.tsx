// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, enumLabel } from "./i18n";
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  KeyService,
  KeyState,
  KeyKind,
  type InferenceKey,
} from "@aiprouter/client";
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
        <Dialog title={translate("yourNewApiKey")} onClose={() => setRaw("")}>
          <Notice tone="warning">{translate("thisKeyIsShownOnlyOnce")}</Notice>
          <Field label={translate("apiKey")}>
            <input readOnly value={raw} autoFocus />
          </Field>
          <CopyButton value={raw} label={translate("copyApiKey")} />
          <div className="actions">
            <Button variant="primary" onClick={() => setRaw("")}>
              {translate("done")}
            </Button>
          </div>
        </Dialog>
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
  const [name, setName] = useState("");
  const [days, setDays] = useState(30);
  return (
    <Dialog title={translate("createApiKey")} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          onSave(name.trim(), days);
        }}
      >
        <Field label={translate("name")}>
          <input
            autoFocus
            required
            maxLength={100}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field label={translate("expiry")}>
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
        <ErrorNotice error={error} />
        <div className="actions">
          <Button onClick={onClose}>{translate("cancel")}</Button>
          <Button
            type="submit"
            variant="primary"
            disabled={pending || !name.trim()}
          >
            {pending
              ? translate("message_c79ed9492e")
              : translate("createApiKey")}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
