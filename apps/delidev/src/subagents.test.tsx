import { render, screen } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { expect, test } from "vitest";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { SubagentRows } from "./subagents";

test("shows native hierarchy, missing telemetry and exact observed child counters without controls", () => {
  const root = newRequestId(), parent = newRequestId();
  const row = (native: string, parentId: string, model: string | null, usage: unknown) => create(ResourceSchema, {
    id: newRequestId(), revision: 1n, kind: EntityKind.SUBAGENT, schemaVersion: 1,
    documentJson: new TextEncoder().encode(JSON.stringify({ root_id: root, execution_id: newRequestId(), harness: "codex", native_version: "0.151.0",
      sources: [{ source: "codex-collaboration", source_id: "original-spawn", sequence: 3 }],
      observation: { native_id: native, parent_id: parentId, status: "running", requested_model: "requested-only", observed_model: model, output: null, usage },
    })),
  });
  render(<SubagentRows rows={[row(parent, root, null, null), row("nested-native-child", parent, "observed-model", { scope: "child-cumulative", total: "18446744073709551615", input: null, output: "0" })]} />);
  expect(screen.getByText("nested-native-child")).toBeTruthy();
  expect(screen.getAllByText(parent).length).toBe(2);
  expect(screen.getByText("Observed: observed-model")).toBeTruthy();
  expect(screen.getByText("Observed: Unavailable")).toBeTruthy();
  expect(screen.getByText("Total: 18446744073709551615")).toBeTruthy();
  expect(screen.getByText("Output: 0")).toBeTruthy();
  expect(screen.getAllByText("Requested: requested-only").length).toBe(2);
  expect(screen.queryByRole("button")).toBeNull();
});
