// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { expect, it } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { genericResponseBytes } from "./generic-question-response";
import {
  interactionProjection,
  InteractionPresentation,
  InteractionPresentationStore,
  displayedInteraction,
  completeEmptyInteractionTray,
  missingQuestionReceipts,
  orderedPageEntries,
  PageEntryKind,
  unpagedQuestionReceipts,
} from "./interaction-presentation";
const original = {
  type: "user-question",
  closure: "open",
  questions: {
    questions: [
      {
        id: "original",
        header: "Original",
        text: "Choose",
        other: false,
        secret: false,
        options: [{ label: "A", description: "" }],
      },
    ],
  },
};
const id = newRequestId();
function row(data: Record<string, unknown>, revision = 9n) {
  return create(ResourceSchema, {
    id,
    sessionId: newRequestId(),
    kind: EntityKind.INTERACTION,
    schemaVersion: 1,
    revision,
    documentJson: encode(data),
  });
}
const acceptedResponse = {
  id: newRequestId(),
  state: "accepted",
  input: { answers: { original: ["A"] } },
  accepted_at: "2026-01-02T03:04:05.123456789Z",
  claim: {
    id: newRequestId(),
    job_id: newRequestId(),
    machine_id: newRequestId(),
    instance_id: newRequestId(),
    device_id: newRequestId(),
    claimed_at: "2026-01-02T03:04:06Z",
  },
  delivery: { state: "transmitted", sequence: 10 },
  acceptance: { evidence: "native-question-output", sequence: 11 },
};
const accepted = {
  ...original,
  closure: "native-closed",
  response: acceptedResponse,
};
it("keeps pending, unknown and contradictory responses compact without inferring acceptance", () => {
  for (const state of [
    "queued",
    "claimed",
    "transmitted",
    "uncertain",
    "failed",
    "unknown",
  ])
    for (const closure of ["open", "native-closed", "turn-ended"])
      expect(
        interactionProjection(
          row({ ...original, closure, response: { state } }),
        ).presentation,
      ).toBe(InteractionPresentation.Pending);
  expect(
    interactionProjection(row({ ...accepted, closure: "open" })).presentation,
  ).toBe(InteractionPresentation.Pending);
  expect(
    interactionProjection(row({ ...original, closure: "mystery" }))
      .presentation,
  ).toBe(InteractionPresentation.Pending);
  expect(
    interactionProjection(row({ ...accepted, response: { state: "accepted" } }))
      .presentation,
  ).toBe(InteractionPresentation.Pending);
});
it("keeps accepted questions with a malformed envelope pending and unavailable", () => {
  const withResponse = (patch: Record<string, unknown>) => ({
    ...accepted,
    response: { ...acceptedResponse, ...patch },
  });
  const malformed: Record<string, unknown>[] = [
    { claim: {} },
    { claim: { id: newRequestId() } },
    { claim: { ...acceptedResponse.claim, device_id: "device" } },
    { claim: { ...acceptedResponse.claim, claimed_at: "yesterday" } },
    {
      claim: { ...acceptedResponse.claim, claimed_at: "2026-02-30T03:04:05Z" },
    },
    { id: undefined },
    { id: "not-a-uuid" },
    { input: undefined },
    { input: {} },
    { input: { answers: { original: "A" } } },
    { accepted_at: undefined },
    { accepted_at: "2026-01-02" },
    { delivery: undefined },
    { delivery: { state: "not-sent", sequence: 10 } },
    { delivery: { state: "transmitted", sequence: "10" } },
    { delivery: { state: "transmitted", sequence: -1 } },
    { delivery: { state: "transmitted", sequence: 1.5 } },
    { delivery: { state: "transmitted", sequence: 2 ** 53 } },
    { delivery: { state: "transmitted" } },
    { acceptance: undefined },
    {
      acceptance: { evidence: "native-opencode-question-reply", sequence: 11 },
    },
    { acceptance: { evidence: "native-question-output", sequence: "11" } },
    { acceptance: { evidence: "native-question-output", sequence: 10 } },
    { acceptance: { evidence: "native-question-output", sequence: 9 } },
    { acceptance: { evidence: "native-question-output", sequence: 10.5 } },
    { acceptance: { evidence: "native-question-output", sequence: 2 ** 53 } },
  ];
  for (const patch of malformed) {
    const projection = interactionProjection(row(withResponse(patch)));
    expect(projection.presentation, JSON.stringify(patch)).toBe(
      InteractionPresentation.Pending,
    );
    expect(projection.unavailable, JSON.stringify(patch)).toBe(true);
    expect(displayedInteraction(projection, new Set())).toBe(true);
  }
  const missingCreatedAt = { ...acceptedResponse } as Record<string, unknown>;
  delete missingCreatedAt.accepted_at;
  const missingInput = { ...acceptedResponse } as Record<string, unknown>;
  delete missingInput.input;
  for (const response of [missingCreatedAt, missingInput]) {
    const projection = interactionProjection(row({ ...accepted, response }));
    expect(projection.presentation).toBe(InteractionPresentation.Pending);
    expect(projection.unavailable).toBe(true);
  }
  const complete = interactionProjection(row(accepted));
  expect(complete.presentation).toBe(InteractionPresentation.Retired);
  expect(complete.unavailable).toBe(false);
  expect(displayedInteraction(complete, new Set())).toBe(false);
  expect(displayedInteraction(complete, new Set([complete.mutationKey]))).toBe(
    true,
  );
  expect(
    interactionProjection(
      row(withResponse({ delivery: { state: "uncertain", sequence: 0 } })),
    ).presentation,
  ).toBe(InteractionPresentation.Retired);
});
it("retires an MCP elicitation only with its exact native resolution evidence", () => {
  const elicitation = (evidence: string) => ({
    ...accepted,
    response: {
      ...accepted.response,
      acceptance: { evidence, sequence: 11 },
    },
  });
  expect(
    interactionProjection(
      row(elicitation("native-mcp-elicitation-resolved")),
    ).presentation,
  ).toBe(InteractionPresentation.Retired);
  expect(
    interactionProjection(row(elicitation("transport-delivery"))).presentation,
  ).toBe(InteractionPresentation.Pending);
});
it("retires only accepted or safely closed questions and keeps receipt recovery", () => {
  for (const data of [
    accepted,
    { ...original, closure: "turn-ended" },
    { ...original, closure: "native-closed", response: { state: "canceled" } },
  ]) {
    const projection = interactionProjection(row(data));
    expect(projection.presentation).toBe(InteractionPresentation.Retired);
    expect(displayedInteraction(projection, new Set())).toBe(false);
    expect(
      displayedInteraction(projection, new Set([projection.mutationKey])),
    ).toBe(true);
  }
  expect(
    interactionProjection(
      row({
        ...original,
        closure: "turn-ended",
        response: { state: "canceled", claim: { id: newRequestId() } },
      }),
    ).presentation,
  ).toBe(InteractionPresentation.Pending);
  expect(interactionProjection(row(original)).presentation).toBe(
    InteractionPresentation.Form,
  );
  expect(
    interactionProjection(
      row({ type: "native-approval", closure: "native-closed" }),
    ).presentation,
  ).toBe(InteractionPresentation.Form);
});
it("does not revive original forms through stale list, live or restored payload revisions", () => {
  const store = new InteractionPresentationStore();
  expect(store.observe(row(original)).presentation).toBe(
    InteractionPresentation.Form,
  );
  const queued = store.observe(
    row({ ...original, response: { state: "queued" } }, 10n),
  );
  expect(store.observe(row(original))).toBe(queued);
  const closed = store.observe(row(accepted, 11n));
  expect(store.observe(row(original))).toBe(closed);
  expect(
    store.observe(row({ ...original, response: { state: "queued" } }, 10n)),
  ).toBe(closed);
  expect(closed).not.toHaveProperty("documentJson");
});
it("requires complete successful resident coverage with no requests or operations for emptiness", () => {
  const complete = {
    loaded: true,
    loading: false,
    failed: false,
    continuation: false,
    resident: true,
    displayed: 0,
    pending: 0,
  };
  expect(completeEmptyInteractionTray(complete)).toBe(true);
  for (const gap of [
    { loaded: false },
    { loading: true },
    { failed: true },
    { continuation: true },
    { resident: false },
    { displayed: 1 },
    { pending: 1 },
  ])
    expect(completeEmptyInteractionTray({ ...complete, ...gap })).toBe(false);
});

const familyDocuments: [string, Record<string, unknown>][] = [
  ["claude-answer", { ...original, claude: { question: true } }],
  ["opencode-answer", { ...original, opencode: { question: true } }],
  ["grok-answer", { ...original, grok: { question: true } }],
  ["answer", original],
];
it("keeps the original question identity and mutation key across contradictory newer revisions", () => {
  for (const [family, data] of familyDocuments) {
    const contradictions: Record<string, unknown>[] = [
      // Missing or changed type while the family discriminator is retained.
      { ...data, type: undefined, response: { state: "uncertain" } },
      { ...data, type: "native-approval", closure: "native-closed" },
    ];
    // Dropping the family changes the derived answer key.
    if (family !== "answer")
      contradictions.push({ ...original, response: { state: "uncertain" } });
    for (const contradiction of contradictions) {
      const store = new InteractionPresentationStore();
      const first = store.observe(row(data));
      expect(first.mutationKey).toBe(`${family}:${id}`);
      expect(first.question).toBe(true);
      const next = store.observe(row(contradiction, 10n));
      expect(next.mutationKey).toBe(`${family}:${id}`);
      expect(next.question).toBe(true);
      expect(next.revision).toBe(10n);
      expect(next.unavailable).toBe(true);
      expect(next.presentation).toBe(InteractionPresentation.Pending);
      expect(displayedInteraction(next, new Set())).toBe(true);
      // Stale and equal revisions cannot overwrite the newer metadata.
      expect(store.observe(row(data, 10n))).toBe(next);
      expect(store.observe(row(data, 9n))).toBe(next);
    }
  }
});
it("applies unchanged-identity updates normally and still retires valid closed questions", () => {
  for (const [family, data] of familyDocuments) {
    const store = new InteractionPresentationStore();
    store.observe(row(data));
    const queued = store.observe(
      row({ ...data, response: { state: "queued" } }, 10n),
    );
    expect(queued.mutationKey).toBe(`${family}:${id}`);
    expect(queued.responseState).toBe("queued");
    expect(queued.presentation).toBe(InteractionPresentation.Pending);
  }
  const store = new InteractionPresentationStore();
  store.observe(row(original));
  const closed = store.observe(row(accepted, 10n));
  expect(closed.presentation).toBe(InteractionPresentation.Retired);
  expect(closed.unavailable).toBe(false);
});
it("does not make non-question interactions sticky", () => {
  const store = new InteractionPresentationStore();
  store.observe(row({ type: "native-approval", closure: "open" }));
  const next = store.observe(row(original, 10n));
  expect(next.question).toBe(true);
  expect(next.mutationKey).toBe(`answer:${id}`);
  expect(next.unavailable).toBe(false);
});

const receiptIds = (
  store: InteractionPresentationStore,
  present: string[] = [],
  removed: string[] = [],
  pending: string[] = [],
) =>
  missingQuestionReceipts(
    store.entries(),
    new Set(present),
    new Set(removed),
    new Set(pending),
  ).map(([requestId]) => requestId);
it("keeps non-retired evicted questions as compact receipts without a retained mutation", () => {
  for (const state of [
    "queued",
    "claimed",
    "transmitted",
    "uncertain",
    "failed",
    "unknown",
  ])
    for (const closure of ["open", "native-closed", "turn-ended"]) {
      const store = new InteractionPresentationStore();
      store.observe(row({ ...original, closure, response: { state } }));
      // The response RPC completed, so no mutation intent is retained.
      expect(receiptIds(store)).toEqual([id]);
      expect(receiptIds(store, [id])).toEqual([]);
    }
  const open = new InteractionPresentationStore();
  open.observe(row(original));
  expect(receiptIds(open)).toEqual([id]);
});
it("keeps contradictory revisions compact and drops authoritative removals", () => {
  const store = new InteractionPresentationStore();
  store.observe(row(original));
  store.observe(
    row({ type: "native-approval", closure: "native-closed" }, 10n),
  );
  expect(receiptIds(store)).toEqual([id]);
  expect(receiptIds(store, [], [id])).toEqual([]);
});
it("retires accepted receipts unless a mutation is retained and never revives them from older payloads", () => {
  const store = new InteractionPresentationStore();
  store.observe(row({ ...original, response: { state: "queued" } }, 10n));
  expect(receiptIds(store)).toEqual([id]);
  store.observe(row(accepted, 11n));
  expect(receiptIds(store)).toEqual([]);
  expect(receiptIds(store, [], [], [`answer:${id}`])).toEqual([id]);
  // Restoring older payloads cannot downgrade the retired projection.
  store.observe(row(original, 9n));
  store.observe(row({ ...original, response: { state: "queued" } }, 10n));
  expect(receiptIds(store)).toEqual([]);
});
it("ignores non-question projections", () => {
  const store = new InteractionPresentationStore();
  store.observe(row({ type: "native-approval", closure: "open" }));
  expect(receiptIds(store)).toEqual([]);
});

const entryIds = (
  rows: string[],
  resident: string[],
  receipts: string[],
): string[] =>
  orderedPageEntries(
    rows,
    resident.map((value) => ({ id: value })),
    new Map(receipts.map((value) => [value, value])),
  ).map((entry) => `${entry.kind}:${entry.id}`);
it("orders page entries by authoritative row order regardless of receipt observation order", () => {
  expect(entryIds(["a", "b", "c"], [], ["c", "a"])).toEqual([
    `${PageEntryKind.Receipt}:a`,
    `${PageEntryKind.Receipt}:c`,
  ]);
  expect(entryIds(["a", "b", "c"], ["b"], ["c", "a"])).toEqual([
    `${PageEntryKind.Receipt}:a`,
    `${PageEntryKind.Resident}:b`,
    `${PageEntryKind.Receipt}:c`,
  ]);
});
it("renders one entry per original identity and prefers the resident row", () => {
  expect(entryIds(["a", "a", "b"], ["a"], ["a", "b"])).toEqual([
    `${PageEntryKind.Resident}:a`,
    `${PageEntryKind.Receipt}:b`,
  ]);
  // Removed or retired identities have neither a resident row nor a receipt.
  expect(entryIds(["a", "b"], ["b"], [])).toEqual([
    `${PageEntryKind.Resident}:b`,
  ]);
});
it("leaves only receipts that no reached page owns for the trailing position", () => {
  expect(
    unpagedQuestionReceipts(
      [
        ["live", 1],
        ["old", 2],
        ["newer", 3],
      ] as [string, number][],
      new Set(["old", "newer"]),
    ),
  ).toEqual([["live", 1]]);
});

const projectionOf = (data: Record<string, unknown>) => {
  const projection = interactionProjection(row(data));
  return {
    projection,
    retired: projection.presentation === InteractionPresentation.Retired,
    displayed: displayedInteraction(projection, new Set()),
  };
};
const withResponse = (patch: Record<string, unknown>) => ({
  ...accepted,
  response: { ...acceptedResponse, ...patch },
});
const withClaim = (patch: Record<string, unknown>) =>
  withResponse({ claim: { ...acceptedResponse.claim, ...patch } });
const expectRejected = (data: Record<string, unknown>, label: string) => {
  const { projection, retired, displayed } = projectionOf(data);
  expect(retired, label).toBe(false);
  expect(projection.presentation, label).toBe(InteractionPresentation.Pending);
  expect(projection.unavailable, label).toBe(true);
  expect(displayed, label).toBe(true);
  // A matching retained mutation keeps recovery visible independently.
  expect(
    displayedInteraction(projection, new Set([projection.mutationKey])),
  ).toBe(true);
};
const expectRetired = (data: Record<string, unknown>, label: string) => {
  const { projection, retired, displayed } = projectionOf(data);
  expect(retired, label).toBe(true);
  expect(projection.unavailable, label).toBe(false);
  expect(displayed, label).toBe(false);
  expect(
    displayedInteraction(projection, new Set([projection.mutationKey])),
  ).toBe(true);
};

it("rejects normalized or malformed accepted_at and claimed_at timestamps", () => {
  const invalid = [
    "2026-02-30T03:04:05Z",
    "2026-02-29T03:04:05Z",
    "2026-04-31T03:04:05Z",
    "2026-01-02T24:00:00Z",
    "2026-01-02T24:00:00.000Z",
    "2026-01-02",
    "yesterday",
    "2026-13-02T03:04:05Z",
    "2026-00-02T03:04:05Z",
    "2026-01-00T03:04:05Z",
    "2026-01-02T25:00:00Z",
    "2026-01-02T03:60:05Z",
    "2026-01-02T03:04:60Z",
    "2026-01-02T03:04:05",
    "2026-01-02T03:04:05+24:00",
    "2026-01-02T03:04:05+01:60",
    "2026-01-02T03:04:05.1234567890Z",
  ];
  for (const value of invalid) {
    expectRejected(
      withResponse({ accepted_at: value }),
      `accepted_at ${value}`,
    );
    expectRejected(withClaim({ claimed_at: value }), `claimed_at ${value}`);
  }
});
it("retires valid leap days, UTC, offsets and 1-9 digit fractions", () => {
  const valid = [
    "2024-02-29T03:04:05Z",
    "2000-02-29T23:59:59Z",
    "2026-01-02T03:04:05Z",
    "2026-01-02T03:04:05+09:00",
    "2026-01-02T03:04:05-05:30",
    "2026-04-30T03:04:05+23:59",
    ...[1, 2, 3, 4, 5, 6, 7, 8, 9].map(
      (digits) => `2026-01-02T03:04:05.${"123456789".slice(0, digits)}Z`,
    ),
  ];
  for (const value of valid) {
    expectRetired(withResponse({ accepted_at: value }), `accepted_at ${value}`);
    expectRetired(withClaim({ claimed_at: value }), `claimed_at ${value}`);
  }
});

const question = (patch: Record<string, unknown> = {}) => ({
  id: "original",
  header: "Original",
  text: "Choose",
  other: false,
  secret: false,
  options: [
    { label: "A", description: "" },
    { label: "B", description: "second" },
  ],
  ...patch,
});
const accepting = (
  questions: Record<string, unknown>[],
  answers: Record<string, unknown>,
) => ({
  ...accepted,
  questions: { questions },
  response: { ...acceptedResponse, input: { answers } },
});
const bytes = (length: number, char = "x") => char.repeat(length);

it("does not retire generic accepted questions with answers that mismatch the original questions", () => {
  const two = [question(), question({ id: "second", options: [] })];
  const cases: [string, Record<string, unknown>[], Record<string, unknown>][] =
    [
      ["empty answers", [question()], {}],
      ["foreign key", [question()], { foreign: [] }],
      ["foreign extra key", [question()], { original: [], foreign: [] }],
      ["missing second question", two, { original: ["A"] }],
      ["replaced key", two, { original: ["A"], foreign: ["x"] }],
      ["unoffered label", [question()], { original: ["C"] }],
      ["case-different label", [question()], { original: ["a"] }],
      ["duplicate answers", [question()], { original: ["A", "A"] }],
      [
        "duplicate custom answers",
        [question({ options: [], other: true })],
        { original: ["x", "x"] },
      ],
      ["null list", [question()], { original: null }],
      ["non-array list", [question()], { original: "A" }],
      ["non-string element", [question()], { original: [1] }],
      ["null element", [question()], { original: [null] }],
      ["blank answer", [question({ other: true })], { original: ["  "] }],
      ["empty answer", [question({ other: true })], { original: [""] }],
      ["NUL answer", [question({ other: true })], { original: ["a\0b"] }],
      [
        "lone surrogate answer",
        [question({ other: true })],
        { original: ["\ud800"] },
      ],
      [
        "129 distinct answers without options",
        [question({ options: [] })],
        { original: Array.from({ length: 129 }, (_, i) => `v${i}`) },
      ],
      [
        "answer over the byte bound",
        [question({ options: [], other: true })],
        { original: [bytes(256 * 1024 + 1)] },
      ],
      [
        "multibyte answer over the byte bound",
        [question({ options: [], other: true })],
        { original: ["\u20ac".repeat(Math.floor((256 * 1024) / 3) + 1)] },
      ],
      [
        "complete response over the bound",
        [question({ options: [], other: true })],
        {
          original: Array.from({ length: 128 }, (_, i) =>
            `${i}`.padEnd(2049, "x"),
          ),
        },
      ],
      [
        "secret original question",
        [question({ secret: true, options: [], other: true })],
        { original: ["hidden"] },
      ],
      [
        "secret original question alongside a plain one",
        [question(), question({ id: "s", secret: true })],
        { original: ["A"], s: [] },
      ],
    ];
  for (const [label, questions, answers] of cases)
    expectRejected(accepting(questions, answers), label);
});
it("rejects accepted generic questions whose original request is invalid", () => {
  const answers = { original: [] };
  const requests: [string, Record<string, unknown>[]][] = [
    ["no questions", []],
    [
      "129 questions",
      Array.from({ length: 129 }, (_, i) => question({ id: `q${i}` })),
    ],
    ["duplicate ids", [question(), question()]],
    ["blank id", [question({ id: " " })]],
    ["oversized id", [question({ id: bytes(129) })]],
    ["missing header", [question({ header: undefined })]],
    ["missing other", [question({ other: undefined })]],
    ["missing secret", [question({ secret: undefined })]],
    [
      "129 options",
      [
        question({
          options: Array.from({ length: 129 }, (_, i) => ({
            label: `o${i}`,
            description: "",
          })),
        }),
      ],
    ],
    [
      "duplicate option labels",
      [
        question({
          options: [
            { label: "A", description: "" },
            { label: "A", description: "" },
          ],
        }),
      ],
    ],
    [
      "blank option label",
      [question({ options: [{ label: " ", description: "" }] })],
    ],
    ["missing option description", [question({ options: [{ label: "A" }] })]],
    ["non-array options", [question({ options: "A" })]],
  ];
  for (const [label, questions] of requests)
    expectRejected(accepting(questions, answers), label);
  expectRejected(
    { ...accepted, questions: undefined },
    "missing questions request",
  );
});
it("retires generic accepted questions with valid empty, offered and permitted custom answers", () => {
  const cases: [string, Record<string, unknown>[], Record<string, unknown>][] =
    [
      ["explicit empty array", [question()], { original: [] }],
      ["offered label", [question()], { original: ["B"] }],
      ["multiple offered labels", [question()], { original: ["A", "B"] }],
      [
        "custom text when other is permitted",
        [question({ other: true })],
        { original: ["something else"] },
      ],
      [
        "free text without options",
        [question({ options: [], other: false })],
        { original: ["free"] },
      ],
      [
        "absent options as free text",
        [question({ options: undefined })],
        { original: ["free"] },
      ],
      [
        "128 distinct answers",
        [question({ options: [] })],
        { original: Array.from({ length: 128 }, (_, i) => `v${i}`) },
      ],
      [
        "two questions",
        [question(), question({ id: "second", options: [] })],
        { original: ["A"], second: [] },
      ],
      [
        "answer at the byte bound",
        [question({ options: [], other: true })],
        // 2 bytes of quotes and the fixed wrapper reduce the exact fit.
        { original: [bytes(256 * 1024 - 100)] },
      ],
      [
        "unicode whitespace is not blank when mixed with text",
        [question({ options: [], other: true })],
        { original: ["\u00a0x\u3000"] },
      ],
    ];
  for (const [label, questions, answers] of cases)
    expectRetired(accepting(questions, answers), label);
});
it("counts Go JSON escaping toward the complete response bound", () => {
  const free = [question({ options: [], other: true })];
  // "<" is one byte raw but six bytes once Go HTML-escapes it, so this
  // 100000-character answer expands to 600000 bytes and exceeds the bound.
  expectRejected(accepting(free, { original: ["<".repeat(100000)] }), "<");
  expectRejected(
    accepting(free, { original: ["\u2028".repeat(50000)] }),
    "u2028",
  );
  expectRetired(accepting(free, { original: ["<".repeat(40000)] }), "40000 <");
});
it("keeps invalid generic accepted rows compact through eviction and receipt projection", () => {
  const store = new InteractionPresentationStore();
  const invalid = accepting([question()], { original: ["B", "B"] });
  const projection = store.observe(row(invalid, 11n));
  expect(projection.presentation).toBe(InteractionPresentation.Pending);
  expect(projection.unavailable).toBe(true);
  // Payload evicted: the compact receipt is retained without a mutation.
  const receipts = missingQuestionReceipts(
    store.entries(),
    new Set(),
    new Set(),
    new Set(),
  );
  expect(receipts.map(([requestId]) => requestId)).toEqual([id]);
  // An older valid payload cannot revive or retire it.
  const older = store.observe(
    row(accepting([question()], { original: ["A"] }), 10n),
  );
  expect(older).toBe(projection);
  expect(
    missingQuestionReceipts(store.entries(), new Set(), new Set(), new Set()),
  ).toHaveLength(1);
  // A valid higher revision retires normally, and retained mutations stay visible.
  const fixed = store.observe(
    row(accepting([question()], { original: ["A"] }), 12n),
  );
  expect(fixed.presentation).toBe(InteractionPresentation.Retired);
  expect(
    missingQuestionReceipts(store.entries(), new Set(), new Set(), new Set()),
  ).toHaveLength(0);
  expect(
    missingQuestionReceipts(
      store.entries(),
      new Set(),
      new Set(),
      new Set([fixed.mutationKey]),
    ),
  ).toHaveLength(1);
});

it("matches the Go encoding/json byte size of the complete response", () => {
  // Reference: Go json.Marshal of {Answers: {"id<": [s, "b"], "q": []}} is 108.
  const s = '"\\\b\f\n\r\t\x01\x1f\x7f<>&\u2028\u2029\u00e9\u20ac\u{1F600} z';
  const ids = ["id<", "q"].map((qid) => ({
    id: qid,
    other: true,
    secret: false,
    options: [],
  }));
  expect(genericResponseBytes({ "id<": [s, "b"], q: [] }, ids)).toBe(108);
  const free = [question({ options: [], other: true })];
  // Wrapper, key and quotes add 29 bytes around the answer text.
  expectRetired(accepting(free, { original: [bytes(256 * 1024 - 29)] }), "fit");
  expectRejected(
    accepting(free, { original: [bytes(256 * 1024 - 28)] }),
    "one over",
  );
});
