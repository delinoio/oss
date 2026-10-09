// SPDX-License-Identifier: Apache-2.0
import { useEffect, useLayoutEffect, useId, useRef, useState, type KeyboardEvent, type RefObject } from "react";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { SkillQuery, SkillProvenance, type SkillEntry, type SkillSelection } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";

export interface SkillToken { start: number; end: number; prefix: string }
export function skillToken(value: string, caret: number): SkillToken | undefined {
  const before = value.slice(0, caret), match = /(?:^|\s)\$([^\s$]*)$/u.exec(before);
  if (!match) return undefined;
  const suffix = /^[^\s$]*/u.exec(value.slice(caret))![0];
  return { start: caret - match[1]!.length - 1, end: caret + suffix.length, prefix: match[1]! };
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
  const completion = useRef<HTMLDivElement>(null), keyboardNavigation = useRef(false);
  editable.current = active && !disabled;
  const canEdit = () => editable.current && Boolean(textarea.current?.isConnected) && !textarea.current?.matches(":disabled") && !textarea.current?.closest("[inert], [hidden]");
  const token = !dismissed && !composing.current ? skillToken(value, caret) : undefined;
  const contextChanged = previousScope.current.scope !== scope || (!retainTransportContext && previousScope.current.transport !== transport);
  useEffect(() => {
    if (contextChanged && machineId && agentId) { previousScope.current = { scope, transport }; setBindings(prior => prior.map(binding => ({ ...binding, stale: true }))); setSelected(0); }
  }, [scope, transport, contextChanged, machineId, agentId]);
  const query = useQuery(SkillQuery.listSkills, { machineId, agentId, sessionId, projectId }, { enabled: active && enabled && !disabled && !!token && !!machineId && !!agentId && !contextChanged, retry: false, staleTime: 0, gcTime: 0 });
  const prefix = token?.prefix.toLocaleLowerCase() ?? "";
  const candidates = (query.data?.skills ?? []).filter(entry => entry.selection && entry.name.toLocaleLowerCase().startsWith(prefix)).sort((a, b) => Number(b.name.toLocaleLowerCase() === prefix) - Number(a.name.toLocaleLowerCase() === prefix) || a.name.localeCompare(b.name) || a.selection!.skillId.localeCompare(b.selection!.skillId));
  const validIndex = Math.min(selected, Math.max(0, candidates.length - 1));
  const accept = (entry: SkillEntry) => {
    if (!canEdit() || !enabled || !token || !entry.selection || composing.current || contextChanged) return;
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
      if (event.key === "ArrowDown") { keyboardNavigation.current = true; setSelected((validIndex + 1) % candidates.length); }
      else if (event.key === "ArrowUp") { keyboardNavigation.current = true; setSelected((validIndex + candidates.length - 1) % candidates.length); }
      else accept(candidates[validIndex]!); return true;
    }
    return false;
  };
  const distinct = new Map(bindings.filter(binding => !binding.stale && value.slice(binding.start,binding.end)===binding.token).map(binding => [binding.selection.skillId, binding.selection]));
  const blocked = contextChanged && bindings.length > 0 || bindings.some(binding => binding.stale || binding.context && (!machineId || !agentId || binding.context !== scope) || !binding.ambiguous && value.slice(binding.start,binding.end)!==binding.token) || distinct.size > 16;
  const visible = active && !disabled && Boolean(token);
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
    {enabled ? query.isFetching ? <p role="status">{copy("skills.loading")}</p> : query.error ? <><p role="status">{copy("skills.unavailable")}</p><button type="button" onClick={() => { if (canEdit()) void query.refetch(); }}>{copy("skills.retry")}</button></> : candidates.length ? <ul role="listbox" id={id} aria-label={copy("skills.available")}>{candidates.map((entry, index) => <li key={entry.selection!.skillId} id={`${id}-${index}`} role="option" aria-selected={index === validIndex} onMouseDown={event => event.preventDefault()} onClick={() => accept(entry)}><strong className="skill-completion-name">{entry.name}</strong><span className="skill-completion-description">{entry.description}</span><span className="skill-completion-provenance">{entry.provenance === SkillProvenance.PROJECT ? copy("skills.project") : copy("skills.user")}</span></li>)}</ul> : <p role="status">{copy("skills.empty")}</p> : <p role="status">{copy("skills.unsupported")}</p>}
  </div> : null;
  return { selections: [...distinct.values()], blocked, list, clear: () => { if (canEdit()) setBindings([]); }, // Original receipt acceptance settles ownership before the pending render unlocks.
    replaceUnbound: (next: string, position: number) => { if (!canEdit()) return; if (change(next, []) === false) return; setBindings([]); bindingsChanged?.([]); setCaret(position); setDismissed(true); setSelected(0); },
    clearAccepted: () => { bindingsChanged?.([]); setBindings([]); }, onChange, onKeyDown,
    onSelect: () => { if (!canEdit()) return; setCaret(textarea.current?.selectionStart ?? 0); },
    onCompositionStart: () => { if (!canEdit()) return; composing.current = true; setDismissed(true); }, onCompositionEnd: () => { composing.current = false; if (!canEdit()) return; setCaret(textarea.current?.selectionStart ?? 0); setDismissed(false); },
    attributes: { "aria-controls": visible && candidates.length ? id : undefined, "aria-autocomplete": "list" as const, "aria-expanded": visible, "aria-activedescendant": visible && candidates.length ? `${id}-${validIndex}` : undefined },
    warning: blocked ? <p role="status">{copy("skills.reselect")}</p> : null,
  };
}
