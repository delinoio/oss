// SPDX-License-Identifier: Apache-2.0
import { act, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { SubmissionStatus, TranscriptItem, messageRows } from "./session";
import { transcriptResource, transcriptRoleFixtures, transcriptSessionId } from "./transcript-role-fixtures";
import { copy, i18n } from "./localization";
import { SubmissionPhase } from "./session-submissions";
import { statusLabel } from "./product-status";
afterEach(async () => { await i18n.changeLanguage("en"); });
it("preserves inert text, accessible roles and native detail controls on recognized roots", () => {
  const data = transcriptRoleFixtures();
  const { container } = render(<>{Object.values(data).map((value, index) => <TranscriptItem key={index} resource={transcriptResource(value, index + 2)} />)}</>);
  expect(container.querySelectorAll(".message-user")).toHaveLength(2);
  expect(container.querySelectorAll(".message-assistant")).toHaveLength(2);
  expect(container.querySelector(".message-claude-assistant > .native-claude-message-content")).not.toBeNull();
  expect(screen.getByLabelText("User message").querySelector("pre")?.textContent).toBe(data.user.text);
  expect(screen.getAllByLabelText("Assistant message")[0]!.querySelector("pre")?.textContent).toBe(data.assistant.text);
  expect(screen.getByLabelText("Grok user input").querySelector("pre")?.textContent).toBe(data.grokUser.text);
  expect(screen.getByText("Verified from closed native history")).toBeTruthy();
  expect(screen.getByText("Grok input details").closest("details")).toBeTruthy();
  expect(screen.getByText("Grok text details").closest("details")).toBeTruthy();
  expect(screen.getByText(/Reasoning ·/).closest("details")).toBeTruthy();
  expect(container.querySelector("script")).toBeNull();
  expect(container.querySelectorAll("article > header")).toHaveLength(1);
  expect(container.querySelector("article > header")?.textContent).toBe("Verified from closed native history");
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

it.each(["en", "ko"])("omits recognized role and success headers in %s, preserving accessible roles and exact content", async locale => {
  await act(() => i18n.changeLanguage(locale));
  const data = transcriptRoleFixtures();
  const { container } = render(<>{Object.values(data).map((value, index) => <TranscriptItem key={index} resource={transcriptResource(value, index + 2)} />)}<TranscriptItem resource={transcriptResource({ ...data.user, state: "completed" }, 20)} /></>);
  expect(screen.getAllByRole("article", { name: copy("session.userMessage") })).toHaveLength(2);
  expect(screen.getAllByRole("article", { name: copy("session.assistantMessage_8352f5") })).toHaveLength(2);
  for (const article of container.querySelectorAll("article")) {
    expect(article.querySelector("header strong")).toBeNull();
    expect(within(article).queryByText(statusLabel("complete"), { exact: true })).toBeNull();
    expect(within(article).queryByText(statusLabel("completed"), { exact: true })).toBeNull();
  }
  expect(container.querySelectorAll("article > header")).toHaveLength(1);
  expect(screen.getByRole("article", { name: copy("native-grok.grokUserInput_b19458") }).querySelector("header")?.textContent).toBe(copy("native-grok.verifiedFromClosedNativeHistory_2d5065"));
  expect(screen.getByRole("article", { name: copy("native-grok.grokAssistantText_45c8b0") }).querySelector("pre")?.textContent).toBe(data.grokText.text);
});
it.each(["streaming", "interrupted", "failed", "recovery-required"])("preserves operational %s on generic conversation roots", state => {
  const { container } = render(<TranscriptItem resource={transcriptResource({ role: "assistant", state, text: "Original operational text" })} />);
  expect(container.querySelector("header")?.textContent).toBe(statusLabel(state));
  expect(container.querySelector("header strong")).toBeNull();
});
it("keeps validated Grok partial-response and native details while suppressing success", () => {
  const data = transcriptRoleFixtures().grokText;
  const { container } = render(<TranscriptItem resource={transcriptResource({ ...data, grok_text: { ...data.grok_text as object, interruption: { request_id: "01960dcb-e1fa-7000-8000-000000000099", native_event_id: `${transcriptSessionId}-11` } } })} />);
  expect(container.querySelector("header")?.textContent).toBe("Partial response stopped");
  expect(screen.getByText("Grok text details").closest("details")).not.toBeNull();
});
it.each(["en", "ko"])("keeps every projected delivery status without a visible role in %s", async locale => {
  await act(() => i18n.changeLanguage(locale));
  const phases = Object.values(SubmissionPhase);
  const { container } = render(<>{phases.map(phase => <article key={phase} aria-label={copy("session.submittedUserMessage")}><SubmissionStatus phase={phase} /></article>)}</>);
  expect(screen.getAllByRole("article", { name: copy("session.submittedUserMessage") })).toHaveLength(phases.length);
  expect(screen.getAllByRole("status")).toHaveLength(phases.length);
  for (const status of screen.getAllByRole("status")) expect(status.textContent?.length).toBeGreaterThan(0);
  expect(container.querySelector("strong")).toBeNull();
});
