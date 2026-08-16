import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { messages } from "./localization";
import { LifecycleState, NativeBridgeError, NotificationPermission, RuntimePlatform, nativeBridge, type NativeBridgeEventV1, type NativeBridgeV1, type RuntimeSnapshot } from "./native-bridge";
import { ContentStateKind, ContentStateView, EmptyState, OfflineState, type ContentState } from "./surface-state";
import { ActionId, ExternalLinkTarget, LanguagePreference, PlatformCapability, SurfaceId, ThemePreference, actionRegistry, availableActions, browserShell, completeOnboarding, getLocalStorage, hasCompletedOnboarding, isValidApiOrigin, markFrontendReady, readPreferences, resolveLanguage, setTrayLanguage, synchronizeDocumentPreferences, writePreferences, type Preferences, type RuntimeCapabilities } from "./shell";

const surfaces: readonly SurfaceId[] = [SurfaceId.Home, SurfaceId.Realqa, SurfaceId.Deck, SurfaceId.Settings, SurfaceId.Account, SurfaceId.Diagnostics];
const labels: Record<SurfaceId, keyof typeof messages.en> = { home: "home", realqa: "realqa", deck: "deck", settings: "settings", account: "account", diagnostics: "diagnostics" };
const notificationPermissionLabels: Record<NotificationPermission, keyof typeof messages.en> = {
  [NotificationPermission.NotDetermined]: "notificationNotDetermined",
  [NotificationPermission.Denied]: "notificationDenied",
  [NotificationPermission.Authorized]: "notificationAuthorized",
};
const defaultContentState: ContentState = { kind: ContentStateKind.Ready };
const rightModifierLocation = 2;
type ExternalMessage = "opened" | "failed" | "invalid-api-origin";

export interface AppProps {
  readonly bridge?: NativeBridgeV1;
  readonly initialRuntime?: RuntimeSnapshot;
  readonly initialContentState?: ContentState;
}

function capabilitiesFor(runtime: RuntimeSnapshot): RuntimeCapabilities {
  const available = new Set<PlatformCapability>();
  if (runtime.platform === RuntimePlatform.Desktop) {
    available.add(PlatformCapability.Desktop);
    available.add(PlatformCapability.Tray);
  } else {
    available.add(PlatformCapability.Mobile);
  }
  if (runtime.capabilities.notifications) available.add(PlatformCapability.Notifications);
  if (runtime.capabilities.secureSettings) available.add(PlatformCapability.SecureSettings);
  return { available };
}

export function App({ bridge = nativeBridge, initialRuntime, initialContentState = defaultContentState }: AppProps) {
  const storage = getLocalStorage();
  const [preferences, setPreferences] = useState<Preferences>(() => readPreferences(storage));
  const [onboarding, setOnboarding] = useState(() => !hasCompletedOnboarding(storage));
  const [surface, setSurface] = useState<SurfaceId>(SurfaceId.Home);
  const [palette, setPalette] = useState(false);
  const [query, setQuery] = useState("");
  const [externalMessage, setExternalMessage] = useState<ExternalMessage | null>(null);
  const [systemLanguage, setSystemLanguage] = useState(() => resolveLanguage(LanguagePreference.System, navigator.languages));
  const [runtime, setRuntime] = useState<RuntimeSnapshot | undefined>(initialRuntime);
  const [runtimeState, setRuntimeState] = useState<ContentState>(() => initialRuntime ? initialContentState : { kind: ContentStateKind.Loading });
  const [lifecycle, setLifecycle] = useState<LifecycleState>(initialRuntime?.lifecycle ?? LifecycleState.Active);
  const [notificationPermission, setNotificationPermission] = useState<NotificationPermission>(NotificationPermission.NotDetermined);
  const [storeConfigured, setStoreConfigured] = useState(false);
  const [authCallbackReceived, setAuthCallbackReceived] = useState(false);
  const [online, setOnline] = useState(() => navigator.onLine);
  const search = useRef<HTMLInputElement>(null);
  const apiOriginInput = useRef<HTMLInputElement>(null);
  const paletteRef = useRef<HTMLElement>(null);
  const paletteTrigger = useRef<HTMLButtonElement>(null);
  const rightModifier = useRef<"ControlRight" | "MetaRight" | null>(null);
  const externalAttempt = useRef(0);
  const language = preferences.language === LanguagePreference.System ? systemLanguage : preferences.language;
  const copy = messages[language];
  const runtimeCapabilities = runtime ? capabilitiesFor(runtime) : { available: new Set<PlatformCapability>() };
  const mobile = runtime?.platform === RuntimePlatform.Ios || runtime?.platform === RuntimePlatform.Android;
  const isMac = runtime?.platform === RuntimePlatform.Ios || /Mac/u.test(navigator.userAgent);

  const update = (next: Partial<Preferences>) => {
    if ("apiOrigin" in next) {
      externalAttempt.current += 1;
      setExternalMessage(null);
    }
    const value = { ...preferences, ...next };
    synchronizeDocumentPreferences(document.documentElement, value, matchMedia("(prefers-color-scheme: dark)").matches, navigator.languages);
    writePreferences(storage, value);
    setPreferences(value);
  };
  const closePalette = (restoreTriggerFocus = true) => {
    setPalette(false);
    if (restoreTriggerFocus) requestAnimationFrame(() => paletteTrigger.current?.focus());
  };

  useEffect(() => {
    document.title = "DevHUD";
    void markFrontendReady()?.catch(() => {});
  }, []);
  useEffect(() => {
    if (initialRuntime) return;
    let active = true;
    void bridge.request({ operation: "runtime.snapshot" }).then((response) => {
      if (!active || response.kind !== "runtime") return;
      setRuntime(response.snapshot);
      setLifecycle(response.snapshot.lifecycle);
      setRuntimeState(initialContentState);
      return bridge.request({ operation: "auth.take-pending-callback" });
    }).then((response) => {
      if (active && response?.kind === "auth-callback" && response.url) setAuthCallbackReceived(true);
    }).catch(() => {
      if (active) setRuntimeState({ kind: ContentStateKind.Error, retryable: true });
    });
    return () => { active = false; };
  }, [bridge, initialContentState, initialRuntime]);
  useEffect(() => {
    let active = true;
    let unlisten: (() => void) | undefined;
    const receive = (event: NativeBridgeEventV1) => {
      if (event.version !== 1) return;
      if (event.kind === "lifecycle") setLifecycle(event.state);
      if (event.kind === "auth-callback") {
        setAuthCallbackReceived(true);
        void bridge.request({ operation: "auth.take-pending-callback" }).catch(() => {});
      }
    };
    void bridge.listen(receive).then((value) => {
      if (active) unlisten = value;
      else value();
    }).catch(() => {});
    return () => {
      active = false;
      unlisten?.();
    };
  }, [bridge]);
  useEffect(() => {
    if (!runtime?.capabilities.notifications || lifecycle !== LifecycleState.Active) return;
    let active = true;
    void bridge.request({ operation: "notifications.permission" }).then((response) => {
      if (active && response.kind === "notification-permission") setNotificationPermission(response.permission);
    }).catch(() => {});
    return () => { active = false; };
  }, [bridge, lifecycle, runtime?.capabilities.notifications]);
  useEffect(() => {
    if (!runtime?.capabilities.storeUpdates) return;
    let active = true;
    void bridge.request({ operation: "updates.status" }).then((response) => {
      if (active && response.kind === "update-status") setStoreConfigured(response.configured);
    }).catch(() => {});
    return () => { active = false; };
  }, [bridge, runtime?.capabilities.storeUpdates]);
  useEffect(() => {
    void setTrayLanguage(language).catch(() => {});
  }, [language]);
  useEffect(() => {
    const updateSystemLanguage = () => { setSystemLanguage(resolveLanguage(LanguagePreference.System, navigator.languages)); };
    addEventListener("languagechange", updateSystemLanguage);
    return () => removeEventListener("languagechange", updateSystemLanguage);
  }, []);
  useEffect(() => {
    const updateConnectivity = () => { setOnline(navigator.onLine); };
    addEventListener("online", updateConnectivity);
    addEventListener("offline", updateConnectivity);
    return () => {
      removeEventListener("online", updateConnectivity);
      removeEventListener("offline", updateConnectivity);
    };
  }, []);
  useEffect(() => {
    const media = matchMedia("(prefers-color-scheme: dark)");
    const updateTheme = () => { synchronizeDocumentPreferences(document.documentElement, preferences, media.matches, navigator.languages); };
    updateTheme();
    media.addEventListener("change", updateTheme);
    return () => media.removeEventListener("change", updateTheme);
  }, [preferences.language, preferences.theme, language]);
  useEffect(() => {
    const key = (event: KeyboardEvent) => {
      const platformModifier = isMac ? "MetaRight" : "ControlRight";
      if (event.code === platformModifier && event.location === rightModifierLocation) rightModifier.current = event.code;
      const matchingRightModifier = rightModifier.current === platformModifier && (isMac ? event.metaKey : event.ctrlKey);
      const exactRightModifierChord = matchingRightModifier && !event.shiftKey && !event.altKey && (isMac ? !event.ctrlKey : !event.metaKey);
      if (!mobile && !onboarding && exactRightModifierChord && event.code === "KeyK") {
        event.preventDefault();
        setPalette(true);
      }
      if (event.key === "Escape" && palette) closePalette();
    };
    const releaseRightModifier = (event: KeyboardEvent) => { if (rightModifier.current === event.code) rightModifier.current = null; };
    const clearRightModifier = () => { rightModifier.current = null; };
    addEventListener("keydown", key);
    addEventListener("keyup", releaseRightModifier);
    addEventListener("blur", clearRightModifier);
    return () => {
      removeEventListener("keydown", key);
      removeEventListener("keyup", releaseRightModifier);
      removeEventListener("blur", clearRightModifier);
    };
  }, [isMac, mobile, onboarding, palette]);
  useEffect(() => { if (palette) search.current?.focus(); }, [palette]);
  useEffect(() => { if (surface === SurfaceId.Account) apiOriginInput.current?.focus(); }, [surface]);

  const actions = useMemo(() => availableActions(runtimeCapabilities).filter((action) => copy[action.title].toLowerCase().includes(query.toLowerCase())), [copy, query, runtime]);
  const unavailableCaptureActions = actionRegistry.filter((action) => action.required.includes(PlatformCapability.Capture) && !runtimeCapabilities.available.has(PlatformCapability.Capture));
  const execute = (id: ActionId) => {
    const action = actions.find((item) => item.id === id);
    if (action?.surface) setSurface(action.surface);
    closePalette(action?.surface !== SurfaceId.Account);
    if (action?.surface === SurfaceId.Account) requestAnimationFrame(() => apiOriginInput.current?.focus());
  };
  const trapPaletteFocus = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (event.key !== "Tab") return;
    const focusable = paletteRef.current?.querySelectorAll<HTMLElement>("button:not([disabled]), input:not([disabled]), select:not([disabled]), [href]");
    if (!focusable?.length) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault(); last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault(); first.focus();
    }
  };
  const external = async (target: ExternalLinkTarget) => {
    const attempt = externalAttempt.current + 1;
    externalAttempt.current = attempt;
    const updateExternalMessage = (message: ExternalMessage | null) => { if (attempt === externalAttempt.current) setExternalMessage(message); };
    updateExternalMessage(null);
    if (target === ExternalLinkTarget.Authentication && !isValidApiOrigin(preferences.apiOrigin)) {
      updateExternalMessage("invalid-api-origin"); return false;
    }
    try {
      if (mobile) {
        if (target === ExternalLinkTarget.Issue) throw new Error("mobile issue creation is unavailable");
        await bridge.request({ operation: "lifecycle.open-external", target, apiOrigin: preferences.apiOrigin });
      } else {
        await browserShell.openExternal(target, preferences.apiOrigin);
      }
      updateExternalMessage("opened"); return true;
    } catch {
      updateExternalMessage("failed"); return false;
    }
  };
  const finishOnboarding = () => {
    externalAttempt.current += 1;
    setExternalMessage(null);
    completeOnboarding(storage);
    setOnboarding(false);
    setSurface(SurfaceId.Home);
  };
  const startSignIn = async () => { if (await external(ExternalLinkTarget.Authentication)) finishOnboarding(); };
  const requestNotifications = async () => {
    try {
      const response = await bridge.request({ operation: "notifications.request-permission" });
      if (response.kind === "notification-permission") setNotificationPermission(response.permission);
    } catch (error) {
      if (!(error instanceof NativeBridgeError)) setRuntimeState({ kind: ContentStateKind.Error, retryable: true });
    }
  };
  const openStore = async () => {
    try { await bridge.request({ operation: "updates.open-store" }); }
    catch (error) {
      if (!(error instanceof NativeBridgeError)) setRuntimeState({ kind: ContentStateKind.Error, retryable: true });
    }
  };
  const supportsLaunchAtLogin = runtimeCapabilities.available.has(PlatformCapability.LaunchAtLogin);
  const externalMessageText = externalMessage === "invalid-api-origin" ? copy.invalidApiOrigin : externalMessage === "opened" ? copy.externalOpened : copy.externalFailed;
  const externalMessageIsError = externalMessage !== "opened";

  if (runtimeState.kind !== ContentStateKind.Ready) return <main className="app-shell onboarding" data-devhud-ready="true"><section className="content"><ContentStateView state={runtimeState} copy={copy} onRetry={() => location.reload()} /></section></main>;

  if (onboarding) return <main className="app-shell onboarding" data-devhud-ready="true" data-runtime-platform={runtime?.platform ?? "loading"}><section className="content"><p className="eyebrow">{copy.account}</p><h1>{copy.accountTitle}</h1><p>{copy.accountSummary}</p><label>{copy.apiOrigin}<input autoFocus value={preferences.apiOrigin} onChange={(event) => update({ apiOrigin: event.target.value })} /></label><p>{copy.apiOriginHint}</p><div className="actions"><button onClick={() => void startSignIn()}>{copy.signIn}</button><button onClick={finishOnboarding}>{copy.continueLocally}</button></div>{externalMessage && <p className="external-message" role={externalMessageIsError ? "alert" : "status"}>{externalMessageText}</p>}</section></main>;

  return <main className="app-shell" data-devhud-ready="true" data-runtime-platform={runtime?.platform ?? "desktop"} data-lifecycle={lifecycle}>
    <aside aria-label={copy.mobileNavigation}>
      <h1>{copy.appName}</h1>
      <nav>{surfaces.map((item) => <button className={surface === item ? "active" : ""} aria-current={surface === item ? "page" : undefined} key={item} onClick={() => setSurface(item)}>{copy[labels[item]]}</button>)}</nav>
      <button className="palette-trigger" ref={paletteTrigger} onClick={() => setPalette(true)} aria-label={copy.openPalette}>{mobile ? copy.openPalette : isMac ? copy.rightCommandK : copy.rightControlK}</button>
    </aside>
    <section className="content" aria-live="polite">
      {surface === SurfaceId.Home && <><p className="eyebrow">{copy.available}</p><h2>{copy.welcome}</h2><p>{copy.homeSummary}</p></>}
      {surface === SurfaceId.Realqa && mobile && <><p className="eyebrow">{copy.desktopOnly}</p><h2>{copy.realqaMobileTitle}</h2><p>{copy.realqaMobileSummary}</p><p className="notice">{copy.unavailable}</p></>}
      {surface === SurfaceId.Realqa && !mobile && <><p className="eyebrow">{copy.realqa}</p><h2>{copy.realqaTitle}</h2><p>{copy.realqaSummary}</p><div className="disabled-actions">{unavailableCaptureActions.map((action) => <button disabled key={action.id}>{copy[action.title]}</button>)}</div><p className="notice">{copy.planned}</p></>}
      {surface === SurfaceId.Deck && <><p className="eyebrow">{copy.deck}</p><h2>{copy.deckTitle}</h2><p>{copy.deckSummary}</p>{online ? <EmptyState copy={copy} /> : <OfflineState copy={copy} />}</>}
      {surface === SurfaceId.Settings && <><p className="eyebrow">{copy.settings}</p><h2>{copy.settingsTitle}</h2><p>{copy.settingsSummary}</p><label>{copy.theme}<select value={preferences.theme} onChange={(event) => update({ theme: event.target.value as ThemePreference })}>{Object.values(ThemePreference).map((value) => <option key={value} value={value}>{copy[value]}</option>)}</select></label><label>{copy.language}<select value={preferences.language} onChange={(event) => update({ language: event.target.value as LanguagePreference })}><option value="system">{copy.system}</option><option value="en">{copy.english}</option><option value="ko">{copy.korean}</option></select></label>{supportsLaunchAtLogin && <><label className="check"><input type="checkbox" checked={preferences.launchAtLogin} onChange={(event) => { update({ launchAtLogin: event.target.checked }); void browserShell.setLaunchAtLogin(event.target.checked); }} />{copy.launchAtLogin}</label><p>{copy.launchAtLoginHint}</p></>}{runtime?.capabilities.notifications && <div className="native-setting"><button className="primary" onClick={() => void requestNotifications()}>{copy.notificationPermission}</button><output aria-live="polite">{copy[notificationPermissionLabels[notificationPermission]]}</output></div>}{runtime?.capabilities.storeUpdates && <div className="native-setting"><p>{copy.updatePolicy}</p>{storeConfigured && <button className="primary" onClick={() => void openStore()}>{copy.updatePolicy}</button>}</div>}</>}
      {surface === SurfaceId.Account && <><p className="eyebrow">{copy.account}</p><h2>{copy.accountTitle}</h2><p>{copy.accountSummary}</p><label>{copy.apiOrigin}<input ref={apiOriginInput} value={preferences.apiOrigin} onChange={(event) => update({ apiOrigin: event.target.value })} /></label><p>{copy.apiOriginHint}</p><div className="actions"><button onClick={() => void external(ExternalLinkTarget.Authentication)}>{copy.signIn}</button><button onClick={() => void external(ExternalLinkTarget.Pat)}>{copy.pat}</button>{!mobile && <button onClick={() => void external(ExternalLinkTarget.Issue)}>{copy.issue}</button>}</div>{authCallbackReceived && <p role="status">{copy.runtimeReady}</p>}{externalMessage && <p className="external-message" role={externalMessageIsError ? "alert" : "status"}>{externalMessageText}</p>}</>}
      {surface === SurfaceId.Diagnostics && <><p className="eyebrow">{copy.diagnostics}</p><h2>{copy.diagnosticsTitle}</h2><p>{copy.diagnosticsSummary}</p><p className="notice">{copy.diagnosticsUnavailable}</p>{runtime && <dl className="runtime-diagnostics"><dt>{copy.diagnosticPlatform}</dt><dd>{runtime.platform}</dd><dt>{copy.diagnosticArchitecture}</dt><dd>{runtime.architecture}</dd><dt>{copy.diagnosticBridge}</dt><dd>v{runtime.bridgeVersion}</dd></dl>}</>}
    </section>
    {palette && <div className="overlay" role="presentation"><section ref={paletteRef} className="palette" role="dialog" aria-modal="true" aria-label={copy.commandPalette} onKeyDown={trapPaletteFocus}><input ref={search} value={query} onChange={(event) => setQuery(event.target.value)} placeholder={copy.searchCommands} aria-label={copy.searchCommands} /><div className="commands">{actions.length === 0 ? <p role="status">{copy.noCommands}</p> : actions.map((action) => <button key={action.id} onClick={() => execute(action.id)}>{copy[action.title]}</button>)}</div><button onClick={() => closePalette()}>{copy.close}</button></section></div>}
  </main>;
}
