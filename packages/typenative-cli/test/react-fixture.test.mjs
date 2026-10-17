import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import test from "node:test";
import * as React from "react";

// This is a fixture-oracle unit test, not native React acceptance.
const source = readFileSync(
  new URL("fixtures/react-core.ts", import.meta.url),
  "utf8",
).replace(/^import .* from "react";\n/, "");

test("React fixture rejects cloning that ignores the requested props", () => {
  const context = { ...React, console: { log() {} } };
  assert.doesNotThrow(() => runInNewContext(source, context));
  assert.throws(
    () =>
      runInNewContext(source, {
        ...context,
        cloneElement: (element) => element,
      }),
    /React core probe failed/,
  );
});

test("React fixture rejects clones that mutate the source element", () => {
  for (const returnOriginal of [true, false]) {
    assert.throws(
      () =>
        runInNewContext(source, {
          ...React,
          console: { log() {} },
          createElement: (...args) => {
            const element = React.createElement(...args);
            return { ...element, props: { ...element.props } };
          },
          cloneElement: (element, overrides) => {
            Object.assign(element.props, overrides);
            return returnOriginal
              ? element
              : { ...element, props: { ...element.props } };
          },
        }),
      /React core probe failed/,
    );
  }
});
