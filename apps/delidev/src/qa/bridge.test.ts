// SPDX-License-Identifier: Apache-2.0
import { beforeEach, expect, test, vi } from "vitest";
import { AppearanceProblem, Theme } from "../appearance";
import { appearanceBridge, hostRequest } from "./bridge";

beforeEach(() => window.localStorage.clear());
test("appearance uses only its environment and preserves revision conflicts", async () => {
  const a = appearanceBridge("server-a"), b = appearanceBridge("server-b");
  await a.update(Theme.Dark, 0); expect(await b.read()).toEqual({ theme: Theme.System, revision: 0, problem: null });
  expect(await a.update(Theme.Light, 0)).toEqual({ theme: Theme.Dark, revision: 1, problem: AppearanceProblem.Changed });
  expect(await appearanceBridge("server-a").read()).toEqual({ theme: Theme.Dark, revision: 1, problem: null });
  expect(Object.keys(window.localStorage)).toEqual(["delidev-qa-appearance:server-a"]);
});
test("host calls carry no cookies and prohibit caching and redirects", async () => {
  const fetcher = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ machineId: "fixture" }) });
  vi.stubGlobal("fetch", fetcher);
  try {
    expect(await hostRequest("proof", {})).toEqual({ machineId: "fixture" });
    expect(fetcher).toHaveBeenCalledWith("/__qa/proof", expect.objectContaining({ credentials: "omit", cache: "no-store", redirect: "error", body: "{}" }));
    expect(window.localStorage.length).toBe(0);
  } finally { vi.unstubAllGlobals(); }
});
