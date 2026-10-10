// SPDX-License-Identifier: Apache-2.0
import { renderHook } from "@testing-library/react";
import { expect, it } from "vitest";
import { FailureCode } from "@delinoio/delidev-api-client";
import { ReadStage, type PaginationFailure } from "./scroll-pagination";
import { useBackgroundQueueRead } from "./queue-read-presentation";

type Read = { loaded: boolean; loading?: ReadStage; error?: PaginationFailure };
it("keeps initial, restoration and continuation reads visible while retaining healthy rereads", () => {
  const view = renderHook(({ query }: { query: Read }) => useBackgroundQueueRead(query), { initialProps: { query: { loaded: false, loading: ReadStage.Initial } as Read } });
  expect(view.result.current).toBe(false);
  view.rerender({ query: { loaded: true } });
  for (let cycle = 0; cycle < 3; cycle++) {
    for (const loading of [ReadStage.Refresh, ReadStage.Reload]) {
      view.rerender({ query: { loaded: true, loading } });
      expect(view.result.current).toBe(true);
      view.rerender({ query: { loaded: true } });
    }
  }
  for (const loading of [ReadStage.Additional, ReadStage.Restore]) {
    view.rerender({ query: { loaded: true, loading } });
    expect(view.result.current).toBe(false);
  }
});
it("keeps failed-read recovery visible and never borrows a retained presentation for an unread scope", () => {
  const view = renderHook(({ query }: { query: Read }) => useBackgroundQueueRead(query), { initialProps: { query: { loaded: true } as Read } });
  view.rerender({ query: { loaded: true, error: { stage: ReadStage.Refresh, token: "", failure: { code: FailureCode.CursorExpired, message: "Expired", guidance: "Reload" } } } });
  expect(view.result.current).toBe(false);
  view.rerender({ query: { loaded: true, loading: ReadStage.Reload } });
  expect(view.result.current).toBe(false);
  view.rerender({ query: { loaded: false, loading: ReadStage.Initial } });
  expect(view.result.current).toBe(false);
});
