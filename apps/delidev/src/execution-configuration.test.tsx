import { create } from "@bufbuild/protobuf";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { ExecutionConfiguration } from "./execution-configuration";

function fixture(harness = "codex") {
  const execution = newRequestId(), input = newRequestId(), account = newRequestId();
  const first = "Original first template 지침.\n  Keep indentation. <script>privateScript()</script>";
  const second = "Original second template.\nDo not use the later edited template.";
  const configuration = {
    agent_id: newRequestId(), agent_revision: 3, model_id: newRequestId(), model_revision: 7,
    provider_id: newRequestId(), harness, native_model: "original-model", effort: "high",
    options: { permission: "workspace-write", approval_policy: "on-request", subagent_model: "original-child", subagent_effort: "medium", max_concurrency: 4, approval_review_model: "original-review", service_tier: "priority" },
    accounts: [{ id: account, weight: 2 }, { id: newRequestId(), weight: 1 }], routing: "priority",
    templates: [{ id: newRequestId(), revision: 2, contents: first }, { id: newRequestId(), revision: 8, contents: second }], instructions: first + "\n\n" + second,
    native_system_prompt: "PRIVATE NATIVE PROMPT MUST NEVER APPEAR",
  };
  const data = {
    initial_execution: { id: execution, input_id: input, initial_account_id: account, configuration, accepted_at: "2026-09-26T01:00:00Z" },
    execution: { execution_id: execution, input_id: input, native_thread_id: "native-original", observed: { model: "original-model", effort: "high", service_tier: null, permission: "workspace-write", approval_policy: "on-request", native_prompt: "PRIVATE OBSERVER PROMPT" } },
  };
  const resource = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 9n, schemaVersion: 1, documentJson: encode(data) });
  return { data, resource, configuration, first, second };
}

function detail(region: HTMLElement, label: string): string | null | undefined {
  return within(region).getByText(label, { selector: "dt" }).nextElementSibling?.textContent;
}

it("shows the immutable ordered instructions as inert read-only text without native prompts", () => {
  const value = fixture();
  const { container } = render(<ExecutionConfiguration resource={value.resource} />);
  fireEvent.click(screen.getByText("Execution configuration and instructions"));
  const combined = screen.getByLabelText("Combined applied instructions");
  expect(combined.textContent).toBe(value.configuration.instructions);
  expect(container.querySelector("script")).toBeNull();
  expect(container.querySelector("input, textarea, button, a")).toBeNull();
  expect(container.textContent).not.toContain("PRIVATE NATIVE PROMPT");
  expect(container.textContent).not.toContain("PRIVATE OBSERVER PROMPT");
  const templates = screen.getByLabelText("Applied instruction template order").querySelectorAll("li");
  expect(templates).toHaveLength(2);
  expect(templates[0].querySelector("pre")?.textContent).toBe(value.first);
  expect(templates[1].querySelector("pre")?.textContent).toBe(value.second);
  expect(templates[0].textContent).toContain("revision 2");
  expect(templates[1].textContent).toContain("revision 8");
  const saved = screen.getByRole("region", { name: "Saved execution configuration" });
  expect(detail(saved, "Requested subagent model")).toBe("original-child");
  expect(detail(saved, "Requested concurrency")).toBe("4");
  expect(detail(saved, "Requested approval review model")).toBe("original-review");
  expect(detail(saved, "Requested service tier")).toBe("priority");
  expect(screen.getByLabelText("Saved account order").children[0].textContent).toContain(value.configuration.accounts[0].id);
});

it("distinguishes missing native values from requested settings and empty native observations", () => {
  const value = fixture();
  value.data.execution.observed.effort = "";
  render(<ExecutionConfiguration resource={{ ...value.resource, documentJson: encode(value.data) }} />);
  fireEvent.click(screen.getByText("Execution configuration and instructions"));
  const native = screen.getByRole("region", { name: "Native execution observations" });
  expect(detail(native, "Observed reasoning effort")).toBe("Empty native value");
  expect(detail(native, "Observed service tier")).toBe("Unavailable");
  const saved = screen.getByRole("region", { name: "Saved execution configuration" });
  expect(detail(saved, "Requested reasoning effort")).toBe("high");
  expect(detail(saved, "Requested service tier")).toBe("priority");
});

it("preserves first-execution choices while separately identifying retained prior observations", () => {
  const value = fixture();
  const view = render(<ExecutionConfiguration resource={value.resource} />);
  fireEvent.click(screen.getByText("Execution configuration and instructions"));
  const next = { ...value.data, current_execution: { id: newRequestId(), input_id: newRequestId(), account_id: newRequestId() } };
  view.rerender(<ExecutionConfiguration resource={{ ...value.resource, revision: 10n, documentJson: encode(next) }} />);
  expect(screen.getByText(/retained observations belong to an earlier execution/)).toBeTruthy();
  expect(screen.getByLabelText("Combined applied instructions").textContent).toBe(value.configuration.instructions);
  const saved = screen.getByRole("region", { name: "Saved execution configuration" });
  expect(detail(saved, "First account")).toBe(value.data.initial_execution.initial_account_id);
  expect(detail(saved, "Selected execution account")).toBe(next.current_execution.account_id);
  const bound = { ...next, execution: { ...next.execution, execution_id: next.current_execution.id, input_id: next.current_execution.input_id } };
  view.rerender(<ExecutionConfiguration resource={{ ...value.resource, revision: 11n, documentJson: encode(bound) }} />);
  expect(screen.queryByText(/retained observations belong to an earlier execution/)).toBeNull();
  expect(screen.getByLabelText("Combined applied instructions").textContent).toBe(value.configuration.instructions);
});

it.each([
  ["claude-code", "claude_permission", "plan", "Observed Claude permission"],
  ["opencode", "opencode_agent", "plan", "Observed OpenCode primary agent"],
])("keeps %s native policy separate from Codex sandbox labels", (harness, key, selection, label) => {
  const value = fixture(harness);
  const data = { ...value.data, execution: { ...value.data.execution, observed: { model: "original-model", effort: null, service_tier: null, permission: "default", approval_policy: "", [key]: selection } } };
  render(<ExecutionConfiguration resource={{ ...value.resource, documentJson: encode(data) }} />);
  fireEvent.click(screen.getByText("Execution configuration and instructions"));
  const native = screen.getByRole("region", { name: "Native execution observations" });
  expect(detail(native, label)).toBe("plan");
  expect(within(native).queryByText("Observed sandbox")).toBeNull();
  expect(within(native).queryByText("Observed approval policy")).toBeNull();
});

it("keeps unavailable snapshots, unknown options and imprecise revisions explicit", () => {
  const value = fixture();
  const view = render(<ExecutionConfiguration resource={{ ...value.resource, documentJson: encode({}) }} />);
  fireEvent.click(screen.getByText("Execution configuration and instructions"));
  expect(screen.getByText(/No accepted execution configuration/)).toBeTruthy();
  const data = { ...value.data, initial_execution: { ...value.data.initial_execution, configuration: { ...value.configuration, agent_revision: Number.MAX_SAFE_INTEGER + 1, options: { ...value.configuration.options, future_native_setting: "private unknown data" } } }, execution: { observed: { model: "unbound-model" } } };
  view.rerender(<ExecutionConfiguration resource={{ ...value.resource, documentJson: encode(data) }} />);
  expect(screen.getByText(/additional options that this app version cannot display/)).toBeTruthy();
  expect(screen.queryByText("private unknown data")).toBeNull();
  expect(screen.getByText(/No native settings have been observed/)).toBeTruthy();
  expect(screen.queryByText("unbound-model")).toBeNull();
  expect(screen.getByText(`${value.configuration.agent_id} · revision Unavailable`)).toBeTruthy();
  view.rerender(<ExecutionConfiguration resource={{ ...value.resource, schemaVersion: 2 }} />);
  expect(screen.getByText(/session document version is not supported/)).toBeTruthy();
  expect(screen.queryByLabelText("Combined applied instructions")).toBeNull();
});
