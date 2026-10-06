// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { NativeContextCompaction, validNativeContextCompaction } from "./native-context-compaction";

const progress = { kind: "native-compaction", compaction: { harness: "codex", trigger: "automatic", stage: "completed", native_item_id: "original-item" } };

describe("native context compaction", () => {
  it("presents original OpenCode context without inventing counters", () => { render(<NativeContextCompaction progress={{...progress,compaction:{...progress.compaction,harness:"opencode",native_item_id:"prt_01960dcbe1faABCDEFGHIJKLMN"}}} state="complete" />);expect(screen.getByText("OpenCode is compacting its working context. The conversation remains retained.")).toBeTruthy();expect(screen.getByText("Not reported")).toBeTruthy(); });
  it("retains unavailable counters and escapes the original reference", () => {
    const { container } = render(<NativeContextCompaction progress={{ ...progress, compaction: { ...progress.compaction, native_item_id: "<script>original reference</script>" } }} state="complete" />);
    expect(screen.getByText("Context compaction · Automatic · Completed")).toBeTruthy();
    expect(screen.getByText("Not reported")).toBeTruthy();
    expect(container.querySelector("script")).toBeNull();
  });
  it.each([null, { ...progress, diff: "private" }, { ...progress, compaction: { ...progress.compaction, stage: "idle" } }, { ...progress, compaction: { ...progress.compaction, harness: "claude-code" } }, { ...progress, compaction: { ...progress.compaction, trigger: "manual" } }])("rejects unverified context metadata", (value) => {
    expect(validNativeContextCompaction(value)).toBe(false);
  });
});

it.each(["not-a-part", "msg_01960dcbe1faABCDEFGHIJKLMN", "prt_01960dcbe1faABCDEFGHIJKLM"])("rejects invalid OpenCode compaction part %s", native_item_id => {
 expect(validNativeContextCompaction({...progress,compaction:{...progress.compaction,harness:"opencode",native_item_id}})).toBe(false);
});
