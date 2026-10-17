// SPDX-License-Identifier: Apache-2.0
// Synthetic Session Diff options layout fixture; grants no account, repository
// or native access. The diff is read-only fixture text inside the real
// `.session-diff` frame, so any displacement of the diff or following content by
// the options popup is visible as a moved rectangle.
import { createRoot } from "react-dom/client";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  ResourceService,
  SessionService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { SessionDiff } from "./session-diff";
import "./themes.css";
import "./styles.css";
import "./session.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") === "ko" ? "ko" : "en");
document.documentElement.dataset.theme =
  args.get("theme") === "dark" ? "dark" : "light";
const win = window as unknown as { __diffReads: string[] };
win.__diffReads = [];
const sessionId = newRequestId();
const repository = newRequestId();
const reply = (value: object) => ({
  documentJson: encode({
    size: "0",
    binary: false,
    truncated: false,
    ...value,
  }),
});
const patch = Array.from(
  { length: 60 },
  (_, line) => `+synthetic diff line ${line} ${"padding ".repeat(8)}`,
).join("\n");
const transport = createRouterTransport((router) => {
  router.service(SessionService, {
    readSessionWorkspace: (request) => {
      const query = JSON.parse(new TextDecoder().decode(request.queryJson));
      win.__diffReads.push(String(query.operation));
      if (query.operation === "roots")
        return reply({
          roots: [
            { repository_id: repository, name: "Primary", primary: true },
          ],
        });
      if (query.operation === "git-diff-options")
        return reply({
          diff_options: {
            version: 1,
            repository_id: repository,
            choices: [
              {
                reference: { type: "local-branch", name: "main" },
                available: true,
                configured: false,
              },
            ],
            default: { type: "local-branch", name: "main" },
          },
        });
      return reply({
        diff: {
          comparison: query.comparison,
          repository_id: repository,
          path: query.path,
          base: "commit",
          base_object: "a".repeat(40),
          head_commit: "a".repeat(40),
          patch,
          untracked: [],
          revision: "b".repeat(64),
          ...(query.comparison === "branch"
            ? {
                base_ref: query.base_ref,
                base_commit: "a".repeat(40),
                merge_base: "a".repeat(40),
              }
            : {}),
        },
      });
    },
    readSessionReviewContext: () => {
      throw new Error("unsupported context");
    },
  });
  router.service(ResourceService, {
    listResources: () => ({ resources: [] }),
  });
});
const client = new QueryClient({
  defaultOptions: { queries: { retry: false } },
});
createRoot(document.getElementById("root")!).render(
  <TransportProvider transport={transport}>
    <QueryClientProvider client={client}>
      <MutationIntents>
        {/* SessionDiff renders its own aside; the real host is the narrow,
            scrolling Session app panel. */}
        <div
          data-scroll-owner
          className="session-app-panel"
          style={{
            width: "min(400px, 100%)",
            maxHeight: "100dvh",
            marginLeft: "auto",
            overflow: "auto",
            background: "var(--surface)",
          }}
        >
          <SessionDiff sessionId={sessionId} worktree close={() => {}} />
        </div>
      </MutationIntents>
    </QueryClientProvider>
  </TransportProvider>,
);
