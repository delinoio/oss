// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { StrictMode, useState } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { BrowserService, BrowserCapability, BrowserProfileSchema, BrowserProfileState, EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { MutationIntents } from "./mutation";
import { SessionBrowser, browserProfile, browserState, validAddress } from "./session-browser";
import { BrowserHostProvider } from "./host-capabilities";
import { SessionTabKind, SessionTabsProvider, useSessionTabsStore } from "./session-tabs";
const native = vi.hoisted(() => vi.fn());
vi.mock("@tauri-apps/api/core", () => ({ invoke: native }));
vi.mock("@tauri-apps/api/event", () => ({ listen: vi.fn(async()=>()=>{}) }));
beforeEach(() => {
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ x: 100, y: 100, left: 100, top: 100, width: 300, height: 200, right: 400, bottom: 300, toJSON: () => ({}) });
  native.mockReset();
});
function fixture(empty=false) {
  const accountId=newRequestId(), profileId=newRequestId(),tabId=newRequestId();
  const profile=create(BrowserProfileSchema,{id:profileId,revision:1n,serverId:newRequestId(),deviceId:newRequestId(),accountId,state:BrowserProfileState.ACTIVE});
  const session=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,revision:7n});
  const register=vi.fn(async (request: {session?:{requestId:string}})=>({profile,requestId:request.session?.requestId}));
  const list=vi.fn(async()=>({profiles:[profile]}));
  const transport=createRouterTransport(router=>router.service(BrowserService,{registerBrowserProfile:register,listBrowserProfiles:list,getBrowserCapabilities:()=>({capabilities:[BrowserCapability.PROTECTED_DEVICE_PROFILE_V1]})}));
  const local={tabs:{tabs:empty?[] as {id:string;url:string}[]:[{id:tabId,url:"https://fixture.test/page"}],selected:empty?"":tabId},removal_pending:false};
  const ledger=new Map<string,{page_id:string;phase:"complete"|"native-pending"}>();
  let pendingClose=false, pendingCreate=false, closePage:ReturnType<typeof descriptor>|undefined;
  const descriptor=(id:string)=>({kind:SessionTabKind.Page as const,profile:profileId,id,title:"fixture.test/page",label:"https://fixture.test/page"});
  const closed=vi.fn();
  function reply(args: {action:{action:string;operation_id?:string;url?:string;page_id?:string}}) {
    const action=args.action;
    if(action.action==="inventory")return {state:local,pending_pages:[...ledger.values()].filter(op=>op.phase==="native-pending").map(op=>op.page_id)};
    let receipt=ledger.get(action.operation_id!);
    if(!receipt&&action.action==="create"){
      const page_id=newRequestId();local.tabs.tabs.push({id:page_id,url:action.url!});local.tabs.selected=page_id;
      receipt={page_id,phase:pendingCreate?"native-pending":"complete"};ledger.set(action.operation_id!,receipt);
    }
    if(!receipt&&action.action==="close"){
      local.tabs.tabs=local.tabs.tabs.filter(tab=>tab.id!==action.page_id);local.tabs.selected=local.tabs.tabs[0]?.id??"";
      receipt={page_id:action.page_id!,phase:pendingClose?"native-pending":"complete"};ledger.set(action.operation_id!,receipt);
    }
    if(!receipt)throw new Error("unknown original operation");
    return {state:local,pending_pages:receipt.phase==="native-pending"?[receipt.page_id]:[],operation:{operation_id:action.operation_id!,...receipt}};
  }
  const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
  function Browser(){
    const store=useSessionTabsStore();
    const [selected,setSelected]=useState(empty?undefined:descriptor(tabId));
    const [creation,setCreation]=useState(false);const [closing,setClosing]=useState<ReturnType<typeof descriptor>>();
    return <><button onClick={()=>setCreation(true)}>New browser page</button><button onClick={()=>{closePage=descriptor(tabId);setClosing(closePage);}}>Close original page</button><button onClick={()=>setSelected(undefined)}>Conversation</button><SessionBrowser session={session} accountId={accountId} close={()=>{}} active={Boolean(selected)} selectedPage={selected} creationOpen={creation} closeCreation={()=>setCreation(false)} closeRequest={closing} closeSettled={page=>{closed(page);store.closePage(session.id,page);setClosing(undefined);if(selected?.id===page.id)setSelected(undefined);}} openPage={page=>{store.open(session.id,{kind:SessionTabKind.Page,...page});setSelected({...page,kind:SessionTabKind.Page});}}/></>;
  }
  function View(){return <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionTabsProvider><Browser/></SessionTabsProvider></MutationIntents></QueryClientProvider></TransportProvider>;}
  const f={accountId,profileId,tabId,profile,session,register,list,local,ledger,reply,View,closed,setPendingClose:(value:boolean)=>{pendingClose=value;},setPendingCreate:(value:boolean)=>{pendingCreate=value;}};
  setNative(f);return f;
}
function setNative(f: {local:unknown;reply:(args:any)=>unknown}, presentation?: (operation:string,args:any)=>Promise<unknown>) {
  native.mockImplementation(async(operation,args)=>operation==="browser_pages"?f.reply(args):presentation?presentation(operation,args):f.local);
}
async function opened(){await waitFor(()=>expect(native.mock.calls.some(([operation])=>operation==="open_browser")).toBe(true));}
async function open(){await opened();}
async function dialog(){fireEvent.click(screen.getByRole("button",{name:"New browser page"}));return screen.findByRole("dialog",{name:"New browser page"});}
async function submit(){const modal=await dialog();fireEvent.change(within(modal).getByRole("textbox",{name:"Address"}),{target:{value:"https://fixture.test/explicit"}});const button=within(modal).getByRole("button",{name:"Open page"});await waitFor(()=>expect(button).toHaveProperty("disabled",false));fireEvent.click(button);}
function retryView(){const summary=screen.getByLabelText("More browser actions");if(!(summary.closest("details") as HTMLDetailsElement).open)fireEvent.click(summary);fireEvent.click(screen.getByRole("button",{name:"Retry native view"}));}
it("restores exact native pages without registration, creation, inner tabs or a header close",async()=>{
 const f=fixture();const view=render(<StrictMode><f.View/></StrictMode>);await opened();expect(f.register).not.toHaveBeenCalled();expect(native.mock.calls.some(([op,args])=>op==="browser_pages"&&args.action.action==="create")).toBe(false);
 expect(native).toHaveBeenCalledWith("open_browser",expect.objectContaining({profileId:f.profileId,tabId:f.tabId}));expect(screen.queryByRole("button",{name:"New tab"})).toBeNull();expect(screen.queryByRole("button",{name:"Close Browser"})).toBeNull();expect(view.container.querySelector("iframe,webview,script,a")).toBeNull();
});
it("opening and cancelling the address dialog leaves an empty profile empty",async()=>{
 const f=fixture(true);render(<f.View/>);const modal=await dialog();await waitFor(()=>expect(f.list).toHaveBeenCalled());expect(within(modal).getByRole("textbox",{name:"Address"})).toBe(document.activeElement);fireEvent.click(within(modal).getByRole("button",{name:"Cancel"}));expect(f.register).not.toHaveBeenCalled();expect(f.local.tabs.tabs).toHaveLength(0);expect(native.mock.calls.some(([op,args])=>op==="open_browser"||op==="browser_pages"&&args.action.action==="create")).toBe(false);
});
it.each([true,false])("explicit submission validates the original session and creates exactly one page (empty=%s)",async empty=>{
 const f=fixture(empty);render(<f.View/>);await submit();await waitFor(()=>expect(f.local.tabs.tabs).toHaveLength(empty?1:2));await waitFor(()=>expect(screen.queryByRole("dialog",{name:"New browser page"})).toBeNull());expect(f.register).toHaveBeenCalledTimes(1);expect(f.register.mock.calls[0][0]).toMatchObject({accountId:f.accountId,session:{id:f.session.id,expectedRevision:7n}});expect(f.register.mock.calls[0][0]).not.toHaveProperty("url");expect(native.mock.calls.filter(([op,args])=>op==="browser_pages"&&args.action.action==="create")).toHaveLength(1);
});
it("creates one profile/page only after explicit submission when inventory is absent",async()=>{
 const f=fixture(true);f.list.mockResolvedValue({profiles:[]});render(<f.View/>);await submit();await waitFor(()=>expect(f.local.tabs.tabs).toHaveLength(1));expect(f.register).toHaveBeenCalledTimes(1);expect(native.mock.calls.filter(([op,args])=>op==="browser_pages"&&args.action.action==="create")).toHaveLength(1);
});
it("hides only the earlier child when its exact native open resolves after replacement",async()=>{
 const f=fixture();let finish!:(value:typeof f.local)=>void;let opens=0;setNative(f,async(op)=>{if(op==="open_browser"&&++opens===1)return new Promise(resolve=>{finish=resolve;});return f.local;});render(<f.View/>);await opened();const first=native.mock.calls.find(([op])=>op==="open_browser")![1];retryView();await waitFor(()=>expect(native.mock.calls.filter(([op])=>op==="open_browser")).toHaveLength(2));const second=native.mock.calls.filter(([op])=>op==="open_browser")[1][1];await act(async()=>finish(f.local));await waitFor(()=>expect(native.mock.calls.some(([op,args])=>op==="control_browser"&&args.action==="hide"&&args.viewId===first.viewId)).toBe(true));expect(native.mock.calls.some(([op,args])=>op==="control_browser"&&args.action==="hide"&&args.viewId===second.viewId)).toBe(false);
});
it("retains uncertain original registration and retries its exact request only on explicit retry",async()=>{
 const f=fixture(true);f.register.mockRejectedValueOnce(new ConnectError("lost",Code.Unavailable));render(<f.View/>);await submit();fireEvent.click(await screen.findByRole("button",{name:"Retry the same registration"}));await waitFor(()=>expect(f.local.tabs.tabs).toHaveLength(1));expect(f.register.mock.calls[1][0]).toEqual(f.register.mock.calls[0][0]);
});
it("dismissal and reopening observe a lost original create reply without another create",async()=>{
 const f=fixture(true);let lost=true;native.mockImplementation(async(op,args)=>{if(op!=="browser_pages")return f.local;const reply=f.reply(args);if(args.action.action==="create"&&lost){lost=false;throw new Error("lost reply");}return reply;});render(<f.View/>);await submit();await screen.findByText("The original page operation could not be confirmed. Check its result before starting another operation.");const modal=screen.getByRole("dialog",{name:"New browser page"});fireEvent.click(within(modal).getByRole("button",{name:"Cancel"}));await dialog();fireEvent.click(screen.getByRole("button",{name:"Check original operation"}));await waitFor(()=>expect(screen.queryByRole("dialog",{name:"New browser page"})).toBeNull());expect(f.local.tabs.tabs).toHaveLength(1);expect(native.mock.calls.filter(([op,args])=>op==="browser_pages"&&args.action.action==="create")).toHaveLength(1);
});
it("closing an inactive page uses the hidden original operation and waits for native cleanup",async()=>{
 const f=fixture();f.setPendingClose(true);render(<f.View/>);await opened();fireEvent.click(screen.getByRole("button",{name:"Conversation"}));const opens=native.mock.calls.filter(([op])=>op==="open_browser").length;fireEvent.click(screen.getByRole("button",{name:"Close original page"}));await waitFor(()=>expect(f.local.tabs.tabs).toHaveLength(0));expect(f.closed).not.toHaveBeenCalled();for(const receipt of f.ledger.values())receipt.phase="complete";fireEvent.click(await screen.findByRole("button",{name:"Check original operation"}));await waitFor(()=>expect(f.closed).toHaveBeenCalledTimes(1));expect(native.mock.calls.filter(([op])=>op==="open_browser")).toHaveLength(opens);expect(native.mock.calls.filter(([op,args])=>op==="browser_pages"&&args.action.action==="close")).toHaveLength(1);
});
it("caps pages at 16 and keeps navigation bound to the exact selected native child",async()=>{
 const f=fixture();for(let n=1;n<16;n++)f.local.tabs.tabs.push({id:newRequestId(),url:`https://fixture.test/${n}`});render(<f.View/>);await opened();const modal=await dialog();fireEvent.change(within(modal).getByRole("textbox",{name:"Address"}),{target:{value:"https://fixture.test/new"}});expect(within(modal).getByRole("button",{name:"Open page"})).toHaveProperty("disabled",true);fireEvent.click(within(modal).getByRole("button",{name:"Cancel"}));await waitFor(()=>expect(native.mock.calls.filter(([op])=>op==="open_browser").length).toBeGreaterThan(1));for(const name of ["Back","Forward","Reload"]){fireEvent.click(screen.getByRole("button",{name}));await waitFor(()=>expect(screen.getByRole("button",{name})).toHaveProperty("disabled",false));}expect(native.mock.calls.filter(([op,args])=>op==="control_browser"&&["back","forward","reload"].includes(args.action)).every(([,args])=>args.profileId===f.profileId)).toBe(true);expect(f.register).not.toHaveBeenCalled();
});
it.each(["file:///fixture","https://user:secret@fixture.test/","https://fixture.test/"+"a".repeat(8192)])("denies an invalid creation address: %s",async address=>{
 const f=fixture(true);render(<f.View/>);const modal=await dialog();fireEvent.change(within(modal).getByRole("textbox",{name:"Address"}),{target:{value:address}});expect(within(modal).getByRole("button",{name:"Open page"})).toHaveProperty("disabled",true);expect(f.register).not.toHaveBeenCalled();
});
it("does not grant native authority to deleted or foreign profiles",()=>{const f=fixture();expect(()=>browserProfile({...f.profile,accountId:newRequestId()},f.accountId)).toThrow();expect(()=>browserProfile({...f.profile,state:BrowserProfileState.REMOVAL_PENDING,deletionRequestId:newRequestId()},f.accountId)).toThrow();expect(()=>browserState({...f.local,tabs:{...f.local.tabs,selected:newRequestId()}})).toThrow();expect(validAddress("https://fixture.test/")).toBe(true);});
it("an unavailable host shows a disabled dialog without RPC or native authority",()=>{const session=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,revision:1n});render(<BrowserHostProvider available={false}><SessionBrowser session={session} accountId={newRequestId()} close={()=>{}} creationOpen/></BrowserHostProvider>);expect(screen.getByRole("button",{name:"Open page"})).toHaveProperty("disabled",true);expect(native).not.toHaveBeenCalled();});

it("retries identical geometry after a failed native resize and caches only success", async () => {
  const f = fixture();
  let top = 100, attempts = 0;
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(() => ({ x: 100, y: top, left: 100, top, width: 300, height: 200, right: 400, bottom: top + 200, toJSON: () => ({}) }));
  setNative(f, async (_operation, args) => {
    if (args.action === "resize" && ++attempts === 1) throw new Error("transient native resize failure");
    return f.local;
  });
  render(<f.View />);
  await open();
  await opened();
  top = 125;
  fireEvent.resize(window);
  await screen.findByText("The browser view could not be updated.");
  fireEvent.resize(window);
  await waitFor(() => expect(attempts).toBe(2));
  const resizes = native.mock.calls.filter(([, args]) => args.action === "resize");
  expect(resizes[1][1]).toEqual(resizes[0][1]);
  await act(async () => { fireEvent.resize(window); });
  expect(attempts).toBe(2);
});
it("surfaces a delayed native child failure and reopens only after explicit retry", async () => {
  const f = fixture();
  setNative(f, async (operation) => {
    if (operation === "browser_state") throw new Error("sidecar-failed");
    return f.local;
  });
  render(<f.View />);
  await open();
  await opened();
  const first = native.mock.calls.find(([operation]) => operation === "open_browser")![1];
  await screen.findByText("The local browser state is unavailable.", {}, { timeout: 2500 });
  expect(native.mock.calls.filter(([operation]) => operation === "open_browser")).toHaveLength(1);
  retryView();
  await waitFor(() => expect(native.mock.calls.filter(([operation]) => operation === "open_browser")).toHaveLength(2));
  expect(native.mock.calls.filter(([operation]) => operation === "open_browser")[1][1].viewId).not.toBe(first.viewId);
});
it("retains the exact view after failed hide and blocks resize or reopening until closure", async () => {
  const f = fixture();
  let attempts = 0, finish!: (value: typeof f.local) => void;
  setNative(f, async (_operation, args) => {
    if (args.action === "hide") {
      if (++attempts === 1) throw new Error("busy");
      if (attempts === 2) return new Promise(resolve => { finish = resolve; });
    }
    return f.local;
  });
  const view = render(<f.View />);
  await open();
  await opened();
  const original = native.mock.calls.find(([op]) => op === "open_browser")![1];
  const modal = document.createElement("dialog"); modal.setAttribute("open", "");
  try {
    await act(async () => { document.body.append(modal); });
    await screen.findByText("The browser view could not be hidden. Retrying native closure.");
    await waitFor(() => expect(attempts).toBe(2));
    await act(async () => { modal.remove(); fireEvent.resize(window); });
    expect(native.mock.calls.filter(([op]) => op === "open_browser")).toHaveLength(1);
    expect(native.mock.calls.filter(([, args]) => args.action === "resize")).toHaveLength(0);
    expect(native.mock.calls.filter(([, args]) => args.action === "hide").every(([, args]) => args.viewId === original.viewId)).toBe(true);
    await act(async () => { finish(f.local); });
    await waitFor(() => expect(native.mock.calls.filter(([op]) => op === "open_browser")).toHaveLength(2));
    expect(native.mock.calls.filter(([op]) => op === "open_browser")[1][1].viewId).not.toBe(original.viewId);
  } finally { modal.remove(); view.unmount(); }
});
it("retries failed cleanup after unmount with only its original view identity", async () => {
  const f = fixture();
  let attempts = 0;
  setNative(f, async (_operation, args) => {
    if (args.action === "hide" && ++attempts === 1) throw new Error("busy");
    return f.local;
  });
  const view = render(<f.View />);
  await open();
  await opened();
  const original = native.mock.calls.find(([op]) => op === "open_browser")![1];
  view.unmount();
  await waitFor(() => expect(attempts).toBe(2));
  expect(native.mock.calls.filter(([, args]) => args.action === "hide").every(([, args]) => args.viewId === original.viewId)).toBe(true);
  await new Promise(resolve => setTimeout(resolve, 300));
  expect(attempts).toBe(2);
  expect(native.mock.calls.filter(([op]) => op === "open_browser")).toHaveLength(1);
});
it.each(["closed drawer", "wide region", "hidden popup"])("opens beside the persistent %s and hides only for an active modal", async (surface) => {
  const f = fixture();
  const sidebar = document.createElement(surface === "hidden popup" ? "div" : "dialog");
  sidebar.setAttribute("role", surface === "wide region" ? "region" : "dialog");
  if (surface === "wide region") sidebar.setAttribute("open", "");
  if (surface === "hidden popup") sidebar.hidden = true;
  document.body.append(sidebar);
  try {
    render(<f.View />);
    await open();
    await waitFor(() => expect(native.mock.calls.filter(([op]) => op === "open_browser")).toHaveLength(1));
    const first = native.mock.calls.find(([op]) => op === "open_browser")![1];
    await act(async () => { sidebar.setAttribute("role", "dialog"); sidebar.setAttribute("open", ""); sidebar.hidden = false; });
    await waitFor(() => expect(native).toHaveBeenCalledWith("control_browser", expect.objectContaining({ action: "hide", viewId: first.viewId })));
    await act(async () => { sidebar.removeAttribute("open"); sidebar.hidden = true; });
    await waitFor(() => expect(native.mock.calls.filter(([op]) => op === "open_browser")).toHaveLength(2));
    const second = native.mock.calls.filter(([op]) => op === "open_browser")[1][1];
    expect(second.viewId).not.toBe(first.viewId);
  } finally { sidebar.remove(); }
});

it("opens an information modal through the original native hide owner without registering again", async () => {
  const f = fixture(); render(<f.View />); await open();
  await opened();
  const original = native.mock.calls.find(([operation]) => operation === "open_browser")![1];
  const information = screen.getByRole("button", { name: "Browser information" });
  information.focus(); fireEvent.click(information);
  const dialog = screen.getByRole("dialog", { name: "Browser information" });
  expect(dialog.textContent).toContain("Tabs, cookies, history and browser credentials stay local.");
  await waitFor(() => expect(native).toHaveBeenCalledWith("control_browser", expect.objectContaining({ action: "hide", viewId: original.viewId })));
  fireEvent.click(screen.getByRole("button", { name: "Close Browser information" }));
  await waitFor(() => expect(native.mock.calls.filter(([operation]) => operation === "open_browser")).toHaveLength(2));
  expect(f.register).not.toHaveBeenCalled(); expect(document.activeElement).toBe(information);
  expect(native.mock.calls.some(([, args]) => args.action === "navigate")).toBe(false);
});

it("fences a retained Browser under the maximized upper inert region through exact Hide uncertainty", async () => {
  const f = fixture(); let attempts = 0, finish!: (value: typeof f.local) => void;
  setNative(f, async (_operation, args) => {
    if (args.action === "hide") {
      if (++attempts === 1) throw new Error("busy");
      if (attempts === 2) return new Promise(resolve => { finish = resolve; });
    }
    return f.local;
  });
  const view = render(<div data-upper><f.View /></div>), upper = view.container.querySelector("[data-upper]")!;
  await open();await opened();
  const original = native.mock.calls.find(([operation]) => operation === "open_browser")![1];
  try {
    await act(async () => { upper.setAttribute("inert", ""); upper.setAttribute("aria-hidden", "true"); });
    await waitFor(() => expect(attempts).toBe(2));
    await act(async () => { upper.removeAttribute("inert"); upper.removeAttribute("aria-hidden"); fireEvent.resize(window); });
    expect(native.mock.calls.filter(([operation]) => operation === "open_browser")).toHaveLength(1);
    expect(native.mock.calls.filter(([, args]) => args.action === "hide").every(([, args]) => args.viewId === original.viewId)).toBe(true);
    expect(native.mock.calls.filter(([, args]) => args.action === "resize")).toHaveLength(0);
    await act(async () => { finish(f.local); });
    await waitFor(() => expect(native.mock.calls.filter(([operation]) => operation === "open_browser")).toHaveLength(2));
    expect(f.register).not.toHaveBeenCalled();
    expect(native.mock.calls.filter(([operation]) => operation === "open_browser")[1][1].profileId).toBe(original.profileId);
  } finally { view.unmount(); }
});
