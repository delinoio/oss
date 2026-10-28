// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { readFile, readdir, access } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const app = fileURLToPath(new URL("../", import.meta.url));
const root = path.join(app, "docs/tairouter");
async function markdownFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = await Promise.all(
    entries.map(async (entry) => {
      const file = path.join(directory, entry.name);
      return entry.isDirectory()
        ? markdownFiles(file)
        : file.endsWith(".md")
          ? [file]
          : [];
    }),
  );
  return files.flat();
}

test("public pages have resolvable local links and no internal contract links", async () => {
  const files = await markdownFiles(root);
  assert.ok(files.length > 0);
  for (const file of files) {
    const content = await readFile(file, "utf8");
    assert.match(content, /^# .+/m, file);
    assert.doesNotMatch(
      content,
      /(?:\/tmp\/|AGENTS\.md|(?:apps|servers|packages|protos|packaging)\/tairouter|repository-[\w-]+-contract\.md|project-tairouter\.md)/,
      file,
    );
    for (const [, link] of content.matchAll(/\[[^\]]*\]\(([^)]+)\)/g)) {
      if (/^(?:https?:|mailto:|#)/.test(link)) continue;
      const target = path.resolve(path.dirname(file), link.split("#")[0]);
      assert.ok(
        target.startsWith(root + path.sep),
        `${file}: link leaves public content: ${link}`,
      );
      await access(target);
    }
  }
});

test("every public page is reachable from standard-theme navigation", async () => {
  const config = await readFile(path.join(app, "rspress.config.ts"), "utf8");
  const linked = new Set(
    [...config.matchAll(/link:\s*["']([^"']+)["']/g)].map((match) => match[1]),
  );
  for (const file of await markdownFiles(root)) {
    const relative = path.relative(root, file).replaceAll(path.sep, "/");
    const route =
      relative === "index.md"
        ? "/tairouter/"
        : "/tairouter/" + relative.replace(/\.md$/, "");
    assert.ok(linked.has(route), `No navigation entry for ${route}`);
  }
  for (const route of [...linked].filter((route) =>
    route.startsWith("/tairouter/"),
  )) {
    await access(
      path.join(
        root,
        route === "/tairouter/"
          ? "index.md"
          : route.slice("/tairouter/".length) + ".md",
      ),
    );
  }
});
