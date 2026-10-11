// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { describe,it,expect,vi } from "vitest";
import { i18n, SupportedLanguage } from "./localization";
import { PaidCredits,PaidCreditState,paidCreditProjection } from "./subscription-paid-credits";
const at="2026-10-09T00:00:00Z",now=Date.parse(at),bucket={id:"codex",hasCredits:true,unlimited:false,balance:"1250.50000000000000001",observedAt:at};
describe("paid-credit projection",()=>{
 it("preserves decimal strings, null and bucket identity",()=>{
  const parsed=paidCreditProjection({paid_credits:[{id:"codex",has_credits:true,unlimited:false,balance:bucket.balance,observed_at:at},{id:"another",has_credits:false,unlimited:true,balance:null,observed_at:at}]});
  expect(parsed.map(v=>v.balance)).toEqual([bucket.balance,null]);expect(parsed.map(v=>v.id)).toEqual(["codex","another"]);
 });
 it.each(["-1","1e4","private", "1.", "1".repeat(65),undefined])("refuses malformed values %s",balance=>{expect(paidCreditProjection({paid_credits:[{id:"codex",has_credits:true,unlimited:false,balance,observed_at:at}]})).toEqual([])});
 it("shows a rounded value with unchanged exact evidence and no purchase controls",()=>{
  render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now}/>);
  expect(screen.getByText("1,250.50")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", {name:"Show exact balance for codex"}));
  expect(screen.getByRole("note").textContent).toBe(`Exact balance: ${bucket.balance}`);
  expect(screen.queryAllByRole("button")).toHaveLength(1);expect(screen.getByText("codex")).toBeTruthy();
 });
 it("keeps zero, null and unlimited distinct and never sums buckets",()=>{render(<PaidCredits buckets={[{...bucket,id:"zero",balance:"0"},{...bucket,id:"null",balance:null},{...bucket,id:"unlimited",unlimited:true,balance:"15"}]} state={PaidCreditState.Observed} now={now}/>);expect(screen.getByText("0.00")).toBeTruthy();expect(screen.getByText("Balance unknown")).toBeTruthy();expect(screen.getByText("Unlimited credits")).toBeTruthy();expect(screen.queryByText("15.00")).toBeNull()});
 it("retains values with truthful failure and stale labels",()=>{const v=render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Failed} now={now+600000}/>);expect(screen.getByText("1,250.50")).toBeTruthy();expect(screen.getByText(/Refresh failed; retained observation/)).toBeTruthy();v.rerender(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now+600000}/>);expect(screen.getByText(/Stale observation/)).toBeTruthy()});
 it("shows a count for multiple compact balances and no aggregate",()=>{render(<PaidCredits buckets={[bucket,{...bucket,id:"another",balance:"2"}]} state={PaidCreditState.Observed} compact now={now}/>);expect(screen.getByText(/2 credit balances/)).toBeTruthy();expect(screen.queryByText(bucket.balance+" credits")).toBeNull()});
 it("support states cannot fabricate zero or send a request",()=>{const v=render(<PaidCredits state={PaidCreditState.Unsupported}/>);expect(screen.getByRole("status").textContent).toContain("Update the server");v.rerender(<PaidCredits state={PaidCreditState.Loading}/>);expect(screen.getByRole("status").textContent).toContain("Loading");expect(screen.queryAllByRole("button")).toHaveLength(0)});
});


it.each([false, true])("shows initial refresh failure without inventing retained evidence (compact %s)", async compact => {
 const v=render(<PaidCredits state={PaidCreditState.Failed} compact={compact}/>);
 expect(screen.getByText(/Refresh failed; no balance observed/)).toBeTruthy();
 expect(screen.getByRole("status").textContent).toBe("Balance unavailable");
 expect(screen.queryByText(/Balance unknown|retained observation/)).toBeNull();
 await i18n.changeLanguage(SupportedLanguage.Korean);
 expect(screen.getByText(/새로고침 실패; 관측된 잔액 없음/)).toBeTruthy();
 expect(screen.queryByText(/잔액 알 수 없음|기존 관측 유지/)).toBeNull();
 await i18n.changeLanguage(SupportedLanguage.English);
 v.rerender(<PaidCredits state={PaidCreditState.Unknown} compact={compact}/>);
 expect(screen.getByText(/Balance unknown/)).toBeTruthy();
 expect(screen.queryByText(/Refresh failed/)).toBeNull();
});

it.each([300000,300001,-1])("preserves freshness at age %s ms", age => {
 render(<PaidCredits compact buckets={[bucket]} state={PaidCreditState.Observed} now={now+age}/>);
 expect(screen.getByText(age===300000 ? /^Observed/ : /^Stale observation/)).toBeTruthy();
 expect(document.querySelector("time")?.getAttribute("datetime")).toBe(at);
});
it("keeps source ordering and individual balances in management", () => {
 render(<PaidCredits buckets={[{...bucket,id:"z-first",balance:"1.995"},{...bucket,id:"a-second",balance:"0.004"}]} state={PaidCreditState.Observed} now={now}/>);
 const rows=screen.getAllByRole("listitem");
 expect(within(rows[0]).getByText("z-first")).toBeTruthy();expect(within(rows[0]).getByText("2.00")).toBeTruthy();
 expect(within(rows[1]).getByText("a-second")).toBeTruthy();expect(within(rows[1]).getByText("<0.01")).toBeTruthy();
});
it("keeps 64-character evidence and the complete bucket identity accessible", () => {
 const exact="9".repeat(60)+".995", id="x".repeat(110);
 render(<PaidCredits buckets={[{...bucket,id,balance:exact}]} state={PaidCreditState.Observed} now={now}/>);
 fireEvent.click(screen.getByRole("button",{name:`Show exact balance for ${id}`}));
 expect(screen.getByRole("note").textContent).toBe(`Exact balance: ${exact}`);expect(screen.getByText(id)).toBeTruthy();
});
it("opens on hover and dismisses Escape without stealing outside focus or dismissing the parent", () => {
 const parent=vi.fn();
 render(<div onKeyDown={parent}><button>Outside</button><PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now}/></div>);
 const outside=screen.getByRole("button",{name:"Outside"});outside.focus();
 fireEvent.mouseEnter(screen.getByText("1,250.50"));expect(screen.getByRole("note")).toBeTruthy();
 fireEvent.keyDown(outside,{key:"Escape"});expect(screen.queryByRole("note")).toBeNull();expect(document.activeElement).toBe(outside);expect(parent).not.toHaveBeenCalled();
 fireEvent.keyDown(outside,{key:"Escape"});expect(parent).toHaveBeenCalledOnce();
});
it("supports focus, native button activation, touch clicks, blur and Escape", () => {
 render(<><PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now}/><button>Outside</button></>);
 const button=screen.getByRole("button",{name:"Show exact balance for codex"});
 act(()=>button.focus());expect(button.getAttribute("aria-expanded")).toBe("true");
 fireEvent.keyDown(button,{key:"Escape"});expect(button.getAttribute("aria-expanded")).toBe("false");expect(document.activeElement).toBe(button);
 // Native buttons dispatch clicks for Enter, Space and touch; no key handlers override them.
 fireEvent.click(button);expect(screen.getByRole("note")).toBeTruthy();
 fireEvent.click(button);expect(screen.queryByRole("note")).toBeNull();
 fireEvent.click(button);act(()=>screen.getByRole("button",{name:"Outside"}).focus());expect(screen.queryByRole("note")).toBeNull();
});
it("updates future and expiry transitions without changing the original timestamp", () => {
 vi.useFakeTimers();vi.setSystemTime(now-1);
 const view=render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed}/>);
 try {
  expect(screen.getByText(/^Stale observation/)).toBeTruthy();
  act(()=>vi.advanceTimersByTime(1));expect(screen.getByText(/^Observed/)).toBeTruthy();
  act(()=>vi.advanceTimersByTime(300000));expect(screen.getByText(/^Observed/)).toBeTruthy();
  act(()=>vi.advanceTimersByTime(1));expect(screen.getByText(/^Stale observation/)).toBeTruthy();
  expect(document.querySelector("time")?.getAttribute("datetime")).toBe(at);
 } finally {view.unmount();vi.useRealTimers();}
});
it("keeps numeric grouping and exact evidence across language changes", async () => {
 render(<PaidCredits buckets={[{...bucket,balance:"60961.1135370000"}]} state={PaidCreditState.Observed} now={now}/>);
 try {
  await i18n.changeLanguage(SupportedLanguage.Korean);
  expect(screen.getByText("60,961.11")).toBeTruthy();expect(screen.getByText("크레딧")).toBeTruthy();
  fireEvent.click(screen.getByRole("button",{name:"codex의 정확한 잔액 보기"}));
  expect(screen.getByRole("note").textContent).toBe("정확한 잔액: 60961.1135370000");
 } finally {await i18n.changeLanguage(SupportedLanguage.English);}
});

it.each([PaidCreditState.Loading,PaidCreditState.Unsupported])("hides retained numeric evidence while %s", state => {
 render(<PaidCredits compact buckets={[bucket]} state={state} now={now}/>);
 expect(screen.getByRole("status")).toBeTruthy();expect(screen.queryByText("1,250.50")).toBeNull();
 expect(screen.queryByRole("button")).toBeNull();expect(document.querySelector("time")).toBeNull();
});
it("clears an open exact disclosure when its numeric evidence changes", () => {
 const view=render(<PaidCredits buckets={[bucket]} state={PaidCreditState.Observed} now={now}/>);
 fireEvent.click(screen.getByRole("button"));expect(screen.getByRole("note")).toBeTruthy();
 view.rerender(<PaidCredits buckets={[{...bucket,balance:"7"}]} state={PaidCreditState.Observed} now={now}/>);
 expect(screen.queryByRole("note")).toBeNull();expect(screen.getByText("7.00")).toBeTruthy();
});
