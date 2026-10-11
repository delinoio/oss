// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { HarnessInheritanceFields } from "./configuration-fields";
import type { Document } from "./documents";

it("preserves empty and zero overrides and removes their value only on explicit inheritance", () => {
  function Fixture() {
    const [data, change] = useState<Document>({ effort: "", options: { permission: "default", max_concurrency: 0 }, harness_settings: { effort: { mode: "inherit" }, options: { mode: "inherit" } } });
    return <><HarnessInheritanceFields data={data} change={change} /><output data-testid="selection">{JSON.stringify(data.harness_settings)}</output></>;
  }
  render(<Fixture />);
  const effort = screen.getByRole("combobox", { name: "Reasoning effort" });
  const options = screen.getByRole("combobox", { name: "Native options" });
  fireEvent.change(effort, { target: { value: "override" } });
  fireEvent.change(options, { target: { value: "override" } });
  expect(JSON.parse(screen.getByTestId("selection").textContent!)).toEqual({ effort: { mode: "override", value: "" }, options: { mode: "override", value: { permission: "default", max_concurrency: 0 } } });
  fireEvent.change(effort, { target: { value: "inherit" } });
  expect(JSON.parse(screen.getByTestId("selection").textContent!)).toEqual({ effort: { mode: "inherit" }, options: { mode: "override", value: { permission: "default", max_concurrency: 0 } } });
});
