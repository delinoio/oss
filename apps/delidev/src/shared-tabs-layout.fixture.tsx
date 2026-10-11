// SPDX-License-Identifier: Apache-2.0
// Synthetic geometry and component evidence; no native resource authority.
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  ProjectEditTab,
  ProjectEditTabs,
  revealProjectInvalidControl,
} from "./project-edit-tabs";
import {
  SettingsTaskActions,
  SettingsTaskDialog,
  SettingsDialogSize,
} from "./settings-task";
import { SessionTabBar } from "./session-tab-bar";
import { SessionTabKind, sessionTabKey, type SessionTab } from "./session-tabs";
import { Comparison } from "./session-diff-model";
import { i18n } from "./localization";
import "./themes.css";
import "./styles.css";
import "./settings-presentation.css";
import "./usage.css";
import "./backups.css";
import "./session.css";
import "./terminal-dock.css";
import "./shared-tabs-layout.fixture.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") ?? "en");
document.documentElement.dataset.theme = args.get("theme") ?? "light";
document.body.style.zoom = args.get("zoom") ?? "1";
if (args.get("theme") === "custom") {
  for (const [name, value] of Object.entries({
    "selected-text": "#74298a",
    "selected-border": "#74298a",
    focus: "#a74618",
    muted: "#54535e",
    border: "#bab3c2",
  }))
    document.documentElement.style.setProperty(`--${name}`, value);
}
const tabs: SessionTab[] = [
  { kind: SessionTabKind.Conversation },
  { kind: SessionTabKind.Files },
  {
    kind: SessionTabKind.File,
    repository: "original",
    path: "long/".repeat(35) + "original.ts",
  },
  {
    kind: SessionTabKind.Comparison,
    repository: "original",
    path: "original.ts",
    comparison: Comparison.Staged,
  },
  { kind: SessionTabKind.Terminal, id: "original-terminal", number: 3 },
  {
    kind: SessionTabKind.Page,
    profile: "original-profile",
    id: "original-page",
    title: "Very long browser title ".repeat(15),
    label: "https://fixture.test/original",
  },
  {
    kind: SessionTabKind.Sidechat,
    id: "original-child",
    name: "Retained sidechat",
  },
  { kind: SessionTabKind.Diagnostics },
];
function Fixture() {
  const [project, setProject] = useState(true),
    [items, setItems] = useState(tabs),
    [selected, select] = useState(sessionTabKey(tabs[5]!));
  return (
    <>
      <div className="session-workspace fixture-workspace">
        <SessionTabBar
          id="fixture"
          tabs={items}
          selected={selected}
          select={select}
          close={(key) => {
            const at = items.findIndex((tab) => sessionTabKey(tab) === key);
            if (key === selected)
              select(sessionTabKey(items[Math.max(0, at - 1)]!));
            setItems(items.filter((tab) => sessionTabKey(tab) !== key));
          }}
        />
        <div className="fixture-panel">
          <input
            aria-label="Retained workspace draft"
            defaultValue="Original draft"
          />
        </div>
      </div>
      <div className="fixture-secondary">
        {(
          [
            "usage-tabs",
            "backups-tabs",
            "terminal-tabs",
            "terminal-tabs",
          ] as const
        ).map((name, index) => (
          <div
            key={index}
            className={`${name} shared-tab-strip`}
            role={index === 3 ? "group" : "tablist"}
            aria-label={`Synthetic ${index}`}
          >
            {[
              "First fixed label",
              "Second fixed label",
              "Third fixed label",
            ].map((label, at) => (
              <button
                key={label}
                type="button"
                className="shared-tab-item shared-tab-control shared-tab-label"
                role={index === 3 ? undefined : "tab"}
                aria-selected={index === 3 ? undefined : at === 1}
                data-selected={index === 3 ? at === 1 : undefined}
              >
                {label}
              </button>
            ))}
          </div>
        ))}
      </div>
      {project ? (
        <SettingsTaskDialog
          title="Edit project"
          size={SettingsDialogSize.Form}
          close={() => setProject(false)}
        >
          <form
            className="project-editor"
            onInvalidCapture={revealProjectInvalidControl}
            onSubmit={(event) => event.preventDefault()}
          >
            <ProjectEditTabs
              disabled={false}
              panels={{
                [ProjectEditTab.General]: (
                  <label>
                    Name
                    <input
                      aria-label="Project draft"
                      required
                      defaultValue="Original project"
                    />
                  </label>
                ),
                [ProjectEditTab.Repositories]: (
                  <div className="fixture-tall">
                    <label>
                      Required repository
                      <input aria-label="Required repository" required />
                    </label>
                  </div>
                ),
                [ProjectEditTab.Execution]: <p>Original execution controls</p>,
                [ProjectEditTab.Access]: <p>Original access controls</p>,
              }}
            />
            <SettingsTaskActions>
              <button type="submit">Save Project</button>
            </SettingsTaskActions>
          </form>
        </SettingsTaskDialog>
      ) : null}
    </>
  );
}
createRoot(document.getElementById("root")!).render(
  <QueryClientProvider client={new QueryClient()}>
    <Fixture />
  </QueryClientProvider>,
);
