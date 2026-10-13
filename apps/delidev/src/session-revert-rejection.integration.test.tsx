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
  within,
} from "@testing-library/react";
import { useRef, useState } from "react";
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
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { DraftEditGenerations } from "./draft-edit-generations";
import { MutationIntents, useRetainedMutationPresentation } from "./mutation";
import { useSessionRevert } from "./session-revert";
import { TranscriptItem } from "./session";

enum Navigation {
  None = "none",
  BeforeStart = "before-start",
  BeforeRejection = "before-rejection",
}
enum Outcome {
  Rejected = "rejected",
  Uncertain = "uncertain",
  AcceptedUnchanged = "accepted-unchanged",
  AcceptedEdited = "accepted-edited",
}

const machine = newRequestId();
const thread = newRequestId();
const queryClient = () =>
  new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
const transportFor = (
  rows: () => Resource | undefined,
  revertSession: (request: any) => unknown,
) =>
  createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.CODEX_SESSION_REVERT_V1],
      }),
    });
    router.service(ResourceService, {
      getResource: (request) => ({
        resource:
          request.kind === EntityKind.MACHINE
            ? create(ResourceSchema, {
                id: machine,
                kind: EntityKind.MACHINE,
                schemaVersion: 1,
                revision: 1n,
                documentJson: encode({
                  worker_capabilities: ["codex-session-revert-v1"],
                }),
              })
            : rows(),
      }),
    });
    router.service(SessionService, { revertSession: revertSession as never });
  });
const sessionRow = (id: string, revision: bigint, extra: object) =>
  create(ResourceSchema, {
    kind: EntityKind.SESSION,
    id,
    sessionId: id,
    revision,
    schemaVersion: 1,
    documentJson: encode({
      machine_id: machine,
      initial_execution: { configuration: { harness: "codex" } },
      archive: "active",
      recovery: "none",
      execution: { cleanup_verified: true, native_thread_id: thread },
      ...extra,
    }),
  });
const message = (id: string, sessionId: string, turn: string) =>
  create(ResourceSchema, {
    kind: EntityKind.MESSAGE,
    id,
    sessionId,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({
      role: "user",
      state: "complete",
      text: "Later prompt",
      input_id: newRequestId(),
      native_thread_id: thread,
      native_turn_id: turn,
      context_revision: 0,
    }),
  });
const verifiedRevert = (
  action: string,
  job: string,
  target: { message: string; turn: string; context: number },
  prompt: string,
  retained: string[],
) => ({
  context_revision: target.context + 1,
  revert: {
    action_id: action,
    job_id: job,
    result: {
      context_revision: target.context + 1,
      retained_turn_ids: retained,
      target: {
        message_id: target.message,
        native_turn_id: target.turn,
        context_revision: target.context,
        prompt: { prompt, mode: "execute" },
      },
    },
  },
});

it.each([
  [Navigation.None, Outcome.Rejected],
  [Navigation.BeforeStart, Outcome.Rejected],
  [Navigation.BeforeRejection, Outcome.Rejected],
  [Navigation.BeforeRejection, Outcome.Uncertain],
  [Navigation.None, Outcome.AcceptedUnchanged],
  [Navigation.None, Outcome.AcceptedEdited],
])(
  "keeps restored Revert A when later Revert B ends with %s navigation and %s outcome",
  async (navigation, outcome) => {
    const id = newRequestId(),
      jobA = newRequestId(),
      jobB = newRequestId(),
      actionA = newRequestId(),
      messageA = newRequestId(),
      messageB = newRequestId(),
      turnA = newRequestId(),
      turnB = newRequestId();
    // A is verified and still current. Only B's turn remains eligible.
    const afterA = sessionRow(
      id,
      4n,
      verifiedRevert(
        actionA,
        jobA,
        { message: messageA, turn: turnA, context: 0 },
        "Prompt A",
        [turnB],
      ),
    );
    let row: Resource = afterA;
    const target = message(messageB, id, turnB);
    let settle!: () => void, fail!: (error: Error) => void;
    const answer = new Promise<void>((resolve, reject) => {
      settle = resolve;
      fail = reject;
    });
    const requests: any[] = [];
    const revert = vi.fn(async (request) => {
      requests.push(request);
      await answer;
      return {
        requestId: request.mutation.requestId,
        job: create(ResourceSchema, {
          kind: EntityKind.JOB,
          id: jobB,
          sessionId: id,
          revision: 1n,
          schemaVersion: 1,
          documentJson: encode({
            input: {
              version: 4,
              action_id: request.mutation.requestId,
              revert: {
                message_id: messageB,
                native_turn_id: turnB,
                context_revision: 1,
              },
            },
          }),
        }),
      };
    });
    const transport = transportFor(() => row, revert);
    const edits = new DraftEditGenerations();
    let setRow!: (next: Resource) => void,
      setDraft!: (value: string) => void,
      remount!: () => void;
    function Probe() {
      const owner = useRetainedMutationPresentation<{
        pending?: object;
        replacement?: object;
        restoredAction?: string;
      }>(`revert:${id}`);
      return (
        <output aria-label="retained presentation">
          {JSON.stringify({
            restoredAction: owner.value?.restoredAction,
            pending: Boolean(owner.value?.pending),
            replacement: Boolean(owner.value?.replacement),
          })}
        </output>
      );
    }
    function Presenter({ session, draft }: { session: Resource; draft: string }) {
      const composer = useRef<HTMLTextAreaElement>(null);
      const state = useSessionRevert({
        session,
        active: true,
        draft,
        draftGeneration: () => edits.read(id),
        composer,
        blocked: false,
        changed: (resource) => setRow(resource),
        restore: (prompt) => setDraft(prompt),
      });
      return (
        <>
          <textarea
            aria-label="Unsent prompt"
            ref={composer}
            value={draft}
            onChange={(event) => {
              edits.advance(id);
              setDraft(event.target.value);
            }}
          />
          <TranscriptItem resource={target} revertAction={state.action(target)} />
          {state.content}
        </>
      );
    }
    function Root() {
      const [session, setSession] = useState(row);
      const [draft, setDraftState] = useState("unsent draft");
      const [generation, setGeneration] = useState(0);
      setRow = (next) => {
        row = next;
        setSession(next);
      };
      setDraft = setDraftState;
      remount = () => setGeneration((value) => value + 1);
      return <Presenter key={generation} session={session} draft={draft} />;
    }
    render(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={queryClient()}>
          <MutationIntents>
            <Probe />
            <Root />
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>,
    );
    const retained = () =>
      JSON.parse(
        screen.getByLabelText("retained presentation").textContent ?? "{}",
      );
    // The verified A result is offered once, then restored.
    fireEvent.click(
      await screen.findByRole("button", { name: "Restore earlier prompt" }),
    );
    await waitFor(() => expect(retained().restoredAction).toBe(actionA));
    expect(
      screen.getByRole("textbox", { name: "Unsent prompt" }),
    ).toHaveProperty("value", "Prompt A");
    if (navigation === Navigation.BeforeStart) act(() => remount());
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Revert and edit" }),
      ).toHaveProperty("disabled", false),
    );
    fireEvent.click(screen.getByRole("button", { name: "Revert and edit" }));
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Revert and edit",
      }),
    );
    await waitFor(() => expect(revert).toHaveBeenCalledTimes(1));
    // B's pending guard carries A's restored identity.
    expect(retained()).toEqual({
      restoredAction: actionA,
      pending: true,
      replacement: false,
    });
    if (navigation === Navigation.BeforeRejection) act(() => remount());
    if (outcome === Outcome.AcceptedEdited)
      act(() => {
        fireEvent.change(screen.getByRole("textbox", { name: "Unsent prompt" }), {
          target: { value: "edited during Revert B" },
        });
      });
    await act(async () => {
      if (outcome === Outcome.Rejected)
        fail(new ConnectError("Definite rejection", Code.FailedPrecondition));
      else if (outcome === Outcome.Uncertain)
        fail(new ConnectError("Lost acknowledgment", Code.Unavailable));
      else settle();
      await Promise.resolve();
    });
    if (outcome === Outcome.Rejected) {
      await waitFor(() => expect(retained().pending).toBe(false));
      // Only B's guard is released: A stays restored and is not offered again.
      expect(retained()).toEqual({
        restoredAction: actionA,
        pending: false,
        replacement: false,
      });
      expect(
        screen.queryByRole("button", { name: "Restore earlier prompt" }),
      ).toBeNull();
      expect(
        screen.getByRole("button", { name: "Revert and edit" }),
      ).toHaveProperty("disabled", false);
      expect(revert).toHaveBeenCalledTimes(1);
      return;
    }
    if (outcome === Outcome.Uncertain) {
      await screen.findByRole("button", {
        name: "Retry the same Revert request",
      });
      expect(retained().pending).toBe(true);
      expect(retained().restoredAction).toBe(actionA);
      expect(
        screen.getByRole("button", { name: "Revert and edit" }),
      ).toHaveProperty("disabled", true);
      expect(
        screen.queryByRole("button", { name: "Restore earlier prompt" }),
      ).toBeNull();
      expect(revert).toHaveBeenCalledTimes(1);
      return;
    }
    // A later verified B follows the existing automatic/explicit restoration.
    act(() =>
      setRow(
        sessionRow(
          id,
          6n,
          verifiedRevert(
            requests[0].mutation.requestId,
            jobB,
            { message: messageB, turn: turnB, context: 1 },
            "Prompt B",
            [],
          ),
        ),
      ),
    );
    if (outcome === Outcome.AcceptedEdited) {
      fireEvent.click(
        await screen.findByRole("button", { name: "Restore earlier prompt" }),
      );
    }
    await waitFor(() =>
      expect(
        screen.getByRole("textbox", { name: "Unsent prompt" }),
      ).toHaveProperty("value", "Prompt B"),
    );
    await waitFor(() =>
      expect(retained().restoredAction).toBe(requests[0].mutation.requestId),
    );
    expect(
      screen.queryByRole("button", { name: "Restore earlier prompt" }),
    ).toBeNull();
  },
);

it("frees the presentation slot of 1000 sequentially rejected Reverts", async () => {
  const total = 1000;
  const sessions = Array.from({ length: total + 1 }, () => {
    const id = newRequestId();
    return { id, turn: newRequestId(), message: newRequestId() };
  });
  const revert = vi.fn(async (_request: unknown) => {
    throw new ConnectError("Definite rejection", Code.FailedPrecondition);
  });
  let current = sessions[0];
  const row = (index: number) =>
    sessionRow(sessions[index].id, 2n, {});
  let setIndex!: (index: number) => void;
  const transport = transportFor(() => row(sessions.indexOf(current)), revert);
  function Presenter({ index }: { index: number }) {
    const composer = useRef<HTMLTextAreaElement>(null);
    const session = row(index);
    const target = message(
      sessions[index].message,
      sessions[index].id,
      sessions[index].turn,
    );
    const state = useSessionRevert({
      session,
      active: true,
      draft: "draft",
      draftGeneration: () => 0n,
      composer,
      blocked: false,
      changed: () => {},
      restore: () => {},
    });
    return (
      <>
        <textarea aria-label="Unsent prompt" ref={composer} readOnly />
        <TranscriptItem resource={target} revertAction={state.action(target)} />
        {state.content}
      </>
    );
  }
  function Root() {
    const [index, set] = useState(0);
    setIndex = (next) => {
      current = sessions[next];
      set(next);
    };
    return <Presenter index={index} />;
  }
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={queryClient()}>
        <MutationIntents>
          <Root />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  const start = async (index: number) => {
    const button = await screen.findByRole("button", {
      name: "Revert and edit",
    });
    await waitFor(() => expect(button).toHaveProperty("disabled", false));
    fireEvent.click(button);
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Revert and edit",
      }),
    );
    await waitFor(() => expect(revert).toHaveBeenCalledTimes(index + 1));
    await waitFor(() =>
      expect(screen.queryByRole("status")).toBeNull(),
    );
  };
  for (let index = 0; index < total; index += 1) {
    if (index > 0) act(() => setIndex(index));
    await start(index);
  }
  // Session 1001 can still reserve a guard and send exactly once.
  act(() => setIndex(total));
  await start(total);
  expect(screen.queryByText(/Resolve retained operations/)).toBeNull();
  expect(revert).toHaveBeenCalledTimes(total + 1);
}, 600_000);
