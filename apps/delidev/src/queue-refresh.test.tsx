// SPDX-License-Identifier: Apache-2.0
import { renderHook } from "@testing-library/react";
import { expect, it } from "vitest";
import { FailureCode } from "@delinoio/delidev-api-client";
import { ReadStage, type PaginationSnapshot, type PaginationRow } from "./scroll-pagination";
import { useQueueBackgroundRead } from "./queue-refresh";

function retained(): PaginationSnapshot<PaginationRow, unknown> {
  return { loaded: true, rows: [], pages: [{ token: "", rows: [], nextPageToken: "" }], payloadPages: [{ token: "", payload: [] }], nextPageToken: "" };
}
it("keeps three healthy retained queue reads quiet without hiding recovery or restoration", () => {
  const { result, rerender } = renderHook(({ identity, query }) => useQueueBackgroundRead(identity, query), { initialProps: { identity: "original", query: retained() } });
  expect(result.current).toBe(false);
  for (let cycle = 0; cycle < 3; cycle++) {
    rerender({ identity: "original", query: { ...retained(), loading: ReadStage.Refresh } });
    expect(result.current).toBe(true);
    rerender({ identity: "original", query: retained() });
    expect(result.current).toBe(false);
  }
  for (const loading of [ReadStage.Initial, ReadStage.Additional, ReadStage.Restore]) {
    rerender({ identity: "original", query: { ...retained(), loading } });
    expect(result.current).toBe(false);
  }
  rerender({ identity: "original", query: { ...retained(), payloadPages: [], loading: ReadStage.Refresh } });
  expect(result.current).toBe(false);
  rerender({ identity: "original", query: { ...retained(), error: { stage: ReadStage.Refresh, token: "", failure: { code: FailureCode.Unavailable, message: "", guidance: "" } } } });
  expect(result.current).toBe(false);
  rerender({ identity: "original", query: { ...retained(), loading: ReadStage.Reload } });
  expect(result.current).toBe(false);
  rerender({ identity: "original", query: retained() });
  rerender({ identity: "original", query: { ...retained(), loading: ReadStage.Reload } });
  expect(result.current).toBe(true);
  rerender({ identity: "replacement", query: { ...retained(), loaded: false, loading: ReadStage.Initial } });
  expect(result.current).toBe(false);
});
