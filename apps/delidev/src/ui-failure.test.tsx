// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { FailureCode } from "@delinoio/delidev-api-client";
import { i18n } from "./localization";
import { Failure, failureSummary } from "./ui";

afterEach(async () => {
  await i18n.changeLanguage("en");
});

it.each([
  ["en", "The selected harness default could not be verified."],
  ["ko", "선택한 하네스의 기본값을 확인할 수 없습니다."],
])("presents unverified defaults without inventing a value in %s", async (language, summary) => {
  await i18n.changeLanguage(language);
  render(
    <Failure
      failure={{
        code: FailureCode.HarnessDefaultUnverified,
        message: "Original verification evidence",
        guidance: "Original selected-source guidance",
      }}
    />,
  );
  expect(screen.getByRole("alert").textContent).toContain(summary);
  expect(failureSummary("harness-default-unverified")).toContain(summary);
  expect(screen.getByText("Original verification evidence")).toBeTruthy();
  expect(screen.getByText("Original selected-source guidance")).toBeTruthy();
  expect(screen.queryByRole("button")).toBeNull();
});
