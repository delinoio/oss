import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { NativeReasoning } from "./native-reasoning";
import { NativeChanges, NativeRevision } from "./native-changes";

const turn = "msg_01960dcbe1faABCDEFGHIJKLMN";
function changeFixture() {
  return { kind: "opencode-changes", changes: { source: "input-summary", native_event_id: "evt_01960dcbe1faABCDEFGHIJKLMN", native_message_id: turn, title: "Original title", body: "Original body", diffs: [{ file: "https://untrusted.invalid/file", patch: " original\n<script>untrusted()</script>🙂 ", additions: 2, deletions: 1, status: "modified" }] } };
}
function revisionFixture() {
  const snapshot = { kind: "opencode-revision", text: "", summary: null, content: null, revision: { source: "patch", hash: "original snapshot reference", files: ["one.txt", "https://untrusted.invalid/file"] } };
  return { started: snapshot, completed: structuredClone(snapshot) };
}

it("renders the exact original change summary as inert content with independent counts", () => {
  const progress = changeFixture();
  const { container } = render(<NativeChanges progress={progress} state="complete" turn={turn} />);
  expect(container.querySelector("details")?.open).toBe(false);
  expect(screen.getByText("Original title")).toBeTruthy();
  expect(screen.getByText("Original body")).toBeTruthy();
  expect(screen.getByText("https://untrusted.invalid/file · modified · +2 / −1")).toBeTruthy();
  expect([...container.querySelectorAll("pre")].at(-1)?.textContent).toBe(progress.changes.diffs[0]!.patch);
  expect(container.querySelector("script, a, button")).toBeNull();
});

it("preserves original empty diffs and missing versus empty file/patch values", () => {
  const changes = { source: "session-diff", native_event_id: "evt_01960dcbe1faABCDEFGHIJKLMN", diffs: [] as unknown[] };
  const { rerender, container } = render(<NativeChanges progress={{ kind: "opencode-changes", changes }} state="complete" turn={turn} />);
  expect(screen.getByText("The original diff list is empty.")).toBeTruthy();
  changes.diffs = [{ additions: 0, deletions: 0 }, { file: "", patch: "", additions: 0, deletions: 0 }];
  rerender(<NativeChanges progress={{ kind: "opencode-changes", changes }} state="complete" turn={turn} />);
  expect(screen.getByText("File unavailable · Status unavailable · +0 / −0")).toBeTruthy();
  expect(screen.getByText("Patch unavailable.")).toBeTruthy();
  expect(container.querySelectorAll("pre")).toHaveLength(1);
  expect(container.querySelector("pre")?.textContent).toBe("");
});

it("retains ordered native snapshot file references without a restore action", () => {
  const { container } = render(<NativeRevision artifact={revisionFixture()} state="complete" />);
  expect(screen.getByText("Changed file references")).toBeTruthy();
  expect([...container.querySelectorAll("li code")].map((n) => n.textContent)).toEqual(["one.txt", "https://untrusted.invalid/file"]);
  expect(container.querySelector("button, a")).toBeNull();
});

it.each([
  { name: "foreign input", change: (p: ReturnType<typeof changeFixture>) => { p.changes.native_message_id = "msg_01960dcbe1fbABCDEFGHIJKLMN"; } },
  { name: "unsafe counter", change: (p: ReturnType<typeof changeFixture>) => { p.changes.diffs[0]!.additions = Number.MAX_SAFE_INTEGER + 1; } },
  { name: "fractional counter", change: (p: ReturnType<typeof changeFixture>) => { p.changes.diffs[0]!.deletions = 1.5; } },
  { name: "unknown status", change: (p: ReturnType<typeof changeFixture>) => { p.changes.diffs[0]!.status = "other"; } },
  { name: "invalid Unicode", change: (p: ReturnType<typeof changeFixture>) => { p.changes.body = "\uD800"; } },
  { name: "oversized patch", change: (p: ReturnType<typeof changeFixture>) => { p.changes.diffs[0]!.patch = "한".repeat(90000); } },
])("refuses $name without partial summary display", ({ change }) => {
  const progress = changeFixture(); change(progress);
  const { container } = render(<NativeChanges progress={progress} state="complete" turn={turn} />);
  expect(screen.getByText("Native changes · Unavailable")).toBeTruthy();
  expect(container.querySelector("pre")).toBeNull();
});

it.each([
  { name: "changed reference", change: (a: ReturnType<typeof revisionFixture>) => { a.completed.revision.hash = "changed"; } },
  { name: "changed order", change: (a: ReturnType<typeof revisionFixture>) => { a.completed.revision.files.reverse(); } },
  { name: "changed source", change: (a: ReturnType<typeof revisionFixture>) => { a.completed.revision.source = "step-start"; } },
  { name: "invented text", change: (a: ReturnType<typeof revisionFixture>) => { a.completed.text = "invented"; } },
])("refuses $name on a completed native revision", ({ change }) => {
  const artifact = revisionFixture(); change(artifact);
  const { container } = render(<NativeRevision artifact={artifact} state="complete" />);
  expect(screen.getByText("Native revision · Unavailable")).toBeTruthy();
  expect(container.querySelector("code")).toBeNull();
});

it("does not grant closure from an uncompleted revision or streaming diff", () => {
  const { rerender } = render(<NativeRevision artifact={{ started: revisionFixture().started }} state="streaming" />);
  expect(screen.getByText("Native revision · Unavailable")).toBeTruthy();
  rerender(<NativeChanges progress={changeFixture()} state="streaming" turn={turn} />);
  expect(screen.getByText("Native changes · Unavailable")).toBeTruthy();
});

it("rejects a revision payload mixed into plain reasoning", () => {
 const snapshot={kind:"reasoning-text",text:"original",revision:{source:"snapshot",hash:"opaque",files:null}};
 const {container}=render(<NativeReasoning artifact={{started:snapshot,completed:snapshot}} state="complete" />);
 expect(screen.getByText("Reasoning · Unavailable")).toBeTruthy();
 expect(container.querySelector("pre")).toBeNull();
});
