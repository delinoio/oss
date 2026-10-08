// SPDX-License-Identifier: Apache-2.0
// Synthetic App transport: no account, native process or external RPC authority.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { EntityKind, InboxReadState, InboxService, InboxSource, InboxViewSchema, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SystemService } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { AppearanceProvider, Theme } from "./appearance";
import { encode } from "./documents";
import { i18n, SupportedLanguage } from "./localization";
import "./themes.css";
import "./styles.css";
const args = new URLSearchParams(location.search);
void i18n.changeLanguage(args.get("language") === "ko" ? SupportedLanguage.Korean : SupportedLanguage.English);
const theme = args.get("theme") === "dark" ? Theme.Dark : Theme.Light;
const id = (last: number) => `0195c9c0-7b13-7000-8000-${last.toString().padStart(12, "0")}`;
const session = create(ResourceSchema, { id: id(1), kind: EntityKind.SESSION, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Synthetic original conversation", archive: "active", dispatch: "paused", recovery: "none" }) });
const project = create(ResourceSchema, { id: id(2), kind: EntityKind.PROJECT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Synthetic project" }) });
const view = create(InboxViewSchema, { session, entry: create(ResourceSchema, { id: id(3), sessionId: session.id, kind: EntityKind.INBOX, revision: 1n, schemaVersion: 1, createdAt: "2026-10-08T00:00:00Z", documentJson: encode({ source: "execution-terminal", source_id: id(4), read_state: "unread", terminal: { outcome: "succeeded", execution_id: id(4) } }) }) });
const state = { requests: [] as { readState: InboxReadState; source: InboxSource; projectId: string; sessionId: string; pageToken: string }[], writes: 0 };
Object.defineProperty(window, "__inboxFilterFixture", { value: state });
const transport = createRouterTransport(router => {
 router.service(SystemService, { getStatus: () => ({ protocolVersion: 1 }) });
 router.service(SessionService, { listSessions: () => ({ sessions: [] }) });
 router.service(InboxService, { getNotificationPreferences: () => ({ preferences: create(NotificationPreferencesSchema, { revision: 1n }) }), listInbox: request => {
  state.requests.push({ readState: request.readState, source: request.source, projectId: request.projectId, sessionId: request.sessionId, pageToken: request.pageToken });
  return { entries: request.readState === InboxReadState.READ || request.source === InboxSource.INTERACTION || request.source === InboxSource.SUBSCRIPTION_RECOVERY ? [] : [view], nextPageToken: "" };
 }, getInboxEntry: () => ({ view }), setInboxReadState: () => { state.writes++; return { view }; } });
 router.service(ResourceService, { listResources: request => ({ resources: request.filter?.kind === EntityKind.PROJECT ? [project] : request.filter?.kind === EntityKind.SESSION ? [session] : [] }), getResource: request => ({ resource: request.id === project.id ? project : session }) });
});
createRoot(document.getElementById("root")!).render(<AppearanceProvider bridge={{ read: async () => ({ revision: 1, theme, problem: null }), update: async value => ({ revision: 2, theme: value, problem: null }), subscribe: async () => () => {} }}><App transport={transport} /></AppearanceProvider>);
