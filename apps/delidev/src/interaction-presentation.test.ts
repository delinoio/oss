// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { expect, it } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import {
  interactionProjection,
  InteractionPresentation,
  InteractionPresentationStore,
  displayedInteraction,
  completeEmptyInteractionTray,
} from "./interaction-presentation";
const original = {
  type: "user-question",
  closure: "open",
  questions: {
    questions: [{ id: "original", text: "Choose", options: [] }],
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
const accepted = {
  ...original,
  closure: "native-closed",
  response: {
    state: "accepted",
    claim: { id: newRequestId() },
    delivery: { state: "transmitted", sequence: 10 },
    acceptance: { evidence: "native-question-output", sequence: 11 },
  },
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
