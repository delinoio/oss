// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, type RefObject } from "react";
import { EntityKind, FailureCause, FailureCode } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";

export interface ConfigurationNameIssue { name: string; attempt: number }
export const ConfigurationNameContext = createContext<{ kind: EntityKind; issue?: ConfigurationNameIssue } | undefined>(undefined);
export function useConfigurationNameIssue(kind?: EntityKind) {
  const value = useContext(ConfigurationNameContext);
  return value && value.kind === kind ? value.issue : undefined;
}
export function configurationNameConflict(problem: { code?: unknown; cause?: unknown } | undefined) {
  return problem?.code === FailureCode.Conflict && problem.cause === FailureCause.ConfigurationNameConflict;
}
export function ConfigurationNameProblem({ kind, id }: { kind: EntityKind; id?: string }) {
  useLocale();
  return <p role="alert" id={id}>{copy(kind === EntityKind.PROJECT ? "configuration-fields.projectNameConflict" : "configuration-fields.repositoryNameConflict")}</p>;
}
// This restores the original editor's name control after definitive refusal.
// It never changes a draft/revision or rewrites a retained uncertain request.
export function focusConfigurationName(form: RefObject<HTMLElement | null>) {
  const control = form.current?.querySelector<HTMLInputElement>("[data-configuration-name]");
  if (!control) return;
  control.closest<HTMLElement>("[data-project-edit-panel]")?.dispatchEvent(new Event("project-reveal-invalid", { bubbles: true }));
  for (let parent = control.parentElement; parent && parent !== form.current; parent = parent.parentElement) if (parent instanceof HTMLDetailsElement) parent.open = true;
  const frame = requestAnimationFrame(() => { if (control.isConnected && !control.disabled) { control.focus({ preventScroll: true }); control.scrollIntoView?.({ block: "nearest" }); } });
  return () => cancelAnimationFrame(frame);
}
