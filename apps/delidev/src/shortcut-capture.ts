// SPDX-License-Identifier: Apache-2.0
import { invoke, isTauri } from "@tauri-apps/api/core";
export enum CaptureOperation { Begin="begin", Inspect="inspect", End="end" }
export enum CaptureStatus { Active="active", Expired="expired", Released="released" }
interface Receipt {token:string;status:CaptureStatus;remaining_ms:number}
export type CaptureBridge=(operation:CaptureOperation,token:string,revision:number)=>Promise<unknown>;
const native:CaptureBridge=(operation,token,expectedRevision)=>isTauri()?invoke("shortcut_capture_native",{operation,token,expectedRevision}):Promise.resolve({token,status:operation===CaptureOperation.End?CaptureStatus.Released:CaptureStatus.Active,remaining_ms:15000});
async function bounded(request:Promise<unknown>):Promise<unknown> {
 let timer:ReturnType<typeof setTimeout>|undefined;
 try{return await Promise.race([request,new Promise((_,reject)=>{timer=setTimeout(()=>reject(Error("Capture outcome unavailable")),2000);})]);}finally{clearTimeout(timer);}
}
function receipt(raw:unknown,token:string):Receipt {
 if(!raw||typeof raw!=="object")throw Error("Invalid capture receipt");const v=raw as Receipt;
 if(v.token!==token||!Object.values(CaptureStatus).includes(v.status)||!Number.isInteger(v.remaining_ms)||v.remaining_ms<0||v.remaining_ms>15000)throw Error("Invalid capture receipt");return v;
}
// Retain uncertain admission across category disposal. A new editor must settle
// this exact original token before another explicit capture can be admitted.
export class ShortcutCapture {
 private owned?:{token:string;revision:number}; private pending?:Promise<unknown>;
 constructor(private bridge:CaptureBridge=native){}
 async begin(revision:number):Promise<{deadline:number;token:string}>{
  if(this.owned||this.pending)throw Error("Original capture requires recovery");
  const token=crypto.randomUUID();const owner={token,revision};this.owned=owner;const started=performance.now();
  const request=this.bridge(CaptureOperation.Begin,token,revision);this.pending=request;
  try {const result=receipt(await bounded(request),token);if(result.status!==CaptureStatus.Active)throw Error("Capture admission expired");if(this.owned!==owner)throw Error("Original capture retired");return {deadline:started+result.remaining_ms-2000,token};}finally{this.pending=undefined;}
 }
 async end(expectedToken?:string):Promise<void>{
  const owner=this.owned;if(!owner||expectedToken&&owner.token!==expectedToken)return;

  const inspected=receipt(await bounded(this.bridge(CaptureOperation.Inspect,owner.token,owner.revision)),owner.token);
  {const ended=receipt(await bounded(this.bridge(CaptureOperation.End,owner.token,owner.revision)),owner.token);if(ended.status!==CaptureStatus.Released)throw Error("Capture retirement unavailable");}
  if(this.owned===owner)this.owned=undefined;
 }
}
export const shortcutCapture=new ShortcutCapture();
