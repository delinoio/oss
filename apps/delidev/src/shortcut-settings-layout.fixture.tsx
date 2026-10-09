// SPDX-License-Identifier: Apache-2.0
// Isolated device preference adapters; no native storage or product mutation runs.
import { createRoot } from "react-dom/client";
import { useState } from "react";
import { i18n, copy } from "./localization";
import { ShortcutPreferenceProvider, type ShortcutPreferenceSnapshot, type ShortcutPreferenceBridge } from "./shortcut-preference-controller";
import { ShortcutProvider, useGlobalShortcutAria, useShortcuts } from "./shortcut-provider";
import { ShortcutSettings } from "./shortcut-settings";
import { SettingsActionScope } from "./settings-action";
import { ShortcutId, ShortcutScope, globalShortcutBindings, ShortcutInput } from "./shortcuts";
import "./themes.css";
import "./styles.css";
import "./settings-presentation.css";
const args=new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language")??"en");
document.documentElement.dataset.theme=args.get("theme")??"light";
let snapshot:ShortcutPreferenceSnapshot={revision:1,overrides:{},problem:null};
const listeners=new Set<(raw:unknown)=>void>();
let writes=0;
const bridge:ShortcutPreferenceBridge={read:async()=>snapshot,update:async(overrides,revision)=>{writes++;if(revision!==snapshot.revision)return{...snapshot,problem:"changed"};snapshot={revision:revision+1,overrides,problem:null};for(const callback of listeners)callback(snapshot);return snapshot;},subscribe:async callback=>{listeners.add(callback);return()=>{listeners.delete(callback);};}};
function Fixture(){const [runs,setRuns]=useState(0);const aria=useGlobalShortcutAria(ShortcutId.NewSession);useShortcuts([{id:ShortcutId.NewSession,scope:ShortcutScope.Global,label:"shortcuts.newSession",input:ShortcutInput.Allow,bindings:globalShortcutBindings[ShortcutId.NewSession],run:()=>setRuns(value=>value+1)}]);return <main className="settings-content settings-shortcuts" style={{width:"100%",padding:24,minWidth:0}}><div className="settings-content-column"><button data-shortcut-action aria-keyshortcuts={aria}>Fixture action</button><output data-shortcut-state>{runs}:{writes}</output><h1>{copy("shortcuts.title")}</h1><p>{copy("shortcut-settings.scope")}</p><ShortcutSettings/></div></main>;}
(globalThis as unknown as Record<string,unknown>).__shortcutPreferencesFixture=true;
createRoot(document.getElementById("root")!).render(<ShortcutPreferenceProvider bridge={bridge}><ShortcutProvider><SettingsActionScope><Fixture/></SettingsActionScope></ShortcutProvider></ShortcutPreferenceProvider>);
