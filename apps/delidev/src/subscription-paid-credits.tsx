// SPDX-License-Identifier: Apache-2.0
import "./subscription-paid-credits.css";
import { useEffect, useReducer } from "react";
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
export function PaidCredits({buckets=[],state,compact=false,active=true,now:givenNow}:{buckets?:readonly PaidCreditBucket[];state:PaidCreditState;compact?:boolean;active?:boolean;now?:number}) {
 useLocale();const[,tick]=useReducer(v=>v+1,0);const now=givenNow??Date.now();
 useEffect(()=>{if(!active||givenNow!==undefined)return;const due=buckets.map(b=>Date.parse(b.observedAt)+300001).filter(t=>t>now);if(!due.length)return;const timer=setTimeout(tick,Math.min(2147483647,Math.max(1,Math.min(...due)-now)));return()=>clearTimeout(timer)},[active,buckets,givenNow,now]);
 const unavailable=state===PaidCreditState.Loading?copy("paid-credits.loading"):state===PaidCreditState.Unsupported?copy("paid-credits.unsupported"):state===PaidCreditState.Failed?copy("paid-credits.failed-empty"):copy("paid-credits.unknown");
 const value=(b:PaidCreditBucket)=>b.unlimited?copy("paid-credits.unlimited"):b.balance===null?copy("paid-credits.unknown"):copy("paid-credits.value",{balance:b.balance});
 if(compact)return <small className="subscription-paid-summary">{copy("paid-credits.title")}: {state===PaidCreditState.Unsupported||state===PaidCreditState.Loading||!buckets.length?unavailable:buckets.length===1?value(buckets[0]):copy("paid-credits.count",{count:buckets.length})}{buckets.length>0&&state!==PaidCreditState.Loading&&state!==PaidCreditState.Unsupported&&(state===PaidCreditState.Failed||buckets.some(b=>Date.parse(b.observedAt)>now||now-Date.parse(b.observedAt)>300000))?<> · {copy(state===PaidCreditState.Failed?"paid-credits.failed":"paid-credits.stale")}</>:null}</small>;
 return <section className="subscription-paid-credits" aria-label={copy("paid-credits.title")}><h3>{copy("paid-credits.title")}</h3>{state===PaidCreditState.Unsupported||state===PaidCreditState.Loading||!buckets.length?<p role="status">{unavailable}</p>:<ul>{buckets.map(b=><li key={b.id}><strong>{b.id}</strong><span>{value(b)}</span><small>{state===PaidCreditState.Failed?copy("paid-credits.failed"):Date.parse(b.observedAt)>now||now-Date.parse(b.observedAt)>300000?copy("paid-credits.stale"):copy("paid-credits.observed")} · <Timestamp active={active} value={b.observedAt}/></small></li>)}</ul>}</section>;
}
