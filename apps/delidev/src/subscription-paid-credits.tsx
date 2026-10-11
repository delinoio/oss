// SPDX-License-Identifier: Apache-2.0
import "./subscription-paid-credits.css";
import { useEffect, useId, useReducer, useRef, useState, type ReactNode } from "react";
import { copy, displayLocale, useLocale } from "./localization";
import { Timestamp } from "./timestamp-display";
import { object, items, text } from "./documents";
import { formatPaidCreditBalance } from "./subscription-paid-credit-format";
export { formatPaidCreditBalance } from "./subscription-paid-credit-format";
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
function ExactBalance({ balance, children }: { balance: string; children: ReactNode }) {
 const id=useId(),button=useRef<HTMLButtonElement>(null),[open,setOpen]=useState(false),[pinned,setPinned]=useState(false);
 return <div className="subscription-paid-balance" onMouseEnter={()=>setOpen(true)} onMouseLeave={()=>{if(!pinned&&document.activeElement!==button.current)setOpen(false);}}>
  {children}<button ref={button} type="button" className="subscription-paid-info" aria-label={copy("paid-credits.exact-action")} aria-describedby={open?id:undefined} aria-expanded={open} aria-controls={id} onFocus={()=>setOpen(true)} onBlur={()=>{setPinned(false);setOpen(false);}} onClick={()=>{setPinned(!pinned);setOpen(!pinned);}} onKeyDown={event=>{if(event.key==="Escape"&&open){event.preventDefault();event.stopPropagation();setPinned(false);setOpen(false);button.current?.focus({preventScroll:true});}}}><span aria-hidden="true">ⓘ</span></button>
  <span id={id} hidden={!open} role="tooltip" className="subscription-paid-tooltip">{copy("paid-credits.exact-label")} <span>{balance}</span></span>
 </div>;
}
function CreditValue({ bucket }: { bucket: PaidCreditBucket }) {
 if(bucket.unlimited)return <strong className="subscription-paid-state">{copy("paid-credits.unlimited")}</strong>;
 if(bucket.balance===null)return <strong className="subscription-paid-state">{copy("paid-credits.unknown")}</strong>;
 return <ExactBalance key={bucket.balance} balance={bucket.balance}><span className="subscription-paid-amount">{formatPaidCreditBalance(bucket.balance,displayLocale())}</span><span className="subscription-paid-unit">{copy("paid-credits.unit")}</span></ExactBalance>;
}
export function PaidCredits({buckets=[],state,compact=false,active=true,now:givenNow}:{buckets?:readonly PaidCreditBucket[];state:PaidCreditState;compact?:boolean;active?:boolean;now?:number}) {
 useLocale();const[,tick]=useReducer(v=>v+1,0);const now=givenNow??Date.now();
 useEffect(()=>{if(!active||givenNow!==undefined)return;const due=buckets.map(b=>Date.parse(b.observedAt)+300001).filter(t=>t>now);if(!due.length)return;const timer=setTimeout(tick,Math.min(2147483647,Math.max(1,Math.min(...due)-now)));return()=>clearTimeout(timer)},[active,buckets,givenNow,now]);
 const unavailable=state===PaidCreditState.Loading?copy("paid-credits.loading"):state===PaidCreditState.Unsupported?copy("paid-credits.unsupported"):state===PaidCreditState.Failed?copy("paid-credits.failed-empty"):copy("paid-credits.unknown");
 const status=(b:PaidCreditBucket)=>copy(state===PaidCreditState.Failed?"paid-credits.failed":Date.parse(b.observedAt)>now||now-Date.parse(b.observedAt)>300000?"paid-credits.stale":"paid-credits.observed");
 const unavailableState=state===PaidCreditState.Unsupported||state===PaidCreditState.Loading||!buckets.length;
 return <section className={`subscription-paid-credits${compact?" subscription-paid-summary":""}`} aria-label={copy("paid-credits.title")}><h3>{copy("paid-credits.title")}</h3>{unavailableState?<div role="status">{state===PaidCreditState.Failed?<strong className="subscription-paid-state">{copy("paid-credits.unavailable")}</strong>:null}<p>{unavailable}</p></div>:compact&&buckets.length>1?<><strong className="subscription-paid-state">{copy("paid-credits.count",{count:buckets.length})}</strong><ul>{buckets.map(b=><li key={b.id}><small>{status(b)} · <Timestamp active={active} value={b.observedAt}/></small></li>)}</ul></>:<ul>{buckets.map(b=><li key={b.id}>{!compact?<strong className="subscription-paid-bucket">{b.id}</strong>:null}<CreditValue bucket={b}/><small>{status(b)} · <Timestamp active={active} value={b.observedAt}/></small></li>)}</ul>}</section>;
}
