// SPDX-License-Identifier: Apache-2.0
import { newRequestId } from "@delinoio/delidev-api-client";
import { cleanup, render } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, beforeAll, describe, expect, it } from "vitest";
import type { Document } from "./documents";
import { NativeClaudeCompactionBoundary } from "./native-claude-compaction";
import { NativeClaudeProgress } from "./native-claude-progress";
import { NativeContextCompaction } from "./native-context-compaction";
import { NativeGrokToolsTerminal } from "./native-grok-interactions";

// jsdom cannot parse the whole stylesheet, so load only the real visibility
// rules. They are the same attribute selectors the desktop app applies.
beforeAll(() => {
  const source = readFileSync(join(process.cwd(), "src/themes.css"), "utf8");
  const rules = source.match(
    /:root\[data-show-(?:time|tokens)="false"\][^{}]*\{[^}]*\}/g,
  );
  expect(rules?.length).toBeGreaterThanOrEqual(3);
  const style = document.createElement("style");
  style.textContent = rules!.join("\n");
  document.head.appendChild(style);
});

enum Toggle {
  Tokens = "showTokens",
  Time = "showTime",
}

function setToggles(tokens: boolean, time: boolean) {
  document.documentElement.dataset[Toggle.Tokens] = String(tokens);
  document.documentElement.dataset[Toggle.Time] = String(time);
}

afterEach(() => {
  cleanup();
  delete document.documentElement.dataset[Toggle.Tokens];
  delete document.documentElement.dataset[Toggle.Time];
});

function rendered(node: React.ReactElement) {
  const result = render(<div className="transcript">{node}</div>);
  const hidden = (label: RegExp, value: string) => {
    const dt = [...result.container.querySelectorAll("dt")].find((item) =>
      label.test(item.textContent ?? ""),
    );
    expect(dt, `label ${label}`).toBeTruthy();
    const dd = dt!.nextElementSibling as HTMLElement;
    expect(dd.textContent).toContain(value);
    const labelHidden = getComputedStyle(dt!).display === "none";
    const valueHidden = getComputedStyle(dd).display === "none";
    expect(labelHidden).toBe(valueHidden);
    return labelHidden;
  };
  return { ...result, hidden };
}

function progressData(fields: Document): Document {
  const native = newRequestId();
  return {
    execution_id: newRequestId(),
    native_thread_id: newRequestId(),
    native_turn_id: newRequestId(),
    native_id: native,
    role: "progress",
    state: "complete",
    text: "",
    first_sequence: 2,
    last_sequence: 2,
    claude_progress: {
      native_event_id: native,
      input_accepted: true,
      status: null,
      thinking: null,
      ...fields,
    },
  };
}

const compactionValue: Document = {
  trigger: "auto",
  pre_tokens: "111",
  post_tokens: "222",
  duration_ms: "333",
  cumulative_dropped_tokens: "444",
  preserved_messages: { anchor_uuid: newRequestId(), uuids: [newRequestId()] },
};

describe("transcript token and time observation toggles", () => {
  it.each([
    [true, true],
    [false, true],
    [true, false],
    [false, false],
  ])("Claude compaction boundary tokens=%s time=%s", (tokens, time) => {
    setToggles(tokens, time);
    const view = rendered(
      <NativeClaudeCompactionBoundary value={compactionValue} />,
    );
    expect(view.hidden(/before compaction/i, "111")).toBe(!tokens);
    expect(view.hidden(/after compaction/i, "222")).toBe(!tokens);
    expect(view.hidden(/dropped/i, "444")).toBe(!tokens);
    expect(view.hidden(/duration/i, "333")).toBe(!time);
    // Trigger and disclosures are not token or time observations.
    expect(view.hidden(/trigger/i, "Automatic")).toBe(false);
    expect(view.container.querySelector("summary")).toBeTruthy();
  });

  it.each([
    [true, true],
    [false, true],
    [true, false],
    [false, false],
  ])("Claude task usage tokens=%s time=%s", (tokens, time) => {
    setToggles(tokens, time);
    const view = rendered(
      <NativeClaudeProgress
        data={progressData({
          kind: "task-lifecycle",
          task: {
            kind: "task_notification",
            task_id: "original-task",
            status: "stopped",
            reason: "worker_restart",
            output_file: "file:///private/original-output",
            summary: "summary",
            skip_transcript: false,
            usage: {
              total_tokens: "555",
              tool_uses: "666",
              duration_ms: "777",
            },
          },
        })}
      />,
    );
    expect(view.hidden(/token/i, "555")).toBe(!tokens);
    expect(view.hidden(/tool uses/i, "666")).toBe(false);
    expect(view.hidden(/duration/i, "777")).toBe(!time);
  });

  it.each([
    [true, true],
    [false, true],
    [true, false],
  ])("Claude thinking estimates tokens=%s time=%s", (tokens, time) => {
    setToggles(tokens, time);
    const view = rendered(
      <NativeClaudeProgress
        data={progressData({
          kind: "thinking-tokens-estimated",
          thinking: { estimated_tokens: "888", estimated_tokens_delta: "999" },
        })}
      />,
    );
    expect(view.hidden(/thinking tokens/i, "888")).toBe(!tokens);
    expect(view.hidden(/token delta/i, "999")).toBe(!tokens);
  });

  it.each([
    [true, true],
    [true, false],
    [false, true],
  ])("Claude tool progress elapsed tokens=%s time=%s", (tokens, time) => {
    setToggles(tokens, time);
    const view = rendered(
      <NativeClaudeProgress
        data={progressData({
          kind: "tool-progress",
          tool: {
            tool: {
              id: newRequestId(),
              native_id: "tool_original",
              name: "Bash",
            },
            parent_tool_use_id: null,
            elapsed_time_seconds: "12.5",
            heartbeat: false,
          },
        })}
      />,
    );
    expect(view.hidden(/elapsed/i, "12.5")).toBe(!time);
    expect(view.hidden(/heartbeat/i, "")).toBe(false);
  });

  it.each([
    [true, true],
    [true, false],
  ])("Claude task update paused time tokens=%s time=%s", (tokens, time) => {
    setToggles(tokens, time);
    const view = rendered(
      <NativeClaudeProgress
        data={progressData({
          kind: "task-lifecycle",
          task: {
            kind: "task_updated",
            task_id: "original-task",
            patch: { status: "paused", total_paused_ms: "4321" },
          },
        })}
      />,
    );
    expect(view.hidden(/paused/i, "4321")).toBe(!time);
    // Unrelated task fields stay visible.
    expect(view.hidden(/^task$/i, "original-task")).toBe(false);
  });

  it("native context compaction hides only the token label and value", () => {
    setToggles(false, true);
    const view = rendered(
      <NativeContextCompaction
        state="complete"
        progress={{
          kind: "native-compaction",
          compaction: {
            harness: "codex",
            trigger: "automatic",
            stage: "completed",
            native_item_id: "original-item",
          },
        }}
      />,
    );
    expect(view.hidden(/context tokens/i, "")).toBe(true);
    expect(view.hidden(/reference/i, "original-item")).toBe(false);
  });

  it("restores the original markup when toggles return to On", () => {
    setToggles(false, false);
    const view = rendered(
      <NativeClaudeCompactionBoundary value={compactionValue} />,
    );
    const hiddenHtml = view.container.innerHTML;
    expect(view.hidden(/before compaction/i, "111")).toBe(true);
    setToggles(true, true);
    expect(view.hidden(/before compaction/i, "111")).toBe(false);
    expect(view.hidden(/duration/i, "333")).toBe(false);
    // Visibility is presentation only: the rendered values never change.
    expect(view.container.innerHTML).toBe(hiddenHtml);
  });

  it.each([
    [true, true],
    [false, true],
    [true, false],
    [false, false],
  ])("Grok tools terminal totals tokens=%s time=%s", (tokens, time) => {
    setToggles(tokens, time);
    const id = newRequestId();
    const counts = {
      input_tokens: "101",
      output_tokens: "102",
      cache_read_input_tokens: "0",
      cache_creation_input_tokens: "0",
      reasoning_tokens: "0",
    };
    const view = rendered(
      <NativeGrokToolsTerminal
        progress={{
          execution_id: id,
          native_thread_id: id,
          native_turn_id: "526452fa-1956-42dd-b5f4-60e2b23dfe92",
          outcome: "succeeded",
          observed: { model: "original-model" },
          grok_content: { responses: 1 },
          grok_response_totals: counts,
          grok_tools_terminal: {
            kind: "closed-first-tools",
            native_event_id: `${id}-10`,
            timestamp_ms: "1",
            elapsed_ms: "203",
            model: "original-model",
            reason: "end_turn",
            counts,
            total_tokens: "204",
            model_calls: "1",
            api_duration_ms: "2",
            turns: "1",
          },
        }}
      />,
    );
    expect(view.hidden(/input tokens/i, "101")).toBe(!tokens);
    expect(view.hidden(/output tokens/i, "102")).toBe(!tokens);
    expect(view.hidden(/total tokens/i, "204")).toBe(!tokens);
    expect(view.hidden(/elapsed/i, "203")).toBe(!time);
    expect(view.hidden(/model responses/i, "1")).toBe(false);
  });
});
