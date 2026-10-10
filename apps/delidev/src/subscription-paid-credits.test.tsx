// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen } from "@testing-library/react";
import { describe,it,expect } from "vitest";
import { i18n, SupportedLanguage } from "./localization";
import { formatPaidCreditBalance } from "./paid-credit-format";
import { PaidCredits,PaidCreditState,paidCreditProjection } from "./subscription-paid-credits";
const at="2026-10-09T00:00:00Z",now=Date.parse(at),bucket={id:"codex",hasCredits:true,unlimited:false,balance:"1250.50000000000000001",observedAt:at};
describe("paid-credit projection",()=>{
 it("preserves decimal strings, null and bucket identity",()=>{
  const parsed=paidCreditProjection({paid_credits:[{id:"codex",has_credits:true,unlimited:false,balance:bucket.balance,observed_at:at},{id:"another",has_credits:false,unlimited:true,balance:null,observed_at:at}]});
  expect(parsed.map(v=>v.balance)).toEqual([bucket.balance,null]);expect(parsed.map(v=>v.id)).toEqual(["codex","another"]);
 });
 it.each(["-1","1e4","private", "1.", "1".repeat(65),undefined])("refuses malformed values %s",balance=>{expect(paidCreditProjection({paid_credits:[{id:"codex",has_credits:true,unlimited:false,balance,observed_at:at}]})).toEqual([])});
 it("shows rounded balances with exact disclosure and no purchase controls",()=>{render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now}/>);expect(screen.getByText("1,250.50")).toBeTruthy();fireEvent.focus(screen.getByRole("button",{name:"Exact balance"}));expect(screen.getByRole("tooltip").textContent).toContain(bucket.balance);expect(screen.getAllByRole("button")).toHaveLength(1);expect(screen.getByText("codex")).toBeTruthy()});
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


it.each([["60961.1135370000","60,961.11"],["1.995","2.00"],["0","0.00"],["0.004","<0.01"],["9.999","10.00"]])("formats exact decimal %s as %s",(value,expected)=>{expect(formatPaidCreditBalance(value,"en")).toBe(expected);expect(formatPaidCreditBalance(value,"ko")).toBe(expected);});
it("formats 64-character integers without floating-point precision loss",()=>{const value="9".repeat(64);expect(formatPaidCreditBalance(value,"en")).toBe(new Intl.NumberFormat("en").format(BigInt(value))+".00");});
it("supports click, focus, Escape and original exact string in both locales",async()=>{
 for(const language of [SupportedLanguage.English,SupportedLanguage.Korean]){
  await i18n.changeLanguage(language);const view=render(<PaidCredits compact buckets={[{...bucket,balance:"60961.1135370000"}]} state={PaidCreditState.Observed} now={now}/>);
  const info=screen.getByRole("button",{name:language===SupportedLanguage.English?"Exact balance":"정확한 잔액"});
  fireEvent.click(info);expect(screen.getByRole("tooltip").textContent).toContain("60961.1135370000");fireEvent.keyDown(info,{key:"Escape"});expect(screen.queryByRole("tooltip")).toBeNull();expect(document.activeElement).toBe(info);
  fireEvent.mouseEnter(info.parentElement!);expect(screen.getByRole("tooltip")).toBeTruthy();fireEvent.blur(info,{relatedTarget:document.body});expect(screen.queryByRole("tooltip")).toBeNull();view.unmount();
 }
 await i18n.changeLanguage(SupportedLanguage.English);
});
it("retains original timestamps at the five-minute boundary and flags future observations",()=>{
 const view=render(<PaidCredits compact buckets={[bucket]} state={PaidCreditState.Observed} now={now+300000}/>);expect(screen.queryByText(/Stale observation/)).toBeNull();
 view.rerender(<PaidCredits compact buckets={[bucket]} state={PaidCreditState.Observed} now={now+300001}/>);expect(screen.getByText(/Stale observation/)).toBeTruthy();
 view.rerender(<PaidCredits compact buckets={[{...bucket,observedAt:new Date(now+1).toISOString()}]} state={PaidCreditState.Observed} now={now}/>);expect(screen.getByText(/Stale observation/)).toBeTruthy();
});
