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
