// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import type { SkillTokenBinding } from "./skill-completion";
import { useSessionTabsStore } from "./session-tabs";

/** A published child's authoring outlives its tab; native authority stays in SessionView. */
export function SidechatAuthoring({ id, active, children }: { id: string; active: boolean; children: (value: { draft: string; setDraft: (text: string, bindings?: SkillTokenBinding[]) => void; bindings: SkillTokenBinding[]; setBindings: (bindings: SkillTokenBinding[]) => void }) => ReactNode }) {
  const store = useSessionTabsStore(), initial = useRef(store.childDraft(id));
  const [draft, updateDraft] = useState(initial.current?.text ?? ""), [bindings, setBindings] = useState<SkillTokenBinding[]>([]);
  useLayoutEffect(() => {
    const seed = initial.current;
    if (!seed) return;
    store.takeChildFocus(id);
    // Completion only restores focus from a composer that owned it. A background
    // or hidden result cannot override deliberate focus elsewhere in the workspace.
    if (!seed.focus || !active || document.activeElement !== document.body) return;
    const input = document.getElementById(`prompt-${id}`) as HTMLTextAreaElement | null;
    input?.focus(); input?.setSelectionRange(seed.start, seed.end);
  }, [id, active, store]);
  return children({ draft, bindings, setBindings, setDraft: (text, nextBindings) => { updateDraft(text); if (nextBindings) setBindings(nextBindings); } });
}
