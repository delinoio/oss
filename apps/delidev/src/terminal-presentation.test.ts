// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema } from "@delinoio/delidev-api-client";
import { expect, it } from "vitest";
import { encode } from "./documents";
import { TerminalPresentation, terminalFallback } from "./terminal-presentation";
const resource = (revision: bigint, data: object) => create(ResourceSchema, { kind: EntityKind.TERMINAL, id: "original", sessionId: "parent", schemaVersion: 1, revision, documentJson: encode(data) });
it("keeps monotonic original-identity tombstones through stale and newer history restoration", () => {
 const view = new TerminalPresentation();
 expect(view.observe("parent", resource(5n, { state: "running" }))).toBe(false);
 expect(view.observe("parent", resource(4n, { state: "exited", cleanup_verified: true }))).toBe(false);
 expect(view.observe("parent", resource(6n, { state: "exited", cleanup_verified: true }))).toBe(true);
 expect(view.observe("parent", resource(7n, { state: "running" }))).toBe(false);
 expect(view.hidden("original")).toBe(true);
});
it.each([{ state: "exited" }, { state: "exited", cleanup_verified: "true" }, { state: "closed", cleanup_verified: true }, { state: "running", cleanup_verified: true }, { state: "exited", cleanup_verified: true, pending: { request_id: "original" } }, { state: "exited", cleanup_verified: true, pending: "malformed" }])("retains incomplete or unsettled cleanup %j", data => {
 const view = new TerminalPresentation(); expect(view.observe("parent", resource(4n, data))).toBe(false); expect(view.hidden("original")).toBe(false);
});
it("preserves a retained uncertain operation until its original resolution", () => {
 const view = new TerminalPresentation(), exited = resource(4n, { state: "exited", cleanup_verified: true });
 expect(view.observe("parent", exited, true)).toBe(false);
 expect(view.observe("parent", exited)).toBe(true);
});
it.each([{ sessionId: "other" }, { schemaVersion: 2 }, { revision: 0n }, { kind: EntityKind.SESSION }])("rejects foreign or unsupported resource", change => {
 const view = new TerminalPresentation(); expect(view.observe("parent", create(ResourceSchema, { ...resource(4n, { state: "exited", cleanup_verified: true }), ...change }))).toBe(false);
});
it("selects the nearest left neighbor, then the first remaining terminal", () => {
 expect(terminalFallback(["one", "two", "three"], "two")).toBe("one"); expect(terminalFallback(["one", "two", "three"], "one")).toBe("two"); expect(terminalFallback(["one"], "one")).toBe("");
});
