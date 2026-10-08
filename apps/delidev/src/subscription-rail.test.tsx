// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport, Code, ConnectError } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SystemCapability, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { freshWindow, remainingBadge, railAccount, type RailWindow } from "./subscription-rail-data";
import { SubscriptionRail } from "./subscription-rail";
const now = Date.now();
const window = (remaining: number, extra: Partial<RailWindow> = {}): RailWindow => ({ id: "weekly", remaining, state: "observed", observedAt: new Date(now).toISOString(), resetAt: "", comparisonGroup: "weekly", blocking: true, ...extra });
function resource(name: string, service = "chatgpt", extra = {}): Resource { return create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 2, documentJson: encode({ type: "subscription", name, subscription_service: service, connection: { id: newRequestId() }, quota: [{ id: "weekly", remaining: .28, state: "observed", observed_at: new Date().toISOString(), comparison_group: "weekly", blocking: true }], ...extra }) }); }
afterEach(() => vi.useRealTimers());
it("uses only complete fresh blocking evidence, preserving measured endpoints", () => {
 expect(remainingBadge([window(.28), window(.64), window(.01, {blocking:false})], now)).toBe(28);
 expect(remainingBadge([window(0)], now)).toBe(0); expect(remainingBadge([window(1)], now)).toBe(100);
 for (const entry of [window(NaN), window(Infinity), window(-.1), window(1.1), window(.2,{remaining:undefined}), window(.2,{comparisonGroup:""}), window(.2,{state:"failed"}),window(.2,{state:"unsupported"}),window(.2,{observedAt:new Date(now+1).toISOString()}),window(.2,{observedAt:new Date(now-300001).toISOString()}),window(.2,{resetAt:new Date(now).toISOString()})]) expect(remainingBadge([window(.8),entry],now)).toBeUndefined();
 expect(remainingBadge([],now)).toBeUndefined(); expect(remainingBadge([window(.2,{blocking:false})],now)).toBeUndefined();
});
it("projects safe independent identities and excludes disconnected/removal/recovery accounts", () => {
 const a=resource("Personal"), b=resource("Personal"); expect(railAccount(a).id).not.toBe(railAccount(b).id);
 for(const extra of [{connection:{}},{removal:{}},{subscription:{recovery_required:true}}]) expect(railAccount(resource("Hidden","chatgpt",extra)).connected).toBe(false);
 expect(Object.keys(railAccount(a)).sort()).toEqual(["alias","connected","disabled","id","revision","service","windows"]);
});
function mount(read: (token:string)=>{resources:Resource[];nextPageToken?:string}|Promise<{resources:Resource[];nextPageToken?:string}>, capable=true) {
 const requests=vi.fn(read), manage=vi.fn(), focusFallback=vi.fn(); const transport=createRouterTransport(router=>{router.service(SystemService,{getStatus:()=>({capabilities:capable?[SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1]:[]})});router.service(ResourceService,{listResources:r=>{expect(r.accountType).toBe(2);expect(r.filter?.pageSize).toBe(50);return requests(r.filter!.pageToken);}});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}}); const view=(enabled=true)=><QueryClientProvider client={client}><TransportProvider transport={transport}><SubscriptionRail enabled={enabled} manage={manage} focusFallback={focusFallback}/></TransportProvider></QueryClientProvider>; return {...render(view()),view,requests,manage,focusFallback,client};
}
it("shows separate accounts, read-only selected details, manages and restores opener focus",async()=>{
 const values=[resource("Personal"),resource("Work"),resource("Claude","claude"),resource("Grok","grok"),resource("Disconnected","chatgpt",{connection:{}})];const f=mount(()=>({resources:values}));
 const opener=await screen.findByRole("button",{name:/ChatGPT · Personal/});expect(screen.getAllByRole("button",{name:/remaining/})).toHaveLength(4);fireEvent.click(opener);expect(screen.getByRole("dialog",{name:"ChatGPT · Personal"})).toBeTruthy();
 fireEvent.keyDown(screen.getByRole("dialog"),{key:"Escape"});expect(screen.queryByRole("dialog")).toBeNull();expect(document.activeElement).toBe(opener);
 fireEvent.click(opener);fireEvent.click(screen.getByRole("button",{name:"Manage subscriptions"}));expect(f.manage).toHaveBeenCalledOnce();expect(f.requests).toHaveBeenCalledOnce();
});
it("continues exact pages with UUID deduplication",async()=>{
 const first=resource("Personal");const f=mount(token=>token?{resources:[first,resource("Second")],nextPageToken:""}:{resources:[first],nextPageToken:"tail"});await screen.findByRole("button",{name:/Personal/});fireEvent.click(screen.getByRole("button",{name:/Load more Subscriptions/}));await screen.findByRole("button",{name:/Second/});expect(screen.getAllByRole("button",{name:/Personal/})).toHaveLength(1);expect(f.requests.mock.calls.map(call=>call[0])).toEqual(["","tail"]);
});
it("failure makes retained badges unavailable and exact explicit read retry recovers",async()=>{
 const first=resource("Personal");let failed=false;const f=mount(()=>{if(failed)throw new ConnectError("read failed",Code.Unavailable);return{resources:[first]};});await screen.findByRole("button",{name:/28% remaining/});failed=true;fireEvent(windowThis(),new Event("focus"));await screen.findByRole("button",{name:/Personal · Quota unavailable/});expect(f.requests).toHaveBeenCalledTimes(2);failed=false;fireEvent.click(screen.getByRole("button",{name:"Explain subscription read problem"}));fireEvent.click(screen.getByRole("button",{name:"Retry"}));await screen.findByRole("button",{name:/28% remaining/});
});
function windowThis(){return globalThis.window;}
it("hides unsupported inventory without reading accounts",async()=>{const f=mount(()=>({resources:[]}),false);fireEvent.click(await screen.findByRole("button",{name:"Explain subscription read problem"}));await screen.findByText("Update the server to read subscription accounts.");expect(f.requests).not.toHaveBeenCalled();});
it("fences a deferred read after disconnection",async()=>{let resolve!:(value:{resources:Resource[]})=>void;const f=mount(()=>new Promise(done=>resolve=done));await waitFor(()=>expect(f.requests).toHaveBeenCalledOnce());f.rerender(f.view(false));await act(async()=>resolve({resources:[resource("Late")]}));expect(screen.queryByRole("button",{name:/Late/})).toBeNull();});
it("requires Reload for repeating cursors without publishing the page",async()=>{
 const first=resource("Personal");const f=mount(token=>token?{resources:[resource("Should not publish")],nextPageToken:"tail"}:{resources:[first],nextPageToken:"tail"});await screen.findByRole("button",{name:/Personal/});fireEvent.click(screen.getByRole("button",{name:/Load more Subscriptions/}));await waitFor(()=>expect(f.requests).toHaveBeenCalledTimes(2));expect(screen.queryByRole("button",{name:/Should not publish/})).toBeNull();expect(screen.queryByRole("button",{name:"Retry"})).toBeNull();
});
it("clears private account projections on authentication loss",async()=>{const first=resource("Private");let lost=false;const f=mount(()=>{if(lost)throw new ConnectError("auth",Code.Unauthenticated);return{resources:[first]};});await screen.findByRole("button",{name:/Private/});lost=true;fireEvent(windowThis(),new Event("focus"));await waitFor(()=>expect(screen.queryByRole("button",{name:/Private/})).toBeNull());fireEvent.click(await screen.findByRole("button",{name:"Explain subscription read problem"}));await screen.findByText("Authentication expired. Reconnect to read subscriptions.");expect(screen.queryByRole("button",{name:/Private/})).toBeNull();});
it("a successful empty inventory reserves no account region",async()=>{const f=mount(()=>({resources:[]}));await waitFor(()=>expect(f.requests).toHaveBeenCalledOnce());await waitFor(()=>expect(f.container.children).toHaveLength(0));});
it("refreshes reached pages atomically without discovering an unseen tail",async()=>{
 const first=resource("Personal"), second=resource("Work");let refreshing=false;let finish!:(value:{resources:Resource[];nextPageToken:string})=>void;
 const f=mount(token=>{if(!refreshing)return token?{resources:[second],nextPageToken:"unseen"}:{resources:[first],nextPageToken:"tail"};if(token)return new Promise(done=>finish=done);return{resources:[{...first,revision:2n,documentJson:encode({type:"subscription",name:"Personal",subscription_service:"chatgpt",connection:{id:newRequestId()},quota:[{id:"weekly",remaining:.64,state:"observed",observed_at:new Date().toISOString(),comparison_group:"weekly",blocking:true}]})}],nextPageToken:"renewed"};});
 await screen.findByRole("button",{name:/Personal · 28%/});fireEvent.click(screen.getByRole("button",{name:/Load more Subscriptions/}));await screen.findByRole("button",{name:/Work/});refreshing=true;fireEvent(windowThis(),new Event("focus"));await waitFor(()=>expect(f.requests).toHaveBeenCalledTimes(4));expect(screen.getByRole("button",{name:/Personal · 28%/})).toBeTruthy();
 await act(async()=>finish({resources:[second],nextPageToken:"renewed-unseen"}));await screen.findByRole("button",{name:/Personal · 64%/});expect(f.requests.mock.calls.map(call=>call[0])).toEqual(["","tail","","tail"]);
});
it("refreshes saved reads at sixty seconds only while visible",async()=>{
 vi.useFakeTimers({toFake:["setInterval","clearInterval"]});const f=mount(()=>({resources:[resource("Personal")]}));await screen.findByRole("button",{name:/Personal/});expect(f.requests).toHaveBeenCalledOnce();await act(async()=>{await vi.advanceTimersByTimeAsync(59999);});expect(f.requests).toHaveBeenCalledOnce();await act(async()=>{await vi.advanceTimersByTimeAsync(1);});expect(f.requests).toHaveBeenCalledTimes(2);
 const visibility=vi.spyOn(document,"visibilityState","get").mockReturnValue("hidden");fireEvent(document,new Event("visibilitychange"));await act(async()=>{await vi.advanceTimersByTimeAsync(120000);});expect(f.requests).toHaveBeenCalledTimes(2);visibility.mockRestore();
});
it("keeps retained badges unconfirmed during a deferred retry",async()=>{
 const first=resource("Personal");let stage=0;let finish!:(value:{resources:Resource[]})=>void;
 const f=mount(()=>{if(stage===1)throw new ConnectError("read failed",Code.Unavailable);if(stage===2)return new Promise(done=>finish=done);return{resources:[first]};});await screen.findByRole("button",{name:/28% remaining/});stage=1;fireEvent(windowThis(),new Event("focus"));await screen.findByRole("button",{name:/Personal · Quota unavailable/});stage=2;fireEvent.click(screen.getByRole("button",{name:"Explain subscription read problem"}));fireEvent.click(screen.getByRole("button",{name:"Retry"}));await waitFor(()=>expect(f.requests).toHaveBeenCalledTimes(3));expect(screen.queryByRole("button",{name:/28% remaining/})).toBeNull();expect(screen.getByRole("button",{name:/Personal · Quota unavailable/})).toBeTruthy();await act(async()=>finish({resources:[first]}));await screen.findByRole("button",{name:/28% remaining/});
});
it("rejects unknown blocking membership and malformed reset evidence",()=>{
 const quota=(extra:Record<string,unknown>)=>({id:"weekly",state:"observed",remaining:.28,observed_at:new Date(now).toISOString(),blocking:true,comparison_group:"weekly",...extra});
 for(const extra of [{blocking:"true"},{blocking:1},{blocking:null},{blocking:undefined},{reset_at:"not-a-date"},{reset_at:123},{reset_at:null},{observed_at:123},{comparison_group:123},{remaining:"0.28"}]){
 const projected=railAccount(resource("Malformed","chatgpt",{quota:[quota({}),quota(extra)]}));expect(remainingBadge(projected.windows,now)).toBeUndefined();expect(freshWindow(projected.windows[1]!,now)).toBe(false);
 }
 const nonblocking=railAccount(resource("Nonblocking","chatgpt",{quota:[quota({}),quota({blocking:false,reset_at:"bad"})]}));expect(remainingBadge(nonblocking.windows,now)).toBe(28);expect(freshWindow(nonblocking.windows[1]!,now)).toBe(false);
 for(const reset of [undefined,""]){expect(remainingBadge(railAccount(resource("Valid","chatgpt",{quota:[quota({reset_at:reset})]})).windows,now)).toBe(28);}
});
it("shows malformed observation dates as unknown without individual percentages",async()=>{
 const f=mount(()=>({resources:[resource("Invalid date","chatgpt",{quota:[{id:"weekly",state:"observed",remaining:.28,observed_at:"not-a-date",blocking:true,comparison_group:"weekly"}]})]}));const opener=await screen.findByRole("button",{name:/Invalid date · Quota unavailable/});fireEvent.click(opener);const dialog=screen.getByRole("dialog",{name:"ChatGPT · Invalid date"});expect(dialog.textContent).toContain("No current quota evidence");expect(dialog.textContent).not.toContain("28% remaining");expect(dialog.textContent).not.toContain("Stale observation");expect(f.requests).toHaveBeenCalledOnce();
});
it("missing observed timestamps cannot establish individual or aggregate evidence",()=>{
 for(const observed_at of [undefined, ""]){const projected=railAccount(resource("Missing observation","chatgpt",{quota:[{id:"weekly",state:"observed",remaining:.28,observed_at,blocking:true,comparison_group:"weekly"}]}));expect(projected.windows[0]!.valid).toBe(false);expect(remainingBadge(projected.windows,now)).toBeUndefined();}
});

it("explains a denied partial page locally and uses only its original read retry", async () => {
 const first=resource("Retained"); let denied=true;
 const f=mount(token=>{if(token && denied)throw new ConnectError("untrusted-private-path",Code.PermissionDenied);return token?{resources:[resource("Later")]}:{resources:[first],nextPageToken:"tail"};});
 await screen.findByRole("button",{name:/Retained/}); fireEvent.click(screen.getByRole("button",{name:/Load more Subscriptions/}));
 const trigger=await screen.findByRole("button",{name:"Explain subscription read problem"});
 expect(screen.queryByRole("dialog")).toBeNull(); fireEvent.click(trigger);
 const popup=screen.getByRole("dialog",{name:"Explain subscription read problem"});
 expect(popup.textContent).toContain("unread pages do not establish"); expect(popup.textContent).toContain("not authorized");
 expect(screen.queryByText(/untrusted-private-path/)).toBeNull(); expect(f.requests.mock.calls.map(call=>call[0])).toEqual(["","tail"]);expect(f.manage).not.toHaveBeenCalled();
 denied=false;fireEvent.click(screen.getByRole("button",{name:"Retry"})); await screen.findByRole("button",{name:/Later/});
 expect(f.requests.mock.calls.map(call=>call[0])).toEqual(["","tail","tail"]);expect(f.manage).not.toHaveBeenCalled();
});
it("offers a saved-evidence recheck in the owning account popup without quota mutation", async () => {
 const f=mount(()=>({resources:[resource("Original")] }));fireEvent.click(await screen.findByRole("button",{name:/Original ·/}));
 fireEvent.click(screen.getByRole("button",{name:"Recheck saved quota evidence"}));await waitFor(()=>expect(f.requests).toHaveBeenCalledTimes(2));expect(f.manage).not.toHaveBeenCalled();
});

it("shows sidebar reset countdowns without quota or account operations", async () => {
  const reset = new Date(Date.now() + 529200000 + 120000).toISOString();
  const row = resource("Countdown rail", "chatgpt", { quota: [{ id: "weekly", remaining: .28, state: "observed", observed_at: new Date().toISOString(), reset_at: reset, comparison_group: "weekly", blocking: true }] });
  const read = vi.fn(() => ({ resources: [row] }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1] }) });
    router.service(ResourceService, { listResources: read });
  });
  const manage = vi.fn();
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><TransportProvider transport={transport}><SubscriptionRail enabled manage={manage} /></TransportProvider></QueryClientProvider>);
  const opener = await screen.findByRole("button", { name: /Countdown rail/ }); fireEvent.click(opener);
  expect(screen.getByText("Resets in 6 days 3 hours")).toBeTruthy(); expect(screen.getByTitle(reset).getAttribute("datetime")).toBe(reset);
  expect(read).toHaveBeenCalledTimes(1); expect(manage).not.toHaveBeenCalled();
});

it.each([
 { name: "healthy", quota: [{ id: "weekly", remaining: .28, state: "observed", observed_at: new Date().toISOString(), comparison_group: "weekly", blocking: true }], status: "Observed" },
 { name: "stale", quota: [{ id: "weekly", remaining: .28, state: "observed", observed_at: new Date(Date.now() - 360000).toISOString(), comparison_group: "weekly", blocking: true }], status: "Stale" },
 { name: "failed", quota: [{ id: "weekly", remaining: .28, state: "failed", observed_at: new Date().toISOString(), comparison_group: "weekly", blocking: true }], status: "Observation failed" },
 { name: "empty", quota: [], status: "No current quota evidence" },
])("shows truthful $name observations without inventing an operation failure", async ({ quota, status }) => {
 const f = mount(() => ({ resources: [resource("Personal", "chatgpt", { quota })] }));
 fireEvent.click(await screen.findByRole("button", { name: /ChatGPT · Personal/ }));
 const dialog = screen.getByRole("dialog", { name: "ChatGPT · Personal" });
 expect(dialog.textContent).toContain(status);
 expect(dialog.textContent).not.toContain("Quota observation was not confirmed.");
 expect(f.requests).toHaveBeenCalledTimes(1);
 expect(f.manage).not.toHaveBeenCalled();
});


it.each(["focus", "timer"])("retains exhausted account nodes without a refresh-only anchor on %s", async trigger => {
 if(trigger==="timer")vi.useFakeTimers({toFake:["setInterval","clearInterval"]});
 const rows=[resource("Personal"),resource("Work")]; let finish!:(value:{resources:Resource[]})=>void; let refreshing=false;
 const f=mount(()=>refreshing?new Promise(done=>finish=done):{resources:rows});
 const opener=await screen.findByRole("button",{name:/Personal · 28%/}); const image=opener.querySelector("img");
 expect(screen.queryByRole("button",{name:"Reload list"})).toBeNull(); expect(f.container.querySelector(".sidebar-continuation")).toBeNull();
 fireEvent.click(opener); await act(async()=>{await new Promise(done=>setTimeout(done,0));}); refreshing=true;
 if(trigger==="focus")fireEvent(windowThis(),new Event("focus"));else await act(async()=>vi.advanceTimersByTimeAsync(60000));
 await waitFor(()=>expect(f.requests).toHaveBeenCalledTimes(2));
 expect(screen.getByRole("button",{name:/Personal · 28%/})).toBe(opener); expect(opener.querySelector("img")).toBe(image); expect(opener.getAttribute("aria-expanded")).toBe("true");
 expect(f.container.querySelector(".sidebar-continuation")).toBeNull();
 await act(async()=>finish({resources:rows})); expect(screen.getByRole("button",{name:/Personal · 28%/})).toBe(opener);
});
it("retains an accepted continuation without refresh text and disables admission",async()=>{
 const first=resource("Personal"); let finish!:(value:{resources:Resource[];nextPageToken:string})=>void;let refreshing=false;
 const f=mount(()=>refreshing?new Promise(done=>finish=done):{resources:[first],nextPageToken:"tail"});
 await screen.findByRole("button",{name:/Personal/});const anchor=f.container.querySelector(".sidebar-continuation");const more=screen.getByRole("button",{name:/Load more Subscriptions/});
 await act(async()=>{await new Promise(done=>setTimeout(done,0));}); refreshing=true;fireEvent(windowThis(),new Event("focus"));await waitFor(()=>expect(more.hasAttribute("disabled")).toBe(true));
 expect(f.container.querySelector(".sidebar-continuation")).toBe(anchor);expect(anchor?.querySelector('[role="status"]')).toBeNull();expect(f.requests.mock.calls.map(call=>call[0])).toEqual(["",""]);
 await act(async()=>finish({resources:[first],nextPageToken:"renewed"}));await waitFor(()=>expect(more.hasAttribute("disabled")).toBe(false));
});
it("restores navigation fallback when an accepted refresh removes the popover opener",async()=>{
 const first=resource("Personal");let removed=false;const f=mount(()=>({resources:removed?[]:[first]}));
 fireEvent.click(await screen.findByRole("button",{name:/Personal/}));removed=true;fireEvent(windowThis(),new Event("focus"));
 await waitFor(()=>expect(screen.queryByRole("dialog")).toBeNull());expect(f.focusFallback).toHaveBeenCalledOnce();expect(screen.queryByRole("button",{name:"Reload list"})).toBeNull();
});
it("keeps explicit restart at the accepted 200-page limit",async()=>{
 const first=resource("Personal");const f=mount(token=>({resources:token?[]:[first],nextPageToken:String(token?Number(token)+1:1)}));
 await screen.findByRole("button",{name:/Personal/});
 for(let page=1;page<200;page++){fireEvent.click(screen.getByRole("button",{name:/Load more Subscriptions/}));await waitFor(()=>expect(f.requests).toHaveBeenCalledTimes(page+1));if(page<199)await waitFor(()=>expect(screen.getByRole("button",{name:/Load more Subscriptions/}).hasAttribute("disabled")).toBe(false));}
 const reload=await screen.findByRole("button",{name:"Reload list"});expect(screen.queryByRole("button",{name:/Load more Subscriptions/})).toBeNull();fireEvent.click(reload);
 await waitFor(()=>expect(f.requests).toHaveBeenCalledTimes(201));expect(f.requests.mock.calls.at(-1)?.[0]).toBe("");
},30000);
