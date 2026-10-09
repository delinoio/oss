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
import { BrowserHostProvider } from "./host-capabilities";
const native = vi.hoisted(() => vi.fn());
vi.mock("@tauri-apps/api/core", () => ({ invoke: native }));
beforeEach(() => {
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ x: 100, y: 100, left: 100, top: 100, width: 300, height: 200, right: 400, bottom: 300, toJSON: () => ({}) });
  native.mockReset();
});
it("an unavailable browser host does not register a profile or call native code", () => {
  const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, revision: 1n });
  render(<BrowserHostProvider available={false}><SessionBrowser session={session} accountId={newRequestId()} close={() => {}} /></BrowserHostProvider>);
  expect(screen.getByText("CEF browser profiles require the supported desktop host.")).toBeTruthy();
  expect(native).not.toHaveBeenCalled();
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
function retryView() {
  const summary = screen.getByLabelText("More browser actions");
  if (!(summary.closest("details") as HTMLDetailsElement).open) fireEvent.click(summary);
  fireEvent.click(screen.getByRole("button", { name: "Retry native view" }));
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
 const first=native.mock.calls.find(([op])=>op==="open_browser")![1];retryView();
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
it("surfaces a delayed native child failure and reopens only after explicit retry", async () => {
  const f = fixture();
  native.mockImplementation(async (operation) => {
    if (operation === "browser_state") throw new Error("sidecar-failed");
    return f.local;
  });
  render(<f.View />);
  await open();
  await screen.findByRole("button", { name: /Tab 1/ });
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
  native.mockImplementation(async (_operation, args) => {
    if (args.action === "hide") {
      if (++attempts === 1) throw new Error("busy");
      if (attempts === 2) return new Promise(resolve => { finish = resolve; });
    }
    return f.local;
  });
  const view = render(<f.View />);
  await open();
  await screen.findByRole("button", { name: /Tab 1/ });
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
  native.mockImplementation(async (_operation, args) => {
    if (args.action === "hide" && ++attempts === 1) throw new Error("busy");
    return f.local;
  });
  const view = render(<f.View />);
  await open();
  await screen.findByRole("button", { name: /Tab 1/ });
  const original = native.mock.calls.find(([op]) => op === "open_browser")![1];
  view.unmount();
  await waitFor(() => expect(attempts).toBe(2));
  expect(native.mock.calls.filter(([, args]) => args.action === "hide").every(([, args]) => args.viewId === original.viewId)).toBe(true);
  await new Promise(resolve => setTimeout(resolve, 300));
  expect(attempts).toBe(2);
  expect(native.mock.calls.filter(([op]) => op === "open_browser")).toHaveLength(1);
});
it("closes only the earlier instance when its native open resolves after replacement",async()=>{
 const f=fixture();let finish!:(value:typeof f.local)=>void;native.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve}));render(<f.View />);await open();await waitFor(()=>expect(native).toHaveBeenCalledTimes(1));const first=native.mock.calls[0][1];
 retryView();await waitFor(()=>expect(native.mock.calls.filter(([op])=>op==="open_browser")).toHaveLength(2));const second=native.mock.calls.filter(([op])=>op==="open_browser")[1][1];
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

it("rechecks failed browser capabilities locally without registration or clearing the address", async () => {
 const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, schemaVersion: 1, revision: 7n });
 const capabilities = vi.fn().mockRejectedValueOnce(new ConnectError("private-native-capability", Code.PermissionDenied)).mockResolvedValue({ capabilities: [] });
 const register = vi.fn();
 const transport = createRouterTransport(router => router.service(BrowserService, { getBrowserCapabilities: capabilities, registerBrowserProfile: register }));
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionBrowser session={session} accountId={newRequestId()} close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
 const address = screen.getByRole("textbox", { name: "Address" });
 fireEvent.change(address, { target: { value: "https://fixture.test/original" } });
 fireEvent.click(await screen.findByRole("button", { name: "Retry browser capability read" }));
 await waitFor(() => expect(capabilities).toHaveBeenCalledTimes(2));
 expect(address).toHaveProperty("value", "https://fixture.test/original");
 expect(register).not.toHaveBeenCalled(); expect(native).not.toHaveBeenCalled();
});


it("opens an information modal through the original native hide owner without registering again", async () => {
  const f = fixture(); render(<f.View />); await open();
  await screen.findByRole("button", { name: /Tab 1/ });
  const original = native.mock.calls.find(([operation]) => operation === "open_browser")![1];
  const information = screen.getByRole("button", { name: "Browser information" });
  information.focus(); fireEvent.click(information);
  const dialog = screen.getByRole("dialog", { name: "Browser information" });
  expect(dialog.textContent).toContain("Tabs, cookies, history and browser credentials stay local.");
  await waitFor(() => expect(native).toHaveBeenCalledWith("control_browser", expect.objectContaining({ action: "hide", viewId: original.viewId })));
  fireEvent.click(screen.getByRole("button", { name: "Close Browser information" }));
  await waitFor(() => expect(native.mock.calls.filter(([operation]) => operation === "open_browser")).toHaveLength(2));
  expect(f.register).toHaveBeenCalledTimes(1); expect(document.activeElement).toBe(information);
  expect(native.mock.calls.some(([, args]) => args.action === "navigate")).toBe(false);
});

it("keeps local tab and navigation commands on the original profile/view and enforces the 16-tab cap", async () => {
  const f = fixture();
  for (let n = 1; n < 16; n++) f.local.tabs.tabs.push({ id: newRequestId(), url: `https://fixture.test/${n}` });
  render(<f.View />); await open(); await screen.findByRole("button", { name: /Tab 16/ });
  const original = native.mock.calls.find(([operation]) => operation === "open_browser")![1];
  expect(screen.getByRole("button", { name: "New tab" })).toHaveProperty("disabled", true);
  for (const name of ["Back", "Forward", "Reload"]) { fireEvent.click(screen.getByRole("button", { name })); await waitFor(() => expect(screen.getByRole("button", { name })).toHaveProperty("disabled", false)); }
  fireEvent.click(screen.getByRole("button", { name: /Tab 2 ·/ }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Go" })).toHaveProperty("disabled", false));
  fireEvent.click(screen.getByRole("button", { name: "Close tab 2" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Go" })).toHaveProperty("disabled", false));
  fireEvent.change(screen.getByRole("textbox", { name: "Address" }), { target: { value: "https://fixture.test/explicit" } });
  fireEvent.click(screen.getByRole("button", { name: "Go" }));
  await waitFor(() => expect(native).toHaveBeenCalledWith("control_browser", expect.objectContaining({ action: "navigate", profileId: f.profileId, viewId: original.viewId, url: "https://fixture.test/explicit" })));
  const controls = native.mock.calls.filter(([operation, args]) => operation === "control_browser" && !["resize", "hide"].includes(args.action));
  expect(controls.map(([, args]) => args.action)).toEqual(["back", "forward", "reload", "select-tab", "close-tab", "navigate"]);
  expect(controls.every(([, args]) => args.profileId === f.profileId && args.viewId === original.viewId)).toBe(true);
  expect(f.register).toHaveBeenCalledTimes(1);
});

it.each(["file:///fixture", "https://user:secret@fixture.test/"])("blocks invalid initial addresses without native authority: %s", async address => {
  const f = fixture(); render(<f.View />);
  fireEvent.change(screen.getByRole("textbox", { name: "Address" }), { target: { value: address } });
  await waitFor(() => expect(screen.getByRole("button", { name: "Open account browser" })).toHaveProperty("disabled", true));
  expect(document.querySelector(".browser-viewport")).toBeNull(); expect(f.register).not.toHaveBeenCalled(); expect(native).not.toHaveBeenCalled();
});

it("fences a retained Browser under the maximized upper inert region through exact Hide uncertainty", async () => {
  const f = fixture(); let attempts = 0, finish!: (value: typeof f.local) => void;
  native.mockImplementation(async (_operation, args) => {
    if (args.action === "hide") {
      if (++attempts === 1) throw new Error("busy");
      if (attempts === 2) return new Promise(resolve => { finish = resolve; });
    }
    return f.local;
  });
  const view = render(<div data-upper><f.View /></div>), upper = view.container.querySelector("[data-upper]")!;
  await open();await screen.findByRole("button", { name: /Tab 1/ });
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
    expect(f.register).toHaveBeenCalledTimes(1);
    expect(native.mock.calls.filter(([operation]) => operation === "open_browser")[1][1].profileId).toBe(original.profileId);
  } finally { view.unmount(); }
});
