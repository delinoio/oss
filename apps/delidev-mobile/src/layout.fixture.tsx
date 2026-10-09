// SPDX-License-Identifier: Apache-2.0
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  SystemService,
  SessionService,
  InboxService,
} from "@delinoio/delidev-api-client";
import { App } from "./app";
import { ProtectedState, documentBytes, uuid } from "./state";
import "./styles.css";
const parameters = new URLSearchParams(location.search),
  server = uuid(),
  profile = uuid(),
  project = uuid(),
  agent = uuid(),
  runner = uuid(),
  session = uuid();
const resource = (id: string, kind: EntityKind, data: unknown) =>
  create(ResourceSchema, {
    id,
    kind,
    revision: 1n,
    schemaVersion: 1,
    documentJson: documentBytes(data),
  });
const sessions = [
  resource(session, EntityKind.SESSION, {
    name: "A long session name that must wrap without clipping at narrow widths",
    workspace: "worktree",
    agent_id: agent,
    machine_id: runner,
    project_id: project,
    outcome: "not-started",
    archive: "active",
    recovery: "none",
    dispatch: "paused",
  }),
];
const saved = [
  resource(project, EntityKind.PROJECT, {
    name: "Project with a long readable name",
  }),
  resource(agent, EntityKind.AGENT, { name: "Codex Agent" }),
  resource(runner, EntityKind.MACHINE, {
    name: "Remote Runner",
    disabled: false,
  }),
  ...sessions,
];
const transport = createRouterTransport(({ service }) => {
  service(SystemService, {
    getStatus: () => ({
      serverId: server,
      protocolVersion: 1,
      version: "0.1.0",
    }),
  });
  service(ResourceService, {
    getSnapshot: () => ({
      resources: [],
      cursor: "opaque-owned-fixture-cursor",
    }),
    getResource: (r) => ({ resource: saved.find((v) => v.id === r.id) }),
    listResources: (r) => ({
      resources: saved.filter((v) => v.kind === r.filter?.kind),
    }),
    watchEvents: async function* (_r, context) {
      await new Promise<void>((done) =>
        context.signal.addEventListener("abort", () => done(), { once: true }),
      );
    },
  });
  service(SessionService, {
    listSessions: () => ({ sessions }),
    listQueue: () => ({ inputs: [] }),
    createSession: (r) => {
      document.documentElement.dataset.mutations = "1";
      return { change: { requestId: r.requestId, session: sessions[0] } };
    },
  });
  service(InboxService, {
    listInbox: () => ({ entries: [] }),
    getNotificationPreferences: () => ({
      preferences: { revision: 1n, interactions: true, terminals: false },
    }),
  });
});
class FixtureState extends ProtectedState {
  override transport() {
    return transport;
  }
}
let raw = JSON.stringify({
  version: 1,
  profiles: [
    {
      id: profile,
      name: "HTTPS profile",
      origin: "https://example.test",
      serverId: server,
      deviceId: uuid(),
      token: "x".repeat(43),
    },
  ],
  selectedProfile: profile,
  language: parameters.get("language") === "ko" ? "ko" : "en",
  theme: parameters.get("theme") === "dark" ? "dark" : "light",
  notifications: false,
});
const state = new FixtureState({
  read: async () => raw,
  write: async (v) => {
    raw = v;
  },
});
createRoot(document.getElementById("root")!).render(<App state={state} />);
