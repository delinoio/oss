// SPDX-License-Identifier: Apache-2.0
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { TranscriptItem } from "./session";
import {
  transcriptResource,
  transcriptRoleFixtures,
} from "./transcript-role-fixtures";

const native = vi.hoisted(() => ({
  invoke: vi.fn(),
  isTauri: vi.fn(() => true),
}));
vi.mock("@tauri-apps/api/core", () => native);

const url = "https://example.com/path?q=1#part";
beforeEach(() => {
  native.invoke.mockReset();
  native.invoke.mockResolvedValue(undefined);
  native.isTauri.mockReturnValue(true);
});
afterEach(cleanup);

function renderRows(rows: Record<string, unknown>[]) {
  return render(
    <>
      {rows.map((value, index) => (
        <TranscriptItem
          key={index}
          resource={transcriptResource(value as never, index + 2)}
        />
      ))}
    </>,
  );
}

it("makes URLs in generic user and assistant text activatable while preserving the original text", () => {
  const data = transcriptRoleFixtures();
  const user = { ...data.user, text: `Read  ${url}.\n\n  Then http://localhost:3000/` };
  const assistant = { ...data.assistant, text: `Done: ${url}` };
  renderRows([user, assistant]);
  const userMessage = screen.getByLabelText("User message");
  expect(userMessage.querySelector("pre")!.textContent).toBe(user.text);
  expect(
    [...userMessage.querySelectorAll("a")].map((link) => link.textContent),
  ).toEqual([url, "http://localhost:3000/"]);
  const assistantMessage = screen.getAllByLabelText("Assistant message")[0]!;
  expect(assistantMessage.querySelector("pre")!.textContent).toBe(assistant.text);
  fireEvent.click(assistantMessage.querySelector("a")!);
  expect(native.invoke).toHaveBeenCalledWith("open_external_url", { url });
});

it("keeps unknown roles and tool, artifact and progress records inert", () => {
  const rows = [
    { role: "unknown", state: "complete", text: `Unknown ${url}` },
    { role: "assistant", state: "complete", text: `Tool ${url}`, tool: { started: { kind: "fixture" } } },
    { role: "user", state: "complete", text: `Artifact ${url}`, artifact: { started: { kind: "fixture" } } },
    { role: "assistant", state: "complete", text: `Progress ${url}`, progress: {} },
  ];
  const { container } = renderRows(rows);
  expect(container.querySelectorAll("a")).toHaveLength(0);
  expect(container.textContent).toContain(`Tool ${url}`);
});

it("activates URLs in Grok user input and assistant text only", () => {
  const data = transcriptRoleFixtures();
  const { container } = renderRows([
    { ...data.grokUser, text: `Grok user ${url}` },
    { ...data.grokText, text: `Grok assistant ${url}` },
  ]);
  const user = screen.getByLabelText("Grok user input");
  expect(user.querySelector("pre")!.textContent).toBe(`Grok user ${url}`);
  expect(user.querySelectorAll("a")).toHaveLength(1);
  const assistant = screen.getByLabelText("Grok assistant text");
  expect(assistant.querySelectorAll("a")).toHaveLength(1);
  // Native identifiers and details are never linked.
  expect(container.querySelectorAll("dd a, details a")).toHaveLength(0);
});

it("activates URLs only in Claude text blocks and keeps reasoning inert", () => {
  const data = transcriptRoleFixtures();
  const claude = data.claude as any;
  claude.claude.blocks[0].block.text = `Claude text ${url}`;
  claude.claude.blocks[1].block.text = `Claude reasoning ${url}`;
  const { container } = renderRows([claude]);
  const links = container.querySelectorAll("a");
  expect(links).toHaveLength(1);
  expect(links[0]!.closest("li")!.textContent).toContain("Claude text");
  expect(container.querySelector("details")!.textContent).toContain(
    `Claude reasoning ${url}`,
  );
  expect(container.querySelector("details a")).toBeNull();
});

it("renders no links on web or mobile", () => {
  native.isTauri.mockReturnValue(false);
  const data = transcriptRoleFixtures();
  const { container } = renderRows([
    { ...data.user, text: `See ${url}` },
    { ...data.grokUser, text: `See ${url}` },
  ]);
  expect(container.querySelectorAll("a")).toHaveLength(0);
  expect(container.textContent).toContain(`See ${url}`);
});
