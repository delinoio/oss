import { useEffect, useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  desktopApi,
  errorCode,
  type CandidateState,
  type DesktopApi,
  type ExcludedApp,
  type Settings,
} from "./api";
import { messages } from "./locale";

enum Section {
  General = "general",
  Exclusions = "exclusions",
  Shortcuts = "shortcuts",
  Updates = "updates",
  Diagnostics = "diagnostics",
}

function Modal({
  title,
  children,
  onClose,
}: {
  title: string;
  children: ReactNode;
  onClose(): void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current!;
    dialog.showModal();
    dialog.querySelector<HTMLElement>("[data-autofocus]")?.focus();
    return () => dialog.close();
  }, []);
  return (
    <dialog ref={ref} aria-label={title} onCancel={onClose} onClose={onClose}>
      <h2>{title}</h2>
      {children}
    </dialog>
  );
}

export function App({ api = desktopApi }: { api?: DesktopApi }) {
  const client = useQueryClient();
  const query = useQuery({
    queryKey: ["desktop"],
    queryFn: () => api.state(),
    refetchInterval: 1500,
    retry: false,
  });
  const [section, setSection] = useState(Section.General);
  const [error, setError] = useState<string | null>(null);
  const [permissionDismissed, setPermissionDismissed] = useState(false);
  const [inputSetupOpen, setInputSetupOpen] = useState(false);
  const permissionRequested = useRef(false);
  const [capture, setCapture] = useState(false);
  const [choosing, setChoosing] = useState(false);
  const [apps, setApps] = useState<ExcludedApp[]>([]);
  const [appIndex, setAppIndex] = useState(0);
  const data = query.data;
  const language =
    data?.settings.uiLanguage === "system"
      ? data.osLanguage
      : (data?.settings.uiLanguage ?? "en");
  const t = messages[language];
  const save = useMutation({
    mutationFn: (settings: Settings) => api.save(settings, data!.revision),
    onSuccess: (state) => {
      client.setQueryData(["desktop"], state);
      setError(null);
    },
    onError: (reason) => {
      setError(errorCode(reason));
      void query.refetch();
    },
  });
  const action = useMutation({
    mutationFn: (name: Parameters<DesktopApi["action"]>[0]) => api.action(name),
    onSuccess: (_, name) => {
      if (name === "recheck") setInputSetupOpen(false);
      setError(null);
      void query.refetch();
    },
    onError: (reason) => setError(errorCode(reason)),
  });
  const requestPermission = action.mutate;
  useEffect(() => {
    if (
      data?.platform === "macos-arm64" &&
      (data.status === "permission-missing" ||
        data.status === "input-permission-missing") &&
      !permissionDismissed &&
      !permissionRequested.current
    ) {
      // Request from FixKey itself so macOS can list this app in Accessibility.
      // Polling must never repeat a dismissed system prompt.
      permissionRequested.current = true;
      requestPermission("request-permission");
    }
  }, [data?.platform, data?.status, permissionDismissed, requestPermission]);
  useEffect(() => {
    document.documentElement.lang = language;
    document.documentElement.dataset.theme = data?.settings.theme ?? "system";
  }, [language, data?.settings.theme]);
  const busy = save.isPending || action.isPending;
  const change = (patch: Partial<Settings>) => {
    if (data && !busy) save.mutate({ ...data.settings, ...patch });
  };
  const chooseApp = async () => {
    try {
      setApps(await api.apps());
      setAppIndex(0);
      setChoosing(true);
      setError(null);
    } catch (reason) {
      setError(errorCode(reason));
    }
  };
  if (!data)
    return (
      <main className="connection">
        <h1>FixKey</h1>
        <p role="status">{query.isError ? t.connection : t.loading}</p>
        {query.isError && (
          <button onClick={() => void query.refetch()}>{t.retry}</button>
        )}
      </main>
    );
  const settings = data.settings;
  const waitingForField = !data.active && data.status === "unknown-field";
  const permissionNeeded =
    data.status === "permission-missing" ||
    data.status === "input-permission-missing";
  const inputPermissionNeeded =
    data.platform === "macos-arm64" &&
    (data.status === "input-permission-missing" || inputSetupOpen);
  const closePermission = () => {
    setInputSetupOpen(false);
    setPermissionDismissed(true);
  };
  const permissionTitle = inputSetupOpen
    ? t.inputPermissionSetup
    : inputPermissionNeeded
      ? t.inputPermission
      : t.permission;
  const permissionRequestLabel = inputPermissionNeeded
    ? t.inputPermissionRequest
    : t.permissionRequest;
  const permissionInstruction =
    data.platform === "macos-arm64"
      ? t.macPermission
      : data.platform === "windows-x64"
        ? t.windowsPermission
        : t.linuxPermission;
  const row = (label: string, control: ReactNode) => (
    <div className="row">
      <span>{label}</span>
      {control}
    </div>
  );
  const toggle = (label: string, checked: boolean, field: keyof Settings) => (
    <label className="row">
      <span>{label}</span>
      <input
        type="checkbox"
        checked={checked}
        disabled={busy}
        onChange={(e) => change({ [field]: e.target.checked })}
      />
    </label>
  );
  const select = (
    label: string,
    value: string,
    field: "theme" | "uiLanguage",
    options: [string, string][],
  ) => (
    <label className="row">
      <span>{label}</span>
      <select
        aria-label={label}
        value={value}
        disabled={busy}
        onChange={(e) => change({ [field]: e.target.value })}
      >
        {options.map(([key, name]) => (
          <option value={key} key={key}>
            {name}
          </option>
        ))}
      </select>
    </label>
  );
  return (
    <div className="shell">
      <aside>
        <h1>FixKey</h1>
        <p className="tagline">{t.tagline}</p>
        <nav aria-label="FixKey">
          {Object.values(Section).map((key) => (
            <button
              key={key}
              aria-current={section === key ? "page" : undefined}
              onClick={() => setSection(key)}
            >
              {t[key]}
            </button>
          ))}
        </nav>
        <p className="version">v{data.version}</p>
      </aside>
      <main>
        <h2>{t[section]}</h2>
        <div className={`status ${data.active ? "active" : ""}`} role="status">
          <strong>
            {data.active
              ? t.active
              : waitingForField
                ? t.waitingField
                : t.inactive}
          </strong>
          {waitingForField ? (
            <span>{t.waitingFieldHelp}</span>
          ) : (
            <span>
              {t.reason}: <code>{data.status}</code>
            </span>
          )}
        </div>
        {(error || data.settingsError || !data.logsHealthy) && (
          <div role="alert" className="error">
            <strong>{t.error}</strong>
            <p>
              {t.reason}:{" "}
              <code>{error ?? data.settingsError ?? "log-write-failed"}</code>
            </p>
            {data.settingsError && (
              <button
                disabled={busy}
                onClick={() => action.mutate("restore-backup")}
              >
                {t.restore}
              </button>
            )}
          </div>
        )}
        {data.diagnosticError && (
          <p role="alert">
            {t.reason}: <code>{data.diagnosticError}</code>
          </p>
        )}
        <fieldset disabled={busy} className="controls">
          {section === Section.General && (
            <>
              <section className="card">
                {row(
                  t.correction,
                  <button
                    className="primary"
                    onClick={() => change({ paused: !settings.paused })}
                  >
                    {settings.paused ? t.resume : t.pause}
                  </button>,
                )}
                <p>{t.local}</p>
                <button
                  onClick={() => {
                    setPermissionDismissed(false);
                    action.mutate("recheck");
                  }}
                >
                  {t.recheck}
                </button>
                {data.platform === "macos-arm64" && (
                  <button
                    onClick={() => {
                      setInputSetupOpen(true);
                      setPermissionDismissed(false);
                    }}
                  >
                    {t.inputPermissionSetup}
                  </button>
                )}
              </section>
              <section className="card">
                <h3>{t.languages}</h3>
                {toggle(t.korean, settings.koreanEnabled, "koreanEnabled")}
                {toggle(t.english, settings.englishEnabled, "englishEnabled")}
              </section>
              <section className="card">
                {toggle(t.startup, settings.loginStartup, "loginStartup")}
                {select(t.uiLanguage, settings.uiLanguage, "uiLanguage", [
                  ["system", t.system],
                  ["en", "English"],
                  ["ko", "한국어"],
                ])}
                {select(t.theme, settings.theme, "theme", [
                  ["system", t.system],
                  ["light", t.light],
                  ["dark", t.dark],
                ])}
              </section>
            </>
          )}
          {section === Section.Exclusions && (
            <section className="card">
              <div className="row">
                <h3>{t.exclusions}</h3>
                <button onClick={() => void chooseApp()}>{t.add}</button>
              </div>
              {settings.excludedApps.length === 0 ? (
                <div className="empty">
                  <h3>{t.empty}</h3>
                  <p>{t.emptyDetail}</p>
                </div>
              ) : (
                <ul className="apps">
                  {settings.excludedApps.map((app, index) => (
                    <li key={JSON.stringify(app.identity)}>
                      <div>
                        <strong>{app.displayName}</strong>
                        {data.unresolvedApps.includes(index) && (
                          <p>{t.unresolved}</p>
                        )}
                      </div>
                      <button
                        aria-label={`${t.remove} ${app.displayName}`}
                        onClick={() =>
                          change({
                            excludedApps: settings.excludedApps.filter(
                              (_, i) => i !== index,
                            ),
                          })
                        }
                      >
                        {t.remove}
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          )}
          {section === Section.Shortcuts && (
            <section className="card">
              {row(t.shortcut, <kbd>{settings.shortcut ?? t.unassigned}</kbd>)}
              <div className="actions">
                <button onClick={() => setCapture(true)}>{t.capture}</button>
                <button
                  disabled={!settings.shortcut}
                  onClick={() => change({ shortcut: null })}
                >
                  {t.clear}
                </button>
              </div>
            </section>
          )}
          {section === Section.Updates && (
            <section className="card">
              {row(t.currentVersion, <span>{data.version}</span>)}
              <p>{t.updateHelp}</p>
              <p role="status">
                <code>{data.update.phase}</code>
                {data.update.version && ` · ${data.update.version}`}
              </p>
              {data.update.error && (
                <p role="alert">
                  {t.reason}: <code>{data.update.error}</code>
                </p>
              )}
              {data.update.phase === "downloading" && (
                <>
                  <progress
                    aria-label={t.updateConsent}
                    max={100}
                    value={data.update.progress ?? undefined}
                  />
                  <span>
                    {data.update.progress == null
                      ? "…"
                      : `${Math.round(data.update.progress)}%`}
                  </span>
                </>
              )}
              <div className="actions">
                <button
                  disabled={["checking", "downloading", "installing"].includes(
                    data.update.phase,
                  )}
                  onClick={() => action.mutate("check-update")}
                >
                  {t.checkUpdate}
                </button>
                {data.update.phase === "available" && (
                  <button
                    className="primary"
                    onClick={() => action.mutate("install-update")}
                  >
                    {t.updateConsent}
                  </button>
                )}
              </div>
              <p>{t.recovery}</p>
            </section>
          )}
          {section === Section.Diagnostics && (
            <>
              <section className="card">
                {row(t.reason, <code>{data.status}</code>)}
                <p>{t.logHelp}</p>
                <div className="actions">
                  <button onClick={() => action.mutate("open-logs")}>
                    {t.openLogs}
                  </button>
                  <button onClick={() => action.mutate("delete-logs")}>
                    {t.deleteLogs}
                  </button>
                  <button onClick={() => action.mutate("support")}>
                    {t.support}
                  </button>
                </div>
              </section>
              <section className="card">
                <h3>{t.help}</h3>
                {t.helpTopics.map(([title, body]) => (
                  <details key={title}>
                    <summary>{title}</summary>
                    <p>{body}</p>
                  </details>
                ))}
              </section>
            </>
          )}
        </fieldset>
      </main>
      {(permissionNeeded || inputSetupOpen) && !permissionDismissed && (
        <Modal title={permissionTitle} onClose={closePermission}>
          <p>
            {inputSetupOpen
              ? t.inputPermissionListHelp
              : inputPermissionNeeded
                ? t.inputPermissionHelp
                : t.permissionHelp}
          </p>
          {data.status === "input-permission-missing" && (
            <p className="permission-granted">{t.accessibilityGranted}</p>
          )}
          <p className="permission-privacy">{t.local}</p>
          {data.platform === "macos-arm64" ? (
            <ol className="permission-steps">
              {(inputPermissionNeeded
                ? t.inputPermissionSteps
                : t.permissionSteps
              ).map((step) => (
                <li key={step}>{step}</li>
              ))}
            </ol>
          ) : (
            <p>{permissionInstruction}</p>
          )}
          {!inputSetupOpen && <p role="status">{t.permissionWaiting}</p>}
          {error && (
            <p role="alert">
              {t.error}: <code>{error}</code>
            </p>
          )}
          <div className="actions">
            <button
              className="primary"
              data-autofocus
              disabled={busy}
              onClick={() => action.mutate("request-permission")}
            >
              {data.platform === "macos-arm64"
                ? permissionRequestLabel
                : t.openSettings}
            </button>
            {data.platform === "macos-arm64" && (
              <button
                disabled={busy}
                onClick={() => action.mutate("permission-settings")}
              >
                {t.openSettings}
              </button>
            )}
            <button disabled={busy} onClick={() => action.mutate("recheck")}>
              {t.recheck}
            </button>
            <button onClick={closePermission}>{t.later}</button>
          </div>
        </Modal>
      )}
      {capture && (
        <Modal title={t.capture} onClose={() => setCapture(false)}>
          <p>{t.captureHelp}</p>
          <input
            autoFocus
            readOnly
            aria-label={t.capture}
            placeholder={t.unassigned}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                setCapture(false);
                return;
              }
              e.preventDefault();
              if (
                ["Control", "Alt", "Shift", "Meta"].includes(e.key) ||
                !(e.ctrlKey || e.altKey || e.metaKey || e.shiftKey)
              )
                return;
              const key = e.code.startsWith("Key")
                ? e.code.slice(3)
                : e.code.startsWith("Digit")
                  ? e.code.slice(5)
                  : e.code;
              change({
                shortcut: [
                  e.ctrlKey && "Control",
                  e.altKey && "Alt",
                  e.shiftKey && "Shift",
                  e.metaKey && "Super",
                  key,
                ]
                  .filter(Boolean)
                  .join("+"),
              });
              setCapture(false);
            }}
          />
          <button onClick={() => setCapture(false)}>{t.cancel}</button>
        </Modal>
      )}
      {choosing && (
        <Modal title={t.choose} onClose={() => setChoosing(false)}>
          {apps.length ? (
            <>
              <select
                autoFocus
                aria-label={t.choose}
                value={appIndex}
                onChange={(e) => setAppIndex(Number(e.target.value))}
              >
                {apps.map((app, i) => (
                  <option key={JSON.stringify(app.identity)} value={i}>
                    {app.displayName}
                  </option>
                ))}
              </select>
              <button
                className="primary"
                onClick={() => {
                  const app = apps[appIndex];
                  if (
                    !settings.excludedApps.some(
                      (other) =>
                        JSON.stringify(other.identity) ===
                        JSON.stringify(app.identity),
                    )
                  )
                    change({ excludedApps: [...settings.excludedApps, app] });
                  setChoosing(false);
                }}
              >
                {t.add}
              </button>
            </>
          ) : (
            <p>{t.noApps}</p>
          )}
          <button onClick={() => setChoosing(false)}>{t.cancel}</button>
        </Modal>
      )}
    </div>
  );
}

export function CandidatePicker({
  api = desktopApi,
  language = "en",
}: {
  api?: DesktopApi;
  language?: "en" | "ko";
}) {
  const [state, setState] = useState<CandidateState>({
    generation: 0,
    selected: 0,
    items: [],
  });
  const [error, setError] = useState<string | null>(null);
  const t = messages[state.language ?? language];
  useEffect(() => {
    document.documentElement.dataset.theme = state.theme ?? "system";
    document.documentElement.lang = state.language ?? language;
  }, [state.theme, state.language, language]);
  useEffect(() => {
    let cancelled = false;
    let stop: (() => void) | undefined;
    void api
      .candidates((next) => {
        if (!cancelled)
          setState((old) =>
            next.generation > old.generation ||
            (next.generation === old.generation &&
              (next.revision ?? 0) >= (old.revision ?? 0))
              ? next
              : old,
          );
      })
      .then((unsubscribe) => {
        if (cancelled) unsubscribe();
        else stop = unsubscribe;
      })
      .catch(() => setError("candidate-unavailable"));
    return () => {
      cancelled = true;
      stop?.();
    };
  }, [api]);
  const act = (
    action: "next" | "previous" | "apply" | "close",
    index?: number,
  ) => {
    if (action === "close") setState({ ...state, items: [] });
    void api
      .candidateAction(state.generation, action, index)
      .catch((reason) => {
        setError(errorCode(reason));
        setState((old) => ({ ...old, items: [] }));
      });
  };
  return (
    <main
      className="picker"
      onKeyDown={(e) => {
        const key = {
          ArrowDown: "next",
          ArrowUp: "previous",
          Enter: "apply",
          Escape: "close",
        }[e.key] as "next" | "previous" | "apply" | "close" | undefined;
        if (key) {
          e.preventDefault();
          act(key);
        }
      }}
    >
      <h1>{t.candidates}</h1>
      <p>{t.candidateHelp}</p>
      {state.items.length ? (
        <div role="listbox" aria-label={t.candidates}>
          {state.items.map((item, i) => (
            <button
              key={`${item.language}-${i}`}
              role="option"
              aria-selected={state.selected === i}
              onClick={() => act("apply", i)}
            >
              <strong>{item.text}</strong>
              <small>
                {item.isOriginal
                  ? `${t.original} · `
                  : `${item.language === "ko-dubeolsik" ? t.korean : t.english} · `}
                {item.validation === "recognized"
                  ? t.recognized
                  : item.validation === "not-recognized"
                    ? t.notRecognized
                    : t.unchecked}
              </small>
            </button>
          ))}
        </div>
      ) : (
        <p role="status">{t.noCandidates}</p>
      )}
      {error && (
        <p role="alert">
          {t.reason}: {error}
        </p>
      )}
    </main>
  );
}
