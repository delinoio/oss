import { expect, it } from "vitest";
import { allowLocalProxy } from "./dev-proxy";

it("rewrites only same-origin development RPC requests", () => {
  const headers = { host: "localhost:46308", origin: "http://localhost:46308", "x-ach-api-version": "1" };
  expect(allowLocalProxy({ method: "POST", headers })).toBe(true);
  for (const origin of [undefined, "null", "https://ach.delino.io", "http://localhost:46309", "http://evil.example"]) {
    expect(allowLocalProxy({ method: "POST", headers: { ...headers, origin } })).toBe(false);
  }
  expect(allowLocalProxy({ method: "POST", headers: { ...headers, host: "rebind.example:46308" } })).toBe(false);
  expect(allowLocalProxy({ method: "POST", headers: { ...headers, "x-ach-api-version": undefined } })).toBe(false);
  expect(allowLocalProxy({ method: "GET", headers })).toBe(false);
});
