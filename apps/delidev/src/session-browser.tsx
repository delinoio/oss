import { listen } from "@tauri-apps/api/event";
import { useSessionTabsStore, SessionTabKind, type SessionTab } from "./session-tabs";
import { shortcutModalVisible } from "./shortcuts";
// SPDX-License-Identifier: Apache-2.0
import { copy, useLocale, ownedMessage, useProductMessage } from "./localization";
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { invoke } from "@tauri-apps/api/core";
import { BrowserQuery, BrowserCapability, BrowserProfileState, newRequestId, type Resource, type BrowserProfile } from "@delinoio/delidev-api-client";

import { useRetainedMutation } from "./mutation";
import { createPortal } from "react-dom";
import { Modal, Problem } from "./ui";
import { useBrowserHost } from "./host-capabilities";

enum BrowserAction { Navigate = "navigate", Back = "back", Forward = "forward", Reload = "reload", Resize = "resize", Hide = "hide" }
interface BrowserState { tabs: { tabs: { id: string; url: string }[]; selected: string }; removal_pending: boolean }
const idPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
export function browserProfile(profile: BrowserProfile | undefined, accountId: string): string {
 if (!profile || profile.revision === 0n || ![profile.id,profile.serverId,profile.deviceId,profile.accountId].every((id)=>idPattern.test(id)) || profile.accountId!==accountId || profile.state!==BrowserProfileState.ACTIVE || profile.deletionRequestId) throw new Error("Browser profile ownership is unavailable.");
 return profile.id;
}

export function browserState(value: BrowserState): BrowserState {
  if (!value || typeof value.removal_pending !== "boolean" || !Array.isArray(value.tabs?.tabs) || value.tabs.tabs.length > 16 || typeof value.tabs.selected !== "string") throw new Error("Local browser state is unavailable.");
  const ids = new Set<string>();
  for (const tab of value.tabs.tabs) {
    if (!idPattern.test(tab.id) || ids.has(tab.id) || typeof tab.url !== "string" || tab.url.length > 8192) throw new Error("Local browser state is unavailable.");
    const url = new URL(tab.url);
    if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) throw new Error("Local browser state is unavailable.");
    ids.add(tab.id);
  }
  if (ids.size ? !ids.has(value.tabs.selected) : value.tabs.selected !== "") throw new Error("Local browser state is unavailable.");
  return value;
}
type PageDescriptor = Extract<SessionTab,{kind:SessionTabKind.Page}>;
interface TabbedBrowser { active?:boolean; selectedPage?: {profile:string;id:string;title:string;label?:string}; openPage?: (page:{profile:string;id:string;title:string;label?:string})=>void; workspaceActive?:boolean; creationOpen?:boolean; closeCreation?:()=>void; closeRequest?:PageDescriptor; closeSettled?:(page:PageDescriptor)=>void; pendingChanged?:(pending:boolean)=>void }
interface PageReply {state:BrowserState;pending_pages:string[];operation?:{operation_id:string;page_id:string;phase:"uncommitted"|"native-pending"|"complete"}}
interface PageOperation {id:string;kind:"create"|"close";url?:string;page?:PageDescriptor;profile?:string;sent:boolean;registered:boolean}
export interface BrowserLayoutControls { wide: boolean; expanded: boolean; toggleExpanded: () => void }
function BrowserHeader({ layout }: { layout?: BrowserLayoutControls }) {
  const [information, setInformation] = useState(false);
  return <><header className="browser-header"><h3>{copy("session-browser.browser_d31de1")}</h3><small>{copy("session-browser.accountProfile")}</small><div className="browser-header-actions"><button type="button" aria-label={copy("session-browser.information")} onClick={() => setInformation(true)}>ⓘ</button>{layout ? <button type="button" disabled={!layout.wide} onClick={layout.toggleExpanded}>{copy(layout.expanded ? "session-browser.restore" : "session-browser.expand")}</button> : null}</div></header>{information ? <Modal title={copy("session-browser.information")} close={() => setInformation(false)} className="browser-information" focusClose><p>{copy("session-browser.sharedWithThisAccountSSessions_361a18")}</p><p>{copy("session-browser.enterAWebAddressThenOpen_60a48b")}</p></Modal> : null}</>;
}
export function browserTabTitle(address: string): string {
  try { const url = new URL(address); return `${url.host}${url.pathname === "/" ? "" : url.pathname}`; } catch { return address; }
}
export function SessionBrowser(props: {session:Resource;accountId:string;close:()=>void;layout?:BrowserLayoutControls}&TabbedBrowser) {
  useLocale();const available=useBrowserHost();const address=useRef<HTMLInputElement>(null);
  if(!available)return <><section hidden={!props.active}><p>{copy("session-browser.cefBrowserProfilesRequireTheSupported_c9b9e8")}</p></section>{props.creationOpen&&props.workspaceActive!==false?createPortal(<Modal title={copy("session-browser.newPage")} close={props.closeCreation??(()=>{})} className="browser-page-dialog" initialFocus={address} trapFocus><p>{copy("session-browser.sharedWithThisAccountSSessions_361a18")}</p><label>{copy("session-browser.address_56ef8f")}<input ref={address} maxLength={8192}/></label><p>{copy("session-browser.cefBrowserProfilesRequireTheSupported_c9b9e8")}</p><footer><button type="button" onClick={props.closeCreation}>{copy("ui.cancel")}</button><button type="button" disabled>{copy("session-browser.openPage")}</button></footer></Modal>,documentGlobal().body):null}</>;
  return <NativeSessionBrowser {...props}/>;
}
function NativeSessionBrowser({ session, accountId, layout, active=false, selectedPage, openPage, workspaceActive=true, creationOpen=false, closeCreation=()=>{},closeRequest,closeSettled,pendingChanged }: { session: Resource; accountId: string; close: () => void; layout?: BrowserLayoutControls } & TabbedBrowser) {
  useLocale();
  const available = useBrowserHost();
  const [address, setAddress] = useState("");
  const [profileId, setProfileId] = useState<string>();
  const [state, setState] = useState<BrowserState>();
  const [failure, setFailure] = useProductMessage();
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const [operation,setOperation]=useState<PageOperation>();
  const operationOwner=useRef<PageOperation|undefined>(undefined);
  const operationRunning=useRef(false);
  useEffect(()=>{pendingChanged?.(Boolean(operation));return()=>pendingChanged?.(false);},[Boolean(operation),pendingChanged]);
  const [uncommitted,setUncommitted]=useState(false);
  const selectedPageRef=useRef(selectedPage);selectedPageRef.current=selectedPage;
  const [profileUnavailable,setProfileUnavailable]=useState(false);
  const profileTokens=useRef(new Set<string>());
  const [profileCursor,setProfileCursor]=useState("");
  const needsPresentation = useRef(true);
  needsPresentation.current = Boolean(selectedPage && selectedPage.profile===profileId && state?.tabs.tabs.some(tab=>tab.id===selectedPage.id));
  const viewport = useRef<HTMLDivElement>(null);
  const addressInput = useRef<HTMLInputElement>(null);
  const alive = useRef(true);
  const presentation = useRef("");
  const shortcutAdmission=useRef("");
  const tabsStore=useSessionTabsStore();
  const activeRef=useRef(active);activeRef.current=active;
  const capabilities = useQuery(BrowserQuery.getBrowserCapabilities, {}, {retry:false,enabled:available&&workspaceActive});
  const supported = capabilities.data?.capabilities.includes(BrowserCapability.PROTECTED_DEVICE_PROFILE_V1) === true;
  const registration = useRetainedMutation(`browser-register:${session.id}:${accountId}`, BrowserQuery.registerBrowserProfile, (response) => {
    try { if(!alive.current)return;const profile=browserProfile(response.profile,accountId);setProfileId(profile);setFailure(undefined);const original=operationOwner.current;if(original?.kind==="create"&&!original.sent){original.registered=true;original.profile=profile;setOperation({...original});} } catch { setFailure(ownedMessage("session-browser.extra.5092c54369dd")); }
  },(response,request)=>{try{return response.requestId===request.session?.requestId && browserProfile(response.profile,request.accountId)!=="";}catch{return false;}});
  const profiles=useQuery(BrowserQuery.listBrowserProfiles,{pageSize:50,pageToken:profileCursor},{enabled:available&&workspaceActive&&supported,retry:false});
  useEffect(()=>{const rows=profiles.data?.profiles;if(!rows)return;const matches=rows.filter(profile=>profile.accountId===accountId);if(matches.length>1){setProfileUnavailable(true);setFailure(ownedMessage("session-browser.extra.5092c54369dd"));return;}if(matches[0]){try{setProfileId(browserProfile(matches[0],accountId));}catch{setProfileUnavailable(true);setFailure(ownedMessage("session-browser.thisAccountWasDeletedItsProfile_4cf6fc"));}}else if(profiles.data?.nextPageToken){const token=profiles.data.nextPageToken;if(token===profileCursor||profileTokens.current.has(token)||profileTokens.current.size>=64){setProfileUnavailable(true);setFailure(ownedMessage("session-browser.operationFailure"));}else{profileTokens.current.add(token);setProfileCursor(token);}}},[profiles.data,accountId]);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const reconcile=(reply:PageReply)=>{
    const state=browserState(reply.state);
    if(!Array.isArray(reply.pending_pages)||reply.pending_pages.length>256||reply.pending_pages.some(id=>!idPattern.test(id)))throw new Error("Local browser state is unavailable.");
    setState(state);
    if(profileId)tabsStore.reconcilePages(session.id,profileId,state.tabs.tabs.map(tab=>({kind:SessionTabKind.Page,profile:profileId,id:tab.id,title:browserTabTitle(tab.url),label:tab.url})),reply.pending_pages);
    return state;
  };
  const settle=(reply:PageReply,original:PageOperation)=>{
    if(!alive.current||operationOwner.current!==original)return;
    const receipt=reply.operation;
    if(!receipt||receipt.operation_id!==original.id||!idPattern.test(receipt.page_id)||original.kind==="close"&&receipt.page_id!==original.page?.id||!["uncommitted","native-pending","complete"].includes(receipt.phase))throw new Error("Local browser operation is unavailable.");
    // Keep the original descriptor until metadata removal AND its original
    // native close acknowledgment are confirmed. Inventory carries pending IDs.
    if(receipt.phase==="complete"&&original.kind==="close"&&original.page)closeSettled?.(original.page);
    const current=reconcile(reply);
    if(receipt.phase==="complete"){
      if(original.kind==="create"){const tab=current.tabs.tabs.find(tab=>tab.id===receipt.page_id);if(tab&&original.profile)openPage?.({profile:original.profile,id:tab.id,title:browserTabTitle(tab.url),label:tab.url});closeCreation();}
      operationOwner.current=undefined;setOperation(undefined);setBusy(false);setFailure(undefined);
    }else if(receipt.phase==="uncommitted"){setUncommitted(true);setFailure(ownedMessage("session-browser.uncommitted"));}
  };
  const observe=async()=>{const original=operationOwner.current;if(!original?.profile||operationRunning.current)return;operationRunning.current=true;try{settle(await invoke<PageReply>("browser_pages",{profileId:original.profile,accountId,action:{action:"observe",operation_id:original.id}}),original);}catch{if(alive.current)setFailure(ownedMessage("session-browser.operationFailure"));}finally{operationRunning.current=false;}};
  useEffect(()=>{if(creationOpen&&!operationOwner.current){setAddress("");if(!profileUnavailable)setFailure(undefined);}},[creationOpen]);
  useEffect(()=>{if(!creationOpen&&!operationOwner.current&&selectedPage?.label)setAddress(selectedPage.label);},[selectedPage?.id,selectedPage?.label,creationOpen]);
  useEffect(()=>{if(registration.error&&!registration.uncertain&&!registration.busy&&operationOwner.current&&!operationOwner.current.sent)setUncommitted(true);},[registration.error,registration.uncertain,registration.busy]);
  useEffect(()=>{if(!profileId||!available)return;let disposed=false;const read=async()=>{try{const reply=await invoke<PageReply>("browser_pages",{profileId,accountId,action:{action:"inventory"}});if(!disposed&&alive.current)reconcile(reply);}catch{if(!disposed)setFailure(ownedMessage("session-browser.operationFailure"));}};void read();const timer=window.setInterval(()=>{if(workspaceActive&&!operationOwner.current)void read();},1000);return()=>{disposed=true;window.clearInterval(timer);};},[profileId,available,workspaceActive]);
  useEffect(()=>{
    const original=operationOwner.current;if(!original||!original.registered||!profileId||original.sent||operationRunning.current)return;
    original.profile=profileId;original.sent=true;operationRunning.current=true;
    void invoke<PageReply>("browser_pages",{profileId,accountId,action:original.kind==="create"?{action:"create",operation_id:original.id,url:original.url}:{action:"close",operation_id:original.id,page_id:original.page!.id}}).then(reply=>settle(reply,original)).catch(()=>{if(alive.current)setFailure(ownedMessage("session-browser.operationFailure"));}).finally(()=>{operationRunning.current=false;});
  },[profileId,operation]);
  useEffect(()=>{if(!operation?.profile)return;const timer=window.setInterval(()=>void observe(),1000);return()=>window.clearInterval(timer);},[operation,profileId]);
  useEffect(()=>{if(!closeRequest||operationOwner.current||closeRequest.profile!==profileId)return;const original:PageOperation={id:newRequestId(),kind:"close",page:closeRequest,profile:profileId,sent:false,registered:true};operationOwner.current=original;setOperation(original);setBusy(true);},[closeRequest,profileId,operation]);
  const create=()=>{
    if(!workspaceActive||!available||!supported||!idPattern.test(accountId)||!validAddress(address)||operationOwner.current||registration.busy||registration.uncertain||(state?.tabs.tabs.length??0)>=16||failure||profileUnavailable||profiles.isFetching||profiles.error||(!profileId&&profiles.data?.nextPageToken))return;
    const original:PageOperation={id:newRequestId(),kind:"create",url:address,profile:profileId,sent:false,registered:false};operationOwner.current=original;setOperation(original);setBusy(true);setUncommitted(false);
    void registration.send({session:{id:session.id,expectedRevision:session.revision,requestId:newRequestId()},accountId});
  };

  useEffect(() => {
    if (!profileId || !viewport.current) return;
    let disposed = false, opened = false, opening = false, resizing = false;
    let lastBounds = "", queued = false, failed = false, ownedViewId = "";
    let hiding = false, hideRequired = false, hideRetry: number | undefined;
    const node = viewport.current;
    const visible = () => activeRef.current && needsPresentation.current && node.isConnected && !node.closest("[hidden], [inert]") && !Array.from(documentGlobal().querySelectorAll('dialog[open]:not([role="region"]), [role="dialog"]:not(dialog)')).some((dialog) => {
      // The responsive sidebar retains a closed dialog, and its wide layout is
      // an open nonmodal region. Only a visible dialog should cover the child.
      const style = getComputedStyle(dialog), rect = dialog.getBoundingClientRect();
      return !dialog.closest("[hidden], [inert]") && style.display !== "none" && style.visibility !== "hidden" && rect.width > 0 && rect.height > 0;
    }) && node.getBoundingClientRect().width >= 1 && node.getBoundingClientRect().height >= 1;
    const bounds = () => {
      const r = node.getBoundingClientRect();
      let left = Math.max(0, r.left), top = Math.max(0, r.top), right = Math.min(window.innerWidth, r.right), bottom = Math.min(window.innerHeight, r.bottom);
      for (let parent = node.parentElement; parent; parent = parent.parentElement) {
        const style = getComputedStyle(parent), rect = parent.getBoundingClientRect();
        if (/auto|scroll|hidden|clip/.test(style.overflowX)) { left = Math.max(left, rect.left); right = Math.min(right, rect.right); }
        if (/auto|scroll|hidden|clip/.test(style.overflowY)) { top = Math.max(top, rect.top); bottom = Math.min(bottom, rect.bottom); }
      }
      return { x: left, y: top, width: Math.max(0, right - left), height: Math.max(0, bottom - top) };
    };
    const hide = async () => {
      if (!opened || hiding) return;
      hideRequired = true; hiding = true;
      const viewId = ownedViewId;
      let closed = false;
      try {
        await invoke("control_browser", { profileId, viewId, action: BrowserAction.Hide });
        opened = false; ownedViewId = ""; lastBounds = ""; hideRequired = false; closed = true;
        if (presentation.current === viewId) presentation.current = "";
        if (hideRetry !== undefined) { window.clearTimeout(hideRetry); hideRetry = undefined; }
      } catch {
        if (!disposed) setFailure(ownedMessage("session-browser.extra.a520c066c88f"));
        // Keep exact cleanup ownership after unmount. A failed Hide has not
        // released the raw child; retry only this closure, never an open/action.
        if (hideRetry === undefined) hideRetry = window.setTimeout(() => { hideRetry = undefined; void hide(); }, 250);
      } finally {
        hiding = false;
        if (closed && !disposed) { queued = false; void update(); }
      }
    };
    const update = async () => {
      if (disposed || failed) return;
      if (opening || resizing || hiding) { queued = true; return; }
      if (opened && hideRequired) { await hide(); return; }
      const area = bounds(), geometry = JSON.stringify(area);
      if (!visible() || area.width < 1 || area.height < 1) { await hide(); return; }
      if (!opened) {
        opening = true;
        const viewId = newRequestId(); ownedViewId = viewId; presentation.current = viewId;
        try {
          const result = browserState(await invoke<BrowserState>("open_browser", { profileId, viewId, url: selectedPageRef.current?.label ?? address, tabId: selectedPageRef.current?.id, bounds: area }));
          opened = true; lastBounds = geometry;
          if (disposed || presentation.current !== viewId) { await hide(); return; }
          if (!visible()) { await hide(); return; }
          setState(result);
        } catch { failed = true; if (!disposed) {setBusy(false);setFailure(ownedMessage("session-browser.extra.08759b664829"));} }
        finally { opening = false; if (queued) { queued = false; void update(); } }
      } else {
        if (geometry === lastBounds) return;
        resizing = true;
        const viewId = presentation.current;
        try { const result = browserState(await invoke<BrowserState>("control_browser", { profileId, viewId, action: BrowserAction.Resize, bounds: area })); if (!disposed && opened && presentation.current === viewId) { lastBounds = geometry; setState(result); } } catch { lastBounds = ""; if (!disposed) setFailure(ownedMessage("session-browser.extra.1b8cff3d45fb")); }
        finally { resizing = false; if (queued) { queued = false; void update(); } }
      }
    };
    void update();
    const move = () => void update(); window.addEventListener("scroll", move, true); window.addEventListener("resize", move);
    const observer = new ResizeObserver(() => void update()); observer.observe(node);
    const visibility = new MutationObserver(() => void update()); visibility.observe(documentGlobal().body, { subtree: true, attributes: true, attributeFilter: ["hidden", "inert", "open", "class"], childList: true });
    const timer = window.setInterval(() => {
      if (!opened || disposed || !visible()) return;
      void invoke<BrowserState>("browser_state", { profileId, viewId: presentation.current }).then((value) => { if (!disposed) setState(browserState(value)); }).catch(() => { if (!disposed) setFailure(ownedMessage("session-browser.extra.c6d1ec80c1c4")); });
    }, 1000);
    return () => { disposed = true; window.removeEventListener("scroll", move, true); window.removeEventListener("resize", move); observer.disconnect(); visibility.disconnect(); window.clearInterval(timer); void hide(); };
    // Address changes navigate the existing native tab only at the explicit Go action.
    // Reopening after a modal restores native local tabs without another registration.
  }, [profileId, retry, selectedPage?.id]);
  const control = async (action: BrowserAction, tabId?: string) => {
    if (!profileId || busy || state?.removal_pending || !presentation.current) return;
    setBusy(true); setFailure(undefined);
    try { const result = browserState(await invoke<BrowserState>("control_browser", { profileId, viewId: presentation.current, action, url: address, tabId })); if (alive.current) { setState(result);if(openPage && action===BrowserAction.Navigate){const tab=result.tabs.tabs.find(value=>value.id===selectedPageRef.current?.id);if(tab)openPage({profile:profileId,id:tab.id,title:browserTabTitle(tab.url),label:tab.url});} } }
    catch { if (alive.current) setFailure(ownedMessage("session-browser.extra.51fee7071e2b")); }
    finally { if (alive.current) setBusy(false); }
  };

  useEffect(()=>{if(!profileId||!openPage)return;let disposed=false;let unlisten:(()=>void)|undefined;void listen<{profile_id:string;view_id:string;position:number;token:string}>("session-tab-selection",event=>{if(!disposed&&activeRef.current&&event.payload.profile_id===profileId&&event.payload.view_id===presentation.current&&event.payload.token===shortcutAdmission.current&&!shortcutModalVisible()){document.getElementById(`session-tab-${session.id}-${event.payload.position-1}`)?.focus({preventScroll:true});tabsStore.position(session.id,event.payload.position);}}).then(stop=>{if(disposed)stop();else unlisten=stop;}).catch(()=>{if(!disposed)console.warn("delidev.browser_shortcuts",{stage:"listener",classification:"unavailable"});});return()=>{disposed=true;unlisten?.();};},[profileId,session.id,tabsStore]);
  useEffect(()=>{if(!profileId||!openPage)return;let disposed=false;const update=()=>{const viewId=presentation.current;if(!viewId)return;const token=newRequestId();shortcutAdmission.current=token;void invoke("browser_tab_shortcuts",{profileId,viewId,token,count:active&&!shortcutModalVisible()?Math.min(9,tabsStore.snapshot(session.id).tabs.length):0}).catch(()=>{if(!disposed)console.warn("delidev.browser_shortcuts",{stage:"admission",classification:"unavailable"});});};update();const observer=new MutationObserver(update);observer.observe(document.body,{subtree:true,attributes:true,attributeFilter:["open","hidden","inert"],childList:true});const stop=tabsStore.subscribe(update);const timer=window.setInterval(update,250);return()=>{disposed=true;shortcutAdmission.current="";window.clearInterval(timer);observer.disconnect();stop();const viewId=presentation.current;if(viewId)void invoke("browser_tab_shortcuts",{profileId,viewId,token:newRequestId(),count:0}).catch(()=>{});};},[active,profileId,state?.tabs.selected,tabsStore,session.id]);
  const blocked = !active || Boolean(operation) || busy || registration.busy || registration.uncertain || state?.removal_pending;
  const addressField = <label>{copy("session-browser.address_56ef8f")}<input ref={addressInput} value={address} maxLength={8192} disabled={Boolean(operation)||registration.busy||registration.uncertain} onChange={event => setAddress(event.target.value)} placeholder="https://example.com/" /></label>;
  return <><div className="session-app-panel" hidden={!active} inert={!active}><section className={`session-browser${profileId ? " browser-registered" : ""}`} aria-label={copy("session-browser.sessionBrowser_47d746")}>
    <div className="browser-chrome">
      <BrowserHeader layout={layout} />
      {!supported && !capabilities.error ? <p role="status">{copy("session-browser.protectedBrowserProfilesAreUnavailableUntil_033b80")}</p> : null}
      <Problem error={capabilities.error} actions={<button disabled={capabilities.isFetching} onClick={() => void capabilities.refetch()}>{copy("session-browser.retryCapability")}</button>} />
      {failure ? <p role="alert">{failure}</p> : null}
      {state?.removal_pending ? <p role="alert">{copy("session-browser.thisAccountWasDeletedItsProfile_4cf6fc")}</p> : null}
      {profileId ? <>

        <form onSubmit={event => { event.preventDefault(); void control(BrowserAction.Navigate); }} className="browser-address">
          <button type="button" disabled={blocked} onClick={() => void control(BrowserAction.Back)} aria-label={copy("session-browser.back_76900f")}>←</button><button type="button" disabled={blocked} onClick={() => void control(BrowserAction.Forward)} aria-label={copy("session-browser.forward_f1c65e")}>→</button><button type="button" disabled={blocked} onClick={() => void control(BrowserAction.Reload)} aria-label={copy("session-browser.reload_bdc090")}>↻</button>
          <label>{copy("session-browser.address_56ef8f")}<input value={address} maxLength={8192} onChange={event=>setAddress(event.target.value)} /></label><button disabled={blocked||!validAddress(address)}>{copy("session-browser.go_6cc851")}</button>
          <details className="browser-overflow"><summary aria-label={copy("session-browser.moreActions")}>⋯</summary><button type="button" disabled={blocked} onClick={() => { setFailure(undefined); setRetry(n => n + 1); }}>{copy("session-browser.retryNativeView_0d84d0")}</button></details>
        </form>
      </> : null}
    </div>
    <div ref={viewport} className="browser-viewport" hidden={!needsPresentation.current} aria-label={copy("session-browser.untrustedBrowserContent_9c54bc")} />
  </section></div>
    {closeRequest&&!operation?<div className="browser-operation-status" role="status">{copy("session-browser.loadingPages")}{failure?<p role="alert">{failure}</p>:null}<Problem error={capabilities.error||profiles.error}/><button type="button" onClick={()=>{void capabilities.refetch();void profiles.refetch();}}>{copy("session-browser.retryCapability")}</button></div>:null}
    {operation&&!creationOpen?<div className="browser-operation-status" role="status">{copy("session-browser.operationPending")}<button type="button" onClick={()=>void observe()}>{copy("session-browser.observeOperation")}</button>{failure?<p role="alert">{failure}</p>:null}</div>:null}
    {creationOpen&&workspaceActive?createPortal(<Modal title={copy("session-browser.newPage")} close={closeCreation} className="browser-page-dialog" initialFocus={addressInput} trapFocus><form onSubmit={event=>{event.preventDefault();create();}}><p>{copy("session-browser.sharedWithThisAccountSSessions_361a18")}</p>{addressField}{!available?<p role="status">{copy("session-browser.cefBrowserProfilesRequireTheSupported_c9b9e8")}</p>:!supported?<p role="status">{copy("session-browser.protectedBrowserProfilesAreUnavailableUntil_033b80")}</p>:null}{capabilities.isFetching||profiles.isFetching?<p role="status">{copy("session-browser.loadingPages")}</p>:null}{(state?.tabs.tabs.length??0)>=16?<p role="status">{copy("session-browser.pageLimit")}</p>:null}{operation?<p role="status">{copy("session-browser.operationPending")}</p>:null}{failure?<p role="alert">{failure}</p>:null}<Problem error={registration.error||capabilities.error||profiles.error} actions={capabilities.error||profiles.error?<button type="button" onClick={()=>{void capabilities.refetch();void profiles.refetch();}}>{copy("session-browser.retryCapability")}</button>:undefined}/>{registration.uncertain?<button type="button" disabled={registration.busy} onClick={registration.retry}>{copy("session-browser.retryTheSameRegistration_c86ef9")}</button>:null}{uncommitted&&operation?.kind==="create"?<button type="button" onClick={()=>{if(!operationRunning.current){operationOwner.current=undefined;setOperation(undefined);setUncommitted(false);setBusy(false);setFailure(undefined);}}}>{copy("session-browser.discardRequest")}</button>:null}{operation?.profile?<button type="button" onClick={()=>void observe()}>{copy("session-browser.observeOperation")}</button>:null}<footer><button type="button" onClick={closeCreation}>{copy("ui.cancel")}</button><button type="submit" disabled={!available||!supported||Boolean(operation)||registration.busy||registration.uncertain||!validAddress(address)||Boolean(failure)||profileUnavailable||profiles.isFetching||Boolean(profiles.error)||Boolean(!profileId&&profiles.data?.nextPageToken)||(state?.tabs.tabs.length??0)>=16}>{copy("session-browser.openPage")}</button></footer></form></Modal>,documentGlobal().body):null}
  </>;
}
// Keep DOM access distinct from the versioned resource-document decoder.
const documentGlobal = () => globalThis.document;

export function validAddress(raw: string): boolean { try { const url = new URL(raw); return ["http:", "https:"].includes(url.protocol) && !url.username && !url.password && raw.length <= 8192; } catch { return false; } }
