// SPDX-License-Identifier: Apache-2.0
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { copy, useLocale } from "./localization";
import { bindingKeys, ShortcutId, shortcutPlatform, type ShortcutBinding } from "./shortcuts";
import { captureShortcut, customizationBindings, ShortcutGroup, ShortcutOverrideState, shortcutCatalog, editableShortcutCatalog, readOnlyShortcutCatalog, shortcutConflicts, type ShortcutOverrides } from "./shortcut-preferences";
import { ShortcutPreferenceOperation, useShortcutPreferences } from "./shortcut-preference-controller";
import "./shortcut-settings.css";
const serialize = (value: ShortcutOverrides) => JSON.stringify(Object.entries(value).sort(([a],[b]) => a.localeCompare(b)));
export function ShortcutSettings() {
  useLocale();
  const { snapshot, operation, save, reload } = useShortcutPreferences();
  const [draft, setDraft] = useState(snapshot.overrides), [baseline, setBaseline] = useState(snapshot);
  const [capturing, setCapturing] = useState<ShortcutId>(), [invalid, setInvalid] = useState(false), [saved, setSaved] = useState(false);
  const restoreFocus = useRef(false);
  const opener = useRef<HTMLButtonElement|null>(null), mounted = useRef(false), platform = shortcutPlatform();
  const dirty = serialize(draft) !== serialize(baseline.overrides), conflict = dirty && baseline.revision !== snapshot.revision;
  const locked = Boolean(operation || snapshot.problem || conflict);
  const conflicts = shortcutConflicts(draft);
  useEffect(() => { mounted.current=true; return () => { mounted.current=false; }; }, []);
  useEffect(() => {
    if (!dirty || saved) { setBaseline(snapshot); setDraft(snapshot.overrides); setSaved(false); }
  }, [snapshot, saved]);
  const cancelCapture = () => { restoreFocus.current=true; setCapturing(undefined); setInvalid(false); };
  useLayoutEffect(()=>{if(!capturing&&restoreFocus.current){restoreFocus.current=false;const button=opener.current;if(button?.isConnected&&!button.matches(":disabled")&&!button.closest("[hidden],[inert]"))button.focus({preventScroll:true});}},[capturing]);
  useEffect(() => {
    if (!capturing || locked) { if(capturing)cancelCapture(); return; }
    const action = capturing;
    const capture = (event:KeyboardEvent) => {
      // Capture owns its event before application dispatch or native editing.
      event.preventDefault();event.stopImmediatePropagation();
      if(event.key==="Escape"&&!event.isComposing&&event.keyCode!==229&&!event.repeat){cancelCapture();return;}
      const chord=captureShortcut(event,platform);
      if(!chord){setInvalid(true);return;}
      if(!mounted.current)return;
      setDraft(previous=>({...previous,[action]:{state:ShortcutOverrideState.Binding,chord}}));cancelCapture();
    };
    document.addEventListener("keydown",capture,true);
    return()=>document.removeEventListener("keydown",capture,true);
  },[capturing,locked,platform]);
  const bindingLabel = (bindings: readonly ShortcutBinding[]) => bindings.length ? bindings.map(binding => bindingKeys(binding,platform).join(" + ")).join(` ${copy("shortcuts.or")} `) : copy("shortcut-settings.disabled");
  const restore = (id:ShortcutId) => setDraft(previous=>{const next={...previous};delete next[id];return next;});
  return <section className="shortcut-settings" data-settings-search-target="shortcut-bindings" aria-label={copy("shortcuts.title")}>
    <p>{copy("shortcut-settings.scope")}</p>
    {Object.values(ShortcutGroup).map(group=><section key={group}><h2>{copy(group===ShortcutGroup.Common?"shortcuts.global":group===ShortcutGroup.Session?"shortcut-settings.session":group===ShortcutGroup.Creation?"shortcut-settings.creation":"shortcut-settings.search")}</h2>
      {editableShortcutCatalog.filter(action=>action.group===group).map(action=><div className="shortcut-settings-row" key={action.id}>
        <div><h3>{copy(action.label)}</h3><p>{bindingLabel(customizationBindings(action.id,draft))}</p><small>{copy("shortcut-settings.default",{binding:bindingLabel(action.defaults)})}</small></div>
        <div className="actions"><button type="button" disabled={locked||Boolean(capturing)} aria-label={copy("shortcut-settings.captureAction",{name:copy(action.label)})} onClick={event=>{opener.current=event.currentTarget;setInvalid(false);setCapturing(action.id);}}>{copy("shortcut-settings.capture")}</button>
          <button type="button" disabled={locked||Boolean(capturing)} aria-label={copy("shortcut-settings.disableAction",{name:copy(action.label)})} onClick={()=>setDraft(previous=>({...previous,[action.id]:{state:ShortcutOverrideState.Disabled}}))}>{copy("shortcut-settings.disable")}</button>
          <button type="button" disabled={locked||Boolean(capturing)||!draft[action.id]} aria-label={copy("shortcut-settings.restoreAction",{name:copy(action.label)})} onClick={()=>restore(action.id)}>{copy("shortcut-settings.restore")}</button></div>
      </div>)}
      <dl>{readOnlyShortcutCatalog.filter(action=>action.group===group).map(action=><div key={action.id}><dt>{copy(action.label)}</dt><dd>{bindingLabel(action.defaults)}</dd></div>)}</dl>
    </section>)}
    <section><h2>{copy("shortcut-settings.fixed")}</h2><p>{copy("shortcut-settings.fixedHelp")}</p></section>
    {capturing?<div role="status"><p>{copy("shortcut-settings.captureHelp")}</p>{invalid?<p role="alert">{copy("shortcut-settings.invalid")}</p>:null}<button type="button" onClick={cancelCapture}>{copy("shortcut-settings.cancelCapture")}</button></div>:null}
    {conflicts.map(([a,b])=><p role="alert" key={`${a}:${b}`}>{copy("shortcut-settings.conflict",{first:copy(shortcutCatalog.find(action=>action.id===a)!.label),second:copy(shortcutCatalog.find(action=>action.id===b)!.label)})}</p>)}
    {conflict?<p role="alert">{copy("shortcut-settings.changed")}</p>:null}
    {snapshot.problem?<><p role="alert">{copy(`shortcut-settings.problem.${snapshot.problem}`)}</p><button type="button" disabled={Boolean(operation)} onClick={reload}>{copy("shortcut-settings.reload")}</button></>:null}
    <p role="status">{copy(operation===ShortcutPreferenceOperation.Saving?"shortcut-settings.saving":operation===ShortcutPreferenceOperation.Reading?"shortcut-settings.reading":dirty?"shortcut-settings.unsaved":"shortcut-settings.saved")}</p>
    <div className="actions"><button type="button" disabled={locked||Boolean(capturing)} onClick={()=>setDraft({})}>{copy("shortcut-settings.restoreAll")}</button>
      <button type="button" disabled={Boolean(operation)||Boolean(capturing)||!dirty&&!conflict} onClick={()=>{setDraft(snapshot.overrides);setBaseline(snapshot);setSaved(false);}}>{copy("shortcut-settings.discard")}</button>
      <button type="button" className="primary" disabled={locked||Boolean(capturing)||!dirty||Boolean(conflicts.length)} onClick={()=>{const original=structuredClone(draft);void save(original,baseline.revision).then(success=>{if(mounted.current&&success)setSaved(true);});}}>{copy("shortcut-settings.save")}</button></div>
  </section>;
}
