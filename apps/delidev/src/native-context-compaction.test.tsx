// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { NativeContextCompaction, validNativeContextCompaction } from "./native-context-compaction";

const progress = { kind: "native-compaction", compaction: { harness: "codex", trigger: "automatic", stage: "completed", native_item_id: "original-item" } };

describe("native context compaction", () => {
  it("retains unavailable counters and escapes the original reference", () => {
    const { container } = render(<NativeContextCompaction progress={{ ...progress, compaction: { ...progress.compaction, native_item_id: "<script>original reference</script>" } }} state="complete" />);
    expect(screen.getByText("Context compaction · Automatic · Completed")).toBeTruthy();
    expect(screen.getByText("Not reported")).toBeTruthy();
    expect(container.querySelector("script")).toBeNull();
  });
  it.each([null, { ...progress, diff: "private" }, { ...progress, compaction: { ...progress.compaction, stage: "idle" } }, { ...progress, compaction: { ...progress.compaction, harness: "opencode" } }, { ...progress, compaction: { ...progress.compaction, trigger: "manual" } }])("rejects unverified context metadata", (value) => {
    expect(validNativeContextCompaction(value)).toBe(false);
  });
});
