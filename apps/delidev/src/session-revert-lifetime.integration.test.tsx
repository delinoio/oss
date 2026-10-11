// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useRef, useState } from "react";
import { expect, it, vi } from "vitest";
import {
  EntityKind, ResourceSchema, ResourceService, SessionService, SystemService,
  SystemCapability, newRequestId, type Resource,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { DraftEditGenerations } from "./draft-edit-generations";
import { MutationIntents } from "./mutation";
import { useSessionRevert } from "./session-revert";
import { useShortcuts } from "./shortcut-provider";
import { ShortcutId, ShortcutInput, ShortcutPlatform, shortcutPlatform } from "./shortcuts";
import { Surface } from "./surface";

const cases = ["mounted", "reuse", "remount"] as const;
it.each(cases)("retains original busy/action/draft ownership across %s navigation", async (mode) => {
  await scenario(mode, "accepted", true);
});
it.each(cases)("keeps exact uncertain replay and edited ABA draft across %s navigation", async (mode) => {
  await scenario(mode, "uncertain", false);
});
it.each(cases)("preserves empty ABA draft edits across %s navigation", async (mode) => {
  await scenario(mode, "uncertain", false, "");
});
it.each(cases)("releases only the rejected original fence across %s navigation", async (mode) => {
  await scenario(mode, "rejected", false);
});

async function scenario(
  mode: (typeof cases)[number],
  outcome: "accepted" | "uncertain" | "rejected",
  untouched: boolean,
  initialDraft = "A",
) {
  const a = newRequestId(), b = newRequestId(), machine = newRequestId();
  const message = newRequestId(), turn = newRequestId(), input = newRequestId();
  const thread = newRequestId(), job = newRequestId();
  const body = {
    machine_id: machine,
    initial_execution: { configuration: { harness: "codex" } },
    archive: "active", recovery: "none",
    execution: { cleanup_verified: true, native_thread_id: thread },
  };
  const original = (id: string) => create(ResourceSchema, {
    id, sessionId: id, kind: EntityKind.SESSION, schemaVersion: 1,
    revision: 2n, documentJson: encode(body),
  });
  const target = create(ResourceSchema, {
    id: message, sessionId: a, kind: EntityKind.MESSAGE, schemaVersion: 1,
    revision: 1n, documentJson: encode({
      role: "user", state: "complete", text: "Historical H", input_id: input,
      native_thread_id: thread, native_turn_id: turn,
    }),
  });
  const rows = new Map([[a, original(a)], [b, original(b)]]);
  const drafts = new Map([[a, initialDraft], [b, "Independent B"]]);
  const edits = new DraftEditGenerations();
  let settle!: () => void, fail!: (error: Error) => void;
  const acknowledgment = new Promise<void>((resolve, reject) => {
    settle = resolve; fail = reject;
  });
  const requests: any[] = [];
  const revert = vi.fn(async (request) => {
    requests.push(request);
    if (requests.length === 1) await acknowledgment;
    return {
      requestId: request.mutation.requestId,
      job: create(ResourceSchema, {
        id: job, sessionId: a, kind: EntityKind.JOB, schemaVersion: 1,
        revision: 1n, documentJson: encode({
          input: { version: 4, action_id: request.mutation.requestId,
            revert: { message_id: message, native_turn_id: turn, context_revision: 0 } },
        }),
      }),
    };
  });
  const enqueue = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({ capabilities: [SystemCapability.CODEX_SESSION_REVERT_V1] }),
    });
    router.service(ResourceService, {
      getResource: (request) => ({ resource: request.kind === EntityKind.MACHINE
        ? create(ResourceSchema, { id: machine, kind: EntityKind.MACHINE,
          schemaVersion: 1, revision: 1n,
          documentJson: encode({ worker_capabilities: ["codex-session-revert-v1"] }) })
        : rows.get(request.id) }),
    });
    router.service(SessionService, { revertSession: revert, enqueueInput: enqueue });
  });
  let publish!: () => void, editWhileAbsent!: () => void;
  function Root() {
    const [selected, select] = useState(a);
    const [, redraw] = useState(0);
    publish = () => redraw((value) => value + 1);
    editWhileAbsent = () => {
      edits.advance(a); drafts.set(a, "B");
      edits.advance(a); drafts.set(a, initialDraft);
      publish();
    };
    const save = (id: string, value: string) => {
      drafts.set(id, value); publish();
    };
    return <>
      <button onClick={() => select(a)}>Show A</button>
      <button onClick={() => select(b)}>Show B</button>
      {mode === "mounted" ? [a, b].map((id) => (
        <div key={id} hidden={selected !== id} inert={selected !== id}>
          <Presenter row={rows.get(id)!} active={selected === id} save={save} />
        </div>
      )) : <Presenter key={mode === "remount" ? selected : "original-presenter"}
        row={rows.get(selected)!} active save={save} />}
    </>;
  }
  function Presenter({ row, active, save }: {
    row: Resource; active: boolean; save: (id: string, value: string) => void;
  }) {
    const composer = useRef<HTMLTextAreaElement>(null);
    const state = useSessionRevert({
      session: row, active, draft: drafts.get(row.id)!,
      draftGeneration: () => edits.read(row.id), composer, blocked: false,
      changed: (resource) => { rows.set(resource.id, resource); publish(); },
      restore: (prompt) => save(row.id, prompt),
    });
    const locked = state.pending || state.uncertain;
    const shortcuts = useShortcuts([{
      id: ShortcutId.SessionSend, scope: Surface.Sessions,
      label: "shortcuts.queueMessage", bindings: [{ key: "Enter", primary: true }],
      target: composer, input: ShortcutInput.Target, active, enabled: !locked,
      run: () => { if (active && !locked) enqueue(); },
    }]);
    return <section aria-label={row.id === a ? "Session A" : "Session B"}>
      <textarea aria-label="Unsent prompt" ref={composer} value={drafts.get(row.id)}
        onKeyDown={shortcuts.onKeyDown}
        onChange={(event) => { edits.advance(row.id); save(row.id, event.target.value); }} />
      <button disabled={!active || locked} onClick={() => {
        if (active && !locked) enqueue();
      }}>Send</button>
      <output>{locked ? "Original context locked" : "Context available"}</output>
      {row.id === a ? state.action(target) : null}
      {state.content}
    </section>;
  }
  render(<TransportProvider transport={transport}>
    <QueryClientProvider client={new QueryClient({ defaultOptions: {
      queries: { retry: false }, mutations: { retry: false },
    } })}>
      <MutationIntents><Root /></MutationIntents>
    </QueryClientProvider>
  </TransportProvider>);
  const viewA = () => within(screen.getByRole("region", { name: "Session A" }));
  await waitFor(() => expect(viewA().getByRole("button", { name: "Revert and edit" }))
    .toHaveProperty("disabled", false));
  fireEvent.click(viewA().getByRole("button", { name: "Revert and edit" }));
  fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Revert and edit" }));
  await waitFor(() => expect(revert).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Show B" }));
  expect(within(screen.getByRole("region", { name: "Session B" })).getByRole("button", { name: "Send" }))
    .toHaveProperty("disabled", false);
  if (!untouched) act(() => editWhileAbsent());
  fireEvent.click(screen.getByRole("button", { name: "Show A" }));
  // The source still has no compaction job. The retained operation supplies the fence.
  expect(viewA().getByRole("button", { name: "Send" })).toHaveProperty("disabled", true);
  expect(viewA().getByRole("button", { name: "Revert and edit" })).toHaveProperty("disabled", true);
  fireEvent.click(viewA().getByRole("button", { name: "Send" }));
  const composer = viewA().getByRole("textbox", { name: "Unsent prompt" });
  composer.focus();
  fireEvent.keyDown(composer, {
    key: "Enter",
    ctrlKey: shortcutPlatform() === ShortcutPlatform.Other,
    metaKey: shortcutPlatform() === ShortcutPlatform.Mac,
  });
  expect(enqueue).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Show B" }));
  await act(async () => {
    if (outcome === "accepted") settle();
    else fail(new ConnectError("Original controlled failure", outcome === "uncertain" ? Code.Unavailable : Code.FailedPrecondition));
    await Promise.resolve();
  });
  fireEvent.click(screen.getByRole("button", { name: "Show A" }));
  if (outcome === "rejected") {
    await waitFor(() => expect(viewA().getByRole("button", { name: "Send" })).toHaveProperty("disabled", false));
    expect(drafts.get(a)).toBe(initialDraft);
    expect(drafts.get(b)).toBe("Independent B");
    expect(enqueue).not.toHaveBeenCalled();
    expect(revert).toHaveBeenCalledTimes(1);
    return;
  }
  if (outcome === "uncertain") {
    expect(viewA().getByRole("button", { name: "Send" })).toHaveProperty("disabled", true);
    fireEvent.click(await viewA().findByRole("button", { name: "Retry the same Revert request" }));
    await waitFor(() => expect(revert).toHaveBeenCalledTimes(2));
    expect(requests[1]).toEqual(requests[0]);
  }
  fireEvent.click(screen.getByRole("button", { name: "Show B" }));
  act(() => {
    rows.set(a, create(ResourceSchema, { ...rows.get(a)!, revision: 4n,
      documentJson: encode({ ...body, context_revision: 1,
        revert: { action_id: requests[0].mutation.requestId, job_id: job,
          result: { context_revision: 1, retained_turn_ids: [], target: {
            message_id: message, native_turn_id: turn, context_revision: 0,
            prompt: { prompt: "Historical H", mode: "execute" },
          } },
        },
      }),
    }));
    publish();
  });
  // Verified native completion arrives while A has no active presenter. Its
  // original draft guard must still apply when A is shown again.
  expect(drafts.get(a)).toBe(initialDraft);
  fireEvent.click(screen.getByRole("button", { name: "Show A" }));
  if (!untouched) {
    await viewA().findByRole("button", { name: "Restore earlier prompt" });
    expect(drafts.get(a)).toBe(initialDraft);
    fireEvent.click(viewA().getByRole("button", { name: "Restore earlier prompt" }));
  }
  await waitFor(() => expect(drafts.get(a)).toBe("Historical H"));
  expect(document.activeElement).toBe(viewA().getByRole("textbox", { name: "Unsent prompt" }));
  expect(enqueue).not.toHaveBeenCalled();
  expect(drafts.get(b)).toBe("Independent B");
  await waitFor(() => expect(viewA().getByRole("button", { name: "Send" })).toHaveProperty("disabled", false));
  expect(revert).toHaveBeenCalledTimes(outcome === "uncertain" ? 2 : 1);
}
