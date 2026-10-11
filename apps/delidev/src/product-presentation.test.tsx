// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { RepositoryRow } from "./repository-list";
import { SettingsActionScope } from "./settings-action";
import {
  PresentationLabels,
  PresentationLabelsProvider,
  diagnosticPresentation,
} from "./product-presentation";
const first = "019c0000-0000-7000-8000-000000000001";
const second = "019c0000-0000-7000-8000-000000000002";
it("retains numbers through reorder, refresh, missing names and newly observed targets", () => {
  const labels = new PresentationLabels();
  labels.observe(first, "Same", "repository");
  labels.observe(second, "Same", "repository");
  expect(labels.name(first, "Same", "repository")).toBe("Same · 1");
  expect(labels.name(second, "Same", "repository")).toBe("Same · 2");
  expect(labels.number(second, "repository")).toBe(2);
  expect(labels.name(first, "", "repository")).toBe(
    "Repository 1 · Name unavailable",
  );
  expect(
    labels.number("019c0000-0000-7000-8000-000000000003", "repository"),
  ).toBe(3);
  expect(labels.name(first, first, "repository")).toBe(first);
});
it("assigns labels to identity-only references without requiring resource documents", () => {
  const labels = new PresentationLabels();
  labels.observeIds([first, second], "account");
  expect(labels.identity(first, "account")).toBe("Account 1");
  expect(labels.identity(second, "account")).toBe("Account 2");
  expect(labels.identity("native-account-id", "account")).toBe(
    "native-account-id",
  );
});
it("projects only owned observation keys while preserving native/user bytes and original metadata", () => {
  const labels = new PresentationLabels();
  const original = {
    execution_id: first,
    correlation_id: second,
    native_thread_id: first,
    message: second,
    outcome: "failed",
    sequence: 17,
  };
  const displayed = labels.executionMetadata(original);
  expect(displayed).toEqual({
    execution_id: "Record 1",
    native_thread_id: first,
    message: second,
    outcome: "failed",
    sequence: 17,
  });
  expect(original.execution_id).toBe(first);
  expect(original.correlation_id).toBe(second);
  expect(
    diagnosticPresentation(`Unable to inspect ${first}. Refresh this record.`),
  ).toBe("Unable to inspect Internal reference. Refresh this record.");
});
it("removes repository UUID text and accessible action UUIDs without changing the action target", () => {
  const row = create(ResourceSchema, {
    id: first,
    kind: EntityKind.REPOSITORY,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      name: "oss",
      remote_url: "https://example.org/oss.git",
      checkouts: [],
    }),
  });
  const edit = vi.fn(),
    remove = vi.fn();
  const mounted = render(
    <PresentationLabelsProvider>
      <SettingsActionScope>
        <RepositoryRow
          row={row}
          edit={() => edit(row.id)}
          remove={() => remove(row.id)}
        />
      </SettingsActionScope>
    </PresentationLabelsProvider>,
  );
  expect(mounted.container.textContent).not.toContain(first);
  expect(mounted.container.querySelector(".repository-resource-id")).toBeNull();
  const button = screen.getByRole("button", {
    name: /Edit oss · Repository 1/,
  });
  expect(button.getAttribute("aria-label")).not.toContain(first);
  fireEvent.click(button);
  expect(edit).toHaveBeenCalledWith(first);
  fireEvent.click(
    screen.getByRole("button", { name: /Delete oss · Repository 1/ }),
  );
  expect(remove).toHaveBeenCalledWith(first);
});
