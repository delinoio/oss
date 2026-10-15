// SPDX-License-Identifier: Apache-2.0
// The Danger tone is an allow-list: only destructive branches named in the
// desktop contract table may carry it. This inventory fails when a new Danger
// site appears or an existing one disappears, so the contract table, the
// per-surface tests and this list must change together. Occurrences are
// counted by file; each count is explained by its row in the contract table.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, it } from "vitest";

const directory = join(process.cwd(), "src");
// opener + confirmation + explicit retry of the same deletion, per surface.
const expected: Record<string, number> = {
  "account-deletion.tsx": 3,
  "agent-worker-row.tsx": 1,
  "api-account-deletion.tsx": 2,
  "api-entry-row.tsx": 1,
  "appearance-controls.tsx": 2,
  "backups.tsx": 4,
  "browser-settings.tsx": 1,
  "configuration-actions.tsx": 2,
  "device-settings.tsx": 3,
  "integrations.tsx": 2,
  "local-reviews.tsx": 2,
  "network-settings.tsx": 3,
  "project-list.tsx": 1,
  "provider-model-settings.tsx": 1,
  "queue.tsx": 4,
  "repository-list.tsx": 1,
  "saved-connections.tsx": 3,
  "schedules.tsx": 3,
  "session-storage.tsx": 5,
  "settings.tsx": 1,
  "ssh-setup.tsx": 1,
  "subscription-settings.tsx": 2,
};

it("limits the Danger tone to the documented destructive sites", () => {
  const actual: Record<string, number> = {};
  for (const name of readdirSync(directory)) {
    if (!/\.tsx?$/.test(name) || /\.test\.tsx?$/.test(name)) continue;
    if (name === "settings-action.tsx" || name === "test-action-tone.ts")
      continue;
    const count = (
      readFileSync(join(directory, name), "utf8").match(
        /SettingsActionTone\.Danger/g,
      ) ?? []
    ).length;
    if (count) actual[name] = count;
  }
  expect(actual).toEqual(expected);
});
it("never derives the tone from visible copy or icon names", () => {
  for (const name of Object.keys(expected)) {
    const source = readFileSync(join(directory, name), "utf8");
    for (const match of source.matchAll(
      /(?:tone=\{|actionTone\()([^}]*?)SettingsActionTone\.Danger/gs,
    )) {
      expect(match[1], `${name}: ${match[0]}`).not.toMatch(
        /copy\(|SettingsActionIcon|label\(|textContent|includes\(/,
      );
    }
  }
});
