import { useCallback, useEffect, useMemo, useRef, useState, type ComponentType, type ReactNode } from "react";
import { messages, type Copy } from "./localization";
import { appendDiagnosticEvent, captureDiagnosticEvent, readDiagnosticCorrelations, readDiagnosticEvents, recentDiagnosticCorrelationIds } from "./diagnostics";
import { DiagnosticsPanel } from "./diagnostics-ui";
import { DiagnosticComponent, DiagnosticSeverity } from "@delinoio/devhud-api-client";
import type { IdentitySession } from "./identity-client";
import { AccountIdentity, FirstRunIdentity, ShortcutPaletteTrigger, SynchronizedAppearanceBoundary, SynchronizedSettingsBoundary, SynchronizedShortcutBoundary, UrlMappingDraftProvider } from "./identity-ui";
import { LifecycleState, NativeBridgeError, NotificationPermission, RuntimePlatform, nativeBridge, type CaptureDraft, type NativeBridgeEventV1, type NativeBridgeV1, type RuntimeSnapshot } from "./native-bridge";
import { clearIdentityForApiChange, DevHudServiceBoundary } from "./service-boundary";
import { ContentStateKind, ContentStateView, type ContentState } from "./surface-state";
import { DeckPollingBoundary, DeckSurface } from "./deck-ui.tsx";
import { ActionId, ExternalLinkTarget, LanguagePreference, PlatformCapability, SurfaceId, actionRegistry, availableActions, browserShell, completeOnboarding, getLocalStorage, hasCompletedOnboarding, isValidApiOrigin, markFrontendReady, normalizeApiOrigin, readPreferences, resolveLanguage, setTrayLanguage, synchronizeDocumentPreferences, writePreferences, type Preferences, type RuntimeCapabilities } from "./shell";
import { ShortcutActionId } from "./shortcuts";
import { RealqaSurface, type CaptureActionId, type RealqaController } from "./realqa-ui";
import { DesktopUpdaterPanel } from "./updater-ui";
import { AppShell, Button, Card, DataRow, Dialog, Field, PageHeader, Sheet, ShellLayout, StatusBadge, useShellLayout } from "./ui-foundation";
import { AccountIcon, ArrowRightIcon, DeckIcon, DiagnosticsIcon, HomeIcon, MoreIcon, RealqaIcon, SearchIcon, SettingsIcon, type IconProps } from "./ui-icons";

const surfaces: readonly SurfaceId[] = [SurfaceId.Home, SurfaceId.Realqa, SurfaceId.Deck, SurfaceId.Settings, SurfaceId.Account, SurfaceId.Diagnostics];
const labels: Record<SurfaceId, keyof typeof messages.en> = { home: "home", realqa: "realqa", deck: "deck", settings: "settings", account: "account", diagnostics: "diagnostics" };
const surfaceIcons: Record<SurfaceId, ComponentType<IconProps>> = { home: HomeIcon, realqa: RealqaIcon, deck: DeckIcon, settings: SettingsIcon, account: AccountIcon, diagnostics: DiagnosticsIcon };
const homeTools = [SurfaceId.Realqa, SurfaceId.Deck, SurfaceId.Settings, SurfaceId.Diagnostics] as const satisfies readonly SurfaceId[];
const mobilePrimarySurfaces: readonly SurfaceId[] = [SurfaceId.Home, SurfaceId.Deck, SurfaceId.Settings, SurfaceId.Account];
const MobileNavigationId = { More: "more" } as const;
const homeToolTitles: Record<(typeof homeTools)[number], keyof typeof messages.en> = { realqa: "realqaTitle", deck: "deckTitle", settings: "settingsTitle", diagnostics: "diagnosticsTitle" };
const homeToolSummaries: Record<(typeof homeTools)[number], keyof typeof messages.en> = { realqa: "realqaSummary", deck: "deckSummary", settings: "settingsSummary", diagnostics: "diagnosticsSummary" };
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
  readonly nativeMessaging?: {
    readonly Boundary: ComponentType;
    readonly Settings: ComponentType<{ readonly copy: Copy }>;
    readonly takeContext: (draftId: string, expectedRevision: number) => Promise<CaptureDraft | null>;
  };
}

function capabilitiesFor(runtime: RuntimeSnapshot): RuntimeCapabilities {
  const available = new Set<PlatformCapability>();
  if (runtime.platform === RuntimePlatform.Desktop) {
    available.add(PlatformCapability.Desktop);
    available.add(PlatformCapability.Tray);
  } else if (runtime.platform === RuntimePlatform.Ios || runtime.platform === RuntimePlatform.Android) {
    available.add(PlatformCapability.Mobile);
  }
  if (runtime.capabilities.notifications) available.add(PlatformCapability.Notifications);
  if (runtime.capabilities.secureSettings) available.add(PlatformCapability.SecureSettings);
  if (runtime.capabilities.capture) available.add(PlatformCapability.Capture);
  return { available };
}

function browserNotificationsSupported(): boolean { return typeof Notification !== "undefined"; }
function browserNotificationPermission(): NotificationPermission { return !browserNotificationsSupported() || Notification.permission === "default" ? NotificationPermission.NotDetermined : Notification.permission === "granted" ? NotificationPermission.Authorized : NotificationPermission.Denied; }

export function App({ bridge = nativeBridge, initialRuntime, initialContentState = defaultContentState, nativeMessaging }: AppProps) {
  const storage = getLocalStorage();
  const [preferences, setPreferences] = useState<Preferences>(() => readPreferences(storage));
  const [onboarding, setOnboarding] = useState(() => !hasCompletedOnboarding(storage));
  const [surface, setSurface] = useState<SurfaceId>(SurfaceId.Home);
  const [palette, setPalette] = useState(false);
  const [paletteRestoresFocus, setPaletteRestoresFocus] = useState(true);
  const [moreOpen, setMoreOpen] = useState(false);
  const [moreRestoresFocus, setMoreRestoresFocus] = useState(true);
  const [accountDeleteConfirmationOpen, setAccountDeleteConfirmationOpen] = useState(false);
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
  const [deckLink, setDeckLink] = useState<string | null>(null);
  const [deckLinkPending, setDeckLinkPending] = useState(false);
  const [deckLinkPolicyOrigin, setDeckLinkPolicyOrigin] = useState<string | null>(null);
  const [updaterApprovalOpen, setUpdaterApprovalOpen] = useState(false);
  const [online, setOnline] = useState(() => navigator.onLine);
  const [requestedCapture, setRequestedCapture] = useState<{ action: CaptureActionId; sequence: number } | null>(null);
  const search = useRef<HTMLInputElement>(null);
  const apiOriginInput = useRef<HTMLInputElement>(null);
  const captureSequence = useRef(0);
  const paletteTrigger = useRef<HTMLButtonElement>(null);
  const moreTrigger = useRef<HTMLButtonElement>(null);
  const selectedDesktopNavigationItem = useRef<HTMLButtonElement>(null);
  const externalAttempt = useRef(0);
  const identitySession = useRef<IdentitySession | null>(null);
  const updaterApprovalOpenRef = useRef(false);
  const language = preferences.language === LanguagePreference.System ? systemLanguage : preferences.language;
  const copy = messages[language];
  const shellLayout = useShellLayout();
  const runtimeCapabilities = runtime ? capabilitiesFor(runtime) : { available: new Set<PlatformCapability>() };
  const mobile = runtime?.platform === RuntimePlatform.Ios || runtime?.platform === RuntimePlatform.Android;
  const isMac = runtime?.platform === RuntimePlatform.Ios || /Mac/u.test(navigator.userAgent);
  const supportsNotifications = runtime?.capabilities.notifications === true || runtime?.platform === RuntimePlatform.Desktop && browserNotificationsSupported();
  const shortcutContext = useRef({ mobile, onboarding, capabilities: runtimeCapabilities });
  const realqaController = useRef<RealqaController | null>(null);
  shortcutContext.current = { mobile, onboarding, capabilities: runtimeCapabilities };
  const consumeRequestedCapture = useCallback((sequence: number) => {
    setRequestedCapture((current) => current?.sequence === sequence ? null : current);
  }, []);
  const handleUpdaterApprovalOpenChange = useCallback((open: boolean) => {
    updaterApprovalOpenRef.current = open;
    setUpdaterApprovalOpen(open);
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
    setPaletteRestoresFocus(restoreTriggerFocus);
    setPalette(false);
  };
  const openPalette = () => { setPaletteRestoresFocus(true); setPalette(true); };
  const closeMore = (restoreTriggerFocus = true) => {
    setMoreRestoresFocus(restoreTriggerFocus);
    setMoreOpen(false);
  };
  const openMore = () => {
    if (accountDeleteConfirmationOpen) return;
    setMoreRestoresFocus(true);
    setMoreOpen(true);
  };

  useEffect(() => {
    document.title = "DevHUD";
    void markFrontendReady()?.catch(() => {});
  }, []);
  useEffect(() => {
    readDiagnosticEvents(storage);
    readDiagnosticCorrelations(storage);
  }, [storage]);
  useEffect(() => {
    let active = true;
    let unlisten: (() => void) | undefined;
    const peekPendingDeckLink = () => {
      void bridge.request({ operation: "deck.peek-pending-link" }).then((pendingDeck) => {
        if (active && pendingDeck.kind === "deck-link" && pendingDeck.deckId) setDeckLinkPending(true);
      }).catch(() => {});
    };
    const receive = (event: NativeBridgeEventV1) => {
      if (event.version !== 1) return;
      if (event.kind === "lifecycle") setLifecycle(event.state);
      if (event.kind === "auth-callback") {
        setAuthCallback(event.url);
      }
      if (event.kind === "deck-link") peekPendingDeckLink();
      if (event.kind === "shortcut-triggered") {
        if (updaterApprovalOpenRef.current) return;
        const context = shortcutContext.current;
        if (context.mobile || context.onboarding) return;
        if (event.action === ShortcutActionId.CommandPalette) {
          closeMore(false);
          openPalette();
          return;
        }
        if (event.action.startsWith("realqa.capture.")) {
          closeMore(false);
          const captureAction = event.action as CaptureActionId;
          const opensCaptureDialog = captureAction === ShortcutActionId.CaptureSelection || captureAction === ShortcutActionId.CaptureToolbar;
          closePalette(!opensCaptureDialog);
          setSurface(SurfaceId.Realqa);
          captureSequence.current += 1;
          setRequestedCapture({ action: captureAction, sequence: captureSequence.current });
          return;
        }
        const action = actionRegistry.find((candidate) => candidate.id === event.action);
        if (action && action.required.every((required) => context.capabilities.available.has(required)) && action.surface) {
          closeMore(false);
          setSurface(action.surface);
        }
      }
    };
    void bridge.listen(receive).then(async (value) => {
      if (!active) { value(); return; }
      unlisten = value;
      if (initialRuntime) {
        if (initialRuntime.platform === RuntimePlatform.Desktop && window.__TAURI_INTERNALS__) peekPendingDeckLink();
        return;
      }
      const response = await bridge.request({ operation: "runtime.snapshot" });
      if (!active || response.kind !== "runtime") return;
      setRuntime(response.snapshot);
      setLifecycle(response.snapshot.lifecycle);
      setRuntimeState(initialContentState);
      const pending = await bridge.request({ operation: "auth.peek-pending-callback" });
      if (active && pending.kind === "auth-callback" && pending.url) setAuthCallback(pending.url);
      if (window.__TAURI_INTERNALS__) peekPendingDeckLink();
    }).catch(() => {
      if (active && !initialRuntime) setRuntimeState({ kind: ContentStateKind.Error, retryable: true });
    });
    return () => {
      active = false;
      unlisten?.();
    };
  }, [bridge, initialContentState, initialRuntime]);
  useEffect(() => {
    if (onboarding || updaterApprovalOpen || !deckLinkPending || deckLinkPolicyOrigin !== preferences.apiOrigin) return;
    let active = true;
    void bridge.request({ operation: "deck.take-pending-link" }).then((pendingDeck) => {
      if (!active) return;
      setDeckLinkPending(false);
      if (pendingDeck.kind === "deck-link" && pendingDeck.deckId) {
        closeMore(false);
        setDeckLink(pendingDeck.deckId);
        setSurface(SurfaceId.Deck);
      }
    }).catch(() => {
      if (active) setDeckLinkPending(false);
    });
    return () => { active = false; };
  }, [bridge, deckLinkPending, deckLinkPolicyOrigin, onboarding, preferences.apiOrigin, updaterApprovalOpen]);
  useEffect(() => {
    if (!runtime) return;
    const captureError = (event: ErrorEvent) => {
      appendDiagnosticEvent(storage, captureDiagnosticEvent(runtime, { component: DiagnosticComponent.APP, severity: DiagnosticSeverity.ERROR, errorCode: "APP_UNHANDLED_ERROR", error: event.error, relatedCorrelationIds: recentDiagnosticCorrelationIds(storage) }));
    };
    const captureRejection = (event: PromiseRejectionEvent) => {
      appendDiagnosticEvent(storage, captureDiagnosticEvent(runtime, { component: DiagnosticComponent.APP, severity: DiagnosticSeverity.ERROR, errorCode: "APP_UNHANDLED_REJECTION", error: event.reason, relatedCorrelationIds: recentDiagnosticCorrelationIds(storage) }));
    };
    addEventListener("error", captureError);
    addEventListener("unhandledrejection", captureRejection);
    return () => {
      removeEventListener("error", captureError);
      removeEventListener("unhandledrejection", captureRejection);
    };
  }, [runtime, storage]);
  useEffect(() => {
    if (!supportsNotifications || lifecycle !== LifecycleState.Active) return;
    if (!runtime?.capabilities.notifications) {
      setNotificationPermission(browserNotificationPermission());
      return;
    }
    let active = true;
    void bridge.request({ operation: "notifications.permission" }).then((response) => {
      if (active && response.kind === "notification-permission") setNotificationPermission(response.permission);
    }).catch(() => {});
    return () => { active = false; };
  }, [bridge, lifecycle, runtime?.capabilities.notifications, supportsNotifications]);
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
  useEffect(() => { if (surface === SurfaceId.Account) apiOriginInput.current?.focus(); }, [surface]);
  useEffect(() => {
    if (shellLayout === ShellLayout.Mobile || !moreOpen) return;
    closeMore(false);
    requestAnimationFrame(() => selectedDesktopNavigationItem.current?.focus());
  }, [shellLayout, moreOpen]);

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
  const markDeckLinkPolicyReady = useCallback(() => {
    setDeckLinkPolicyOrigin(preferences.apiOrigin);
  }, [preferences.apiOrigin]);
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
      if (!runtime?.capabilities.notifications) {
        if (!browserNotificationsSupported()) throw new Error("browser-notifications-unsupported");
        const permission = Notification.permission === "granted" ? "granted" : await Notification.requestPermission();
        setNotificationPermission(permission === "granted" ? NotificationPermission.Authorized : permission === "denied" ? NotificationPermission.Denied : NotificationPermission.NotDetermined);
        return;
      }
      const response = await bridge.request({ operation: "notifications.request-permission" });
      if (response.kind === "notification-permission") setNotificationPermission(response.permission);
    } catch (error) {
      if (error instanceof NativeBridgeError || !runtime?.capabilities.notifications) setNotificationRequestFailed(true);
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

  const NativeMessagingBoundary = nativeMessaging?.Boundary;
  const boundary = (content: ReactNode) => runtime ? <DevHudServiceBoundary key={preferences.apiOrigin} apiOrigin={preferences.apiOrigin} active online={online} callbackUrl={authCallback} platform={runtime.platform} bridge={bridge} onCallbackConsumed={clearConsumedAuthCallback} onDeckLinkPolicyReady={markDeckLinkPolicyReady} onContinueLocally={finishOnboarding} onLoggedOut={() => { realqaController.current?.reset(); setRequestedCapture(null); setSurface(SurfaceId.Account); }} initialAppearance={{ theme: preferences.theme, language: preferences.language }} identitySessionRef={identitySession}><UrlMappingDraftProvider><DeckPollingBoundary bridge={bridge} active={lifecycle === LifecycleState.Active} online={online} language={language}><SynchronizedAppearanceBoundary onAppearance={(appearance) => update({ theme: appearance.theme, language: appearance.language })} />{runtime.platform === RuntimePlatform.Desktop && NativeMessagingBoundary && <NativeMessagingBoundary />}{content}</DeckPollingBoundary></UrlMappingDraftProvider></DevHudServiceBoundary> : content;

  if (runtimeState.kind !== ContentStateKind.Ready) return <main className="standalone-shell" data-devhud-ready="true"><ContentStateView state={runtimeState} copy={copy} onRetry={() => location.reload()} /></main>;

  if (onboarding) return boundary(<main className="standalone-shell" data-devhud-ready="true" data-runtime-platform={runtime?.platform ?? "loading"}><FirstRunIdentity copy={copy} apiOrigin={preferences.apiOrigin} onApiOrigin={applyApiOrigin} onComplete={finishOnboarding} />{externalMessage && <p className="external-message" role={externalMessageIsError ? "alert" : "status"}>{externalMessageText}</p>}</main>);

  const moreCurrent = surface === SurfaceId.Realqa || surface === SurfaceId.Diagnostics;
  const navigate = (nextSurface: SurfaceId) => { setSurface(nextSurface); setMoreOpen(false); };
  const navigation = shellLayout !== ShellLayout.Mobile && <aside className={`shell-navigation shell-navigation-${shellLayout}`}>
    <h1 aria-label={shellLayout === ShellLayout.Rail ? copy.appName : undefined}>{shellLayout === ShellLayout.Sidebar ? copy.appName : "D"}</h1>
    <nav aria-label={copy.mobileNavigation}>{surfaces.map((item) => {
      const Icon = surfaceIcons[item];
      const tooltipId = `navigation-tooltip-${item}`;
      return <button ref={surface === item ? selectedDesktopNavigationItem : undefined} className="shell-nav-item" aria-label={copy[labels[item]]} aria-describedby={shellLayout === ShellLayout.Rail ? tooltipId : undefined} aria-current={surface === item ? "page" : undefined} key={item} onClick={() => navigate(item)}>
        <Icon />
        {shellLayout === ShellLayout.Sidebar ? <span>{copy[labels[item]]}</span> : <span id={tooltipId} className="nav-tooltip" role="tooltip">{copy[labels[item]]}</span>}
      </button>;
    })}</nav>
    {mobile ? <Button className="palette-trigger" ref={paletteTrigger} variant="ghost" icon={<SearchIcon />} onClick={openPalette} aria-label={copy.openPalette}>{shellLayout === ShellLayout.Sidebar ? copy.openPalette : null}</Button> : <ShortcutPaletteTrigger copy={copy} isMac={isMac} triggerRef={paletteTrigger} onOpen={openPalette} compact={shellLayout === ShellLayout.Rail} />}
  </aside>;
  const topBar = shellLayout === ShellLayout.Mobile && <header className="mobile-app-bar"><h1>{copy.appName}</h1><span>{copy[labels[surface]]}</span><Button ref={paletteTrigger} variant="ghost" icon={<SearchIcon />} onClick={openPalette} aria-label={copy.openPalette} /></header>;
  const bottomBar = shellLayout === ShellLayout.Mobile && <nav className="mobile-bottom-navigation" aria-label={copy.mobileNavigation}>
    {mobilePrimarySurfaces.map((item) => {
      const Icon = surfaceIcons[item];
      return <button type="button" key={item} aria-current={surface === item ? "page" : undefined} onClick={() => navigate(item)}><Icon /><span>{copy[labels[item]]}</span></button>;
    })}
    <button key={MobileNavigationId.More} ref={moreTrigger} type="button" aria-current={moreCurrent ? "page" : undefined} aria-haspopup="dialog" aria-expanded={moreOpen} disabled={accountDeleteConfirmationOpen} onClick={openMore}><MoreIcon /><span>{copy.more}</span></button>
  </nav>;

  return boundary(<>
    {runtime?.platform === RuntimePlatform.Desktop && <SynchronizedShortcutBoundary bridge={bridge} />}
    <AppShell layout={shellLayout} skipLabel={copy.skipToContent} navigation={navigation} topBar={topBar} bottomBar={bottomBar} data-devhud-ready="true" data-runtime-platform={runtime?.platform ?? "desktop"} data-lifecycle={lifecycle}>
      {surface === SurfaceId.Home && <><PageHeader eyebrow={copy.availableTools} title={copy.welcome} summary={copy.homeSummary} /><div className="tool-grid">{homeTools.map((item) => {
        const Icon = surfaceIcons[item];
        return <Card key={item} interactive><DataRow icon={<Icon />} title={copy[homeToolTitles[item]]} description={copy[homeToolSummaries[item]]} trailing={<>{mobile && item === SurfaceId.Realqa && <StatusBadge tone="neutral">{copy.desktopOnly}</StatusBadge>}<ArrowRightIcon /></>} onClick={() => navigate(item)} /></Card>;
      })}</div></>}
      {surface === SurfaceId.Realqa && mobile && <><PageHeader eyebrow={copy.desktopOnly} title={copy.realqaMobileTitle} summary={copy.realqaMobileSummary} /><Card className="notice"><StatusBadge tone="neutral">{copy.desktopOnly}</StatusBadge><p>{copy.unavailable}</p></Card></>}
      {!mobile && runtimeCapabilities.available.has(PlatformCapability.Capture) && <RealqaSurface ref={realqaController} bridge={bridge} copy={copy} active={surface === SurfaceId.Realqa} paletteOpen={palette} onActivate={() => setSurface(SurfaceId.Realqa)} requestedAction={requestedCapture} onRequestedActionConsumed={consumeRequestedCapture} takeBrowserContext={nativeMessaging?.takeContext} />}
      {surface === SurfaceId.Realqa && !mobile && !runtimeCapabilities.available.has(PlatformCapability.Capture) && <><PageHeader eyebrow={copy.realqa} title={copy.realqaTitle} summary={copy.realqaSummary} /><div className="disabled-actions">{unavailableCaptureActions.map((action) => <button disabled key={action.id}>{copy[action.title]}</button>)}</div><p className="notice">{copy.unavailable}</p></>}
      {surface === SurfaceId.Deck && <DeckSurface copy={copy} bridge={bridge} language={language} selectedDeckId={deckLink} onDismissMissingLink={() => setDeckLink(null)} />}
      {surface === SurfaceId.Settings && <><PageHeader eyebrow={copy.settings} title={copy.settingsTitle} summary={copy.settingsSummary} /><SynchronizedSettingsBoundary copy={copy} bridge={bridge} onOpenExternal={openExternal} showNativeShortcuts={runtime?.platform === RuntimePlatform.Desktop} shortcutCapabilities={runtimeCapabilities} NativeMessagingSettings={nativeMessaging?.Settings} />{supportsLaunchAtLogin && <><label className="check"><input type="checkbox" checked={preferences.launchAtLogin} onChange={(event) => { update({ launchAtLogin: event.target.checked }); void browserShell.setLaunchAtLogin(event.target.checked); }} />{copy.launchAtLogin}</label><p>{copy.launchAtLoginHint}</p></>}{supportsNotifications && <div className="native-setting"><button className="primary" onClick={() => void requestNotifications()}>{copy.notificationPermission}</button><output aria-live="polite">{copy[notificationPermissionLabels[notificationPermission]]}</output>{notificationRequestFailed && <p className="native-setting-error" role="alert">{copy.notificationPermissionFailed}</p>}</div>}{runtime?.capabilities.storeUpdates && <div className="native-setting"><p>{copy.updatePolicy}</p>{storeConfigured && <button className="primary" onClick={() => void openStore()}>{copy.updatePolicy}</button>}{storeOpenFailed && <p className="native-setting-error" role="alert">{copy.storeOpenFailed}</p>}</div>}{runtime?.platform === RuntimePlatform.Desktop && <DesktopUpdaterPanel bridge={bridge} language={language} onApprovalOpenChange={handleUpdaterApprovalOpenChange} />}</>}
      {surface === SurfaceId.Account && <><AccountIdentity copy={copy} apiOrigin={preferences.apiOrigin} inputRef={apiOriginInput} onApiOrigin={applyApiOrigin} onDeleteConfirmationOpenChange={setAccountDeleteConfirmationOpen} /><div className="actions"><button onClick={() => void external(ExternalLinkTarget.Pat)}>{copy.githubCreateFinePat}</button><button onClick={() => void external(ExternalLinkTarget.ClassicPat)}>{copy.githubCreateClassicPat}</button>{!mobile && <button onClick={() => void external(ExternalLinkTarget.Issue)}>{copy.issue}</button>}</div>{externalMessage && <p className="external-message" role={externalMessageIsError ? "alert" : "status"}>{externalMessageText}</p>}</>}
      {surface === SurfaceId.Diagnostics && <><PageHeader eyebrow={copy.diagnostics} title={copy.diagnosticsTitle} summary={copy.diagnosticsSummary} />{runtime && <><dl className="runtime-diagnostics"><dt>{copy.diagnosticPlatform}</dt><dd>{runtime.operatingSystem}</dd><dt>{copy.diagnosticArchitecture}</dt><dd>{runtime.architecture}</dd><dt>{copy.diagnosticBridge}</dt><dd>v{runtime.bridgeVersion}</dd></dl><DiagnosticsPanel copy={copy} runtime={runtime} bridge={bridge} storage={storage} online={online} /></>}</>}
    </AppShell>
    <Dialog open={palette} title={copy.commandPalette} initialFocusRef={search} returnFocusRef={paletteTrigger} restoreFocus={paletteRestoresFocus} onClose={() => closePalette()}>
      <Field label={copy.searchCommands} inputId="command-search"><input id="command-search" ref={search} value={query} onChange={(event) => setQuery(event.target.value)} placeholder={copy.searchCommands} /></Field>
      <div className="commands">{actions.length === 0 ? <p role="status">{copy.noCommands}</p> : actions.map((action) => <Button variant="ghost" key={action.id} onClick={() => execute(action.id)}>{copy[action.title]}</Button>)}</div>
      <Button onClick={() => closePalette()}>{copy.close}</Button>
    </Dialog>
    <Sheet open={moreOpen} title={copy.more} backLabel={copy.back} returnFocusRef={moreTrigger} restoreFocus={moreRestoresFocus} onClose={() => closeMore()}>
      <DataRow icon={<RealqaIcon />} title={copy.realqa} description={mobile ? copy.realqaMobileSummary : copy.realqaSummary} trailing={mobile ? <StatusBadge tone="neutral">{copy.desktopOnly}</StatusBadge> : <ArrowRightIcon />} ariaCurrent={surface === SurfaceId.Realqa ? "page" : undefined} onClick={() => navigate(SurfaceId.Realqa)} />
      <DataRow icon={<DiagnosticsIcon />} title={copy.diagnostics} description={copy.diagnosticsSummary} trailing={<ArrowRightIcon />} ariaCurrent={surface === SurfaceId.Diagnostics ? "page" : undefined} onClick={() => navigate(SurfaceId.Diagnostics)} />
    </Sheet>
  </>);
}
