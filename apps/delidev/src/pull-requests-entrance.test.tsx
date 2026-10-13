// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  InboxService,
  IntegrationService,
  NotificationPreferencesSchema,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";
import {
  dispatchAnimation,
  reducedMotion,
  setAnimation,
} from "./entrance-test-support";

const repositoryId = "0195c9c0-7b13-7000-8000-000000000001";
function fixture() {
  const integrationId = newRequestId();
  const row = create(ResourceSchema, {
    id: repositoryId,
    kind: EntityKind.REPOSITORY,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({
      name: "Example repository",
      integration_id: integrationId,
      github_owner: "owner",
      github_name: "repo",
    }),
  });
  const query = vi.fn(
    async (request: { repositoryId: string; queryJson: Uint8Array }) => {
      const q = JSON.parse(new TextDecoder().decode(request.queryJson));
      return {
        schemaVersion: 1,
        documentJson: encode({
          repository_id: row.id,
          repository_revision: row.revision.toString(),
          profile_id: integrationId,
          generation_id: newRequestId(),
          observed_at: "2026-09-28T00:00:00Z",
          identity: { id: "17", node_id: "U_17", login: "fixture-user" },
          repository: {
            provider: "github.com",
            id: "37",
            node_id: "R_37",
            owner: "owner",
            name: "repo",
            private: true,
          },
          query: q,
          items: [
            {
              provider: "github.com",
              kind: "pull-request",
              identity_source:
                q.operation === "search" ? "issue-api" : "pull-request-api",
              id: "9007199254740993",
              node_id: "ITEM_17",
              number: "17",
              title: "Original fixture title",
              state: "open",
              created_at: "2026-09-01T00:00:00Z",
              updated_at: "2026-09-28T00:00:00Z",
              author: {
                id: "19",
                node_id: "U_19",
                login: "fixture-author",
                kind: "user",
                provider_type: "User",
              },
              url: "https://github.com/owner/repo/pull/17",
              draft: false,
            },
          ],
          ...(q.operation === "search"
            ? { total_count: "1", incomplete: false }
            : {}),
        }),
      };
    },
  );
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({ version: "0.1.0", protocolVersion: 1 }),
    });
    router.service(SessionService, { listSessions: () => ({ sessions: [] }) });
    router.service(ResourceService, {
      listResources: (request: {
        filter?: { kind: EntityKind; pageToken: string };
      }) =>
        request.filter?.kind !== EntityKind.REPOSITORY
          ? { resources: [] }
          : request.filter.pageToken
            ? { resources: [] }
            : { resources: [row], nextPageToken: "repository-next" },
      getResource: () => ({ resource: row }),
    });
    router.service(InboxService, {
      listInbox: () => ({ entries: [] }),
      getNotificationPreferences: () => ({
        preferences: create(NotificationPreferencesSchema, { revision: 1n }),
      }),
    });
    router.service(IntegrationService, { queryRepositoryIntegration: query });
  });
  return { transport, query };
}

const page = () =>
  document.querySelector<HTMLElement>("section.pull-requests-page")!;
const entering = () => page().getAttribute("data-entrance");

async function openPullRequests() {
  fireEvent.click(await screen.findByRole("button", { name: "Pull requests" }));
  return within(
    await screen.findByRole("region", {
      name: "Pull requests navigation and filters",
    }),
  );
}
async function chooseRepository() {
  fireEvent.click(
    await screen.findByRole("button", { name: "Example repository" }),
  );
  await screen.findByText(/Original fixture title/);
}

beforeEach(() => {
  reducedMotion(false);
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("plays one Y4 entrance per destination activation and never for ordinary edits", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  // The retained page is hidden and idle until the destination is activated.
  await screen.findByRole("button", { name: "Pull requests" });
  expect(entering()).toBeNull();
  const pane = await openPullRequests();
  expect(page().hidden).toBe(false);
  expect(entering()).toBe("y4");
  expect(page().hasAttribute("style")).toBe(false);
  dispatchAnimation(page());
  expect(entering()).toBeNull();

  // Repository selection, state change, typing, page size and refresh never replay.
  await chooseRepository();
  expect(entering()).toBeNull();
  const open = pane.getByRole("radio", { name: "Open" });
  fireEvent.click(pane.getByRole("radio", { name: "Closed" }));
  await waitFor(() => expect(value.query.mock.calls.length).toBeGreaterThan(1));
  expect(entering()).toBeNull();
  fireEvent.change(pane.getByLabelText("Search title and body"), {
    target: { value: "fix" },
  });
  fireEvent.change(pane.getByLabelText("PR page size"), {
    target: { value: "5" },
  });
  expect(entering()).toBeNull();
  // The segmented controls keep their mounted identity throughout.
  expect(pane.getByRole("radio", { name: "Open" })).toBe(open);

  // Leaving and returning replays exactly once per return.
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expect(entering()).toBeNull();
  await openPullRequests();
  expect(entering()).toBe("y4");
  dispatchAnimation(page());
  expect(entering()).toBeNull();
});

it("replaces an in-flight entrance on rapid leave and re-entry without a stale completion", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  await openPullRequests();
  expect(entering()).toBe("y4");
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  await openPullRequests();
  expect(entering()).toBe("y4");
  // One completion settles the single replacement activation.
  dispatchAnimation(page());
  expect(entering()).toBeNull();
});

it("suppresses the entrance under Animation Off and reduced motion and never replays on re-enable", async () => {
  const value = fixture();
  const setReduced = reducedMotion(false);
  render(<App transport={value.transport} />);
  await openPullRequests();
  expect(entering()).toBe("y4");
  // A preference change mid-flight settles immediately.
  await setAnimation("false");
  expect(entering()).toBeNull();
  await setAnimation(undefined);
  expect(entering()).toBeNull();
  setReduced(true);
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  await openPullRequests();
  expect(entering()).toBeNull();
  setReduced(false);
  expect(entering()).toBeNull();
  // Animation Off at activation time never starts the motion either.
  await setAnimation("false");
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  await openPullRequests();
  expect(entering()).toBeNull();
  await act(async () => {
    await Promise.resolve();
  });
});

it("transitions only the selected state segment background in 120ms", () => {
  const css = readFileSync(join(__dirname, "styles.css"), "utf8");
  const base = css.match(
    /\.sidebar-pull-requests \.pr-state-choice > span \{([^}]*)\}/u,
  );
  // Unchecked segments, including hover, never animate.
  expect(base?.[1]).not.toMatch(/transition/u);
  const checked = css.match(
    /\.sidebar-pull-requests \.pr-state-choice > input:checked \+ span \{([^}]*)\}/u,
  );
  expect(checked?.[1]).toMatch(
    /transition:\s*background-color 120ms ease-out;/u,
  );
  const transitions = checked?.[1].match(/transition[a-z-]*:/gu) ?? [];
  expect(transitions).toEqual(["transition:"]);
});

it("settles the page entrance before positioning the Refresh tooltip", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  await openPullRequests();
  dispatchAnimation(page());
  await chooseRepository();
  // Leave and return so the retained results sit beneath a fresh entrance.
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  await openPullRequests();
  expect(entering()).toBe("y4");
  const refresh = page().querySelector<HTMLButtonElement>(".pr-list-refresh");
  expect(refresh).not.toBeNull();
  fireEvent.pointerEnter(refresh!);
  expect(entering()).toBeNull();
});
