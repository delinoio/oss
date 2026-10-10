// SPDX-License-Identifier: Apache-2.0
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { copy, useLocale } from "./localization";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import type { DesktopUpdateControls } from "./updates";
import "./app-information.css";

export enum AppInformationLink { Releases="releases", License="license", Notices="notices" }
export interface AppInformationProps {
  readAppContext?: DesktopUpdateControls["readContext"];
  openAppInformationLink?: (action: AppInformationLink) => Promise<void>;
  onAppUpdatesSlot?: (slot: HTMLElement | undefined) => void;
}
const platforms: Record<string,string>={"darwin-amd64":"macOS · Intel","darwin-arm64":"macOS · Apple Silicon","windows-amd64":"Windows · x64","windows-arm64":"Windows · ARM64","linux-amd64":"Linux · x64","linux-arm64":"Linux · ARM64"};
export function AppInformation({readAppContext,openAppInformationLink,onAppUpdatesSlot}:AppInformationProps){
  useLocale();
  const [context,setContext]=useState<{current_version:string;target:string}>(),[loading,setLoading]=useState(Boolean(readAppContext)),[error,setError]=useState(false),[opening,setOpening]=useState<AppInformationLink>(),[openError,setOpenError]=useState(false);
  const slot=useRef<HTMLDivElement>(null),alive=useRef(true);
  useEffect(()=>{alive.current=true;return()=>{alive.current=false;};},[]);
  useLayoutEffect(()=>{onAppUpdatesSlot?.(slot.current??undefined);return()=>onAppUpdatesSlot?.(undefined);},[onAppUpdatesSlot]);
  useEffect(()=>{let current=true;setLoading(Boolean(readAppContext));setContext(undefined);setError(false);if(readAppContext)void readAppContext().then(value=>{if(!value.current_version||!platforms[value.target])throw new Error("App context unavailable");if(current)setContext(value);}).catch(()=>{if(current)setError(true);}).finally(()=>{if(current)setLoading(false);});return()=>{current=false;};},[readAppContext]);
  const open=async(action:AppInformationLink)=>{if(!openAppInformationLink||opening)return;setOpening(action);setOpenError(false);try{await openAppInformationLink(action);}catch{if(alive.current)setOpenError(true);}finally{if(alive.current)setOpening(undefined);}};
  const fallback=copy(loading?"app-information.loading":"app-information.unavailable");
  return <div className="app-information">
    <section className="app-information-group" aria-label="DeliDev"><h2>DeliDev</h2><dl><div><dt>{copy("app-information.version")}</dt><dd>{context?.current_version??fallback}</dd></div><div><dt>{copy("app-information.platform")}</dt><dd>{context?platforms[context.target]:fallback}</dd></div></dl>{loading?<p role="status">{copy("app-information.loading")}</p>:null}{error?<p role="alert">{copy("app-information.contextError")}</p>:null}</section>
    <section className="app-information-group" aria-label={copy("app-information.updates")}><h2>{copy("app-information.updates")}</h2><p>{copy("app-information.updateHelp")}</p><div ref={slot}/>{!onAppUpdatesSlot?<p role="status">{copy("app-information.unavailable")}</p>:null}</section>
    <section className="app-information-group" aria-label={copy("app-information.related")}><h2>{copy("app-information.related")}</h2>{[AppInformationLink.Releases,AppInformationLink.License,AppInformationLink.Notices].map(action=><SettingsActionButton key={action} type="button" role="link" icon={SettingsActionIcon.Inspect} className="app-information-link" data-settings-search-target={`app-${action}`} disabled={!openAppInformationLink||Boolean(opening)} onClick={()=>void open(action)}>{copy(`app-information.${action}`)}<svg aria-hidden="true" focusable="false" viewBox="0 0 24 24"><path d="M14 3h7v7m0-7L10 14M10 3H3v18h18v-7"/></svg></SettingsActionButton>)}<p>{copy("app-information.noticesHelp")}</p>{opening?<p role="status">{copy("app-information.opening")}</p>:null}{openError?<p role="alert">{copy("app-information.openError")}</p>:null}</section>
  </div>;
}
