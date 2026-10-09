// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { createRequire } from "node:module";
import test from "node:test";

const { launch } = createRequire(import.meta.url)("../src/launcher.cjs");

function fixture() {
  const parent = new EventEmitter();
  const child = new EventEmitter();
  const acknowledgement = new EventEmitter();
  child.stdio = [null, null, null, acknowledgement];
  child.exitCode = child.signalCode = null;
  const signals = [];
  child.kill = (signal) => signals.push(signal);
  let now = 0;
  let serial = 0;
  const timers = new Map();
  const setTimer = (callback, wait) => {
    const id = ++serial;
    timers.set(id, { callback, due: now + wait });
    return id;
  };
  const clearTimer = (id) => timers.delete(id);
  const tick = (amount) => {
    now += amount;
    for (const [id, timer] of [...timers]) {
      if (timer.due <= now) {
        timers.delete(id);
        timer.callback();
      }
    }
  };
  const result = launch("synthetic-clibox", [], {
    platform: "linux", parent, spawnChild: () => child, setTimer, clearTimer,
  });
  const interrupt = () => parent.emit("SIGINT");
  const ack = () => acknowledgement.emit("data", Buffer.from([1]));
  const finish = async () => {
    child.emit("exit", 130, null);
    assert.deepEqual(await result, { code: 130, signal: null });
    assert.equal(timers.size, 0);
    assert.deepEqual(parent.eventNames(), []);
    assert.deepEqual(acknowledgement.eventNames(), []);
  };
  return { parent, child, signals, timers, tick, interrupt, ack, finish, result };
}

test("delayed acknowledgements cannot suppress subsequent launcher interrupts", async () => {
  const f = fixture();
  f.interrupt();
  f.tick(25);
  f.ack();
  f.interrupt();
  f.tick(25);
  assert.deepEqual(f.signals, ["SIGINT", "SIGINT"]);
  await f.finish();
});

test("early acknowledgements suppress only their current grace attempt", async () => {
  const f = fixture();
  f.interrupt();
  f.ack();
  f.tick(10);
  assert.deepEqual(f.signals, []);
  f.interrupt();
  f.tick(10);
  assert.deepEqual(f.signals, ["SIGINT"]);
  await f.finish();
});

test("unsolicited acknowledgements are discarded", async () => {
  const f = fixture();
  f.ack();
  f.interrupt();
  f.tick(10);
  assert.deepEqual(f.signals, ["SIGINT"]);
  await f.finish();
});

test("overlapping interrupts disable suppression before old bytes arrive", async () => {
  const f = fixture();
  f.interrupt();
  f.tick(5);
  f.interrupt();
  f.ack();
  f.tick(5);
  f.interrupt();
  assert.deepEqual(f.signals, ["SIGINT", "SIGINT", "SIGINT"]);
  await f.finish();
});

test("falling back disables acknowledgement suppression before synchronous delivery", async () => {
  const f = fixture();
  f.child.kill = (signal) => { f.signals.push(signal); f.ack(); };
  f.interrupt();
  f.tick(10);
  f.interrupt();
  assert.deepEqual(f.signals, ["SIGINT", "SIGINT"]);
  await f.finish();
});

for (const outcome of ["exit", "error"]) {
  test(`${outcome} clears all owned timers and prevents later signals`, async () => {
    const f = fixture();
    f.interrupt();
    if (outcome === "exit") await f.finish();
    else {
      const rejection = assert.rejects(f.result, /Unable to start clibox/);
      f.child.emit("error", new Error("synthetic spawn error"));
      await rejection;
      assert.equal(f.timers.size, 0);
      assert.deepEqual(f.parent.eventNames(), []);
      assert.deepEqual(f.child.stdio[3].eventNames(), []);
    }
    f.tick(25);
    f.ack();
    assert.deepEqual(f.signals, []);
  });
}
