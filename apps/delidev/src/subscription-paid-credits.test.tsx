// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { describe,it,expect,vi } from "vitest";
import { i18n, SupportedLanguage } from "./localization";
import { PaidCredits,PaidCreditState,paidCreditProjection,formatPaidCreditBalance } from "./subscription-paid-credits";
const at="2026-10-09T00:00:00Z",now=Date.parse(at),bucket={id:"codex",hasCredits:true,unlimited:false,balance:"1250.50000000000000001",observedAt:at};
describe("paid-credit projection",()=>{
 it("preserves decimal strings, null and bucket identity",()=>{
  const parsed=paidCreditProjection({paid_credits:[{id:"codex",has_credits:true,unlimited:false,balance:bucket.balance,observed_at:at},{id:"another",has_credits:false,unlimited:true,balance:null,observed_at:at}]});
  expect(parsed.map(v=>v.balance)).toEqual([bucket.balance,null]);expect(parsed.map(v=>v.id)).toEqual(["codex","another"]);
 });
 it.each(["-1","1e4","private", "1.", "1".repeat(65),undefined])("refuses malformed values %s",balance=>{expect(paidCreditProjection({paid_credits:[{id:"codex",has_credits:true,unlimited:false,balance,observed_at:at}]})).toEqual([])});
 it("shows rounded balances with unchanged exact disclosure",()=>{render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now}/>);expect(screen.getByText("1,250.50")).toBeTruthy();fireEvent.click(screen.getByRole("button",{name:"Show exact credit balance"}));expect(within(screen.getByRole("tooltip")).getByText(bucket.balance)).toBeTruthy();expect(screen.getByText("codex")).toBeTruthy()});
 it("keeps zero, null and unlimited distinct and never sums buckets",()=>{render(<PaidCredits buckets={[{...bucket,id:"zero",balance:"0"},{...bucket,id:"null",balance:null},{...bucket,id:"unlimited",unlimited:true,balance:"15"}]} state={PaidCreditState.Observed} now={now}/>);expect(screen.getByText("0.00")).toBeTruthy();expect(screen.getByText("Balance unknown")).toBeTruthy();expect(screen.getByText("Unlimited credits")).toBeTruthy();expect(screen.queryByText("15 credits")).toBeNull()});
 it("retains values with truthful failure and stale labels",()=>{const v=render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Failed} now={now+600000}/>);expect(screen.getByText("1,250.50")).toBeTruthy();expect(screen.getByText(/Refresh failed; retained observation/)).toBeTruthy();v.rerender(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now+600000}/>);expect(screen.getByText(/Stale observation/)).toBeTruthy()});
 it("shows a count for multiple compact balances and no aggregate",()=>{render(<PaidCredits buckets={[bucket,{...bucket,id:"another",balance:"2"}]} state={PaidCreditState.Observed} compact now={now}/>);expect(screen.getByText(/2 credit balances/)).toBeTruthy();expect(screen.queryByText(bucket.balance+" credits")).toBeNull()});
 it("support states cannot fabricate zero or send a request",()=>{const v=render(<PaidCredits state={PaidCreditState.Unsupported}/>);expect(screen.getByRole("status").textContent).toContain("Update the server");v.rerender(<PaidCredits state={PaidCreditState.Loading}/>);expect(screen.getByRole("status").textContent).toContain("Loading");expect(screen.queryAllByRole("button")).toHaveLength(0)});
});


it.each([false, true])("shows initial refresh failure without inventing retained evidence (compact %s)", async compact => {
 const v=render(<PaidCredits state={PaidCreditState.Failed} compact={compact}/>);
 expect(screen.getByText("Balance unavailable")).toBeTruthy();
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

describe("exact decimal presentation",()=>{
 it.each([
  ["60961.1135370000","60,961.11"], ["1.995","2.00"], ["9.999","10.00"], ["999.995","1,000.00"],
  ["0","0.00"], ["000.0000","0.00"], ["0.004","<0.01"], ["0.00999999","<0.01"], ["0.01","0.01"],
  ["9007199254740993.005","9,007,199,254,740,993.01"], ["0001250.50000000000000001","1,250.50"],
 ])("formats %s as %s",(source,expected)=>{expect(formatPaidCreditBalance(source,"en-US")).toBe(expected);expect(formatPaidCreditBalance(source,"ko-KR")).toBe(expected)});
 it("rounds a 64-character decimal without losing its original bytes",()=>{
  const source="9".repeat(60)+".995";
  expect(source.length).toBe(64);expect(formatPaidCreditBalance(source,"en-US")).toBe(new Intl.NumberFormat("en-US").format(10n**60n)+".00");
  render(<PaidCredits buckets={[{...bucket,balance:source,id:"x".repeat(110)}]} state={PaidCreditState.Observed} now={now}/>);
  fireEvent.click(screen.getByRole("button",{name:"Show exact credit balance"}));expect(within(screen.getByRole("tooltip")).getByText(source)).toBeTruthy();expect(screen.getByText("x".repeat(110))).toBeTruthy();
 });
 it.each(["-1","1e4","1.","1".repeat(65)])("refuses malformed display input %s",source=>expect(formatPaidCreditBalance(source)).toBeUndefined());
});

describe("credit disclosure and retained observations",()=>{
 it.each([false,true])("opens by hover, focus or touch activation and closes on Escape (compact %s)",compact=>{
  render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} compact={compact} now={now}/>);
  expect(screen.getByText("1,250.50")).toBeTruthy();expect(screen.queryByRole("tooltip")).toBeNull();
  const button=screen.getByRole("button",{name:"Show exact credit balance"});
  fireEvent.pointerEnter(button);expect(within(screen.getByRole("tooltip")).getByText(bucket.balance)).toBeTruthy();
  fireEvent.pointerLeave(button);expect(screen.queryByRole("tooltip")).toBeNull();
  act(()=>button.focus());expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.keyDown(button,{key:"Escape"});expect(screen.queryByRole("tooltip")).toBeNull();expect(document.activeElement).toBe(button);
  // Native button activation supplies click for Enter, Space and touch.
  fireEvent.click(button);expect(button.getAttribute("aria-expanded")).toBe("true");
  fireEvent.click(button);expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerDown(button,{pointerType:"touch"});fireEvent.click(button);expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.pointerDown(document.body);expect(screen.queryByRole("tooltip")).toBeNull();
 });
 it("keeps source order and separate bucket values",()=>{
  render(<PaidCredits buckets={[bucket,{...bucket,id:"second",balance:"2"}]} state={PaidCreditState.Observed} now={now}/>);
  expect(screen.getByText("2.00")).toBeTruthy();expect(screen.getAllByRole("listitem").map(row=>row.querySelector(".paid-credit-bucket")?.textContent)).toEqual(["codex","second"]);
 });
 it("preserves the original timestamp at the exact freshness boundary and after failure",()=>{
  const v=render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now+300000}/>);
  expect(screen.queryByText(/Stale observation/)).toBeNull();expect(v.container.querySelector("time")?.getAttribute("datetime")).toBe(at);
  v.rerender(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now+300001}/>);expect(screen.getByText(/Stale observation/)).toBeTruthy();
  v.rerender(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now-1}/>);expect(screen.getByText(/Stale observation/)).toBeTruthy();
  v.rerender(<PaidCredits buckets={[bucket]} state={PaidCreditState.Failed} now={now}/>);expect(screen.getByText(/Refresh failed; retained observation/)).toBeTruthy();expect(v.container.querySelector("time")?.getAttribute("datetime")).toBe(at);
 });
 it("expires evidence without retaining inactive timers",()=>{
  vi.useFakeTimers();vi.setSystemTime(now);const v=render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed}/>);
  try {act(()=>vi.advanceTimersByTime(300001));expect(screen.getByText(/Stale observation/)).toBeTruthy();v.rerender(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} active={false}/>);expect(vi.getTimerCount()).toBe(0)} finally {v.unmount();vi.useRealTimers()}
 });
 it("closes old disclosure on changed balance or inactive presentation",()=>{
  const v=render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now}/>);fireEvent.click(screen.getByRole("button"));
  v.rerender(<PaidCredits buckets={[{...bucket,balance:"2"}]} state={PaidCreditState.Observed} now={now}/>);expect(screen.queryByText(bucket.balance)).toBeNull();expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.click(screen.getByRole("button"));v.rerender(<PaidCredits buckets={[{...bucket,balance:"2"}]} state={PaidCreditState.Observed} active={false} now={now}/>);expect(screen.queryByRole("tooltip")).toBeNull();
 });
 it("localizes the summary and disclosure while retaining exact balance bytes",async()=>{
  const v=render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now}/>);await i18n.changeLanguage(SupportedLanguage.Korean);
  expect(screen.getByRole("region",{name:"유료 크레딧"})).toBeTruthy();expect(screen.getByText("1,250.50")).toBeTruthy();fireEvent.click(screen.getByRole("button",{name:"정확한 크레딧 잔액 보기"}));expect(screen.getByText(bucket.balance)).toBeTruthy();
  v.rerender(<PaidCredits buckets={[bucket,{...bucket,id:"two"}]} state={PaidCreditState.Observed} compact now={now}/>);expect(screen.getByText("크레딧 잔액 2개")).toBeTruthy();
 });
});
