import { QueryClient } from "@tanstack/react-query";

export function connectionQueryClient() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 1000, gcTime: 60000, refetchOnWindowFocus: false }, mutations: { retry: false, gcTime: 0 } } });
  const activate = () => {
    let pruning = false;
    const unsubscribe = client.getQueryCache().subscribe((event) => {
      if (pruning || (event.type !== "observerRemoved" && !(event.type === "updated" && ["success", "error"].includes(event.action.type)))) return;
      pruning = true;
      try {
        // Ordinary query payloads own bounded pages. Home separately retains
        // connection-owned navigation projections, never full Resource documents.
        // Retain at most
        // eight additional inactive pages. Never remove a just-added query
        // before its observer attaches or its first result is committed.
        const inactive = client.getQueryCache().getAll().filter((query) => query.getObserversCount() === 0 && query.state.fetchStatus !== "fetching").sort((a, b) => a.state.dataUpdatedAt - b.state.dataUpdatedAt);
        for (const query of inactive.slice(0, Math.max(0, inactive.length - 8))) client.removeQueries({ queryKey: query.queryKey, exact: true });
      } finally { pruning = false; }
    });
    return () => { unsubscribe(); void client.cancelQueries(); client.clear(); };
  };
  return { client, activate };
}
