// SPDX-License-Identifier: Apache-2.0
import { createHash } from "node:crypto";
import { readFile, readdir } from "node:fs/promises";
import { join } from "node:path";
import { app } from "./run.mjs";

const terminalAssets = [];
async function check(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) await check(path);
    else if (entry.name.endsWith(".wasm")) terminalAssets.push(path);
    else if (/\.(js|html|json)$/.test(entry.name)) {
      const bytes = await readFile(path, "utf8");
      if (/__qa\/|delidev-qa-appearance|DeliDev browser QA|qa-environments|qa-owner\.json|__prSidebarFixture|__usageFixture|__accountRemediationFixture|__sessionRemediationFixture|__claudeRunnersFixture|__transcriptRoleFixture|__imageInputFixture|__sessionComposerFixture|__sessionHeaderFixture|__browserSplitFixture|__generalChatFixture|__projectSessionFixture|__planModeFixture|__apiDetailsFixture|__inboxFilterFixture|__apiVerificationFixture|__connectionsFixture|__subscriptionRailFixture|__shortcutPreferencesFixture|__commandMenuFixture|__terminalHistoryFixture/.test(bytes)) throw new Error("QA code leaked into the release frontend");

    }
  }
}
await check(join(app, "dist"));
if (terminalAssets.length !== 1) throw new Error("Expected one bundled terminal WASM asset");
if (!/ghostty-vt\.[a-zA-Z0-9]+\.wasm$/.test(terminalAssets[0])) throw new Error("Terminal WASM asset is not content hashed");
if (createHash("sha256").update(await readFile(terminalAssets[0])).digest("hex") !== "da382d54a9d1115e994802c90411e754fc1f7d090e3a28b34b465b187f41be6d") throw new Error("Bundled terminal WASM digest mismatch");
for (const name of await readdir(join(app,"public/terminal-notices"))) {
  const original=await readFile(join(app,"public/terminal-notices",name));
  if (!original.equals(await readFile(join(app,"dist/terminal-notices",name)))) throw new Error("Terminal notice distribution mismatch");
}
console.log(JSON.stringify({ operation: "qa-release-exclusion", result: "passed" }));
