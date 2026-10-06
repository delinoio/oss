// SPDX-License-Identifier: Apache-2.0
import { createServer } from "node:http";
import { lstat, readFile, realpath } from "node:fs/promises";
import { extname, resolve, sep } from "node:path";
import { QaError } from "./processes.mjs";

const types = { ".html": "text/html; charset=utf-8", ".js": "application/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png", ".woff2": "font/woff2" };
async function body(request) {
  const chunks = []; let size = 0;
  for await (const chunk of request) { size += chunk.length; if (size > 140_000) throw new QaError("request-too-large"); chunks.push(chunk); }
  try { return JSON.parse(Buffer.concat(chunks).toString("utf8")); } catch { throw new QaError("invalid-request"); }
}
export function closedObject(value, allowed) {
  if (!value || typeof value !== "object" || Array.isArray(value) || Object.keys(value).some(key => !allowed.includes(key))) throw new QaError("invalid-request");
  return value;
}

export async function createHost(assets, environment) {
  let accepting = true, active;
  const server = createServer(async (request, response) => {
    response.setHeader("Cache-Control", "no-store");
    response.setHeader("X-Content-Type-Options", "nosniff");
    response.setHeader("Referrer-Policy", "no-referrer");
    response.setHeader("Cross-Origin-Resource-Policy", "same-origin");
    const send = (status, value) => { response.writeHead(status, { "Content-Type": "application/json" }); response.end(JSON.stringify(value)); };
    try {
      if (request.headers.host !== new URL(host.origin).host || request.headers.origin && request.headers.origin !== host.origin || request.headers["sec-fetch-site"] === "cross-site") throw new QaError("origin-rejected");
      if (!accepting) throw new QaError("host-closing");
      const url = new URL(request.url, host.origin);
      if (url.search || url.hash) throw new QaError("invalid-request");
      if (url.pathname.startsWith("/__qa/")) {
        if (request.method === "GET" && url.pathname === "/__qa/bootstrap") { send(200, await environment.bootstrap()); return; }
        if (request.method === "GET" && url.pathname === "/__qa/status") { send(200, await environment.status()); return; }
        if (request.method !== "POST" || request.headers.origin !== host.origin || request.headers["content-type"] !== "application/json") throw new QaError("origin-rejected");
        if (active) throw new QaError("control-busy");
        // Set admission before reading the body, so simultaneous controls cannot
        // both enter while one request is still uploading an encrypted bundle.
        active = (async () => {
          const value = await body(request);
          if (url.pathname === "/__qa/start") { closedObject(value, []); return environment.startServer(); }
          if (url.pathname === "/__qa/worker") { closedObject(value, ["action", "generation"]); return environment.controlWorker(value); }
          if (url.pathname === "/__qa/proof") { closedObject(value, []); return environment.proof(); }
          if (url.pathname === "/__qa/network") { closedObject(value, ["machine", "action", "ciphertext", "digest"]); return environment.network(value); }
          throw new QaError("unknown-control");
        })();
        try { send(200, await active); } finally { active = undefined; }
        return;
      }
      if (request.method !== "GET") throw new QaError("invalid-request");
      const file = resolve(assets, `.${decodeURIComponent(url.pathname === "/" ? "/index.html" : url.pathname)}`);
      if (!file.startsWith(`${assets}${sep}`) || !(await lstat(file)).isFile() || await realpath(file) !== file) throw new QaError("asset-unavailable");
      response.setHeader("Content-Security-Policy", `default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self' ${environment.endpoint || "'none'"}; frame-src 'none'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'`);
      response.writeHead(200, { "Content-Type": types[extname(file)] ?? "application/octet-stream" }); response.end(await readFile(file));
    } catch (error) { send(error.code === "control-busy" ? 409 : error.code === "origin-rejected" ? 403 : 503, { code: error instanceof QaError ? error.code : "host-request-failed" }); }
  });
  let closing;
  const host = { origin: "", close() {
    closing ??= (async () => { accepting = false; await active?.catch(() => {}); server.closeIdleConnections(); await new Promise(resolve => server.close(resolve)); })();
    return closing;
  }, server };
  server.requestTimeout = 15_000; server.headersTimeout = 10_000;
  await new Promise((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolve); });
  host.origin = `http://127.0.0.1:${server.address().port}`;
  return host;
}
