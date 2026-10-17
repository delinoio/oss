// SPDX-License-Identifier: Apache-2.0
// Synthetic Reasoning effort layout fixture; grants no account or native access.
import { createRoot } from "react-dom/client";
import { useState, type ReactNode } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { SystemCapability, SystemService } from "@delinoio/delidev-api-client";
import { CodexSubagentConfiguration } from "./codex-subagent-configuration";
import type { Document } from "./documents";
import { i18n } from "./localization";
import {
  ReasoningEffortField,
  codexEffortSuggestions,
} from "./reasoning-effort-field";
import { SettingsTaskDialog, SettingsTasks } from "./settings-task";
import { SettingsDialogSize } from "./settings-task-context";
import "./themes.css";
import "./styles.css";
import "./settings-presentation.css";
import "./agent-configuration.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") === "ko" ? "ko" : "en");
document.documentElement.dataset.theme =
  args.get("theme") === "dark" ? "dark" : "light";
const host = args.get("host") === "dialog" ? "dialog" : "page";
// A short page leaves the scrolling content pane taller than the content
// column, so the column and the pane report different bottom edges.
const short = args.get("layout") === "short";
const transport = createRouterTransport((router) =>
  router.service(SystemService, {
    getStatus: () => ({
      capabilities: [SystemCapability.CODEX_SUBAGENT_CONFIGURATION_V1],
    }),
  }),
);
const client = new QueryClient({
  defaultOptions: { queries: { retry: false } },
});
// The two consumers of the shared field: the primary Reasoning effort field
// and the Codex Subagent effort field of the Codex subagent fieldset. The
// controls around them make any displacement visible as a moved rectangle.
function Form() {
  const [effort, setEffort] = useState("");
  const [options, setOptions] = useState<Document>({});
  return (
    <form
      className="agent-configuration"
      onSubmit={(event) => event.preventDefault()}
    >
      <label>
        Name
        <input defaultValue="Fixture worker" />
      </label>
      <ReasoningEffortField
        label="Reasoning effort"
        value={effort}
        suggestions={codexEffortSuggestions}
        change={setEffort}
      />
      <label>
        Following control
        <input defaultValue="Following value" />
      </label>
      <p data-outside>Outside paragraph that owns no behavior.</p>
      <CodexSubagentConfiguration
        active
        options={options}
        change={(key, value) =>
          setOptions((previous) => ({ ...previous, [key]: value }))
        }
      />
      {short ? null : (
        <label>
          Trailing control
          <input defaultValue="Trailing value" />
        </label>
      )}
      {short ? null : <div style={{ height: 480 }} data-filler />}
    </form>
  );
}
function Frame({ children }: { children: ReactNode }) {
  // The scroll owner fills the viewport like the application's content pane.
  return (
    <div
      data-scroll-owner
      style={{
        height: "100dvh",
        overflow: "auto",
        background: "var(--surface)",
      }}
    >
      {children}
    </div>
  );
}
function Fixture() {
  return host === "dialog" ? (
    <Frame>
      <main className="settings-content" aria-label="Reasoning fixture">
        <SettingsTasks>
          <h1>Fixture category</h1>
          <SettingsTaskDialog
            title="Edit Worker"
            size={SettingsDialogSize.Form}
            close={() => {}}
          >
            <Form />
          </SettingsTaskDialog>
        </SettingsTasks>
      </main>
    </Frame>
  ) : (
    <Frame>
      <main className="settings-content" aria-label="Reasoning fixture">
        <div className="settings-content-column">
          <Form />
        </div>
      </main>
    </Frame>
  );
}
createRoot(document.getElementById("root")!).render(
  <TransportProvider transport={transport}>
    <QueryClientProvider client={client}>
      <Fixture />
    </QueryClientProvider>
  </TransportProvider>,
);
