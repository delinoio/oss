// SPDX-License-Identifier: Apache-2.0
// Browser-only synthetic repositories; this entry has no native or account authority.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { EntityKind, InboxService, IntegrationService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SystemService } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { AppearanceProvider, Theme } from "./appearance";
import { encode } from "./documents";
import { i18n, SupportedLanguage } from "./localization";
import "./themes.css";
import "./styles.css";

const args = new URLSearchParams(location.search);
const theme = args.get("theme") === "dark" ? Theme.Dark : Theme.Light;
void i18n.changeLanguage(args.get("language") === "ko" ? SupportedLanguage.Korean : SupportedLanguage.English);
const requests = { github: 0, repository: 0 };
// Layout checks can inspect only synthetic read counts, never product state or credentials.
Object.defineProperty(window, "__prSidebarFixture", { value: requests });
const rows = args.get("empty") === "true" ? [] : ["oss", "delidev", args.get("long") === "true" ? "long-repository-name-".repeat(18) : "docs"].map((name, index) => create(ResourceSchema, {
  id: `0195c9c0-7b13-7000-8000-00000000000${index + 1}`, kind: EntityKind.REPOSITORY, schemaVersion: 1, revision: 1n,
  documentJson: encode({ name, integration_id: "0195c9c0-7b13-7000-8000-000000000010", github_owner: args.get("long") === "true" ? "long-owner-".repeat(8) : "delinoio", github_name: name }),
}));
const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ protocolVersion: 2 }) });
  router.service(SessionService, { listSessions: () => ({ sessions: [] }) });
  router.service(InboxService, { listInbox: () => ({ entries: [] }), getNotificationPreferences: () => ({ preferences: create(NotificationPreferencesSchema, { revision: 1n }) }) });
  router.service(ResourceService, {
    listResources: request => ({ resources: request.filter?.kind === EntityKind.REPOSITORY ? rows : [] }),
    getResource: request => { requests.repository++; return { resource: rows.find(row => row.id === request.id) }; },
  });
  router.service(IntegrationService, { queryRepositoryIntegration: () => { requests.github++; throw new ConnectError("Synthetic observation unavailable", Code.Unavailable); } });
});
createRoot(document.getElementById("root")!).render(<AppearanceProvider bridge={{ read: async () => ({ revision: 1, theme, problem: null }), update: async next => ({ revision: 2, theme: next, problem: null }), subscribe: async () => () => {} }}><App transport={transport} /></AppearanceProvider>);
