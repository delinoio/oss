// SPDX-License-Identifier: Apache-2.0
import { useId, useLayoutEffect, useRef, useState, type ReactNode, type SyntheticEvent } from "react";
import { flushSync } from "react-dom";
import { copy, useLocale } from "./localization";
import "./project-edit-tabs.css";
export enum ProjectEditTab { General = "general", Repositories = "repositories", Execution = "execution", Access = "access" }
const order = Object.values(ProjectEditTab);
let reportingInvalid = false;
export function revealProjectInvalidControl(event: SyntheticEvent<HTMLFormElement>) {
  if (reportingInvalid) return;
  event.preventDefault();
  const target = Array.from(event.currentTarget.elements).find(element =>
    (element instanceof HTMLInputElement || element instanceof HTMLSelectElement || element instanceof HTMLTextAreaElement) && element.willValidate && !element.validity.valid
  ) as HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement | undefined;
  if (!target || event.target !== target) return;
  const panel = target.closest<HTMLElement>("[data-project-edit-panel]");
  flushSync(() => panel?.dispatchEvent(new Event("project-reveal-invalid", { bubbles: true })));
  for (let parent = target.parentElement; parent && parent !== event.currentTarget; parent = parent.parentElement) if (parent instanceof HTMLDetailsElement) parent.open = true;
  requestAnimationFrame(() => {
    if (!target.isConnected) return;
    target.focus();
    reportingInvalid = true;
    try { target.reportValidity(); } finally { reportingInvalid = false; }
  });
}
export function ProjectEditTabs({ panels, disabled }: { panels: Record<ProjectEditTab, ReactNode>; disabled: boolean }) {
  useLocale();
  const id = useId(), root = useRef<HTMLDivElement>(null), tabs = useRef(new Map<ProjectEditTab, HTMLButtonElement>());
  const [selected, setSelected] = useState(ProjectEditTab.General);
  useLayoutEffect(() => {
    const node = root.current;
    const reveal = (event: Event) => { const panel = (event.target as HTMLElement).dataset.projectEditPanel as ProjectEditTab; if (order.includes(panel)) setSelected(panel); };
    node?.addEventListener("project-reveal-invalid", reveal);
    return () => node?.removeEventListener("project-reveal-invalid", reveal);
  }, []);
  return <div className="project-edit-tabs" ref={root}>
    <div role="tablist" aria-label={copy("configuration-fields.projectEdit.tabs")} className="project-edit-tablist">
      {order.map(tab => <button key={tab} ref={node => { if (node) tabs.current.set(tab, node); else tabs.current.delete(tab); }} type="button" role="tab" id={`${id}-${tab}-tab`} aria-controls={`${id}-${tab}-panel`} aria-selected={selected === tab} tabIndex={selected === tab ? 0 : -1} onClick={() => setSelected(tab)} onKeyDown={event => {
        const index = order.indexOf(tab), next = event.key === "ArrowRight" ? order[(index + 1) % order.length] : event.key === "ArrowLeft" ? order[(index + order.length - 1) % order.length] : event.key === "Home" ? order[0] : event.key === "End" ? order[order.length - 1] : undefined;
        if (next) { event.preventDefault(); tabs.current.get(next)?.focus(); }
      }}>{copy(`configuration-fields.projectEdit.${tab}`)}</button>)}
    </div>
    {order.map(tab => <div key={tab} role="tabpanel" id={`${id}-${tab}-panel`} aria-labelledby={`${id}-${tab}-tab`} data-project-edit-panel={tab} hidden={selected !== tab} tabIndex={0}><fieldset disabled={disabled}>{panels[tab]}</fieldset></div>)}
  </div>;
}
