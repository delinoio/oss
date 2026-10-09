// SPDX-License-Identifier: Apache-2.0
import { Fragment, useEffect, useLayoutEffect, useRef, useState } from "react";
import { copy, useLocale } from "./localization";
import { bindingKeys, ShortcutId, ShortcutPlatform, shortcutPlatform, type ShortcutBinding } from "./shortcuts";
import { captureShortcut, fixedNativeShortcutCatalog, customizationBindings, ShortcutGroup, ShortcutOverrideState, shortcutCatalog, editableShortcutCatalog, readOnlyShortcutCatalog, shortcutConflicts, type ShortcutOverrides } from "./shortcut-preferences";
import { ShortcutPreferenceOperation, useShortcutPreferences } from "./shortcut-preference-controller";
import "./shortcut-settings.css";
import { shortcutCapture } from "./shortcut-capture";
import { SettingsActionButton, SettingsActionIcon, SettingsActionPresentation } from "./settings-action";
const serialize = (value: ShortcutOverrides) => JSON.stringify(Object.entries(value).sort(([a],[b]) => a.localeCompare(b)));
export function ShortcutSettings() {
  useLocale();
  const { snapshot, operation, save, reload } = useShortcutPreferences();
  const [draft, setDraft] = useState(snapshot.overrides), [baseline, setBaseline] = useState(snapshot);
  const [capturing, setCapturing] = useState<ShortcutId>(), [invalid, setInvalid] = useState(false), [saved, setSaved] = useState(false);
  const [captureState,setCaptureState]=useState<"arming"|"active"|"retiring"|"uncertain">();
  const deadline=useRef(0),captureGeneration=useRef(0);
  const restoreFocus = useRef(false), pointerTarget = useRef<Element|null>(null);
  const opener = useRef<HTMLButtonElement|null>(null), mounted = useRef(false), platform = shortcutPlatform();
  const dirty = serialize(draft) !== serialize(baseline.overrides), conflict = dirty && baseline.revision !== snapshot.revision;
  const locked = Boolean(operation || snapshot.problem || conflict);
  const conflicts = shortcutConflicts(draft);
  useEffect(() => { mounted.current=true; return () => { mounted.current=false;captureGeneration.current++;void shortcutCapture.end().catch(()=>{}); }; }, []);
  useEffect(() => {
    if (!dirty || saved) { setBaseline(snapshot); setDraft(snapshot.overrides); setSaved(false); }
  }, [snapshot, saved]);
  const cancelCapture = () => {
    const generation=++captureGeneration.current;deadline.current=0;setCaptureState("retiring");setInvalid(false);
    void shortcutCapture.end().then(()=>{if(mounted.current&&generation===captureGeneration.current){restoreFocus.current=true;setCapturing(undefined);setCaptureState(undefined);}}).catch(()=>{if(mounted.current&&generation===captureGeneration.current)setCaptureState("uncertain");});
  };
  const beginCapture=(id:ShortcutId)=>{
    const generation=++captureGeneration.current;setCapturing(id);setCaptureState("arming");setInvalid(false);
    void shortcutCapture.begin(snapshot.revision).then(({deadline:until,token})=>{
      if(!mounted.current||generation!==captureGeneration.current){void shortcutCapture.end(token).catch(()=>{});return;}
      if(until<=performance.now()){cancelCapture();return;}
      deadline.current=until;setCaptureState("active");
    }).catch(()=>{if(mounted.current&&generation===captureGeneration.current)setCaptureState("uncertain");});
  };
  useLayoutEffect(()=>{if(!capturing&&restoreFocus.current){restoreFocus.current=false;const button=opener.current;if(button?.isConnected&&!button.matches(":disabled")&&!button.closest("[hidden],[inert]"))button.focus({preventScroll:true});}},[capturing]);
  // Install capture before the active prompt can be painted. A key pressed
  // immediately after admission must not escape into ordinary dispatch.
  useLayoutEffect(() => {
    if (!capturing || locked) { if(capturing&&captureState!=="retiring"&&captureState!=="uncertain")cancelCapture(); return; }
    const action = capturing;
    const expire=setTimeout(()=>{if(captureState==="active")cancelCapture();},Math.max(0,deadline.current-performance.now()));
    const capture = (event:KeyboardEvent) => {
      // Capture owns its event before application dispatch or native editing.
      event.preventDefault();event.stopImmediatePropagation();
      if(captureState!=="active"||performance.now()>=deadline.current){if(captureState==="active")cancelCapture();return;}
      if(event.key==="Escape"&&!event.isComposing&&event.keyCode!==229&&!event.repeat){cancelCapture();return;}
      const chord=captureShortcut(event,platform);
      if(!chord){setInvalid(true);return;}
      if(!mounted.current)return;
      setDraft(previous=>({...previous,[action]:{state:ShortcutOverrideState.Binding,chord}}));cancelCapture();
    };
    const departed=()=>cancelCapture();
    const visibility=()=>{if(document.visibilityState==="hidden")cancelCapture();};
    window.addEventListener("blur",departed);document.addEventListener("visibilitychange",visibility);
    document.addEventListener("keydown",capture,true);
    return()=>{clearTimeout(expire);window.removeEventListener("blur",departed);document.removeEventListener("visibilitychange",visibility);document.removeEventListener("keydown",capture,true);};
  },[capturing,locked,platform,captureState]);
  const bindingLabel = (bindings: readonly ShortcutBinding[]) => bindings.length ? bindings.map(binding => bindingKeys(binding,platform).join(" + ")).join(` ${copy("shortcuts.or")} `) : copy("shortcut-settings.disabled");
  const bindingCaps = (bindings: readonly ShortcutBinding[]) => bindings.length ? bindings.map((binding,bindingIndex) => <span className="shortcut-binding" key={bindingIndex}>{bindingIndex ? <span> {copy("shortcuts.or")} </span> : null}{bindingKeys(binding,platform).map((key,keyIndex) => <Fragment key={keyIndex}>{keyIndex ? <span aria-hidden="true"> + </span> : null}<kbd>{key}</kbd></Fragment>)}</span>) : copy("shortcut-settings.disabled");
  const restore = (id:ShortcutId) => setDraft(previous=>{const next={...previous};delete next[id];return next;});
  return <section className="shortcut-settings" data-settings-search-target="shortcut-bindings" aria-label={copy("shortcuts.title")}>
    <div className="shortcut-catalog" role="region" aria-label={copy("shortcut-settings.catalog")} tabIndex={0} onFocusCapture={event => {
      // Reveal the exact row inside this scrollport without moving Settings.
      const target = event.target as HTMLElement, catalog = event.currentTarget;
      if (target === catalog) return;
      // Pointer focus precedes click hit-testing. Preserve its original target
      // until click; keyboard and independent focus still reveal the row.
      if (pointerTarget.current && target.contains(pointerTarget.current)) return;
      pointerTarget.current = null;
      const bounds = catalog.getBoundingClientRect(), row = target.getBoundingClientRect();
      // The shared focus ring extends 2px plus its 2px outline offset.
      // Reveal that complete indicator, not just the control border box.
      const outlineAllowance = 4;
      if (row.top - outlineAllowance < bounds.top) catalog.scrollTop -= bounds.top - row.top + outlineAllowance;
      else if (row.bottom + outlineAllowance > bounds.bottom) catalog.scrollTop += row.bottom - bounds.bottom + outlineAllowance;
    }} onPointerDownCapture={event=>{pointerTarget.current=event.target instanceof Element?event.target:null;}}
      onClickCapture={()=>{pointerTarget.current=null;}}
      onPointerCancelCapture={()=>{pointerTarget.current=null;}}
      onKeyDownCapture={()=>{pointerTarget.current=null;}}
      onBlurCapture={event=>{if(!event.currentTarget.contains(event.relatedTarget as Node|null))pointerTarget.current=null;}}>
    {Object.values(ShortcutGroup).map(group=><section key={group}><h2>{copy(group===ShortcutGroup.Common?"shortcuts.global":group===ShortcutGroup.Session?"shortcut-settings.session":group===ShortcutGroup.Creation?"shortcut-settings.creation":"shortcut-settings.search")}</h2>
      {editableShortcutCatalog.filter(action=>action.group===group).map(action=><div className="shortcut-settings-row" key={action.id}>
        <div className="shortcut-action-description"><h3>{copy(action.label)}</h3><small>{copy("shortcut-settings.default",{binding:bindingLabel(action.defaults)})}</small></div>
        <div className="shortcut-current-binding">{bindingCaps(customizationBindings(action.id,draft))}</div>
        <div className="actions"><SettingsActionButton icon={SettingsActionIcon.Edit} presentation={SettingsActionPresentation.Icon} type="button" disabled={locked||Boolean(capturing)} aria-label={copy("shortcut-settings.captureAction",{name:copy(action.label)})} onClick={event=>{opener.current=event.currentTarget;beginCapture(action.id);}}>{copy("shortcut-settings.capture")}</SettingsActionButton>
          <SettingsActionButton icon={SettingsActionIcon.Stop} presentation={SettingsActionPresentation.Icon} type="button" disabled={locked||Boolean(capturing)} aria-label={copy("shortcut-settings.disableAction",{name:copy(action.label)})} onClick={()=>setDraft(previous=>({...previous,[action.id]:{state:ShortcutOverrideState.Disabled}}))}>{copy("shortcut-settings.disable")}</SettingsActionButton>
          <SettingsActionButton icon={SettingsActionIcon.Back} presentation={SettingsActionPresentation.Icon} type="button" disabled={locked||Boolean(capturing)||!draft[action.id]} aria-label={copy("shortcut-settings.restoreAction",{name:copy(action.label)})} onClick={()=>restore(action.id)}>{copy("shortcut-settings.restore")}</SettingsActionButton></div>
      </div>)}
      <dl>{readOnlyShortcutCatalog.filter(action=>action.group===group).map(action=><div key={action.id}><dt>{copy(action.label)}</dt><dd>{bindingCaps(action.defaults)}</dd></div>)}</dl>
    </section>)}
    <section><h2>{copy("shortcut-settings.fixed")}</h2><p>{copy("shortcut-settings.fixedHelp")}</p><dl>{fixedNativeShortcutCatalog.filter(action=>!action.macOnly||platform===ShortcutPlatform.Mac).map(action=><div key={action.id}><dt>{copy(action.label)}</dt><dd>{bindingCaps([{key:action.key,primary:true}])}</dd></div>)}</dl></section>
    </div>
    <div className="shortcut-operation-guidance">
    {capturing?<div role="status"><p>{copy(captureState==="active"?"shortcut-settings.captureHelp":captureState==="uncertain"?"shortcut-settings.captureUncertain":"shortcut-settings.capturePending")}</p>{invalid?<p role="alert">{copy("shortcut-settings.invalid")}</p>:null}<SettingsActionButton icon={SettingsActionIcon.Cancel} type="button" onClick={cancelCapture}>{copy("shortcut-settings.cancelCapture")}</SettingsActionButton></div>:null}
    {conflicts.map(([a,b])=><p role="alert" key={`${a}:${b}`}>{copy("shortcut-settings.conflict",{first:copy(shortcutCatalog.find(action=>action.id===a)!.label),second:copy(shortcutCatalog.find(action=>action.id===b)!.label)})}</p>)}
    {conflict?<p role="alert">{copy("shortcut-settings.changed")}</p>:null}
    {snapshot.problem?<><p role="alert">{copy(`shortcut-settings.problem.${snapshot.problem}`)}</p><SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={Boolean(operation)} onClick={reload}>{copy("shortcut-settings.reload")}</SettingsActionButton></>:null}
    <p role="status">{copy(operation===ShortcutPreferenceOperation.Saving?"shortcut-settings.saving":operation===ShortcutPreferenceOperation.Reading?"shortcut-settings.reading":dirty?"shortcut-settings.unsaved":"shortcut-settings.saved")}</p>
    </div>
    <div className="actions shortcut-settings-footer"><SettingsActionButton icon={SettingsActionIcon.Back} type="button" disabled={locked||Boolean(capturing)} onClick={()=>setDraft({})}>{copy("shortcut-settings.restoreAll")}</SettingsActionButton>
      <div className="actions shortcut-settings-commit-actions"><SettingsActionButton icon={SettingsActionIcon.Cancel} type="button" disabled={Boolean(operation)||Boolean(capturing)||!dirty&&!conflict} onClick={()=>{setDraft(snapshot.overrides);setBaseline(snapshot);setSaved(false);}}>{copy("shortcut-settings.discard")}</SettingsActionButton>
      <SettingsActionButton icon={SettingsActionIcon.Save} type="button" className="primary" disabled={locked||Boolean(capturing)||!dirty||Boolean(conflicts.length)} onClick={()=>{const original=structuredClone(draft);void save(original,baseline.revision).then(success=>{if(mounted.current&&success)setSaved(true);});}}>{copy("shortcut-settings.save")}</SettingsActionButton></div></div>
  </section>;
}
