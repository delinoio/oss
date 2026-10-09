// SPDX-License-Identifier: Apache-2.0
// Synthetic transcript presentation only: no native/account/business authority.
import { createRoot } from "react-dom/client";
import { useRef, useState } from "react";
import { EntityKind, ResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { create } from "@bufbuild/protobuf";
import { encode } from "./documents";
import { i18n } from "./localization";
import { TranscriptItem } from "./session";
import { ToolTurnTranscript } from "./tool-turn-transcript";
import { conversationProjection } from "./tool-turn-projection";
import "./themes.css";
import "./styles.css";
const args=new URLSearchParams(location.search);document.documentElement.dataset.theme=args.get('theme')??'light';void i18n.changeLanguage(args.get('language')??'en');
const sessionId='01960dcb-e1fa-7000-8000-000000000001',execution='01960dcb-e1fa-7000-8000-000000000002';
const record=(data:object,index:number,revision=1n)=>create(ResourceSchema,{id:`01960dcb-e1fa-7000-8000-${String(index).padStart(12,'0')}`,sessionId,kind:EntityKind.MESSAGE,schemaVersion:1,revision,documentJson:encode(data)});
const tool=(name:string,state='streaming')=>({role:'tool',state,text:'',execution_id:execution,native_thread_id:'original-thread',native_turn_id:'original-turn',tool:{started:{kind:name},completed:{status:state,command:{command:'echo  exact\n  original',aggregated_output:'aggregate\n  exact'}},output:'<script>inert()</script>\n  original'}});
const first=record(tool('command'),3),second=record(tool('patch'),5),tail=record(tool('shell'),7);
const pages=[[record({role:'assistant',text:'Before'},4),first],[record({role:'assistant',text:'Interleaved commentary'},6),second]];
function Fixture(){
 const root=useRef<HTMLDivElement>(null),[evicted,setEvicted]=useState(false),[revision,setRevision]=useState(1n),[reads,setReads]=useState<string[]>([]),[owner,setOwner]=useState(0);
 const query={pages:pages.map((rows,i)=>({token:i?'original-next':'',nextPageToken:i?'':'original-next',rows:rows.map(row=>conversationProjection(row,sessionId))})),payloadPages:pages.map((payload,i)=>({token:i?'original-next':'',payload})).filter((_,i)=>!evicted||i!==0),nextPageToken:'',restore:(token:string)=>{setReads(v=>[...v,token]);setEvicted(false);},measure:()=>{},protect:()=>{}};
 const updated={...first,revision,documentJson:encode(tool('command',revision===1n?'streaming':'complete'))};
 return <main style={{padding:8,maxWidth:720,minWidth:0}} data-kind={args.get('kind')}><button data-update onClick={()=>setRevision(v=>v+1n)}>Revise</button><button data-evict onClick={()=>setEvicted(true)}>Evict</button><button data-dispose onClick={()=>setOwner(v=>v+1)}>Dispose</button><button data-language onClick={()=>void i18n.changeLanguage(i18n.language==='en'?'ko':'en')}>Language</button><output data-reads data-tokens={JSON.stringify(reads)}>{reads.length}</output><div ref={root} className="transcript" style={{maxHeight:500,overflow:'auto'}}><ToolTurnTranscript key={owner} sessionId={sessionId} query={query} live={new Map<string,Resource>([[first.id,updated],[tail.id,tail]])} removed={new Set()} arrivals={[tail.id]} root={root} render={row=><TranscriptItem resource={row}/>}/></div><aside data-open-request><label>Original open question<input defaultValue="unsent original response"/></label><button data-response>Respond</button></aside><textarea aria-label="Original composer" defaultValue="retained draft"/></main>;
}
createRoot(document.getElementById('root')!).render(<Fixture/>);
