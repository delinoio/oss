// SPDX-License-Identifier: Apache-2.0
import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { create, type Message } from "@bufbuild/protobuf";
import { createClient } from "@connectrpc/connect";
import {
  QueryClient,
  QueryClientProvider,
  useQuery as useLocalQuery,
} from "@tanstack/react-query";
import {
  TransportProvider,
  useQuery,
  useTransport,
} from "@connectrpc/connect-query";
import {
  SessionQuery,
  ResourceQuery,
  InboxQuery,
  ResourceService,
  InboxService,
  EntityKind,
  type Resource,
  type InboxView,
  CreateSessionRequestSchema,
  EnqueueInputRequestSchema,
  ControlSessionRequestSchema,
  SessionAction,
  SteerQueuedInputRequestSchema,
  RespondQuestionRequestSchema,
  RespondApprovalRequestSchema,
  SetInboxReadStateRequestSchema,
  SetNotificationPreferencesRequestSchema,
  InboxReadState,
  supportsResourceSchema,
  clientFailure,
  FailureCode,
  RevokeDeviceRequestSchema,
} from "@delinoio/delidev-api-client";
import {
  ProtectedState,
  Operation,
  uuid,
  documentBytes,
  documentOf,
  type State,
} from "./state";
import { Connection, Status } from "./connection";
import {
  storage,
  observeForeground,
  permission,
  notify,
  tls,
} from "./platform";
import { en, ko, type Labels } from "./localization";
import { presentForeground } from "./notifications";
import { RequestResponse } from "./interaction";
const owner = new ProtectedState(storage);
const Copy = createContext<Labels>(en);
const useCopy = () => useContext(Copy);
function value(resource?: Resource): Record<string, unknown> {
  try {
    return resource ? documentOf(resource.documentJson) : {};
  } catch {
    return {};
  }
}
function text(v: unknown): string {
  return typeof v === "string" ? v : "";
}
function record(v: unknown): Record<string, unknown> {
  return v && typeof v === "object" && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : {};
}
function label(resource: Resource): string {
  return text(value(resource).name) || resource.id;
}
function observation(v: unknown, c: Labels): string {
  const key = text(v);
  if (key === "general-chat") return c.general;
  if (key === "worktree") return c.worktree;
  return Object.hasOwn(c, key) ? c[key as keyof Labels] : key;
}
function guidance(error: unknown, c: Labels, pairing = false): string {
  const code = clientFailure(error).code;
  return code === FailureCode.Conflict
    ? c.stale
    : code === FailureCode.PermissionDenied
      ? c.permissionDenied
      : code === FailureCode.Unsupported
        ? c.unsupported
        : code === FailureCode.RecoveryRequired
          ? c.recoveryRequired
          : code === FailureCode.Unauthenticated
            ? pairing
              ? c.pairingExpired
              : c.revoked
            : code === FailureCode.InvalidArgument
              ? c.pairingInvalid
              : c.error;
}
function Modal({
  title,
  children,
  close,
}: {
  title: string;
  children: ReactNode;
  close: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null),
    c = useCopy();
  useEffect(() => {
    const previous = document.activeElement;
    ref.current?.showModal();
    return () => {
      ref.current?.close();
      if (previous instanceof HTMLElement && previous.isConnected)
        previous.focus();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      onCancel={(event) => {
        event.preventDefault();
        close();
      }}
      aria-label={title}
    >
      <header>
        <h2>{title}</h2>
        <button onClick={close}>{c.close}</button>
      </header>
      {children}
    </dialog>
  );
}
export function App({ state = owner }: { state?: ProtectedState }) {
  const [loaded, setLoaded] = useState(false),
    [failed, setFailed] = useState(false),
    [revision, setRevision] = useState(0),
    [tab, setTab] = useState("sessions"),
    [active, setActive] = useState(document.visibilityState === "visible");
  const changed = () => setRevision((v) => v + 1);
  useEffect(() => {
    void state
      .load()
      .then(() => {
        setLoaded(true);
        changed();
      })
      .catch(() => setFailed(true));
  }, [state]);
  useEffect(() => {
    let stop: (() => void) | undefined;
    let disposed = false;
    void observeForeground(setActive)
      .then((fn) => {
        if (disposed) fn();
        else stop = fn;
      })
      .catch(() => {});
    return () => {
      disposed = true;
      stop?.();
    };
  }, []);
  const s = state.state,
    c = s.language === "ko" ? ko : en;
  useEffect(() => {
    document.documentElement.lang = s.language;
    document.documentElement.dataset.theme = s.theme;
  }, [s.language, s.theme, revision]);
  const profile = s.profiles.find((p) => p.id === s.selectedProfile);
  return (
    <Copy.Provider value={c}>
      <div className="shell">
        <header className="app-header">
          <h1>DeliDev</h1>
        </header>
        <main>
          {failed ? (
            <p role="alert">{c.error}</p>
          ) : !loaded ? (
            <p role="status">{c.loading}</p>
          ) : tab === "settings" ? (
            <Settings state={state} changed={changed} active={active} />
          ) : profile && !profile.pairing && !profile.revoked ? (
            <Connected
              key={profile.id}
              state={state}
              id={profile.id}
              active={active}
              tab={tab}
              changed={changed}
            />
          ) : (
            <p>{profile?.revoked ? c.revoked : c.selectProfile}</p>
          )}
        </main>
        <nav aria-label="DeliDev">
          {(["sessions", "inbox", "settings"] as const).map((name) => (
            <button
              key={name}
              aria-current={tab === name ? "page" : undefined}
              onClick={() => setTab(name)}
            >
              {c[name]}
            </button>
          ))}
        </nav>
      </div>
    </Copy.Provider>
  );
}
function Settings({
  state,
  changed,
  active,
}: {
  state: ProtectedState;
  changed: () => void;
  active: boolean;
}) {
  const c = useCopy(),
    [adding, setAdding] = useState(false),
    [name, setName] = useState(""),
    [origin, setOrigin] = useState(""),
    [grant, setGrant] = useState(""),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [confirm, setConfirm] = useState<() => Promise<void>>(),
    [confirmText, setConfirmText] = useState("");
  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError("");
    try {
      await fn();
      changed();
    } catch (error) {
      let message = guidance(
        error,
        c,
        !!state.state.profiles.find((p) => p.id === state.state.selectedProfile)
          ?.pairing,
      );
      const p = state.state.profiles.find(
        (p) => p.id === state.state.selectedProfile,
      );
      if (p && clientFailure(error).code === FailureCode.ServerUnavailable)
        try {
          if ((await tls(p.origin)) === "certificate") message = c.certificate;
        } catch {}
      setError(message);
    } finally {
      setBusy(false);
    }
  };
  const settings = (change: (s: State) => void) =>
    void run(() => state.update(change));
  return (
    <section>
      <h2>{c.settings}</h2>
      <h3>{c.profile}</h3>
      {state.state.profiles.map((p) => (
        <article key={p.id}>
          <button
            aria-pressed={state.state.selectedProfile === p.id}
            disabled={busy}
            onClick={() => void run(() => state.select(p.id))}
          >
            {p.name}
          </button>
          <p>{p.origin}</p>
          {p.pairing ? (
            <>
              <p>{c.pendingPair}</p>
              <button
                disabled={busy}
                onClick={() =>
                  void run(async () => {
                    await state.pair(p.id);
                  })
                }
              >
                {c.retryPair}
              </button>
            </>
          ) : null}
          <button
            disabled={busy}
            onClick={() => {
              setConfirmText(c.confirmForget);
              setConfirm(() => () => state.forget(p.id));
            }}
          >
            {c.forget}
          </button>
          <p>{c.localNote}</p>
        </article>
      ))}
      <button onClick={() => setAdding(true)}>{c.add}</button>
      <label>
        {c.language}
        <select
          value={state.state.language}
          onChange={(e) =>
            settings((s) => {
              s.language = e.target.value as State["language"];
            })
          }
        >
          <option value="en">English</option>
          <option value="ko">한국어</option>
        </select>
      </label>
      <label>
        {c.theme}
        <select
          value={state.state.theme}
          onChange={(e) =>
            settings((s) => {
              s.theme = e.target.value as State["theme"];
            })
          }
        >
          {(["system", "light", "dark"] as const).map((v) => (
            <option value={v} key={v}>
              {c[v]}
            </option>
          ))}
        </select>
      </label>
      <label className="check">
        <input
          type="checkbox"
          checked={state.state.notifications}
          onChange={(e) => {
            const enabled = e.target.checked;
            void run(async () => {
              if (enabled && !(await permission())) throw new Error("denied");
              await state.update((s) => {
                s.notifications = enabled;
              });
            });
          }}
        />
        {c.notifications}
      </label>
      <p>{c.notificationNote}</p>
      {state.state.selectedProfile &&
      !state.profile(state.state.selectedProfile).pairing &&
      !state.profile(state.state.selectedProfile).revoked ? (
        <NotificationSettings
          key={state.state.selectedProfile}
          state={state}
          id={state.state.selectedProfile}
          active={active}
          changed={changed}
        />
      ) : null}
      <details>
        <summary>{c.diagnostics}</summary>
        <p>{c.safe}</p>
        <pre>
          {JSON.stringify(
            {
              operation: "connection-profile",
              profile_id: state.state.selectedProfile,
              paired: state.state.profiles.filter(
                (p) => !p.pairing && !p.revoked,
              ).length,
              unresolved: state.state.profiles.filter(
                (p) => p.pending || p.pairing,
              ).length,
            },
            null,
            2,
          )}
        </pre>
      </details>
      {error ? <p role="alert">{error}</p> : null}
      {adding ? (
        <Modal title={c.add} close={() => setAdding(false)}>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void run(async () => {
                const id = await state.preparePair(name, origin, grant);
                changed();
                await state.pair(id);
                setAdding(false);
                setGrant("");
                setName("");
                setOrigin("");
              });
            }}
          >
            <fieldset disabled={busy}>
              <label>
                {c.name}
                <input
                  required
                  maxLength={120}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </label>
              <label>
                {c.origin}
                <input
                  required
                  type="url"
                  autoCapitalize="none"
                  autoCorrect="off"
                  value={origin}
                  onChange={(e) => setOrigin(e.target.value)}
                />
              </label>
              <label>
                {c.pairing}
                <textarea
                  required
                  autoCapitalize="none"
                  autoCorrect="off"
                  spellCheck={false}
                  value={grant}
                  onChange={(e) => setGrant(e.target.value)}
                />
              </label>
              <p>{c.pairingNote}</p>
              <button>{c.pair}</button>
            </fieldset>
          </form>
        </Modal>
      ) : null}
      {confirm ? (
        <Modal title={confirmText} close={() => setConfirm(undefined)}>
          <button
            disabled={busy}
            onClick={() =>
              void run(async () => {
                await confirm();
                setConfirm(undefined);
              })
            }
          >
            {c.confirm}
          </button>
        </Modal>
      ) : null}
    </section>
  );
}
function NotificationSettings({
  state,
  id,
  changed,
  active,
}: {
  state: ProtectedState;
  id: string;
  changed: () => void;
  active: boolean;
}) {
  const cache = useMemo(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: { retry: false, gcTime: 0 },
          mutations: { retry: false },
        },
      }),
    [id],
  );
  const transport = useMemo(() => state.transport(id), [state, id]);
  useEffect(() => {
    if (!active) void cache.cancelQueries();
  }, [active, cache]);
  useEffect(
    () => () => {
      void cache.cancelQueries();
      cache.clear();
    },
    [cache],
  );
  return (
    <QueryClientProvider client={cache}>
      <TransportProvider transport={transport}>
        <ServerNotificationSettings
          state={state}
          id={id}
          changed={changed}
          active={active}
        />
      </TransportProvider>
    </QueryClientProvider>
  );
}
function ServerNotificationSettings({
  state,
  id,
  changed,
  active,
}: {
  state: ProtectedState;
  id: string;
  changed: () => void;
  active: boolean;
}) {
  const c = useCopy(),
    [busy, setBusy] = useState(false),
    [failed, setFailed] = useState(false),
    query = useQuery(
      InboxQuery.getNotificationPreferences,
      {},
      { enabled: active },
    ),
    preferences = query.data?.preferences;
  const update = async (
    field: "interactions" | "terminals",
    enabled: boolean,
  ) => {
    if (!active || !preferences || busy || state.profile(id).pending) return;
    setBusy(true);
    setFailed(false);
    try {
      await state.perform(
        id,
        Operation.Preferences,
        create(SetNotificationPreferencesRequestSchema, {
          requestId: uuid(),
          preferences: { ...preferences, [field]: enabled },
        }),
        "",
      );
      await query.refetch();
    } catch {
      setFailed(true);
    } finally {
      setBusy(false);
      changed();
    }
  };
  return (
    <fieldset
      disabled={!active || busy || !preferences || !!state.profile(id).pending}
    >
      <legend>{c.serverNotifications}</legend>
      {(["interactions", "terminals"] as const).map((field) => (
        <label key={field} className="check">
          <input
            type="checkbox"
            checked={preferences?.[field] ?? false}
            onChange={(e) => void update(field, e.target.checked)}
          />
          {c[field]}
        </label>
      ))}
      {query.isError || failed ? <p role="alert">{c.error}</p> : null}
    </fieldset>
  );
}
function Connected({
  state,
  id,
  active,
  tab,
  changed,
}: {
  state: ProtectedState;
  id: string;
  active: boolean;
  tab: string;
  changed: () => void;
}) {
  const c = useCopy(),
    [status, setStatus] = useState(Status.Connecting),
    [sessionId, setSessionId] = useState(""),
    [creation, setCreation] = useState(false),
    [project, setProject] = useState(""),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [inspected, setInspected] = useState(""),
    [confirm, setConfirm] = useState<() => Promise<void>>(),
    [revokeConfirm, setRevokeConfirm] = useState(false),
    [syncTick, setSyncTick] = useState(0);
  const connection = useMemo(
    () => new Connection(state, id, setStatus, () => setSyncTick((n) => n + 1)),
    [state, id],
  );
  const transport = useMemo(() => state.transport(id), [state, id]);
  useEffect(() => {
    if (active) void connection.resume();
    else connection.suspend();
    return () => connection.suspend();
  }, [connection, active]);
  const mutate = async (
    operation: Operation,
    request: Message,
    target: string,
  ) => {
    if (!connection.canMutate() || busy) return;
    setBusy(true);
    setError("");
    try {
      await state.perform(id, operation, request, target);
      changed();
      await connection.cache.invalidateQueries();
    } catch (error) {
      setError(guidance(error, c));
      changed();
    } finally {
      setBusy(false);
      setInspected("");
    }
  };
  const pending = state.profile(id).pending;
  useEffect(() => {
    if (
      active &&
      status === Status.Live &&
      state.state.notifications &&
      !pending
    )
      void presentForeground(
        state,
        id,
        () => connection.active && connection.status === Status.Live,
        { notify },
      )
        .then(changed)
        .catch(changed);
  }, [active, status, state, id, connection, pending, syncTick]);
  const enabled = active && status === Status.Live;
  async function inspect() {
    if (!pending || !enabled) return;
    setBusy(true);
    try {
      const kind =
        pending.operation === Operation.Question ||
        pending.operation === Operation.Approval
          ? EntityKind.INTERACTION
          : pending.operation === Operation.Read
            ? EntityKind.INBOX
            : pending.operation === Operation.Revoke
              ? EntityKind.DEVICE
              : EntityKind.SESSION;
      if (pending.operation === Operation.Create) {
        await createClient(ResourceService, transport).listResources({
          filter: { kind: EntityKind.SESSION, pageSize: 50 },
        });
      } else if (
        pending.operation === Operation.Preferences ||
        pending.operation === Operation.Claim ||
        pending.operation === Operation.Report
      ) {
        if (pending.operation === Operation.Preferences)
          await createClient(
            InboxService,
            transport,
          ).getNotificationPreferences({});
        else
          await createClient(InboxService, transport).getNotificationDelivery({
            inboxId: pending.target,
          });
      } else {
        await createClient(ResourceService, transport).getResource({
          kind,
          id: pending.target,
        });
      }
      setInspected(pending.request);
    } catch {
      setError(c.error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <QueryClientProvider client={connection.cache}>
      <TransportProvider transport={transport}>
        <section>
          <div className="connection">
            <strong>{state.profile(id).name}</strong>
            <span role="status">
              {
                c[
                  status === Status.Live
                    ? "live"
                    : status === Status.Suspended
                      ? "suspended"
                      : status === Status.Certificate
                        ? "certificate"
                        : status === Status.Unavailable
                          ? "unavailable"
                          : status === Status.Revoked
                            ? "revoked"
                            : status === Status.Version
                              ? "version"
                              : "connecting"
                ]
              }
            </span>
            <button
              disabled={!active || busy}
              onClick={() => void connection.resume()}
            >
              {c.refresh}
            </button>
          </div>
          {pending ? (
            <aside role="status">
              <p>{c.pending}</p>
              <p>{c.inspectNote}</p>
              <button
                disabled={!enabled || busy}
                onClick={() => void inspect()}
              >
                {c.inspect}
              </button>
              <button
                disabled={!enabled || busy || inspected !== pending.request}
                onClick={() => {
                  setRevokeConfirm(false);
                  setConfirm(() => async () => {
                    setBusy(true);
                    try {
                      await state.retry(id);
                      changed();
                      await connection.cache.invalidateQueries();
                    } catch {
                      setError(c.error);
                    } finally {
                      setBusy(false);
                      setConfirm(undefined);
                    }
                  });
                }}
              >
                {c.retry}
              </button>
              {inspected === pending.request ? <p>{c.inspected}</p> : null}
            </aside>
          ) : null}
          {error ? <p role="alert">{error}</p> : null}
          {tab === "inbox" && !sessionId ? (
            <Inbox
              enabled={enabled}
              busy={busy || !!pending}
              mutate={mutate}
              open={setSessionId}
            />
          ) : sessionId ? (
            <Conversation
              id={sessionId}
              enabled={enabled}
              busy={busy || !!pending}
              mutate={mutate}
              back={() => setSessionId("")}
            />
          ) : (
            <Sessions
              enabled={enabled}
              project={project}
              setProject={setProject}
              open={setSessionId}
              create={() => setCreation(true)}
            />
          )}
          {creation ? (
            <Modal title={c.newSession} close={() => setCreation(false)}>
              <NewSession
                enabled={enabled && !busy && !pending}
                mutate={async (r) => {
                  await mutate(Operation.Create, r, "");
                  if (!state.profile(id).pending) setCreation(false);
                }}
              />
            </Modal>
          ) : null}
          {confirm ? (
            <Modal
              title={revokeConfirm ? c.confirmRevoke : c.confirmRetry}
              close={() => setConfirm(undefined)}
            >
              <button disabled={busy} onClick={() => void confirm()}>
                {c.confirm}
              </button>
            </Modal>
          ) : null}
          <details>
            <summary>{c.diagnostics}</summary>
            <p>
              {c.identity}: {state.profile(id).serverId}
            </p>
            <p>{status}</p>
            <button
              disabled={!enabled || busy || !!pending}
              onClick={() => {
                setRevokeConfirm(true);
                setConfirm(() => async () => {
                  const result = await createClient(
                    ResourceService,
                    transport,
                  ).getResource({
                    kind: EntityKind.DEVICE,
                    id: state.profile(id).deviceId,
                  });
                  if (result.resource)
                    await mutate(
                      Operation.Revoke,
                      create(RevokeDeviceRequestSchema, {
                        mutation: {
                          id: result.resource.id,
                          expectedRevision: result.resource.revision,
                          requestId: uuid(),
                        },
                      }),
                      result.resource.id,
                    );
                  setConfirm(undefined);
                });
              }}
            >
              {c.revoke}
            </button>
          </details>
        </section>
      </TransportProvider>
    </QueryClientProvider>
  );
}
type Mutate = (
  operation: Operation,
  request: Message,
  target: string,
) => Promise<void>;
function Choices({
  kind,
  enabled,
  selected,
  onSelect,
  title,
}: {
  kind: EntityKind;
  enabled: boolean;
  selected: string;
  onSelect: (id: string) => void;
  title: string;
}) {
  const c = useCopy(),
    [page, setPage] = useState(""),
    [items, setItems] = useState<Resource[]>([]);
  const query = useQuery(
    ResourceQuery.listResources,
    { filter: { kind, pageSize: 50, pageToken: page } },
    { enabled },
  );
  useEffect(() => {
    if (query.data)
      setItems((previous) =>
        page
          ? [
              ...previous,
              ...query.data!.resources.filter(
                (r) => !previous.some((p) => p.id === r.id),
              ),
            ]
          : query.data!.resources,
      );
  }, [query.data, page]);
  return (
    <div>
      <label>
        {title}
        <select
          required
          value={selected}
          onChange={(e) => onSelect(e.target.value)}
        >
          <option value="">{c.choose}</option>
          {items
            .filter(
              (r) =>
                r.kind === kind &&
                r.revision > 0n &&
                supportsResourceSchema(r) &&
                (kind !== EntityKind.MACHINE ||
                  (value(r).disabled !== true && value(r).enabled !== false)),
            )
            .map((r) => (
              <option key={r.id} value={r.id}>
                {label(r)}
              </option>
            ))}
        </select>
      </label>
      {query.data?.nextPageToken ? (
        <button
          type="button"
          onClick={() => setPage(query.data!.nextPageToken)}
        >
          {c.more}
        </button>
      ) : null}
      {query.isError ? <p role="alert">{c.unavailable}</p> : null}
    </div>
  );
}
function Sessions({
  enabled,
  project,
  setProject,
  open,
  create: openCreation,
}: {
  enabled: boolean;
  project: string;
  setProject: (id: string) => void;
  open: (id: string) => void;
  create: () => void;
}) {
  const c = useCopy(),
    [page, setPage] = useState(""),
    [items, setItems] = useState<Resource[]>([]);
  const query = useQuery(
    SessionQuery.listSessions,
    { projectId: project, pageSize: 50, pageToken: page },
    { enabled },
  );
  useEffect(() => {
    setPage("");
    setItems([]);
  }, [project]);
  useEffect(() => {
    if (query.data)
      setItems((previous) =>
        page
          ? [
              ...previous,
              ...query.data!.sessions.filter(
                (r) => !previous.some((p) => p.id === r.id),
              ),
            ]
          : query.data!.sessions,
      );
  }, [query.data, page]);
  return (
    <section>
      <header>
        <h2>{c.sessions}</h2>
        <button disabled={!enabled} onClick={openCreation}>
          {c.newSession}
        </button>
      </header>
      <Choices
        title={c.project}
        kind={EntityKind.PROJECT}
        enabled={enabled}
        selected={project}
        onSelect={setProject}
      />
      <button onClick={() => setProject("")}>{c.all}</button>
      {query.isPending ? (
        <p role="status">{c.loading}</p>
      ) : query.isError ? (
        <p role="alert">{c.unavailable}</p>
      ) : !items.length ? (
        <p>{c.empty}</p>
      ) : (
        items.map((r) => (
          <article key={r.id}>
            <button className="row" onClick={() => open(r.id)}>
              <strong>{label(r)}</strong>
              <span>
                {observation(value(r).outcome, c)} ·{" "}
                {observation(value(r).workspace, c)}
              </span>
            </button>
          </article>
        ))
      )}
      {query.data?.nextPageToken ? (
        <button onClick={() => setPage(query.data!.nextPageToken)}>
          {c.more}
        </button>
      ) : null}
    </section>
  );
}
function NewSession({
  enabled,
  mutate,
}: {
  enabled: boolean;
  mutate: (request: Message) => Promise<void>;
}) {
  const c = useCopy(),
    [project, setProject] = useState(""),
    [agent, setAgent] = useState(""),
    [runner, setRunner] = useState(""),
    [workspace, setWorkspace] = useState("worktree"),
    [title, setTitle] = useState(""),
    [prompt, setPrompt] = useState(""),
    [mode, setMode] = useState("execute");
  const valid =
    enabled &&
    agent &&
    runner &&
    (workspace === "general-chat" || project) &&
    prompt.trim() &&
    new TextEncoder().encode(prompt).length <= 256 << 10;
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (valid)
          void mutate(
            create(CreateSessionRequestSchema, {
              requestId: uuid(),
              documentJson: documentBytes({
                name: title.trim() || c.newSession,
                agent_id: agent,
                machine_id: runner,
                ...(workspace === "worktree" ? { project_id: project } : {}),
                workspace,
                prompt,
                mode,
                source: "MANUAL",
              }),
            }),
          );
      }}
    >
      <fieldset disabled={!enabled}>
        <label>
          {c.workspace}
          <select
            value={workspace}
            onChange={(e) => setWorkspace(e.target.value)}
          >
            <option value="worktree">{c.worktree}</option>
            <option value="general-chat">{c.general}</option>
          </select>
        </label>
        {workspace === "worktree" ? (
          <Choices
            title={c.project}
            kind={EntityKind.PROJECT}
            enabled={enabled}
            selected={project}
            onSelect={setProject}
          />
        ) : null}
        <Choices
          title={c.agent}
          kind={EntityKind.AGENT}
          enabled={enabled}
          selected={agent}
          onSelect={setAgent}
        />
        <Choices
          title={c.runner}
          kind={EntityKind.MACHINE}
          enabled={enabled}
          selected={runner}
          onSelect={setRunner}
        />
        <label>
          {c.title}
          <input
            maxLength={256}
            value={title}
            onChange={(e) => setTitle(e.target.value)}
          />
        </label>
        <label>
          {c.mode}
          <select value={mode} onChange={(e) => setMode(e.target.value)}>
            <option value="execute">{c.execute}</option>
            <option value="plan">{c.plan}</option>
          </select>
        </label>
        <label>
          {c.prompt}
          <textarea
            required
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
          />
        </label>
        <button disabled={!valid}>{c.newSession}</button>
      </fieldset>
    </form>
  );
}
function Conversation({
  id,
  enabled,
  busy,
  mutate,
  back,
}: {
  id: string;
  enabled: boolean;
  busy: boolean;
  mutate: Mutate;
  back: () => void;
}) {
  const transport = useTransport();
  const c = useCopy(),
    [page, setPage] = useState(""),
    [history, setHistory] = useState<Resource[]>([]),
    [prompt, setPrompt] = useState(""),
    [mode, setMode] = useState("execute"),
    [confirmation, setConfirmation] = useState<SessionAction>();
  const session = useQuery(
    ResourceQuery.getResource,
    { kind: EntityKind.SESSION, id },
    { enabled },
  );
  const messages = useQuery(
    ResourceQuery.listResources,
    {
      filter: {
        kind: EntityKind.MESSAGE,
        sessionId: id,
        pageSize: 50,
        pageToken: page,
      },
    },
    { enabled },
  );
  const interactions = useQuery(
    ResourceQuery.listResources,
    { filter: { kind: EntityKind.INTERACTION, sessionId: id, pageSize: 50 } },
    { enabled },
  );
  const queue = useQuery(
    SessionQuery.listQueue,
    { sessionId: id, pageSize: 50 },
    { enabled },
  );
  useEffect(() => {
    if (messages.data)
      setHistory((previous) =>
        page
          ? [
              ...previous,
              ...messages.data!.resources.filter(
                (r) => !previous.some((p) => p.id === r.id),
              ),
            ]
          : messages.data!.resources,
      );
  }, [messages.data, page]);
  const resource = session.data?.resource,
    data = value(resource),
    available =
      enabled &&
      !busy &&
      !!resource &&
      supportsResourceSchema(resource) &&
      data.archive === "active" &&
      data.recovery === "none";
  return (
    <section>
      <button onClick={back}>{c.back}</button>
      <h2>{resource ? label(resource) : c.loading}</h2>
      <details>
        <summary>{c.information}</summary>
        <dl>
          {["workspace", "outcome", "dispatch", "recovery"].map((key) => (
            <div key={key}>
              <dt>{c[key as keyof Labels]}</dt>
              <dd>{observation(data[key], c)}</dd>
            </div>
          ))}
        </dl>
        <p>
          {c.runner}: {text(data.machine_id)}
        </p>
        <p>
          {c.agent}: {text(data.agent_id)}
        </p>
      </details>
      {session.isError ? <p role="alert">{c.sessionGone}</p> : null}
      <div className="transcript" aria-live="polite">
        {history.map((r) => (
          <article key={r.id}>
            <small>{text(value(r).role)}</small>
            <p>{text(value(r).text) || text(value(r).content)}</p>
          </article>
        ))}
      </div>
      {messages.data?.nextPageToken ? (
        <button onClick={() => setPage(messages.data!.nextPageToken)}>
          {c.more}
        </button>
      ) : null}
      {interactions.data?.resources
        .filter((r) => value(r).closure === "open")
        .map((r) => (
          <RequestResponse
            key={r.id}
            resource={r}
            enabled={available}
            copy={c}
            send={async (question, response) => {
              const fresh = await createClient(
                ResourceService,
                transport,
              ).getResource({ kind: EntityKind.INTERACTION, id: r.id });
              if (!fresh.resource || fresh.resource.revision !== r.revision)
                throw new Error("stale-request");
              await mutate(
                question ? Operation.Question : Operation.Approval,
                create(
                  question
                    ? RespondQuestionRequestSchema
                    : RespondApprovalRequestSchema,
                  {
                    mutation: {
                      id: r.id,
                      expectedRevision: r.revision,
                      requestId: uuid(),
                    },
                    responseJson: documentBytes(response),
                  },
                ),
                r.id,
              );
            }}
          />
        ))}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (
            !available ||
            !prompt.trim() ||
            new TextEncoder().encode(prompt).length > 256 << 10
          )
            return;
          void mutate(
            Operation.Send,
            create(EnqueueInputRequestSchema, {
              requestId: uuid(),
              sessionId: id,
              documentJson: documentBytes({ prompt, mode }),
            }),
            id,
          );
        }}
      >
        <fieldset disabled={!available}>
          <label>
            {c.prompt}
            <textarea
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
            />
          </label>
          <label>
            {c.mode}
            <select value={mode} onChange={(e) => setMode(e.target.value)}>
              <option value="execute">{c.execute}</option>
              <option value="plan">{c.plan}</option>
            </select>
          </label>
          <button disabled={!prompt.trim()}>{c.send}</button>
        </fieldset>
      </form>
      <h3>{c.queue}</h3>
      {queue.data?.inputs.map((input) => (
        <article key={input.id}>
          <p>{text(value(input).prompt)}</p>
          <button
            disabled={
              !available ||
              data.outcome !== "running" ||
              !text(data.active_execution_id) ||
              !text(record(data.execution).native_turn_id)
            }
            onClick={() =>
              void mutate(
                Operation.Steer,
                create(SteerQueuedInputRequestSchema, {
                  mutation: {
                    id: input.id,
                    expectedRevision: input.revision,
                    requestId: uuid(),
                  },
                  sessionId: id,
                  expectedExecutionId: text(data.active_execution_id),
                  expectedTurnId: text(record(data.execution).native_turn_id),
                }),
                id,
              )
            }
          >
            {c.steer}
          </button>
        </article>
      ))}
      <button
        disabled={!available}
        onClick={() => setConfirmation(SessionAction.STOP)}
      >
        {c.stop}
      </button>
      <button
        disabled={!available || data.dispatch !== "paused"}
        onClick={() => setConfirmation(SessionAction.RESUME)}
      >
        {c.resume}
      </button>
      {confirmation !== undefined && resource ? (
        <Modal
          title={
            confirmation === SessionAction.STOP
              ? c.stopConfirm
              : c.resumeConfirm
          }
          close={() => setConfirmation(undefined)}
        >
          <button
            disabled={!available}
            onClick={() => {
              void mutate(
                Operation.Control,
                create(ControlSessionRequestSchema, {
                  mutation: {
                    id,
                    expectedRevision: resource.revision,
                    requestId: uuid(),
                  },
                  action: confirmation,
                }),
                id,
              );
              setConfirmation(undefined);
            }}
          >
            {c.confirm}
          </button>
        </Modal>
      ) : null}
    </section>
  );
}
function Inbox({
  enabled,
  busy,
  mutate,
  open,
}: {
  enabled: boolean;
  busy: boolean;
  mutate: Mutate;
  open: (id: string) => void;
}) {
  const c = useCopy(),
    [page, setPage] = useState(""),
    [items, setItems] = useState<InboxView[]>([]),
    [selected, setSelected] = useState("");
  const list = useQuery(
    InboxQuery.listInbox,
    { pageSize: 50, pageToken: page },
    { enabled },
  );
  const current = useQuery(
    InboxQuery.getInboxEntry,
    { id: selected },
    { enabled: enabled && !!selected },
  );
  useEffect(() => {
    if (list.data)
      setItems((previous) =>
        page
          ? [
              ...previous,
              ...list.data!.entries.filter(
                (r) => !previous.some((p) => p.entry?.id === r.entry?.id),
              ),
            ]
          : list.data!.entries,
      );
  }, [list.data, page]);
  const v = current.data?.view,
    r = v?.entry,
    d = value(r);
  return (
    <section>
      <h2>{c.inbox}</h2>
      <p>{c.inboxOriginal}</p>
      {list.isError ? <p role="alert">{c.unavailable}</p> : null}
      {items.map((v) => (
        <article key={v.entry?.id}>
          <button className="row" onClick={() => setSelected(v.entry!.id)}>
            <strong>
              {v.session ? label(v.session) : text(value(v.entry).source)}
            </strong>
            <span>
              {observation(value(v.entry).source, c)} ·{" "}
              {observation(record(value(v.entry).terminal).outcome, c)} ·{" "}
              {observation(value(v.entry).read_state, c)}
            </span>
          </button>
        </article>
      ))}
      {list.data?.nextPageToken ? (
        <button onClick={() => setPage(list.data!.nextPageToken)}>
          {c.more}
        </button>
      ) : null}
      {selected ? (
        <Modal title={c.original} close={() => setSelected("")}>
          {current.isPending ? (
            <p>{c.loading}</p>
          ) : current.isError ? (
            <p role="alert">{c.unavailable}</p>
          ) : r ? (
            <>
              <p>{text(d.source)}</p>
              {v?.session ? (
                <button
                  onClick={() => {
                    open(v.session!.id);
                    setSelected("");
                  }}
                >
                  {c.open}
                </button>
              ) : null}
              <button
                disabled={!enabled || busy}
                onClick={() =>
                  void mutate(
                    Operation.Read,
                    create(SetInboxReadStateRequestSchema, {
                      mutation: {
                        id: r.id,
                        expectedRevision: r.revision,
                        requestId: uuid(),
                      },
                      readState:
                        d.read_state === "read"
                          ? InboxReadState.UNREAD
                          : InboxReadState.READ,
                    }),
                    r.id,
                  )
                }
              >
                {d.read_state === "read" ? c.unread : c.read}
              </button>
              {v?.interaction ? (
                <RequestResponse
                  resource={v.interaction}
                  enabled={enabled && !busy}
                  copy={c}
                  send={async (question, response) => {
                    await mutate(
                      question ? Operation.Question : Operation.Approval,
                      create(
                        question
                          ? RespondQuestionRequestSchema
                          : RespondApprovalRequestSchema,
                        {
                          mutation: {
                            id: v.interaction!.id,
                            expectedRevision: v.interaction!.revision,
                            requestId: uuid(),
                          },
                          responseJson: documentBytes(response),
                        },
                      ),
                      v.interaction!.id,
                    );
                  }}
                />
              ) : null}
            </>
          ) : null}
        </Modal>
      ) : null}
    </section>
  );
}
