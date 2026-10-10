// SPDX-License-Identifier: Apache-2.0
import "./subscription-paid-credits.css";
import { useEffect, useId, useReducer, useRef, useState } from "react";
import { formatPaidCreditBalance } from "./paid-credit-format";
import { copy, useLocale } from "./localization";
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
export function PaidCreditBalance({bucket}: {bucket: PaidCreditBucket}) {
 const locale=useLocale(),id=useId(),root=useRef<HTMLSpanElement>(null),button=useRef<HTMLButtonElement>(null);
 const [open,setOpen]=useState(false),[pinned,setPinned]=useState(false);
 useEffect(()=>{setOpen(false);setPinned(false);},[bucket.id,bucket.balance,bucket.unlimited]);
 const rounded=bucket.balance===null?undefined:formatPaidCreditBalance(bucket.balance,locale);
 if(bucket.unlimited)return <span className="paid-credit-balance">{copy("paid-credits.unlimited")}</span>;
 if(rounded===undefined)return <span className="paid-credit-balance">{copy("paid-credits.unknown")}</span>;
 return <span ref={root} className="paid-credit-number" onMouseEnter={()=>setOpen(true)} onMouseLeave={()=>{if(!pinned&&!root.current?.contains(document.activeElement))setOpen(false);}} onBlur={event=>{if(!event.currentTarget.contains(event.relatedTarget as Node|null)){setPinned(false);setOpen(false);}}} onKeyDown={event=>{if(event.key==="Escape"&&open){event.preventDefault();event.stopPropagation();setOpen(false);setPinned(false);button.current?.focus({preventScroll:true});}}}>
  <span className="paid-credit-balance">{rounded}</span><span className="paid-credit-unit">{copy("paid-credits.unit")}</span>
  <button ref={button} type="button" className="paid-credit-information" aria-label={copy("paid-credits.exact")} aria-expanded={open} aria-describedby={open?id:undefined} onFocus={()=>setOpen(true)} onClick={()=>{setPinned(!pinned);setOpen(!pinned);}}>ⓘ</button>
  {open?<span id={id} role="tooltip" className="paid-credit-exact">{copy("paid-credits.exact")}: <span>{bucket.balance}</span></span>:null}
 </span>;
}
export function PaidCredits({buckets=[],state,compact=false,active=true,now:givenNow}:{buckets?:readonly PaidCreditBucket[];state:PaidCreditState;compact?:boolean;active?:boolean;now?:number}) {
 useLocale();const[,tick]=useReducer(v=>v+1,0);const now=givenNow??Date.now();
 useEffect(()=>{if(!active||givenNow!==undefined)return;const due=buckets.map(b=>Date.parse(b.observedAt)+300001).filter(t=>t>now);if(!due.length)return;const timer=setTimeout(tick,Math.min(2147483647,Math.max(1,Math.min(...due)-now)));return()=>clearTimeout(timer)},[active,buckets,givenNow,now]);
 const unavailable=state===PaidCreditState.Loading?copy("paid-credits.loading"):state===PaidCreditState.Unsupported?copy("paid-credits.unsupported"):state===PaidCreditState.Failed?copy("paid-credits.balance-unavailable"):copy("paid-credits.unknown");
 const noEvidence=state===PaidCreditState.Unsupported||state===PaidCreditState.Loading||!buckets.length;
 const status=(b:PaidCreditBucket)=>state===PaidCreditState.Failed?copy("paid-credits.failed"):Date.parse(b.observedAt)>now||now-Date.parse(b.observedAt)>300000?copy("paid-credits.stale"):copy("paid-credits.observed");
 return <section className={`subscription-paid-credits${compact?" subscription-paid-summary":""}`} aria-label={copy("paid-credits.title")}>
  <h3>{copy("paid-credits.title")}</h3>
  {noEvidence?<><p className="paid-credit-balance" role="status">{unavailable}</p>{state===PaidCreditState.Failed?<small>{copy("paid-credits.failed-empty")}</small>:null}</>:compact?<>
   {buckets.length===1?<PaidCreditBalance bucket={buckets[0]}/>:<p className="paid-credit-balance">{copy("paid-credits.count",{count:buckets.length})}</p>}
   {buckets.map(b=><small key={b.id}>{buckets.length>1?<>{b.id} · </>:null}{status(b)} · <Timestamp active={active} value={b.observedAt}/></small>)}
  </>:<ul>{buckets.map(b=><li key={b.id}><strong>{b.id}</strong><PaidCreditBalance bucket={b}/><small>{status(b)} · <Timestamp active={active} value={b.observedAt}/></small></li>)}</ul>}
 </section>;
}
