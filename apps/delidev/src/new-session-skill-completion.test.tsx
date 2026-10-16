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
function fixture(kind: NewSessionKind) {
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
  const createSession = vi.fn(async (_request: CreateSessionRequest) => ({
    change: { session: resource(EntityKind.SESSION, "Accepted") },
  }));
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {
      getResource: (request) => ({
        resource: [agent, machine].find(
          (row) => row.id === request.id && row.kind === request.kind,
        ),
      }),
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
      getStatus: () => ({
        capabilities: [
          SystemCapability.AUTOMATIC_TITLES_V1,
          SystemCapability.NATIVE_SKILLS_V1,
        ],
      }),
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
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  return { agent, machine, selection, createSession, view };
}

it.each([NewSessionKind.Session, NewSessionKind.GeneralChat])(
  "completes a skill with a trailing separator in %s creation without creating a session",
  async (kind) => {
    const f = fixture(kind);
    render(f.view);
    await waitFor(() =>
      expect(
        scrollChoiceValue(
          screen.getByRole("combobox", { name: "Agent Worker" }),
        ),
      ).toBe(f.agent.id),
    );
    const input = screen.getByRole("textbox", {
      name: "First message",
    }) as HTMLTextAreaElement;
    input.focus();
    fireEvent.change(input, {
      target: { value: "$add-iss", selectionStart: 8 },
    });
    fireEvent.click(await screen.findByRole("option", { name: /add-issue/ }));
    expect(input.value).toBe("$add-issue ");
    await waitFor(() => expect(input.selectionStart).toBe(11));
    expect(input.selectionEnd).toBe(11);
    expect(document.activeElement).toBe(input);
    expect(
      screen.queryByRole("listbox", { name: "Available skills" }),
    ).toBeNull();
    expect(f.createSession).not.toHaveBeenCalled();
  },
);

it("refuses completion atomically when only the separator would exceed the 256 KiB prompt limit", async () => {
  const f = fixture(NewSessionKind.Session);
  const view = render(f.view);
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.agent.id),
  );
  const input = screen.getByRole("textbox", {
    name: "First message",
  }) as HTMLTextAreaElement;
  // The completed name alone fills the limit exactly; the separator exceeds it.
  const draft = `${"a".repeat((256 << 10) - 2 - 9)} $add-iss`;
  expect(new TextEncoder().encode(draft).byteLength).toBe((256 << 10) - 2);
  input.focus();
  fireEvent.change(input, {
    target: { value: draft, selectionStart: draft.length },
  });
  const option = await screen.findByRole("option", { name: /add-issue/ });
  input.setSelectionRange(5, 7);
  fireEvent.click(option);
  await Promise.resolve();
  expect(input.value).toBe(draft);
  expect([input.selectionStart, input.selectionEnd]).toEqual([5, 7]);
  expect(document.activeElement).toBe(input);
  expect(view.container.querySelector(".skill-token-selected")).toBeNull();
  expect(
    screen.queryByRole("listbox", { name: "Available skills" }),
  ).not.toBeNull();
  expect(f.createSession).not.toHaveBeenCalled();
});

it.each([NewSessionKind.Session, NewSessionKind.GeneralChat])(
  "converts a pasted URL to a collapsed Markdown link in %s creation",
  async (kind) => {
    const f = fixture(kind);
    render(f.view);
    await waitFor(() =>
      expect(
        scrollChoiceValue(
          screen.getByRole("combobox", { name: "Agent Worker" }),
        ),
      ).toBe(f.agent.id),
    );
    const input = screen.getByRole("textbox", {
      name: "First message",
    }) as HTMLTextAreaElement;
    const url = "https://example.com/docs";
    fireEvent.paste(input, {
      clipboardData: { getData: () => url, files: [] },
    });
    expect(input.value).toBe(url);
    expect(document.querySelector(".prompt-markdown-link")?.textContent).toBe(
      url,
    );
    input.setSelectionRange(5, 5);
    fireEvent.select(input);
    expect(input.value).toBe(`[${url}](${url})`);
    expect(f.createSession).not.toHaveBeenCalled();
  },
);
