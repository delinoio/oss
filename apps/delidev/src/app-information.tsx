// SPDX-License-Identifier: Apache-2.0
import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { copy, useLocale } from "./localization";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { Updates, type DesktopUpdateControls } from "./updates";
import "./app-information.css";

export enum AppInformationLink { Releases = "releases", License = "license", Notices = "notices" }
export interface AppInformationContext { current_version: string; target: string }
const platforms: Record<string, string> = { "darwin-amd64": "macOS · x64", "darwin-arm64": "macOS · ARM64", "windows-amd64": "Windows · x64", "windows-arm64": "Windows · ARM64", "linux-amd64": "Linux · x64", "linux-arm64": "Linux · ARM64" };
const Presentation = createContext<{ target?: HTMLElement; setTarget?: (target?: HTMLElement) => void; available?: boolean; setAvailable?: (value: boolean) => void }>({});
export function AppInformationPresentationProvider({ children }: { children: ReactNode }) {
  const [target, setTarget] = useState<HTMLElement>(), [available, setAvailable] = useState(false);
  return <Presentation.Provider value={{ target, setTarget, available, setAvailable }}>{children}</Presentation.Provider>;
}
/** The connection-owned updater stays mounted while its DOM host moves between
 * Settings and a hidden outlet. A category visit owns only presentation. */
export function AppInformationUpdates({ controls }: { controls: DesktopUpdateControls }) {
  const { target, setAvailable } = useContext(Presentation);
  useEffect(() => { setAvailable?.(true); return () => setAvailable?.(false); }, [setAvailable]);
  const [host] = useState(() => document.createElement("div"));
  const fallback = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const focused = host.contains(document.activeElement) ? document.activeElement as HTMLElement : undefined;
    const outlet = target?.isConnected ? target : fallback.current;
    if (outlet && host.parentElement !== outlet) outlet.append(host);
    if (focused && target?.isConnected && !target.closest("[hidden], [inert]")) focused.focus({ preventScroll: true });
    else focused?.blur();
  }, [host, target]);
  useLayoutEffect(() => () => host.remove(), [host]);
  return <><div ref={fallback} hidden />{createPortal(<Updates active={Boolean(target)} controls={controls} desktopSettings />, host)}</>;
}
const readNativeContext = () => invoke<AppInformationContext>("desktop_update_context");
const openNativeLink = (action: AppInformationLink) => invoke<void>("open_app_information_link", { action });
export function AppInformation({ readContext = isTauri() ? readNativeContext : undefined, openLink = isTauri() ? openNativeLink : undefined }: { readContext?: () => Promise<AppInformationContext>; openLink?: (action: AppInformationLink) => Promise<void> }) {
  useLocale();
  const { setTarget, available } = useContext(Presentation);
  const outlet = useCallback((node: HTMLDivElement | null) => setTarget?.(node ?? undefined), [setTarget]);
  const [context, setContext] = useState<AppInformationContext>(), [pending, setPending] = useState(Boolean(readContext)), [failed, setFailed] = useState(false), [opening, setOpening] = useState<AppInformationLink>(), [openFailed, setOpenFailed] = useState(false);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  useEffect(() => {
    let current = true;
    setPending(Boolean(readContext)); setFailed(false); setContext(undefined);
    if (readContext) void readContext().then(value => {
      if (!value.current_version || !platforms[value.target]) throw new Error("Invalid app context");
      if (current) setContext(value);
    }).catch(() => { if (current) setFailed(true); }).finally(() => { if (current) setPending(false); });
    return () => { current = false; };
  }, [readContext]);
  const open = async (action: AppInformationLink) => {
    if (!openLink || opening) return;
    setOpening(action); setOpenFailed(false);
    try { await openLink(action); } catch { if (alive.current) setOpenFailed(true); }
    finally { if (alive.current) setOpening(undefined); }
  };
  const observation = pending ? copy("app-information.loading") : failed ? copy("app-information.failed") : copy("app-information.unavailable");
  return <div className="app-information">
    <section className="app-information-group" data-settings-search-target="app-information" tabIndex={-1}>
      <h2>{copy("app-information.title")}</h2><p className="app-information-name">DeliDev</p>
      <dl aria-live="polite"><div><dt>{copy("app-information.version")}</dt><dd>{context?.current_version ?? observation}</dd></div><div><dt>{copy("app-information.platform")}</dt><dd>{context ? platforms[context.target] : observation}</dd></div></dl>
      {pending || failed || !readContext ? <p role={failed ? "alert" : "status"}>{observation}</p> : null}
    </section>
    <section className="app-information-group"><h2>{copy("app-information.updates")}</h2><div ref={outlet} />{!available ? <p role="status">{copy("app-information.updateUnavailable")}</p> : null}</section>
    <section className="app-information-group"><h2>{copy("app-information.related")}</h2><div className="app-information-links">
      {([AppInformationLink.Releases, AppInformationLink.License, AppInformationLink.Notices] as const).map(action => <SettingsActionButton key={action} role="link" icon={SettingsActionIcon.Inspect} type="button" data-settings-search-target={`app-${action}`} disabled={!openLink || Boolean(opening)} onClick={() => void open(action)}>{copy(`app-information.${action}`)}</SettingsActionButton>)}
      </div><p>{copy("app-information.sourceNotices")}</p>{opening ? <p role="status">{copy("app-information.opening")}</p> : null}{openFailed ? <p role="alert">{copy("app-information.openFailed")}</p> : null}
    </section>
  </div>;
}
