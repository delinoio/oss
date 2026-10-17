// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  SessionService,
  SkillProvenance,
  SkillService,
  SystemCapability,
  SystemService,
  newRequestId,
  type CreateSessionRequest,
  type Resource,
} from "@delinoio/delidev-api-client";
import { scrollChoiceValue } from "./test-scroll-picker";
import { NewSession, NewSessionKind } from "./new-session";
import {
  SessionSubmissionsProvider,
  useSessionSubmissions,
} from "./session-submissions";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";

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
function Probe() {
  const { rows } = useSessionSubmissions();
  return (
    <output data-testid="rows">
      {JSON.stringify(
        rows.map((row) => ({ original: row.original, prompt: row.prompt })),
      )}
    </output>
  );
}
/** A read held open until the test releases it, so ordering never depends on timing. */
function gate() {
  let release!: () => void;
  let enter!: () => void;
  const released = new Promise<void>((resolve) => (release = resolve));
  const entered = new Promise<void>((resolve) => (enter = resolve));
  return {
    entered,
    release,
    hold: async () => {
      enter();
      await released;
    },
  };
}
type Gate = ReturnType<typeof gate>;
interface Gates {
  machine?: Gate;
  status?: Gate;
}
function fixture(
  kind: NewSessionKind,
  omitInitialInput = false,
  gates: Gates = {},
) {
  const agent = resource(EntityKind.AGENT, "Remembered agent"),
    machine = resource(EntityKind.MACHINE, "Runner", { disabled: false });
  const selection = {
    $typeName: "delidev.v1.SkillSelection" as const,
    inventoryId: newRequestId(),
    workerDeviceId: newRequestId(),
    skillId: newRequestId(),
    contentRevision: "a".repeat(64),
  };
  const scope = { server_id: newRequestId(), device_id: newRequestId() };
  const bridge = {
    read: vi.fn(async (): Promise<unknown> => ({
      revision: 1,
      scope,
      pair: { agent_id: agent.id, machine_id: machine.id },
      problem: null,
    })),
    update: vi.fn(),
  };
  const createSession = vi.fn(async (_request: CreateSessionRequest) => {
    const inputId = newRequestId();
    const session = resource(
      EntityKind.SESSION,
      "Accepted",
      omitInitialInput ? {} : { initial_execution: { input_id: inputId } },
    );
    session.id = newRequestId();
    const input = create(ResourceSchema, {
      id: inputId,
      sessionId: session.id,
      kind: EntityKind.QUEUE,
      schemaVersion: 1,
      revision: 1n,
      documentJson: encode({
        prompt: "Created first prompt",
        mode: "execute",
        delivery: "queued",
        sequence: 1,
      }),
    });
    return { change: { session, input } };
  });
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {
      getResource: async (request) => {
        if (request.kind === EntityKind.MACHINE) await gates.machine?.hold();
        return {
          resource: [agent, machine].find(
            (row) => row.id === request.id && row.kind === request.kind,
          ),
        };
      },
      listResources: (request) => ({
        resources:
          request.filter?.kind === EntityKind.AGENT
            ? [agent]
            : request.filter?.kind === EntityKind.MACHINE
              ? [machine]
              : [],
      }),
    });
    router.service(SystemService, {
      getStatus: async () => {
        await gates.status?.hold();
        return {
          capabilities: [
            SystemCapability.AUTOMATIC_TITLES_V1,
            SystemCapability.NATIVE_SKILLS_V1,
          ],
        };
      },
    });
    router.service(SkillService, {
      listSkills: () => ({
        skills: [
          {
            name: "add-issue",
            description: "Fixture skill",
            provenance: SkillProvenance.USER,
            selection,
          },
        ],
      }),
    });
    router.service(SessionService, { createSession });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const view = (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <SessionSubmissionsProvider>
            <Probe />
            <NewSession
              kind={kind}
              active
              ownsActivation
              activation={1}
              back={() => {}}
              openSettings={() => {}}
              open={() => {}}
              created={() => {}}
              preferenceBridge={bridge}
              preferenceScope={scope}
            />
          </SessionSubmissionsProvider>
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  return { agent, machine, selection, createSession, view };
}

/**
 * Wait for both remembered choices. Typing marks the form as touched, which stops
 * any restoration still pending, so input must follow restoration.
 */
async function awaitRestoredChoices(agentId: string, machineId: string) {
  await waitFor(
    () => {
      expect(
        scrollChoiceValue(
          screen.getByRole("combobox", { name: "Agent Worker" }),
        ),
      ).toBe(agentId);
      expect(
        scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
      ).toBe(machineId);
    },
    { timeout: 4000 },
  );
}
/** Wait until creation is actually allowed instead of clicking a disabled button. */
async function awaitSubmitEnabled(label: string) {
  return waitFor(
    () => {
      const button = screen.getByRole("button", {
        name: label,
      }) as HTMLButtonElement;
      expect(button.disabled).toBe(false);
      return button;
    },
    { timeout: 4000 },
  );
}
function submitButton(label: string) {
  return screen.getByRole("button", { name: label }) as HTMLButtonElement;
}

it.each([
  [NewSessionKind.Session, "Create session"],
  [NewSessionKind.GeneralChat, "Start general chat"],
])(
  "registers the verified original input after accepted %s creation",
  async (kind, label) => {
    const f = fixture(kind);
    render(f.view);
    await awaitRestoredChoices(f.agent.id, f.machine.id);
    fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
      target: { value: "Created first prompt" },
    });
    fireEvent.click(await awaitSubmitEnabled(label));
    await waitFor(
      () =>
        expect(screen.getByTestId("rows").textContent).toContain(
          "Created first prompt",
        ),
      { timeout: 4000 },
    );
    expect(JSON.parse(screen.getByTestId("rows").textContent!)).toEqual([
      { original: true, prompt: "Created first prompt" },
    ]);
    expect(f.createSession).toHaveBeenCalledOnce();
  },
);

it("does not register an original input when the session omits its initial input identity", async () => {
  const f = fixture(NewSessionKind.Session, true);
  render(f.view);
  await awaitRestoredChoices(f.agent.id, f.machine.id);
  const box = screen.getByRole("textbox", { name: "First message" });
  fireEvent.change(box, { target: { value: "Created first prompt" } });
  fireEvent.click(await awaitSubmitEnabled("Create session"));
  await waitFor(() => expect((box as HTMLTextAreaElement).value).toBe(""), {
    timeout: 4000,
  });
  expect(f.createSession).toHaveBeenCalledOnce();
  expect(JSON.parse(screen.getByTestId("rows").textContent!)).toEqual([]);
});

it("keeps creation disabled until the delayed machine restoration completes", async () => {
  const machine = gate();
  const f = fixture(NewSessionKind.Session, false, { machine });
  render(f.view);
  await waitFor(
    () =>
      expect(
        scrollChoiceValue(
          screen.getByRole("combobox", { name: "Agent Worker" }),
        ),
      ).toBe(f.agent.id),
    { timeout: 4000 },
  );
  await machine.entered;
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Runs on" })),
  ).toBe("");
  expect(submitButton("Create session").disabled).toBe(true);
  expect(f.createSession).not.toHaveBeenCalled();
  machine.release();
  await awaitRestoredChoices(f.agent.id, f.machine.id);
  fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
    target: { value: "Created first prompt" },
  });
  fireEvent.click(await awaitSubmitEnabled("Create session"));
  await waitFor(
    () =>
      expect(screen.getByTestId("rows").textContent).toContain(
        "Created first prompt",
      ),
    { timeout: 4000 },
  );
  expect(JSON.parse(screen.getByTestId("rows").textContent!)).toEqual([
    { original: true, prompt: "Created first prompt" },
  ]);
  expect(f.createSession).toHaveBeenCalledOnce();
});

it("keeps creation disabled until the delayed status capabilities arrive", async () => {
  const status = gate();
  const f = fixture(NewSessionKind.Session, false, { status });
  render(f.view);
  await awaitRestoredChoices(f.agent.id, f.machine.id);
  await status.entered;
  fireEvent.change(screen.getByRole("textbox", { name: "First message" }), {
    target: { value: "Created first prompt" },
  });
  expect(submitButton("Create session").disabled).toBe(true);
  expect(f.createSession).not.toHaveBeenCalled();
  status.release();
  fireEvent.click(await awaitSubmitEnabled("Create session"));
  await waitFor(
    () =>
      expect(screen.getByTestId("rows").textContent).toContain(
        "Created first prompt",
      ),
    { timeout: 4000 },
  );
  expect(JSON.parse(screen.getByTestId("rows").textContent!)).toEqual([
    { original: true, prompt: "Created first prompt" },
  ]);
  expect(f.createSession).toHaveBeenCalledOnce();
});
