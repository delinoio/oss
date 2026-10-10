// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { AgentWorkerRow } from "./agent-worker-row";
import { encode } from "./documents";
import { copy } from "./localization";
afterEach(cleanup);
function row(effort: unknown, schemaVersion = 4) {
 return create(ResourceSchema, { id: "fixture-agent", kind: EntityKind.AGENT, schemaVersion, revision: 1n, documentJson: encode({ name: "Fixture", harness: "codex", ...(effort === undefined ? {} : { effort }), options: { subagent_effort: "medium" }, routes: [
  { model: { provider_id: newRequestId(), native_id: "first-native" }, accounts: [{ id: "a" }, { id: "b" }] },
  { model: { provider_id: newRequestId(), native_id: "second-native" }, accounts: [{ id: "c" }] },
 ] }) });
}
it.each(["high", "none", "max", "xhigh", " Future-Effort ", "   ", undefined, "", null, 3, {}, []])("projects the exact root effort once across disclosure (%s)", effort => {
 const action = vi.fn(); const mounted = render(<AgentWorkerRow row={row(effort)} edit={action} preview={action} remove={action} />);
 const value = mounted.container.querySelector(".agent-reasoning-effort-value")!;
 expect(value.textContent).toBe(effort === undefined || effort === "" ? copy("reasoning-effort.nativeDefault") : typeof effort === "string" ? effort : copy("agent-worker-row.effortUnavailable"));
 fireEvent.click(screen.getByRole("button", { name: copy("agent-worker-row.moreModels", { count: 1 }) }));
 expect(mounted.container.querySelectorAll(".agent-reasoning-effort")).toHaveLength(1);
 expect([...mounted.container.querySelectorAll(".agent-model-routes code")].map(node => node.textContent)).toEqual(["first-native", "second-native"]);
 expect([...mounted.container.querySelectorAll(".agent-model-routes .agent-route-account-count")].map(node => node.textContent)).toEqual([copy("agent-worker-row.accountCount", { count: 2 }), copy("agent-worker-row.accountCount", { count: 1 })]);
 expect(action).not.toHaveBeenCalled();
});
it("does not project effort for unsupported schemas or unreadable documents", () => {
 for (const resource of [row("high", 1), row("high", 99), create(ResourceSchema, { ...row("high"), documentJson: new TextEncoder().encode("{") })]) {
  const mounted = render(<AgentWorkerRow row={resource} edit={() => {}} preview={() => {}} remove={() => {}} />);
  expect(mounted.container.querySelector(".agent-reasoning-effort")).toBeNull();
  mounted.unmount();
 }
});
