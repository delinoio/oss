import { create } from "@bufbuild/protobuf";
import { StrictMode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { BrowserService, BrowserCapability, BrowserProfileSchema, BrowserProfileState, EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { MutationIntents } from "./mutation";
import { SessionBrowser, browserProfile } from "./session-browser";
const native = vi.hoisted(() => vi.fn());
vi.mock("@tauri-apps/api/core", () => ({ invoke: native }));
beforeEach(() => {
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ x: 100, y: 100, left: 100, top: 100, width: 300, height: 200, right: 400, bottom: 300, toJSON: () => ({}) });
  native.mockReset();
});
function fixture() {
  const accountId = newRequestId(), profileId = newRequestId(), tabId = newRequestId();
  const profile = create(BrowserProfileSchema, {id:profileId,revision:1n,serverId:newRequestId(),deviceId:newRequestId(),accountId,state:BrowserProfileState.ACTIVE});
  const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, revision: 7n });
  const register = vi.fn(async (_request: unknown) => ({ profile }));
  const transport = createRouterTransport((router) => router.service(BrowserService, { registerBrowserProfile: register, getBrowserCapabilities:()=>({capabilities:[BrowserCapability.PROTECTED_DEVICE_PROFILE_V1]}) }));
  const local = { tabs: { tabs: [{ id: tabId, url: "https://fixture.test/page" }], selected: tabId }, removal_pending: false };
  native.mockResolvedValue(local);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function View() { return <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionBrowser session={session} accountId={accountId} close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>; }
  return { accountId, profileId, tabId, profile, session, register, local, View };
}
async function open() {
  const button = screen.getByRole("button", { name: "Open account browser" });
  expect((button as HTMLButtonElement).disabled).toBe(true);
  fireEvent.change(screen.getByRole("textbox", { name: "Address" }), { target: { value: "https://fixture.test/page" } });
  await waitFor(()=>expect((button as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(button);
}
it("registers only metadata and preserves the original uncertain request without automatic retry", async () => {
 const f=fixture();f.register.mockRejectedValueOnce(new ConnectError("uncertain", Code.Unavailable));render(<f.View />);await open();
 await screen.findByRole("button",{name:"Retry the same registration"});expect(f.register).toHaveBeenCalledTimes(1);expect(native).not.toHaveBeenCalled();
 const original=f.register.mock.calls[0][0];fireEvent.click(screen.getByRole("button",{name:"Retry the same registration"}));
 await waitFor(()=>expect(native).toHaveBeenCalledWith("open_browser",expect.objectContaining({profileId:f.profileId,url:"https://fixture.test/page"})));
 expect(f.register.mock.calls[1][0]).toEqual(original);expect(original).toMatchObject({accountId:f.accountId,session:{id:f.session.id,expectedRevision:7n}});expect(original).not.toHaveProperty("url");
});
it("uses distinct native presentation identities and never gives external content a DOM frame", async()=>{
 const f=fixture();const view=render(<StrictMode><f.View /></StrictMode>);await open();await waitFor(()=>expect(native.mock.calls.some(([op])=>op==="open_browser")).toBe(true));
 const first=native.mock.calls.find(([op])=>op==="open_browser")![1];fireEvent.click(screen.getByRole("button",{name:"Retry native view"}));
 await waitFor(()=>expect(native.mock.calls.filter(([op])=>op==="open_browser")).toHaveLength(2));const second=native.mock.calls.filter(([op])=>op==="open_browser")[1][1];
 expect(first.viewId).not.toBe(second.viewId);expect(native.mock.calls.some(([op,args])=>op==="control_browser"&&args.action==="hide"&&args.viewId===first.viewId)).toBe(true);
 expect(view.container.querySelector("iframe,webview,script,a")).toBeNull();view.unmount();await waitFor(()=>expect(native.mock.calls.some(([op,args])=>op==="control_browser"&&args.action==="hide"&&args.viewId===second.viewId)).toBe(true));
});
it("rejects deleted or foreign profiles before native presentation",async()=>{
 const f=fixture();f.profile.accountId=newRequestId();render(<f.View />);await open();await screen.findByText("Browser profile ownership is unavailable. Refresh the session.");expect(native).not.toHaveBeenCalled();
 expect(()=>browserProfile({...f.profile,accountId:f.accountId,state:BrowserProfileState.REMOVAL_PENDING,deletionRequestId:newRequestId()},f.accountId)).toThrow();
});
it("retries identical geometry after a failed native resize and caches only success", async () => {
  const f = fixture();
  let top = 100, attempts = 0;
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(() => ({ x: 100, y: top, left: 100, top, width: 300, height: 200, right: 400, bottom: top + 200, toJSON: () => ({}) }));
  native.mockImplementation(async (_operation, args) => {
    if (args.action === "resize" && ++attempts === 1) throw new Error("transient native resize failure");
    return f.local;
  });
  render(<f.View />);
  await open();
  await screen.findByRole("button", { name: /Tab 1/ });
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
it("closes only the earlier instance when its native open resolves after replacement",async()=>{
 const f=fixture();let finish!:(value:typeof f.local)=>void;native.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve}));render(<f.View />);await open();await waitFor(()=>expect(native).toHaveBeenCalledTimes(1));const first=native.mock.calls[0][1];
 fireEvent.click(screen.getByRole("button",{name:"Retry native view"}));await waitFor(()=>expect(native.mock.calls.filter(([op])=>op==="open_browser")).toHaveLength(2));const second=native.mock.calls.filter(([op])=>op==="open_browser")[1][1];
 await act(async()=>finish(f.local));await waitFor(()=>expect(native.mock.calls.some(([op,args])=>op==="control_browser"&&args.action==="hide"&&args.viewId===first.viewId)).toBe(true));
 expect(native.mock.calls.some(([op,args])=>op==="control_browser"&&args.action==="hide"&&args.viewId===second.viewId)).toBe(false);
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
