// SPDX-License-Identifier: Apache-2.0
import "./command-menu.css";
import { Command, defaultFilter } from "cmdk";
import { createPortal, flushSync } from "react-dom";
import { useId, useLayoutEffect, useRef, useState } from "react";
import { newRequestId } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { DialogSurface } from "./ui";
import { availableShortcutTarget } from "./shortcuts";
import { useCommandMenuOwner, useCommandMenuKeyDown } from "./shortcut-provider";
import { Surface } from "./surface";
import { settingsCategories, settingsGroups, type SettingsNavigationEntry } from "./settings";
import { settingsSearchTargets } from "./settings-search";

export enum CommandGroup { Navigate = "navigate", Create = "create", Settings = "settings", Help = "help" }
export enum CommandId { Sessions = "sessions", PullRequests = "pull-requests", Usage = "usage", Schedules = "schedules", Inbox = "inbox", Search = "search", Settings = "settings", NewSession = "new-session", NewGeneralChat = "new-general-chat", NewProject = "new-project", Shortcuts = "shortcuts" }
export interface MenuCommand { value: string; group: CommandGroup; label: string; help?: string; enabled?: boolean; reason?: string; run: () => void }
export interface CommandActions { navigate: (surface: Surface) => void; navigateHeader: (surface: Surface.Inbox | Surface.Search) => void; openSettings: (entry?: SettingsNavigationEntry) => void; newSession: () => void; newGeneralChat: () => void; newProject: () => void; help: () => void }
/** This catalog contains bundled metadata and original callbacks only. */
export function applicationCommands(actions: CommandActions): MenuCommand[] {
 const navigations: [CommandId, Surface, string][] = [
  [CommandId.Sessions,Surface.Sessions,copy("sidebar.sessions_6fa3cb")], [CommandId.PullRequests,Surface.PullRequests,copy("sidebar.pullRequests_d9e3f2")], [CommandId.Usage,Surface.Usage,copy("sidebar.usage_8d5982")], [CommandId.Schedules,Surface.Schedules,copy("sidebar.schedules_221ff1")], [CommandId.Inbox,Surface.Inbox,copy("sidebar.inbox_94835e")], [CommandId.Search,Surface.Search,copy("sidebar.search_49c266")], [CommandId.Settings,Surface.Settings,copy("sidebar.settings_74a883")],
 ];
 return [
  ...navigations.map(([value,surface,label])=>({value:`navigate:${value}`,group:CommandGroup.Navigate,label,run:()=>surface===Surface.Settings?actions.openSettings():surface===Surface.Search||surface===Surface.Inbox?actions.navigateHeader(surface):actions.navigate(surface)})),
  ...[[CommandId.NewSession,copy("shortcuts.newSession"),actions.newSession],[CommandId.NewGeneralChat,copy("sidebar.newGeneralChat"),actions.newGeneralChat],[CommandId.NewProject,copy("sidebar.newProject_a41eb2"),actions.newProject]].map(([id,label,run])=>({value:`create:${id}`,group:CommandGroup.Create,label:label as string,run:run as ()=>void})),
  ...settingsGroups.flatMap(group=>group.categories.flatMap(category=>{
   const metadata=settingsCategories[category];
   return [{value:`settings:${category}:category`,group:CommandGroup.Settings,label:metadata.label,help:metadata.description,run:()=>actions.openSettings({category,generation:newRequestId()})},...(settingsSearchTargets[category]??[]).map(target=>({value:`settings:${category}:${target.target}`,group:CommandGroup.Settings,label:`${metadata.label} › ${copy(target.label)}`,help:target.help?copy(target.help):"",run:()=>actions.openSettings({category,target:target.target,generation:newRequestId()})}))];
  })),
  {value:`help:${CommandId.Shortcuts}`,group:CommandGroup.Help,label:copy("shortcuts.title"),run:actions.help},
 ];
}
const groupNames = { [CommandGroup.Navigate]: "command-menu.navigate", [CommandGroup.Create]: "command-menu.create", [CommandGroup.Settings]: "sidebar.settings_74a883", [CommandGroup.Help]: "command-menu.help" } as const;
export function CommandMenu({commands,close}:{commands:readonly MenuCommand[];close:()=>void}) {
 const locale = useLocale();
 const routeKey = useCommandMenuKeyDown();
 const [query,setQuery]=useState(""),[selection,setSelection]=useState("");
 const id=useId(),dialog=useRef<HTMLDialogElement>(null),input=useRef<HTMLInputElement>(null),composing=useRef(false),actionDismissal=useRef(false),owner=useCommandMenuOwner();
 useLayoutEffect(()=>{
  const opener=document.activeElement instanceof HTMLElement?document.activeElement:null,node=dialog.current!;
  node.showModal(); if(owner)owner.current=node;input.current?.focus();
  return()=>{if(owner?.current===node)owner.current=undefined;node.close();if(actionDismissal.current)return;
   const target=availableShortcutTarget(opener)&&(opener!.tabIndex>=0||opener!.hasAttribute("tabindex")||opener!.isContentEditable)?opener:document.querySelector<HTMLElement>("#main");
   if(availableShortcutTarget(target))target?.focus({preventScroll:true});
  };
 },[]);
 const activate=(command:MenuCommand)=>{
  if(composing.current||command.enabled===false||actionDismissal.current)return;
  actionDismissal.current=true;flushSync(close);command.run();
 };
 return createPortal(<DialogSurface ref={dialog} className="command-menu" aria-modal="true" aria-labelledby={`${id}-title`} onCancel={event=>{event.stopPropagation();close();}} onClick={event=>{if(event.target===event.currentTarget){const rect=event.currentTarget.getBoundingClientRect();if(event.clientX<rect.left||event.clientX>rect.right||event.clientY<rect.top||event.clientY>rect.bottom)close();}}} onKeyDownCapture={event=>{
  routeKey(event);
  if(event.defaultPrevented)return;
  if(event.key==="Enter"&&(composing.current||event.nativeEvent.isComposing||event.keyCode===229)){event.preventDefault();event.stopPropagation();}
  if(event.key==="Escape"){event.preventDefault();event.stopPropagation();close();}
  if(event.key==="Tab"){
   const nodes=[...event.currentTarget.querySelectorAll<HTMLElement>('input,button,[tabindex="0"]')].filter(node=>availableShortcutTarget(node)&&node.tabIndex>=0),first=nodes[0],last=nodes.at(-1);
   if(first&&last&&(!event.currentTarget.contains(document.activeElement)||(event.shiftKey?document.activeElement===first:document.activeElement===last))){event.preventDefault();(event.shiftKey?last:first).focus();}
  }
 }}>
  <header><h2 id={`${id}-title`}>{copy("command-menu.title")}</h2><button type="button" aria-label={copy("command-menu.close")} onClick={close}>{copy("ui.close_7d9eb7")}</button></header>
  <Command loop shouldFilter={Boolean(query.trim())} label={copy("command-menu.title")} value={selection} onValueChange={setSelection} filter={(value,search)=>{
   const item=commands.find(command=>command.value===value);return item?defaultFilter(item.label.normalize("NFC"),search.normalize("NFC"),item.help?[item.help.normalize("NFC")]:[]):0;
  }}>
   <Command.Input ref={input} value={query} onValueChange={setQuery} aria-label={copy("command-menu.search")} placeholder={copy("command-menu.search")} onCompositionStart={()=>{composing.current=true;}} onCompositionEnd={()=>{composing.current=false;}} />
   {/* cmdk 1.1.1 caches metadata by value and reorders DOM nodes. Refresh only
       result rows on locale/empty transitions; retain input/query/focus. */}
   <Command.List><Command.Empty>{copy("command-menu.empty")}</Command.Empty>{Object.values(CommandGroup).map(group=><Command.Group key={`${group}:${locale}:${query.trim() ? "search" : "all"}`} heading={copy(groupNames[group])}>{commands.filter(command=>command.group===group).map(command=><Command.Item key={command.value} value={command.value} keywords={[command.label,command.help??""]} disabled={command.enabled===false} onSelect={()=>activate(command)}><span>{command.label}</span>{command.enabled===false?<small>{command.reason??copy("shortcuts.unavailable")}</small>:null}</Command.Item>)}</Command.Group>)}</Command.List>
  </Command>
 </DialogSurface>,document.body);
}
