// SPDX-License-Identifier: Apache-2.0
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { NativeFunctionOutput, functionOutput } from "./native-function-output";
afterEach(cleanup);
const content = {
  name: "original_tool",
  namespace: "original_namespace",
  structured: true,
  content: [
    {
      kind: "input_text",
      text: "<script>inert()</script> https://private.example",
    },
    { kind: "input_image", reference_kind: "image_url", detail: "original" },
    { kind: "input_image", reference_kind: "file_id" },
    { kind: "input_audio", reference_kind: "audio_url" },
    { kind: "encrypted_content" },
    { kind: "input_text", text: "last" },
  ],
};
test("renders ordered inert text and reference markers without retrieval controls", () => {
  const { container } = render(<NativeFunctionOutput content={content} />);
  expect(screen.getByText("original_namespace.original_tool")).toBeTruthy();
  expect(
    screen.getByText("<script>inert()</script> https://private.example"),
  ).toBeTruthy();
  expect(
    container.querySelectorAll("script,a,img,audio,video,button,input,iframe"),
  ).toHaveLength(0);
  const parts = Array.from(container.querySelectorAll("pre,p"));
  expect(parts).toHaveLength(6);
  expect(parts[1]?.textContent).toContain("image_url");
  expect(parts[2]?.textContent).toContain("file_id");
  expect(parts[4]?.textContent).toContain("Encrypted");
  expect(parts[5]?.textContent).toBe("last");
});
test("preserves empty string and array as distinct safe variants", () => {
  expect(
    functionOutput({
      name: "tool",
      namespace: null,
      structured: false,
      text: "",
    })?.structured,
  ).toBe(false);
  expect(
    functionOutput({
      name: "tool",
      namespace: "",
      structured: true,
      content: [],
    })?.structured,
  ).toBe(true);
  expect(
    functionOutput({ name: "tool", namespace: null, structured: true })
      ?.structured,
  ).toBe(true);
});
for (const [name, value] of Object.entries({
  ciphertext: {
    ...content,
    content: [
      { kind: "encrypted_content", encrypted_content: "private-sentinel" },
    ],
  },
  locator: {
    ...content,
    content: [
      {
        kind: "input_image",
        reference_kind: "image_url",
        image_url: "https://private.example",
      },
    ],
  },
  detail: {
    ...content,
    content: [{ kind: "input_image", reference_kind: "file_id", detail: null }],
  },
  namespace: { name: "tool", structured: false, text: "" },
  mixed: { ...content, text: "conflict" },
  null: { ...content, content: null },
  surrogate: {
    name: "tool",
    namespace: null,
    structured: false,
    text: "\ud800",
  },
  oversized: {
    name: "tool",
    namespace: null,
    structured: false,
    text: "x".repeat((256 << 10) + 1),
  },
}))
  test(`rejects ${name} before display`, () => {
    expect(functionOutput(value)).toBeUndefined();
    const { container } = render(<NativeFunctionOutput content={value} />);
    expect(screen.getByText("Function result is unavailable.")).toBeTruthy();
    expect(container.textContent).not.toContain("private-sentinel");
  });

test("retains omitted and explicit-null namespace metadata as inert output", () => {
  for (const namespace_present of [false, true]) {
    const value = {
      name: "tool",
      namespace: null,
      namespace_present,
      structured: false,
      text: "inert",
    };
    expect(functionOutput(value)?.namespace_present).toBe(namespace_present);
    const { container, unmount } = render(
      <NativeFunctionOutput content={value} />,
    );
    expect(screen.getByText("tool")).toBeTruthy();
    expect(
      container.querySelectorAll("a,img,audio,video,button,input,iframe"),
    ).toHaveLength(0);
    unmount();
  }
  expect(
    functionOutput({ ...content, namespace_present: false }),
  ).toBeUndefined();
  expect(
    functionOutput({ ...content, namespace_present: "false" }),
  ).toBeUndefined();
});
