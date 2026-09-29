import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { SystemQuery, newRequestId } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export function Backups({ active }: { active: boolean }) {
  const [page, setPage] = useState("");
  const [selected, setSelected] = useState("");
  const [created, setCreated] = useState("");
  const inventory = useQuery(SystemQuery.listBackups, { pageSize: 20, pageToken: page }, { enabled: active, retry: false });
  const inspection = useQuery(SystemQuery.inspectBackup, { id: selected }, { enabled: active && Boolean(selected), retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const refresh = () => { if (page) setPage(""); else void inventory.refetch(); };
  const create = useRetainedMutation("backup-create", SystemQuery.createBackup, (result) => { setCreated(result.id); refresh(); });
  const checked = inspection.data?.backup?.id === selected && !inspection.error && !inspection.isFetching ? inspection.data : undefined;
  return <section aria-label="Managed database backups">
    <h2>Database backups</h2>
    <p>Backups contain private server data and committed database changes. Credentials, browser profiles, and Worker files are separate.</p>
    <div className="actions"><button disabled={!active || create.busy || create.uncertain} onClick={() => { setCreated(""); void create.send({ requestId: newRequestId() }); }}>Create database backup</button><button disabled={!active || inventory.isFetching} onClick={refresh}>Refresh backups</button></div>
    <Problem error={create.error} />
    {create.uncertain ? <button disabled={!active || create.busy} onClick={create.retry}>Retry the same backup creation</button> : null}
    {created ? <p role="status">Backup created: {created}</p> : null}
    <Problem error={inventory.error} />
    {inventory.error && inventory.data ? <p>The previous backup list is shown. Refresh before relying on it.</p> : null}
    {inventory.isPending ? <p>Reading managed backups…</p> : inventory.data?.backups.length === 0 ? <p>No managed backups.</p> : null}
    {inventory.data?.backups.map((item) => <article className="result" key={item.id}><h3>{item.id}</h3><p>{item.sizeBytes.toString()} bytes · {item.modifiedAt}</p><p>Integrity not established by this listing.</p><button disabled={!active || Boolean(inventory.error) || inventory.isFetching || inspection.isFetching} onClick={() => { if (selected === item.id) void inspection.refetch(); else setSelected(item.id); }}>Inspect backup {item.id}</button></article>)}
    <nav aria-label="Backup pages"><button disabled={!active || !page || inventory.isFetching} onClick={() => setPage("")}>First backup page</button><button disabled={!active || !inventory.data?.nextPageToken || inventory.isFetching || Boolean(inventory.error)} onClick={() => setPage(inventory.data!.nextPageToken)}>Next backup page</button></nav>
    {selected ? <section aria-label="Backup integrity inspection"><h3>Inspection: {selected}</h3><Problem error={inspection.error} />{inspection.isFetching ? <p role="status">Checking the selected backup…</p> : null}{checked ? <><p role="status">Database integrity and original server identity verified.</p><p>Schema {checked.schemaVersion} · {checked.backup!.sizeBytes.toString()} bytes</p><p>SHA-256: <code>{checked.sha256}</code></p><p>This observation does not restore data or prove that Worker files and credentials are recoverable.</p></> : null}<button disabled={!active || inspection.isFetching} onClick={() => void inspection.refetch()}>Recheck selected backup</button><button onClick={() => setSelected("")}>Close backup inspection</button></section> : null}
  </section>;
}
