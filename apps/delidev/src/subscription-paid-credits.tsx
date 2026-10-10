// SPDX-License-Identifier: Apache-2.0
import "./subscription-paid-credits.css";
import { useEffect, useId, useReducer, useRef, useState } from "react";
import { copy, displayLocale, useLocale } from "./localization";
import { Timestamp } from "./timestamp-display";
import { object, items, text } from "./documents";
export enum PaidCreditState { Loading="loading", Unsupported="unsupported", Unknown="unknown", Observed="observed", Failed="failed" }
export interface PaidCreditBucket { id:string; hasCredits:boolean; unlimited:boolean; balance:string|null; observedAt:string }
const decimal=/^[0-9]+(?:\.[0-9]+)?$/;
export function paidCreditProjection(subscription:Record<string,unknown>):PaidCreditBucket[] {
 const values=items(subscription.paid_credits);if(values.length>32)return [];
 const result:PaidCreditBucket[]=[];const seen=new Set<string>();
 for(const entry of values){const b=object(entry),id=text(b.id),balance=b.balance,at=text(b.observed_at);
 if(!/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,109}$/.test(id)||seen.has(id)||typeof b.has_credits!=="boolean"||typeof b.unlimited!=="boolean"||(balance!==null&&(typeof balance!=="string"||balance.length>64||!decimal.test(balance)))||!Number.isFinite(Date.parse(at)))return [];
 seen.add(id);result.push({id,hasCredits:b.has_credits,unlimited:b.unlimited,balance,observedAt:at});
 }return result;
}

/** Round the presentation only. Original decimal bytes never pass through Number. */
export function formatPaidCreditBalance(balance: string, locale = displayLocale()): string | undefined {
 if (balance.length > 64 || !decimal.test(balance)) return undefined;
 const [rawWhole, fraction = ""] = balance.split(".");
 let whole = rawWhole.replace(/^0+(?=\d)/, "");
 const separator = new Intl.NumberFormat(locale).formatToParts(1.1).find(part => part.type === "decimal")?.value ?? ".";
 if (whole === "0" && /[1-9]/.test(fraction) && !/[1-9]/.test(fraction.slice(0, 2))) return `<0${separator}01`;
 let digits = whole + fraction.padEnd(2, "0").slice(0, 2);
 if (fraction.length > 2 && fraction[2] >= "5") {
  const incremented = digits.split("");
  let position = incremented.length - 1;
  for (; position >= 0 && incremented[position] === "9"; position--) incremented[position] = "0";
  if (position < 0) incremented.unshift("1");
  else incremented[position] = String.fromCharCode(incremented[position].charCodeAt(0) + 1);
  digits = incremented.join("");
 }
 whole = digits.slice(0, -2);
 const grouped = new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(BigInt(whole));
 return `${grouped}${separator}${digits.slice(-2)}`;
}

function ExactBalance({ balance, active }: { balance: string; active: boolean }) {
 const id = useId(), host = useRef<HTMLSpanElement>(null), trigger = useRef<HTMLButtonElement>(null);
 const [open, setOpen] = useState(false), [pinned, setPinned] = useState(false);
 const close = () => { setOpen(false); setPinned(false); };
 useEffect(() => { if (!active) { setOpen(false); setPinned(false); } }, [active]);
 useEffect(() => {
  if (!open) return;
  const outside = (event: PointerEvent) => { if (!host.current?.contains(event.target as Node)) { setOpen(false); setPinned(false); } };
  document.addEventListener("pointerdown", outside);
  return () => document.removeEventListener("pointerdown", outside);
 }, [open]);
 return <span ref={host} className="paid-credit-information" onPointerEnter={() => { if (active) setOpen(true); }} onPointerLeave={() => { if (!pinned && !host.current?.contains(document.activeElement)) close(); }} onBlurCapture={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) close(); }} onKeyDown={event => {
  if (open && event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); trigger.current?.focus(); }
 }}>
  <button ref={trigger} type="button" className="paid-credit-information-button" aria-label={copy("paid-credits.exact-action")} aria-expanded={active && open} aria-controls={id} aria-describedby={active && open ? id : undefined} disabled={!active} onFocus={() => { if (active) setOpen(true); }} onClick={() => { setOpen(!pinned); setPinned(!pinned); }}>ⓘ</button>
  <span id={id} role="tooltip" hidden={!active || !open} className="paid-credit-exact"><span>{copy("paid-credits.exact-label")}</span><span>{balance}</span></span>
 </span>;
}

function CreditValue({ bucket, active }: { bucket: PaidCreditBucket; active: boolean }) {
 if (bucket.unlimited) return <span className="paid-credit-state">{copy("paid-credits.unlimited")}</span>;
 const formatted = bucket.balance === null ? undefined : formatPaidCreditBalance(bucket.balance);
 if (formatted === undefined) return <span className="paid-credit-state">{copy("paid-credits.unknown")}</span>;
 return <div className="paid-credit-value"><strong className="paid-credit-balance">{formatted}</strong><span className="paid-credit-unit">{copy("paid-credits.unit")}</span><ExactBalance key={bucket.balance} balance={bucket.balance!} active={active}/></div>;
}

export function PaidCredits({buckets=[],state,compact=false,active=true,now:givenNow}:{buckets?:readonly PaidCreditBucket[];state:PaidCreditState;compact?:boolean;active?:boolean;now?:number}) {
 useLocale();const[,tick]=useReducer(v=>v+1,0);const now=givenNow??Date.now();
 useEffect(()=>{if(!active||givenNow!==undefined)return;const due=buckets.map(b=>Date.parse(b.observedAt)+300001).filter(t=>t>now);if(!due.length)return;const timer=setTimeout(tick,Math.min(2147483647,Math.max(1,Math.min(...due)-now)));return()=>clearTimeout(timer)},[active,buckets,givenNow,now]);
 const unavailable=state===PaidCreditState.Loading?copy("paid-credits.loading"):state===PaidCreditState.Unsupported?copy("paid-credits.unsupported"):state===PaidCreditState.Failed?copy("paid-credits.unavailable"):copy("paid-credits.unknown");
 const stale=(b:PaidCreditBucket)=>Date.parse(b.observedAt)>now||now-Date.parse(b.observedAt)>300000;
 const observation=(b:PaidCreditBucket)=><small className="paid-credit-observation">{copy(state===PaidCreditState.Failed?"paid-credits.failed":stale(b)?"paid-credits.stale":"paid-credits.observed")} · <Timestamp active={active} value={b.observedAt}/></small>;
 const noEvidence=state===PaidCreditState.Unsupported||state===PaidCreditState.Loading||!buckets.length;
 return <section className={`subscription-paid-credits${compact?" subscription-paid-summary":""}`} aria-label={copy("paid-credits.title")}>
  {compact?<p className="paid-credit-heading">{copy("paid-credits.title")}</p>:<h3 className="paid-credit-heading">{copy("paid-credits.title")}</h3>}
  {noEvidence?<div role="status"><p>{unavailable}</p>{state===PaidCreditState.Failed?<small>{copy("paid-credits.failed-empty")}</small>:null}</div>:compact?buckets.length===1?<><CreditValue bucket={buckets[0]} active={active}/>{observation(buckets[0])}</>:<><p>{copy("paid-credits.count",{count:buckets.length})}</p><small className="paid-credit-observation">{copy(state===PaidCreditState.Failed?"paid-credits.failed":buckets.some(stale)?"paid-credits.stale":"paid-credits.observed")}</small></>:<ul>{buckets.map(b=><li key={b.id}><strong className="paid-credit-bucket">{b.id}</strong><CreditValue bucket={b} active={active}/>{observation(b)}</li>)}</ul>}
 </section>;
}
