// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { expect, it, vi } from "vitest";
import { readAutomaticCreationMode } from "./creation-plan-defaults";
import { encode, Mode } from "./documents";

function row(kind: EntityKind, data: object): Resource {
  return create(ResourceSchema, { kind, id: newRequestId(), revision: 2n, schemaVersion: 3, documentJson: encode(data) });
}

it("accepts the absent legacy global default and a complete newer Project override", async () => {
  const project = row(EntityKind.PROJECT, { name: "Fixture", repositories: [], settings: { plan_mode_default: "enabled" } });
  const reader = { listResources: vi.fn(async () => ({ resources: [] as Resource[] })), getResource: vi.fn(async () => ({ resource: project })) };
  expect(await readAutomaticCreationMode(reader, "")).toBe(Mode.Execute);
  expect(reader.getResource).not.toHaveBeenCalled();
  expect(await readAutomaticCreationMode(reader, project.id, undefined, { ...project, revision: 1n })).toBe(Mode.Plan);
  expect(reader.getResource).toHaveBeenCalledWith({ kind: EntityKind.PROJECT, id: project.id });
});

it("rejects incomplete, regressed, foreign and malformed default sources", async () => {
  const settings = row(EntityKind.SETTINGS, { plan_mode_default: true, automatic_plan_approval: false, branch_prefix: "delidev/" });
  const project = row(EntityKind.PROJECT, { name: "Fixture", repositories: [], settings: { plan_mode_default: "inherit" } });
  const cases = [
    { resources: [settings], nextPageToken: "unread" },
    { resources: [settings, settings] },
    { resources: [] },
    { resources: [{ ...settings, revision: 0n }] },
    { resources: [{ ...settings, revision: 1n }] },
    { resources: [{ ...settings, id: newRequestId() }] },
    { resources: [{ ...settings, schemaVersion: 99 }] },
    { resources: [{ ...settings, documentJson: encode({ plan_mode_default: false, automatic_plan_approval: false, branch_prefix: "delidev/" }) }] },
    { resources: [{ ...settings, documentJson: encode({ plan_mode_default: "true" }) }] },
  ];
  for (const page of cases) {
    const reader = { listResources: vi.fn(async () => page), getResource: vi.fn(async () => ({ resource: project })) };
    await expect(readAutomaticCreationMode(reader, project.id, settings, project)).rejects.toThrow();
  }
  for (const current of [undefined, { ...project, revision: 1n }, { ...project, id: newRequestId() }, { ...project, documentJson: encode({ name: "Fixture", repositories: [], settings: { plan_mode_default: "other" } }) }]) {
    const reader = { listResources: vi.fn(async () => ({ resources: [settings] })), getResource: vi.fn(async () => ({ resource: current })) };
    await expect(readAutomaticCreationMode(reader, project.id, settings, project)).rejects.toThrow();
  }
});
