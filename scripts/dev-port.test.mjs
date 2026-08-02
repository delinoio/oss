import assert from "node:assert/strict";
import net from "node:net";
import { after, before, test } from "node:test";

import { isDevPortAvailable, parseDevPort } from "./dev-port.mjs";

const listener = net.createServer();
let occupiedPort;

before(async () => {
  await new Promise((resolve, reject) => {
    listener.once("error", reject);
    listener.listen({ port: 0, host: "127.0.0.1" }, resolve);
  });
  occupiedPort = listener.address().port;
});

after(async () => {
  await new Promise((resolve, reject) => {
    listener.close((error) => (error ? reject(error) : resolve()));
  });
});

test("parseDevPort accepts the complete TCP port range", () => {
  assert.equal(parseDevPort("1", "TEST_PORT"), 1);
  assert.equal(parseDevPort("65535", "TEST_PORT"), 65_535);
});

test("parseDevPort rejects invalid values", () => {
  for (const value of ["", "0", "1.5", "65536", "not-a-port"]) {
    assert.throws(() => parseDevPort(value, "TEST_PORT"), /TEST_PORT must be an integer/);
  }
});

test("isDevPortAvailable detects an IPv4 loopback listener", async () => {
  assert.equal(await isDevPortAvailable(occupiedPort), false);
});
