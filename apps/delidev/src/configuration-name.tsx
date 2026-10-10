// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState } from "react";
import { clientFailure, FailureCause, FailureCode, EntityKind } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { JobState } from "./jobs";
import { text, type Document } from "./documents";

export function configurationNameConflict(error: unknown): boolean {
  return Boolean(error && clientFailure(error).cause === FailureCause.ConfigurationNameConflict);
}
export function configurationNameProblem(problem: Document): boolean {
  return problem.code === FailureCode.Conflict && problem.cause === FailureCause.ConfigurationNameConflict;
}
export function NameConflictCompletion({ state, problem, complete }: { state: string; problem: Document; complete: (problem: Document) => void }) {
  const handled = useRef(false);
  useEffect(() => {
    if (!handled.current && state === JobState.Failed && configurationNameProblem(problem)) {
      handled.current = true; complete(problem);
    }
  }, [state, problem, complete]);
  return null;
}
export function ConfigurationNameField({ kind, name, change, conflict, label, max = 256, disabled = false, focus = true }: { kind: EntityKind; name: unknown; change: (name: string) => void; conflict?: unknown; label?: string; max?: number; disabled?: boolean; focus?: boolean }) {
  useLocale();
  const input = useRef<HTMLInputElement>(null), help = useId();
  const [rejected, setRejected] = useState<string>();
  const focusedConflict = useRef<unknown>(undefined);
  useEffect(() => {
    if (conflict) setRejected(text(name));
  }, [conflict]);
  useEffect(() => {
    if (!conflict || focusedConflict.current === conflict || !focus || disabled) return;
    const control = input.current;
    // Reuse the editor's existing panel handoff; keep the draft and geometry.
    const panel = control?.closest<HTMLElement>("[data-project-edit-panel]");
    panel?.dispatchEvent(new Event("project-reveal-invalid", { bubbles: true }));
    for (let parent = control?.parentElement; parent; parent = parent.parentElement) if (parent instanceof HTMLDetailsElement) parent.open = true;
    const frame = requestAnimationFrame(() => { if (control?.isConnected) { focusedConflict.current = conflict; control.focus(); } });
    return () => cancelAnimationFrame(frame);
  }, [conflict, focus, disabled]);
  const invalid = Boolean(conflict && rejected === text(name));
  return <><label>{label ?? copy("configuration-fields.name_dcd1d5")}<input ref={input} data-project-focus="name" required maxLength={max} disabled={disabled} value={text(name)} aria-invalid={invalid || undefined} aria-describedby={invalid ? help : undefined} onChange={event => change(event.target.value)} /></label>{invalid ? <p id={help} role="alert">{copy(kind === EntityKind.PROJECT ? "configuration-fields.nameConflict.project" : "configuration-fields.nameConflict.repository")}</p> : null}</>;
}
