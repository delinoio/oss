// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { expect, it } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import {
  InteractionPresentation,
  InteractionPresentationStore,
  missingQuestionReceipts,
  questionMutationKey,
} from "./interaction-presentation";
import { PaginationChain, type PaginationReader } from "./scroll-pagination";

const sessionId = newRequestId();
const question = {
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
const ids = [0, 1, 2, 3].map(() => newRequestId());
const resource = (
  index: number,
  revision: bigint,
  data: Record<string, unknown>,
): Resource =>
  create(ResourceSchema, {
    id: ids[index],
    sessionId,
    kind: EntityKind.INTERACTION,
    schemaVersion: 1,
    revision,
    documentJson: encode(data),
  });

/** Four one-row pages; the reader serves the current rows of `current`. */
function fixture(initial: Resource[]) {
  const current = [...initial];
  const store = new InteractionPresentationStore();
  const reader: PaginationReader<
    { id: string; revision: bigint },
    Resource
  > = async (token) => {
    const index = Number(token || 0);
    const row = current[index];
    return {
      rows: [{ id: row.id, revision: row.revision }],
      payload: [row],
      nextPageToken: index < current.length - 1 ? String(index + 1) : "",
      accepted: () => store.observe(row),
    };
  };
  const chain = new PaginationChain<
    { id: string; revision: bigint },
    Resource
  >();
  chain.activate();
  const receipts = (pending: ReadonlySet<string> = new Set()) => {
    const resident = new Set(
      chain
        .getSnapshot()
        .payloadPages.flatMap((page) => page.payload.map((row) => row.id)),
    );
    return missingQuestionReceipts(
      store.entries(),
      resident,
      new Set(),
      pending,
    );
  };
  return { current, store, chain, reader, receipts };
}

async function reachFourPages(f: ReturnType<typeof fixture>) {
  await f.chain.refresh(f.reader);
  for (let index = 0; index < 3; index++) await f.chain.append(f.reader);
}

const queued = { ...question, response: { state: "queued" } };
const others = (revision = 1n) =>
  [1, 2, 3].map((index) => resource(index, revision, question));

it("retires an evicted accepted question from a list refresh alone", async () => {
  const f = fixture([resource(0, 1n, queued), ...others()]);
  await reachFourPages(f);
  expect(f.chain.getSnapshot().payloadPages.map((page) => page.token)).toEqual([
    "1",
    "2",
    "3",
  ]);
  expect(f.receipts().map(([id]) => id)).toEqual([ids[0]]);
  f.current[0] = resource(0, 2n, {
    ...question,
    closure: "native-closed",
    response: acceptedResponse,
  });
  await f.chain.refresh(f.reader);
  const projection = [...f.store.entries()].find(([id]) => id === ids[0])![1];
  expect(projection.revision).toBe(2n);
  expect(projection.presentation).toBe(InteractionPresentation.Retired);
  expect(f.receipts()).toEqual([]);
  expect(f.chain.getSnapshot().payloadPages).toHaveLength(3);
  expect(f.chain.getSnapshot().payloadPages.map((page) => page.token)).toEqual([
    "1",
    "2",
    "3",
  ]);
});

it("retires an evicted confirmed-closed question without a response", async () => {
  const f = fixture([resource(0, 1n, queued), ...others()]);
  await reachFourPages(f);
  f.current[0] = resource(0, 2n, { ...question, closure: "turn-ended" });
  await f.chain.refresh(f.reader);
  expect(f.receipts()).toEqual([]);
});

it("keeps retained mutations and unresolved or invalid observations visible", async () => {
  const f = fixture([resource(0, 1n, queued), ...others()]);
  await reachFourPages(f);
  f.current[0] = resource(0, 2n, {
    ...question,
    closure: "native-closed",
    response: acceptedResponse,
  });
  await f.chain.refresh(f.reader);
  // A retained original mutation keeps the retired question compact.
  expect(
    f.receipts(new Set([questionMutationKey(ids[0], question)])),
  ).toHaveLength(1);
  // Unresolved delivery stays visible.
  f.current[0] = resource(0, 3n, {
    ...question,
    closure: "native-closed",
    response: { state: "uncertain" },
  });
  await f.chain.refresh(f.reader);
  expect(f.receipts().map(([id]) => id)).toEqual([ids[0]]);
  // A contradictory newer document stays visible, unavailable and sticky.
  f.current[0] = resource(0, 4n, { type: "message", text: "other" });
  await f.chain.refresh(f.reader);
  const [, projection] = f.receipts()[0];
  expect(projection.revision).toBe(4n);
  expect(projection.unavailable).toBe(true);
  expect(projection.question).toBe(true);
});

it("never lets older, equal or malformed observations overwrite newer metadata", async () => {
  const f = fixture([resource(0, 1n, queued), ...others()]);
  await reachFourPages(f);
  f.current[0] = resource(0, 3n, {
    ...question,
    closure: "native-closed",
    response: acceptedResponse,
  });
  await f.chain.refresh(f.reader);
  expect(f.receipts()).toEqual([]);
  // Older revision with a form document cannot revive the answered question.
  f.current[0] = resource(0, 2n, question);
  await f.chain.refresh(f.reader);
  // Equal revision with a different document is also ignored.
  f.current[0] = resource(0, 3n, queued);
  await f.chain.refresh(f.reader);
  const projection = [...f.store.entries()].find(([id]) => id === ids[0])![1];
  expect(projection.revision).toBe(3n);
  expect(projection.presentation).toBe(InteractionPresentation.Retired);
  expect(f.receipts()).toEqual([]);
});

it("does not observe canceled refreshes", async () => {
  const f = fixture([resource(0, 1n, queued), ...others()]);
  await reachFourPages(f);
  f.current[0] = resource(0, 2n, {
    ...question,
    closure: "native-closed",
    response: acceptedResponse,
  });
  let release!: (value: Awaited<ReturnType<typeof f.reader>>) => void;
  const pending = f.chain.refresh(
    () =>
      new Promise((resolve) => {
        release = resolve;
      }),
  );
  f.chain.suspend();
  f.chain.activate();
  release(await f.reader("", new AbortController().signal));
  await pending;
  const projection = [...f.store.entries()].find(([id]) => id === ids[0])![1];
  // The canceled read's accepted callback never ran; only the creation read
  // metadata is present.
  expect(projection.revision).toBe(1n);
  expect(projection.presentation).toBe(InteractionPresentation.Pending);
});
