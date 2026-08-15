import assert from "node:assert/strict";
import test from "node:test";

import { hasExecutableRemoteLoad } from "./frontend-output-policy.mjs";

const remoteLoads = [
  '<script src="https://example.com/app.js"></script>',
  '<link href="//example.com/app.css" rel="stylesheet">',
  'fetch("//example.com/data")',
  'import("https://example.com/module.js")',
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
    '@import "./theme.css";',
    ".icon { background-image: url(/assets/icon.svg); }",
    ".icon { background-image: url(data:image/svg+xml;base64,PHN2Zz4=); }",
  ];

  for (const bundledLoad of bundledLoads) {
    assert.equal(hasExecutableRemoteLoad(bundledLoad), false);
  }
});
