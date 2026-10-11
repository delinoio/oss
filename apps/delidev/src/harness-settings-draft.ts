// SPDX-License-Identifier: Apache-2.0
import { object, type Document } from "./documents";

const optionFields = [
  "permission",
  "claude_permission",
  "approval_policy",
  "approvals_reviewer",
  "subagent_model",
  "subagent_effort",
  "max_concurrency",
  "approval_review_model",
  "service_tier",
] as const;

// The existing editor projects explicit selectors for its controls. A displayed
// fallback is never persisted as an override unless the owner changes it.
export function harnessEditorDocument(saved: Document): Document {
  if (!saved.harness_settings) return saved;
  const settings = object(saved.harness_settings);
  const options: Document = { permission: "default" };
  for (const field of optionFields) {
    const selector = object(settings[field]);
    if (selector.mode === "override") options[field] = selector.value;
  }
  return {
    ...saved,
    effort:
      object(settings.effort).mode === "override"
        ? object(settings.effort).value
        : "",
    options,
  };
}

export function captureHarnessEditorChanges(
  saved: Document,
  edited: Document,
  requireTyped = false,
): Document {
  if (!saved.harness_settings && !requireTyped) return edited;
  const projected = harnessEditorDocument(saved);
  const settings: Document = saved.harness_settings
    ? { ...object(saved.harness_settings) }
    : { version: 1 };
  if (
    saved.harness_settings
      ? edited.effort !== projected.effort
      : Object.hasOwn(edited, "effort")
  ) {
    settings.effort = { mode: "override", value: edited.effort };
  }
  const before = object(projected.options),
    after = object(edited.options);
  for (const field of optionFields) {
    if (
      saved.harness_settings
        ? after[field] !== before[field]
        : Object.hasOwn(after, field)
    ) {
      settings[field] = {
        mode: "override",
        value:
          after[field] ??
          (field === "max_concurrency"
            ? 0
            : field === "permission"
              ? "default"
              : ""),
      };
    }
  }
  const result: Document = {
    ...edited,
    harness_settings: settings,
    options: { permission: "default" },
  };
  delete result.effort;
  return result;
}
