// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { webcrypto } from "node:crypto";
import { afterEach, expect, it, vi } from "vitest";
import {
  AttachmentService,
  EntityKind,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemCapability,
  SystemService,
  WorkerCapability,
  newRequestId,
  type CreateSessionRequest,
  type Resource,
} from "@delinoio/delidev-api-client";
import { scrollChoiceValue } from "./test-scroll-picker";
import { NewSession, NewSessionKind } from "./new-session";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";

function resource(kind: EntityKind, modalities: string[]): Resource {
  return create(ResourceSchema, {
    kind,
    id: newRequestId(),
    revision: 1n,
    schemaVersion: kind === EntityKind.AGENT ? 4 : 1,
    documentJson: encode(
      kind === EntityKind.AGENT
        ? {
            name: "Codex agent",
            harness: "codex",
            routes: [
              {
                model: {
                  provider_id: newRequestId(),
                  native_id: "fixture-native-model",
                  input_modalities: modalities,
                  metadata_source: "unknown",
                },
                accounts: [{ id: newRequestId(), weight: 1 }],
              },
            ],
            templates: [],
            options: { permission: "default" },
          }
        : {
            name: "Runner",
            disabled: false,
            worker_capabilities: [WorkerCapability.IMAGE_INPUTS_V1],
          },
    ),
  });
}

function fixture(modalities: string[]) {
  const agent = resource(EntityKind.AGENT, modalities),
    machine = resource(EntityKind.MACHINE, []);
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
    change: { session: resource(EntityKind.AGENT, []) },
  }));
  const beginUpload = vi.fn();
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
          SystemCapability.IMAGE_INPUTS_V1,
          SystemCapability.AUTOMATIC_TITLES_V1,
        ],
      }),
    });
    router.service(AttachmentService, { beginUpload });
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
            kind={NewSessionKind.Session}
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
  return { agent, createSession, beginUpload, view };
}

async function pasteImage(f: ReturnType<typeof fixture>) {
  vi.stubGlobal("crypto", webcrypto);
  vi.stubGlobal("createImageBitmap", async () => ({
    width: 1,
    height: 1,
    close: () => {},
  }));
  const BaseURL = URL;
  vi.stubGlobal(
    "URL",
    class extends BaseURL {
      static createObjectURL = vi.fn(() => "blob:codex-image");
      static revokeObjectURL = vi.fn();
    },
  );
  const bytes = Uint8Array.from(
    Buffer.from(
      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a9d8AAAAASUVORK5CYII=",
      "base64",
    ),
  );
  const file = new File([bytes], "fixture.png", { type: "image/png" });
  Object.defineProperty(file, "arrayBuffer", {
    value: async () => bytes.buffer,
  });
  render(f.view);
  await waitFor(() =>
    expect(
      scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
    ).toBe(f.agent.id),
  );
  const input = screen.getByRole("textbox", {
    name: "First message",
  }) as HTMLTextAreaElement;
  fireEvent.change(input, { target: { value: "describe" } });
  fireEvent.paste(input, { clipboardData: { files: [file] } });
  await screen.findByRole("img", { name: "Image 1" });
}

afterEach(() => vi.unstubAllGlobals());

it("keeps a text-only Codex image draft unsent without attachment calls and restores text submission", async () => {
  const f = fixture(["text"]);
  await pasteImage(f);
  await new Promise((done) => setTimeout(done, 200));
  const submit = screen.getByRole("button", { name: "Create session" });
  expect(submit).toHaveProperty("disabled", true);
  fireEvent.click(submit);
  expect(f.createSession).not.toHaveBeenCalled();
  expect(f.beginUpload).not.toHaveBeenCalled();
  expect(screen.getByRole("img", { name: "Image 1" })).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: /Remove image 1/i }));
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Create session" }),
    ).toHaveProperty("disabled", false),
  );
  fireEvent.click(screen.getByRole("button", { name: "Create session" }));
  await waitFor(() => expect(f.createSession).toHaveBeenCalledOnce());
  expect(f.createSession.mock.calls[0]![0].attachments).toHaveLength(0);
  expect(f.beginUpload).not.toHaveBeenCalled();
});

it("enables submission for a Codex agent whose every route model declares image input", async () => {
  const f = fixture(["text", "image"]);
  await pasteImage(f);
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Create session" }),
    ).toHaveProperty("disabled", false),
  );
});
