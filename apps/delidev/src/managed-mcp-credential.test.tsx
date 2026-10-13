// SPDX-License-Identifier: Apache-2.0
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  credentialCurrent,
  useCredentialClock,
} from "./managed-mcp-credential";

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2030-01-01T00:00:00Z"));
});
afterEach(() => vi.useRealTimers());

const row = (authenticated: boolean, credentialExpiresAt: string) => ({
  authenticated,
  credentialExpiresAt,
});

it("revokes an authenticated claim at the credential deadline without another read", () => {
  const rows = [
    row(true, "2030-01-01T00:00:05Z"),
    row(true, ""),
    row(false, "2030-01-01T01:00:00Z"),
  ];
  const { result } = renderHook(() => useCredentialClock(rows));
  expect(credentialCurrent(rows[0], result.current)).toBe(true);
  act(() => {
    vi.advanceTimersByTime(6000);
  });
  expect(credentialCurrent(rows[0], result.current)).toBe(false);
  // A credential without an expiry stays current and an unauthenticated one never is.
  expect(credentialCurrent(rows[1], result.current)).toBe(true);
  expect(credentialCurrent(rows[2], result.current)).toBe(false);
});

it("treats a malformed expiry as not current", () => {
  expect(credentialCurrent(row(true, "not-a-date"), Date.now())).toBe(false);
});
