// SPDX-License-Identifier: Apache-2.0
import { useEffect, useLayoutEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from "react";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { SkillQuery, SkillProvenance, isEntityId, type SkillEntry, type SkillSelection } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";

export interface SkillToken { start: number; end: number; prefix: string }
export function skillToken(value: string, caret: number): SkillToken | undefined {
  const before = value.slice(0, caret), match = /(?:^|\s)\$([^\s$]*)$/u.exec(before);
  if (!match) return undefined;
  const suffix = /^[^\s$]*/u.exec(value.slice(caret))![0];
  return { start: caret - match[1]!.length - 1, end: caret + suffix.length, prefix: match[1]! };
}
export enum SkillAvailability { Available = "available", Unavailable = "unavailable", Unknown = "unknown" }
export function skillRanges(value: string): SkillToken[] {
  return [...value.matchAll(/(?:^|\s)\$([^\s$]+)(?=$|\s)/gu)].map(match => ({ start: match.index! + match[0].length - match[1]!.length - 1, end: match.index! + match[0].length, prefix: match[1]! }));
}
// A malformed or mixed snapshot cannot prove absence in the current scope.
function completeInventory(entries: readonly SkillEntry[]): boolean {
  const ids = new Set<string>(), first = entries[0]?.selection;
  return entries.length <= 256 && entries.every(entry => {
    const selection = entry.selection;
    if (!entry.name || /[\s$]/u.test(entry.name) || !selection || !isEntityId(selection.skillId) || !isEntityId(selection.inventoryId) || !isEntityId(selection.workerDeviceId) || !/^[a-f0-9]{64}$/.test(selection.contentRevision) || ids.has(selection.skillId) || selection.inventoryId !== first?.inventoryId || selection.workerDeviceId !== first?.workerDeviceId) return false;
    ids.add(selection.skillId); return true;
  });
}
export interface SkillTokenBinding { start: number; end: number; token: string; selection: SkillSelection; stale: boolean; context?: string; ambiguous?: boolean }
export function editedBindings(before: string, after: string, bindings: readonly SkillTokenBinding[]): SkillTokenBinding[] {
  let start = 0; while (start < before.length && start < after.length && before[start] === after[start]) start++;
  let end = before.length, nextEnd = after.length; while (end > start && nextEnd > start && before[end - 1] === after[nextEnd - 1]) { end--; nextEnd--; }
  const delta = after.length - before.length;
  return bindings.flatMap(binding => {
    if (binding.ambiguous) return [binding];
    if (end > binding.start && start < binding.end || start > binding.start && start < binding.end) return [];
    const shift = start <= binding.start ? delta : 0, next = { ...binding, start: binding.start + shift, end: binding.end + shift };
    return after.slice(next.start, next.end) === next.token && (next.start === 0 || /\s/u.test(after[next.start - 1]!)) && (next.end === after.length || /\s/u.test(after[next.end]!)) ? [next] : [];
  });
}
export function useSkillCompletion({ value, change, textarea, machineId, agentId, sessionId = "", projectId = "", active = true, enabled = true, disabled = false, initialBindings = [], bindingsChanged, retainTransportContext = false }: {
  value: string; change: (value: string, bindings?: SkillTokenBinding[]) => boolean | void; textarea: RefObject<HTMLTextAreaElement | null>;
  machineId: string; agentId: string; sessionId?: string; projectId?: string; active?: boolean; enabled?: boolean; disabled?: boolean; initialBindings?: SkillTokenBinding[]; bindingsChanged?: (bindings: SkillTokenBinding[]) => void; retainTransportContext?: boolean;
}) {
  useLocale(); const transport = useTransport(), id = useId();
  const scope = `${machineId}:${agentId}:${sessionId}:${projectId}`;
  const previousScope = useRef({ scope: initialBindings.find(binding => binding.context)?.context ?? scope, transport });
  const [bindings, setBindings] = useState<SkillTokenBinding[]>(initialBindings), [caret, setCaret] = useState(0), [dismissed, setDismissed] = useState(false), [selected, setSelected] = useState(0);
  useEffect(() => { bindingsChanged?.(bindings); }, [bindings, bindingsChanged]);
  const composing = useRef(false), editable = useRef(false);
  const [, setComposition] = useState(false);
  const completion = useRef<HTMLDivElement>(null), keyboardNavigation = useRef(false);
  editable.current = active && !disabled;
  const canEdit = () => editable.current && Boolean(textarea.current?.isConnected) && !textarea.current?.matches(":disabled") && !textarea.current?.closest("[inert], [hidden]");
  const token = !dismissed && !composing.current ? skillToken(value, caret) : undefined;
  const contextChanged = previousScope.current.scope !== scope || (!retainTransportContext && previousScope.current.transport !== transport);
  useEffect(() => {
    if (contextChanged && machineId && agentId) { previousScope.current = { scope, transport }; setBindings(prior => prior.map(binding => ({ ...binding, stale: true }))); setSelected(0); }
  }, [scope, transport, contextChanged, machineId, agentId]);
  const ranges = skillRanges(value);
  const query = useQuery(SkillQuery.listSkills, { machineId, agentId, sessionId, projectId }, { enabled: active && enabled && !disabled && (!!token || ranges.length > 0) && !!machineId && !!agentId && !contextChanged, retry: false, staleTime: 0, gcTime: 0 });
  const prefix = token?.prefix.toLocaleLowerCase() ?? "";
  const inventoryKnown = active && enabled && !disabled && !!machineId && !!agentId && !contextChanged && query.isSuccess && !query.isFetching && !query.error && completeInventory(query.data.skills);
  const inventory = inventoryKnown ? query.data!.skills : [];
  const visible = active && !disabled && Boolean(token);
  const retained = useRef<{ scope: string; transport: typeof transport; entries: SkillEntry[] }>({ scope, transport, entries: [] });
  if (!visible || retained.current.scope !== scope || retained.current.transport !== transport) retained.current = { scope, transport, entries: [] };
  const live = inventory.filter(entry => entry.selection && entry.name.toLocaleLowerCase().startsWith(prefix));
  if (visible && inventoryKnown) {
    const ids = new Set(inventory.map(entry => entry.selection!.skillId));
    retained.current.entries = [...live, ...retained.current.entries.filter(entry => !ids.has(entry.selection!.skillId))].slice(0, 256);
  }
  const candidates = (inventoryKnown ? retained.current.entries : []).filter(entry => entry.name.toLocaleLowerCase().startsWith(prefix)).map(entry => ({ ...entry, availability: inventory.some(current => current.selection!.skillId === entry.selection!.skillId) ? SkillAvailability.Available : SkillAvailability.Unavailable })).sort((a, b) => Number(b.name.toLocaleLowerCase() === prefix) - Number(a.name.toLocaleLowerCase() === prefix) || a.name.localeCompare(b.name) || a.selection!.skillId.localeCompare(b.selection!.skillId));
  const enabledIndices = candidates.flatMap((entry,index) => entry.availability === SkillAvailability.Available ? [index] : []);
  const validIndex = enabledIndices.includes(selected) ? selected : enabledIndices[0] ?? -1;
  const unavailable = inventoryKnown && !composing.current ? ranges.filter(range => !inventory.some(entry => entry.name === range.prefix) && !(token && range.start === token.start && inventory.some(entry => entry.name.toLocaleLowerCase().startsWith(token.prefix.toLocaleLowerCase())))) : [];
  const accept = (entry: SkillEntry) => {
    if (!canEdit() || !enabled || !token || !entry.selection || composing.current || contextChanged || !inventoryKnown || !inventory.some(current => current.selection === entry.selection)) return;
    const replacement = `$${entry.name}`, next = value.slice(0, token.start) + replacement + value.slice(token.end), end = token.start + replacement.length;
    const nextBindings = [...editedBindings(value, next, bindings).filter(binding => binding.start !== token.start), { start: token.start, end, token: replacement, selection: entry.selection!, stale: false, context: scope }];
    if (change(next, nextBindings) === false) return;
    setBindings(nextBindings); setCaret(end); setDismissed(true); queueMicrotask(() => { if (!canEdit()) return; textarea.current?.focus(); textarea.current?.setSelectionRange(end, end); });
  };
  const onChange = (next: string, position: number) => { if (!canEdit()) return; const nextBindings = editedBindings(value, next, bindings); if (change(next, nextBindings) === false) return; setBindings(nextBindings); setCaret(position); setDismissed(false); setSelected(0); };
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (!canEdit() || event.nativeEvent.isComposing || composing.current || event.nativeEvent.keyCode === 229 || event.repeat || event.getModifierState("AltGraph")) return false;
    if (!token) return false;
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); setDismissed(true); return true; }
    if (candidates.length && ["ArrowDown", "ArrowUp", "Enter", "Tab"].includes(event.key) && !event.shiftKey && !event.metaKey && !event.ctrlKey) {
      event.preventDefault(); event.stopPropagation();
      if (event.key === "ArrowDown") { keyboardNavigation.current = true; setSelected(enabledIndices[(enabledIndices.indexOf(validIndex) + 1) % enabledIndices.length] ?? -1); }
      else if (event.key === "ArrowUp") { keyboardNavigation.current = true; setSelected(enabledIndices[(enabledIndices.indexOf(validIndex) + enabledIndices.length - 1) % enabledIndices.length] ?? -1); }
      else if (validIndex >= 0) accept(candidates[validIndex]!); return true;
    }
    return false;
  };
  const distinct = new Map(bindings.filter(binding => !binding.stale && value.slice(binding.start,binding.end)===binding.token).map(binding => [binding.selection.skillId, binding.selection]));
  const blocked = contextChanged && bindings.length > 0 || bindings.some(binding => binding.stale || binding.context && (!machineId || !agentId || binding.context !== scope) || !binding.ambiguous && value.slice(binding.start,binding.end)!==binding.token) || distinct.size > 16;
  useLayoutEffect(() => {
    if (!keyboardNavigation.current) return;
    keyboardNavigation.current = false;
    const container = completion.current;
    const option = container?.querySelector<HTMLElement>(`[aria-selected="true"]`);
    if (!container || !option || !canEdit()) return;
    // Scroll only this list. scrollIntoView can move ancestor composers or the
    // page and displace the textarea while its original focus is retained.
    const bounds = container.getBoundingClientRect(), row = option.getBoundingClientRect();
    const scale = bounds.height / container.offsetHeight || 1;
    const top = bounds.top + container.clientTop * scale, bottom = top + container.clientHeight * scale;
    if (row.top < top) container.scrollTop += (row.top - top) / scale;
    else if (row.bottom > bottom) container.scrollTop += (row.bottom - bottom) / scale;
  }, [validIndex, visible, query.isFetching]);
  const list = visible ? <div ref={completion} className="skill-completion">
    {enabled ? query.isFetching ? <p role="status">{copy("skills.loading")}</p> : !inventoryKnown ? <><p role="status">{copy("skills.unavailable")}</p><button type="button" onClick={() => { if (canEdit() && enabled && machineId && agentId && !contextChanged) void query.refetch(); }}>{copy("skills.retry")}</button></> : candidates.length ? <ul role="listbox" id={id} aria-label={copy("skills.available")}>{candidates.map((entry, index) => <li key={entry.selection!.skillId} id={`${id}-${index}`} role="option" aria-selected={index === validIndex} aria-disabled={entry.availability === SkillAvailability.Unavailable || undefined} data-availability={entry.availability} onMouseDown={event => event.preventDefault()} onClick={() => accept(entry)}><strong className="skill-completion-name">{entry.name}</strong><span className="skill-completion-description">{entry.description}</span>{entry.availability === SkillAvailability.Unavailable ? <span className="skill-completion-status">{copy("skills.entryUnavailable")}</span> : null}<span className="skill-completion-provenance">{entry.provenance === SkillProvenance.PROJECT ? copy("skills.project") : copy("skills.user")}</span></li>)}</ul> : <p role="status">{copy("skills.empty")}</p> : <p role="status">{copy("skills.unsupported")}</p>}
  </div> : null;
  return { selections: [...distinct.values()], blocked, list, clear: () => { if (canEdit()) setBindings([]); }, // Original receipt acceptance settles ownership before the pending render unlocks.
    replaceUnbound: (next: string, position: number) => { if (!canEdit()) return; if (change(next, []) === false) return; setBindings([]); bindingsChanged?.([]); setCaret(position); setDismissed(true); setSelected(0); },
    clearAccepted: () => { bindingsChanged?.([]); setBindings([]); }, onChange, onKeyDown,
    onSelect: () => { if (!canEdit()) return; setCaret(textarea.current?.selectionStart ?? 0); },
    onCompositionStart: () => { if (!canEdit()) return; composing.current = true; setComposition(true); setDismissed(true); }, onCompositionEnd: () => { composing.current = false; setComposition(false); if (!canEdit()) return; setCaret(textarea.current?.selectionStart ?? 0); setDismissed(false); },
    attributes: { "aria-describedby": unavailable.length ? `${id}-unavailable` : undefined, "aria-controls": visible && candidates.length ? id : undefined, "aria-autocomplete": "list" as const, "aria-expanded": visible, "aria-activedescendant": visible && validIndex >= 0 ? `${id}-${validIndex}` : undefined },
    unavailable, availability: inventoryKnown ? SkillAvailability.Available : SkillAvailability.Unknown, wrap: (child: ReactNode) => <SkillText textarea={textarea} value={value} ranges={unavailable} descriptionId={`${id}-unavailable`}>{child}</SkillText>,
    warning: blocked ? <p role="status">{copy("skills.reselect")}</p> : null,
  };
}


// The textarea retains editing, selection and undo ownership; this sibling only paints text.
function SkillText({ textarea, value, ranges, descriptionId, children }: { textarea: RefObject<HTMLTextAreaElement | null>; value: string; ranges: SkillToken[]; descriptionId: string; children: ReactNode }) {
  const frame = useRef<HTMLDivElement>(null), content = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const input = textarea.current, viewport = frame.current, text = content.current;
    if (!input || !viewport || !text) return;
    const align = () => {
      const style = getComputedStyle(input);
      Object.assign(viewport.style, { left: `${input.offsetLeft + input.clientLeft}px`, top: `${input.offsetTop + input.clientTop}px`, width: `${input.clientWidth}px`, height: `${input.clientHeight}px` });
      for (const property of ["fontFamily", "fontSize", "fontWeight", "fontStyle", "fontVariant", "fontStretch", "fontKerning", "lineHeight", "letterSpacing", "wordSpacing", "textTransform", "direction", "paddingTop", "paddingRight", "paddingBottom", "paddingLeft", "textAlign", "textIndent", "tabSize", "wordBreak", "overflowWrap"] as const) text.style[property] = style[property];
      text.style.width = `${input.clientWidth}px`; text.style.transform = `translate(${-input.scrollLeft}px, ${-input.scrollTop}px)`;
    };
    align(); const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(align); observer?.observe(input); input.addEventListener("scroll", align); window.addEventListener("resize", align);
    return () => { observer?.disconnect(); input.removeEventListener("scroll", align); window.removeEventListener("resize", align); };
  }, [textarea, value, ranges.length]);
  let end = 0;
  const segments = ranges.map(range => { const preceding=value.slice(end,range.start); end=range.end; return <span key={range.start}>{preceding}<span className="skill-token-unavailable" data-skill-start={range.start}>{value.slice(range.start,range.end)}</span></span>; });
  return <div className="skill-textarea" data-skill-overlay={ranges.length > 0 || undefined}>{children}{ranges.length ? <><div ref={frame} className="skill-text-overlay" aria-hidden="true"><div ref={content}>{segments}{value.slice(end)}{"\u200b"}</div></div><span id={descriptionId} className="sidebar-sr-only" aria-hidden="true">{copy("skills.tokensUnavailable", { count: ranges.length })}</span></> : null}</div>;
}
