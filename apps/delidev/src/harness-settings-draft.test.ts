// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import {
  captureHarnessEditorChanges,
  harnessEditorDocument,
} from "./harness-settings-draft";

it("preserves inherited and explicit selectors through unrelated editor changes and JSON restart", () => {
  const saved = {
    name: "Original",
    harness_settings: {
      version: 1,
      effort: { mode: "inherit" },
      max_concurrency: { mode: "override", value: 0 },
      service_tier: { mode: "override", value: "" },
    },
    options: { permission: "default" },
    routes: [
      {
        model_selection: { mode: "inherit" },
        model: { native_id: "", metadata_source: "unknown" },
      },
    ],
  };
  const draft = harnessEditorDocument(saved);
  const next = captureHarnessEditorChanges(saved, {
    ...draft,
    name: "Renamed",
  });
  const reopened = JSON.parse(JSON.stringify(next));
  expect(reopened.harness_settings).toEqual(saved.harness_settings);
  expect(reopened.routes).toEqual(saved.routes);
  expect(reopened.effort).toBeUndefined();
  expect(
    captureHarnessEditorChanges(reopened, harnessEditorDocument(reopened)),
  ).toEqual(reopened);
});
it("records only actual edits including an explicit empty string or zero", () => {
  const saved = {
    harness_settings: {
      version: 1,
      effort: { mode: "override", value: "high" },
      max_concurrency: { mode: "override", value: 3 },
    },
    options: { permission: "default" },
  };
  const projected = harnessEditorDocument(saved);
  const changed = captureHarnessEditorChanges(saved, {
    ...projected,
    effort: "",
    options: {
      ...(projected.options as Record<string, unknown>),
      max_concurrency: 0,
    },
  });
  expect(changed.harness_settings).toEqual({
    version: 1,
    effort: { mode: "override", value: "" },
    max_concurrency: { mode: "override", value: 0 },
  });
});
it("normalizes a new inherited route without manufacturing omitted choices", () => {
  const created = captureHarnessEditorChanges(
    {},
    { name: "New", options: { permission: "default" } },
    true,
  );
  expect(created.harness_settings).toEqual({
    version: 1,
    permission: { mode: "override", value: "default" },
  });
  expect(JSON.parse(JSON.stringify(created))).toEqual(created);
});
