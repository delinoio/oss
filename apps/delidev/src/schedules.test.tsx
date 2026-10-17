import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { danger, expectTone, neutral } from "./test-action-tone";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  ScheduleService,
  WorkerService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { document, encode } from "./documents";
import { MutationIntents } from "./mutation";
import {
  ScheduleDetails,
  ScheduleEditor,
  Schedules,
  StartingReferences,
} from "./schedules";
import {
  fakeDestinationWindow,
  primaryClick,
} from "./destination-window-test-support";
import { Surface } from "./surface";
import { i18n } from "./localization";
import { pressEnter } from "./test-form-enter";
import { MachineSettings } from "./machine-settings";
import {
  RunnerRemediationProvider,
  useRunnerRemediation,
} from "./runner-remediation";

function fixture() {
  const definition = {
    name: "Weekday review",
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
  };
  const schedule = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SCHEDULE,
    revision: 3n,
    schemaVersion: 1,
    documentJson: encode({
      definition,
      configuration_revision: 2,
      created_by: newRequestId(),
      last_occurrence: 9,
      next_run_at: "2026-09-28T00:00:00Z",
    }),
  });
  const machine = create(ResourceSchema, {
    id: definition.machine_id,
    kind: EntityKind.MACHINE,
    revision: 5n,
    schemaVersion: 1,
    documentJson: encode({
      name: "Selected Worker",
      os: "darwin",
      architecture: "arm64",
      installations: [
        {
          harness: "codex",
          state: "detected",
          explicit_path: "/original/codex",
          version: "0.151.0",
        },
      ],
    }),
  });
  const occurrence = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.OCCURRENCE,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({
      schedule_id: schedule.id,
      state: "waiting",
      trigger: "manual",
      overlap: "wait",
      selection: { ...definition, prompt: "Original occurrence prompt" },
    }),
  });
  const job = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.JOB,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({ state: "queued", type: "harness-discovery" }),
  });
  const save = vi.fn(async (_request: unknown) => ({ schedule }));
  const run = vi.fn(async (_request: unknown) => ({ occurrence }));
  const control = vi.fn(async (_request: unknown) => ({ schedule }));
  const remove = vi.fn(async (_request: unknown) => ({}));
  const discovery = vi.fn(async (_request: unknown) => ({ machine, job }));
  const repositoryId = newRequestId();
  const project = create(ResourceSchema, {
    id: definition.project_id,
    kind: EntityKind.PROJECT,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      name: "Selected project",
      repositories: [repositoryId],
      base: { type: "local-branch", name: "main" },
    }),
  });
  const agent = create(ResourceSchema, {
    id: definition.agent_id,
    kind: EntityKind.AGENT,
    schemaVersion: 4,
    revision: 1n,
    documentJson: encode({
      name: "Selected agent",
      harness: "codex",
      routes: [
        {
          model: {
            provider_id: newRequestId(),
            native_id: "fixture-native-model",
            input_modalities: ["text"],
            metadata_source: "unknown",
          },
          accounts: [{ id: newRequestId(), weight: 1 }],
        },
      ],
      templates: [],
      options: { permission: "default" },
    }),
  });
  const repository = create(ResourceSchema, {
    id: repositoryId,
    kind: EntityKind.REPOSITORY,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({ name: "oss" }),
  });
  const resources = [machine, project, agent, repository];
  const list = vi.fn(
    async (request: { filter?: { kind: EntityKind; pageToken: string } }) => ({
      resources: resources.filter((row) => row.kind === request.filter?.kind),
      nextPageToken: "",
    }),
  );
  const get = vi.fn(async (request: { kind: EntityKind; id: string }) => ({
    resource:
      request.kind === EntityKind.JOB
        ? job
        : resources.find((row) => row.id === request.id),
  }));
  const listSchedules = vi.fn((_request: unknown) => ({
    schedules: [schedule],
  }));
  const getSchedule = vi.fn((_request: unknown) => ({ schedule }));
  const transport = createRouterTransport((router) => {
    router.service(ScheduleService, {
      listSchedules,
      getSchedule,
      saveSchedule: save,
      runScheduleNow: run,
      controlSchedule: control,
      deleteSchedule: remove,
      listScheduleOccurrences: () => ({ occurrences: [occurrence] }),
      getScheduleOccurrence: () => ({ occurrence }),
    });
    router.service(ResourceService, { listResources: list, getResource: get });
    router.service(WorkerService, { discoverHarnesses: discovery });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const view = (children: React.ReactNode) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>{children}</MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  return {
    schedule,
    machine,
    resources,
    definition,
    occurrence,
    save,
    run,
    control,
    remove,
    discovery,
    listSchedules,
    getSchedule,
    view,
    client,
    list,
    get,
    project,
    agent,
    repositoryId,
  };
}

it.each([false, true])(
  "preserves saved enabled=%s while editing and retrying its exact original revision",
  async (enabled) => {
    const value = fixture();
    value.definition.enabled = enabled;
    value.schedule.documentJson = encode({
      ...document(value.schedule),
      definition: value.definition,
    });
    value.save.mockRejectedValueOnce(
      new ConnectError("ack lost", Code.Unavailable),
    );
    render(
      value.view(
        <ScheduleEditor
          initial={value.schedule}
          active
          saved={() => {}}
          cancel={() => {}}
        />,
      ),
    );
    fireEvent.change(screen.getByLabelText("Scheduled prompt"), {
      target: { value: "Updated prompt" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save schedule" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Retry the same schedule" }),
    );
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
    expect(value.save.mock.calls[0][0]).toEqual(value.save.mock.calls[1][0]);
    const request = value.save.mock.calls[0][0] as {
      definitionJson: Uint8Array;
      mutation: { expectedRevision: bigint };
      localWorkerToken: string;
    };
    expect(
      JSON.parse(new TextDecoder().decode(request.definitionJson)),
    ).toEqual({ ...value.definition, prompt: "Updated prompt" });
    expect(request.mutation.expectedRevision).toBe(3n);
    expect(request.localWorkerToken).toBe("");
  },
);

it("retains a new schedule draft when navigating away and back", () => {
  const value = fixture(),
    rendered = render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "New schedule" }));
  fireEvent.change(screen.getByLabelText("Scheduled prompt"), {
    target: { value: "Keep this schedule draft" },
  });
  rendered.rerender(value.view(<Schedules active={false} open={() => {}} />));
  rendered.rerender(value.view(<Schedules active open={() => {}} />));
  expect(
    (screen.getByLabelText("Scheduled prompt") as HTMLTextAreaElement).value,
  ).toBe("Keep this schedule draft");
  expect(value.save).not.toHaveBeenCalled();
});

function notificationTarget(value: ReturnType<typeof fixture>) {
  return {
    id: value.occurrence.id,
    scheduleId: value.schedule.id,
    generation: "notification-1",
  };
}

it("keeps the editing draft hidden and inert during notification review and restores it on return", async () => {
  const value = fixture(),
    clear = vi.fn();
  const rendered = render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "New schedule" }));
  const prompt = screen.getByLabelText(
    "Scheduled prompt",
  ) as HTMLTextAreaElement;
  fireEvent.change(prompt, { target: { value: "Draft kept during review" } });
  rendered.rerender(
    value.view(
      <Schedules
        active
        open={() => {}}
        notificationOccurrence={notificationTarget(value)}
        clearNotificationOccurrence={clear}
      />,
    ),
  );
  await screen.findByText("Original occurrence prompt");
  const workspace = prompt.closest(".schedule-workspace") as HTMLElement;
  expect(workspace.hidden).toBe(true);
  expect(workspace.hasAttribute("inert")).toBe(true);
  expect(
    screen.queryByRole("textbox", { name: "Scheduled prompt" }),
  ).toBeNull();
  expect(screen.queryByRole("button", { name: "Save schedule" })).toBeNull();
  expect(
    screen.getByRole("button", { name: "Back to schedules" }),
  ).toBeTruthy();
  rendered.rerender(value.view(<Schedules active open={() => {}} />));
  const restored = screen.getByLabelText(
    "Scheduled prompt",
  ) as HTMLTextAreaElement;
  expect(restored).toBe(prompt);
  expect(restored.value).toBe("Draft kept during review");
  expect(workspace.hidden).toBe(false);
  expect(workspace.hasAttribute("inert")).toBe(false);
  expect(value.save).not.toHaveBeenCalled();
});

it("restores focus to the retained workspace control after notification review", async () => {
  const value = fixture(),
    clear = vi.fn();
  const rendered = render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "New schedule" }));
  const prompt = screen.getByLabelText(
    "Scheduled prompt",
  ) as HTMLTextAreaElement;
  prompt.focus();
  expect(globalThis.document.activeElement).toBe(prompt);
  rendered.rerender(
    value.view(
      <Schedules
        active
        open={() => {}}
        notificationOccurrence={notificationTarget(value)}
        clearNotificationOccurrence={clear}
      />,
    ),
  );
  await screen.findByText("Original occurrence prompt");
  // Browsers drop focus from a hidden inert subtree; the keyboard user then
  // activates Back, which unmounts together with the review.
  prompt.blur();
  const back = screen.getByRole("button", { name: "Back to schedules" });
  back.focus();
  expect(globalThis.document.activeElement).toBe(back);
  rendered.rerender(value.view(<Schedules active open={() => {}} />));
  expect(globalThis.document.activeElement).toBe(prompt);
});

it("defers an accepted schedule save and list reads until notification review returns", async () => {
  const value = fixture();
  let release: () => void = () => {};
  value.save.mockImplementationOnce(async () => {
    await new Promise<void>((resolve) => {
      release = resolve;
    });
    return { schedule: value.schedule };
  });
  const rendered = render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(
    await screen.findByRole("button", { name: /Weekday review/ }),
  );
  fireEvent.click(await screen.findByRole("button", { name: "Edit schedule" }));
  const prompt = screen.getByLabelText(
    "Scheduled prompt",
  ) as HTMLTextAreaElement;
  fireEvent.change(prompt, { target: { value: "Saved during review" } });
  fireEvent.click(screen.getByRole("button", { name: "Save schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  rendered.rerender(
    value.view(
      <Schedules
        active
        open={() => {}}
        notificationOccurrence={notificationTarget(value)}
        clearNotificationOccurrence={() => {}}
      />,
    ),
  );
  await screen.findAllByText("Original occurrence prompt");
  const reads = value.listSchedules.mock.calls.length;
  await act(async () => {
    release();
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
  // The accepted response keeps the hidden editor and issues no list read.
  expect(prompt.isConnected).toBe(true);
  expect((prompt.closest(".schedule-workspace") as HTMLElement).hidden).toBe(
    true,
  );
  expect(value.listSchedules.mock.calls.length).toBe(reads);
  rendered.rerender(value.view(<Schedules active open={() => {}} />));
  await waitFor(() =>
    expect(screen.queryByLabelText("Scheduled prompt")).toBeNull(),
  );
  expect(await screen.findByRole("button", { name: "Run now" })).toBeTruthy();
  await waitFor(() =>
    expect(value.listSchedules.mock.calls.length).toBeGreaterThan(reads),
  );
  expect(value.save).toHaveBeenCalledTimes(1);
});

it("moves focus to the resulting details when a deferred save unmounts the focused editor", async () => {
  const value = fixture();
  let release: () => void = () => {};
  value.save.mockImplementationOnce(async () => {
    await new Promise<void>((resolve) => {
      release = resolve;
    });
    return { schedule: value.schedule };
  });
  const rendered = render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(
    await screen.findByRole("button", { name: /Weekday review/ }),
  );
  fireEvent.click(await screen.findByRole("button", { name: "Edit schedule" }));
  const prompt = screen.getByLabelText(
    "Scheduled prompt",
  ) as HTMLTextAreaElement;
  fireEvent.change(prompt, { target: { value: "Saved during review" } });
  fireEvent.click(screen.getByRole("button", { name: "Save schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  prompt.focus();
  rendered.rerender(
    value.view(
      <Schedules
        active
        open={() => {}}
        notificationOccurrence={notificationTarget(value)}
        clearNotificationOccurrence={() => {}}
      />,
    ),
  );
  await screen.findAllByText("Original occurrence prompt");
  await act(async () => {
    release();
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
  // Browsers drop focus from the hidden inert editor; Back then takes it.
  prompt.blur();
  screen.getByRole("button", { name: "Back to schedules" }).focus();
  rendered.rerender(value.view(<Schedules active open={() => {}} />));
  const runNow = await screen.findByRole("button", { name: "Run now" });
  const workspace = runNow.closest(".schedule-workspace") as HTMLElement;
  expect(prompt.isConnected).toBe(false);
  await waitFor(() => {
    const focused = globalThis.document.activeElement;
    expect(focused).not.toBe(globalThis.document.body);
    expect(workspace.contains(focused)).toBe(true);
  });
});

it("defers the schedule detail refetch after an accepted control until the view is active", async () => {
  const value = fixture();
  let release: () => void = () => {};
  value.control.mockImplementationOnce(async () => {
    await new Promise<void>((resolve) => {
      release = resolve;
    });
    return { schedule: value.schedule };
  });
  const view = (active: boolean) =>
    value.view(
      <ScheduleDetails
        initial={value.schedule}
        active={active}
        open={() => {}}
        close={() => {}}
        edit={() => {}}
      />,
    );
  const rendered = render(view(true));
  await waitFor(() => expect(value.getSchedule).toHaveBeenCalled());
  fireEvent.click(screen.getByRole("button", { name: "Resume future runs" }));
  await waitFor(() => expect(value.control).toHaveBeenCalledTimes(1));
  rendered.rerender(view(false));
  const reads = value.getSchedule.mock.calls.length;
  await act(async () => {
    release();
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
  expect(value.getSchedule.mock.calls.length).toBe(reads);
  rendered.rerender(view(true));
  await waitFor(() =>
    expect(value.getSchedule.mock.calls.length).toBeGreaterThan(reads),
  );
});

it("defers accepted detail state updates with the refetch until the view is active", async () => {
  const value = fixture();
  let release: () => void = () => {};
  value.remove.mockImplementationOnce(async () => {
    await new Promise<void>((resolve) => {
      release = resolve;
    });
    return {};
  });
  const view = (active: boolean) =>
    value.view(
      <ScheduleDetails
        initial={value.schedule}
        active={active}
        open={() => {}}
        close={() => {}}
        edit={() => {}}
      />,
    );
  const rendered = render(view(true));
  await waitFor(() => expect(value.getSchedule).toHaveBeenCalled());
  fireEvent.click(screen.getByRole("button", { name: "Delete schedule" }));
  const confirm = screen.getByRole("button", {
    name: "Confirm schedule deletion",
  });
  fireEvent.click(confirm);
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(1));
  rendered.rerender(view(false));
  const reads = value.getSchedule.mock.calls.length;
  await act(async () => {
    release();
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
  // The accepted delete keeps the retained controls mounted while inactive.
  expect(confirm.isConnected).toBe(true);
  expect(screen.queryByText(/Schedule configuration deleted/)).toBeNull();
  expect(value.getSchedule.mock.calls.length).toBe(reads);
  rendered.rerender(view(true));
  await waitFor(() => expect(confirm.isConnected).toBe(false));
  expect(screen.getByText(/Schedule configuration deleted/)).toBeTruthy();
});

it("keeps loaded occurrence history mounted during notification review", async () => {
  const value = fixture();
  const rendered = render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(
    await screen.findByRole("button", { name: /Weekday review/ }),
  );
  const heading = await screen.findByRole("button", { name: "Run now" });
  const workspace = heading.closest(".schedule-workspace") as HTMLElement;
  const history = await waitFor(() => {
    const row = workspace.querySelector("article.result");
    expect(row).not.toBeNull();
    return row as HTMLElement;
  });
  rendered.rerender(
    value.view(
      <Schedules
        active
        open={() => {}}
        notificationOccurrence={notificationTarget(value)}
        clearNotificationOccurrence={() => {}}
      />,
    ),
  );
  await screen.findAllByText("Original occurrence prompt");
  expect(history.isConnected).toBe(true);
  expect(workspace.contains(history)).toBe(true);
  rendered.rerender(value.view(<Schedules active open={() => {}} />));
  expect(history.isConnected).toBe(true);
  expect(workspace.querySelector("article.result")).toBe(history);
});

it("hides selected schedule details without writes during notification review and restores the same instance", async () => {
  const value = fixture();
  const rendered = render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(
    await screen.findByRole("button", { name: /Weekday review/ }),
  );
  const heading = await screen.findByRole("button", { name: "Run now" });
  const workspace = heading.closest(".schedule-workspace") as HTMLElement;
  rendered.rerender(
    value.view(
      <Schedules
        active
        open={() => {}}
        notificationOccurrence={notificationTarget(value)}
        clearNotificationOccurrence={() => {}}
      />,
    ),
  );
  await screen.findAllByText("Original occurrence prompt");
  expect(workspace.hidden).toBe(true);
  expect(workspace.hasAttribute("inert")).toBe(true);
  expect(screen.queryByRole("button", { name: "Run now" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Delete schedule" })).toBeNull();
  rendered.rerender(value.view(<Schedules active open={() => {}} />));
  expect(screen.getByRole("button", { name: "Run now" })).toBe(heading);
  expect(value.run).not.toHaveBeenCalled();
  expect(value.control).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
});

it.each(["selected details", "empty workspace"])(
  "locks the schedule sidebar and its list reads during notification review from %s",
  async (origin) => {
    const value = fixture(),
      clear = vi.fn();
    const rendered = render(value.view(<Schedules active open={() => {}} />));
    const row = await screen.findByRole("button", { name: /Weekday review/ });
    if (origin === "selected details") {
      fireEvent.click(row);
      await screen.findByRole("button", { name: "Run now" });
    }
    await waitFor(() => expect(value.listSchedules).toHaveBeenCalled());
    rendered.rerender(
      value.view(
        <Schedules
          active
          open={() => {}}
          notificationOccurrence={notificationTarget(value)}
          clearNotificationOccurrence={clear}
        />,
      ),
    );
    await screen.findAllByText("Original occurrence prompt");
    const reads = value.listSchedules.mock.calls.length;
    for (const name of [
      "New schedule",
      "Refresh",
      "All schedules",
      "Enabled",
      "Paused",
    ]) {
      expect(
        (screen.getByRole("button", { name }) as HTMLButtonElement).disabled,
      ).toBe(true);
    }
    fireEvent.click(screen.getByRole("button", { name: "New schedule" }));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(value.listSchedules.mock.calls.length).toBe(reads);
    rendered.rerender(value.view(<Schedules active open={() => {}} />));
    expect(
      (
        screen.getByRole("button", {
          name: "New schedule",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  },
);

it("hides the empty workspace and keeps the unavailable status for a missing notification occurrence", async () => {
  const value = fixture();
  render(
    value.view(
      <Schedules
        active
        open={() => {}}
        notificationOccurrence={{
          ...notificationTarget(value),
          id: newRequestId(),
        }}
        clearNotificationOccurrence={() => {}}
      />,
    ),
  );
  expect((await screen.findByRole("status")).textContent).toBeTruthy();
  expect(screen.queryByText("Select a schedule from the sidebar.")).toBeNull();
  expect(screen.queryByText("Original occurrence prompt")).toBeNull();
});

it("binds Run now retries to one occurrence without resuming the paused schedule", async () => {
  const value = fixture();
  value.run.mockRejectedValueOnce(
    new ConnectError("lost acknowledgment", Code.Unavailable),
  );
  render(
    value.view(
      <ScheduleDetails
        initial={value.schedule}
        active
        open={() => {}}
        close={() => {}}
        edit={() => {}}
      />,
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "Run now" }));
  expect(value.run).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm Run now" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Retry the same Run now" }),
  );
  await screen.findByText("Run now accepted");
  expect(value.run.mock.calls[0][0]).toEqual(value.run.mock.calls[1][0]);
  expect(value.control).not.toHaveBeenCalled();
  expect(
    screen.getAllByText("No session has been created for this occurrence.")
      .length,
  ).toBeGreaterThan(0);
});

it("keeps original occurrence history after configuration deletion without deleting sessions", async () => {
  const value = fixture();
  render(
    value.view(
      <ScheduleDetails
        initial={value.schedule}
        active
        open={() => {}}
        close={() => {}}
        edit={() => {}}
      />,
    ),
  );
  await screen.findByText("Original occurrence prompt");
  fireEvent.click(screen.getByRole("button", { name: "Delete schedule" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Confirm schedule deletion" }),
  );
  await screen.findByText(
    "Schedule configuration deleted. Retained occurrences and sessions remain.",
  );
  expect(screen.getByText("Original occurrence prompt")).toBeTruthy();
  expect(value.run).not.toHaveBeenCalled();
  expect(value.control).not.toHaveBeenCalled();
});

it("labels occurrence history rows as scheduled runs with kind-scoped numbers", async () => {
  const value = fixture();
  const rendered = render(
    value.view(
      <ScheduleDetails
        initial={value.schedule}
        active
        open={() => {}}
        close={() => {}}
        edit={() => {}}
      />,
    ),
  );
  await screen.findByText("Original occurrence prompt");
  const labels = () => {
    const rows = [...globalThis.document.querySelectorAll("small")].map(
      (node) => node.textContent ?? "",
    );
    return {
      occurrence: rows.filter((text) => text.startsWith("Scheduled run")),
      schedule: rows.filter((text) => text.startsWith("Schedule unavailable")),
    };
  };
  // The occurrence row never borrows the schedule noun, and the real schedule
  // identity keeps its own kind with an independent number.
  expect(labels()).toEqual({
    occurrence: ["Scheduled run · 1"],
    schedule: ["Schedule unavailable · 1"],
  });
  rendered.rerender(
    value.view(
      <ScheduleDetails
        initial={value.schedule}
        active
        open={() => {}}
        close={() => {}}
        edit={() => {}}
      />,
    ),
  );
  await screen.findByText("Original occurrence prompt");
  expect(labels()).toEqual({
    occurrence: ["Scheduled run · 1"],
    schedule: ["Schedule unavailable · 1"],
  });
  await act(() => i18n.changeLanguage("ko"));
  try {
    const rows = [...globalThis.document.querySelectorAll("small")].map(
      (node) => node.textContent ?? "",
    );
    expect(rows).toContain("예약 실행 · 1");
    expect(rows.filter((text) => text.startsWith("예약 실행"))).toHaveLength(1);
  } finally {
    await act(() => i18n.changeLanguage("en"));
  }
});

it("retains the original authenticated Local machine without inventing another origin", async () => {
  const value = fixture();
  value.schedule.documentJson = encode({
    ...document(value.schedule),
    definition: { ...value.definition, workspace: "local" },
    local_origin: { machine_id: value.machine.id, device_id: newRequestId() },
  });
  render(
    value.view(
      <ScheduleEditor
        initial={value.schedule}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  expect(
    (screen.getByLabelText("Runner Device") as HTMLSelectElement).disabled,
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Save schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = value.save.mock.calls[0][0] as {
    definitionJson: Uint8Array;
    localWorkerToken: string;
  };
  expect(JSON.parse(new TextDecoder().decode(request.definitionJson))).toEqual({
    ...value.definition,
    workspace: "local",
  });
  expect(request.localWorkerToken).toBe("");
});

it("retains exact executable selections and protocol intent after uncertain Worker acceptance", async () => {
  const value = fixture();
  value.discovery.mockRejectedValueOnce(
    new ConnectError("ack lost", Code.Unavailable),
  );
  render(
    value.view(
      <MachineSettings initial={value.machine} active close={() => {}} />,
    ),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Edit executable paths" }),
  );
  fireEvent.change(screen.getByLabelText("codex executable path"), {
    target: { value: "/selected/codex" },
  });
  fireEvent.click(
    screen.getByRole("checkbox", {
      name: "Verify the installed native protocol without login or inference",
    }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Run optional diagnostics" }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Retry the same harness check" }),
  );
  await screen.findByText(
    "Accepted by the server. Waiting for the selected Worker to finish.",
  );
  expect(value.discovery.mock.calls[0][0]).toEqual(
    value.discovery.mock.calls[1][0],
  );
  const request = value.discovery.mock.calls[0][0] as {
    selectionsJson: Uint8Array;
    verifyProtocol: boolean;
    mutation: { expectedRevision: bigint };
  };
  expect(request.verifyProtocol).toBe(true);
  expect(request.mutation.expectedRevision).toBe(5n);
  expect(JSON.parse(new TextDecoder().decode(request.selectionsJson))).toEqual({
    executables: [
      { harness: "codex", path: "/selected/codex" },
      { harness: "claude-code", path: "" },
      { harness: "opencode", path: "" },
      { harness: "grok-build", path: "" },
    ],
  });
  expect(screen.getByText("Native protocol: Not checked")).toBeTruthy();
});

it("blocks stale executable path edits while retaining the staged path", async () => {
  const value = fixture();
  render(
    value.view(
      <MachineSettings initial={value.machine} active close={() => {}} />,
    ),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Edit executable paths" }),
  );
  fireEvent.change(screen.getByLabelText("codex executable path"), {
    target: { value: "/staged/codex" },
  });
  value.resources[0] = create(ResourceSchema, {
    ...value.machine,
    revision: 6n,
  });
  await screen.findByText(
    /Worker configuration changed elsewhere/,
    {},
    { timeout: 7000 },
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Run optional diagnostics",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(
    (screen.getByLabelText("codex executable path") as HTMLInputElement).value,
  ).toBe("/staged/codex");
  expect(value.discovery).not.toHaveBeenCalled();
});

async function fillCreation(value: ReturnType<typeof fixture>) {
  fireEvent.change(screen.getByLabelText("Schedule name"), {
    target: { value: "Morning review" },
  });
  fireEvent.change(screen.getByLabelText("Scheduled prompt"), {
    target: { value: "Review project changes" },
  });
  for (const [label, resource] of [
    ["Project", value.project],
    ["Agent Worker", value.agent],
    ["Runner Device", value.machine],
  ] as const) {
    if (label === "Agent Worker") goStep(1);
    const select = screen.getByRole("combobox", { name: label });
    fireEvent.click(select);
    fireEvent.click(
      await screen.findByRole("option", {
        name: JSON.parse(new TextDecoder().decode(resource.documentJson)).name,
      }),
    );
    await waitFor(() =>
      expect(select.getAttribute("data-value")).toBe(resource.id),
    );
  }
}
function goStep(target: number) {
  const current = () =>
    Number(
      globalThis.document
        .querySelector("[data-authoring-step]:not([hidden])")
        ?.getAttribute("data-authoring-step") ?? 3,
    );
  for (let count = 0; current() !== target && count < 4; count++)
    fireEvent.click(
      screen.getByRole("button", {
        name: current() < target ? "Next" : "Back",
      }),
    );
}
const choose = (label: string, value: string) => {
  const control = screen.getByLabelText(label),
    step = control.closest("[data-authoring-step]");
  if (step) goStep(Number(step.getAttribute("data-authoring-step")));
  fireEvent.change(control, { target: { value } });
};
function createRequest(value: ReturnType<typeof fixture>) {
  return value.save.mock.calls[0][0] as {
    definitionJson: Uint8Array;
    mutation: { id: string; requestId: string; expectedRevision: bigint };
    schemaVersion: number;
    localWorkerToken: string;
  };
}

it("creates an enabled strict definition with explicit resources and the original defaults", async () => {
  const value = fixture(),
    saved = vi.fn();
  render(value.view(<ScheduleEditor active saved={saved} cancel={() => {}} />));
  expect(screen.getByLabelText("Schedule name")).toBe(
    globalThis.document.activeElement,
  );
  await fillCreation(value);
  expect(
    screen
      .getByRole("button", { name: /Starting reference overrides/ })
      .getAttribute("aria-expanded"),
  ).toBe("false");
  expect(screen.getByLabelText("Project").getAttribute("aria-required")).toBe(
    "true",
  );
  goStep(2);
  expect(
    (
      screen.getByRole("checkbox", {
        name: "Enable future scheduled runs",
      }) as HTMLInputElement
    ).checked,
  ).toBe(true);
  goStep(3);
  expect(
    globalThis.document.querySelector(".schedule-creation-review")!.textContent,
  ).toContain("Enabled on creation");
  expect(
    globalThis.document.querySelector(".schedule-creation-footer")!.textContent,
  ).toContain("Enabled on creation");
  fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
  await waitFor(() => expect(saved).toHaveBeenCalledOnce());
  const request = createRequest(value);
  expect(JSON.parse(new TextDecoder().decode(request.definitionJson))).toEqual({
    name: "Morning review",
    prompt: "Review project changes",
    enabled: true,
    project_id: value.project.id,
    agent_id: value.agent.id,
    machine_id: value.machine.id,
    workspace: "worktree",
    mode: "execute",
    cron: "0 9 * * 1-5",
    timezone: "UTC",
    overlap: "overlap",
    starting: [],
  });
  expect(request.mutation).toMatchObject({ id: "", expectedRevision: 0n });
  expect(request.schemaVersion).toBe(1);
  expect(request.mutation.requestId).toMatch(/^[0-9a-f-]{36}$/);
  expect(request.localWorkerToken).toBe("");
});

it.each([
  ["daily", "07:05", undefined, "5 7 * * *"],
  ["weekdays", "18:30", undefined, "30 18 * * 1-5"],
  ["weekly", "23:59", "0", "59 23 * * 0"],
])(
  "serializes %s as only the generated cron",
  async (frequency, time, weekday, cron) => {
    const value = fixture();
    render(
      value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
    );
    await fillCreation(value);
    choose("Frequency", frequency!);
    choose("Time", time!);
    if (weekday) choose("Weekday", weekday);
    choose("IANA timezone", "Asia/Seoul");
    goStep(3);
    fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
    await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
    const definition = JSON.parse(
      new TextDecoder().decode(createRequest(value).definitionJson),
    );
    expect(definition).toMatchObject({
      cron,
      timezone: "Asia/Seoul",
      enabled: true,
      mode: "execute",
      overlap: "overlap",
    });
    expect(Object.keys(definition).sort()).toEqual(
      [
        "name",
        "prompt",
        "enabled",
        "project_id",
        "agent_id",
        "machine_id",
        "workspace",
        "mode",
        "cron",
        "timezone",
        "overlap",
        "starting",
      ].sort(),
    );
  },
);

it("retains preset time and Weekly weekday while importing only canonical custom time", async () => {
  const value = fixture();
  render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  await fillCreation(value);
  choose("Frequency", "weekly");
  expect((screen.getByLabelText("Weekday") as HTMLSelectElement).value).toBe(
    "1",
  );
  choose("Weekday", "3");
  choose("Time", "07:05");
  choose("Frequency", "daily");
  choose("Frequency", "weekdays");
  expect((screen.getByLabelText("Time") as HTMLInputElement).value).toBe(
    "07:05",
  );
  choose("Frequency", "custom");
  expect(
    (screen.getByLabelText("Cron expression") as HTMLInputElement).value,
  ).toBe("5 7 * * 1-5");
  choose("Cron expression", "5 7 * * 2");
  choose("Frequency", "weekly");
  expect((screen.getByLabelText("Weekday") as HTMLSelectElement).value).toBe(
    "3",
  );
  expect(screen.getByText("5 7 * * 3")).toBeTruthy();
  choose("Time", "18:30");
  choose("Frequency", "custom");
  choose("Cron expression", "*/15 * * * *");
  expect(
    (screen.getByLabelText("Cron expression") as HTMLInputElement).value,
  ).toBe("*/15 * * * *");
  choose("Frequency", "daily");
  expect(screen.getByText("30 18 * * *")).toBeTruthy();
  choose("Frequency", "custom");
  choose("Cron expression", "05 07 * * 2");
  choose("Frequency", "weekly");
  expect(screen.getByText("30 18 * * 3")).toBeTruthy();
});

it("uses 09:00 when no valid preset time has been retained, without parsing arbitrary cron", async () => {
  const value = fixture();
  render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  await fillCreation(value);
  choose("Time", "");
  choose("Frequency", "custom");
  expect(
    (screen.getByLabelText("Cron expression") as HTMLInputElement).value,
  ).toBe("0 9 * * 1-5");
  choose("Cron expression", " 5 7 * * 2 ");
  choose("Frequency", "daily");
  expect(screen.getByText("0 9 * * *")).toBeTruthy();
});

it("keeps empty Time visible and blocks even direct form submission instead of sending stale cron", async () => {
  const value = fixture();
  render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  await fillCreation(value);
  choose("Time", "");
  expect((screen.getByLabelText("Time") as HTMLInputElement).value).toBe("");
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  expect(screen.queryByRole("button", { name: "Create schedule" })).toBeNull();
  fireEvent.submit(
    screen.getByRole("button", { name: "Next" }).closest("form")!,
  );
  expect(value.save).not.toHaveBeenCalled();
  expect(screen.queryByText("0 9 * * 1-5")).toBeNull();
});

it.each([
  ["0 0 31 2 *", "UTC"],
  ["0 9 * * 1-5", "unknown/zone"],
])(
  "retains server-rejected calendar %s and timezone %s for correction",
  async (cron, timezone) => {
    const value = fixture();
    value.save.mockRejectedValueOnce(
      new ConnectError("Calendar rejected", Code.InvalidArgument),
    );
    render(
      value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
    );
    await fillCreation(value);
    choose("Frequency", "custom");
    choose("Cron expression", cron);
    choose("IANA timezone", timezone);
    goStep(3);
    fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
    await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "Create schedule",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    expect(
      (screen.getByLabelText("Cron expression") as HTMLInputElement).value,
    ).toBe(cron);
    expect(
      (screen.getByLabelText("IANA timezone") as HTMLInputElement).value,
    ).toBe(timezone);
    expect(
      screen.getAllByText(
        "Saving confirms the actual next run. Scheduled times do not guarantee execution.",
      )[0],
    ).toBeTruthy();
    expect(screen.getAllByRole("alert").length).toBeGreaterThan(0);
  },
);

it("keeps complete reference drafts mounted behind the disclosure and clears them on project change", async () => {
  const value = fixture();
  render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  await fillCreation(value);
  const disclosure = screen.getByRole("button", {
    name: /Starting reference overrides/,
  });
  fireEvent.click(disclosure);
  await within(screen.getByLabelText("Add repository override")).findByRole(
    "option",
    { name: "oss" },
  );
  choose("Add repository override", value.repositoryId);
  fireEvent.click(
    screen.getByRole("button", { name: "Add starting override" }),
  );
  const reference = screen.getByLabelText("Starting oss name");
  fireEvent.change(reference, { target: { value: "retained-branch" } });
  expect(disclosure.textContent).toContain("1 override");
  fireEvent.click(disclosure);
  fireEvent.click(disclosure);
  expect(screen.getByLabelText("Starting oss name")).toBe(reference);
  expect((reference as HTMLInputElement).value).toBe("retained-branch");
  fireEvent.click(
    screen.getByRole("button", { name: "Remove starting override" }),
  );
  expect(disclosure.textContent).toContain("Using saved project references");
  choose("Add repository override", value.repositoryId);
  fireEvent.click(
    screen.getByRole("button", { name: "Add starting override" }),
  );
  goStep(0);
  fireEvent.click(screen.getByRole("combobox", { name: "Project" }));
  fireEvent.click(screen.getByRole("option", { name: "Select project" }));
  await waitFor(() =>
    expect(disclosure.textContent).toContain("Using saved project references"),
  );
  expect(screen.queryByLabelText("Starting oss name")).toBeNull();
  expect(document(value.project)).toMatchObject({ base: { name: "main" } });
});

it.each([true, false])(
  "locks enabled=%s creation during fresh Local proof and retains identical bytes/token on uncertain retry",
  async (enabled) => {
    const value = fixture();
    let resolveProof!: (proof: { machineId: string; token: string }) => void;
    const read = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveProof = resolve;
          }),
      )
      .mockResolvedValue({
        machineId: value.machine.id,
        token: "a".repeat(42) + "A",
      });
    value.save.mockRejectedValueOnce(
      new ConnectError("ack lost", Code.Unavailable),
    );
    const rendered = render(
      value.view(
        <ScheduleEditor
          active
          saved={() => {}}
          cancel={() => {}}
          readLocalWorker={read}
        />,
      ),
    );
    await fillCreation(value);
    if (!enabled) {
      goStep(2);
      fireEvent.click(
        screen.getByRole("checkbox", { name: "Enable future scheduled runs" }),
      );
      goStep(1);
    }
    fireEvent.click(screen.getByRole("radio", { name: "Local computer" }));
    expect(
      (screen.getByRole("button", { name: "Cancel" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(
      screen.getByLabelText("Scheduled prompt").closest("fieldset")!.disabled,
    ).toBe(true);
    resolveProof({ machineId: value.machine.id, token: "a".repeat(42) + "A" });
    await waitFor(() =>
      expect(
        (
          screen.getByRole("radio", {
            name: "Local computer",
          }) as HTMLInputElement
        ).checked,
      ).toBe(true),
    );
    expect(
      (screen.getByLabelText("Runner Device") as HTMLSelectElement).disabled,
    ).toBe(true);
    expect(
      (screen.getByLabelText("Agent Worker") as HTMLSelectElement).disabled,
    ).toBe(false);
    expect(
      screen.queryByRole("button", { name: /Starting reference overrides/ }),
    ).toBeNull();
    goStep(3);
    fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
    await screen.findByRole("button", { name: "Retry the same schedule" });
    rendered.rerender(
      value.view(
        <ScheduleEditor
          active={false}
          saved={() => {}}
          cancel={() => {}}
          readLocalWorker={read}
        />,
      ),
    );
    rendered.rerender(
      value.view(
        <ScheduleEditor
          active
          saved={() => {}}
          cancel={() => {}}
          readLocalWorker={read}
        />,
      ),
    );
    fireEvent.submit(
      screen.getByRole("button", { name: "Create schedule" }).closest("form")!,
    );
    expect(value.save).toHaveBeenCalledOnce();
    fireEvent.click(
      screen.getByRole("button", { name: "Retry the same schedule" }),
    );
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
    expect(value.save.mock.calls[0][0]).toEqual(value.save.mock.calls[1][0]);
    expect(read).toHaveBeenCalledTimes(2);
    expect(createRequest(value).localWorkerToken).toBe("a".repeat(42) + "A");
    expect(
      JSON.parse(new TextDecoder().decode(createRequest(value).definitionJson))
        .enabled,
    ).toBe(enabled);
  },
);

it.each(["changed", "malformed", "rejected"])(
  "refuses %s Local submission proof without machine fallback",
  async (failure) => {
    const value = fixture();
    const proof = { machineId: value.machine.id, token: "a".repeat(42) + "A" };
    const read = vi.fn().mockResolvedValueOnce(proof);
    if (failure === "rejected")
      read.mockRejectedValueOnce(new Error("private failure"));
    else
      read.mockResolvedValueOnce(
        failure === "changed"
          ? { ...proof, machineId: newRequestId() }
          : { ...proof, token: "bad" },
      );
    render(
      value.view(
        <ScheduleEditor
          active
          saved={() => {}}
          cancel={() => {}}
          readLocalWorker={read}
        />,
      ),
    );
    await fillCreation(value);
    fireEvent.click(screen.getByRole("radio", { name: "Local computer" }));
    await waitFor(() =>
      expect(
        (
          screen.getByRole("radio", {
            name: "Local computer",
          }) as HTMLInputElement
        ).checked,
      ).toBe(true),
    );
    goStep(3);
    fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
    await screen.findByText(/paired Worker could not be verified/);
    expect(value.save).not.toHaveBeenCalled();
    expect(
      screen.getByLabelText("Runner Device").getAttribute("data-value"),
    ).toBe(value.machine.id);
  },
);

it("retains creation UI state across inactivity without focus theft and Cancel returns to guidance", async () => {
  const value = fixture(),
    rendered = render(value.view(<Schedules active={false} open={() => {}} />));
  rendered.rerender(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "New schedule" }));
  expect(screen.getByLabelText("Schedule name")).toBe(
    globalThis.document.activeElement,
  );
  await fillCreation(value);
  choose("Frequency", "weekly");
  choose("Weekday", "3");
  choose("Time", "18:30");
  choose("Frequency", "custom");
  choose("Cron expression", "*/15 * * * *");
  goStep(1);
  fireEvent.click(
    screen.getByRole("button", { name: /Starting reference overrides/ }),
  );
  goStep(0);
  screen.getByLabelText("Scheduled prompt").focus();
  rendered.rerender(value.view(<Schedules active={false} open={() => {}} />));
  rendered.rerender(value.view(<Schedules active open={() => {}} />));
  await value.client.invalidateQueries();
  expect(globalThis.document.activeElement).toBe(
    screen.getByLabelText("Scheduled prompt"),
  );
  expect(
    (screen.getByLabelText("Cron expression") as HTMLInputElement).value,
  ).toBe("*/15 * * * *");
  goStep(1);
  expect(
    screen
      .getByRole("button", { name: /Starting reference overrides/ })
      .getAttribute("aria-expanded"),
  ).toBe("true");
  choose("Frequency", "weekly");
  expect((screen.getByLabelText("Weekday") as HTMLSelectElement).value).toBe(
    "3",
  );
  expect((screen.getByLabelText("Time") as HTMLInputElement).value).toBe(
    "18:30",
  );
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(screen.getByText(/Select a schedule from the sidebar/)).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
});

function newScheduleOpener() {
  return screen.getByRole("button", {
    name: /^(New schedule|새 예약 작업)$/,
  }) as HTMLButtonElement;
}
const pressEscape = (target: Element, init: KeyboardEventInit = {}) =>
  fireEvent.keyDown(target, { key: "Escape", ...init });

it("closes the idle Step 1 wizard with Escape, re-enables and focuses New schedule and issues no save", () => {
  const value = fixture();
  render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(newScheduleOpener());
  expect(newScheduleOpener().disabled).toBe(true);
  const name = screen.getByLabelText("Schedule name");
  expect(name).toBe(globalThis.document.activeElement);
  pressEscape(name);
  expect(screen.queryByLabelText("Schedule name")).toBeNull();
  expect(screen.getByText(/Select a schedule from the sidebar/)).toBeTruthy();
  expect(newScheduleOpener().disabled).toBe(false);
  expect(globalThis.document.activeElement).toBe(newScheduleOpener());
  expect(value.save).not.toHaveBeenCalled();
});

it("restores New schedule focus after the Cancel button as well", () => {
  const value = fixture();
  render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(newScheduleOpener());
  const cancel = screen.getByRole("button", { name: "Cancel" });
  cancel.focus();
  fireEvent.click(cancel);
  expect(screen.queryByLabelText("Schedule name")).toBeNull();
  expect(globalThis.document.activeElement).toBe(newScheduleOpener());
  expect(value.save).not.toHaveBeenCalled();
});

it("keeps focus behavior unchanged when cancelling the edit of an existing schedule", async () => {
  const value = fixture();
  render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(
    await screen.findByRole("button", { name: /Weekday review/ }),
  );
  fireEvent.click(await screen.findByRole("button", { name: "Edit schedule" }));
  const cancel = screen.getByRole("button", { name: "Cancel schedule edit" });
  cancel.focus();
  expect(globalThis.document.activeElement).toBe(cancel);
  fireEvent.click(cancel);
  expect(screen.queryByLabelText("Scheduled prompt")).toBeNull();
  // Only dismissing the creation wizard restores the New schedule opener.
  expect(newScheduleOpener().disabled).toBe(false);
  expect(globalThis.document.activeElement).not.toBe(newScheduleOpener());
  expect(value.save).not.toHaveBeenCalled();
});

it.each([1, 2, 3])(
  "closes the idle wizard with Escape from step index %s with the Cancel cleanup",
  async (step) => {
    const value = fixture();
    render(value.view(<Schedules active open={() => {}} />));
    fireEvent.click(newScheduleOpener());
    await fillCreation(value);
    goStep(step);
    pressEscape(
      globalThis.document.querySelector<HTMLElement>(
        "[data-authoring-step]:not([hidden]), .schedule-creation-review",
      ) ?? screen.getByRole("button", { name: "Cancel" }),
    );
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
    expect(newScheduleOpener().disabled).toBe(false);
    expect(globalThis.document.activeElement).toBe(newScheduleOpener());
    expect(value.save).not.toHaveBeenCalled();
  },
);

it("ignores Escape while a save is pending or uncertain, keeping the draft and recovery controls", async () => {
  const value = fixture();
  let settle!: (error: Error) => void;
  value.save.mockImplementationOnce(
    () =>
      new Promise((_resolve, reject) => {
        settle = reject;
      }),
  );
  render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(newScheduleOpener());
  await fillCreation(value);
  goStep(3);
  fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  pressEscape(screen.getByLabelText("Schedule name"));
  expect(screen.getByLabelText("Schedule name")).toBeTruthy();
  expect(
    (screen.getByRole("button", { name: "Cancel" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  await act(async () => {
    settle(new ConnectError("ack lost", Code.Unavailable));
  });
  await screen.findByRole("button", { name: "Retry the same schedule" });
  pressEscape(screen.getByLabelText("Schedule name"));
  expect(screen.getByLabelText("Schedule name")).toBeTruthy();
  expect(
    (screen.getByLabelText("Scheduled prompt") as HTMLTextAreaElement).value,
  ).toBe("Review project changes");
  expect(newScheduleOpener().disabled).toBe(true);
  expect(value.save).toHaveBeenCalledOnce();
});

it("ignores Escape while a fresh Local proof is pending", async () => {
  const value = fixture();
  let resolveProof!: (proof: { machineId: string; token: string }) => void;
  const read = vi.fn(
    () =>
      new Promise<{ machineId: string; token: string }>((resolve) => {
        resolveProof = resolve;
      }),
  );
  render(
    value.view(<Schedules active open={() => {}} readLocalWorker={read} />),
  );
  fireEvent.click(newScheduleOpener());
  await fillCreation(value);
  fireEvent.click(screen.getByRole("radio", { name: "Local computer" }));
  pressEscape(screen.getByLabelText("Schedule name"));
  expect(screen.getByLabelText("Schedule name")).toBeTruthy();
  expect(
    screen.getByLabelText("Scheduled prompt").closest("fieldset")!.disabled,
  ).toBe(true);
  await act(async () => {
    resolveProof({ machineId: value.machine.id, token: "a".repeat(42) + "A" });
  });
  expect(value.save).not.toHaveBeenCalled();
});

it("closes only an open picker on the first Escape and the wizard on the second", async () => {
  const value = fixture();
  render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(newScheduleOpener());
  const project = screen.getByRole("combobox", { name: "Project" });
  fireEvent.click(project);
  await screen.findByRole("option", { name: "Selected project" });
  pressEscape(project);
  expect(screen.queryByRole("option", { name: "Selected project" })).toBeNull();
  expect(screen.getByLabelText("Schedule name")).toBeTruthy();
  pressEscape(project);
  expect(screen.queryByLabelText("Schedule name")).toBeNull();
  expect(globalThis.document.activeElement).toBe(newScheduleOpener());
  expect(value.save).not.toHaveBeenCalled();
});

it.each([
  ["Frequency", "Frequency"],
  ["Weekday", "Weekday"],
  ["overlap", /previous occurrence is still active/i],
  ["Time", "Time"],
])(
  "leaves Escape from the native %s control to its platform picker and cancels from a non-picker control",
  async (_name, label) => {
    const value = fixture();
    render(value.view(<Schedules active open={() => {}} />));
    fireEvent.click(newScheduleOpener());
    await fillCreation(value);
    goStep(2);
    // The weekday and time controls only exist for the weekly preset.
    choose("Frequency", "weekly");
    const control = screen.getByLabelText(label);
    expect(
      control instanceof HTMLSelectElement ||
        (control instanceof HTMLInputElement && control.type === "time"),
    ).toBe(true);
    control.focus();
    // Native picker Escape never sets defaultPrevented before bubbling.
    const event = new KeyboardEvent("keydown", {
      key: "Escape",
      bubbles: true,
      cancelable: true,
    });
    control.dispatchEvent(event);
    expect(screen.getByLabelText("Frequency")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeTruthy();
    expect(newScheduleOpener().disabled).toBe(true);
    pressEscape(control);
    pressEscape(control);
    expect(screen.getByRole("button", { name: "Cancel" })).toBeTruthy();
    // Escape from a non-picker control still cancels the wizard.
    const next = screen.getByRole("button", { name: "Next" });
    next.focus();
    pressEscape(next);
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
    expect(newScheduleOpener().disabled).toBe(false);
    expect(globalThis.document.activeElement).toBe(newScheduleOpener());
    expect(value.save).not.toHaveBeenCalled();
  },
);

it.each([
  ["a defaultPrevented event", (event: Event) => event.preventDefault()],
  ["an IME composition", undefined],
  ["a repeated key", undefined],
  ["a modified key", undefined],
])("ignores Escape for %s", (kind, prevent) => {
  const value = fixture();
  render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(newScheduleOpener());
  const name = screen.getByLabelText("Schedule name");
  if (prevent) {
    name.addEventListener("keydown", prevent, { once: true });
    pressEscape(name);
  } else if (kind === "an IME composition") {
    fireEvent.keyDown(name, { key: "Escape", keyCode: 229, isComposing: true });
  } else if (kind === "a repeated key") {
    pressEscape(name, { repeat: true });
  } else {
    for (const modifier of ["shiftKey", "ctrlKey", "altKey", "metaKey"])
      pressEscape(name, { [modifier]: true });
  }
  expect(screen.getByLabelText("Schedule name")).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
});

it("falls back to a visible Schedules control when the New schedule opener is hidden", () => {
  const value = fixture();
  render(
    <main>
      <button type="button" className="sidebar-context-trigger">
        Open Schedules
      </button>
      {value.view(<Schedules active open={() => {}} />)}
    </main>,
  );
  fireEvent.click(newScheduleOpener());
  // A closed compact drawer makes its whole sidebar surface inert.
  newScheduleOpener().closest("section")!.setAttribute("inert", "");
  pressEscape(screen.getByLabelText("Schedule name"));
  expect(screen.queryByLabelText("Schedule name")).toBeNull();
  expect(globalThis.document.activeElement).toBe(
    screen.getByRole("button", { name: "Open Schedules" }),
  );
});

// Mirrors the stylesheet contract: the compact opener is display:none outside
// the compact breakpoint, so a collapsed wide sidebar must fall through to main.
function dismissWithCollapsedSidebar(
  shell: (children: React.ReactNode) => React.ReactNode,
  css: string,
  beforeDismiss?: () => void,
) {
  const value = fixture();
  const style = globalThis.document.createElement("style");
  style.textContent = css;
  globalThis.document.head.append(style);
  render(<>{shell(value.view(<Schedules active open={() => {}} />))}</>);
  fireEvent.click(newScheduleOpener());
  newScheduleOpener().closest("section")!.setAttribute("inert", "");
  beforeDismiss?.();
  pressEscape(screen.getByLabelText("Schedule name"));
  expect(screen.queryByLabelText("Schedule name")).toBeNull();
  return { value, style };
}

it("skips the CSS-hidden compact opener on a wide layout and focuses main", () => {
  const { style } = dismissWithCollapsedSidebar(
    (children) => (
      <main id="main" tabIndex={-1}>
        <button type="button" className="sidebar-context-trigger">
          Open Schedules
        </button>
        {children}
      </main>
    ),
    ".sidebar-context-trigger { display: none; }",
  );
  try {
    expect(globalThis.document.activeElement).toBe(
      globalThis.document.getElementById("main"),
    );
  } finally {
    style.remove();
  }
});

it("focuses the compact opener when its rule renders it in the compact layout", () => {
  const { style } = dismissWithCollapsedSidebar(
    (children) => (
      <main id="main" tabIndex={-1}>
        <button type="button" className="sidebar-context-trigger">
          Open Schedules
        </button>
        {children}
      </main>
    ),
    ".sidebar-context-trigger { display: block; }",
  );
  try {
    expect(globalThis.document.activeElement).toBe(
      screen.getByRole("button", { name: "Open Schedules" }),
    );
  } finally {
    style.remove();
  }
});

it.each([
  ["a hidden ancestor", { hidden: true }],
  ["an inert ancestor", { inert: true }],
  ["a display:none ancestor", { style: "display: none" }],
  ["a visibility:hidden opener", { buttonStyle: "visibility: hidden" }],
])("falls through to main when the compact opener has %s", (_name, init) => {
  const { style } = dismissWithCollapsedSidebar(
    (children) => (
      <main id="main" tabIndex={-1}>
        <div
          {...("hidden" in init ? { hidden: true } : {})}
          inert={"inert" in init}
          style={"style" in init ? { display: "none" } : undefined}
        >
          <button
            type="button"
            className="sidebar-context-trigger"
            style={"buttonStyle" in init ? { visibility: "hidden" } : undefined}
          >
            Open Schedules
          </button>
        </div>
        {children}
      </main>
    ),
    "",
  );
  try {
    expect(globalThis.document.activeElement).toBe(
      globalThis.document.getElementById("main"),
    );
  } finally {
    style.remove();
  }
});

it("leaves focus untouched when no candidate is rendered", () => {
  const { style } = dismissWithCollapsedSidebar(
    (children) => (
      <main id="main" tabIndex={-1}>
        <button type="button" className="sidebar-context-trigger">
          Open Schedules
        </button>
        {children}
      </main>
    ),
    ".sidebar-context-trigger { display: none; }",
    () => {
      globalThis.document.getElementById("main")!.style.display = "none";
    },
  );
  try {
    expect(globalThis.document.activeElement).toBe(globalThis.document.body);
  } finally {
    style.remove();
  }
});

it("does not take focus back after Escape when focus already moved elsewhere", () => {
  const value = fixture();
  render(
    <>
      <button type="button">Elsewhere</button>
      {value.view(<Schedules active open={() => {}} />)}
    </>,
  );
  fireEvent.click(newScheduleOpener());
  const elsewhere = screen.getByRole("button", { name: "Elsewhere" });
  elsewhere.focus();
  pressEscape(screen.getByLabelText("Schedule name"));
  expect(screen.queryByLabelText("Schedule name")).toBeNull();
  expect(globalThis.document.activeElement).toBe(elsewhere);
});

it("renders and dismisses with Escape in Korean", async () => {
  const value = fixture();
  render(value.view(<Schedules active open={() => {}} />));
  await act(() => i18n.changeLanguage("ko"));
  try {
    fireEvent.click(newScheduleOpener());
    expect(newScheduleOpener().disabled).toBe(true);
    const name = globalThis.document.activeElement as HTMLElement;
    expect(name.tagName).toBe("INPUT");
    pressEscape(name);
    expect(globalThis.document.querySelector("form")).toBeNull();
    expect(globalThis.document.activeElement).toBe(newScheduleOpener());
    expect(value.save).not.toHaveBeenCalled();
  } finally {
    await act(() => i18n.changeLanguage("en"));
  }
});

it("keeps Escape inert for editing an existing schedule", () => {
  const value = fixture(),
    cancel = vi.fn();
  render(
    value.view(
      <ScheduleEditor
        active
        initial={value.schedule}
        saved={() => {}}
        cancel={cancel}
      />,
    ),
  );
  pressEscape(screen.getByLabelText("Schedule name"));
  expect(cancel).not.toHaveBeenCalled();
  expect(screen.getByLabelText("Schedule name")).toBeTruthy();
});

it.each(["a", "한"])(
  "retains the previous prompt at its UTF-8 limit for %s",
  (character) => {
    const value = fixture();
    render(
      value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
    );
    const prompt = character.repeat(
      Math.floor(262144 / new TextEncoder().encode(character).byteLength),
    );
    choose("Scheduled prompt", prompt);
    choose("Scheduled prompt", prompt + character);
    expect(
      (screen.getByLabelText("Scheduled prompt") as HTMLTextAreaElement).value,
    ).toBe(prompt);
    expect(screen.getByText(/previous draft is retained/)).toBeTruthy();
  },
);

it("shows initial catalog loading and successful empty pages without selecting a default", async () => {
  const value = fixture();
  let ready!: () => void;
  const pending = new Promise<void>((resolve) => {
    ready = resolve;
  });
  value.list.mockImplementation(async () => {
    await pending;
    return { resources: [], nextPageToken: "" };
  });
  render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  expect(screen.getByText("Loading Project choices…")).toBeTruthy();
  expect(screen.getByLabelText("Project").getAttribute("data-value")).toBe("");
  ready();
  await screen.findByText("No selectable Project choices are on this page.");
  expect(
    screen.queryByText("No selectable Agent Worker choices are on this page."),
  ).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
});

it.each([Code.PermissionDenied, Code.Unauthenticated, Code.Unavailable])(
  "shows catalog failure %s without substituting a resource",
  async (code) => {
    const value = fixture();
    value.list.mockRejectedValue(new ConnectError("fixture denied", code));
    render(
      value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
    );
    await screen.findAllByText(
      code === Code.Unavailable
        ? /server connection failed while loading these choices/
        : /server denied access to these choices/,
    );
    expect(screen.getByLabelText("Project").getAttribute("data-value")).toBe(
      "",
    );
    expect(value.save).not.toHaveBeenCalled();
  },
);

it("reports cached catalog refresh failure with the previous exact choices and selections", async () => {
  const value = fixture();
  render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  await fillCreation(value);
  goStep(0);
  value.list.mockRejectedValue(
    new ConnectError("fixture offline", Code.Unavailable),
  );
  await value.client.invalidateQueries();
  await screen.findByText(/Showing cached Project choices/);
  expect(screen.getByLabelText("Project").getAttribute("data-value")).toBe(
    value.project.id,
  );
  fireEvent.click(screen.getByRole("combobox", { name: "Project" }));
  expect(screen.getByRole("option", { name: "Selected project" })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
});

it("preserves exact choices through accepted continuation and suspends inactive reads", async () => {
  const value = fixture();
  value.list.mockImplementation(async (request) => ({
    resources: request.filter?.pageToken
      ? []
      : value.resources.filter((row) => row.kind === request.filter?.kind),
    nextPageToken:
      request.filter?.kind === EntityKind.PROJECT && !request.filter.pageToken
        ? "next-project-page"
        : "",
  }));
  const rendered = render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  await fillCreation(value);
  goStep(0);
  const selected = screen.getByRole("combobox", { name: "Project" });
  fireEvent.click(selected);
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Load more Project",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("button", { name: "Load more Project" }));
  await waitFor(() =>
    expect(
      value.list.mock.calls.some(
        ([request]) =>
          request.filter?.kind === EntityKind.PROJECT &&
          request.filter.pageToken === "next-project-page",
      ),
    ).toBe(true),
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Load more Project" }),
    ).toBeNull(),
  );
  expect(selected.getAttribute("data-value")).toBe(value.project.id);
  expect(
    screen
      .getByRole("option", { name: "Selected project" })
      .getAttribute("aria-selected"),
  ).toBe("true");
  fireEvent.keyDown(selected, { key: "Escape" });
  rendered.rerender(
    value.view(
      <ScheduleEditor active={false} saved={() => {}} cancel={() => {}} />,
    ),
  );
  const reads = value.list.mock.calls.length;
  await value.client.invalidateQueries();
  expect(value.list).toHaveBeenCalledTimes(reads);
  expect(value.save).not.toHaveBeenCalled();
});

it("discards a Local selection proof that arrives after creation disposal", async () => {
  const value = fixture();
  let ready!: (proof: { machineId: string; token: string }) => void;
  const read = () =>
    new Promise<{ machineId: string; token: string }>((resolve) => {
      ready = resolve;
    });
  const rendered = render(
    value.view(
      <ScheduleEditor
        active
        saved={() => {}}
        cancel={() => {}}
        readLocalWorker={read}
      />,
    ),
  );
  await fillCreation(value);
  fireEvent.click(screen.getByRole("radio", { name: "Local computer" }));
  rendered.unmount();
  ready({ machineId: value.machine.id, token: "a".repeat(42) + "A" });
  await Promise.resolve();
  expect(value.save).not.toHaveBeenCalled();
});

it("revalidates untouched Runner defaults against the selected Worktree project", async () => {
  const value = fixture(),
    history = newRequestId(),
    local = newRequestId(),
    server = newRequestId(),
    device = newRequestId();
  const machines = [history, local].map((id) =>
    create(ResourceSchema, {
      id,
      kind: EntityKind.MACHINE,
      schemaVersion: 1,
      revision: 1n,
      documentJson: encode({
        name: id,
        enabled: true,
        ...(id === local
          ? { worker_capabilities: ["remote-workspace-clone-v1"] }
          : {}),
      }),
    }),
  );
  const project = create(ResourceSchema, {
    id: value.definition.project_id,
    kind: EntityKind.PROJECT,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      name: "Worktree project",
      repositories: [newRequestId()],
    }),
  });
  const get = vi.fn(async (request: { id: string }) => ({
    resource:
      request.id === project.id
        ? project
        : machines.find((row) => row.id === request.id),
  }));
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {
      getResource: get,
      listResources: (request) => ({
        resources:
          request.filter?.kind === EntityKind.PROJECT
            ? [project]
            : request.filter?.kind === EntityKind.MACHINE
              ? machines
              : [],
      }),
    });
    router.service(ScheduleService, { saveSchedule: value.save });
  });
  const bridge = {
    read: vi.fn(async () => ({
      revision: 1,
      scope: { server_id: server, device_id: device },
      machine_id: history,
      problem: null,
    })),
    update: vi.fn(),
  };
  const { RunnerPreferenceProvider } =
    await import("./runner-device-preferences");
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={value.client}>
        <MutationIntents>
          <RunnerPreferenceProvider
            bridge={bridge}
            readLocalWorker={async () => ({
              machineId: local,
              token: "discarded",
            })}
          >
            <ScheduleEditor active saved={() => {}} cancel={() => {}} />
          </RunnerPreferenceProvider>
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  await waitFor(() =>
    expect(
      screen.getByLabelText("Runner Device").getAttribute("data-value"),
    ).toBe(history),
  );
  const { chooseScrollOption } = await import("./test-scroll-picker");
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Project" }),
    project.id,
  );
  await waitFor(() =>
    expect(
      screen.getByLabelText("Runner Device").getAttribute("data-value"),
    ).toBe(local),
  );
  expect(value.save).not.toHaveBeenCalled();
  expect(bridge.update).not.toHaveBeenCalled();
});

it("validates each wizard step, retains drafts through Back and Edit, and only Review saves", async () => {
  const value = fixture();
  render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  const name = screen.getByLabelText("Schedule name");
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  expect(globalThis.document.activeElement).toBe(name);
  expect(screen.queryByRole("button", { name: "Create schedule" })).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
  fireEvent.keyDown(name, { key: "Enter" });
  expect(value.save).not.toHaveBeenCalled();
  await fillCreation(value);
  goStep(3);
  expect(
    screen.getByText(
      "Review shows configured selections, not execution readiness.",
    ),
  ).toBeTruthy();
  const review = globalThis.document.querySelector(
    ".schedule-creation-review",
  )!;
  for (const id of [value.project.id, value.agent.id, value.machine.id])
    expect(review.outerHTML).not.toContain(id);
  expect(
    review.querySelectorAll(":scope > .schedule-creation-card"),
  ).toHaveLength(3);
  expect(
    screen.getByText("Review project changes", { selector: "dd" }),
  ).toBeTruthy();
  expect(
    (screen.getByRole("button", { name: "Edit task" }) as HTMLButtonElement)
      .disabled,
  ).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Edit task" }));
  expect((name as HTMLInputElement).value).toBe("Morning review");
  expect(globalThis.document.activeElement?.textContent).toBe("Task");
  choose("Schedule name", "Changed title");
  goStep(3);
  expect(screen.getByText("Changed title")).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
  fireEvent.change(name, { target: { value: "" } });
  fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
  expect(globalThis.document.activeElement).toBe(name);
  expect(screen.queryByRole("button", { name: "Create schedule" })).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
});
it("advances exactly one schedule step per plain Enter and creates once from the review step", async () => {
  const value = fixture();
  render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  const name = screen.getByLabelText("Schedule name");
  const stepOf = () =>
    Number(
      globalThis.document
        .querySelector("[data-authoring-step]:not([hidden])")
        ?.getAttribute("data-authoring-step") ?? 3,
    );
  // An invalid step keeps its place and focuses the original validation target.
  expect(pressEnter(name)).toBe(1);
  expect(stepOf()).toBe(0);
  expect(globalThis.document.activeElement).toBe(name);
  await fillCreation(value);
  goStep(0);
  expect(pressEnter(name, { repeat: true })).toBe(0);
  expect(pressEnter(name, { composing: true })).toBe(0);
  expect(pressEnter(name, { keyCode: 229 })).toBe(0);
  expect(pressEnter(name, { shiftKey: true })).toBe(0);
  expect(stepOf()).toBe(0);
  expect(pressEnter(name)).toBe(1);
  expect(stepOf()).toBe(1);
  goStep(3);
  expect(value.save).not.toHaveBeenCalled();
  fireEvent.submit(name.closest("form")!);
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
});
it("opens the Execution step and collapsed overrides for an invalid reference", async () => {
  const value = fixture();
  render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  await fillCreation(value);
  const disclosure = screen.getByRole("button", {
    name: /Starting reference overrides/,
  });
  fireEvent.click(disclosure);
  await within(screen.getByLabelText("Add repository override")).findByRole(
    "option",
    { name: "oss" },
  );
  choose("Add repository override", value.repositoryId);
  fireEvent.click(
    screen.getByRole("button", { name: "Add starting override" }),
  );
  const field = screen.getByLabelText("Starting oss name");
  fireEvent.click(disclosure);
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  expect(disclosure.getAttribute("aria-expanded")).toBe("true");
  expect(globalThis.document.activeElement).toBe(field);
  expect(value.save).not.toHaveBeenCalled();
});

it("retains exact Review labels through locale changes without another choice read or save", async () => {
  const value = fixture();
  render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  await fillCreation(value);
  goStep(3);
  const assertLabels = () => {
    const review = globalThis.document.querySelector(
      ".schedule-creation-review",
    )!;
    for (const label of [
      "Selected project",
      "Selected agent",
      "Selected Worker",
    ])
      expect(review.textContent).toContain(label);
  };
  assertLabels();
  const reads = value.list.mock.calls.length;
  await act(() => i18n.changeLanguage("ko"));
  assertLabels();
  await act(() => i18n.changeLanguage("en"));
  assertLabels();
  expect(value.list).toHaveBeenCalledTimes(reads);
  expect(value.save).not.toHaveBeenCalled();
});

it("allows the focused step heading to scroll into view on Next, Back and Review Edit", async () => {
  const value = fixture();
  render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  await fillCreation(value);
  const heading = globalThis.document.querySelector<HTMLElement>(
    ".schedule-creation-step-title",
  )!;
  const focus = vi.spyOn(heading, "focus");
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  expect(focus).toHaveBeenLastCalledWith();
  expect(globalThis.document.activeElement).toBe(heading);
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(focus).toHaveBeenLastCalledWith();
  goStep(3);
  fireEvent.click(screen.getByRole("button", { name: "Edit task" }));
  expect(focus).toHaveBeenLastCalledWith();
  expect(heading.textContent).toBe("Task");
  expect(value.save).not.toHaveBeenCalled();
});

it("preserves an opted-out draft through navigation and submits paused creation intent", async () => {
  const value = fixture(),
    rendered = render(
      value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
    );
  const footer = globalThis.document.querySelector(
    ".schedule-creation-footer",
  )!;
  expect(footer.textContent).toContain("Enabled on creation");
  await fillCreation(value);
  goStep(2);
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Enable future scheduled runs" }),
  );
  expect(footer.textContent).toContain("Paused on creation");
  goStep(1);
  goStep(2);
  expect(
    (
      screen.getByRole("checkbox", {
        name: "Enable future scheduled runs",
      }) as HTMLInputElement
    ).checked,
  ).toBe(false);
  rendered.rerender(
    value.view(
      <ScheduleEditor active={false} saved={() => {}} cancel={() => {}} />,
    ),
  );
  rendered.rerender(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  goStep(3);
  expect(
    globalThis.document.querySelector(".schedule-creation-review")!.textContent,
  ).toContain("Paused on creation");
  fireEvent.click(screen.getByRole("button", { name: "Edit task" }));
  expect(footer.textContent).toContain("Paused on creation");
  expect(footer.textContent).toContain("1 of 4");
  goStep(3);
  fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  expect(
    JSON.parse(new TextDecoder().decode(createRequest(value).definitionJson))
      .enabled,
  ).toBe(false);
});

it("starts checked again after canceling an opted-out draft without saving", async () => {
  const value = fixture();
  render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "New schedule" }));
  await fillCreation(value);
  goStep(2);
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Enable future scheduled runs" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(value.save).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "New schedule" }));
  await fillCreation(value);
  goStep(2);
  expect(
    (
      screen.getByRole("checkbox", {
        name: "Enable future scheduled runs",
      }) as HTMLInputElement
    ).checked,
  ).toBe(true);
  expect(value.save).not.toHaveBeenCalled();
});

it.each([
  ["Project", EntityKind.PROJECT, "project_id", 0],
  ["Agent Worker", EntityKind.AGENT, "agent_id", 1],
  ["Runner Device", EntityKind.MACHINE, "machine_id", 1],
] as const)(
  "retains %s verification until it settles before allowing wizard navigation",
  async (label, kind, key, step) => {
    const value = fixture();
    const replacement = create(ResourceSchema, {
      id: newRequestId(),
      kind,
      schemaVersion: kind === EntityKind.AGENT ? 4 : 1,
      revision: 1n,
      documentJson: encode({
        ...(kind === EntityKind.AGENT ? document(value.agent) : {}),
        name: "Replacement selection",
      }),
    });
    value.resources.push(replacement);
    const originalGet = value.get.getMockImplementation()!;
    let complete!: (response: { resource: Resource }) => void;
    value.get.mockImplementation((request) =>
      request.id === replacement.id
        ? new Promise((resolve) => {
            complete = resolve;
          })
        : originalGet(request),
    );
    render(
      value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
    );
    await fillCreation(value);
    goStep(step);
    const picker = screen.getByRole("combobox", { name: label });
    fireEvent.click(picker);
    fireEvent.click(
      await screen.findByRole("option", { name: "Replacement selection" }),
    );
    await waitFor(() => expect(complete).toBeTypeOf("function"));
    expect(picker.dataset.value).toBe(value.definition[key]);
    expect(screen.getByRole("button", { name: "Next" })).toHaveProperty(
      "disabled",
      true,
    );
    expect(screen.getByRole("button", { name: "Cancel" })).toHaveProperty(
      "disabled",
      true,
    );
    if (step)
      expect(screen.getByRole("button", { name: "Back" })).toHaveProperty(
        "disabled",
        true,
      );
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    fireEvent.submit(
      globalThis.document.querySelector(".schedule-creation form")!,
    );
    expect(
      globalThis.document.querySelector(".schedule-creation-step-title")
        ?.textContent,
    ).toBe(step ? "Execution" : "Task");
    expect(value.save).not.toHaveBeenCalled();
    await act(() => complete({ resource: replacement }));
    await waitFor(() => expect(picker.dataset.value).toBe(replacement.id));
    expect(screen.getByRole("button", { name: "Next" })).toHaveProperty(
      "disabled",
      false,
    );
    goStep(3);
    expect(
      globalThis.document.querySelector(".schedule-creation-review")
        ?.textContent,
    ).toContain("Replacement selection");
    expect(
      globalThis.document.querySelector(".schedule-creation-review")?.outerHTML,
    ).not.toContain(replacement.id);
    expect(value.save).not.toHaveBeenCalled();
  },
);

function StartingFixture({
  project,
  initial = [],
}: {
  project: string;
  initial: unknown[];
}) {
  const [starting, setStarting] = useState(initial);
  return (
    <StartingReferences
      project={project}
      starting={starting}
      change={setStarting}
      active
      showRepositoryNames
    />
  );
}
it("labels duplicate schedule repositories by their original positions and saves the selected UUID", async () => {
  const value = fixture(),
    second = newRequestId();
  value.resources.push(
    create(ResourceSchema, {
      id: second,
      kind: EntityKind.REPOSITORY,
      schemaVersion: 1,
      revision: 1n,
      documentJson: encode({ name: "oss" }),
    }),
  );
  value.project.documentJson = encode({
    ...document(value.project),
    repositories: [value.repositoryId, second],
  });
  render(
    value.view(
      <ScheduleEditor
        initial={value.schedule}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  await screen.findByRole("option", { name: "oss · 2" });
  choose("Add repository override", second);
  fireEvent.click(
    screen.getByRole("button", { name: "Add starting override" }),
  );
  const field = screen.getByLabelText("Starting oss · 2 name");
  fireEvent.change(field, { target: { value: "feature/retained" } });
  expect(screen.getByRole("option", { name: "oss · 1" })).toBeTruthy();
  expect(screen.queryByRole("option", { name: "oss · 2" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Save schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  const request = value.save.mock.calls[0][0] as {
    definitionJson: Uint8Array;
    mutation: { expectedRevision: bigint };
  };
  expect(
    JSON.parse(new TextDecoder().decode(request.definitionJson)).starting,
  ).toEqual([
    {
      repository_id: second,
      reference: { type: "local-branch", name: "feature/retained" },
    },
  ]);
  expect(request.mutation.expectedRevision).toBe(3n);
});
it.each(["missing", "unsupported", "invalid", "failure"])(
  "preserves retained schedule overrides when repository names are %s",
  async (kind) => {
    const value = fixture(),
      initial = [
        {
          repository_id: value.repositoryId,
          reference: { type: "local-branch", name: "retained" },
        },
      ];
    const original = value.get.getMockImplementation()!;
    value.get.mockImplementation(async (request) => {
      if (request.id === value.repositoryId) {
        if (kind === "failure")
          throw new ConnectError("private provider detail", Code.Unavailable);
        if (kind === "missing") return { resource: undefined };
        return {
          resource: create(ResourceSchema, {
            id: value.repositoryId,
            kind: EntityKind.REPOSITORY,
            schemaVersion: kind === "unsupported" ? 999 : 1,
            revision: 1n,
            documentJson: encode({
              name: kind === "invalid" ? "\0" : "wrong name",
            }),
          }),
        };
      }
      return original(request);
    });
    render(
      value.view(
        <StartingFixture project={value.project.id} initial={initial} />,
      ),
    );
    await waitFor(() =>
      expect(
        screen.queryByText("Loading selected repository names…"),
      ).toBeNull(),
    );
    expect(
      (
        screen.getByLabelText(
          "Starting Repository unavailable · 1 name",
        ) as HTMLInputElement
      ).value,
    ).toBe("retained");
    expect(globalThis.document.body.textContent).not.toContain(
      value.repositoryId,
    );
    expect(globalThis.document.body.textContent).not.toContain(
      "private provider detail",
    );
    if (kind === "failure" || kind === "missing") {
      value.get.mockImplementation(original);
      fireEvent.click(
        screen.getByRole("button", { name: "Retry repository loading" }),
      );
      await screen.findByLabelText("Starting oss name");
      expect(
        (screen.getByLabelText("Starting oss name") as HTMLInputElement).value,
      ).toBe("retained");
    }
    fireEvent.click(
      screen.getByRole("button", { name: "Remove starting override" }),
    );
    await screen.findByRole("option", {
      name:
        kind === "failure" || kind === "missing"
          ? "oss"
          : "Repository unavailable · 1",
    });
    expect(value.save).not.toHaveBeenCalled();
  },
);
it("rejects delayed repository names from the previous schedule Project", async () => {
  const value = fixture(),
    otherProject = newRequestId(),
    otherRepository = newRequestId();
  let finish!: (value: { resource: Resource }) => void;
  value.resources.push(
    create(ResourceSchema, {
      id: otherProject,
      kind: EntityKind.PROJECT,
      schemaVersion: 1,
      revision: 1n,
      documentJson: encode({ repositories: [otherRepository] }),
    }),
    create(ResourceSchema, {
      id: otherRepository,
      kind: EntityKind.REPOSITORY,
      schemaVersion: 1,
      revision: 1n,
      documentJson: encode({ name: "Current repository" }),
    }),
  );
  const original = value.get.getMockImplementation()!;
  value.get.mockImplementation(async (request) =>
    request.id === value.repositoryId
      ? new Promise<{ resource: Resource }>((resolve) => {
          finish = resolve;
        })
      : original(request),
  );
  const mounted = render(
    value.view(
      <StartingFixture
        key={value.project.id}
        project={value.project.id}
        initial={[]}
      />,
    ),
  );
  await waitFor(() => expect(finish).toBeDefined());
  expect(screen.getByText("Loading selected repository names…")).toBeTruthy();
  expect(globalThis.document.body.textContent).not.toContain(
    value.repositoryId,
  );
  mounted.rerender(
    value.view(
      <StartingFixture
        key={otherProject}
        project={otherProject}
        initial={[]}
      />,
    ),
  );
  await screen.findByRole("option", { name: "Current repository" });
  await act(async () =>
    finish({
      resource: create(ResourceSchema, {
        id: value.repositoryId,
        kind: EntityKind.REPOSITORY,
        schemaVersion: 1,
        revision: 1n,
        documentJson: encode({ name: "Previous repository" }),
      }),
    }),
  );
  expect(
    screen.queryByRole("option", { name: "Previous repository" }),
  ).toBeNull();
  expect(
    screen
      .getByRole("option", { name: "Current repository" })
      .getAttribute("value"),
  ).toBe(otherRepository);
  expect(value.save).not.toHaveBeenCalled();
});

it("resolves retained override names outside the current Project without changing their references", async () => {
  const value = fixture(),
    retained = newRequestId(),
    name = "repository/" + "long-name".repeat(20);
  value.resources.push(
    create(ResourceSchema, {
      id: retained,
      kind: EntityKind.REPOSITORY,
      schemaVersion: 1,
      revision: 1n,
      documentJson: encode({ name }),
    }),
  );
  render(
    value.view(
      <StartingFixture
        project={value.project.id}
        initial={[
          {
            repository_id: retained,
            reference: { type: "local-branch", name: "saved-branch" },
          },
        ]}
      />,
    ),
  );
  const field = await screen.findByLabelText(`Starting ${name} name`);
  expect((field as HTMLInputElement).value).toBe("saved-branch");
  expect(
    (await screen.findByRole("option", { name: "oss" })).getAttribute("value"),
  ).toBe(value.repositoryId);
  await act(async () => {
    await i18n.changeLanguage("ko");
  });
  expect((field as HTMLInputElement).value).toBe("saved-branch");
  expect(globalThis.document.body.textContent).toContain(name);
  expect(value.save).not.toHaveBeenCalled();
});

it("uses localized unavailable Review names for missing or mismatched cache without reads", async () => {
  const value = fixture();
  render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  await fillCreation(value);
  goStep(3);
  const reads = value.get.mock.calls.length,
    lists = value.list.mock.calls.length;
  const selected = value.client
    .getQueryCache()
    .getAll()
    .filter((query) => {
      const resource = (query.state.data as { resource?: Resource } | undefined)
        ?.resource;
      return (
        resource &&
        [value.project.id, value.agent.id, value.machine.id].includes(
          resource.id,
        )
      );
    });
  expect(selected).toHaveLength(3);
  await act(async () => {
    selected.forEach((query, index) =>
      value.client.setQueryData(
        query.queryKey,
        index === 0
          ? {}
          : {
              resource: create(ResourceSchema, {
                id: newRequestId(),
                kind: EntityKind.PROJECT,
                documentJson: encode({ name: "Wrong resource" }),
              }),
            },
      ),
    );
  });
  const review = globalThis.document.querySelector(
    ".schedule-creation-review",
  )!;
  await waitFor(() =>
    expect(
      within(review as HTMLElement).getAllByText("Name unavailable"),
    ).toHaveLength(3),
  );
  expect(review.textContent).not.toContain("Wrong resource");
  await act(() => i18n.changeLanguage("ko"));
  expect(
    within(review as HTMLElement).getAllByText("이름을 사용할 수 없음"),
  ).toHaveLength(3);
  await act(() => i18n.changeLanguage("en"));
  expect(value.get).toHaveBeenCalledTimes(reads);
  expect(value.list).toHaveBeenCalledTimes(lists);
  fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = value.save.mock.calls[0][0] as { definitionJson: Uint8Array };
  expect(
    JSON.parse(new TextDecoder().decode(request.definitionJson)),
  ).toMatchObject({
    project_id: value.project.id,
    agent_id: value.agent.id,
    machine_id: value.machine.id,
  });
});

it("renders ordered name-only override rows and preserves complete user UUID text and exact submissions", async () => {
  const value = fixture(),
    second = newRequestId(),
    userText = newRequestId();
  value.resources.push(
    create(ResourceSchema, {
      id: second,
      kind: EntityKind.REPOSITORY,
      schemaVersion: 1,
      revision: 1n,
      documentJson: encode({ name: "other" }),
    }),
  );
  value.project.documentJson = encode({
    ...document(value.project),
    repositories: [value.repositoryId, second],
  });
  render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  await fillCreation(value);
  fireEvent.click(
    screen.getByRole("button", { name: /Starting reference overrides/ }),
  );
  await screen.findByRole("option", { name: "oss" });
  choose("Add repository override", value.repositoryId);
  fireEvent.click(
    screen.getByRole("button", { name: "Add starting override" }),
  );
  choose("Starting oss name", `feature/${userText}`);
  choose("Add repository override", second);
  fireEvent.click(
    screen.getByRole("button", { name: "Add starting override" }),
  );
  await screen.findByLabelText("Starting other name");
  choose("Starting other type", "commit");
  choose("Starting other name", "release/full-reference");
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  choose("Scheduled prompt", `First line\n${userText}\nLast line`);
  goStep(3);
  const review = globalThis.document.querySelector(
    ".schedule-creation-review",
  )!;
  for (const id of [
    value.project.id,
    value.agent.id,
    value.machine.id,
    value.repositoryId,
    second,
  ])
    expect(review.outerHTML).not.toContain(id);
  expect(review.querySelector(".schedule-review-prompt")?.textContent).toBe(
    `First line\n${userText}\nLast line`,
  );
  expect(within(review as HTMLElement).getByText("Repository 1")).toBeTruthy();
  expect(within(review as HTMLElement).getByText("Repository 2")).toBeTruthy();
  expect(
    within(review as HTMLElement).getByText(`feature/${userText}`),
  ).toBeTruthy();
  expect(within(review as HTMLElement).getByText("commit")).toBeTruthy();
  await act(() => i18n.changeLanguage("ko"));
  expect(within(review as HTMLElement).getByText("저장소 2")).toBeTruthy();
  await act(() => i18n.changeLanguage("en"));
  fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = value.save.mock.calls[0][0] as { definitionJson: Uint8Array };
  expect(
    JSON.parse(new TextDecoder().decode(request.definitionJson)),
  ).toMatchObject({
    prompt: `First line\n${userText}\nLast line`,
    starting: [
      {
        repository_id: value.repositoryId,
        reference: { type: "local-branch", name: `feature/${userText}` },
      },
      {
        repository_id: second,
        reference: { type: "commit", name: "release/full-reference" },
      },
    ],
  });
});

it("keeps same-named Review selections distinct by their original identity", async () => {
  const value = fixture(),
    selectedId = newRequestId();
  value.resources.push(
    create(ResourceSchema, { ...value.project, id: selectedId }),
  );
  render(
    value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
  );
  choose("Schedule name", "Equal names");
  choose("Scheduled prompt", "Keep exact selection");
  const { chooseScrollOption } = await import("./test-scroll-picker");
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Project" }),
    selectedId,
  );
  await waitFor(() =>
    expect(
      screen.getByRole("combobox", { name: "Project" }).dataset.value,
    ).toBe(selectedId),
  );
  goStep(1);
  for (const [label, resource] of [
    ["Agent Worker", value.agent],
    ["Runner Device", value.machine],
  ] as const) {
    await chooseScrollOption(
      screen.getByRole("combobox", { name: label }),
      resource.id,
    );
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: label }).dataset.value).toBe(
        resource.id,
      ),
    );
  }
  goStep(3);
  const review = globalThis.document.querySelector(
    ".schedule-creation-review",
  )!;
  expect(review.textContent).toContain("Selected project");
  expect(review.outerHTML).not.toContain(selectedId);
  fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(
    JSON.parse(new TextDecoder().decode(createRequest(value).definitionJson)),
  ).toMatchObject({ project_id: selectedId });
});

it.each([
  [
    "en",
    "New schedules enable future runs by default. Uncheck this option to create a paused schedule.",
  ],
  [
    "ko",
    "새 일정은 향후 실행이 기본으로 활성화됩니다. 일시 중지 상태로 만들려면 이 옵션의 체크를 해제하세요.",
  ],
])(
  "explains the enabled creation default in %s without changing an opted-out draft",
  async (language, guidance) => {
    const value = fixture();
    render(
      value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />),
    );
    await fillCreation(value);
    goStep(2);
    const option = screen.getByRole("checkbox", {
      name: "Enable future scheduled runs",
    }) as HTMLInputElement;
    expect(option.checked).toBe(true);
    fireEvent.click(option);
    await act(async () => {
      await i18n.changeLanguage(language);
    });
    expect(screen.getByText(guidance)).toBeTruthy();
    expect(option.checked).toBe(false);
    expect(value.save).not.toHaveBeenCalled();
  },
);

it.each(["succeeded", "failed", "canceled"])(
  "keeps Schedule Task usable after closing a verified %s inspection without Finish",
  async (state) => {
    const value = fixture();
    const initial = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.JOB,
      schemaVersion: 1,
      revision: 1n,
      documentJson: encode({
        type: "harness-discovery",
        machine_id: value.machine.id,
        state: "queued",
      }),
    });
    const settled = create(ResourceSchema, {
      ...initial,
      revision: 2n,
      documentJson: encode({
        type: "harness-discovery",
        machine_id: value.machine.id,
        state,
        ...(state === "failed"
          ? {
              problem: {
                message: "Original inspection failed",
                code: "unsupported",
              },
            }
          : {}),
      }),
    });
    value.discovery.mockResolvedValue({ machine: value.machine, job: initial });
    value.get.mockImplementation(async (request) => ({
      resource:
        request.kind === EntityKind.JOB
          ? settled
          : value.resources.find((row) => row.id === request.id),
    }));
    const { RunnerPreferenceProvider } =
      await import("./runner-device-preferences");
    const scope = { server_id: newRequestId(), device_id: newRequestId() };
    const bridge = {
      read: vi.fn(async () => ({
        revision: 1,
        scope,
        machine_id: value.machine.id,
        problem: null,
      })),
      update: vi.fn(),
    };
    function Surface() {
      const inspect = useRunnerRemediation();
      return (
        <>
          <button onClick={() => inspect?.(value.machine)}>
            Inspect preferred Runner
          </button>
          <ScheduleEditor active saved={() => {}} cancel={() => {}} />
          {inspect?.body}
        </>
      );
    }
    render(
      value.view(
        <RunnerPreferenceProvider bridge={bridge}>
          <RunnerRemediationProvider active>
            <Surface />
          </RunnerRemediationProvider>
        </RunnerPreferenceProvider>,
      ),
    );
    await waitFor(() =>
      expect(
        screen.getByLabelText("Runner Device").getAttribute("data-value"),
      ).toBe(value.machine.id),
    );
    const prompt = screen.getByLabelText("Scheduled prompt");
    fireEvent.change(screen.getByLabelText("Schedule name"), {
      target: { value: "Retained Task name" },
    });
    fireEvent.change(prompt, {
      target: { value: "Unchanged retained schedule draft" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Inspect preferred Runner" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Run optional diagnostics" }),
    );
    await screen.findByRole("button", { name: "Finish inspection" });
    fireEvent.click(
      screen.getByRole("button", { name: "Close Inspect installed harnesses" }),
    );
    await waitFor(() =>
      expect(
        screen.getByLabelText("Scheduled prompt").matches(":disabled"),
      ).toBe(false),
    );
    expect(screen.getByLabelText("Scheduled prompt")).toHaveProperty(
      "value",
      "Unchanged retained schedule draft",
    );
    expect(screen.getByLabelText("Schedule name").matches(":disabled")).toBe(
      false,
    );
    expect(
      screen.getByRole("combobox", { name: "Project" }).matches(":disabled"),
    ).toBe(false);
    expect(screen.getByRole("button", { name: "Cancel" })).toHaveProperty(
      "disabled",
      false,
    );
    expect(screen.getByRole("button", { name: "Next" })).toHaveProperty(
      "disabled",
      false,
    );
    expect(screen.getByLabelText("Schedule name")).toHaveProperty(
      "value",
      "Retained Task name",
    );
    expect(value.save).not.toHaveBeenCalled();
    fireEvent.click(
      screen.getByRole("button", { name: "Inspect preferred Runner" }),
    );
    expect(
      await screen.findByRole("button", { name: "Finish inspection" }),
    ).toBeTruthy();
    expect(value.discovery).toHaveBeenCalledOnce();
    expect(value.save).not.toHaveBeenCalled();
    value.client.clear();
  },
);

it("opens a Schedule in a new window on a primary modified click and keeps the workspace unchanged", async () => {
  const destination = fakeDestinationWindow();
  const value = fixture();
  render(destination.wrap(value.view(<Schedules active open={() => {}} />)));
  const row = await screen.findByRole("button", { name: /Weekday review/ });
  fireEvent.click(row, primaryClick);
  expect(destination.opened).toHaveBeenCalledExactlyOnceWith({
    surface: Surface.Schedules,
    item: value.schedule.id,
  });
  expect(row.getAttribute("aria-current")).toBeNull();
  expect(screen.queryByRole("button", { name: "Run now" })).toBeNull();
  fireEvent.click(row, { detail: 1 });
  await screen.findByRole("button", { name: "Run now" });
  expect(destination.opened).toHaveBeenCalledTimes(1);
});

function details(value: ReturnType<typeof fixture>) {
  return render(
    value.view(
      <ScheduleDetails
        initial={value.schedule}
        active
        open={() => {}}
        close={() => {}}
        edit={() => {}}
      />,
    ),
  );
}
it("marks Delete schedule Danger while Run now and Resume stay neutral", async () => {
  const value = fixture();
  details(value);
  expectTone(
    await screen.findByRole("button", { name: "Resume future runs" }),
    null,
  );
  expectTone(screen.getByRole("button", { name: "Run now" }), null);
  expectTone(screen.getByRole("button", { name: "Delete schedule" }), danger);
  expect(value.remove).not.toHaveBeenCalled();
});
it("derives the shared confirmation tone from the selected branch: delete is Danger and Run now is neutral", async () => {
  const value = fixture();
  details(value);
  await screen.findByRole("button", { name: "Run now" });
  fireEvent.click(screen.getByRole("button", { name: "Delete schedule" }));
  expectTone(
    screen.getByRole("button", { name: "Confirm schedule deletion" }),
    danger,
  );
  expectTone(screen.getByRole("button", { name: "Cancel" }), null);
  fireEvent.click(screen.getByRole("button", { name: "Run now" }));
  expectTone(screen.getByRole("button", { name: "Confirm Run now" }), neutral);
  expect(
    screen.queryByRole("button", { name: "Confirm schedule deletion" }),
  ).toBeNull();
  expect(value.remove).not.toHaveBeenCalled();
  expect(value.run).not.toHaveBeenCalled();
});
it("marks only the retry of the same schedule deletion Danger", async () => {
  const value = fixture();
  value.remove.mockRejectedValueOnce(
    new ConnectError("lost acknowledgment", Code.Unavailable),
  );
  details(value);
  await screen.findByRole("button", { name: "Run now" });
  fireEvent.click(screen.getByRole("button", { name: "Delete schedule" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Confirm schedule deletion" }),
  );
  expectTone(
    await screen.findByRole("button", {
      name: "Retry the same schedule deletion",
    }),
    danger,
  );
});
it("keeps the Run now retry neutral", async () => {
  const value = fixture();
  value.run.mockRejectedValueOnce(
    new ConnectError("lost acknowledgment", Code.Unavailable),
  );
  details(value);
  await screen.findByRole("button", { name: "Run now" });
  fireEvent.click(screen.getByRole("button", { name: "Run now" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm Run now" }));
  expectTone(
    await screen.findByRole("button", { name: "Retry the same Run now" }),
    neutral,
  );
});
it("keeps the schedule control retry neutral", async () => {
  const value = fixture();
  value.control.mockRejectedValueOnce(
    new ConnectError("lost acknowledgment", Code.Unavailable),
  );
  details(value);
  fireEvent.click(
    await screen.findByRole("button", { name: "Resume future runs" }),
  );
  expectTone(
    await screen.findByRole("button", {
      name: "Retry the same schedule control",
    }),
    neutral,
  );
});
