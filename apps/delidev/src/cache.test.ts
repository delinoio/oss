import { QueryObserver } from "@tanstack/react-query";
import { expect, it } from "vitest";
import { connectionQueryClient } from "./cache";

it("bounds inactive history while preserving the observed page and clearing the connection on disposal", () => {
  const cache = connectionQueryClient();
  const dispose = cache.activate();
  cache.client.setQueryData(["active"], "visible");
  const observer = new QueryObserver(cache.client, { queryKey: ["active"], enabled: false });
  const unsubscribe = observer.subscribe(() => {});
  for (let index = 0; index < 100; index++) cache.client.setQueryData(["page", index], "retained");
  expect(cache.client.getQueryCache().getAll()).toHaveLength(9);
  expect(cache.client.getQueryData(["active"])).toBe("visible");
  unsubscribe();
  expect(cache.client.getQueryCache().getAll()).toHaveLength(8);
  dispose();
  expect(cache.client.getQueryCache().getAll()).toHaveLength(0);
});

it("retains a newly built pending page while its observer replaces the previous page", () => {
  const cache = connectionQueryClient();
  const dispose = cache.activate();
  cache.client.setQueryData(["active"], "visible");
  const observer = new QueryObserver(cache.client, { queryKey: ["active"], enabled: false });
  const unsubscribe = observer.subscribe(() => {});
  for (let index = 0; index < 8; index++) cache.client.setQueryData(["page", index], "retained");
  const pending = cache.client.getQueryCache().build(cache.client, { queryKey: ["next"] });
  // QueryObserver builds its replacement before removing the previous observer.
  // Pruning that pending instance during removal would restart its read forever.
  observer.setOptions({ queryKey: ["next"], enabled: false });
  expect(cache.client.getQueryCache().find({ queryKey: ["next"], exact: true })).toBe(pending);
  cache.client.setQueryData(["next"], "loaded");
  unsubscribe();
  expect(cache.client.getQueryCache().getAll()).toHaveLength(8);
  dispose();
});
