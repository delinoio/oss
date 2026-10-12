// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { decodeManagedSkills } from "./managed-skills-settings";
import { encode } from "./documents";
const entry = {
  id: "package",
  worker_device_id: "worker",
  revision: "9007199254740993",
  content_revision: "a".repeat(64),
  name: "example",
  description: "Bounded fixture",
  enabled: true,
  provenance: "worker-import",
  support: [],
};
describe("Worker-owned managed skill projection", () => {
  it("retains exact revisions beyond JavaScript number precision", () => {
    expect(decodeManagedSkills(encode([entry]), "worker")[0].revision).toBe(
      "9007199254740993",
    );
  });
  it("rejects foreign owners, duplicate identities and invalid package digests", () => {
    expect(() => decodeManagedSkills(encode([entry]), "foreign")).toThrow();
    expect(() =>
      decodeManagedSkills(encode([entry, entry]), "worker"),
    ).toThrow();
    expect(() =>
      decodeManagedSkills(
        encode([{ ...entry, content_revision: "unknown" }]),
        "worker",
      ),
    ).toThrow();
  });
});
