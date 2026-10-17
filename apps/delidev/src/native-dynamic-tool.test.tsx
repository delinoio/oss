// SPDX-License-Identifier: Apache-2.0
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { dynamicTool, NativeDynamicTool } from "./native-dynamic-tool";
afterEach(cleanup);
const token = "01912345-1234-7123-8123-123456789abc";
const identity = {
  tool: "fixture_tool",
  namespace: null,
  arguments_digest: "a".repeat(64),
};
const tool = {
  snapshot: {
    identity,
    status: "inProgress",
    content: null,
    success: null,
    duration_ms: null,
  },
  request: {
    token,
    id_kind: "number",
    id_value: "9223372036854775807",
    identity,
  },
  reply: {
    token,
    claim_id: "01912345-1234-7123-8123-123456789abd",
    delivery: "uncertain",
    unavailable: true,
  },
  resolved: false,
};
test("shows fixed unavailable text and independent uncertain/open facts without controls", () => {
  const { container } = render(<NativeDynamicTool content={tool} />);
  expect(screen.getByText("fixture_tool")).toBeTruthy();
  expect(
    screen.getByText("This dynamic tool is unavailable in DeliDev."),
  ).toBeTruthy();
  expect(screen.getByText("Reply delivery: Uncertain")).toBeTruthy();
  expect(screen.getByText("Native request: Open")).toBeTruthy();
  expect(container.textContent).not.toContain(token);
  expect(container.textContent).not.toContain("9223372036854775807");
  expect(
    container.querySelectorAll("a,img,audio,video,button,input,iframe,script"),
  ).toHaveLength(0);
});
test("renders ordered inert dynamic content and preserves native false outcome", () => {
  const completed = {
    ...tool,
    resolved: true,
    snapshot: {
      identity,
      status: "completed",
      success: false,
      duration_ms: "9007199254740993",
      content: [
        { kind: "inputText", text: "<script>inert()</script>" },
        { kind: "inputImage", reference_digest: "b".repeat(64) },
        { kind: "inputAudio", reference_digest: "c".repeat(64) },
        { kind: "inputText", text: "last" },
      ],
    },
  };
  const { container } = render(<NativeDynamicTool content={completed} />);
  expect(screen.getByText("Native success: false")).toBeTruthy();
  expect(screen.getByText("Native request: Resolved")).toBeTruthy();
  expect(screen.getByText("Reply delivery: Uncertain")).toBeTruthy();
  expect(screen.getByText("<script>inert()</script>")).toBeTruthy();
  expect(container.querySelectorAll("img,audio,script,a,button")).toHaveLength(
    0,
  );
  expect(
    Array.from(container.querySelectorAll("pre,p"))
      .slice(-4)
      .map((v) => v.textContent),
  ).toEqual([
    "<script>inert()</script>",
    "Image reference",
    "Audio reference",
    "last",
  ]);
});
test("keeps missing lifecycle evidence unknown", () => {
  const requestOnly = {
    request: tool.request,
    reply: { ...tool.reply, delivery: "not-sent" as const },
    resolved: false,
  };
  const { container } = render(<NativeDynamicTool content={requestOnly} />);
  expect(screen.getByText("unknown")).toBeTruthy();
  expect(container.textContent).not.toContain("Running");
});
test("shows the fixed unavailable reply even when delivery was not sent", () => {
  const requestOnly = {
    ...tool,
    reply: { ...tool.reply, delivery: "not-sent" as const },
  };
  render(<NativeDynamicTool content={requestOnly} />);
  expect(
    screen.getByText("This dynamic tool is unavailable in DeliDev."),
  ).toBeTruthy();
  expect(screen.getByText("Reply delivery: Not sent")).toBeTruthy();
});
test("rejects raw arguments, media locators, mixed unions and invalid identity", () => {
  for (const content of [
    { ...tool, arguments: { private: "hidden" } },
    { ...tool, reply: { ...tool.reply, token: "foreign" } },
    { ...tool, request: { ...tool.request, id_value: "9223372036854775808" } },
    {
      ...tool,
      snapshot: {
        ...tool.snapshot,
        content: [{ kind: "inputImage", imageUrl: "https://private.invalid" }],
      },
    },
    {
      ...tool,
      snapshot: {
        ...tool.snapshot,
        content: [{ kind: "input_text", text: "wrong union" }],
      },
    },
    {
      ...tool,
      snapshot: {
        ...tool.snapshot,
        content: [{ kind: "inputText", text: "x".repeat(256 * 1024 + 1) }],
      },
    },
  ])
    expect(dynamicTool(content)).toBeUndefined();
});

test("preserves a selected backend reply without showing unavailable", () => {
  const selected = {
    ...tool,
    reply: { ...tool.reply, unavailable: false, delivery: "transmitted" },
  };
  expect(dynamicTool(selected)).not.toBeNull();
  render(<NativeDynamicTool content={selected} />);
  expect(
    screen.queryByText("This dynamic tool is unavailable in DeliDev."),
  ).toBeNull();
  expect(screen.getByText("Reply delivery: Transmitted")).toBeTruthy();
  expect(screen.getByText("Native request: Open")).toBeTruthy();
});
