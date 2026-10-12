// SPDX-License-Identifier: Apache-2.0
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  EntityKind,
  MCPQuery,
  ErrorDetailSchema,
  FailureCode,
  MCPMutationState,
  ResourceQuery,
  SystemQuery,
  SystemCapability,
  newRequestId,
} from "@delinoio/delidev-api-client";
import {
  encode,
  items,
  object,
  resourceName,
  text,
  type Document,
} from "./documents";
import { copy, useLocale } from "./localization";
import { useRetainedMutation } from "./mutation";
import { Modal, Problem } from "./ui";
import "./mcp-settings.css";

export interface MCPDefinition {
  id: string;
  worker_device_id: string;
  revision: string;
  name: string;
  enabled: boolean;
  transport: "stdio" | "streamable-http";
  executable?: string;
  arguments?: string[];
  working_directory?: string;
  endpoint?: string;
  authentication: "none" | "required" | "authenticated";
  authentication_expires_at?: string;
  oauth?: {
    authorization_url: string;
    token_url: string;
    client_id: string;
    redirect_uri: string;
    scopes?: string[];
  };
  support: { harness: string; verified: boolean; profile?: string }[];
  agent_references?: { id: string; name: string }[];
}
export function decodeMCPDefinitions(
  raw: Uint8Array,
  worker?: string,
): MCPDefinition[] {
  if (raw.byteLength > 1 << 20)
    throw new Error("MCP inventory exceeds its bound.");
  const value: unknown = JSON.parse(
    new TextDecoder("utf-8", { fatal: true }).decode(raw),
  );
  if (!Array.isArray(value) || value.length > 256)
    throw new Error("Invalid MCP inventory.");
  const seen = new Set<string>();
  for (const entry of value) {
    if (
      !entry ||
      typeof entry.id !== "string" ||
      seen.has(entry.id) ||
      typeof entry.worker_device_id !== "string" ||
      (worker && entry.worker_device_id !== worker) ||
      typeof entry.revision !== "string" ||
      !/^[1-9][0-9]*$/.test(entry.revision) ||
      typeof entry.name !== "string" ||
      typeof entry.enabled !== "boolean" ||
      !["stdio", "streamable-http"].includes(entry.transport) ||
      !["none", "required", "authenticated"].includes(entry.authentication) ||
      !Array.isArray(entry.support) ||
      entry.support.some(
        (s: unknown) =>
          !s ||
          typeof object(s).harness !== "string" ||
          typeof object(s).verified !== "boolean",
      )
    )
      throw new Error("Invalid Worker-owned MCP projection.");
    seen.add(entry.id);
  }
  return value as MCPDefinition[];
}
function decodeResult(raw: Uint8Array) {
  const result = object(
    JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw)),
  );
  const problem = object(result.problem);
  const failure = Object.values(FailureCode).includes(
    text(problem.code) as FailureCode,
  )
    ? new ConnectError(text(problem.message), Code.Unknown, undefined, [
        {
          desc: ErrorDetailSchema,
          value: {
            code: text(problem.code),
            guidance: text(problem.guidance),
            correlationId: "",
          },
        },
      ])
    : undefined;
  return {
    problem: failure,
    request: text(result.request_id),
    state: text(result.state),
    definition: text(result.definition_id),
    url: text(result.authorization_url),
    definitions: decodeMCPDefinitions(
      encode(result.definitions),
      text(result.worker_device_id),
    ),
  };
}
const terminal = (state: MCPMutationState) =>
  state === MCPMutationState.MCP_MUTATION_STATE_SUCCEEDED ||
  state === MCPMutationState.MCP_MUTATION_STATE_CANCELED ||
  state === MCPMutationState.MCP_MUTATION_STATE_FAILED;

export function MCPSettings({ active }: { active: boolean }) {
  useLocale();
  const [machine, setMachine] = useState("");
  const [page, setPage] = useState("");
  const [machines, setMachines] = useState<{ id: string; name: string }[]>([]);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const runners = useQuery(
    ResourceQuery.listResources,
    { filter: { kind: EntityKind.MACHINE, pageSize: 200, pageToken: page } },
    { enabled: active },
  );
  useEffect(() => {
    if (!runners.data) return;
    setMachines((previous) => {
      const map = new Map(previous.map((v) => [v.id, v]));
      for (const r of runners.data!.resources)
        map.set(r.id, { id: r.id, name: resourceName(r) });
      return [...map.values()];
    });
  }, [runners.data]);
  const supported = status.data?.capabilities.includes(
    SystemCapability.MANAGED_MCP_V1,
  );
  return (
    <section className="mcp-settings">
      <label>
        {copy("mcp.runner")}
        <select
          value={machine}
          disabled={!active}
          onChange={(e) => setMachine(e.target.value)}
        >
          <option value="">{copy("mcp.selectRunner")}</option>
          {machines.map((r) => (
            <option key={r.id} value={r.id}>
              {r.name}
            </option>
          ))}
        </select>
      </label>
      {runners.data?.nextPageToken ? (
        <button
          type="button"
          disabled={runners.isFetching}
          onClick={() => setPage(runners.data!.nextPageToken)}
        >
          {copy("mcp.moreRunners")}
        </button>
      ) : null}
      <Problem error={status.error ?? runners.error} />
      {status.isPending ? (
        <p role="status">{copy("mcp.loading")}</p>
      ) : !supported ? (
        <p role="status">{copy("mcp.unsupported")}</p>
      ) : machine ? (
        <MCPCatalog key={machine} machine={machine} active={active} />
      ) : (
        <p>{copy("mcp.selectRunner")}</p>
      )}
    </section>
  );
}
function MCPCatalog({ machine, active }: { machine: string; active: boolean }) {
  useLocale();
  const [search, setSearch] = useState("");
  const [editing, setEditing] = useState<MCPDefinition | null>();
  const [auth, setAuth] = useState<MCPDefinition>();
  const [deleting, setDeleting] = useState<MCPDefinition>();
  const [receipt, setReceipt] = useState("");
  const [oauth, setOAuth] = useState<{
    request: string;
    url: string;
    definition: MCPDefinition;
  }>();
  const [callback, setCallback] = useState("");
  const [parseError, setParseError] = useState<unknown>();
  const trigger = useRef<HTMLButtonElement>(null);
  const inventory = useQuery(
    MCPQuery.listMCPDefinitions,
    { machineId: machine },
    { enabled: active },
  );
  let defs: MCPDefinition[] = [];
  let invalid: unknown;
  try {
    if (inventory.data)
      defs = decodeMCPDefinitions(
        inventory.data.definitionsJson,
        inventory.data.workerDeviceId,
      );
  } catch (error) {
    invalid = error;
  }
  const activeScope = useRef(active);
  activeScope.current = active;
  useEffect(
    () => () => {
      activeScope.current = false;
    },
    [],
  );
  const changed = () => {
    if (activeScope.current) void inventory.refetch();
  };
  const mutation = useRetainedMutation(
    `mcp:${machine}:edit`,
    MCPQuery.mutateMCPDefinition,
    (r) => {
      setParseError(decodeResult(r.resultJson).problem);
      setEditing(undefined);
      setDeleting(undefined);
      changed();
    },
    (r, q) => r.requestId === q.requestId && terminal(r.state),
    true,
  );
  const authentication = useRetainedMutation(
    `mcp:${machine}:auth`,
    MCPQuery.authenticateMCP,
    (r, q) => {
      const result = decodeResult(r.resultJson);
      setParseError(result.problem);
      const target =
        auth ?? result.definitions.find((d) => d.id === result.definition);
      if (result.url && target)
        setOAuth({ request: q.requestId, url: result.url, definition: target });
      if (terminal(r.state)) {
        setAuth(undefined);
        setOAuth(undefined);
      }
      changed();
    },
    (r, q) =>
      r.requestId === q.requestId &&
      (terminal(r.state) ||
        (r.state === MCPMutationState.MCP_MUTATION_STATE_PENDING &&
          Boolean(decodeResult(r.resultJson).url))),
    true,
  );
  const original = mutation.uncertain
    ? text(object(mutation.input).requestId)
    : authentication.uncertain
      ? text(object(authentication.input).requestId)
      : receipt;
  const inspection = useQuery(
    MCPQuery.getMCPMutation,
    { machineId: machine, requestId: original },
    { enabled: false },
  );
  const blocked =
    !active ||
    mutation.busy ||
    mutation.uncertain ||
    authentication.busy ||
    authentication.uncertain;
  async function inspect() {
    const response = await inspection.refetch();
    if (!activeScope.current || !response.data) return;
    const r = response.data;
    if (r.requestId !== original) return;
    if (terminal(r.state)) {
      mutation.observe(r);
      authentication.observe(r);
      changed();
    } else if (r.state === MCPMutationState.MCP_MUTATION_STATE_PENDING) {
      authentication.observe(r);
    }
  }
  const send = (data: Document) => {
    const requestId = newRequestId();
    setReceipt(requestId);
    void mutation.send({
      machineId: machine,
      requestId,
      documentJson: encode(data),
    });
  };
  const authenticate = (
    d: MCPDefinition,
    action: string,
    extra: Document = {},
  ) => {
    setAuth(d);
    const requestId = newRequestId();
    setReceipt(requestId);
    void authentication.send({
      machineId: machine,
      requestId,
      documentJson: encode({
        version: 1,
        action,
        definition_id: d.id,
        expected_revision: d.revision,
        ...extra,
      }),
    });
  };
  const dismiss = () => {
    setEditing(undefined);
    setAuth(undefined);
    setDeleting(undefined);
  };
  return (
    <>
      <div className="mcp-toolbar">
        <label>
          {copy("mcp.search")}
          <input
            type="search"
            value={search}
            disabled={!active}
            onChange={(e) => setSearch(e.target.value)}
          />
        </label>
        <SettingsActionButton
          icon={SettingsActionIcon.Add}
          ref={trigger}
          type="button"
          disabled={blocked}
          onClick={() => setEditing(null)}
        >
          {copy("mcp.add")}
        </SettingsActionButton>
      </div>
      <p>{copy("mcp.future")}</p>
      <Problem
        error={
          inventory.error ??
          invalid ??
          parseError ??
          mutation.error ??
          authentication.error ??
          inspection.error
        }
      />
      {original ? (
        <button
          type="button"
          disabled={
            !active ||
            inspection.isFetching ||
            mutation.busy ||
            authentication.busy
          }
          onClick={() => void inspect()}
        >
          {copy("mcp.inspect")}
        </button>
      ) : null}
      {mutation.uncertain || authentication.uncertain ? (
        <p role="status">{copy("mcp.uncertain")}</p>
      ) : null}
      {inventory.isPending ? (
        <p role="status">{copy("mcp.loading")}</p>
      ) : !inventory.error && !invalid && defs.length === 0 ? (
        <p>{copy("mcp.empty")}</p>
      ) : null}
      <div>
        {defs
          .filter((d) =>
            d.name.toLocaleLowerCase().includes(search.toLocaleLowerCase()),
          )
          .map((d) => (
            <article key={d.id} className="mcp-row" data-disabled={!d.enabled}>
              <div>
                <strong>{d.name}</strong>
                <p>
                  {d.transport === "stdio"
                    ? copy("mcp.stdio")
                    : copy("mcp.http")}{" "}
                  ·{" "}
                  {copy(
                    d.authentication === "authenticated" &&
                      (!d.authentication_expires_at ||
                        Date.parse(d.authentication_expires_at) > Date.now())
                      ? "mcp.authenticated"
                      : d.authentication !== "none"
                        ? "mcp.authenticationRequired"
                        : "mcp.noAuthentication",
                  )}
                </p>
                <p>
                  {["codex", "claude-code", "opencode", "grok-build"]
                    .map(
                      (h) =>
                        `${h}: ${copy(d.support.some((s) => s.harness === h && s.verified) ? "mcp.verified" : "mcp.unsupported")}`,
                    )
                    .join(" · ")}
                </p>
                <p>
                  {copy("mcp.references")}:{" "}
                  {d.agent_references?.map((r) => r.name).join(", ") ||
                    copy("mcp.noReferences")}
                </p>
              </div>
              <div className="mcp-actions">
                <SettingsActionButton
                  icon={SettingsActionIcon.Edit}
                  aria-label={`${copy("mcp.edit")} ${d.name}`}
                  type="button"
                  disabled={blocked}
                  onClick={() => setEditing(d)}
                >
                  {copy("mcp.edit")}
                </SettingsActionButton>
                <button
                  type="button"
                  disabled={blocked}
                  onClick={() =>
                    send({
                      version: 1,
                      action: "save",
                      definition_id: d.id,
                      expected_revision: d.revision,
                      definition: { ...d, enabled: !d.enabled },
                    })
                  }
                >
                  {copy(d.enabled ? "mcp.disable" : "mcp.enable")}
                </button>
                <SettingsActionButton
                  icon={SettingsActionIcon.Connect}
                  aria-label={`${copy("mcp.authenticate")} ${d.name}`}
                  type="button"
                  disabled={blocked}
                  onClick={() => setAuth(d)}
                >
                  {copy("mcp.authenticate")}
                </SettingsActionButton>
                <SettingsActionButton
                  icon={SettingsActionIcon.Delete}
                  aria-label={`${copy("mcp.delete")} ${d.name}`}
                  type="button"
                  disabled={blocked || Boolean(d.agent_references?.length)}
                  onClick={() => setDeleting(d)}
                >
                  {copy("mcp.delete")}
                </SettingsActionButton>
              </div>
            </article>
          ))}
      </div>
      {editing !== undefined ? (
        <Modal title={copy(editing ? "mcp.edit" : "mcp.add")} close={dismiss}>
          <MCPEditor
            initial={editing ?? undefined}
            busy={blocked}
            save={(value) => {
              try {
                send(value);
              } catch (e) {
                setParseError(e);
              }
            }}
            cancel={dismiss}
          />
        </Modal>
      ) : null}
      {deleting ? (
        <Modal title={copy("mcp.delete")} close={dismiss}>
          <p>{copy("mcp.deleteConfirm", { name: deleting.name })}</p>
          <button
            disabled={blocked}
            type="button"
            onClick={() =>
              send({
                version: 1,
                action: "delete",
                definition_id: deleting.id,
                expected_revision: deleting.revision,
                confirmed: true,
              })
            }
          >
            {copy("mcp.confirmDelete")}
          </button>
          <button type="button" onClick={dismiss}>
            {copy("mcp.cancel")}
          </button>
        </Modal>
      ) : null}
      {auth && !oauth ? (
        <Modal title={copy("mcp.authenticate")} close={dismiss}>
          <MCPManualAuth
            transport={auth.transport}
            busy={blocked}
            save={(secrets) =>
              authenticate(auth, "manual-authentication", { secrets })
            }
          />
          {auth.oauth ? (
            <button
              type="button"
              disabled={blocked}
              onClick={() => authenticate(auth, "oauth-start")}
            >
              {copy("mcp.oauthStart")}
            </button>
          ) : null}
        </Modal>
      ) : null}
      {oauth ? (
        <Modal
          title={copy("mcp.oauth")}
          close={() => {
            setOAuth(undefined);
            dismiss();
          }}
        >
          <p>{copy("mcp.oauthInstructions")}</p>
          <a href={oauth.url} target="_blank" rel="noreferrer">
            {copy("mcp.oauthOpen")}
          </a>
          <label>
            {copy("mcp.callback")}
            <input
              autoFocus
              value={callback}
              onChange={(e) => setCallback(e.target.value)}
              disabled={blocked}
            />
          </label>
          <button
            type="button"
            disabled={blocked || !callback}
            onClick={() =>
              authenticate(oauth.definition, "oauth-callback", {
                original_request_id: oauth.request,
                callback_uri: callback,
              })
            }
          >
            {copy("mcp.oauthComplete")}
          </button>
          <button
            type="button"
            disabled={blocked}
            onClick={() =>
              authenticate(oauth.definition, "oauth-cancel", {
                original_request_id: oauth.request,
              })
            }
          >
            {copy("mcp.oauthCancel")}
          </button>
        </Modal>
      ) : null}
    </>
  );
}
function SecretInputs({
  value,
  set,
}: {
  value: { name: string; value: string }[];
  set: (v: { name: string; value: string }[]) => void;
}) {
  return (
    <div>
      {value.map((entry, i) => (
        <div className="mcp-secret" key={i}>
          <input
            aria-label={copy("mcp.secretName")}
            value={entry.name}
            onChange={(e) =>
              set(
                value.map((v, n) =>
                  n === i ? { ...v, name: e.target.value } : v,
                ),
              )
            }
          />
          <input
            type="password"
            autoComplete="new-password"
            aria-label={copy("mcp.secretValue")}
            value={entry.value}
            onChange={(e) =>
              set(
                value.map((v, n) =>
                  n === i ? { ...v, value: e.target.value } : v,
                ),
              )
            }
          />
        </div>
      ))}
      <button
        type="button"
        onClick={() => set([...value, { name: "", value: "" }])}
      >
        {copy("mcp.addSecret")}
      </button>
    </div>
  );
}
function MCPManualAuth({
  transport,
  busy,
  save,
}: {
  transport: string;
  busy: boolean;
  save: (secrets: Document) => void;
}) {
  const [token, setToken] = useState("");
  const [values, setValues] = useState<{ name: string; value: string }[]>([]);
  return (
    <form
      className="mcp-form"
      onSubmit={(e) => {
        e.preventDefault();
        save({
          ...(transport === "streamable-http" ? { access_token: token } : {}),
          [transport === "stdio" ? "environment" : "headers"]:
            Object.fromEntries(
              values.filter((v) => v.name).map((v) => [v.name, v.value]),
            ),
        });
      }}
    >
      <fieldset disabled={busy}>
        {transport === "streamable-http" ? (
          <label>
            {copy("mcp.token")}
            <input
              autoFocus
              type="password"
              autoComplete="new-password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
            />
          </label>
        ) : null}
        <SecretInputs value={values} set={setValues} />
        <button type="submit">{copy("mcp.authenticate")}</button>
      </fieldset>
    </form>
  );
}
function MCPEditor({
  initial,
  busy,
  save,
  cancel,
}: {
  initial?: MCPDefinition;
  busy: boolean;
  save: (value: Document) => void;
  cancel: () => void;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [transport, setTransport] = useState(initial?.transport ?? "stdio");
  const [executable, setExecutable] = useState(initial?.executable ?? "");
  const [argumentsText, setArguments] = useState(
    JSON.stringify(initial?.arguments ?? []),
  );
  const [cwd, setCwd] = useState(initial?.working_directory ?? "");
  const [endpoint, setEndpoint] = useState(initial?.endpoint ?? "");
  const [oauth, setOAuth] = useState(Boolean(initial?.oauth));
  const [authorizationURL, setAuthorizationURL] = useState(
    initial?.oauth?.authorization_url ?? "",
  );
  const [tokenURL, setTokenURL] = useState(initial?.oauth?.token_url ?? "");
  const [clientID, setClientID] = useState(initial?.oauth?.client_id ?? "");
  const [redirectURI, setRedirectURI] = useState(
    initial?.oauth?.redirect_uri ?? "http://127.0.0.1/callback",
  );
  const [scopes, setScopes] = useState(initial?.oauth?.scopes?.join(" ") ?? "");
  const [error, setError] = useState<unknown>();
  return (
    <form
      className="mcp-form"
      onSubmit={(e) => {
        e.preventDefault();
        try {
          const definition: Document = {
            name,
            transport,
            enabled: initial?.enabled ?? true,
            support: [],
          };
          if (transport === "stdio") {
            definition.executable = executable;
            definition.arguments = JSON.parse(argumentsText);
            definition.working_directory = cwd;
          } else {
            definition.endpoint = endpoint;
            if (oauth)
              definition.oauth = {
                authorization_url: authorizationURL,
                token_url: tokenURL,
                client_id: clientID,
                redirect_uri: redirectURI,
                scopes: scopes.split(/\s+/).filter(Boolean),
              };
          }
          const value: Document = {
            version: 1,
            action: "save",
            definition_id: initial?.id ?? newRequestId(),
            expected_revision: initial?.revision ?? "0",
            definition,
          };
          save(value);
        } catch (error) {
          setError(error);
        }
      }}
    >
      <fieldset disabled={busy}>
        <label>
          {copy("mcp.name")}
          <input
            autoFocus
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <label>
          {copy("mcp.transport")}
          <select
            value={transport}
            onChange={(e) =>
              setTransport(e.target.value as "stdio" | "streamable-http")
            }
          >
            <option value="stdio">{copy("mcp.stdio")}</option>
            <option value="streamable-http">{copy("mcp.http")}</option>
          </select>
        </label>
        {transport === "stdio" ? (
          <>
            <label>
              {copy("mcp.executable")}
              <input
                required
                value={executable}
                onChange={(e) => setExecutable(e.target.value)}
              />
            </label>
            <label>
              {copy("mcp.arguments")}
              <textarea
                value={argumentsText}
                onChange={(e) => setArguments(e.target.value)}
              />
            </label>
            <label>
              {copy("mcp.cwd")}
              <input value={cwd} onChange={(e) => setCwd(e.target.value)} />
            </label>
          </>
        ) : (
          <>
            <label>
              {copy("mcp.endpoint")}
              <input
                type="url"
                required
                value={endpoint}
                onChange={(e) => setEndpoint(e.target.value)}
              />
            </label>
            <label>
              <input
                type="checkbox"
                checked={oauth}
                onChange={(e) => setOAuth(e.target.checked)}
              />
              {copy("mcp.oauth")}
            </label>
            {oauth ? (
              <>
                <label>
                  {copy("mcp.authorizationURL")}
                  <input
                    type="url"
                    required
                    value={authorizationURL}
                    onChange={(e) => setAuthorizationURL(e.target.value)}
                  />
                </label>
                <label>
                  {copy("mcp.tokenURL")}
                  <input
                    type="url"
                    required
                    value={tokenURL}
                    onChange={(e) => setTokenURL(e.target.value)}
                  />
                </label>
                <label>
                  {copy("mcp.clientID")}
                  <input
                    required
                    value={clientID}
                    onChange={(e) => setClientID(e.target.value)}
                  />
                </label>
                <label>
                  {copy("mcp.redirectURI")}
                  <input
                    required
                    value={redirectURI}
                    onChange={(e) => setRedirectURI(e.target.value)}
                  />
                </label>
                <label>
                  {copy("mcp.scopes")}
                  <input
                    value={scopes}
                    onChange={(e) => setScopes(e.target.value)}
                  />
                </label>
              </>
            ) : null}
          </>
        )}
        <p>{copy("mcp.writeOnly")}</p>
        <Problem error={error} />
        <button type="submit">{copy("mcp.save")}</button>
        <button type="button" onClick={cancel}>
          {copy("mcp.cancel")}
        </button>
      </fieldset>
    </form>
  );
}
export function MCPAgentSelections({
  value,
  onChange,
  disabled,
  active,
}: {
  value: unknown;
  onChange: (v: Document) => void;
  disabled: boolean;
  active: boolean;
}) {
  useLocale();
  const [machine, setMachine] = useState("");
  const [page, setPage] = useState("");
  const [machines, setMachines] = useState<{ id: string; name: string }[]>([]);
  const runners = useQuery(
    ResourceQuery.listResources,
    { filter: { kind: EntityKind.MACHINE, pageSize: 200, pageToken: page } },
    { enabled: active },
  );
  useEffect(() => {
    if (!runners.data) return;
    setMachines((previous) => {
      const map = new Map(previous.map((v) => [v.id, v]));
      for (const r of runners.data!.resources)
        map.set(r.id, { id: r.id, name: resourceName(r) });
      return [...map.values()];
    });
  }, [runners.data]);
  const list = useQuery(
    MCPQuery.listMCPDefinitions,
    { machineId: machine },
    { enabled: active && Boolean(machine) },
  );
  const selected = items(object(value).selections).map(object);
  let defs: MCPDefinition[] = [];
  let error: unknown;
  try {
    if (list.data)
      defs = decodeMCPDefinitions(
        list.data.definitionsJson,
        list.data.workerDeviceId,
      );
  } catch (e) {
    error = e;
  }
  return (
    <fieldset disabled={disabled || !active}>
      <legend>{copy("mcp.selections")}</legend>
      <p>{copy("mcp.future")}</p>
      <label>
        {copy("mcp.runner")}
        <select value={machine} onChange={(e) => setMachine(e.target.value)}>
          <option value="">{copy("mcp.selectRunner")}</option>
          {machines.map((r) => (
            <option key={r.id} value={r.id}>
              {r.name}
            </option>
          ))}
        </select>
      </label>
      {runners.data?.nextPageToken ? (
        <button
          type="button"
          disabled={runners.isFetching}
          onClick={() => setPage(runners.data!.nextPageToken)}
        >
          {copy("mcp.moreRunners")}
        </button>
      ) : null}
      <Problem error={runners.error ?? list.error ?? error} />
      {defs.map((d) => (
        <label key={d.id}>
          <input
            type="checkbox"
            checked={selected.some(
              (s) =>
                s.definition_id === d.id &&
                s.worker_device_id === d.worker_device_id,
            )}
            onChange={(e) =>
              onChange({
                selections: e.target.checked
                  ? [
                      ...selected,
                      {
                        definition_id: d.id,
                        worker_device_id: d.worker_device_id,
                        name: d.name,
                      },
                    ]
                  : selected.filter(
                      (s) =>
                        s.definition_id !== d.id ||
                        s.worker_device_id !== d.worker_device_id,
                    ),
              })
            }
          />
          {d.name}
          {!d.enabled ? ` · ${copy("mcp.disabled")}` : ""}
        </label>
      ))}
      {selected.map((s, index) => (
        <div key={`${text(s.definition_id)}:${index}`}>
          <span>{text(s.name) || copy("mcp.selectedDefinition")}</span>
          <button
            type="button"
            onClick={() =>
              onChange({ selections: selected.filter((_, i) => i !== index) })
            }
          >
            {copy("mcp.unselect")}
          </button>
          {s.unresolved ? (
            <label>
              {copy("mcp.rebind")}
              <select
                value=""
                onChange={(e) => {
                  const d = defs.find((v) => v.id === e.target.value);
                  if (d)
                    onChange({
                      selections: selected.map((v, i) =>
                        i === index
                          ? {
                              definition_id: d.id,
                              worker_device_id: d.worker_device_id,
                              name: d.name,
                            }
                          : v,
                      ),
                    });
                }}
              >
                <option value="">{copy("mcp.chooseReplacement")}</option>
                {defs
                  .filter(
                    (d) => !selected.some((v) => v.definition_id === d.id),
                  )
                  .map((d) => (
                    <option key={d.id} value={d.id}>
                      {d.name}
                    </option>
                  ))}
              </select>
            </label>
          ) : null}
        </div>
      ))}
      <button type="button" onClick={() => onChange({ selections: [] })}>
        {copy("mcp.clearSelections")}
      </button>
    </fieldset>
  );
}
