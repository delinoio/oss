import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  ActivityKind, ActivityQuery, EntityKind, ResourceQuery,
  SearchArchiveState, SearchQuery, SessionQuery, newRequestId,
} from "@delinoio/delidev-api-client";
import { document, items, object, resourceName, text } from "./documents";
import { Problem } from "./ui";

export enum Surface { Sessions = "sessions", NewSession = "new-session", Search = "search", Activity = "activity", Inbox = "inbox", Schedules = "schedules", Usage = "usage" }
function Pager({ page, next, setPage, busy }: { page: string; next?: string; setPage: (value: string) => void; busy: boolean }) {
  return <nav aria-label="Results pages"><button disabled={!page || busy} onClick={() => setPage("")}>First page</button><button disabled={!next || busy} onClick={() => setPage(next!)}>Next page</button></nav>;
}

export function Search({ open }: { open: (id: string) => void }) {
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState("");
  const [archive, setArchive] = useState(SearchArchiveState.UNSPECIFIED);
  const result = useQuery(SearchQuery.searchConversations, { query, archive, pageSize: 30, pageToken: page }, { enabled: query.trim().length > 0 });
  return <section className="page"><h2>Search conversations</h2><form className="search-form" onSubmit={(event) => { event.preventDefault(); setQuery(draft); setPage(""); }}>
    <label>Search text<input autoFocus value={draft} onChange={(event) => setDraft(event.target.value)} /></label><label>Archive<select value={archive} onChange={(event) => { setArchive(Number(event.target.value)); setPage(""); }}><option value={SearchArchiveState.UNSPECIFIED}>Include archived</option><option value={SearchArchiveState.ACTIVE}>Active only</option><option value={SearchArchiveState.ARCHIVED}>Archived only</option></select></label><button className="primary" disabled={!draft.trim()}>Search</button></form>
    <Problem error={result.error} />{query && result.isPending ? <p>Searching…</p> : null}
    {result.data?.hits.map((hit) => <article key={hit.message?.id} className="result"><button disabled={!hit.message?.sessionId} onClick={() => open(hit.message!.sessionId)}>{hit.sessionName}</button><p>{text(document(hit.message).text)}</p><small>{hit.message?.sessionId}</small></article>)}
    {result.data?.hits.length === 0 ? <p>No retained conversation matches.</p> : null}<Pager page={page} setPage={setPage} next={result.data?.nextPageToken} busy={result.isFetching} />
  </section>;
}

export function Activity({ open }: { open: (id: string) => void }) {
  const [page, setPage] = useState("");
  const result = useQuery(ActivityQuery.listActivity, { pageSize: 50, pageToken: page });
  return <section className="page"><header><h2>Activity</h2><button onClick={() => { setPage(""); void result.refetch(); }}>Refresh</button></header><Problem error={result.error} />
    {result.data?.entries.map((entry) => <article className="result" key={entry.id}><strong>{ActivityKind[entry.kind]?.toLowerCase().replaceAll("_", " ")}</strong><p><time dateTime={new Date(Number(entry.observedAtUnixMs)).toISOString()}>{new Date(Number(entry.observedAtUnixMs)).toLocaleString()}</time></p>{entry.sessionId ? <button onClick={() => open(entry.sessionId)}>Open session</button> : <p>Waiting or skipped occurrence</p>}{entry.accountId ? <small>Account {entry.accountId}</small> : null}</article>)}
    {result.data?.entries.length === 0 ? <p>No activity yet.</p> : null}<Pager page={page} setPage={setPage} next={result.data?.nextPageToken} busy={result.isFetching} />
  </section>;
}

export { Settings } from "./settings";
