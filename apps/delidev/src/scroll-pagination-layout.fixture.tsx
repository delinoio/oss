// SPDX-License-Identifier: Apache-2.0
// Synthetic browser evidence. No native account, credential or mutation exists.
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { RepositoryRow } from "./repository-list";
import { encode } from "./documents";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useCallback, useLayoutEffect, useRef, useState } from "react";
import { usePaginationChain } from "./scroll-pagination-query";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { ScrollPicker } from "./scroll-picker";
import { copy, i18n } from "./localization";
import "./themes.css";
import "./styles.css";
import "./routing-preview.css";
import "./settings-task.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") ?? "en");
document.documentElement.dataset.theme = args.get("theme") ?? "light";
if (args.get("zoom") === "2") document.body.style.zoom = "2";
function Fixture() {
  const root = useRef<HTMLDivElement>(null);
  const [active, setActive] = useState(true), [selected, setSelected] = useState("off-page");
  const reads = useRef<string[]>([]);
  const reader = useCallback(async (token: string) => {
    reads.current.push(token); await new Promise(resolve => setTimeout(resolve, 20));
    const index = Number(token || 0), row = { id: `row-${index}`, revision: 1n, text: `Fixture ${index} ${"wrapping content ".repeat(10)}` };
    return { rows: [{ id: row.id, revision: row.revision }], payload: [row], nextPageToken: index < 4 ? String(index + 1) : "" };
  }, []);
  const query = usePaginationChain("synthetic-layout", active, reader, false);
  const picker = { ...query, append: query.append };
  return <main style={{ padding: 8, width: "100%", maxWidth: 600, minWidth: 0, margin: "auto" }}>
    <button onClick={query.reload}>Fixture Load</button><button onClick={() => setActive(value => !value)}>Fixture visibility</button>
    <input aria-label="Fixture composer" placeholder="Keep connected focus" />
    <output data-fixture-count>{query.rows.length}:{query.payloadPages.length}:{reads.current.length}</output>
    <div ref={root} hidden={!active} style={{ overflow: "auto", height: 260, border: "1px solid var(--border)", marginTop: 8 }}>
      <ScrollPayloadWindow query={query} root={root} active={active} identity={row => row.id} revision={row => row.revision}>{payload => payload.map(row => <article key={row.id} style={{ minHeight: 190, overflowWrap: "anywhere", padding: 12 }}><p>{row.text}</p><button>Fixture action {row.id}</button></article>)}</ScrollPayloadWindow>
      <ScrollContinuation query={query} root={root} active={active} label={copy("schedules.savedSchedules_97f381")} />
    </div>
    <ScrollPicker label="Fixture picker" value={selected} selectedLabel="Retained off-page identity" change={setSelected} active options={query.rows.map(row => ({ id: row.id, label: "Duplicate label", disabled: row.id === "row-2" }))} query={picker} />
  </main>;
}


// Real cards and payload owners with synthetic reads; no repository operation runs.
function RepositoryFixture() {
  const root = useRef<HTMLDivElement>(null);
  const reads = useRef(0);
  const [payloads] = useState(() => Array.from({ length: 5 }, (_, index) => Array.from({ length: 3 }, (_, offset) => create(ResourceSchema, {
      id: newRequestId(), kind: EntityKind.REPOSITORY, revision: 1n,
      schemaVersion: index === 4 && offset === 2 ? 99 : 1,
      documentJson: encode({ name: `Repository ${index}-${offset} ${"complete-name-".repeat(6)}`, checkouts: [{ machine_id: newRequestId(), path: `/fixture/${"long-path/".repeat(15)}` }] }),
    }))));
  const reader = useCallback(async (token: string) => {
    reads.current++;
    const index = Number(token || 0), payload = payloads[index]!;
    return { rows: payload.map(row => ({ id: row.id, revision: row.revision })), payload, nextPageToken: index < 4 ? String(index + 1) : "" };
  }, [payloads]);
  const query = usePaginationChain("repository-spacing-layout", true, reader, false);
  return <><main className="settings-content settings-repositories" style={{ padding: 16, width: "100%", minWidth: 0 }}>
    <button onClick={query.reload}>Fixture Load</button>
    <button disabled={!query.nextPageToken || Boolean(query.loading)} onClick={query.append}>Fixture Next</button>
    <input aria-label="Fixture composer" />
    <output data-repository-state>{JSON.stringify({ reads: reads.current, pages: query.pages.map(page => ({ token: page.token, height: page.height })), payloads: query.payloadPages.map(page => page.token) })}</output>
    <div ref={root} data-repository-scroll style={{ height: 360, overflow: "auto", containerType: "inline-size", containerName: "settings-body" }}>
      <div className="repository-list"><ScrollPayloadWindow query={query} root={root} active identity={row => row.id} revision={row => row.revision}>{payload => payload.map(row => <RepositoryRow key={row.id} row={row} edit={() => {}} remove={() => {}} />)}</ScrollPayloadWindow></div>
    </div>
  </main><section className="settings-content settings-projects"><div className="repository-list"><div data-payload-page="other"><article>Other category</article><article>Unchanged spacing</article></div></div></section>
  </>;
}

function OverlayFixture() {
 const owner=useRef<HTMLDialogElement>(null),nested=useRef<HTMLDialogElement>(null), [selected,setSelected]=useState("off-page");
 const kind=args.get("surface")??"ordinary", many=args.get("many")==="1";
 const reads=useRef<string[]>([]),fail=useRef(true), changes=useRef(0);
 const reader=useCallback(async(token:string)=>{reads.current.push(token);await new Promise(resolve=>setTimeout(resolve,20));if(token==="page-two" && fail.current){fail.current=false;throw new Error("Fixture second-page failure");}const start=token?20:0;return {rows:Array.from({length:20},(_,index)=>({id:`option-${start+index}`,revision:1n})),payload:Array.from({length:20},(_,index)=>({id:`option-${start+index}`,revision:1n})),nextPageToken:token?"":"page-two"};},[]);
 const pages=usePaginationChain("overlay-fixture",true,reader,false);
 const query=many?pages:{loaded:true,nextPageToken:"",append:()=>{},retry:()=>{},reload:()=>{}};
 const options=many?pages.rows.map((row,index)=>({id:row.id,label:`${index%2===0?"Duplicate label":"Long wrapping option ".repeat(6)}`,disabled:row.id==="option-2"})):[{id:"general",label:copy("documents.generalChat_f634bc")}];
 useLayoutEffect(()=>{if(kind==="dialog"){owner.current?.showModal();nested.current?.showModal();}return()=>{nested.current?.close();owner.current?.close();};},[kind]);
 const controls=<><div className="routing-project" data-measure-policy><div className="routing-project-controls"><div className="resource-choice" data-measure-picker><ScrollPicker label={copy("configuration-actions.project_985959")} value={selected} selectedLabel="Retained off-page identity" change={id=>{changes.current++;setSelected(id);}} active options={options} query={query}/></div><button data-measure-refresh type="button">{copy("routing-preview.refresh")}</button></div><p data-measure-helper>{copy("configuration-actions.generalChatNoProjectRestrictions_abff59")}</p></div><p data-measure-following>{copy("routing-preview.policy")}</p><output hidden data-picker-count>{pages.rows.length}</output><output hidden data-picker-query>{JSON.stringify({loaded:pages.loaded,next:pages.nextPageToken,loading:pages.loading,error:pages.error})}</output><output data-overlay-state>{selected}:{changes.current}:{reads.current.join(",")}</output>{many?<button onClick={pages.reload}>Fixture picker Load</button>:null}</>;
 if(kind==="dialog") return <dialog ref={owner} data-overlay-parent style={{width:Math.max(100,window.innerWidth/(args.get("zoom")==="2"?2:1)-24),height:Math.max(100,window.innerHeight/(args.get("zoom")==="2"?2:1)-24),padding:8}}><dialog ref={nested} className="settings-task-dialog" style={{width:Math.min(600,window.innerWidth/(args.get("zoom")==="2"?2:1)-32),maxHeight:Math.max(100,window.innerHeight/(args.get("zoom")==="2"?2:1)-48)}}><header className="settings-task-header" data-fixed-header>Fixture nested task</header><div className="settings-task-body" data-overlay-owner style={{height:300,overflow:"auto"}}><div style={{height:190}}/>{controls}<div style={{height:190}}/></div><footer className="settings-task-footer" data-fixed-footer><button onClick={()=>nested.current?.close()}>Fixture close task</button></footer></dialog></dialog>;
 return <main className={kind==="sidebar"?"sidebar-pane":""} data-overlay-owner style={{width:kind==="sidebar"?240:"100%",maxWidth:600,padding:12,minWidth:0,margin:"auto",height:kind==="sidebar"?"85dvh":"auto",overflow:kind==="sidebar"?"auto":"visible"}}>{controls}<div style={{height:40}}/></main>;
}
createRoot(document.getElementById("root")!).render(<QueryClientProvider client={new QueryClient()}>{args.get("repositories")==="1"?<RepositoryFixture/>:args.get("overlay")==="1"?<OverlayFixture/>:<Fixture/>}</QueryClientProvider>);
