// SPDX-License-Identifier: Apache-2.0
import "./subscription-paid-credits.css";
import { useEffect, useReducer, useId, useRef, useState } from "react";
import { copy, useLocale, displayLocale } from "./localization";
import { Timestamp } from "./timestamp-display";
import { object, items, text } from "./documents";
import { formatPaidCreditBalance } from "./subscription-paid-credit-format";
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
/** A native button supplies Enter, Space and touch activation without custom key emulation. */
export function PaidCreditValue({ bucket }: { bucket: PaidCreditBucket }) {
 useLocale();
 const id = useId(), trigger = useRef<HTMLButtonElement>(null), container = useRef<HTMLSpanElement>(null);
 const [open, setOpen] = useState(false), [pinned, setPinned] = useState(false);
 const suppressFocus = useRef(false);
 useEffect(() => {
   if (!open) return;
   const dismiss = (event: KeyboardEvent) => {
     if (event.key !== "Escape") return;
     event.preventDefault(); event.stopPropagation(); setOpen(false); setPinned(false);
     // A hovered disclosure closes before its parent, but never steals outside focus.
     if (container.current?.contains(document.activeElement)) {
       suppressFocus.current = true; trigger.current?.focus(); suppressFocus.current = false;
     }
   };
   document.addEventListener("keydown", dismiss, true);
   return () => document.removeEventListener("keydown", dismiss, true);
 }, [open]);
 if (bucket.unlimited) return <span className="subscription-paid-value">{copy("paid-credits.unlimited")}</span>;
 if (bucket.balance === null) return <span className="subscription-paid-value">{copy("paid-credits.unknown")}</span>;
 return <span ref={container} className="subscription-paid-number" onMouseEnter={() => setOpen(true)}
   onMouseLeave={() => { if (!pinned && document.activeElement !== trigger.current) setOpen(false); }}
   onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget)) { setOpen(false); setPinned(false); } }}>
   <span className="subscription-paid-value">{formatPaidCreditBalance(bucket.balance, displayLocale())}</span>
   <span className="subscription-paid-suffix">{copy("paid-credits.suffix")}</span>
   <button ref={trigger} type="button" className="subscription-paid-info" aria-label={copy("paid-credits.exact-label", { id: bucket.id })}
     aria-expanded={open} aria-controls={id} aria-describedby={open ? id : undefined}
     onFocus={() => { if (!suppressFocus.current) setOpen(true); }}
     onClick={() => { setOpen(!pinned); setPinned(!pinned); }}><span aria-hidden="true">ⓘ</span></button>
   {open ? <span id={id} role="note" className="subscription-paid-exact">{copy("paid-credits.exact", { balance: bucket.balance })}</span> : null}
 </span>;
}
export function PaidCredits({buckets=[],state,compact=false,active=true,now:givenNow}:{buckets?:readonly PaidCreditBucket[];state:PaidCreditState;compact?:boolean;active?:boolean;now?:number}) {
 useLocale();const[,tick]=useReducer(v=>v+1,0);const now=givenNow??Date.now();
 useEffect(()=>{if(!active||givenNow!==undefined)return;const due=buckets.flatMap(b=>[Date.parse(b.observedAt),Date.parse(b.observedAt)+300001]).filter(t=>t>now);if(!due.length)return;const timer=setTimeout(tick,Math.min(2147483647,Math.max(1,Math.min(...due)-now)));return()=>clearTimeout(timer)},[active,buckets,givenNow,now]);
 const unavailable=state===PaidCreditState.Loading?copy("paid-credits.loading"):state===PaidCreditState.Unsupported?copy("paid-credits.unsupported"):state===PaidCreditState.Failed?copy("paid-credits.unavailable"):copy("paid-credits.unknown");
 const status=(b:PaidCreditBucket)=>copy(state===PaidCreditState.Failed?"paid-credits.failed":Date.parse(b.observedAt)>now||now-Date.parse(b.observedAt)>300000?"paid-credits.stale":"paid-credits.observed");
 const missing=state===PaidCreditState.Unsupported||state===PaidCreditState.Loading||!buckets.length;
 return <section className={`subscription-paid-credits${compact ? " subscription-paid-summary" : ""}`} aria-label={copy("paid-credits.title")}>
   <h3>{copy("paid-credits.title")}</h3>
   {missing ? <><p role="status">{unavailable}</p>{state===PaidCreditState.Failed ? <small>{copy("paid-credits.failed-empty")}</small> : null}</> : compact && buckets.length>1 ? <>
     <p>{copy("paid-credits.count",{count:buckets.length})}</p>
     <ul>{buckets.map(b=><li key={b.id}><small>{b.id} · {status(b)} · <Timestamp active={active} value={b.observedAt}/></small></li>)}</ul>
   </> : <ul>{buckets.map(b=><li key={b.id}>
     {!compact ? <strong>{b.id}</strong> : null}
     <PaidCreditValue key={`${b.id}:${b.balance}:${b.unlimited}`} bucket={b}/>
     <small>{status(b)} · <Timestamp active={active} value={b.observedAt}/></small>
   </li>)}</ul>}
 </section>;
}
