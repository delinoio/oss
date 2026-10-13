// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { Resource } from "@delinoio/delidev-api-client";
import {
  customAllowed,
  QuestionFamily,
  RequestResponse,
  singleChoice,
} from "./interaction";
import { en, ko } from "./localization";
import { documentBytes } from "./state";

afterEach(cleanup);

enum Family {
  Ordinary = "ordinary",
  Claude = "claude",
  OpenCode = "opencode",
  Grok = "grok",
}
type Question = Record<string, unknown>;

const options = [{ label: "A" }, { label: "B" }];
const documentFor = (family: Family, question: Question) => {
  const base = { closure: "open", type: "user-question" };
  switch (family) {
    case Family.Ordinary:
      return { ...base, questions: { questions: [question] } };
    case Family.OpenCode:
      return { ...base, opencode: { questions: [question] } };
    case Family.Claude:
      return {
        ...base,
        claude: { input_json: JSON.stringify({ questions: [question] }) },
      };
    case Family.Grok:
      return {
        ...base,
        grok: { event: { payload: { questions: [question] } } },
      };
  }
};
const mount = (family: Family, question: Question) => {
  const send = vi.fn(async () => {});
  render(
    <RequestResponse
      resource={
        { documentJson: documentBytes(documentFor(family, question)) } as Resource
      }
      enabled
      copy={en}
      send={send}
    />,
  );
  return send;
};
const submit = () => fireEvent.click(screen.getByText(en.submit));
const textarea = () => screen.queryByLabelText(en.custom);
const type = (value: string) =>
  fireEvent.change(textarea()!, { target: { value } });
const pick = (label: string) => fireEvent.click(screen.getByLabelText(label));
const sent = (send: ReturnType<typeof mount>) =>
  (send.mock.calls[0] as unknown[])[1];

it("keeps localized catalogs key-aligned for the custom copy", () => {
  expect(Object.keys(en).sort()).toEqual(Object.keys(ko).sort());
});

it("hides custom text for ordinary questions with options and other absent", () => {
  const send = mount(Family.Ordinary, { id: "q", question: "Pick", options });
  expect(textarea()).toBeNull();
  expect(screen.getByText(en.submit)).toHaveProperty("disabled", true);
  pick("A");
  submit();
  expect(sent(send)).toEqual({ answers: { q: ["A"] } });
});

it("hides custom text for ordinary questions with other false", () => {
  mount(Family.Ordinary, { id: "q", options, other: false });
  expect(textarea()).toBeNull();
});

it("submits custom text for ordinary questions with other true", () => {
  const send = mount(Family.Ordinary, { id: "q", options, other: true });
  type("mine");
  submit();
  expect(sent(send)).toEqual({ answers: { q: ["mine"] } });
});

it("keeps free text for ordinary questions without options", () => {
  const send = mount(Family.Ordinary, { id: "q" });
  type("free");
  submit();
  expect(sent(send)).toEqual({ answers: { q: ["free"] } });
});

it("derives custom-text and single-choice eligibility per question family", () => {
  const ordinary: Question = { id: "q", options };
  const opencode: Question = { id: "q", options, custom: false };
  expect(customAllowed(QuestionFamily.Ordinary, ordinary)).toBe(false);
  expect(customAllowed(QuestionFamily.OpenCode, opencode)).toBe(false);
  expect(customAllowed(QuestionFamily.OpenCode, { id: "q" })).toBe(true);
  expect(customAllowed(QuestionFamily.Claude, ordinary)).toBe(true);
  expect(customAllowed(QuestionFamily.Grok, ordinary)).toBe(true);
  expect(singleChoice(QuestionFamily.OpenCode, ordinary)).toBe(true);
  expect(singleChoice(QuestionFamily.OpenCode, { multiple: true })).toBe(false);
  expect(singleChoice(QuestionFamily.Claude, ordinary)).toBe(false);
  expect(singleChoice(QuestionFamily.Ordinary, ordinary)).toBe(false);
});

it("hides custom text for OpenCode questions with custom false", () => {
  const send = mount(Family.OpenCode, { options, custom: false });
  expect(textarea()).toBeNull();
  pick("A");
  submit();
  expect(sent(send)).toEqual({ opencode: { answers: [["A"]] } });
});

it("allows OpenCode custom text when custom is absent", () => {
  const send = mount(Family.OpenCode, { options });
  type("mine");
  submit();
  expect(sent(send)).toEqual({ opencode: { answers: [["mine"]] } });
});

it("renders OpenCode single choice as radios and keeps only the last pick", () => {
  const send = mount(Family.OpenCode, { options });
  expect(screen.getByLabelText("A")).toHaveProperty("type", "radio");
  pick("A");
  pick("B");
  expect(screen.getByLabelText("A")).toHaveProperty("checked", false);
  submit();
  expect(sent(send)).toEqual({ opencode: { answers: [["B"]] } });
});

it("switches between an offered option and custom text in single choice", () => {
  const send = mount(Family.OpenCode, { options, multiple: false });
  pick("A");
  type("mine");
  expect(screen.getByLabelText("A")).toHaveProperty("checked", false);
  pick("B");
  expect(textarea()).toHaveProperty("value", "");
  submit();
  expect(sent(send)).toEqual({ opencode: { answers: [["B"]] } });
});

it("submits custom text alone after switching away from an option", () => {
  const send = mount(Family.OpenCode, { options });
  pick("A");
  type("mine");
  submit();
  expect(sent(send)).toEqual({ opencode: { answers: [["mine"]] } });
});

it("keeps checkboxes and appended custom text for OpenCode multiple", () => {
  const send = mount(Family.OpenCode, { options, multiple: true });
  expect(screen.getByLabelText("A")).toHaveProperty("type", "checkbox");
  pick("A");
  pick("B");
  type("mine");
  submit();
  expect(sent(send)).toEqual({ opencode: { answers: [["A", "B", "mine"]] } });
});

it("keeps the leave-unanswered control for native OpenCode questions", () => {
  const send = mount(Family.OpenCode, { options });
  pick("A");
  fireEvent.click(screen.getByLabelText(en.leave));
  submit();
  expect(sent(send)).toEqual({ opencode: { answers: [[]] } });
});

it("blocks protected OpenCode questions", () => {
  mount(Family.OpenCode, { options, secret: true });
  expect(screen.getByText(en.secret)).toBeTruthy();
  expect(screen.getByText(en.submit)).toHaveProperty("disabled", true);
});

it("keeps Claude checkboxes with custom text joined", () => {
  const send = mount(Family.Claude, { question: "Pick", options });
  expect(screen.getByLabelText("A")).toHaveProperty("type", "checkbox");
  pick("A");
  pick("B");
  type("mine");
  submit();
  expect(sent(send)).toEqual({
    claude: { behavior: "allow", answers: { Pick: "A, B, mine" } },
  });
});

it("keeps Grok checkboxes with custom text joined", () => {
  const send = mount(Family.Grok, { question: "Pick", options });
  pick("A");
  pick("B");
  type("mine");
  submit();
  expect(sent(send)).toEqual({
    grok: { outcome: "accepted", answers: { Pick: "A, B, mine" } },
  });
});
