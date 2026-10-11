// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe,it,expect } from "vitest";
import { i18n, SupportedLanguage } from "./localization";
import { PaidCredits,PaidCreditState,paidCreditProjection } from "./subscription-paid-credits";
const at="2026-10-09T00:00:00Z",now=Date.parse(at),bucket={id:"codex",hasCredits:true,unlimited:false,balance:"1250.50000000000000001",observedAt:at};
describe("paid-credit projection",()=>{
 it("preserves decimal strings, null and bucket identity",()=>{
  const parsed=paidCreditProjection({paid_credits:[{id:"codex",has_credits:true,unlimited:false,balance:bucket.balance,observed_at:at},{id:"another",has_credits:false,unlimited:true,balance:null,observed_at:at}]});
  expect(parsed.map(v=>v.balance)).toEqual([bucket.balance,null]);expect(parsed.map(v=>v.id)).toEqual(["codex","another"]);
 });
 it.each(["-1","1e4","private", "1.", "1".repeat(65),undefined])("refuses malformed values %s",balance=>{expect(paidCreditProjection({paid_credits:[{id:"codex",has_credits:true,unlimited:false,balance,observed_at:at}]})).toEqual([])});
 it("shows rounded balances with exact disclosure and no purchase controls",()=>{render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now}/>);expect(screen.getByText("1,250.50")).toBeTruthy();fireEvent.focus(screen.getByRole("button",{name:"Show exact credit balance"}));expect(screen.getByRole("tooltip").textContent).toContain(bucket.balance);expect(screen.queryAllByRole("button")).toHaveLength(1);expect(screen.getByText("codex")).toBeTruthy()});
 it("keeps zero, null and unlimited distinct and never sums buckets",()=>{render(<PaidCredits buckets={[{...bucket,id:"zero",balance:"0"},{...bucket,id:"null",balance:null},{...bucket,id:"unlimited",unlimited:true,balance:"15"}]} state={PaidCreditState.Observed} now={now}/>);expect(screen.getByText("0.00")).toBeTruthy();expect(screen.getByText("Balance unknown")).toBeTruthy();expect(screen.getByText("Unlimited credits")).toBeTruthy();expect(screen.queryByText("15 credits")).toBeNull()});
 it("retains values with truthful failure and stale labels",()=>{const v=render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Failed} now={now+600000}/>);expect(screen.getByText("1,250.50")).toBeTruthy();expect(screen.getByText(/Refresh failed; retained observation/)).toBeTruthy();v.rerender(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now+600000}/>);expect(screen.getByText(/Stale observation/)).toBeTruthy()});
 it("shows a count for multiple compact balances and no aggregate",()=>{render(<PaidCredits buckets={[bucket,{...bucket,id:"another",balance:"2"}]} state={PaidCreditState.Observed} compact now={now}/>);expect(screen.getByText(/2 credit balances/)).toBeTruthy();expect(screen.queryByText(bucket.balance+" credits")).toBeNull()});
 it("support states cannot fabricate zero or send a request",()=>{const v=render(<PaidCredits state={PaidCreditState.Unsupported}/>);expect(screen.getByRole("status").textContent).toContain("Update the server");v.rerender(<PaidCredits state={PaidCreditState.Loading}/>);expect(screen.getByRole("status").textContent).toContain("Loading");expect(screen.queryAllByRole("button")).toHaveLength(0)});
});


it.each([false, true])("shows initial refresh failure without inventing retained evidence (compact %s)", async compact => {
 const v=render(<PaidCredits state={PaidCreditState.Failed} compact={compact}/>);
 expect(screen.getByText(/Refresh failed; no balance observed/)).toBeTruthy();
 expect(screen.queryByText(/Balance unknown|retained observation/)).toBeNull();
 await i18n.changeLanguage(SupportedLanguage.Korean);
 expect(screen.getByText(/새로고침 실패; 관측된 잔액 없음/)).toBeTruthy();
 expect(screen.queryByText(/잔액 알 수 없음|기존 관측 유지/)).toBeNull();
 await i18n.changeLanguage(SupportedLanguage.English);
 v.rerender(<PaidCredits state={PaidCreditState.Unknown} compact={compact}/>);
 expect(screen.getByText(/Balance unknown/)).toBeTruthy();
 expect(screen.queryByText(/Refresh failed/)).toBeNull();
});

it("opens exact evidence through hover, focus and touch-compatible activation, then consumes Escape", () => {
 const outer=()=>{throw new Error("Escape escaped the disclosure");};
 const view=render(<div onKeyDown={outer}><PaidCredits buckets={[{...bucket,balance:"60961.1135370000"}]} state={PaidCreditState.Observed} compact now={now}/></div>);
 const button=screen.getByRole("button",{name:"Show exact credit balance"});
 expect(screen.getByText("60,961.11")).toBeTruthy();expect(screen.queryByRole("tooltip")).toBeNull();
 fireEvent.mouseEnter(button.parentElement!);expect(screen.getByRole("tooltip").textContent).toContain("60961.1135370000");
 fireEvent.mouseLeave(button.parentElement!);expect(screen.queryByRole("tooltip")).toBeNull();
 act(()=>button.focus());expect(screen.getByRole("tooltip")).toBeTruthy();
 fireEvent.keyDown(button,{key:"Escape"});expect(screen.queryByRole("tooltip")).toBeNull();expect(document.activeElement).toBe(button);
 fireEvent.click(button);expect(screen.getByRole("tooltip")).toBeTruthy();
 fireEvent.mouseLeave(button.parentElement!);expect(screen.getByRole("tooltip")).toBeTruthy();
 fireEvent.keyDown(button,{key:"Escape"});expect(screen.queryByRole("tooltip")).toBeNull();
 view.unmount();
});

it("retains the exact 64-character value and separate source-ordered bucket balances", () => {
 const balance="9".repeat(61)+".99";
 render(<PaidCredits buckets={[{...bucket,id:"first".repeat(20),balance},{...bucket,id:"second",balance:"1.995"}]} state={PaidCreditState.Observed} now={now}/>);
 expect(screen.getAllByRole("listitem").map(row=>row.querySelector("strong")?.textContent)).toEqual(["first".repeat(20),"second"]);
 fireEvent.focus(screen.getAllByRole("button")[0]);expect(screen.getByRole("tooltip").textContent).toContain(balance);
 expect(screen.getByText("2.00")).toBeTruthy();
});

it("distinguishes a tiny positive value from zero without changing exact evidence", () => {
 const view=render(<PaidCredits buckets={[{...bucket,balance:"0.004"}]} state={PaidCreditState.Observed} now={now}/>);
 expect(screen.getByText("<0.01")).toBeTruthy();fireEvent.focus(screen.getByRole("button"));expect(screen.getByRole("tooltip").textContent).toContain("0.004");
 view.rerender(<PaidCredits buckets={[{...bucket,balance:"0"}]} state={PaidCreditState.Observed} now={now}/>);
 expect(screen.getByText("0.00")).toBeTruthy();expect(screen.queryByRole("tooltip")).toBeNull();
});

it("keeps the exact freshness boundary, future dates and retained read failure truthful", () => {
 const view=render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} compact now={now+300000}/>);
 expect(screen.queryByText(/Stale observation/)).toBeNull();
 view.rerender(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} compact now={now+300001}/>);
 expect(screen.getByText(/Stale observation/)).toBeTruthy();
 view.rerender(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} compact now={now-1}/>);
 expect(screen.getByText(/Stale observation/)).toBeTruthy();
 view.rerender(<PaidCredits buckets={[bucket]} state={PaidCreditState.Failed} compact now={now}/>);
 expect(screen.getByText("1,250.50")).toBeTruthy();expect(screen.getByText(/Refresh failed; retained observation/)).toBeTruthy();
});
