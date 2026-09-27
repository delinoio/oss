import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { InboxQuery, isEntityId } from "@delinoio/delidev-api-client";
import { currentInboxSource } from "./inbox-source";
import { InboxRow } from "./views";
import { Problem } from "./ui";

// Notification activation selects only an opaque inbox identity. Always join
// its current source through the original connection before presenting actions.
export function InboxSelection({ id, activation = 0, open, close }: { id: string; activation?: number; open: (id: string) => void; close: () => void }) {
  const valid = isEntityId(id);
  const result = useQuery(InboxQuery.getInboxEntry, { id }, { enabled: valid, refetchInterval: valid ? 5000 : false });
  const [loadedActivation, setLoadedActivation] = useState(-1);
  useEffect(() => {
    if (!valid) return;
    let canceled = false;
    // Every native click reconciles even when this same item is already open.
    // Keep the row/draft mounted while disabling actions until that read ends.
    void result.refetch({ cancelRefetch: false }).then(() => { if (!canceled) setLoadedActivation(activation); });
    return () => { canceled = true; };
  }, [activation, valid, result.refetch]);
  const view = result.data?.view;
  const matched = Boolean(view && currentInboxSource(view, id));
  return <section className="page"><header><h2>Selected inbox item</h2><div className="actions"><button onClick={close}>Show all inbox items</button><button disabled={!valid || result.isFetching} onClick={() => void result.refetch()}>Refresh selected item</button></div></header>
    <p>Opening this item does not mark it read, answer it or resume its session.</p><Problem error={result.error} />
    {view && matched ? <fieldset disabled={loadedActivation !== activation || Boolean(result.error) || result.isFetching}><legend>Current server state</legend><InboxRow view={view} open={open} refresh={() => void result.refetch()} /></fieldset> : <p>{result.isPending && valid ? "Loading the current request…" : "This inbox item is unavailable. Its notification cannot restore a deleted or inaccessible request."}</p>}
  </section>;
}
