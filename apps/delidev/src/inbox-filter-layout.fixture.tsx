// SPDX-License-Identifier: Apache-2.0
// Synthetic App transport: no account, native process or external RPC authority.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { EntityKind, InboxReadState, InboxService, InboxSource, InboxViewSchema, InteractionService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SystemService } from "@delinoio/delidev-api-client";
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
const presentation = args.has("presentation");
const views = [view];
if (presentation) {
 for (const [index, outcome] of ["failed", "stopped"].entries()) {
  const sibling = create(ResourceSchema, { ...session, id: id(10 + index), documentJson: encode({ name: index ? "Stopped original session" : "Long original conversation 이름과 번역이 온전히 보이는 긴 대화 이름 ".repeat(5), archive: "active", dispatch: "paused", recovery: "none", outcome: "succeeded" }) });
  views.push(create(InboxViewSchema, { session: sibling, entry: create(ResourceSchema, { ...view.entry!, id: id(20 + index), sessionId: sibling.id, documentJson: encode({ source: "execution-terminal", source_id: id(30 + index), read_state: "unread", terminal: { job_id: id(40 + index), input_id: id(50 + index), execution_id: id(30 + index), native_thread_id: "original-native-thread", native_turn_id: "original-native-turn", sequence: 17, outcome } }) }) }));
 }
 const original = JSON.parse(new TextDecoder().decode(view.entry!.documentJson));
 view.entry!.documentJson = encode({ ...original, terminal: { ...original.terminal, job_id: id(40), input_id: id(50), native_thread_id: "original-native-thread", native_turn_id: "original-native-turn", sequence: 17 } });
 const questionSession = create(ResourceSchema, { ...session, id: id(60), documentJson: encode({ name: "Original question session", archive: "active", dispatch: "claimed", recovery: "none", active_execution_id: id(61) }) });
 const interaction = create(ResourceSchema, { id: id(62), sessionId: questionSession.id, kind: EntityKind.INTERACTION, revision: 3n, schemaVersion: 1, documentJson: encode({ type: "user-question", execution_id: id(61), closure: "open", questions: { questions: [{ id: "method", header: "Original question", text: "Which original method should be supported?", secret: false, other: true, options: [] }] } }) });
 views.push(create(InboxViewSchema, { session: questionSession, interaction, entry: create(ResourceSchema, { ...view.entry!, id: id(63), sessionId: questionSession.id, documentJson: encode({ source: "interaction", source_id: interaction.id, read_state: "unread" }) }) }));
 for (let index = 0; index < 8; index++) views.push(create(InboxViewSchema, { session, entry: create(ResourceSchema, { ...view.entry!, id: id(90 + index), documentJson: encode({ source: "execution-terminal", source_id: id(100 + index), read_state: "unread", terminal: { outcome: "succeeded", execution_id: id(100 + index) } }) }) }));
 const account = create(ResourceSchema, { id: id(70), kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Original quota account" }) });
 views.push(create(InboxViewSchema, { account, entry: create(ResourceSchema, { ...view.entry!, id: id(71), sessionId: "", documentJson: encode({ source: "subscription-recovery", read_state: "unread", recovery: { account_id: account.id, connection_id: id(72), observed_at: "2026-10-08T00:00:00Z" } }) }) }));
}
const state = { reads: 0, answers: 0, requests: [] as { readState: InboxReadState; source: InboxSource; projectId: string; sessionId: string; pageToken: string }[], writes: 0 };
Object.defineProperty(window, "__inboxFilterFixture", { value: state });
const transport = createRouterTransport(router => {
 router.service(SystemService, { getStatus: () => ({ protocolVersion: 1 }) });
 router.service(SessionService, { listSessions: () => ({ sessions: [] }) });
 router.service(InboxService, { getNotificationPreferences: () => ({ preferences: create(NotificationPreferencesSchema, { revision: 1n }) }), listInbox: request => {
  state.requests.push({ readState: request.readState, source: request.source, projectId: request.projectId, sessionId: request.sessionId, pageToken: request.pageToken });
  return { entries: presentation ? views.filter(item => {
   const source = JSON.parse(new TextDecoder().decode(item.entry!.documentJson)).source;
   return request.readState !== InboxReadState.READ && (request.source === InboxSource.UNSPECIFIED || request.source === InboxSource.INTERACTION && source === "interaction" || request.source === InboxSource.EXECUTION_TERMINAL && source === "execution-terminal" || request.source === InboxSource.SUBSCRIPTION_RECOVERY && source === "subscription-recovery") && (!request.projectId || item.entry!.projectId === request.projectId) && (!request.sessionId || item.entry!.sessionId === request.sessionId);
  }) : request.readState === InboxReadState.READ || request.source === InboxSource.INTERACTION || request.source === InboxSource.SUBSCRIPTION_RECOVERY ? [] : [view], nextPageToken: "" };
 }, getInboxEntry: request => { state.reads++; const selected = views.find(item => item.entry!.id === request.id) ?? view; return { view: create(InboxViewSchema, { ...selected, entry: create(ResourceSchema, { ...selected.entry!, revision: BigInt(state.reads + 1) }) }) }; }, setInboxReadState: () => { state.writes++; return { view }; } });
 router.service(InteractionService, { respondQuestion: () => { state.answers++; return { interaction: views.find(item => item.interaction)?.interaction }; } });
 router.service(ResourceService, { listResources: request => ({ resources: request.filter?.kind === EntityKind.PROJECT ? [project] : request.filter?.kind === EntityKind.SESSION ? [session] : [] }), getResource: request => ({ resource: request.id === project.id ? project : session }) });
});
createRoot(document.getElementById("root")!).render(<AppearanceProvider bridge={{ read: async () => ({ revision: 1, theme, problem: null }), update: async value => ({ revision: 2, theme: value, problem: null }), subscribe: async () => () => {} }}><App transport={transport} /></AppearanceProvider>);
