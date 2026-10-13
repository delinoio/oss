import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  InteractionService,
  ResourceSchema,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { Interaction, advancesInteraction } from "./interactions";
import {
  InteractionPresentationStore,
  interactionProjection,
  type InteractionProjection,
} from "./interaction-presentation";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";

function fixture(data: Record<string, unknown>, activeTray = false) {
  const resource = create(ResourceSchema, {
    id: newRequestId(),
    sessionId: newRequestId(),
    kind: EntityKind.INTERACTION,
    schemaVersion: 1,
    revision: 9n,
    documentJson: encode(data),
  });
  const questions = vi.fn(async (_request: unknown) => ({
    interaction: create(ResourceSchema, {
      ...resource,
      revision: 10n,
      documentJson: encode({ ...data, response: { state: "queued" } }),
    }),
  }));
  const approvals = vi.fn(async (_request: unknown) => ({
    interaction: create(ResourceSchema, {
      ...resource,
      revision: 10n,
      documentJson: encode({ ...data, approval_response: { state: "queued" } }),
    }),
  }));
  const transport = createRouterTransport((router) =>
    router.service(InteractionService, {
      respondQuestion: questions,
      respondApproval: approvals,
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const view = (row = resource, projection?: InteractionProjection) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <Interaction
            resource={row}
            refresh={() => {}}
            activeTray={activeTray}
            projection={projection}
          />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  return { resource, questions, approvals, view };
}
const question = {
  type: "user-question",
  closure: "open",
  questions: {
    questions: [
      {
        id: "__proto__",
        header: "Pick",
        text: "Original choice",
        secret: false,
        other: false,
        options: [
          { label: "Unicode 답변", description: "Exact offered answer" },
        ],
      },
      {
        id: "empty",
        header: "Optional",
        text: "Optional answer",
        secret: false,
        other: true,
        options: [],
      },
    ],
  },
};
function input(call: unknown) {
  return call as {
    mutation: { requestId: string; id: string; expectedRevision: bigint };
    responseJson: Uint8Array;
  };
}

it("sends every original question identity, exact Unicode choice and explicit unanswered array", async () => {
  const value = fixture(question);
  render(value.view());
  fireEvent.click(screen.getByRole("checkbox", { name: /Unicode 답변/ }));
  fireEvent.click(
    screen.getAllByRole("checkbox", {
      name: "Leave this question unanswered",
    })[1],
  );
  fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await screen.findByText("Response: queued");
  const request = input(value.questions.mock.calls[0][0]);
  expect(request.mutation).toMatchObject({
    id: value.resource.id,
    expectedRevision: 9n,
  });
  const answers = JSON.parse(
    new TextDecoder().decode(request.responseJson),
  ).answers;
  expect(Object.hasOwn(answers, "__proto__")).toBe(true);
  expect(answers.__proto__).toEqual(["Unicode 답변"]);
  expect(answers.empty).toEqual([]);
  expect(value.approvals).not.toHaveBeenCalled();
});

it("retains the exact uncertain answer after another client closes the original request", async () => {
  const value = fixture(question);
  value.questions.mockRejectedValueOnce(
    new ConnectError("Lost acknowledgement", Code.Unavailable),
  );
  const mounted = render(value.view());
  for (const checkbox of screen.getAllByRole("checkbox", {
    name: "Leave this question unanswered",
  }))
    fireEvent.click(checkbox);
  fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await screen.findByRole("button", { name: "Retry the same answers" });
  mounted.rerender(
    value.view(
      create(ResourceSchema, {
        ...value.resource,
        revision: 11n,
        documentJson: encode({ ...question, closure: "turn-ended" }),
      }),
    ),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Retry the same answers" }),
  );
  await waitFor(() => expect(value.questions).toHaveBeenCalledTimes(2));
  expect(value.questions.mock.calls[0][0]).toEqual(
    value.questions.mock.calls[1][0],
  );
});

it("never presents an ordinary text field for a protected native answer", () => {
  const value = fixture({
    ...question,
    questions: {
      questions: [{ ...question.questions.questions[1], secret: true }],
    },
  });
  render(value.view());
  expect(screen.queryByRole("textbox")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  expect(value.questions).not.toHaveBeenCalled();
});

it("submits only the selected original command decision, including its exact amendment", async () => {
  const offered = {
    kind: "acceptWithExecpolicyAmendment",
    execpolicy: ["git", "status"],
    network_policy: null,
  };
  const value = fixture({
    type: "native-approval",
    closure: "open",
    approval: {
      harness: "codex",
      version: "0.151.0",
      codex: {
        kind: "command",
        command: { command: "git status", available_decisions: [offered] },
      },
    },
  });
  render(value.view());
  fireEvent.change(screen.getByRole("combobox", { name: "Decision" }), {
    target: { value: "1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send decision" }));
  await screen.findByText("Response: queued");
  const request = input(value.approvals.mock.calls[0][0]);
  expect(JSON.parse(new TextDecoder().decode(request.responseJson))).toEqual({
    decision: offered,
  });
  expect(request.mutation.expectedRevision).toBe(9n);
  expect(value.questions).not.toHaveBeenCalled();
});

it("retains native deny descriptors when reducing a requested write to read", async () => {
  const denied = { access: "deny", path: { type: "path", path: "/private" } };
  const writable = {
    access: "write",
    path: { type: "glob_pattern", pattern: "/workspace/**" },
  };
  const value = fixture({
    type: "native-approval",
    closure: "open",
    approval: {
      harness: "codex",
      version: "0.151.0",
      codex: {
        kind: "permissions",
        permissions: {
          cwd: "/workspace",
          permissions: {
            file_system: {
              entries: [denied, writable],
              read: ["/must-not-be-granted"],
              write: null,
              glob_scan_max_depth: 3,
            },
          },
        },
      },
    },
  });
  render(value.view());
  fireEvent.change(
    screen.getByRole("combobox", { name: "Access for Pattern: /workspace/**" }),
    { target: { value: "read" } },
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Send selected permissions" }),
  );
  await screen.findByText("Response: queued");
  const request = input(value.approvals.mock.calls[0][0]);
  expect(JSON.parse(new TextDecoder().decode(request.responseJson))).toEqual({
    grant: {
      scope: "turn",
      permissions: {
        file_system: {
          read: null,
          write: null,
          entries: [denied, { ...writable, access: "read" }],
          glob_scan_max_depth: 3,
        },
      },
    },
  });
});

it("refuses an unrecognized native approval version without inventing choices", () => {
  const value = fixture({
    type: "native-approval",
    closure: "open",
    approval: {
      harness: "codex",
      version: "unknown",
      codex: { kind: "file-change", file: {} },
    },
  });
  render(value.view());
  fireEvent.change(screen.getByRole("combobox", { name: "Decision" }), {
    target: { value: "1" },
  });
  fireEvent.submit(
    screen.getByRole("button", { name: "Send decision" }).closest("form")!,
  );
  expect(value.approvals).not.toHaveBeenCalled();
});

it("retires submitted answer controls while retaining compact queued delivery", async () => {
  const value = fixture(question, true);
  const view = render(value.view());
  for (const checkbox of screen.getAllByRole("checkbox", {
    name: "Leave this question unanswered",
  }))
    fireEvent.click(checkbox);
  fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await screen.findByText("Response: queued");
  expect(screen.queryByRole("textbox")).toBeNull();
  expect(screen.queryByRole("checkbox")).toBeNull();
  expect(screen.queryByRole("button", { name: "Send answers" })).toBeNull();
  view.rerender(value.view());
  expect(screen.queryByRole("button", { name: "Send answers" })).toBeNull();
  expect(value.questions).toHaveBeenCalledTimes(1);
});

it("keeps the original receipt retry accessible after closure without an answer form", async () => {
  const value = fixture(question, true);
  value.questions.mockRejectedValueOnce(
    new ConnectError("Lost acknowledgement", Code.Unavailable),
  );
  const view = render(value.view());
  for (const checkbox of screen.getAllByRole("checkbox", {
    name: "Leave this question unanswered",
  }))
    fireEvent.click(checkbox);
  fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await screen.findByRole("button", { name: "Retry the same answers" });
  view.rerender(
    value.view(
      create(ResourceSchema, {
        ...value.resource,
        revision: 11n,
        documentJson: encode({ ...question, closure: "turn-ended" }),
      }),
    ),
  );
  expect(screen.queryByRole("checkbox")).toBeNull();
  expect(screen.queryByRole("textbox")).toBeNull();
  expect(value.questions).toHaveBeenCalledTimes(1);
  fireEvent.click(
    screen.getByRole("button", { name: "Retry the same answers" }),
  );
  await waitFor(() => expect(value.questions).toHaveBeenCalledTimes(2));
  expect(value.questions.mock.calls[1][0]).toEqual(
    value.questions.mock.calls[0][0],
  );
});

it("collapses the active tray to compact recovery as soon as an answer is uncertain", async () => {
  const value = fixture(question, true);
  value.questions.mockRejectedValueOnce(
    new ConnectError("Lost acknowledgement", Code.Unavailable),
  );
  render(value.view());
  for (const checkbox of screen.getAllByRole("checkbox", {
    name: "Leave this question unanswered",
  }))
    fireEvent.click(checkbox);
  fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await screen.findByRole("button", { name: "Retry the same answers" });
  // No newer revision has arrived: the original controls must already be gone.
  expect(screen.queryByRole("checkbox")).toBeNull();
  expect(screen.queryByRole("textbox")).toBeNull();
  expect(screen.queryByRole("button", { name: "Send answers" })).toBeNull();
  // The dispatched answer may have been accepted: never claim non-submission.
  expect(screen.getByText("Response: uncertain")).toBeTruthy();
  expect(screen.queryByText(/Not submitted/)).toBeNull();
  expect(value.questions).toHaveBeenCalledTimes(1);
  fireEvent.click(
    screen.getByRole("button", { name: "Retry the same answers" }),
  );
  await screen.findByText("Response: queued");
  expect(screen.queryByText("Response: uncertain")).toBeNull();
  expect(value.questions).toHaveBeenCalledTimes(2);
  expect(value.questions.mock.calls[1][0]).toEqual(
    value.questions.mock.calls[0][0],
  );
  expect(
    screen.queryByRole("button", { name: "Retry the same answers" }),
  ).toBeNull();
});

it("keeps original answer controls beside an uncertain answer outside the active tray", async () => {
  const value = fixture(question);
  value.questions.mockRejectedValueOnce(
    new ConnectError("Lost acknowledgement", Code.Unavailable),
  );
  render(value.view());
  for (const checkbox of screen.getAllByRole("checkbox", {
    name: "Leave this question unanswered",
  }))
    fireEvent.click(checkbox);
  fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await screen.findByRole("button", { name: "Retry the same answers" });
  expect(screen.getAllByRole("checkbox").length).toBeGreaterThan(0);
  expect(screen.getByRole("button", { name: "Send answers" })).toBeTruthy();
});

it("accepts only a same-interaction result that advances the retained revision", () => {
  const expected = {
    id: newRequestId(),
    sessionId: newRequestId(),
    kind: EntityKind.INTERACTION,
    revision: 9n,
  };
  const result = (overrides: Partial<typeof expected> = {}) =>
    create(ResourceSchema, {
      ...expected,
      schemaVersion: 1,
      revision: 10n,
      documentJson: encode({}),
      ...overrides,
    });
  expect(advancesInteraction(result(), expected)).toBe(true);
  expect(advancesInteraction(undefined, expected)).toBe(false);
  expect(advancesInteraction(result({ revision: 9n }), expected)).toBe(false);
  expect(advancesInteraction(result({ revision: 8n }), expected)).toBe(false);
  expect(advancesInteraction(result({ id: newRequestId() }), expected)).toBe(
    false,
  );
  expect(
    advancesInteraction(result({ sessionId: newRequestId() }), expected),
  ).toBe(false);
  expect(
    advancesInteraction(result({ kind: EntityKind.MESSAGE }), expected),
  ).toBe(false);
});

it("keeps the unanswered label for a compact question without retained uncertainty", () => {
  const closed = { ...question, closure: "turn-ended" };
  const value = fixture(closed, true);
  render(value.view());
  expect(screen.getByRole("heading", { name: "Agent question" })).toBeTruthy();
  expect(screen.getByText("Response: Not submitted")).toBeTruthy();
  expect(screen.queryByText("Response: uncertain")).toBeNull();
});

it("renders the uncertain response status in Korean for a lost acknowledgement", async () => {
  const value = fixture(question, true);
  value.questions.mockRejectedValueOnce(
    new ConnectError("Lost acknowledgement", Code.Unavailable),
  );
  render(value.view());
  for (const checkbox of screen.getAllByRole("checkbox", {
    name: "Leave this question unanswered",
  }))
    fireEvent.click(checkbox);
  fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await screen.findByRole("button", { name: "Retry the same answers" });
  await i18n.changeLanguage("ko");
  try {
    await screen.findByText("응답: 불확실");
    expect(screen.queryByText(/제출되지 않음/)).toBeNull();
  } finally {
    await i18n.changeLanguage("en");
  }
});

const families: [string, Record<string, unknown>][] = [
  ["codex", question],
  ["claude", { ...question, claude: { question: true } }],
  ["opencode", { ...question, opencode: { question: true } }],
  ["grok", { ...question, grok: { question: true } }],
];

it("keeps the Agent question heading for retained questions whose newer revision changes or removes the type", () => {
  for (const [family, data] of families) {
    for (const contradiction of [
      { ...data, type: "native-approval", closure: "native-closed" },
      { ...data, type: undefined, response: { state: "uncertain" } },
    ]) {
      const value = fixture(data, true);
      const store = new InteractionPresentationStore();
      store.observe(value.resource);
      const newer = create(ResourceSchema, {
        ...value.resource,
        revision: 10n,
        documentJson: encode(contradiction),
      });
      const projection = store.observe(newer);
      expect(projection.question, family).toBe(true);
      const mounted = render(value.view(newer, projection));
      expect(
        screen.getByRole("heading", { name: "Agent question" }),
        family,
      ).toBeTruthy();
      expect(screen.queryByText("Native approval"), family).toBeNull();
      expect(screen.getByText(/The operation is unavailable/), family).toBeTruthy();
      expect(screen.queryByRole("checkbox"), family).toBeNull();
      expect(screen.queryByRole("textbox"), family).toBeNull();
      expect(screen.queryByRole("button", { name: /Accept|Decline/ })).toBeNull();
      mounted.unmount();
    }
  }
});

it("keeps the original exact retry beside the Agent question heading after a contradictory newer type", async () => {
  const value = fixture(question, true);
  value.questions.mockRejectedValueOnce(
    new ConnectError("Lost acknowledgement", Code.Unavailable),
  );
  const store = new InteractionPresentationStore();
  const original = store.observe(value.resource);
  const mounted = render(value.view(value.resource, original));
  for (const checkbox of screen.getAllByRole("checkbox", {
    name: "Leave this question unanswered",
  }))
    fireEvent.click(checkbox);
  fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await screen.findByRole("button", { name: "Retry the same answers" });
  const newer = create(ResourceSchema, {
    ...value.resource,
    revision: 10n,
    documentJson: encode({ type: "native-approval", closure: "native-closed" }),
  });
  mounted.rerender(value.view(newer, store.observe(newer)));
  expect(screen.getByRole("heading", { name: "Agent question" })).toBeTruthy();
  expect(screen.queryByText("Native approval")).toBeNull();
  expect(screen.queryByRole("checkbox")).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Retry the same answers" }),
  );
  await waitFor(() => expect(value.questions).toHaveBeenCalledTimes(2));
  expect(value.questions.mock.calls[1][0]).toEqual(
    value.questions.mock.calls[0][0],
  );
});

it("keeps the Native approval heading for genuine approvals", () => {
  const approval = { type: "native-approval", closure: "open" };
  const value = fixture(approval, true);
  expect(interactionProjection(value.resource).question).toBe(false);
  render(value.view());
  expect(screen.getByRole("heading", { name: "Native approval" })).toBeTruthy();
  expect(screen.queryByRole("heading", { name: "Agent question" })).toBeNull();
});
