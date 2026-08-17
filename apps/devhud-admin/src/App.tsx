import { Code, ConnectError } from "@connectrpc/connect";
import {
  AdministrativeBlockState,
  AuditOutcome,
  StaticCapability,
  UploadState,
  mapDevHudError,
  validateAdminReason,
  type AdminUpload,
  type AdminUser,
  type AuditEvent,
  type DevHudClientError,
  type UsageCounter,
} from "@delinoio/devhud-api-client";
import { useEffect, useRef, useState } from "react";
import { createAdminClient, getBootstrap, type AdminClient } from "./api";
import { AdminAuth } from "./auth";
import { Dialog } from "./Dialog";
import { text, type Locale } from "./i18n";

type Phase =
  | { kind: "loading" }
  | { kind: "error"; error: unknown }
  | { kind: "signed-out"; auth: AdminAuth }
  | {
      kind: "ready";
      auth: AdminAuth;
      client: AdminClient;
      uploadsAvailable: boolean;
      publicAssetBaseUrl: string;
    };

type View = "users" | "uploads" | "audit";
type Loadable<T> =
  | { kind: "loading" }
  | { kind: "loaded"; value: T }
  | { kind: "error"; error: DevHudClientError };
type UsageState =
  | { kind: "loading"; user: AdminUser }
  | { kind: "loaded"; user: AdminUser; counters: UsageCounter[] }
  | { kind: "error"; user: AdminUser };

const maximumAdminSearchBytes = 512;
const textEncoder = new TextEncoder();
let appInitialization: Promise<Phase> | undefined;

async function initializeApp(): Promise<Phase> {
  const bootstrap = await getBootstrap();
  const auth = AdminAuth.fromBootstrap(bootstrap);
  if (await auth.completeCallback(window.location.href)) {
    history.replaceState(null, "", "/admin/");
  }
  return (await auth.isAuthenticated())
    ? {
        kind: "ready",
        auth,
        client: createAdminClient(auth),
        uploadsAvailable: bootstrap.capabilities.includes(
          StaticCapability.OFFICIAL_UPLOADS,
        ),
        publicAssetBaseUrl: bootstrap.publicAssetBaseUrl,
      }
    : { kind: "signed-out", auth };
}

function initializeAppOnce(): Promise<Phase> {
  if (appInitialization) return appInitialization;
  const operation = initializeApp();
  appInitialization = operation;
  const clear = () => {
    if (appInitialization === operation) appInitialization = undefined;
  };
  void operation.then(clear, clear);
  return operation;
}

function localeInitial(): Locale {
  const stored = localStorage.getItem("devhud.admin.locale");
  if (stored === "en" || stored === "ko") return stored;
  return navigator.language.toLowerCase().startsWith("ko") ? "ko" : "en";
}

export function App() {
  const [locale, setLocale] = useState<Locale>(localeInitial);
  const [phase, setPhase] = useState<Phase>({ kind: "loading" });
  const copy = text(locale);

  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  useEffect(() => {
    let current = true;
    void (async () => {
      try {
        const next = await initializeAppOnce();
        if (current) setPhase(next);
      } catch (error) {
        if (current) setPhase({ kind: "error", error });
      }
    })();
    return () => {
      current = false;
    };
  }, []);

  const toggleLocale = () => {
    const next = locale === "en" ? "ko" : "en";
    localStorage.setItem("devhud.admin.locale", next);
    setLocale(next);
  };

  if (phase.kind === "loading") {
    return <StatePage title={copy.app} message={copy.loading} busy />;
  }
  if (phase.kind === "error") {
    return <StatePage title={copy.app} message={copy.error} />;
  }
  if (phase.kind === "signed-out") {
    return (
      <main className="auth-page">
        <section className="auth-card">
          <Brand title={copy.app} subtitle={copy.subtitle} />
          <p>{copy.permission}</p>
          <button className="primary" onClick={() => void phase.auth.begin()}>
            {copy.signIn}
          </button>
          <button className="text-button" onClick={toggleLocale}>
            {copy.language}
          </button>
        </section>
      </main>
    );
  }
  return (
    <Console
      auth={phase.auth}
      client={phase.client}
      copy={copy}
      locale={locale}
      onToggleLocale={toggleLocale}
      publicAssetBaseUrl={phase.publicAssetBaseUrl}
      uploadsAvailable={phase.uploadsAvailable}
    />
  );
}

function Console({
  auth,
  client,
  copy,
  locale,
  onToggleLocale,
  publicAssetBaseUrl,
  uploadsAvailable,
}: {
  auth: AdminAuth;
  client: AdminClient;
  copy: ReturnType<typeof text>;
  locale: Locale;
  onToggleLocale: () => void;
  publicAssetBaseUrl: string;
  uploadsAvailable: boolean;
}) {
  const [view, setView] = useState<View>("users");
  return (
    <div className="shell">
      <a className="skip-link" href="#main-content">{copy.skip}</a>
      <header className="topbar">
        <Brand title={copy.app} subtitle={copy.subtitle} />
        <div className="top-actions">
          <button className="text-button" onClick={onToggleLocale}>
            {copy.language}
          </button>
          <button className="secondary" onClick={() => void auth.signOut()}>
            {copy.signOut}
          </button>
        </div>
      </header>
      <nav aria-label={copy.navigation} className="tabs">
        {(["users", "uploads", "audit"] as const).map((item) => (
          <button
            aria-current={view === item ? "page" : undefined}
            key={item}
            onClick={() => setView(item)}
          >
            {copy[item]}
          </button>
        ))}
      </nav>
      <main className="content" id="main-content">
        {view === "users" ? (
          <Users
            client={client}
            copy={copy}
            locale={locale}
            publicAssetBaseUrl={publicAssetBaseUrl}
            uploadsAvailable={uploadsAvailable}
          />
        ) : view === "uploads" ? (
          uploadsAvailable ? (
            <Uploads
              client={client}
              copy={copy}
              locale={locale}
              publicAssetBaseUrl={publicAssetBaseUrl}
            />
          ) : (
            <Empty message={copy.noUploads} />
          )
        ) : (
          <Audit client={client} copy={copy} locale={locale} />
        )}
      </main>
    </div>
  );
}

function Users({
  client,
  copy,
  locale,
  publicAssetBaseUrl,
  uploadsAvailable,
}: {
  client: AdminClient;
  copy: ReturnType<typeof text>;
  locale: Locale;
  publicAssetBaseUrl: string;
  uploadsAvailable: boolean;
}) {
  const [query, setQuery] = useState("");
  const [appliedQuery, setAppliedQuery] = useState("");
  const [state, setState] = useState<Loadable<{ users: AdminUser[]; next: string }>>({
    kind: "loading",
  });
  const [mutation, setMutation] = useState<AdminUser | null>(null);
  const [usage, setUsage] = useState<UsageState | null>(null);
  const [conflict, setConflict] = useState(false);
  const requestGeneration = useRef(0);
  const usageRequestGeneration = useRef(0);

  const load = async (token = "", append = false, searchQuery = appliedQuery) => {
    const generation = ++requestGeneration.current;
    if (!append) setState({ kind: "loading" });
    try {
      const response = await client.listUsers({
        query: searchQuery,
        page: { pageSize: 50, pageToken: token },
      });
      if (generation !== requestGeneration.current) return;
      setState((current) => ({
        kind: "loaded",
        value: {
          users:
            append && current.kind === "loaded"
              ? [...current.value.users, ...response.users]
              : response.users,
          next: response.nextPageToken,
        },
      }));
    } catch (error) {
      if (generation !== requestGeneration.current) return;
      setState(errorState(error));
    }
  };

  const loadUsage = async (user: AdminUser) => {
    const generation = ++usageRequestGeneration.current;
    setUsage({ kind: "loading", user });
    try {
      const response = await client.getUserUsage({ userId: user.userId });
      if (generation !== usageRequestGeneration.current) return;
      setUsage({ kind: "loaded", user, counters: response.counters });
    } catch {
      if (generation !== usageRequestGeneration.current) return;
      setUsage({ kind: "error", user });
    }
  };

  const closeUsage = () => {
    usageRequestGeneration.current++;
    setUsage(null);
  };

  useEffect(() => {
    void load();
    return () => {
      requestGeneration.current++;
      usageRequestGeneration.current++;
    };
    // Query is applied by the explicit search form.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const queryValid = textEncoder.encode(query).byteLength <= maximumAdminSearchBytes;

  return (
    <section aria-labelledby="users-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">{copy.identity}</p>
          <h1 id="users-title">{copy.users}</h1>
        </div>
        <form
          className="search"
          onSubmit={(event) => {
            event.preventDefault();
            if (!queryValid) return;
            setAppliedQuery(query);
            void load("", false, query);
          }}
          role="search"
        >
          <label className="sr-only" htmlFor="user-query">
            {copy.search}
          </label>
          <div className="search-field">
            <input
              aria-describedby={!queryValid ? "user-query-error" : undefined}
              aria-invalid={!queryValid || undefined}
              id="user-query"
              maxLength={512}
              onChange={(event) => setQuery(event.target.value)}
              placeholder={copy.search}
              value={query}
            />
            {!queryValid && (
              <p className="inline-error" id="user-query-error" role="alert">
                {copy.searchInvalid}
              </p>
            )}
          </div>
          <button className="primary" disabled={!queryValid} type="submit">
            {copy.users}
          </button>
        </form>
      </div>
      {conflict && <p className="inline-error" role="alert">{copy.conflict}</p>}
      <Resource
        state={state}
        copy={copy}
        onRetry={() => void load("", false, appliedQuery)}
      >
        {(value) => (
          <>
            <div className="cards">
              {value.users.map((user) => (
                <article className="card" key={user.userId?.value}>
                  <div className="card-title">
                    <div>
                      <h2>{user.displayName || user.email || user.logtoSubject}</h2>
                      <p>{user.email || "—"}</p>
                    </div>
                    <Status
                      tone={
                        user.administrativeBlockState ===
                        AdministrativeBlockState.BLOCKED
                          ? "danger"
                          : "success"
                      }
                    >
                      {copy.administrativeBlockStates[user.administrativeBlockState]}
                    </Status>
                  </div>
                  <dl>
                    <dt>{copy.logtoSubject}</dt>
                    <dd>{user.logtoSubject}</dd>
                    <dt>{copy.created}</dt>
                    <dd>{formatTime(user.createdAt, locale)}</dd>
                  </dl>
                  <div className="card-actions">
                    <button
                      className="secondary"
                      onClick={() => {
                        setConflict(false);
                        setMutation(user);
                      }}
                    >
                      {user.administrativeBlockState ===
                      AdministrativeBlockState.BLOCKED
                        ? copy.unblock
                        : copy.block}
                    </button>
                    {uploadsAvailable && (
                      <button
                        className="text-button"
                        onClick={() => {
                          void loadUsage(user);
                        }}
                      >
                        {copy.usage}
                      </button>
                    )}
                  </div>
                </article>
              ))}
            </div>
            {value.next && (
              <button className="load-more" onClick={() => void load(value.next, true, appliedQuery)}>
                {copy.next}
              </button>
            )}
          </>
        )}
      </Resource>
      {mutation && (
        <UserMutationDialog
          client={client}
          copy={copy}
          publicAssetBaseUrl={publicAssetBaseUrl}
          user={mutation}
          onClose={() => setMutation(null)}
          onDone={() => {
            setMutation(null);
            void load();
          }}
          onConflict={() => {
            setMutation(null);
            setConflict(true);
            void load();
          }}
        />
      )}
      {usage && (
        <Dialog title={copy.usage} onClose={closeUsage}>
          <p>{usage.user.displayName || usage.user.email}</p>
          {usage.kind === "loading" ? (
            <Empty message={copy.loading} busy />
          ) : usage.kind === "error" ? (
            <p className="inline-error" role="alert">{copy.error}</p>
          ) : usage.counters.length === 0 ? (
            <Empty message={copy.empty} />
          ) : (
            <dl className="quota-list">
              {usage.counters.map((counter, index) => (
                <div key={`${counter.quota}-${counter.submissionId?.value ?? index}`}>
                  <dt>{copy.quotaKinds[counter.quota]}</dt>
                  <dd>
                    {counter.used.toLocaleString()} / {counter.limit.toLocaleString()}
                  </dd>
                </div>
              ))}
            </dl>
          )}
          <button className="primary" data-autofocus onClick={closeUsage}>
            {copy.confirm}
          </button>
        </Dialog>
      )}
    </section>
  );
}

function UserMutationDialog({
  user,
  client,
  copy,
  publicAssetBaseUrl,
  onClose,
  onDone,
  onConflict,
}: {
  user: AdminUser;
  client: AdminClient;
  copy: ReturnType<typeof text>;
  publicAssetBaseUrl: string;
  onClose: () => void;
  onDone: () => void;
  onConflict: () => void;
}) {
  const blocked =
    user.administrativeBlockState === AdministrativeBlockState.BLOCKED;
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [pending, setPending] = useState(false);
  const [failed, setFailed] = useState(false);
  const reasonValid = isAdminReasonValid(reason, publicAssetBaseUrl);
  return (
    <Dialog title={blocked ? copy.unblock : copy.block} onClose={onClose}>
      <MutationFields
        confirmed={confirmed}
        copy={copy}
        onConfirmed={setConfirmed}
        onReason={setReason}
        reason={reason}
        reasonValid={reasonValid}
      />
      {failed && <p className="inline-error" role="alert">{copy.mutationError}</p>}
      <DialogActions
        copy={copy}
        disabled={!reasonValid || !confirmed || pending}
        onCancel={onClose}
        onConfirm={() => {
          setFailed(false);
          setPending(true);
          void client
            .setUserBlocked({
              userId: user.userId,
              expectedState: user.administrativeBlockState,
              targetState: blocked
                ? AdministrativeBlockState.UNBLOCKED
                : AdministrativeBlockState.BLOCKED,
              reason,
            })
            .then(onDone, (error) => {
              setPending(false);
              if (ConnectError.from(error).code === Code.Aborted) {
                onConflict();
                return;
              }
              setFailed(true);
            });
        }}
      />
    </Dialog>
  );
}

function Uploads({
  client,
  copy,
  locale,
  publicAssetBaseUrl,
}: {
  client: AdminClient;
  copy: ReturnType<typeof text>;
  locale: Locale;
  publicAssetBaseUrl: string;
}) {
  const [state, setState] = useState<Loadable<{ uploads: AdminUpload[]; next: string }>>({
    kind: "loading",
  });
  const [selected, setSelected] = useState<{
    upload: AdminUpload;
    action: "quarantine" | "delete";
  } | null>(null);
  const [conflict, setConflict] = useState(false);
  const [continuationPending, setContinuationPending] = useState(false);
  const continuationPendingRef = useRef(false);
  const requestGeneration = useRef(0);
  const load = async (token = "", append = false) => {
    if (append && continuationPendingRef.current) return;
    if (append) {
      continuationPendingRef.current = true;
      setContinuationPending(true);
    } else {
      continuationPendingRef.current = false;
      setContinuationPending(false);
      setState({ kind: "loading" });
    }
    const generation = ++requestGeneration.current;
    try {
      const response = await client.listUploads({
        page: { pageSize: 50, pageToken: token },
      });
      if (generation !== requestGeneration.current) return;
      setState((current) => ({
        kind: "loaded",
        value: {
          uploads:
            append && current.kind === "loaded"
              ? [...current.value.uploads, ...response.uploads]
              : response.uploads,
          next: response.nextPageToken,
        },
      }));
    } catch (error) {
      if (generation !== requestGeneration.current) return;
      setState(errorState(error));
    } finally {
      if (append && generation === requestGeneration.current) {
        continuationPendingRef.current = false;
        setContinuationPending(false);
      }
    }
  };
  useEffect(() => {
    void load();
    return () => {
      requestGeneration.current++;
      continuationPendingRef.current = false;
    };
  }, []);

  return (
    <section aria-labelledby="uploads-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">{copy.moderation}</p>
          <h1 id="uploads-title">{copy.uploads}</h1>
        </div>
      </div>
      {conflict && <p className="inline-error" role="alert">{copy.conflict}</p>}
      <Resource state={state} copy={copy} onRetry={() => void load()}>
        {(value) => (
          <>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>{copy.upload}</th>
                    <th>{copy.group}</th>
                    <th>{copy.state}</th>
                    <th>{copy.size}</th>
                    <th>{copy.created}</th>
                    <th aria-label={copy.actions} />
                  </tr>
                </thead>
                <tbody>
                  {value.uploads.map((upload) => (
                    <tr key={upload.uploadId?.value}>
                      <td className="mono">{upload.uploadId?.value}</td>
                      <td className="mono">{upload.uploadGroupId?.value}</td>
                      <td><Status>{copy.uploadStates[upload.state]}</Status></td>
                      <td>{formatBytes(upload.sizeBytes)}</td>
                      <td>{formatTime(upload.createdAt, locale)}</td>
                      <td className="row-actions">
                        {upload.state === UploadState.FINALIZED && (
                          <button
                            className="secondary"
                            onClick={() => {
                              setConflict(false);
                              setSelected({ upload, action: "quarantine" });
                            }}
                          >
                            {copy.quarantine}
                          </button>
                        )}
                        {(upload.state === UploadState.FINALIZED ||
                          upload.state === UploadState.PENDING ||
                          upload.state === UploadState.QUARANTINED) && (
                          <button
                            className="danger-button"
                            onClick={() => {
                              setConflict(false);
                              setSelected({ upload, action: "delete" });
                            }}
                          >
                            {copy.delete}
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {value.next && (
              <button
                className="load-more"
                disabled={continuationPending}
                onClick={() => void load(value.next, true)}
              >
                {copy.next}
              </button>
            )}
          </>
        )}
      </Resource>
      {selected && (
        <UploadMutationDialog
          client={client}
          copy={copy}
          publicAssetBaseUrl={publicAssetBaseUrl}
          selection={selected}
          onClose={() => setSelected(null)}
          onDone={() => {
            setSelected(null);
            void load();
          }}
          onConflict={() => {
            setSelected(null);
            setConflict(true);
            void load();
          }}
        />
      )}
    </section>
  );
}

function UploadMutationDialog({
  selection,
  client,
  copy,
  publicAssetBaseUrl,
  onClose,
  onDone,
  onConflict,
}: {
  selection: { upload: AdminUpload; action: "quarantine" | "delete" };
  client: AdminClient;
  copy: ReturnType<typeof text>;
  publicAssetBaseUrl: string;
  onClose: () => void;
  onDone: () => void;
  onConflict: () => void;
}) {
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [pending, setPending] = useState(false);
  const [failed, setFailed] = useState(false);
  const label = selection.action === "delete" ? copy.delete : copy.quarantine;
  const reasonValid = isAdminReasonValid(reason, publicAssetBaseUrl);
  return (
    <Dialog title={label} onClose={onClose}>
      <p className="mono break">{selection.upload.uploadId?.value}</p>
      <MutationFields
        confirmed={confirmed}
        copy={copy}
        onConfirmed={setConfirmed}
        onReason={setReason}
        reason={reason}
        reasonValid={reasonValid}
      />
      {failed && <p className="inline-error" role="alert">{copy.mutationError}</p>}
      <DialogActions
        copy={copy}
        disabled={!reasonValid || !confirmed || pending}
        onCancel={onClose}
        onConfirm={() => {
          setFailed(false);
          setPending(true);
          const request = {
            uploadId: selection.upload.uploadId,
            expectedState: selection.upload.state,
            reason,
          };
          const promise =
            selection.action === "delete"
              ? client.deleteUpload(request)
              : client.quarantineUpload(request);
          void promise.then(onDone, (error) => {
            setPending(false);
            if (ConnectError.from(error).code === Code.Aborted) {
              onConflict();
              return;
            }
            setFailed(true);
          });
        }}
      />
    </Dialog>
  );
}

function Audit({
  client,
  copy,
  locale,
}: {
  client: AdminClient;
  copy: ReturnType<typeof text>;
  locale: Locale;
}) {
  const [state, setState] = useState<Loadable<{ events: AuditEvent[]; next: string }>>({
    kind: "loading",
  });
  const [continuationPending, setContinuationPending] = useState(false);
  const continuationPendingRef = useRef(false);
  const requestGeneration = useRef(0);
  const load = async (token = "", append = false) => {
    if (append && continuationPendingRef.current) return;
    if (append) {
      continuationPendingRef.current = true;
      setContinuationPending(true);
    } else {
      continuationPendingRef.current = false;
      setContinuationPending(false);
      setState({ kind: "loading" });
    }
    const generation = ++requestGeneration.current;
    try {
      const response = await client.listAuditEvents({
        page: { pageSize: 50, pageToken: token },
      });
      if (generation !== requestGeneration.current) return;
      setState((current) => ({
        kind: "loaded",
        value: {
          events:
            append && current.kind === "loaded"
              ? [...current.value.events, ...response.auditEvents]
              : response.auditEvents,
          next: response.nextPageToken,
        },
      }));
    } catch (error) {
      if (generation !== requestGeneration.current) return;
      setState(errorState(error));
    } finally {
      if (append && generation === requestGeneration.current) {
        continuationPendingRef.current = false;
        setContinuationPending(false);
      }
    }
  };
  useEffect(() => {
    void load();
    return () => {
      requestGeneration.current++;
      continuationPendingRef.current = false;
    };
  }, []);
  return (
    <section aria-labelledby="audit-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">{copy.security}</p>
          <h1 id="audit-title">{copy.audit}</h1>
        </div>
      </div>
      <Resource state={state} copy={copy} onRetry={() => void load()}>
        {(value) => (
          <>
            <ol className="timeline">
              {value.events.map((event) => (
                <li key={event.auditEventId?.value}>
                  <div>
                    <strong>{copy.auditActions[event.action]}</strong>
                    <p>{event.reason || "—"}</p>
                    {(event.actorUserId || event.targetUserId || event.targetUploadId) && (
                      <dl className="audit-identifiers">
                        {event.actorUserId && (
                          <>
                            <dt>{copy.actorUser}</dt>
                            <dd className="mono">{event.actorUserId.value}</dd>
                          </>
                        )}
                        {event.targetUserId && (
                          <>
                            <dt>{copy.targetUser}</dt>
                            <dd className="mono">{event.targetUserId.value}</dd>
                          </>
                        )}
                        {event.targetUploadId && (
                          <>
                            <dt>{copy.targetUpload}</dt>
                            <dd className="mono">{event.targetUploadId.value}</dd>
                          </>
                        )}
                      </dl>
                    )}
                    <span className="mono">{event.correlationId?.value}</span>
                  </div>
                  <div className="timeline-meta">
                    <Status tone={event.outcome === AuditOutcome.REJECTED ? "danger" : "success"}>
                      {copy.auditOutcomes[event.outcome]}
                    </Status>
                    <time>{formatTime(event.createdAt, locale)}</time>
                  </div>
                </li>
              ))}
            </ol>
            {value.next && (
              <button
                className="load-more"
                disabled={continuationPending}
                onClick={() => void load(value.next, true)}
              >
                {copy.next}
              </button>
            )}
          </>
        )}
      </Resource>
    </section>
  );
}

function MutationFields({
  copy,
  reason,
  confirmed,
  onReason,
  onConfirmed,
  reasonValid,
}: {
  copy: ReturnType<typeof text>;
  reason: string;
  confirmed: boolean;
  onReason: (value: string) => void;
  onConfirmed: (value: boolean) => void;
  reasonValid: boolean;
}) {
  return (
    <>
      <label htmlFor="mutation-reason">{copy.reason}</label>
      <textarea
        aria-describedby={
          reason !== "" && !reasonValid
            ? "mutation-reason-help mutation-reason-error"
            : "mutation-reason-help"
        }
        aria-invalid={reason !== "" && !reasonValid}
        data-autofocus
        id="mutation-reason"
        onChange={(event) => onReason(event.target.value)}
        rows={4}
        value={reason}
      />
      <p className="help" id="mutation-reason-help">{copy.reasonHelp}</p>
      {reason !== "" && !reasonValid && (
        <p className="inline-error" id="mutation-reason-error" role="alert">
          {copy.reasonInvalid}
        </p>
      )}
      <label className="check">
        <input
          checked={confirmed}
          onChange={(event) => onConfirmed(event.target.checked)}
          type="checkbox"
        />
        <span>{copy.destructive}</span>
      </label>
    </>
  );
}

function DialogActions({
  copy,
  disabled,
  onCancel,
  onConfirm,
}: {
  copy: ReturnType<typeof text>;
  disabled: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div className="dialog-actions">
      <button className="secondary" onClick={onCancel}>{copy.cancel}</button>
      <button className="danger-button" disabled={disabled} onClick={onConfirm}>
        {copy.confirm}
      </button>
    </div>
  );
}

function Resource<T>({
  state,
  copy,
  children,
  onRetry,
}: {
  state: Loadable<T>;
  copy: ReturnType<typeof text>;
  children: (value: T) => React.ReactNode;
  onRetry: () => void;
}) {
  if (state.kind === "loading") return <Empty message={copy.loading} busy />;
  if (state.kind === "error") {
    const retryable =
      state.error.kind !== "unauthenticated" &&
      state.error.code !== Code.PermissionDenied;
    const message =
      state.error.kind === "unauthenticated"
        ? copy.sessionExpired
        : state.error.code === Code.PermissionDenied
          ? copy.permission
          : state.error.kind === "pagination"
            ? copy.paginationError
            : state.error.code === Code.Unavailable
              ? copy.unavailable
              : copy.error;
    return (
      <div className="empty" role="alert">
        <p>{message}</p>
        {state.error.correlationId && (
          <p className="correlation">
            {copy.correlationId}: <code>{state.error.correlationId}</code>
          </p>
        )}
        {retryable && (
          <button className="primary" onClick={onRetry}>{copy.retry}</button>
        )}
      </div>
    );
  }
  const value = state.value as T & { users?: unknown[]; uploads?: unknown[]; events?: unknown[] };
  if (
    value.users?.length === 0 ||
    value.uploads?.length === 0 ||
    value.events?.length === 0
  ) {
    return <Empty message={copy.empty} />;
  }
  return children(state.value);
}

function errorState(error: unknown): Loadable<never> {
  return { kind: "error", error: mapDevHudError(error) };
}

function Empty({ message, busy = false }: { message: string; busy?: boolean }) {
  return (
    <div aria-busy={busy} aria-live="polite" className="empty" role="status">
      {busy && <span aria-hidden="true" className="spinner" />}
      <p>{message}</p>
    </div>
  );
}

function StatePage({
  title,
  message,
  busy = false,
}: {
  title: string;
  message: string;
  busy?: boolean;
}) {
  return (
    <main className="auth-page">
      <section className="auth-card">
        <h1>{title}</h1>
        <Empty message={message} busy={busy} />
      </section>
    </main>
  );
}

function Brand({ title, subtitle }: { title: string; subtitle: string }) {
  return (
    <div className="brand">
      <span aria-hidden="true" className="brand-mark">D</span>
      <div><strong>{title}</strong><span>{subtitle}</span></div>
    </div>
  );
}

function Status({
  children,
  tone = "neutral",
}: {
  children: React.ReactNode;
  tone?: "neutral" | "success" | "danger";
}) {
  return <span className={`status ${tone}`}>{children}</span>;
}

function formatBytes(value: bigint): string {
  const bytes = Number(value);
  return bytes < 1024 * 1024
    ? `${(bytes / 1024).toFixed(1)} KB`
    : `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function isAdminReasonValid(reason: string, publicAssetBaseUrl: string): boolean {
  try {
    validateAdminReason(reason, publicAssetBaseUrl);
    return true;
  } catch {
    return false;
  }
}

function formatTime(
  value: { seconds: bigint; nanos: number } | undefined,
  locale: Locale,
): string {
  if (!value) return "—";
  return new Intl.DateTimeFormat(locale === "ko" ? "ko-KR" : "en-US", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(Number(value.seconds) * 1000 + value.nanos / 1_000_000));
}
