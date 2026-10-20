// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";
import ts from "typescript";
import { lowerTsx } from "./experiments/custom-tsx/lower.mjs";

async function app() {
  let dispatch;
  let initial;
  let clears = 0;
  const updates = new Map();
  const source = await readFile(
    new URL("../examples/native-todo/app.tsx", import.meta.url),
    "utf8",
  );
  const lowered = lowerTsx(ts, source);
  const javascript = ts.transpileModule(lowered, {
    compilerOptions: { target: ts.ScriptTarget.ES2022 },
  }).outputText;
  vm.runInNewContext(javascript, {
    nativeRun(html, handler) {
      initial = html;
      dispatch = handler;
    },
    nativeUpdate(target, text) {
      updates.set(target, text);
    },
    nativeClearInput() {
      clears++;
    },
  });
  return {
    initial,
    updates,
    dispatch,
    get clears() {
      return clears;
    },
  };
}

test("todo adds trimmed Unicode text, escapes markup and ignores blank submissions", async () => {
  const todo = await app();
  assert.match(todo.initial, /2 remaining/);
  assert.match(todo.initial, /id="new-todo"/);
  todo.dispatch(1, "   ");
  assert.equal(todo.updates.size, 0);
  assert.equal(todo.clears, 0);
  todo.dispatch(1, '  한글 😀 <img src=x> & "quoted"  ');
  assert.match(
    todo.updates.get(1),
    /한글 😀 &lt;img src=x&gt; &amp; &quot;quoted&quot;/,
  );
  assert.doesNotMatch(todo.updates.get(1), /<img/);
  assert.equal(todo.updates.get(2), "3 remaining");
  assert.equal(todo.clears, 1);
  assert.equal(todo.updates.get(3), "What needs to be done?");
  todo.dispatch(2, "typing");
  assert.equal(todo.updates.get(3), "");
  todo.dispatch(2, "");
  assert.equal(todo.updates.get(3), "What needs to be done?");
});

test("todo handlers keep stable identities after deletion and reach the empty state", async () => {
  const todo = await app();
  todo.dispatch(100, "");
  assert.equal(todo.updates.get(2), "1 remaining");
  todo.dispatch(100, "");
  assert.equal(todo.updates.get(2), "2 remaining");
  todo.dispatch(101, "");
  assert.equal(todo.updates.get(2), "1 remaining");
  todo.dispatch(102, "");
  assert.equal(todo.updates.get(2), "0 remaining");
  todo.dispatch(103, "");
  todo.dispatch(105, "");
  assert.match(todo.updates.get(1), /No tasks yet/);
  todo.dispatch(1, "New task");
  assert.match(todo.updates.get(1), /data-action="106"/);
  todo.dispatch(106, "");
  assert.equal(todo.updates.get(2), "0 remaining");
  todo.dispatch(107, "");
  assert.match(todo.updates.get(1), /No tasks yet/);
});
