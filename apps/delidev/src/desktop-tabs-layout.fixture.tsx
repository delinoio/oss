// SPDX-License-Identifier: Apache-2.0
// Synthetic CSS/keyboard fixture; no native, account or execution authority.
import { createRoot } from "react-dom/client";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ProjectEditTab, ProjectEditTabs } from "./project-edit-tabs";
import { SettingsDialogSize, SettingsTaskDialog } from "./settings-task";
import { SessionTabBar } from "./session-tab-bar";
import { SessionTabKind, type SessionTab } from "./session-tabs";
import { i18n } from "./localization";
import { revealDesktopTab } from "./desktop-tabs";
import { useState, type ReactNode } from "react";
import "./themes.css";
import "./styles.css";
import "./settings-presentation.css";
import "./usage.css";
import "./backups.css";
import "./session.css";
import "./terminal-dock.css";
import "./desktop-tabs-layout.fixture.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") ?? "en");
document.documentElement.dataset.theme = args.get("theme") ?? "light";
if (args.get("theme") === "custom") {
  document.documentElement.style.setProperty("--accent", "rgb(130, 50, 170)");
  document.documentElement.style.setProperty("--selected-text", "rgb(130, 50, 170)");
}
if (args.get("zoom") === "2") document.body.style.zoom = "2";
const labels = Array.from({ length: 12 }, (_, n) => `${n + 1} ${args.get("language") === "ko" ? "긴 탭 이름" : "Long tab label"} ${"retained-".repeat(10)}`);
function Controls({ name, closeable, role }: { name: string; closeable?: boolean; role?: "tablist" | "group" }) {
  const [selected, select] = useState(0);
  return <div className={name.includes("terminal") ? "terminal-dock" : name === "backups-tabs" ? "backups-settings" : name === "usage-tabs" ? "usage-page" : "browser-tab-bar"}><div data-fixture-strip={name} role={role} className={`${name} desktop-tab-strip`} onFocusCapture={event => revealDesktopTab(event.target as HTMLElement)}>{labels.map((label, index) => closeable ? <div key={index} className={`desktop-tab-item${selected === index ? " is-selected" : ""}`}><button className="desktop-tab-label" disabled={index === 3} onClick={() => select(index)}>{label}</button><button className="desktop-tab-close" disabled={index === 3} aria-label={`Close ${label}`}>×</button></div> : <button key={index} role={role === "tablist" ? "tab" : undefined} aria-selected={role === "tablist" ? selected === index : undefined} className={`desktop-tab-item desktop-tab-label${selected === index ? " is-selected" : ""}`} onClick={() => select(index)}>{label}</button>)}</div></div>;
}
function Fixture() {
  const [project, showProject] = useState(false), [selected, select] = useState("conversation");
  const tabs: SessionTab[] = [{ kind: SessionTabKind.Conversation } as const, ...labels.map((path, index) => ({ kind: SessionTabKind.File as const, repository: "synthetic", path: `${index}/${path}` }))];
  return <main className="desktop-tabs-fixture"><h1>Desktop tab fixture</h1><button onClick={() => showProject(true)}>Open Project</button>
    <SessionTabBar tabs={tabs} selected={selected} select={select} close={() => {}} id="fixture" />
    <div className="settings-content" aria-label="Synthetic Settings"><div className="settings-content-column"><Controls name="usage-tabs" role="tablist" /><Controls name="backups-tabs" role="tablist" /><input aria-label="Ordinary input" /><button>Ordinary Save</button></div></div>
    <Controls name="browser-tabs" closeable /><Controls name="browser-pages" role="group" closeable /><Controls name="terminal-tabs" role="tablist" /><Controls name="terminal-tabs" role="group" />
    {project ? <SettingsTaskDialog title="Project" size={SettingsDialogSize.Form} close={() => showProject(false)}><div><form className="project-editor"><ProjectEditTabs disabled={false} panels={Object.fromEntries(Object.values(ProjectEditTab).map(tab => [tab, <label key={tab}>{tab}<input defaultValue="Retained Project draft" /></label>])) as Record<ProjectEditTab, ReactNode>} /><button>Save</button></form></div></SettingsTaskDialog> : null}
  </main>;
}
createRoot(document.getElementById("root")!).render(<TransportProvider transport={createRouterTransport(() => {})}><QueryClientProvider client={new QueryClient()}><Fixture /></QueryClientProvider></TransportProvider>);
