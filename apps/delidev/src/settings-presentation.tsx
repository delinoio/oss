// SPDX-License-Identifier: Apache-2.0
import { copy, useLocale } from "./localization";
import { useContext, type ReactNode } from "react";
import { SettingsTaskContext } from "./settings-task-context";
import "./settings-presentation.css";

// These helpers own markup only. Reads, eligibility, pagination, mutations and
// visit lifetime remain with each category's existing controller.
export function SettingsHeading({ title, description, scope = copy("settings-presentation.extra.93dbee418838"), actions }: { title: string; description?: string; scope?: string; actions?: ReactNode }) {
  useLocale();
  const task = useContext(SettingsTaskContext);
  return <header className="settings-category-heading">
    <div className="settings-category-title"><h1 hidden={Boolean(task)} aria-live="polite" aria-atomic="true">{title}</h1>{description ? <p>{description}</p> : null}{scope ? <p className="settings-scope">{scope}</p> : null}</div>
    {actions ? <div className="settings-toolbar">{actions}</div> : null}
  </header>;
}

export function SettingsEmpty({ title, children, icon }: { title: string; children: ReactNode; icon?: ReactNode }) {
  useLocale();
  return <section className="settings-empty" aria-label={title}>
    <span className="settings-empty-icon" aria-hidden="true">{icon ?? <svg viewBox="0 0 24 24" focusable="false"><path d="M4 6h6l2 2h8v12H4zM4 6V4h6l2 2" /></svg>}</span>
    <div><h2>{title}</h2>{children}</div>
  </section>;
}

export function SettingsLoading({ label }: { label: string }) {
  useLocale();
  return <div className="settings-loading"><p role="status">{label}</p><div aria-hidden="true">{[0, 1].map(row => <div className="settings-skeleton-row" key={row}><span /><span /></div>)}</div></div>;
}
