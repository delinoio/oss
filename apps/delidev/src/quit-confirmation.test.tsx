// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SystemService } from "@delinoio/delidev-api-client";
import { QuitConfirmation, validQuitCount, type QuitAttempt } from "./quit-confirmation";
const native=vi.hoisted(()=>({view:null as QuitAttempt|null,invoke:vi.fn(),event:()=>{}}));
vi.mock("@tauri-apps/api/core",()=>({isTauri:()=>true,invoke:native.invoke}));
vi.mock("@tauri-apps/api/event",()=>({listen:async(_event:string,callback:()=>void)=>{native.event=callback;return()=>{};}}));
function fixture(){native.view={id:"12345678-1234-7234-8234-123456789012",checking:true,unknown:false,count:"0",present:true,observe:true};native.invoke.mockReset().mockImplementation(async(method:string,input?:unknown)=>{if(method==="read_quit_attempt")return native.view;if(method==="decide_quit_attempt")native.view=null;return undefined;});const read=vi.fn(async()=>({activeSessions:9007199254740993n,observedAt:new Date().toISOString()}));const transport=createRouterTransport(router=>router.service(SystemService,{getOverview:read}));return {read,view:(ready=true)=><TransportProvider transport={transport}><QuitConfirmation ready={ready} transport={transport}/></TransportProvider>};}
it("rejects noncanonical or overflowing aggregate count metadata",()=>{for(const value of ["01","-1","1.0","340282366920938463463374607431768211456",7])expect(validQuitCount(value)).toBe(false);expect(validQuitCount("9007199254740993")).toBe(true);});
it("keeps Cancel available before cleanup and reports exact fresh ownership without native-state claims",async()=>{const f=fixture();render(f.view());await screen.findByText("Checking session status…");await waitFor(()=>expect(document.activeElement).toBe(screen.getByRole("button",{name:"Cancel"})));expect(screen.getByRole("button",{name:"Quit"})).toHaveProperty("disabled",true);await waitFor(()=>expect(native.invoke).toHaveBeenCalledWith("observe_quit_attempt",{id:native.view?.id,count:"9007199254740993"}));fireEvent.click(screen.getByRole("button",{name:"Cancel"}));await waitFor(()=>expect(screen.queryByRole("alertdialog")).toBeNull());expect(native.invoke.mock.calls.some(([method,input])=>method==="decide_quit_attempt" && (input as {decision:string}).decision==="confirm")).toBe(false);});
it("shows incomplete coverage and its verified partial count with original confirm identity",async()=>{const f=fixture();native.view={...native.view!,checking:false,observe:false,unknown:true,count:"3"};render(f.view());await screen.findByText("Session status could not be verified for some connections.");expect(screen.getByText("3 sessions have not finished.")).toBeTruthy();const id=native.view.id;fireEvent.click(screen.getByRole("button",{name:"Quit"}));await waitFor(()=>expect(native.invoke).toHaveBeenCalledWith("decide_quit_attempt",{id,decision:"confirm"}));expect(f.read).not.toHaveBeenCalled();});
it("publishes unknown rather than a cached zero when disconnected",async()=>{const f=fixture();render(f.view(false));await waitFor(()=>expect(native.invoke).toHaveBeenCalledWith("observe_quit_attempt",{id:native.view?.id,count:null}));expect(f.read).not.toHaveBeenCalled();});

it("presents and cancels an original disconnected attempt without any transport",async()=>{fixture();render(<QuitConfirmation ready={false}/>);await screen.findByText("Checking session status…");await waitFor(()=>expect(native.invoke).toHaveBeenCalledWith("observe_quit_attempt",{id:native.view?.id,count:null}));fireEvent.click(screen.getByRole("button",{name:"Cancel"}));await waitFor(()=>expect(screen.queryByRole("alertdialog")).toBeNull());});

it("drops a retired observation before publication and preserves its successor",async()=>{
 const f=fixture();const old=native.view!.id;
 let finish!:(value:{activeSessions:bigint;observedAt:string})=>void;
 f.read.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve;}));
 render(f.view());await waitFor(()=>expect(f.read).toHaveBeenCalledTimes(1));
 fireEvent.click(await screen.findByRole("button",{name:"Cancel"}));
 await waitFor(()=>expect(screen.queryByRole("alertdialog")).toBeNull());
 const next="12345678-1234-7234-8234-123456789013";
 native.view={id:next,checking:false,unknown:true,count:"0",present:true,observe:false};
 await act(async()=>native.event());await screen.findByRole("alertdialog");
 await act(async()=>finish({activeSessions:0n,observedAt:new Date().toISOString()}));
 expect(native.invoke.mock.calls.some(([name,input])=>name==="observe_quit_attempt"&&(input as {id:string}).id===old)).toBe(false);
 expect(screen.queryByRole("alert")).toBeNull();
});
it("ignores an old failed native read after a newer attempt succeeds",async()=>{
 const f=fixture();let reject!:(error:Error)=>void;
 native.invoke.mockImplementationOnce(()=>new Promise((_resolve,fail)=>{reject=fail;}));
 render(f.view());await waitFor(()=>expect(reject).toBeTypeOf("function"));
 await act(async()=>native.event());await screen.findByRole("alertdialog");
 await act(async()=>reject(new Error("retired read")));
 expect(screen.queryByRole("alert")).toBeNull();
});

it("includes the error disclosure in the dialog keyboard cycle",async()=>{
 const f=fixture();native.view={...native.view!,checking:false,unknown:true};
 const original=native.invoke.getMockImplementation()!;
 native.invoke.mockImplementation(async(method:string,input?:unknown)=>{if(method==="decide_quit_attempt")throw new Error("fixture failure");return original(method,input);});
 render(f.view());fireEvent.click(await screen.findByRole("button",{name:"Cancel"}));await screen.findByRole("alert");
 const dialog=screen.getByRole("alertdialog"),summary=dialog.querySelector("summary")!;
 const cancel=screen.getByRole("button",{name:"Cancel"}),quit=screen.getByRole("button",{name:"Quit"});
 for(const control of [summary,cancel,quit])vi.spyOn(control,"getClientRects").mockReturnValue({length:1} as DOMRectList);
 cancel.focus();expect(fireEvent.keyDown(dialog,{key:"Tab",shiftKey:true})).toBe(true);
 summary.focus();expect(fireEvent.keyDown(dialog,{key:"Tab",shiftKey:true})).toBe(false);expect(document.activeElement).toBe(quit);
 quit.focus();expect(fireEvent.keyDown(dialog,{key:"Tab"})).toBe(false);expect(document.activeElement).toBe(summary);
});

it("keeps native reads idle outside the original presented attempt",async()=>{
 fixture();native.view=null;vi.useFakeTimers();
 try {
  render(<QuitConfirmation ready={false}/>);
  await act(async()=>{await vi.advanceTimersByTimeAsync(1);});
  const reads=()=>native.invoke.mock.calls.filter(([method])=>method==="read_quit_attempt").length;
  expect(reads()).toBe(1);
  await act(async()=>{await vi.advanceTimersByTimeAsync(10000);});expect(reads()).toBe(1);
  native.view={id:"12345678-1234-7234-8234-123456789012",checking:false,unknown:true,count:"0",present:true,observe:false};
  await act(async()=>native.event());expect(reads()).toBe(2);
  await act(async()=>{await vi.advanceTimersByTimeAsync(2000);});expect(reads()).toBe(4);
  fireEvent.click(screen.getByRole("button",{name:"Cancel"}));await act(async()=>{});
  const retired=reads();await act(async()=>{await vi.advanceTimersByTimeAsync(10000);});expect(reads()).toBe(retired);
 } finally {vi.useRealTimers();}
});
