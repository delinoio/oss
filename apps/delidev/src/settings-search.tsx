// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState, type RefObject } from "react";
import { copy, useLocale, type MessageKey } from "./localization";
import { SettingsCategory as Category } from "./settings-category";
import "./settings-search.css";
export enum SettingsSearchTarget {
 Category="category", Theme="theme", Language="language", DateFormat="date-format", AccountRouting="account-routing", DefaultRouting="default-routing", Network="network", Worktree="worktree", AutomaticFetch="automatic-fetch", Ci="remediation-ci_failure", Feedback="remediation-review_feedback", MergeConflict="remediation-merge_conflict", Session="remediation-session", Agent="remediation-agent", Runner="remediation-runner", Conflict="remediation-conflict", Attempts="remediation-attempts", Reviewers="remediation-reviewers", NotificationPreferences="notification-preferences", Questions="notification-questions", Outcomes="notification-outcomes", Delivery="notification-delivery", Export="export", Import="import", TransferGuidance="transfer-guidance", BackupDatabase="backup-database", BackupHistory="backup-history", ProviderInventory="provider-inventory", ProviderPresets="provider-presets", ProviderLocal="provider-local", ProviderCustom="provider-custom", LocalWorker="local-worker", Ssh="ssh", RunnerInventory="runner-inventory", GitProfiles="git-profiles", CurrentConnection="current-connection",
}
interface StaticTarget { target: SettingsSearchTarget; label: MessageKey; help?: MessageKey }
const target = (target: SettingsSearchTarget, label: MessageKey, help?: MessageKey): StaticTarget => ({ target, label, help });
// Only bundled presentation metadata belongs here. Never index resource values,
// options, native observations or workflow interiors.
export const settingsSearchTargets: Partial<Record<Category, readonly StaticTarget[]>> = {
 [Category.Appearance]: [target(SettingsSearchTarget.Theme,"appearance.theme_efb52e","appearance.systemFollowsThisComputerSAppearance_edf3db"),target(SettingsSearchTarget.Language,"language.title","language.scope"),target(SettingsSearchTarget.DateFormat,"date-format.title","date-format.scope")],
 [Category.ServerPreferences]: [target(SettingsSearchTarget.AccountRouting,"configuration-fields.accountRouting_0c3707"),target(SettingsSearchTarget.DefaultRouting,"configuration-fields.defaultAccountRouting_bb44ea","configuration-fields.usedByAgentWorkersThatInherit_4e05b2"),target(SettingsSearchTarget.Network,"network-settings.networkSettings_600f22")],
 [Category.GitWorkflow]: [target(SettingsSearchTarget.Worktree,"configuration-fields.worktreePreparation_24002c"),target(SettingsSearchTarget.AutomaticFetch,"configuration-fields.allowAutomaticFetchBeforeWorktreePreparation_6c9a8c","configuration-fields.fetchingRequiresBothThisServerPreference_10697f"),target(SettingsSearchTarget.Ci,"remediation-policy.extra.5ecef0b36ba3"),target(SettingsSearchTarget.Feedback,"remediation-policy.extra.feab8b0b6d7b"),target(SettingsSearchTarget.MergeConflict,"remediation-policy.extra.7f64e75c8c6e"),target(SettingsSearchTarget.Session,"remediation-policy.remediationSessionStrategy_225752","remediation-policy.reuseSelectsAnEligibleLinkedSession_c48674"),target(SettingsSearchTarget.Agent,"configuration-fields.remediationAgentWorker_6bee33"),target(SettingsSearchTarget.Runner,"configuration-fields.remediationRunnerDevice_c56ba6"),target(SettingsSearchTarget.Conflict,"remediation-policy.conflictResolutionStrategy_9753fe"),target(SettingsSearchTarget.Attempts,"remediation-policy.consecutiveAutomaticAttemptLimit_844605"),target(SettingsSearchTarget.Reviewers,"remediation-policy.automaticFeedbackReviewers_940eb7","remediation-policy.anyOneSelectorMayMatchIdentities_db62c3")],
 [Category.Notifications]: [target(SettingsSearchTarget.NotificationPreferences,"notification-settings.notifyThisClientAbout_db8955"),target(SettingsSearchTarget.Questions,"notification-settings.questionsAndApprovalRequests_e6c1b4","notification-settings.whenASessionNeedsYourAnswer_b52af9"),target(SettingsSearchTarget.Outcomes,"notification-settings.executionCompletionFailureAndInterruption_275b47","notification-settings.whenAnExecutionSucceedsFailsOr_fae45f"),target(SettingsSearchTarget.Delivery,"notification-settings.aboutNotificationDelivery_e8b4e9","notification-settings.aSubmittedNotificationDoesNotProve_0edca6")],
 [Category.Transfer]: [target(SettingsSearchTarget.Export,"configuration-transfer.exportConfiguration_0657bc","configuration-transfer.copyAPortableJsonDocumentFrom_777321"),target(SettingsSearchTarget.Import,"configuration-transfer.importConfiguration_8a507f"),target(SettingsSearchTarget.TransferGuidance,"configuration-transfer.beforeYouTransfer_1de385")],
 [Category.Backups]: [target(SettingsSearchTarget.BackupDatabase,"backups.databaseBackups_e6ded7"),target(SettingsSearchTarget.BackupHistory,"backups.operationHistory_93bf53")],
 [Category.Providers]: [target(SettingsSearchTarget.ProviderInventory,"provider-model-settings.apiProviderInventory_db530c","provider-model-settings.manageApiProvidersAndTheirAvailability_946ee7"),target(SettingsSearchTarget.ProviderPresets,"provider-model-settings.presets_954f93"),target(SettingsSearchTarget.ProviderLocal,"provider-model-settings.localApiServers_2052f0"),target(SettingsSearchTarget.ProviderCustom,"provider-model-settings.customProviders_52b22a")],
 [Category.ExecutionWorkers]: [target(SettingsSearchTarget.LocalWorker,"local-worker-controls.workerOnThisComputer_53d365","local-worker-controls.registrationIsSeparateFromStartupThe_f724cc"),target(SettingsSearchTarget.Ssh,"ssh-setup.setUpAWorkerOverSsh_9a1626"),target(SettingsSearchTarget.RunnerInventory,"settings.savedRunnerDevices_9e6092")],
 [Category.Integrations]: [target(SettingsSearchTarget.GitProfiles,"integrations.githubProfiles_e47e4e")],
 [Category.Diagnostics]: [target(SettingsSearchTarget.CurrentConnection,"settings.connections.current")],
};
export interface SettingsSearchCategory { category: Category; label: string; help: string }
export interface SettingsSearchResult { category: Category; target: SettingsSearchTarget; categoryLabel: string; label: string; help: string }
export interface SettingsSearchRequest { category: Category; target: SettingsSearchTarget; generation: string }
const normalized = (text: string) => text.normalize("NFC").toLowerCase();
export function matchSettings(query: string, categories: readonly SettingsSearchCategory[]): SettingsSearchResult[] {
 const tokens = normalized(query).trim().split(/\s+/).filter(Boolean);
 if (!tokens.length) return [];
 return categories.flatMap(category => [target(SettingsSearchTarget.Category,"settings.search.category"),...(settingsSearchTargets[category.category]??[])].map(entry => ({category:category.category,target:entry.target,categoryLabel:category.label,label:copy(entry.label),help:entry.help?copy(entry.help):entry.target===SettingsSearchTarget.Category?category.help:""})).filter(entry => tokens.every(token => normalized(`${entry.categoryLabel} ${entry.label} ${entry.help}`).includes(token))));
}
export function SettingsSearch({ categories, select }: { categories: readonly SettingsSearchCategory[]; select: (result: SettingsSearchResult) => void }) {
 useLocale();
 const [query,setQuery]=useState(""); const input=useRef<HTMLInputElement>(null),composing=useRef(false);
 const searching=query.trim()!=="",matches=matchSettings(query,categories);
 const ime=(event:React.KeyboardEvent) => composing.current || event.nativeEvent.isComposing || event.keyCode===229;
 return <div className="settings-search" data-searching={searching||undefined}>
  <label>{copy("settings.search.label")}<input ref={input} type="search" value={query} maxLength={256} onChange={event=>setQuery(event.target.value)} onCompositionStart={()=>{composing.current=true;}} onCompositionEnd={()=>{composing.current=false;}} onKeyDown={event=>{if(ime(event)&&event.key==="Enter")event.preventDefault();}} /></label>
  <button type="button" disabled={!query} onClick={()=>{setQuery("");input.current?.focus();}}>{copy("settings.search.clear")}</button>
  {searching?<div className="settings-search-results" aria-label={copy("settings.search.label")}>{matches.length?matches.map(result=><button type="button" key={`${result.category}:${result.target}`} data-settings-search-result={result.target} onKeyDown={event=>{if((event.key==="Enter"||event.key===" ")&&ime(event))event.preventDefault();}} onClick={()=>{if(!composing.current)select(result);}}><span>{result.categoryLabel} › {result.label}</span></button>):<p role="status">{copy("settings.search.empty")}</p>}</div>:null}
 </div>;
}
/** A navigation generation owns one presentation-only focus; polling cannot repeat it. */
export function SettingsSearchFocus({request,category,root}:{request?:SettingsSearchRequest;category:Category;root:RefObject<HTMLElement|null>}) {
 const locale=useLocale(); const [unavailable,setUnavailable]=useState(false);
 const handled=useRef<{generation:string;locale:string;consumed:boolean}>(undefined);
 useEffect(()=>{
  if(!request){handled.current=undefined;setUnavailable(false);return;}
  // Locale changes clean up an armed wait without replaying this generation.
  if(handled.current?.generation===request.generation){
   if(handled.current.locale!==locale)handled.current.consumed=true;
   if(handled.current.consumed)return;
  }
  handled.current={generation:request.generation,locale,consumed:false};setUnavailable(false);
  if(request.category!==category||!root.current)return;
  const scope=root.current; let pending=true,frame=0;
  const dispose=()=>{pending=false;observer.disconnect();cancelAnimationFrame(frame);};
  const cancel=()=>{if(handled.current?.generation===request.generation)handled.current.consumed=true;dispose();};
  const intent=(event:Event)=>{if(event.type==="keydown"&&!['Tab','Enter',' ','Escape','ArrowUp','ArrowDown','ArrowLeft','ArrowRight'].includes((event as KeyboardEvent).key))return;cancel();};
  const focus=(node:HTMLElement)=>{cancel();if(node.tabIndex<0)node.tabIndex=-1;node.focus({preventScroll:true});node.scrollIntoView?.({block:"nearest",inline:"nearest"});};
  const attempt=()=>{
   if(!pending||!scope.isConnected)return;
   if(scope.closest('[inert], [aria-hidden="true"]')||document.querySelector('dialog[open]:not([role="region"])')){cancel();return;}
   const heading=scope.querySelector<HTMLElement>('h1');
   const node=request.target===SettingsSearchTarget.Category?heading:scope.querySelector<HTMLElement>(`[data-settings-search-target="${request.target}"]`);
   if(node&&!node.closest('dialog, [hidden], [inert]')){
    const details=node.closest<HTMLDetailsElement>('details.server-remediation-details');if(details)details.open=true;
    if(node.closest('details:not([open])')&&node.tagName!=="SUMMARY"){cancel();return;}
    focus(node);return;
   }
   if(scope.querySelector('[data-settings-search-pending="true"]'))return;
   if(heading){setUnavailable(true);focus(heading);}
  };
  const observer=new MutationObserver(()=>{cancelAnimationFrame(frame);frame=requestAnimationFrame(attempt);});
  observer.observe(scope,{subtree:true,childList:true,attributes:true,attributeFilter:['data-settings-search-pending','inert','hidden']});
  document.addEventListener('pointerdown',intent,true);document.addEventListener('keydown',intent,true);document.addEventListener('focusin',intent,true);window.addEventListener('resize',cancel);
  frame=requestAnimationFrame(attempt);
  // Strict Mode cleanup disposes the observer without consuming an unhandled target.
  return()=>{dispose();document.removeEventListener('pointerdown',intent,true);document.removeEventListener('keydown',intent,true);document.removeEventListener('focusin',intent,true);window.removeEventListener('resize',cancel);};
 },[request?.generation,category,root,locale]);
 return unavailable?<p className="settings-search-unavailable" role="status">{copy("settings.search.unavailable")}</p>:null;
}
