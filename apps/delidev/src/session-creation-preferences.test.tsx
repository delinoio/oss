// SPDX-License-Identifier: Apache-2.0
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
} from "@testing-library/react";
import { StrictMode } from "react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemService,
  SystemCapability,
  newRequestId,
  type Resource,
  type CreateSessionRequest,
} from "@delinoio/delidev-api-client";
import {
  chooseScrollOption,
  scrollChoiceValue,
  waitScrollChoices,
} from "./test-scroll-picker";
import { NewSession, NewSessionKind } from "./new-session";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import {
  parseCreationPreferences,
  CreationPreferenceProblem,
  type CreationPreferencePair,
  type CreationPreferenceSnapshot,
} from "./session-creation-preferences";

function resource(kind: EntityKind, name: string, extra = {}): Resource {
  return create(ResourceSchema, {
    kind,
    id: newRequestId(),
    revision: 1n,
    schemaVersion: kind === EntityKind.AGENT ? 4 : 1,
    documentJson: encode({
      name,
      ...(kind === EntityKind.AGENT
        ? {
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
          }
        : {}),
      ...extra,
    }),
  });
}
function fixture(
  initial = true,
  includeRemembered = false,
  defaults?: {
    global: boolean;
    fail?: boolean;
    project?: "enabled" | "disabled" | "inherit";
  },
) {
  const agent = resource(EntityKind.AGENT, "Remembered agent"),
    machine = resource(EntityKind.MACHINE, "Disconnected runner", {
      disabled: false,
    });
  const otherAgent = resource(EntityKind.AGENT, "Manual agent"),
    otherMachine = resource(EntityKind.MACHINE, "Manual runner");
  const projects = [
    resource(EntityKind.PROJECT, "Allowed project", {
      repositories: [],
      agents: { configured: true, ids: [agent.id] },
    }),
    resource(EntityKind.PROJECT, "Forbidden project", {
      repositories: [],
      agents: { configured: true, ids: [otherAgent.id] },
    }),
    resource(EntityKind.PROJECT, "Empty restriction", {
      repositories: [],
      agents: { configured: true, ids: [] },
    }),
  ];
  const defaultSettings = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SETTINGS,
    revision: 1n,
    schemaVersion: 3,
    documentJson: encode({
      automatic_plan_approval: false,
      plan_mode_default: defaults?.global ?? false,
      branch_prefix: "delidev/",
    }),
  });
  if (defaults)
    for (const row of projects) {
      row.schemaVersion = 3;
      row.documentJson = encode({
        ...JSON.parse(new TextDecoder().decode(row.documentJson)),
        settings: { plan_mode_default: defaults.project ?? "inherit" },
      });
    }
  const scope = { server_id: newRequestId(), device_id: newRequestId() };
  let revision = 1;
  const memory = new Map<NewSessionKind, CreationPreferencePair>();
  if (initial)
    memory.set(NewSessionKind.Session, {
      agent_id: agent.id,
      machine_id: machine.id,
    });
  const bridge = {
    read: vi.fn(async (kind: NewSessionKind): Promise<unknown> => ({
      revision,
      scope,
      pair: memory.get(kind) ?? null,
      problem: null,
    })),
    update: vi.fn(
      async (
        kind: NewSessionKind,
        pair: CreationPreferencePair,
        expected: number,
      ): Promise<unknown> => {
        expect(expected).toBe(revision);
        memory.set(kind, pair);
        return { revision: ++revision, scope, pair, problem: null };
      },
    ),
  };
  const get = vi.fn((request: { kind: EntityKind; id: string }) => ({
    resource: [agent, machine, otherAgent, otherMachine, ...projects].find(
      (row) => row.id === request.id && row.kind === request.kind,
    ),
  }));
  const createSession = vi.fn(async (_request: CreateSessionRequest) => ({
    change: { session: resource(EntityKind.SESSION, "Accepted") },
  }));
  const list = vi.fn(async (request: { filter?: { kind?: EntityKind } }) => {
    if (request.filter?.kind === EntityKind.SETTINGS && defaults?.fail)
      throw new ConnectError("defaults unavailable", Code.Unavailable);
    return {
      resources:
        request.filter?.kind === EntityKind.SETTINGS && defaults
          ? [defaultSettings]
          : request.filter?.kind === EntityKind.AGENT
            ? [otherAgent, ...(includeRemembered ? [agent] : [])]
            : request.filter?.kind === EntityKind.MACHINE
              ? [otherMachine]
              : request.filter?.kind === EntityKind.PROJECT
                ? projects
                : [],
    };
  });
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {
      getResource: get,
      listResources: list,
    });
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [
          SystemCapability.AUTOMATIC_TITLES_V1,
          ...(defaults ? [SystemCapability.SESSION_DEFAULTS_V1] : []),
        ],
      }),
    });
    router.service(SessionService, { createSession });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const view = (
    kind = NewSessionKind.Session,
    readLocalWorker?: () => Promise<{ machineId: string; token: string }>,
    active = true,
    creationScope = scope,
  ) => (
    <StrictMode>
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <MutationIntents>
            <NewSession
              kind={kind}
              active={active}
              ownsActivation
              activation={1}
              back={() => {}}
              openSettings={() => {}}
              open={() => {}}
              created={() => {}}
              preferenceBridge={bridge}
              preferenceScope={creationScope}
              readLocalWorker={readLocalWorker}
            />
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
    </StrictMode>
  );
  return {
    agent,
    machine,
    otherAgent,
    otherMachine,
    projects,
    scope,
    bridge,
    memory,
    get,
    createSession,
    defaultSettings,
    list,
    client,
    view,
  };
}

// Agent and Runner restoration settle independently. Submit only after the
// original exact-resource eligibility reads have enabled the product action.
async function submitCreation(name = "Create session") {
  const button = screen.getByRole("button", { name });
  await waitFor(() =>
    expect((button as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(button);
}

it("restores exact off-page resources including a disconnected registered runner", async () => {
  const f = fixture();
  render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.agent.id),
  );
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
  ).toBe(f.machine.id);
  expect(
    screen.getByRole("combobox", { name: "Agent Worker" }).textContent,
  ).toBe("Remembered agent");
  expect(screen.getByRole("combobox", { name: "Runs on" }).textContent).toBe(
    "Disconnected runner",
  );
  expect(f.get.mock.calls.some(([request]) => request.id === f.agent.id)).toBe(
    true,
  );
  expect(f.bridge.update).not.toHaveBeenCalled();
  expect(f.createSession).not.toHaveBeenCalled();
});
it("fences late preference reads after manual selection and prompt edits", async () => {
  const f = fixture();
  let resolve!: (value: unknown) => void;
  f.bridge.read.mockImplementation(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  render(f.view());
  await waitScrollChoices(
    screen.getByRole("combobox", { name: "Agent Worker" }),
  );
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Agent Worker" }),
    f.otherAgent.id,
  );
  fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
    target: { value: "Edited draft" },
  });
  await act(async () =>
    resolve({
      revision: 1,
      scope: f.scope,
      pair: { agent_id: f.agent.id, machine_id: f.machine.id },
      problem: null,
    }),
  );
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
  ).toBe(f.otherAgent.id);
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
  ).toBe("");
  expect(f.bridge.update).not.toHaveBeenCalled();
});
it("restores project-permitted fields independently and keeps configured-empty deny-all", async () => {
  const f = fixture();
  render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.agent.id),
  );
  for (const [index, expected] of [
    [0, f.agent.id],
    [1, ""],
    [2, ""],
  ] as const) {
    await chooseScrollOption(
      screen.getByRole("listbox", { name: "Project" }),
      f.projects[index].id,
    );
    await waitFor(() =>
      expect(
        f.get.mock.calls.some(([r]) => r.id === f.projects[index].id),
      ).toBe(true),
    );
    await waitFor(() =>
      expect(
        scrollChoiceValue(
          screen.getByRole("combobox", { name: "Agent Worker" }),
        ),
      ).toBe(expected),
    );
    await waitFor(() =>
      expect(
        scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
      ).toBe(f.machine.id),
    );
  }
  expect(f.bridge.update).not.toHaveBeenCalled();
});
it("saves only the original accepted request pair and restores it after controller restart", async () => {
  const f = fixture(false);
  const view = render(f.view());
  await waitScrollChoices(
    screen.getByRole("combobox", { name: "Agent Worker" }),
  );
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Agent Worker" }),
    f.otherAgent.id,
  );
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Runs on" }),
    f.otherMachine.id,
  );
  fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
    target: { value: "Create once" },
  });
  expect(f.bridge.update).not.toHaveBeenCalled();
  await submitCreation();
  await waitFor(() => expect(f.bridge.update).toHaveBeenCalledTimes(1));
  expect(f.bridge.update.mock.calls[0][1]).toEqual({
    agent_id: f.otherAgent.id,
    machine_id: f.otherMachine.id,
  });
  expect(f.createSession).toHaveBeenCalledTimes(1);
  view.unmount();
  render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.otherAgent.id),
  );
});
it.each(["failed", "unreadable"])(
  "does not save a %s creation",
  async (outcome) => {
    const f = fixture();
    const originalGet = f.get.getMockImplementation()!;
    let resolveRunner!: (value: { resource: Resource }) => void;
    const pendingRunner = new Promise<{ resource: Resource }>((resolve) => {
      resolveRunner = resolve;
    });
    f.get.mockImplementation((request) =>
      request.id === f.machine.id
        ? (pendingRunner as never)
        : originalGet(request),
    );
    f.createSession.mockImplementation(async () => {
      if (outcome === "failed")
        throw new ConnectError("Denied", Code.PermissionDenied);
      return { change: { session: undefined } } as never;
    });
    render(f.view());
    await waitFor(() =>
      expect(
        scrollChoiceValue(
          screen.getByRole("combobox", { name: "Agent Worker" }),
        ),
      ).toBe(f.agent.id),
    );
    expect(
      (
        screen.getByRole("button", {
          name: "Create session",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(f.createSession).not.toHaveBeenCalled();
    await act(async () => resolveRunner({ resource: f.machine }));
    await waitFor(() =>
      expect(
        scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
      ).toBe(f.machine.id),
    );
    fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
      target: { value: "No preference change" },
    });
    await submitCreation();
    await waitFor(() => expect(f.createSession).toHaveBeenCalledTimes(1));
    await screen.findByRole("alert");
    expect(f.bridge.update).not.toHaveBeenCalled();
  },
);
it("requires explicit reinspection after uncertain preference write without resending creation", async () => {
  const f = fixture();
  f.bridge.update.mockRejectedValueOnce(new Error("Lost preference response"));
  render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.agent.id),
  );
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id),
  );
  fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
    target: { value: "Accepted once" },
  });
  await submitCreation();
  await screen.findByRole("button", { name: "Inspect saved choices" });
  expect(
    screen.queryByRole("button", { name: "Retry saving accepted choices" }),
  ).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Inspect saved choices" }),
  );
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Retry saving accepted choices",
    }),
  );
  await waitFor(() => expect(f.bridge.update).toHaveBeenCalledTimes(2));
  expect(f.createSession).toHaveBeenCalledTimes(1);
});
it("rejects foreign scope and malformed preference responses", () => {
  const f = fixture();
  const valid: CreationPreferenceSnapshot = {
    revision: 1,
    scope: f.scope,
    pair: null,
    problem: null,
  };
  expect(parseCreationPreferences(valid)).toEqual(valid);
  for (const invalid of [
    { ...valid, revision: 0 },
    { ...valid, extra: true },
    { ...valid, scope: { server_id: "invalid", device_id: f.scope.device_id } },
    { ...valid, pair: { agent_id: f.agent.id, machine_id: "invalid" } },
    { ...valid, problem: "other" },
  ])
    expect(() => parseCreationPreferences(invalid)).toThrow();
  expect(
    parseCreationPreferences({
      ...valid,
      problem: CreationPreferenceProblem.Changed,
    }).problem,
  ).toBe(CreationPreferenceProblem.Changed);
});
it("keeps independent screen history and empty defaults when a kind has no record", async () => {
  const f = fixture();
  const view = render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.agent.id),
  );
  view.unmount();
  const chat = render(f.view(NewSessionKind.GeneralChat));
  await waitScrollChoices(
    screen.getByRole("combobox", { name: "Agent Worker" }),
  );
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
  ).toBe("");
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Agent Worker" }),
    f.otherAgent.id,
  );
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Runs on" }),
    f.otherMachine.id,
  );
  fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
    target: { value: "Independent conversation" },
  });
  await submitCreation("Start general chat");
  await waitFor(() => expect(f.bridge.update).toHaveBeenCalledTimes(1));
  expect(f.memory.get(NewSessionKind.Session)).toEqual({
    agent_id: f.agent.id,
    machine_id: f.machine.id,
  });
  chat.unmount();
  render(f.view(NewSessionKind.GeneralChat));
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.otherAgent.id),
  );
});
it("rejects disabled or missing exact-ID resources without deleting remembered history", async () => {
  const f = fixture();
  f.get.mockImplementation((request) =>
    request.kind === EntityKind.MACHINE
      ? {
          resource: create(ResourceSchema, {
            ...f.machine,
            documentJson: encode({ name: "Disabled runner", disabled: true }),
          }),
        }
      : { resource: undefined },
  );
  render(f.view());
  await waitFor(() => expect(f.get).toHaveBeenCalledTimes(2));
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
  ).toBe("");
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
  ).toBe("");
  expect(f.memory.get(NewSessionKind.Session)).toEqual({
    agent_id: f.agent.id,
    machine_id: f.machine.id,
  });
  expect(f.bridge.update).not.toHaveBeenCalled();
});
it("does not restore or save another server/device scope", async () => {
  const f = fixture();
  f.bridge.read.mockResolvedValue({
    revision: 1,
    scope: { server_id: newRequestId(), device_id: f.scope.device_id },
    pair: { agent_id: f.agent.id, machine_id: f.machine.id },
    problem: null,
  });
  render(f.view());
  await screen.findByRole("button", { name: "Inspect saved choices" });
  expect(f.get).not.toHaveBeenCalled();
  expect(f.bridge.update).not.toHaveBeenCalled();
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
  ).toBe("");
});
it("records the exact original pair only when an uncertain creation retry is acknowledged", async () => {
  const f = fixture();
  f.createSession.mockRejectedValueOnce(
    new ConnectError("Lost creation acknowledgment", Code.Unavailable),
  );
  render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.agent.id),
  );
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id),
  );
  fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
    target: { value: "Original request" },
  });
  await submitCreation();
  await screen.findByRole("button", {
    name: "Retry the same session creation",
  });
  expect(f.bridge.update).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "Retry the same session creation" }),
  );
  await waitFor(() => expect(f.bridge.update).toHaveBeenCalledTimes(1));
  expect(f.createSession).toHaveBeenCalledTimes(2);
  expect(f.createSession.mock.calls[0][0]).toEqual(
    f.createSession.mock.calls[1][0],
  );
  expect(f.bridge.update.mock.calls[0][1]).toEqual({
    agent_id: f.agent.id,
    machine_id: f.machine.id,
  });
});
it("keeps Local proof pinned above saved runner choices without additional proof reads", async () => {
  const f = fixture();
  const proof = vi.fn(async () => ({
    machineId: f.otherMachine.id,
    token: "A".repeat(43),
  }));
  render(f.view(NewSessionKind.Session, proof));
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id),
  );
  await chooseScrollOption(
    screen.getByRole("listbox", { name: "Project" }),
    f.projects[0].id,
  );
  fireEvent.click(screen.getByRole("button", { name: "Options" }));
  fireEvent.click(screen.getByRole("radio", { name: "Local" }));
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.otherMachine.id),
  );
  expect(
    (screen.getByRole("combobox", { name: "Runs on" }) as HTMLSelectElement)
      .disabled,
  ).toBe(true);
  expect(proof).toHaveBeenCalledTimes(1);
  expect(f.bridge.update).not.toHaveBeenCalled();
});
it("does not restore a runner missing the current project's Worktree clone capability", async () => {
  const f = fixture();
  f.projects[0].documentJson = encode({
    name: "Allowed project",
    repositories: [newRequestId()],
    agents: { configured: true, ids: [f.agent.id] },
  });
  render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id),
  );
  await chooseScrollOption(
    screen.getByRole("listbox", { name: "Project" }),
    f.projects[0].id,
  );
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.agent.id),
  );
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
  ).toBe("");
  expect(f.bridge.update).not.toHaveBeenCalled();
});

it("does not restore cached exact-ID resources after their current read fails", async () => {
  const f = fixture();
  const first = render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.agent.id),
  );
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
  ).toBe(f.machine.id);
  first.unmount();
  f.get.mockImplementation(() => {
    throw new ConnectError(
      "Current exact resource lookup failed",
      Code.Unavailable,
    );
  });
  render(f.view());
  await waitFor(() => expect(f.get).toHaveBeenCalledTimes(4));
  await screen.findByRole("alert");
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
  ).toBe("");
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
  ).toBe("");
  expect(f.bridge.update).not.toHaveBeenCalled();
});

it("keeps later accepted choices in memory until uncertain preference writes are explicitly reinspected", async () => {
  const f = fixture();
  f.bridge.update.mockRejectedValueOnce(
    new Error("Preference acknowledgment lost"),
  );
  render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.agent.id),
  );
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id),
  );
  fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
    target: { value: "First accepted" },
  });
  await submitCreation();
  await screen.findByRole("button", { name: "Inspect saved choices" });
  expect(f.bridge.update).toHaveBeenCalledTimes(1);
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Agent Worker" }),
    f.otherAgent.id,
  );
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Runs on" }),
    f.otherMachine.id,
  );
  fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
    target: { value: "Second accepted" },
  });
  await submitCreation();
  await waitFor(() =>
    expect(
      (
        screen.getByRole("textbox", {
          name: "First message",
        }) as HTMLTextAreaElement
      ).value,
    ).toBe(""),
  );
  expect(f.createSession).toHaveBeenCalledTimes(2);
  expect(f.bridge.update).toHaveBeenCalledTimes(1);
  fireEvent.click(
    screen.getByRole("button", { name: "Inspect saved choices" }),
  );
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Retry saving accepted choices",
    }),
  );
  await waitFor(() => expect(f.bridge.update).toHaveBeenCalledTimes(2));
  expect(f.bridge.update.mock.calls[1][1]).toEqual({
    agent_id: f.otherAgent.id,
    machine_id: f.otherMachine.id,
  });
  expect(f.createSession).toHaveBeenCalledTimes(2);
});

it.each(["disabled", "missing", "unsupported", "forbidden"])(
  "clears auto-restored choices after a settled %s exact read without changing history",
  async (outcome) => {
    const f = fixture();
    render(f.view());
    await waitFor(() =>
      expect(
        scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
      ).toBe(f.machine.id),
    );
    fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
      target: { value: "Keep this draft" },
    });
    f.get.mockImplementation((request) => {
      if (outcome === "forbidden")
        throw new ConnectError("Denied", Code.PermissionDenied);
      if (outcome === "missing") return { resource: undefined };
      const original = request.kind === EntityKind.AGENT ? f.agent : f.machine;
      return {
        resource: create(ResourceSchema, {
          ...original,
          ...(outcome === "unsupported" ? { schemaVersion: 999 } : {}),
          documentJson: encode({
            name: "Changed",
            disabled: outcome === "disabled",
          }),
        }),
      };
    });
    await act(async () => {
      await f.client.invalidateQueries();
    });
    await waitFor(() =>
      expect(
        scrollChoiceValue(
          screen.getByRole("combobox", { name: "Agent Worker" }),
        ),
      ).toBe(""),
    );
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe("");
    expect(
      (
        screen.getByRole("textbox", {
          name: "First message",
        }) as HTMLTextAreaElement
      ).value,
    ).toBe("Keep this draft");
    expect(
      (
        screen.getByRole("button", {
          name: "Create session",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(f.bridge.update).not.toHaveBeenCalled();
    expect(f.createSession).not.toHaveBeenCalled();
    expect(f.memory.get(NewSessionKind.Session)).toEqual({
      agent_id: f.agent.id,
      machine_id: f.machine.id,
    });
  },
);
it("revalidates the untouched automatic runner while preserving a subsequent manual Agent choice", async () => {
  const f = fixture();
  render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id),
  );
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Agent Worker" }),
    f.otherAgent.id,
  );
  f.machine.documentJson = encode({
    name: "Disconnected runner",
    disabled: true,
  });
  await act(async () => {
    await f.client.invalidateQueries();
  });
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(""),
  );
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
  ).toBe(f.otherAgent.id);
  expect(f.createSession).not.toHaveBeenCalled();
  expect(f.bridge.update).not.toHaveBeenCalled();
});
it("clears a restored runner when a current Worktree capability is lost", async () => {
  const f = fixture();
  f.projects[0].documentJson = encode({
    name: "Allowed project",
    repositories: [newRequestId()],
    agents: { configured: true, ids: [f.agent.id] },
  });
  f.machine.documentJson = encode({
    name: "Runner",
    worker_capabilities: ["remote-workspace-clone-v1"],
  });
  render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id),
  );
  await chooseScrollOption(
    screen.getByRole("listbox", { name: "Project" }),
    f.projects[0].id,
  );
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id),
  );
  f.machine.documentJson = encode({ name: "Runner", worker_capabilities: [] });
  await act(async () => {
    await f.client.invalidateQueries();
  });
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(""),
  );
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
  ).toBe(f.agent.id);
  expect(f.createSession).not.toHaveBeenCalled();
});
it("blocks new creation during automatic proof refresh without erasing the draft or replaying creation", async () => {
  const f = fixture();
  render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id),
  );
  fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
    target: { value: "Still editing" },
  });
  let resolve!: (value: { resource: Resource }) => void;
  const original = f.get.getMockImplementation()!;
  f.get.mockImplementation((request) =>
    request.id === f.agent.id
      ? (new Promise((done) => {
          resolve = done;
        }) as never)
      : original(request),
  );
  let refresh!: Promise<void>;
  act(() => {
    refresh = f.client.invalidateQueries();
  });
  await waitFor(() => expect(resolve).toBeTypeOf("function"));
  expect(
    (
      screen.getByRole("button", {
        name: "Create session",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
  ).toBe(f.agent.id);
  await act(async () => {
    resolve({ resource: f.agent });
    await refresh;
  });
  expect(
    (
      screen.getByRole("textbox", {
        name: "First message",
      }) as HTMLTextAreaElement
    ).value,
  ).toBe("Still editing");
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Create session",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  expect(f.createSession).not.toHaveBeenCalled();
});
it("does not clear a manual selection of the formerly remembered exact ID", async () => {
  const f = fixture(true, true);
  render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id),
  );
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Agent Worker" }),
    f.agent.id,
  );
  f.agent.documentJson = encode({ name: "Remembered agent", disabled: true });
  await act(async () => {
    await f.client.invalidateQueries();
  });
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
  ).toBe(f.agent.id);
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
  ).toBe(f.machine.id);
  expect(f.createSession).not.toHaveBeenCalled();
});
it("clears only the restored Agent after current project permissions change", async () => {
  const f = fixture();
  render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id),
  );
  await chooseScrollOption(
    screen.getByRole("listbox", { name: "Project" }),
    f.projects[0].id,
  );
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.agent.id),
  );
  f.projects[0].documentJson = encode({
    name: "Allowed project",
    repositories: [],
    agents: { configured: true, ids: [] },
  });
  await act(async () => {
    await f.client.invalidateQueries();
  });
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(""),
  );
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
  ).toBe(f.machine.id);
  expect(f.bridge.update).not.toHaveBeenCalled();
  expect(f.createSession).not.toHaveBeenCalled();
});

it.each(["initial", "current"])(
  "excludes a remembered Agent requiring reconfiguration in its %s exact read",
  async (phase) => {
    const f = fixture();
    if (phase === "initial")
      f.agent.documentJson = encode({
        name: "Remembered agent",
        reconfiguration_required: true,
      });
    render(f.view());
    await waitFor(() =>
      expect(
        scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
      ).toBe(f.machine.id),
    );
    if (phase === "current") {
      await waitFor(() =>
        expect(
          scrollChoiceValue(
            screen.getByRole("combobox", { name: "Agent Worker" }),
          ),
        ).toBe(f.agent.id),
      );
      f.agent.documentJson = encode({
        name: "Remembered agent",
        reconfiguration_required: true,
      });
      await act(async () => {
        await f.client.invalidateQueries();
      });
    }
    await waitFor(() =>
      expect(
        scrollChoiceValue(
          screen.getByRole("combobox", { name: "Agent Worker" }),
        ),
      ).toBe(""),
    );
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id);
    expect(f.memory.get(NewSessionKind.Session)).toEqual({
      agent_id: f.agent.id,
      machine_id: f.machine.id,
    });
    expect(f.bridge.update).not.toHaveBeenCalled();
    expect(f.createSession).not.toHaveBeenCalled();
  },
);

it.each([EntityKind.AGENT, EntityKind.MACHINE, EntityKind.PROJECT])(
  "retains automatic choices during a transient kind %s refresh and recovers current proof without replay",
  async (kind) => {
    const f = fixture();
    render(f.view());
    await waitFor(() =>
      expect(
        scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
      ).toBe(f.machine.id),
    );
    if (kind === EntityKind.PROJECT) {
      await chooseScrollOption(
        screen.getByRole("listbox", { name: "Project" }),
        f.projects[0].id,
      );
      await waitFor(() =>
        expect(
          scrollChoiceValue(
            screen.getByRole("combobox", { name: "Agent Worker" }),
          ),
        ).toBe(f.agent.id),
      );
    }
    fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
      target: { value: "Preserved draft" },
    });
    const original = f.get.getMockImplementation()!;
    f.get.mockImplementation((request) => {
      if (request.kind === kind)
        throw new ConnectError(
          "Temporary connection failure",
          Code.Unavailable,
        );
      return original(request);
    });
    await act(async () => {
      await f.client.invalidateQueries();
    });
    await screen.findAllByRole("alert");
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.agent.id);
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id);
    expect(
      (
        screen.getByRole("button", {
          name: "Create session",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    f.get.mockImplementation(original);
    await act(async () => {
      await f.client.invalidateQueries();
    });
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "Create session",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    expect(
      (
        screen.getByRole("textbox", {
          name: "First message",
        }) as HTMLTextAreaElement
      ).value,
    ).toBe("Preserved draft");
    expect(f.memory.get(NewSessionKind.Session)).toEqual({
      agent_id: f.agent.id,
      machine_id: f.machine.id,
    });
    expect(f.bridge.update).not.toHaveBeenCalled();
    expect(f.createSession).not.toHaveBeenCalled();
  },
);
it("retains automatic selections after a deadline failure but clears a subsequent exact not-found response", async () => {
  const f = fixture();
  render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id),
  );
  const original = f.get.getMockImplementation()!;
  f.get.mockImplementation((request) => {
    if (request.kind === EntityKind.AGENT)
      throw new ConnectError("Timed out", Code.DeadlineExceeded);
    return original(request);
  });
  await act(async () => {
    await f.client.invalidateQueries();
  });
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
  ).toBe(f.agent.id);
  f.get.mockImplementation((request) => {
    if (request.kind === EntityKind.AGENT)
      throw new ConnectError("Missing", Code.NotFound);
    return original(request);
  });
  await act(async () => {
    await f.client.invalidateQueries();
  });
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(""),
  );
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
  ).toBe(f.machine.id);
  expect(f.bridge.update).not.toHaveBeenCalled();
  expect(f.createSession).not.toHaveBeenCalled();
});
it("retains the full 256 KiB UTF-8 first-message budget and preserves the draft on overflow", async () => {
  const value = fixture();
  render(value.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(value.machine.id),
  );
  const input = screen.getByRole("textbox", { name: "First message" });
  const accepted = "a".repeat(256 << 10);
  fireEvent.change(input, { target: { value: accepted } });
  expect(input).toHaveProperty("value", accepted);
  fireEvent.change(input, { target: { value: accepted + "한" } });
  expect(input).toHaveProperty("value", accepted);
  await screen.findByRole("alert");
  fireEvent.change(input, { target: { value: "a".repeat(300) } });
  await submitCreation();
  await waitFor(() => expect(value.createSession).toHaveBeenCalledOnce());
  expect(
    JSON.parse(
      new TextDecoder().decode(
        value.createSession.mock.calls[0][0].documentJson,
      ),
    ).prompt,
  ).toBe("a".repeat(300));
});

it("resolves global Plan defaults and retains an explicit mode across project changes", async () => {
  const f = fixture(true, false, { global: true, project: "disabled" });
  render(f.view());
  const plan = screen.getByRole("checkbox", {
    name: "Plan Mode",
  }) as HTMLInputElement;
  await waitFor(() => expect(plan.checked).toBe(true));
  fireEvent.click(plan);
  expect(plan.checked).toBe(false);
  await chooseScrollOption(
    screen.getByRole("listbox", { name: "Project" }),
    f.projects[0].id,
  );
  await waitFor(() => expect(plan.checked).toBe(false));
  await f.client.invalidateQueries();
  expect(plan.checked).toBe(false);
});
it("follows project override until an explicit mode choice", async () => {
  const f = fixture(true, false, { global: true, project: "disabled" });
  render(f.view());
  const plan = screen.getByRole("checkbox", {
    name: "Plan Mode",
  }) as HTMLInputElement;
  await waitFor(() => expect(plan.checked).toBe(true));
  await chooseScrollOption(
    screen.getByRole("listbox", { name: "Project" }),
    f.projects[0].id,
  );
  await waitFor(() => expect(plan.checked).toBe(false));
});
it("does not submit a provisional default after a failed settings read", async () => {
  const f = fixture(true, false, { global: true, fail: true });
  render(f.view());
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.agent.id),
  );
  fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
    target: { value: "Synthetic input" },
  });
  const create = screen.getByRole("button", {
    name: "Create session",
  }) as HTMLButtonElement;
  expect(create.disabled).toBe(true);
  expect(f.createSession).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("checkbox", { name: "Plan Mode" }));
  await waitFor(() => expect(create.disabled).toBe(false));
});

async function readyAutomaticCreation(f: ReturnType<typeof fixture>) {
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
    ).toBe(f.machine.id),
  );
  fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
    target: { value: "Fresh defaults fixture" },
  });
  const button = screen.getByRole("button", {
    name: "Create session",
  }) as HTMLButtonElement;
  await waitFor(() => expect(button.disabled).toBe(false));
  return button;
}

function freshPlanSettings(f: ReturnType<typeof fixture>) {
  return {
    ...f.defaultSettings,
    revision: f.defaultSettings.revision + 1n,
    documentJson: encode({
      automatic_plan_approval: false,
      plan_mode_default: true,
      branch_prefix: "delidev/",
    }),
  };
}

function submittedMode(f: ReturnType<typeof fixture>) {
  return JSON.parse(
    new TextDecoder().decode(f.createSession.mock.calls[0][0].documentJson),
  ).mode;
}

it.each([NewSessionKind.Session, NewSessionKind.GeneralChat])(
  "refreshes a changed global default before untouched %s creation",
  async (kind) => {
    const f = fixture(true, false, { global: false });
    if (kind === NewSessionKind.GeneralChat)
      f.memory.set(kind, { agent_id: f.agent.id, machine_id: f.machine.id });
    render(f.view(kind));
    await waitFor(() =>
      expect(
        scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
      ).toBe(f.machine.id),
    );
    fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
      target: { value: "Changed in another client" },
    });
    const original = f.list.getMockImplementation()!;
    f.list.mockImplementation(async (request) =>
      request.filter?.kind === EntityKind.SETTINGS
        ? { resources: [freshPlanSettings(f)] }
        : original(request),
    );
    expect(screen.getByRole("checkbox", { name: "Plan Mode" })).toHaveProperty(
      "checked",
      false,
    );
    await submitCreation(
      kind === NewSessionKind.GeneralChat
        ? "Start general chat"
        : "Create session",
    );
    await waitFor(() => expect(f.createSession).toHaveBeenCalledOnce());
    expect(submittedMode(f)).toBe("plan");
  },
);

it("refreshes the exact selected project override before automatic creation", async () => {
  const f = fixture(true, false, { global: false, project: "inherit" });
  render(f.view());
  await chooseScrollOption(
    screen.getByRole("listbox", { name: "Project" }),
    f.projects[0].id,
  );
  const button = await readyAutomaticCreation(f);
  const original = f.get.getMockImplementation()!;
  f.get.mockImplementation((request) =>
    request.id === f.projects[0].id
      ? {
          resource: {
            ...f.projects[0],
            revision: 2n,
            documentJson: encode({
              name: "Allowed project",
              repositories: [],
              agents: { configured: true, ids: [f.agent.id] },
              settings: { plan_mode_default: "enabled" },
            }),
          },
        }
      : original(request),
  );
  fireEvent.click(button);
  await waitFor(() => expect(f.createSession).toHaveBeenCalledOnce());
  expect(submittedMode(f)).toBe("plan");
});

it.each([
  "missing",
  "foreign",
  "older",
  "malformed",
  "schema",
  "continued",
  "multiple",
  "failed",
])("blocks automatic creation when fresh settings are %s", async (problem) => {
  const f = fixture(true, false, { global: false });
  f.defaultSettings.revision = 3n;
  render(f.view());
  const button = await readyAutomaticCreation(f);
  const original = f.list.getMockImplementation()!;
  f.list.mockImplementation(async (request) => {
    if (request.filter?.kind !== EntityKind.SETTINGS) return original(request);
    if (problem === "failed")
      throw new ConnectError("Unavailable", Code.Unavailable);
    const row = freshPlanSettings(f);
    if (problem === "foreign") row.id = newRequestId();
    if (problem === "older") row.revision = 2n;
    if (problem === "malformed")
      row.documentJson = encode({ plan_mode_default: "true" });
    if (problem === "schema") row.schemaVersion = 99;
    return {
      resources:
        problem === "missing"
          ? []
          : problem === "multiple"
            ? [row, row]
            : [row],
      nextPageToken: problem === "continued" ? "more" : "",
    };
  });
  fireEvent.click(button);
  await screen.findByRole("button", { name: "Retry Plan Mode defaults" });
  expect(f.createSession).not.toHaveBeenCalled();
  expect(screen.getByRole("textbox", { name: "First message" })).toHaveProperty(
    "value",
    "Fresh defaults fixture",
  );
});

it.each(["missing", "foreign", "older", "malformed"])(
  "blocks automatic creation when the fresh selected project is %s",
  async (problem) => {
    const f = fixture(true, false, { global: false, project: "inherit" });
    f.projects[0].revision = 3n;
    render(f.view());
    await chooseScrollOption(
      screen.getByRole("listbox", { name: "Project" }),
      f.projects[0].id,
    );
    const button = await readyAutomaticCreation(f);
    const original = f.get.getMockImplementation()!;
    f.get.mockImplementation((request) => {
      if (request.id !== f.projects[0].id) return original(request);
      const row = { ...f.projects[0] };
      if (problem === "foreign") row.id = newRequestId();
      if (problem === "older") row.revision = 2n;
      if (problem === "malformed")
        row.documentJson = encode({
          settings: { plan_mode_default: "invalid" },
        });
      return { resource: problem === "missing" ? undefined : row };
    });
    fireEvent.click(button);
    await screen.findByRole("button", { name: "Retry Plan Mode defaults" });
    expect(f.createSession).not.toHaveBeenCalled();
  },
);

it("keeps the highest mounted defaults revision after a lower refresh", async () => {
  const f = fixture(true, false, { global: false });
  f.defaultSettings.revision = 5n;
  render(f.view());
  await readyAutomaticCreation(f);
  const original = f.list.getMockImplementation()!;
  f.list.mockImplementation(async (request) =>
    request.filter?.kind === EntityKind.SETTINGS
      ? { resources: [{ ...f.defaultSettings, revision: 4n }] }
      : original(request),
  );
  await act(async () => {
    await f.client.invalidateQueries();
  });

  fireEvent.click(await readyAutomaticCreation(f));
  await screen.findByRole("button", { name: "Retry Plan Mode defaults" });
  expect(f.createSession).not.toHaveBeenCalled();
});

it("blocks automatic creation after the mounted Settings identity changes", async () => {
  const f = fixture(true, false, { global: false });
  render(f.view());
  await readyAutomaticCreation(f);

  const replacement = { ...f.defaultSettings, id: newRequestId() };
  const original = f.list.getMockImplementation()!;
  f.list.mockImplementation(async (request) =>
    request.filter?.kind === EntityKind.SETTINGS
      ? { resources: [replacement] }
      : original(request),
  );
  await act(async () => {
    await f.client.invalidateQueries();
  });

  fireEvent.click(await readyAutomaticCreation(f));
  await screen.findByRole("button", { name: "Retry Plan Mode defaults" });
  expect(f.createSession).not.toHaveBeenCalled();
});

it("keeps manual Execute without rereading defaults", async () => {
  const f = fixture(true, false, { global: false });
  render(f.view());
  const button = await readyAutomaticCreation(f);
  const checkbox = screen.getByRole("checkbox", { name: "Plan Mode" });
  fireEvent.click(checkbox);
  fireEvent.click(checkbox);
  f.list.mockRejectedValue(
    new ConnectError("Changed elsewhere", Code.Unavailable),
  );
  fireEvent.click(button);
  await waitFor(() => expect(f.createSession).toHaveBeenCalledOnce());
  expect(submittedMode(f)).toBe("execute");
});

it("clears a prior automatic-default failure after accepted manual creation", async () => {
  const f = fixture(true, false, { global: false });
  render(f.view());
  const button = await readyAutomaticCreation(f);
  f.list.mockRejectedValue(
    new ConnectError("Defaults temporarily unavailable", Code.Unavailable),
  );
  fireEvent.click(button);
  await screen.findByRole("button", { name: "Retry Plan Mode defaults" });
  fireEvent.click(screen.getByRole("checkbox", { name: "Plan Mode" }));
  fireEvent.click(button);

  await waitFor(() => expect(f.createSession).toHaveBeenCalledOnce());
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Retry Plan Mode defaults" }),
    ).toBeNull(),
  );
});

it("clears an automatic-default failure when the creation scope changes", async () => {
  const f = fixture(true, false, { global: false, project: "inherit" });
  render(f.view());
  const button = await readyAutomaticCreation(f);
  f.list.mockRejectedValue(
    new ConnectError("Defaults temporarily unavailable", Code.Unavailable),
  );
  fireEvent.click(button);
  await screen.findByRole("button", { name: "Retry Plan Mode defaults" });

  await chooseScrollOption(
    screen.getByRole("listbox", { name: "Project" }),
    f.projects[0].id,
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Retry Plan Mode defaults" }),
    ).toBeNull(),
  );
});

it.each(["current", "departure", "connection"])(
  "serializes automatic preflight and fences its scope (%s)",
  async (scope) => {
    const f = fixture(true, false, { global: false });
    const view = render(f.view());
    const button = await readyAutomaticCreation(f);
    let release!: () => void;
    const pending = new Promise<void>((resolve) => {
      release = resolve;
    });
    const original = f.list.getMockImplementation()!;
    f.list.mockImplementation(async (request) => {
      if (request.filter?.kind !== EntityKind.SETTINGS)
        return original(request);
      await pending;
      return { resources: [freshPlanSettings(f)] };
    });
    const before = f.list.mock.calls.length;
    fireEvent.submit(button.closest("form")!);
    fireEvent.submit(button.closest("form")!);
    await waitFor(() => expect(f.list.mock.calls.length).toBe(before + 1));
    expect(
      screen
        .getByRole("textbox", { name: "First message" })
        .closest("fieldset"),
    ).toHaveProperty("disabled", true);
    if (scope === "departure") {
      view.rerender(f.view(NewSessionKind.Session, undefined, false));
      view.rerender(f.view());
    } else if (scope === "connection") {
      view.rerender(
        f.view(NewSessionKind.Session, undefined, true, {
          ...f.scope,
          server_id: newRequestId(),
        }),
      );
    }
    await act(async () => {
      release();
      await pending;
    });
    if (scope !== "current") {
      await waitFor(() =>
        expect(
          screen
            .getByRole("textbox", { name: "First message" })
            .closest("fieldset"),
        ).toHaveProperty("disabled", false),
      );
      expect(f.createSession).not.toHaveBeenCalled();
    } else {
      await waitFor(() => expect(f.createSession).toHaveBeenCalledOnce());
    }
  },
);

it("keeps a replacement submission locked when the old scope settles", async () => {
  const f = fixture(true, false, { global: false });
  const view = render(f.view());
  const button = await readyAutomaticCreation(f);
  let releaseFirst!: () => void;
  let releaseSecond!: () => void;
  const first = new Promise<void>((resolve) => {
    releaseFirst = resolve;
  });
  const second = new Promise<void>((resolve) => {
    releaseSecond = resolve;
  });
  let settingsReads = 0;
  const original = f.list.getMockImplementation()!;
  f.list.mockImplementation(async (request) => {
    if (request.filter?.kind !== EntityKind.SETTINGS) return original(request);
    const currentRead = settingsReads++;
    await (currentRead === 0 ? first : second);
    return { resources: [freshPlanSettings(f)] };
  });

  fireEvent.submit(button.closest("form")!);
  await waitFor(() => expect(settingsReads).toBe(1));
  view.rerender(
    f.view(NewSessionKind.Session, undefined, true, {
      ...f.scope,
      server_id: newRequestId(),
    }),
  );
  expect(
    screen.getByRole("textbox", { name: "First message" }).closest("fieldset"),
  ).toHaveProperty("disabled", false);

  fireEvent.submit(
    screen.getByRole("button", { name: "Create session" }).closest("form")!,
  );
  await waitFor(() => expect(settingsReads).toBe(2));
  expect(
    screen.getByRole("textbox", { name: "First message" }).closest("fieldset"),
  ).toHaveProperty("disabled", true);

  await act(async () => {
    releaseFirst();
    await first;
  });
  expect(
    screen.getByRole("textbox", { name: "First message" }).closest("fieldset"),
  ).toHaveProperty("disabled", true);

  await act(async () => {
    releaseSecond();
    await second;
  });
  await waitFor(() => expect(f.createSession).toHaveBeenCalledOnce());
});

it("retries the exact frozen automatic request without another default read", async () => {
  const f = fixture(true, false, { global: false });
  f.createSession.mockRejectedValueOnce(
    new ConnectError("Lost acknowledgment", Code.Unavailable),
  );
  render(f.view());
  const button = await readyAutomaticCreation(f);
  const original = f.list.getMockImplementation()!;
  f.list.mockImplementation(async (request) =>
    request.filter?.kind === EntityKind.SETTINGS
      ? { resources: [freshPlanSettings(f)] }
      : original(request),
  );
  fireEvent.click(button);
  const retry = await screen.findByRole("button", {
    name: "Retry the same session creation",
  });
  f.list.mockRejectedValue(
    new ConnectError("Defaults now unavailable", Code.Unavailable),
  );
  fireEvent.click(retry);
  await waitFor(() => expect(f.createSession).toHaveBeenCalledTimes(2));
  expect(f.createSession.mock.calls[1][0]).toEqual(
    f.createSession.mock.calls[0][0],
  );
  expect(submittedMode(f)).toBe("plan");
});

it("allows an authoritative empty Settings singleton when none was previously observed", async () => {
  const f = fixture(true, false, { global: false });
  const original = f.list.getMockImplementation()!;
  f.list.mockImplementation(async (request) =>
    request.filter?.kind === EntityKind.SETTINGS
      ? { resources: [] }
      : original(request),
  );
  render(f.view());
  fireEvent.click(await readyAutomaticCreation(f));
  await waitFor(() => expect(f.createSession).toHaveBeenCalledOnce());
  expect(submittedMode(f)).toBe("execute");
});
