import { useShortcuts } from "./shortcut-provider";
import { ShortcutId, ShortcutInput } from "./shortcuts";
import { Surface } from "./surface";
import { copy, useLocale } from "./localization";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { SessionQuery } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { Comparison, diffQuery, readDiff, readDiffOptions, referenceKey, referenceLabel, type ComparisonIdentity, type Reference } from "./session-diff-model";
import { readReviewContext, type ReviewFile } from "./local-review-model";
import { workspaceReadOptions } from "./session-files";
import { Problem } from "./ui";
import { LocalReviewRecovery, LocalReviews } from "./local-reviews";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { useRetainedMutationIntents } from "./mutation";
import "./session-diff.css";

export function SessionDiff({ sessionId, worktree, close, selected, openComparison }: { sessionId: string; worktree: boolean; close: () => void; selected?: ComparisonIdentity; openComparison?: (value:ComparisonIdentity)=>void }) {
 useLocale();
 const panelRoot=useRef<HTMLElement>(null);
 const shortcuts=useShortcuts([{id:ShortcutId.DiffClose,scope:Surface.Sessions,label:"shortcuts.closeDiff",bindings:[{key:"Escape"}],target:panelRoot,input:ShortcutInput.Target,run:close}]);
 const roots=useQuery(SessionQuery.readSessionWorkspace,{sessionId,queryJson:encode({operation:"roots"})},workspaceReadOptions);
 const [repository,setRepository]=useState<string>(),[acceptedDeletionId,setAcceptedDeletionId]=useState<string>();
 const pending=useRetainedMutationIntents("review:").filter(intent=>intent.key.includes(`:${sessionId}`) && !intent.key.startsWith("review:delete:"));
 const available=roots.data?.roots.filter(root=>root.repository_id);
 const selectedRepository=repository ?? selected?.repository ?? available?.find(root=>root.primary)?.repository_id ?? available?.[0]?.repository_id;
 useEffect(()=>{setRepository(undefined);},[selected?.repository]);
 return <aside ref={panelRoot} aria-keyshortcuts={shortcuts.aria(ShortcutId.DiffClose)} className="session-files session-diff" aria-label={copy("session-diff.sessionGitDiff_d6706d")} onKeyDown={shortcuts.onKeyDown}>
 <Problem error={roots.error}/>{roots.isPending?<p role="status">{copy("session-diff.loadingWorkspaceRoots_0d8c0f")}</p>:null}
 {roots.error?<button disabled={roots.isFetching} onClick={()=>void roots.refetch()}>{copy("session-diff.retryWorkspaceRoots_6b5165")}</button>:null}
 {selectedRepository ? <RepositoryDiff key={`${selectedRepository}:${selected?.comparison}:${selected?.path}:${referenceKey(selected?.base_ref)}`} sessionId={sessionId} repository={selectedRepository} worktree={worktree} acceptedDeletionId={acceptedDeletionId} initial={selectedRepository===selected?.repository?selected:selected && {comparison:selected.comparison,path:selected.path}} openComparison={openComparison} close={close} repositoryControl={<label><span className="diff-control-label">{copy("session-diff.diffRepository_12d492")}</span><select value={selectedRepository} onChange={event=>openComparison && selected && selected.comparison!==Comparison.Branch?openComparison({repository:event.target.value,comparison:selected.comparison,path:selected.path}):setRepository(event.target.value)}>{available?.map(root=><option key={root.repository_id} value={root.repository_id}>{root.name}{root.primary?copy("session-diff.primary_b88564"):""}</option>)}</select></label>}/> : <><header><h2>{copy("session-diff.gitDiff_fa5e4e")}</h2><button onClick={close} aria-label={copy("session-diff.closeSessionDiff_43130b")}>{copy("session-diff.close_7d9eb7")}</button></header>{roots.data?<p>{copy("session-diff.thisWorkspaceHasNoPreparedGit_9235fc")}</p>:null}</>}
 {pending.length?<section aria-label={copy("session-diff.pendingReviews")}>{pending.map(intent=><p key={intent.key} role="status">{copy(intent.busy?"session-diff.waitingReview":"session-diff.uncertainReview")}</p>)}</section>:null}
 <LocalReviewRecovery sessionId={sessionId} onAccepted={setAcceptedDeletionId}/>
 </aside>;
}

function StructuredPatch({files}:{files:ReviewFile[]}) {
 return <div className="diff-files">{files.map(file=><section key={file.path}><h3>{file.path}</h3>{file.kind==="text"?<div className="diff-lines" tabIndex={0} role="region" aria-label={file.path}>{file.lines.map((line,i)=><div key={i} className={`diff-line ${line.old===undefined?"added":line.new===undefined?"removed":"context"}`}><span className="diff-line-number">{line.old ?? ""}</span><span className="diff-line-number">{line.new ?? ""}</span><span aria-hidden="true">{line.old===undefined?"+":line.new===undefined?"−":" "}</span><span>{line.text}</span></div>)}</div>:null}</section>)}</div>;
}

export function RepositoryDiff({sessionId,repository,worktree,acceptedDeletionId,initial,openComparison,repositoryControl,close}:{sessionId:string;repository:string;worktree:boolean;acceptedDeletionId?:string;initial?:{comparison:Comparison;path:string;base_ref?:Reference};openComparison?:(value:ComparisonIdentity)=>void;repositoryControl?:ReactNode;close?:()=>void}) {
 useLocale();
 const [comparison,setComparison]=useState(initial?.comparison ?? Comparison.Branch);
 const [path,setPath]=useState(initial?.path ?? "."),[pathDraft,setPathDraft]=useState(initial?.path ?? "."),[selectedBase,setBase]=useState<Reference|undefined>(initial?.base_ref);
 const menu=useRef<HTMLDetailsElement>(null),trigger=useRef<HTMLElement>(null),heading=useRef<HTMLHeadingElement>(null);
 const options=useQuery(SessionQuery.readSessionWorkspace,{sessionId,queryJson:encode({operation:"git-diff-options",repository_id:repository,path})},{...workspaceReadOptions,enabled:comparison===Comparison.Branch,select:r=>readDiffOptions(r.documentJson,repository,path)});
 const base=selectedBase ?? options.data?.default;
 const supported=Boolean(options.data && !options.error);
 const enabled=comparison!==Comparison.Branch || supported && Boolean(base) && (selectedBase!==undefined || Boolean(options.data?.default_available));
 const descriptorReady=!openComparison || Boolean(initial && (comparison!==Comparison.Branch || initial.base_ref));
 const query={operation:"git-diff",repository_id:repository,comparison,path,...(comparison===Comparison.Branch && supported && base?{base_ref:base}:{})};
 const result=useQuery(SessionQuery.readSessionWorkspace,{sessionId,queryJson:encode(query)},{...workspaceReadOptions,enabled:enabled && descriptorReady,select:r=>readDiff(r.documentJson,repository,comparison,path,comparison===Comparison.Branch?base:undefined)});
 const reviewContext=useQuery(SessionQuery.readSessionReviewContext,{sessionId,queryJson:encode(result.data?diffQuery(result.data):query)},{...workspaceReadOptions,enabled:Boolean(result.data && !result.error && !result.isFetching),select:r=>readReviewContext(r.documentJson,result.data!)});
 useEffect(()=>{if(result.data && !result.error && !result.isFetching)void reviewContext.refetch({cancelRefetch:false});},[result.data?.revision]);
 const pending=useRetainedMutationIntents(`review:`).filter(intent=>intent.key.includes(`:${sessionId}`));
 const choose=(value:ComparisonIdentity)=>{if(openComparison)openComparison(value);else{setComparison(value.comparison);setPath(value.path);setBase(value.base_ref);}};
 useEffect(()=>{heading.current?.focus();},[]);
 useEffect(()=>{if(openComparison && (!initial || initial.comparison===Comparison.Branch && !initial.base_ref) && supported && base && options.data?.default_available)openComparison({repository,comparison:Comparison.Branch,path,base_ref:base});},[openComparison,initial,supported,repository,path,base,options.data?.default_available]);
 return <>
 <header className="diff-navbar"><h2 ref={heading} tabIndex={-1}>{copy("session-diff.gitDiff_fa5e4e")}</h2>{repositoryControl}
 <label><span className="diff-control-label">{copy("session-diff.comparison_571527")}</span><select value={comparison} onChange={e=>choose({repository,comparison:e.target.value as Comparison,path,...(e.target.value===Comparison.Branch && base?{base_ref:base}:{})})}>
 <option value={Comparison.Branch}>{copy("session-diff.branchChanges")}</option><option value={Comparison.WorkingTree}>{copy("session-diff.uncommittedChanges")}</option><option value={Comparison.Staged}>{copy("session-diff.stagedChanges")}</option>{worktree?<option value={Comparison.Creation}>{copy("session-diff.sinceCreation")}</option>:null}</select></label>
 {comparison===Comparison.Branch?<label><span className="diff-control-label">{copy("session-diff.base_7b4736")}</span><select disabled={!supported} value={referenceKey(base)} onChange={e=>{const chosen=options.data?.choices.find(c=>referenceKey(c.reference)===e.target.value)?.reference;if(chosen)choose({repository,comparison,path,base_ref:chosen});}}><option value="">{copy("session-diff.selectBase")}</option>{options.data?.choices.map(c=><option key={referenceKey(c.reference)} value={referenceKey(c.reference)}>{referenceLabel(c.reference)}{c.configured?copy("session-diff.savedBase"):""}</option>)}</select></label>:null}
 <span className="diff-navbar-space"/><button type="button" disabled={result.isFetching || !enabled || !descriptorReady} onClick={()=>void result.refetch()} aria-label={copy("session-diff.refreshDiff_f700bc")} title={copy("session-diff.refreshDiff_f700bc")}>↻</button>
 <details className="diff-menu" ref={menu} onKeyDown={e=>{if(e.key==="Escape" && menu.current?.open){e.preventDefault();e.stopPropagation();menu.current.open=false;trigger.current?.focus();}}}><summary ref={trigger} aria-label={copy("session-diff.moreOptions")}>⋯</summary><div className="diff-menu-content"><form onSubmit={e=>{e.preventDefault();choose({repository,comparison,path:pathDraft,...(comparison===Comparison.Branch && base?{base_ref:base}:{})});if(menu.current)menu.current.open=false;trigger.current?.focus();}}><label>{copy("session-diff.relativeDiffPath_e67374")}<input value={pathDraft} onChange={e=>setPathDraft(e.target.value)} autoComplete="off" spellCheck={false}/></label><button>{copy("session-diff.comparePath_31d172")}</button></form>{close?<button onClick={close}>{copy("session-diff.close_7d9eb7")}</button>:null}</div></details>
 </header>
 {path!=="."?<p className="diff-path">{copy("session-diff.relativeDiffPath_e67374")}: <code>{path}</code></p>:null}
 {comparison===Comparison.Branch && options.isPending?<p role="status">{copy("session-diff.loadingOptions")}</p>:null}
 {comparison===Comparison.Branch && options.error?<><Problem error={options.error}/><p role="alert">{copy("session-diff.unsupportedBranch")}</p><button disabled={options.isFetching} onClick={()=>void options.refetch()}>{copy("session-diff.retryOptions")}</button></>:null}
 {comparison===Comparison.Branch && supported && !enabled?<p role="status">{copy("session-diff.missingBase")}</p>:null}
 <Problem error={result.error}/>{result.isFetching?<p role="status">{copy("session-diff.readingGitDiff_13bd34")}</p>:null}
 {result.error && result.data?<p role="alert">{copy("session-diff.staleObservation")}</p>:null}
 {result.data?<section className="git-comparison" aria-label={copy("session-diff.gitComparison_de5514")}>
 <Disclosure><DisclosureSummary>{copy("session-diff.observationDetails")}</DisclosureSummary><p>{copy("session-diff.untrackedFilesAreListedSeparatelySubmodules_49f75e")}</p><dl><dt>{copy("session-diff.base_7b4736")}</dt><dd>{result.data.base_ref?referenceLabel(result.data.base_ref):result.data.base==="empty-tree"?copy("session-diff.emptyGitTreeUnbornBranch_82b1a5"):result.data.base_object}</dd>{result.data.base_commit?<><dt>{copy("session-diff.baseCommit")}</dt><dd>{result.data.base_commit}</dd><dt>{copy("session-diff.mergeBase")}</dt><dd>{result.data.merge_base}</dd></>:null}<dt>{copy("session-diff.observedHead_075434")}</dt><dd>{result.data.head_commit ?? copy("session-diff.noCommitYet_c0724a")}</dd><dt>{copy("session-diff.diffRevision_5eddeb")}</dt><dd>{result.data.revision}</dd></dl></Disclosure>
 {result.data.patch ? reviewContext.data && !reviewContext.error && reviewContext.data.diff.revision===result.data.revision ? <><StructuredPatch files={reviewContext.data.files}/>{reviewContext.data.files.some(f=>f.kind==="non-line")?<pre tabIndex={0}>{result.data.patch}</pre>:null}</> : <pre tabIndex={0}>{result.data.patch}</pre> : <p>{copy("session-diff.noTrackedChangesInThisComparison_3c7169")}</p>}
 <Disclosure><DisclosureSummary>{copy("session-diff.untrackedFiles_be1f1a")} ({result.data.untracked.length})</DisclosureSummary>{result.data.untracked.length?<ul>{result.data.untracked.map(file=><li key={file}>{file}</li>)}</ul>:<p>{copy("session-diff.noUntrackedFilesInThisPath_bd7a62")}</p>}</Disclosure>
 </section>:null}
 {result.data?<Disclosure open={pending.length?true:undefined} onToggle={e=>{if(pending.length && !e.currentTarget.open)e.currentTarget.open=true;}}><DisclosureSummary>{copy("session-diff.localReview")}{pending.length?` (${pending.length})`:""}</DisclosureSummary><LocalReviews sessionId={sessionId} diff={result.data} reading={result.isFetching || Boolean(result.error)} acceptedDeletionId={acceptedDeletionId}/></Disclosure>:null}
 </>;
}
