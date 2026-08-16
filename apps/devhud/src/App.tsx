import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { messages } from "./localization";
import { ActionId, ExternalLinkTarget, LanguagePreference, PlatformCapability, SurfaceId, ThemePreference, actionRegistry, availableActions, browserShell, completeOnboarding, desktopCapabilities, getLocalStorage, hasCompletedOnboarding, isValidApiOrigin, markFrontendReady, readPreferences, resolveLanguage, setTrayLanguage, synchronizeDocumentPreferences, writePreferences, type Preferences } from "./shell";

const surfaces: readonly SurfaceId[] = [SurfaceId.Home, SurfaceId.Realqa, SurfaceId.Deck, SurfaceId.Settings, SurfaceId.Account, SurfaceId.Diagnostics];
const labels: Record<SurfaceId, keyof typeof messages.en> = { home: "home", realqa: "realqa", deck: "deck", settings: "settings", account: "account", diagnostics: "diagnostics" };
const isMac = /Mac|iPhone|iPad/u.test(navigator.userAgent);
const rightModifierLocation = 2;
type ExternalMessage = "opened" | "failed" | "invalid-api-origin";

export function App() {
  const storage = getLocalStorage();
  const [preferences, setPreferences] = useState<Preferences>(() => readPreferences(storage));
  const [onboarding, setOnboarding] = useState(() => !hasCompletedOnboarding(storage));
  const [surface, setSurface] = useState<SurfaceId>(SurfaceId.Home);
  const [palette, setPalette] = useState(false);
  const [query, setQuery] = useState("");
  const [externalMessage, setExternalMessage] = useState<ExternalMessage | null>(null);
  const search = useRef<HTMLInputElement>(null);
  const apiOriginInput = useRef<HTMLInputElement>(null);
  const paletteRef = useRef<HTMLElement>(null);
  const paletteTrigger = useRef<HTMLButtonElement>(null);
  const rightModifier = useRef<"ControlRight" | "MetaRight" | null>(null);
  const signInAttempt = useRef(0);
  const language = resolveLanguage(preferences.language, navigator.languages);
  const copy = messages[language];
  const update = (next: Partial<Preferences>) => {
    if ("apiOrigin" in next) setExternalMessage(null);
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
    void markFrontendReady();
  }, []);
  useEffect(() => {
    void setTrayLanguage(language).catch(() => {});
  }, [language]);
  useEffect(() => {
    const media = matchMedia("(prefers-color-scheme: dark)");
    const updateTheme = () => { synchronizeDocumentPreferences(document.documentElement, preferences, media.matches, navigator.languages); };
    updateTheme();
    media.addEventListener("change", updateTheme);
    return () => media.removeEventListener("change", updateTheme);
  }, [preferences.language, preferences.theme]);
  useEffect(() => {
    const key = (event: KeyboardEvent) => {
      const platformModifier = isMac ? "MetaRight" : "ControlRight";
      if (event.code === platformModifier && event.location === rightModifierLocation) rightModifier.current = event.code;
      const matchingRightModifier = rightModifier.current === platformModifier && (isMac ? event.metaKey : event.ctrlKey);
      const exactRightModifierChord = matchingRightModifier && !event.shiftKey && !event.altKey && (isMac ? !event.ctrlKey : !event.metaKey);
      if (!onboarding && exactRightModifierChord && event.code === "KeyK") {
        event.preventDefault();
        setPalette(true);
      }
      if (event.key === "Escape" && palette) closePalette();
    };
    const releaseRightModifier = (event: KeyboardEvent) => {
      if (rightModifier.current === event.code) rightModifier.current = null;
    };
    const clearRightModifier = () => { rightModifier.current = null; };
    addEventListener("keydown", key);
    addEventListener("keyup", releaseRightModifier);
    addEventListener("blur", clearRightModifier);
    return () => {
      removeEventListener("keydown", key);
      removeEventListener("keyup", releaseRightModifier);
      removeEventListener("blur", clearRightModifier);
    };
  }, [onboarding, palette]);
  useEffect(() => { if (palette) search.current?.focus(); }, [palette]);
  useEffect(() => { if (surface === SurfaceId.Account) apiOriginInput.current?.focus(); }, [surface]);

  const actions = useMemo(() => availableActions(desktopCapabilities).filter((action) => copy[action.title].toLowerCase().includes(query.toLowerCase())), [copy, query]);
  const unavailableCaptureActions = actionRegistry.filter((action) => action.required.includes(PlatformCapability.Capture) && !desktopCapabilities.available.has(PlatformCapability.Capture));
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
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };
  const external = async (target: ExternalLinkTarget) => {
    setExternalMessage(null);
    if (target === ExternalLinkTarget.Authentication && !isValidApiOrigin(preferences.apiOrigin)) {
      setExternalMessage("invalid-api-origin");
      return false;
    }
    try {
      await browserShell.openExternal(target, preferences.apiOrigin);
      setExternalMessage("opened");
      return true;
    } catch {
      setExternalMessage("failed");
      return false;
    }
  };
  const finishOnboarding = () => {
    signInAttempt.current += 1;
    setExternalMessage(null);
    completeOnboarding(storage);
    setOnboarding(false);
    setSurface(SurfaceId.Home);
  };
  const startSignIn = async () => {
    const attempt = signInAttempt.current + 1;
    signInAttempt.current = attempt;
    if (await external(ExternalLinkTarget.Authentication) && attempt === signInAttempt.current) finishOnboarding();
  };
  const supportsLaunchAtLogin = desktopCapabilities.available.has(PlatformCapability.LaunchAtLogin);
  const externalMessageText = externalMessage === "invalid-api-origin" ? copy.invalidApiOrigin : externalMessage === "opened" ? copy.externalOpened : copy.externalFailed;
  const externalMessageIsError = externalMessage !== "opened";

  if (onboarding) return <main className="app-shell onboarding" data-devhud-ready="true"><section className="content"><p className="eyebrow">{copy.account}</p><h1>{copy.accountTitle}</h1><p>{copy.accountSummary}</p><label>{copy.apiOrigin}<input autoFocus value={preferences.apiOrigin} onChange={(event) => update({ apiOrigin: event.target.value })} /></label><p>{copy.apiOriginHint}</p><div className="actions"><button onClick={() => void startSignIn()}>{copy.signIn}</button><button onClick={finishOnboarding}>{copy.continueLocally}</button></div>{externalMessage && <p className="external-message" role={externalMessageIsError ? "alert" : "status"}>{externalMessageText}</p>}</section></main>;

  return <main className="app-shell" data-devhud-ready="true">
    <aside aria-label="DevHUD">
      <h1>{copy.appName}</h1>
      <nav>{surfaces.map((item) => <button className={surface === item ? "active" : ""} aria-current={surface === item ? "page" : undefined} key={item} onClick={() => setSurface(item)}>{copy[labels[item]]}</button>)}</nav>
      <button className="palette-trigger" ref={paletteTrigger} onClick={() => setPalette(true)} aria-label={copy.openPalette}>{isMac ? copy.rightCommandK : copy.rightControlK}</button>
    </aside>
    <section className="content">
      {surface === SurfaceId.Home && <><p className="eyebrow">{copy.available}</p><h2>{copy.welcome}</h2><p>{copy.homeSummary}</p></>}
      {surface === SurfaceId.Realqa && <><p className="eyebrow">{copy.realqa}</p><h2>{copy.realqaTitle}</h2><p>{copy.realqaSummary}</p><div className="disabled-actions">{unavailableCaptureActions.map((action) => <button disabled key={action.id}>{copy[action.title]}</button>)}</div><p className="notice">{copy.planned}</p></>}
      {surface === SurfaceId.Deck && <><p className="eyebrow">{copy.deck}</p><h2>{copy.deckTitle}</h2><p>{copy.deckSummary}</p><p className="notice">{copy.planned}</p></>}
      {surface === SurfaceId.Settings && <><p className="eyebrow">{copy.settings}</p><h2>{copy.settingsTitle}</h2><p>{copy.settingsSummary}</p><label>{copy.theme}<select value={preferences.theme} onChange={(event) => update({ theme: event.target.value as ThemePreference })}>{Object.values(ThemePreference).map((value) => <option key={value} value={value}>{copy[value]}</option>)}</select></label><label>{copy.language}<select value={preferences.language} onChange={(event) => update({ language: event.target.value as LanguagePreference })}><option value="system">{copy.system}</option><option value="en">{copy.english}</option><option value="ko">{copy.korean}</option></select></label>{supportsLaunchAtLogin && <><label className="check"><input type="checkbox" checked={preferences.launchAtLogin} onChange={(event) => { update({ launchAtLogin: event.target.checked }); void browserShell.setLaunchAtLogin(event.target.checked); }} />{copy.launchAtLogin}</label><p>{copy.launchAtLoginHint}</p></>}</>}
      {surface === SurfaceId.Account && <><p className="eyebrow">{copy.account}</p><h2>{copy.accountTitle}</h2><p>{copy.accountSummary}</p><label>{copy.apiOrigin}<input ref={apiOriginInput} value={preferences.apiOrigin} onChange={(event) => update({ apiOrigin: event.target.value })} /></label><p>{copy.apiOriginHint}</p><div className="actions"><button onClick={() => void external(ExternalLinkTarget.Authentication)}>{copy.signIn}</button><button onClick={() => void external(ExternalLinkTarget.Pat)}>{copy.pat}</button><button onClick={() => void external(ExternalLinkTarget.Issue)}>{copy.issue}</button></div>{externalMessage && <p className="external-message" role={externalMessageIsError ? "alert" : "status"}>{externalMessageText}</p>}</>}
      {surface === SurfaceId.Diagnostics && <><p className="eyebrow">{copy.diagnostics}</p><h2>{copy.diagnosticsTitle}</h2><p>{copy.diagnosticsSummary}</p><p className="notice">{copy.diagnosticsUnavailable}</p></>}
    </section>
    {palette && <div className="overlay" role="presentation"><section ref={paletteRef} className="palette" role="dialog" aria-modal="true" aria-label={copy.commandPalette} onKeyDown={trapPaletteFocus}><input ref={search} value={query} onChange={(event) => setQuery(event.target.value)} placeholder={copy.searchCommands} aria-label={copy.searchCommands} /><div className="commands">{actions.length === 0 ? <p role="status">{copy.noCommands}</p> : actions.map((action) => <button key={action.id} onClick={() => execute(action.id)}>{copy[action.title]}</button>)}</div><button onClick={() => closePalette()}>{copy.close}</button></section></div>}
  </main>;
}
