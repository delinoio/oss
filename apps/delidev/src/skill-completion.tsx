// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState, type KeyboardEvent, type RefObject } from "react";
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
export interface SkillTokenBinding { start: number; end: number; token: string; selection: SkillSelection; stale: boolean; ambiguous?: boolean }
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
export function useSkillCompletion({ value, change, textarea, machineId, agentId, sessionId = "", projectId = "", active = true, enabled = true, initialBindings = [], bindingsChanged }: {
  value: string; change: (value: string) => void; textarea: RefObject<HTMLTextAreaElement | null>;
  machineId: string; agentId: string; sessionId?: string; projectId?: string; active?: boolean; enabled?: boolean; initialBindings?: SkillTokenBinding[]; bindingsChanged?: (bindings: SkillTokenBinding[]) => void;
}) {
  useLocale(); const transport = useTransport(), id = useId();
  const scope = `${machineId}:${agentId}:${sessionId}:${projectId}`;
  const previousScope = useRef({ scope, transport });
  const [bindings, setBindings] = useState<SkillTokenBinding[]>(initialBindings), [caret, setCaret] = useState(0), [dismissed, setDismissed] = useState(false), [selected, setSelected] = useState(0);
  useEffect(() => { bindingsChanged?.(bindings); }, [bindings, bindingsChanged]);
  const composing = useRef(false);
  const token = !dismissed && !composing.current ? skillToken(value, caret) : undefined;
  const contextChanged = previousScope.current.scope !== scope || previousScope.current.transport !== transport;
  useEffect(() => {
    if (contextChanged) { previousScope.current = { scope, transport }; setBindings(prior => prior.map(binding => ({ ...binding, stale: true }))); setSelected(0); }
  }, [scope, transport, contextChanged]);
  const query = useQuery(SkillQuery.listSkills, { machineId, agentId, sessionId, projectId }, { enabled: active && enabled && !!token && !!machineId && !!agentId && !contextChanged, retry: false, staleTime: 0, gcTime: 0 });
  const prefix = token?.prefix.toLocaleLowerCase() ?? "";
  const candidates = (query.data?.skills ?? []).filter(entry => entry.selection && entry.name.toLocaleLowerCase().startsWith(prefix)).sort((a, b) => Number(b.name.toLocaleLowerCase() === prefix) - Number(a.name.toLocaleLowerCase() === prefix) || a.name.localeCompare(b.name) || a.selection!.skillId.localeCompare(b.selection!.skillId));
  const validIndex = Math.min(selected, Math.max(0, candidates.length - 1));
  const accept = (entry: SkillEntry) => {
    if (!token || !entry.selection || composing.current || contextChanged) return;
    const replacement = `$${entry.name}`, next = value.slice(0, token.start) + replacement + value.slice(token.end), end = token.start + replacement.length;
    setBindings(prior => [...editedBindings(value, next, prior).filter(binding => binding.start !== token.start), { start: token.start, end, token: replacement, selection: entry.selection!, stale: false }]);
    change(next); setCaret(end); setDismissed(true); queueMicrotask(() => { textarea.current?.focus(); textarea.current?.setSelectionRange(end, end); });
  };
  const onChange = (next: string, position: number) => { setBindings(prior => editedBindings(value, next, prior)); change(next); setCaret(position); setDismissed(false); setSelected(0); };
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.nativeEvent.isComposing || composing.current || event.nativeEvent.keyCode === 229 || event.repeat || event.getModifierState("AltGraph")) return false;
    if (!token) return false;
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); setDismissed(true); return true; }
    if (candidates.length && ["ArrowDown", "ArrowUp", "Enter", "Tab"].includes(event.key) && !event.shiftKey && !event.metaKey && !event.ctrlKey) {
      event.preventDefault(); event.stopPropagation();
      if (event.key === "ArrowDown") setSelected((validIndex + 1) % candidates.length);
      else if (event.key === "ArrowUp") setSelected((validIndex + candidates.length - 1) % candidates.length);
      else accept(candidates[validIndex]!); return true;
    }
    return false;
  };
  const distinct = new Map(bindings.filter(binding => !binding.stale && value.slice(binding.start,binding.end)===binding.token).map(binding => [binding.selection.skillId, binding.selection]));
  const blocked = contextChanged && bindings.length > 0 || bindings.some(binding => binding.stale) || distinct.size > 16;
  const list = token ? <div className="skill-completion">
    {enabled ? query.isFetching ? <p role="status">{copy("skills.loading")}</p> : query.error ? <><p role="status">{copy("skills.unavailable")}</p><button type="button" onClick={() => void query.refetch()}>{copy("skills.retry")}</button></> : candidates.length ? <ul role="listbox" id={id} aria-label={copy("skills.available")}>{candidates.map((entry, index) => <li key={entry.selection!.skillId} id={`${id}-${index}`} role="option" aria-selected={index === validIndex} onMouseDown={event => event.preventDefault()} onClick={() => accept(entry)}><strong>{entry.name}</strong> <span>{entry.provenance === SkillProvenance.PROJECT ? copy("skills.project") : copy("skills.user")}</span><p>{entry.description}</p></li>)}</ul> : <p role="status">{copy("skills.empty")}</p> : <p role="status">{copy("skills.unsupported")}</p>}
  </div> : null;
  return { selections: [...distinct.values()], blocked, list, clear: () => setBindings([]), onChange, onKeyDown,
    onSelect: () => { setCaret(textarea.current?.selectionStart ?? 0); },
    onCompositionStart: () => { composing.current = true; setDismissed(true); }, onCompositionEnd: () => { composing.current = false; setCaret(textarea.current?.selectionStart ?? 0); setDismissed(false); },
    attributes: { "aria-controls": token && candidates.length ? id : undefined, "aria-autocomplete": "list" as const, "aria-expanded": !!token, "aria-activedescendant": token && candidates.length ? `${id}-${validIndex}` : undefined },
    warning: blocked ? <p role="status">{copy("skills.reselect")}</p> : null,
  };
}
