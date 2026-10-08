// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema } from "@delinoio/delidev-api-client";
import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { encode } from "./documents";
import { Harness } from "./configuration-fields";
import { retainedSessionHarness, SessionHarness } from "./session-harness";

const resource = (data: unknown, schemaVersion = 1) => create(ResourceSchema, { kind: EntityKind.SESSION, schemaVersion, documentJson: encode(data) });
const initial = (harness: unknown) => ({ initial_execution: { configuration: { harness } } });
it.each([[Harness.Codex, "Codex"], [Harness.Claude, "Claude Code"], [Harness.OpenCode, "OpenCode"], [Harness.Grok, "Grok Build"]])("shows the retained %s identity decoratively", (harness, name) => {
  const { container } = render(<SessionHarness resource={resource(initial(harness))}><h2>Original title</h2></SessionHarness>);
  expect(screen.getByText(name)).toBeTruthy();
  const mark = container.querySelector(".session-harness-mark")!;
  expect(mark.classList.contains(`session-harness-mark-${harness}`)).toBe(true);
  expect(mark.getAttribute("aria-hidden")).toBe("true");
  expect(mark.hasAttribute("tabindex")).toBe(false);
  expect(mark.querySelector("button,a,input")).toBeNull();
});
it("uses initial execution ahead of conflicting fork, current selection and edited Agent", () => {
  const data = { ...initial(Harness.Codex), fork: { snapshot: { configuration: { harness: Harness.Claude } } }, current_execution: { configuration: { harness: Harness.Grok }, account_id: "changed" }, agent: { harness: Harness.OpenCode } };
  expect(retainedSessionHarness(resource(data))).toBe(Harness.Codex);
  expect(retainedSessionHarness(resource({ ...data, current_execution: { configuration: { harness: "unknown" } }, agent: null }))).toBe(Harness.Codex);
});
it("uses fork configuration only when initial execution is absent", () => {
  const data = { fork: { snapshot: { configuration: { harness: Harness.Claude } } } };
  expect(retainedSessionHarness(resource(data))).toBe(Harness.Claude);
  for (const value of [null, [], "invalid", {}, { configuration: {} }, { configuration: { harness: "unknown" } }]) {
    expect(retainedSessionHarness(resource({ ...data, initial_execution: value }))).toBeUndefined();
  }
});
it.each([{}, initial("unknown"), initial(2), [], null])("shows an unbranded fallback for invalid evidence %j", data => {
  const { container } = render(<SessionHarness resource={resource(data)}><h2>Original title</h2></SessionHarness>);
  expect(screen.getByText("Harness unavailable")).toBeTruthy();
  expect(container.querySelector(".session-harness-mark")).toBeNull();
});
it("rejects unsupported schema and malformed wire bytes", () => {
  expect(retainedSessionHarness(resource(initial(Harness.Codex), 99))).toBeUndefined();
  expect(retainedSessionHarness({ ...resource(initial(Harness.Codex)), kind: EntityKind.AGENT })).toBeUndefined();
  expect(retainedSessionHarness(create(ResourceSchema, { kind: EntityKind.SESSION, schemaVersion: 1, documentJson: new Uint8Array([255]) }))).toBeUndefined();
});
