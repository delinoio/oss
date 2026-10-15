// SPDX-License-Identifier: Apache-2.0
// Builds the real rsbuild config and serves the output at the root and under a
// nested path to prove emitted CSS asset URLs resolve against the stylesheet.
import { createHash } from "node:crypto";
import assert from "node:assert/strict";
import { mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { createRsbuild, loadConfig } from "@rsbuild/core";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const RASTER_MARKS = ["codex.webp", "claude-code.png"];
const MIME = {
  ".html": "text/html",
  ".js": "application/javascript",
  ".css": "text/css",
  ".wasm": "application/wasm",
  ".png": "image/png",
  ".webp": "image/webp",
  ".svg": "image/svg+xml",
};
const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");

async function build(assetPrefix) {
  const directory = await mkdtemp(join(tmpdir(), "delidev-asset-prefix-"));
  const config = (await loadConfig({ cwd: app })).content;
  const instance = await createRsbuild({
    cwd: app,
    rsbuildConfig: {
      ...config,
      output: {
        ...config.output,
        ...(assetPrefix === undefined ? {} : { assetPrefix }),
        distPath: { root: directory },
      },
    },
  });
  await instance.build();
  return { directory, configuredPrefix: config.output.assetPrefix };
}

async function files(directory, prefix = "") {
  const found = [];
  for (const entry of await readdir(join(directory, prefix), {
    withFileTypes: true,
  })) {
    const path = join(prefix, entry.name);
    if (entry.isDirectory()) found.push(...(await files(directory, path)));
    else found.push(path.split(sep).join("/"));
  }
  return found;
}

async function serve(directory, base) {
  const server = createServer(async (request, response) => {
    const pathname = new URL(request.url, "http://127.0.0.1").pathname;
    if (!pathname.startsWith(base)) return void response.writeHead(404).end();
    const relative = pathname.slice(base.length) || "/";
    const file = resolve(
      directory,
      `.${relative === "/" ? "/index.html" : relative}`,
    );
    try {
      if (!file.startsWith(`${directory}${sep}`)) throw new Error("outside");
      response.setHeader(
        "Content-Type",
        MIME[extname(file)] ?? "application/octet-stream",
      );
      response.end(await readFile(file));
    } catch {
      response.writeHead(404).end();
    }
  });
  await new Promise((done) => server.listen(0, "127.0.0.1", done));
  return { server, origin: `http://127.0.0.1:${server.address().port}` };
}

// Returns CSS file-backed URLs that fail to resolve to the expected raster bytes.
async function brokenRasterUrls(directory, base) {
  const { server, origin } = await serve(directory, base);
  try {
    const sources = new Map(
      await Promise.all(
        RASTER_MARKS.map(async (file) => [
          sha256(await readFile(join(app, "public/harness-marks", file))),
          file,
        ]),
      ),
    );
    const html = await (await fetch(`${origin}${base}/`)).text();
    const stylesheets = [...html.matchAll(/<link[^>]+href="([^"]+\.css)"/g)];
    assert(stylesheets.length > 0, "index.html must link a stylesheet");
    const broken = [];
    const seen = new Set();
    for (const [, href] of stylesheets) {
      const cssUrl = new URL(href, `${origin}${base}/`);
      const css = await (await fetch(cssUrl)).text();
      for (const [, raw] of css.matchAll(/url\(\s*["']?([^"')]+)["']?\s*\)/g)) {
        if (raw.startsWith("data:")) continue;
        const resolved = new URL(raw, cssUrl);
        const response = await fetch(resolved);
        const bytes = Buffer.from(await response.arrayBuffer());
        const digest = sha256(bytes);
        if (!response.ok || !sources.has(digest)) {
          broken.push(`${resolved.pathname} (${response.status})`);
        } else seen.add(sources.get(digest));
      }
    }
    assert.deepEqual([...seen].sort(), [...RASTER_MARKS].sort());
    return broken;
  } finally {
    await new Promise((done) => server.close(done));
  }
}

test("production asset policy is auto", async () => {
  const config = (await loadConfig({ cwd: app })).content;
  assert.equal(config.output.assetPrefix, "auto");
});

test("built CSS marks resolve at root and nested paths", async () => {
  const { directory } = await build();
  try {
    const all = await files(directory);
    assert(all.some((file) => file.endsWith(".wasm")), "WASM is emitted");
    assert(all.some((file) => /static\/js\/.+\.js$/.test(file)), "JS chunks");
    for (const base of ["", "/nested/delidev"])
      assert.deepEqual(await brokenRasterUrls(directory, base), [], base);
    // Entry scripts and stylesheets referenced by HTML must load in both places.
    for (const base of ["", "/nested/delidev"]) {
      const { server, origin } = await serve(directory, base);
      try {
        const html = await (await fetch(`${origin}${base}/`)).text();
        const refs = [...html.matchAll(/(?:src|href)="([^"]+\.(?:js|css))"/g)];
        assert(refs.length > 0);
        for (const [, ref] of refs) {
          const response = await fetch(new URL(ref, `${origin}${base}/`));
          assert.equal(response.status, 200, `${base} ${ref}`);
        }
      } finally {
        await new Promise((done) => server.close(done));
      }
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("negative control: the './' policy breaks CSS image URLs", async () => {
  const { directory } = await build("./");
  try {
    const broken = await brokenRasterUrls(directory, "").catch((error) => error);
    // Either every raster URL is broken (404) or the digest set is incomplete.
    assert(
      broken instanceof Error || broken.length > 0,
      "The './' policy must be rejected by this check",
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
