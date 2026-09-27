import { object, type Document } from "./documents";

enum Kind { Character = "char_location", Page = "page_location", Block = "content_block_location", Search = "search_result_location", Web = "web_search_result_location" }
const exact = (v: Document, keys: string[]) => Object.keys(v).length === keys.length && keys.every((key) => Object.hasOwn(v, key));
const text = (v: unknown, max: number, required = false): v is string => typeof v === "string" && (!required || v.trim().length > 0) && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= max;
const count = (v: unknown): v is string => typeof v === "string" && /^(0|[1-9][0-9]{0,19})$/.test(v) && BigInt(v) <= 18446744073709551615n;
function location(v: Document, page: boolean) {
 return count(v.index) && count(v.start) && count(v.end) && BigInt(v.end) > BigInt(v.start) && (!page || BigInt(v.start) > 0n);
}
export function validClaudeCitation(value: unknown): boolean {
 const v=object(value), kind=v.kind as Kind;
 if(!Object.values(Kind).includes(kind) || !text(v.text,256<<10) || (v.title!==null && !text(v.title,16<<10))) return false;
 if(kind===Kind.Web) return exact(v,["kind","text","title","web"]) && exact(object(v.web),["url"]) && text(object(v.web).url,16<<10,true);
 if(kind===Kind.Search) { const s=object(v.search);return exact(v,["kind","text","title","search"]) && exact(s,["index","start","end","source"]) && location(s,false) && text(s.source,16<<10,true); }
 const d=object(v.document), file=Object.hasOwn(d,"file"), f=object(d.file);
 return exact(v,["kind","text","title","document"]) && exact(d,["index","start","end",...(file?["file"]:[])]) && location(d,kind===Kind.Page) && (!file || exact(f,["value"]) && (f.value===null || text(f.value,1024,true)));
}
function collection(value: unknown): boolean {
 if(value===null) return true;
 const v=object(value);
 return exact(v,["null","entries"]) && typeof v.null==="boolean" && (v.null ? v.entries===null : Array.isArray(v.entries) && v.entries.length<=1024 && v.entries.every(validClaudeCitation));
}
const entries = (value: unknown): Document[] => Array.isArray(object(value).entries) ? object(value).entries as Document[] : [];
function citationKey(v: Document) {
 const d=object(v.document),s=object(v.search);
 return JSON.stringify([v.kind,v.text,v.title,v.document==null?null:[d.index,d.start,d.end,Object.hasOwn(d,"file")?[object(d.file).value]:[]],v.search==null?null:[s.index,s.start,s.end,s.source],v.web==null?null:object(v.web).url]);
}
export function claudeCitationHistoryBytes(value: unknown) {
 // Match Go's escaped JSON retention bound, including HTML-significant text.
 return new TextEncoder().encode(JSON.stringify(value).replace(/[<>&\u2028\u2029]/g,c=>`\\u${c.charCodeAt(0).toString(16).padStart(4,"0")}`)).length;
}
export function validClaudeCitationHistory(value: unknown, state: string): boolean {
 const h=object(value);
 if(!exact(h,["initial","deltas","completed",...(Object.hasOwn(h,"completion")?["completion"]:[])]) || !collection(h.initial) || !collection(h.completed) || !Array.isArray(h.deltas) || !h.deltas.every(validClaudeCitation) || entries(h.initial).length+h.deltas.length>1024 || h.initial===null && h.deltas.length===0 && h.completed===null || claudeCitationHistoryBytes(value)>(256<<10)) return false;
 if(state==="streaming") return h.completed===null && !Object.hasOwn(h,"completion");
 if(state!=="completed" && state!=="stopped") return false;
 const expected=[...entries(h.initial),...h.deltas as Document[]], actual=entries(h.completed);
 if(h.completion==="matched") return expected.length===actual.length && expected.every((v,i)=>citationKey(v)===citationKey(actual[i]));
 return h.completion==="omitted-by-native" && entries(h.initial).length===0 && h.deltas.length>0 && h.completed!==null && object(h.completed).null===false && actual.length===0;
}
const labels = { [Kind.Character]: "Document character range", [Kind.Page]: "Document page range", [Kind.Block]: "Document block range", [Kind.Search]: "Search result block range", [Kind.Web]: "Web source" };
function Citation({value}: {value: Document}) {
 const kind=value.kind as Kind,d=object(value.document),s=object(value.search),ref=kind===Kind.Search?s:d;
 return <li><p>{labels[kind]}</p><pre>{value.text as string}</pre><dl>
  <dt>Original title</dt><dd>{value.title===null?"Not reported":value.title as string}</dd>
  {kind===Kind.Web?<><dt>Original URL</dt><dd><pre>{object(value.web).url as string}</pre></dd></>:<>
   <dt>{kind===Kind.Search?"Search result index":"Document index"}</dt><dd>{ref.index as string}</dd>
   <dt>Start</dt><dd>{ref.start as string}</dd><dt>End</dt><dd>{ref.end as string}</dd>
   {kind===Kind.Search?<><dt>Original source</dt><dd><pre>{s.source as string}</pre></dd></>:<><dt>Native file reference</dt><dd>{d.file===undefined?"Not reported":object(d.file).value===null?"Explicitly null":object(d.file).value as string}</dd></>}
  </>}
 </dl></li>;
}
function Collection({value,label}: {value: unknown;label: string}) {
 return <details><summary>{label}</summary>{value===null?<p>Not reported</p>:object(value).null===true?<p>Explicitly null</p>:entries(value).length===0?<p>Empty native list</p>:<ol>{entries(value).map((entry,index)=><Citation key={index} value={entry}/>)}</ol>}</details>;
}
export function NativeClaudeCitations({value}: {value: Document}) {
 return <details><summary>Original citations</summary>
  <p>Locations refer to native sources. These references do not open files or fetch content.</p>
  {value.completion==="omitted-by-native"?<p>Claude omitted streamed citations from its completed block. The original streamed references remain below.</p>:null}
  <Collection value={value.initial} label="Initial native citations"/>
  <details><summary>Streamed citations</summary><ol>{(value.deltas as Document[]).map((v,index)=><Citation key={index} value={v}/>)}</ol></details>
  {value.completion!==undefined?<Collection value={value.completed} label="Completed native citations"/>:null}
 </details>;
}
