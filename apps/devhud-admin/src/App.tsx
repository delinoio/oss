import { Code, ConnectError } from "@connectrpc/connect";
import {
  AdministrativeBlockState,
  AuditAction,
  AuditOutcome,
  QuotaKind,
  StaticCapability,
  UploadState,
  type AdminUpload,
  type AdminUser,
  type AuditEvent,
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
    };

type View = "users" | "uploads" | "audit";
type Loadable<T> =
  | { kind: "loading" }
  | { kind: "loaded"; value: T }
  | { kind: "permission" }
  | { kind: "error" };
type UsageState =
  | { kind: "loading"; user: AdminUser }
  | { kind: "loaded"; user: AdminUser; counters: UsageCounter[] }
  | { kind: "error"; user: AdminUser };

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
    let current = true;
    void (async () => {
      try {
        const bootstrap = await getBootstrap();
        const auth = AdminAuth.fromBootstrap(bootstrap);
        if (await auth.completeCallback(window.location.href)) {
          history.replaceState(null, "", "/admin/");
        }
        const next: Phase = (await auth.isAuthenticated())
          ? {
              kind: "ready",
              auth,
              client: createAdminClient(auth),
              uploadsAvailable: bootstrap.capabilities.includes(
                StaticCapability.OFFICIAL_UPLOADS,
              ),
            }
          : { kind: "signed-out", auth };
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
    document.documentElement.lang = next;
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
  uploadsAvailable,
}: {
  auth: AdminAuth;
  client: AdminClient;
  copy: ReturnType<typeof text>;
  locale: Locale;
  onToggleLocale: () => void;
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
      <nav aria-label="Administration" className="tabs">
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
          <Users client={client} copy={copy} locale={locale} />
        ) : view === "uploads" ? (
          uploadsAvailable ? (
            <Uploads client={client} copy={copy} locale={locale} />
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
}: {
  client: AdminClient;
  copy: ReturnType<typeof text>;
  locale: Locale;
}) {
  const [query, setQuery] = useState("");
  const [state, setState] = useState<Loadable<{ users: AdminUser[]; next: string }>>({
    kind: "loading",
  });
  const [mutation, setMutation] = useState<AdminUser | null>(null);
  const [usage, setUsage] = useState<UsageState | null>(null);

  const load = async (token = "", append = false) => {
    if (!append) setState({ kind: "loading" });
    try {
      const response = await client.listUsers({
        query,
        page: { pageSize: 50, pageToken: token },
      });
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
      setState(errorState(error));
    }
  };

  useEffect(() => {
    void load();
    // Query is applied by the explicit search form.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <section aria-labelledby="users-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">Identity</p>
          <h1 id="users-title">{copy.users}</h1>
        </div>
        <form
          className="search"
          onSubmit={(event) => {
            event.preventDefault();
            void load();
          }}
          role="search"
        >
          <label className="sr-only" htmlFor="user-query">
            {copy.search}
          </label>
          <input
            id="user-query"
            maxLength={512}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={copy.search}
            value={query}
          />
          <button className="primary" type="submit">
            {copy.users}
          </button>
        </form>
      </div>
      <Resource state={state} copy={copy}>
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
                      {AdministrativeBlockState[
                        user.administrativeBlockState
                      ]?.toLowerCase()}
                    </Status>
                  </div>
                  <dl>
                    <dt>Logto subject</dt>
                    <dd>{user.logtoSubject}</dd>
                    <dt>Created</dt>
                    <dd>{formatTime(user.createdAt, locale)}</dd>
                  </dl>
                  <div className="card-actions">
                    <button className="secondary" onClick={() => setMutation(user)}>
                      {user.administrativeBlockState ===
                      AdministrativeBlockState.BLOCKED
                        ? copy.unblock
                        : copy.block}
                    </button>
                    <button
                      className="text-button"
                      onClick={() => {
                        setUsage({ kind: "loading", user });
                        void client
                          .getUserUsage({ userId: user.userId })
                          .then((response) =>
                            setUsage({ kind: "loaded", user, counters: response.counters }),
                            () => setUsage({ kind: "error", user }),
                          )
                      }}
                    >
                      {copy.usage}
                    </button>
                  </div>
                </article>
              ))}
            </div>
            {value.next && (
              <button className="load-more" onClick={() => void load(value.next, true)}>
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
          user={mutation}
          onClose={() => setMutation(null)}
          onDone={() => {
            setMutation(null);
            void load();
          }}
        />
      )}
      {usage && (
        <Dialog title={copy.usage} onClose={() => setUsage(null)}>
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
                  <dt>{humanEnum(QuotaKind[counter.quota])}</dt>
                  <dd>
                    {counter.used.toLocaleString()} / {counter.limit.toLocaleString()}
                  </dd>
                </div>
              ))}
            </dl>
          )}
          <button className="primary" data-autofocus onClick={() => setUsage(null)}>
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
  onClose,
  onDone,
}: {
  user: AdminUser;
  client: AdminClient;
  copy: ReturnType<typeof text>;
  onClose: () => void;
  onDone: () => void;
}) {
  const blocked =
    user.administrativeBlockState === AdministrativeBlockState.BLOCKED;
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [pending, setPending] = useState(false);
  const [failed, setFailed] = useState(false);
  return (
    <Dialog title={blocked ? copy.unblock : copy.block} onClose={onClose}>
      <MutationFields
        confirmed={confirmed}
        copy={copy}
        onConfirmed={setConfirmed}
        onReason={setReason}
        reason={reason}
      />
      {failed && <p className="inline-error" role="alert">{copy.mutationError}</p>}
      <DialogActions
        copy={copy}
        disabled={!reason.trim() || !confirmed || pending}
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
            .then(onDone, () => {
              setPending(false);
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
}: {
  client: AdminClient;
  copy: ReturnType<typeof text>;
  locale: Locale;
}) {
  const [state, setState] = useState<Loadable<{ uploads: AdminUpload[]; next: string }>>({
    kind: "loading",
  });
  const [selected, setSelected] = useState<{
    upload: AdminUpload;
    action: "quarantine" | "delete";
  } | null>(null);
  const load = async (token = "", append = false) => {
    if (!append) setState({ kind: "loading" });
    try {
      const response = await client.listUploads({
        page: { pageSize: 50, pageToken: token },
      });
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
      setState(errorState(error));
    }
  };
  useEffect(() => {
    void load();
  }, []);

  return (
    <section aria-labelledby="uploads-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">Moderation</p>
          <h1 id="uploads-title">{copy.uploads}</h1>
        </div>
      </div>
      <Resource state={state} copy={copy}>
        {(value) => (
          <>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Upload</th>
                    <th>Group</th>
                    <th>State</th>
                    <th>Size</th>
                    <th>Created</th>
                    <th aria-label="Actions" />
                  </tr>
                </thead>
                <tbody>
                  {value.uploads.map((upload) => (
                    <tr key={upload.uploadId?.value}>
                      <td className="mono">{upload.uploadId?.value}</td>
                      <td className="mono">{upload.uploadGroupId?.value}</td>
                      <td><Status>{UploadState[upload.state]?.toLowerCase()}</Status></td>
                      <td>{formatBytes(upload.sizeBytes)}</td>
                      <td>{formatTime(upload.createdAt, locale)}</td>
                      <td className="row-actions">
                        {upload.state === UploadState.FINALIZED && (
                          <button
                            className="secondary"
                            onClick={() => setSelected({ upload, action: "quarantine" })}
                          >
                            {copy.quarantine}
                          </button>
                        )}
                        {(upload.state === UploadState.FINALIZED ||
                          upload.state === UploadState.PENDING ||
                          upload.state === UploadState.QUARANTINED) && (
                          <button
                            className="danger-button"
                            onClick={() => setSelected({ upload, action: "delete" })}
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
              <button className="load-more" onClick={() => void load(value.next, true)}>
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
          selection={selected}
          onClose={() => setSelected(null)}
          onDone={() => {
            setSelected(null);
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
  onClose,
  onDone,
}: {
  selection: { upload: AdminUpload; action: "quarantine" | "delete" };
  client: AdminClient;
  copy: ReturnType<typeof text>;
  onClose: () => void;
  onDone: () => void;
}) {
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [pending, setPending] = useState(false);
  const [failed, setFailed] = useState(false);
  const label = selection.action === "delete" ? copy.delete : copy.quarantine;
  return (
    <Dialog title={label} onClose={onClose}>
      <p className="mono break">{selection.upload.uploadId?.value}</p>
      <MutationFields
        confirmed={confirmed}
        copy={copy}
        onConfirmed={setConfirmed}
        onReason={setReason}
        reason={reason}
      />
      {failed && <p className="inline-error" role="alert">{copy.mutationError}</p>}
      <DialogActions
        copy={copy}
        disabled={!reason.trim() || !confirmed || pending}
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
          void promise.then(onDone, () => {
            setPending(false);
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
  const load = async (token = "", append = false) => {
    if (!append) setState({ kind: "loading" });
    try {
      const response = await client.listAuditEvents({
        page: { pageSize: 50, pageToken: token },
      });
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
      setState(errorState(error));
    }
  };
  useEffect(() => {
    void load();
  }, []);
  return (
    <section aria-labelledby="audit-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">Security</p>
          <h1 id="audit-title">{copy.audit}</h1>
        </div>
      </div>
      <Resource state={state} copy={copy}>
        {(value) => (
          <>
            <ol className="timeline">
              {value.events.map((event) => (
                <li key={event.auditEventId?.value}>
                  <div>
                    <strong>{humanEnum(AuditAction[event.action])}</strong>
                    <p>{event.reason || "—"}</p>
                    <span className="mono">{event.correlationId?.value}</span>
                  </div>
                  <div className="timeline-meta">
                    <Status tone={event.outcome === AuditOutcome.REJECTED ? "danger" : "success"}>
                      {AuditOutcome[event.outcome]?.toLowerCase()}
                    </Status>
                    <time>{formatTime(event.createdAt, locale)}</time>
                  </div>
                </li>
              ))}
            </ol>
            {value.next && (
              <button className="load-more" onClick={() => void load(value.next, true)}>
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
}: {
  copy: ReturnType<typeof text>;
  reason: string;
  confirmed: boolean;
  onReason: (value: string) => void;
  onConfirmed: (value: boolean) => void;
}) {
  return (
    <>
      <label htmlFor="mutation-reason">{copy.reason}</label>
      <textarea
        data-autofocus
        id="mutation-reason"
        maxLength={4096}
        onChange={(event) => onReason(event.target.value)}
        rows={4}
        value={reason}
      />
      <p className="help">{copy.reasonHelp}</p>
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
}: {
  state: Loadable<T>;
  copy: ReturnType<typeof text>;
  children: (value: T) => React.ReactNode;
}) {
  if (state.kind === "loading") return <Empty message={copy.loading} busy />;
  if (state.kind === "permission") return <Empty message={copy.permission} />;
  if (state.kind === "error") return <Empty message={copy.error} />;
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
  return ConnectError.from(error).code === Code.PermissionDenied
    ? { kind: "permission" }
    : { kind: "error" };
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

function humanEnum(value: string | undefined): string {
  return value?.toLowerCase().replaceAll("_", " ") ?? "—";
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
