// SPDX-License-Identifier: Apache-2.0
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
        title="API keys"
        description={`Your keys in ${organization.name}.`}
        action={
          <Button variant="primary" onClick={() => setCreate(true)}>
            Create API key
          </Button>
        }
      />
      <Notice>New keys are shown only once. Store them securely.</Notice>
      {action.feedback}
      <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <Loading />
      ) : query.data?.keys.length === 0 ? (
        <EmptyState title="Create your first API key">
          Keys connect your clients to your personal routing.
        </EmptyState>
      ) : (
        <div className="records">
          {query.data?.keys.map((key) => (
            <Card key={key.id} className="record">
              <div>
                <h2>{key.name}</h2>
                <code>{key.prefix}••••</code>
                <p>
                  {key.kind === KeyKind.CLI ? "CLI" : "Personal"} · Created{" "}
                  {formatDate(key.createdAt)}
                </p>
                <p>
                  Expires:{" "}
                  {key.expiresAt ? formatDate(key.expiresAt) : "No expiry"}
                </p>
              </div>
              <div className="actions">
                <Badge
                  tone={key.state === KeyState.ACTIVE ? "success" : "neutral"}
                >
                  {KeyState[key.state]?.toLowerCase() ?? "Unknown"}
                </Badge>
                {key.state === KeyState.ACTIVE && (
                  <Button variant="danger" onClick={() => setRevoke(key)}>
                    Revoke
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
              "API key created.",
            );
          }}
        />
      )}
      {raw && (
        <Dialog title="Your new API key" onClose={() => setRaw("")}>
          <Notice tone="warning">This key is shown only once.</Notice>
          <Field label="API key">
            <input readOnly value={raw} autoFocus />
          </Field>
          <CopyButton value={raw} label="Copy API key" />
          <div className="actions">
            <Button variant="primary" onClick={() => setRaw("")}>
              Done
            </Button>
          </div>
        </Dialog>
      )}
      {revoke && (
        <ConfirmDialog
          title={`Revoke ${revoke.name}?`}
          confirmLabel="Revoke key"
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
              "Key revoked.",
            );
          }}
        >
          Revoke this key in {organization.name}. New requests stop immediately.
          Responses already sent to a provider may finish.
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
  const [name, setName] = useState("");
  const [days, setDays] = useState(30);
  return (
    <Dialog title="Create API key" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          onSave(name.trim(), days);
        }}
      >
        <Field label="Name">
          <input
            autoFocus
            required
            maxLength={100}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field label="Expiry">
          <select
            value={days}
            onChange={(e) => setDays(Number(e.target.value))}
          >
            <option value={7}>7 days</option>
            <option value={30}>30 days</option>
            <option value={90}>90 days</option>
            <option value={0}>No expiry</option>
          </select>
        </Field>
        <ErrorNotice error={error} />
        <div className="actions">
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            variant="primary"
            disabled={pending || !name.trim()}
          >
            {pending ? "Creating…" : "Create API key"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
