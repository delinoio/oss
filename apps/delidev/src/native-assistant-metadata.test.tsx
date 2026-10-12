// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import {
  NativeAssistantMetadata,
  AssistantDeliveryLabel,
  originalAssistantMetadata,
} from "./native-assistant-metadata";
import { i18n } from "./localization";
const value = {
  started: { delivery: null, questions: null },
  completed: {
    delivery: "async",
    questions: [
      { title: "Original null", options: null },
      { title: "Original empty", options: [] },
      { title: "Original choices", options: ["B", "A"] },
    ],
  },
};
afterEach(async () => {
  await i18n.changeLanguage("en");
});
test("retains async provenance and ordered inert nullable question options", () => {
  const choose = vi.fn();
  const { container } = render(
    <>
      <AssistantDeliveryLabel value={value} state="complete" />
      <NativeAssistantMetadata
        value={value}
        state="complete"
        suggestion={{ disabled: false, choose }}
      />
    </>,
  );
  expect(screen.getByText("Asynchronous message")).toBeTruthy();
  expect(
    [...container.querySelectorAll("button")].map(
      (button) => button.textContent,
    ),
  ).toEqual(["B", "A"]);
  fireEvent.click(screen.getByRole("button", { name: "B" }));
  expect(choose).toHaveBeenCalledExactlyOnceWith("B");
  expect(screen.getByText("Original null")).toBeTruthy();
  expect(screen.getByText("Original empty")).toBeTruthy();
  expect(container.querySelectorAll("form,input,textarea,a")).toHaveLength(0);
});
test("occupied drafts disable choices; read-only history has no reply or send buttons", () => {
  const choose = vi.fn();
  const { rerender, container } = render(
    <NativeAssistantMetadata
      value={value}
      state="complete"
      suggestion={{ disabled: true, occupied: true, choose }}
    />,
  );
  const option = screen.getByRole("button", { name: "A" }) as HTMLButtonElement;
  expect(option.disabled).toBe(true);
  fireEvent.click(option);
  expect(choose).not.toHaveBeenCalled();
  expect(
    screen.getByText("Clear your draft to use a suggestion."),
  ).toBeTruthy();
  rerender(<NativeAssistantMetadata value={value} state="complete" />);
  expect(container.querySelectorAll("button")).toHaveLength(0);
  expect(screen.getByText("A")).toBeTruthy();
});
test("unknown and reordered metadata remains unavailable", () => {
  for (const bad of [
    { completed: { delivery: "sync" } },
    { completed: { questions: [{ title: "q" }] } },
    { completed: { questions: [{ title: "q", options: null, extra: 1 }] } },
    {
      started: { questions: [{ title: "q", options: ["A", "B"] }] },
      completed: { questions: [{ title: "q", options: ["B", "A"] }] },
    },
  ])
    expect(originalAssistantMetadata(bad, "complete")).toBeUndefined();
  expect(
    originalAssistantMetadata({ completed: { questions: [] } }, "complete")
      ?.questions,
  ).toEqual([]);
  expect(
    originalAssistantMetadata({ completed: { questions: null } }, "complete")
      ?.questions,
  ).toBeNull();
});
test("Korean preserves exact original choice text", async () => {
  await i18n.changeLanguage("ko");
  render(
    <>
      <AssistantDeliveryLabel value={value} state="complete" />
      <NativeAssistantMetadata value={value} state="complete" />
    </>,
  );
  expect(screen.getByText("비동기 메시지")).toBeTruthy();
  expect(screen.getByText("Original choices")).toBeTruthy();
  expect(screen.getByText("B")).toBeTruthy();
});
