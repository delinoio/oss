import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { messages } from "./localization";
import type { IdentitySession } from "./identity-client";
import { AccountIdentity, FirstRunIdentity, ShortcutPaletteTrigger, SynchronizedAppearanceBoundary, SynchronizedSettingsBoundary, SynchronizedShortcutBoundary, UrlMappingDraftProvider } from "./identity-ui";
import { LifecycleState, NativeBridgeError, NotificationPermission, RuntimePlatform, nativeBridge, type NativeBridgeEventV1, type NativeBridgeV1, type RuntimeSnapshot } from "./native-bridge";
import { clearIdentityForApiChange, DevHudServiceBoundary } from "./service-boundary";
import { ContentStateKind, ContentStateView, EmptyState, OfflineState, type ContentState } from "./surface-state";
import { ActionId, ExternalLinkTarget, LanguagePreference, PlatformCapability, SurfaceId, actionRegistry, availableActions, browserShell, completeOnboarding, getLocalStorage, hasCompletedOnboarding, isValidApiOrigin, markFrontendReady, normalizeApiOrigin, readPreferences, resolveLanguage, setTrayLanguage, synchronizeDocumentPreferences, writePreferences, type Preferences, type RuntimeCapabilities } from "./shell";
import { ShortcutActionId } from "./shortcuts";
import { RealqaSurface, type CaptureActionId, type RealqaController } from "./realqa-ui";

const surfaces: readonly SurfaceId[] = [SurfaceId.Home, SurfaceId.Realqa, SurfaceId.Deck, SurfaceId.Settings, SurfaceId.Account, SurfaceId.Diagnostics];
const labels: Record<SurfaceId, keyof typeof messages.en> = { home: "home", realqa: "realqa", deck: "deck", settings: "settings", account: "account", diagnostics: "diagnostics" };
const notificationPermissionLabels: Record<NotificationPermission, keyof typeof messages.en> = {
  [NotificationPermission.NotDetermined]: "notificationNotDetermined",
  [NotificationPermission.Denied]: "notificationDenied",
  [NotificationPermission.Authorized]: "notificationAuthorized",
};
const defaultContentState: ContentState = { kind: ContentStateKind.Ready };
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
  if (runtime.capabilities.capture) available.add(PlatformCapability.Capture);
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
  const [notificationRequestFailed, setNotificationRequestFailed] = useState(false);
  const [storeConfigured, setStoreConfigured] = useState(false);
  const [storeOpenFailed, setStoreOpenFailed] = useState(false);
  const [authCallback, setAuthCallback] = useState<string | null>(null);
  const [online, setOnline] = useState(() => navigator.onLine);
  const [requestedCapture, setRequestedCapture] = useState<{ action: CaptureActionId; sequence: number } | null>(null);
  const search = useRef<HTMLInputElement>(null);
  const apiOriginInput = useRef<HTMLInputElement>(null);
  const paletteRef = useRef<HTMLElement>(null);
  const captureSequence = useRef(0);
  const paletteTrigger = useRef<HTMLButtonElement>(null);
  const externalAttempt = useRef(0);
  const identitySession = useRef<IdentitySession | null>(null);
  const language = preferences.language === LanguagePreference.System ? systemLanguage : preferences.language;
  const copy = messages[language];
  const runtimeCapabilities = runtime ? capabilitiesFor(runtime) : { available: new Set<PlatformCapability>() };
  const mobile = runtime?.platform === RuntimePlatform.Ios || runtime?.platform === RuntimePlatform.Android;
  const isMac = runtime?.platform === RuntimePlatform.Ios || /Mac/u.test(navigator.userAgent);
  const shortcutContext = useRef({ mobile, onboarding, capabilities: runtimeCapabilities });
  const realqaController = useRef<RealqaController | null>(null);
  shortcutContext.current = { mobile, onboarding, capabilities: runtimeCapabilities };
  const consumeRequestedCapture = useCallback((sequence: number) => {
    setRequestedCapture((current) => current?.sequence === sequence ? null : current);
  }, []);

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
    let active = true;
    let unlisten: (() => void) | undefined;
    const receive = (event: NativeBridgeEventV1) => {
      if (event.version !== 1) return;
      if (event.kind === "lifecycle") setLifecycle(event.state);
      if (event.kind === "auth-callback") {
        setAuthCallback(event.url);
      }
      if (event.kind === "shortcut-triggered") {
        const context = shortcutContext.current;
        if (context.mobile || context.onboarding) return;
        if (event.action === ShortcutActionId.CommandPalette) {
          setPalette(true);
          return;
        }
        if (event.action.startsWith("realqa.capture.")) {
          setPalette(false);
          setSurface(SurfaceId.Realqa);
          captureSequence.current += 1;
          setRequestedCapture({ action: event.action as CaptureActionId, sequence: captureSequence.current });
          return;
        }
        const action = actionRegistry.find((candidate) => candidate.id === event.action);
        if (action && action.required.every((required) => context.capabilities.available.has(required)) && action.surface) setSurface(action.surface);
      }
    };
    void bridge.listen(receive).then(async (value) => {
      if (!active) { value(); return; }
      unlisten = value;
      if (initialRuntime) return;
      const response = await bridge.request({ operation: "runtime.snapshot" });
      if (!active || response.kind !== "runtime") return;
      setRuntime(response.snapshot);
      setLifecycle(response.snapshot.lifecycle);
      setRuntimeState(initialContentState);
      const pending = await bridge.request({ operation: "auth.peek-pending-callback" });
      if (active && pending.kind === "auth-callback" && pending.url) setAuthCallback(pending.url);
    }).catch(() => {
      if (active && !initialRuntime) setRuntimeState({ kind: ContentStateKind.Error, retryable: true });
    });
    return () => {
      active = false;
      unlisten?.();
    };
  }, [bridge, initialContentState, initialRuntime]);
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
    const key = (event: KeyboardEvent) => { if (event.key === "Escape" && palette) closePalette(); };
    addEventListener("keydown", key);
    return () => removeEventListener("keydown", key);
  }, [palette]);
  useEffect(() => { if (palette) search.current?.focus(); }, [palette]);
  useEffect(() => { if (surface === SurfaceId.Account) apiOriginInput.current?.focus(); }, [surface]);

  const actions = useMemo(() => availableActions(runtimeCapabilities).filter((action) => copy[action.title].toLowerCase().includes(query.toLowerCase())), [copy, query, runtime]);
  const unavailableCaptureActions = actionRegistry.filter((action) => action.required.includes(PlatformCapability.Capture) && !runtimeCapabilities.available.has(PlatformCapability.Capture));
  const execute = (id: ActionId) => {
    const action = actions.find((item) => item.id === id);
    if (action?.surface) setSurface(action.surface);
    if (id.startsWith("realqa.capture.")) {
      captureSequence.current += 1;
      setRequestedCapture({ action: id as CaptureActionId, sequence: captureSequence.current });
    }
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
  const openExternal = async (target: ExternalLinkTarget) => {
    if (mobile) {
      if (target === ExternalLinkTarget.Issue) throw new Error("mobile issue creation is unavailable");
      await bridge.request({ operation: "lifecycle.open-external", target, apiOrigin: preferences.apiOrigin });
      return;
    }
    await browserShell.openExternal(target, preferences.apiOrigin);
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
      await openExternal(target);
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
  const clearConsumedAuthCallback = useCallback((url: string) => {
    setAuthCallback((current) => current === url ? null : current);
  }, []);
  const applyApiOrigin = async (nextOrigin: string) => {
    const normalized = normalizeApiOrigin(nextOrigin);
    if (normalized === null || normalized === normalizeApiOrigin(preferences.apiOrigin)) return;
    if (!window.confirm(copy.apiChangeConfirm)) return;
    try { await clearIdentityForApiChange(bridge, storage, preferences.apiOrigin, identitySession); }
    catch { setExternalMessage("failed"); return; }
    setAuthCallback(null);
    update({ apiOrigin: normalized });
    const policy = await bridge.request({ operation: "session.configure-origins", apiOrigin: normalized });
    if (policy.kind === "session-network-policy" && policy.changed) location.reload();
  };
  const requestNotifications = async () => {
    setNotificationRequestFailed(false);
    try {
      const response = await bridge.request({ operation: "notifications.request-permission" });
      if (response.kind === "notification-permission") setNotificationPermission(response.permission);
    } catch (error) {
      if (error instanceof NativeBridgeError) setNotificationRequestFailed(true);
      else setRuntimeState({ kind: ContentStateKind.Error, retryable: true });
    }
  };
  const openStore = async () => {
    setStoreOpenFailed(false);
    try { await bridge.request({ operation: "updates.open-store" }); }
    catch (error) {
      if (error instanceof NativeBridgeError) setStoreOpenFailed(true);
      else setRuntimeState({ kind: ContentStateKind.Error, retryable: true });
    }
  };
  const supportsLaunchAtLogin = runtimeCapabilities.available.has(PlatformCapability.LaunchAtLogin);
  const externalMessageText = externalMessage === "invalid-api-origin" ? copy.invalidApiOrigin : externalMessage === "opened" ? copy.externalOpened : copy.externalFailed;
  const externalMessageIsError = externalMessage !== "opened";

  const boundary = (content: ReactNode) => runtime ? <DevHudServiceBoundary key={preferences.apiOrigin} apiOrigin={preferences.apiOrigin} active online={online} callbackUrl={authCallback} platform={runtime.platform} bridge={bridge} onCallbackConsumed={clearConsumedAuthCallback} onContinueLocally={finishOnboarding} onLoggedOut={() => setSurface(SurfaceId.Account)} initialAppearance={{ theme: preferences.theme, language: preferences.language }} identitySessionRef={identitySession}><UrlMappingDraftProvider><SynchronizedAppearanceBoundary onAppearance={(appearance) => update({ theme: appearance.theme, language: appearance.language })} />{content}</UrlMappingDraftProvider></DevHudServiceBoundary> : content;

  if (runtimeState.kind !== ContentStateKind.Ready) return <main className="app-shell onboarding" data-devhud-ready="true"><section className="content"><ContentStateView state={runtimeState} copy={copy} onRetry={() => location.reload()} /></section></main>;

  if (onboarding) return boundary(<main className="app-shell onboarding" data-devhud-ready="true" data-runtime-platform={runtime?.platform ?? "loading"}><section className="content"><FirstRunIdentity copy={copy} apiOrigin={preferences.apiOrigin} onApiOrigin={applyApiOrigin} onComplete={finishOnboarding} />{externalMessage && <p className="external-message" role={externalMessageIsError ? "alert" : "status"}>{externalMessageText}</p>}</section></main>);

  return boundary(<main className="app-shell" data-devhud-ready="true" data-runtime-platform={runtime?.platform ?? "desktop"} data-lifecycle={lifecycle}>
    {runtime?.platform === RuntimePlatform.Desktop && <SynchronizedShortcutBoundary bridge={bridge} />}
    <aside aria-label={copy.mobileNavigation}>
      <h1>{copy.appName}</h1>
      <nav>{surfaces.map((item) => <button className={surface === item ? "active" : ""} aria-current={surface === item ? "page" : undefined} key={item} onClick={() => setSurface(item)}>{copy[labels[item]]}</button>)}</nav>
      {mobile ? <button className="palette-trigger" ref={paletteTrigger} onClick={() => setPalette(true)} aria-label={copy.openPalette}>{copy.openPalette}</button> : <ShortcutPaletteTrigger copy={copy} isMac={isMac} triggerRef={paletteTrigger} onOpen={() => setPalette(true)} />}
    </aside>
    <section className="content" aria-live="polite">
      {surface === SurfaceId.Home && <><p className="eyebrow">{copy.available}</p><h2>{copy.welcome}</h2><p>{copy.homeSummary}</p></>}
      {surface === SurfaceId.Realqa && mobile && <><p className="eyebrow">{copy.desktopOnly}</p><h2>{copy.realqaMobileTitle}</h2><p>{copy.realqaMobileSummary}</p><p className="notice">{copy.unavailable}</p></>}
      {surface === SurfaceId.Realqa && !mobile && runtimeCapabilities.available.has(PlatformCapability.Capture) && <RealqaSurface ref={realqaController} bridge={bridge} copy={copy} requestedAction={requestedCapture} onRequestedActionConsumed={consumeRequestedCapture} />}
      {surface === SurfaceId.Realqa && !mobile && !runtimeCapabilities.available.has(PlatformCapability.Capture) && <><p className="eyebrow">{copy.realqa}</p><h2>{copy.realqaTitle}</h2><p>{copy.realqaSummary}</p><div className="disabled-actions">{unavailableCaptureActions.map((action) => <button disabled key={action.id}>{copy[action.title]}</button>)}</div><p className="notice">{copy.unavailable}</p></>}
      {surface === SurfaceId.Deck && <><p className="eyebrow">{copy.deck}</p><h2>{copy.deckTitle}</h2><p>{copy.deckSummary}</p>{online ? <EmptyState copy={copy} /> : <OfflineState copy={copy} />}</>}
      {surface === SurfaceId.Settings && <><p className="eyebrow">{copy.settings}</p><h2>{copy.settingsTitle}</h2><p>{copy.settingsSummary}</p><SynchronizedSettingsBoundary copy={copy} bridge={bridge} onOpenExternal={openExternal} showNativeShortcuts={runtime?.platform === RuntimePlatform.Desktop} shortcutCapabilities={runtimeCapabilities} />{supportsLaunchAtLogin && <><label className="check"><input type="checkbox" checked={preferences.launchAtLogin} onChange={(event) => { update({ launchAtLogin: event.target.checked }); void browserShell.setLaunchAtLogin(event.target.checked); }} />{copy.launchAtLogin}</label><p>{copy.launchAtLoginHint}</p></>}{runtime?.capabilities.notifications && <div className="native-setting"><button className="primary" onClick={() => void requestNotifications()}>{copy.notificationPermission}</button><output aria-live="polite">{copy[notificationPermissionLabels[notificationPermission]]}</output>{notificationRequestFailed && <p className="native-setting-error" role="alert">{copy.notificationPermissionFailed}</p>}</div>}{runtime?.capabilities.storeUpdates && <div className="native-setting"><p>{copy.updatePolicy}</p>{storeConfigured && <button className="primary" onClick={() => void openStore()}>{copy.updatePolicy}</button>}{storeOpenFailed && <p className="native-setting-error" role="alert">{copy.storeOpenFailed}</p>}</div>}</>}
      {surface === SurfaceId.Account && <><AccountIdentity copy={copy} apiOrigin={preferences.apiOrigin} inputRef={apiOriginInput} onApiOrigin={applyApiOrigin} /><div className="actions"><button onClick={() => void external(ExternalLinkTarget.Pat)}>{copy.githubCreateFinePat}</button><button onClick={() => void external(ExternalLinkTarget.ClassicPat)}>{copy.githubCreateClassicPat}</button>{!mobile && <button onClick={() => void external(ExternalLinkTarget.Issue)}>{copy.issue}</button>}</div>{externalMessage && <p className="external-message" role={externalMessageIsError ? "alert" : "status"}>{externalMessageText}</p>}</>}
      {surface === SurfaceId.Diagnostics && <><p className="eyebrow">{copy.diagnostics}</p><h2>{copy.diagnosticsTitle}</h2><p>{copy.diagnosticsSummary}</p><p className="notice">{copy.diagnosticsUnavailable}</p>{runtime && <dl className="runtime-diagnostics"><dt>{copy.diagnosticPlatform}</dt><dd>{runtime.platform}</dd><dt>{copy.diagnosticArchitecture}</dt><dd>{runtime.architecture}</dd><dt>{copy.diagnosticBridge}</dt><dd>v{runtime.bridgeVersion}</dd></dl>}</>}
    </section>
    {palette && <div className="overlay" role="presentation"><section ref={paletteRef} className="palette" role="dialog" aria-modal="true" aria-label={copy.commandPalette} onKeyDown={trapPaletteFocus}><input ref={search} value={query} onChange={(event) => setQuery(event.target.value)} placeholder={copy.searchCommands} aria-label={copy.searchCommands} /><div className="commands">{actions.length === 0 ? <p role="status">{copy.noCommands}</p> : actions.map((action) => <button key={action.id} onClick={() => execute(action.id)}>{copy[action.title]}</button>)}</div><button onClick={() => closePalette()}>{copy.close}</button></section></div>}
  </main>);
}
