import { useState } from "react";
import { useConversationDrafts } from "./conversation-drafts";
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
import { afterEach, expect, it, vi } from "vitest";
import { danger, expectTone } from "./test-action-tone";
import {
  EntityKind,
  ResourceSchema,
  SessionService,
  SkillProvenance,
  SkillService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { QueuedInput, type QueuedInputDraft } from "./queue";
import { MutationIntents } from "./mutation";
import { encode, document as readDocument } from "./documents";

function fixture(prompt = "Original queued input") {
  const id = newRequestId(),
    machineId = newRequestId(),
    agentId = newRequestId(),
    execution = newRequestId(),
    turn = newRequestId();
  const session = create(ResourceSchema, {
    id,
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 4n,
    documentJson: encode({
      machine_id: machineId,
      agent_id: agentId,
      outcome: "running",
      archive: "active",
      active_execution_id: execution,
      execution: { execution_id: execution, native_turn_id: turn },
    }),
  });
  const resource = create(ResourceSchema, {
    id: newRequestId(),
    sessionId: id,
    kind: EntityKind.QUEUE,
    schemaVersion: 1,
    revision: 6n,
    documentJson: encode({
      prompt,
      sequence: 3,
      delivery: "queued",
      mode: "plan",
    }),
  });
  const edit = vi.fn(async (_request: unknown) => ({
    change: {
      input: create(ResourceSchema, {
        ...resource,
        revision: 7n,
        documentJson: encode({
          prompt: "Changed",
          sequence: 3,
          delivery: "queued",
          mode: "plan",
        }),
      }),
    },
  }));
  const steer = vi.fn(async (_request: unknown) => ({}));
  const remove = vi.fn(async (_request: unknown) => ({}));
  const transport = createRouterTransport((router) => {
    router.service(SessionService, {
      editQueuedInput: edit,
      steerQueuedInput: steer,
      removeQueuedInput: remove,
    });
    router.service(SkillService, {
      listSkills: async () => ({
        skills: [
          {
            name: "available",
            description: "Available fixture skill",
            provenance: SkillProvenance.USER,
            selection: {
              $typeName: "delidev.v1.SkillSelection" as const,
              inventoryId: newRequestId(),
              workerDeviceId: newRequestId(),
              skillId: newRequestId(),
              contentRevision: "a".repeat(64),
            },
          },
        ],
      }),
    });
  });
  const client = new QueryClient();
  const view = (input = resource, compact = false, readOnly = false) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <QueuedInput
            readOnly={readOnly}
            compact={compact}
            resource={input}
            session={session}
            refresh={() => {}}
          />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  return {
    resource,
    session,
    execution,
    turn,
    edit,
    steer,
    remove,
    view,
    transport,
    client,
  };
}

it("keeps the queued editor label stable when unavailable tokens are announced", async () => {
  const value = fixture();
  const view = render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  const editor = screen.getByRole("textbox", { name: "Edited input" });
  fireEvent.change(editor, {
    target: { value: "$missing ", selectionStart: 9 },
  });
  await waitFor(() =>
    expect(
      screen.getByRole("textbox", {
        name: "Edited input",
        description: "1 skill token is unavailable in this scope.",
      }),
    ).toBe(editor),
  );
  expect(view.container.querySelector('[role="status"]')?.textContent).toBe(
    "1 skill token is unavailable in this scope.",
  );
});

it.each([
  { name: "100,000-byte queued ASCII prompt", prompt: "a".repeat(100000) },
  { name: "65,537-byte ASCII prompt", prompt: "a".repeat(65537) },
  { name: "256 KiB ASCII prompt", prompt: "a".repeat(256 << 10) },
  { name: "256 KiB supplementary Unicode prompt", prompt: "😀".repeat(65536) },
])("edits the complete $name at its original revision", async ({ prompt }) => {
  const value = fixture(prompt);
  render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  const editor = screen.getByRole("textbox", { name: "Edited input" });
  // jsdom does not enforce native maxlength during typing. Check the actual
  // textarea attribute as well as the controlled draft and transmitted bytes.
  expect(editor.hasAttribute("maxlength")).toBe(false);
  expect(editor).toHaveProperty("value", prompt);
  const edited = prompt.endsWith("😀")
    ? `${prompt.slice(0, -2)}😃`
    : `${prompt.slice(0, -1)}b`;
  fireEvent.change(editor, {
    target: { value: edited, selectionStart: edited.length },
  });
  expect(editor).toHaveProperty("value", edited);
  fireEvent.click(screen.getByRole("button", { name: "Save input" }));
  await waitFor(() => expect(value.edit).toHaveBeenCalledOnce());
  expect(value.edit.mock.calls[0]![0]).toMatchObject({
    prompt: edited,
    mutation: {
      id: value.resource.id,
      expectedRevision: value.resource.revision,
    },
    sessionId: value.session.id,
  });
  expect(new TextEncoder().encode(edited)).toHaveLength(
    new TextEncoder().encode(prompt).byteLength,
  );
});

it.each([
  {
    name: "ASCII",
    valid: "a".repeat(256 << 10),
    overflow: "a".repeat((256 << 10) + 1),
  },
  {
    name: "supplementary Unicode",
    valid: "😀".repeat(65536),
    overflow: `${"😀".repeat(65536)}a`,
  },
])(
  "retains the last valid $name edit on UTF-8 overflow",
  ({ valid, overflow }) => {
    const value = fixture();
    render(value.view());
    fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
    const editor = screen.getByRole("textbox", { name: "Edited input" });
    fireEvent.change(editor, { target: { value: valid, selectionStart: 0 } });
    fireEvent.change(editor, {
      target: { value: overflow, selectionStart: 0 },
    });
    expect(editor).toHaveProperty("value", valid);
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(value.edit).not.toHaveBeenCalled();
    fireEvent.change(editor, {
      target: { value: "Within the budget", selectionStart: 0 },
    });
    expect(editor).toHaveProperty("value", "Within the budget");
    expect(screen.queryByRole("alert")).toBeNull();
    fireEvent.change(editor, {
      target: { value: "  \n  ", selectionStart: 0 },
    });
    expect(screen.getByRole("button", { name: "Save input" })).toHaveProperty(
      "disabled",
      true,
    );
    fireEvent.click(screen.getByRole("button", { name: "Save input" }));
    expect(value.edit).not.toHaveBeenCalled();
  },
);

it("locks a large pending and uncertain edit and explicitly retries the original bytes", async () => {
  const value = fixture("a".repeat(100000));
  let reject!: (reason: ConnectError) => void;
  value.edit.mockImplementationOnce(
    () =>
      new Promise((_resolve, fail) => {
        reject = fail;
      }),
  );
  render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  const editor = screen.getByRole("textbox", { name: "Edited input" });
  const edited = `${"a".repeat(99999)}b`;
  fireEvent.change(editor, {
    target: { value: edited, selectionStart: edited.length },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save input" }));
  await waitFor(() => expect(value.edit).toHaveBeenCalledOnce());
  expect(editor).toHaveProperty("disabled", true);
  fireEvent.change(editor, { target: { value: "Replacement" } });
  fireEvent.submit(editor.closest("form")!);
  expect(editor).toHaveProperty("value", edited);
  expect(value.edit).toHaveBeenCalledOnce();
  await act(async () => {
    reject(new ConnectError("Lost response", Code.Unavailable));
  });
  const retry = await screen.findByRole("button", {
    name: "Retry the same edit",
  });
  expect(editor).toHaveProperty("disabled", true);
  fireEvent.change(editor, { target: { value: "Another replacement" } });
  expect(editor).toHaveProperty("value", edited);
  fireEvent.click(retry);
  await waitFor(() => expect(value.edit).toHaveBeenCalledTimes(2));
  expect(value.edit.mock.calls[1]![0]).toEqual(value.edit.mock.calls[0]![0]);
  expect(value.edit.mock.calls[0]![0]).toMatchObject({
    prompt: edited,
    mutation: { expectedRevision: value.resource.revision },
  });
});

it("binds an edit to the revision at which editing began and preserves its draft after peer changes", async () => {
  const value = fixture();
  const mounted = render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Edited input" }), {
    target: { value: "My staged change" },
  });
  mounted.rerender(
    value.view(
      create(ResourceSchema, {
        ...value.resource,
        revision: 8n,
        documentJson: encode({
          prompt: "Peer change",
          sequence: 3,
          delivery: "queued",
          mode: "plan",
        }),
      }),
    ),
  );
  expect(
    (
      screen.getByRole("textbox", {
        name: "Edited input",
      }) as HTMLTextAreaElement
    ).value,
  ).toBe("My staged change");
  fireEvent.submit(
    screen.getByRole("button", { name: "Save input" }).closest("form")!,
  );
  expect(value.edit).not.toHaveBeenCalled();
  expect(
    (screen.getByRole("button", { name: "Save input" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
});

it("Steers exactly the selected queue item into the observed active execution and turn", async () => {
  const value = fixture();
  render(value.view());
  fireEvent.click(
    screen.getByRole("button", { name: "Steer with this input" }),
  );
  await waitFor(() => expect(value.steer).toHaveBeenCalledTimes(1));
  expect(value.steer.mock.calls[0][0]).toMatchObject({
    mutation: { id: value.resource.id, expectedRevision: 6n },
    sessionId: value.session.id,
    expectedExecutionId: value.execution,
    expectedTurnId: value.turn,
  });
  expect(value.edit).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
});

it("does not expose editing or removal once native delivery has claimed the input", () => {
  const value = fixture();
  render(
    value.view(
      create(ResourceSchema, {
        ...value.resource,
        documentJson: encode({
          prompt: "Claimed input",
          sequence: 3,
          delivery: "claimed",
          mode: "plan",
        }),
      }),
    ),
  );
  expect(screen.queryByRole("button", { name: "Edit input" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Remove input" })).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Steer with this input" }),
  ).toBeNull();
});

it("restores a queue edit after payload eviction without replay or autofocus", async () => {
  const f = fixture();
  function View() {
    const [visible, setVisible] = useState(true);
    const drafts = useConversationDrafts<QueuedInputDraft>();
    return (
      <>
        <input aria-label="Composer outside payload" />
        <button onClick={() => setVisible((value) => !value)}>
          Toggle payload
        </button>
        {visible ? (
          <QueuedInput
            resource={f.resource}
            session={f.session}
            refresh={() => {}}
            draft={drafts.values.get(f.resource.id)}
            changeDraft={(value) => drafts.save(f.resource.id, value)}
          />
        ) : null}
      </>
    );
  }
  render(
    <TransportProvider transport={f.transport}>
      <QueryClientProvider client={f.client}>
        <MutationIntents>
          <View />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Edited input" }), {
    target: { value: "Preserved original edit" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Toggle payload" }));
  const composer = screen.getByRole("textbox", {
    name: "Composer outside payload",
  });
  composer.focus();
  fireEvent.click(screen.getByRole("button", { name: "Toggle payload" }));
  expect(screen.getByRole("textbox", { name: "Edited input" })).toHaveProperty(
    "value",
    "Preserved original edit",
  );
  expect(document.activeElement).toBe(composer);
  expect(f.edit).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Save input" }));
  await waitFor(() => expect(f.edit).toHaveBeenCalledTimes(1));
  expect(f.edit.mock.calls[0][0]).toMatchObject({
    prompt: "Preserved original edit",
    mutation: { id: f.resource.id, expectedRevision: f.resource.revision },
  });
});

it("edits skill-bound input with explicit retained selections and clears them deliberately", async () => {
  const f = fixture(),
    skillId = newRequestId(),
    inventoryId = newRequestId(),
    workerDeviceId = newRequestId();
  f.session.documentJson = encode({
    ...readDocument(f.session),
    machine_id: newRequestId(),
    agent_id: newRequestId(),
  });
  const bound = create(ResourceSchema, {
    ...f.resource,
    documentJson: encode({
      prompt: "Before $add-issue after",
      sequence: 3,
      delivery: "queued",
      mode: "plan",
      skills: [
        {
          skill_id: skillId,
          inventory_id: inventoryId,
          worker_device_id: workerDeviceId,
          content_revision: "a".repeat(64),
          snapshot_id: newRequestId(),
        },
      ],
      skill_names: { [skillId]: "add-issue" },
    }),
  });
  const view = render(f.view(bound));
  expect(
    (
      screen.getByRole("button", {
        name: "Steer with this input",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  expect(
    view.container.querySelector(".skill-token-selected")?.textContent,
  ).toBe("$add-issue");
  fireEvent.change(screen.getByRole("textbox", { name: "Edited input" }), {
    target: { value: "New Before $add-issue after", selectionStart: 0 },
  });
  expect(
    view.container
      .querySelector(".skill-token-selected")
      ?.getAttribute("data-skill-start"),
  ).toBe("11");
  fireEvent.click(screen.getByRole("button", { name: "Save input" }));
  await waitFor(() => expect(f.edit).toHaveBeenCalledTimes(1));
  expect(f.edit.mock.calls[0]![0]).toMatchObject({
    skills: {
      selections: [
        {
          skillId,
          inventoryId,
          workerDeviceId,
          contentRevision: "a".repeat(64),
        },
      ],
    },
  });
});
it("completes a skill in the queue editor with a trailing separator and binds only the name", async () => {
  const f = fixture();
  f.session.documentJson = encode({
    ...readDocument(f.session),
    machine_id: newRequestId(),
    agent_id: newRequestId(),
  });
  const view = render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  const editor = screen.getByRole("textbox", {
    name: "Edited input",
  }) as HTMLTextAreaElement;
  editor.focus();
  fireEvent.change(editor, { target: { value: "$avail", selectionStart: 6 } });
  fireEvent.click(await screen.findByRole("option", { name: /available/ }));
  expect(editor.value).toBe("$available ");
  await waitFor(() => expect(editor.selectionStart).toBe(11));
  expect(editor.selectionEnd).toBe(11);
  expect(document.activeElement).toBe(editor);
  const span = view.container.querySelector(".skill-token-selected")!;
  expect(span.textContent).toBe("$available");
  expect(span.getAttribute("data-skill-start")).toBe("0");
  fireEvent.click(screen.getByRole("button", { name: "Save input" }));
  await waitFor(() => expect(f.edit).toHaveBeenCalledTimes(1));
  expect(f.edit.mock.calls[0]![0]).toMatchObject({
    skills: { selections: [expect.objectContaining({})] },
  });
});
it("explicitly clears selected bindings without altering visible text", async () => {
  const f = fixture(),
    skillId = newRequestId();
  f.session.documentJson = encode({
    ...readDocument(f.session),
    machine_id: newRequestId(),
    agent_id: newRequestId(),
  });
  const bound = create(ResourceSchema, {
    ...f.resource,
    documentJson: encode({
      prompt: "$add-issue remains plain",
      sequence: 3,
      delivery: "queued",
      mode: "plan",
      skills: [
        {
          skill_id: skillId,
          inventory_id: newRequestId(),
          worker_device_id: newRequestId(),
          content_revision: "a".repeat(64),
          snapshot_id: newRequestId(),
        },
      ],
      skill_names: { [skillId]: "add-issue" },
    }),
  });
  const view = render(f.view(bound));
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  expect(
    view.container.querySelector(".skill-token-selected")?.textContent,
  ).toBe("$add-issue");
  fireEvent.click(
    screen.getByRole("button", { name: "Clear selected skills" }),
  );
  expect(view.container.querySelector(".skill-token-selected")).toBeNull();
  expect(screen.getByRole("textbox", { name: "Edited input" })).toHaveProperty(
    "value",
    "$add-issue remains plain",
  );
  fireEvent.click(screen.getByRole("button", { name: "Save input" }));
  await waitFor(() => expect(f.edit).toHaveBeenCalledTimes(1));
  expect(f.edit.mock.calls[0]![0]).toMatchObject({
    skills: { selections: [] },
  });
});

it("retains skill bindings in the external queue draft across payload eviction", async () => {
  const f = fixture(),
    skillId = newRequestId(),
    inventoryId = newRequestId(),
    workerDeviceId = newRequestId();
  const resource = create(ResourceSchema, {
    ...f.resource,
    documentJson: encode({
      prompt: "Original $add-issue",
      sequence: 3,
      delivery: "queued",
      mode: "plan",
      skills: [
        {
          skill_id: skillId,
          inventory_id: inventoryId,
          worker_device_id: workerDeviceId,
          content_revision: "a".repeat(64),
          snapshot_id: newRequestId(),
        },
      ],
      skill_names: { [skillId]: "add-issue" },
    }),
  });
  function View() {
    const [visible, setVisible] = useState(true),
      drafts = useConversationDrafts<QueuedInputDraft>();
    return (
      <>
        <button onClick={() => setVisible((v) => !v)}>Toggle payload</button>
        {visible ? (
          <QueuedInput
            resource={resource}
            session={f.session}
            refresh={() => {}}
            draft={drafts.values.get(resource.id)}
            changeDraft={(value) => drafts.save(resource.id, value)}
          />
        ) : null}
      </>
    );
  }
  render(
    <TransportProvider transport={f.transport}>
      <QueryClientProvider client={f.client}>
        <MutationIntents>
          <View />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Edited input" }), {
    target: { value: "Changed Original $add-issue", selectionStart: 0 },
  });
  fireEvent.click(screen.getByRole("button", { name: "Toggle payload" }));
  fireEvent.click(screen.getByRole("button", { name: "Toggle payload" }));
  expect(screen.getByRole("textbox", { name: "Edited input" })).toHaveProperty(
    "value",
    "Changed Original $add-issue",
  );
  fireEvent.click(screen.getByRole("button", { name: "Save input" }));
  await waitFor(() => expect(f.edit).toHaveBeenCalledTimes(1));
  expect(f.edit.mock.calls[0]![0]).toMatchObject({
    skills: { selections: [{ skillId, inventoryId, workerDeviceId }] },
  });
});

it.each(["two packages", "earlier literal"])(
  "blocks ambiguous restored token spans through edits: %s",
  async (kind) => {
    const f = fixture(),
      ids =
        kind === "two packages"
          ? [newRequestId(), newRequestId()]
          : [newRequestId()];
    f.session.documentJson = encode({
      ...readDocument(f.session),
      machine_id: newRequestId(),
      agent_id: newRequestId(),
    });
    const resource = create(ResourceSchema, {
      ...f.resource,
      documentJson: encode({
        prompt: "$same $same",
        sequence: 3,
        delivery: "queued",
        mode: "plan",
        skills: ids.map((skill_id) => ({
          skill_id,
          inventory_id: newRequestId(),
          worker_device_id: newRequestId(),
          content_revision: "a".repeat(64),
          snapshot_id: newRequestId(),
        })),
        skill_names: Object.fromEntries(ids.map((id) => [id, "same"])),
      }),
    });
    const view = render(f.view(resource));
    fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
    const input = screen.getByRole("textbox", { name: "Edited input" });
    expect(view.container.querySelector(".skill-token-selected")).toBeNull();
    for (const value of ["$other $same", "$same $other", "plain text"]) {
      fireEvent.change(input, { target: { value, selectionStart: 0 } });
      expect(
        (
          screen.getByRole("button", {
            name: "Save input",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(true);
      expect(view.container.querySelector(".skill-token-selected")).toBeNull();
      fireEvent.submit(input.closest("form")!);
      expect(f.edit).not.toHaveBeenCalled();
    }
    fireEvent.click(
      screen.getByRole("button", { name: "Clear selected skills" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Save input" }));
    await waitFor(() => expect(f.edit).toHaveBeenCalledTimes(1));
    expect(f.edit.mock.calls[0]![0]).toMatchObject({
      skills: { selections: [] },
    });
  },
);

it("disables image Steer and verifies immutable images before accepting an empty text edit", async () => {
  const value = fixture();
  const image = {
    id: newRequestId(),
    machine_id: newRequestId(),
    media_type: "image/png",
    byte_length: 24,
    sha256: "a".repeat(64),
  };
  const input = create(ResourceSchema, {
    ...value.resource,
    documentJson: encode({
      prompt: "Caption",
      delivery: "queued",
      attachments: [image],
    }),
  });
  value.edit.mockImplementation(async () => ({
    change: {
      input: create(ResourceSchema, {
        ...input,
        revision: 7n,
        documentJson: encode({
          prompt: "",
          delivery: "queued",
          attachments: [],
        }),
      }),
    },
  }));
  render(value.view(input));
  expect(
    screen.getByRole("button", { name: "Steer with this input" }),
  ).toHaveProperty("disabled", true);
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Edited input" }), {
    target: { value: "" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save input" }));
  await screen.findByRole("button", { name: "Retry the same edit" });
  expect(screen.getByRole("textbox", { name: "Edited input" })).toHaveProperty(
    "value",
    "",
  );
  const request = value.edit.mock.calls[0][0] as {
    mutation: { requestId: string };
  };
  value.edit.mockImplementation(async () => ({
    change: {
      requestId: request.mutation.requestId,
      session: value.session,
      input: create(ResourceSchema, {
        ...input,
        revision: 7n,
        documentJson: encode({
          prompt: "",
          delivery: "queued",
          attachments: [image],
        }),
      }),
    },
  }));
  fireEvent.click(screen.getByRole("button", { name: "Retry the same edit" }));
  await waitFor(() =>
    expect(screen.queryByRole("textbox", { name: "Edited input" })).toBeNull(),
  );
  expect(value.edit.mock.calls[1][0]).toEqual(value.edit.mock.calls[0][0]);
  expect(value.steer).not.toHaveBeenCalled();
});

it("opens the complete original compact input through More and restores menu focus on Escape", async () => {
  const f = fixture("Long original prompt ".repeat(100));
  render(f.view(f.resource, true));
  expect(screen.queryByText("plan · queued")).toBeNull();
  expect(screen.queryByRole("button", { name: "Edit input" })).toBeNull();
  const more = screen.getByRole("button", { name: "More input actions" });
  fireEvent.click(more);
  const edit = screen.getByRole("menuitem", { name: "Edit input" });
  expect(document.activeElement).toBe(edit);
  fireEvent.keyDown(edit, { key: "Escape" });
  expect(screen.queryByRole("menu")).toBeNull();
  expect(document.activeElement).toBe(more);
  fireEvent.click(more);
  fireEvent.click(screen.getByRole("menuitem", { name: "Edit input" }));
  expect(screen.getByRole("textbox", { name: "Edited input" })).toHaveProperty(
    "value",
    "Long original prompt ".repeat(100),
  );
  expect(f.edit).not.toHaveBeenCalled();
  expect(f.remove).not.toHaveBeenCalled();
  expect(f.steer).not.toHaveBeenCalled();
});

it("reveals the complete compact input while retained pagination is read-only", () => {
  const prompt = "Complete retained pagination input ".repeat(100),
    value = fixture(prompt);
  render(value.view(value.resource, true, true));
  fireEvent.click(screen.getByRole("button", { name: "More input actions" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "Edit input" }));
  expect(screen.getByRole("textbox", { name: "Edited input" })).toHaveProperty(
    "value",
    prompt,
  );
  expect(screen.getByRole("button", { name: "Save input" })).toHaveProperty(
    "disabled",
    true,
  );
  expect(screen.getByRole("button", { name: "Remove input" })).toHaveProperty(
    "disabled",
    true,
  );
  expect(value.edit).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
});

it("reveals the complete compact input during pending and uncertain removal without replacing the request", async () => {
  const prompt = "Complete original pending removal input ".repeat(100),
    value = fixture(prompt);
  let reject!: (error: ConnectError) => void;
  value.remove.mockImplementationOnce(
    () =>
      new Promise((_resolve, fail) => {
        reject = fail;
      }),
  );
  render(value.view(value.resource, true));
  fireEvent.click(screen.getByRole("button", { name: "Remove input" }));
  await waitFor(() => expect(value.remove).toHaveBeenCalledOnce());
  fireEvent.click(screen.getByRole("button", { name: "More input actions" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "Edit input" }));
  expect(screen.getByRole("textbox", { name: "Edited input" })).toHaveProperty(
    "value",
    prompt,
  );
  expect(screen.getByRole("button", { name: "Save input" })).toHaveProperty(
    "disabled",
    true,
  );
  await act(async () =>
    reject(new ConnectError("Lost original removal", Code.Unavailable)),
  );
  expect(
    screen.getByRole("button", { name: "More input actions" }),
  ).toHaveProperty("disabled", false);
  expect(screen.getByRole("button", { name: "Save input" })).toHaveProperty(
    "disabled",
    true,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Retry the same removal" }),
  );
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(2));
  expect(value.remove.mock.calls[1]![0]).toEqual(
    value.remove.mock.calls[0]![0],
  );
  expect(value.edit).not.toHaveBeenCalled();
});

it("keeps movement locked when inspecting an input with an uncertain removal", async () => {
  const value = fixture(),
    up = vi.fn(),
    down = vi.fn();
  value.remove.mockRejectedValueOnce(
    new ConnectError("Lost original removal", Code.Unavailable),
  );
  render(
    <TransportProvider transport={value.transport}>
      <QueryClientProvider client={value.client}>
        <MutationIntents>
          <QueuedInput
            compact
            resource={value.resource}
            session={value.session}
            refresh={() => {}}
            movement={{
              disabled: false,
              up,
              down,
              start: () => {},
              end: () => {},
            }}
          />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Remove input" }));
  await screen.findByRole("button", { name: "Retry the same removal" });
  fireEvent.click(screen.getByRole("button", { name: "More input actions" }));
  expect(screen.getByRole("menuitem", { name: "Edit input" })).toHaveProperty(
    "disabled",
    false,
  );
  expect(screen.getByRole("menuitem", { name: "Move up" })).toHaveProperty(
    "disabled",
    true,
  );
  expect(screen.getByRole("menuitem", { name: "Move down" })).toHaveProperty(
    "disabled",
    true,
  );
  fireEvent.click(screen.getByRole("menuitem", { name: "Move up" }));
  expect(up).not.toHaveBeenCalled();
  expect(down).not.toHaveBeenCalled();
});

it.each([false, true])(
  "marks only queued input removal Danger when compact=%s",
  (compact) => {
    const value = fixture();
    render(value.view(value.resource, compact));
    expectTone(screen.getByRole("button", { name: "Remove input" }), danger);
    for (const name of ["Steer with this input", "Edit input"]) {
      const button = screen.queryByRole("button", { name });
      if (button) expectTone(button, null);
    }
    expect(value.remove).not.toHaveBeenCalled();
  },
);

it("shows a stored Markdown link collapsed and saves the canonical Markdown", async () => {
  const url = "https://example.com/docs";
  const canonical = `Read [${url}](${url}) first`;
  const value = fixture(canonical);
  render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  const editor = screen.getByRole("textbox", {
    name: "Edited input",
  }) as HTMLTextAreaElement;
  expect(editor.value).toBe(`Read ${url} first`);
  editor.setSelectionRange(editor.value.length, editor.value.length);
  fireEvent.paste(editor, {
    clipboardData: { getData: () => " https://example.org/x", files: [] },
  });
  expect(editor.value).toBe(`Read ${url} first https://example.org/x`);
  fireEvent.click(screen.getByRole("button", { name: "Save input" }));
  await waitFor(() => expect(value.edit).toHaveBeenCalledOnce());
  expect(value.edit.mock.calls[0]![0]).toMatchObject({
    prompt: `${canonical} [https://example.org/x](https://example.org/x)`,
  });
});

afterEach(() => {
  delete (HTMLElement.prototype as { showPopover?: unknown }).showPopover;
  delete (HTMLElement.prototype as { hidePopover?: unknown }).hidePopover;
});
function stubPopover() {
  const show = vi.fn(),
    hide = vi.fn();
  Object.defineProperty(HTMLElement.prototype, "showPopover", {
    configurable: true,
    value: show,
  });
  Object.defineProperty(HTMLElement.prototype, "hidePopover", {
    configurable: true,
    value: hide,
  });
  return { show, hide };
}
// Everything outside the popup is the surrounding layout. Opening and closing
// the More menu must leave it byte-for-byte equal apart from the trigger state.
function surrounding(container: HTMLElement) {
  const clone = container.cloneNode(true) as HTMLElement;
  clone.querySelectorAll(".queue-more-popup").forEach((node) => node.remove());
  return clone.innerHTML.replace(/ aria-expanded="[^"]*"/g, "");
}

it("presents the compact More menu as an anchored top-layer popup that reserves no layout", () => {
  const { show, hide } = stubPopover();
  const first = fixture("First queued input"),
    second = fixture("Second queued input");
  const view = render(
    <>
      {first.view(first.resource, true)}
      {second.view(second.resource, true)}
      <button>Composer</button>
    </>,
  );
  const closed = surrounding(view.container);
  const [more] = screen.getAllByRole("button", { name: "More input actions" });
  fireEvent.click(more!);
  const menu = screen.getByRole("menu", { name: "More input actions" });
  // Original DOM owner, native manual popover, shared anchored-popup chrome.
  expect(menu.closest(".queue-more")).toBe(more!.closest(".queue-more"));
  expect(menu.getAttribute("popover")).toBe("manual");
  expect(menu.classList.contains("anchored-popup")).toBe(true);
  expect(show).toHaveBeenCalledOnce();
  expect(hide).not.toHaveBeenCalled();
  expect(screen.getAllByRole("menu")).toHaveLength(1);
  expect(surrounding(view.container)).toBe(closed);
  // The menu keeps its own focus semantics.
  expect(document.activeElement).toBe(
    screen.getByRole("menuitem", { name: "Edit input" }),
  );
  fireEvent.click(more!);
  expect(screen.queryByRole("menu")).toBeNull();
  expect(hide).toHaveBeenCalledOnce();
  expect(surrounding(view.container)).toBe(closed);
  for (const value of [first, second]) {
    expect(value.edit).not.toHaveBeenCalled();
    expect(value.steer).not.toHaveBeenCalled();
    expect(value.remove).not.toHaveBeenCalled();
  }
});

it("dismisses the More popup when focus leaves it and keeps it while focus moves between items", () => {
  const { hide } = stubPopover();
  const value = fixture();
  render(
    <>
      <TransportProvider transport={value.transport}>
        <QueryClientProvider client={value.client}>
          <MutationIntents>
            <QueuedInput
              compact
              resource={value.resource}
              session={value.session}
              refresh={() => {}}
              movement={{
                disabled: false,
                up: () => {},
                down: () => {},
                start: () => {},
                end: () => {},
              }}
            />
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
      <button>Outside</button>
    </>,
  );
  const more = screen.getByRole("button", { name: "More input actions" });
  fireEvent.click(more);
  const edit = screen.getByRole("menuitem", { name: "Edit input" }),
    up = screen.getByRole("menuitem", { name: "Move up" });
  fireEvent.blur(edit, { relatedTarget: up });
  expect(screen.getByRole("menu")).toBeTruthy();
  expect(hide).not.toHaveBeenCalled();
  // Outside pointer and Tab both move focus outside the menu owner.
  fireEvent.blur(up, { relatedTarget: screen.getByText("Outside") });
  expect(screen.queryByRole("menu")).toBeNull();
  expect(hide).toHaveBeenCalledOnce();
  fireEvent.click(more);
  fireEvent.blur(screen.getByRole("menuitem", { name: "Edit input" }), {
    relatedTarget: null,
  });
  expect(screen.queryByRole("menu")).toBeNull();
  expect(value.edit).not.toHaveBeenCalled();
  expect(value.steer).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
});

it("dismisses the More popup once its row scrolls completely out of the compact list", () => {
  const { hide } = stubPopover();
  const value = fixture();
  let moreTop = 120;
  const rect = (left: number, top: number, width: number, height: number) =>
    ({
      left,
      top,
      width,
      height,
      right: left + width,
      bottom: top + height,
      x: left,
      y: top,
      toJSON: () => ({}),
    }) as DOMRect;
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    function (this: HTMLElement) {
      if (this.classList.contains("queue-compact-list"))
        return rect(10, 100, 400, 80);
      if (this.getAttribute("aria-label") === "More input actions")
        return this.getAttribute("role") === "menu"
          ? rect(0, 0, 160, 120)
          : rect(360, moreTop, 32, 32);
      return rect(0, 0, 0, 0);
    },
  );
  try {
    render(
      <div className="queue-compact-list" style={{ overflow: "auto" }}>
        {value.view(value.resource, true)}
      </div>,
    );
    fireEvent.click(screen.getByRole("button", { name: "More input actions" }));
    expect(screen.getByRole("menu")).toBeTruthy();
    // Partly visible: still presented, even though the popup may extend past the list.
    moreTop = 170;
    fireEvent.scroll(document.querySelector(".queue-compact-list")!);
    expect(screen.getByRole("menu")).toBeTruthy();
    expect(hide).not.toHaveBeenCalled();
    // Scrolled completely below the list's visible box.
    moreTop = 180;
    fireEvent.scroll(document.querySelector(".queue-compact-list")!);
    expect(screen.queryByRole("menu")).toBeNull();
    expect(hide).toHaveBeenCalledOnce();
    expect(
      screen
        .getByRole("button", { name: "More input actions" })
        .getAttribute("aria-expanded"),
    ).toBe("false");
    expect(value.edit).not.toHaveBeenCalled();
    expect(value.steer).not.toHaveBeenCalled();
    expect(value.remove).not.toHaveBeenCalled();
  } finally {
    vi.restoreAllMocks();
  }
});

it("hides the More popup when the row unmounts while it is open", () => {
  const { hide } = stubPopover();
  const value = fixture();
  const view = render(value.view(value.resource, true));
  fireEvent.click(screen.getByRole("button", { name: "More input actions" }));
  expect(screen.getByRole("menu")).toBeTruthy();
  view.unmount();
  expect(hide).toHaveBeenCalledOnce();
});

it("selects Edit from the popup without any mutation and closes it", () => {
  const { hide } = stubPopover();
  const value = fixture("Complete queued input");
  render(value.view(value.resource, true));
  fireEvent.click(screen.getByRole("button", { name: "More input actions" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "Edit input" }));
  expect(screen.queryByRole("menu")).toBeNull();
  expect(hide).toHaveBeenCalledOnce();
  expect(screen.getByRole("textbox", { name: "Edited input" })).toHaveProperty(
    "value",
    "Complete queued input",
  );
  expect(value.edit).not.toHaveBeenCalled();
  expect(value.steer).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
});
