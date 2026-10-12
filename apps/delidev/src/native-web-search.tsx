// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { object } from "./documents";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy, useLocale, type MessageKey } from "./localization";
import "./native-web-search.css";

export enum NativeWebAction {
  Search = "search",
  Open = "openPage",
  Find = "findInPage",
  Other = "other",
}
const labels: Record<NativeWebAction, MessageKey> = {
  [NativeWebAction.Search]: "session.webSearch",
  [NativeWebAction.Open]: "session.openPage",
  [NativeWebAction.Find]: "session.findText",
  [NativeWebAction.Other]: "session.webSearch",
};
const bounded = (v: unknown, limit: number): v is string =>
  typeof v === "string" && new TextEncoder().encode(v).length <= limit;
function observation(snapshot: unknown, status: string) {
  const s = object(snapshot),
    w = object(s.web_search),
    a = object(w.action);
  if (
    s.kind !== "web-search" ||
    s.status !== status ||
    Object.keys(s).some(
      (key) => !["kind", "status", "web_search", "changes"].includes(key),
    ) ||
    s.changes != null ||
    Object.keys(w).some(
      (key) => !["query", "action", "results", "mode", "source"].includes(key),
    ) ||
    !bounded(w.query, 16384) ||
    !bounded(w.source, 256) ||
    !w.source ||
    typeof w.mode !== "string" ||
    !["cached", "live"].includes(w.mode) ||
    !Object.hasOwn(w, "action") ||
    !Object.hasOwn(w, "results")
  )
    return;
  if (w.action !== null) {
    if (!Object.values(NativeWebAction).includes(a.type as NativeWebAction))
      return;
    const fields =
      a.type === NativeWebAction.Search
        ? ["query", "queries"]
        : a.type === NativeWebAction.Open
          ? ["url"]
          : a.type === NativeWebAction.Find
            ? ["url", "pattern"]
            : [];
    if (Object.keys(a).some((key) => key !== "type" && !fields.includes(key)))
      return;
    for (const key of ["query", "url", "pattern"])
      if (Object.hasOwn(a, key) && !bounded(a[key], 16384)) return;
    if (
      Object.hasOwn(a, "queries") &&
      (!Array.isArray(a.queries) ||
        a.queries.length > 128 ||
        a.queries.some((query) => !bounded(query, 16384)))
    )
      return;
  }
  if (w.results !== null) {
    if (!bounded(w.results, 393216)) return;
    try {
      const results: unknown = JSON.parse(w.results);
      if (!Array.isArray(results) || results.length > 1024) return;
    } catch {
      return;
    }
  }
  return {
    query: w.query,
    source: w.source,
    mode: w.mode,
    action: w.action === null ? null : a,
    results: w.results as string | null,
  };
}
export function nativeWebObservation(tool: unknown) {
  const t = object(tool),
    first = observation(t.started, "running");
  if (!first) return;
  const last =
    t.completed == null ? undefined : observation(t.completed, "completed");
  if (
    t.completed != null &&
    (!last || last.mode !== first.mode || last.source !== first.source)
  )
    return;
  return { first, last };
}
export function nativeWebLabel(tool: unknown): string {
  const observed = nativeWebObservation(tool),
    action = (observed?.last ?? observed?.first)?.action;
  return copy(
    labels[(action?.type as NativeWebAction) ?? NativeWebAction.Search],
  );
}
/** Render original native evidence as inert text. No request or citation is created. */
export function NativeWebSearch({ tool }: { tool: unknown }) {
  useLocale();
  const [open, setOpen] = useState(false),
    observed = nativeWebObservation(tool);
  if (!observed) return <p role="status">{copy("session.webUnavailable")}</p>;
  const value = observed.last ?? observed.first;
  return (
    <Disclosure
      className="native-web-search"
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <DisclosureSummary>{nativeWebLabel(tool)}</DisclosureSummary>
      {!observed.last ? (
        <p role="status">{copy("session.webProgress")}</p>
      ) : null}
      <dl>
        <dt>{copy("session.webOriginalQuery")}</dt>
        <dd>
          <pre>{value.query}</pre>
        </dd>
        <dt>{copy("session.webSource")}</dt>
        <dd>
          <pre>{value.source}</pre>
        </dd>
        {value.action !== null ? (
          <>
            <dt>{copy("session.webOriginalAction")}</dt>
            <dd>
              <pre>{JSON.stringify(value.action)}</pre>
            </dd>
          </>
        ) : null}
        <dt>{copy("session.webNativeResult")}</dt>
        <dd>
          {value.results === null ? (
            <p>{copy("session.webResultUnavailable")}</p>
          ) : (
            <pre>{value.results}</pre>
          )}
        </dd>
      </dl>
    </Disclosure>
  );
}
