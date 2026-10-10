// SPDX-License-Identifier: Apache-2.0
import "./native-function-output.css";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy, useLocale } from "./localization";
import { object } from "./documents";

const bounded = (value: unknown, max: number): value is string => typeof value === "string" && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && new TextEncoder().encode(value).length <= max;
const only = (value: Record<string, unknown>, keys: string[]) => Object.keys(value).every(key => keys.includes(key));
export function validFunctionOutput(progress: unknown): boolean {
 const p=object(progress),v=object(p.function_output);
 if(p.kind!=="codex-function-output"||!only(p,["kind","function_output"])||!only(v,["native_item_id","name","namespace","variant","text","content"])||!bounded(v.native_item_id,1024)||!v.native_item_id.trim()||!bounded(v.name,1024)||!v.name.trim()||!(v.namespace===null||bounded(v.namespace,1024)))return false;
 if(v.variant==="string")return bounded(v.text,256<<10)&&v.content===null;
 if(v.variant!=="structured"||Object.hasOwn(v,"text")||!Array.isArray(v.content)||v.content.length>1024)return false;
 return v.content.every(raw=>{
  const c=object(raw);
  if(c.type==="encrypted_content")return Object.keys(c).length===1;
  if(c.type==="input_text")return only(c,["type","text"])&&bounded(c.text,256<<10);
  if(c.type==="input_audio")return only(c,["type","audio_url"])&&bounded(c.audio_url,256<<10);
  return c.type==="input_image"&&only(c,["type","image_url","file_id","detail"])&&(Object.hasOwn(c,"image_url")!==Object.hasOwn(c,"file_id"))&&bounded(c.image_url??c.file_id,256<<10)&&(!Object.hasOwn(c,"detail")||["auto","low","high","original"].includes(c.detail as string));
 });
}
/** Original native references remain text: no image/audio/link element can
 * initiate a request or treat native metadata as a local media grant. */
export function NativeFunctionOutput({progress,state}:{progress:unknown;state:string}) {
 useLocale();
 if(state!=="complete"||!validFunctionOutput(progress))return <p role="status">{copy("session.functionOutputUnavailable")}</p>;
 const v=object(object(progress).function_output);
 return <Disclosure className="native-function-output" onKeyDown={event=>{if(event.key==="Escape"&&event.currentTarget.open){event.preventDefault();event.stopPropagation();event.currentTarget.open=false;event.currentTarget.querySelector("summary")?.focus()}}}><DisclosureSummary>{copy("session.functionOutput")} · {v.name as string}</DisclosureSummary>
 {v.namespace!==null?<p>{v.namespace as string}</p>:null}
 {v.variant==="string"?<pre>{v.text as string}</pre>:<ol>{(v.content as unknown[]).map((raw,index)=>{const c=object(raw);return <li key={index}>{c.type==="encrypted_content"?<span>{copy("session.encryptedOutput")}</span>:c.type==="input_text"?<pre>{c.text as string}</pre>:<><small>{c.type as string}</small><pre>{(c.image_url??c.file_id??c.audio_url) as string}</pre>{c.detail?<small>{c.detail as string}</small>:null}</>}</li>})}</ol>}
 </Disclosure>;
}
