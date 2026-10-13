// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema } from "@delinoio/delidev-api-client";
import { expect, it, vi } from "vitest";
import { encode } from "./documents";
import {
  TerminalPresentation,
  TerminalPresentationRejection,
  terminalFallback,
} from "./terminal-presentation";
const resource = (revision: bigint, data: object) =>
  create(ResourceSchema, {
    kind: EntityKind.TERMINAL,
    id: "original",
    sessionId: "parent",
    schemaVersion: 1,
    revision,
    documentJson: encode(data),
  });
it("keeps monotonic original-identity tombstones through stale and newer history restoration", () => {
  const view = new TerminalPresentation();
  expect(view.observe("parent", resource(5n, { state: "running" }))).toBe(
    false,
  );
  expect(
    view.observe(
      "parent",
      resource(4n, { state: "exited", cleanup_verified: true }),
    ),
  ).toBe(false);
  expect(
    view.observe(
      "parent",
      resource(6n, { state: "exited", cleanup_verified: true }),
    ),
  ).toBe(true);
  expect(view.observe("parent", resource(7n, { state: "running" }))).toBe(
    false,
  );
  expect(view.hidden("original")).toBe(true);
});
it.each([
  { state: "exited" },
  { state: "exited", cleanup_verified: "true" },
  { state: "closed", cleanup_verified: true },
  { state: "running", cleanup_verified: true },
  {
    state: "exited",
    cleanup_verified: true,
    pending: { request_id: "original" },
  },
  { state: "exited", cleanup_verified: true, pending: "malformed" },
  { state: "exited", cleanup_verified: true, pending: {} },
  { state: "exited", cleanup_verified: true, pending: [] },
  { state: "exited", cleanup_verified: true, pending: 0 },
  {
    state: "exited",
    cleanup_verified: true,
    pending: { action: "close", request_id: "original" },
  },
])("retains incomplete or unsettled cleanup %j", (data) => {
  const view = new TerminalPresentation();
  expect(view.observe("parent", resource(4n, data))).toBe(false);
  expect(view.hidden("original")).toBe(false);
});
it.each([{}, { pending: null }])(
  "treats missing or null pending as no operation %j",
  (extra) => {
    const view = new TerminalPresentation();
    expect(
      view.observe(
        "parent",
        resource(4n, { state: "exited", cleanup_verified: true, ...extra }),
      ),
    ).toBe(true);
    expect(view.hidden("original")).toBe(true);
  },
);
it("rejects stale revisions after a pending empty object is retained", () => {
  const view = new TerminalPresentation();
  expect(
    view.observe(
      "parent",
      resource(5n, { state: "exited", cleanup_verified: true, pending: {} }),
    ),
  ).toBe(false);
  expect(
    view.observe(
      "parent",
      resource(4n, { state: "exited", cleanup_verified: true }),
    ),
  ).toBe(false);
  expect(view.hidden("original")).toBe(false);
});
it("never tombstones a contradictory equal-revision exit after running", () => {
  const view = new TerminalPresentation();
  expect(view.observe("parent", resource(5n, { state: "running" }))).toBe(
    false,
  );
  expect(
    view.observe(
      "parent",
      resource(5n, { state: "exited", cleanup_verified: true }),
    ),
  ).toBe(false);
  expect(view.hidden("original")).toBe(false);
  expect(view.observe("parent", resource(5n, { state: "running" }))).toBe(
    false,
  );
  expect(
    view.observe(
      "parent",
      resource(6n, { state: "exited", cleanup_verified: true }),
    ),
  ).toBe(true);
});
it.each([
  { state: "running" },
  { state: "exited" },
  { state: "exited", cleanup_verified: false },
  { state: "exited", cleanup_verified: true },
  {
    state: "exited",
    cleanup_verified: true,
    pending: { action: "close", request_id: "original" },
  },
])("rejects equal-revision evidence that changes from exited %j", (change) => {
  const view = new TerminalPresentation();
  expect(
    view.observe(
      "parent",
      resource(5n, { state: "exited", cleanup_verified: true, pending: {} }),
    ),
  ).toBe(false);
  expect(view.observe("parent", resource(5n, change))).toBe(false);
  expect(view.hidden("original")).toBe(false);
});
it("logs a content-free diagnostic only for equal-revision contradictions", () => {
  const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  try {
    const view = new TerminalPresentation(),
      pending = { request_id: "secret-request" };
    // Ordinary running, unsettled, stale and identical-replay observations
    // stay silent.
    view.observe("parent", resource(5n, { state: "running" }));
    view.observe("parent", resource(5n, { state: "running" }));
    view.observe("parent", resource(4n, { state: "running" }));
    expect(warn).not.toHaveBeenCalled();
    expect(
      view.observe(
        "parent",
        resource(5n, { state: "exited", cleanup_verified: true, pending }),
      ),
    ).toBe(false);
    expect(warn).toHaveBeenCalledTimes(1);
    expect(warn).toHaveBeenCalledWith({
      event: "terminal-presentation-observation-rejected",
      category: TerminalPresentationRejection.RevisionContradiction,
      terminalId: "original",
      revision: "5",
    });
    expect(JSON.stringify(warn.mock.calls)).not.toContain("secret-request");
    expect(view.hidden("original")).toBe(false);
  } finally {
    warn.mockRestore();
  }
});
const nested = (depth: number, leaf: unknown = "leaf"): unknown => {
  let value = leaf;
  for (let level = 0; level < depth; level++) value = { next: [value] };
  return value;
};
it("keeps bounded nested pending evidence comparable and unsettled", () => {
  const view = new TerminalPresentation(),
    pending = nested(8);
  expect(
    view.observe(
      "parent",
      resource(5n, { state: "exited", cleanup_verified: true, pending }),
    ),
  ).toBe(false);
  expect(
    view.observe(
      "parent",
      resource(5n, {
        state: "exited",
        cleanup_verified: true,
        pending: nested(8, "other"),
      }),
    ),
  ).toBe(false);
  expect(view.hidden("original")).toBe(false);
});
// Raw JSON text nested far deeper than an unbounded recursion tolerates;
// JSON.stringify cannot build it, but JSON.parse accepts it within 1 MiB.
const DEEP_LEVELS = 100_000;
const deepJson = "[".repeat(DEEP_LEVELS) + "]".repeat(DEEP_LEVELS);
const rawResource = (revision: bigint, documentJson: string) =>
  create(ResourceSchema, {
    ...resource(revision, {}),
    documentJson: new TextEncoder().encode(documentJson),
  });
const overDepthDocuments = {
  pending: `{"state":"exited","cleanup_verified":true,"pending":${deepJson}}`,
  state: `{"state":${deepJson},"cleanup_verified":true}`,
  "cleanup proof": `{"state":"exited","cleanup_verified":${deepJson}}`,
};
it.each(Object.entries(overDepthDocuments))(
  "treats excessively nested %s evidence as unsettled without overflowing",
  (_name, json) => {
    const view = new TerminalPresentation();
    expect(() => view.observe("parent", rawResource(5n, json))).not.toThrow();
    expect(view.observe("parent", rawResource(5n, json))).toBe(false);
    expect(view.hidden("original")).toBe(false);
    // A newer, shallow and settled exit still dismisses exactly once.
    expect(
      view.observe(
        "parent",
        resource(6n, { state: "exited", cleanup_verified: true }),
      ),
    ).toBe(true);
    expect(view.hidden("original")).toBe(true);
  },
);
it("rejects an equal-revision shallow replacement of over-depth evidence", () => {
  const view = new TerminalPresentation();
  expect(
    view.observe("parent", rawResource(5n, overDepthDocuments.pending)),
  ).toBe(false);
  expect(
    view.observe(
      "parent",
      resource(5n, { state: "exited", cleanup_verified: true }),
    ),
  ).toBe(false);
  expect(view.hidden("original")).toBe(false);
});
it("accepts identical equal-revision replays and dismisses exactly once", () => {
  const view = new TerminalPresentation(),
    data = { state: "exited", cleanup_verified: true };
  expect(view.observe("parent", resource(5n, data))).toBe(true);
  expect(view.observe("parent", resource(5n, data))).toBe(false);
  expect(view.hidden("original")).toBe(true);
});
it("compares pending evidence independent of key order", () => {
  const view = new TerminalPresentation(),
    pending = { action: "close", request_id: "original" };
  expect(
    view.observe(
      "parent",
      resource(5n, { state: "exited", cleanup_verified: true, pending }),
    ),
  ).toBe(false);
  expect(
    view.observe(
      "parent",
      resource(5n, {
        cleanup_verified: true,
        pending: { request_id: "original", action: "close" },
        state: "exited",
      }),
    ),
  ).toBe(false);
  expect(
    view.observe(
      "parent",
      resource(5n, {
        state: "exited",
        cleanup_verified: true,
        pending: { action: "close", request_id: "other" },
      }),
    ),
  ).toBe(false);
  expect(view.hidden("original")).toBe(false);
  expect(
    view.observe(
      "parent",
      resource(6n, { state: "exited", cleanup_verified: true }),
    ),
  ).toBe(true);
});
it("keys comparison evidence by original ID", () => {
  const view = new TerminalPresentation(),
    other = (revision: bigint, data: object) =>
      create(ResourceSchema, {
        ...resource(revision, data),
        id: "second",
      });
  expect(view.observe("parent", resource(5n, { state: "running" }))).toBe(
    false,
  );
  expect(
    view.observe(
      "parent",
      other(5n, { state: "exited", cleanup_verified: true }),
    ),
  ).toBe(true);
  expect(view.hidden("second")).toBe(true);
  expect(view.hidden("original")).toBe(false);
});
it("preserves a retained uncertain operation until its original resolution", () => {
  const view = new TerminalPresentation(),
    exited = resource(4n, { state: "exited", cleanup_verified: true });
  expect(view.observe("parent", exited, true)).toBe(false);
  expect(view.observe("parent", exited)).toBe(true);
});
it.each([
  { sessionId: "other" },
  { schemaVersion: 2 },
  { revision: 0n },
  { kind: EntityKind.SESSION },
])("rejects foreign or unsupported resource", (change) => {
  const view = new TerminalPresentation();
  expect(
    view.observe(
      "parent",
      create(ResourceSchema, {
        ...resource(4n, { state: "exited", cleanup_verified: true }),
        ...change,
      }),
    ),
  ).toBe(false);
});
it("selects the nearest left neighbor, then the first remaining terminal", () => {
  expect(terminalFallback(["one", "two", "three"], "two")).toBe("one");
  expect(terminalFallback(["one", "two", "three"], "one")).toBe("two");
  expect(terminalFallback(["one"], "one")).toBe("");
});
