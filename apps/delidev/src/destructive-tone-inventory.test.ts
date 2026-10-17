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
  "session-pull-requests.tsx": 2,
  "session-storage.tsx": 5,
  "settings.tsx": 2,
  "ssh-setup.tsx": 1,
  "subscription-settings.tsx": 2,
  "worker-removal.tsx": 2,
};

// Modules that invoke a destructive-named operation but render no Danger
// control. The reason names the contract row that keeps them neutral or the
// module that owns the opener.
const reviewedNeutral: Record<string, string> = {
  "account-connection.tsx":
    "Disconnect and its credential-cleanup retry stay Default",
  "browser-preference-controller.tsx":
    "controller only; the single Reset browser settings action is in browser-settings.tsx",
  "claude-usage-tracking.tsx":
    "Disconnect usage and its retry stay Default; it never deletes the execution account",
  "desktop.tsx":
    "bridge owner only; the Remove opener, confirmation and retry are in saved-connections.tsx",
  "image-drafts.tsx": "removes an unsent draft attachment",
  "opencode-go-account.tsx": "Disconnect and its retry stay Default",
  "pr-problems.tsx":
    "dismissal is a recoverable local state change that retains the record",
  "pull-requests.tsx":
    "dismissal is a recoverable local state change that retains the record",
  "session-revert.tsx": "revert controls stay Default",
  "subscription-accounts.tsx":
    "Disconnect and Log out stay Default; account deletion opens account-deletion.tsx",
  "tray-status.tsx": "dismisses a transient tray status, not persisted data",
};
// Operation helpers whose name starts with a destructive verb. Query helper
// suffixes (for example Query.deleteBackup) and bridge command names are
// matched; DOM, cache and local-state helpers are not.
const verbs =
  "delete|remove|unlink|revoke|forget|discard|purge|erase|reset|clear|disconnect|dismiss|revert|wipe";
const destructiveOperation = new RegExp(
  String.raw`\b[A-Z]\w*Query\.(?:${verbs})[A-Z]\w*|\bclients?\.\w+\.(?:${verbs})[A-Z]\w*|\bclient\(\)\.(?:${verbs})[A-Z]\w*|invoke(?:<[^>]*>)?\(\s*["'](?:\w*?_)?(?:${verbs})\w*["']`,
  "g",
);
function sourceNames() {
  return readdirSync(directory).filter(
    (name) =>
      /\.tsx?$/.test(name) &&
      !/\.test\.tsx?$/.test(name) &&
      !/fixture/.test(name) &&
      name !== "settings-action.tsx" &&
      name !== "test-action-tone.ts",
  );
}

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
it("requires every module with a destructive operation to be Danger-marked or reviewed as neutral", () => {
  const operating = sourceNames().filter((name) =>
    readFileSync(join(directory, name), "utf8").match(destructiveOperation),
  );
  const unreviewed = operating.filter(
    (name) => !(name in expected) && !(name in reviewedNeutral),
  );
  expect(unreviewed, "add Danger tone or a reviewed neutral reason").toEqual(
    [],
  );
  // A stale reviewed entry would hide a future destructive control.
  expect(
    Object.keys(reviewedNeutral).filter((name) => !operating.includes(name)),
  ).toEqual([]);
  expect(
    Object.keys(reviewedNeutral).filter((name) => name in expected),
  ).toEqual([]);
});
