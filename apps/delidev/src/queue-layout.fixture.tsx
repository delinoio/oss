// SPDX-License-Identifier: Apache-2.0
// Synthetic queued-message More menu layout fixture; grants no account or
// native access. Three compact queued rows sit in the real scrolling
// `.queue-compact-list` above a composer-like form, so any displacement of a
// neighbor row or the composer by the More menu is visible as a moved rectangle.
import { create } from "@bufbuild/protobuf";
import { createRoot } from "react-dom/client";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  EntityKind,
  ResourceSchema,
  SessionService,
  SkillService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { QueuedInput } from "./queue";
import "./themes.css";
import "./styles.css";
import "./session.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") === "ko" ? "ko" : "en");
document.documentElement.dataset.theme =
  args.get("theme") === "dark" ? "dark" : "light";
// "top" leaves room below the tray; "bottom" pins the tray and composer to the
// bottom edge so the menu of the last row must flip above its anchor.
const place = args.get("place") === "bottom" ? "bottom" : "top";
// "tray" nests the list in the real `.session-input-tray` (max-height 30vh,
// overflow auto) followed by tall content, so the tray can scroll the whole
// list away independently of the list's own scrolling.
const tray = args.get("tray") === "1";
const win = window as unknown as {
  __queueMutations: unknown[];
  __queueMoves: string[];
};
win.__queueMutations = [];
win.__queueMoves = [];
const sessionId = newRequestId();
const session = create(ResourceSchema, {
  id: sessionId,
  sessionId,
  kind: EntityKind.SESSION,
  schemaVersion: 1,
  revision: 4n,
  documentJson: encode({
    machine_id: newRequestId(),
    agent_id: newRequestId(),
    outcome: "running",
    archive: "active",
    active_execution_id: "execution",
    execution: { execution_id: "execution", native_turn_id: "turn" },
  }),
});
const rows = ["First", "Second", "Third", "Fourth"].map((name, index) =>
  create(ResourceSchema, {
    id: newRequestId(),
    sessionId,
    kind: EntityKind.QUEUE,
    schemaVersion: 1,
    revision: 6n,
    documentJson: encode({
      prompt: `${name} queued synthetic message ${"with a long preview ".repeat(6)}`,
      sequence: index + 1,
      delivery: "queued",
      mode: "plan",
    }),
  }),
);
// A mutation on open, close or placement would land here and fail the script.
const transport = createRouterTransport((router) => {
  router.service(SessionService, {
    editQueuedInput: (request) => {
      win.__queueMutations.push({ edit: request.prompt });
      return {};
    },
    steerQueuedInput: () => {
      win.__queueMutations.push({ steer: true });
      return {};
    },
    removeQueuedInput: () => {
      win.__queueMutations.push({ remove: true });
      return {};
    },
  });
  router.service(SkillService, { listSkills: () => ({ skills: [] }) });
});
const client = new QueryClient({
  defaultOptions: { queries: { retry: false } },
});
function Fixture() {
  const list = (
    <div className="queue-compact-list" style={{ maxHeight: 112 }}>
      {rows.map((row, index) => (
        <div className="queue-drop-row" key={row.id}>
          <QueuedInput
            compact
            resource={row}
            session={session}
            refresh={() => {}}
            movement={{
              disabled: false,
              up: index
                ? () => win.__queueMoves.push(`up:${index}`)
                : undefined,
              down:
                index < rows.length - 1
                  ? () => win.__queueMoves.push(`down:${index}`)
                  : undefined,
              start: () => {},
              end: () => {},
            }}
          />
        </div>
      ))}
    </div>
  );
  return (
    <div
      data-scroll-owner
      className="queue-layout-frame"
      style={{
        height: "100dvh",
        display: "flex",
        flexDirection: "column",
        justifyContent: place === "bottom" ? "flex-end" : "flex-start",
        overflow: "auto",
        background: "var(--surface)",
        color: "var(--text)",
      }}
    >
      <p data-outside style={{ margin: 12 }}>
        Outside paragraph that owns no behavior.
      </p>
      {/* The list height is capped so it scrolls like the real tray does. */}
      {tray ? (
        <div className="session-input-tray" data-tray>
          {list}
          <div data-tray-spacer style={{ height: 1200 }} />
        </div>
      ) : (
        list
      )}
      <form
        data-composer
        style={{ margin: 12, display: "flex", gap: 8 }}
        onSubmit={(event) => event.preventDefault()}
      >
        <textarea aria-label="Composer" rows={2} style={{ flex: 1 }} />
        <button type="button">Send</button>
      </form>
    </div>
  );
}
createRoot(document.getElementById("root")!).render(
  <TransportProvider transport={transport}>
    <QueryClientProvider client={client}>
      <MutationIntents>
        <Fixture />
      </MutationIntents>
    </QueryClientProvider>
  </TransportProvider>,
);
