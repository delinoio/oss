// SPDX-License-Identifier: Apache-2.0
import { readFile, readdir } from "node:fs/promises";
import { join } from "node:path";
import { app } from "./run.mjs";

async function check(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) await check(path);
    else if (/\.(js|html|json)$/.test(entry.name)) {
      const bytes = await readFile(path, "utf8");
      if (/__qa\/|delidev-qa-appearance|DeliDev browser QA|qa-environments|qa-owner\.json|__prSidebarFixture|__usageFixture|__accountRemediationFixture|__sessionRemediationFixture|__claudeRunnersFixture|__transcriptRoleFixture|__imageInputFixture|__sessionComposerFixture|__browserSplitFixture|__generalChatFixture|__planModeFixture|__apiDetailsFixture|__inboxFilterFixture|__apiVerificationFixture/.test(bytes)) throw new Error("QA code leaked into the release frontend");

    }
  }
}
await check(join(app, "dist"));
console.log(JSON.stringify({ operation: "qa-release-exclusion", result: "passed" }));
