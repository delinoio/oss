import { copy, useLocale } from "./localization";
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
const labels = { get [Kind.Character]() { return copy("native-claude-citations.documentCharacterRange_0acfb2"); }, get [Kind.Page]() { return copy("native-claude-citations.documentPageRange_a5be82"); }, get [Kind.Block]() { return copy("native-claude-citations.documentBlockRange_dd02ea"); }, get [Kind.Search]() { return copy("native-claude-citations.searchResultBlockRange_89c759"); }, get [Kind.Web]() { return copy("native-claude-citations.webSource_7d8b46"); } };
function Citation({value}: {value: Document}) {
  useLocale();
 const kind=value.kind as Kind,d=object(value.document),s=object(value.search),ref=kind===Kind.Search?s:d;
 return <li><p>{labels[kind]}</p><pre>{value.text as string}</pre><dl>
  <dt>{copy("native-claude-citations.originalTitle_4c198f")}</dt><dd>{value.title===null?copy("native-claude-citations.notReported_adadfa"):value.title as string}</dd>
  {kind===Kind.Web?<><dt>{copy("native-claude-citations.originalUrl_b62a4e")}</dt><dd><pre>{object(value.web).url as string}</pre></dd></>:<>
   <dt>{kind===Kind.Search?copy("native-claude-citations.searchResultIndex_788333"):copy("native-claude-citations.documentIndex_ab06ed")}</dt><dd>{ref.index as string}</dd>
   <dt>{copy("native-claude-citations.start_e4bb9f")}</dt><dd>{ref.start as string}</dd><dt>{copy("native-claude-citations.end_f4db1e")}</dt><dd>{ref.end as string}</dd>
   {kind===Kind.Search?<><dt>{copy("native-claude-citations.originalSource_fdf375")}</dt><dd><pre>{s.source as string}</pre></dd></>:<><dt>{copy("native-claude-citations.nativeFileReference_6cbeef")}</dt><dd>{d.file===undefined?copy("native-claude-citations.notReported_adadfa"):object(d.file).value===null?copy("native-claude-citations.explicitlyNull_a8f253"):object(d.file).value as string}</dd></>}
  </>}
 </dl></li>;
}
function Collection({value,label}: {value: unknown;label: string}) {
  useLocale();
 return <details><summary>{label}</summary>{value===null?<p>{copy("native-claude-citations.notReported_adadfa")}</p>:object(value).null===true?<p>{copy("native-claude-citations.explicitlyNull_a8f253")}</p>:entries(value).length===0?<p>{copy("native-claude-citations.emptyNativeList_c4376f")}</p>:<ol>{entries(value).map((entry,index)=><Citation key={index} value={entry}/>)}</ol>}</details>;
}
export function NativeClaudeCitations({value}: {value: Document}) {
  useLocale();
 return <details><summary>{copy("native-claude-citations.originalCitations_ec8a0d")}</summary>
  <p>{copy("native-claude-citations.locationsReferToNativeSourcesThese_f537fe")}</p>
  {value.completion==="omitted-by-native"?<p>{copy("native-claude-citations.claudeOmittedStreamedCitationsFromIts_989f28")}</p>:null}
  <Collection value={value.initial} label={copy("native-claude-citations.initialNativeCitations_ec43fb")}/>
  <details><summary>{copy("native-claude-citations.streamedCitations_4cfef6")}</summary><ol>{(value.deltas as Document[]).map((v,index)=><Citation key={index} value={v}/>)}</ol></details>
  {value.completion!==undefined?<Collection value={value.completed} label={copy("native-claude-citations.completedNativeCitations_f0aab4")}/>:null}
 </details>;
}
