// SPDX-License-Identifier: Apache-2.0
import "./subscription-paid-credits.css";
import { useEffect, useReducer, useId, useRef, useState } from "react";
import { copy, i18n, useLocale } from "./localization";
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
/** Round the bounded source decimal without a floating-point conversion. */
export function formatPaidCreditBalance(balance: string, locale: string): string | null {
 if(balance.length>64 || !decimal.test(balance))return null;
 const [whole, fraction=""]=balance.split(".");
 const integer=BigInt(whole);
 const cents=integer*100n+BigInt((fraction+"00").slice(0,2))+(fraction.length>2&&fraction[2]>="5"?1n:0n);
 const positive=/[1-9]/.test(balance);
 if(positive && integer===0n && !/[1-9]/.test(fraction.slice(0,2)))return "<0.01";
 const grouped=new Intl.NumberFormat(locale,{maximumFractionDigits:0}).format(cents/100n);
 const separator=new Intl.NumberFormat(locale).formatToParts(1.1).find(p=>p.type==="decimal")?.value??".";
 return grouped+separator+(cents%100n).toString().padStart(2,"0");
}

function CreditValue({bucket}:{bucket:PaidCreditBucket}) {
 const [open,setOpen]=useState(false);const id=useId();const trigger=useRef<HTMLButtonElement>(null);
 const formatted=bucket.balance===null?null:formatPaidCreditBalance(bucket.balance,i18n.resolvedLanguage??"en");
 if(bucket.unlimited)return <span className="subscription-paid-value">{copy("paid-credits.unlimited")}</span>;
 if(formatted===null)return <span className="subscription-paid-value">{copy("paid-credits.unknown")}</span>;
 return <span className="subscription-paid-number" onMouseEnter={()=>setOpen(true)} onMouseLeave={()=>{if(document.activeElement!==trigger.current)setOpen(false)}}>
  <span className="subscription-paid-value">{formatted}</span> <span className="subscription-paid-unit">{copy("paid-credits.unit")}</span>
  <button ref={trigger} type="button" className="subscription-paid-info" aria-label={copy("paid-credits.exact-label")} aria-describedby={open?id:undefined} aria-expanded={open} onFocus={()=>setOpen(true)} onBlur={()=>setOpen(false)} onClick={()=>setOpen(true)} onKeyDown={event=>{if(event.key==="Escape"&&open){event.preventDefault();event.stopPropagation();setOpen(false);trigger.current?.focus()}}}>ⓘ</button>
  {open?<span id={id} role="tooltip" className="subscription-paid-exact">{copy("paid-credits.exact",{balance:bucket.balance})}</span>:null}
 </span>;
}

export function PaidCredits({buckets=[],state,compact=false,active=true,now:givenNow}:{buckets?:readonly PaidCreditBucket[];state:PaidCreditState;compact?:boolean;active?:boolean;now?:number}) {
 useLocale();const[,tick]=useReducer(v=>v+1,0);const now=givenNow??Date.now();
 useEffect(()=>{if(!active||givenNow!==undefined)return;const due=buckets.map(b=>Date.parse(b.observedAt)+300001).filter(t=>t>now);if(!due.length)return;const timer=setTimeout(tick,Math.min(2147483647,Math.max(1,Math.min(...due)-now)));return()=>clearTimeout(timer)},[active,buckets,givenNow,now]);
 const noEvidence=state===PaidCreditState.Unsupported||state===PaidCreditState.Loading||!buckets.length;
 const unavailable=state===PaidCreditState.Loading?copy("paid-credits.loading"):state===PaidCreditState.Unsupported?copy("paid-credits.unsupported"):state===PaidCreditState.Failed?copy("paid-credits.unavailable"):copy("paid-credits.unknown");
 const status=(bucket:PaidCreditBucket)=>state===PaidCreditState.Failed?copy("paid-credits.failed"):Date.parse(bucket.observedAt)>now||now-Date.parse(bucket.observedAt)>300000?copy("paid-credits.stale"):copy("paid-credits.observed");
 return <section className={`subscription-paid-credits${compact?" subscription-paid-summary":""}`} aria-label={copy("paid-credits.title")}><h3>{copy("paid-credits.title")}</h3>
 {noEvidence?<div role="status"><span className="subscription-paid-value">{unavailable}</span>{state===PaidCreditState.Failed?<small>{copy("paid-credits.failed-empty")}</small>:null}</div>:compact&&buckets.length>1?<div><span className="subscription-paid-value">{copy("paid-credits.count",{count:buckets.length})}</span>{buckets.map(b=><small key={b.id}>{status(b)} · <Timestamp active={active} value={b.observedAt}/></small>)}</div>:<ul>{buckets.map(b=><li key={b.id}>{!compact?<strong>{b.id}</strong>:null}<CreditValue key={b.balance} bucket={b}/><small>{status(b)} · <Timestamp active={active} value={b.observedAt}/></small></li>)}</ul>}
 </section>;
}
