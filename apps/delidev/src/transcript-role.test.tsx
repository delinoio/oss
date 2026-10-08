// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { TranscriptItem, messageRows } from "./session";
import { transcriptResource, transcriptRoleFixtures, transcriptSessionId } from "./transcript-role-fixtures";
it("preserves inert text, role/status labels and native detail controls on recognized roots", () => {
  const data = transcriptRoleFixtures();
  const { container } = render(<>{Object.values(data).map((value, index) => <TranscriptItem key={index} resource={transcriptResource(value, index + 2)} />)}</>);
  expect(container.querySelectorAll(".message-user")).toHaveLength(2);
  expect(container.querySelectorAll(".message-assistant")).toHaveLength(2);
  expect(container.querySelector(".message-claude-assistant > .native-claude-message-content")).not.toBeNull();
  expect(screen.getByLabelText("user message").querySelector("pre")?.textContent).toBe(data.user.text);
  expect(screen.getByLabelText("assistant message").querySelector("pre")?.textContent).toBe(data.assistant.text);
  expect(screen.getByLabelText("Grok user input").querySelector("pre")?.textContent).toBe(data.grokUser.text);
  expect(screen.getByText("Verified from closed native history")).toBeTruthy();
  expect(screen.getByText("Grok input details").closest("details")).toBeTruthy();
  expect(screen.getByText("Grok text details").closest("details")).toBeTruthy();
  expect(screen.getByText(/Reasoning ·/).closest("details")).toBeTruthy();
  expect(container.querySelector("script")).toBeNull();
});
it("keeps unknown roles, non-text records and invalid native roots outside role presentation", () => {
  const data = transcriptRoleFixtures();
  const rows = [
    { role: "unknown", state: "complete", text: "Unknown retained text" },
    { role: "assistant", state: "complete", text: "Original tool", tool: { started: { kind: "fixture" } } },
    { role: "user", state: "complete", text: "Original artifact", artifact: { started: { kind: "fixture" } } },
    { role: "assistant", state: "complete", text: "Original progress", progress: {} },
    { ...data.grokUser, native_id: "foreign" }, { ...data.grokText, role: "user" },
    { ...data.claude, claude: { invalid: true } }, { ...data.claude, role: "tool" },
    ...["grok_text", "claude", "claude_tool", "claude_progress", "claude_interruption"].map(key => ({ role: "assistant", state: "complete", text: `Retained null ${key}`, [key]: null })),
  ];
  const { container } = render(<>{rows.map((value, index) => <TranscriptItem key={index} resource={transcriptResource(value, index + 2)} />)}</>);
  expect(container.querySelectorAll(".message-user, .message-assistant, .native-claude-message-content")).toHaveLength(0);
  expect(screen.getByText("Unknown retained text")).toBeTruthy();
  expect(screen.getByLabelText("Grok user input unavailable")).toBeTruthy();
  expect(screen.getByLabelText("Grok text unavailable")).toBeTruthy();
  expect(screen.getAllByLabelText("Claude message unavailable")).toHaveLength(2);
  expect(screen.getByText("Retained null grok_text")).toBeTruthy();
  expect(screen.getByText("Retained null claude")).toBeTruthy();
});
it("replaces a live revision in its original ordering slot without changing role or duplicating text", () => {
  const data = transcriptRoleFixtures(), history = transcriptResource(data.user);
  const streamed = transcriptResource({ ...data.assistant, state: "streaming", text: "Partial original text" }, 3);
  const complete = transcriptResource({ ...data.assistant, text: "Complete original text" }, 3, 2n);
  const { container, rerender } = render(<><div data-payload-page=""><TranscriptItem resource={history} /></div><TranscriptItem resource={streamed} /></>);
  const rows = messageRows([history], new Map([[complete.id, complete]]), new Set(), [complete.id], transcriptSessionId, true);
  expect(rows.map(row => row.id)).toEqual([history.id, complete.id]);
  rerender(<><div data-payload-page=""><TranscriptItem resource={rows[0]!} /></div><TranscriptItem resource={rows[1]!} /></>);
  expect(container.querySelectorAll(".message-assistant")).toHaveLength(1);
  expect(screen.queryByText("Partial original text")).toBeNull();
  expect(screen.getByText("Complete original text")).toBeTruthy();
  expect(container.querySelectorAll("article")[0]?.classList.contains("message-user")).toBe(true);
});
