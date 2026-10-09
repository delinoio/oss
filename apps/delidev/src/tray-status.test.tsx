// SPDX-License-Identifier: Apache-2.0
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { i18n, SupportedLanguage } from "./localization";
import { DateFormatPreference } from "./timestamp-format";
import { TrayQuotaState, TraySubscriptionService } from "./tray-types";
import { TrayPanelAction, parseTrayPanel, type TrayPanelSnapshot } from "./tray-status-model";
import { TrayStatus, type TrayPanelBridge } from "./tray-status";
const instance="11111111-1111-4111-8111-111111111111", first="22222222-2222-4222-8222-222222222222", second="33333333-3333-4333-8333-333333333333", scope="44444444-4444-4444-8444-444444444444";
function fixture(): TrayPanelSnapshot {
  const observed_at = new Date(Date.now() - 60000).toISOString();
  return { instance, more:false, recent:first, theme:"light", language:SupportedLanguage.English, date_format:DateFormatPreference.Ymd,
    windows:[{ target:{label:"main",instance:first,scope,revision:7},name:"This computer · Window 1",stale:false,observed_age_ms:0,summary:{ overview:{observed_at,stale:false,active_sessions:"3",pending_interactions:"2",connected_workers:"1",registered_workers:"2"},usage:{known_tokens:"90071992547409930000",incomplete:true,estimates:[{currency:"USD",known_amount:"0.000000001"},{currency:"KRW",known_amount:"123.000"}]},accounts:{more:false,entries:[{alias:"Personal",subscription_service:TraySubscriptionService.ChatGPT,more:false,windows:[{id:"codex:primary",state:TrayQuotaState.Observed,remaining_basis_points:5700,observed_at,reset_at:new Date(Date.now()+3600000).toISOString()},{state:TrayQuotaState.Stale,remaining_basis_points:2401,observed_at,reset_at:null}]}]}}},
      {target:{label:"local-second",instance:second,scope,revision:1},name:"This computer · Window 2",stale:true,observed_age_ms:46000,summary:null}] };
}
function controller(initial=fixture()) {
  let current=initial, changed=()=>{};
  const bridge: TrayPanelBridge = { read:vi.fn(async()=>current), subscribe:vi.fn(async callback=>{changed=callback;return vi.fn();}),activate:vi.fn(async()=>{}),dismiss:vi.fn(async()=>{}) };
  return {bridge,update:(snapshot:TrayPanelSnapshot)=>{current=snapshot;changed();}};
}
afterEach(async()=>{cleanup();await i18n.changeLanguage("en");});
describe("isolated tray panel",()=>{
  it("shows independent quotas before exact metrics, focuses the selector and only navigates",async()=>{
    const {bridge}=controller();render(<TrayStatus instance={instance} bridge={bridge}/>);
    await screen.findByText("57.00% remaining");
    expect(screen.getByText("24.01% remaining")).toBeTruthy();
    expect(screen.getByText("codex:primary")).toBeTruthy();
    expect(screen.getByText("Quota 2")).toBeTruthy();
    expect(screen.getByText("90,071,992,547,409,930,000")).toBeTruthy();
    expect(screen.getByRole("combobox")).toBe(document.activeElement);
    expect(screen.getByText("Usage details").closest("details")?.open).toBe(false);
    expect(screen.getByText("Account quotas").compareDocumentPosition(screen.getByText("Active sessions")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.click(screen.getByRole("button",{name:"Manage subscriptions"}));
    await waitFor(()=>expect(bridge.activate).toHaveBeenCalledWith(TrayPanelAction.Settings,fixture().windows[0].target));
    expect(bridge.read).toHaveBeenCalledTimes(1);
  });
  it("selection changes only projection, persists across snapshots and rejects disappeared identity",async()=>{
    const {bridge,update}=controller();render(<TrayStatus instance={instance} bridge={bridge}/>);
    await screen.findByText("57.00% remaining");
    fireEvent.change(screen.getByRole("combobox"),{target:{value:second}});
    expect(screen.queryByText("57.00% remaining")).toBeNull();
    expect(bridge.activate).not.toHaveBeenCalled();expect(bridge.read).toHaveBeenCalledTimes(1);
    const next=fixture();next.recent=first;update(next);
    await waitFor(()=>expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe(second));
    next.windows.splice(1,1);update({...next});
    await waitFor(()=>expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe(first));
  });
  it("retains quota state, supports locale updates, and Escape hides without activating product windows",async()=>{
    const {bridge,update}=controller();render(<TrayStatus instance={instance} bridge={bridge}/>);
    await screen.findByText("57.00% remaining");
    const next=fixture();next.language=SupportedLanguage.Korean;next.theme="dark";next.windows[0].stale=true;next.windows[0].summary!.accounts!.entries[0].windows[0].state=TrayQuotaState.Failed;update(next);
    await screen.findByText("관측 실패");expect(screen.queryByText("57.00% 남음")).toBeNull();expect(document.documentElement.dataset.theme).toBe("dark");
    fireEvent.keyDown(screen.getByRole("main"),{key:"Escape"});
    await waitFor(()=>expect(bridge.dismiss).toHaveBeenCalledOnce());expect(bridge.activate).not.toHaveBeenCalled();
  });
  it("keeps empty accounts distinct from unavailable observations and preserves recovery",async()=>{
    const snapshot=fixture();snapshot.windows[0].summary!.accounts={entries:[],more:false};
    const {bridge,update}=controller(snapshot);render(<TrayStatus instance={instance} bridge={bridge}/>);
    await screen.findByText("No accounts in this observation.");
    update({...snapshot,windows:[],recent:null});await screen.findByText("No ready product window");
    fireEvent.click(screen.getByRole("button",{name:"Open DeliDev"}));
    await waitFor(()=>expect(bridge.activate).toHaveBeenCalledWith(TrayPanelAction.Recovery,undefined));
  });
});
it("rejects foreign instances, authority fields, oversized data, duplicates and impossible quota",()=>{
  const valid=fixture();expect(parseTrayPanel(valid,instance)).toBe(valid);
  expect(()=>parseTrayPanel({...valid,endpoint:"https://private.test"},instance)).toThrow();
  expect(()=>parseTrayPanel(valid,second)).toThrow();
  expect(()=>parseTrayPanel({...valid,windows:[...valid.windows,valid.windows[0]]},instance)).toThrow();
  const bad=fixture();bad.windows[0].summary!.accounts!.entries[0].windows[0].remaining_basis_points=10001;
  expect(()=>parseTrayPanel(bad,instance)).toThrow();
});
