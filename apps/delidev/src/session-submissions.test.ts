// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { expect, it } from "vitest";
import {
  EntityKind,
  EnqueueInputRequestSchema,
  EnqueueInputResponseSchema,
  ResourceSchema,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode, Mode } from "./documents";
import { RetainedMutationPhase } from "./mutation";
import {
  acknowledgeSessionSubmission,
  SessionSubmissions,
  SubmissionPhase,
} from "./session-submissions";
import { sessionInputReceipt } from "./test-session-input";

function fixture(prompt = "Duplicate text") {
  const sessionId = newRequestId(),
    requestId = newRequestId();
  const session = create(ResourceSchema, {
    id: sessionId,
    sessionId,
    kind: EntityKind.SESSION,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({}),
  });
  const request = create(EnqueueInputRequestSchema, {
    requestId,
    sessionId,
    documentJson: encode({ prompt, mode: Mode.Execute }),
  });
  const receipt = create(
    EnqueueInputResponseSchema,
    sessionInputReceipt(session, request),
  );
  const store = new SessionSubmissions();
  store.freeze(requestId, sessionId, prompt, Mode.Execute, 0);
  const key = `enqueue:${sessionId}`;
  const input = receipt.change!.input!;
  const queue = (delivery: string, revision: bigint, text = prompt) =>
    create(ResourceSchema, {
      ...input,
      revision,
      documentJson: encode({ prompt: text, mode: Mode.Execute, delivery }),
    });
  const native = create(ResourceSchema, {
    id: newRequestId(),
    sessionId,
    kind: EntityKind.MESSAGE,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({
      role: "user",
      state: "complete",
      input_id: input.id,
      text: prompt,
    }),
  });
  return { store, sessionId, requestId, request, receipt, key, queue, native };
}
it("keeps admission distinct from native acceptance and reconciles by input identity", () => {
  const f = fixture();
  expect(f.store.snapshot()[0].phase).toBe(SubmissionPhase.Sending);
  f.store.accepted(f.key, f.request, f.receipt);
  expect(f.store.snapshot()[0].phase).toBe(SubmissionPhase.Queued);
  f.store.observe(f.sessionId, [f.queue("claimed", 2n)]);
  expect(f.store.snapshot()[0].phase).toBe(SubmissionPhase.Claimed);
  f.store.observe(f.sessionId, [f.queue("accepted", 3n)]);
  expect(f.store.snapshot()[0].phase).toBe(SubmissionPhase.Accepted);
  expect(f.store.snapshot()[0].native).toBe(false);
  f.store.observe(f.sessionId, [f.native]);
  expect(f.store.snapshot()[0]).toMatchObject({
    native: true,
    prompt: "",
    attachments: [],
  });
  f.store.accepted(f.key, f.request, f.receipt);
  expect(f.store.snapshot()[0].native).toBe(true);
});
it.each([true, false])(
  "reconciles stream before receipt=%s and never matches duplicate prompt text",
  (first) => {
    const f = fixture();
    const otherRequest = newRequestId();
    f.store.freeze(
      otherRequest,
      f.sessionId,
      "Duplicate text",
      Mode.Execute,
      0,
    );
    if (first) f.store.observe(f.sessionId, [f.native]);
    f.store.accepted(f.key, f.request, f.receipt);
    if (!first) f.store.observe(f.sessionId, [f.native]);
    expect(f.store.snapshot().map((row) => row.native)).toEqual([true, false]);
  },
);
it("keeps observed edits and removals ahead of stale receipt and paged revisions", () => {
  const f = fixture();
  f.store.observe(f.sessionId, [
    f.queue("queued", 5n, "Edited authoritative input"),
  ]);
  f.store.accepted(f.key, f.request, f.receipt);
  expect(f.store.snapshot()[0]).toMatchObject({
    prompt: "Edited authoritative input",
    queueRevision: 5n,
  });
  f.store.observe(f.sessionId, [
    f.queue("removed", 6n, "Removed authoritative input"),
  ]);
  f.store.observe(f.sessionId, [f.queue("queued", 2n)]);
  f.store.accepted(f.key, f.request, f.receipt);
  expect(f.store.snapshot()[0]).toMatchObject({
    phase: SubmissionPhase.Removed,
    prompt: "Removed authoritative input",
    queueRevision: 6n,
  });
});
it("retains uncertainty and rejects missing/foreign/malformed acknowledgments", () => {
  const f = fixture();
  for (const invalid of [
    create(EnqueueInputResponseSchema),
    create(EnqueueInputResponseSchema, {
      change: { ...f.receipt.change!, requestId: newRequestId() },
    }),
    create(EnqueueInputResponseSchema, {
      change: {
        ...f.receipt.change!,
        input: { ...f.receipt.change!.input!, sessionId: newRequestId() },
      },
    }),
  ]) {
    expect(acknowledgeSessionSubmission(invalid, f.request)).toBe(false);
    f.store.accepted(f.key, f.request, invalid);
  }
  f.store.outcome(f.key, f.request, RetainedMutationPhase.Uncertain);
  expect(f.store.snapshot()[0].phase).toBe(SubmissionPhase.Uncertain);
  f.store.outcome(f.key, f.request, RetainedMutationPhase.Sending);
  expect(f.store.snapshot()[0].phase).toBe(SubmissionPhase.Sending);
  f.store.outcome(f.key, f.request, RetainedMutationPhase.Rejected);
  expect(f.store.snapshot()[0].phase).toBe(SubmissionPhase.Rejected);
});
it("ignores foreign sessions, unknown deliveries and replacement-connection results", () => {
  const f = fixture();
  f.store.accepted(f.key, f.request, f.receipt);
  f.store.observe(newRequestId(), [f.queue("accepted", 3n)]);
  expect(f.store.snapshot()[0].phase).toBe(SubmissionPhase.Queued);
  f.store.observe(f.sessionId, [f.queue("future-state", 4n)]);
  expect(f.store.snapshot()[0].phase).toBe(SubmissionPhase.Uncertain);
  f.store.dispose();
  f.store.accepted(f.key, f.request, f.receipt);
  f.store.observe(f.sessionId, [f.native]);
  expect(f.store.snapshot()).toEqual([]);
  expect(
    f.store.freeze(newRequestId(), f.sessionId, "late", Mode.Execute, 0),
  ).toBe(false);
});
it("bounds retained prompts atomically and retires only native-settled records", () => {
  const store = new SessionSubmissions(),
    sessionId = newRequestId();
  for (let index = 0; index < 16; index++)
    store.freeze(
      newRequestId(),
      sessionId,
      "x".repeat(256 << 10),
      Mode.Execute,
      0,
    );
  expect(() =>
    store.freeze(newRequestId(), sessionId, "overflow", Mode.Execute, 0),
  ).toThrow();
  expect(store.snapshot()).toHaveLength(16);
});

it("keeps failed original observations unavailable until matching current or newer evidence", () => {
  const f = fixture();
  f.store.accepted(f.key, f.request, f.receipt);
  f.store.observationFailed(f.receipt.change!.input!.id);
  expect(f.store.snapshot()[0].observationUnavailable).toBe(true);
  f.store.observe(f.sessionId, [f.queue("queued", 1n)]);
  expect(f.store.snapshot()[0].observationUnavailable).toBe(true);
  f.store.observe(
    f.sessionId,
    [f.queue("queued", 1n, "Changed without revision")],
    true,
  );
  expect(f.store.snapshot()[0]).toMatchObject({
    observationUnavailable: true,
    prompt: "Duplicate text",
  });
  f.store.observe(f.sessionId, [f.queue("queued", 1n)], true);
  expect(f.store.snapshot()[0].observationUnavailable).toBe(false);
  f.store.observationFailed(f.receipt.change!.input!.id);
  f.store.observe(f.sessionId, [f.queue("queued", 2n, "Current edit")]);
  expect(f.store.snapshot()[0]).toMatchObject({
    observationUnavailable: false,
    prompt: "Current edit",
  });
});

function original(prompt = "First prompt") {
  const sessionId = newRequestId(),
    inputId = newRequestId();
  const input = (
    revision: bigint,
    delivery = "queued",
    text = prompt,
    sequence = 1,
    owner = sessionId,
  ) =>
    create(ResourceSchema, {
      id: inputId,
      sessionId: owner,
      kind: EntityKind.QUEUE,
      revision,
      schemaVersion: 1,
      documentJson: encode({
        prompt: text,
        mode: Mode.Execute,
        delivery,
        sequence,
      }),
    });
  const native = create(ResourceSchema, {
    id: newRequestId(),
    sessionId,
    kind: EntityKind.MESSAGE,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({
      role: "user",
      state: "complete",
      input_id: inputId,
      text: prompt,
    }),
  });
  return { store: new SessionSubmissions(), sessionId, inputId, input, native };
}
it("registers only the verified first input and reconciles it once by input identity", () => {
  const f = original();
  expect(f.store.registerOriginal(f.sessionId, f.input(1n), f.inputId)).toBe(
    true,
  );
  expect(f.store.registerOriginal(f.sessionId, f.input(1n), f.inputId)).toBe(
    false,
  );
  expect(f.store.snapshot()).toHaveLength(1);
  expect(f.store.snapshot()[0]).toMatchObject({
    original: true,
    prompt: "First prompt",
    phase: SubmissionPhase.Queued,
  });
  f.store.observe(f.sessionId, [f.native]);
  expect(f.store.snapshot()[0]).toMatchObject({ native: true, prompt: "" });
});
it("rejects foreign, malformed and non-first original evidence", () => {
  const f = original();
  expect(
    f.store.registerOriginal(f.sessionId, f.input(1n, "queued", "x", 2)),
  ).toBe(false);
  expect(
    f.store.registerOriginal(
      f.sessionId,
      f.input(1n, "queued", "x", 1, newRequestId()),
    ),
  ).toBe(false);
  expect(
    f.store.registerOriginal(f.sessionId, f.input(1n), newRequestId()),
  ).toBe(false);
  expect(f.store.registerOriginal(f.sessionId, f.native)).toBe(false);
  expect(f.store.registerOriginal(f.sessionId, undefined)).toBe(false);
  expect(f.store.snapshot()).toHaveLength(0);
});
it("evicts a settled native mapping before the retained-record bound rejects an original", () => {
  // Fill a store to the bound with one settled native mapping among live rows.
  const g = original();
  expect(g.store.registerOriginal(g.sessionId, g.input(1n), g.inputId)).toBe(
    true,
  );
  g.store.observe(g.sessionId, [g.native]);
  for (let index = 0; index < 999; index++)
    g.store.freeze(newRequestId(), g.sessionId, "x", Mode.Execute, 0);
  expect(g.store.snapshot()).toHaveLength(1000);
  const next = original();
  expect(
    g.store.registerOriginal(next.sessionId, next.input(1n), next.inputId),
  ).toBe(true);
  expect(g.store.snapshot()).toHaveLength(1000);
  expect(g.store.snapshot().some((row) => row.native)).toBe(false);
  // Without a settled mapping the bound still rejects.
  const last = original();
  expect(
    g.store.registerOriginal(last.sessionId, last.input(1n), last.inputId),
  ).toBe(false);
});
it("does not dedupe equal text under a different input identity", () => {
  const f = original("Same text");
  f.store.registerOriginal(f.sessionId, f.input(1n));
  const other = create(ResourceSchema, {
    ...f.native,
    documentJson: encode({
      role: "user",
      state: "complete",
      input_id: newRequestId(),
      text: "Same text",
    }),
  });
  f.store.observe(f.sessionId, [other]);
  expect(f.store.snapshot()[0].native).toBe(false);
});
it("keeps edits and removals ahead of stale receipts and never resurrects removed content", () => {
  const f = original();
  f.store.registerOriginal(f.sessionId, f.input(1n));
  f.store.observe(f.sessionId, [f.input(3n, "queued", "Edited prompt")]);
  expect(f.store.snapshot()[0].prompt).toBe("Edited prompt");
  f.store.registerOriginal(f.sessionId, f.input(1n));
  expect(f.store.snapshot()[0].prompt).toBe("Edited prompt");
  f.store.observe(f.sessionId, [f.input(4n, "removed", "Edited prompt")]);
  expect(f.store.snapshot()[0]).toMatchObject({
    phase: SubmissionPhase.Removed,
    prompt: "",
    attachmentCount: 0,
  });
  f.store.registerOriginal(f.sessionId, f.input(2n, "queued", "Stale"));
  f.store.observe(f.sessionId, [f.input(3n, "queued", "Stale")]);
  expect(f.store.snapshot()[0]).toMatchObject({
    phase: SubmissionPhase.Removed,
    prompt: "",
  });
});
it("discards original projections when the store is disposed", () => {
  const f = original();
  f.store.registerOriginal(f.sessionId, f.input(1n));
  f.store.dispose();
  expect(f.store.snapshot()).toHaveLength(0);
  expect(f.store.registerOriginal(f.sessionId, f.input(1n))).toBe(false);
});
