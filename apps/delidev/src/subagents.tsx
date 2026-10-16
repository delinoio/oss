import { useProductLabels, PresentationKind } from "./product-identity-labels";
import { createPortal } from "react-dom";
import {
  useSessionActive,
  useSessionQuery as useQuery,
} from "./session-activity";

import {
  paginationIdentity,
  paginationRevision,
  ReadStage,
} from "./scroll-pagination";
import { useConversationPages } from "./conversation-pagination";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { LocalizedText, copy, useLocale } from "./localization";
import { useCallback, useEffect, useId, useMemo, useRef } from "react";

import {
  EntityKind,
  SystemCapability,
  SystemQuery,
  type Resource,
} from "@delinoio/delidev-api-client";
import { items, object, text } from "./documents";
import { Failure, Problem } from "./ui";
import {
  SubagentHarness,
  subagentRowDisplayName,
  subagentSafeRow,
  validateSubagentPage,
  type SubagentRow,
} from "./subagent-record";
import {
  HoverSide,
  SessionHoverCard,
  SessionHoverProvider,
  useSessionHover,
} from "./session-hover-card";

const unavailable = () => (
  <p>{copy("subagents.theRetainedChildPageIsUnavailable_e2dd4a")}</p>
);
export function SubagentRows({
  rows,
  sessionId,
  labels,
  names,
  enabled = true,
}: {
  rows: readonly Resource[];
  sessionId: string;
  labels?: Map<string, number>;
  names?: Map<string, string>;
  enabled?: boolean;
}) {
  return (
    <SessionHoverProvider enabled={enabled} scope={sessionId}>
      <SubagentList
        rows={rows}
        sessionId={sessionId}
        labels={labels}
        names={names}
      />
    </SessionHoverProvider>
  );
}

// Validates one resident payload page before presenting any of its children.
// It must render beneath a hover provider that the section owns.
function SubagentList({
  rows,
  sessionId,
  labels,
  names,
}: {
  rows: readonly Resource[];
  sessionId: string;
  labels?: Map<string, number>;
  names?: Map<string, string>;
}) {
  useLocale();
  const accepted = useMemo(
    () => validateSubagentPage(rows, sessionId),
    [rows, sessionId],
  );
  return accepted ? (
    <ChildList rows={accepted} labels={labels} names={names} />
  ) : (
    unavailable()
  );
}

// Diagnostics owns only original source references. Conversation output and
// ordinary status/model/usage presentation remain in the mounted Info owner.
export function SubagentSourceRows({
  rows,
  sessionId,
}: {
  rows: readonly Resource[];
  sessionId: string;
}) {
  const productLabel = useProductLabels();
  useLocale();
  const accepted = useMemo(
    () => validateSubagentPage(rows, sessionId),
    [rows, sessionId],
  );
  return accepted ? (
    <table aria-label={copy("session-name.nativeEvidence")}>
      <thead>
        <tr>
          <th>{copy("subagents.child_805332")}</th>
          <th>{copy("subagents.parent_5f7953")}</th>
          <th>{copy("subagents.sourceCoverage_0b8649")}</th>
        </tr>
      </thead>
      <tbody>
        {accepted.map(({ resource, record, child }) => (
          <tr key={resource.id}>
            <td>
              <code>{text(child.native_id)}</code>
            </td>
            <td>
              <code>{text(child.parent_id)}</code>
            </td>
            <td>
              <p>
                {text(record.harness)} {text(record.native_version)}
              </p>
              <p>
                <LocalizedText
                  id="subagents.execution_2ce365"
                  components={{
                    s0: (
                      <>
                        {productLabel(
                          PresentationKind.Execution,
                          text(record.execution_id),
                        )}
                      </>
                    ),
                  }}
                />
              </p>
              <p>
                <LocalizedText
                  id="subagents.parentTool_5586e7"
                  components={{
                    s0: (
                      <>
                        {text(child.parent_tool_id) ||
                          copy("subagents.extra.ca1844969742")}
                      </>
                    ),
                  }}
                />
              </p>
              {text(object(child.output).native_message_id) ? (
                <code>{text(object(child.output).native_message_id)}</code>
              ) : null}
              <ul>
                {items(record.sources)
                  .map(object)
                  .map((source, index) => (
                    <li key={index}>
                      <LocalizedText
                        id="subagents.sequence_440e77"
                        components={{
                          s0: <>{text(source.source)}</>,
                          s1: <>{text(source.source_id)}</>,
                          s2: (
                            <>
                              {String(
                                source.sequence ??
                                  copy("subagents.extra.ca1844969742"),
                              )}
                            </>
                          ),
                          s3: (
                            <>
                              {text(object(source.usage).native_report) ? (
                                <pre>
                                  {text(object(source.usage).native_report)}
                                </pre>
                              ) : null}
                            </>
                          ),
                        }}
                      />
                    </li>
                  ))}
              </ul>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  ) : (
    unavailable()
  );
}

// Ordinary Info shows only a plain list of agent names. Every detail appears in
// a noninteractive hover/focus card built from the resident validated payload,
// so opening it sends no request and cannot mutate or control a child.
function ChildList({
  rows,
  labels,
  names,
}: {
  rows: readonly SubagentRow[];
  labels?: Map<string, number>;
  names?: Map<string, string>;
}) {
  useLocale();
  // Native names are optional display metadata. The stable ordinal remains the
  // fallback and the only label for Claude and OpenCode children.
  const fallback = (key: string) =>
    copy("session-name.subagent", { number: labels?.get(key) ?? "—" });
  const label = (execution: unknown, native: unknown) => {
    const key = `${execution}:${native}`;
    return names?.get(key) ?? fallback(key);
  };
  return (
    <ul
      className="subagent-name-list"
      aria-label={copy("subagents.agentNames")}
    >
      {rows.map((row) => {
        const { record, child } = row;
        const key = `${record.execution_id}:${child.native_id}`;
        return (
          <ChildName
            key={row.resource.id}
            row={row}
            name={subagentRowDisplayName(row) ?? fallback(key)}
            parent={
              child.parent_id === record.root_id
                ? copy("subagents.rootSession_6cdfaa")
                : labels?.has(`${record.execution_id}:${child.parent_id}`)
                  ? label(record.execution_id, child.parent_id)
                  : copy("subagents.unavailable_ca1844")
            }
          />
        );
      })}
    </ul>
  );
}

function ChildName({
  row,
  name,
  parent,
}: {
  row: SubagentRow;
  name: string;
  parent: string;
}) {
  const anchor = useRef<HTMLSpanElement>(null);
  const hover = useSessionHover(anchor);
  const cardId = useId();
  return (
    <li className="subagent-name-item">
      <span
        ref={anchor}
        className="subagent-name"
        tabIndex={0}
        aria-describedby={hover.visible ? cardId : undefined}
        onPointerEnter={hover.onPointerEnter}
        onPointerLeave={hover.onPointerLeave}
        onFocus={hover.onFocus}
        onBlur={hover.onBlur}
      >
        {name}
      </span>
      {hover.visible ? (
        <SessionHoverCard hover={hover} id={cardId} side={HoverSide.Left}>
          <ChildDetails row={row} name={name} parent={parent} />
        </SessionHoverCard>
      ) : null}
    </li>
  );
}

function ChildDetails({
  row: { child },
  name,
  parent,
}: {
  row: SubagentRow;
  name: string;
  parent: string;
}) {
  useLocale();
  const output = object(child.output),
    usage = object(child.usage);
  const blocks = items(output.blocks).map(object);
  const missing = () => copy("subagents.extra.ca1844969742");
  return (
    <>
      <p className="sidebar-session-card-title">{name}</p>
      <dl className="subagent-card-details">
        <dt>{copy("subagents.parent_5f7953")}</dt>
        <dd>{parent}</dd>
        <dt>{copy("subagents.status_920e41")}</dt>
        <dd>{text(child.status) || missing()}</dd>
        <dt>{copy("subagents.model_5e2c61")}</dt>
        <dd>
          <p>
            <LocalizedText
              id="subagents.observed_ae32e4"
              components={{
                s0: <>{text(child.observed_model) || missing()}</>,
              }}
            />
          </p>
          <p>
            <LocalizedText
              id="subagents.requested_a7d830"
              components={{
                s0: <>{text(child.requested_model) || missing()}</>,
              }}
            />
          </p>
        </dd>
        <dt>{copy("subagents.recentOutput_4bafa7")}</dt>
        <dd>
          {child.output == null ? (
            copy("subagents.unavailable_ca1844")
          ) : (
            <>
              <p>
                {output.partial === true
                  ? copy("subagents.partialNativeOutput_731a58")
                  : copy("subagents.observedNativeOutput_37d11c")}
              </p>
              {text(output.text) ? <pre>{text(output.text)}</pre> : null}
              {blocks.map((block, index) => (
                <div key={index}>
                  <small>{text(block.kind)}</small>
                  {block.text == null ? (
                    <p>{copy("subagents.contentUnavailable_e9f250")}</p>
                  ) : (
                    <pre>{text(block.text)}</pre>
                  )}
                </div>
              ))}
            </>
          )}
        </dd>
        <dt>{copy("subagents.usageObservation_9c75b9")}</dt>
        <dd>
          {child.usage == null ? (
            copy("subagents.unavailable_ca1844")
          ) : (
            <>
              <p>{text(usage.scope)}</p>
              <p>
                <LocalizedText
                  id="subagents.total_dc9841"
                  components={{ s0: <>{text(usage.total) || missing()}</> }}
                />
              </p>
              <p>
                <LocalizedText
                  id="subagents.input_f51d4a"
                  components={{ s0: <>{text(usage.input) || missing()}</> }}
                />
              </p>
              <p>
                <LocalizedText
                  id="subagents.output_296ead"
                  components={{ s0: <>{text(usage.output) || missing()}</> }}
                />
              </p>
              <p>
                {copy(
                  "subagents.observationOnlyExcludedFromAdditiveBilling_23cc58",
                )}
              </p>
            </>
          )}
        </dd>
      </dl>
    </>
  );
}

// Retain only identity/ordinal metadata for reached validated children, just as
// the original pagination owner retains its reached identity projections. An
// arbitrary friendly-label cap must not erase valid later child relationships.
export function retainSubagentLabels(
  labels: Map<string, number>,
  rows: readonly SubagentRow[],
  names?: Map<string, string>,
) {
  for (const row of rows) {
    const key = `${row.record.execution_id}:${row.child.native_id}`;
    if (!labels.has(key)) labels.set(key, labels.size + 1);
    // Names only decorate the stable ordinal so a parent shown in a card keeps
    // the same label as its list row after its own page leaves the window. A
    // row observed now is authoritative: missing, foreign, blank, oversized,
    // control-bearing or non-Codex metadata clears a stale name instead of
    // leaving an older one. Entries of rows that left the window stay retained.
    const name = subagentRowDisplayName(row);
    if (name !== undefined) names?.set(key, name);
    else names?.delete(key);
  }
}

export function Subagents({
  sessionId,
  revision,
  visible = true,
  emptyChanged,
  diagnosticsTarget,
}: {
  sessionId: string;
  revision: string;
  visible?: boolean;
  emptyChanged?: (value: boolean) => void;
  diagnosticsTarget?: HTMLElement | null;
}) {
  useLocale();
  const active = useSessionActive();
  const status = useQuery(SystemQuery.getStatus, {});
  const supported =
    status.data?.capabilities.includes(
      SystemCapability.SUBAGENT_OBSERVATION_V1,
    ) === true;
  const openCodeSupported =
    status.data?.capabilities.includes(
      SystemCapability.OPENCODE_FOREGROUND_SUBAGENTS_V1,
    ) === true;
  const root = useRef<HTMLDivElement>(null);
  const validate = useCallback(
    (resources: Resource[]) => {
      if (!validateSubagentPage(resources, sessionId))
        throw new Error("The retained child page is unavailable.");
    },
    [sessionId],
  );
  const safeRow = useCallback(
    (resource: Resource) => subagentSafeRow(resource, sessionId),
    [sessionId],
  );
  const query = useConversationPages(
    EntityKind.SUBAGENT,
    sessionId,
    supported,
    50,
    validate,
    undefined,
    undefined,
    safeRow,
  );
  // Native events invalidate only this read. No observation can issue a child
  // input, resume, interruption, retry, or mutation.
  useEffect(() => {
    if (active && supported) void query.refresh();
  }, [revision, supported, query.refresh, active]);
  const rows = useMemo(() => {
    if (!query.loaded) return undefined;
    // The accepted payload window can contain several independently bounded
    // pages. Keep each page's validation bound before composing its rows.
    const pages = query.payloadPages.map((page) =>
      validateSubagentPage(page.payload, sessionId),
    );
    return pages.every((page): page is SubagentRow[] => page !== undefined)
      ? pages.flat()
      : undefined;
  }, [query.loaded, query.payloadPages, sessionId]);
  const labels = useRef(new Map<string, number>());
  const names = useRef(new Map<string, string>());
  retainSubagentLabels(labels.current, rows ?? [], names.current);
  // The section is hidden only for a confirmed complete empty result: a
  // supported owner holds an accepted, current, single-page empty snapshot. A
  // healthy background Refresh over that snapshot keeps it hidden, so every
  // session revision does not flash the section. Initial, Reload, Retry and
  // Additional reads, any error, the failed-read latch (`unrecovered`, which
  // outlives `error` while Retry/Reload is pending), a continuation token and a
  // multi-page chain (an empty terminal page reached through Additional leaves
  // no token or rows but is not a single-page snapshot) never count as
  // confirmed empty.
  const confirmedEmpty =
    supported &&
    query.loaded &&
    !query.error &&
    !query.unrecovered &&
    !query.nextPageToken &&
    query.pages.length === 1 &&
    query.rows.length === 0 &&
    (!query.loading || query.loading === ReadStage.Refresh);
  useEffect(() => {
    console.debug("delidev.subagents.empty_presentation", {
      confirmedEmpty,
      loading: query.loading ?? null,
      unrecovered: Boolean(query.unrecovered),
    });
    emptyChanged?.(confirmedEmpty);
  }, [confirmedEmpty, query.loading, query.unrecovered, emptyChanged]);
  // Presentation belongs to the current session owner. Disposing or replacing
  // that owner makes the section visible again so earlier emptiness never hides
  // the next owner's initial state; a stale response cannot publish because the
  // old chain is reset and the new chain starts unloaded.
  const emptyChangedRef = useRef(emptyChanged);
  emptyChangedRef.current = emptyChanged;
  useEffect(
    () => () => {
      console.debug("delidev.subagents.empty_presentation_reset", {
        sessionId,
      });
      emptyChangedRef.current?.(false);
    },
    [sessionId],
  );
  // Derive from every accepted reached row so the warning survives eviction of
  // the payload page holding the only OpenCode child.
  const needsOpenCodeUpdate =
    query.loaded &&
    query.rows.some(
      (row) => "harness" in row && row.harness === SubagentHarness.OpenCode,
    ) &&
    !openCodeSupported;
  return (
    <div ref={root} className="conversation-page-scroll">
      {status.isPending ? (
        <p role="status">{copy("subagents.checkingCapability")}</p>
      ) : null}
      {status.error ? <Problem error={status.error} /> : null}
      {status.data && !supported ? (
        <p>{copy("subagents.thisServerDoesNotSupportChild_0baf88")}</p>
      ) : null}
      {query.error ? <Failure failure={query.error?.failure} /> : null}
      {query.isPending && supported ? (
        <p>{copy("subagents.loadingChildObservations_0a8d53")}</p>
      ) : null}
      {supported && ((query.error && !query.loaded) || (query.data && !rows))
        ? unavailable()
        : null}
      {supported && rows?.length === 0 ? (
        <p>{copy("subagents.noNativeChildObservationsAreAvailable_8c2be5")}</p>
      ) : null}
      {needsOpenCodeUpdate ? (
        <p>{copy("subagents.updateTheServerAndRunnerDevice_ac66cd")}</p>
      ) : supported && rows && rows.length > 0 ? (
        <SessionHoverProvider enabled={active && visible} scope={sessionId}>
          <ScrollPayloadWindow
            identity={paginationIdentity}
            revision={paginationRevision}
            query={query}
            root={root}
            active={active && visible}
          >
            {(payload) => (
              <>
                <SubagentList
                  rows={payload}
                  sessionId={sessionId}
                  labels={labels.current}
                  names={names.current}
                />
                {diagnosticsTarget
                  ? createPortal(
                      <section>
                        <h3>{copy("session-name.nativeEvidence")}</h3>
                        <SubagentSourceRows
                          rows={payload}
                          sessionId={sessionId}
                        />
                      </section>,
                      diagnosticsTarget,
                    )
                  : null}
              </>
            )}
          </ScrollPayloadWindow>
        </SessionHoverProvider>
      ) : null}
      {query.error ? (
        <button
          disabled={!supported || query.isFetching}
          onClick={() => void query.refetch()}
        >
          {copy("session-name.retryRead")}
        </button>
      ) : null}
      <ScrollContinuation
        query={query}
        root={root}
        active={active && visible && supported && !needsOpenCodeUpdate}
        label={copy("subagents.subagents_88296a")}
      />
    </div>
  );
}
