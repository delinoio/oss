// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  ScheduleService,
  WorkerService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import {
  dispatchAnimation,
  reducedMotion,
  setAnimation,
} from "./entrance-test-support";
import { MutationIntents } from "./mutation";
import { Schedules } from "./schedules";

function schedule(name: string): Resource {
  return create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SCHEDULE,
    revision: 3n,
    schemaVersion: 1,
    documentJson: encode({
      definition: {
        name,
        enabled: false,
        prompt: "Retained prompt",
        project_id: newRequestId(),
        agent_id: newRequestId(),
        machine_id: newRequestId(),
        workspace: "worktree",
        mode: "plan",
        cron: "0 9 * * 1-5",
        timezone: "Asia/Seoul",
        overlap: "wait",
      },
      configuration_revision: 2,
      created_by: newRequestId(),
      last_occurrence: 9,
      next_run_at: "2026-09-28T00:00:00Z",
    }),
  });
}

const first = schedule("First entrance schedule");
const second = schedule("Second entrance schedule");
const occurrence = create(ResourceSchema, {
  id: newRequestId(),
  kind: EntityKind.OCCURRENCE,
  revision: 1n,
  schemaVersion: 1,
  documentJson: encode({
    schedule_id: first.id,
    state: "waiting",
    trigger: "manual",
    overlap: "wait",
  }),
});

function fixture() {
  const run = vi.fn(async (_request: unknown) => ({ occurrence }));
  const transport = createRouterTransport((router) => {
    router.service(ScheduleService, {
      listSchedules: () => ({ schedules: [first, second] }),
      getSchedule: (request) => ({
        schedule: request.id === second.id ? second : first,
      }),
      runScheduleNow: run,
      listScheduleOccurrences: () => ({ occurrences: [occurrence] }),
      getScheduleOccurrence: () => ({ occurrence }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [], nextPageToken: "" }),
      getResource: () => ({}),
    });
    router.service(WorkerService, {});
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const view = (
    active = true,
    notification?: { id: string; scheduleId: string; generation: string },
  ) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <Schedules
            active={active}
            open={() => {}}
            notificationOccurrence={notification}
            clearNotificationOccurrence={() => {}}
          />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  return { view, run, client };
}

const page = () => document.querySelector<HTMLElement>("div.schedule-page")!;
const entering = () => page().getAttribute("data-entrance");
const animated = () => document.querySelectorAll("[data-entrance]").length;
const notification = (generation: string) => ({
  id: occurrence.id,
  scheduleId: first.id,
  generation,
});

beforeEach(() => {
  reducedMotion(false);
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("plays one Y4 entrance per destination activation and never for refetch or polling", async () => {
  const f = fixture();
  const view = render(f.view(false));
  expect(entering()).toBeNull();
  view.rerender(f.view(true));
  await screen.findByRole("button", { name: /^First entrance schedule/ });
  expect(entering()).toBe("y4");
  expect(page().hasAttribute("style")).toBe(false);
  dispatchAnimation(page());
  expect(entering()).toBeNull();
  await act(async () => {
    await f.client.invalidateQueries();
  });
  expect(entering()).toBeNull();

  view.rerender(f.view(false));
  expect(entering()).toBeNull();
  view.rerender(f.view(true));
  expect(entering()).toBe("y4");
  dispatchAnimation(page());
  expect(entering()).toBeNull();
  expect(f.run).not.toHaveBeenCalled();
});

it("plays one entrance for New schedule and each schedule selection with a single active transform", async () => {
  const f = fixture();
  render(f.view());
  await screen.findByRole("button", { name: /^First entrance schedule/ });
  // The destination entrance is still running when New schedule is chosen.
  expect(entering()).toBe("y4");
  fireEvent.click(screen.getByRole("button", { name: "New schedule" }));
  expect(entering()).toBe("y4");
  expect(animated()).toBe(1);
  dispatchAnimation(page());
  expect(entering()).toBeNull();

  // Editing the draft and canceling never replay.
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(entering()).toBeNull();

  const reselect = await screen.findByRole("button", {
    name: /^First entrance schedule/,
  });
  fireEvent.click(reselect);
  expect(entering()).toBe("y4");
  expect(animated()).toBe(1);
  dispatchAnimation(page());
  // Reselecting the same schedule and a refetch do not replay.
  fireEvent.click(reselect);
  expect(entering()).toBeNull();
  await act(async () => {
    await f.client.invalidateQueries();
  });
  expect(entering()).toBeNull();

  fireEvent.click(
    await screen.findByRole("button", { name: /^Second entrance schedule/ }),
  );
  expect(entering()).toBe("y4");
  expect(animated()).toBe(1);
  dispatchAnimation(page());
  expect(entering()).toBeNull();
  expect(f.run).not.toHaveBeenCalled();
});

it("plays one entrance per explicit notification occurrence entry and not for polling or Back", async () => {
  const f = fixture();
  const view = render(f.view(true));
  await screen.findByRole("button", { name: /^First entrance schedule/ });
  dispatchAnimation(page());
  view.rerender(f.view(false));
  // Opening the original occurrence activates Schedules and enters it at once.
  view.rerender(f.view(true, notification("generation-1")));
  expect(entering()).toBe("y4");
  expect(animated()).toBe(1);
  dispatchAnimation(page());
  await screen.findByRole("button", { name: "Back to schedules" });
  // Polling and rerenders of the same generation never replay.
  view.rerender(f.view(true, notification("generation-1")));
  await act(async () => {
    await f.client.invalidateQueries();
  });
  expect(entering()).toBeNull();
  // Back to schedules clears the occurrence without an entrance.
  view.rerender(f.view(true));
  expect(entering()).toBeNull();
  // A later explicit open with a new generation plays again.
  view.rerender(f.view(true, notification("generation-2")));
  expect(entering()).toBe("y4");
  expect(animated()).toBe(1);
  dispatchAnimation(page());
  expect(entering()).toBeNull();
});

it("suppresses Schedules motion under Animation Off and reduced motion and never replays on re-enable", async () => {
  const setReduced = reducedMotion(false);
  const f = fixture();
  const view = render(f.view());
  await screen.findByRole("button", { name: /^First entrance schedule/ });
  expect(entering()).toBe("y4");
  await setAnimation("false");
  expect(entering()).toBeNull();
  await setAnimation(undefined);
  expect(entering()).toBeNull();

  fireEvent.click(screen.getByRole("button", { name: "New schedule" }));
  expect(entering()).toBe("y4");
  setReduced(true);
  expect(entering()).toBeNull();
  setReduced(false);
  expect(entering()).toBeNull();

  setReduced(true);
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  fireEvent.click(
    await screen.findByRole("button", { name: /^Second entrance schedule/ }),
  );
  view.rerender(f.view(false));
  view.rerender(f.view(true));
  expect(entering()).toBeNull();
});
