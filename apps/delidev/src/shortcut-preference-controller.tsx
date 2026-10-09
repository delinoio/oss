// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { parseShortcutOverrides, type ShortcutOverrides } from "./shortcut-preferences";
export enum ShortcutPreferenceProblem { Unavailable="unavailable", ReadFailed="read-failed", InvalidDocument="invalid-document", UnsupportedVersion="unsupported-version", WriteFailed="write-failed", OutcomeUnknown="outcome-unknown", Changed="changed" }
export enum ShortcutPreferenceOperation { Reading="reading", Saving="saving" }
export interface ShortcutPreferenceSnapshot { revision: number; overrides: ShortcutOverrides; problem: ShortcutPreferenceProblem | null }
export interface ShortcutPreferenceBridge { read:()=>Promise<unknown>; update:(overrides:ShortcutOverrides,revision:number)=>Promise<unknown>; subscribe:(changed:(snapshot:unknown)=>void)=>Promise<()=>void> }
const defaults:ShortcutPreferenceSnapshot={revision:0,overrides:{},problem:ShortcutPreferenceProblem.Unavailable};
const nativeBridge:ShortcutPreferenceBridge={read:()=>isTauri()?invoke("read_shortcut_preferences"):Promise.resolve(defaults),update:(overrides,revision)=>invoke("update_shortcut_preferences",{overrides,expectedRevision:revision}),subscribe:async changed=>isTauri()?listen<unknown>("shortcut-preferences-changed",event=>changed(event.payload)):()=>{}};
export function parseShortcutSnapshot(raw:unknown):ShortcutPreferenceSnapshot {
  if(!raw || typeof raw!=="object" || Array.isArray(raw)) throw new Error("Invalid shortcut snapshot");
  const data=raw as Record<string,unknown>;
  if(Object.keys(data).sort().join(",")!=="overrides,problem,revision" || !Number.isInteger(data.revision) || Number(data.revision)<0 || Number(data.revision)>0xffffffff || data.problem!==null && !Object.values(ShortcutPreferenceProblem).includes(data.problem as ShortcutPreferenceProblem)) throw new Error("Invalid shortcut snapshot");
  return {revision:Number(data.revision),overrides:parseShortcutOverrides(data.overrides),problem:data.problem as ShortcutPreferenceProblem|null};
}
interface Controller {snapshot:ShortcutPreferenceSnapshot;operation?:ShortcutPreferenceOperation;save:(overrides:ShortcutOverrides,revision:number)=>Promise<boolean>;reload:()=>void}
const Context=createContext<Controller>({snapshot:defaults,save:async()=>false,reload:()=>{}});
export const useShortcutPreferences=()=>useContext(Context);
export function ShortcutPreferenceProvider({children,bridge=nativeBridge}:{children:ReactNode;bridge?:ShortcutPreferenceBridge}) {
  const [snapshot,setSnapshot]=useState(defaults),current=useRef(defaults);
  const [operation,setOperation]=useState<ShortcutPreferenceOperation|undefined>(ShortcutPreferenceOperation.Reading);
  const running=useRef(false),generation=useRef(0),subscription=useRef<(()=>void)|undefined>(undefined);
  const accept=(raw:unknown)=>{const next=parseShortcutSnapshot(raw);if(next.revision<current.current.revision)return; if(next.revision===current.current.revision && JSON.stringify(next.overrides)!==JSON.stringify(current.current.overrides)) throw new Error("Contradictory shortcut revision");current.current=next;setSnapshot(next);};
  const failed=(problem:ShortcutPreferenceProblem)=>{current.current={...current.current,problem};setSnapshot(current.current);};
  const subscribe=async(owned:number)=>{const remove=await bridge.subscribe(raw=>{if(generation.current!==owned || current.current.problem && current.current.problem!==ShortcutPreferenceProblem.Unavailable)return;try{accept(raw);}catch{failed(ShortcutPreferenceProblem.OutcomeUnknown);}});if(generation.current!==owned){remove();return false;}subscription.current=remove;return true;};
  useEffect(()=>{const owned=++generation.current;running.current=true;setOperation(ShortcutPreferenceOperation.Reading);void(async()=>{try{if(!await subscribe(owned))return;const result=await bridge.read();if(generation.current===owned)accept(result);}catch{if(generation.current===owned)failed(ShortcutPreferenceProblem.ReadFailed);}finally{if(generation.current===owned){running.current=false;setOperation(undefined);}}})();return()=>{generation.current++;subscription.current?.();subscription.current=undefined;};},[bridge]);
  const run=async(overrides?:ShortcutOverrides,revision?:number)=>{
    if(running.current || overrides!==undefined && (current.current.problem || revision!==current.current.revision))return false;
    const owned=generation.current;running.current=true;setOperation(overrides===undefined?ShortcutPreferenceOperation.Reading:ShortcutPreferenceOperation.Saving);
    try{if(overrides!==undefined)parseShortcutOverrides(overrides);if(!subscription.current && !await subscribe(owned))return false;const result=overrides===undefined?await bridge.read():await bridge.update(structuredClone(overrides),revision!);if(generation.current!==owned)return false;accept(result);return !current.current.problem;}
    catch{if(generation.current===owned)failed(overrides===undefined?ShortcutPreferenceProblem.ReadFailed:ShortcutPreferenceProblem.OutcomeUnknown);return false;}
    finally{if(generation.current===owned){running.current=false;setOperation(undefined);}}
  };
  useEffect(()=>{const inspect=()=>{if(document.visibilityState==="visible"&&!current.current.problem)void run();};window.addEventListener("focus",inspect);document.addEventListener("visibilitychange",inspect);return()=>{window.removeEventListener("focus",inspect);document.removeEventListener("visibilitychange",inspect);};},[bridge]);
  return <Context.Provider value={{snapshot,operation,save:(overrides,revision)=>run(overrides,revision),reload:()=>{void run();}}}>{children}</Context.Provider>;
}
