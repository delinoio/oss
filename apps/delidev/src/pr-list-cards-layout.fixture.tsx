// SPDX-License-Identifier: Apache-2.0
// Browser-only synthetic repositories; this entry has no native or account authority.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
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
  router.service(SystemService, { getStatus: () => ({ protocolVersion: 1 }) });
  router.service(SessionService, { listSessions: () => ({ sessions: [] }) });
  router.service(InboxService, { listInbox: () => ({ entries: [] }), getNotificationPreferences: () => ({ preferences: create(NotificationPreferencesSchema, { revision: 1n }) }) });
  router.service(ResourceService, {
    listResources: request => ({ resources: request.filter?.kind === EntityKind.REPOSITORY ? rows : [] }),
    getResource: request => { requests.repository++; return { resource: rows.find(row => row.id === request.id) }; },
  });
  router.service(IntegrationService, { queryRepositoryIntegration: request => {
    requests.github++;
    const query = JSON.parse(new TextDecoder().decode(request.queryJson));
    const row = rows.find(row => row.id === request.repositoryId)!;
    const config = JSON.parse(new TextDecoder().decode(row.documentJson));
    const numbers = query.number ? [Number(query.number)] : query.page === 1 ? [1674, 974, 1671, 1649] : [];
    const detail = query.operation === "detail";
    return { schemaVersion: 1, documentJson: encode({ repository_id: row.id, repository_revision: "1", profile_id: config.integration_id, generation_id: "0195c9c0-7b13-7000-8000-000000000011", observed_at: "2026-10-07T08:11:52.759123456Z", identity: { id: "17", node_id: "U_17", login: "fixture-user" }, repository: { provider: "github.com", id: "37", node_id: "R_37", owner: config.github_owner, name: config.github_name, private: true }, query, items: numbers.map((number, index) => ({ provider: "github.com", kind: "pull-request", identity_source: query.operation === "search" ? "issue-api" : "pull-request-api", id: String(number + 10000), node_id: `ITEM_${number}`, number: String(number), title: index === 0 ? "feat(delidev): preserve original account authority with a long fully wrapping title ".repeat(5) : `Fixture pull request ${number}`, state: query.state === "closed" ? "closed" : "open", created_at: "2026-09-01T00:00:00Z", updated_at: "2026-10-07T08:07:22.123456789Z", author: { id: "19", node_id: "U_19", login: index === 0 ? "long-author-".repeat(7) : "kdy1", kind: index === 0 ? "unknown" : "user", provider_type: index === 0 ? "FutureActor" : "User" }, url: `https://github.com/${config.github_owner}/${config.github_name}/pull/${number}`, draft: index === 0, ...(detail ? { body: "Original fixture body", merged: false, base_ref: "main", base_sha: "a".repeat(40), head_ref: "feature", head_sha: "b".repeat(40) } : {}) })), ...(!detail && query.page === 1 ? { next_page: 2 } : {}), ...(query.operation === "search" ? { total_count: "4", incomplete: true } : {}) }) };
  } });
});
createRoot(document.getElementById("root")!).render(<AppearanceProvider bridge={{ read: async () => ({ revision: 1, theme, problem: null }), update: async next => ({ revision: 2, theme: next, problem: null }), subscribe: async () => () => {} }}><App transport={transport} /></AppearanceProvider>);
