// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import http from "node:http";
import test from "node:test";

const moduleUrl = new URL("./ccow-publish.mjs", import.meta.url).href;
const hasGh = !spawnSync("gh", ["--version"]).error;

test(
  "real gh transport sends an exact upload length and preserves binary readback",
  { skip: !hasGh },
  async (t) => {
    const bytes = Buffer.from([0, 27, 255, 10, 13, 42, 127]);
    const received = [];
    const server = http.createServer((request, response) => {
      if (request.method === "POST") {
        const chunks = [];
        request.on("data", (chunk) => chunks.push(chunk));
        request.on("end", () => {
          received.push({
            length: request.headers["content-length"],
            transfer: request.headers["transfer-encoding"],
            bytes: Buffer.concat(chunks),
          });
          response.writeHead(request.headers["content-length"] ? 201 : 411, {
            "content-type": "application/json",
          });
          response.end(JSON.stringify({ id: 7 }));
        });
      } else if (request.url === "/redirect") {
        response.writeHead(302, { location: "/binary" });
        response.end();
      } else if (request.url === "/failure") {
        response.writeHead(422, { "content-type": "application/json" });
        response.end(JSON.stringify({ message: "fixture-private-metadata" }));
      } else {
        response.writeHead(200, {
          "content-type": "application/octet-stream",
          "content-length": bytes.length,
        });
        response.end(bytes);
      }
    });
    await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
    t.after(() => server.close());
    const url = `http://127.0.0.1:${server.address().port}`;
    const code = `import assert from 'node:assert/strict';
import {githubRequest, GitHubRequestError} from ${JSON.stringify(moduleUrl)};
const bytes=Buffer.from(${JSON.stringify([...bytes])});
assert.deepEqual(githubRequest(${JSON.stringify(url + "/upload")}, {method:'POST',bytes}),{id:7});
assert.deepEqual(githubRequest(${JSON.stringify(url + "/redirect")}, {binary:true}),bytes);
assert.throws(()=>githubRequest(${JSON.stringify(url + "/failure")}), error=>error instanceof GitHubRequestError && error.code==='http_failure' && error.status===422 && !error.message.includes('fixture-private-metadata'));`;
    const child = spawn(process.execPath, ["--input-type=module", "-e", code], {
      env: {
        ...process.env,
        GH_TOKEN: "local-fixture",
        GITHUB_TOKEN: "local-fixture",
        GH_HOST: "github.com",
      },
      stdio: ["ignore", "pipe", "pipe"],
    });
    let output = "";
    child.stdout.on("data", (bytes) => (output += bytes));
    child.stderr.on("data", (bytes) => (output += bytes));
    const status = await new Promise((resolve, reject) => {
      child.on("error", reject);
      child.on("close", resolve);
    });
    assert.equal(status, 0, output);
    assert.equal(received.length, 1);
    assert.equal(received[0].length, String(bytes.length));
    assert.equal(received[0].transfer, undefined);
    assert.deepEqual(received[0].bytes, bytes);
  },
);
