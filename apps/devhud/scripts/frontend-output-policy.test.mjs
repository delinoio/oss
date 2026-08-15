import assert from "node:assert/strict";
import test from "node:test";

import {
  hasExactCspDirectiveSources,
  hasExecutableRemoteLoad,
} from "./frontend-output-policy.mjs";

const remoteLoads = [
  '<script src="https://example.com/app.js"></script>',
  '<link href="//example.com/app.css" rel="stylesheet">',
  'fetch("//example.com/data")',
  'import("https://example.com/module.js")',
  'new WebSocket("wss://example.com/socket")',
  "new WebSocket('ws://example.com/socket')",
  'new EventSource("https://example.com/events")',
  'const request = new XMLHttpRequest(); request.open("GET", "https://example.com/data")',
  'request.open("POST", "//example.com/data")',
  '@import "https://example.com/theme.css";',
  "@import url('//example.com/theme.css');",
  ".icon { background-image: url(https://example.com/icon.svg); }",
  '.font { src: url("//example.com/font.woff2"); }',
];

for (const remoteLoad of remoteLoads) {
  test(`rejects remote frontend load: ${remoteLoad}`, () => {
    assert.equal(hasExecutableRemoteLoad(remoteLoad), true);
  });
}

test("allows bundled and inline frontend resources", () => {
  const bundledLoads = [
    '<script src="/assets/app.js"></script>',
    'fetch("./data.json")',
    'new EventSource("/events")',
    'request.open("GET", "/data.json")',
    '@import "./theme.css";',
    ".icon { background-image: url(/assets/icon.svg); }",
    ".icon { background-image: url(data:image/svg+xml;base64,PHN2Zz4=); }",
  ];

  for (const bundledLoad of bundledLoads) {
    assert.equal(hasExecutableRemoteLoad(bundledLoad), false);
  }
});

test("requires an exact CSP directive source list", () => {
  assert.equal(
    hasExactCspDirectiveSources(
      "default-src 'self'; connect-src   'none'; object-src 'none'",
      "connect-src",
      ["'none'"],
    ),
    true,
  );

  for (const policy of [
    "default-src 'self'",
    "connect-src 'none' https://example.com",
    "connect-src 'none'; connect-src https://example.com",
  ]) {
    assert.equal(hasExactCspDirectiveSources(policy, "connect-src", ["'none'"]), false);
  }
});
