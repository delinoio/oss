// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render } from "@testing-library/react";
import { expect, it } from "vitest";
import { TranscriptItem } from "./session";
import { transcriptResource } from "./transcript-role-fixtures";
import { statusLabel } from "./product-status";
const snapshot = { kind: "reasoning", text: "", summary: ["Original summary"], content: ["Original body"] };
it.each(["streaming", "complete", "failed", "interrupted", "unavailable"])("uses the Codex route while preserving %s operational state", state => {
  const { container } = render(<TranscriptItem resource={transcriptResource({ role: "artifact", text: "", state, artifact: { started: snapshot, ...(state === "complete" ? { completed: snapshot } : {}) } })} />);
  const row = container.querySelector("article")!;
  expect(row.classList.contains("message-codex-reasoning")).toBe(true);
  expect(row.querySelector("details")?.open).toBe(false);
  expect(row.querySelector("summary")?.textContent).toBe("Original summary");
  expect(row.querySelector("header")?.textContent ?? "").toBe(state === "complete" ? "" : statusLabel(state));
  expect(row.textContent).not.toContain("Completed artifact");
  fireEvent.click(row.querySelector("summary")!);
  expect(row.querySelector("pre")?.textContent).toBe("Original summary");
});
it.each([
  { artifact: { started: { ...snapshot, summary: [null] } } },
  { artifact: { started: { ...snapshot, kind: "plan", text: "original plan", summary: null, content: null } } },
  { artifact: { started: snapshot }, state: "unknown" },
  { artifact: { started: snapshot }, text: "foreign root text" },
])("preserves generic fallback for malformed or foreign artifacts", change => {
  const { container } = render(<TranscriptItem resource={transcriptResource({ role: "artifact", text: "", state: "complete", ...change })} />);
  expect(container.querySelector(".message-codex-reasoning")).toBeNull();
  expect(container.querySelector("header strong")?.textContent).toBe("artifact");
});
it("keeps OpenCode reasoning and sibling plan disclosures independent", () => {
  const { container } = render(<><TranscriptItem resource={transcriptResource({ role: "artifact", text: "", state: "complete", artifact: { started: snapshot } })} /><TranscriptItem resource={transcriptResource({ role: "artifact", text: "", state: "complete", artifact: { started: { kind: "reasoning-text", text: "OpenCode original", summary: null, content: null }, completed: { kind: "reasoning-text", text: "OpenCode original", summary: null, content: null } } }, 3)} /><TranscriptItem resource={transcriptResource({ role: "artifact", text: "", state: "complete", artifact: { started: { kind: "plan", text: "Original plan", summary: null, content: null } } }, 4)} /></>);
  expect(container.querySelectorAll(".message-codex-reasoning")).toHaveLength(1);
  const details = container.querySelectorAll("details");
  expect([...details].map(row => row.open)).toEqual([false, false, true]);
  fireEvent.click(details[0].querySelector("summary")!);
  expect(details[1].open).toBe(false); expect(details[2].open).toBe(true);
  expect(details[1].querySelector("pre")?.textContent).toBe("OpenCode original");
});
